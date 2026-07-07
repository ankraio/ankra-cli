package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"ankra/internal/client"

	"gopkg.in/yaml.v3"
)

// ImportClusterDoc is the parsed shape of the IaC YAML returned by
// GET /api/v1/org/clusters/imported/{cluster_id}/iac. It mirrors the
// `ImportCluster` resource definition the user passes to `ankra cluster apply`.
type ImportClusterDoc struct {
	APIVersion string `yaml:"apiVersion,omitempty"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description,omitempty"`
	} `yaml:"metadata"`
	Spec struct {
		Stacks []client.StackSpec `yaml:"stacks"`
	} `yaml:"spec"`
}

// parseImportClusterYAML parses the raw IaC YAML returned by the backend into
// a typed document. Returns a descriptive error on malformed YAML or wrong
// kind.
func parseImportClusterYAML(data []byte) (*ImportClusterDoc, error) {
	var doc ImportClusterDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse IaC YAML: %w", err)
	}
	if doc.Kind != "" && doc.Kind != "ImportCluster" {
		return nil, fmt.Errorf("expected kind=ImportCluster in IaC, got %q", doc.Kind)
	}
	return &doc, nil
}

// findAddonInIaC locates the named addon in the parsed IaC document.
//
//   - stackHint == "":       return the unique stack containing `name`, or
//     an "ambiguous" error listing all matches.
//   - stackHint != "":       only consider that stack; return wrong-stack
//     error when the addon lives elsewhere.
//
// The returned StackSpec and AddonSpec are pointers into the doc; callers
// MUST not mutate them in place (use copyStackMetadata + applyAddonMutations).
func findAddonInIaC(doc *ImportClusterDoc, name, stackHint string) (*client.StackSpec, *client.AddonSpec, error) {
	var matches []match
	for i := range doc.Spec.Stacks {
		stack := &doc.Spec.Stacks[i]
		for j := range stack.Addons {
			if stack.Addons[j].Name == name {
				matches = append(matches, match{stack: stack, addonIdx: j})
			}
		}
	}
	if len(matches) == 0 {
		return nil, nil, fmt.Errorf("addon %q not found on cluster", name)
	}
	if stackHint != "" {
		for _, m := range matches {
			if m.stack.Name == stackHint {
				return m.stack, &m.stack.Addons[m.addonIdx], nil
			}
		}
		return nil, nil, fmt.Errorf("addon %q is not part of stack %q (it is in: %s)",
			name, stackHint, stackNamesOf(matches))
	}
	if len(matches) > 1 {
		return nil, nil, fmt.Errorf("addon %q exists in multiple stacks (%s); pass --stack to disambiguate",
			name, stackNamesOf(matches))
	}
	m := matches[0]
	return m.stack, &m.stack.Addons[m.addonIdx], nil
}

// findManifestInIaC locates the named manifest in the parsed IaC document.
//
// Manifest names are unique across a cluster (a manifest can only belong to
// one stack), so this function returns the single match. If the IaC somehow
// reports the same name in multiple stacks (a data-integrity bug) we surface
// it as an error so callers don't silently patch the wrong stack.
func findManifestInIaC(doc *ImportClusterDoc, name string) (*client.StackSpec, *client.ManifestSpec, error) {
	var matches []manifestMatch
	for i := range doc.Spec.Stacks {
		stack := &doc.Spec.Stacks[i]
		for j := range stack.Manifests {
			if stack.Manifests[j].Name == name {
				matches = append(matches, manifestMatch{stack: stack, manifestIdx: j})
			}
		}
	}
	if len(matches) == 0 {
		return nil, nil, fmt.Errorf("manifest %q not found on cluster", name)
	}
	if len(matches) > 1 {
		return nil, nil, fmt.Errorf("manifest %q found in multiple stacks (%s); this is a data-integrity bug, please report it",
			name, manifestStackNamesOf(matches))
	}
	m := matches[0]
	return m.stack, &m.stack.Manifests[m.manifestIdx], nil
}

type match struct {
	stack    *client.StackSpec
	addonIdx int
}

type manifestMatch struct {
	stack       *client.StackSpec
	manifestIdx int
}

func stackNamesOf(ms []match) string {
	seen := map[string]struct{}{}
	for _, m := range ms {
		seen[m.stack.Name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func manifestStackNamesOf(ms []manifestMatch) string {
	seen := map[string]struct{}{}
	for _, m := range ms {
		seen[m.stack.Name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// copyStackMetadata returns a new StackSpec carrying only the stack-level
// metadata (name, description, description_from_file, variables,
// deploy_wave). The caller must append exactly one mutated addon or manifest
// before sending the patch. This guarantees the backend's
// upsert_external(stack) won't wipe description, variables or the deploy
// wave when the patch only changes one nested resource.
func copyStackMetadata(src *client.StackSpec) client.StackSpec {
	var vars map[string]string
	if len(src.Variables) > 0 {
		vars = make(map[string]string, len(src.Variables))
		for k, v := range src.Variables {
			vars[k] = v
		}
	}
	var deployWave *int
	if src.DeployWave != nil {
		waveCopy := *src.DeployWave
		deployWave = &waveCopy
	}
	return client.StackSpec{
		Name:                src.Name,
		Description:         src.Description,
		DescriptionFromFile: src.DescriptionFromFile,
		Variables:           vars,
		Manifests:           []client.ManifestSpec{},
		Addons:              []client.AddonSpec{},
		DeployWave:          deployWave,
	}
}

// buildPartialStackPatch wraps a single stack into a PatchStackRequest with
// partial_stack=true, the shape expected by PATCH /stacks/{stack_name}.
func buildPartialStackPatch(stack client.StackSpec) client.PatchStackRequest {
	return client.PatchStackRequest{
		Spec:         client.ResourceSpecSpec{Stacks: []client.StackSpec{stack}},
		PartialStack: true,
	}
}

// readSource reads bytes from a file path, or os.Stdin when path == "-".
// Used by both addons (`--values-from-file` / `--values -`) and manifests
// (`--from-file` / `--manifest -`).
func readSource(filePath, stdinFlag string) ([]byte, error) {
	if stdinFlag == "-" {
		return io.ReadAll(os.Stdin)
	}
	if filePath == "" {
		return nil, errors.New("no source provided (use a file path or `-` for stdin)")
	}
	if filePath == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(filePath)
}

// outputFormat is the legal set of values for the shared -o flag.
type outputFormat string

const (
	outputDefault outputFormat = ""
	outputJSON    outputFormat = "json"
	outputYAML    outputFormat = "yaml"
)

func parseOutputFormat(s string) (outputFormat, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return outputDefault, nil
	case "json":
		return outputJSON, nil
	case "yaml", "yml":
		return outputYAML, nil
	default:
		return outputDefault, fmt.Errorf("invalid -o format %q (expected json or yaml)", s)
	}
}

// encodeStructured writes value as indented JSON or YAML for the structured -o
// formats. It is a no-op for outputDefault, so callers gate on the format being
// non-default before falling back to their human-readable rendering.
func encodeStructured(out io.Writer, format outputFormat, value interface{}) error {
	switch format {
	case outputJSON:
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	case outputYAML:
		encoder := yaml.NewEncoder(out)
		encoder.SetIndent(2)
		defer func() { _ = encoder.Close() }()
		return encoder.Encode(value)
	default:
		return nil
	}
}

// dryRunEnvelope is the structured shape emitted by --dry-run -o json|yaml so
// CI scripts can lint the diff before approving.
type dryRunEnvelope struct {
	Before  client.StackSpec `json:"before" yaml:"before"`
	After   client.StackSpec `json:"after" yaml:"after"`
	Notices []string         `json:"notices,omitempty" yaml:"notices,omitempty"`
}

// renderDryRun emits a human-readable before/after YAML by default, or a
// structured envelope for -o json|yaml. The envelope is well-defined for CI
// pipelines that want to gate on planned changes.
func renderDryRun(out io.Writer, before, after client.StackSpec, notices []string, format outputFormat) error {
	env := dryRunEnvelope{Before: before, After: after, Notices: notices}
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(env)
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(env)
	}
	_, _ = fmt.Fprintln(out, "DRY RUN — no changes will be sent to the API.")
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Before:")
	if err := writeYAMLIndented(out, before, "  "); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "After:")
	if err := writeYAMLIndented(out, after, "  "); err != nil {
		return err
	}
	if len(notices) > 0 {
		_, _ = fmt.Fprintln(out, "Notices:")
		for _, n := range notices {
			_, _ = fmt.Fprintf(out, "  - %s\n", n)
		}
	}
	return nil
}

func writeYAMLIndented(out io.Writer, v any, indent string) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	_ = enc.Close()
	scanner := bufio.NewScanner(&buf)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		_, _ = fmt.Fprintln(out, indent+scanner.Text())
	}
	return scanner.Err()
}

// printAsOutput renders the post-apply PatchStackResult. Default mode is a
// human-readable summary including the commit URL when present; json/yaml
// emit the raw struct for CI.
func printAsOutput(out io.Writer, res *client.PatchStackResult, format outputFormat) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	case outputYAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(res)
	}
	_, _ = fmt.Fprintf(out, "Stack %q updated.\n", res.StackName)
	if res.OperationID != "" {
		_, _ = fmt.Fprintf(out, "  Operation ID: %s\n", res.OperationID)
	}
	if res.JobCount > 0 {
		_, _ = fmt.Fprintf(out, "  Jobs queued: %d\n", res.JobCount)
	}
	if res.CommitSHA != "" {
		_, _ = fmt.Fprintf(out, "  Commit:       %s\n", res.CommitSHA)
	}
	if res.CommitURL != "" {
		_, _ = fmt.Fprintf(out, "  Commit URL:   %s\n", res.CommitURL)
	}
	return nil
}

// mapPatchError converts a typed PatchStackError into a friendly, actionable
// message. The backend status code semantics are documented in the use-case
// layer:
//
//   - 400: validation (stack mismatch, count != 1) — should be unreachable
//     via CLI preflight; print as-is so users see the underlying issue.
//   - 403: sandbox cluster — cannot be modified through the CLI.
//   - 409: stack pending deletion — orphaned resources marked for cleanup.
//   - 422: git push failure (credential/auth or write issue), or a circular
//     dependency rejection (surfaced verbatim).
//   - else: print status + body for debugging.
func mapPatchError(perr *client.PatchStackError) error {
	if perr == nil {
		return nil
	}
	detail := extractDetail(perr.Body)
	switch perr.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("request rejected by API: %s", detail)
	case http.StatusForbidden:
		// Platform RBAC denials carry their own error type (and exit code 7):
		// the role lacks a permission, which re-authenticating cannot fix.
		if denied := client.PermissionDeniedFromResponse(perr.StatusCode, perr.Body); denied != nil {
			return denied
		}
		// Any other 403 is a rejected credential/permission: classify as
		// exitAuth while keeping the human-readable message identical.
		if strings.Contains(strings.ToLower(string(perr.Body)), "sandbox") {
			return withExitCode(exitAuth, errors.New("sandbox clusters cannot be patched via the CLI; promote the cluster first"))
		}
		return withExitCode(exitAuth, fmt.Errorf("forbidden: %s", detail))
	case http.StatusNotFound:
		// The stack or cluster does not exist: classify as exitNotFound while
		// preserving the historical default-branch message shape.
		return withExitCode(exitNotFound, fmt.Errorf("update stack failed: status %d, body: %s", perr.StatusCode, detail))
	case http.StatusConflict:
		return fmt.Errorf("cluster is not available — stack may be pending deletion or cluster is deprovisioned: %s", detail)
	case http.StatusUnprocessableEntity:
		// 422 is overloaded: circular-dependency rejections carry a descriptive
		// message that is not a git failure, so surface it as-is.
		if strings.Contains(strings.ToLower(detail), "circular dependency") {
			return errors.New(detail)
		}
		return fmt.Errorf("git push failed: %s", detail)
	case http.StatusUnauthorized:
		// Wrap ErrUnauthorized (whose message is byte-for-byte identical to the
		// historical text) so errors.Is matches and the exit code is exitAuth.
		return client.ErrUnauthorized
	}
	// 5xx and any other unexpected status: wrap an *UnexpectedResponseError
	// carrying the status code so the root-level support hint fires. The
	// message text is unchanged from the historical default branch.
	return client.NewUnexpectedResponseError(perr.StatusCode,
		fmt.Sprintf("update stack failed: status %d, body: %s", perr.StatusCode, detail))
}

// extractDetail tries to surface the FastAPI `detail` field; falls back to
// raw body when the payload isn't JSON or doesn't carry that key.
func extractDetail(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var generic map[string]any
	if err := json.Unmarshal(body, &generic); err == nil {
		if d, ok := generic["detail"]; ok {
			switch v := d.(type) {
			case string:
				return v
			case map[string]any:
				if s, _ := json.Marshal(v); s != nil {
					return string(s)
				}
			}
		}
	}
	if len(body) > 500 {
		return string(body[:500]) + "..."
	}
	return string(body)
}

// confirmNamespaceChange prompts the user to confirm a destructive namespace
// change on an addon (Helm reinstall, leaves the old release orphaned).
// Returns nil on user approval; an error otherwise.
func confirmNamespaceChange(ctx context.Context, in io.Reader, out io.Writer, oldNs, newNs string, yes bool) error {
	if yes {
		return nil
	}
	_, _ = fmt.Fprintf(out, "WARNING: changing the namespace from %q to %q is destructive.\n", oldNs, newNs)
	_, _ = fmt.Fprintln(out, "The existing Helm release will be reinstalled in the new namespace; the old release is left orphaned.")
	_, _ = fmt.Fprint(out, "Continue? [y/N]: ")
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("read confirmation: %w", err)
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "y" || line == "yes" {
		return nil
	}
	return errors.New("namespace change cancelled")
}

// addonsUpgradeFlags is the parsed view of the flag set for
// `ankra cluster addons upgrade <name>`.
type addonsUpgradeFlags struct {
	ChartVersion           string
	Namespace              string
	RegistryName           string
	RegistryURL            string
	RegistryCredentialName string

	ValuesFromFile string
	ValuesStdin    string // "-" if set
	SetEntries     []string
	SetStrings     []string
	SetFiles       []string

	AddParents    []string
	RemoveParents []string
	SetParents    []string

	Cluster string
	Stack   string
	DryRun  bool
	Yes     bool
	Output  outputFormat
}

func (f addonsUpgradeFlags) HasValuesReplace() bool {
	return f.ValuesFromFile != "" || f.ValuesStdin == "-"
}

func (f addonsUpgradeFlags) HasSet() bool {
	return len(f.SetEntries) > 0 || len(f.SetStrings) > 0 || len(f.SetFiles) > 0
}

func (f addonsUpgradeFlags) HasParentEdit() bool {
	return len(f.AddParents) > 0 || len(f.RemoveParents) > 0 || len(f.SetParents) > 0
}

func (f addonsUpgradeFlags) HasAnyMutation() bool {
	return f.ChartVersion != "" ||
		f.Namespace != "" ||
		f.RegistryName != "" ||
		f.RegistryURL != "" ||
		f.RegistryCredentialName != "" ||
		f.HasValuesReplace() ||
		f.HasSet() ||
		f.HasParentEdit()
}

// applyAddonMutations returns a NEW AddonSpec with only the requested fields
// changed. Empty-string registry flags are treated as "no change" (cobra
// can't distinguish unset from empty in v1).
//
// When newValuesB64 is non-nil, the configuration block is included in the
// patch with the new bytes. When newValuesB64 is nil and the user supplied no
// values flags, the configuration is omitted entirely so the backend's
// addon_configuration_resource preserves the existing values_base64.
func applyAddonMutations(orig client.AddonSpec, flags addonsUpgradeFlags, newValuesB64 *string) client.AddonSpec {
	out := client.AddonSpec{
		Name:                   orig.Name,
		ChartName:              orig.ChartName,
		ChartVersion:           orig.ChartVersion,
		Namespace:              orig.Namespace,
		RegistryName:           orig.RegistryName,
		RegistryURL:            orig.RegistryURL,
		RegistryCredentialName: orig.RegistryCredentialName,
		Parents:                orig.Parents,
		Settings:               orig.Settings,
	}
	if flags.ChartVersion != "" {
		out.ChartVersion = flags.ChartVersion
	}
	if flags.Namespace != "" {
		out.Namespace = flags.Namespace
	}
	if flags.RegistryName != "" {
		out.RegistryName = flags.RegistryName
	}
	if flags.RegistryURL != "" {
		out.RegistryURL = flags.RegistryURL
	}
	if flags.RegistryCredentialName != "" {
		out.RegistryCredentialName = flags.RegistryCredentialName
	}
	if newValuesB64 != nil {
		out.Configuration = &client.AddonConfigurationSpec{
			ValuesBase64: *newValuesB64,
		}
	}
	return out
}

// manifestsUpgradeFlags is the parsed view of the flag set for
// `ankra cluster manifests upgrade <name>`. There is no --stack flag here:
// manifest names are globally unique across a cluster (a manifest belongs to
// exactly one stack), so there is nothing to disambiguate.
type manifestsUpgradeFlags struct {
	FromFile      string
	ManifestStdin string // "-" if set
	Namespace     string

	SetEntries []string
	SetStrings []string
	SetFiles   []string
	TargetKind string
	TargetName string

	AddParents    []string
	RemoveParents []string
	SetParents    []string

	Cluster string
	DryRun  bool
	Yes     bool
	Output  outputFormat
}

func (f manifestsUpgradeFlags) HasContent() bool {
	return f.FromFile != "" || f.ManifestStdin == "-"
}

func (f manifestsUpgradeFlags) HasSet() bool {
	return len(f.SetEntries) > 0 || len(f.SetStrings) > 0 || len(f.SetFiles) > 0
}

func (f manifestsUpgradeFlags) HasParentEdit() bool {
	return len(f.AddParents) > 0 || len(f.RemoveParents) > 0 || len(f.SetParents) > 0
}

func (f manifestsUpgradeFlags) HasAnyMutation() bool {
	return f.HasContent() || f.HasSet() || f.Namespace != "" || f.HasParentEdit()
}

// confirmPrompt prints message and a [y/N] prompt, returning nil only when the
// user confirms. When yes is true the prompt is skipped.
func confirmPrompt(in io.Reader, out io.Writer, message string, yes bool) error {
	if yes {
		return nil
	}
	_, _ = fmt.Fprint(out, message)
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("read confirmation: %w", err)
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "y" || line == "yes" {
		return nil
	}
	return errCancelled
}

// writeDecodedDoc writes a decoded document (addon values or manifest YAML) to
// out in the requested format. An empty format or "yaml" prints the decoded
// text as-is; "raw" prints the base64-encoded form. Any other format is an
// error.
func writeDecodedDoc(out io.Writer, decoded, format string) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "yaml", "yml":
		text := decoded
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		_, err := io.WriteString(out, text)
		return err
	case "raw":
		_, err := io.WriteString(out, base64.StdEncoding.EncodeToString([]byte(decoded))+"\n")
		return err
	default:
		return fmt.Errorf("invalid -o format %q (expected yaml or raw)", format)
	}
}

// parseParentFlag parses a single `--add-parent` / `--remove-parent` /
// `--set-parent` value of the form `name=<name>,kind=<manifest|addon>`. The
// `kind` is optional and defaults to `manifest`. Only `manifest` and `addon`
// are valid parent kinds (matching the backend's allowed_parents set).
func parseParentFlag(s string) (client.Parent, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return client.Parent{}, errors.New("parent value is empty; expected name=<name>,kind=<manifest|addon>")
	}
	var name, kind string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			return client.Parent{}, fmt.Errorf("invalid parent %q: each field must be key=value (e.g. name=infisical-ns,kind=manifest)", s)
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		switch key {
		case "name":
			name = val
		case "kind":
			kind = strings.ToLower(val)
		default:
			return client.Parent{}, fmt.Errorf("invalid parent field %q: only name and kind are supported", key)
		}
	}
	if name == "" {
		return client.Parent{}, fmt.Errorf("invalid parent %q: name is required", s)
	}
	if kind == "" {
		kind = "manifest"
	}
	if kind != "manifest" && kind != "addon" {
		return client.Parent{}, fmt.Errorf("invalid parent kind %q: must be manifest or addon", kind)
	}
	return client.Parent{Name: name, Kind: client.AnkraResourceKind(kind)}, nil
}

// parseParentFlags parses a slice of raw parent flag values.
func parseParentFlags(values []string) ([]client.Parent, error) {
	out := make([]client.Parent, 0, len(values))
	for _, v := range values {
		p, err := parseParentFlag(v)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// mergeParents computes the final parent list for a resource. When set is
// non-empty it replaces the list wholesale; otherwise the original list is
// taken, removes are applied, then adds. Combining --set-parent with
// --add-parent/--remove-parent is rejected. The result is sorted by
// (kind, name) to match the backend's deterministic ordering. An empty result
// is valid and intentionally clears all parents.
func mergeParents(orig []client.Parent, addRaw, removeRaw, setRaw []string) ([]client.Parent, error) {
	if len(setRaw) > 0 && (len(addRaw) > 0 || len(removeRaw) > 0) {
		return nil, errors.New("--set-parent is mutually exclusive with --add-parent/--remove-parent")
	}

	if len(setRaw) > 0 {
		set, err := parseParentFlags(setRaw)
		if err != nil {
			return nil, err
		}
		return dedupParents(set), nil
	}

	add, err := parseParentFlags(addRaw)
	if err != nil {
		return nil, err
	}
	remove, err := parseParentFlags(removeRaw)
	if err != nil {
		return nil, err
	}

	removeSet := make(map[client.Parent]bool, len(remove))
	for _, p := range remove {
		removeSet[p] = true
	}

	result := make([]client.Parent, 0, len(orig)+len(add))
	for _, p := range orig {
		if removeSet[p] {
			continue
		}
		result = append(result, p)
	}
	result = append(result, add...)
	return dedupParents(result), nil
}

// dedupParents removes duplicate parents (by name+kind) and returns a stable,
// sorted slice.
func dedupParents(parents []client.Parent) []client.Parent {
	seen := make(map[client.Parent]bool, len(parents))
	out := make([]client.Parent, 0, len(parents))
	for _, p := range parents {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// resolveClusterForCmd resolves the cluster context for upgrade commands.
// Priority: --cluster flag (name or ID) > currently selected cluster.
func resolveClusterForCmd(flagValue string) (id, name string, err error) {
	if flagValue != "" {
		clusterID, err := resolveClusterID(flagValue)
		if err != nil {
			return "", "", err
		}
		return clusterID, flagValue, nil
	}
	cluster, err := loadSelectedCluster()
	if err != nil {
		return "", "", errors.New("no active cluster selected. Run `ankra cluster select <name>` or pass `--cluster <name|id>`")
	}
	return cluster.ID, cluster.Name, nil
}

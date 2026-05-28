package cmd

import (
	"bufio"
	"bytes"
	"context"
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
// metadata (name, description, description_from_file, variables). The caller
// must append exactly one mutated addon or manifest before sending the patch.
// This guarantees the backend's upsert_external(stack) won't wipe description
// or variables when the patch only changes one nested resource.
func copyStackMetadata(src *client.StackSpec) client.StackSpec {
	var vars map[string]string
	if len(src.Variables) > 0 {
		vars = make(map[string]string, len(src.Variables))
		for k, v := range src.Variables {
			vars[k] = v
		}
	}
	return client.StackSpec{
		Name:                src.Name,
		Description:         src.Description,
		DescriptionFromFile: src.DescriptionFromFile,
		Variables:           vars,
		Manifests:           []client.ManifestSpec{},
		Addons:              []client.AddonSpec{},
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
		defer enc.Close()
		return enc.Encode(env)
	}
	fmt.Fprintln(out, "DRY RUN — no changes will be sent to the API.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Before:")
	if err := writeYAMLIndented(out, before, "  "); err != nil {
		return err
	}
	fmt.Fprintln(out, "After:")
	if err := writeYAMLIndented(out, after, "  "); err != nil {
		return err
	}
	if len(notices) > 0 {
		fmt.Fprintln(out, "Notices:")
		for _, n := range notices {
			fmt.Fprintf(out, "  - %s\n", n)
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
		fmt.Fprintln(out, indent+scanner.Text())
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
		defer enc.Close()
		return enc.Encode(res)
	}
	fmt.Fprintf(out, "Stack %q updated.\n", res.StackName)
	if res.OperationID != "" {
		fmt.Fprintf(out, "  Operation ID: %s\n", res.OperationID)
	}
	if res.JobCount > 0 {
		fmt.Fprintf(out, "  Jobs queued: %d\n", res.JobCount)
	}
	if res.CommitSHA != "" {
		fmt.Fprintf(out, "  Commit:       %s\n", res.CommitSHA)
	}
	if res.CommitURL != "" {
		fmt.Fprintf(out, "  Commit URL:   %s\n", res.CommitURL)
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
//   - 422: UpdateClusterStackGitPushError — credential/auth or write issue.
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
		if strings.Contains(strings.ToLower(string(perr.Body)), "sandbox") {
			return errors.New("sandbox clusters cannot be patched via the CLI; promote the cluster first")
		}
		return fmt.Errorf("forbidden: %s", detail)
	case http.StatusConflict:
		return fmt.Errorf("cluster is not available — stack may be pending deletion or cluster is deprovisioned: %s", detail)
	case http.StatusUnprocessableEntity:
		return fmt.Errorf("git push failed: %s", detail)
	case http.StatusUnauthorized:
		return errors.New("unauthorized. Run `ankra login` to re-authenticate")
	}
	return fmt.Errorf("update stack failed: status %d, body: %s", perr.StatusCode, detail)
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
	fmt.Fprintf(out, "WARNING: changing the namespace from %q to %q is destructive.\n", oldNs, newNs)
	fmt.Fprintln(out, "The existing Helm release will be reinstalled in the new namespace; the old release is left orphaned.")
	fmt.Fprint(out, "Continue? [y/N]: ")
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

func (f addonsUpgradeFlags) HasAnyMutation() bool {
	return f.ChartVersion != "" ||
		f.Namespace != "" ||
		f.RegistryName != "" ||
		f.RegistryURL != "" ||
		f.RegistryCredentialName != "" ||
		f.HasValuesReplace() ||
		f.HasSet()
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

	Cluster string
	DryRun  bool
	Output  outputFormat
}

func (f manifestsUpgradeFlags) HasContent() bool {
	return f.FromFile != "" || f.ManifestStdin == "-"
}

func (f manifestsUpgradeFlags) HasAnyMutation() bool {
	return f.HasContent() || f.Namespace != ""
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


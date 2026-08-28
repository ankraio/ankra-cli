package cmd

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ankra/internal/client"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var clusterApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply an ImportCluster YAML to the Ankra API",
	Args:  cobra.NoArgs,
	RunE:  runApply,
}

func init() {
	clusterApplyCmd.Flags().StringP("file", "f", "", "Path to the ImportCluster YAML file to apply")
	clusterApplyCmd.Flags().Bool("dry-run", false, "Validate the ImportCluster YAML locally without calling the API")
	// Repointing a cluster's GitOps source writes the cluster's state to the new
	// source first and makes it authoritative from then on. The server refuses
	// it without these, so they are the CLI half of that gate rather than a
	// local check (ankra-po6d, ankra-apjjn).
	clusterApplyCmd.Flags().Bool("allow-repoint", false,
		"Allow this apply to change the cluster's GitOps repository or branch. Ankra writes the cluster's current state to the new source first, then syncs from it; anything that later leaves that source is pruned. A target that cannot be written leaves the cluster unchanged")
	clusterApplyCmd.Flags().Bool("allow-repoint-destroying-data", false,
		"Additionally allow a repoint on a cluster holding PersistentVolumeClaims, whose data is destroyed if the new source stops defining their workloads. Requires --allow-repoint")
	registerAsyncWriteFlags(clusterApplyCmd)
	// The shared wording ("wait for the operation to finish") is true of the
	// node-group and bastion writes, but on apply it promises more than it
	// delivers: the server commits the configuration, pushes to Git and
	// returns, and the reconciler dispatches the add-on deploys afterwards.
	// A reporter read the old text as a rollout gate and took a clean exit 0
	// as proof the deploy had succeeded while three add-ons crash-looped
	// (ankra-6j2w / PLA-748, from Ankra #1059).
	if waitFlag := clusterApplyCmd.Flags().Lookup("wait"); waitFlag != nil {
		waitFlag.Usage = "Wait for the configuration write to be applied and report its result. " +
			"Add-on deploys are dispatched afterwards and are NOT covered by this flag - " +
			"watch them with 'ankra cluster operations list'"
	}
	registerStructuredOutputFlags(clusterApplyCmd)
	setDryRunOffline(clusterApplyCmd)
	_ = clusterApplyCmd.MarkFlagRequired("file")
	clusterCmd.AddCommand(clusterApplyCmd)
}

func runApply(cmd *cobra.Command, _ []string) error {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		return fmt.Errorf("reading --file: %w", err)
	}
	if filePath == "" {
		return errors.New("--file is required")
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return fmt.Errorf("reading --dry-run: %w", err)
	}

	importRequest, err := buildImportRequest(filePath)
	if err != nil {
		return fmt.Errorf("invalid ImportCluster in %q:\n  %w", filePath, err)
	}

	allowRepoint, err := cmd.Flags().GetBool("allow-repoint")
	if err != nil {
		return fmt.Errorf("reading --allow-repoint: %w", err)
	}
	allowRepointDestroyingData, err := cmd.Flags().GetBool("allow-repoint-destroying-data")
	if err != nil {
		return fmt.Errorf("reading --allow-repoint-destroying-data: %w", err)
	}
	if allowRepointDestroyingData && !allowRepoint {
		return errors.New("--allow-repoint-destroying-data requires --allow-repoint")
	}
	importRequest.AllowRepoint = allowRepoint
	importRequest.AllowRepointDestroyingData = allowRepointDestroyingData

	if err := validateResourceGraph(importRequest); err != nil {
		return fmt.Errorf("invalid ImportCluster in %q:\n  %w", filePath, err)
	}

	if dryRun {
		fmt.Printf("Validation succeeded for %q; no changes applied (--dry-run).\n", filePath)
		return nil
	}

	wait, err := asyncWriteWaitFlag(cmd)
	if err != nil {
		return err
	}
	requestContext, cancelRequestContext, err := asyncWriteRequestContext(cmd)
	if err != nil {
		return err
	}
	defer cancelRequestContext()

	importResponse, submitted, err := apiClient.ApplyCluster(requestContext, importRequest, wait)
	if err != nil {
		return asyncWriteError("applying cluster", wait, err)
	}
	if submitted {
		if rendered, err := renderStructured(cmd, newAsyncSubmittedResult("Cluster apply")); rendered || err != nil {
			return err
		}
		printAsyncWriteSubmitted("Cluster apply")
		fmt.Println("For a new cluster, the agent install command is only shown when you use --wait.")
		return nil
	}

	if len(importResponse.Errors) > 0 {
		rendered, renderErr := renderStructured(cmd, importResponse)
		if renderErr != nil {
			return renderErr
		}
		if !rendered {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Import failed with the following issues:")
			for _, resourceError := range importResponse.Errors {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "- %s %q:\n", resourceError.Kind, resourceError.Name)
				for _, detail := range resourceError.Errors {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "    • %s: %s\n", detail.Key, detail.Message)
				}
			}
		}
		return errors.New("import failed")
	}

	if rendered, err := renderStructured(cmd, importResponse); rendered || err != nil {
		return err
	}

	if importResponse.ImportCommand == "" {
		fmt.Printf("Cluster '%s' configuration applied.\n\n", importResponse.Name)
		fmt.Println("Add-on and manifest deploys run in the background from here.")
		fmt.Println("Track them with 'ankra cluster operations list' before treating this as deployed.")
	} else {
		fmt.Printf("Cluster '%s' imported!\n\n", importResponse.Name)
		fmt.Println("To install the Ankra agent, run:")
		commandParts := strings.Fields(importResponse.ImportCommand)
		flattenedCommand := strings.Join(commandParts, " ")
		fmt.Println(flattenedCommand)
	}

	fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
		strings.TrimRight(baseURL, "/"), importResponse.ClusterId)
	return nil
}

func buildImportRequest(path string) (client.CreateImportClusterRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return client.CreateImportClusterRequest{}, fmt.Errorf("could not read the file: %w", err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return client.CreateImportClusterRequest{}, fmt.Errorf("the file is not valid YAML: %w", err)
	}

	kind, _ := raw["kind"].(string)
	if kind == "" {
		return client.CreateImportClusterRequest{}, errors.New("the 'kind' field is missing; it must be set to \"ImportCluster\"")
	}
	if kind != "ImportCluster" {
		return client.CreateImportClusterRequest{}, fmt.Errorf("the 'kind' field must be \"ImportCluster\", but found %q", kind)
	}

	meta, ok := raw["metadata"].(map[string]interface{})
	if !ok {
		return client.CreateImportClusterRequest{}, errors.New("the 'metadata' section is missing (it must contain at least 'name')")
	}
	clusterName, _ := meta["name"].(string)
	if clusterName == "" {
		return client.CreateImportClusterRequest{}, errors.New("'metadata.name' is required (this is the cluster name)")
	}
	clusterDescription, _ := meta["description"].(string)

	spec, ok := raw["spec"].(map[string]interface{})
	if !ok {
		return client.CreateImportClusterRequest{}, errors.New("the 'spec' section is missing (it must contain 'stacks' and optionally 'git_repository')")
	}

	var gitRepository *client.GitRepository
	if gr, ok := spec["git_repository"].(map[string]interface{}); ok {
		gitRepository = &client.GitRepository{
			Provider:       optString(gr, "provider"),
			CredentialName: optString(gr, "credential_name"),
			Branch:         optString(gr, "branch"),
			Repository:     optString(gr, "repository"),
			Workspace:      optString(gr, "workspace"),
			RepoSlug:       optString(gr, "repo_slug"),
			ProjectKey:     optString(gr, "project_key"),
			InstanceURL:    optString(gr, "instance_url"),
		}
		if gitRepository.Provider == "" {
			gitRepository.Provider = "github"
		}
	}

	var prometheusMetrics *client.PrometheusMetrics
	if pm, ok := spec["prometheus_metrics"].(map[string]interface{}); ok {
		endpoint := optString(pm, "endpoint")
		if endpoint != "" {
			prometheusMetrics = &client.PrometheusMetrics{
				Endpoint:       endpoint,
				CredentialName: optString(pm, "credential_name"),
				Flavor:         optString(pm, "flavor"),
			}
		}
	}

	baseDirectory := filepath.Dir(path)
	rawStackItems, _ := spec["stacks"].([]interface{})
	stacks := make([]client.Stack, 0, len(rawStackItems))
	for index, rawStack := range rawStackItems {
		stackLabel := fmt.Sprintf("stack #%d", index+1)
		stackMap, ok := rawStack.(map[string]interface{})
		if !ok {
			return client.CreateImportClusterRequest{}, fmt.Errorf("%s is not a valid object (expected fields such as 'name', 'manifests', 'addons')", stackLabel)
		}
		if stackName, _ := stackMap["name"].(string); stackName != "" {
			stackLabel = fmt.Sprintf("stack %q", stackName)
		}
		builtStack, err := buildStack(stackMap, baseDirectory)
		if err != nil {
			return client.CreateImportClusterRequest{}, fmt.Errorf("%s: %w", stackLabel, err)
		}
		stacks = append(stacks, builtStack)
	}

	return client.CreateImportClusterRequest{
		Name:        clusterName,
		Description: clusterDescription,
		Spec: client.CreateResourceSpec{
			GitRepository:     gitRepository,
			PrometheusMetrics: prometheusMetrics,
			Stacks:            stacks,
		},
	}, nil
}

// addonConfigurationKeys is every key an addon's 'configuration' block may
// carry. Anything else in there is a key this CLI would drop on the floor, so
// buildAddon refuses the file rather than deploying chart defaults.
var addonConfigurationKeys = map[string]bool{
	"from_file":       true,
	"values":          true,
	"values_base64":   true,
	"encrypted_paths": true,
}

// unknownConfigurationKeys lists, in a stable order, the keys of an addon
// configuration block that this CLI does not read.
func unknownConfigurationKeys(conf map[string]interface{}) []string {
	unknown := make([]string, 0)
	for _, key := range sortedKeys(conf) {
		if !addonConfigurationKeys[key] {
			unknown = append(unknown, key)
		}
	}
	return unknown
}

// describeVariableValue renders a rejected variable for an error message: the
// value itself when it is a scalar (the hint only lands if you can see the
// 1.20 that provoked it), the type alone when it is a map or a list, whose
// contents may be a credential and whose shape is the actual problem anyway.
func describeVariableValue(value interface{}) string {
	switch value.(type) {
	case map[string]interface{}, []interface{}, map[interface{}]interface{}:
		return fmt.Sprintf("a %T", value)
	default:
		return fmt.Sprintf("%v", value)
	}
}

// checkConfigurationTypes rejects a recognised addon 'configuration' key whose
// value is not the type that key is read as. Without it the read below simply
// fails its type assertion and the block collapses to no configuration at all,
// which is the ankra-yxxa failure: the addon installs on chart defaults, over
// whatever it was running, and apply reports success.
//
// A key present but explicitly nil is left alone - 'values:' with nothing
// after it means the same deliberate "no configuration" as 'values: ""'.
func checkConfigurationTypes(conf map[string]interface{}) error {
	for _, key := range []string{"from_file", "values", "values_base64"} {
		value, present := conf[key]
		if !present || value == nil {
			continue
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("addon 'configuration.%s' must be a string, got %T", key, value)
		}
	}
	if value, present := conf["encrypted_paths"]; present && value != nil {
		if _, ok := value.([]interface{}); !ok {
			return fmt.Errorf("addon 'configuration.encrypted_paths' must be a list of strings, got %T", value)
		}
	}
	return nil
}

// sortedKeys returns a map's keys in a stable order, so an error message that
// lists what a block did contain reads the same on every run.
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func optString(m map[string]interface{}, key string) string {
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	return ""
}

// validateYAMLDocuments confirms that content parses as one or more valid YAML
// documents (manifests may contain several `---`-separated documents). Empty
// content is treated as valid (no documents).
func validateYAMLDocuments(content []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	for {
		var document interface{}
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type resourceNode struct {
	kind string
	name string
}

func (node resourceNode) String() string {
	return fmt.Sprintf("%s %q", node.kind, node.name)
}

// validateResourceGraph checks the parent/dependency tree of an assembled
// ImportCluster offline: resource names must be unique per kind, every parent
// reference must name a real manifest or addon declared in the same document,
// must use a valid kind, and the resulting graph must be acyclic. This catches
// the dependency errors the backend would otherwise only reject at apply time
// (HTTP 422).
func validateResourceGraph(request client.CreateImportClusterRequest) error {
	declaredResources := map[resourceNode]bool{}
	resourceLabels := map[resourceNode]string{}
	declarationOrder := make([]resourceNode, 0)

	// Parent references resolve by (kind, name) with no stack qualifier, so the
	// same (kind, name) declared twice - even across stacks - is ambiguous and
	// the backend rejects it. Flag duplicates here rather than silently merging
	// them into a single node.
	addResource := func(kind, name, stackName string) error {
		node := resourceNode{kind: kind, name: name}
		if declaredResources[node] {
			return fmt.Errorf("%s is declared more than once (most recently in stack %q); names must be unique per kind across the whole file", node, stackName)
		}
		declaredResources[node] = true
		declarationOrder = append(declarationOrder, node)
		resourceLabels[node] = fmt.Sprintf("%s in stack %q", node, stackName)
		return nil
	}

	for _, stack := range request.Spec.Stacks {
		for _, manifest := range stack.Manifests {
			if err := addResource("manifest", manifest.Name, stack.Name); err != nil {
				return err
			}
		}
		for _, addon := range stack.Addons {
			if err := addResource("addon", addon.Name, stack.Name); err != nil {
				return err
			}
		}
	}

	dependencyEdges := map[resourceNode][]resourceNode{}

	collectParents := func(current resourceNode, parents []client.Parent) error {
		label := resourceLabels[current]
		for parentIndex, parent := range parents {
			kind := strings.ToLower(strings.TrimSpace(string(parent.Kind)))
			if kind != "manifest" && kind != "addon" {
				return fmt.Errorf("%s: parent #%d has invalid kind %q (must be \"manifest\" or \"addon\")", label, parentIndex+1, parent.Kind)
			}
			parentNode := resourceNode{kind: kind, name: parent.Name}
			if !declaredResources[parentNode] {
				return fmt.Errorf("%s: parent %s is not defined anywhere in this file", label, parentNode)
			}
			dependencyEdges[current] = append(dependencyEdges[current], parentNode)
		}
		return nil
	}

	for _, stack := range request.Spec.Stacks {
		for _, manifest := range stack.Manifests {
			if err := collectParents(resourceNode{kind: "manifest", name: manifest.Name}, manifest.Parents); err != nil {
				return err
			}
		}
		for _, addon := range stack.Addons {
			if err := collectParents(resourceNode{kind: "addon", name: addon.Name}, addon.Parents); err != nil {
				return err
			}
		}
	}

	return detectDependencyCycle(declarationOrder, dependencyEdges)
}

func detectDependencyCycle(declarationOrder []resourceNode, dependencyEdges map[resourceNode][]resourceNode) error {
	const (
		unvisited = iota
		onCurrentPath
		fullyExplored
	)
	visitState := map[resourceNode]int{}
	currentPath := make([]resourceNode, 0, len(declarationOrder))

	var visit func(node resourceNode) error
	visit = func(node resourceNode) error {
		visitState[node] = onCurrentPath
		currentPath = append(currentPath, node)
		for _, parent := range dependencyEdges[node] {
			switch visitState[parent] {
			case onCurrentPath:
				return fmt.Errorf("dependency cycle detected: %s", formatCycle(currentPath, parent))
			case unvisited:
				if err := visit(parent); err != nil {
					return err
				}
			}
		}
		currentPath = currentPath[:len(currentPath)-1]
		visitState[node] = fullyExplored
		return nil
	}

	for _, node := range declarationOrder {
		if visitState[node] == unvisited {
			if err := visit(node); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatCycle(currentPath []resourceNode, cycleStart resourceNode) string {
	startIndex := 0
	for index, node := range currentPath {
		if node == cycleStart {
			startIndex = index
			break
		}
	}
	cycle := append([]resourceNode{}, currentPath[startIndex:]...)
	cycle = append(cycle, cycleStart)
	parts := make([]string, 0, len(cycle))
	for _, node := range cycle {
		parts = append(parts, node.String())
	}
	return strings.Join(parts, " -> ")
}

func buildStack(sm map[string]interface{}, baseDir string) (client.Stack, error) {
	name, _ := sm["name"].(string)
	if name == "" {
		return client.Stack{}, errors.New("every stack needs a 'name'")
	}
	desc, _ := sm["description"].(string)
	if descFile, ok := sm["description_from_file"].(string); ok && descFile != "" {
		full, err := resolveSafePath(baseDir, descFile)
		if err != nil {
			return client.Stack{}, fmt.Errorf("refusing to read the file referenced by 'description_from_file' (%q): %w", descFile, err)
		}
		fileContent, err := os.ReadFile(full)
		if err != nil {
			return client.Stack{}, fmt.Errorf("could not read the file referenced by 'description_from_file' (%q): %w", full, err)
		}
		if desc == "" {
			desc = string(fileContent)
		}
	}

	var manifests []client.Manifest
	if rawMan, ok := sm["manifests"].([]interface{}); ok {
		for i, mi := range rawMan {
			manifestLabel := fmt.Sprintf("manifest #%d", i+1)
			mm, ok := mi.(map[string]interface{})
			if !ok {
				return client.Stack{}, fmt.Errorf("%s is not a valid object (expected fields such as 'name' and 'from_file' or 'manifest')", manifestLabel)
			}
			if manifestName, _ := mm["name"].(string); manifestName != "" {
				manifestLabel = fmt.Sprintf("manifest %q", manifestName)
			}
			m, err := buildManifest(mm, baseDir)
			if err != nil {
				return client.Stack{}, fmt.Errorf("%s: %w", manifestLabel, err)
			}
			manifests = append(manifests, m)
		}
	}

	var addons []client.Addon
	if rawAdd, ok := sm["addons"].([]interface{}); ok {
		for i, ai := range rawAdd {
			addonLabel := fmt.Sprintf("addon #%d", i+1)
			am, ok := ai.(map[string]interface{})
			if !ok {
				return client.Stack{}, fmt.Errorf("%s is not a valid object (expected fields such as 'name', 'chart_name', 'chart_version')", addonLabel)
			}
			if addonName, _ := am["name"].(string); addonName != "" {
				addonLabel = fmt.Sprintf("addon %q", addonName)
			}
			a, err := buildAddon(am, baseDir)
			if err != nil {
				return client.Stack{}, fmt.Errorf("%s: %w", addonLabel, err)
			}
			addons = append(addons, a)
		}
	}

	deployWave, err := parseDeployWave(sm["deploy_wave"])
	if err != nil {
		return client.Stack{}, err
	}

	variables, err := parseStackVariables(sm["variables"])
	if err != nil {
		return client.Stack{}, err
	}

	return client.Stack{
		Name:        name,
		Description: desc,
		Manifests:   manifests,
		Addons:      addons,
		DeployWave:  deployWave,
		Variables:   variables,
	}, nil
}

// parseStackVariables reads the optional stack-level 'variables' map, the
// scope that shadows cluster and organisation variables when this stack's
// manifests and addons are rendered. Apply used to drop it (ankra-yxxa): the
// stack came back with no variables at all and every '${VAR}' reached the
// cluster as a literal token, so string fields got nonsense and numeric ones
// failed the typed patch outright.
//
// The backend stores variables as strings. Integers and booleans are written
// out as their literal text because that conversion is exact; anything a
// conversion would mangle - a float such as 1.20, a nested structure, a key
// with no value - is rejected with a hint to quote it, the same guard
// requiredAddonString applies to chart_version.
//
// The int cases carry the common path, not the rare one: this file is decoded
// by gopkg.in/yaml.v3, which yields a Go int for an unquoted 'REPLICAS: 2'.
// A decoder that round-trips through JSON would hand every number over as a
// float64 and send plain integers down the reject branch instead, so that
// contract is pinned by a test rather than assumed.
func parseStackVariables(raw interface{}) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	rawMap, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("'variables' must be a map of name to value, got %v", raw)
	}
	variables := make(map[string]string, len(rawMap))
	for _, key := range sortedKeys(rawMap) {
		switch typed := rawMap[key].(type) {
		case string:
			variables[key] = typed
		case int:
			variables[key] = strconv.Itoa(typed)
		case int64:
			variables[key] = strconv.FormatInt(typed, 10)
		case uint64:
			variables[key] = strconv.FormatUint(typed, 10)
		case bool:
			variables[key] = strconv.FormatBool(typed)
		default:
			// Echo a scalar, because seeing 1.20 is what makes the hint land,
			// but describe a map or a list by type only: a nested block is
			// where a credential would be hiding, and this error travels into
			// CI logs.
			return nil, fmt.Errorf(
				"variable %q must be a string (got %s - quote it, as variables are stored as strings and an unquoted value like 1.20 would be rewritten to \"1.2\")",
				key, describeVariableValue(rawMap[key]))
		}
	}
	return variables, nil
}

// parseDeployWave validates the optional 'deploy_wave' stack field: a
// non-negative integer that orders stacks against each other (stacks in wave
// N deploy only after every stack in a lower wave finished).
func parseDeployWave(raw interface{}) (*int, error) {
	if raw == nil {
		return nil, nil
	}
	var wave int
	switch typed := raw.(type) {
	case int:
		wave = typed
	case int64:
		wave = int(typed)
	case float64:
		if typed != float64(int(typed)) {
			return nil, fmt.Errorf("'deploy_wave' must be a whole number, got %v", typed)
		}
		wave = int(typed)
	default:
		return nil, fmt.Errorf("'deploy_wave' must be an integer, got %v", raw)
	}
	if wave < 0 {
		return nil, fmt.Errorf("'deploy_wave' must be zero or positive, got %d", wave)
	}
	return &wave, nil
}

// parseAgentsMdFields extracts the optional 'agents_md' /
// 'agents_md_from_file' pair shared by manifests and addons. The returned
// pointers carry the backend's tri-state semantics: nil = key absent (the
// backend preserves the stored AGENTS.md), pointer to "" = explicit clear.
// When 'agents_md_from_file' is set, the file's content is read (mirroring
// how the stack-level 'description_from_file' is handled) and the path is
// passed through so the stored pointer matches the file the user authored;
// a non-empty inline 'agents_md' wins over the file content.
func parseAgentsMdFields(m map[string]interface{}, baseDir string) (*string, *string, error) {
	var agentsMd, agentsMdFromFile *string
	if inline, ok := m["agents_md"].(string); ok {
		agentsMd = &inline
	}
	if fileRef, ok := m["agents_md_from_file"].(string); ok && fileRef != "" {
		full, err := resolveSafePath(baseDir, fileRef)
		if err != nil {
			return nil, nil, fmt.Errorf("refusing to read the file referenced by 'agents_md_from_file' (%q): %w", fileRef, err)
		}
		fileContent, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, fmt.Errorf("could not read the file referenced by 'agents_md_from_file' (%q): %w", full, err)
		}
		if agentsMd == nil || *agentsMd == "" {
			content := string(fileContent)
			agentsMd = &content
		}
		agentsMdFromFile = &fileRef
	}
	return agentsMd, agentsMdFromFile, nil
}

// parseGroupField extracts the optional 'group' organizational label shared
// by manifests and addons - the key the platform's own IaC export writes to
// record which group a resource sits in within its stack.
//
// A non-string value is an error rather than an empty string: the platform
// types 'group' as a string and would answer 422, and quietly turning a
// mistyped value into "no group" is the same silent drop that made the label
// disappear from applied exports in the first place (ankra-o0k2f). An absent
// key stays "ungrouped", which apply then prunes server-side like any other
// value the file no longer declares.
func parseGroupField(m map[string]interface{}) (string, error) {
	raw, present := m["group"]
	if !present || raw == nil {
		return "", nil
	}
	group, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("'group' must be a quoted string (got %v)", raw)
	}
	return group, nil
}

func buildManifest(mm map[string]interface{}, baseDir string) (client.Manifest, error) {
	name, _ := mm["name"].(string)
	if name == "" {
		return client.Manifest{}, errors.New("every manifest needs a 'name'")
	}

	var content []byte
	var contentSource string
	// encoded is what actually goes to the platform. When the manifest arrived
	// already base64-encoded it is that same string, passed straight through,
	// so an export -> apply round trip sends back byte-for-byte what it read.
	var encoded string
	if inline, ok := mm["manifest"].(string); ok && inline != "" {
		content = []byte(inline)
		encoded = base64.StdEncoding.EncodeToString(content)
		contentSource = "the inline 'manifest' content"
	} else if fileRef, ok := mm["from_file"].(string); ok {
		full, err := resolveSafePath(baseDir, fileRef)
		if err != nil {
			return client.Manifest{}, fmt.Errorf("refusing to read the file referenced by 'from_file' (%q): %w", fileRef, err)
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return client.Manifest{}, fmt.Errorf("could not read the file referenced by 'from_file' (%q): %w", full, err)
		}
		content = b
		encoded = base64.StdEncoding.EncodeToString(content)
		contentSource = fmt.Sprintf("the file referenced by 'from_file' (%q)", full)
	} else if b64, ok := mm["manifest_base64"].(string); ok && b64 != "" {
		// This is the form the platform's own IaC export emits (ManifestSpec),
		// so it is what an export/clone -> apply round trip carries. Until
		// ankra-62cvj nothing read it and every such manifest was rejected as
		// having no content at all - loudly, unlike the ankra-yxxa addon drop,
		// but it made an exported ImportCluster unappliable without hand-editing.
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return client.Manifest{}, fmt.Errorf("the manifest 'manifest_base64' is not valid base64: %w", err)
		}
		content = decoded
		encoded = b64
		contentSource = "the 'manifest_base64' content"
	} else {
		return client.Manifest{}, errors.New("a manifest must set either 'manifest' (inline YAML), 'from_file' (path to a YAML file) or 'manifest_base64' (base64-encoded YAML)")
	}

	if err := validateYAMLDocuments(content); err != nil {
		return client.Manifest{}, fmt.Errorf("%s is not valid YAML: %w", contentSource, err)
	}

	ns, _ := mm["namespace"].(string)
	parents := parseParentList(mm["parents"])

	// Parse encrypted_paths if present
	var encryptedPaths []string
	if rawPaths, ok := mm["encrypted_paths"].([]interface{}); ok {
		for _, p := range rawPaths {
			if s, ok := p.(string); ok {
				encryptedPaths = append(encryptedPaths, s)
			}
		}
	}

	group, err := parseGroupField(mm)
	if err != nil {
		return client.Manifest{}, err
	}

	agentsMd, agentsMdFromFile, err := parseAgentsMdFields(mm, baseDir)
	if err != nil {
		return client.Manifest{}, err
	}

	return client.Manifest{
		Name:             name,
		ManifestBase64:   encoded,
		Namespace:        ns,
		Parents:          parents,
		EncryptedPaths:   encryptedPaths,
		Group:            group,
		AgentsMd:         agentsMd,
		AgentsMdFromFile: agentsMdFromFile,
	}, nil
}

func buildAddon(am map[string]interface{}, baseDir string) (client.Addon, error) {
	name, _ := am["name"].(string)
	if name == "" {
		return client.Addon{}, errors.New("every addon needs a 'name'")
	}
	chart, err := requiredAddonString(am, name, "chart_name")
	if err != nil {
		return client.Addon{}, err
	}
	ver, err := requiredAddonString(am, name, "chart_version")
	if err != nil {
		return client.Addon{}, err
	}
	ns, _ := am["namespace"].(string)
	parents := parseParentList(am["parents"])

	// Handle legacy repository_url (optional now)
	var repo string
	if r, ok := am["repository_url"].(string); ok {
		repo = r
	}

	// Handle new registry fields
	registryName, _ := am["registry_name"].(string)
	registryURL, _ := am["registry_url"].(string)
	registryCredentialName, _ := am["registry_credential_name"].(string)

	// Handle settings
	var settings map[string]interface{}
	if s, ok := am["settings"].(map[string]interface{}); ok {
		settings = s
	}

	var cfg interface{}
	if conf, ok := am["configuration"].(map[string]interface{}); ok {
		// A key we DO read, holding the wrong type, used to fail its type
		// assertion and fall through to a nil configuration - the ankra-yxxa
		// silent drop again, entered through a malformed value rather than an
		// unread key, and invisible to unknownConfigurationKeys because the
		// key itself is recognised. An explicit nil ('values:' with nothing
		// after it) stays equivalent to the deliberately-empty 'values: ""'.
		if err := checkConfigurationTypes(conf); err != nil {
			return client.Addon{}, err
		}

		// Computed once: the same list decides the hard error below (when the
		// block yielded nothing) and the warning at the end (when it yielded
		// values anyway), and two call sites would be free to drift apart.
		unknownKeys := unknownConfigurationKeys(conf)

		var encryptedPaths []string
		if rawPaths, ok := conf["encrypted_paths"].([]interface{}); ok {
			for i, p := range rawPaths {
				s, ok := p.(string)
				if !ok {
					// Skipping one silently would ship the secret it names
					// unencrypted.
					return client.Addon{}, fmt.Errorf(
						"addon 'configuration.encrypted_paths' entry %d must be a string, got %T", i+1, p)
				}
				encryptedPaths = append(encryptedPaths, s)
			}
		}

		if pf, ok := conf["from_file"].(string); ok {
			full, err := resolveSafePath(baseDir, pf)
			if err != nil {
				return client.Addon{}, fmt.Errorf("refusing to read addon configuration %q: %w", pf, err)
			}
			b, err := os.ReadFile(full)
			if err != nil {
				return client.Addon{}, fmt.Errorf("read addon configuration %q: %w", full, err)
			}
			if err := validateYAMLDocuments(b); err != nil {
				return client.Addon{}, fmt.Errorf("the addon configuration file %q is not valid YAML: %w", full, err)
			}
			cfg = client.AddonStandaloneConfiguration{
				ValuesBase64:   base64.StdEncoding.EncodeToString(b),
				EncryptedPaths: encryptedPaths,
			}
		} else if inline, ok := conf["values"].(string); ok && inline != "" {
			if err := validateYAMLDocuments([]byte(inline)); err != nil {
				return client.Addon{}, fmt.Errorf("the inline addon 'configuration.values' is not valid YAML: %w", err)
			}
			cfg = client.AddonStandaloneConfiguration{
				ValuesBase64:   base64.StdEncoding.EncodeToString([]byte(inline)),
				EncryptedPaths: encryptedPaths,
			}
		} else if encoded, ok := conf["values_base64"].(string); ok && encoded != "" {
			// This is the form the platform's own IaC export emits, so it is
			// what a clone/export -> apply round trip carries. Reading it was
			// missing until ankra-yxxa: the block fell through to a nil
			// configuration and every addon in the file installed with chart
			// defaults, silently and with validate passing.
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return client.Addon{}, fmt.Errorf("the addon 'configuration.values_base64' is not valid base64: %w", err)
			}
			if err := validateYAMLDocuments(decoded); err != nil {
				return client.Addon{}, fmt.Errorf("the addon 'configuration.values_base64' does not decode to valid YAML: %w", err)
			}
			cfg = client.AddonStandaloneConfiguration{
				ValuesBase64:   encoded,
				EncryptedPaths: encryptedPaths,
			}
		} else if len(unknownKeys) > 0 {
			// Keys we do not read are either a typo or a dialect newer than
			// this CLI. Either way, say so - a configuration block that turns
			// into nothing means the addon deploys with chart defaults over
			// whatever it is running, which is how this went unnoticed.
			//
			// This runs ahead of the encrypted_paths complaint below because
			// a block with both a typo'd key and encrypted_paths is better
			// served by the message that names the typo. Neither branch is
			// reached unless the block yielded no values at all, so the set
			// of files that apply is unchanged either way.
			return client.Addon{}, fmt.Errorf(
				"addon 'configuration' has no values this CLI can read: set 'from_file', 'values' or 'values_base64' (unrecognised: %s)",
				strings.Join(unknownKeys, ", "))
		} else if len(encryptedPaths) > 0 {
			return client.Addon{}, errors.New("addon 'configuration.encrypted_paths' is set but there is nothing to decrypt (set 'from_file', 'values' or 'values_base64')")
		}

		// A key we cannot read alongside one we can: the values do reach the
		// addon, so this is not the ankra-yxxa drop, but whatever the extra
		// key meant to say is still being ignored. Warn rather than fail -
		// the platform's own IaC export writes these files, and erroring
		// would mean a dialect that gains a key breaks every older CLI on a
		// file whose values it reads perfectly. Stderr keeps '-o json|yaml'
		// parseable.
		if cfg != nil && len(unknownKeys) > 0 {
			_, _ = fmt.Fprintf(os.Stderr,
				"Warning: addon %q has 'configuration' keys this CLI does not read, so they are ignored: %s. "+
					"Check the spelling, or upgrade if the file came from a newer Ankra.\n",
				name, strings.Join(unknownKeys, ", "))
		}
	}

	group, err := parseGroupField(am)
	if err != nil {
		return client.Addon{}, err
	}

	agentsMd, agentsMdFromFile, err := parseAgentsMdFields(am, baseDir)
	if err != nil {
		return client.Addon{}, err
	}

	return client.Addon{
		Name:                   name,
		ChartName:              chart,
		ChartVersion:           ver,
		RepositoryURL:          repo,
		Namespace:              ns,
		Configuration:          cfg,
		Parents:                parents,
		RegistryName:           registryName,
		RegistryURL:            registryURL,
		RegistryCredentialName: registryCredentialName,
		Settings:               settings,
		Group:                  group,
		AgentsMd:               agentsMd,
		AgentsMdFromFile:       agentsMdFromFile,
	}, nil
}

// requiredAddonString extracts a mandatory string field from an addon map.
// A missing or empty value is an error; a non-string value (e.g. an unquoted
// YAML number that parses as a float) is rejected with a hint to quote it,
// since fmt.Sprint would otherwise mangle it (chart_version: 1.20 -> "1.2").
func requiredAddonString(am map[string]interface{}, addonName, key string) (string, error) {
	value, ok := am[key]
	if !ok || value == nil {
		return "", fmt.Errorf("addon %q: %s is required", addonName, key)
	}
	str, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("addon %q: %s must be a quoted string (got %v - quote it, as YAML reads unquoted numbers like 1.20 as numbers, not \"1.20\")", addonName, key, value)
	}
	if str == "" {
		return "", fmt.Errorf("addon %q: %s is required", addonName, key)
	}
	return str, nil
}

func parseParentList(raw interface{}) []client.Parent {
	arr, ok := raw.([]interface{})
	if !ok {
		return []client.Parent{}
	}
	out := make([]client.Parent, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, okName := m["name"].(string)
		kind, okKind := m["kind"].(string)
		if okName && okKind {
			out = append(out, client.Parent{Name: name, Kind: client.AnkraResourceKind(kind)})
		}
	}
	return out
}

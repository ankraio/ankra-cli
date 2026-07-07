package cmd

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	registerAsyncWriteFlags(clusterApplyCmd)
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
		fmt.Printf("Cluster '%s' has been updated!\n\n", importResponse.Name)
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

	return client.Stack{
		Name:        name,
		Description: desc,
		Manifests:   manifests,
		Addons:      addons,
		DeployWave:  deployWave,
	}, nil
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

func buildManifest(mm map[string]interface{}, baseDir string) (client.Manifest, error) {
	name, _ := mm["name"].(string)
	if name == "" {
		return client.Manifest{}, errors.New("every manifest needs a 'name'")
	}

	var content []byte
	var contentSource string
	if inline, ok := mm["manifest"].(string); ok && inline != "" {
		content = []byte(inline)
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
		contentSource = fmt.Sprintf("the file referenced by 'from_file' (%q)", full)
	} else {
		return client.Manifest{}, errors.New("a manifest must set either 'manifest' (inline YAML) or 'from_file' (path to a YAML file)")
	}

	if err := validateYAMLDocuments(content); err != nil {
		return client.Manifest{}, fmt.Errorf("%s is not valid YAML: %w", contentSource, err)
	}

	encoded := base64.StdEncoding.EncodeToString(content)
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

	return client.Manifest{
		Name:           name,
		ManifestBase64: encoded,
		Namespace:      ns,
		Parents:        parents,
		EncryptedPaths: encryptedPaths,
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
		var encryptedPaths []string
		if rawPaths, ok := conf["encrypted_paths"].([]interface{}); ok {
			for _, p := range rawPaths {
				if s, ok := p.(string); ok {
					encryptedPaths = append(encryptedPaths, s)
				}
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
		} else if len(encryptedPaths) > 0 {
			return client.Addon{}, errors.New("addon 'configuration.encrypted_paths' is set but there is nothing to decrypt (set 'from_file' or 'values')")
		}
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

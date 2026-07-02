package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	// maxRemoteDownloadBytes caps the size of remote cluster YAML / artifact
	// downloads from `ankra cluster clone <url>`. Anything larger almost
	// certainly indicates a misconfiguration or an attempt to exhaust disk.
	maxRemoteDownloadBytes = 10 * 1024 * 1024

	httpDownloadTimeout      = 60 * time.Second
	httpDownloadMaxRedirects = 5
)

// httpDownloadClient is a bounded HTTP client used for `cluster clone` to
// fetch a remote cluster YAML or a referenced manifest/values file. It has
// a hard request timeout, capped redirects, and emits no Authorization
// header, since the remote URL is untrusted user input.
var httpDownloadClient = &http.Client{
	Timeout: httpDownloadTimeout,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= httpDownloadMaxRedirects {
			return fmt.Errorf("stopped after %d redirects", httpDownloadMaxRedirects)
		}
		return nil
	},
}

// openDestinationFile creates the leaf file referenced by dstPath. It uses
// O_CREATE|O_TRUNC|O_WRONLY when --force is set, otherwise O_CREATE|O_EXCL
// so a pre-existing file fails fast and is never silently truncated.
func openDestinationFile(dstPath string) (*os.File, error) {
	flag := os.O_CREATE | os.O_WRONLY | os.O_EXCL
	if forceFlag {
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	file, err := os.OpenFile(dstPath, flag, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open destination file %q: %w", dstPath, err)
	}
	return file, nil
}

var (
	cleanFlag       bool
	forceFlag       bool
	copyMissingFlag bool
	stackFlag       []string
)

var clusterCloneCmd = &cobra.Command{
	Use:   "clone <existing_cluster_file_or_url> <new_cluster_path>",
	Short: "Clone stacks from an existing cluster to a new cluster configuration",
	Long: `Clone stacks from an existing cluster ImportCluster YAML to a new cluster.

The source can be either a local file path or a URL (http/https).

Examples:
  ankra cluster clone cluster.yaml new-cluster.yaml
  ankra cluster clone https://github.com/user/repo/raw/main/cluster.yaml new-cluster.yaml
  ankra cluster clone cluster.yaml new-cluster.yaml --stack "monitoring" --stack "networking"

Flags:
  --clean: Replace all stacks in the new cluster with those from the existing cluster
  --force: Force merge even when stack/addon/manifest names conflict
  --copy-missing: Copy missing files even for skipped stacks
  --stack: Clone only specific stacks by name (can be used multiple times)

Without flags: Merge stacks, skipping any with conflicting names`,
	Args: cobra.ExactArgs(2),
	RunE: runClone,
}

func init() {
	clusterCloneCmd.Flags().BoolVar(&cleanFlag, "clean", false, "Replace all stacks in the new cluster")
	clusterCloneCmd.Flags().BoolVar(&forceFlag, "force", false, "Force merge even when names conflict")
	clusterCloneCmd.Flags().BoolVar(&copyMissingFlag, "copy-missing", false, "Copy missing files even for skipped stacks")
	clusterCloneCmd.Flags().StringSliceVar(&stackFlag, "stack", []string{}, "Clone only specific stacks by name (can be used multiple times)")
	clusterCmd.AddCommand(clusterCloneCmd)
}

func runClone(cmd *cobra.Command, args []string) error {
	existingFileOrURL := args[0]
	newClusterPath := args[1]

	// Determine if source is URL or file path
	var existingData []byte
	var existingBaseDir string
	var err error

	if isURL(existingFileOrURL) {
		fmt.Printf("Downloading cluster configuration from URL: %s\n", existingFileOrURL)
		existingData, err = downloadFile(existingFileOrURL)
		if err != nil {
			return fmt.Errorf("failed to download from URL %q: %w", existingFileOrURL, err)
		}
		// For URLs, we need to construct the base URL for relative file downloads
		parsedURL, _ := url.Parse(existingFileOrURL)
		// Keep the full path up to the cluster file, then remove just the filename
		parsedURL.Path = strings.TrimSuffix(parsedURL.Path, filepath.Base(parsedURL.Path))
		existingBaseDir = strings.TrimSuffix(parsedURL.String(), "/") // Remove trailing slash
	} else {
		// Read existing cluster from local file
		existingData, err = os.ReadFile(existingFileOrURL)
		if err != nil {
			return fmt.Errorf("failed to read existing cluster file %q: %w", existingFileOrURL, err)
		}
		existingBaseDir = filepath.Dir(existingFileOrURL)
	}

	var existingCluster ImportClusterConfig
	if err := yaml.Unmarshal(existingData, &existingCluster); err != nil {
		return fmt.Errorf("failed to parse existing cluster YAML: %w", err)
	}

	if existingCluster.Kind != "ImportCluster" {
		return fmt.Errorf("existing file is not an ImportCluster (kind: %s)", existingCluster.Kind)
	}

	if len(stackFlag) > 0 {
		availableStacks := make(map[string]bool)
		for _, stack := range existingCluster.Spec.Stacks {
			availableStacks[stack.Name] = true
		}

		var missingStacks []string
		for _, requestedStack := range stackFlag {
			if !availableStacks[requestedStack] {
				missingStacks = append(missingStacks, requestedStack)
			}
		}

		if len(missingStacks) > 0 {
			fmt.Printf("Available stacks in source cluster:\n")
			for i, stack := range existingCluster.Spec.Stacks {
				fmt.Printf("  %d. %s\n", i+1, stack.Name)
			}
			return fmt.Errorf("requested stacks not found: %s", strings.Join(missingStacks, ", "))
		}
	}

	// Read or create new cluster
	var newCluster ImportClusterConfig
	var newClusterExists bool

	if _, err := os.Stat(newClusterPath); err == nil {
		newClusterExists = true
		newData, err := os.ReadFile(newClusterPath)
		if err != nil {
			return fmt.Errorf("failed to read new cluster file %q: %w", newClusterPath, err)
		}

		if err := yaml.Unmarshal(newData, &newCluster); err != nil {
			return fmt.Errorf("failed to parse new cluster YAML: %w", err)
		}

		if newCluster.Kind != "ImportCluster" {
			return fmt.Errorf("new file is not an ImportCluster (kind: %s)", newCluster.Kind)
		}
	} else {
		// Create new cluster structure
		newCluster = ImportClusterConfig{
			APIVersion: "v1",
			Kind:       "ImportCluster",
			Metadata: ClusterMetadata{
				Name:        generateNewClusterName(existingCluster.Metadata.Name),
				Description: "Cloned cluster",
			},
			Spec: ClusterSpec{
				GitRepository: existingCluster.Spec.GitRepository,
				Stacks:        []StackConfig{},
			},
		}
	}

	// Get base directories
	newBaseDir := filepath.Dir(newClusterPath)

	// Clone stacks with conflict resolution
	if err := cloneStacks(&existingCluster, &newCluster, existingBaseDir, newBaseDir, isURL(existingFileOrURL)); err != nil {
		return fmt.Errorf("failed to clone stacks: %w", err)
	}

	// Write the new cluster file
	if err := writeClusterFile(newClusterPath, &newCluster); err != nil {
		return fmt.Errorf("failed to write new cluster file: %w", err)
	}

	// Print summary
	printCloneSummary(existingFileOrURL, newClusterPath, newClusterExists, &existingCluster, &newCluster)

	return nil
}

type ImportClusterConfig struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   ClusterMetadata `yaml:"metadata"`
	Spec       ClusterSpec     `yaml:"spec"`
}

type ClusterMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

type ClusterSpec struct {
	GitRepository *GitRepositoryConfig `yaml:"git_repository,omitempty"`
	Stacks        []StackConfig        `yaml:"stacks"`
}

type GitRepositoryConfig struct {
	Provider       string `yaml:"provider"`
	CredentialName string `yaml:"credential_name"`
	Branch         string `yaml:"branch"`
	Repository     string `yaml:"repository,omitempty"`
	Workspace      string `yaml:"workspace,omitempty"`
	RepoSlug       string `yaml:"repo_slug,omitempty"`
	ProjectKey     string `yaml:"project_key,omitempty"`
	InstanceURL    string `yaml:"instance_url,omitempty"`
}

type StackConfig struct {
	Name                string           `yaml:"name"`
	Description         string           `yaml:"description,omitempty"`
	DescriptionFromFile string           `yaml:"description_from_file,omitempty"`
	Manifests           []ManifestConfig `yaml:"manifests,omitempty"`
	Addons              []AddonConfig    `yaml:"addons,omitempty"`
}

type ManifestConfig struct {
	Name           string         `yaml:"name"`
	Parents        []ParentConfig `yaml:"parents,omitempty"`
	FromFile       string         `yaml:"from_file,omitempty"`
	Manifest       string         `yaml:"manifest,omitempty"`
	Namespace      string         `yaml:"namespace,omitempty"`
	EncryptedPaths []string       `yaml:"encrypted_paths,omitempty"`
}

type AddonConfig struct {
	Name                   string                 `yaml:"name"`
	ChartName              string                 `yaml:"chart_name"`
	ChartVersion           string                 `yaml:"chart_version"`
	RepositoryURL          string                 `yaml:"repository_url,omitempty"`
	Namespace              string                 `yaml:"namespace,omitempty"`
	Configuration          map[string]interface{} `yaml:"configuration,omitempty"`
	Parents                []ParentConfig         `yaml:"parents,omitempty"`
	RegistryName           string                 `yaml:"registry_name,omitempty"`
	RegistryURL            string                 `yaml:"registry_url,omitempty"`
	RegistryCredentialName string                 `yaml:"registry_credential_name,omitempty"`
	Settings               map[string]interface{} `yaml:"settings,omitempty"`
}

type ParentConfig struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
}

func isURL(path string) bool {
	parsed, err := url.Parse(path)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// ensureSafeDownloadURL parses rawURL and rejects it if the scheme is
// `http://` against a non-loopback host, unless the operator has set
// ANKRA_ALLOW_INSECURE_HTTP=1. This mirrors the base URL validation
// applied to API credentials: the cluster-clone source is just as
// security-sensitive as the rest of the CLI's network traffic.
func ensureSafeDownloadURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse URL %q: %w", rawURL, err)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		host := parsed.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		if os.Getenv("ANKRA_ALLOW_INSECURE_HTTP") == "1" {
			return nil
		}
		return fmt.Errorf("refusing plaintext http:// download from %q; set ANKRA_ALLOW_INSECURE_HTTP=1 for loopback dev only or use https://", rawURL)
	default:
		return fmt.Errorf("unsupported URL scheme %q in %q", parsed.Scheme, rawURL)
	}
}

func downloadFile(rawURL string) ([]byte, error) {
	if err := ensureSafeDownloadURL(rawURL); err != nil {
		return nil, err
	}
	resp, err := httpDownloadClient.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download file: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteDownloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return data, nil
}

func downloadFileFromURL(baseURL, relPath, dstPath string) error {
	safeRel, err := safeRelURLPath(relPath)
	if err != nil {
		return fmt.Errorf("refusing to download %q: %w", relPath, err)
	}
	fullURL := strings.TrimRight(baseURL, "/") + "/" + safeRel

	if err := ensureSafeDownloadURL(fullURL); err != nil {
		return err
	}

	fmt.Printf("Downloading file from URL: %s\n", fullURL)

	resp, err := httpDownloadClient.Get(fullURL)
	if err != nil {
		return fmt.Errorf("failed to download file from %q: %w", fullURL, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			fmt.Printf("File %q not found at URL, skipping\n", relPath)
			return nil
		}
		return fmt.Errorf("failed to download file from %q: HTTP %d", fullURL, resp.StatusCode)
	}

	dstDir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dstDir, err)
	}

	dstFile, err := openDestinationFile(dstPath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dstFile.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close destination file: %v\n", closeErr)
		}
	}()

	if _, err := io.Copy(dstFile, io.LimitReader(resp.Body, maxRemoteDownloadBytes)); err != nil {
		return fmt.Errorf("failed to write file content: %w", err)
	}

	fmt.Printf("Downloaded file: %s\n", relPath)
	return nil
}

func cloneStacks(existing, new *ImportClusterConfig, existingBaseDir, newBaseDir string, fromURL bool) error {
	if cleanFlag {
		new.Spec.Stacks = []StackConfig{}
	}

	existingStackNames := getStackNames(new.Spec.Stacks)

	var requestedStacks map[string]bool
	if len(stackFlag) > 0 {
		requestedStacks = make(map[string]bool)
		for _, stackName := range stackFlag {
			requestedStacks[stackName] = true
		}
		fmt.Printf("Filtering to clone only specific stacks: %s\n", strings.Join(stackFlag, ", "))
	}

	stacksAdded := 0
	stacksSkipped := 0
	stacksFiltered := 0

	for _, existingStack := range existing.Spec.Stacks {
		if len(stackFlag) > 0 && !requestedStacks[existingStack.Name] {
			fmt.Printf("Filtering out stack %q - not in requested list\n", existingStack.Name)
			stacksFiltered++
			continue
		}
		if _, exists := existingStackNames[existingStack.Name]; exists && !forceFlag {
			fmt.Printf("Skipping stack %q - name already exists (use --force to override)\n", existingStack.Name)

			if copyMissingFlag {
				fmt.Printf("Copying missing files from skipped stack %q due to --copy-missing flag\n", existingStack.Name)
				if err := copyStackFiles(existingStack, existingBaseDir, newBaseDir, true, fromURL); err != nil {
					return fmt.Errorf("failed to copy missing files from stack %q: %w", existingStack.Name, err)
				}
			}

			stacksSkipped++
			continue
		}

		if !forceFlag {
			manifestConflicts, addonConflicts := checkStackConflicts(existingStack, new.Spec.Stacks)
			if len(manifestConflicts) > 0 || len(addonConflicts) > 0 {
				fmt.Printf("Skipping stack %q due to conflicts:\n", existingStack.Name)
				if len(manifestConflicts) > 0 {
					fmt.Printf("  Manifest conflicts: %s\n", strings.Join(manifestConflicts, ", "))
				}
				if len(addonConflicts) > 0 {
					fmt.Printf("  Addon conflicts: %s\n", strings.Join(addonConflicts, ", "))
				}

				if copyMissingFlag {
					fmt.Printf("Copying missing files from skipped stack %q due to --copy-missing flag\n", existingStack.Name)
					if err := copyStackFiles(existingStack, existingBaseDir, newBaseDir, true, fromURL); err != nil {
						return fmt.Errorf("failed to copy missing files from stack %q: %w", existingStack.Name, err)
					}
				}

				stacksSkipped++
				continue
			}
		}

		clonedStack := existingStack

		if err := copyStackFiles(clonedStack, existingBaseDir, newBaseDir, false, fromURL); err != nil {
			return fmt.Errorf("failed to copy files for stack %q: %w", clonedStack.Name, err)
		}

		if forceFlag && existingStackNames[existingStack.Name] {
			for i, stack := range new.Spec.Stacks {
				if stack.Name == existingStack.Name {
					new.Spec.Stacks[i] = clonedStack
					break
				}
			}
		} else {
			new.Spec.Stacks = append(new.Spec.Stacks, clonedStack)
		}

		stacksAdded++
	}

	if len(stackFlag) > 0 {
		fmt.Printf("Clone completed: %d stacks added, %d stacks skipped, %d stacks filtered out\n", stacksAdded, stacksSkipped, stacksFiltered)
	} else {
		fmt.Printf("Clone completed: %d stacks added, %d stacks skipped\n", stacksAdded, stacksSkipped)
	}
	return nil
}

func getStackNames(stacks []StackConfig) map[string]bool {
	names := make(map[string]bool)
	for _, stack := range stacks {
		names[stack.Name] = true
	}
	return names
}

func checkStackConflicts(newStack StackConfig, existingStacks []StackConfig) ([]string, []string) {
	var manifestConflicts, addonConflicts []string

	// Get all existing manifest and addon names
	existingManifests := make(map[string]bool)
	existingAddons := make(map[string]bool)

	for _, stack := range existingStacks {
		for _, manifest := range stack.Manifests {
			existingManifests[manifest.Name] = true
		}
		for _, addon := range stack.Addons {
			existingAddons[addon.Name] = true
		}
	}

	// Check for conflicts
	for _, manifest := range newStack.Manifests {
		if existingManifests[manifest.Name] {
			manifestConflicts = append(manifestConflicts, manifest.Name)
		}
	}

	for _, addon := range newStack.Addons {
		if existingAddons[addon.Name] {
			addonConflicts = append(addonConflicts, addon.Name)
		}
	}

	return manifestConflicts, addonConflicts
}

func copyStackFiles(stack StackConfig, existingBaseDir, newBaseDir string, onlyMissing bool, fromURL bool) error {
	if stack.DescriptionFromFile != "" {
		if err := transferStackFile(existingBaseDir, newBaseDir, stack.DescriptionFromFile, "stack description file", onlyMissing, fromURL); err != nil {
			return err
		}
	}

	for i := range stack.Manifests {
		fromFile := stack.Manifests[i].FromFile
		if fromFile == "" {
			continue
		}
		if err := transferStackFile(existingBaseDir, newBaseDir, fromFile, "manifest file", onlyMissing, fromURL); err != nil {
			return err
		}
	}

	for i := range stack.Addons {
		config := stack.Addons[i].Configuration
		if config == nil {
			continue
		}
		fromFile, ok := config["from_file"].(string)
		if !ok || fromFile == "" {
			continue
		}
		if err := transferStackFile(existingBaseDir, newBaseDir, fromFile, "addon configuration file", onlyMissing, fromURL); err != nil {
			return err
		}
	}

	return nil
}

// transferStackFile applies safe-path validation to the relative file
// reference and either downloads it from the remote base URL or copies it
// from the local existing cluster directory.
func transferStackFile(existingBaseDir, newBaseDir, relPath, label string, onlyMissing, fromURL bool) error {
	dstPath, err := resolveSafePath(newBaseDir, relPath)
	if err != nil {
		return fmt.Errorf("refusing to write %s %q: %w", label, relPath, err)
	}
	if onlyMissing {
		if _, err := os.Stat(dstPath); err == nil {
			return nil
		}
	}

	if fromURL {
		if err := downloadFileFromURL(existingBaseDir, relPath, dstPath); err != nil {
			return fmt.Errorf("failed to download %s %q: %w", label, relPath, err)
		}
		return nil
	}

	if err := copyFile(existingBaseDir, newBaseDir, relPath); err != nil {
		return fmt.Errorf("failed to copy %s %q: %w", label, relPath, err)
	}
	return nil
}

func copyFile(srcBaseDir, dstBaseDir, relPath string) error {
	srcPath, err := resolveSafePath(srcBaseDir, relPath)
	if err != nil {
		return fmt.Errorf("refusing to read %q: %w", relPath, err)
	}
	dstPath, err := resolveSafePath(dstBaseDir, relPath)
	if err != nil {
		return fmt.Errorf("refusing to write %q: %w", relPath, err)
	}

	dstDir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dstDir, err)
	}

	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		fmt.Printf("Source file %q does not exist, skipping\n", relPath)
		return nil
	}

	if _, err := os.Stat(dstPath); err == nil {
		if !forceFlag {
			fmt.Printf("File %q already exists, skipping copy (use --force to override)\n", relPath)
			return nil
		}
		fmt.Printf("File %q already exists, overwriting due to --force flag\n", relPath)
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source file %q: %w", srcPath, err)
	}
	defer func() {
		if closeErr := srcFile.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close source file: %v\n", closeErr)
		}
	}()

	dstFile, err := openDestinationFile(dstPath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dstFile.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close destination file: %v\n", closeErr)
		}
	}()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy file content: %w", err)
	}

	fmt.Printf("Copied file: %s\n", relPath)
	return nil
}

func generateNewClusterName(existingName string) string {
	if strings.Contains(existingName, "-cluster") {
		return strings.Replace(existingName, "-cluster", "-cloned-cluster", 1)
	}
	return existingName + "-cloned"
}

func writeClusterFile(path string, cluster *ImportClusterConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dir, err)
	}

	var buf strings.Builder
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(cluster); err != nil {
		return fmt.Errorf("failed to marshal cluster YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("failed to close YAML encoder: %w", err)
	}

	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("failed to write cluster file: %w", err)
	}

	return nil
}

func printCloneSummary(existingFile, newFile string, newClusterExists bool, existing, new *ImportClusterConfig) {
	fmt.Printf("\n" + strings.Repeat("=", 60) + "\n")
	fmt.Printf("CLONE SUMMARY\n")
	fmt.Printf(strings.Repeat("=", 60) + "\n")

	fmt.Printf("Source cluster: %s (%s)\n", existing.Metadata.Name, existingFile)
	fmt.Printf("Target cluster: %s (%s)\n", new.Metadata.Name, newFile)

	if newClusterExists {
		fmt.Printf("Target existed: Yes (merged)\n")
	} else {
		fmt.Printf("Target existed: No (created)\n")
	}

	fmt.Printf("Flags used: ")
	flags := []string{}
	if cleanFlag {
		flags = append(flags, "--clean")
	}
	if forceFlag {
		flags = append(flags, "--force")
	}
	if copyMissingFlag {
		flags = append(flags, "--copy-missing")
	}
	if len(stackFlag) > 0 {
		flags = append(flags, fmt.Sprintf("--stack=%s", strings.Join(stackFlag, ",")))
	}
	if len(flags) == 0 {
		fmt.Printf("none\n")
	} else {
		fmt.Printf("%s\n", strings.Join(flags, ", "))
	}

	fmt.Printf("Total stacks in result: %d\n", len(new.Spec.Stacks))

	totalManifests := 0
	totalAddons := 0
	for _, stack := range new.Spec.Stacks {
		totalManifests += len(stack.Manifests)
		totalAddons += len(stack.Addons)
	}

	fmt.Printf("Total manifests: %d\n", totalManifests)
	fmt.Printf("Total addons: %d\n", totalAddons)

	fmt.Printf("\nStacks in result:\n")
	for i, stack := range new.Spec.Stacks {
		fmt.Printf("  %d. %s (%d manifests, %d addons)\n",
			i+1, stack.Name, len(stack.Manifests), len(stack.Addons))
	}

	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Review the generated file: %s\n", newFile)
	fmt.Printf("  2. Apply the cluster: ankra cluster apply -f %s\n", newFile)
	fmt.Printf(strings.Repeat("=", 60) + "\n")
}

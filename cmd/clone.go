package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	cleanFlag      bool
	forceFlag      bool
	copyMissingFlag bool
)

var cloneCmd = &cobra.Command{
	Use:   "clone <existing_cluster_file_or_url> <new_cluster_path>",
	Short: "Clone stacks from an existing cluster to a new cluster configuration",
	Long: `Clone stacks from an existing cluster ImportCluster YAML to a new cluster.

The source can be either a local file path or a URL (http/https).

Examples:
  ankra clone cluster.yaml new-cluster.yaml
  ankra clone https://github.com/user/repo/raw/main/cluster.yaml new-cluster.yaml

Flags:
  --clean: Replace all stacks in the new cluster with those from the existing cluster
  --force: Force merge even when stack/addon/manifest names conflict
  --copy-missing: Copy missing files even for skipped stacks

Without flags: Merge stacks, skipping any with conflicting names`,
	Args: cobra.ExactArgs(2),
	RunE: runClone,
}

func init() {
	cloneCmd.Flags().BoolVar(&cleanFlag, "clean", false, "Replace all stacks in the new cluster")
	cloneCmd.Flags().BoolVar(&forceFlag, "force", false, "Force merge even when names conflict")
	cloneCmd.Flags().BoolVar(&copyMissingFlag, "copy-missing", false, "Copy missing files even for skipped stacks")
	rootCmd.AddCommand(cloneCmd)
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
		existingBaseDir = strings.TrimSuffix(parsedURL.String(), "/")  // Remove trailing slash
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
	Repository     string `yaml:"repository"`
}

type StackConfig struct {
	Name        string           `yaml:"name"`
	Description string           `yaml:"description,omitempty"`
	Manifests   []ManifestConfig `yaml:"manifests,omitempty"`
	Addons      []AddonConfig    `yaml:"addons,omitempty"`
}

type ManifestConfig struct {
	Name      string         `yaml:"name"`
	Parents   []ParentConfig `yaml:"parents,omitempty"`
	FromFile  string         `yaml:"from_file,omitempty"`
	Manifest  string         `yaml:"manifest,omitempty"`
	Namespace string         `yaml:"namespace,omitempty"`
}

type AddonConfig struct {
	Name              string                 `yaml:"name"`
	ChartName         string                 `yaml:"chart_name"`
	ChartVersion      string                 `yaml:"chart_version"`
	RepositoryURL     string                 `yaml:"repository_url"`
	Namespace         string                 `yaml:"namespace,omitempty"`
	ConfigurationType string                 `yaml:"configuration_type,omitempty"`
	Configuration     map[string]interface{} `yaml:"configuration,omitempty"`
	Parents           []ParentConfig         `yaml:"parents,omitempty"`
}

type ParentConfig struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
}

func isURL(path string) bool {
	parsed, err := url.Parse(path)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func downloadFile(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download file: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return data, nil
}

func downloadFileFromURL(baseURL, relPath, dstPath string) error {
	// Create the full URL for the file by joining base URL with relative path
	fullURL := baseURL + "/" + relPath

	fmt.Printf("Downloading file from URL: %s\n", fullURL)

	// Download the file
	resp, err := http.Get(fullURL)
	if err != nil {
		return fmt.Errorf("failed to download file from %q: %w", fullURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			fmt.Printf("File %q not found at URL, skipping\n", relPath)
			return nil
		}
		return fmt.Errorf("failed to download file from %q: HTTP %d", fullURL, resp.StatusCode)
	}

	// Create destination directory if it doesn't exist
	dstDir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dstDir, err)
	}

	// Create the destination file
	dstFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file %q: %w", dstPath, err)
	}
	defer dstFile.Close()

	// Copy the downloaded content to the file
	if _, err := io.Copy(dstFile, resp.Body); err != nil {
		return fmt.Errorf("failed to write file content: %w", err)
	}

	fmt.Printf("Downloaded file: %s\n", relPath)
	return nil
}

func cloneStacks(existing, new *ImportClusterConfig, existingBaseDir, newBaseDir string, fromURL bool) error {
	if cleanFlag {
		// Replace all stacks
		new.Spec.Stacks = []StackConfig{}
	}

	// Build name maps for conflict detection
	existingStackNames := getStackNames(new.Spec.Stacks)

	stacksAdded := 0
	stacksSkipped := 0

	for _, existingStack := range existing.Spec.Stacks {
		// Check for stack name conflicts
		if _, exists := existingStackNames[existingStack.Name]; exists && !forceFlag {
			fmt.Printf("Skipping stack %q - name already exists (use --force to override)\n", existingStack.Name)

			// If copy-missing flag is set, still copy missing files from this stack
			if copyMissingFlag {
				fmt.Printf("Copying missing files from skipped stack %q due to --copy-missing flag\n", existingStack.Name)
				if err := copyStackFiles(existingStack, existingBaseDir, newBaseDir, true, fromURL); err != nil {
					return fmt.Errorf("failed to copy missing files from stack %q: %w", existingStack.Name, err)
				}
			}

			stacksSkipped++
			continue
		}

		// Check for manifest/addon conflicts within the stack
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

				// If copy-missing flag is set, still copy missing files from this stack
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

		// Clone the stack
		clonedStack := existingStack

		// Copy files and update paths for manifests and addons
		if err := copyStackFiles(clonedStack, existingBaseDir, newBaseDir, false, fromURL); err != nil {
			return fmt.Errorf("failed to copy files for stack %q: %w", clonedStack.Name, err)
		}

		// Add or replace the stack
		if forceFlag && existingStackNames[existingStack.Name] {
			// Replace existing stack
			for i, stack := range new.Spec.Stacks {
				if stack.Name == existingStack.Name {
					new.Spec.Stacks[i] = clonedStack
					break
				}
			}
		} else {
			// Add new stack
			new.Spec.Stacks = append(new.Spec.Stacks, clonedStack)
		}

		stacksAdded++
	}

	fmt.Printf("Clone completed: %d stacks added, %d stacks skipped\n", stacksAdded, stacksSkipped)
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
	// Copy files for manifests
	for i := range stack.Manifests {
		if stack.Manifests[i].FromFile != "" {
			dstPath := filepath.Join(newBaseDir, stack.Manifests[i].FromFile)

			if onlyMissing {
				// Only copy if the file doesn't exist in the destination
				if _, err := os.Stat(dstPath); err == nil {
					continue // File exists, skip
				}
			}

			if fromURL {
				if err := downloadFileFromURL(existingBaseDir, stack.Manifests[i].FromFile, dstPath); err != nil {
					return fmt.Errorf("failed to download manifest file %q: %w", stack.Manifests[i].FromFile, err)
				}
			} else {
				if err := copyFile(existingBaseDir, newBaseDir, stack.Manifests[i].FromFile); err != nil {
					return fmt.Errorf("failed to copy manifest file %q: %w", stack.Manifests[i].FromFile, err)
				}
			}
		}
	}

	// Copy files for addons
	for i := range stack.Addons {
		if config := stack.Addons[i].Configuration; config != nil {
			if fromFile, ok := config["from_file"].(string); ok && fromFile != "" {
				dstPath := filepath.Join(newBaseDir, fromFile)

				if onlyMissing {
					// Only copy if the file doesn't exist in the destination
					if _, err := os.Stat(dstPath); err == nil {
						continue // File exists, skip
					}
				}

				if fromURL {
					if err := downloadFileFromURL(existingBaseDir, fromFile, dstPath); err != nil {
						return fmt.Errorf("failed to download addon configuration file %q: %w", fromFile, err)
					}
				} else {
					if err := copyFile(existingBaseDir, newBaseDir, fromFile); err != nil {
						return fmt.Errorf("failed to copy addon configuration file %q: %w", fromFile, err)
					}
				}
			}
		}
	}

	return nil
}

func copyFile(srcBaseDir, dstBaseDir, relPath string) error {
	srcPath := filepath.Join(srcBaseDir, relPath)
	dstPath := filepath.Join(dstBaseDir, relPath)

	// Create destination directory if it doesn't exist
	dstDir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dstDir, err)
	}

	// Check if source file exists
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		fmt.Printf("Source file %q does not exist, skipping\n", relPath)
		return nil
	}

	// Check if file already exists
	if _, err := os.Stat(dstPath); err == nil {
		if !forceFlag {
			fmt.Printf("File %q already exists, skipping copy (use --force to override)\n", relPath)
			return nil
		}
		fmt.Printf("File %q already exists, overwriting due to --force flag\n", relPath)
	}

	// Copy the file
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source file %q: %w", srcPath, err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file %q: %w", dstPath, err)
	}
	defer dstFile.Close()

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
	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dir, err)
	}

	data, err := yaml.Marshal(cluster)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster YAML: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
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
	fmt.Printf("  2. Apply the cluster: ankra apply -f %s\n", newFile)
	fmt.Printf(strings.Repeat("=", 60) + "\n")
}

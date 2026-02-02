package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	encryptClusterFile string
	encryptKey         string
	encryptAddonName   string
)

var clusterEncryptCmd = &cobra.Command{
	Use:   "encrypt",
	Short: "Encrypt values in manifests or addons",
	Long:  `Encrypt sensitive values in manifest or addon configuration files using SOPS.`,
}

var clusterEncryptManifestCmd = &cobra.Command{
	Use:   "manifest <manifest_name>",
	Short: "Encrypt a key in a manifest file",
	Long: `Encrypt a specific key in a manifest file referenced by the cluster configuration.

This command will:
1. Find the manifest by name in the cluster YAML
2. Read the referenced manifest file
3. Encrypt the specified key using your organisation's SOPS key
4. Update the manifest file with encrypted values
5. Add the key to encrypted_paths in the cluster YAML

Example:
  ankra cluster encrypt manifest trinity-database-secret --key TRINITY_DB_PASSWORD -f cluster.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: runEncryptManifest,
}

var clusterEncryptAddonCmd = &cobra.Command{
	Use:   "addon",
	Short: "Encrypt a key in an addon configuration file",
	Long: `Encrypt a specific key in an addon's values file referenced by the cluster configuration.

This command will:
1. Find the addon by name in the cluster YAML
2. Read the referenced configuration file (from_file)
3. Encrypt the specified key using your organisation's SOPS key
4. Update the configuration file with encrypted values
5. Add the key to encrypted_paths in the addon's configuration

Example:
  ankra cluster encrypt addon --name grafana --key adminPassword -f cluster.yaml`,
	RunE: runEncryptAddon,
}

func init() {
	// Manifest subcommand flags
	clusterEncryptManifestCmd.Flags().StringVarP(&encryptClusterFile, "file", "f", "", "Path to the cluster YAML file (required)")
	clusterEncryptManifestCmd.Flags().StringVar(&encryptKey, "key", "", "Key name to encrypt (required)")
	_ = clusterEncryptManifestCmd.MarkFlagRequired("file")
	_ = clusterEncryptManifestCmd.MarkFlagRequired("key")

	// Addon subcommand flags
	clusterEncryptAddonCmd.Flags().StringVarP(&encryptClusterFile, "file", "f", "", "Path to the cluster YAML file (required)")
	clusterEncryptAddonCmd.Flags().StringVar(&encryptKey, "key", "", "Key name to encrypt (required)")
	clusterEncryptAddonCmd.Flags().StringVar(&encryptAddonName, "name", "", "Name of the addon (required)")
	_ = clusterEncryptAddonCmd.MarkFlagRequired("file")
	_ = clusterEncryptAddonCmd.MarkFlagRequired("key")
	_ = clusterEncryptAddonCmd.MarkFlagRequired("name")

	// Register subcommands
	clusterEncryptCmd.AddCommand(clusterEncryptManifestCmd)
	clusterEncryptCmd.AddCommand(clusterEncryptAddonCmd)
	clusterCmd.AddCommand(clusterEncryptCmd)
}

func runEncryptManifest(cmd *cobra.Command, args []string) error {
	manifestName := args[0]

	// Read and parse cluster YAML
	clusterData, err := os.ReadFile(encryptClusterFile)
	if err != nil {
		return fmt.Errorf("failed to read cluster file %q: %w", encryptClusterFile, err)
	}

	var cluster ImportClusterConfig
	if err := yaml.Unmarshal(clusterData, &cluster); err != nil {
		return fmt.Errorf("failed to parse cluster YAML: %w", err)
	}

	if cluster.Kind != "ImportCluster" {
		return fmt.Errorf("file is not an ImportCluster (kind: %s)", cluster.Kind)
	}

	// Find the manifest across all stacks
	var foundManifest *ManifestConfig
	var foundStackIdx, foundManifestIdx int

	for stackIdx, stack := range cluster.Spec.Stacks {
		for manifestIdx, manifest := range stack.Manifests {
			if manifest.Name == manifestName {
				foundManifest = &cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx]
				foundStackIdx = stackIdx
				foundManifestIdx = manifestIdx
				break
			}
		}
		if foundManifest != nil {
			break
		}
	}

	if foundManifest == nil {
		return fmt.Errorf("manifest %q not found in any stack", manifestName)
	}

	if foundManifest.FromFile == "" {
		return fmt.Errorf("manifest %q does not have a from_file reference", manifestName)
	}

	// Resolve the file path relative to the cluster YAML
	clusterDir := filepath.Dir(encryptClusterFile)
	manifestFilePath := filepath.Join(clusterDir, foundManifest.FromFile)

	// Read the manifest file
	manifestContent, err := os.ReadFile(manifestFilePath)
	if err != nil {
		return fmt.Errorf("failed to read manifest file %q: %w", manifestFilePath, err)
	}

	fmt.Printf("Encrypting key %q in manifest %q...\n", encryptKey, manifestName)

	// Call the encrypt API
	encryptedContent, err := client.EncryptYAML(apiToken, baseURL, string(manifestContent), []string{encryptKey})
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	// Write the encrypted content back to the manifest file
	if err := os.WriteFile(manifestFilePath, []byte(encryptedContent), 0644); err != nil {
		return fmt.Errorf("failed to write encrypted manifest file: %w", err)
	}

	fmt.Printf("Updated manifest file: %s\n", manifestFilePath)

	// Update the cluster YAML with encrypted_paths
	if !containsString(foundManifest.EncryptedPaths, encryptKey) {
		cluster.Spec.Stacks[foundStackIdx].Manifests[foundManifestIdx].EncryptedPaths = append(
			cluster.Spec.Stacks[foundStackIdx].Manifests[foundManifestIdx].EncryptedPaths,
			encryptKey,
		)

		// Write the updated cluster YAML
		if err := writeClusterFile(encryptClusterFile, &cluster); err != nil {
			return fmt.Errorf("failed to update cluster file: %w", err)
		}

		fmt.Printf("Updated cluster file with encrypted_paths: %s\n", encryptClusterFile)
	} else {
		fmt.Printf("Key %q already in encrypted_paths, cluster file unchanged\n", encryptKey)
	}

	fmt.Println("Encryption complete!")
	return nil
}

func runEncryptAddon(cmd *cobra.Command, args []string) error {
	// Read and parse cluster YAML
	clusterData, err := os.ReadFile(encryptClusterFile)
	if err != nil {
		return fmt.Errorf("failed to read cluster file %q: %w", encryptClusterFile, err)
	}

	var cluster ImportClusterConfig
	if err := yaml.Unmarshal(clusterData, &cluster); err != nil {
		return fmt.Errorf("failed to parse cluster YAML: %w", err)
	}

	if cluster.Kind != "ImportCluster" {
		return fmt.Errorf("file is not an ImportCluster (kind: %s)", cluster.Kind)
	}

	// Find the addon across all stacks
	var foundAddon *AddonConfig
	var foundStackIdx, foundAddonIdx int

	for stackIdx, stack := range cluster.Spec.Stacks {
		for addonIdx, addon := range stack.Addons {
			if addon.Name == encryptAddonName {
				foundAddon = &cluster.Spec.Stacks[stackIdx].Addons[addonIdx]
				foundStackIdx = stackIdx
				foundAddonIdx = addonIdx
				break
			}
		}
		if foundAddon != nil {
			break
		}
	}

	if foundAddon == nil {
		return fmt.Errorf("addon %q not found in any stack", encryptAddonName)
	}

	// Get from_file from configuration
	if foundAddon.Configuration == nil {
		return fmt.Errorf("addon %q does not have a configuration section", encryptAddonName)
	}

	fromFile, ok := foundAddon.Configuration["from_file"].(string)
	if !ok || fromFile == "" {
		return fmt.Errorf("addon %q does not have a from_file configuration reference", encryptAddonName)
	}

	// Resolve the file path relative to the cluster YAML
	clusterDir := filepath.Dir(encryptClusterFile)
	addonFilePath := filepath.Join(clusterDir, fromFile)

	// Read the addon values file
	addonContent, err := os.ReadFile(addonFilePath)
	if err != nil {
		return fmt.Errorf("failed to read addon configuration file %q: %w", addonFilePath, err)
	}

	fmt.Printf("Encrypting key %q in addon %q...\n", encryptKey, encryptAddonName)

	// Call the encrypt API
	encryptedContent, err := client.EncryptYAML(apiToken, baseURL, string(addonContent), []string{encryptKey})
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	// Write the encrypted content back to the addon file
	if err := os.WriteFile(addonFilePath, []byte(encryptedContent), 0644); err != nil {
		return fmt.Errorf("failed to write encrypted addon configuration file: %w", err)
	}

	fmt.Printf("Updated addon configuration file: %s\n", addonFilePath)

	// Update the cluster YAML with encrypted_paths in the configuration
	encryptedPaths := getEncryptedPathsFromConfig(foundAddon.Configuration)
	if !containsString(encryptedPaths, encryptKey) {
		encryptedPaths = append(encryptedPaths, encryptKey)
		cluster.Spec.Stacks[foundStackIdx].Addons[foundAddonIdx].Configuration["encrypted_paths"] = encryptedPaths

		// Write the updated cluster YAML
		if err := writeClusterFile(encryptClusterFile, &cluster); err != nil {
			return fmt.Errorf("failed to update cluster file: %w", err)
		}

		fmt.Printf("Updated cluster file with encrypted_paths: %s\n", encryptClusterFile)
	} else {
		fmt.Printf("Key %q already in encrypted_paths, cluster file unchanged\n", encryptKey)
	}

	fmt.Println("Encryption complete!")
	return nil
}

func containsString(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

func getEncryptedPathsFromConfig(config map[string]interface{}) []string {
	if config == nil {
		return []string{}
	}

	encryptedPaths, ok := config["encrypted_paths"]
	if !ok {
		return []string{}
	}

	switch v := encryptedPaths.(type) {
	case []string:
		return v
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return []string{}
	}
}

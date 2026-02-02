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
	decryptClusterFile string
)

var clusterDecryptCmd = &cobra.Command{
	Use:   "decrypt",
	Short: "Decrypt values in manifests",
	Long:  `Decrypt SOPS-encrypted values in manifest files and display the decrypted content.`,
}

var clusterDecryptManifestCmd = &cobra.Command{
	Use:   "manifest <manifest_name>",
	Short: "Decrypt and display a manifest file",
	Long: `Decrypt a SOPS-encrypted manifest file and print its contents to stdout.

This command will:
1. Find the manifest by name in the cluster YAML
2. Read the referenced manifest file
3. Decrypt the content using your organisation's SOPS key
4. Print the decrypted YAML to stdout

Example:
  ankra cluster decrypt manifest trinity-database-secret -f cluster.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: runDecryptManifest,
}

func init() {
	// Manifest subcommand flags
	clusterDecryptManifestCmd.Flags().StringVarP(&decryptClusterFile, "file", "f", "", "Path to the cluster YAML file (required)")
	_ = clusterDecryptManifestCmd.MarkFlagRequired("file")

	// Register subcommands
	clusterDecryptCmd.AddCommand(clusterDecryptManifestCmd)
	clusterCmd.AddCommand(clusterDecryptCmd)
}

func runDecryptManifest(cmd *cobra.Command, args []string) error {
	manifestName := args[0]

	// Read and parse cluster YAML
	clusterData, err := os.ReadFile(decryptClusterFile)
	if err != nil {
		return fmt.Errorf("failed to read cluster file %q: %w", decryptClusterFile, err)
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

	for stackIdx := range cluster.Spec.Stacks {
		for manifestIdx := range cluster.Spec.Stacks[stackIdx].Manifests {
			if cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx].Name == manifestName {
				foundManifest = &cluster.Spec.Stacks[stackIdx].Manifests[manifestIdx]
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
	clusterDir := filepath.Dir(decryptClusterFile)
	manifestFilePath := filepath.Join(clusterDir, foundManifest.FromFile)

	// Read the manifest file
	manifestContent, err := os.ReadFile(manifestFilePath)
	if err != nil {
		return fmt.Errorf("failed to read manifest file %q: %w", manifestFilePath, err)
	}

	// Call the decrypt API
	decryptedContent, err := client.DecryptYAML(apiToken, baseURL, string(manifestContent))
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}

	// Print the decrypted content to stdout
	fmt.Print(decryptedContent)
	return nil
}

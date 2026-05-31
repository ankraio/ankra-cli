package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	decryptClusterFile string
	decryptClusterFlag string
	decryptStackFlag   string
	decryptAddonName   string
)

var clusterDecryptCmd = &cobra.Command{
	Use:   "decrypt",
	Short: "Decrypt SOPS-encrypted values in manifests or addons",
	Long:  `Decrypt SOPS-encrypted values stored on a cluster or in a local cluster.yaml.`,
}

var clusterDecryptManifestCmd = &cobra.Command{
	Use:   "manifest <manifest_name>",
	Short: "Decrypt and print a manifest",
	Long: `Decrypt a SOPS-encrypted manifest and print the result to stdout.

Two modes:
  Cluster mode (default): fetch the manifest from a live cluster, decrypt it,
    and print to stdout.

  File mode (-f cluster.yaml): read the manifest file referenced from a local
    cluster.yaml, decrypt it, and print to stdout.

Examples:
  # Cluster mode against the selected cluster
  ankra cluster decrypt manifest db-secret

  # Cluster mode against a specific cluster
  ankra cluster decrypt manifest db-secret --cluster prod

  # File mode
  ankra cluster decrypt manifest db-secret -f cluster.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: runDecryptManifest,
}

var clusterDecryptAddonCmd = &cobra.Command{
	Use:   "addon",
	Short: "Decrypt and print an addon's values",
	Long: `Decrypt a SOPS-encrypted addon's Helm values and print the result to stdout.

Two modes:
  Cluster mode (default): fetch the addon values from a live cluster, decrypt,
    and print to stdout.

  File mode (-f cluster.yaml): read the addon values file referenced from a
    local cluster.yaml, decrypt, and print to stdout.

Examples:
  ankra cluster decrypt addon --name grafana
  ankra cluster decrypt addon --name grafana --cluster prod --stack monitoring
  ankra cluster decrypt addon --name grafana -f cluster.yaml`,
	RunE: runDecryptAddon,
}

func init() {
	clusterDecryptManifestCmd.Flags().StringVarP(&decryptClusterFile, "file", "f", "", "Path to a local cluster YAML (enables file mode)")
	clusterDecryptManifestCmd.Flags().StringVar(&decryptClusterFlag, "cluster", "", "Target cluster (name or ID); defaults to the active selection (cluster mode)")
	clusterDecryptManifestCmd.MarkFlagsMutuallyExclusive("file", "cluster")

	clusterDecryptAddonCmd.Flags().StringVarP(&decryptClusterFile, "file", "f", "", "Path to a local cluster YAML (enables file mode)")
	clusterDecryptAddonCmd.Flags().StringVar(&decryptAddonName, "name", "", "Name of the addon (required)")
	clusterDecryptAddonCmd.Flags().StringVar(&decryptClusterFlag, "cluster", "", "Target cluster (name or ID); defaults to the active selection (cluster mode)")
	clusterDecryptAddonCmd.Flags().StringVar(&decryptStackFlag, "stack", "", "Stack name (cluster mode; required when the addon exists in multiple stacks)")
	_ = clusterDecryptAddonCmd.MarkFlagRequired("name")
	clusterDecryptAddonCmd.MarkFlagsMutuallyExclusive("file", "cluster")
	clusterDecryptAddonCmd.MarkFlagsMutuallyExclusive("file", "stack")

	clusterDecryptCmd.AddCommand(clusterDecryptManifestCmd)
	clusterDecryptCmd.AddCommand(clusterDecryptAddonCmd)
	clusterCmd.AddCommand(clusterDecryptCmd)
}

func runDecryptManifest(cmd *cobra.Command, args []string) error {
	manifestName := args[0]
	if decryptClusterFile == "" {
		return runDecryptManifestCluster(cmd, manifestName)
	}
	return runDecryptManifestFile(cmd, manifestName)
}

func runDecryptManifestCluster(cmd *cobra.Command, manifestName string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()

	clusterID, _, _, err := fetchClusterIaCDoc(ctx, decryptClusterFlag)
	if err != nil {
		return err
	}

	encoded, err := apiClient.GetClusterManifestConfiguration(ctx, clusterID, manifestName)
	if err != nil {
		return fmt.Errorf("fetch manifest content: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("base64-decode manifest content: %w", err)
	}

	decryptedContent, err := apiClient.DecryptYAML(string(decoded))
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	fmt.Fprint(cmd.OutOrStdout(), decryptedContent)
	if len(decryptedContent) > 0 && decryptedContent[len(decryptedContent)-1] != '\n' {
		fmt.Fprintln(cmd.OutOrStdout())
	}
	return nil
}

func runDecryptManifestFile(cmd *cobra.Command, manifestName string) error {
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

	clusterDir := filepath.Dir(decryptClusterFile)
	manifestFilePath, err := resolveSafePath(clusterDir, foundManifest.FromFile)
	if err != nil {
		return fmt.Errorf("refusing to access manifest %q: %w", foundManifest.FromFile, err)
	}

	manifestContent, err := os.ReadFile(manifestFilePath)
	if err != nil {
		return fmt.Errorf("failed to read manifest file %q: %w", manifestFilePath, err)
	}

	decryptedContent, err := apiClient.DecryptYAML(string(manifestContent))
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}

	fmt.Fprint(cmd.OutOrStdout(), decryptedContent)
	return nil
}

func runDecryptAddon(cmd *cobra.Command, args []string) error {
	if decryptClusterFile == "" {
		return runDecryptAddonCluster(cmd, decryptAddonName)
	}
	return runDecryptAddonFile(cmd, decryptAddonName)
}

func runDecryptAddonCluster(cmd *cobra.Command, addonName string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()

	clusterID, _, _, err := fetchClusterIaCDoc(ctx, decryptClusterFlag)
	if err != nil {
		return err
	}

	currentValues, err := apiClient.GetClusterAddonValues(ctx, clusterID, addonName)
	if err != nil {
		return fmt.Errorf("fetch addon values: %w", err)
	}

	decryptedContent, err := apiClient.DecryptYAML(currentValues)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	fmt.Fprint(cmd.OutOrStdout(), decryptedContent)
	if len(decryptedContent) > 0 && decryptedContent[len(decryptedContent)-1] != '\n' {
		fmt.Fprintln(cmd.OutOrStdout())
	}
	return nil
}

func runDecryptAddonFile(cmd *cobra.Command, addonName string) error {
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

	var foundAddon *AddonConfig
	for stackIdx := range cluster.Spec.Stacks {
		for addonIdx := range cluster.Spec.Stacks[stackIdx].Addons {
			if cluster.Spec.Stacks[stackIdx].Addons[addonIdx].Name == addonName {
				foundAddon = &cluster.Spec.Stacks[stackIdx].Addons[addonIdx]
				break
			}
		}
		if foundAddon != nil {
			break
		}
	}
	if foundAddon == nil {
		return fmt.Errorf("addon %q not found in any stack", addonName)
	}
	if foundAddon.Configuration == nil {
		return fmt.Errorf("addon %q does not have a configuration section", addonName)
	}
	fromFile, ok := foundAddon.Configuration["from_file"].(string)
	if !ok || fromFile == "" {
		return fmt.Errorf("addon %q does not have a from_file configuration reference", addonName)
	}

	clusterDir := filepath.Dir(decryptClusterFile)
	addonFilePath, err := resolveSafePath(clusterDir, fromFile)
	if err != nil {
		return fmt.Errorf("refusing to access addon configuration %q: %w", fromFile, err)
	}
	addonContent, err := os.ReadFile(addonFilePath)
	if err != nil {
		return fmt.Errorf("failed to read addon configuration file %q: %w", addonFilePath, err)
	}

	decryptedContent, err := apiClient.DecryptYAML(string(addonContent))
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	fmt.Fprint(cmd.OutOrStdout(), decryptedContent)
	return nil
}

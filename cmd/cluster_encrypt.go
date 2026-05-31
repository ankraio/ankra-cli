package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var clusterSopsConfigCmd = &cobra.Command{
	Use:   "sops-config",
	Short: "Display SOPS configuration for the organisation",
	Long:  "Show the SOPS encryption configuration including the public key used for encrypting secrets.",
	Run: func(cmd *cobra.Command, args []string) {
		config, err := apiClient.GetSopsConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching SOPS config: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("SOPS Configuration:\n")
		fmt.Printf("  Enabled:     %t\n", config.Enabled)
		fmt.Printf("  Initialized: %t\n", config.Initialized)
		if config.AgePublicKey != "" {
			fmt.Printf("  Public Key:  %s\n", config.AgePublicKey)
		} else {
			fmt.Printf("  Public Key:  not configured\n")
		}
	},
}

var (
	encryptClusterFile string
	encryptKey         string
	encryptAddonName   string
	encryptClusterFlag string
	encryptStackFlag   string
)

var clusterEncryptCmd = &cobra.Command{
	Use:   "encrypt",
	Short: "Encrypt values in manifests or addons",
	Long:  `Encrypt sensitive values in manifest or addon configuration files using SOPS.`,
}

var clusterEncryptManifestCmd = &cobra.Command{
	Use:   "manifest <manifest_name>",
	Short: "Encrypt a key in a manifest",
	Long: `Encrypt a specific key in a manifest using SOPS.

Two modes:
  Cluster mode (default): fetch the manifest from a live cluster, encrypt the
    key, and push the result back via the partial-stack PATCH endpoint. The
    owning stack is resolved automatically.

  File mode (-f cluster.yaml): rewrite a local cluster.yaml's referenced
    from_file in place, adding the key to encrypted_paths in the file. Used by
    GitOps workflows where the source of truth is on disk.

Examples:
  # Cluster mode against the selected cluster
  ankra cluster encrypt manifest db-secret --key data.password

  # Cluster mode against a specific cluster
  ankra cluster encrypt manifest db-secret --key data.password --cluster prod

  # File mode
  ankra cluster encrypt manifest db-secret --key data.password -f cluster.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: runEncryptManifest,
}

var clusterEncryptAddonCmd = &cobra.Command{
	Use:   "addon",
	Short: "Encrypt a key in an addon's values",
	Long: `Encrypt a specific key in an addon's Helm values using SOPS.

Two modes:
  Cluster mode (default): fetch the addon's values from a live cluster,
    encrypt the key, and push the result back via the partial-stack PATCH
    endpoint. The owning stack is resolved automatically.

  File mode (-f cluster.yaml): rewrite the local addon values file referenced
    by the cluster.yaml in place, adding the key to encrypted_paths.

Examples:
  # Cluster mode against the selected cluster
  ankra cluster encrypt addon --name grafana --key adminPassword

  # Cluster mode against a specific cluster, disambiguating stack
  ankra cluster encrypt addon --name grafana --key adminPassword --cluster prod --stack monitoring

  # File mode
  ankra cluster encrypt addon --name grafana --key adminPassword -f cluster.yaml`,
	RunE: runEncryptAddon,
}

func init() {
	clusterEncryptManifestCmd.Flags().StringVarP(&encryptClusterFile, "file", "f", "", "Path to a local cluster YAML (enables file mode)")
	clusterEncryptManifestCmd.Flags().StringVar(&encryptKey, "key", "", "Key name to encrypt (required)")
	clusterEncryptManifestCmd.Flags().StringVar(&encryptClusterFlag, "cluster", "", "Target cluster (name or ID); defaults to the active selection (cluster mode)")
	_ = clusterEncryptManifestCmd.MarkFlagRequired("key")
	clusterEncryptManifestCmd.MarkFlagsMutuallyExclusive("file", "cluster")

	clusterEncryptAddonCmd.Flags().StringVarP(&encryptClusterFile, "file", "f", "", "Path to a local cluster YAML (enables file mode)")
	clusterEncryptAddonCmd.Flags().StringVar(&encryptKey, "key", "", "Key name to encrypt (required)")
	clusterEncryptAddonCmd.Flags().StringVar(&encryptAddonName, "name", "", "Name of the addon (required)")
	clusterEncryptAddonCmd.Flags().StringVar(&encryptClusterFlag, "cluster", "", "Target cluster (name or ID); defaults to the active selection (cluster mode)")
	clusterEncryptAddonCmd.Flags().StringVar(&encryptStackFlag, "stack", "", "Stack name (cluster mode; required when the addon exists in multiple stacks)")
	_ = clusterEncryptAddonCmd.MarkFlagRequired("key")
	_ = clusterEncryptAddonCmd.MarkFlagRequired("name")
	clusterEncryptAddonCmd.MarkFlagsMutuallyExclusive("file", "cluster")
	clusterEncryptAddonCmd.MarkFlagsMutuallyExclusive("file", "stack")

	clusterEncryptCmd.AddCommand(clusterEncryptManifestCmd)
	clusterEncryptCmd.AddCommand(clusterEncryptAddonCmd)
	clusterCmd.AddCommand(clusterEncryptCmd)
	clusterCmd.AddCommand(clusterSopsConfigCmd)
}

func runEncryptManifest(cmd *cobra.Command, args []string) error {
	manifestName := args[0]
	if encryptClusterFile == "" {
		return runEncryptManifestCluster(cmd, manifestName)
	}
	return runEncryptManifestFile(cmd, manifestName)
}

func runEncryptManifestFile(cmd *cobra.Command, manifestName string) error {
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

	clusterDir := filepath.Dir(encryptClusterFile)
	manifestFilePath, err := resolveSafePath(clusterDir, foundManifest.FromFile)
	if err != nil {
		return fmt.Errorf("refusing to access manifest %q: %w", foundManifest.FromFile, err)
	}

	manifestContent, err := os.ReadFile(manifestFilePath)
	if err != nil {
		return fmt.Errorf("failed to read manifest file %q: %w", manifestFilePath, err)
	}

	fmt.Printf("Encrypting key %q in manifest %q...\n", encryptKey, manifestName)

	encryptedContent, err := apiClient.EncryptYAML(string(manifestContent), []string{encryptKey})
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	if err := os.WriteFile(manifestFilePath, []byte(encryptedContent), 0o644); err != nil {
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
	if encryptClusterFile == "" {
		return runEncryptAddonCluster(cmd, encryptAddonName)
	}
	return runEncryptAddonFile(cmd)
}

func runEncryptAddonFile(cmd *cobra.Command) error {
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

	clusterDir := filepath.Dir(encryptClusterFile)
	addonFilePath, err := resolveSafePath(clusterDir, fromFile)
	if err != nil {
		return fmt.Errorf("refusing to access addon configuration %q: %w", fromFile, err)
	}

	addonContent, err := os.ReadFile(addonFilePath)
	if err != nil {
		return fmt.Errorf("failed to read addon configuration file %q: %w", addonFilePath, err)
	}

	fmt.Printf("Encrypting key %q in addon %q...\n", encryptKey, encryptAddonName)

	encryptedContent, err := apiClient.EncryptYAML(string(addonContent), []string{encryptKey})
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	if err := os.WriteFile(addonFilePath, []byte(encryptedContent), 0o644); err != nil {
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

// fetchClusterIaCDoc resolves the target cluster, fetches its IaC, and parses
// it into an ImportClusterDoc ready for findAddonInIaC / findManifestInIaC.
// Shared by the cluster-bound SOPS flows so they have the same resolution
// semantics as `manifests upgrade` / `addons upgrade`.
func fetchClusterIaCDoc(ctx context.Context, clusterFlag string) (clusterID, clusterName string, doc *ImportClusterDoc, err error) {
	clusterID, clusterName, err = resolveClusterForCmd(clusterFlag)
	if err != nil {
		return "", "", nil, err
	}
	iacYAML, err := apiClient.GetClusterIaC(ctx, clusterID)
	if err != nil {
		if errors.Is(err, client.ErrClusterEmpty) {
			return "", "", nil, fmt.Errorf("no resources on cluster %q; nothing to encrypt/decrypt", clusterName)
		}
		return "", "", nil, fmt.Errorf("fetch cluster IaC: %w", err)
	}
	doc, err = parseImportClusterYAML([]byte(iacYAML))
	if err != nil {
		return "", "", nil, err
	}
	return clusterID, clusterName, doc, nil
}

func runEncryptManifestCluster(cmd *cobra.Command, manifestName string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()

	clusterID, _, doc, err := fetchClusterIaCDoc(ctx, encryptClusterFlag)
	if err != nil {
		return err
	}
	stack, manifest, err := findManifestInIaC(doc, manifestName)
	if err != nil {
		return err
	}

	encoded, err := apiClient.GetClusterManifestConfiguration(ctx, clusterID, manifestName)
	if err != nil {
		return fmt.Errorf("fetch current manifest content: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("base64-decode manifest content: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Encrypting key %q in manifest %q (stack %q)...\n", encryptKey, manifestName, stack.Name)
	encryptedYAML, err := apiClient.EncryptYAML(string(decoded), []string{encryptKey})
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	newPaths := append([]string{}, manifest.EncryptedPaths...)
	if !containsString(newPaths, encryptKey) {
		newPaths = append(newPaths, encryptKey)
	}

	mutated := *manifest
	mutated.FromFile = ""
	mutated.ManifestBase64 = base64.StdEncoding.EncodeToString([]byte(encryptedYAML))
	mutated.EncryptedPaths = newPaths

	patchStack := copyStackMetadata(stack)
	patchStack.Manifests = []client.ManifestSpec{mutated}
	req := buildPartialStackPatch(patchStack)

	res, err := apiClient.PatchClusterStackPartial(ctx, clusterID, stack.Name, req)
	if err != nil {
		var perr *client.PatchStackError
		if errors.As(err, &perr) {
			return mapPatchError(perr)
		}
		return err
	}
	if len(res.Errors) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "Encryption completed with resource errors:")
		for _, e := range res.Errors {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %s %s [%s]: %s\n", e.Kind, e.Name, e.Key, e.Message)
		}
		return errors.New("encryption partially failed; see errors above")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Manifest %q encrypted in stack %q.\n", manifestName, stack.Name)
	return nil
}

func runEncryptAddonCluster(cmd *cobra.Command, addonName string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()

	clusterID, _, doc, err := fetchClusterIaCDoc(ctx, encryptClusterFlag)
	if err != nil {
		return err
	}
	stack, addon, err := findAddonInIaC(doc, addonName, encryptStackFlag)
	if err != nil {
		return err
	}

	currentValues, err := apiClient.GetClusterAddonValues(ctx, clusterID, addonName)
	if err != nil {
		return fmt.Errorf("fetch current addon values: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Encrypting key %q in addon %q (stack %q)...\n", encryptKey, addonName, stack.Name)
	encryptedYAML, err := apiClient.EncryptYAML(currentValues, []string{encryptKey})
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	existingPaths := []string{}
	if addon.Configuration != nil {
		existingPaths = addon.Configuration.EncryptedPaths
	}
	newPaths := append([]string{}, existingPaths...)
	if !containsString(newPaths, encryptKey) {
		newPaths = append(newPaths, encryptKey)
	}

	mutatedAddon := *addon
	mutatedAddon.Configuration = &client.AddonConfigurationSpec{
		ValuesBase64:   base64.StdEncoding.EncodeToString([]byte(encryptedYAML)),
		EncryptedPaths: newPaths,
	}

	patchStack := copyStackMetadata(stack)
	patchStack.Addons = []client.AddonSpec{mutatedAddon}
	req := buildPartialStackPatch(patchStack)

	res, err := apiClient.PatchClusterStackPartial(ctx, clusterID, stack.Name, req)
	if err != nil {
		var perr *client.PatchStackError
		if errors.As(err, &perr) {
			return mapPatchError(perr)
		}
		return err
	}
	if len(res.Errors) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "Encryption completed with resource errors:")
		for _, e := range res.Errors {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %s %s [%s]: %s\n", e.Kind, e.Name, e.Key, e.Message)
		}
		return errors.New("encryption partially failed; see errors above")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Addon %q encrypted in stack %q.\n", addonName, stack.Name)
	return nil
}

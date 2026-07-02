package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	sopsMetadataKey          = "sops"
	sopsEncryptedValuePrefix = "ENC["
)

var clusterSopsConfigCmd = &cobra.Command{
	Use:   "sops-config",
	Short: "Display SOPS configuration for the organisation",
	Long:  "Show the SOPS encryption configuration including the public key used for encrypting secrets.",
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := apiClient.GetSopsConfig()
		if err != nil {
			return fmt.Errorf("fetching SOPS config: %w", err)
		}

		fmt.Printf("SOPS Configuration:\n")
		fmt.Printf("  Enabled:     %t\n", config.Enabled)
		fmt.Printf("  Initialized: %t\n", config.Initialized)
		if config.AgePublicKey != "" {
			fmt.Printf("  Public Key:  %s\n", config.AgePublicKey)
		} else {
			fmt.Printf("  Public Key:  not configured\n")
		}
		return nil
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

--key takes the YAML key name whose values should be encrypted (for a Secret's
data.password, that is "password"). SOPS matches key names anywhere in the
document, not dotted paths; a dotted --key is normalised to its last segment.
A key whose own name starts with a dot (such as ".dockerconfigjson" in a
kubernetes.io/dockerconfigjson Secret) is kept literally. After encrypting, the
CLI verifies the value is actually ENC[...] ciphertext and fails if it is not.

Two modes:
  Cluster mode (default): fetch the manifest from a live cluster, encrypt the
    key, and push the result back via the partial-stack PATCH endpoint. The
    owning stack is resolved automatically.

  File mode (-f cluster.yaml): rewrite a local cluster.yaml's referenced
    from_file in place, adding the key to encrypted_paths in the file. Used by
    GitOps workflows where the source of truth is on disk.

Examples:
  # Cluster mode against the selected cluster
  ankra cluster encrypt manifest db-secret --key password

  # Cluster mode against a specific cluster
  ankra cluster encrypt manifest db-secret --key password --cluster prod

  # File mode
  ankra cluster encrypt manifest db-secret --key password -f cluster.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: runEncryptManifest,
}

var clusterEncryptAddonCmd = &cobra.Command{
	Use:   "addon",
	Short: "Encrypt a key in an addon's values",
	Long: `Encrypt a specific key in an addon's Helm values using SOPS.

--key takes the YAML key name whose values should be encrypted. SOPS matches
key names anywhere in the document, not dotted paths; a dotted --key is
normalised to its last segment. A key whose own name starts with a dot (such
as ".dockerconfigjson") is kept literally. After encrypting, the CLI verifies
the value is actually ENC[...] ciphertext and fails if it is not.

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
	clusterEncryptManifestCmd.Flags().StringVar(&encryptKey, "key", "", "YAML key name to encrypt (required); dotted paths are normalised to the last segment, a leading-dot key like .dockerconfigjson is kept literally")
	clusterEncryptManifestCmd.Flags().StringVar(&encryptClusterFlag, "cluster", "", "Target cluster (name or ID); defaults to the active selection (cluster mode)")
	_ = clusterEncryptManifestCmd.MarkFlagRequired("key")
	clusterEncryptManifestCmd.MarkFlagsMutuallyExclusive("file", "cluster")

	clusterEncryptAddonCmd.Flags().StringVarP(&encryptClusterFile, "file", "f", "", "Path to a local cluster YAML (enables file mode)")
	clusterEncryptAddonCmd.Flags().StringVar(&encryptKey, "key", "", "YAML key name to encrypt (required); dotted paths are normalised to the last segment, a leading-dot key like .dockerconfigjson is kept literally")
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

// normalizeEncryptKey maps the user-facing --key value onto the YAML key name
// SOPS will actually match. SOPS' encrypted_regex is applied to individual key
// names during tree traversal, never to dotted paths: --key data.password must
// become "password", otherwise SOPS encrypts nothing while still writing full
// sops metadata, leaving a file that looks encrypted but is plaintext.
//
// A leading dot marks a literal key whose own name contains a dot, such as the
// ".dockerconfigjson" key in a kubernetes.io/dockerconfigjson Secret. Those are
// kept verbatim instead of being split on the dot.
func normalizeEncryptKey(rawKey string) (string, error) {
	trimmedKey := strings.TrimSpace(rawKey)
	if trimmedKey == "" {
		return "", fmt.Errorf("--key must not be empty")
	}
	if strings.HasPrefix(trimmedKey, ".") {
		if trimmedKey == "." {
			return "", fmt.Errorf("invalid --key %q: empty key name", rawKey)
		}
		return trimmedKey, nil
	}
	segments := strings.Split(trimmedKey, ".")
	leafKey := segments[len(segments)-1]
	if leafKey == "" {
		return "", fmt.Errorf("invalid --key %q: empty key name after the last dot", rawKey)
	}
	return leafKey, nil
}

func announceEncryptKeyNormalization(cmd *cobra.Command, rawKey, leafKey string) {
	if leafKey == strings.TrimSpace(rawKey) {
		return
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"Note: SOPS matches YAML key names, not dotted paths; encrypting key %q (derived from --key %q).\n",
		leafKey, rawKey)
}

// verifyKeyEncrypted guards against SOPS "succeeding" without encrypting
// anything: it checks that the target key exists in the encrypted output and
// that every value under it is ENC[...] ciphertext. Without this guard a bad
// key silently produces a file with sops metadata and plaintext values.
func verifyKeyEncrypted(encryptedYAML, keyName string) error {
	decoder := yaml.NewDecoder(strings.NewReader(encryptedYAML))
	matchedKeyCount := 0
	plaintextValueCount := 0
	for {
		var document yaml.Node
		decodeErr := decoder.Decode(&document)
		if errors.Is(decodeErr, io.EOF) {
			break
		}
		if decodeErr != nil {
			return fmt.Errorf("parse encrypted YAML: %w", decodeErr)
		}
		rootNode := &document
		if rootNode.Kind == yaml.DocumentNode && len(rootNode.Content) > 0 {
			rootNode = rootNode.Content[0]
		}
		inspectEncryptedKey(rootNode, keyName, true, &matchedKeyCount, &plaintextValueCount)
	}
	if matchedKeyCount == 0 {
		return fmt.Errorf(
			"no YAML key named %q exists in the encrypted output, so SOPS encrypted nothing while still writing sops metadata; "+
				"pass the key name itself (for data.password use --key password)",
			keyName)
	}
	if plaintextValueCount > 0 {
		return fmt.Errorf(
			"value under key %q is still plaintext after encryption; refusing to write a file that only looks encrypted",
			keyName)
	}
	return nil
}

func inspectEncryptedKey(node *yaml.Node, keyName string, isDocumentRoot bool, matchedKeyCount, plaintextValueCount *int) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for entryIndex := 0; entryIndex+1 < len(node.Content); entryIndex += 2 {
			keyNode := node.Content[entryIndex]
			valueNode := node.Content[entryIndex+1]
			if isDocumentRoot && keyNode.Value == sopsMetadataKey {
				continue
			}
			if keyNode.Value == keyName {
				*matchedKeyCount++
				if !isSubtreeEncrypted(valueNode) {
					*plaintextValueCount++
				}
				continue
			}
			inspectEncryptedKey(valueNode, keyName, false, matchedKeyCount, plaintextValueCount)
		}
	case yaml.SequenceNode:
		for _, itemNode := range node.Content {
			inspectEncryptedKey(itemNode, keyName, false, matchedKeyCount, plaintextValueCount)
		}
	}
}

func isSubtreeEncrypted(node *yaml.Node) bool {
	if node == nil {
		return true
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == "!!null" {
			return true
		}
		return strings.HasPrefix(node.Value, sopsEncryptedValuePrefix)
	case yaml.MappingNode:
		for valueIndex := 1; valueIndex < len(node.Content); valueIndex += 2 {
			if !isSubtreeEncrypted(node.Content[valueIndex]) {
				return false
			}
		}
		return true
	case yaml.SequenceNode:
		for _, itemNode := range node.Content {
			if !isSubtreeEncrypted(itemNode) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

func runEncryptManifest(cmd *cobra.Command, args []string) error {
	manifestName := args[0]
	leafKey, err := normalizeEncryptKey(encryptKey)
	if err != nil {
		return err
	}
	announceEncryptKeyNormalization(cmd, encryptKey, leafKey)
	if encryptClusterFile == "" {
		return runEncryptManifestCluster(cmd, manifestName, leafKey)
	}
	return runEncryptManifestFile(cmd, manifestName, leafKey)
}

func runEncryptManifestFile(cmd *cobra.Command, manifestName, leafKey string) error {
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

	fmt.Printf("Encrypting key %q in manifest %q...\n", leafKey, manifestName)

	encryptedContent, err := apiClient.EncryptYAML(string(manifestContent), []string{leafKey})
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	if err := verifyKeyEncrypted(encryptedContent, leafKey); err != nil {
		return fmt.Errorf("encryption verification failed: %w", err)
	}

	if err := os.WriteFile(manifestFilePath, []byte(encryptedContent), 0o644); err != nil {
		return fmt.Errorf("failed to write encrypted manifest file: %w", err)
	}

	fmt.Printf("Updated manifest file: %s\n", manifestFilePath)

	// Update the cluster YAML with encrypted_paths
	if !containsString(foundManifest.EncryptedPaths, leafKey) {
		cluster.Spec.Stacks[foundStackIdx].Manifests[foundManifestIdx].EncryptedPaths = append(
			cluster.Spec.Stacks[foundStackIdx].Manifests[foundManifestIdx].EncryptedPaths,
			leafKey,
		)

		// Write the updated cluster YAML
		if err := writeClusterFile(encryptClusterFile, &cluster); err != nil {
			return fmt.Errorf("failed to update cluster file: %w", err)
		}

		fmt.Printf("Updated cluster file with encrypted_paths: %s\n", encryptClusterFile)
	} else {
		fmt.Printf("Key %q already in encrypted_paths, cluster file unchanged\n", leafKey)
	}

	fmt.Println("Encryption complete!")
	return nil
}

func runEncryptAddon(cmd *cobra.Command, args []string) error {
	leafKey, err := normalizeEncryptKey(encryptKey)
	if err != nil {
		return err
	}
	announceEncryptKeyNormalization(cmd, encryptKey, leafKey)
	if encryptClusterFile == "" {
		return runEncryptAddonCluster(cmd, encryptAddonName, leafKey)
	}
	return runEncryptAddonFile(cmd, leafKey)
}

func runEncryptAddonFile(cmd *cobra.Command, leafKey string) error {
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

	fmt.Printf("Encrypting key %q in addon %q...\n", leafKey, encryptAddonName)

	encryptedContent, err := apiClient.EncryptYAML(string(addonContent), []string{leafKey})
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	if err := verifyKeyEncrypted(encryptedContent, leafKey); err != nil {
		return fmt.Errorf("encryption verification failed: %w", err)
	}

	if err := os.WriteFile(addonFilePath, []byte(encryptedContent), 0o644); err != nil {
		return fmt.Errorf("failed to write encrypted addon configuration file: %w", err)
	}

	fmt.Printf("Updated addon configuration file: %s\n", addonFilePath)

	// Update the cluster YAML with encrypted_paths in the configuration
	encryptedPaths := getEncryptedPathsFromConfig(foundAddon.Configuration)
	if !containsString(encryptedPaths, leafKey) {
		encryptedPaths = append(encryptedPaths, leafKey)
		cluster.Spec.Stacks[foundStackIdx].Addons[foundAddonIdx].Configuration["encrypted_paths"] = encryptedPaths

		// Write the updated cluster YAML
		if err := writeClusterFile(encryptClusterFile, &cluster); err != nil {
			return fmt.Errorf("failed to update cluster file: %w", err)
		}

		fmt.Printf("Updated cluster file with encrypted_paths: %s\n", encryptClusterFile)
	} else {
		fmt.Printf("Key %q already in encrypted_paths, cluster file unchanged\n", leafKey)
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

func runEncryptManifestCluster(cmd *cobra.Command, manifestName, leafKey string) error {
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

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Encrypting key %q in manifest %q (stack %q)...\n", leafKey, manifestName, stack.Name)
	encryptedYAML, err := apiClient.EncryptYAML(string(decoded), []string{leafKey})
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	if err := verifyKeyEncrypted(encryptedYAML, leafKey); err != nil {
		return fmt.Errorf("encryption verification failed: %w", err)
	}

	newPaths := append([]string{}, manifest.EncryptedPaths...)
	if !containsString(newPaths, leafKey) {
		newPaths = append(newPaths, leafKey)
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
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Encryption completed with resource errors:")
		for _, e := range res.Errors {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  - %s %s [%s]: %s\n", e.Kind, e.Name, e.Key, e.Message)
		}
		return errors.New("encryption partially failed; see errors above")
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Manifest %q encrypted in stack %q.\n", manifestName, stack.Name)
	return nil
}

func runEncryptAddonCluster(cmd *cobra.Command, addonName, leafKey string) error {
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

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Encrypting key %q in addon %q (stack %q)...\n", leafKey, addonName, stack.Name)
	encryptedYAML, err := apiClient.EncryptYAML(currentValues, []string{leafKey})
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	if err := verifyKeyEncrypted(encryptedYAML, leafKey); err != nil {
		return fmt.Errorf("encryption verification failed: %w", err)
	}

	existingPaths := []string{}
	if addon.Configuration != nil {
		existingPaths = addon.Configuration.EncryptedPaths
	}
	newPaths := append([]string{}, existingPaths...)
	if !containsString(newPaths, leafKey) {
		newPaths = append(newPaths, leafKey)
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
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Encryption completed with resource errors:")
		for _, e := range res.Errors {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  - %s %s [%s]: %s\n", e.Kind, e.Name, e.Key, e.Message)
		}
		return errors.New("encryption partially failed; see errors above")
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Addon %q encrypted in stack %q.\n", addonName, stack.Name)
	return nil
}

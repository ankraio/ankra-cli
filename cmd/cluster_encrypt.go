package cmd

import (
	"bytes"
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
	encryptKeys        []string
	encryptAllData     bool
	encryptAddonName   string
	encryptClusterFlag string
	encryptStackFlag   string
	encryptSetEntries  []string
)

var clusterEncryptCmd = &cobra.Command{
	Use:   "encrypt",
	Short: "Encrypt values in manifests or addons",
	Long:  `Encrypt sensitive values in manifest or addon configuration files using SOPS.`,
}

var clusterEncryptManifestCmd = &cobra.Command{
	Use:   "manifest <manifest_name>",
	Short: "Encrypt one or more keys in a manifest",
	Long: `Encrypt one or more keys in a manifest using SOPS.

--key takes the YAML key name whose values should be encrypted (for a Secret's
data.password, that is "password"). Repeat --key to encrypt several keys in a
single run; all keys are encrypted in one SOPS pass and one write. SOPS matches
key names anywhere in the document, not dotted paths; a dotted --key is
normalised to its last segment. A key whose own name starts with a dot (such as
".dockerconfigjson" in a kubernetes.io/dockerconfigjson Secret) is kept
literally. After encrypting, the CLI verifies every value is actually ENC[...]
ciphertext and fails if any is not.

--all-data selects every key under data and stringData of a Kubernetes Secret
manifest instead of naming keys individually; keys whose values are already
encrypted are skipped. The manifest must be a Secret. --all-data and --key are
mutually exclusive.

Two modes:
  Cluster mode (default): fetch the manifest from a live cluster, encrypt the
    key, and push the result back via the partial-stack PATCH endpoint. The
    owning stack is resolved automatically.

  File mode (-f cluster.yaml): rewrite a local cluster.yaml's referenced
    from_file in place, adding the key to encrypted_paths in the file. Used by
    GitOps workflows where the source of truth is on disk.

In cluster mode, --set applies value edits in-memory BEFORE encrypting, so the
new secret value and its encryption land in a single commit - the plaintext
value never reaches git history. This is the recommended way to set a new
secret value:

  ankra cluster encrypt manifest db-secret --key password \
    --set 'data.password=aHVudGVyMg==' --cluster prod

(compared to "manifests upgrade --set" followed by "encrypt manifest", which
commits the plaintext value first).

Examples:
  # Cluster mode against the selected cluster
  ankra cluster encrypt manifest db-secret --key password

  # Cluster mode against a specific cluster
  ankra cluster encrypt manifest db-secret --key password --cluster prod

  # Set a new value and encrypt it atomically (single commit, no plaintext)
  ankra cluster encrypt manifest db-secret --key password \
    --set 'data.password=bmV3LXNlY3JldA==' --cluster prod

  # Encrypt several keys in one run
  ankra cluster encrypt manifest db-secret --key password --key api-token

  # Encrypt every data/stringData key of a Secret manifest
  ankra cluster encrypt manifest db-secret --all-data --cluster prod

  # File mode
  ankra cluster encrypt manifest db-secret --key password -f cluster.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: runEncryptManifest,
}

var clusterEncryptAddonCmd = &cobra.Command{
	Use:   "addon",
	Short: "Encrypt one or more keys in an addon's values",
	Long: `Encrypt one or more keys in an addon's Helm values using SOPS.

--key takes the YAML key name whose values should be encrypted. Repeat --key to
encrypt several keys in a single run; all keys are encrypted in one SOPS pass
and one write. SOPS matches key names anywhere in the document, not dotted
paths; a dotted --key is normalised to its last segment. A key whose own name
starts with a dot (such as ".dockerconfigjson") is kept literally. After
encrypting, the CLI verifies every value is actually ENC[...] ciphertext and
fails if any is not.

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

  # Encrypt several keys in one run
  ankra cluster encrypt addon --name grafana --key adminPassword --key smtpPassword

  # File mode
  ankra cluster encrypt addon --name grafana --key adminPassword -f cluster.yaml`,
	RunE: runEncryptAddon,
}

func init() {
	clusterEncryptManifestCmd.Flags().StringVarP(&encryptClusterFile, "file", "f", "", "Path to a local cluster YAML (enables file mode)")
	clusterEncryptManifestCmd.Flags().StringArrayVar(&encryptKeys, "key", nil, "YAML key name to encrypt (repeatable); dotted paths are normalised to the last segment, a leading-dot key like .dockerconfigjson is kept literally")
	clusterEncryptManifestCmd.Flags().BoolVar(&encryptAllData, "all-data", false, "Encrypt every key under data and stringData of a Secret manifest, skipping values that are already encrypted")
	clusterEncryptManifestCmd.Flags().StringVar(&encryptClusterFlag, "cluster", "", "Target cluster (name or ID); defaults to the active selection (cluster mode)")
	clusterEncryptManifestCmd.Flags().StringArrayVar(&encryptSetEntries, "set", nil, "Apply value edits in-memory before encrypting (same syntax as manifests upgrade --set, e.g. --set 'data.password=bmV3'); the plaintext value never reaches git (cluster mode only; repeatable)")
	clusterEncryptManifestCmd.MarkFlagsOneRequired("key", "all-data")
	clusterEncryptManifestCmd.MarkFlagsMutuallyExclusive("key", "all-data")
	clusterEncryptManifestCmd.MarkFlagsMutuallyExclusive("file", "cluster")
	clusterEncryptManifestCmd.MarkFlagsMutuallyExclusive("file", "set")

	clusterEncryptAddonCmd.Flags().StringVarP(&encryptClusterFile, "file", "f", "", "Path to a local cluster YAML (enables file mode)")
	clusterEncryptAddonCmd.Flags().StringArrayVar(&encryptKeys, "key", nil, "YAML key name to encrypt (required; repeatable); dotted paths are normalised to the last segment, a leading-dot key like .dockerconfigjson is kept literally")
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

// normalizeAndAnnounceEncryptKeys runs every --key value through
// normalizeEncryptKey, announces each normalisation, and drops duplicates after
// normalisation while preserving first-seen order.
func normalizeAndAnnounceEncryptKeys(cmd *cobra.Command, rawKeys []string) ([]string, error) {
	if len(rawKeys) == 0 {
		return nil, fmt.Errorf("at least one --key is required")
	}
	seenLeafKeys := map[string]bool{}
	leafKeys := make([]string, 0, len(rawKeys))
	for _, rawKey := range rawKeys {
		leafKey, err := normalizeEncryptKey(rawKey)
		if err != nil {
			return nil, err
		}
		announceEncryptKeyNormalization(cmd, rawKey, leafKey)
		if seenLeafKeys[leafKey] {
			continue
		}
		seenLeafKeys[leafKey] = true
		leafKeys = append(leafKeys, leafKey)
	}
	return leafKeys, nil
}

// describeEncryptKeys renders leaf keys for progress messages: `key "a"` for a
// single key, `keys "a", "b"` for several.
func describeEncryptKeys(leafKeys []string) string {
	quotedKeys := make([]string, len(leafKeys))
	for keyIndex, leafKey := range leafKeys {
		quotedKeys[keyIndex] = fmt.Sprintf("%q", leafKey)
	}
	if len(quotedKeys) == 1 {
		return "key " + quotedKeys[0]
	}
	return "keys " + strings.Join(quotedKeys, ", ")
}

// describeEncryptKeysCapitalised is describeEncryptKeys for sentence starts.
func describeEncryptKeysCapitalised(leafKeys []string) string {
	described := describeEncryptKeys(leafKeys)
	return strings.ToUpper(described[:1]) + described[1:]
}

// selectSecretDataKeys implements --all-data key selection: it parses manifest
// YAML, requires every document to be a Kubernetes Secret, and returns the
// keys under data and stringData. Keys whose value is already ENC[...]
// ciphertext are returned separately so callers can skip re-encrypting them.
func selectSecretDataKeys(manifestYAML []byte) (plaintextKeys, alreadyEncryptedKeys []string, err error) {
	decoder := yaml.NewDecoder(bytes.NewReader(manifestYAML))
	seenPlaintextKeys := map[string]bool{}
	seenEncryptedKeys := map[string]bool{}
	secretDocumentCount := 0
	for {
		var document yaml.Node
		decodeErr := decoder.Decode(&document)
		if errors.Is(decodeErr, io.EOF) {
			break
		}
		if decodeErr != nil {
			return nil, nil, fmt.Errorf("parse manifest YAML: %w", decodeErr)
		}
		rootNode := &document
		if rootNode.Kind == yaml.DocumentNode && len(rootNode.Content) > 0 {
			rootNode = resolveYAMLAlias(rootNode.Content[0])
		}
		if rootNode == nil || rootNode.Kind != yaml.MappingNode {
			continue
		}
		manifestKind := yamlMapString(rootNode, "kind")
		if manifestKind != "Secret" {
			if manifestKind == "" {
				return nil, nil, fmt.Errorf("--all-data requires a Secret manifest, but the manifest has no kind field")
			}
			return nil, nil, fmt.Errorf("--all-data requires a Secret manifest, but the manifest is a %s", manifestKind)
		}
		secretDocumentCount++
		for _, sectionKey := range []string{"data", "stringData"} {
			section := yamlMapValue(rootNode, sectionKey)
			if section == nil || section.Kind != yaml.MappingNode {
				continue
			}
			for entryIndex := 0; entryIndex+1 < len(section.Content); entryIndex += 2 {
				dataKey := section.Content[entryIndex].Value
				valueNode := resolveYAMLAlias(section.Content[entryIndex+1])
				if valueNode != nil && valueNode.Kind == yaml.ScalarNode && strings.HasPrefix(valueNode.Value, sopsEncryptedValuePrefix) {
					if !seenEncryptedKeys[dataKey] {
						seenEncryptedKeys[dataKey] = true
						alreadyEncryptedKeys = append(alreadyEncryptedKeys, dataKey)
					}
					continue
				}
				if !seenPlaintextKeys[dataKey] {
					seenPlaintextKeys[dataKey] = true
					plaintextKeys = append(plaintextKeys, dataKey)
				}
			}
		}
	}
	if secretDocumentCount == 0 {
		return nil, nil, fmt.Errorf("--all-data requires a Secret manifest, but the manifest contains no YAML documents")
	}
	if len(plaintextKeys) == 0 && len(alreadyEncryptedKeys) == 0 {
		return nil, nil, fmt.Errorf("the Secret manifest has no data or stringData keys to encrypt")
	}
	skippedKeys := make([]string, 0, len(alreadyEncryptedKeys))
	for _, dataKey := range alreadyEncryptedKeys {
		if !seenPlaintextKeys[dataKey] {
			skippedKeys = append(skippedKeys, dataKey)
		}
	}
	return plaintextKeys, skippedKeys, nil
}

// announceSkippedEncryptedKeys reports --all-data keys left untouched because
// their values are already ENC[...] ciphertext.
func announceSkippedEncryptedKeys(out io.Writer, alreadyEncryptedKeys []string) {
	if len(alreadyEncryptedKeys) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "Skipping already-encrypted %s.\n", describeEncryptKeys(alreadyEncryptedKeys))
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
	var leafKeys []string
	if !encryptAllData {
		normalizedKeys, err := normalizeAndAnnounceEncryptKeys(cmd, encryptKeys)
		if err != nil {
			return err
		}
		leafKeys = normalizedKeys
	}
	if encryptClusterFile == "" {
		return runEncryptManifestCluster(cmd, manifestName, leafKeys)
	}
	return runEncryptManifestFile(cmd, manifestName, leafKeys)
}

func runEncryptManifestFile(cmd *cobra.Command, manifestName string, leafKeys []string) error {
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

	for stackIdx := range cluster.Spec.Stacks {
		manifests := cluster.Spec.Stacks[stackIdx].Manifests
		for manifestIdx := range manifests {
			if manifests[manifestIdx].Name == manifestName {
				foundManifest = &manifests[manifestIdx]
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

	if encryptAllData {
		plaintextKeys, alreadyEncryptedKeys, selectErr := selectSecretDataKeys(manifestContent)
		if selectErr != nil {
			return selectErr
		}
		announceSkippedEncryptedKeys(os.Stdout, alreadyEncryptedKeys)
		if len(plaintextKeys) == 0 {
			fmt.Printf("All data keys in manifest %q are already encrypted; nothing to encrypt.\n", manifestName)
			return nil
		}
		leafKeys = plaintextKeys
	}

	fmt.Printf("Encrypting %s in manifest %q...\n", describeEncryptKeys(leafKeys), manifestName)

	encryptedContent, err := apiClient.EncryptYAML(string(manifestContent), leafKeys)
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	for _, leafKey := range leafKeys {
		if err := verifyKeyEncrypted(encryptedContent, leafKey); err != nil {
			return fmt.Errorf("encryption verification failed: %w", err)
		}
	}

	if err := os.WriteFile(manifestFilePath, []byte(encryptedContent), 0o644); err != nil {
		return fmt.Errorf("failed to write encrypted manifest file: %w", err)
	}

	fmt.Printf("Updated manifest file: %s\n", manifestFilePath)

	// Update the cluster YAML with encrypted_paths. The file is mutated at
	// the yaml.Node level so fields the CLI structs do not model
	// (deploy_wave, prometheus_metrics, future keys), comments, and ordering
	// survive the rewrite of this GitOps source of truth.
	missingKeys := make([]string, 0, len(leafKeys))
	for _, leafKey := range leafKeys {
		if !containsString(foundManifest.EncryptedPaths, leafKey) {
			missingKeys = append(missingKeys, leafKey)
		}
	}
	if len(missingKeys) > 0 {
		clusterDoc, err := parseClusterYAMLDoc(clusterData)
		if err != nil {
			return fmt.Errorf("failed to update cluster file: %w", err)
		}
		for _, leafKey := range missingKeys {
			if err := appendManifestEncryptedPath(clusterDoc, manifestName, leafKey); err != nil {
				return fmt.Errorf("failed to update cluster file: %w", err)
			}
		}
		if err := writeClusterFile(encryptClusterFile, clusterDoc); err != nil {
			return fmt.Errorf("failed to update cluster file: %w", err)
		}

		fmt.Printf("Updated cluster file with encrypted_paths: %s\n", encryptClusterFile)
	} else {
		fmt.Printf("%s already in encrypted_paths, cluster file unchanged\n", describeEncryptKeysCapitalised(leafKeys))
	}

	fmt.Println("Encryption complete!")
	return nil
}

func runEncryptAddon(cmd *cobra.Command, args []string) error {
	leafKeys, err := normalizeAndAnnounceEncryptKeys(cmd, encryptKeys)
	if err != nil {
		return err
	}
	if encryptClusterFile == "" {
		return runEncryptAddonCluster(cmd, encryptAddonName, leafKeys)
	}
	return runEncryptAddonFile(cmd, leafKeys)
}

func runEncryptAddonFile(cmd *cobra.Command, leafKeys []string) error {
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

	for stackIdx := range cluster.Spec.Stacks {
		addons := cluster.Spec.Stacks[stackIdx].Addons
		for addonIdx := range addons {
			if addons[addonIdx].Name == encryptAddonName {
				foundAddon = &addons[addonIdx]
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

	fmt.Printf("Encrypting %s in addon %q...\n", describeEncryptKeys(leafKeys), encryptAddonName)

	encryptedContent, err := apiClient.EncryptYAML(string(addonContent), leafKeys)
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	for _, leafKey := range leafKeys {
		if err := verifyKeyEncrypted(encryptedContent, leafKey); err != nil {
			return fmt.Errorf("encryption verification failed: %w", err)
		}
	}

	if err := os.WriteFile(addonFilePath, []byte(encryptedContent), 0o644); err != nil {
		return fmt.Errorf("failed to write encrypted addon configuration file: %w", err)
	}

	fmt.Printf("Updated addon configuration file: %s\n", addonFilePath)

	// Update the cluster YAML with encrypted_paths in the configuration. The
	// file is mutated at the yaml.Node level so fields the CLI structs do not
	// model (deploy_wave, prometheus_metrics, future keys), comments, and
	// ordering survive the rewrite of this GitOps source of truth.
	encryptedPaths := getEncryptedPathsFromConfig(foundAddon.Configuration)
	missingKeys := make([]string, 0, len(leafKeys))
	for _, leafKey := range leafKeys {
		if !containsString(encryptedPaths, leafKey) {
			missingKeys = append(missingKeys, leafKey)
		}
	}
	if len(missingKeys) > 0 {
		clusterDoc, err := parseClusterYAMLDoc(clusterData)
		if err != nil {
			return fmt.Errorf("failed to update cluster file: %w", err)
		}
		for _, leafKey := range missingKeys {
			if err := appendAddonEncryptedPath(clusterDoc, encryptAddonName, leafKey); err != nil {
				return fmt.Errorf("failed to update cluster file: %w", err)
			}
		}
		if err := writeClusterFile(encryptClusterFile, clusterDoc); err != nil {
			return fmt.Errorf("failed to update cluster file: %w", err)
		}

		fmt.Printf("Updated cluster file with encrypted_paths: %s\n", encryptClusterFile)
	} else {
		fmt.Printf("%s already in encrypted_paths, cluster file unchanged\n", describeEncryptKeysCapitalised(leafKeys))
	}

	fmt.Println("Encryption complete!")
	return nil
}

// deriveSopsEncryptedPaths inspects raw manifest YAML for SOPS encryption
// state. isSopsDocument reports whether any document carries a top-level
// "sops" metadata mapping. The returned paths are the YAML key names whose
// values are ENC[...] ciphertext - the same leaf-key convention that
// `ankra cluster encrypt` records in encrypted_paths. The sops metadata
// subtree itself is skipped (its mac/lastmodified entries are ciphertext but
// are not user data).
func deriveSopsEncryptedPaths(rawYAML []byte) (paths []string, isSopsDocument bool, err error) {
	decoder := yaml.NewDecoder(bytes.NewReader(rawYAML))
	seenPaths := map[string]bool{}
	for {
		var document yaml.Node
		decodeErr := decoder.Decode(&document)
		if errors.Is(decodeErr, io.EOF) {
			break
		}
		if decodeErr != nil {
			return nil, false, fmt.Errorf("parse manifest YAML: %w", decodeErr)
		}
		rootNode := &document
		if rootNode.Kind == yaml.DocumentNode && len(rootNode.Content) > 0 {
			rootNode = rootNode.Content[0]
		}
		if rootNode.Kind != yaml.MappingNode {
			continue
		}
		for entryIndex := 0; entryIndex+1 < len(rootNode.Content); entryIndex += 2 {
			if rootNode.Content[entryIndex].Value == sopsMetadataKey &&
				rootNode.Content[entryIndex+1].Kind == yaml.MappingNode {
				isSopsDocument = true
			}
		}
		collectEncryptedLeafKeys(rootNode, true, seenPaths, &paths)
	}
	return paths, isSopsDocument, nil
}

// collectEncryptedLeafKeys walks a YAML tree recording the mapping key names
// whose scalar values are ENC[...] ciphertext, skipping the document-root
// sops metadata mapping.
func collectEncryptedLeafKeys(node *yaml.Node, isDocumentRoot bool, seenPaths map[string]bool, paths *[]string) {
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
			if valueNode.Kind == yaml.ScalarNode && strings.HasPrefix(valueNode.Value, sopsEncryptedValuePrefix) {
				if !seenPaths[keyNode.Value] {
					seenPaths[keyNode.Value] = true
					*paths = append(*paths, keyNode.Value)
				}
				continue
			}
			collectEncryptedLeafKeys(valueNode, false, seenPaths, paths)
		}
	case yaml.SequenceNode:
		for _, itemNode := range node.Content {
			collectEncryptedLeafKeys(itemNode, false, seenPaths, paths)
		}
	}
}

// unionStringLists merges the given lists preserving first-seen order and
// dropping duplicates.
func unionStringLists(lists ...[]string) []string {
	seenValues := map[string]bool{}
	var merged []string
	for _, list := range lists {
		for _, value := range list {
			if !seenValues[value] {
				seenValues[value] = true
				merged = append(merged, value)
			}
		}
	}
	return merged
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

func runEncryptManifestCluster(cmd *cobra.Command, manifestName string, leafKeys []string) error {
	// The command ends in a partial-stack PATCH, which the backend serves
	// synchronously (DB transaction + a full GitOps commit/push when the
	// cluster has a linked repo) rather than enqueuing it - that can
	// legitimately take longer than 60s on a large cluster, so the timeout
	// matches the HTTP client's slow-write ceiling (see httpClientForSlowWrite).
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
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

	if len(encryptSetEntries) > 0 {
		assignments, assignErr := collectSetAssignments(encryptSetEntries, nil, nil)
		if assignErr != nil {
			return assignErr
		}
		mutatedB64, setErr := applyManifestSet(encoded, assignments, "", "")
		if setErr != nil {
			return setErr
		}
		encoded = mutatedB64
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Applied %d --set edit(s) in-memory; the new values are encrypted before anything is pushed.\n", len(assignments))
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("base64-decode manifest content: %w", err)
	}

	if encryptAllData {
		plaintextKeys, alreadyEncryptedKeys, selectErr := selectSecretDataKeys(decoded)
		if selectErr != nil {
			return selectErr
		}
		announceSkippedEncryptedKeys(cmd.OutOrStdout(), alreadyEncryptedKeys)
		if len(plaintextKeys) == 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "All data keys in manifest %q are already encrypted; nothing to encrypt.\n", manifestName)
			return nil
		}
		leafKeys = plaintextKeys
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Encrypting %s in manifest %q (stack %q)...\n", describeEncryptKeys(leafKeys), manifestName, stack.Name)
	encryptedYAML, err := apiClient.EncryptYAML(string(decoded), leafKeys)
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	for _, leafKey := range leafKeys {
		if err := verifyKeyEncrypted(encryptedYAML, leafKey); err != nil {
			return fmt.Errorf("encryption verification failed: %w", err)
		}
	}

	newPaths := append([]string{}, manifest.EncryptedPaths...)
	for _, leafKey := range leafKeys {
		if !containsString(newPaths, leafKey) {
			newPaths = append(newPaths, leafKey)
		}
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
		renderPatchResourceErrors(cmd.ErrOrStderr(), res.Errors)
		return errors.New("encryption partially failed; see errors above")
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Manifest %q encrypted in stack %q.\n", manifestName, stack.Name)
	return nil
}

func runEncryptAddonCluster(cmd *cobra.Command, addonName string, leafKeys []string) error {
	// The command ends in a partial-stack PATCH, which the backend serves
	// synchronously (DB transaction + a full GitOps commit/push when the
	// cluster has a linked repo) rather than enqueuing it - that can
	// legitimately take longer than 60s on a large cluster, so the timeout
	// matches the HTTP client's slow-write ceiling (see httpClientForSlowWrite).
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
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

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Encrypting %s in addon %q (stack %q)...\n", describeEncryptKeys(leafKeys), addonName, stack.Name)
	encryptedYAML, err := apiClient.EncryptYAML(currentValues, leafKeys)
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	for _, leafKey := range leafKeys {
		if err := verifyKeyEncrypted(encryptedYAML, leafKey); err != nil {
			return fmt.Errorf("encryption verification failed: %w", err)
		}
	}

	existingPaths := []string{}
	if addon.Configuration != nil {
		existingPaths = addon.Configuration.EncryptedPaths
	}
	newPaths := append([]string{}, existingPaths...)
	for _, leafKey := range leafKeys {
		if !containsString(newPaths, leafKey) {
			newPaths = append(newPaths, leafKey)
		}
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
		renderPatchResourceErrors(cmd.ErrOrStderr(), res.Errors)
		return errors.New("encryption partially failed; see errors above")
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Addon %q encrypted in stack %q.\n", addonName, stack.Name)
	return nil
}

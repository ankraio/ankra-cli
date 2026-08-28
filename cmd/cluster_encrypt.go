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
	"regexp"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	sopsMetadataKey          = "sops"
	sopsEncryptedValuePrefix = "ENC["

	// encryptKeyGlobPrefix marks a --key value as a key-name glob rather than
	// an exact key: "glob:stringData.DB_*" encrypts every key whose name
	// starts with DB_, including keys added later. It mirrors the platform's
	// encrypted_paths grammar (clusterengine.EncryptedPathGlobPrefix in the
	// cluster repo's enginekit) byte-for-byte, so the entry the CLI records
	// in encrypted_paths is the entry the platform's push lane re-expands on
	// every re-render. Only "*" is a wildcard; an optional leading "data." or
	// "stringData." section is accepted and stripped, as it is for exact
	// keys. TestEncryptKeyGlobGrammarMirrorsThePlatform pins both.
	encryptKeyGlobPrefix   = "glob:"
	encryptKeyGlobWildcard = "*"
)

// encryptKeySectionPrefixes are the Secret sections a --key value may name
// in front of the key; the platform strips the same two.
var encryptKeySectionPrefixes = []string{"data.", "stringData."}

// isEncryptKeyGlob reports whether an encrypted_paths entry uses the glob
// form.
func isEncryptKeyGlob(entry string) bool {
	return strings.HasPrefix(entry, encryptKeyGlobPrefix)
}

// parseEncryptKeyGlob compiles a glob: entry into an anchored key-name
// matcher, refusing the shapes the platform refuses: nothing after the
// prefix, a section with no pattern, no wildcard (an exact key in disguise
// - pass it without the prefix), and a pattern that is only wildcards (it
// would encrypt every key, metadata.name and kind included).
func parseEncryptKeyGlob(entry string) (*regexp.Regexp, error) {
	body := strings.TrimPrefix(entry, encryptKeyGlobPrefix)
	if body == "" {
		return nil, fmt.Errorf("invalid --key %q: the %s prefix must be followed by a key-name pattern such as glob:stringData.DB_*", entry, encryptKeyGlobPrefix)
	}
	keyPattern := body
	for _, sectionPrefix := range encryptKeySectionPrefixes {
		if strings.HasPrefix(body, sectionPrefix) {
			keyPattern = body[len(sectionPrefix):]
			break
		}
	}
	if keyPattern == "" {
		return nil, fmt.Errorf("invalid --key %q: names a section but no key-name pattern; add the pattern after the section, such as glob:stringData.DB_*", entry)
	}
	if !strings.Contains(keyPattern, encryptKeyGlobWildcard) {
		return nil, fmt.Errorf("invalid --key %q: contains no * wildcard; pass the exact key name without the %s prefix", entry, encryptKeyGlobPrefix)
	}
	if strings.Trim(keyPattern, encryptKeyGlobWildcard) == "" {
		return nil, fmt.Errorf("invalid --key %q: would match every key in the document; keep at least one literal character, such as glob:stringData.DB_*", entry)
	}
	literalPieces := strings.Split(keyPattern, encryptKeyGlobWildcard)
	quotedPieces := make([]string, 0, len(literalPieces))
	for _, piece := range literalPieces {
		quotedPieces = append(quotedPieces, regexp.QuoteMeta(piece))
	}
	matcher, compileErr := regexp.Compile("^" + strings.Join(quotedPieces, ".*") + "$")
	if compileErr != nil {
		return nil, fmt.Errorf("invalid --key %q: %w", entry, compileErr)
	}
	return matcher, nil
}

// encryptKeyMatcher returns the key-name predicate for one encrypted_paths
// entry: the glob's pattern, or exact equality for a leaf key.
func encryptKeyMatcher(entry string) (func(keyName string) bool, error) {
	if !isEncryptKeyGlob(entry) {
		return func(keyName string) bool { return keyName == entry }, nil
	}
	matcher, parseErr := parseEncryptKeyGlob(entry)
	if parseErr != nil {
		return nil, parseErr
	}
	return matcher.MatchString, nil
}

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

--key glob:<pattern> encrypts every key whose name matches the pattern, now
and on every later platform re-encrypt - including keys added afterwards.
Only "*" is a wildcard (any run of characters); everything else is literal,
and a leading "data." or "stringData." is accepted and ignored, exactly as for
an exact key. The entry is recorded in encrypted_paths as written
("glob:stringData.DB_*"), which is the form the platform re-expands into the
SOPS encrypted_regex on every push. A pattern that matches no key fails, the
same way a misspelled exact key does. Requires cluster#1867 on the platform.

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

  # Encrypt every DB_* key, including ones added later
  ankra cluster encrypt manifest db-secret --key 'glob:stringData.DB_*' --cluster prod

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

--key glob:<pattern> encrypts every key whose name matches the pattern, now
and on every later platform re-encrypt - including keys added afterwards.
Only "*" is a wildcard; everything else is literal. The entry is recorded in
encrypted_paths as written ("glob:*Password"). A pattern that matches no key
fails, the same way a misspelled exact key does. Requires cluster#1867 on the
platform.

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

  # Encrypt every *Password key, including ones added later
  ankra cluster encrypt addon --name grafana --key 'glob:*Password' --cluster prod

  # File mode
  ankra cluster encrypt addon --name grafana --key adminPassword -f cluster.yaml`,
	RunE: runEncryptAddon,
}

func init() {
	clusterEncryptManifestCmd.Flags().StringVarP(&encryptClusterFile, "file", "f", "", "Path to a local cluster YAML (enables file mode)")
	clusterEncryptManifestCmd.Flags().StringArrayVar(&encryptKeys, "key", nil, "YAML key name to encrypt (repeatable), or glob:<pattern> to encrypt every key whose name matches (only * is a wildcard, e.g. glob:stringData.DB_*); dotted paths are normalised to the last segment, a leading-dot key like .dockerconfigjson is kept literally")
	clusterEncryptManifestCmd.Flags().BoolVar(&encryptAllData, "all-data", false, "Encrypt every key under data and stringData of a Secret manifest, skipping values that are already encrypted")
	clusterEncryptManifestCmd.Flags().StringVar(&encryptClusterFlag, "cluster", "", "Target cluster (name or ID); defaults to the active selection (cluster mode)")
	clusterEncryptManifestCmd.Flags().StringArrayVar(&encryptSetEntries, "set", nil, "Apply value edits in-memory before encrypting (same syntax as manifests upgrade --set, e.g. --set 'data.password=bmV3'); the plaintext value never reaches git (cluster mode only; repeatable)")
	clusterEncryptManifestCmd.MarkFlagsOneRequired("key", "all-data")
	clusterEncryptManifestCmd.MarkFlagsMutuallyExclusive("key", "all-data")
	clusterEncryptManifestCmd.MarkFlagsMutuallyExclusive("file", "cluster")
	clusterEncryptManifestCmd.MarkFlagsMutuallyExclusive("file", "set")

	clusterEncryptAddonCmd.Flags().StringVarP(&encryptClusterFile, "file", "f", "", "Path to a local cluster YAML (enables file mode)")
	clusterEncryptAddonCmd.Flags().StringArrayVar(&encryptKeys, "key", nil, "YAML key name to encrypt (required; repeatable), or glob:<pattern> to encrypt every key whose name matches (only * is a wildcard, e.g. glob:*Password); dotted paths are normalised to the last segment, a leading-dot key like .dockerconfigjson is kept literally")
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
//
// A glob: entry is validated against the platform's grammar and kept
// verbatim, prefix included: the platform expands it into the SOPS
// encrypted_regex itself, on this encrypt and on every later re-render.
func normalizeEncryptKey(rawKey string) (string, error) {
	trimmedKey := strings.TrimSpace(rawKey)
	if trimmedKey == "" {
		return "", fmt.Errorf("--key must not be empty")
	}
	if isEncryptKeyGlob(trimmedKey) {
		if _, parseErr := parseEncryptKeyGlob(trimmedKey); parseErr != nil {
			return "", parseErr
		}
		return trimmedKey, nil
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
	if isEncryptKeyGlob(leafKey) {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Note: %s is recorded in encrypted_paths as written; every key whose name matches it is encrypted now and on every later platform re-encrypt, including keys added afterwards.\n",
			leafKey)
		return
	}
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
// single key, `keys "a", "b"` for several; a glob entry reads `keys matching
// glob:DB_*` because it names a set, not one key.
func describeEncryptKeys(leafKeys []string) string {
	quotedKeys := make([]string, 0, len(leafKeys))
	globEntries := make([]string, 0, len(leafKeys))
	for _, leafKey := range leafKeys {
		if isEncryptKeyGlob(leafKey) {
			globEntries = append(globEntries, leafKey)
			continue
		}
		quotedKeys = append(quotedKeys, fmt.Sprintf("%q", leafKey))
	}
	parts := make([]string, 0, 2)
	if len(quotedKeys) == 1 {
		parts = append(parts, "key "+quotedKeys[0])
	} else if len(quotedKeys) > 1 {
		parts = append(parts, "keys "+strings.Join(quotedKeys, ", "))
	}
	if len(globEntries) > 0 {
		parts = append(parts, "keys matching "+strings.Join(globEntries, ", "))
	}
	return strings.Join(parts, " and ")
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
// key silently produces a file with sops metadata and plaintext values. A
// glob: entry must select at least one key - a pattern matching nothing has
// encrypted nothing - and every key it selects must be ciphertext.
func verifyKeyEncrypted(encryptedYAML, keyName string) error {
	keyMatches, matcherErr := encryptKeyMatcher(keyName)
	if matcherErr != nil {
		return matcherErr
	}
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
		inspectEncryptedKey(rootNode, keyMatches, true, &matchedKeyCount, &plaintextValueCount)
	}
	if matchedKeyCount == 0 {
		if isEncryptKeyGlob(keyName) {
			return fmt.Errorf(
				"no YAML key matching %s exists in the encrypted output, so SOPS encrypted nothing while still writing sops metadata; "+
					"pass a pattern that selects at least one key in the document",
				keyName)
		}
		return fmt.Errorf(
			"no YAML key named %q exists in the encrypted output, so SOPS encrypted nothing while still writing sops metadata; "+
				"pass the key name itself (for data.password use --key password)",
			keyName)
	}
	if plaintextValueCount > 0 {
		if isEncryptKeyGlob(keyName) {
			return fmt.Errorf(
				"a value under a key matching %s is still plaintext after encryption; refusing to write a file that only looks encrypted",
				keyName)
		}
		return fmt.Errorf(
			"value under key %q is still plaintext after encryption; refusing to write a file that only looks encrypted",
			keyName)
	}
	return nil
}

func inspectEncryptedKey(node *yaml.Node, keyMatches func(keyName string) bool, isDocumentRoot bool, matchedKeyCount, plaintextValueCount *int) {
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
			if keyMatches(keyNode.Value) {
				*matchedKeyCount++
				if !isSubtreeEncrypted(valueNode) {
					*plaintextValueCount++
				}
				continue
			}
			inspectEncryptedKey(valueNode, keyMatches, false, matchedKeyCount, plaintextValueCount)
		}
	case yaml.SequenceNode:
		for _, itemNode := range node.Content {
			inspectEncryptedKey(itemNode, keyMatches, false, matchedKeyCount, plaintextValueCount)
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

	// Update the cluster YAML with encrypted_paths. Entries are matched by
	// key name however the file spells them - the platform writes
	// "stringData.PASSWORD", the CLI's grammar is the bare "PASSWORD", and
	// the platform reads both as one entry - and a new entry follows the
	// file's spelling. It is spliced into the file's own bytes at the
	// position the parser reports, so this GitOps source of truth changes by
	// exactly the line being added; re-encoding the tree would reflow every
	// sequence of a platform-written file.
	newEntries := make([]string, 0, len(leafKeys))
	for _, leafKey := range leafKeys {
		if !containsEncryptedPath(foundManifest.EncryptedPaths, leafKey) {
			newEntries = append(newEntries, encryptedPathEntry(foundManifest.EncryptedPaths, leafKey, secretKeySection(manifestContent, leafKey)))
		}
	}
	if len(newEntries) > 0 {
		updatedClusterData, err := spliceManifestEncryptedPaths(clusterData, manifestName, newEntries)
		if err != nil {
			return fmt.Errorf("failed to update cluster file: %w", err)
		}
		if err := os.WriteFile(encryptClusterFile, updatedClusterData, 0o644); err != nil {
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

	// Update the cluster YAML with encrypted_paths in the configuration -
	// matched by key name, spelled like the file, spliced into the file's own
	// bytes; see runEncryptManifestFile.
	encryptedPaths := getEncryptedPathsFromConfig(foundAddon.Configuration)
	newEntries := make([]string, 0, len(leafKeys))
	for _, leafKey := range leafKeys {
		if !containsEncryptedPath(encryptedPaths, leafKey) {
			newEntries = append(newEntries, encryptedPathEntry(encryptedPaths, leafKey, ""))
		}
	}
	if len(newEntries) > 0 {
		updatedClusterData, err := spliceAddonEncryptedPaths(clusterData, encryptAddonName, newEntries)
		if err != nil {
			return fmt.Errorf("failed to update cluster file: %w", err)
		}
		if err := os.WriteFile(encryptClusterFile, updatedClusterData, 0o644); err != nil {
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

// unionEncryptedPaths merges encrypted_paths lists preserving first-seen
// order. Two entries naming the same key in different spellings
// ("stringData.PASSWORD" and "PASSWORD") are one entry; the first spelling
// seen is kept. Globs are compared verbatim.
func unionEncryptedPaths(lists ...[]string) []string {
	seenKeys := map[string]bool{}
	var merged []string
	for _, list := range lists {
		for _, entry := range list {
			leaf := encryptedPathLeaf(entry)
			if !seenKeys[leaf] {
				seenKeys[leaf] = true
				merged = append(merged, entry)
			}
		}
	}
	return merged
}

// encryptedPathLeaf returns the key name an encrypted_paths entry names: the
// entry with a leading "data." or "stringData." section removed, the two the
// platform strips when it reads the list. A glob entry is returned as
// written; globs are compared verbatim.
func encryptedPathLeaf(entry string) string {
	if isEncryptKeyGlob(entry) {
		return entry
	}
	return entry[len(encryptedPathSection(entry)):]
}

// encryptedPathSection returns the section an exact entry names in front of
// its key ("data." or "stringData."), or "" for a bare or glob entry.
func encryptedPathSection(entry string) string {
	if isEncryptKeyGlob(entry) {
		return ""
	}
	for _, sectionPrefix := range encryptKeySectionPrefixes {
		if strings.HasPrefix(entry, sectionPrefix) {
			return sectionPrefix
		}
	}
	return ""
}

// containsEncryptedPath reports whether entries already record leafKey,
// however they spell it.
func containsEncryptedPath(entries []string, leafKey string) bool {
	wanted := encryptedPathLeaf(leafKey)
	for _, entry := range entries {
		if encryptedPathLeaf(entry) == wanted {
			return true
		}
	}
	return false
}

// encryptedPathEntry spells leafKey the way entries already spell theirs.
// The platform reads every spelling alike; matching the file keeps a GitOps
// diff to the line being added. When every exact entry carries a section,
// the new one does too - the section holding the key when the caller knows
// it, else the one section the file agrees on. Otherwise, or when the list
// is empty, the entry is the bare key: the CLI's own grammar.
func encryptedPathEntry(entries []string, leafKey, section string) string {
	leaf := encryptedPathLeaf(leafKey)
	sections := map[string]bool{}
	exactEntries := 0
	for _, entry := range entries {
		if isEncryptKeyGlob(entry) {
			continue
		}
		exactEntries++
		sections[encryptedPathSection(entry)] = true
	}
	if exactEntries == 0 || sections[""] {
		return leaf
	}
	if section != "" {
		return section + "." + leaf
	}
	if len(sections) == 1 {
		for onlySection := range sections {
			return onlySection + leaf
		}
	}
	return leaf
}

// secretKeySection reports which Secret section - "data" or "stringData" -
// holds leafKey in manifestYAML, or "" when neither does.
func secretKeySection(manifestYAML []byte, leafKey string) string {
	decoder := yaml.NewDecoder(bytes.NewReader(manifestYAML))
	for {
		var document yaml.Node
		decodeErr := decoder.Decode(&document)
		if decodeErr != nil {
			return ""
		}
		rootNode := &document
		if rootNode.Kind == yaml.DocumentNode && len(rootNode.Content) > 0 {
			rootNode = resolveYAMLAlias(rootNode.Content[0])
		}
		if rootNode == nil || rootNode.Kind != yaml.MappingNode {
			continue
		}
		for _, sectionKey := range []string{"data", "stringData"} {
			section := yamlMapValue(rootNode, sectionKey)
			if section != nil && section.Kind == yaml.MappingNode && mapIndexOf(section, leafKey) >= 0 {
				return sectionKey
			}
		}
	}
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
		if !containsEncryptedPath(newPaths, leafKey) {
			newPaths = append(newPaths, encryptedPathEntry(newPaths, leafKey, ""))
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
		if !containsEncryptedPath(newPaths, leafKey) {
			newPaths = append(newPaths, encryptedPathEntry(newPaths, leafKey, ""))
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

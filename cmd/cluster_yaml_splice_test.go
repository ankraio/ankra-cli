package cmd

import (
	"strings"
	"testing"
)

// The splice helpers exist so that recording one encrypted path changes one
// line of a GitOps source of truth. Every test here therefore compares whole
// files: the expectation is the input with exactly the asked-for lines added.

func spliceDBSecretPath(t *testing.T, source, value string) string {
	t.Helper()
	got, err := spliceManifestEncryptedPaths([]byte(source), "db-secret", []string{value})
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	return string(got)
}

func assertSameBytes(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("file changed beyond the requested line.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// platformClusterYAML is written the way the platform writes cluster files:
// sequences indentless under their key, a long plain scalar folded across
// two lines, keys in the platform's alphabetical order.
const platformClusterYAML = `apiVersion: v1
kind: ImportCluster
metadata:
  name: prod
spec:
  stacks:
  - addons: []
    description: a description long enough that the platform's emitter folds it
      onto a second line, which must come back exactly as it was
    manifests:
    - encrypted_paths:
      - stringData.PASSWORD
      force: false # keep this comment
      from_file: manifests/secret.yaml
      name: db-secret
    name: core
`

func TestSpliceListEntry_IndentlessBlockListGainsOneLineInItsOwnStyle(t *testing.T) {
	got := spliceDBSecretPath(t, platformClusterYAML, "stringData.TOKEN")
	want := strings.Replace(platformClusterYAML,
		"      - stringData.PASSWORD\n",
		"      - stringData.PASSWORD\n      - stringData.TOKEN\n", 1)
	assertSameBytes(t, got, want)
}

func TestSpliceListEntry_IndentedBlockListGainsOneLineInItsOwnStyle(t *testing.T) {
	source := `apiVersion: v1
kind: ImportCluster
metadata:
  name: prod
spec:
  stacks:
    - name: core
      manifests:
        - name: db-secret
          from_file: manifests/secret.yaml
          encrypted_paths:
            - password
`
	got := spliceDBSecretPath(t, source, "token")
	want := strings.Replace(source, "            - password\n", "            - password\n            - token\n", 1)
	assertSameBytes(t, got, want)
}

func TestSpliceListEntry_MissingKeyIsCreatedIndentlessWhenTheDocumentIs(t *testing.T) {
	source := strings.Replace(platformClusterYAML,
		"    - encrypted_paths:\n      - stringData.PASSWORD\n      force: false # keep this comment\n",
		"    - force: false # keep this comment\n", 1)
	got := spliceDBSecretPath(t, source, "password")
	// The list goes after the mapping's last line, dashes level with the key,
	// because that is how every other sequence in the file is written.
	want := strings.Replace(source,
		"      name: db-secret\n",
		"      name: db-secret\n      encrypted_paths:\n      - password\n", 1)
	assertSameBytes(t, got, want)
}

func TestSpliceListEntry_MissingKeyIsCreatedIndentedWhenTheDocumentIs(t *testing.T) {
	source := `apiVersion: v1
kind: ImportCluster
metadata:
  name: prod
spec:
  stacks:
    - name: core
      manifests:
        - name: db-secret
          from_file: manifests/secret.yaml
`
	got := spliceDBSecretPath(t, source, "password")
	want := strings.Replace(source,
		"          from_file: manifests/secret.yaml\n",
		"          from_file: manifests/secret.yaml\n          encrypted_paths:\n            - password\n", 1)
	assertSameBytes(t, got, want)
}

func TestSpliceListEntry_EmptyFlowListIsFilledOnItsLine(t *testing.T) {
	source := strings.Replace(platformClusterYAML,
		"    - encrypted_paths:\n      - stringData.PASSWORD\n",
		"    - encrypted_paths: [] # none yet\n", 1)
	got := spliceDBSecretPath(t, source, "password")
	want := strings.Replace(source, "encrypted_paths: [] # none yet", "encrypted_paths: [password] # none yet", 1)
	assertSameBytes(t, got, want)
}

func TestSpliceListEntry_FlowListIsExtendedOnItsLine(t *testing.T) {
	source := strings.Replace(platformClusterYAML,
		"    - encrypted_paths:\n      - stringData.PASSWORD\n",
		"    - encrypted_paths: [stringData.PASSWORD, stringData.USER]\n", 1)
	got := spliceDBSecretPath(t, source, "stringData.TOKEN")
	want := strings.Replace(source,
		"[stringData.PASSWORD, stringData.USER]",
		"[stringData.PASSWORD, stringData.USER, stringData.TOKEN]", 1)
	assertSameBytes(t, got, want)
}

func TestSpliceListEntry_BareKeyWithNoValueGetsItsFirstItem(t *testing.T) {
	source := strings.Replace(platformClusterYAML,
		"    - encrypted_paths:\n      - stringData.PASSWORD\n",
		"    - encrypted_paths:\n", 1)
	got := spliceDBSecretPath(t, source, "password")
	want := strings.Replace(source, "    - encrypted_paths:\n", "    - encrypted_paths:\n      - password\n", 1)
	assertSameBytes(t, got, want)
}

func TestSpliceListEntry_GlobEntryIsWrittenVerbatim(t *testing.T) {
	got := spliceDBSecretPath(t, platformClusterYAML, "glob:stringData.DB_*")
	want := strings.Replace(platformClusterYAML,
		"      - stringData.PASSWORD\n",
		"      - stringData.PASSWORD\n      - glob:stringData.DB_*\n", 1)
	assertSameBytes(t, got, want)
}

func TestSpliceListEntry_SeveralEntriesLandInOrder(t *testing.T) {
	got, err := spliceManifestEncryptedPaths([]byte(platformClusterYAML), "db-secret", []string{"stringData.A", "stringData.B"})
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	want := strings.Replace(platformClusterYAML,
		"      - stringData.PASSWORD\n",
		"      - stringData.PASSWORD\n      - stringData.A\n      - stringData.B\n", 1)
	assertSameBytes(t, string(got), want)
}

func TestSpliceListEntry_AliasedListIsRefusedRatherThanEditedThroughTheAnchor(t *testing.T) {
	source := `apiVersion: v1
kind: ImportCluster
metadata:
  name: prod
x-shared: &shared
  - password
spec:
  stacks:
  - name: core
    manifests:
    - name: db-secret
      from_file: manifests/secret.yaml
      encrypted_paths: *shared
`
	_, err := spliceManifestEncryptedPaths([]byte(source), "db-secret", []string{"token"})
	if err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("expected an alias refusal, got %v", err)
	}
}

func TestSpliceListEntry_FlowMappingIsRefused(t *testing.T) {
	source := `apiVersion: v1
kind: ImportCluster
metadata:
  name: prod
spec:
  stacks:
  - name: core
    manifests:
    - {name: db-secret, from_file: manifests/secret.yaml}
`
	_, err := spliceManifestEncryptedPaths([]byte(source), "db-secret", []string{"password"})
	if err == nil || !strings.Contains(err.Error(), "flow") {
		t.Fatalf("expected a flow-mapping refusal, got %v", err)
	}
}

func TestSpliceListEntry_CRLFFileKeepsItsLineEndings(t *testing.T) {
	source := strings.ReplaceAll(platformClusterYAML, "\n", "\r\n")
	got := spliceDBSecretPath(t, source, "stringData.TOKEN")
	want := strings.Replace(source,
		"      - stringData.PASSWORD\r\n",
		"      - stringData.PASSWORD\r\n      - stringData.TOKEN\r\n", 1)
	assertSameBytes(t, got, want)
}

func TestSpliceAddonEncryptedPaths_CreatesTheListUnderConfiguration(t *testing.T) {
	source := `apiVersion: v1
kind: ImportCluster
metadata:
  name: prod
spec:
  stacks:
  - addons:
    - chart_name: grafana
      configuration:
        from_file: values/grafana.yaml
      name: grafana
    name: core
`
	got, err := spliceAddonEncryptedPaths([]byte(source), "grafana", []string{"adminPassword", "smtpPassword"})
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	want := strings.Replace(source,
		"        from_file: values/grafana.yaml\n",
		"        from_file: values/grafana.yaml\n        encrypted_paths:\n        - adminPassword\n        - smtpPassword\n", 1)
	assertSameBytes(t, string(got), want)
}

func TestSpliceManifestEncryptedPaths_UnknownManifestIsAnError(t *testing.T) {
	_, err := spliceManifestEncryptedPaths([]byte(platformClusterYAML), "nonexistent", []string{"password"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a not-found error, got %v", err)
	}
}

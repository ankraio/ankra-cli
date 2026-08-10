package cmd

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for repeatable --key and --all-data on `cluster encrypt` (bead
// ankra-5h4e.6, PLA-741 / customer ticket #1054): encrypting many keys used to
// take one invocation per key, and there was no way to encrypt every data key
// of a Secret at once.

const bothKeysEncryptedSecretManifestYAML = `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: web
type: Opaque
data:
  username: ENC[AES256_GCM,data:usr,iv:abc,tag:def,type:str]
  password: ENC[AES256_GCM,data:pwd,iv:abc,tag:def,type:str]
sops:
  mac: ENC[AES256_GCM,data:mac]
  encrypted_regex: ^(password|username)$
`

const allDataPlainSecretManifestYAML = `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: web
type: Opaque
data:
  username: YWRtaW4=
  password: aHVudGVyMg==
stringData:
  api-token: hunter2
`

const allDataEncryptedSecretManifestYAML = `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: web
type: Opaque
data:
  username: ENC[AES256_GCM,data:usr,iv:abc,tag:def,type:str]
  password: ENC[AES256_GCM,data:pwd,iv:abc,tag:def,type:str]
stringData:
  api-token: ENC[AES256_GCM,data:tok,iv:abc,tag:def,type:str]
sops:
  mac: ENC[AES256_GCM,data:mac]
`

const partiallyEncryptedSecretManifestYAML = `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: web
type: Opaque
data:
  username: YWRtaW4=
  password: ENC[AES256_GCM,data:pwd,iv:abc,tag:def,type:str]
stringData:
  api-token: hunter2
sops:
  mac: ENC[AES256_GCM,data:mac]
`

const configMapManifestYAML = `apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
data:
  setting: enabled
`

func TestRunEncryptManifest_FileModeMultipleKeysSingleEncryptCall(t *testing.T) {
	clusterPath, manifestPath := writeEncryptFileModeFixture(t, plainSecretManifestYAML)

	mock := &upgradeMock{encryptResult: bothKeysEncryptedSecretManifestYAML}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "my-secret",
		"--key", "password",
		"--key", "data.username",
		"-f", clusterPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected exactly one EncryptYAML call for two keys, got %d", len(mock.encryptCalls))
	}
	paths := mock.encryptCalls[0].EncryptedPaths
	if len(paths) != 2 || paths[0] != "password" || paths[1] != "username" {
		t.Errorf("encrypted paths = %v, want [password username]", paths)
	}

	writtenManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read written manifest: %v", err)
	}
	if string(writtenManifest) != bothKeysEncryptedSecretManifestYAML {
		t.Errorf("manifest file = %q, want the encrypted content", writtenManifest)
	}

	writtenCluster, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatalf("read written cluster file: %v", err)
	}
	assertContainsAll(t, string(writtenCluster), []string{"- password", "- username"})
}

func TestRunEncryptManifest_ClusterModeMultipleKeysPatchesAllPaths(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(plainSecretManifestYAML)),
		encryptResult: bothKeysEncryptedSecretManifestYAML,
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "demo-namespace",
		"--key", "password",
		"--key", "username",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected exactly one EncryptYAML call for two keys, got %d", len(mock.encryptCalls))
	}
	callPaths := mock.encryptCalls[0].EncryptedPaths
	if len(callPaths) != 2 || callPaths[0] != "password" || callPaths[1] != "username" {
		t.Errorf("encrypted paths = %v, want [password username]", callPaths)
	}
	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.capturedRequests))
	}
	manifest := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if len(manifest.EncryptedPaths) != 2 || manifest.EncryptedPaths[0] != "password" || manifest.EncryptedPaths[1] != "username" {
		t.Errorf("manifest.encrypted_paths = %v, want [password username]", manifest.EncryptedPaths)
	}
}

func TestRunEncryptManifest_DuplicateKeysDeduplicatedAfterNormalisation(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(plainSecretManifestYAML)),
		encryptResult: encryptedSecretManifestYAML,
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "demo-namespace",
		"--key", "data.password",
		"--key", "password",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected one EncryptYAML call, got %d", len(mock.encryptCalls))
	}
	callPaths := mock.encryptCalls[0].EncryptedPaths
	if len(callPaths) != 1 || callPaths[0] != "password" {
		t.Errorf("encrypted paths = %v, want the deduplicated [password]", callPaths)
	}
	manifest := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if len(manifest.EncryptedPaths) != 1 || manifest.EncryptedPaths[0] != "password" {
		t.Errorf("manifest.encrypted_paths = %v, want [password]", manifest.EncryptedPaths)
	}
}

func TestRunEncryptManifest_AllDataFileModeEncryptsEveryDataKey(t *testing.T) {
	clusterPath, manifestPath := writeEncryptFileModeFixture(t, allDataPlainSecretManifestYAML)

	mock := &upgradeMock{encryptResult: allDataEncryptedSecretManifestYAML}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "my-secret",
		"--all-data",
		"-f", clusterPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected exactly one EncryptYAML call, got %d", len(mock.encryptCalls))
	}
	callPaths := mock.encryptCalls[0].EncryptedPaths
	if len(callPaths) != 3 || callPaths[0] != "username" || callPaths[1] != "password" || callPaths[2] != "api-token" {
		t.Errorf("encrypted paths = %v, want [username password api-token]", callPaths)
	}

	writtenManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read written manifest: %v", err)
	}
	if string(writtenManifest) != allDataEncryptedSecretManifestYAML {
		t.Errorf("manifest file = %q, want the encrypted content", writtenManifest)
	}

	writtenCluster, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatalf("read written cluster file: %v", err)
	}
	assertContainsAll(t, string(writtenCluster), []string{"- username", "- password", "- api-token"})
}

func TestRunEncryptManifest_AllDataClusterModeSkipsAlreadyEncrypted(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(partiallyEncryptedSecretManifestYAML)),
		encryptResult: allDataEncryptedSecretManifestYAML,
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "demo-namespace",
		"--all-data",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected one EncryptYAML call, got %d", len(mock.encryptCalls))
	}
	callPaths := mock.encryptCalls[0].EncryptedPaths
	if len(callPaths) != 2 || callPaths[0] != "username" || callPaths[1] != "api-token" {
		t.Errorf("encrypted paths = %v, want [username api-token] (password already encrypted)", callPaths)
	}
	if !strings.Contains(out.String(), `Skipping already-encrypted key "password"`) {
		t.Errorf("expected a skip notice for the already-encrypted key, got:\n%s", out.String())
	}
	manifest := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if len(manifest.EncryptedPaths) != 2 || manifest.EncryptedPaths[0] != "username" || manifest.EncryptedPaths[1] != "api-token" {
		t.Errorf("manifest.encrypted_paths = %v, want [username api-token]", manifest.EncryptedPaths)
	}
}

func TestRunEncryptManifest_AllDataNothingLeftToEncrypt(t *testing.T) {
	mock := &upgradeMock{
		iac:         sampleIaCYAMLForCmd,
		manifestB64: base64.StdEncoding.EncodeToString([]byte(allDataEncryptedSecretManifestYAML)),
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "demo-namespace",
		"--all-data",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 0 {
		t.Errorf("expected no EncryptYAML call when every key is encrypted, got %d", len(mock.encryptCalls))
	}
	if len(mock.capturedRequests) != 0 {
		t.Errorf("expected no PATCH when every key is encrypted, got %d", len(mock.capturedRequests))
	}
	if !strings.Contains(out.String(), "already encrypted; nothing to encrypt") {
		t.Errorf("expected a nothing-to-encrypt notice, got:\n%s", out.String())
	}
}

func TestRunEncryptManifest_AllDataRejectsNonSecret(t *testing.T) {
	clusterPath, manifestPath := writeEncryptFileModeFixture(t, configMapManifestYAML)

	mock := &upgradeMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "my-secret",
		"--all-data",
		"-f", clusterPath,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a non-Secret manifest")
	}
	if !strings.Contains(err.Error(), "ConfigMap") {
		t.Errorf("expected the error to name the actual kind, got: %v", err)
	}
	if len(mock.encryptCalls) != 0 {
		t.Errorf("must not call EncryptYAML for a non-Secret manifest, got %d calls", len(mock.encryptCalls))
	}

	manifestAfter, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatalf("read manifest after failed run: %v", readErr)
	}
	if string(manifestAfter) != configMapManifestYAML {
		t.Errorf("manifest file must be untouched on rejection, got:\n%s", manifestAfter)
	}
}

func TestRunEncryptManifest_AllDataAndKeyMutuallyExclusive(t *testing.T) {
	mock := &upgradeMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "any",
		"--key", "password",
		"--all-data",
		"-f", "/tmp/x.yaml",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected mutual-exclusion error for --key with --all-data")
	}
	if !strings.Contains(err.Error(), "none of the others can be") {
		t.Errorf("expected the flag-group exclusion error, got: %v", err)
	}
}

func TestRunEncryptManifest_RequiresKeyOrAllData(t *testing.T) {
	mock := &upgradeMock{}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "any",
		"-f", "/tmp/x.yaml",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when neither --key nor --all-data is given")
	}
	if !strings.Contains(err.Error(), "at least one of the flags") {
		t.Errorf("expected the one-required flag-group error, got: %v", err)
	}
}

func TestRunEncryptAddon_ClusterModeMultipleKeys(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		addonValues:   "adminPassword: hunter2\nsmtpPassword: hunter3\n",
		encryptResult: "adminPassword: ENC[AES256_GCM,data:a]\nsmtpPassword: ENC[AES256_GCM,data:b]\n",
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "addon",
		"--name", "website",
		"--key", "adminPassword",
		"--key", "smtpPassword",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected exactly one EncryptYAML call for two keys, got %d", len(mock.encryptCalls))
	}
	callPaths := mock.encryptCalls[0].EncryptedPaths
	if len(callPaths) != 2 || callPaths[0] != "adminPassword" || callPaths[1] != "smtpPassword" {
		t.Errorf("encrypted paths = %v, want [adminPassword smtpPassword]", callPaths)
	}
	addon := mock.capturedRequests[0].Body.Spec.Stacks[0].Addons[0]
	if addon.Configuration == nil {
		t.Fatal("expected configuration in PATCH")
	}
	if len(addon.Configuration.EncryptedPaths) != 2 ||
		addon.Configuration.EncryptedPaths[0] != "adminPassword" ||
		addon.Configuration.EncryptedPaths[1] != "smtpPassword" {
		t.Errorf("addon.encrypted_paths = %v, want [adminPassword smtpPassword]", addon.Configuration.EncryptedPaths)
	}
}

func writeEncryptAddonFileModeFixture(t *testing.T, valuesYAML string) (clusterPath, valuesPath string) {
	t.Helper()
	dir := t.TempDir()
	valuesPath = filepath.Join(dir, "values", "grafana.yaml")
	if err := os.MkdirAll(filepath.Dir(valuesPath), 0o755); err != nil {
		t.Fatalf("create values dir: %v", err)
	}
	if err := os.WriteFile(valuesPath, []byte(valuesYAML), 0o644); err != nil {
		t.Fatalf("write values fixture: %v", err)
	}
	clusterPath = filepath.Join(dir, "cluster.yaml")
	clusterYAML := `apiVersion: v1
kind: ImportCluster
metadata:
  name: file-mode-test
spec:
  stacks:
    - name: web
      addons:
        - name: grafana
          chart_name: grafana
          chart_version: "7.0.0"
          configuration:
            from_file: values/grafana.yaml
`
	if err := os.WriteFile(clusterPath, []byte(clusterYAML), 0o644); err != nil {
		t.Fatalf("write cluster fixture: %v", err)
	}
	return clusterPath, valuesPath
}

func TestRunEncryptAddon_FileModeMultipleKeys(t *testing.T) {
	clusterPath, valuesPath := writeEncryptAddonFileModeFixture(t, "adminPassword: hunter2\nsmtpPassword: hunter3\n")

	encryptedValues := "adminPassword: ENC[AES256_GCM,data:a]\nsmtpPassword: ENC[AES256_GCM,data:b]\n"
	mock := &upgradeMock{encryptResult: encryptedValues}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "addon",
		"--name", "grafana",
		"--key", "adminPassword",
		"--key", "smtpPassword",
		"-f", clusterPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected exactly one EncryptYAML call for two keys, got %d", len(mock.encryptCalls))
	}
	callPaths := mock.encryptCalls[0].EncryptedPaths
	if len(callPaths) != 2 || callPaths[0] != "adminPassword" || callPaths[1] != "smtpPassword" {
		t.Errorf("encrypted paths = %v, want [adminPassword smtpPassword]", callPaths)
	}

	writtenValues, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("read written values file: %v", err)
	}
	if string(writtenValues) != encryptedValues {
		t.Errorf("values file = %q, want the encrypted content", writtenValues)
	}

	writtenCluster, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatalf("read written cluster file: %v", err)
	}
	assertContainsAll(t, string(writtenCluster), []string{"encrypted_paths:", "- adminPassword", "- smtpPassword"})
}

// formattingFixtureClusterYAML exercises everything the yaml.Node round-trip
// editor must preserve: a head comment, line comments, a deliberate key order
// (addons before name), an anchor/alias pair, and fields the CLI structs do
// not model (prometheus_metrics, deploy_wave, future_field).
const formattingFixtureClusterYAML = `# GitOps source of truth for prod - hand-maintained ordering.
apiVersion: v1
kind: ImportCluster
metadata:
  name: prod
spec:
  prometheus_metrics:
    endpoint: https://prom.example.com # scraped by victoria
    flavor: victoriametrics
  stacks:
    - addons:
        - name: grafana
          chart_name: grafana
          chart_version: "7.0.0"
          configuration:
            from_file: values/grafana.yaml
      name: core # addons deliberately listed before name
      deploy_wave: 2 # after networking
      namespace: &web-ns web
      manifests:
        - name: db-secret
          namespace: *web-ns
          from_file: manifests/secret.yaml
      future_field: keep-me
`

func TestRunEncryptManifest_FileModePreservesUntouchedLinesByteForByte(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(clusterPath, []byte(formattingFixtureClusterYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifests", "secret.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(plainSecretManifestYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	mock := &upgradeMock{encryptResult: encryptedSecretManifestYAML}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "db-secret",
		"--key", "password",
		"-f", clusterPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	written, err := os.ReadFile(clusterPath)
	if err != nil {
		t.Fatal(err)
	}

	// The only permitted change to cluster.yaml is the encrypted_paths entry
	// appended to the db-secret manifest; every other line must survive the
	// rewrite byte-identically (the referenced manifest file changes in its
	// own file, not here).
	expected := strings.Replace(formattingFixtureClusterYAML,
		"          from_file: manifests/secret.yaml\n",
		"          from_file: manifests/secret.yaml\n          encrypted_paths:\n            - password\n",
		1)
	if string(written) != expected {
		t.Errorf("cluster file changed beyond the encrypted_paths append.\n--- got ---\n%s\n--- want ---\n%s", written, expected)
	}
}

func TestSelectSecretDataKeys_MultiDocumentSecrets(t *testing.T) {
	manifestYAML := []byte(`apiVersion: v1
kind: Secret
metadata:
  name: first
data:
  password: aHVudGVyMg==
  token: ENC[AES256_GCM,data:tok]
---
apiVersion: v1
kind: Secret
metadata:
  name: second
stringData:
  password: hunter2
  extra: value
`)
	plaintextKeys, alreadyEncryptedKeys, err := selectSecretDataKeys(manifestYAML)
	if err != nil {
		t.Fatalf("selectSecretDataKeys failed: %v", err)
	}
	if len(plaintextKeys) != 2 || plaintextKeys[0] != "password" || plaintextKeys[1] != "extra" {
		t.Errorf("plaintext keys = %v, want [password extra]", plaintextKeys)
	}
	if len(alreadyEncryptedKeys) != 1 || alreadyEncryptedKeys[0] != "token" {
		t.Errorf("already-encrypted keys = %v, want [token]", alreadyEncryptedKeys)
	}
}

func TestSelectSecretDataKeys_NoDataKeys(t *testing.T) {
	manifestYAML := []byte(`apiVersion: v1
kind: Secret
metadata:
  name: empty
type: Opaque
`)
	if _, _, err := selectSecretDataKeys(manifestYAML); err == nil {
		t.Error("expected an error for a Secret without data or stringData keys")
	}
}

package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// The GitOps pointers in this fixture are deliberately NOT the paths the
// platform falls back to (stacks/<stack>/add-ons/<name>/values.yaml and
// stacks/<stack>/manifests/<name>.yaml): a PATCH that dropped them would
// still store a path, just the wrong one, so only a hand-placed path tells
// "kept" apart from "defaulted" (ankra-syg75, PLA-863).
const customFromFileIaCYAML = `apiVersion: v1
kind: ImportCluster
metadata:
  name: website-demo
spec:
  stacks:
    - name: demo-web-app
      addons:
        - name: website
          chart_name: website
          chart_version: 1.0.145
          namespace: web
          configuration:
            from_file: platform/website/values.yaml
      manifests:
        - name: demo-namespace
          namespace: web
          from_file: platform/namespaces/web.yaml
`

const (
	customAddonFromFile    = "platform/website/values.yaml"
	customManifestFromFile = "platform/namespaces/web.yaml"
)

// runFromFileCommand executes one CLI invocation against the mock and
// returns the combined output.
func runFromFileCommand(t *testing.T, mock *upgradeMock, arguments ...string) string {
	t.Helper()
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)
	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(arguments)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	return out.String()
}

// assertPatchWireCarries checks the serialized PATCH body, not just the
// struct: from_file is omitempty, so an empty value vanishes from the wire and
// the platform falls back to the default path.
func assertPatchWireCarries(t *testing.T, mock *upgradeMock, fragment string) {
	t.Helper()
	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.capturedRequests))
	}
	body, err := json.Marshal(mock.capturedRequests[0].Body)
	if err != nil {
		t.Fatalf("marshal PATCH body: %v", err)
	}
	if !strings.Contains(string(body), fragment) {
		t.Errorf("PATCH body does not carry %s:\n%s", fragment, body)
	}
}

func TestRunAddonsUpgrade_SetKeepsValuesFromFile(t *testing.T) {
	mock := &upgradeMock{iac: customFromFileIaCYAML, addonValues: "image:\n  tag: 1.0.0\n"}
	runFromFileCommand(t, mock, "cluster", "addons", "upgrade", "website",
		"--set", "image.tag=1.0.146", "--cluster", fakeClusterUUID)

	addon := mock.capturedRequests[0].Body.Spec.Stacks[0].Addons[0]
	if addon.Configuration == nil {
		t.Fatal("expected configuration in PATCH")
	}
	if addon.Configuration.FromFile != customAddonFromFile {
		t.Errorf("configuration.from_file = %q, want %q", addon.Configuration.FromFile, customAddonFromFile)
	}
	decoded, err := base64.StdEncoding.DecodeString(addon.Configuration.ValuesBase64)
	if err != nil || !strings.Contains(string(decoded), "tag: 1.0.146") {
		t.Errorf("values_base64 = %q (%v), want the mutated values", decoded, err)
	}
	assertPatchWireCarries(t, mock, `"from_file":"`+customAddonFromFile+`"`)
}

func TestRunAddonsUpgrade_DryRunNamesTheValuesFile(t *testing.T) {
	mock := &upgradeMock{iac: customFromFileIaCYAML, addonValues: "image:\n  tag: 1.0.0\n"}
	output := runFromFileCommand(t, mock, "cluster", "addons", "upgrade", "website",
		"--set", "image.tag=1.0.146", "--cluster", fakeClusterUUID, "--dry-run")

	if len(mock.capturedRequests) != 0 {
		t.Fatalf("dry-run must not PATCH, got %d", len(mock.capturedRequests))
	}
	if strings.Contains(output, "replacing the configuration.from_file") {
		t.Errorf("dry-run still says the reference is replaced:\n%s", output)
	}
	if !strings.Contains(output, customAddonFromFile) {
		t.Errorf("dry-run notice should name the values file %q:\n%s", customAddonFromFile, output)
	}
}

func TestRunManifestsUpgrade_SetKeepsFromFile(t *testing.T) {
	mock := &upgradeMock{
		iac:         customFromFileIaCYAML,
		manifestB64: base64.StdEncoding.EncodeToString([]byte(sampleDeploymentManifestYAML)),
	}
	runFromFileCommand(t, mock, "cluster", "manifests", "upgrade", "demo-namespace",
		"--set", "spec.template.spec.containers[name=app].image=nginx:1.27", "--cluster", fakeClusterUUID)

	manifest := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if manifest.FromFile != customManifestFromFile {
		t.Errorf("from_file = %q, want %q", manifest.FromFile, customManifestFromFile)
	}
	assertPatchWireCarries(t, mock, `"from_file":"`+customManifestFromFile+`"`)
}

func TestRunEncryptManifest_ClusterModeKeepsFromFile(t *testing.T) {
	mock := &upgradeMock{
		iac:           customFromFileIaCYAML,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(plainSecretManifestYAML)),
		encryptResult: encryptedSecretManifestYAML,
	}
	runFromFileCommand(t, mock, "cluster", "encrypt", "manifest", "demo-namespace",
		"--key", "data.password", "--cluster", fakeClusterUUID)

	manifest := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if manifest.FromFile != customManifestFromFile {
		t.Errorf("from_file = %q, want %q", manifest.FromFile, customManifestFromFile)
	}
	assertPatchWireCarries(t, mock, `"from_file":"`+customManifestFromFile+`"`)
}

func TestRunEncryptAddon_ClusterModeKeepsValuesFromFile(t *testing.T) {
	mock := &upgradeMock{
		iac:           customFromFileIaCYAML,
		addonValues:   "adminPassword: hunter2\n",
		encryptResult: "adminPassword: ENC[AES256_GCM,data:abc]\n",
	}
	runFromFileCommand(t, mock, "cluster", "encrypt", "addon",
		"--name", "website", "--key", "adminPassword", "--cluster", fakeClusterUUID)

	addon := mock.capturedRequests[0].Body.Spec.Stacks[0].Addons[0]
	if addon.Configuration == nil || addon.Configuration.FromFile != customAddonFromFile {
		t.Errorf("configuration = %+v, want from_file %q kept", addon.Configuration, customAddonFromFile)
	}
	assertPatchWireCarries(t, mock, `"from_file":"`+customAddonFromFile+`"`)
}

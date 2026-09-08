package cmd

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

// PLA-830 (ankra-bfvfy): cluster-mode encrypt sealed only the --key paths.
// A manifest or addon that already declared other encrypted_paths - values a
// portal or API save stored as plaintext for the next GitOps push to seal -
// came back as a SOPS document with plaintext under those declared paths,
// which the platform's store-time guard refuses ("update stack failed:
// status 500" on the CLI lane, with the verdict swallowed). The encrypt call
// must carry the union of the requested keys and the declared paths.

const sampleIaCYAMLWithDeclaredPaths = `apiVersion: v1
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
            encrypted_paths:
              - smtpPassword
      manifests:
        - name: demo-namespace
          namespace: web
          encrypted_paths:
            - stringData.OTHER_SECRET
            - data.username
`

func TestRunEncryptManifest_ClusterModeSealsTheDeclaredPathsWithTheKey(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLWithDeclaredPaths,
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
		"--key", "password",
		"--set", `data.password=bmV3LXZhbHVl`,
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected exactly one EncryptYAML call, got %d", len(mock.encryptCalls))
	}
	callPaths := mock.encryptCalls[0].EncryptedPaths
	want := []string{"password", "stringData.OTHER_SECRET", "data.username"}
	if strings.Join(callPaths, ",") != strings.Join(want, ",") {
		t.Errorf("encrypted paths = %v, want the requested key first and every declared path after it %v", callPaths, want)
	}
	if !strings.Contains(out.String(), "Also sealing the already-declared") {
		t.Errorf("output does not say the declared paths are sealed too:\n%s", out.String())
	}

	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.capturedRequests))
	}
	manifest := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if !containsEncryptedPath(manifest.EncryptedPaths, "password") ||
		!containsEncryptedPath(manifest.EncryptedPaths, "OTHER_SECRET") ||
		!containsEncryptedPath(manifest.EncryptedPaths, "username") {
		t.Errorf("manifest.encrypted_paths = %v, want the declared paths kept and password added", manifest.EncryptedPaths)
	}
}

func TestRunEncryptManifest_ClusterModeDoesNotDuplicateAKeyAlreadyDeclared(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLWithDeclaredPaths,
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
		"--key", "username",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	callPaths := mock.encryptCalls[0].EncryptedPaths
	want := []string{"username", "stringData.OTHER_SECRET"}
	if strings.Join(callPaths, ",") != strings.Join(want, ",") {
		t.Errorf("encrypted paths = %v, want %v (data.username and username are one key)", callPaths, want)
	}
	manifest := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if len(manifest.EncryptedPaths) != 2 {
		t.Errorf("manifest.encrypted_paths = %v, want the two declared entries unchanged", manifest.EncryptedPaths)
	}
}

func TestRunEncryptManifest_ClusterModeWithoutDeclaredPathsSealsOnlyTheKeys(t *testing.T) {
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
		"--key", "password",
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	callPaths := mock.encryptCalls[0].EncryptedPaths
	if len(callPaths) != 1 || callPaths[0] != "password" {
		t.Errorf("encrypted paths = %v, want [password]", callPaths)
	}
	if strings.Contains(out.String(), "Also sealing") {
		t.Errorf("nothing was declared, so nothing extra should be announced:\n%s", out.String())
	}
}

func TestRunEncryptAddon_ClusterModeSealsTheDeclaredPathsWithTheKey(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLWithDeclaredPaths,
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
		"--cluster", fakeClusterUUID,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	callPaths := mock.encryptCalls[0].EncryptedPaths
	want := []string{"adminPassword", "smtpPassword"}
	if strings.Join(callPaths, ",") != strings.Join(want, ",") {
		t.Errorf("encrypted paths = %v, want %v", callPaths, want)
	}
	addon := mock.capturedRequests[0].Body.Spec.Stacks[0].Addons[0]
	if addon.Configuration == nil {
		t.Fatal("expected configuration in PATCH")
	}
	if strings.Join(addon.Configuration.EncryptedPaths, ",") != "smtpPassword,adminPassword" {
		t.Errorf("addon.encrypted_paths = %v, want [smtpPassword adminPassword]", addon.Configuration.EncryptedPaths)
	}
}

func TestPathsToSealWithDeclared(t *testing.T) {
	out := new(bytes.Buffer)
	got := pathsToSealWithDeclared(out, []string{"password"}, []string{"stringData.password", "glob:DB_*", "token"})
	if strings.Join(got, ",") != "password,glob:DB_*,token" {
		t.Errorf("union = %v, want the requested spelling kept and the rest appended in order", got)
	}
	if !strings.Contains(out.String(), "glob:DB_*") || !strings.Contains(out.String(), `"token"`) {
		t.Errorf("announcement should name what was added: %q", out.String())
	}

	out.Reset()
	got = pathsToSealWithDeclared(out, []string{"password"}, nil)
	if len(got) != 1 || got[0] != "password" || out.Len() != 0 {
		t.Errorf("no declaration: got %v with output %q", got, out.String())
	}
}

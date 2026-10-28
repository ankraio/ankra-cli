package cmd

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ankra/internal/client"
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
	if !strings.Contains(out.String(), "Including the already-declared") {
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
	if strings.Contains(out.String(), "Including the already-declared") {
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

// File mode reaches the same shape by a different route: `ankra cluster
// decrypt manifest -f` writes the plaintext document back to disk and
// deliberately leaves encrypted_paths declared in the cluster.yaml, so the
// documented decrypt-edit-encrypt loop leaves a plaintext file under a
// multi-entry declaration. Sealing only --key there produces exactly the
// document the platform's store-time guard refuses on the next `ankra apply`
// - the PLA-830 verdict, one lane over.

// writeDeclaredPathsFileModeFixture writes a cluster.yaml whose manifest and
// addon already declare encrypted_paths, over a plaintext from_file - the
// state `ankra cluster decrypt` leaves behind.
func writeDeclaredPathsFileModeFixture(t *testing.T, manifestYAML, addonValuesYAML string) (clusterPath, manifestPath, addonPath string) {
	t.Helper()
	dir := t.TempDir()
	manifestPath = filepath.Join(dir, "manifests", "secret.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("create manifests dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest fixture: %v", err)
	}
	addonPath = filepath.Join(dir, "values", "website.yaml")
	if err := os.MkdirAll(filepath.Dir(addonPath), 0o755); err != nil {
		t.Fatalf("create values dir: %v", err)
	}
	if err := os.WriteFile(addonPath, []byte(addonValuesYAML), 0o644); err != nil {
		t.Fatalf("write addon fixture: %v", err)
	}
	clusterPath = filepath.Join(dir, "cluster.yaml")
	clusterYAML := `apiVersion: v1
kind: ImportCluster
metadata:
  name: file-mode-declared
spec:
  stacks:
    - name: web
      addons:
        - name: website
          chart_name: website
          chart_version: 1.0.145
          namespace: web
          configuration:
            from_file: values/website.yaml
            encrypted_paths:
              - smtpPassword
      manifests:
        - name: my-secret
          from_file: manifests/secret.yaml
          encrypted_paths:
            - data.username
            - stringData.OTHER_SECRET
`
	if err := os.WriteFile(clusterPath, []byte(clusterYAML), 0o644); err != nil {
		t.Fatalf("write cluster fixture: %v", err)
	}
	return clusterPath, manifestPath, addonPath
}

func TestRunEncryptManifest_FileModeSealsTheDeclaredPathsWithTheKey(t *testing.T) {
	clusterPath, _, _ := writeDeclaredPathsFileModeFixture(t, plainSecretManifestYAML, "adminPassword: hunter2\n")

	mock := &upgradeMock{encryptResult: bothKeysEncryptedSecretManifestYAML}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "my-secret",
		"--key", "password",
		"-f", clusterPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected exactly one EncryptYAML call, got %d", len(mock.encryptCalls))
	}
	callPaths := mock.encryptCalls[0].EncryptedPaths
	want := []string{"password", "data.username", "stringData.OTHER_SECRET"}
	if strings.Join(callPaths, ",") != strings.Join(want, ",") {
		t.Errorf("encrypted paths = %v, want the requested key first and every declared entry after it %v", callPaths, want)
	}
	if !strings.Contains(out.String(), "Including the already-declared") {
		t.Errorf("output does not say the declared paths are sealed too:\n%s", out.String())
	}
}

func TestRunEncryptManifest_FileModeWithoutDeclaredPathsSealsOnlyTheKeys(t *testing.T) {
	clusterPath, _ := writeEncryptFileModeFixture(t, plainSecretManifestYAML)

	mock := &upgradeMock{encryptResult: encryptedSecretManifestYAML}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "manifest", "my-secret",
		"--key", "password",
		"-f", clusterPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	callPaths := mock.encryptCalls[0].EncryptedPaths
	if len(callPaths) != 1 || callPaths[0] != "password" {
		t.Errorf("encrypted paths = %v, want [password]", callPaths)
	}
	if strings.Contains(out.String(), "Including the already-declared") {
		t.Errorf("nothing was declared, so nothing extra should be announced:\n%s", out.String())
	}
}

func TestRunEncryptAddon_FileModeSealsTheDeclaredPathsWithTheKey(t *testing.T) {
	clusterPath, _, _ := writeDeclaredPathsFileModeFixture(t, plainSecretManifestYAML,
		"adminPassword: hunter2\nsmtpPassword: hunter3\n")

	mock := &upgradeMock{encryptResult: "adminPassword: ENC[AES256_GCM,data:a]\nsmtpPassword: ENC[AES256_GCM,data:b]\n"}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"cluster", "encrypt", "addon",
		"--name", "website",
		"--key", "adminPassword",
		"-f", clusterPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}

	callPaths := mock.encryptCalls[0].EncryptedPaths
	want := []string{"adminPassword", "smtpPassword"}
	if strings.Join(callPaths, ",") != strings.Join(want, ",") {
		t.Errorf("encrypted paths = %v, want %v", callPaths, want)
	}
}

// The other half of PLA-830: once cluster#2828 turned the store guard's
// verdict into a 422, the CLI's blanket "git push failed:" prefix told the
// customer their GitOps push had failed - for a save the platform refused
// before it wrote anything, and right after they had confirmed no commit was
// made. The refusal has to reach them as itself.
func TestRunEncryptManifest_StoreGuardRefusalIsNotReportedAsAGitPushFailure(t *testing.T) {
	const verdict = "Cannot persist values for 'runway-envs': the document carries SOPS metadata but " +
		"declared encrypted paths [stringData.VOYAGE_API_KEY] are plaintext. " +
		"Re-encrypt the document with sops (fresh MAC) before saving."

	mock := &upgradeMock{
		iac:           sampleIaCYAMLWithDeclaredPaths,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(plainSecretManifestYAML)),
		encryptResult: encryptedSecretManifestYAML,
		patchErr: &client.PatchStackError{
			StatusCode: 422,
			Body:       []byte(`{"detail":` + strconv.Quote(verdict) + `}`),
		},
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
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected the store-guard refusal to surface as an error")
	}
	if strings.Contains(err.Error(), "git push") {
		t.Errorf("the refusal never reached git; it must not be reported as a push failure: %v", err)
	}
	if err.Error() != verdict {
		t.Errorf("error = %q, want the platform's verdict verbatim %q", err.Error(), verdict)
	}
}

func TestMapPatchError_OnlyAMarkedPushFailureIsCalledOne(t *testing.T) {
	marked := &client.PatchStackError{StatusCode: 422,
		Body: []byte(`{"detail":"GitHub authentication failed","error_code":"GIT_PUSH_FAILED"}`)}
	if got := mapPatchError(marked).Error(); got != "git push failed: GitHub authentication failed" {
		t.Errorf("marked push failure = %q, want the git-push prefix kept", got)
	}

	unmarked := &client.PatchStackError{StatusCode: 422,
		Body: []byte(`{"detail":"Invalid encrypted_paths entry \"glob:\": the glob: prefix must be followed by a key-name pattern"}`)}
	got := mapPatchError(unmarked).Error()
	if strings.Contains(got, "git push") {
		t.Errorf("unmarked refusal reported as a push failure: %q", got)
	}
	if !strings.HasPrefix(got, "Invalid encrypted_paths entry") {
		t.Errorf("unmarked refusal = %q, want the platform detail verbatim", got)
	}
}

// The announcement names what the declaration added, worked out by key name.
// Slicing merged[len(leafKeys):] instead would name the wrong entries the
// moment the requested keys repeat a name, and could not be reasoned about
// without checking that normalizeAndAnnounceEncryptKeys still deduplicates.
func TestPathsToSealWithDeclared_RepeatedRequestedKeysDoNotShiftTheAnnouncement(t *testing.T) {
	out := new(bytes.Buffer)
	// "password" and "data.password" are one key; the union collapses them.
	got := pathsToSealWithDeclared(out, []string{"password", "data.password"}, []string{"OTHER_SECRET", "token"})
	if strings.Join(got, ",") != "password,OTHER_SECRET,token" {
		t.Errorf("union = %v, want the repeated request collapsed and the declaration appended", got)
	}
	announced := out.String()
	for _, wanted := range []string{"OTHER_SECRET", "token"} {
		if !strings.Contains(announced, wanted) {
			t.Errorf("announcement does not name the declared %q: %q", wanted, announced)
		}
	}
	if strings.Contains(announced, "password") {
		t.Errorf("a requested key must not be announced as added by the declaration: %q", announced)
	}
}

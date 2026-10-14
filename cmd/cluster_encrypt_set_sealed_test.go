package cmd

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// A --set on a manifest whose value is already sealed edited the sealed
// document in place, leaving the old sops metadata and MAC over a value that
// was now plaintext; the platform could not decrypt it to re-seal and every
// rotation failed with "Failed to encrypt content". The CLI now unseals the
// manifest in memory first and seals the edited plaintext afresh.

func runEncryptManifestOnSealedSecret(t *testing.T, mock *upgradeMock, extraArgs ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	arguments := append([]string{"cluster", "encrypt", "manifest", "demo-namespace", "--cluster", fakeClusterUUID}, extraArgs...)
	cmd.SetArgs(arguments)
	executeError := cmd.Execute()
	return out.String(), executeError
}

func TestRunEncryptManifest_SetRotatesAnAlreadySealedValue(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(encryptedSecretManifestYAML)),
		decryptResult: plainSecretManifestYAML,
		encryptResult: encryptedSecretManifestYAML,
	}
	output, executeError := runEncryptManifestOnSealedSecret(t, mock,
		"--key", "password",
		"--set", "data.password=cm90YXRlZA==",
	)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}

	if len(mock.encryptCalls) != 1 {
		t.Fatalf("expected exactly one EncryptYAML call, got %d", len(mock.encryptCalls))
	}
	sentContent := mock.encryptCalls[0].YamlContent
	if strings.Contains(sentContent, "sops:") || strings.Contains(sentContent, "ENC[") {
		t.Errorf("the content sent to encrypt still carries the old seal:\n%s", sentContent)
	}
	if !strings.Contains(sentContent, "password: cm90YXRlZA==") {
		t.Errorf("the content sent to encrypt does not carry the new value:\n%s", sentContent)
	}
	if len(mock.capturedRequests) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(mock.capturedRequests))
	}
}

func TestRunEncryptManifest_SetOnASealedManifestKeepsEveryPreviouslySealedKeySealed(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(encryptedSecretManifestYAML)),
		decryptResult: plainSecretManifestYAML,
		encryptResult: bothKeysEncryptedSecretManifestYAML,
	}
	output, executeError := runEncryptManifestOnSealedSecret(t, mock,
		"--key", "username",
		"--set", "data.username=cm9vdA==",
	)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}

	callPaths := mock.encryptCalls[0].EncryptedPaths
	if !containsEncryptedPath(callPaths, "username") || !containsEncryptedPath(callPaths, "password") {
		t.Errorf("encrypted paths = %v, want the requested key and the previously sealed password", callPaths)
	}
	manifest := mock.capturedRequests[0].Body.Spec.Stacks[0].Manifests[0]
	if !containsEncryptedPath(manifest.EncryptedPaths, "password") {
		t.Errorf("manifest.encrypted_paths = %v, want password recorded", manifest.EncryptedPaths)
	}
}

func TestRunEncryptManifest_SetOnAPlaintextManifestDoesNotDecrypt(t *testing.T) {
	mock := &upgradeMock{
		iac:           sampleIaCYAMLForCmd,
		manifestB64:   base64.StdEncoding.EncodeToString([]byte(plainSecretManifestYAML)),
		decryptErr:    errors.New("decrypt must not be called"),
		encryptResult: encryptedSecretManifestYAML,
	}
	output, executeError := runEncryptManifestOnSealedSecret(t, mock,
		"--key", "password",
		"--set", "data.password=bmV3",
	)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if paths := mock.encryptCalls[0].EncryptedPaths; len(paths) != 1 || paths[0] != "password" {
		t.Errorf("encrypted paths = %v, want [password]", paths)
	}
}

func TestRunEncryptManifest_SetOnASealedManifestReportsADecryptFailure(t *testing.T) {
	mock := &upgradeMock{
		iac:         sampleIaCYAMLForCmd,
		manifestB64: base64.StdEncoding.EncodeToString([]byte(encryptedSecretManifestYAML)),
		decryptErr:  errors.New("key mismatch"),
	}
	output, executeError := runEncryptManifestOnSealedSecret(t, mock,
		"--key", "password",
		"--set", "data.password=bmV3",
	)
	if executeError == nil {
		t.Fatalf("expected an error, got none\noutput: %s", output)
	}
	if !strings.Contains(executeError.Error(), "decrypt sealed manifest before applying --set") {
		t.Errorf("error = %v, want it to name the decrypt step", executeError)
	}
	if len(mock.encryptCalls) != 0 || len(mock.capturedRequests) != 0 {
		t.Errorf("nothing should be encrypted or pushed after a failed decrypt")
	}
}

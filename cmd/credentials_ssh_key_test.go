package cmd

// The org-wide SSH-key credential commands, against a fake platform that
// answers the real routes with the real response shapes (cluster
// go/internal/providerapi/credentials.go and credapi).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// maasPublicKey is a real ed25519 public key; its fingerprint below is what
// `ssh-keygen -lf` prints for it.
const (
	maasPublicKey   = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBbr+bc5f+n5kIIm++SkvmZ2LX2Coff+TO+DC69XWbfi mattias@ankra.io"
	maasFingerprint = "SHA256:HujLwiCBK4R73zg9mxlgb3WeZXOCNFgcfAthksxMjZc"
)

type fakeSSHKeyPlatform struct {
	mutex       sync.Mutex
	listBody    string
	createCode  int
	createBody  string
	detailBody  string
	requests    []string
	createdWith map[string]any
}

func (platform *fakeSSHKeyPlatform) serve(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		platform.mutex.Lock()
		defer platform.mutex.Unlock()
		platform.requests = append(platform.requests, request.Method+" "+request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/credentials/hetzner/ssh-keys":
			_, _ = writer.Write([]byte(platform.listBody))
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/credentials/hetzner/ssh-key":
			body, _ := io.ReadAll(request.Body)
			platform.createdWith = map[string]any{}
			_ = json.Unmarshal(body, &platform.createdWith)
			writer.WriteHeader(platform.createCode)
			_, _ = writer.Write([]byte(platform.createBody))
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/v1/org/credentials/"):
			_, _ = writer.Write([]byte(platform.detailBody))
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"Not Found"}`))
		}
	}))
	previousClient := apiClient
	apiClient = client.New("test-token", server.URL)
	t.Cleanup(func() {
		apiClient = previousClient
		server.Close()
	})
}

func (platform *fakeSSHKeyPlatform) requestCount() int {
	platform.mutex.Lock()
	defer platform.mutex.Unlock()
	return len(platform.requests)
}

// runSSHKeyCommand sets flags on a fresh copy of the command's flag state and
// runs it, returning what went to stdout (human output is fmt.Printf),
// what went to the command's own writers (structured output, hints) and
// the error.
func runSSHKeyCommand(t *testing.T, command *cobra.Command, flags map[string]string) (string, string, error) {
	t.Helper()
	resetCommandFlags(t, command)
	for name, value := range flags {
		if err := command.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}
	var commandOutput bytes.Buffer
	command.SetOut(&commandOutput)
	command.SetErr(&commandOutput)
	t.Cleanup(func() {
		command.SetOut(nil)
		command.SetErr(nil)
		resetCommandFlags(t, command)
	})
	var runError error
	stdout := captureStdout(t, func() {
		runError = command.RunE(command, nil)
	})
	return stdout, commandOutput.String(), runError
}

func TestSSHKeyListShowsTheCredentialsWithoutAFingerprintColumnTheAPIDoesNotSend(t *testing.T) {
	platform := &fakeSSHKeyPlatform{listBody: `[
		{"id":"66687213-9190-48d4-81d6-4748de371220","name":"maas","provider":"ssh-key","available":true,"state":"up","created_at":"2026-04-11T19:33:28Z"},
		{"id":"d07ea4d0-5928-455b-87e3-e2fe8e6b50ac","name":"mash","provider":"ssh-key","available":false,"created_at":"2026-05-26T19:44:01Z"}
	]`}
	platform.serve(t)

	stdout, _, err := runSSHKeyCommand(t, credentialsSSHKeyListCmd, nil)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	for _, want := range []string{"66687213-9190-48d4-81d6-4748de371220", "maas", "mash", "AVAILABLE"} {
		if !strings.Contains(strings.ToUpper(stdout), strings.ToUpper(want)) {
			t.Errorf("list output lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(strings.ToUpper(stdout), "FINGERPRINT") {
		t.Errorf("list printed a fingerprint column the platform sent no values for:\n%s", stdout)
	}
}

func TestSSHKeyListShowsTheFingerprintWhenThePlatformSendsOne(t *testing.T) {
	platform := &fakeSSHKeyPlatform{listBody: `[
		{"id":"66687213-9190-48d4-81d6-4748de371220","name":"maas","provider":"ssh-key","available":true,"created_at":"2026-04-11T19:33:28Z","fingerprint":"` + maasFingerprint + `"},
		{"id":"d07ea4d0-5928-455b-87e3-e2fe8e6b50ac","name":"mash","provider":"ssh-key","available":true,"created_at":"2026-05-26T19:44:01Z"}
	]`}
	platform.serve(t)

	stdout, _, err := runSSHKeyCommand(t, credentialsSSHKeyListCmd, nil)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(strings.ToUpper(stdout), "FINGERPRINT") || !strings.Contains(stdout, maasFingerprint) {
		t.Errorf("list did not show the platform's fingerprint:\n%s", stdout)
	}
}

func TestSSHKeyListStructuredOutputIsTheList(t *testing.T) {
	platform := &fakeSSHKeyPlatform{listBody: `[{"id":"66687213-9190-48d4-81d6-4748de371220","name":"maas","provider":"ssh-key","available":true,"created_at":"2026-04-11T19:33:28Z"}]`}
	platform.serve(t)

	stdout, structured, err := runSSHKeyCommand(t, credentialsSSHKeyListCmd, map[string]string{"output": "json"})
	if err != nil {
		t.Fatalf("list -o json failed: %v", err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("-o json wrote human output to stdout:\n%s", stdout)
	}
	var decoded []map[string]any
	if decodeError := json.Unmarshal([]byte(structured), &decoded); decodeError != nil {
		t.Fatalf("-o json is not a JSON list: %v\n%s", decodeError, structured)
	}
	if len(decoded) != 1 || decoded[0]["name"] != "maas" {
		t.Errorf("unexpected list: %s", structured)
	}
	if _, present := decoded[0]["fingerprint"]; present {
		t.Errorf("-o json invented a fingerprint the platform did not send: %s", structured)
	}
}

func TestSSHKeyCreateSendsThePublicKeyAndPrintsItsFingerprint(t *testing.T) {
	platform := &fakeSSHKeyPlatform{
		createCode: http.StatusCreated,
		createBody: `{"success":true,"id":"aaaaaaaa-1111-4111-8111-111111111111","name":"ops-laptop","private_key":null,"errors":[]}`,
	}
	platform.serve(t)

	stdout, _, err := runSSHKeyCommand(t, credentialsSSHKeyCreateCmd, map[string]string{
		"name":       "ops-laptop",
		"public-key": "  " + maasPublicKey + "\n",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if platform.createdWith["name"] != "ops-laptop" || platform.createdWith["ssh_public_key"] != maasPublicKey || platform.createdWith["generate"] != false {
		t.Errorf("unexpected create body: %#v", platform.createdWith)
	}
	for _, want := range []string{"ops-laptop", "aaaaaaaa-1111-4111-8111-111111111111", maasFingerprint} {
		if !strings.Contains(stdout, want) {
			t.Errorf("create output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestSSHKeyCreateReadsThePublicKeyFile(t *testing.T) {
	platform := &fakeSSHKeyPlatform{
		createCode: http.StatusCreated,
		createBody: `{"success":true,"id":"aaaaaaaa-1111-4111-8111-111111111111","name":"ops-laptop","errors":[]}`,
	}
	platform.serve(t)
	publicKeyPath := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(publicKeyPath, []byte(maasPublicKey+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, structured, err := runSSHKeyCommand(t, credentialsSSHKeyCreateCmd, map[string]string{
		"name":            "ops-laptop",
		"public-key-file": publicKeyPath,
		"output":          "json",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if platform.createdWith["ssh_public_key"] != maasPublicKey {
		t.Errorf("public key file was not sent as read: %#v", platform.createdWith)
	}
	var decoded sshKeyCreateResult
	if decodeError := json.Unmarshal([]byte(structured), &decoded); decodeError != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", decodeError, structured)
	}
	if decoded.ID != "aaaaaaaa-1111-4111-8111-111111111111" || decoded.Fingerprint != maasFingerprint || decoded.Generated {
		t.Errorf("unexpected structured result: %+v", decoded)
	}
}

// The duplicate refusal is the platform's 400 with a nested resource error
// naming the credential that already holds the key. The user must see that
// sentence, not a JSON body.
func TestSSHKeyCreateRelaysTheDuplicateRefusalNamingTheExistingCredential(t *testing.T) {
	platform := &fakeSSHKeyPlatform{
		createCode: http.StatusBadRequest,
		createBody: `{"detail":{"errors":[{"name":"ops-laptop","kind":"hetzner_ssh_key","errors":[{"key":"ssh_public_key","message":"An SSH key with the same public key already exists ('maas')"}]}]}}`,
	}
	platform.serve(t)

	_, _, err := runSSHKeyCommand(t, credentialsSSHKeyCreateCmd, map[string]string{
		"name":       "ops-laptop",
		"public-key": maasPublicKey,
	})
	if err == nil {
		t.Fatal("create succeeded against a refusal")
	}
	if !strings.Contains(err.Error(), "An SSH key with the same public key already exists ('maas')") {
		t.Errorf("refusal not relayed readably: %v", err)
	}
	if strings.Contains(err.Error(), "{") {
		t.Errorf("refusal printed as raw JSON: %v", err)
	}
	if code := exitCodeFor(err); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
}

func TestSSHKeyCreateRefusesAPrivateKeyBeforeSendingAnything(t *testing.T) {
	platform := &fakeSSHKeyPlatform{createCode: http.StatusCreated, createBody: `{"success":true}`}
	platform.serve(t)
	privateKeyPath := filepath.Join(t.TempDir(), "id_ed25519")
	privateKey := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----\n"
	if err := os.WriteFile(privateKeyPath, []byte(privateKey), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := runSSHKeyCommand(t, credentialsSSHKeyCreateCmd, map[string]string{
		"name":            "oops",
		"public-key-file": privateKeyPath,
	})
	if err == nil || !strings.Contains(err.Error(), "private key") {
		t.Fatalf("private key was not refused: %v", err)
	}
	if exitCodeFor(err) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(err), exitUsage)
	}
	if platform.requestCount() != 0 {
		t.Errorf("a request reached the platform: %v", platform.requests)
	}
}

func TestSSHKeyCreateFlagCombinations(t *testing.T) {
	cases := []struct {
		name  string
		flags map[string]string
		want  string
	}{
		{"no key source", map[string]string{"name": "k"}, "exactly one of"},
		{"two key sources", map[string]string{"name": "k", "public-key": maasPublicKey, "generate": "true"}, "exactly one of"},
		{"no name", map[string]string{"public-key": maasPublicKey}, "--name is required"},
		{"generate without a private key file", map[string]string{"name": "k", "generate": "true"}, "--private-key-file"},
		{"private key file without generate", map[string]string{"name": "k", "public-key": maasPublicKey, "private-key-file": "x"}, "only used with --generate"},
		{"two keys", map[string]string{"name": "k", "public-key": maasPublicKey + "\n" + maasPublicKey}, "found 2 keys"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			platform := &fakeSSHKeyPlatform{createCode: http.StatusCreated, createBody: `{"success":true}`}
			platform.serve(t)
			_, _, err := runSSHKeyCommand(t, credentialsSSHKeyCreateCmd, testCase.flags)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("err = %v, want it to mention %q", err, testCase.want)
			}
			if exitCodeFor(err) != exitUsage {
				t.Errorf("exit code = %d, want %d", exitCodeFor(err), exitUsage)
			}
			if platform.requestCount() != 0 {
				t.Errorf("a request reached the platform: %v", platform.requests)
			}
		})
	}
}

func TestSSHKeyCreateGenerateWritesThePrivateKeyFileAndKeepsItOutOfStructuredOutput(t *testing.T) {
	const generatedPrivateKey = "-----BEGIN RSA PRIVATE KEY-----\nMIIEfake\n-----END RSA PRIVATE KEY-----"
	platform := &fakeSSHKeyPlatform{
		createCode: http.StatusCreated,
		createBody: `{"success":true,"id":"bbbbbbbb-2222-4222-8222-222222222222","name":"break-glass","private_key":"` +
			strings.ReplaceAll(generatedPrivateKey, "\n", `\n`) + `","errors":[]}`,
	}
	platform.serve(t)
	privateKeyPath := filepath.Join(t.TempDir(), "break-glass.pem")

	stdout, structured, err := runSSHKeyCommand(t, credentialsSSHKeyCreateCmd, map[string]string{
		"name":             "break-glass",
		"generate":         "true",
		"private-key-file": privateKeyPath,
		"output":           "json",
	})
	if err != nil {
		t.Fatalf("create --generate failed: %v", err)
	}
	if platform.createdWith["generate"] != true {
		t.Errorf("generate was not sent: %#v", platform.createdWith)
	}
	if _, present := platform.createdWith["ssh_public_key"]; present {
		t.Errorf("a public key was sent with --generate: %#v", platform.createdWith)
	}
	written, readError := os.ReadFile(privateKeyPath)
	if readError != nil {
		t.Fatalf("private key file not written: %v", readError)
	}
	if strings.TrimSpace(string(written)) != generatedPrivateKey {
		t.Errorf("private key file holds %q", written)
	}
	info, _ := os.Stat(privateKeyPath)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("private key file mode = %v, want 0600", info.Mode().Perm())
	}
	if strings.Contains(structured+stdout, "PRIVATE KEY") || strings.Contains(structured+stdout, "MIIEfake") {
		t.Errorf("private key leaked into output:\n%s%s", stdout, structured)
	}
	var decoded sshKeyCreateResult
	if decodeError := json.Unmarshal([]byte(structured), &decoded); decodeError != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", decodeError, structured)
	}
	if !decoded.Generated || decoded.PrivateKeyFile != privateKeyPath || decoded.ID != "bbbbbbbb-2222-4222-8222-222222222222" {
		t.Errorf("unexpected structured result: %+v", decoded)
	}
}

func TestSSHKeyCreateGenerateRefusesAnExistingPrivateKeyFileBeforeAsking(t *testing.T) {
	platform := &fakeSSHKeyPlatform{createCode: http.StatusCreated, createBody: `{"success":true}`}
	platform.serve(t)
	privateKeyPath := filepath.Join(t.TempDir(), "existing.pem")
	if err := os.WriteFile(privateKeyPath, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := runSSHKeyCommand(t, credentialsSSHKeyCreateCmd, map[string]string{
		"name":             "break-glass",
		"generate":         "true",
		"private-key-file": privateKeyPath,
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing file was not refused: %v", err)
	}
	if platform.requestCount() != 0 {
		t.Errorf("the platform was asked to generate a key: %v", platform.requests)
	}
	if contents, _ := os.ReadFile(privateKeyPath); string(contents) != "keep me" {
		t.Errorf("existing file was overwritten: %q", contents)
	}
}

func TestSSHKeyCreateGenerateRemovesTheClaimedFileWhenThePlatformRefuses(t *testing.T) {
	platform := &fakeSSHKeyPlatform{
		createCode: http.StatusBadRequest,
		createBody: `{"detail":{"errors":[{"name":"break-glass","kind":"hetzner_ssh_key","errors":[{"key":"name","message":"A credential named 'break-glass' already exists"}]}]}}`,
	}
	platform.serve(t)
	privateKeyPath := filepath.Join(t.TempDir(), "break-glass.pem")

	_, _, err := runSSHKeyCommand(t, credentialsSSHKeyCreateCmd, map[string]string{
		"name":             "break-glass",
		"generate":         "true",
		"private-key-file": privateKeyPath,
	})
	if err == nil || !strings.Contains(err.Error(), "name: A credential named 'break-glass' already exists") {
		t.Fatalf("refusal not relayed: %v", err)
	}
	if _, statError := os.Stat(privateKeyPath); !os.IsNotExist(statError) {
		t.Errorf("an empty private key file was left behind: %v", statError)
	}
}

func TestCredentialsGetShowsTheSSHKeyItHolds(t *testing.T) {
	platform := &fakeSSHKeyPlatform{detailBody: `{"id":"66687213-9190-48d4-81d6-4748de371220","name":"maas","provider":"ssh-key",` +
		`"organisation_id":"org","available":true,"created_at":"2026-04-11T19:33:28Z","ssh_public_key":"` + maasPublicKey + `"}`}
	platform.serve(t)

	output := runCredentialsGet(t, "66687213-9190-48d4-81d6-4748de371220")
	for _, want := range []string{"Key type: ssh-ed25519", "Fingerprint: " + maasFingerprint, "Comment:  mattias@ankra.io", "Public key: " + maasPublicKey} {
		if !strings.Contains(output, want) {
			t.Errorf("credentials get lacks %q:\n%s", want, output)
		}
	}
}

func TestCredentialsGetPrefersThePlatformsFingerprint(t *testing.T) {
	platform := &fakeSSHKeyPlatform{detailBody: `{"id":"66687213-9190-48d4-81d6-4748de371220","name":"maas","provider":"ssh-key",` +
		`"organisation_id":"org","available":true,"created_at":"2026-04-11T19:33:28Z","ssh_public_key":"` + maasPublicKey + `","fingerprint":"SHA256:fromtheplatform"}`}
	platform.serve(t)

	output := runCredentialsGet(t, "66687213-9190-48d4-81d6-4748de371220")
	if !strings.Contains(output, "Fingerprint: SHA256:fromtheplatform") {
		t.Errorf("platform fingerprint not preferred:\n%s", output)
	}
}

func TestCredentialsGetPrintsNoFingerprintForAKeyItCannotParse(t *testing.T) {
	platform := &fakeSSHKeyPlatform{detailBody: `{"id":"66687213-9190-48d4-81d6-4748de371220","name":"odd","provider":"ssh-key",` +
		`"organisation_id":"org","available":true,"created_at":"2026-04-11T19:33:28Z","ssh_public_key":"not a key"}`}
	platform.serve(t)

	output := runCredentialsGet(t, "66687213-9190-48d4-81d6-4748de371220")
	if strings.Contains(output, "Fingerprint:") {
		t.Errorf("printed a fingerprint for an unparseable key:\n%s", output)
	}
	if !strings.Contains(output, "Public key: not a key") {
		t.Errorf("did not show what the platform returned:\n%s", output)
	}
}

func TestParseSSHPublicKey(t *testing.T) {
	parsed, ok := parseSSHPublicKey(maasPublicKey)
	if !ok || parsed.KeyType != "ssh-ed25519" || parsed.Fingerprint != maasFingerprint || parsed.Comment != "mattias@ankra.io" {
		t.Errorf("parse(maas) = %+v, %v", parsed, ok)
	}

	withOptions, ok := parseSSHPublicKey(`from="10.0.0.0/8",no-pty ` + maasPublicKey)
	if !ok || withOptions.Fingerprint != maasFingerprint {
		t.Errorf("authorized_keys options not skipped: %+v, %v", withOptions, ok)
	}

	noComment, ok := parseSSHPublicKey("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBbr+bc5f+n5kIIm++SkvmZ2LX2Coff+TO+DC69XWbfi")
	if !ok || noComment.Comment != "" || noComment.Fingerprint != maasFingerprint {
		t.Errorf("parse(no comment) = %+v, %v", noComment, ok)
	}

	for _, bad := range []string{
		"",
		"not a key",
		"ssh-ed25519 !!!notbase64",
		// The blob says ssh-ed25519; the line claims ssh-rsa.
		"ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAIBbr+bc5f+n5kIIm++SkvmZ2LX2Coff+TO+DC69XWbfi",
		"ssh-ed25519 AAAA",
	} {
		if parsed, ok := parseSSHPublicKey(bad); ok {
			t.Errorf("parse(%q) = %+v, want refusal", bad, parsed)
		}
	}
}

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// secretTestValue is the value every test feeds in. It must never appear in
// anything the command prints.
const secretTestValue = "s3cr3t-VALUE-never-printed"

const (
	secretTestSlotID = "0b6f2c1e-3c44-4b9e-9a51-2f1f6f0f9a10"
	secretTestOther  = "7d1e9a64-5b2f-4c0e-8f3a-9c6b1d2e4f50"
)

type secretsMock struct {
	baseMock
	slots           []client.SecretSlot
	created         []client.SecretSlotCreateRequest
	replacedIDs     []string
	replaced        []client.SecretSlotReplaceRequest
	deletedIDs      []string
	deleteReference *client.SecretSlotReferencedError
}

func (m *secretsMock) ListSecretSlots(ctx context.Context) (*client.SecretSlotListResult, error) {
	return &client.SecretSlotListResult{Slots: m.slots}, nil
}

func (m *secretsMock) CreateSecretSlotWith(ctx context.Context, request client.SecretSlotCreateRequest) (*client.SecretSlot, error) {
	m.created = append(m.created, request)
	createdVia := "user"
	if request.Generate != nil {
		createdVia = "generated"
	}
	slot := secretSlotFixture(secretTestSlotID, request.Label, createdVia)
	return &slot, nil
}

func (m *secretsMock) ReplaceSecretSlot(ctx context.Context, slotID string, request client.SecretSlotReplaceRequest) (*client.SecretSlot, error) {
	m.replacedIDs = append(m.replacedIDs, slotID)
	m.replaced = append(m.replaced, request)
	for _, slot := range m.slots {
		if slot.SlotID == slotID {
			return &slot, nil
		}
	}
	return nil, client.ErrSecretSlotNotFound
}

func (m *secretsMock) DeleteSecretSlot(ctx context.Context, slotID string) (*client.SecretSlotDeleteResult, error) {
	if m.deleteReference != nil {
		return nil, m.deleteReference
	}
	m.deletedIDs = append(m.deletedIDs, slotID)
	return &client.SecretSlotDeleteResult{Deleted: true}, nil
}

func secretSlotFixture(slotID, label, createdVia string) client.SecretSlot {
	return client.SecretSlot{
		SlotID:     slotID,
		Label:      label,
		CreatedAt:  "2026-10-22T10:00:00Z",
		Sentinel:   "${SECRET_SLOT:" + slotID + "}",
		CreatedVia: createdVia,
		References: []client.SecretSlotReference{},
	}
}

func referencedSlotFixture() client.SecretSlot {
	slot := secretSlotFixture(secretTestSlotID, "grafana-admin", "generated")
	pinned := "2026-10-22T11:00:00Z"
	slot.PinnedAt = &pinned
	slot.References = []client.SecretSlotReference{
		{ClusterName: "prod", ResourceKind: "addon", ResourceName: "grafana", DocumentPath: "adminPassword"},
		{ClusterName: "staging", ResourceKind: "manifest", ResourceName: "grafana-secret", DocumentPath: "stringData.password"},
	}
	return slot
}

// resetSecretsFlags restores every secrets subcommand's flags, so a test
// does not inherit --generate or -o from an earlier execution of the shared
// cobra tree.
func resetSecretsFlags(t *testing.T) {
	t.Helper()
	for _, command := range []*cobra.Command{secretsListCmd, secretsCreateCmd, secretsSetCmd, secretsRotateCmd, secretsDeleteCmd} {
		command.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	}
}

// executeSecrets runs the command with stdin and returns stdout and stderr
// separately.
func executeSecrets(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	resetSecretsFlags(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(args)
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	executeError := rootCmd.Execute()
	return stdout.String(), stderr.String(), executeError
}

func assertNoSecretValue(t *testing.T, outputs ...string) {
	t.Helper()
	for _, output := range outputs {
		if strings.Contains(output, secretTestValue) {
			t.Fatalf("the secret value reached the output: %q", output)
		}
	}
}

func TestSecretsListShowsMetadataNeverValues(t *testing.T) {
	mock := &secretsMock{slots: []client.SecretSlot{referencedSlotFixture(), secretSlotFixture(secretTestOther, "ci-token", "user")}}
	setMockClient(t, mock)

	stdout, _, executeError := executeSecrets(t, "", "secrets", "list")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	for _, want := range []string{"grafana-admin", secretTestSlotID, "generated", "prod/addon/grafana (+1 more)", "never (in use)", "ci-token", "never"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("list output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestSecretsListJSONIsParseable(t *testing.T) {
	setMockClient(t, &secretsMock{slots: []client.SecretSlot{referencedSlotFixture()}})

	stdout, _, executeError := executeSecrets(t, "", "secrets", "list", "-o", "json")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	var decoded client.SecretSlotListResult
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if len(decoded.Slots) != 1 || len(decoded.Slots[0].References) != 2 || decoded.Slots[0].Sentinel == "" {
		t.Errorf("decoded = %+v", decoded)
	}
	if strings.Contains(stdout, `"value"`) {
		t.Errorf("list JSON must carry no value field: %s", stdout)
	}
}

func TestSecretsListEmpty(t *testing.T) {
	setMockClient(t, &secretsMock{})
	stdout, _, executeError := executeSecrets(t, "", "secrets", "list")
	if executeError != nil || !strings.Contains(stdout, "No secrets") {
		t.Errorf("stdout = %q, error = %v", stdout, executeError)
	}
	stdout, _, executeError = executeSecrets(t, "", "secrets", "list", "-o", "json")
	if executeError != nil || !strings.Contains(stdout, `"slots": []`) {
		t.Errorf("empty JSON listing = %q, error = %v, want an empty array", stdout, executeError)
	}
}

func TestSecretsCreateGeneratePrintsOnlyTheSentinelOnStdout(t *testing.T) {
	mock := &secretsMock{}
	setMockClient(t, mock)

	stdout, stderr, executeError := executeSecrets(t, secretTestValue, "secrets", "create", "db-password", "--generate", "--length", "48", "--ttl", "72h")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if stdout != "${SECRET_SLOT:"+secretTestSlotID+"}\n" {
		t.Errorf("stdout = %q, want the sentinel alone", stdout)
	}
	if !strings.Contains(stderr, `Created secret "db-password"`) {
		t.Errorf("stderr = %q", stderr)
	}
	if len(mock.created) != 1 {
		t.Fatalf("created = %+v", mock.created)
	}
	request := mock.created[0]
	if request.Value != "" || request.Generate == nil || request.Generate.Length != 48 || request.TTLSeconds != 72*3600 {
		t.Errorf("request = %+v, want generate 48 with a 72h ttl and no value (stdin ignored)", request)
	}
}

func TestSecretsCreateFromStdin(t *testing.T) {
	mock := &secretsMock{}
	setMockClient(t, mock)

	stdout, stderr, executeError := executeSecrets(t, secretTestValue+"\n", "secrets", "create", "ci-token", "--hint", "CI token")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	assertNoSecretValue(t, stdout, stderr)
	if len(mock.created) != 1 || mock.created[0].Value != secretTestValue || mock.created[0].Generate != nil || mock.created[0].Hint != "CI token" {
		t.Errorf("created = %+v, want the piped value without its line ending", mock.created)
	}
}

func TestSecretsCreateFromFileKeepsTheBytes(t *testing.T) {
	mock := &secretsMock{}
	setMockClient(t, mock)
	path := filepath.Join(t.TempDir(), "value")
	if writeError := os.WriteFile(path, []byte(secretTestValue+"\n"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}

	stdout, stderr, executeError := executeSecrets(t, "", "secrets", "create", "pem", "--from-file", path, "-o", "json")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	assertNoSecretValue(t, stdout, stderr)
	if len(mock.created) != 1 || mock.created[0].Value != secretTestValue+"\n" {
		t.Errorf("created = %+v, want the file's bytes exactly", mock.created)
	}
	var slot client.SecretSlot
	if decodeError := json.Unmarshal([]byte(stdout), &slot); decodeError != nil || slot.Sentinel == "" {
		t.Errorf("stdout = %q (%v), want the slot as JSON", stdout, decodeError)
	}
}

func TestSecretsRefuseAValueInArgv(t *testing.T) {
	for _, args := range [][]string{
		{"secrets", "create", "ci-token", secretTestValue},
		{"secrets", "set", "ci-token", secretTestValue},
		{"secrets", "rotate", "ci-token", secretTestValue},
	} {
		mock := &secretsMock{slots: []client.SecretSlot{secretSlotFixture(secretTestSlotID, "ci-token", "user")}}
		setMockClient(t, mock)
		stdout, stderr, executeError := executeSecrets(t, "", args...)
		if executeError == nil {
			t.Fatalf("%v: a value in argv was accepted", args)
		}
		if code := exitCodeFor(executeError); code != exitUsage {
			t.Errorf("%v: exit code = %d, want %d", args, code, exitUsage)
		}
		assertNoSecretValue(t, stdout, stderr, executeError.Error())
		if len(mock.created)+len(mock.replaced) != 0 {
			t.Errorf("%v: the platform was called", args)
		}
	}
}

func TestSecretsCreateHasNoValueFlag(t *testing.T) {
	mock := &secretsMock{}
	setMockClient(t, mock)
	stdout, stderr, executeError := executeSecrets(t, "", "secrets", "create", "ci-token", "--value="+secretTestValue)
	if executeError == nil {
		t.Fatal("--value was accepted")
	}
	assertNoSecretValue(t, stdout, stderr, executeError.Error())
	if len(mock.created) != 0 {
		t.Error("the platform was called")
	}
}

func TestSecretsCreateRefusesAnEmptyValue(t *testing.T) {
	mock := &secretsMock{}
	setMockClient(t, mock)
	_, _, executeError := executeSecrets(t, "\n", "secrets", "create", "ci-token")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Errorf("error = %v, want a usage error", executeError)
	}
	if len(mock.created) != 0 {
		t.Error("the platform was called")
	}
}

func TestSecretsCreateGenerateFlagsNeedGenerate(t *testing.T) {
	setMockClient(t, &secretsMock{})
	_, _, executeError := executeSecrets(t, secretTestValue, "secrets", "create", "x", "--length", "40")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Errorf("error = %v, want a usage error", executeError)
	}
	_, _, executeError = executeSecrets(t, "", "secrets", "create", "x", "--generate", "--length", "8")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Errorf("error = %v, want a usage error for a short length", executeError)
	}
}

func TestSecretsSetResolvesTheNameAndReplacesFromStdin(t *testing.T) {
	mock := &secretsMock{slots: []client.SecretSlot{
		secretSlotFixture(secretTestOther, "other", "user"),
		secretSlotFixture(secretTestSlotID, "stripe-key", "user"),
	}}
	setMockClient(t, mock)

	stdout, stderr, executeError := executeSecrets(t, secretTestValue, "secrets", "set", "stripe-key")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	assertNoSecretValue(t, stdout, stderr)
	if len(mock.replacedIDs) != 1 || mock.replacedIDs[0] != secretTestSlotID || mock.replaced[0].Value != secretTestValue || mock.replaced[0].Generate != nil {
		t.Errorf("replaced = %v %+v", mock.replacedIDs, mock.replaced)
	}
	if !strings.Contains(stdout, "in place") || !strings.Contains(stdout, "${SECRET_SLOT:"+secretTestSlotID+"}") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestSecretsSetHasNoGenerateFlag(t *testing.T) {
	setMockClient(t, &secretsMock{slots: []client.SecretSlot{secretSlotFixture(secretTestSlotID, "stripe-key", "user")}})
	_, _, executeError := executeSecrets(t, "", "secrets", "set", "stripe-key", "--generate")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Errorf("error = %v, want a usage error", executeError)
	}
}

func TestSecretsRotateGenerateBySentinel(t *testing.T) {
	mock := &secretsMock{slots: []client.SecretSlot{referencedSlotFixture()}}
	setMockClient(t, mock)

	stdout, _, executeError := executeSecrets(t, secretTestValue, "secrets", "rotate", "${SECRET_SLOT:"+secretTestSlotID+"}", "--generate", "--prefix", "pw_")
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if len(mock.replaced) != 1 || mock.replaced[0].Value != "" || mock.replaced[0].Generate == nil ||
		mock.replaced[0].Generate.Prefix != "pw_" || mock.replaced[0].Generate.Length != 0 {
		t.Errorf("replaced = %+v, want generate with the prefix, the platform's length and no value", mock.replaced)
	}
	if !strings.Contains(stdout, "the 2 resource(s) that reference it") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestSecretsRotateUnknownIsNotFound(t *testing.T) {
	mock := &secretsMock{slots: []client.SecretSlot{secretSlotFixture(secretTestSlotID, "stripe-key", "user")}}
	setMockClient(t, mock)
	_, _, executeError := executeSecrets(t, secretTestValue, "secrets", "rotate", "nope", "--generate")
	if executeError == nil || exitCodeFor(executeError) != exitNotFound {
		t.Errorf("error = %v, want not found", executeError)
	}
	if len(mock.replaced) != 0 {
		t.Error("the platform was called")
	}
}

func TestSecretsDeleteRefusesAReferencedSecretAndNamesItsConsumers(t *testing.T) {
	mock := &secretsMock{slots: []client.SecretSlot{referencedSlotFixture()}}
	setMockClient(t, mock)

	_, stderr, executeError := executeSecrets(t, "", "secrets", "delete", "grafana-admin", "--yes")
	if executeError == nil {
		t.Fatal("a referenced secret was deleted")
	}
	if exitCodeFor(executeError) != exitError {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitError)
	}
	for _, want := range []string{"cluster prod: addon grafana at adminPassword", "cluster staging: manifest grafana-secret at stringData.password"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if len(mock.deletedIDs) != 0 {
		t.Error("the delete was sent")
	}
}

func TestSecretsDeleteRaceNamesTheConsumers(t *testing.T) {
	mock := &secretsMock{
		slots:           []client.SecretSlot{secretSlotFixture(secretTestSlotID, "ci-token", "user")},
		deleteReference: &client.SecretSlotReferencedError{Detail: "Secret slot is referenced by addon grafana"},
	}
	setMockClient(t, mock)
	_, _, executeError := executeSecrets(t, "", "secrets", "delete", "ci-token", "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "referenced by addon grafana") {
		t.Errorf("error = %v, want the platform's refusal", executeError)
	}
}

func TestSecretsDeleteConfirmsAndDeletes(t *testing.T) {
	mock := &secretsMock{slots: []client.SecretSlot{secretSlotFixture(secretTestSlotID, "ci-token", "user")}}
	setMockClient(t, mock)

	_, _, executeError := executeSecrets(t, "n\n", "secrets", "delete", "ci-token")
	if exitCodeFor(executeError) != exitCancelled || len(mock.deletedIDs) != 0 {
		t.Errorf("declined prompt: error = %v, deleted = %v", executeError, mock.deletedIDs)
	}

	stdout, _, executeError := executeSecrets(t, "y\n", "secrets", "delete", secretTestSlotID)
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if len(mock.deletedIDs) != 1 || mock.deletedIDs[0] != secretTestSlotID || !strings.Contains(stdout, `Deleted secret "ci-token"`) {
		t.Errorf("deleted = %v, stdout = %q", mock.deletedIDs, stdout)
	}
}

// TestSecretsAgainstAFakeAPI drives create, set, rotate, list and delete
// through the real HTTP client against a fake platform, and checks the
// value travels only in the request body: never in the URL, a header or
// anything the command prints.
func TestSecretsAgainstAFakeAPI(t *testing.T) {
	slot := map[string]any{
		"slot_id": secretTestSlotID, "label": "ci-token", "hint": nil,
		"created_at": "2026-10-22T10:00:00Z", "expires_at": nil, "last_accessed_at": nil,
		"access_count": 0, "sentinel": "${SECRET_SLOT:" + secretTestSlotID + "}",
		"created_via": "user", "pinned_at": nil, "references": []any{}, "created_by_user_id": nil,
	}
	var bodies []string
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		requests = append(requests, r.Method+" "+r.URL.String())
		for name, values := range r.Header {
			for _, value := range values {
				if strings.Contains(value, secretTestValue) {
					t.Errorf("header %s carries the value", name)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"slots": []any{slot}})
		case r.Method == http.MethodDelete:
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
		default:
			_ = json.NewEncoder(w).Encode(slot)
		}
	}))
	t.Cleanup(server.Close)
	setMockClient(t, client.New("test-token", server.URL))

	steps := []struct {
		stdin string
		args  []string
	}{
		{secretTestValue, []string{"secrets", "create", "ci-token"}},
		{secretTestValue, []string{"secrets", "set", "ci-token"}},
		{"", []string{"secrets", "rotate", "ci-token", "--generate"}},
		{"", []string{"secrets", "list", "-o", "json"}},
		{"", []string{"secrets", "delete", "ci-token", "--yes"}},
	}
	for _, step := range steps {
		stdout, stderr, executeError := executeSecrets(t, step.stdin, step.args...)
		if executeError != nil {
			t.Fatalf("%v: %v", step.args, executeError)
		}
		assertNoSecretValue(t, stdout, stderr)
	}
	for _, request := range requests {
		if strings.Contains(request, secretTestValue) {
			t.Errorf("the value reached a URL: %s", request)
		}
	}
	valueBodies := 0
	for _, body := range bodies {
		if strings.Contains(body, secretTestValue) {
			valueBodies++
		}
	}
	if valueBodies != 2 {
		t.Errorf("the value travelled in %d request bodies, want 2 (create and set): %v", valueBodies, requests)
	}
	wantPrefix := []string{"POST /api/v1/org/secret-slots", "GET /api/v1/org/secret-slots", "PUT /api/v1/org/secret-slots/" + secretTestSlotID}
	for index, want := range wantPrefix {
		if requests[index] != want {
			t.Errorf("request %d = %q, want %q", index, requests[index], want)
		}
	}
	if last := requests[len(requests)-1]; last != "DELETE /api/v1/org/secret-slots/"+secretTestSlotID {
		t.Errorf("last request = %q", last)
	}
}

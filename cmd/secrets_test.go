package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	slots []client.SecretSlot
	// relisted, when set, is what every listing after the first answers:
	// the organisation as it stands once a resource started referencing a
	// secret between the command's first read and its write.
	relisted        []client.SecretSlot
	listCalls       int
	created         []client.SecretSlotCreateRequest
	replacedIDs     []string
	replaced        []client.SecretSlotReplaceRequest
	deletedIDs      []string
	deleteReference *client.SecretSlotReferencedError
}

func (m *secretsMock) ListSecretSlots(ctx context.Context) (*client.SecretSlotListResult, error) {
	m.listCalls++
	if m.relisted != nil && m.listCalls > 1 {
		return &client.SecretSlotListResult{Slots: m.relisted}, nil
	}
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

// The write methods answer an expired context the way the real client
// does: the request is never sent.
func (m *secretsMock) ReplaceSecretSlot(ctx context.Context, slotID string, request client.SecretSlotReplaceRequest) (*client.SecretSlot, error) {
	if contextError := ctx.Err(); contextError != nil {
		return nil, contextError
	}
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
	if contextError := ctx.Err(); contextError != nil {
		return nil, contextError
	}
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
	return executeSecretsWithInput(t, strings.NewReader(stdin), args...)
}

// executeSecretsWithInput is executeSecrets reading stdin from input.
func executeSecretsWithInput(t *testing.T, input io.Reader, args ...string) (string, string, error) {
	t.Helper()
	resetSecretsFlags(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(input)
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

// A generated secret has no expiry unless --ttl is given (ankra-e1ifg.28):
// without the flag the request carries no ttl_seconds, so the platform
// stores it without one, and the help says so.
func TestSecretsCreateGenerateWithoutTTLSendsNoExpiry(t *testing.T) {
	mock := &secretsMock{}
	setMockClient(t, mock)

	if _, _, executeError := executeSecrets(t, "", "secrets", "create", "db-password", "--generate"); executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if len(mock.created) != 1 || mock.created[0].Generate == nil || mock.created[0].TTLSeconds != 0 {
		t.Fatalf("created = %+v, want a generate request without a ttl", mock.created)
	}
	encoded, marshalError := json.Marshal(mock.created[0])
	if marshalError != nil || strings.Contains(string(encoded), "ttl_seconds") {
		t.Errorf("request body = %s (%v), want no ttl_seconds", encoded, marshalError)
	}

	help := secretsCreateCmd.Long + "\n" + secretsCreateCmd.Flags().Lookup("ttl").Usage
	for _, stale := range []string{"one day", "defaults to 24h"} {
		if strings.Contains(help, stale) {
			t.Errorf("create help still says a generated secret expires by default (%q): %s", stale, help)
		}
	}
	for _, want := range []string{"does not expire", "unless --ttl is given", "at most a week", "at most 168h"} {
		if !strings.Contains(help, want) {
			t.Errorf("create help is missing %q: %s", want, help)
		}
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

func TestSecretsCreateValidatesHintAndTTL(t *testing.T) {
	mock := &secretsMock{}
	setMockClient(t, mock)
	for _, args := range [][]string{
		{"secrets", "create", "x", "--generate", "--ttl", "200h"},
		{"secrets", "create", "x", "--generate", "--hint", strings.Repeat("h", 241)},
		{"secrets", "create", "x", "--generate", "--ttl", "0s"},
	} {
		_, _, executeError := executeSecrets(t, "", args...)
		if executeError == nil || exitCodeFor(executeError) != exitUsage {
			t.Errorf("%v: error = %v, want a usage error", args, executeError)
		}
	}
	if len(mock.created) != 0 {
		t.Error("the platform was called")
	}
}

func TestSecretsDeleteRefusesASecondArgument(t *testing.T) {
	mock := &secretsMock{slots: []client.SecretSlot{secretSlotFixture(secretTestSlotID, "ci-token", "user")}}
	setMockClient(t, mock)
	stdout, stderr, executeError := executeSecrets(t, "", "secrets", "delete", "ci-token", secretTestValue, "--yes")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Errorf("error = %v, want a usage error", executeError)
	}
	assertNoSecretValue(t, stdout, stderr, executeError.Error())
	if len(mock.deletedIDs) != 0 {
		t.Error("the delete was sent")
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
	stdout, stderr, executeError := executeSecrets(t, "", "secrets", "rotate", secretTestValue, "--generate")
	if executeError == nil || exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("error = %v, want not found", executeError)
	}
	// A value typed where the name belongs is not echoed back.
	assertNoSecretValue(t, stdout, stderr, executeError.Error())
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

// TestSecretsDeleteRaceNamesTheConsumers: the listing the name resolves from
// shows no reference, a resource starts referencing the secret before the
// delete arrives, and the platform refuses it (409) with a sentence that
// names only the first consumer. The command reads the references again and
// names every one of them.
func TestSecretsDeleteRaceNamesTheConsumers(t *testing.T) {
	mock := &secretsMock{
		slots:           []client.SecretSlot{secretSlotFixture(secretTestSlotID, "grafana-admin", "generated")},
		relisted:        []client.SecretSlot{referencedSlotFixture()},
		deleteReference: &client.SecretSlotReferencedError{Detail: "Secret slot is referenced by addon grafana"},
	}
	setMockClient(t, mock)
	_, stderr, executeError := executeSecrets(t, "", "secrets", "delete", "grafana-admin", "--yes")
	if executeError == nil {
		t.Fatal("the platform's refusal was reported as a delete")
	}
	if mock.listCalls != 2 {
		t.Errorf("listed %d time(s), want 2: the resolve and the re-read after the refusal", mock.listCalls)
	}
	for _, want := range []string{
		`Secret "grafana-admin" is referenced by 2 deployed resource(s)`,
		"cluster prod: addon grafana at adminPassword",
		"cluster staging: manifest grafana-secret at stringData.password",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	for _, want := range []string{"still referenced, so nothing was deleted", "${SECRET_SLOT:" + secretTestSlotID + "}", "referenced by addon grafana"} {
		if !strings.Contains(executeError.Error(), want) {
			t.Errorf("error = %q, want it to carry %q", executeError, want)
		}
	}
	if code := exitCodeFor(executeError); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
}

// TestSecretsDeleteRaceFallsBackToThePlatformsSentence: when the re-read
// after a 409 still shows no reference, the platform's own sentence is the
// refusal.
func TestSecretsDeleteRaceFallsBackToThePlatformsSentence(t *testing.T) {
	mock := &secretsMock{
		slots:           []client.SecretSlot{secretSlotFixture(secretTestSlotID, "ci-token", "user")},
		deleteReference: &client.SecretSlotReferencedError{Detail: "Secret slot is referenced by addon grafana"},
	}
	setMockClient(t, mock)
	_, _, executeError := executeSecrets(t, "", "secrets", "delete", "ci-token", "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "referenced by addon grafana") {
		t.Errorf("error = %v, want the platform's refusal", executeError)
	}
	if mock.listCalls != 2 {
		t.Errorf("listed %d time(s), want 2: the resolve and the re-read after the refusal", mock.listCalls)
	}
}

// slowReader stands in for a person at a prompt: the first read answers
// only after delay.
type slowReader struct {
	delay  time.Duration
	reader io.Reader
	waited bool
}

func (slow *slowReader) Read(buffer []byte) (int, error) {
	if !slow.waited {
		time.Sleep(slow.delay)
		slow.waited = true
	}
	return slow.reader.Read(buffer)
}

// TestSecretsRequestBudgetStartsAfterThePerson: the request timeout of set,
// rotate and delete starts once the value or the confirmation is in hand,
// so a person who takes longer than the timeout to answer still gets the
// request sent, not a deadline error for a request that never left.
func TestSecretsRequestBudgetStartsAfterThePerson(t *testing.T) {
	original := secretsRequestTimeout
	secretsRequestTimeout = 200 * time.Millisecond
	t.Cleanup(func() { secretsRequestTimeout = original })

	for _, step := range []struct {
		name  string
		stdin string
		args  []string
	}{
		{"set waits for the value", secretTestValue, []string{"secrets", "set", "ci-token"}},
		{"rotate waits for the value", secretTestValue, []string{"secrets", "rotate", "ci-token"}},
		{"delete waits for the confirmation", "y\n", []string{"secrets", "delete", "ci-token"}},
	} {
		t.Run(step.name, func(t *testing.T) {
			mock := &secretsMock{slots: []client.SecretSlot{secretSlotFixture(secretTestSlotID, "ci-token", "user")}}
			setMockClient(t, mock)
			input := &slowReader{delay: 3 * secretsRequestTimeout, reader: strings.NewReader(step.stdin)}
			stdout, stderr, executeError := executeSecretsWithInput(t, input, step.args...)
			if executeError != nil {
				t.Fatalf("%v: %v (the request budget ran out while the person was answering)", step.args, executeError)
			}
			assertNoSecretValue(t, stdout, stderr)
			if sent := len(mock.replacedIDs) + len(mock.deletedIDs); sent != 1 {
				t.Errorf("%v: %d write(s) sent, want 1", step.args, sent)
			}
		})
	}
}

// TestSecretsPermissionDeniedNamesThePermission drives the commands through
// the real HTTP client against a platform that answers the write with its
// RBAC 403 (ankra-e1ifg.18): set, rotate and delete need secrets.manage,
// create needs variables.write. The refusal names the permission and who
// can grant it, exits 7, is sent once (never retried), keeps stdout empty
// and never carries the value read from stdin or --from-file.
func TestSecretsPermissionDeniedNamesThePermission(t *testing.T) {
	valuePath := filepath.Join(t.TempDir(), "value")
	if writeError := os.WriteFile(valuePath, []byte(secretTestValue), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	slot := map[string]any{
		"slot_id": secretTestSlotID, "label": "ci-token", "created_at": "2026-10-22T10:00:00Z",
		"sentinel": "${SECRET_SLOT:" + secretTestSlotID + "}", "created_via": "user", "references": []any{},
	}
	for _, testCase := range []struct {
		name        string
		stdin       string
		args        []string
		writeMethod string
		permission  string
	}{
		{"set from stdin", secretTestValue, []string{"secrets", "set", "ci-token"}, http.MethodPut, "secrets.manage"},
		{"set from a file", "", []string{"secrets", "set", "ci-token", "--from-file", valuePath}, http.MethodPut, "secrets.manage"},
		{"rotate from stdin as json", secretTestValue, []string{"secrets", "rotate", "ci-token", "-o", "json"}, http.MethodPut, "secrets.manage"},
		{"rotate from a file", "", []string{"secrets", "rotate", "ci-token", "--from-file", valuePath}, http.MethodPut, "secrets.manage"},
		{"rotate generated", secretTestValue, []string{"secrets", "rotate", "ci-token", "--generate"}, http.MethodPut, "secrets.manage"},
		{"delete", "", []string{"secrets", "delete", "ci-token", "--yes"}, http.MethodDelete, "secrets.manage"},
		{"delete as json", "", []string{"secrets", "delete", "ci-token", "--yes", "-o", "json"}, http.MethodDelete, "secrets.manage"},
		{"create from stdin", secretTestValue, []string{"secrets", "create", "ci-token"}, http.MethodPost, "variables.write"},
		{"create from a file as json", "", []string{"secrets", "create", "ci-token", "--from-file", valuePath, "-o", "json"}, http.MethodPost, "variables.write"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(map[string]any{"slots": []any{slot}})
					return
				}
				if r.Method != testCase.writeMethod {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				writes.Add(1)
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"detail": "permission_denied", "permission": testCase.permission, "scope_type": "organisation",
				})
			}))
			t.Cleanup(server.Close)
			setMockClient(t, client.New("test-token", server.URL))

			stdout, stderr, executeError := executeSecrets(t, testCase.stdin, testCase.args...)
			if executeError == nil {
				t.Fatal("the refusal was reported as success")
			}
			if code := exitCodeFor(executeError); code != exitForbidden {
				t.Errorf("exit code = %d, want %d (permission denied)", code, exitForbidden)
			}
			want := fmt.Sprintf("permission denied: this action requires the %q permission. Ask an organisation admin to change your role",
				testCase.permission)
			if !strings.Contains(executeError.Error(), want) {
				t.Errorf("error = %q, want it to carry %q", executeError, want)
			}
			if sent := writes.Load(); sent != 1 {
				t.Errorf("the write was sent %d time(s), want once: a refusal is never retried", sent)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing on a refusal", stdout)
			}
			assertNoSecretValue(t, stdout, stderr, executeError.Error())
		})
	}
}

// TestSecretsHelpNamesThePermissions: each command's help says which
// permission it needs and who holds it.
func TestSecretsHelpNamesThePermissions(t *testing.T) {
	for _, command := range []*cobra.Command{secretsSetCmd, secretsRotateCmd, secretsDeleteCmd} {
		if !strings.Contains(command.Long, "secrets.manage permission, which the organisation owner and admin\nroles hold") {
			t.Errorf("%s help does not name secrets.manage and its holders:\n%s", command.Name(), command.Long)
		}
	}
	if !strings.Contains(secretsCreateCmd.Long, "variables.write permission, which every role but viewer holds") {
		t.Errorf("create help does not name variables.write:\n%s", secretsCreateCmd.Long)
	}
	if !strings.Contains(secretsCmd.Long, "variables.write") || !strings.Contains(secretsCmd.Long, "secrets.manage") {
		t.Errorf("secrets help does not name both permissions:\n%s", secretsCmd.Long)
	}
}

// TestSecretsReplaceExpiredRelaysThePlatformsSentence: replacing the value
// of a secret that expired and is due for deletion is refused with 410 Gone
// (ankra-e1ifg.23). The platform's sentence, which names the expiry and the
// way out, is the whole message: no status line, no raw body.
func TestSecretsReplaceExpiredRelaysThePlatformsSentence(t *testing.T) {
	const expiredDetail = `Secret "ci-token" expired at 2026-11-01 10:00 UTC and is due for deletion; ` +
		`create a new secret instead.`
	slot := map[string]any{
		"slot_id": secretTestSlotID, "label": "ci-token", "created_at": "2026-10-22T10:00:00Z",
		"expires_at": "2026-11-01T10:00:00Z", "sentinel": "${SECRET_SLOT:" + secretTestSlotID + "}",
		"created_via": "user", "references": []any{},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"slots": []any{slot}})
			return
		}
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]any{"detail": expiredDetail, "error_code": "secret_slot_expired"})
	}))
	t.Cleanup(server.Close)
	setMockClient(t, client.New("test-token", server.URL))

	for _, step := range []struct {
		stdin string
		args  []string
	}{
		{secretTestValue, []string{"secrets", "set", "ci-token"}},
		{"", []string{"secrets", "rotate", "ci-token", "--generate", "-o", "json"}},
	} {
		stdout, stderr, executeError := executeSecrets(t, step.stdin, step.args...)
		if executeError == nil {
			t.Fatalf("%v: the refusal was reported as success", step.args)
		}
		if !strings.HasSuffix(executeError.Error(), ": "+expiredDetail) {
			t.Errorf("%v: error = %q, want it to end with the platform's sentence", step.args, executeError)
		}
		for _, unwanted := range []string{"status 410", "body:", "error_code", "{"} {
			if strings.Contains(executeError.Error(), unwanted) {
				t.Errorf("%v: error = %q carries %q", step.args, executeError, unwanted)
			}
		}
		if code := exitCodeFor(executeError); code != exitError {
			t.Errorf("%v: exit code = %d, want %d (a refused request)", step.args, code, exitError)
		}
		if stdout != "" {
			t.Errorf("%v: stdout = %q, want nothing on a refusal", step.args, stdout)
		}
		assertNoSecretValue(t, stdout, stderr, executeError.Error())
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
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"slots": []any{slot}})
		case http.MethodDelete:
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

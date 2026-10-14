package cmd

// The verified-provenance commands against a scripted platform: the real
// client talks to an httptest server, so the method, path and body each verb
// sends are pinned on the request itself.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

const testProvenanceApplicationID = "23298741-6a5a-401a-a681-66f31fbdebe1"

type provenanceRequest struct {
	method string
	path   string
	body   map[string]any
}

type provenanceServer struct {
	mutex       sync.Mutex
	requests    []provenanceRequest
	orgMode     *string
	appMode     *string
	verifyBody  string
	keysBody    string
	status      int
	statusBody  string
}

func newProvenanceServer(t *testing.T) *provenanceServer {
	t.Helper()
	recorder := &provenanceServer{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		recorded := provenanceRequest{method: request.Method, path: request.URL.Path}
		if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &recorded.body); decodeError != nil {
				t.Errorf("the request body is not a JSON object: %v: %s", decodeError, raw)
			}
		}
		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		recorder.requests = append(recorder.requests, recorded)
		if recorder.status != 0 {
			writer.WriteHeader(recorder.status)
			_, _ = writer.Write([]byte(recorder.statusBody))
			return
		}
		pathValue := request.URL.Path
		write := func(value any) { _ = json.NewEncoder(writer).Encode(value) }
		switch {
		case pathValue == "/api/v1/org/provenance/policy":
			if request.Method == http.MethodPut {
				recorder.orgMode = modeFromBody(t, recorded.body)
			}
			write(map[string]any{"mode": recorder.orgMode})
		case strings.HasSuffix(pathValue, "/provenance-policy"):
			if request.Method == http.MethodPut {
				recorder.appMode = modeFromBody(t, recorded.body)
			}
			effective, source := "off", "default"
			switch {
			case recorder.appMode != nil:
				effective, source = *recorder.appMode, "application"
			case recorder.orgMode != nil:
				effective, source = *recorder.orgMode, "organisation"
			}
			write(map[string]any{"application_id": testProvenanceApplicationID, "mode": effective, "source": source,
				"application_mode": recorder.appMode, "organisation_mode": recorder.orgMode})
		case strings.HasSuffix(pathValue, "/provenance/verify"):
			_, _ = writer.Write([]byte(recorder.verifyBody))
		case pathValue == "/api/v1/org/provenance/keys":
			_, _ = writer.Write([]byte(recorder.keysBody))
		case pathValue == "/api/v1/org/provenance/keys/rotate":
			writer.WriteHeader(http.StatusCreated)
			write(map[string]any{"key_id": "k2", "public_key_pem": "PEM2", "generation": 2, "status": "active", "revoked_at": nil})
		case pathValue == "/api/v1/org/provenance/keys/revoke":
			write(map[string]any{"key_id": recorded.body["key_id"], "public_key_pem": "PEM1", "generation": 1,
				"status": "revoked", "revoked_at": "2026-10-14T10:00:00Z"})
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"Not Found"}`))
		}
	}))
	t.Cleanup(server.Close)
	setMockClient(t, client.New("test-token", server.URL))
	return recorder
}

func modeFromBody(t *testing.T, body map[string]any) *string {
	t.Helper()
	value, present := body["mode"]
	if !present {
		t.Errorf("the write did not name mode: %v", body)
		return nil
	}
	if value == nil {
		return nil
	}
	text := value.(string)
	return &text
}

func (recorder *provenanceServer) seen() []provenanceRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]provenanceRequest{}, recorder.requests...)
}

func runProvenance(t *testing.T, stdin string, arguments ...string) (string, string, error) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(arguments)
	var commands []*cobra.Command
	for _, group := range []*cobra.Command{orgProvenanceCmd, findSubcommand(t, applicationCmd(t), "provenance")} {
		commands = append(commands, group)
		commands = append(commands, group.Commands()...)
	}
	resetTreeFlags(t, commands...)
	t.Cleanup(func() { resetTreeFlags(t, commands...) })
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func applicationCmd(t *testing.T) *cobra.Command {
	t.Helper()
	return findSubcommand(t, rootCmd, "application")
}

func TestApplicationProvenanceGetUnderEnforceSaysOlderVersionsCannotBeRolledBack(t *testing.T) {
	recorder := newProvenanceServer(t)
	enforce := "enforce"
	recorder.appMode = &enforce
	stdout, _, runError := runProvenance(t, "", "application", "provenance", "get", testProvenanceApplicationID)
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	for _, want := range []string{"Effective mode:     enforce (this application's own setting)",
		"cannot be deployed or", "rolled back to", "ankra application provenance set " + testProvenanceApplicationID + " --mode warn"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("get output is missing %q:\n%s", want, stdout)
		}
	}
	seen := recorder.seen()
	if len(seen) != 1 || seen[0].method != http.MethodGet ||
		seen[0].path != "/api/v1/org/applications/"+testProvenanceApplicationID+"/provenance-policy" {
		t.Errorf("unexpected requests: %+v", seen)
	}
}

func TestApplicationProvenanceGetUnderWarnHasNoRollbackCaveat(t *testing.T) {
	recorder := newProvenanceServer(t)
	warn := "warn"
	recorder.orgMode = &warn
	stdout, _, runError := runProvenance(t, "", "application", "provenance", "get", testProvenanceApplicationID)
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	if !strings.Contains(stdout, "warn (the organisation default)") || strings.Contains(stdout, "rolled back") {
		t.Errorf("unexpected output:\n%s", stdout)
	}
}

func TestApplicationProvenanceGetJSONKeepsStdoutClean(t *testing.T) {
	newProvenanceServer(t)
	stdout, _, runError := runProvenance(t, "", "application", "provenance", "get", testProvenanceApplicationID, "-o", "json")
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	var decoded map[string]any
	if jsonError := json.Unmarshal([]byte(stdout), &decoded); jsonError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", jsonError, stdout)
	}
	if decoded["mode"] != "off" {
		t.Errorf("unexpected mode: %v", decoded)
	}
}

func TestApplicationProvenanceSetSendsTheMode(t *testing.T) {
	recorder := newProvenanceServer(t)
	stdout, _, runError := runProvenance(t, "", "application", "provenance", "set", testProvenanceApplicationID, "--mode", "warn")
	if runError != nil {
		t.Fatalf("set failed: %v", runError)
	}
	seen := recorder.seen()
	if len(seen) != 1 || seen[0].method != http.MethodPut || seen[0].body["mode"] != "warn" {
		t.Errorf("unexpected requests: %+v", seen)
	}
	if !strings.Contains(stdout, "warn (this application's own setting)") {
		t.Errorf("unexpected output:\n%s", stdout)
	}
}

func TestApplicationProvenanceSetEnforceAsksFirst(t *testing.T) {
	recorder := newProvenanceServer(t)
	_, _, runError := runProvenance(t, "n\n", "application", "provenance", "set", testProvenanceApplicationID, "--mode", "enforce")
	if exitCodeFor(runError) != exitCancelled {
		t.Fatalf("declining must exit %d, got %v", exitCancelled, runError)
	}
	if len(recorder.seen()) != 0 {
		t.Errorf("a declined enforce must send nothing: %+v", recorder.seen())
	}
	stdout, _, runError := runProvenance(t, "y\n", "application", "provenance", "set", testProvenanceApplicationID, "--mode", "enforce")
	if runError != nil {
		t.Fatalf("confirmed enforce failed: %v", runError)
	}
	if !strings.Contains(stdout, "cannot be deployed or rolled back to") && !strings.Contains(stdout, "rolled back to") {
		t.Errorf("the prompt must name the rollback cost:\n%s", stdout)
	}
	if recorder.seen()[0].body["mode"] != "enforce" {
		t.Errorf("unexpected request: %+v", recorder.seen())
	}
}

func TestApplicationProvenanceSetEnforceWithYesSkipsThePrompt(t *testing.T) {
	recorder := newProvenanceServer(t)
	if _, _, runError := runProvenance(t, "", "application", "provenance", "set", testProvenanceApplicationID,
		"--mode", "enforce", "--yes"); runError != nil {
		t.Fatalf("set failed: %v", runError)
	}
	if len(recorder.seen()) != 1 {
		t.Errorf("unexpected requests: %+v", recorder.seen())
	}
}

func TestApplicationProvenanceSetClearSendsNull(t *testing.T) {
	recorder := newProvenanceServer(t)
	warn := "warn"
	recorder.appMode = &warn
	if _, _, runError := runProvenance(t, "", "application", "provenance", "set", testProvenanceApplicationID, "--clear"); runError != nil {
		t.Fatalf("clear failed: %v", runError)
	}
	seen := recorder.seen()
	value, present := seen[0].body["mode"]
	if len(seen) != 1 || !present || value != nil {
		t.Errorf("--clear must send mode:null, got %+v", seen)
	}
}

func TestApplicationProvenanceSetRequiresAnExplicitValidMode(t *testing.T) {
	recorder := newProvenanceServer(t)
	for _, arguments := range [][]string{
		{"application", "provenance", "set", testProvenanceApplicationID},
		{"application", "provenance", "set", testProvenanceApplicationID, "--mode", "strict"},
		{"application", "provenance", "set", testProvenanceApplicationID, "--mode", "warn", "--clear"},
	} {
		_, _, runError := runProvenance(t, "", arguments...)
		if exitCodeFor(runError) != exitUsage {
			t.Errorf("%v must be a usage error, got %v", arguments, runError)
		}
	}
	if len(recorder.seen()) != 0 {
		t.Errorf("a usage error must send nothing: %+v", recorder.seen())
	}
}

func TestApplicationProvenanceSetWithoutPermissionExitsForbidden(t *testing.T) {
	recorder := newProvenanceServer(t)
	recorder.status = http.StatusForbidden
	recorder.statusBody = `{"detail":"You need the security.manage permission to change deploy provenance settings and keys."}`
	_, _, runError := runProvenance(t, "", "application", "provenance", "set", testProvenanceApplicationID, "--mode", "warn")
	if exitCodeFor(runError) != exitForbidden || !strings.Contains(runError.Error(), "security.manage") {
		t.Errorf("want a forbidden error naming security.manage, got %v", runError)
	}
}

func TestApplicationProvenanceOldPlatformIsNamed(t *testing.T) {
	recorder := newProvenanceServer(t)
	recorder.status = http.StatusNotFound
	recorder.statusBody = `{"detail":"Not Found"}`
	_, _, runError := runProvenance(t, "", "application", "provenance", "get", testProvenanceApplicationID)
	if runError == nil || !strings.Contains(runError.Error(), "does not offer verified provenance") {
		t.Errorf("want the old-platform sentence, got %v", runError)
	}
}

const refusedVerification = `{"application_id":"` + testProvenanceApplicationID + `","mode":"enforce","decision":"refuse",
"verdict":{"state":"refused","images":[
 {"image":"registry.ankra.cloud/org-1/shop:sha-old","state":"refused","reason":"unsigned","message":"no attestation"},
 {"image":"registry.ankra.cloud/org-1/shop:sha-new","state":"verified","reason":"verified","message":"ok"}],
 "unchecked_images":["docker.io/library/postgres:16"]}}`

func TestApplicationProvenanceVerifyRefusedExitsNonZeroAndExplainsRollback(t *testing.T) {
	recorder := newProvenanceServer(t)
	recorder.verifyBody = refusedVerification
	stdout, _, runError := runProvenance(t, "", "application", "provenance", "verify", testProvenanceApplicationID,
		"--image", "registry.ankra.cloud/org-1/shop:sha-old", "--image", "registry.ankra.cloud/org-1/shop:sha-new")
	if exitCodeFor(runError) != exitError {
		t.Fatalf("a refused dry-run must exit 1, got %v", runError)
	}
	for _, want := range []string{"REFUSED", "sha-old", "unsigned", "not checked (outside the application's registry): docker.io/library/postgres:16",
		"cannot be deployed or rolled back to", "--mode warn"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("verify output is missing %q:\n%s", want, stdout)
		}
	}
	images, _ := recorder.seen()[0].body["images"].([]any)
	if len(images) != 2 {
		t.Errorf("both images must be sent: %+v", recorder.seen())
	}
}

func TestApplicationProvenanceVerifyNeedsAnImage(t *testing.T) {
	recorder := newProvenanceServer(t)
	_, _, runError := runProvenance(t, "", "application", "provenance", "verify", testProvenanceApplicationID)
	if exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
		t.Errorf("want a usage error with nothing sent, got %v %+v", runError, recorder.seen())
	}
}

func TestApplicationProvenanceVerifyAllowedExitsZero(t *testing.T) {
	recorder := newProvenanceServer(t)
	recorder.verifyBody = `{"application_id":"x","mode":"warn","decision":"deploy","verdict":{"state":"verified","images":[],"unchecked_images":[]}}`
	stdout, _, runError := runProvenance(t, "", "application", "provenance", "verify", testProvenanceApplicationID, "--image", "a/b:c")
	if runError != nil || !strings.Contains(stdout, "a deploy would go ahead") {
		t.Errorf("unexpected result: %v\n%s", runError, stdout)
	}
}

func TestOrgProvenanceGetSetClear(t *testing.T) {
	recorder := newProvenanceServer(t)
	stdout, _, runError := runProvenance(t, "", "org", "provenance", "get")
	if runError != nil || !strings.Contains(stdout, "not set") {
		t.Fatalf("get: %v\n%s", runError, stdout)
	}
	stdout, _, runError = runProvenance(t, "", "org", "provenance", "set", "--mode", "warn")
	if runError != nil || !strings.Contains(stdout, "warn") {
		t.Fatalf("set: %v\n%s", runError, stdout)
	}
	if _, _, runError = runProvenance(t, "", "org", "provenance", "set", "--clear"); runError != nil {
		t.Fatalf("clear: %v", runError)
	}
	seen := recorder.seen()
	if seen[1].body["mode"] != "warn" || seen[2].body["mode"] != nil {
		t.Errorf("unexpected writes: %+v", seen)
	}
}

func TestOrgProvenanceSetEnforceAsksFirstAndSaysWhy(t *testing.T) {
	recorder := newProvenanceServer(t)
	stdout, _, runError := runProvenance(t, "n\n", "org", "provenance", "set", "--mode", "enforce")
	if exitCodeFor(runError) != exitCancelled || len(recorder.seen()) != 0 {
		t.Fatalf("declining must send nothing and exit %d: %v %+v", exitCancelled, runError, recorder.seen())
	}
	if !strings.Contains(stdout, "rollbacks to versions published before") || !strings.Contains(stdout, "warn") {
		t.Errorf("the prompt must name the rollback cost and the way out:\n%s", stdout)
	}
}

const keysBody = `{"predicate_type":"https://ankra.io/attestations/pipeline-gate/v1","keys":[
{"key_id":"k2","public_key_pem":"PEM2\n","generation":2,"status":"active","revoked_at":null},
{"key_id":"k1","public_key_pem":"PEM1\n","generation":1,"status":"revoked","revoked_at":"2026-10-01T00:00:00Z"}]}`

func TestOrgProvenanceKeysTableAndPublicKeys(t *testing.T) {
	recorder := newProvenanceServer(t)
	recorder.keysBody = keysBody
	stdout, _, runError := runProvenance(t, "", "org", "provenance", "keys")
	if runError != nil || !strings.Contains(stdout, "k2") || !strings.Contains(stdout, "revoked") ||
		!strings.Contains(stdout, "pipeline-gate/v1") {
		t.Fatalf("keys: %v\n%s", runError, stdout)
	}
	stdout, _, runError = runProvenance(t, "", "org", "provenance", "keys", "--public-keys")
	if runError != nil || stdout != "PEM2\n" {
		t.Errorf("--public-keys must print only keys that are not revoked, got %q (%v)", stdout, runError)
	}
}

func TestOrgProvenanceRotate(t *testing.T) {
	recorder := newProvenanceServer(t)
	stdout, _, runError := runProvenance(t, "", "org", "provenance", "rotate")
	if runError != nil || !strings.Contains(stdout, "Generation 2") {
		t.Fatalf("rotate: %v\n%s", runError, stdout)
	}
	if seen := recorder.seen(); seen[0].method != http.MethodPost || seen[0].path != "/api/v1/org/provenance/keys/rotate" {
		t.Errorf("unexpected request: %+v", seen)
	}
}

func TestOrgProvenanceRevokeConfirmsAndSendsReason(t *testing.T) {
	recorder := newProvenanceServer(t)
	_, _, runError := runProvenance(t, "n\n", "org", "provenance", "revoke", "k1")
	if exitCodeFor(runError) != exitCancelled || len(recorder.seen()) != 0 {
		t.Fatalf("declining must send nothing: %v %+v", runError, recorder.seen())
	}
	stdout, _, runError := runProvenance(t, "", "org", "provenance", "revoke", "k1", "--yes", "--reason", "exposed")
	if runError != nil || !strings.Contains(stdout, "Revoked generation 1") {
		t.Fatalf("revoke: %v\n%s", runError, stdout)
	}
	body := recorder.seen()[0].body
	if body["key_id"] != "k1" || body["reason"] != "exposed" {
		t.Errorf("unexpected body: %+v", body)
	}
}

package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"

	"gopkg.in/yaml.v3"
)

// The tests of `ankra secrets adopt` drive the command through the real
// HTTP client against a fake platform that answers the way the adopt route
// does (cluster#6139: go/internal/cliapi/secretslot_adopt.go).

const (
	adoptTestClusterID = "5f0c3b8e-1d2a-4c6b-9e7f-0a1b2c3d4e5f"
	adoptTestRoute     = "/api/v1/org/secret-slots/adopt"
	// adoptTestPrefix is how every adopt refusal starts: what was asked for,
	// so a script's log says which key of which Secret failed.
	adoptTestPrefix = `adopt key "password" of Secret app/db-credentials into secret "db-password": `
)

// adoptTestArgs is a complete adopt of the key password of Secret
// app/db-credentials on the cluster named by id, so no listing is needed.
func adoptTestArgs(extra ...string) []string {
	return append([]string{"secrets", "adopt", "db-password",
		"--cluster", adoptTestClusterID, "--namespace", "app", "--name", "db-credentials", "--key", "password"}, extra...)
}

// adoptTestSlot is the platform's SecretSlotResponse for the new slot.
func adoptTestSlot() map[string]any {
	return map[string]any{
		"slot_id": secretTestSlotID, "label": "db-password", "hint": nil,
		"created_at": "2026-11-09T10:00:00Z", "expires_at": nil, "last_accessed_at": nil,
		"access_count": 0, "sentinel": "${SECRET_SLOT:" + secretTestSlotID + "}",
		"created_via": "user", "pinned_at": nil, "references": []any{}, "created_by_user_id": "user-1",
	}
}

// adoptPlatform is a fake platform: it lists one cluster named prod and
// answers every adopt with status and body, recording each request.
type adoptPlatform struct {
	mu         sync.Mutex
	requests   []string
	adoptBody  []map[string]any
	status     int
	body       any
	retryAfter string
}

func (platform *adoptPlatform) serve(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		platform.mu.Lock()
		defer platform.mu.Unlock()
		platform.requests = append(platform.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/clusters":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result":     []any{map[string]any{"id": adoptTestClusterID, "name": "prod", "state": "online"}},
				"pagination": map[string]any{"total_count": 1, "total_pages": 1, "page": 1, "page_size": 100},
			})
		case r.Method == http.MethodPost && r.URL.Path == adoptTestRoute:
			raw, _ := io.ReadAll(r.Body)
			var received map[string]any
			if unmarshalError := json.Unmarshal(raw, &received); unmarshalError != nil {
				t.Errorf("adopt body is not json: %v", unmarshalError)
			}
			platform.adoptBody = append(platform.adoptBody, received)
			if platform.retryAfter != "" {
				w.Header().Set("Retry-After", platform.retryAfter)
			}
			status := platform.status
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			answer := platform.body
			if answer == nil {
				answer = adoptTestSlot()
			}
			_ = json.NewEncoder(w).Encode(answer)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	setMockClient(t, client.New("test-token", server.URL))
}

func (platform *adoptPlatform) adopts() int {
	platform.mu.Lock()
	defer platform.mu.Unlock()
	return len(platform.adoptBody)
}

// bodies returns the adopt bodies received so far.
func (platform *adoptPlatform) bodies() []map[string]any {
	platform.mu.Lock()
	defer platform.mu.Unlock()
	return append([]map[string]any(nil), platform.adoptBody...)
}

func (platform *adoptPlatform) sent() []string {
	platform.mu.Lock()
	defer platform.mu.Unlock()
	return append([]string(nil), platform.requests...)
}

// executeAdopt resets the adopt command's flags and runs it.
func executeAdopt(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	resetTreeFlags(t, secretsAdoptCmd)
	t.Cleanup(func() { resetTreeFlags(t, secretsAdoptCmd) })
	return executeSecrets(t, "", args...)
}

// TestSecretsAdoptSendsTheContractBody: one POST to the adopt route whose
// body is exactly the platform's SecretSlotAdoptRequest, with the cluster
// sent as its id.
func TestSecretsAdoptSendsTheContractBody(t *testing.T) {
	platform := &adoptPlatform{}
	platform.serve(t)

	_, _, executeError := executeAdopt(t, adoptTestArgs("--hint", "Postgres app user", "--strip-trailing-newline")...)
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if sent := platform.sent(); len(sent) != 1 || sent[0] != "POST "+adoptTestRoute {
		t.Fatalf("requests = %v, want one POST %s", sent, adoptTestRoute)
	}
	want := map[string]any{
		"label": "db-password", "hint": "Postgres app user", "cluster_id": adoptTestClusterID,
		"namespace": "app", "name": "db-credentials", "key": "password", "strip_trailing_line_ending": true,
	}
	received := platform.bodies()[0]
	if len(received) != len(want) {
		t.Errorf("body = %v, want exactly %v", received, want)
	}
	for member, value := range want {
		if received[member] != value {
			t.Errorf("body[%q] = %v, want %v", member, received[member], value)
		}
	}
}

// TestSecretsAdoptWithoutOptionsSendsNoHintAndKeepsTheBytes: without --hint
// no hint is sent, and without --strip-trailing-newline the value is stored
// as it is (false is sent, not left to a default).
func TestSecretsAdoptWithoutOptionsSendsNoHintAndKeepsTheBytes(t *testing.T) {
	platform := &adoptPlatform{}
	platform.serve(t)

	if _, _, executeError := executeAdopt(t, adoptTestArgs()...); executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	received := platform.bodies()[0]
	if _, hasHint := received["hint"]; hasHint {
		t.Errorf("body = %v, want no hint", received)
	}
	if strip, present := received["strip_trailing_line_ending"]; !present || strip != false {
		t.Errorf("strip_trailing_line_ending = %v (present %v), want false", strip, present)
	}
}

// TestSecretsAdoptPrintsTheSentinelAndTheSummary: the sentinel alone goes to
// stdout, so SENTINEL="$(ankra secrets adopt ...)" captures it; the summary
// (what was adopted, the new secret's name, id and expiry) goes to stderr.
func TestSecretsAdoptPrintsTheSentinelAndTheSummary(t *testing.T) {
	platform := &adoptPlatform{}
	platform.serve(t)

	stdout, stderr, executeError := executeAdopt(t, adoptTestArgs()...)
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if stdout != "${SECRET_SLOT:"+secretTestSlotID+"}\n" {
		t.Errorf("stdout = %q, want the sentinel alone", stdout)
	}
	for _, want := range []string{
		`Adopted key "password" of Secret app/db-credentials on cluster ` + adoptTestClusterID,
		`into secret "db-password"`, "id " + secretTestSlotID, "does not expire", "Its sentinel:",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to carry %q", stderr, want)
		}
	}
}

// TestSecretsAdoptNamesAnExpiry: a slot the platform says expires is
// reported with its expiry.
func TestSecretsAdoptNamesAnExpiry(t *testing.T) {
	slot := adoptTestSlot()
	slot["expires_at"] = "2026-11-16T10:00:00Z"
	platform := &adoptPlatform{body: slot}
	platform.serve(t)

	_, stderr, executeError := executeAdopt(t, adoptTestArgs()...)
	if executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if !strings.Contains(stderr, "expires 2026-11-16 10:00 UTC") {
		t.Errorf("stderr = %q, want the expiry", stderr)
	}
}

// TestSecretsAdoptStructuredOutputIsTheSlot: -o json and -o yaml print the
// slot (id, name, sentinel, expiry) and nothing else on stdout.
func TestSecretsAdoptStructuredOutputIsTheSlot(t *testing.T) {
	platform := &adoptPlatform{}
	platform.serve(t)

	stdout, _, executeError := executeAdopt(t, adoptTestArgs("-o", "json")...)
	if executeError != nil {
		t.Fatalf("execute -o json: %v", executeError)
	}
	var printed map[string]any
	if unmarshalError := json.Unmarshal([]byte(stdout), &printed); unmarshalError != nil {
		t.Fatalf("-o json stdout is not one JSON document: %v\n%s", unmarshalError, stdout)
	}
	if printed["slot_id"] != secretTestSlotID || printed["label"] != "db-password" ||
		printed["sentinel"] != "${SECRET_SLOT:"+secretTestSlotID+"}" {
		t.Errorf("-o json = %v", printed)
	}
	if expires, present := printed["expires_at"]; !present || expires != nil {
		t.Errorf("-o json expires_at = %v (present %v), want null", expires, present)
	}

	stdout, _, executeError = executeAdopt(t, adoptTestArgs("-o", "yaml")...)
	if executeError != nil {
		t.Fatalf("execute -o yaml: %v", executeError)
	}
	var printedYAML map[string]any
	if unmarshalError := yaml.Unmarshal([]byte(stdout), &printedYAML); unmarshalError != nil {
		t.Fatalf("-o yaml stdout is not YAML: %v\n%s", unmarshalError, stdout)
	}
	if printedYAML["slot_id"] != secretTestSlotID || printedYAML["sentinel"] != "${SECRET_SLOT:"+secretTestSlotID+"}" {
		t.Errorf("-o yaml = %v", printedYAML)
	}
}

// TestSecretsAdoptNeverPrintsAValueTheAnswerCarries: a platform answer with
// members the contract does not name (here a value it must never return)
// prints none of them, in any output format.
func TestSecretsAdoptNeverPrintsAValueTheAnswerCarries(t *testing.T) {
	slot := adoptTestSlot()
	slot["value"] = secretTestValue
	slot["data"] = map[string]any{"password": secretTestValue}
	slot["hint"] = "Postgres app user"
	platform := &adoptPlatform{body: slot}
	platform.serve(t)

	for _, output := range [][]string{nil, {"-o", "json"}, {"-o", "yaml"}} {
		stdout, stderr, executeError := executeAdopt(t, adoptTestArgs(output...)...)
		if executeError != nil {
			t.Fatalf("%v: execute: %v", output, executeError)
		}
		assertNoSecretValue(t, stdout, stderr)
		if strings.Contains(stdout, `"value"`) || strings.Contains(stdout, "value:") || strings.Contains(stdout, "data") {
			t.Errorf("%v: stdout carries a member the contract does not name:\n%s", output, stdout)
		}
	}
}

// TestSecretsAdoptResolvesTheClusterByName: --cluster takes the cluster's
// name as `ankra cluster list` shows it, and the adopt is sent with its id.
// A name no cluster has exits 3 and sends no adopt.
func TestSecretsAdoptResolvesTheClusterByName(t *testing.T) {
	platform := &adoptPlatform{}
	platform.serve(t)

	args := []string{"secrets", "adopt", "db-password", "--cluster", "prod", "-n", "app", "--name", "db-credentials", "--key", "password"}
	if _, _, executeError := executeAdopt(t, args...); executeError != nil {
		t.Fatalf("execute: %v", executeError)
	}
	if bodies := platform.bodies(); len(bodies) != 1 || bodies[0]["cluster_id"] != adoptTestClusterID {
		t.Fatalf("adopt bodies = %v, want one naming cluster %s", bodies, adoptTestClusterID)
	}

	args[4] = "staging"
	stdout, _, executeError := executeAdopt(t, args...)
	if code := exitCodeFor(executeError); code != exitNotFound {
		t.Errorf("unknown cluster: error = %v, exit %d, want %d", executeError, code, exitNotFound)
	}
	if platform.adopts() != 1 || stdout != "" {
		t.Errorf("an unknown cluster sent an adopt (%d in all) or printed %q", platform.adopts(), stdout)
	}
}

// TestSecretsAdoptNeverUsesTheSelectedCluster: the cluster `ankra cluster
// select` saved is shared by every session on the machine (ankra-cli#760),
// so an adopt, which reads a Secret value, never falls back to it: without
// --cluster it exits 2 and sends nothing.
func TestSecretsAdoptNeverUsesTheSelectedCluster(t *testing.T) {
	withTempHome(t)
	writeSelectedClusterJSON(t)
	platform := &adoptPlatform{}
	platform.serve(t)

	_, _, executeError := executeAdopt(t, "secrets", "adopt", "db-password", "-n", "app", "--name", "db-credentials", "--key", "password")
	if code := exitCodeFor(executeError); code != exitUsage || !strings.Contains(executeError.Error(), "--cluster") {
		t.Fatalf("error = %v (exit %d), want a usage error naming --cluster", executeError, code)
	}
	if sent := platform.sent(); len(sent) != 0 {
		t.Errorf("requests = %v, want none", sent)
	}
}

// TestSecretsAdoptRefusesAMistypedOutputBeforeSending: -o jsn exits 2
// before the cluster is looked up or the adopt is sent (the shared pre-send
// check, ankra-v3qdto.6).
func TestSecretsAdoptRefusesAMistypedOutputBeforeSending(t *testing.T) {
	platform := &adoptPlatform{}
	platform.serve(t)

	args := []string{"secrets", "adopt", "db-password", "--cluster", "prod", "-n", "app", "--name", "db-credentials", "--key", "password", "-o", "jsn"}
	stdout, _, executeError := executeAdopt(t, args...)
	if code := exitCodeFor(executeError); code != exitUsage || !strings.Contains(executeError.Error(), `invalid -o format "jsn"`) {
		t.Fatalf("error = %v (exit %d), want the -o usage error", executeError, code)
	}
	if sent := platform.sent(); len(sent) != 0 || stdout != "" {
		t.Errorf("requests = %v, stdout = %q, want nothing sent or printed", sent, stdout)
	}
}

// TestSecretsAdoptUsageErrorsSendNothing: a missing or blank flag, a name
// or hint over the platform's limits and a second argument exit 2 before
// anything is sent. The second argument is never repeated.
func TestSecretsAdoptUsageErrorsSendNothing(t *testing.T) {
	const strayArgument = "stray-ARGUMENT-not-echoed"
	full := func(replacements map[string]string, extra ...string) []string {
		values := map[string]string{"--cluster": adoptTestClusterID, "--namespace": "app", "--name": "db-credentials", "--key": "password"}
		for flag, value := range replacements {
			values[flag] = value
		}
		args := []string{"secrets", "adopt", "db-password"}
		for _, flag := range []string{"--cluster", "--namespace", "--name", "--key"} {
			if value, keep := values[flag]; keep && value != "<omit>" {
				args = append(args, flag, value)
			}
		}
		return append(args, extra...)
	}
	for _, testCase := range []struct {
		name string
		args []string
		want string
	}{
		{"no name", []string{"secrets", "adopt", "--cluster", adoptTestClusterID, "-n", "app", "--name", "db-credentials", "--key", "password"},
			"the new secret's name is required"},
		{"blank name", []string{"secrets", "adopt", "  ", "--cluster", adoptTestClusterID, "-n", "app", "--name", "db-credentials", "--key", "password"},
			"the new secret's name is required"},
		{"a second argument", append(adoptTestArgs(), strayArgument), "adopt takes one argument"},
		{"no --cluster", full(map[string]string{"--cluster": "<omit>"}), "--cluster"},
		{"no --namespace", full(map[string]string{"--namespace": "<omit>"}), "--namespace"},
		{"no --name", full(map[string]string{"--name": "<omit>"}), "--name"},
		{"no --key", full(map[string]string{"--key": "<omit>"}), "--key"},
		{"blank --key", full(map[string]string{"--key": " "}), "--key"},
		{"no flags at all", []string{"secrets", "adopt", "db-password"}, "--cluster, --namespace, --name, --key"},
		{"name over 120 characters", append([]string{"secrets", "adopt", strings.Repeat("n", 121)}, adoptTestArgs()[3:]...),
			"at most 120 characters"},
		{"name with a line break", append([]string{"secrets", "adopt", "db\npassword"}, adoptTestArgs()[3:]...),
			"line break"},
		{"hint over 240 characters", adoptTestArgs("--hint", strings.Repeat("h", 241)), "--hint is at most 240 characters"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			platform := &adoptPlatform{}
			platform.serve(t)
			stdout, stderr, executeError := executeAdopt(t, testCase.args...)
			if code := exitCodeFor(executeError); code != exitUsage {
				t.Fatalf("error = %v, exit %d, want %d", executeError, code, exitUsage)
			}
			if !strings.Contains(executeError.Error(), testCase.want) {
				t.Errorf("error = %q, want it to say %q", executeError, testCase.want)
			}
			if sent := platform.sent(); len(sent) != 0 || stdout != "" {
				t.Errorf("requests = %v, stdout = %q, want nothing sent or printed", sent, stdout)
			}
			for _, output := range []string{stdout, stderr, executeError.Error()} {
				if strings.Contains(output, strayArgument) {
					t.Errorf("the stray argument was repeated: %q", output)
				}
			}
		})
	}
}

// TestSecretsAdoptRefusals: every answer of the adopt route maps to the
// exit-code class sibling commands use for it, with the platform's sentence
// after what was asked for, nothing on stdout, and the adopt sent once.
func TestSecretsAdoptRefusals(t *testing.T) {
	const (
		revealSentence = "Adopting a Secret value needs the kubernetes.secrets_reveal permission on the cluster"
		cacheSentence  = "The cluster answered from its synced cache, which holds no Secret values. " +
			"Adopting needs a live read from the cluster's agent"
	)
	for _, testCase := range []struct {
		name       string
		status     int
		body       any
		retryAfter string
		wantExit   int
		want       []string
	}{
		{"401 rejected token", http.StatusUnauthorized, map[string]any{"detail": "Invalid token"}, "", exitAuth, nil},
		{"403 without variables.write", http.StatusForbidden,
			map[string]any{"detail": "permission_denied", "permission": "variables.write", "scope_type": "organisation"}, "",
			exitForbidden, []string{`requires the "variables.write" permission`}},
		{"403 without kubernetes.secrets_reveal", http.StatusForbidden, map[string]any{"detail": revealSentence}, "",
			exitForbidden, []string{"permission denied: " + revealSentence}},
		{"404 cluster", http.StatusNotFound, map[string]any{"detail": "Cluster not found"}, "", exitNotFound,
			[]string{adoptTestPrefix + "Cluster not found"}},
		{"404 Secret", http.StatusNotFound, map[string]any{"detail": "Secret not found"}, "", exitNotFound,
			[]string{adoptTestPrefix + "Secret not found"}},
		{"404 key", http.StatusNotFound, map[string]any{"detail": "The Secret has no such key"}, "", exitNotFound,
			[]string{adoptTestPrefix + "The Secret has no such key"}},
		{"409 name taken", http.StatusConflict, map[string]any{"detail": "A secret slot with this label already exists"}, "",
			exitError, []string{adoptTestPrefix + "A secret slot with this label already exists"}},
		{"409 synced cache", http.StatusConflict, map[string]any{"detail": cacheSentence}, "", exitError,
			[]string{adoptTestPrefix + cacheSentence}},
		{"400 empty value", http.StatusBadRequest,
			map[string]any{"detail": "The Secret key's value is empty, so there is nothing to adopt"}, "", exitUsage,
			[]string{adoptTestPrefix + "The Secret key's value is empty, so there is nothing to adopt"}},
		{"422 validation", http.StatusUnprocessableEntity, map[string]any{"detail": []any{map[string]any{
			"type": "uuid_parsing", "loc": []any{"body", "cluster_id"}, "msg": "Input should be a valid UUID",
		}}}, "", exitUsage, []string{adoptTestPrefix + "cluster_id: Input should be a valid UUID"}},
		{"502 agent could not read", http.StatusBadGateway,
			map[string]any{"detail": `The cluster's agent could not read the Secret: secrets "db-credentials" is forbidden`}, "",
			exitError, []string{adoptTestPrefix + `The cluster's agent could not read the Secret: secrets "db-credentials" is forbidden`}},
		{"503 NO_AGENT", http.StatusServiceUnavailable,
			map[string]any{"error_code": "NO_AGENT", "detail": "No agent is connected for this cluster", "retry_after": 30}, "30",
			exitError, []string{adoptTestPrefix, "could not be read live", "NO_AGENT: No agent is connected for this cluster", "retry in 30s", "nothing was stored"}},
		{"503 AGENT_TIMEOUT", http.StatusServiceUnavailable,
			map[string]any{"error_code": "AGENT_TIMEOUT", "detail": "The agent did not answer in time", "retry_after": 10}, "10",
			exitError, []string{"AGENT_TIMEOUT: The agent did not answer in time", "retry in 10s", "nothing was stored"}},
		{"503 CLUSTER_OFFLINE", http.StatusServiceUnavailable,
			map[string]any{"error_code": "CLUSTER_OFFLINE", "detail": "Cluster is offline", "retry_after": 30}, "30",
			exitError, []string{"CLUSTER_OFFLINE: Cluster is offline", "retry in 30s", "nothing was stored"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			platform := &adoptPlatform{status: testCase.status, body: testCase.body, retryAfter: testCase.retryAfter}
			platform.serve(t)

			for _, output := range [][]string{nil, {"-o", "json"}} {
				before := platform.adopts()
				stdout, stderr, executeError := executeAdopt(t, adoptTestArgs(output...)...)
				if executeError == nil {
					t.Fatalf("%v: the refusal was reported as success", output)
				}
				if code := exitCodeFor(executeError); code != testCase.wantExit {
					t.Errorf("%v: error = %q, exit %d, want %d", output, executeError, code, testCase.wantExit)
				}
				for _, want := range testCase.want {
					if !strings.Contains(executeError.Error(), want) {
						t.Errorf("%v: error = %q, want it to carry %q", output, executeError, want)
					}
				}
				for _, unwanted := range []string{"status " + strconv.Itoa(testCase.status), "body:", "error_code", "{"} {
					if strings.Contains(executeError.Error(), unwanted) {
						t.Errorf("%v: error = %q carries %q", output, executeError, unwanted)
					}
				}
				if sent := platform.adopts() - before; sent != 1 {
					t.Errorf("%v: the adopt was sent %d time(s), want once: a refusal is never retried", output, sent)
				}
				if stdout != "" {
					t.Errorf("%v: stdout = %q, want nothing on a refusal", output, stdout)
				}
				assertNoSecretValue(t, stdout, stderr, executeError.Error())
			}
		})
	}
}

// TestSecretsAdoptHelpSaysWhyAndWhatItNeeds: the help (the docs source)
// says what adopting is for, which permissions it needs, that the value
// never reaches the terminal, and that the selected cluster is not used.
func TestSecretsAdoptHelpSaysWhyAndWhatItNeeds(t *testing.T) {
	for _, want := range []string{
		"kubernetes.secrets_reveal", "variables.write", "never", "--strip-trailing-newline", "audit log",
		"the selected cluster is never used",
	} {
		if !strings.Contains(secretsAdoptCmd.Long, want) {
			t.Errorf("adopt help does not mention %q:\n%s", want, secretsAdoptCmd.Long)
		}
	}
	if !strings.Contains(secretsAdoptCmd.Example, "ankra secrets adopt") {
		t.Errorf("adopt help has no example:\n%s", secretsAdoptCmd.Example)
	}
	if !strings.Contains(secretsCmd.Long, "ankra secrets adopt") {
		t.Errorf("the secrets help does not list adopt:\n%s", secretsCmd.Long)
	}
}

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"ankra/internal/client"
)

// `ankra services disconnect` runs here against the in-memory platform of
// managed_services_test.go, through the real client, so every test sees the
// exact prepare and confirm requests a real platform would receive.

const (
	// fakeServiceOtherInstanceID is a second PostgreSQL service connected to
	// the same environment, the one a review offers to give DATABASE_URL.
	fakeServiceOtherInstanceID = "c9b96bb8-0000-4000-8000-0000000000a1"
	// fakeServiceStagingConsumerID is the application's second consumer of
	// the service, in its staging environment.
	fakeServiceStagingConsumerID = "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1c22"
)

// The platform's own sentences (cluster serviceruntime disconnection.go and
// disconnectionstatements.go), which the CLI must print as sent.
const (
	fakeDisconnectCredential = "The shared credential: every application connected to this service uses the same password " +
		"(1 other application environment uses it), so Ankra cannot revoke it for this application alone. The application keeps the password it has."
	fakeDisconnectCannotGuarantee = "The application keeps the database owner's password and its full rights from any namespace the network " +
		"still admits, and the data it wrote stays in the database."
	fakeDisconnectRotation = "To make sure the application can no longer authenticate, rotate the service's password. That changes it for " +
		"every connected application, and Ankra has no rotation for this service yet."
	fakeDisconnectSecret    = "Ankra keeps orders-db-connection in orders exactly as it is while pods still read it, and removes it once none does."
	fakeDisconnectWorkloads = "Deployment/api read orders-db-connection through the reference Ankra attached: the next deploy removes it and rolls them. " +
		"CronJob/report read orders-db-connection through a reference written by hand: change them yourself, or they start without its values once it is gone."
	fakeDisconnectNetwork = "Once no pod reads the connection Secret, Ankra takes the namespace orders off the service's port 5432."
	fakeDisconnectKeep    = "The namespace orders keeps its access to port 5432, as you asked: anything running there can still reach the service."
	fakeDisconnectData    = "Nothing is deleted: the database and everything the application wrote in it stay."
	fakeDisconnectTiming  = "The application keeps working against the service until it is deployed again: that deploy removes its workloads' " +
		"reference to orders-db-connection and rolls them, and Ankra removes the Secret once no pod reads it. Deploy the application now to finish sooner."
	fakeDisconnectKeptHistory = "Earlier copies of the connection Secret stay in the namespace's Helm release history (storefront-connections, " +
		"up to 10 revisions) until newer revisions replace them."
	fakeDisconnectVariables = "The application stops receiving DATABASE_URL and ORDERS_DB_DATABASE_URL."
)

// servePrepareDisconnection answers a disconnection prepare the way the
// platform does: a review with a fresh id, digest and expiry, asking for a
// choice of who gives DATABASE_URL when disconnectionChoiceRequired is set,
// and refusing a choice the review does not ask for or offer.
func (platform *fakeServicesPlatform) servePrepareDisconnection(writer http.ResponseWriter, bodyBytes []byte) {
	t := platform.t
	var body client.ServiceDisconnectionRequest
	if decodeError := json.Unmarshal(bodyBytes, &body); decodeError != nil {
		fakeServiceJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Invalid service admission request"})
		return
	}
	namespace := ""
	for _, consumer := range platform.instance()["consumers"].([]any) {
		if consumer.(map[string]any)["id"] == body.ConsumerID {
			namespace = consumer.(map[string]any)["namespace"].(string)
		}
	}
	if namespace == "" {
		fakeServiceJSON(t, writer, http.StatusConflict, map[string]any{"detail": "The service does not serve this application environment"})
		return
	}
	hasChoice := body.ConnectionNamesNone || body.ConnectionNamesInstanceID != nil
	other := map[string]any{"instance_id": fakeServiceOtherInstanceID, "service_name": "orders-db-v2", "none": false, "installing": false}
	names := map[string]any{"variables": []any{"DATABASE_URL", "ORDERS_DB_DATABASE_URL"}, "conventional": []any{"DATABASE_URL"},
		"carries_conventional": true, "choice_required": false, "choices": []any{}, "choice": nil,
		"statement": fakeDisconnectVariables + " No other service of its kind is connected, so the next deploy stops delivering DATABASE_URL."}
	switch {
	case !platform.disconnectionChoiceRequired && hasChoice:
		fakeServiceJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Choose one of the services the disconnection " +
			"review lists for the connection names, or none, and only when the review asks for it"})
		return
	case platform.disconnectionChoiceRequired:
		names["choice_required"] = true
		names["choices"] = []any{other}
		names["statement"] = fakeDisconnectVariables + " orders-db gives it DATABASE_URL, and orders-db-v2 is connected too: choose which " +
			"service gives them from now on, or none, before you confirm."
		switch {
		case body.ConnectionNamesNone:
			names["choice"] = map[string]any{"instance_id": nil, "service_name": "", "none": true, "installing": false}
			names["statement"] = fakeDisconnectVariables + " From the next deploy no service gives the application DATABASE_URL, as you chose."
		case body.ConnectionNamesInstanceID != nil && *body.ConnectionNamesInstanceID == fakeServiceOtherInstanceID:
			names["choice"] = other
			names["statement"] = fakeDisconnectVariables + " From the next deploy orders-db-v2 gives the application DATABASE_URL, as you chose."
		case body.ConnectionNamesInstanceID != nil:
			fakeServiceJSON(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": "Choose one of the services the disconnection " +
				"review lists for the connection names, or none, and only when the review asks for it"})
			return
		}
	}
	network := map[string]any{"namespace": namespace, "narrows": true, "keep_network_access": false, "reason": "narrows", "port": 5432,
		"statement": fakeDisconnectNetwork}
	if body.KeepNetworkAccess {
		network = map[string]any{"namespace": namespace, "narrows": false, "keep_network_access": true, "reason": "keep_requested", "port": 5432,
			"statement": fakeDisconnectKeep}
	}
	if platform.disconnectionNetworkReason != "" {
		network["reason"] = platform.disconnectionNetworkReason
	}
	workloadsState := "known"
	if platform.disconnectionWorkloadsState != "" {
		workloadsState = platform.disconnectionWorkloadsState
	}

	platform.mutex.Lock()
	platform.disconnectionPrepares++
	number := platform.disconnectionPrepares
	platform.mutex.Unlock()
	id := fmt.Sprintf("d15c0000-0000-4000-8000-%012d", number)
	disconnection := map[string]any{
		"id": id, "instance_id": fakeServiceInstanceID, "digest": "sha256:" + strings.Repeat(fmt.Sprintf("%x", number%16), 64),
		"state": "pending", "created_at": "2026-11-01T10:00:00Z", "expires_at": "2026-11-01T10:10:00Z",
		"confirmed_at": nil, "phase": nil, "settled_at": nil, "outcome": nil,
		"running_pods": map[string]any{"count": 2, "names": []any{"api-7d9f-abcde", "report-29811-xyz"}},
		"plan": map[string]any{
			"schema_version": 1, "instance_id": fakeServiceInstanceID, "organisation_id": fakeServiceOrganisation,
			"actor_id": "a0000000-0000-4000-8000-000000000013", "service_name": "orders-db", "cluster_id": fakeServiceClusterID,
			"generation": 2, "engine": "PostgreSQL", "protocol": "postgres",
			"consumer": map[string]any{"id": body.ConsumerID, "application_id": fakeServiceApplication, "cluster_id": fakeServiceClusterID,
				"namespace": namespace},
			"on_service_cluster": true,
			"credential": map[string]any{"kind": "shared", "other_consumers": 1, "statement": fakeDisconnectCredential,
				"cannot_guarantee": fakeDisconnectCannotGuarantee},
			"connection_names":  names,
			"connection_secret": map[string]any{"secret_name": "orders-db-connection", "delivered": true, "statement": fakeDisconnectSecret},
			"workloads": map[string]any{"state": workloadsState, "attached_by_ankra": []any{"Deployment/api"},
				"written_by_hand": []any{"CronJob/report"}, "statement": fakeDisconnectWorkloads},
			"network": network,
			"data":    fakeDisconnectData,
			"timing": map[string]any{"mode": "after_next_deploy", "deploy_now_offered": true, "now_offered": false,
				"statement": fakeDisconnectTiming},
			"kept": []any{fakeDisconnectKeptHistory,
				"A shared credential stays valid: the application can still use the one it has wherever the network admits it."},
			"rotation":   fakeDisconnectRotation,
			"expires_at": "2026-11-01T10:10:00Z",
		},
	}
	platform.mutex.Lock()
	if platform.disconnections == nil {
		platform.disconnections = map[string]map[string]any{}
	}
	platform.disconnections[id] = disconnection
	platform.mutex.Unlock()
	fakeServiceJSON(t, writer, http.StatusOK, disconnection)
}

// serveConfirmDisconnection answers a confirm: only the stored digest, and,
// like the platform, never a review that still needs a choice.
func (platform *fakeServicesPlatform) serveConfirmDisconnection(writer http.ResponseWriter, id string, bodyBytes []byte) {
	t := platform.t
	var body struct{ Digest string }
	_ = json.Unmarshal(bodyBytes, &body)
	platform.mutex.Lock()
	stored, known := platform.disconnections[id]
	platform.mutex.Unlock()
	switch {
	case !known:
		fakeServiceJSON(t, writer, http.StatusNotFound, map[string]any{"detail": "Service disconnection not found"})
		return
	case platform.disconnectionConfirmConflict || body.Digest != stored["digest"]:
		fakeServiceJSON(t, writer, http.StatusConflict, map[string]any{"detail": "The disconnection changed since it was reviewed; review it again"})
		return
	}
	names := stored["plan"].(map[string]any)["connection_names"].(map[string]any)
	if names["choice_required"] == true && names["choice"] == nil {
		fakeServiceJSON(t, writer, http.StatusConflict, map[string]any{"detail": "This service gives the application its conventional " +
			"connection names (such as DATABASE_URL) and another service of the same kind is connected"})
		return
	}
	confirmed := map[string]any{}
	for key, value := range stored {
		confirmed[key] = value
	}
	phase := "draining"
	if platform.disconnectionConfirmPhase != "" {
		phase = platform.disconnectionConfirmPhase
	}
	confirmed["state"] = "in_progress"
	confirmed["confirmed_at"] = "2026-11-01T10:01:00Z"
	confirmed["phase"] = phase
	fakeServiceJSON(t, writer, http.StatusOK, confirmed)
}

// interactivePrompt makes the confirmation question askable, as on a
// terminal, for the length of the test.
func interactivePrompt(t *testing.T) {
	t.Helper()
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return true }
	t.Cleanup(func() { promptIsInteractive = previous })
}

func (platform *fakeServicesPlatform) disconnectionConfirms() []fakeServiceRequest {
	var confirms []fakeServiceRequest
	for _, call := range platform.requests(http.MethodPost, "/confirm") {
		if strings.Contains(call.path, "/disconnections/") {
			confirms = append(confirms, call)
		}
	}
	return confirms
}

func fakeServiceStagingConsumer() map[string]any {
	return map[string]any{"id": fakeServiceStagingConsumerID, "application_id": fakeServiceApplication,
		"cluster_id": fakeServiceClusterID, "namespace": "orders-staging", "application_name": "storefront", "cluster_name": "prod",
		"environment": map[string]any{"id": "e1000000-0000-4000-8000-000000000021", "name": "staging", "role": "development"}}
}

// The whole lane: the prepare carries the consumer, the generation read,
// after_next_deploy and every required field (a null choice); every
// statement of the plan is printed in the platform's words; the confirm
// carries exactly the reviewed digest.
func TestServicesDisconnectPreparesShowsAndConfirmsTheReviewedDigest(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--yes")
	if runError != nil {
		t.Fatalf("disconnect: %v", runError)
	}
	prepares := platform.requests(http.MethodPost, "/disconnections")
	if len(prepares) != 1 {
		t.Fatalf("expected one prepare, got %d", len(prepares))
	}
	wantBody := `{"consumer_id":"` + fakeServiceConsumerID + `","expected_generation":2,"timing":"after_next_deploy",` +
		`"keep_network_access":false,"connection_names_instance_id":null,"connection_names_none":false}`
	if prepares[0].body != wantBody {
		t.Errorf("prepare body:\n got  %s\n want %s", prepares[0].body, wantBody)
	}
	confirms := platform.disconnectionConfirms()
	if len(confirms) != 1 || !strings.HasSuffix(confirms[0].path, "/disconnections/d15c0000-0000-4000-8000-000000000001/confirm") ||
		confirms[0].body != `{"digest":"sha256:`+strings.Repeat("1", 64)+`"}` {
		t.Fatalf("expected one confirm of the reviewed digest, got %+v", confirms)
	}
	for _, expected := range []string{
		`Disconnection review d15c0000-0000-4000-8000-000000000001 of service "orders-db" (generation 2)`,
		"PostgreSQL (postgres)", "storefront (environment production), namespace orders on cluster prod",
		fakeDisconnectVariables, fakeDisconnectCredential, fakeDisconnectCannotGuarantee, fakeDisconnectRotation, fakeDisconnectSecret,
		fakeDisconnectWorkloads, fakeDisconnectNetwork + " (--keep-network-access keeps it.)", fakeDisconnectData, fakeDisconnectTiming,
		"What stays:", "    - " + fakeDisconnectKeptHistory, "2 pods read the connection Secret now: api-7d9f-abcde, report-29811-xyz",
		"sha256:" + strings.Repeat("1", 64),
		`Application storefront (environment production) is disconnected from service "orders-db"`, "in_progress, phase draining",
		"Ankra keeps the connection Secret until no pod and no deployed workload reads it", "'ankra services get orders-db'",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("disconnect output lacks %q:\n%s", expected, stdout)
		}
	}
	if strings.Contains(stdout, "Choose who gives") {
		t.Errorf("a review that asks no choice must not offer one:\n%s", stdout)
	}
}

// --keep-network-access reaches the prepare, and the plan's statement then
// says the namespace keeps its access, with no hint to keep it.
func TestServicesDisconnectKeepsNetworkAccessWhenAsked(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront",
		"--keep-network-access", "--yes")
	if runError != nil {
		t.Fatal(runError)
	}
	prepares := platform.requests(http.MethodPost, "/disconnections")
	if len(prepares) != 1 || !strings.Contains(prepares[0].body, `"keep_network_access":true`) {
		t.Fatalf("the prepare must ask to keep network access: %+v", prepares)
	}
	if !strings.Contains(stdout, fakeDisconnectKeep) || strings.Contains(stdout, "--keep-network-access keeps it") {
		t.Errorf("the kept access must be said in the platform's words, without the hint:\n%s", stdout)
	}
}

// A no at the prompt confirms nothing and exits 4; a yes confirms. The
// question goes to stderr.
func TestServicesDisconnectAsksBeforeConfirming(t *testing.T) {
	interactivePrompt(t)
	platform := newFakeServicesPlatform(t)
	_, stderr, runError := runServicesCommand(t, platform, "n\n", "disconnect", "orders-db", "--application", "storefront")
	if !errors.Is(runError, errCancelled) || exitCodeFor(runError) != exitCancelled {
		t.Fatalf("a declined disconnection must exit 4, got %v", runError)
	}
	if confirms := platform.disconnectionConfirms(); len(confirms) != 0 {
		t.Fatalf("a declined disconnection was confirmed: %+v", confirms)
	}
	if !strings.Contains(stderr, `Disconnect application storefront (environment production) from service "orders-db" as reviewed above? [y/N]`) ||
		!strings.Contains(stderr, "Nothing changed") {
		t.Errorf("the question and what declining did belong on stderr:\n%s", stderr)
	}

	platform = newFakeServicesPlatform(t)
	if _, _, runError = runServicesCommand(t, platform, "y\n", "disconnect", "orders-db", "--application", "storefront"); runError != nil {
		t.Fatalf("a yes must confirm: %v", runError)
	}
	if confirms := platform.disconnectionConfirms(); len(confirms) != 1 {
		t.Fatalf("a yes must confirm once, got %d", len(confirms))
	}
}

// Without a terminal and without --yes nothing is confirmed: the review is
// shown, and the command exits 4 saying how to go on.
func TestServicesDisconnectWithoutATerminalNeedsYes(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, _, runError := runServicesCommand(t, platform, "y\n", "disconnect", "orders-db", "--application", "storefront")
	if runError == nil || exitCodeFor(runError) != exitCancelled || !strings.Contains(runError.Error(), "--yes") ||
		!strings.Contains(runError.Error(), "no terminal") {
		t.Fatalf("expected exit 4 naming --yes, got %v", runError)
	}
	if confirms := platform.disconnectionConfirms(); len(confirms) != 0 {
		t.Fatalf("a disconnection was confirmed without a yes: %+v", confirms)
	}
	if !strings.Contains(stdout, fakeDisconnectCredential) {
		t.Errorf("the review must still be shown:\n%s", stdout)
	}
}

// -o json prints the confirmed disconnection as the only thing on stdout;
// the review goes to stderr. --review-only -o json prints the review and
// confirms nothing.
func TestServicesDisconnectStructuredOutputStaysParseable(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	stdout, stderr, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--yes", "-o", "json")
	if runError != nil {
		t.Fatal(runError)
	}
	var confirmed client.ServiceDisconnection
	if decodeError := json.Unmarshal([]byte(stdout), &confirmed); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout)
	}
	if confirmed.ConfirmedAt == nil || confirmed.State != "in_progress" || confirmed.Plan.Credential.Statement != fakeDisconnectCredential {
		t.Errorf("unexpected receipt: %+v", confirmed)
	}
	if !strings.Contains(stderr, fakeDisconnectCredential) {
		t.Errorf("the review must still be shown, on stderr:\n%s", stderr)
	}

	platform = newFakeServicesPlatform(t)
	stdout, stderr, runError = runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--review-only", "-o", "yaml")
	if runError != nil {
		t.Fatal(runError)
	}
	if confirms := platform.disconnectionConfirms(); len(confirms) != 0 {
		t.Fatalf("--review-only confirmed: %+v", confirms)
	}
	if !strings.HasPrefix(stdout, "id: d15c0000-0000-4000-8000-000000000001\n") || !strings.Contains(stdout, "state: pending\n") ||
		strings.Contains(stdout, "Nothing changed") {
		t.Errorf("stdout must be the pending review as YAML, nothing else:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Nothing changed") {
		t.Errorf("--review-only says nothing changed, on stderr:\n%s", stderr)
	}
}

// When the review asks who gives DATABASE_URL next, nothing is confirmed
// without --names-from: the choices are shown and the command says how to
// choose.
func TestServicesDisconnectNeedsAChoiceForTheConventionalNames(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.disconnectionChoiceRequired = true
	stdout, _, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--yes")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--names-from orders-db-v2, none") ||
		!strings.Contains(runError.Error(), "nothing changed") {
		t.Fatalf("expected a usage refusal naming the choices, got %v", runError)
	}
	if confirms := platform.disconnectionConfirms(); len(confirms) != 0 {
		t.Fatalf("a review that needs a choice was confirmed: %+v", confirms)
	}
	if prepares := platform.requests(http.MethodPost, "/disconnections"); len(prepares) != 1 {
		t.Errorf("expected one prepare, got %d", len(prepares))
	}
	for _, expected := range []string{"Choose who gives DATABASE_URL from now on, with --names-from:",
		"orders-db-v2 (" + fakeServiceOtherInstanceID + ")", "none (no service gives them)"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("the review must offer the choices, lacking %q:\n%s", expected, stdout)
		}
	}
}

// --names-from chooses among the review's own choices: the review is
// prepared again with the choice, and that second review is the one
// confirmed.
func TestServicesDisconnectNamesFromPreparesAgainWithTheChoice(t *testing.T) {
	for _, testCase := range []struct {
		value    string
		wantBody string
		wantSays string
	}{
		{"orders-db-v2", `"connection_names_instance_id":"` + fakeServiceOtherInstanceID + `","connection_names_none":false`, "orders-db-v2 gives the application DATABASE_URL, as you chose"},
		{fakeServiceOtherInstanceID, `"connection_names_instance_id":"` + fakeServiceOtherInstanceID + `","connection_names_none":false`, "orders-db-v2 gives the application DATABASE_URL, as you chose"},
		{"none", `"connection_names_instance_id":null,"connection_names_none":true`, "no service gives the application DATABASE_URL, as you chose"},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			platform := newFakeServicesPlatform(t)
			platform.disconnectionChoiceRequired = true
			stdout, _, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront",
				"--names-from", testCase.value, "--yes")
			if runError != nil {
				t.Fatal(runError)
			}
			prepares := platform.requests(http.MethodPost, "/disconnections")
			if len(prepares) != 2 || !strings.Contains(prepares[0].body, `"connection_names_instance_id":null,"connection_names_none":false`) ||
				!strings.Contains(prepares[1].body, testCase.wantBody) {
				t.Fatalf("expected a prepare without the choice, then one with it: %+v", prepares)
			}
			confirms := platform.disconnectionConfirms()
			if len(confirms) != 1 || !strings.HasSuffix(confirms[0].path, "/disconnections/d15c0000-0000-4000-8000-000000000002/confirm") ||
				confirms[0].body != `{"digest":"sha256:`+strings.Repeat("2", 64)+`"}` {
				t.Fatalf("the review prepared with the choice must be the one confirmed: %+v", confirms)
			}
			if !strings.Contains(stdout, testCase.wantSays) || strings.Contains(stdout, "Choose who gives") {
				t.Errorf("the review shown must be the one with the choice:\n%s", stdout)
			}
		})
	}
}

// A --names-from the review does not offer is refused before a second
// prepare; one given where no choice is asked is not sent, and is said not
// to be used.
func TestServicesDisconnectNamesFromOnlyWhereTheReviewAsks(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.disconnectionChoiceRequired = true
	_, _, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--names-from", "billing-db", "--yes")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "orders-db-v2, none") {
		t.Fatalf("expected a usage refusal listing the offered choices, got %v", runError)
	}
	if prepares := platform.requests(http.MethodPost, "/disconnections"); len(prepares) != 1 {
		t.Errorf("an unoffered choice must not be prepared, got %d prepares", len(prepares))
	}
	if confirms := platform.disconnectionConfirms(); len(confirms) != 0 {
		t.Fatalf("confirmed with an unoffered choice: %+v", confirms)
	}

	platform = newFakeServicesPlatform(t)
	_, stderr, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--names-from", "none", "--yes")
	if runError != nil {
		t.Fatal(runError)
	}
	prepares := platform.requests(http.MethodPost, "/disconnections")
	if len(prepares) != 1 || !strings.Contains(prepares[0].body, `"connection_names_instance_id":null,"connection_names_none":false`) {
		t.Fatalf("a choice the review does not ask for must not be sent: %+v", prepares)
	}
	if !strings.Contains(stderr, "--names-from none was not used") {
		t.Errorf("an unused --names-from must be said:\n%s", stderr)
	}
}

// The application environment is chosen exactly: an application served
// from two environments needs --environment (or --consumer), an unknown
// environment is not found, and the flags are checked before any request.
func TestServicesDisconnectChoosesOneApplicationEnvironment(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.extraConsumers = []any{fakeServiceStagingConsumer()}
	_, _, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--yes")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "environment production") ||
		!strings.Contains(runError.Error(), "environment staging") || !strings.Contains(runError.Error(), "--environment") {
		t.Fatalf("expected a usage refusal listing both environments, got %v", runError)
	}

	for _, arguments := range [][]string{
		{"--application", "storefront", "--environment", "staging"},
		{"--application", "storefront", "--environment", "Staging"},
		{"--application", "storefront", "--environment", "e1000000-0000-4000-8000-000000000021"},
		{"--consumer", fakeServiceStagingConsumerID},
		{"--consumer", fakeServiceStagingConsumerID, "--application", "storefront"},
	} {
		platform := newFakeServicesPlatform(t)
		platform.extraConsumers = []any{fakeServiceStagingConsumer()}
		if _, _, runError := runServicesCommand(t, platform, "", append([]string{"disconnect", "orders-db", "--yes"}, arguments...)...); runError != nil {
			t.Fatalf("%v: %v", arguments, runError)
		}
		prepares := platform.requests(http.MethodPost, "/disconnections")
		if len(prepares) != 1 || !strings.Contains(prepares[0].body, `"consumer_id":"`+fakeServiceStagingConsumerID+`"`) {
			t.Errorf("%v: expected the staging consumer to be prepared, got %+v", arguments, prepares)
		}
	}

	platform = newFakeServicesPlatform(t)
	platform.extraConsumers = []any{fakeServiceStagingConsumer()}
	_, _, runError = runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--environment", "qa", "--yes")
	if runError == nil || exitCodeFor(runError) != exitNotFound || !strings.Contains(runError.Error(), "environment production, environment staging") {
		t.Fatalf("an environment the service does not serve must be not found, naming the served ones, got %v", runError)
	}

	platform = newFakeServicesPlatform(t)
	for _, arguments := range [][]string{
		{},
		{"--consumer", "not-an-id"},
		{"--consumer", fakeServiceConsumerID, "--environment", "production"},
		{"--application", "storefront", "--review-only", "--yes"},
		{"--application", ""},
		{"--application", "storefront", "--names-from", " "},
	} {
		_, _, runError := runServicesCommand(t, platform, "", append([]string{"disconnect", "orders-db"}, arguments...)...)
		if runError == nil || exitCodeFor(runError) != exitUsage {
			t.Errorf("%v: expected a usage refusal, got %v", arguments, runError)
		}
	}
	if calls := len(platform.calls); calls != 0 {
		t.Errorf("refused flags must not reach the platform, got %d requests", calls)
	}
}

// An application the service does not serve is not found; one already being
// disconnected says so, read from the single instance read (a name resolves
// through the listing, which carries no disconnections).
func TestServicesDisconnectSaysWhenTheApplicationIsAlreadyBeingDisconnected(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	_, _, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--consumer", "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1cff", "--yes")
	if runError == nil || exitCodeFor(runError) != exitNotFound {
		t.Fatalf("a consumer the service does not have must be not found, got %v", runError)
	}

	platform = newFakeServicesPlatform(t)
	platform.extraConsumers = nil
	platform.instanceDisconnections = map[string]any{"state": "known", "items": []any{map[string]any{
		"id": "d15c0000-0000-4000-8000-0000000000aa", "consumer_id": "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1cff",
		"application_id": fakeServiceApplication, "cluster_id": fakeServiceClusterID, "namespace": "orders-old",
		"phase": "draining", "requested_at": "2026-11-01T09:00:00Z", "phase_changed_at": "2026-11-01T09:00:00Z", "keeps_connection_secret": true}}}
	_, _, runError = runServicesCommand(t, platform, "", "disconnect", "orders-db", "--consumer", "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1cff", "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "is already being disconnected") ||
		!strings.Contains(runError.Error(), "namespace orders-old, phase draining") {
		t.Fatalf("expected the disconnection in progress to be named, got %v", runError)
	}
	if prepares := platform.requests(http.MethodPost, "/disconnections"); len(prepares) != 0 {
		t.Errorf("nothing must be prepared for an environment already being disconnected")
	}
	if reads := platform.requests(http.MethodGet, "/instances/"+fakeServiceInstanceID); len(reads) != 1 {
		t.Errorf("a service named by name must be read once by id for its disconnections, got %d reads", len(reads))
	}
}

// A refused confirm (the plan moved, or expired) exits non-zero with the
// platform's sentence and the way forward; a retired service is refused
// before anything is prepared.
func TestServicesDisconnectRelaysRefusals(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.disconnectionConfirmConflict = true
	stdout, _, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--yes", "-o", "json")
	if runError == nil || exitCodeFor(runError) != exitError ||
		!strings.Contains(runError.Error(), "The disconnection changed since it was reviewed") || !strings.Contains(runError.Error(), "run the command again") {
		t.Fatalf("expected the platform's conflict with the way forward, got %v", runError)
	}
	if stdout != "" {
		t.Errorf("a refused confirm must leave stdout empty, got:\n%s", stdout)
	}

	platform = newFakeServicesPlatform(t)
	platform.released = true
	_, _, runError = runServicesCommand(t, platform, "", "disconnect", fakeServiceInstanceID, "--application", "storefront", "--yes")
	if runError == nil || !strings.Contains(runError.Error(), "nothing to disconnect") {
		t.Fatalf("a retired service must be refused, got %v", runError)
	}
	if prepares := platform.requests(http.MethodPost, "/disconnections"); len(prepares) != 0 {
		t.Errorf("nothing must be prepared for a retired service")
	}
}

// Enumerations a newer platform adds print as sent and never fail the
// command: a workloads state, a network reason and a phase this CLI does not
// know.
func TestServicesDisconnectPrintsUnknownValuesAsSent(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.disconnectionWorkloadsState = "partially_known"
	platform.disconnectionNetworkReason = "mesh_policy"
	platform.disconnectionConfirmPhase = "settling"
	stdout, _, runError := runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--yes")
	if runError != nil {
		t.Fatalf("an unknown enum value must not fail the command: %v", runError)
	}
	if !strings.Contains(stdout, "in_progress, phase settling") || strings.Contains(stdout, "Waiting:") {
		t.Errorf("an unknown phase prints as sent, without a meaning:\n%s", stdout)
	}

	platform = newFakeServicesPlatform(t)
	platform.disconnectionWorkloadsState = "partially_known"
	stdout, _, runError = runServicesCommand(t, platform, "", "disconnect", "orders-db", "--application", "storefront", "--review-only", "-o", "json")
	if runError != nil {
		t.Fatal(runError)
	}
	var review client.ServiceDisconnection
	if decodeError := json.Unmarshal([]byte(stdout), &review); decodeError != nil || review.Plan.Workloads.State != "partially_known" {
		t.Fatalf("-o json must carry the value as sent (%v): %s", decodeError, stdout)
	}
}

// `services get` lists the disconnections in progress under the consumers,
// with the phase and what it waits for; a list the platform could not read
// says so; a phase this CLI does not know prints as sent.
func TestServicesGetShowsDisconnectionsInProgress(t *testing.T) {
	platform := newFakeServicesPlatform(t)
	platform.instanceDisconnections = map[string]any{"state": "known", "items": []any{
		map[string]any{"id": "d15c0000-0000-4000-8000-0000000000aa", "consumer_id": "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1cff",
			"application_id": fakeServiceApplication, "cluster_id": fakeServiceClusterID, "namespace": "orders-old",
			"phase": "draining", "requested_at": "2026-11-01T09:00:00Z", "phase_changed_at": "2026-11-01T09:00:05Z", "keeps_connection_secret": true},
		map[string]any{"id": "d15c0000-0000-4000-8000-0000000000bb", "consumer_id": "5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1cee",
			"application_id": fakeServiceApplication, "cluster_id": fakeServiceClusterID, "namespace": "orders-qa",
			"phase": "settling", "requested_at": "2026-11-01T08:00:00Z", "phase_changed_at": "2026-11-01T08:30:00Z", "keeps_connection_secret": false},
	}}
	stdout, _, runError := runServicesCommand(t, platform, "", "get", fakeServiceInstanceID)
	if runError != nil {
		t.Fatal(runError)
	}
	for _, expected := range []string{"  Disconnecting:",
		"5b1f0c7e-0d7a-4c55-a6f4-2f1f4a9b1cff  application storefront, cluster", "namespace orders-old (disconnection d15c0000-0000-4000-8000-0000000000aa, confirmed 2026-11-01T09:00:00Z)",
		"Phase:    draining since 2026-11-01T09:00:05Z",
		"Waiting:  Ankra keeps the connection Secret until no pod and no deployed workload reads it",
		"Secret:   kept: every deploy of the application renders it again",
		"Phase:    settling since 2026-11-01T08:30:00Z"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("get output lacks %q:\n%s", expected, stdout)
		}
	}
	if strings.Count(stdout, "Waiting:") != 1 || strings.Count(stdout, "Secret:   kept") != 1 {
		t.Errorf("only the known phase has a meaning, and only the kept Secret is said kept:\n%s", stdout)
	}

	platform = newFakeServicesPlatform(t)
	platform.instanceDisconnections = map[string]any{"state": "unknown", "items": []any{}}
	stdout, _, runError = runServicesCommand(t, platform, "", "get", fakeServiceInstanceID)
	if runError != nil {
		t.Fatal(runError)
	}
	if !strings.Contains(stdout, "Disconnecting: not known") {
		t.Errorf("an unreadable list must be said to be unknown:\n%s", stdout)
	}

	for _, disconnections := range []map[string]any{nil, {"state": "known", "items": []any{}}} {
		platform = newFakeServicesPlatform(t)
		platform.instanceDisconnections = disconnections
		stdout, _, runError = runServicesCommand(t, platform, "", "get", fakeServiceInstanceID)
		if runError != nil {
			t.Fatal(runError)
		}
		if strings.Contains(stdout, "Disconnecting") {
			t.Errorf("%v: no disconnection in progress prints nothing:\n%s", disconnections, stdout)
		}
	}
}

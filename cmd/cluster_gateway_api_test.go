package cmd

// `ankra cluster gateway-api status|enable` against a scripted platform: the
// real client talks to an httptest server that answers the cluster lookup and
// the per-provider gateway-api route, so the method, path and body each verb
// sends are pinned on the request itself, not on a mock's arguments.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// gatewayAPIOtherClusterID is a cluster reached through --cluster rather
// than the selected one.
const gatewayAPIOtherClusterID = "44444444-4444-4444-8444-444444444444"

type gatewayAPIRequest struct {
	method        string
	path          string
	authorization string
	body          string
}

// gatewayAPIFault is one scripted failure of a gateway-api request.
type gatewayAPIFault struct {
	status int
	body   string
}

// gatewayAPIServer is a platform that lists every cluster with one kind and
// serves the gateway-api route from scripted answers: reads come from gets in
// order and writes from puts in order, the last of each repeating. A refusal,
// when set, answers every gateway-api request of its method with it. A fault
// answers one request of a method, by its position among that method's
// requests (0 is the first), without using up a scripted answer.
type gatewayAPIServer struct {
	mutex         sync.Mutex
	kind          string
	gets          []client.GatewayAPIStatus
	puts          []client.GatewayAPIEnableResult
	getIndex      int
	putIndex      int
	requests      []gatewayAPIRequest
	refusalMethod string
	refusalStatus int
	refusalBody   string
	faults        map[string]map[int]gatewayAPIFault
	positions     map[string]int
}

func scriptedAnswerIndex(index int, length int) int {
	if index >= length {
		return length - 1
	}
	return index
}

func newGatewayAPIServer(t *testing.T, kind string, gets ...client.GatewayAPIStatus) *gatewayAPIServer {
	t.Helper()
	recorder := &gatewayAPIServer{kind: kind, gets: gets,
		faults: map[string]map[int]gatewayAPIFault{}, positions: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters" {
			// The cluster lookup: the selected cluster is stored as id and
			// name, so the command asks the platform for its kind.
			recorder.mutex.Lock()
			listedKind := recorder.kind
			recorder.mutex.Unlock()
			_, _ = writer.Write([]byte(`{"result":[{"id":"` + request.URL.Query().Get("cluster_id") +
				`","name":"test-cluster","kind":"` + listedKind + `"}],"pagination":{"total_count":1}}`))
			return
		}
		underClusters, hasPrefix := strings.CutPrefix(request.URL.Path, "/api/v1/clusters/")
		if !hasPrefix || !strings.HasSuffix(underClusters, "/gateway-api") {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"unscripted route in test"}`))
			return
		}
		raw, _ := io.ReadAll(request.Body)
		recorded := gatewayAPIRequest{method: request.Method, path: request.URL.Path,
			authorization: request.Header.Get("Authorization"), body: string(raw)}

		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		recorder.requests = append(recorder.requests, recorded)
		if recorder.refusalStatus != 0 && (recorder.refusalMethod == "" || recorder.refusalMethod == request.Method) {
			writer.WriteHeader(recorder.refusalStatus)
			_, _ = writer.Write([]byte(recorder.refusalBody))
			return
		}
		position := recorder.positions[request.Method]
		recorder.positions[request.Method]++
		if fault, scripted := recorder.faults[request.Method][position]; scripted {
			writer.WriteHeader(fault.status)
			_, _ = writer.Write([]byte(fault.body))
			return
		}
		switch request.Method {
		case http.MethodGet:
			if len(recorder.gets) == 0 {
				writer.WriteHeader(http.StatusInternalServerError)
				_, _ = writer.Write([]byte(`{"detail":"no GET scripted in test"}`))
				return
			}
			answer := recorder.gets[scriptedAnswerIndex(recorder.getIndex, len(recorder.gets))]
			recorder.getIndex++
			_ = json.NewEncoder(writer).Encode(answer)
		case http.MethodPut:
			if len(recorder.puts) == 0 {
				writer.WriteHeader(http.StatusInternalServerError)
				_, _ = writer.Write([]byte(`{"detail":"no PUT scripted in test"}`))
				return
			}
			answer := recorder.puts[scriptedAnswerIndex(recorder.putIndex, len(recorder.puts))]
			recorder.putIndex++
			_ = json.NewEncoder(writer).Encode(answer)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	writeSelectedClusterJSON(t)
	setMockClient(t, client.New("test-token", server.URL))
	// Every test answers on a pipe, never a terminal, unless it opts in.
	previousInteractive := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return false }
	previousInterval := gatewayAPIPollInterval
	gatewayAPIPollInterval = time.Millisecond
	t.Cleanup(func() {
		promptIsInteractive = previousInteractive
		gatewayAPIPollInterval = previousInterval
	})
	return recorder
}

func (recorder *gatewayAPIServer) scriptPuts(answers ...client.GatewayAPIEnableResult) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.puts = answers
}

func (recorder *gatewayAPIServer) refuse(method string, status int, body string) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.refusalMethod, recorder.refusalStatus, recorder.refusalBody = method, status, body
}

// failAt answers the method's requests at these positions with the fault.
func (recorder *gatewayAPIServer) failAt(method string, fault gatewayAPIFault, positions ...int) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.faults[method] == nil {
		recorder.faults[method] = map[int]gatewayAPIFault{}
	}
	for _, position := range positions {
		recorder.faults[method][position] = fault
	}
}

func (recorder *gatewayAPIServer) seen() []gatewayAPIRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]gatewayAPIRequest{}, recorder.requests...)
}

func (recorder *gatewayAPIServer) methods() []string {
	var methods []string
	for _, request := range recorder.seen() {
		methods = append(methods, request.method)
	}
	return methods
}

// gatewayAPIOff is a cluster with the option off: both members managed, the
// values still to be written, no CRDs on the cluster.
func gatewayAPIOff() client.GatewayAPIStatus {
	return client.GatewayAPIStatus{
		Available: true,
		Phase:     client.GatewayAPIPhaseOff,
		CRDs: client.GatewayAPICRDs{State: "absent", Observed: []string{},
			Missing: []string{"gatewayclasses.gateway.networking.k8s.io"}, AddonApplied: "unknown"},
		Traefik: client.GatewayAPIMember{Addon: "traefik", Managed: true, Values: client.GatewayAPIValuesAdd,
			Conflicts: []client.GatewayAPIFinding{}, Applied: "applied"},
		CertManager: client.GatewayAPIMember{Addon: "cert-manager", Managed: true, Values: client.GatewayAPIValuesAdd,
			Conflicts: []client.GatewayAPIFinding{}, Applied: "applied"},
		ResourceIDs: []string{"resource-1"},
		Applies:     "Recording the option hands the cluster's cloud-provider resource to the reconciler.",
		Note:        "The Gateway API is off for this cluster: Traefik serves Ingress only.",
	}
}

// gatewayAPIRecorded is the standing after the option is recorded, at the
// given phase, with the CRDs coming from the addon.
func gatewayAPIRecorded(phase string, note string) client.GatewayAPIStatus {
	status := gatewayAPIOff()
	status.Enabled = true
	status.Phase = phase
	status.CRDs.Source = "addon"
	status.Note = note
	return status
}

func gatewayAPIEnableAnswer(status client.GatewayAPIStatus, recorded bool, valuesWritten bool) client.GatewayAPIEnableResult {
	return client.GatewayAPIEnableResult{GatewayAPIStatus: status, Changed: recorded || valuesWritten,
		Recorded: recorded, ValuesWritten: valuesWritten}
}

// gatewayAPICommands collects the `cluster gateway-api` tree so a test can
// put its flags back: the commands are built once in init and shared by the
// whole package, so a --yes left set would leak into the next test.
func gatewayAPICommands(t *testing.T) []*cobra.Command {
	t.Helper()
	for _, candidate := range clusterCmd.Commands() {
		if candidate.Name() == "gateway-api" {
			return append([]*cobra.Command{candidate}, candidate.Commands()...)
		}
	}
	t.Fatalf("the 'cluster gateway-api' command is not registered")
	return nil
}

// runGatewayAPI executes the command with input on stdin and returns stdout
// and stderr apart, so structured output can be checked for being clean.
func runGatewayAPI(t *testing.T, input string, arguments ...string) (string, string, error) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "gateway-api"}, arguments...))
	resetTreeFlags(t, gatewayAPICommands(t)...)
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetTreeFlags(t, gatewayAPICommands(t)...)
	})
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func TestClusterGatewayAPIStatusPrintsThePlanOverTheProviderRoute(t *testing.T) {
	recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff())

	stdout, _, runError := runGatewayAPI(t, "", "status")
	if runError != nil {
		t.Fatalf("status failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].method != http.MethodGet ||
		requests[0].path != "/api/v1/clusters/hetzner/"+testClusterID+"/gateway-api" {
		t.Fatalf("expected one GET of the selected cluster's option on the hetzner route, got %+v", requests)
	}
	if requests[0].authorization != "Bearer test-token" {
		t.Fatalf("the read must use the bearer route, got Authorization %q", requests[0].authorization)
	}
	assertContainsAll(t, stdout, []string{
		"Gateway API for cluster 'test-cluster' (hetzner):",
		"State:        off (Traefik serves Ingress only)",
		"CRDs:         no source recorded yet; the watch sees absent (0 of 1 kinds); not established",
		"Traefik:      Gateway API settings still to be written (apply applied)",
		"cert-manager: Gateway API settings still to be written (apply applied)",
		"The Gateway API is off for this cluster: Traefik serves Ingress only.",
		"Turn it on with 'ankra cluster gateway-api enable'.",
	})
	if strings.Contains(stdout, "Conflicts") {
		t.Fatalf("a plan without conflicts printed a conflicts section:\n%s", stdout)
	}

	// Run without a subcommand, the command shows the same plan.
	bare, _, bareError := runGatewayAPI(t, "")
	if bareError != nil {
		t.Fatalf("bare gateway-api failed: %v", bareError)
	}
	if bare != stdout {
		t.Fatalf("the bare command printed something other than status:\n%s\n--- status ---\n%s", bare, stdout)
	}
}

func TestClusterGatewayAPIStatusHonoursTheClusterFlag(t *testing.T) {
	recorder := newGatewayAPIServer(t, "upcloud", gatewayAPIOff())

	if _, _, runError := runGatewayAPI(t, "", "status", "--cluster", gatewayAPIOtherClusterID); runError != nil {
		t.Fatalf("status --cluster failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 1 || requests[0].path != "/api/v1/clusters/upcloud/"+gatewayAPIOtherClusterID+"/gateway-api" {
		t.Fatalf("expected the --cluster id on its own kind's route, got %+v", requests)
	}
}

func TestClusterGatewayAPIStatusJSONIsTheResponseBody(t *testing.T) {
	newGatewayAPIServer(t, "hetzner", gatewayAPIOff())

	stdout, _, runError := runGatewayAPI(t, "", "status", "-o", "json")
	if runError != nil {
		t.Fatalf("status -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON: %v\n%s", decodeError, stdout)
	}
	if decoded["phase"] != client.GatewayAPIPhaseOff || decoded["available"] != true {
		t.Fatalf("decoded %v, want the platform's phase and availability", decoded)
	}
	crds, isObject := decoded["crds"].(map[string]any)
	if !isObject || crds["state"] != "absent" {
		t.Fatalf("decoded crds %v, want the platform's reading", decoded["crds"])
	}
	if len(decoded) != 11 {
		t.Fatalf("decoded %d fields %v, want exactly the response's eleven", len(decoded), decoded)
	}
}

// A cluster Ankra did not build has no Ankra-managed ingress to serve the
// Gateway API: its kind is refused before any request to the option's route.
func TestClusterGatewayAPIRefusesAClusterAnkraDidNotBuild(t *testing.T) {
	recorder := newGatewayAPIServer(t, "imported", gatewayAPIOff())

	_, _, runError := runGatewayAPI(t, "", "enable", "--yes")
	if runError == nil {
		t.Fatal("expected the kind refusal, got success")
	}
	if code := exitCodeFor(runError); code != exitUsage {
		t.Fatalf("exit code = %d, want %d (usage): %v", code, exitUsage, runError)
	}
	if !strings.Contains(runError.Error(), "cluster 'test-cluster' does not support the Gateway API option (its kind is imported)") {
		t.Fatalf("error = %q, want it to name the kind", runError.Error())
	}
	if requests := recorder.seen(); len(requests) != 0 {
		t.Fatalf("a refused kind still reached the option's route: %+v", requests)
	}
}

func TestClusterGatewayAPIEnableNeedsConfirmationWithoutATerminal(t *testing.T) {
	recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff())

	stdout, stderr, runError := runGatewayAPI(t, "", "enable")
	if runError == nil {
		t.Fatal("expected the confirmation refusal, got success")
	}
	if code := exitCodeFor(runError); code != exitUsage {
		t.Fatalf("exit code = %d, want %d (usage): %v", code, exitUsage, runError)
	}
	if !strings.Contains(runError.Error(), "re-run with --yes") {
		t.Fatalf("error = %q, want it to name --yes", runError.Error())
	}
	assertContainsAll(t, stderr, []string{
		"Turning the Gateway API on for cluster 'test-cluster' (hetzner)",
		"This will:",
		"record the option on the cluster",
		"install the Gateway API CRDs through a gateway-api-crds addon",
		"write the Gateway API settings into the traefik values once the CRDs are established",
		"write the Gateway API settings into the cert-manager values once the CRDs are established",
		"There is no 'disable'",
	})
	if stdout != "" {
		t.Fatalf("the plan must go to stderr, stdout got:\n%s", stdout)
	}
	if methods := recorder.methods(); len(methods) != 1 || methods[0] != http.MethodGet {
		t.Fatalf("expected the plan read and no write, got %v", methods)
	}
}

func TestClusterGatewayAPIEnableDeclinedPromptExitsFour(t *testing.T) {
	recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff())
	promptIsInteractive = func(io.Reader) bool { return true }

	_, stderr, runError := runGatewayAPI(t, "n\n", "enable")
	if code := exitCodeFor(runError); code != exitCancelled {
		t.Fatalf("exit code = %d, want %d (cancelled): %v", code, exitCancelled, runError)
	}
	if !strings.Contains(stderr, "Turn the Gateway API on for this cluster? [y/N]:") {
		t.Fatalf("expected the question on stderr, got:\n%s", stderr)
	}
	if methods := recorder.methods(); len(methods) != 1 || methods[0] != http.MethodGet {
		t.Fatalf("a declined prompt still wrote: %v", methods)
	}
}

func TestClusterGatewayAPIEnableRecordsTheOptionAndSaysWhatIsNext(t *testing.T) {
	recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff())
	pending := gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending,
		"The CRD addon is not on the cluster yet. The Traefik and cert-manager values are written once the CRDs are established; turn the Gateway API on again then.")
	recorder.scriptPuts(gatewayAPIEnableAnswer(pending, true, false))

	stdout, stderr, runError := runGatewayAPI(t, "", "enable", "--yes")
	if runError != nil {
		t.Fatalf("enable --yes failed: %v", runError)
	}
	requests := recorder.seen()
	if len(requests) != 2 || requests[0].method != http.MethodGet || requests[1].method != http.MethodPut {
		t.Fatalf("expected the plan read then one write, got %+v", requests)
	}
	if requests[1].path != "/api/v1/clusters/hetzner/"+testClusterID+"/gateway-api" || requests[1].body != "" {
		t.Fatalf("the write must PUT the option's route with no body, got %+v", requests[1])
	}
	assertContainsAll(t, stdout, []string{
		"Gateway API enable for cluster 'test-cluster' (hetzner):",
		"Recorded:       yes (this call turned the option on)",
		"Values written: no (not by this call)",
		"State:        recorded, waiting for the Gateway API CRDs",
		"CRDs:         from the gateway-api-crds addon;",
		"turn the Gateway API on again then.",
		"Follow it to enabled with 'ankra cluster gateway-api enable --wait'",
	})
	if !strings.Contains(stderr, "This will:") {
		t.Fatalf("the plan must still be shown with --yes, stderr:\n%s", stderr)
	}
}

func TestClusterGatewayAPIEnableJSONIsTheWriteResponse(t *testing.T) {
	recorder := newGatewayAPIServer(t, "ovh", gatewayAPIOff())
	recorder.scriptPuts(gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "pending"), true, false))

	stdout, _, runError := runGatewayAPI(t, "", "enable", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("enable --yes -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON: %v\n%s", decodeError, stdout)
	}
	if decoded["recorded"] != true || decoded["values_written"] != false || decoded["phase"] != client.GatewayAPIPhaseCRDsPending {
		t.Fatalf("decoded %v, want the write's own answer", decoded)
	}
}

// The platform writes the values only on an enable call once the CRDs are
// established, so --wait repeats the enable when the phase says the values
// are next, and returns once the Gateway API is enabled.
func TestClusterGatewayAPIEnableWaitRepeatsTheEnableWhenTheValuesAreNext(t *testing.T) {
	recorder := newGatewayAPIServer(t, "hetzner",
		gatewayAPIOff(),
		gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"),
		gatewayAPIRecorded(client.GatewayAPIPhaseValuesPending, "The CRDs are established; turning the Gateway API on again writes the Traefik and cert-manager values."),
		gatewayAPIRecorded(client.GatewayAPIPhaseEnabled, "The Gateway API is on."),
	)
	applying := gatewayAPIRecorded(client.GatewayAPIPhaseApplying, "applying")
	applying.Traefik.Values, applying.CertManager.Values = client.GatewayAPIValuesAlreadyOn, client.GatewayAPIValuesAlreadyOn
	recorder.scriptPuts(
		gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"), true, false),
		gatewayAPIEnableAnswer(applying, false, true),
	)

	stdout, stderr, runError := runGatewayAPI(t, "", "enable", "--yes", "--wait", "--timeout", "10s")
	if runError != nil {
		t.Fatalf("enable --wait failed: %v\nstderr:\n%s", runError, stderr)
	}
	want := []string{http.MethodGet, http.MethodPut, http.MethodGet, http.MethodGet, http.MethodPut, http.MethodGet}
	if got := recorder.methods(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("request sequence = %v, want %v (plan, record, poll, poll finds the values next, repeat the enable, poll finds it enabled)", got, want)
	}
	assertContainsAll(t, stderr, []string{
		"recorded, waiting for the Gateway API CRDs",
		"CRDs established, values still to be written",
		"values written, Traefik or cert-manager is applying them",
		"enabled",
		"The Gateway API is on for cluster 'test-cluster'.",
	})
	assertContainsAll(t, stdout, []string{
		"Gateway API for cluster 'test-cluster' (hetzner):",
		"State:        enabled",
		"The Gateway API is on.",
	})
}

func TestClusterGatewayAPIEnableWaitJSONIsTheFinalStanding(t *testing.T) {
	recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff(), gatewayAPIRecorded(client.GatewayAPIPhaseEnabled, "on"))
	recorder.scriptPuts(gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseApplying, "applying"), true, true))

	stdout, _, runError := runGatewayAPI(t, "", "enable", "--yes", "--wait", "--timeout", "10s", "-o", "json")
	if runError != nil {
		t.Fatalf("enable --wait -o json failed: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("structured output is not one JSON document: %v\n%s", decodeError, stdout)
	}
	if decoded["phase"] != client.GatewayAPIPhaseEnabled {
		t.Fatalf("decoded phase %v, want the standing the wait ended on", decoded["phase"])
	}
}

func TestClusterGatewayAPIEnableWaitStopsWhenThePlatformNeedsAPerson(t *testing.T) {
	conflict := gatewayAPIRecorded(client.GatewayAPIPhaseConflict,
		"The option is recorded, but a key in the Traefik or cert-manager values says the opposite of what the Gateway API needs (see conflicts).")
	conflict.Traefik.Values = client.GatewayAPIValuesConflict
	conflict.Traefik.Conflicts = []client.GatewayAPIFinding{{Addon: "traefik", Key: "providers.kubernetesGateway.enabled",
		Outcome: "conflict", Detail: "the values set it to false"}}
	recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff(), conflict)
	recorder.scriptPuts(gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"), true, false))

	stdout, _, runError := runGatewayAPI(t, "", "enable", "--yes", "--wait", "--timeout", "10s")
	if code := exitCodeFor(runError); code != exitError {
		t.Fatalf("exit code = %d, want %d (the platform needs a person): %v", code, exitError, runError)
	}
	if !strings.Contains(runError.Error(), "needs attention (conflict)") {
		t.Fatalf("error = %q, want it to name the phase", runError.Error())
	}
	assertContainsAll(t, stdout, []string{
		"State:        recorded, held by a conflict in the values",
		"Traefik:      conflict on 1 key, see below",
		"Conflicts (nothing is written to these values until they are resolved):",
		"traefik providers.kubernetesGateway.enabled: the values set it to false",
	})
	if got := recorder.methods(); strings.Join(got, " ") != "GET PUT GET" {
		t.Fatalf("request sequence = %v, want the plan, the record and one poll", got)
	}
}

// The verdict does not depend on --wait: when the write's own answer is a
// phase the platform holds for a person, `enable --yes` exits 1 with the same
// message the --wait path gives, so a script is not told "turned on" because
// it did not ask to follow.
func TestClusterGatewayAPIEnableWithoutWaitExitsOneWhenThePlatformNeedsAPerson(t *testing.T) {
	for _, phase := range []string{
		client.GatewayAPIPhaseConflict, client.GatewayAPIPhaseCRDsFailed,
		client.GatewayAPIPhaseApplyFailed, client.GatewayAPIPhaseNoPlatformNetworking,
	} {
		t.Run(phase, func(t *testing.T) {
			held := gatewayAPIRecorded(phase, "the platform's note for "+phase)
			if phase == client.GatewayAPIPhaseConflict {
				held.Traefik.Values = client.GatewayAPIValuesConflict
				held.Traefik.Conflicts = []client.GatewayAPIFinding{{Addon: "traefik", Key: "providers.kubernetesGateway.enabled",
					Outcome: "conflict", Detail: "the values set it to false"}}
			}
			recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff())
			recorder.scriptPuts(gatewayAPIEnableAnswer(held, true, false))

			stdout, stderr, runError := runGatewayAPI(t, "", "enable", "--yes")
			if code := exitCodeFor(runError); code != exitError {
				t.Fatalf("exit code = %d, want %d (the platform needs a person): %v", code, exitError, runError)
			}
			want := "needs attention (" + phase + "): the platform's note for " + phase
			if !strings.Contains(runError.Error(), want) {
				t.Fatalf("error = %q, want it to carry %q", runError.Error(), want)
			}
			if !strings.Contains(stderr, want) {
				t.Fatalf("the verdict must reach stderr, got:\n%s", stderr)
			}
			if !strings.Contains(stdout, "Recorded:       yes (this call turned the option on)") {
				t.Fatalf("the write's answer must still be printed, got:\n%s", stdout)
			}
			if strings.Contains(stdout, "Follow it to enabled") {
				t.Fatalf("a phase that needs a person must not be given the --wait hint, got:\n%s", stdout)
			}
			if got := recorder.methods(); strings.Join(got, " ") != "GET PUT" {
				t.Fatalf("request sequence = %v, want the plan and the record only", got)
			}
		})
	}
}

// gatewayAPIPushFailed is a values write the platform took but could not
// push: the values are in the database and the apply is moving, the GitOps
// repository does not carry them.
func gatewayAPIPushFailed() client.GatewayAPIEnableResult {
	applying := gatewayAPIRecorded(client.GatewayAPIPhaseApplying, "Traefik is applying the values.")
	applying.Traefik.Values, applying.CertManager.Values = client.GatewayAPIValuesAlreadyOn, client.GatewayAPIValuesAlreadyOn
	answer := gatewayAPIEnableAnswer(applying, false, true)
	commit, operation := "0123abcd", "op-1"
	pushFailure := "git push to origin was refused: the deploy key is read-only; the commit is kept and pushed on the next sync"
	answer.CommitSHA, answer.OperationID, answer.GitPushFailure = &commit, &operation, &pushFailure
	return answer
}

// A values write whose GitOps push failed exits 1 without --wait: the answer
// is still printed (and -o json is still the one document), the failure text
// reaches stderr, and the --wait hint is withheld because the next step is a
// person's.
func TestClusterGatewayAPIEnableWithoutWaitExitsOneWhenTheGitOpsPushFailed(t *testing.T) {
	recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIRecorded(client.GatewayAPIPhaseValuesPending, "values next"))
	recorder.scriptPuts(gatewayAPIPushFailed())

	stdout, stderr, runError := runGatewayAPI(t, "", "enable", "--yes")
	if code := exitCodeFor(runError); code != exitError {
		t.Fatalf("exit code = %d, want %d (the GitOps push failed): %v", code, exitError, runError)
	}
	assertContainsAll(t, stderr, []string{
		"the Gateway API values for cluster 'test-cluster' reached the platform, but the GitOps push failed:",
		"git push to origin was refused: the deploy key is read-only",
	})
	assertContainsAll(t, stdout, []string{
		"Values written: yes (this call wrote the Traefik and cert-manager values)",
		"GitOps commit:  0123abcd",
		"GitOps push:    git push to origin was refused",
		"State:        values written, Traefik or cert-manager is applying them",
	})
	if strings.Contains(stdout, "Follow it to enabled") {
		t.Fatalf("a failed push must not be given the --wait hint, got:\n%s", stdout)
	}

	recorder = newGatewayAPIServer(t, "hetzner", gatewayAPIRecorded(client.GatewayAPIPhaseValuesPending, "values next"))
	recorder.scriptPuts(gatewayAPIPushFailed())
	stdout, _, runError = runGatewayAPI(t, "", "enable", "--yes", "-o", "json")
	if code := exitCodeFor(runError); code != exitError {
		t.Fatalf("-o json exit code = %d, want %d (the verdict travels on the exit code): %v", code, exitError, runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("structured output is not one JSON document: %v\n%s", decodeError, stdout)
	}
	if decoded["values_written"] != true || decoded["git_push_failure"] == nil {
		t.Fatalf("decoded %v, want the write's own answer with its push failure", decoded)
	}
	if got := recorder.methods(); strings.Join(got, " ") != "GET PUT" {
		t.Fatalf("request sequence = %v, want the plan and the write only", got)
	}
}

// With --wait the push failure is a verdict too, whether it comes back on the
// first enable or on the one the wait repeats when the values are next: the
// follow stops there instead of reporting "enabled" for values the repository
// does not carry.
func TestClusterGatewayAPIEnableWaitExitsOneWhenTheGitOpsPushFailed(t *testing.T) {
	t.Run("on the first enable", func(t *testing.T) {
		recorder := newGatewayAPIServer(t, "hetzner",
			gatewayAPIRecorded(client.GatewayAPIPhaseValuesPending, "values next"),
			gatewayAPIRecorded(client.GatewayAPIPhaseEnabled, "on"))
		recorder.scriptPuts(gatewayAPIPushFailed())

		stdout, stderr, runError := runGatewayAPI(t, "", "enable", "--yes", "--wait", "--timeout", "10s")
		if code := exitCodeFor(runError); code != exitError {
			t.Fatalf("exit code = %d, want %d (the GitOps push failed): %v", code, exitError, runError)
		}
		if !strings.Contains(stderr, "git push to origin was refused: the deploy key is read-only") {
			t.Fatalf("the failure text must reach stderr, got:\n%s", stderr)
		}
		if !strings.Contains(stdout, "State:        values written, Traefik or cert-manager is applying them") {
			t.Fatalf("the standing the wait stopped on must be printed, got:\n%s", stdout)
		}
		if got := recorder.methods(); strings.Join(got, " ") != "GET PUT" {
			t.Fatalf("request sequence = %v, want the plan and the write, no poll after a failed push", got)
		}
	})
	t.Run("on the repeated enable", func(t *testing.T) {
		recorder := newGatewayAPIServer(t, "hetzner",
			gatewayAPIOff(),
			gatewayAPIRecorded(client.GatewayAPIPhaseValuesPending, "values next"),
			gatewayAPIRecorded(client.GatewayAPIPhaseEnabled, "on"))
		recorder.scriptPuts(
			gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"), true, false),
			gatewayAPIPushFailed())

		_, stderr, runError := runGatewayAPI(t, "", "enable", "--yes", "--wait", "--timeout", "10s")
		if code := exitCodeFor(runError); code != exitError {
			t.Fatalf("exit code = %d, want %d (the GitOps push failed): %v", code, exitError, runError)
		}
		if !strings.Contains(stderr, "reached the platform, but the GitOps push failed: git push to origin was refused") {
			t.Fatalf("the failure text must reach stderr, got:\n%s", stderr)
		}
		if got := recorder.methods(); strings.Join(got, " ") != "GET PUT GET PUT" {
			t.Fatalf("request sequence = %v, want plan, record, the poll that finds the values next, the repeated enable, and nothing after the failed push", got)
		}
	})
}

func TestClusterGatewayAPIEnableWaitExpiresWithExitFive(t *testing.T) {
	recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff(), gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"))
	recorder.scriptPuts(gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"), true, false))

	stdout, _, runError := runGatewayAPI(t, "", "enable", "--yes", "--wait", "--timeout", "30ms")
	if code := exitCodeFor(runError); code != exitWaitTimeout {
		t.Fatalf("exit code = %d, want %d (wait expired): %v", code, exitWaitTimeout, runError)
	}
	if !strings.Contains(runError.Error(), "not enabled for cluster 'test-cluster' after 30ms (phase crds_pending)") {
		t.Fatalf("error = %q, want the phase it stopped on", runError.Error())
	}
	if !strings.Contains(stdout, "State:        recorded, waiting for the Gateway API CRDs") {
		t.Fatalf("the standing the wait ended on must be printed, got:\n%s", stdout)
	}
	if methods := recorder.methods(); len(methods) < 2 || methods[1] != http.MethodPut {
		t.Fatalf("expected the plan read and the record before polling, got %v", methods)
	}
}

// gatewayAPIServerFault is a platform answering 500 for one request: the
// client's transport does not retry it, so the follow sees it.
var gatewayAPIServerFault = gatewayAPIFault{status: http.StatusInternalServerError, body: `{"detail":"Internal Server Error"}`}

// A passing fault while --wait follows the enable, on a read or on the enable
// the wait repeats when the values are next, is tried again on the next poll
// instead of ending a follow whose enable keeps going on the platform.
func TestClusterGatewayAPIEnableWaitRidesOutAPassingFault(t *testing.T) {
	t.Run("on a read", func(t *testing.T) {
		recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff(), gatewayAPIRecorded(client.GatewayAPIPhaseEnabled, "on"))
		recorder.scriptPuts(gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"), true, false))
		recorder.failAt(http.MethodGet, gatewayAPIServerFault, 1, 2)

		stdout, stderr, runError := runGatewayAPI(t, "", "enable", "--yes", "--wait", "--timeout", "10s")
		if runError != nil {
			t.Fatalf("enable --wait failed on a passing fault: %v\nstderr:\n%s", runError, stderr)
		}
		if got := recorder.methods(); strings.Join(got, " ") != "GET PUT GET GET GET" {
			t.Fatalf("request sequence = %v, want the plan, the record, two failed polls and the poll that finds it enabled", got)
		}
		if count := strings.Count(stderr, "trying again on the next poll"); count != 2 {
			t.Fatalf("want each passing fault reported once on stderr, got %d:\n%s", count, stderr)
		}
		if !strings.Contains(stdout, "State:        enabled") {
			t.Fatalf("the standing the wait ended on must be printed, got:\n%s", stdout)
		}
	})
	t.Run("on the repeated enable", func(t *testing.T) {
		recorder := newGatewayAPIServer(t, "hetzner",
			gatewayAPIOff(),
			gatewayAPIRecorded(client.GatewayAPIPhaseValuesPending, "values next"),
			gatewayAPIRecorded(client.GatewayAPIPhaseValuesPending, "values next"),
			gatewayAPIRecorded(client.GatewayAPIPhaseEnabled, "on"))
		applying := gatewayAPIRecorded(client.GatewayAPIPhaseApplying, "applying")
		applying.Traefik.Values, applying.CertManager.Values = client.GatewayAPIValuesAlreadyOn, client.GatewayAPIValuesAlreadyOn
		recorder.scriptPuts(
			gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"), true, false),
			gatewayAPIEnableAnswer(applying, false, true))
		recorder.failAt(http.MethodPut, gatewayAPIServerFault, 1)

		_, stderr, runError := runGatewayAPI(t, "", "enable", "--yes", "--wait", "--timeout", "10s")
		if runError != nil {
			t.Fatalf("enable --wait failed on a passing fault: %v\nstderr:\n%s", runError, stderr)
		}
		want := "GET PUT GET PUT GET PUT GET"
		if got := recorder.methods(); strings.Join(got, " ") != want {
			t.Fatalf("request sequence = %v, want %s (the failed repeated enable is repeated after the next poll)", got, want)
		}
	})
}

// Five passing faults in a row end the follow with exit 1, and the message
// says what the timeout's message says: the option is recorded, the enable
// keeps going on the platform, and how to keep following it.
func TestClusterGatewayAPIEnableWaitStopsAfterTheFaultBudgetAndSaysTheEnableKeepsGoing(t *testing.T) {
	recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff())
	recorder.scriptPuts(gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"), true, false))
	recorder.failAt(http.MethodGet, gatewayAPIServerFault, 1, 2, 3, 4, 5, 6)

	stdout, stderr, runError := runGatewayAPI(t, "", "enable", "--yes", "--wait", "--timeout", "10s")
	if code := exitCodeFor(runError); code != exitError {
		t.Fatalf("exit code = %d, want %d: %v", code, exitError, runError)
	}
	for _, want := range []string{
		"stopped following the Gateway API enable for cluster 'test-cluster' after 5 failed requests in a row",
		"Internal Server Error",
		"The option is recorded and the enable keeps going on the platform",
		"run 'ankra cluster gateway-api enable --wait' again to keep following it",
	} {
		if !strings.Contains(runError.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", runError.Error(), want)
		}
	}
	if count := strings.Count(stderr, "trying again on the next poll"); count != gatewayAPIFollowFaultBudget-1 {
		t.Fatalf("want %d faults tried again before the follow stops, got %d:\n%s", gatewayAPIFollowFaultBudget-1, count, stderr)
	}
	if !strings.Contains(stdout, "State:        recorded, waiting for the Gateway API CRDs") {
		t.Fatalf("the standing the wait stopped on must be printed, got:\n%s", stdout)
	}
	if got := recorder.methods(); strings.Join(got, " ") != "GET PUT GET GET GET GET GET" {
		t.Fatalf("request sequence = %v, want the plan, the record and five failed polls", got)
	}
}

// A refusal while --wait follows is the platform's answer, not a passing
// fault: it ends the follow at once with its own exit code.
func TestClusterGatewayAPIEnableWaitStopsAtOnceOnARefusal(t *testing.T) {
	cases := []struct {
		name     string
		fault    gatewayAPIFault
		wantCode int
	}{
		{name: "a cluster gone from the organisation exits 3",
			fault: gatewayAPIFault{status: http.StatusNotFound, body: `{"detail":"Cluster not found"}`}, wantCode: exitNotFound},
		{name: "a role that cannot read the cluster exits 7",
			fault: gatewayAPIFault{status: http.StatusForbidden, body: `{"detail":"Forbidden"}`}, wantCode: exitForbidden},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff(), gatewayAPIRecorded(client.GatewayAPIPhaseEnabled, "on"))
			recorder.scriptPuts(gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"), true, false))
			recorder.failAt(http.MethodGet, testCase.fault, 1)

			_, stderr, runError := runGatewayAPI(t, "", "enable", "--yes", "--wait", "--timeout", "10s")
			if code := exitCodeFor(runError); code != testCase.wantCode {
				t.Fatalf("exit code = %d, want %d: %v", code, testCase.wantCode, runError)
			}
			if strings.Contains(stderr, "trying again") {
				t.Fatalf("a refusal was tried again:\n%s", stderr)
			}
			if got := recorder.methods(); strings.Join(got, " ") != "GET PUT GET" {
				t.Fatalf("request sequence = %v, want the plan, the record and the refused poll", got)
			}
		})
	}
}

// --timeout bounds --wait: without it the value would be a bound the command
// never applies, so it is refused before any request, as is a timeout that
// is not positive.
func TestClusterGatewayAPIEnableRefusesATimeoutItWouldNotApply(t *testing.T) {
	cases := []struct {
		arguments []string
		wantText  string
	}{
		{arguments: []string{"enable", "--yes", "--timeout", "30m"}, wantText: "--timeout bounds --wait: pass it with --wait"},
		{arguments: []string{"enable", "--yes", "--wait", "--timeout", "0s"}, wantText: "--timeout must be a positive duration, got 0s"},
	}
	for _, testCase := range cases {
		t.Run(strings.Join(testCase.arguments, " "), func(t *testing.T) {
			recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIOff())
			recorder.scriptPuts(gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"), true, false))

			_, _, runError := runGatewayAPI(t, "", testCase.arguments...)
			if code := exitCodeFor(runError); code != exitUsage {
				t.Fatalf("exit code = %d, want %d (usage): %v", code, exitUsage, runError)
			}
			if !strings.Contains(runError.Error(), testCase.wantText) {
				t.Fatalf("error = %q, want it to contain %q", runError.Error(), testCase.wantText)
			}
			if requests := recorder.seen(); len(requests) != 0 {
				t.Fatalf("a refused --timeout still reached the option's route: %+v", requests)
			}
		})
	}
}

// A member whose values state the platform did not report is kept unknown in
// the plan, the way 'status' prints "values not reported", rather than shown
// as a planned write.
func TestClusterGatewayAPIEnablePlanKeepsAnUnreportedValuesStateUnknown(t *testing.T) {
	plan := gatewayAPIOff()
	plan.CertManager.Values = ""
	newGatewayAPIServer(t, "hetzner", plan)

	_, stderr, runError := runGatewayAPI(t, "", "enable")
	if code := exitCodeFor(runError); code != exitUsage {
		t.Fatalf("exit code = %d, want %d (no terminal to confirm on): %v", code, exitUsage, runError)
	}
	assertContainsAll(t, stderr, []string{
		"write the Gateway API settings into the traefik values once the CRDs are established",
		"decide on the cert-manager values once the CRDs are established (the platform did not report what they need, " +
			"so this plan cannot say whether they are written)",
	})
	if strings.Contains(stderr, "write the Gateway API settings into the cert-manager values") {
		t.Fatalf("an unreported values state was shown as a planned write:\n%s", stderr)
	}
}

func TestClusterGatewayAPIEnableIsANoOpWhenAlreadyEnabled(t *testing.T) {
	recorder := newGatewayAPIServer(t, "hetzner", gatewayAPIRecorded(client.GatewayAPIPhaseEnabled, "The Gateway API is on."))

	stdout, _, runError := runGatewayAPI(t, "", "enable", "--yes")
	if runError != nil {
		t.Fatalf("enable on an enabled cluster failed: %v", runError)
	}
	if methods := recorder.methods(); len(methods) != 1 || methods[0] != http.MethodGet {
		t.Fatalf("an enabled cluster was written to: %v", methods)
	}
	assertContainsAll(t, stdout, []string{
		"The Gateway API is already on for cluster 'test-cluster' (hetzner); nothing to do.",
		"State:        enabled",
	})
}

func TestClusterGatewayAPIRefusesAClusterWithoutAnkraNetworking(t *testing.T) {
	unavailable := client.GatewayAPIStatus{Available: false,
		UnavailableReason: "This cluster's ingress was not installed by Ankra (include_networking was off), so there is no Ankra-managed Traefik to serve the Gateway API"}
	recorder := newGatewayAPIServer(t, "digitalocean", unavailable)

	stdout, _, statusError := runGatewayAPI(t, "", "status")
	if statusError != nil {
		t.Fatalf("status failed: %v", statusError)
	}
	if !strings.Contains(stdout, "Not available: This cluster's ingress was not installed by Ankra") {
		t.Fatalf("status must say why the option is not available, got:\n%s", stdout)
	}

	_, _, enableError := runGatewayAPI(t, "", "enable", "--yes")
	if code := exitCodeFor(enableError); code != exitBlocked {
		t.Fatalf("exit code = %d, want %d (blocked, nothing changed): %v", code, exitBlocked, enableError)
	}
	for _, request := range recorder.seen() {
		if request.method == http.MethodPut {
			t.Fatalf("an unavailable cluster was written to: %+v", request)
		}
	}
}

func TestClusterGatewayAPIRefusalsKeepTheirExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		status   int
		body     string
		args     []string
		wantCode int
		wantText string
	}{
		{name: "a write without clusters.write exits 7", method: http.MethodPut, status: http.StatusForbidden,
			body: `{"detail":"Forbidden"}`, args: []string{"enable", "--yes"}, wantCode: exitForbidden,
			wantText: "permission to change cluster settings (clusters.write)"},
		{name: "a write the platform refuses exits 8 with its sentence", method: http.MethodPut, status: http.StatusConflict,
			body: `{"detail":"The cluster's cloud provider stack is still being created or removed; turn the Gateway API on once it is up"}`,
			args: []string{"enable", "--yes"}, wantCode: exitBlocked,
			wantText: "nothing was changed): The cluster's cloud provider stack is still being created or removed"},
		{name: "a cluster outside the organisation exits 3", method: http.MethodGet, status: http.StatusNotFound,
			body: `{"detail":"Cluster not found"}`, args: []string{"status"}, wantCode: exitNotFound,
			wantText: "not found in this organisation"},
		{name: "a platform without the route exits 1 and says so", method: http.MethodGet, status: http.StatusNotFound,
			body: `{"detail":"Not Found"}`, args: []string{"status"}, wantCode: exitError,
			wantText: "does not offer turning the Gateway API on after create yet"},
		{name: "a 404 without a detail is reported as unknown", method: http.MethodGet, status: http.StatusNotFound,
			body: `not json`, args: []string{"status"}, wantCode: exitError,
			wantText: "answered 404 without saying why"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newGatewayAPIServer(t, "scaleway", gatewayAPIOff())
			recorder.scriptPuts(gatewayAPIEnableAnswer(gatewayAPIRecorded(client.GatewayAPIPhaseCRDsPending, "waiting"), true, false))
			recorder.refuse(testCase.method, testCase.status, testCase.body)

			_, _, runError := runGatewayAPI(t, "", testCase.args...)
			if runError == nil {
				t.Fatal("expected a refusal, got success")
			}
			if code := exitCodeFor(runError); code != testCase.wantCode {
				t.Fatalf("exit code = %d, want %d: %v", code, testCase.wantCode, runError)
			}
			if !strings.Contains(runError.Error(), testCase.wantText) {
				t.Fatalf("error = %q, want it to contain %q", runError.Error(), testCase.wantText)
			}
		})
	}
}

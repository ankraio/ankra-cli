package cmd

// `ankra cluster hetzner control-plane set-instance-type` against a scripted
// platform for the single-controller live resize, which takes the Kubernetes
// API down: the real client talks to an httptest server, so whether a PUT was
// sent at all, and what its body carried, are pinned on the requests
// themselves (ankra-ztumbc).

import (
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

const apiDowntimeReason = "This cluster has one control plane, so resizing it takes the Kubernetes API down for a few minutes while the server reboots on the new type."

type apiDowntimeRequest struct {
	method string
	path   string
	body   map[string]any
}

// apiDowntimeServer answers GET .../control-plane with info and PUT
// .../control-plane/instance-type like the platform: a single-controller
// running cluster's resize is refused with a 409 unless the body accepts the
// API downtime.
type apiDowntimeServer struct {
	mutex       sync.Mutex
	info        client.ControlPlaneInfo
	getStatus   int
	requests    []apiDowntimeRequest
	operationID string
}

func newAPIDowntimeServer(t *testing.T, info client.ControlPlaneInfo) (*apiDowntimeServer, *client.Client) {
	t.Helper()
	recorder := &apiDowntimeServer{info: info, operationID: "op-resize-1"}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		recorded := apiDowntimeRequest{method: request.Method, path: request.URL.Path}
		if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &recorded.body); decodeError != nil {
				t.Errorf("the request body is not a JSON object: %v: %s", decodeError, raw)
			}
		}
		recorder.mutex.Lock()
		defer recorder.mutex.Unlock()
		recorder.requests = append(recorder.requests, recorded)
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/control-plane"):
			if recorder.getStatus != 0 {
				writer.WriteHeader(recorder.getStatus)
				_, _ = writer.Write([]byte(`{"detail":"temporarily unavailable"}`))
				return
			}
			_ = json.NewEncoder(writer).Encode(recorder.info)
		case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/control-plane/instance-type"):
			if accepted, _ := recorded.body["accept_api_downtime"].(bool); !accepted {
				writer.WriteHeader(http.StatusConflict)
				_, _ = writer.Write([]byte(`{"detail":"Resizing the only control plane takes the Kubernetes API down; re-send with accept_api_downtime to proceed."}`))
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"previous_instance_type": "cx22",
				"new_instance_type":      recorded.body["instance_type"],
				"updated":                1,
				"mode":                   client.ControlPlaneChangeModeLiveWithAPIDowntime,
				"operation_id":           recorder.operationID,
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"not found"}`))
		}
	}))
	t.Cleanup(server.Close)
	return recorder, client.New("test-token", server.URL)
}

func (recorder *apiDowntimeServer) puts() []apiDowntimeRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	var puts []apiDowntimeRequest
	for _, request := range recorder.requests {
		if request.method == http.MethodPut {
			puts = append(puts, request)
		}
	}
	return puts
}

func (recorder *apiDowntimeServer) gets() int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	count := 0
	for _, request := range recorder.requests {
		if request.method == http.MethodGet {
			count++
		}
	}
	return count
}

func singleControllerLiveInfo() client.ControlPlaneInfo {
	reason := apiDowntimeReason
	return client.ControlPlaneInfo{
		Count:           1,
		SupportedCounts: []int{1, 3},
		InstanceType:    "cx22",
		CanChange:       true,
		InstanceTypeChange: &client.ControlPlaneChangeCapability{
			Allowed: true,
			Mode:    client.ControlPlaneChangeModeLiveWithAPIDowntime,
			Reason:  &reason,
		},
	}
}

// runHetznerSetInstanceType runs the real command tree against platformClient and
// returns stdout, the combined cobra output and the error.
func runHetznerSetInstanceType(t *testing.T, platformClient APIClient, extraArguments ...string) (string, string, error) {
	t.Helper()
	command := findSetInstanceTypeCommand(t, "hetzner")
	arguments := append([]string{"cluster", "hetzner", "control-plane", "set-instance-type", testClusterID, "cx32"}, extraArguments...)
	var combined string
	var runError error
	stdoutOutput := captureStdout(t, func() {
		combined, runError = runConfirmCommand(t, platformClient, "", []*cobra.Command{command}, arguments...)
	})
	return stdoutOutput, combined, runError
}

// Without --accept-api-downtime, a resize the platform says would take the API
// down is refused here, before anything is sent, with an explanation an
// operator can decide on - not just the name of a flag to add.
func TestSetInstanceTypeRefusesTheAPIDowntimeLaneLocallyWithoutTheFlag(t *testing.T) {
	recorder, realClient := newAPIDowntimeServer(t, singleControllerLiveInfo())

	_, _, runError := runHetznerSetInstanceType(t, realClient)

	if runError == nil {
		t.Fatal("expected the unaccepted downtime resize to be refused")
	}
	if code := exitCodeFor(runError); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if puts := recorder.puts(); len(puts) != 0 {
		t.Errorf("no resize may be sent without --accept-api-downtime, got %d PUT(s): %+v", len(puts), puts)
	}
	if recorder.gets() != 1 {
		t.Errorf("expected one control-plane read to decide, got %d", recorder.gets())
	}
	message := runError.Error()
	for _, want := range []string{
		"one control plane",
		"Kubernetes API down for a few minutes",
		"Workloads on the workers keep running",
		"kubectl, deploys, the Ankra agent and controllers pause",
		"--accept-api-downtime",
		"scale to 3 controllers",
		"ankra cluster hetzner control-plane set-count " + testClusterID + " 3",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal does not say %q: %s", want, message)
		}
	}
}

// With the flag the body carries accept_api_downtime: true, no read is needed
// first, and the outcome says the API is about to be unavailable and names the
// operation to follow.
func TestSetInstanceTypeSendsAcceptAPIDowntimeWhenGiven(t *testing.T) {
	recorder, realClient := newAPIDowntimeServer(t, singleControllerLiveInfo())

	stdoutOutput, _, runError := runHetznerSetInstanceType(t, realClient, "--accept-api-downtime")

	if runError != nil {
		t.Fatalf("execute failed: %v", runError)
	}
	puts := recorder.puts()
	if len(puts) != 1 {
		t.Fatalf("PUTs = %d, want 1", len(puts))
	}
	if puts[0].path != "/api/v1/clusters/hetzner/"+testClusterID+"/control-plane/instance-type" {
		t.Errorf("path = %s", puts[0].path)
	}
	if accepted, isBool := puts[0].body["accept_api_downtime"].(bool); !isBool || !accepted {
		t.Errorf("body accept_api_downtime = %v, want JSON true; body: %v", puts[0].body["accept_api_downtime"], puts[0].body)
	}
	if _, hasForceDrain := puts[0].body["force_drain"]; hasForceDrain {
		t.Errorf("force_drain was not asked for, body: %v", puts[0].body)
	}
	if recorder.gets() != 0 {
		t.Errorf("an accepted resize needs no read first, got %d GET(s)", recorder.gets())
	}
	for _, want := range []string{
		"changed from 'cx22' to 'cx32'",
		"Kubernetes API will be unavailable for a few minutes",
		"Operation: op-resize-1",
	} {
		if !strings.Contains(stdoutOutput, want) {
			t.Errorf("expected %q, got: %s", want, stdoutOutput)
		}
	}
	if strings.Contains(stdoutOutput, "Start the cluster") {
		t.Errorf("a running cluster must not be told to start, got: %s", stdoutOutput)
	}
}

// When the read fails the command does not guess a lane: the request goes out
// as given, and the platform's 409 is the explanation the operator sees.
func TestSetInstanceTypeSendsAsGivenWhenTheReadFails(t *testing.T) {
	recorder, realClient := newAPIDowntimeServer(t, singleControllerLiveInfo())
	recorder.getStatus = http.StatusServiceUnavailable

	_, _, runError := runHetznerSetInstanceType(t, realClient)

	if runError == nil {
		t.Fatal("expected the platform's 409 to come back as an error")
	}
	puts := recorder.puts()
	if len(puts) != 1 {
		t.Fatalf("PUTs = %d, want 1: a failed read must not block the request", len(puts))
	}
	if _, hasAccept := puts[0].body["accept_api_downtime"]; hasAccept {
		t.Errorf("the flag was not given, so nothing may be accepted on the operator's behalf, body: %v", puts[0].body)
	}
	if !strings.Contains(runError.Error(), "accept_api_downtime") {
		t.Errorf("expected the platform's refusal text, got: %v", runError)
	}
}

// A resize on another lane (here the rolling one of a three-controller
// cluster) is not held back by the downtime check, and sends no acceptance.
func TestSetInstanceTypeOtherLanesAreNotRefusedLocally(t *testing.T) {
	info := singleControllerLiveInfo()
	info.Count = 3
	info.InstanceTypeChange = &client.ControlPlaneChangeCapability{Allowed: true, Mode: client.ControlPlaneChangeModeRolling}
	var sent []client.ControlPlaneInstanceTypeOptions
	ops := func() controlPlaneOps {
		return controlPlaneOps{
			provider: "hetzner",
			get:      func(string) (*client.ControlPlaneInfo, error) { return &info, nil },
			setInstanceType: func(_ string, instanceType string, options client.ControlPlaneInstanceTypeOptions) (*client.ChangeControlPlaneInstanceTypeResult, error) {
				sent = append(sent, options)
				return &client.ChangeControlPlaneInstanceTypeResult{
					PreviousInstanceType: "cx22", NewInstanceType: instanceType, Updated: 3, Mode: client.ControlPlaneChangeModeRolling,
				}, nil
			},
		}
	}
	command := &cobra.Command{}
	command.Flags().Bool(acceptAPIDowntimeFlag, false, "")
	captureStdout(t, func() {
		if runError := runControlPlaneSetInstanceType(command, ops, testClusterID, "cx32"); runError != nil {
			t.Fatalf("runControlPlaneSetInstanceType returned an error: %v", runError)
		}
	})
	if len(sent) != 1 || sent[0].AcceptAPIDowntime {
		t.Errorf("expected one request without accept_api_downtime, got %+v", sent)
	}
}

// `control-plane get` names the downtime lane and shows the platform's reason
// under it, so "yes" is never read as "yes, without consequence".
func TestControlPlaneGetRendersTheAPIDowntimeLaneAndItsReason(t *testing.T) {
	info := singleControllerLiveInfo()
	stdoutOutput := captureStdout(t, func() {
		if err := runControlPlaneGet(&cobra.Command{}, stubControlPlaneOps(&info, nil), testClusterID); err != nil {
			t.Fatalf("runControlPlaneGet returned an error: %v", err)
		}
	})
	typeLine := lineContaining(t, stdoutOutput, "Change type:")
	if !strings.Contains(typeLine, "yes - live with a few minutes of API downtime (single controller)") {
		t.Errorf("expected the downtime lane to be named, got: %s", typeLine)
	}
	reasonLine := lineContaining(t, stdoutOutput, apiDowntimeReason)
	if strings.Contains(reasonLine, "Change type:") {
		t.Errorf("the reason belongs under the answer, not on it, got: %s", stdoutOutput)
	}
	if !strings.HasPrefix(reasonLine, strings.Repeat(" ", 20)) {
		t.Errorf("the reason should be indented to the value column, got: %q", reasonLine)
	}
}

// The flag exists on every provider's set-instance-type (the server decides
// which lanes it offers), is off by default and is explained in the help.
func TestAcceptAPIDowntimeFlagIsOffByDefaultOnEveryProvider(t *testing.T) {
	for _, provider := range controlPlaneProviderCommands {
		command := findSetInstanceTypeCommand(t, provider)
		flag := command.Flags().Lookup(acceptAPIDowntimeFlag)
		if flag == nil {
			t.Errorf("%s has no --%s flag", command.CommandPath(), acceptAPIDowntimeFlag)
			continue
		}
		if flag.DefValue != "false" {
			t.Errorf("%s --%s default = %s, want false", command.CommandPath(), acceptAPIDowntimeFlag, flag.DefValue)
		}
		if !strings.Contains(command.Long, "--accept-api-downtime") {
			t.Errorf("%s long help does not explain --accept-api-downtime", command.CommandPath())
		}
	}
}

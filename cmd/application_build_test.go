package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

// applicationBuildMock plays the build lane: one scripted answer per read, so
// a test can walk a request from pending through a claim to a conclusion the
// way the platform would.
type applicationBuildMock struct {
	baseMock

	startPayload  json.RawMessage
	startError    error
	startRequest  client.StartApplicationPlatformBuildRequest
	applicationID string
	startCalls    int

	// requestPayloads and buildPayloads are consumed one per poll; the last
	// entry repeats once exhausted so a slow loop cannot run off the end.
	requestPayloads [][]byte
	buildPayloads   [][]byte
	requestCalls    int
	buildCalls      int

	listPayload json.RawMessage
	getPayload  json.RawMessage
	getBuildID  string
}

func (mock *applicationBuildMock) StartApplicationPlatformBuild(requestContext context.Context,
	applicationID string, request client.StartApplicationPlatformBuildRequest) (json.RawMessage, error) {
	mock.startCalls++
	mock.applicationID = applicationID
	mock.startRequest = request
	return mock.startPayload, mock.startError
}

func (mock *applicationBuildMock) ListApplicationPlatformBuilds(requestContext context.Context,
	applicationID string) (json.RawMessage, error) {
	mock.applicationID = applicationID
	return mock.listPayload, nil
}

func (mock *applicationBuildMock) GetApplicationPlatformBuild(requestContext context.Context,
	applicationID string, buildID string) (json.RawMessage, error) {
	mock.applicationID = applicationID
	mock.getBuildID = buildID
	mock.buildCalls++
	if len(mock.buildPayloads) == 0 {
		return mock.getPayload, nil
	}
	return json.RawMessage(scriptedPayload(mock.buildPayloads, mock.buildCalls)), nil
}

func (mock *applicationBuildMock) GetApplicationPlatformBuildRequest(requestContext context.Context,
	applicationID string, buildRequestID string) (json.RawMessage, error) {
	mock.applicationID = applicationID
	mock.requestCalls++
	return json.RawMessage(scriptedPayload(mock.requestPayloads, mock.requestCalls)), nil
}

// scriptedPayload returns the nth scripted answer, repeating the last.
func scriptedPayload(payloads [][]byte, call int) []byte {
	if call > len(payloads) {
		return payloads[len(payloads)-1]
	}
	return payloads[call-1]
}

// fastBuildPolling shrinks the --wait poll interval so the loop tests run in
// milliseconds instead of the production cadence.
func fastBuildPolling(t *testing.T) {
	t.Helper()
	previous := buildPollInterval
	buildPollInterval = time.Millisecond
	t.Cleanup(func() { buildPollInterval = previous })
}

func TestApplicationBuildCommandsRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, subcommand := range newApplicationCommand().Commands() {
		if subcommand.Name() != "build" {
			continue
		}
		for _, child := range subcommand.Commands() {
			registered[child.Name()] = true
		}
	}
	for _, expected := range []string{"start", "list", "get", "request"} {
		if !registered[expected] {
			t.Errorf("build subcommand %q is not registered", expected)
		}
	}
}

// The commit is what gives a request its identity in the queue, so a bare
// 'start' is a usage error rather than a build of something unstated.
func TestApplicationBuildStartRequiresACommit(t *testing.T) {
	mockClient := &applicationBuildMock{}
	_, executeError := runApplicationCommand(t, mockClient, "build", "start", testApplicationID)
	if executeError == nil {
		t.Fatal("start without --commit must fail")
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d, want %d (usage)", exitCodeFor(executeError), exitUsage)
	}
	if mockClient.startCalls != 0 {
		t.Errorf("the platform must not be called without a commit; calls = %d", mockClient.startCalls)
	}
}

func TestApplicationBuildStartSendsWhatWasAsked(t *testing.T) {
	mockClient := &applicationBuildMock{
		startPayload: json.RawMessage(`{"request_id":"req-1","build_id":null,"already_requested":false}`),
	}
	const commit = "9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c"
	output, executeError := runApplicationCommand(t, mockClient, "build", "start", testApplicationID,
		"--commit", commit, "--ref", "main", "--component", "api", "--reason", "push")
	if executeError != nil {
		t.Fatalf("build start error = %v", executeError)
	}
	if mockClient.applicationID != testApplicationID {
		t.Errorf("application id = %q", mockClient.applicationID)
	}
	expected := client.StartApplicationPlatformBuildRequest{
		HeadSHA: commit, Ref: "main", Component: "api", Reason: "push",
	}
	if mockClient.startRequest != expected {
		t.Errorf("request = %+v, want %+v", mockClient.startRequest, expected)
	}
	if !strings.Contains(output, "req-1") {
		t.Errorf("the queued request must be reported: %s", output)
	}
}

// Without --wait the command reports where the request landed and stops; it
// must not start polling.
func TestApplicationBuildStartWithoutWaitDoesNotPoll(t *testing.T) {
	mockClient := &applicationBuildMock{
		startPayload: json.RawMessage(`{"request_id":"req-1","build_id":null,"already_requested":true}`),
	}
	if _, executeError := runApplicationCommand(t, mockClient, "build", "start", testApplicationID,
		"--commit", "9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c"); executeError != nil {
		t.Fatalf("build start error = %v", executeError)
	}
	if mockClient.requestCalls != 0 || mockClient.buildCalls != 0 {
		t.Errorf("no polling without --wait; request=%d build=%d", mockClient.requestCalls, mockClient.buildCalls)
	}
}

// The whole point of --wait: cross the gap between a queued request and the
// build it becomes, then follow that build to a result.
func TestApplicationBuildStartWaitFollowsTheRequestToASucceededBuild(t *testing.T) {
	fastBuildPolling(t)
	mockClient := &applicationBuildMock{
		startPayload: json.RawMessage(`{"request_id":"req-1","build_id":null,"already_requested":false}`),
		requestPayloads: [][]byte{
			[]byte(`{"status":"pending","build_id":null}`),
			[]byte(`{"status":"claimed","build_id":"build-9"}`),
		},
		buildPayloads: [][]byte{
			[]byte(`{"id":"build-9","status":"running","recipe":"dockerfile"}`),
			[]byte(`{"id":"build-9","status":"succeeded","recipe":"dockerfile","image_ref":"registry/app:sha-9f4a1c2"}`),
		},
	}
	output, executeError := runApplicationCommand(t, mockClient, "build", "start", testApplicationID,
		"--commit", "9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c", "--wait")
	if executeError != nil {
		t.Fatalf("build start --wait error = %v", executeError)
	}
	if mockClient.getBuildID != "build-9" {
		t.Errorf("followed build = %q, want build-9", mockClient.getBuildID)
	}
	if mockClient.requestCalls < 2 {
		t.Errorf("the request must be polled across the pending gap; calls = %d", mockClient.requestCalls)
	}
	if !strings.Contains(output, "registry/app:sha-9f4a1c2") {
		t.Errorf("the finished build must be rendered: %s", output)
	}
}

// A failed build fails the command: this flag exists for a pipeline step, and
// a step that reports a failed build and then exits 0 is a broken gate.
func TestApplicationBuildStartWaitFailsWhenTheBuildFailed(t *testing.T) {
	fastBuildPolling(t)
	mockClient := &applicationBuildMock{
		startPayload:    json.RawMessage(`{"request_id":"req-1","build_id":"build-9","already_requested":false}`),
		requestPayloads: [][]byte{[]byte(`{"status":"claimed","build_id":"build-9"}`)},
		buildPayloads: [][]byte{
			[]byte(`{"id":"build-9","status":"failed","error_class":"build_failed","error_message":"npm ci exited 1"}`),
		},
	}
	_, executeError := runApplicationCommand(t, mockClient, "build", "start", testApplicationID,
		"--commit", "9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c", "--wait")
	if executeError == nil {
		t.Fatal("a failed build must fail the command")
	}
	if exitCodeFor(executeError) != exitError {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitError)
	}
	if !strings.Contains(executeError.Error(), "npm ci exited 1") {
		t.Errorf("the build's own error must reach the caller: %v", executeError)
	}
	// A build id already on the start answer is followed directly - there is
	// no pending gap left to poll across.
	if mockClient.requestCalls != 0 {
		t.Errorf("an already-claimed request needs no request poll; calls = %d", mockClient.requestCalls)
	}
}

// Every error class names whose failure it was. build_unknown is a build that
// ran and failed in a way the builder could not classify: it must read neither
// as the repository's broken recipe nor as a platform that could not run the
// build, and it must carry the builder's output, which is the only copy.
func TestDescribeFailedBuildAttributesEachErrorClass(t *testing.T) {
	testCases := []struct {
		name         string
		errorClass   string
		errorMessage string
		wantPresent  []string
		wantAbsent   []string
	}{
		{
			name:         "build_failed is the repository's",
			errorClass:   "build_failed",
			errorMessage: "npm ci exited 1",
			wantPresent:  []string{"the build failed: npm ci exited 1"},
		},
		{
			name:        "clone_auth is the application's credential",
			errorClass:  "clone_auth",
			wantPresent: []string{"could not clone the repository", "repository credential"},
		},
		{
			name:        "recipe_missing is the application's recipe",
			errorClass:  "recipe_missing",
			wantPresent: []string{"no recipe could be resolved"},
		},
		{
			name:         "push_failed is Ankra's",
			errorClass:   "push_failed",
			errorMessage: "401 Unauthorized",
			wantPresent:  []string{"did not finish (push_failed)", "401 Unauthorized"},
		},
		{
			name:        "timeout is Ankra's",
			errorClass:  "timeout",
			wantPresent: []string{"did not finish (timeout)", "platform-side failure"},
		},
		{
			name:        "capacity is Ankra's",
			errorClass:  "capacity",
			wantPresent: []string{"did not finish (capacity)", "platform-side failure"},
		},
		{
			name:       "build_unknown ran and keeps the builder's output",
			errorClass: "build_unknown",
			errorMessage: "The build ran and failed in a way the builder could not classify; " +
				"the end of its output follows: #14 ERROR: signal: killed",
			wantPresent: []string{"ran and failed unclassified (build_unknown)", "include it when reporting",
				"#14 ERROR: signal: killed"},
			wantAbsent: []string{"did not finish", "platform-side failure", "the build failed:"},
		},
		{
			name:        "build_unknown without output says none was recorded",
			errorClass:  "build_unknown",
			wantPresent: []string{"ran and failed unclassified (build_unknown)", "recorded none of its output"},
			wantAbsent:  []string{"did not finish", "no reason was recorded"},
		},
		{
			name:        "an unrecognised class keeps the generic failure",
			errorClass:  "a_class_this_cli_predates",
			wantPresent: []string{"the build failed: no reason was recorded"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			errorClass := testCase.errorClass
			build := followedBuild{ID: "build-9", Status: "failed", ErrorClass: &errorClass}
			if testCase.errorMessage != "" {
				errorMessage := testCase.errorMessage
				build.ErrorMessage = &errorMessage
			}
			described := describeFailedBuild(build).Error()
			for _, want := range testCase.wantPresent {
				if !strings.Contains(described, want) {
					t.Errorf("%q must contain %q", described, want)
				}
			}
			for _, unwanted := range testCase.wantAbsent {
				if strings.Contains(described, unwanted) {
					t.Errorf("%q must not contain %q", described, unwanted)
				}
			}
		})
	}
}

// A request withdrawn before any builder took it is a failure of the ask, and
// must not leave --wait polling forever.
func TestApplicationBuildStartWaitStopsOnACancelledRequest(t *testing.T) {
	fastBuildPolling(t)
	mockClient := &applicationBuildMock{
		startPayload:    json.RawMessage(`{"request_id":"req-1","build_id":null,"already_requested":false}`),
		requestPayloads: [][]byte{[]byte(`{"status":"cancelled","build_id":null}`)},
	}
	_, executeError := runApplicationCommand(t, mockClient, "build", "start", testApplicationID,
		"--commit", "9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c", "--wait")
	if executeError == nil {
		t.Fatal("a cancelled request must fail the command")
	}
	if !strings.Contains(executeError.Error(), "cancelled") {
		t.Errorf("the cancellation must be named: %v", executeError)
	}
	if mockClient.buildCalls != 0 {
		t.Errorf("a cancelled request has no build to follow; calls = %d", mockClient.buildCalls)
	}
}

// --timeout is the caller's budget, not the build's: the build keeps running,
// and the exit code says which of the two ran out.
func TestApplicationBuildStartWaitExpiresWithTheWaitTimeoutCode(t *testing.T) {
	fastBuildPolling(t)
	mockClient := &applicationBuildMock{
		startPayload:    json.RawMessage(`{"request_id":"req-1","build_id":null,"already_requested":false}`),
		requestPayloads: [][]byte{[]byte(`{"status":"pending","build_id":null}`)},
	}
	_, executeError := runApplicationCommand(t, mockClient, "build", "start", testApplicationID,
		"--commit", "9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c", "--wait", "--timeout", "20ms")
	if executeError == nil {
		t.Fatal("an expired --timeout must fail the command")
	}
	if exitCodeFor(executeError) != exitWaitTimeout {
		t.Errorf("exit code = %d, want %d (wait timeout)", exitCodeFor(executeError), exitWaitTimeout)
	}
}

func TestApplicationBuildStartSurfacesTheStartFailure(t *testing.T) {
	mockClient := &applicationBuildMock{startError: errors.New("platform said no")}
	_, executeError := runApplicationCommand(t, mockClient, "build", "start", testApplicationID,
		"--commit", "9f4a1c2e8b7d6053f1a2b3c4d5e6f708192a3b4c")
	if executeError == nil || !strings.Contains(executeError.Error(), "platform said no") {
		t.Fatalf("the start failure must reach the caller: %v", executeError)
	}
}

func TestApplicationBuildListAndGetRenderTheirPayloads(t *testing.T) {
	mockClient := &applicationBuildMock{
		listPayload: json.RawMessage(`{"builds":[{"id":"build-9","status":"succeeded"}],"capped":false}`),
		getPayload:  json.RawMessage(`{"id":"build-9","status":"succeeded","recipe":"generated"}`),
	}
	listOutput, listError := runApplicationCommand(t, mockClient, "build", "list", testApplicationID)
	if listError != nil {
		t.Fatalf("build list error = %v", listError)
	}
	if !strings.Contains(listOutput, "build-9") {
		t.Errorf("listing must render: %s", listOutput)
	}
	getOutput, getError := runApplicationCommand(t, mockClient, "build", "get", testApplicationID, "build-9")
	if getError != nil {
		t.Fatalf("build get error = %v", getError)
	}
	if mockClient.getBuildID != "build-9" || !strings.Contains(getOutput, "generated") {
		t.Errorf("build read = %q, output = %s", mockClient.getBuildID, getOutput)
	}
}

func TestApplicationBuildRequestRendersTheQueuedRequest(t *testing.T) {
	mockClient := &applicationBuildMock{
		requestPayloads: [][]byte{[]byte(`{"request_id":"req-1","status":"pending","build_id":null}`)},
	}
	output, executeError := runApplicationCommand(t, mockClient, "build", "request", testApplicationID, "req-1")
	if executeError != nil {
		t.Fatalf("build request error = %v", executeError)
	}
	if !strings.Contains(output, "pending") {
		t.Errorf("the request state must render: %s", output)
	}
}

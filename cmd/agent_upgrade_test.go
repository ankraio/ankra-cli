package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type agentUpgradeMock struct {
	baseMock
	requests     []client.AgentUpgradeRequest
	upgradeError error
}

func (mock *agentUpgradeMock) UpgradeClusterAgent(_ context.Context, _ string,
	request client.AgentUpgradeRequest) (*client.UpgradeAgentResult, error) {
	mock.requests = append(mock.requests, request)
	if mock.upgradeError != nil {
		return nil, mock.upgradeError
	}
	return &client.UpgradeAgentResult{Success: true, Message: "Agent upgrade initiated"}, nil
}

// runAgentUpgradeCommand runs `ankra cluster agent upgrade` and puts the
// command's flags back afterwards: cobra keeps flag values on the package
// level command between executions.
func runAgentUpgradeCommand(t *testing.T, mock APIClient, arguments ...string) (string, error) {
	t.Helper()
	writeSelectedClusterJSON(t)
	setMockClient(t, mock)
	t.Cleanup(func() {
		_ = clusterAgentUpgradeCmd.Flags().Set("version", "")
		_ = clusterAgentUpgradeCmd.Flags().Set("allow-downgrade", "false")
	})
	return executeCommand(append([]string{"cluster", "agent", "upgrade"}, arguments...)...)
}

// Without flags the CLI sends the plain upgrade, which the platform resolves
// to the newer of the running and latest stable release.
func TestClusterAgentUpgradeWithoutFlagsSendsThePlainUpgrade(t *testing.T) {
	mock := &agentUpgradeMock{}
	output, runError := runAgentUpgradeCommand(t, mock)
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if len(mock.requests) != 1 || !mock.requests[0].IsPlain() {
		t.Fatalf("expected one plain upgrade, got %+v", mock.requests)
	}
	if !strings.Contains(output, "Agent upgrade initiated for cluster 'test-cluster'") {
		t.Errorf("output missing the confirmation:\n%s", output)
	}
}

func TestClusterAgentUpgradeSendsTheNamedVersionAndTheDowngradeSwitch(t *testing.T) {
	mock := &agentUpgradeMock{}
	output, runError := runAgentUpgradeCommand(t, mock, "--version", " 2.1.1247 ", "--allow-downgrade")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	want := client.AgentUpgradeRequest{Version: "2.1.1247", AllowDowngrade: true}
	if len(mock.requests) != 1 || mock.requests[0] != want {
		t.Fatalf("requests = %+v, want %+v", mock.requests, want)
	}
	if !strings.Contains(output, "Agent update to 2.1.1247 initiated for cluster 'test-cluster'") {
		t.Errorf("output missing the named version:\n%s", output)
	}
}

// --allow-downgrade alone names nothing to downgrade to: it is a usage error
// caught before any request, not a request the platform has to refuse.
func TestClusterAgentUpgradeRefusesAllowDowngradeWithoutVersion(t *testing.T) {
	mock := &agentUpgradeMock{}
	_, runError := runAgentUpgradeCommand(t, mock, "--allow-downgrade")
	if runError == nil || !strings.Contains(runError.Error(), "--allow-downgrade needs --version") {
		t.Fatalf("expected the usage refusal, got %v", runError)
	}
	if exitCodeFor(runError) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(runError), exitUsage)
	}
	if len(mock.requests) != 0 {
		t.Fatalf("a usage error still called the platform: %+v", mock.requests)
	}
}

// The platform's refusals reach the user as its own sentence and exit as a
// usage error; the downgrade refusal also names the flag that allows it.
func TestClusterAgentUpgradeRelaysThePlatformRefusals(t *testing.T) {
	for name, testCase := range map[string]struct {
		refusal  *client.AgentUpgradeRefusedError
		wantHint bool
	}{
		"older release": {&client.AgentUpgradeRefusedError{StatusCode: 409,
			Detail: "The agent runs 2.1.1250, which is newer than 2.1.1247. Pass allow_downgrade to downgrade it on purpose."}, true},
		"unknown release": {&client.AgentUpgradeRefusedError{StatusCode: 422,
			Detail: "Agent version 9.9.9 is not a published stable release or the version this agent runs."}, false},
	} {
		mock := &agentUpgradeMock{upgradeError: testCase.refusal}
		output, runError := runAgentUpgradeCommand(t, mock, "--version", "2.1.1247")
		if runError == nil || !strings.Contains(runError.Error(), "upgrading agent: "+testCase.refusal.Detail) {
			t.Fatalf("%s: expected the platform's sentence, got %v", name, runError)
		}
		if hasHint := strings.Contains(runError.Error(), "--allow-downgrade"); hasHint != testCase.wantHint {
			t.Errorf("%s: --allow-downgrade hint present = %v, want %v", name, hasHint, testCase.wantHint)
		}
		if exitCodeFor(runError) != exitUsage {
			t.Errorf("%s: exit code = %d, want %d", name, exitCodeFor(runError), exitUsage)
		}
		if strings.Contains(output, "initiated") {
			t.Errorf("%s: a refused upgrade printed success:\n%s", name, output)
		}
	}
}

// Any other failure keeps the generic wrapping (and its exit code).
func TestClusterAgentUpgradeOtherFailuresStayGeneric(t *testing.T) {
	mock := &agentUpgradeMock{upgradeError: errors.New("upgrade failed: status 503")}
	_, runError := runAgentUpgradeCommand(t, mock)
	if runError == nil || runError.Error() != "upgrading agent: upgrade failed: status 503" {
		t.Fatalf("unexpected error %v", runError)
	}
	if exitCodeFor(runError) == exitUsage {
		t.Error("a platform failure must not exit as a usage error")
	}
}

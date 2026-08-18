package cmd

// Tests for `ankra cluster <provider> bastion`. `resize` follows the async
// accept/wait contract (submit-and-return by default, block with --wait),
// mirroring node-group instance-type upgrades. `status` reads the verdict the
// platform's bastion health loop recorded, and `diagnose` dispatches the
// read-only SSH job and blocks for its report - the endpoint has no
// submit-and-return half, so it carries --timeout without a --wait twin.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

type bastionResizeMock struct {
	baseMock

	resizeCalls []string
	result      *client.UpdateBastionInstanceTypeResult
	submitted   bool
	resizeError error
}

func (m *bastionResizeMock) UpdateHetznerBastionInstanceType(ctx context.Context, clusterID, instanceType string, wait bool) (*client.UpdateBastionInstanceTypeResult, bool, error) {
	m.resizeCalls = append(m.resizeCalls, fmt.Sprintf("%s:%s:%v", clusterID, instanceType, wait))
	if m.resizeError != nil {
		return nil, false, m.resizeError
	}
	return m.result, m.submitted, nil
}

func TestClusterBastionResizeCommandWithWait(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionResizeMock{
		result: &client.UpdateBastionInstanceTypeResult{NodeID: "node-9", Kind: "hetzner_bastion", Name: "bastion", InstanceType: "cx31"},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "bastion", "resize", "cluster-1", "cx31", "--wait")
	})

	if len(mock.resizeCalls) != 1 || mock.resizeCalls[0] != "cluster-1:cx31:true" {
		t.Fatalf("expected one resize call with wait=true, got %v", mock.resizeCalls)
	}
	if !strings.Contains(stdoutOutput, "resized to 'cx31'") {
		t.Errorf("expected resize confirmation, got: %s", stdoutOutput)
	}
}

func TestClusterBastionResizeCommandSubmittedWithoutWait(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionResizeMock{submitted: true}
	setMockClient(t, mock)

	// Pass --wait=false explicitly: the resize command is a package-level
	// singleton reused by every test in the binary, and pflag does not
	// reapply a bool flag's default between Execute() calls, so relying on
	// the default would leak the previous test's --wait=true.
	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "bastion", "resize", "cluster-1", "cx31", "--wait=false")
	})

	if len(mock.resizeCalls) != 1 || mock.resizeCalls[0] != "cluster-1:cx31:false" {
		t.Fatalf("expected one resize call with wait=false, got %v", mock.resizeCalls)
	}
	if !strings.Contains(stdoutOutput, "submitted") {
		t.Errorf("expected async-submitted messaging, got: %s", stdoutOutput)
	}
}

func TestClusterBastionResizeCommandSurfacesError(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionResizeMock{resizeError: fmt.Errorf("No bastion or gateway node found for this cluster")}
	setMockClient(t, mock)

	_, commandError := executeCommand("cluster", "hetzner", "bastion", "resize", "cluster-1", "cx31", "--wait")
	if commandError == nil || !strings.Contains(commandError.Error(), "No bastion or gateway node found") {
		t.Fatalf("expected the not-found error, got %v", commandError)
	}
}

type bastionHealthMock struct {
	baseMock

	healthCalls   []string
	health        *client.BastionHealthResult
	healthError   error
	diagnoseCalls []string
	diagnosis     *client.BastionDiagnoseResult
	diagnoseError error
}

func (m *bastionHealthMock) GetHetznerBastionHealth(clusterID string) (*client.BastionHealthResult, error) {
	m.healthCalls = append(m.healthCalls, clusterID)
	if m.healthError != nil {
		return nil, m.healthError
	}
	return m.health, nil
}

func (m *bastionHealthMock) DiagnoseHetznerBastion(ctx context.Context, clusterID string) (*client.BastionDiagnoseResult, error) {
	m.diagnoseCalls = append(m.diagnoseCalls, clusterID)
	if m.diagnoseError != nil {
		return nil, m.diagnoseError
	}
	return m.diagnosis, nil
}

func TestClusterBastionStatusCommandRendersTheVerdict(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionHealthMock{health: &client.BastionHealthResult{
		ResourceID:          "res-1",
		Kind:                "hetzner_bastion",
		Provider:            "hetzner",
		State:               "offline",
		Hop:                 "bastion",
		Detail:              "ssh dial timed out",
		ConsecutiveFailures: 3,
		VMStatus:            "running",
		CheckedAt:           "2026-08-17T10:00:00",
		DiagnoseSupported:   true,
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "bastion", "status", "cluster-1")
	})

	if len(mock.healthCalls) != 1 || mock.healthCalls[0] != "cluster-1" {
		t.Fatalf("expected one health read for cluster-1, got %v", mock.healthCalls)
	}
	for _, expected := range []string{
		"offline", "bastion", "ssh dial timed out", "3 probe(s) in a row", "running", "2026-08-17T10:00:00",
	} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected %q in the verdict, got: %s", expected, stdoutOutput)
		}
	}
	if !strings.Contains(stdoutOutput, "bastion diagnose cluster-1") {
		t.Errorf("a diagnosable bastion should point at the diagnose command, got: %s", stdoutOutput)
	}
}

// A provider with no bastion diagnose job lane still carries a verdict; the
// command must say the diagnosis is unavailable rather than offering a
// command that would only refuse.
func TestClusterBastionStatusCommandSaysDiagnosisIsUnavailable(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionHealthMock{health: &client.BastionHealthResult{
		ResourceID:        "res-1",
		Kind:              "scaleway_gateway",
		Provider:          "scaleway",
		State:             "healthy",
		DiagnoseSupported: false,
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "bastion", "status", "cluster-1")
	})

	if !strings.Contains(stdoutOutput, "Diagnosis is not available for this provider.") {
		t.Errorf("expected the unavailable-diagnosis line, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, "bastion diagnose") {
		t.Errorf("must not offer the diagnose command, got: %s", stdoutOutput)
	}
}

// The loop stamps nothing until its first pass, so an unprobed bastion has to
// say so instead of printing an empty state.
func TestClusterBastionStatusCommandReportsAnUnprobedBastion(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionHealthMock{health: &client.BastionHealthResult{
		ResourceID: "res-1", Kind: "hetzner_bastion", Provider: "hetzner", DiagnoseSupported: true,
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "bastion", "status", "cluster-1")
	})

	if !strings.Contains(stdoutOutput, "not probed yet") {
		t.Errorf("expected the unprobed wording, got: %s", stdoutOutput)
	}
}

func TestClusterBastionStatusCommandSurfacesTheNoBastionRefusal(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionHealthMock{healthError: fmt.Errorf("This cluster has no bastion resource.")}
	setMockClient(t, mock)

	_, commandError := executeCommand("cluster", "hetzner", "bastion", "status", "cluster-1")
	if commandError == nil || !strings.Contains(commandError.Error(), "no bastion resource") {
		t.Fatalf("expected the no-bastion refusal, got %v", commandError)
	}
}

func TestClusterBastionDiagnoseCommandRendersTheReport(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionHealthMock{diagnosis: &client.BastionDiagnoseResult{
		OperationID: "op-1",
		StepID:      "step-1",
		ResourceID:  "res-1",
		JobName:     "hetzner_bastion_diagnose",
		Status:      "completed",
		Completed:   true,
		Report:      map[string]any{"failed_units": []any{"chrony.service"}},
		Health:      map[string]any{"state": "healthy"},
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "bastion", "diagnose", "cluster-1")
	})

	if len(mock.diagnoseCalls) != 1 || mock.diagnoseCalls[0] != "cluster-1" {
		t.Fatalf("expected one diagnose call for cluster-1, got %v", mock.diagnoseCalls)
	}
	for _, expected := range []string{"hetzner_bastion_diagnose", "op-1", "Report:", "chrony.service"} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected %q in the diagnosis, got: %s", expected, stdoutOutput)
		}
	}
}

// The platform hands back the operation handle when its own wait budget
// elapses first; the command has to say where the report will show up rather
// than implying the diagnosis produced nothing.
func TestClusterBastionDiagnoseCommandPrintsThePollHintWhenStillRunning(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionHealthMock{diagnosis: &client.BastionDiagnoseResult{
		OperationID: "op-2",
		JobName:     "hetzner_bastion_diagnose",
		Status:      "running",
		Completed:   false,
		Health:      map[string]any{"state": "offline"},
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "hetzner", "bastion", "diagnose", "cluster-1")
	})

	if !strings.Contains(stdoutOutput, "still running") ||
		!strings.Contains(stdoutOutput, "ankra cluster operations list op-2") {
		t.Errorf("expected the poll hint, got: %s", stdoutOutput)
	}
}

// A client budget that expires leaves the job running on the platform, so the
// failure is a wait expiry (exit 5) and not a rejected request.
func TestClusterBastionDiagnoseCommandTagsTheWaitExpiry(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionHealthMock{diagnoseError: fmt.Errorf("request failed: %w", context.DeadlineExceeded)}
	setMockClient(t, mock)

	_, commandError := executeCommand("cluster", "hetzner", "bastion", "diagnose", "cluster-1")
	if commandError == nil {
		t.Fatal("expected an error, got nil")
	}
	if code := exitCodeFor(commandError); code != exitWaitTimeout {
		t.Errorf("exit code = %d, want %d", code, exitWaitTimeout)
	}
}

// -o json must stay parseable: the human verdict lines would otherwise land
// in the same stream a script is decoding.
func TestClusterBastionStatusCommandStructuredOutputIsClean(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &bastionHealthMock{health: &client.BastionHealthResult{
		ResourceID: "res-1", Kind: "hetzner_bastion", Provider: "hetzner",
		State: "healthy", DiagnoseSupported: true,
	}}
	setMockClient(t, mock)
	// -o is a persistent flag value on a package-level command singleton, so
	// leaving it set would silently turn every later status test into JSON.
	statusCmd, _, findError := rootCmd.Find([]string{"cluster", "hetzner", "bastion", "status"})
	if findError != nil {
		t.Fatalf("locating the status command: %v", findError)
	}
	t.Cleanup(func() { resetTreeFlags(t, statusCmd) })

	commandOutput, commandError := executeCommand("cluster", "hetzner", "bastion", "status", "cluster-1", "-o", "json")
	if commandError != nil {
		t.Fatalf("status -o json: %v", commandError)
	}

	var decoded client.BastionHealthResult
	if err := json.Unmarshal([]byte(commandOutput), &decoded); err != nil {
		t.Fatalf("output is not parseable JSON (%v): %s", err, commandOutput)
	}
	if decoded.State != "healthy" || !decoded.DiagnoseSupported {
		t.Errorf("decoded = %+v", decoded)
	}
}

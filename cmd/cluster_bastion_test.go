package cmd

// Tests for `ankra cluster <provider> bastion resize`: the async
// accept/wait contract (submit-and-return by default, block with --wait)
// mirrors node-group instance-type upgrades.

import (
	"context"
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

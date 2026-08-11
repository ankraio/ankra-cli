package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type playgroundMock struct {
	baseMock

	createResult    *client.CreatePlaygroundResult
	createError     error
	status          *client.PlaygroundStatus
	statusError     error
	statusRequested string
}

func (m *playgroundMock) CreatePlayground() (*client.CreatePlaygroundResult, error) {
	return m.createResult, m.createError
}

func (m *playgroundMock) GetPlaygroundStatus(clusterID string) (*client.PlaygroundStatus, error) {
	m.statusRequested = clusterID
	return m.status, m.statusError
}

func withPlaygroundMock(t *testing.T, mock *playgroundMock) {
	t.Helper()
	previous := apiClient
	apiClient = mock
	t.Cleanup(func() { apiClient = previous })
}

func TestPlaygroundCreatePrintsTheClusterIDAndTheFollowUpCommand(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{
		createResult: &client.CreatePlaygroundResult{ClusterID: "cluster-42", Success: true},
	})
	output := captureStdout(t, func() {
		if err := clusterPlaygroundCreateCmd.RunE(clusterPlaygroundCreateCmd, nil); err != nil {
			t.Fatalf("create failed: %v", err)
		}
	})
	if !strings.Contains(output, "cluster-42") {
		t.Errorf("expected the cluster id in the output, got: %s", output)
	}
	// Provisioning is asynchronous, so the command has to tell the user how
	// to follow it rather than implying the playground is ready.
	if !strings.Contains(output, "ankra cluster playground status cluster-42") {
		t.Errorf("expected the follow-up command in the output, got: %s", output)
	}
}

func TestPlaygroundCreateSurfacesTheServerError(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{createError: errors.New("A playground already exists for this organisation.")})
	runError := clusterPlaygroundCreateCmd.RunE(clusterPlaygroundCreateCmd, nil)
	if runError == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(runError.Error(), "already exists") {
		t.Errorf("expected the server detail to survive, got: %v", runError)
	}
}

func TestPlaygroundStatusPrintsThePhaseAndMessage(t *testing.T) {
	message := "waiting for the agent"
	mock := &playgroundMock{status: &client.PlaygroundStatus{
		ClusterID:     "cluster-42",
		Phase:         "provisioning",
		StatusMessage: &message,
		ExpiresAt:     "2026-08-14T09:00:00Z",
	}}
	withPlaygroundMock(t, mock)
	output := captureStdout(t, func() {
		if err := clusterPlaygroundStatusCmd.RunE(clusterPlaygroundStatusCmd, []string{"cluster-42"}); err != nil {
			t.Fatalf("status failed: %v", err)
		}
	})
	if mock.statusRequested != "cluster-42" {
		t.Errorf("expected the cluster id to be passed through, got %q", mock.statusRequested)
	}
	for _, expected := range []string{"provisioning", "2026-08-14T09:00:00Z", "waiting for the agent"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q in the output, got: %s", expected, output)
		}
	}
}

// An absent status_message must not print an empty Message line.
func TestPlaygroundStatusOmitsAnAbsentMessage(t *testing.T) {
	withPlaygroundMock(t, &playgroundMock{status: &client.PlaygroundStatus{
		ClusterID: "cluster-42",
		Phase:     "ready",
		ExpiresAt: "2026-08-14T09:00:00Z",
	}})
	output := captureStdout(t, func() {
		if err := clusterPlaygroundStatusCmd.RunE(clusterPlaygroundStatusCmd, []string{"cluster-42"}); err != nil {
			t.Fatalf("status failed: %v", err)
		}
	})
	if strings.Contains(output, "Message:") {
		t.Errorf("expected no Message line, got: %s", output)
	}
}

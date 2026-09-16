package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type hetznerStateMock struct {
	baseMock
	stopOptions  *client.StopClusterOptions
	startOptions *client.StartClusterOptions
}

func (mock *hetznerStateMock) StopHetznerCluster(clusterID string, options client.StopClusterOptions) (*client.ProviderStopClusterResponse, error) {
	mock.stopOptions = &options
	return &client.ProviderStopClusterResponse{Success: true, ClusterID: clusterID, StatePreserved: options.PreserveState == nil || *options.PreserveState,
		StateSnapshot: &client.StateSnapshotRef{ID: "snap-1", ExecutionID: "exec-1", Status: "capturing"}}, nil
}

func (mock *hetznerStateMock) StartHetznerCluster(clusterID string, options client.StartClusterOptions) (*client.ProviderStartClusterResult, error) {
	mock.startOptions = &options
	return &client.ProviderStartClusterResult{Scope: options.Scope, CreatedOperations: 3, StateRestore: "requested"}, nil
}

func boolText(value *bool) string {
	if value == nil {
		return "nil"
	}
	if *value {
		return "true"
	}
	return "false"
}

// --preserve-state is three-state on the wire: absent leaves the backend to
// capture when it can, and only an explicit true/false is sent.
func TestHetznerStopPreserveStateFlagIsThreeState(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		want string
	}{
		{name: "absent", want: "nil"},
		{name: "explicit false", args: []string{"--preserve-state=false"}, want: "false"},
		{name: "explicit true", args: []string{"--preserve-state", "true"}, want: "true"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Cleanup(func() {
				_ = hetznerStopCmd.Flags().Set("preserve-state", "")
				_ = hetznerStopCmd.Flags().Set("force", "false")
			})
			mock := &hetznerStateMock{}
			setMockClient(t, mock)
			args := append([]string{"cluster", "hetzner", "stop", testClusterID}, testCase.args...)
			var err error
			output := captureStdout(t, func() { _, err = executeCommand(args...) })
			if err != nil {
				t.Fatalf("execute failed: %v\noutput: %s", err, output)
			}
			if mock.stopOptions == nil {
				t.Fatal("expected StopHetznerCluster call")
			}
			if got := boolText(mock.stopOptions.PreserveState); got != testCase.want {
				t.Fatalf("preserve_state = %s, want %s", got, testCase.want)
			}
			if mock.stopOptions.Force {
				t.Fatal("a plain stop must not send force")
			}
			if testCase.want != "false" && !strings.Contains(output, "Cluster state: preserved") {
				t.Fatalf("expected the preserved-state line in output, got: %s", output)
			}
		})
	}
}

func TestHetznerStartRestoreStateFlagIsThreeState(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		want string
	}{
		{name: "absent", want: "nil"},
		{name: "explicit false", args: []string{"--restore-state=false"}, want: "false"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Cleanup(func() {
				_ = hetznerStartCmd.Flags().Set("restore-state", "")
				_ = hetznerStartCmd.Flags().Set("scope", "all")
			})
			mock := &hetznerStateMock{}
			setMockClient(t, mock)
			args := append([]string{"cluster", "hetzner", "start", testClusterID, "--scope", "control_plane"}, testCase.args...)
			var err error
			output := captureStdout(t, func() { _, err = executeCommand(args...) })
			if err != nil {
				t.Fatalf("execute failed: %v\noutput: %s", err, output)
			}
			if mock.startOptions == nil {
				t.Fatal("expected StartHetznerCluster call")
			}
			if mock.startOptions.Scope != "control_plane" {
				t.Fatalf("scope = %q, want control_plane", mock.startOptions.Scope)
			}
			if got := boolText(mock.startOptions.RestoreState); got != testCase.want {
				t.Fatalf("restore_state = %s, want %s", got, testCase.want)
			}
			if !strings.Contains(output, "restores the snapshot captured at stop") {
				t.Fatalf("expected the restore line in output, got: %s", output)
			}
		})
	}
}

func TestHetznerStopModeFlagIsSentAndValidated(t *testing.T) {
	t.Cleanup(func() {
		_ = hetznerStopCmd.Flags().Set("mode", "")
		_ = hetznerStopCmd.Flags().Set("force", "false")
	})
	mock := &hetznerStateMock{}
	setMockClient(t, mock)
	var err error
	output := captureStdout(t, func() {
		_, err = executeCommand("cluster", "hetzner", "stop", testClusterID, "--mode", "Pause")
	})
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, output)
	}
	if mock.stopOptions == nil || mock.stopOptions.Mode != "pause" {
		t.Fatalf("--mode must reach the client normalised, got %+v", mock.stopOptions)
	}

	_ = hetznerStopCmd.Flags().Set("mode", "")
	mock = &hetznerStateMock{}
	setMockClient(t, mock)
	_, err = executeCommand("cluster", "hetzner", "stop", testClusterID, "--mode", "hibernate")
	if err == nil || !strings.Contains(err.Error(), "invalid --mode") {
		t.Fatalf("an unknown mode must be refused locally, got %v", err)
	}
	if mock.stopOptions != nil {
		t.Fatal("a refused mode must not reach the client")
	}
}

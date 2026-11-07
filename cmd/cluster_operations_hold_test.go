package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

const staffRecoveryTestCluster = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"

type fakeStaffRecoveryClient struct {
	baseURL      string
	sessionToken string
	calls        []string
	duration     time.Duration
	reason       string
	version      string
	force        bool
	hold         *client.OperationsHold
	lift         *client.OperationsHoldLift
	repair       *client.K8sVersionRecordRepair
	failure      error
}

func (fake *fakeStaffRecoveryClient) GetOperationsHold(_ context.Context, clusterID string) (*client.OperationsHold, error) {
	fake.calls = append(fake.calls, "get "+clusterID)
	return fake.hold, fake.failure
}

func (fake *fakeStaffRecoveryClient) SetOperationsHold(_ context.Context, clusterID string, duration time.Duration, reason string) (*client.OperationsHold, error) {
	fake.calls = append(fake.calls, "set "+clusterID)
	fake.duration, fake.reason = duration, reason
	return fake.hold, fake.failure
}

func (fake *fakeStaffRecoveryClient) LiftOperationsHold(_ context.Context, clusterID string, reason string, force bool) (*client.OperationsHoldLift, error) {
	fake.calls = append(fake.calls, "lift "+clusterID)
	fake.reason, fake.force = reason, force
	return fake.lift, fake.failure
}

func (fake *fakeStaffRecoveryClient) RollbackK8sVersionRecord(_ context.Context, clusterID string, version string, reason string) (*client.K8sVersionRecordRepair, error) {
	fake.calls = append(fake.calls, "rollback-record "+clusterID)
	fake.version, fake.reason = version, reason
	return fake.repair, fake.failure
}

// installFakeStaffRecoveryClient swaps the staff client factory, sets a staff
// session and resets the commands' flags after the test (cobra keeps flag
// values between executions of the shared root command).
func installFakeStaffRecoveryClient(t *testing.T, fake *fakeStaffRecoveryClient) {
	t.Helper()
	t.Setenv(envAnkraStaffSession, "staff-session-token")
	t.Setenv(envAnkraBaseURL, "https://platform.example.test")
	previousFactory := newStaffRecoveryClient
	newStaffRecoveryClient = func(baseURL string, sessionToken string) staffRecoveryClient {
		fake.baseURL, fake.sessionToken = baseURL, sessionToken
		return fake
	}
	t.Cleanup(func() {
		newStaffRecoveryClient = previousFactory
		for _, command := range []*cobra.Command{clusterOperationsHoldSetCmd, clusterOperationsHoldGetCmd,
			clusterOperationsHoldLiftCmd, clusterUpgradeRollbackRecordCmd} {
			for _, name := range []string{"reason", "output"} {
				if flag := command.Flags().Lookup(name); flag != nil {
					_ = flag.Value.Set("")
					flag.Changed = false
				}
			}
			for _, name := range []string{"force", "yes"} {
				if flag := command.Flags().Lookup(name); flag != nil {
					_ = flag.Value.Set("false")
					flag.Changed = false
				}
			}
		}
		if flag := clusterOperationsHoldSetCmd.Flags().Lookup("for"); flag != nil {
			_ = flag.Value.Set("0s")
			flag.Changed = false
		}
	})
}

func staffHeld(blockedUntil time.Time) *client.OperationsHold {
	setBy := "staff@ankra.io"
	reason := "k3s hop failed"
	return &client.OperationsHold{ClusterID: staffRecoveryTestCluster, Held: true, BlockedUntil: &blockedUntil,
		RemainingSeconds: 4 * 60 * 60, Source: "staff_hold", SetBy: &setBy, SetAt: &blockedUntil, Reason: &reason}
}

func TestOperationsHoldSetSendsTheDurationAndReasonWithTheStaffSession(t *testing.T) {
	fake := &fakeStaffRecoveryClient{hold: staffHeld(time.Date(2026, 10, 27, 16, 0, 0, 0, time.UTC))}
	installFakeStaffRecoveryClient(t, fake)

	output, runError := executeCommand("cluster", "operations", "hold", "set", staffRecoveryTestCluster,
		"--for", "4h", "--reason", "k3s hop failed")
	if runError != nil {
		t.Fatalf("hold set: %v\n%s", runError, output)
	}
	if fake.duration != 4*time.Hour || fake.reason != "k3s hop failed" || fake.sessionToken != "staff-session-token" ||
		fake.baseURL != "https://platform.example.test" {
		t.Fatalf("fake = %+v", fake)
	}
	for _, fragment := range []string{"is held", "2026-10-27T16:00:00Z", "staff@ankra.io", "k3s hop failed"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("output %q does not contain %q", output, fragment)
		}
	}
}

func TestOperationsHoldRefusesBadInvocationsBeforeCallingThePlatform(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		args     []string
		exitCode int
	}{
		{"duration too long", []string{"cluster", "operations", "hold", "set", staffRecoveryTestCluster, "--for", "25h", "--reason", "x"}, exitUsage},
		{"duration too short", []string{"cluster", "operations", "hold", "set", staffRecoveryTestCluster, "--for", "30s", "--reason", "x"}, exitUsage},
		{"no reason", []string{"cluster", "operations", "hold", "set", staffRecoveryTestCluster, "--for", "1h"}, exitUsage},
		{"a cluster name", []string{"cluster", "operations", "hold", "get", "production"}, exitUsage},
		{"lift without a reason", []string{"cluster", "operations", "hold", "lift", staffRecoveryTestCluster}, exitUsage},
		{"repair without a reason", []string{"cluster", "upgrade", "rollback-record", staffRecoveryTestCluster, "v1.34.5+k3s1", "--yes"}, exitUsage},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fake := &fakeStaffRecoveryClient{}
			installFakeStaffRecoveryClient(t, fake)
			_, runError := executeCommand(testCase.args...)
			if runError == nil || exitCodeFor(runError) != testCase.exitCode {
				t.Fatalf("error = %v (exit %d), want exit %d", runError, exitCodeFor(runError), testCase.exitCode)
			}
			if len(fake.calls) != 0 {
				t.Fatalf("the platform was called: %v", fake.calls)
			}
		})
	}
}

func TestStaffRecoveryCommandsNeedAStaffSession(t *testing.T) {
	fake := &fakeStaffRecoveryClient{}
	installFakeStaffRecoveryClient(t, fake)
	t.Setenv(envAnkraStaffSession, "")
	_, runError := executeCommand("cluster", "operations", "hold", "get", staffRecoveryTestCluster)
	if runError == nil || exitCodeFor(runError) != exitAuth || !strings.Contains(runError.Error(), envAnkraStaffSession) {
		t.Fatalf("error = %v (exit %d), want an auth refusal naming %s", runError, exitCodeFor(runError), envAnkraStaffSession)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("the platform was called: %v", fake.calls)
	}
}

func TestOperationsHoldGetShowsThePendingRecordCheck(t *testing.T) {
	observedAt := time.Date(2026, 10, 27, 12, 5, 0, 0, time.UTC)
	hold := staffHeld(time.Date(2026, 10, 27, 16, 0, 0, 0, time.UTC))
	hold.RecordCheck = &client.OperationsHoldRecordCheck{
		RepairedAt: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC), Version: "v1.34.5+k3s1",
		Nodes: []client.OperationsHoldRecordNode{
			{Name: "cp-0", Role: "control_plane", Version: "v1.34.5+k3s1", ObservedVersion: "v1.34.5+k3s1",
				RunningVersion: "v1.34.5+k3s1", ObservedAt: &observedAt, Status: "confirmed"},
			{Name: "worker-0", Role: "worker", Version: "v1.34.5+k3s1", Status: "awaiting_read"},
		},
	}
	fake := &fakeStaffRecoveryClient{hold: hold}
	installFakeStaffRecoveryClient(t, fake)
	output, runError := executeCommand("cluster", "operations", "hold", "get", staffRecoveryTestCluster)
	if runError != nil {
		t.Fatalf("hold get: %v", runError)
	}
	for _, fragment := range []string{"NOT confirmed yet", "confirmed", "awaiting_read", "worker-0"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("output %q does not contain %q", output, fragment)
		}
	}
}

func TestOperationsHoldLiftForceAsksFirstAndPassesForce(t *testing.T) {
	liftedUntil := time.Date(2026, 10, 27, 16, 0, 0, 0, time.UTC)
	fake := &fakeStaffRecoveryClient{lift: &client.OperationsHoldLift{ClusterID: staffRecoveryTestCluster, Lifted: true,
		LiftedBlockedUntil: &liftedUntil, Forced: true}}
	installFakeStaffRecoveryClient(t, fake)

	rootCmd.SetIn(strings.NewReader("n\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	_, runError := executeCommand("cluster", "operations", "hold", "lift", staffRecoveryTestCluster,
		"--reason", "customer asked", "--force")
	if !errors.Is(runError, errCancelled) || len(fake.calls) != 0 {
		t.Fatalf("declined forced lift: error %v, calls %v", runError, fake.calls)
	}

	output, runError := executeCommand("cluster", "operations", "hold", "lift", staffRecoveryTestCluster,
		"--reason", "customer asked", "--force", "--yes")
	if runError != nil {
		t.Fatalf("forced lift: %v", runError)
	}
	if !fake.force || fake.reason != "customer asked" || !strings.Contains(output, "overriding an unconfirmed") {
		t.Fatalf("forced lift: fake %+v output %q", fake, output)
	}
}

func TestUpgradeRollbackRecordPrintsWhatWasWrittenAndMapsRefusals(t *testing.T) {
	observedAt := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	blockedUntil := observedAt.Add(4 * time.Hour)
	fake := &fakeStaffRecoveryClient{repair: &client.K8sVersionRecordRepair{
		ClusterID: staffRecoveryTestCluster, Version: "v1.34.5+k3s1", RepairedAt: observedAt, BlockedUntil: &blockedUntil,
		KubernetesRowRepaired: true,
		Nodes: []client.RecordedK8sNode{{Name: "cp-0", Role: "control_plane", PreviousState: "updating",
			PreviousVersion: "v1.35.2+k3s1", Version: "v1.34.5+k3s1", ObservedVersion: "v1.34.5+k3s1",
			ObservedAt: &observedAt, ObservationPostdatesAttempt: true}},
	}}
	installFakeStaffRecoveryClient(t, fake)
	output, runError := executeCommand("cluster", "upgrade", "rollback-record", staffRecoveryTestCluster, "v1.34.5+k3s1",
		"--reason", "cp-0 rolled back", "--yes")
	if runError != nil {
		t.Fatalf("rollback-record: %v", runError)
	}
	if fake.version != "v1.34.5+k3s1" || fake.reason != "cp-0 rolled back" {
		t.Fatalf("fake = %+v", fake)
	}
	for _, fragment := range []string{"repaired to v1.34.5+k3s1", "updating v1.35.2+k3s1 -> v1.34.5+k3s1 up",
		"read after its last write attempt", "operations hold get " + staffRecoveryTestCluster} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("output %q does not contain %q", output, fragment)
		}
	}

	for _, testCase := range []struct {
		statusCode int
		exitCode   int
	}{{409, exitError}, {422, exitUsage}, {403, exitForbidden}, {404, exitNotFound}} {
		fake.failure = client.NewUnexpectedResponseError(testCase.statusCode, "refused")
		_, runError = executeCommand("cluster", "upgrade", "rollback-record", staffRecoveryTestCluster, "v1.34.5+k3s1",
			"--reason", "again", "--yes")
		if exitCodeFor(runError) != testCase.exitCode {
			t.Fatalf("status %d: exit %d, want %d (%v)", testCase.statusCode, exitCodeFor(runError), testCase.exitCode, runError)
		}
	}
}

func TestUpgradeRollbackRecordJSONOutputIsTheServerDocument(t *testing.T) {
	document := []byte(`{"cluster_id":"` + staffRecoveryTestCluster + `","version":"v1.34.5+k3s1","nodes":[],"future_field":1}`)
	repair := &client.K8sVersionRecordRepair{RawDocument: client.NewRawDocument(document), ClusterID: staffRecoveryTestCluster}
	fake := &fakeStaffRecoveryClient{repair: repair}
	installFakeStaffRecoveryClient(t, fake)
	output, runError := executeCommand("cluster", "upgrade", "rollback-record", staffRecoveryTestCluster, "v1.34.5+k3s1",
		"--reason", "x", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("rollback-record -o json: %v", runError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil || decoded["future_field"] == nil {
		t.Fatalf("output %q is not the server document (%v)", output, unmarshalError)
	}
}

func TestStaffRecoveryCommandsNeverAskForTheLoginToken(t *testing.T) {
	for _, command := range []*cobra.Command{clusterOperationsHoldCmd, clusterOperationsHoldSetCmd,
		clusterOperationsHoldGetCmd, clusterOperationsHoldLiftCmd, clusterUpgradeRollbackRecordCmd} {
		if commandRequiresAuth(command) {
			t.Fatalf("'%s' must not require the CLI login: it authenticates with a staff session", command.CommandPath())
		}
	}
}

func TestUpgradeRollbackRecordRefusesAnEmptyVersionBeforeAsking(t *testing.T) {
	fake := &fakeStaffRecoveryClient{}
	installFakeStaffRecoveryClient(t, fake)
	_, runError := executeCommand("cluster", "upgrade", "rollback-record", staffRecoveryTestCluster, "  ",
		"--reason", "cp-0 rolled back", "--yes")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("an empty version must be a usage error, got %v", runError)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("an empty version must send nothing, got %v", fake.calls)
	}
}

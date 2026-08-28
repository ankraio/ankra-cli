package cmd

import (
	"context"
	"errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"strings"
	"testing"

	"ankra/internal/client"
)

type upcloudDeprovisionMock struct {
	baseMock
	called       bool
	gotClusterID string
}

func (m *upcloudDeprovisionMock) DeprovisionUpcloudCluster(clusterID string, force bool) (*client.DeprovisionUpcloudClusterResponse, error) {
	m.called = true
	m.gotClusterID = clusterID
	return &client.DeprovisionUpcloudClusterResponse{Success: true, ClusterID: clusterID}, nil
}

type upcloudNodeGroupDeleteMock struct {
	baseMock
	called       bool
	gotClusterID string
	gotGroupName string
}

func (m *upcloudNodeGroupDeleteMock) DeleteUpcloudNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*client.DeleteNodeGroupResult, bool, error) {
	m.called = true
	m.gotClusterID = clusterID
	m.gotGroupName = groupName
	return &client.DeleteNodeGroupResult{GroupName: groupName, Deleted: 1}, false, nil
}

func TestUpcloudDeprovision_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &upcloudDeprovisionMock{}
	resetConfirmFlag(t, upcloudDeprovisionCmd)
	_, err := runWithInput(t, mock, "n\n", "cluster", "upcloud", "deprovision", "uc-123")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if mock.called {
		t.Error("expected no deprovision call when declined")
	}
}

func TestUpcloudDeprovision_YesProceeds(t *testing.T) {
	mock := &upcloudDeprovisionMock{}
	resetConfirmFlag(t, upcloudDeprovisionCmd)
	out, err := runWithInput(t, mock, "", "cluster", "upcloud", "deprovision", "uc-123", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !mock.called {
		t.Fatal("expected deprovision call with --yes")
	}
	if mock.gotClusterID != "uc-123" {
		t.Errorf("cluster id = %q, want uc-123", mock.gotClusterID)
	}
}

func TestUpcloudNodeGroupDelete_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &upcloudNodeGroupDeleteMock{}
	resetConfirmFlag(t, upcloudNodeGroupDeleteCmd)
	_, err := runWithInput(t, mock, "n\n", "cluster", "upcloud", "node-group", "delete", "uc-123", "workers")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if mock.called {
		t.Error("expected no delete call when declined")
	}
}

func TestUpcloudNodeGroupDelete_YesProceeds(t *testing.T) {
	mock := &upcloudNodeGroupDeleteMock{}
	resetConfirmFlag(t, upcloudNodeGroupDeleteCmd)
	out, err := runWithInput(t, mock, "", "cluster", "upcloud", "node-group", "delete", "uc-123", "workers", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !mock.called {
		t.Fatal("expected delete call with --yes")
	}
	if mock.gotClusterID != "uc-123" || mock.gotGroupName != "workers" {
		t.Errorf("got cluster=%q group=%q, want uc-123/workers", mock.gotClusterID, mock.gotGroupName)
	}
}

type upcloudCreateMock struct {
	baseMock
	gotRequest client.CreateUpcloudClusterRequest
}

func (m *upcloudCreateMock) CreateUpcloudCluster(req client.CreateUpcloudClusterRequest) (*client.CreateUpcloudClusterResponse, error) {
	m.gotRequest = req
	return &client.CreateUpcloudClusterResponse{ClusterID: "uc-123", Name: req.Name}, nil
}

// The flag used to default to the literal 10.0.0.0/16, so every CLI create
// pinned the same range and the platform refused it as overlapping the
// account's existing private network. Unset now means "let Ankra pick".
func TestUpcloudCreate_NetworkIPRangeDefaultsToAuto(t *testing.T) {
	mock := &upcloudCreateMock{}
	resetConfirmFlag(t, upcloudCreateCmd)
	_ = captureStdout(t, func() {
		if _, err := runWithInput(t, mock, "", "cluster", "upcloud", "create",
			"--name", "gpu-chat", "--credential-id", "cred-1",
			"--ssh-key-credential-id", "ssh-1", "--zone", "fi-hel2"); err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if mock.gotRequest.NetworkIPRange != "" {
		t.Errorf("NetworkIPRange = %q, want empty so the platform picks a free range",
			mock.gotRequest.NetworkIPRange)
	}
}

func TestUpcloudCreate_NetworkIPRangeFlagPinsTheRange(t *testing.T) {
	mock := &upcloudCreateMock{}
	resetConfirmFlag(t, upcloudCreateCmd)
	_ = captureStdout(t, func() {
		if _, err := runWithInput(t, mock, "", "cluster", "upcloud", "create",
			"--name", "gpu-chat", "--credential-id", "cred-1",
			"--ssh-key-credential-id", "ssh-1", "--zone", "fi-hel2",
			"--network-ip-range", "10.90.0.0/24"); err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if mock.gotRequest.NetworkIPRange != "10.90.0.0/24" {
		t.Errorf("NetworkIPRange = %q, want 10.90.0.0/24", mock.gotRequest.NetworkIPRange)
	}
}

func TestUpcloudCreate_CNIFlagReachesRequest(t *testing.T) {
	mock := &upcloudCreateMock{}
	resetConfirmFlag(t, upcloudCreateCmd)
	_ = captureStdout(t, func() {
		if _, err := runWithInput(t, mock, "", "cluster", "upcloud", "create",
			"--name", "gpu-chat", "--credential-id", "cred-1",
			"--ssh-key-credential-id", "ssh-1", "--zone", "fi-hel2",
			"--cni", "cilium"); err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if mock.gotRequest.CNI != "cilium" {
		t.Errorf("CNI = %q, want cilium", mock.gotRequest.CNI)
	}
}

func TestUpcloudCreate_CNIDefaultsToPlatformChoice(t *testing.T) {
	mock := &upcloudCreateMock{}
	resetConfirmFlag(t, upcloudCreateCmd)
	_ = captureStdout(t, func() {
		if _, err := runWithInput(t, mock, "", "cluster", "upcloud", "create",
			"--name", "gpu-chat", "--credential-id", "cred-1",
			"--ssh-key-credential-id", "ssh-1", "--zone", "fi-hel2"); err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if mock.gotRequest.CNI != "" {
		t.Errorf("CNI = %q, want empty so the platform applies its default", mock.gotRequest.CNI)
	}
}

func TestUpcloudCreate_ZonePoolFlagsReachTheRequest(t *testing.T) {
	mock := &upcloudCreateMock{}
	resetConfirmFlag(t, upcloudCreateCmd)
	resetSliceFlag(t, upcloudCreateCmd, "zones")
	_ = captureStdout(t, func() {
		if _, err := runWithInput(t, mock, "", "cluster", "upcloud", "create",
			"--name", "prod", "--credential-id", "cred-1",
			"--ssh-key-credential-id", "ssh-1", "--zone", "fi-hel1",
			"--zones", "fi-hel1,fi-hel2,se-sto1", "--control-plane-count", "3",
			"--network-mode", "wireguard_mesh"); err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if got := strings.Join(mock.gotRequest.Zones, ","); got != "fi-hel1,fi-hel2,se-sto1" {
		t.Errorf("Zones = %q, want the pool in order", got)
	}
	if mock.gotRequest.NetworkMode != "wireguard_mesh" || mock.gotRequest.ControlPlaneCount != 3 {
		t.Errorf("NetworkMode/ControlPlaneCount = %q/%d", mock.gotRequest.NetworkMode, mock.gotRequest.ControlPlaneCount)
	}
}

// Omitted flags must stay omitted on the wire: the platform derives the
// mode and a single-zone cluster carries no pool (and no field an
// un-flagged organisation would be refused for).
func TestUpcloudCreate_ZonePoolFlagsDefaultToNothing(t *testing.T) {
	mock := &upcloudCreateMock{}
	resetConfirmFlag(t, upcloudCreateCmd)
	resetSliceFlag(t, upcloudCreateCmd, "zones")
	_ = captureStdout(t, func() {
		if _, err := runWithInput(t, mock, "", "cluster", "upcloud", "create",
			"--name", "dev", "--credential-id", "cred-1",
			"--ssh-key-credential-id", "ssh-1", "--zone", "fi-hel1"); err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if len(mock.gotRequest.Zones) != 0 || mock.gotRequest.NetworkMode != "" {
		t.Errorf("Zones/NetworkMode = %v/%q, want empty", mock.gotRequest.Zones, mock.gotRequest.NetworkMode)
	}
}

type upcloudZonePoolMock struct {
	baseMock
	gotClusterID string
	gotZones     []string
	gotWait      bool
}

func (m *upcloudZonePoolMock) UpdateUpcloudZonePool(_ context.Context, clusterID string, zones []string, wait bool) (*client.UpdateUpcloudZonePoolResult, bool, error) {
	m.gotClusterID, m.gotZones, m.gotWait = clusterID, zones, wait
	return &client.UpdateUpcloudZonePoolResult{Zones: zones, AddedZones: []string{"se-sto1"}}, false, nil
}

func TestUpcloudZones_SendsTheDesiredPool(t *testing.T) {
	mock := &upcloudZonePoolMock{}
	output := captureStdout(t, func() {
		if _, err := runWithInput(t, mock, "", "cluster", "upcloud", "zones", "uc-123",
			"--zones", "fi-hel1,fi-hel2,se-sto1", "--wait"); err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if mock.gotClusterID != "uc-123" || strings.Join(mock.gotZones, ",") != "fi-hel1,fi-hel2,se-sto1" || !mock.gotWait {
		t.Errorf("request = %s / %v / wait=%v", mock.gotClusterID, mock.gotZones, mock.gotWait)
	}
	if !strings.Contains(output, "added se-sto1") {
		t.Errorf("output = %q, want the added zone reported", output)
	}
}

type upcloudNodeGroupZoneMock struct {
	baseMock
	gotRequest client.AddNodeGroupRequest
}

func (m *upcloudNodeGroupZoneMock) AddUpcloudNodeGroup(_ context.Context, _ string, req client.AddNodeGroupRequest, _ bool) (*client.AddNodeGroupResult, bool, error) {
	m.gotRequest = req
	return &client.AddNodeGroupResult{GroupName: req.Name, Count: req.Count}, false, nil
}

func TestUpcloudNodeGroupAdd_ZoneFlagPinsTheGroup(t *testing.T) {
	mock := &upcloudNodeGroupZoneMock{}
	_ = captureStdout(t, func() {
		if _, err := runWithInput(t, mock, "", "cluster", "upcloud", "node-group", "add", "uc-123",
			"--name", "db-sto", "--instance-type", "4xCPU-8GB", "--count", "2", "--zone", "se-sto1", "--wait"); err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	})
	if mock.gotRequest.Zone != "se-sto1" || mock.gotRequest.Name != "db-sto" {
		t.Errorf("request = %+v", mock.gotRequest)
	}
}

// resetSliceFlag empties a list flag between runs: pflag appends on every
// Set after the first, so the generic default reset would stack a literal
// "[]" onto the slice.
func resetSliceFlag(t *testing.T, command *cobra.Command, name string) {
	t.Helper()
	flag := command.Flags().Lookup(name)
	if flag == nil {
		t.Fatalf("flag %s not registered", name)
	}
	if slice, isSlice := flag.Value.(pflag.SliceValue); isSlice {
		_ = slice.Replace([]string{})
	}
	flag.Changed = false
}

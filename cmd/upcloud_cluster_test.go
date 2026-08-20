package cmd

import (
	"context"
	"errors"
	"testing"

	"ankra/internal/client"
)

type upcloudCreateMock struct {
	baseMock
	called     bool
	gotRequest client.CreateUpcloudClusterRequest
}

func (m *upcloudCreateMock) CreateUpcloudCluster(req client.CreateUpcloudClusterRequest) (*client.CreateUpcloudClusterResponse, error) {
	m.called = true
	m.gotRequest = req
	return &client.CreateUpcloudClusterResponse{ClusterID: "uc-cluster-123", Name: req.Name}, nil
}

// The platform assigns a free private range when none is sent; a baked-in
// 10.0.0.0/16 default collided with the networks most accounts already hold
// and was refused at create time, so the flag now defaults to "let the
// platform pick" while an explicit CIDR still travels unchanged.
func TestUpcloudCreateNetworkRangeDefaultsToPlatformAssignment(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantRange string
	}{
		{name: "default is unset so the platform assigns a free range"},
		{name: "explicit range is honoured", args: []string{"--network-ip-range", "10.84.0.0/16"}, wantRange: "10.84.0.0/16"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetConfirmFlag(t, upcloudCreateCmd)
			mock := &upcloudCreateMock{}
			args := append([]string{"cluster", "upcloud", "create",
				"--name", "range-test",
				"--credential-id", "cred-1",
				"--ssh-key-credential-id", "ssh-1",
				"--zone", "fi-hel2"}, tt.args...)
			out, err := runWithInput(t, mock, "", args...)
			if err != nil {
				t.Fatalf("execute failed: %v\noutput: %s", err, out)
			}
			if !mock.called {
				t.Fatal("expected CreateUpcloudCluster call")
			}
			if mock.gotRequest.NetworkIPRange != tt.wantRange {
				t.Errorf("network_ip_range = %q, want %q", mock.gotRequest.NetworkIPRange, tt.wantRange)
			}
		})
	}
	if flag := upcloudCreateCmd.Flags().Lookup("network-ip-range"); flag == nil || flag.DefValue != "" {
		t.Fatalf("--network-ip-range must default to empty (platform-assigned), got %+v", flag)
	}
}

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

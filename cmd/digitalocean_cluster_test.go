package cmd

import (
	"context"
	"errors"
	"testing"

	"ankra/internal/client"
)

type digitaloceanCreateMock struct {
	baseMock
	called     bool
	gotRequest client.CreateDigitaloceanClusterRequest
}

func (m *digitaloceanCreateMock) CreateDigitaloceanCluster(req client.CreateDigitaloceanClusterRequest) (*client.CreateDigitaloceanClusterResponse, error) {
	m.called = true
	m.gotRequest = req
	return &client.CreateDigitaloceanClusterResponse{ClusterID: "do-cluster-123", Name: req.Name}, nil
}

// --include-dns has to reach the wire on every provider lane, not just the
// three that grew their own flag tests: the field carries no omitempty
// precisely so an explicit false survives the round trip instead of being
// re-defaulted to true server-side. It is also independent of the
// cloud-provider/networking pair in both directions.
func TestDigitaloceanCreateSendsIncludeDNS(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantDNS        bool
		wantNetworking bool
	}{
		{name: "default", wantDNS: true, wantNetworking: true},
		{name: "dns off", args: []string{"--include-dns=false"}, wantNetworking: true},
		{name: "networking off keeps dns", args: []string{"--include-networking=false"}, wantDNS: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetConfirmFlag(t, digitaloceanCreateCmd)
			mock := &digitaloceanCreateMock{}
			args := append([]string{"cluster", "digitalocean", "create",
				"--name", "dns-test",
				"--credential-id", "cred-1",
				"--ssh-key-credential-id", "ssh-1",
				"--region", "fra1"}, tt.args...)
			out, err := runWithInput(t, mock, "", args...)
			if err != nil {
				t.Fatalf("execute failed: %v\noutput: %s", err, out)
			}
			if !mock.called {
				t.Fatal("expected CreateDigitaloceanCluster call")
			}
			if mock.gotRequest.IncludeDNS != tt.wantDNS {
				t.Errorf("include_dns = %v, want %v", mock.gotRequest.IncludeDNS, tt.wantDNS)
			}
			if mock.gotRequest.IncludeNetworking != tt.wantNetworking {
				t.Errorf("include_networking = %v, want %v", mock.gotRequest.IncludeNetworking, tt.wantNetworking)
			}
		})
	}
}

type digitaloceanDeprovisionMock struct {
	baseMock
	called       bool
	gotClusterID string
}

func (m *digitaloceanDeprovisionMock) DeprovisionDigitaloceanCluster(clusterID string, force bool) (*client.DeprovisionDigitaloceanClusterResponse, error) {
	m.called = true
	m.gotClusterID = clusterID
	return &client.DeprovisionDigitaloceanClusterResponse{Success: true, ClusterID: clusterID}, nil
}

type digitaloceanNodeGroupDeleteMock struct {
	baseMock
	called       bool
	gotClusterID string
	gotGroupName string
}

func (m *digitaloceanNodeGroupDeleteMock) DeleteDigitaloceanNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*client.DeleteNodeGroupResult, bool, error) {
	m.called = true
	m.gotClusterID = clusterID
	m.gotGroupName = groupName
	return &client.DeleteNodeGroupResult{GroupName: groupName, Deleted: 1}, false, nil
}

func TestDigitaloceanDeprovision_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &digitaloceanDeprovisionMock{}
	resetConfirmFlag(t, digitaloceanDeprovisionCmd)
	_, err := runWithInput(t, mock, "n\n", "cluster", "digitalocean", "deprovision", "uc-123")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if mock.called {
		t.Error("expected no deprovision call when declined")
	}
}

func TestDigitaloceanDeprovision_YesProceeds(t *testing.T) {
	mock := &digitaloceanDeprovisionMock{}
	resetConfirmFlag(t, digitaloceanDeprovisionCmd)
	out, err := runWithInput(t, mock, "", "cluster", "digitalocean", "deprovision", "uc-123", "--yes")
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

func TestDigitaloceanNodeGroupDelete_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &digitaloceanNodeGroupDeleteMock{}
	resetConfirmFlag(t, digitaloceanNodeGroupDeleteCmd)
	_, err := runWithInput(t, mock, "n\n", "cluster", "digitalocean", "node-group", "delete", "uc-123", "workers")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if mock.called {
		t.Error("expected no delete call when declined")
	}
}

func TestDigitaloceanNodeGroupDelete_YesProceeds(t *testing.T) {
	mock := &digitaloceanNodeGroupDeleteMock{}
	resetConfirmFlag(t, digitaloceanNodeGroupDeleteCmd)
	out, err := runWithInput(t, mock, "", "cluster", "digitalocean", "node-group", "delete", "uc-123", "workers", "--yes")
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

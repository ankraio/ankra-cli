package cmd

import (
	"context"
	"errors"
	"testing"

	"ankra/internal/client"
)

type digitaloceanDeprovisionMock struct {
	baseMock
	called       bool
	gotClusterID string
}

func (m *digitaloceanDeprovisionMock) DeprovisionDigitaloceanCluster(clusterID string) (*client.DeprovisionDigitaloceanClusterResponse, error) {
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

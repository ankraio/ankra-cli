package cmd

import (
	"context"
	"errors"
	"testing"

	"ankra/internal/client"
)

type upcloudDeprovisionMock struct {
	baseMock
	called       bool
	gotClusterID string
}

func (m *upcloudDeprovisionMock) DeprovisionUpcloudCluster(clusterID string) (*client.DeprovisionUpcloudClusterResponse, error) {
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

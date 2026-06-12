package cmd

import (
	"context"
	"strings"
	"testing"

	"ankra/internal/client"
)

type clusterAccessMock struct {
	baseMock
	grants          []client.ClusterAccessGrant
	createdRequest  *client.CreateClusterAccessGrantRequest
	deletedGrantIDs []string
}

func (m *clusterAccessMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "11111111-2222-3333-4444-555555555555", Name: name}, nil
}

func (m *clusterAccessMock) ListClusterAccessGrants(ctx context.Context, clusterID string) (*client.ListClusterAccessGrantsResponse, error) {
	return &client.ListClusterAccessGrantsResponse{Result: m.grants}, nil
}

func (m *clusterAccessMock) CreateClusterAccessGrant(ctx context.Context, clusterID string, request client.CreateClusterAccessGrantRequest) (*client.CreateClusterAccessGrantResponse, error) {
	m.createdRequest = &request
	email := request.UserEmail
	return &client.CreateClusterAccessGrantResponse{Grant: client.ClusterAccessGrant{
		ID:              "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		UserEmail:       &email,
		Scope:           request.Scope,
		Namespace:       request.Namespace,
		Role:            request.Role,
		ReconcileStatus: "pending",
		CreatedAt:       "2026-06-01T12:00:00Z",
	}}, nil
}

func (m *clusterAccessMock) DeleteClusterAccessGrant(ctx context.Context, clusterID string, grantID string) (*client.DeleteClusterAccessGrantResponse, error) {
	m.deletedGrantIDs = append(m.deletedGrantIDs, grantID)
	return &client.DeleteClusterAccessGrantResponse{Deleted: true}, nil
}

func grantFixture(grantID string, email string, role string) client.ClusterAccessGrant {
	return client.ClusterAccessGrant{
		ID:              grantID,
		UserEmail:       &email,
		Scope:           "cluster",
		Role:            role,
		ReconcileStatus: "applied",
		CreatedAt:       "2026-06-01T12:00:00Z",
	}
}

func TestClusterAccessListCommand(t *testing.T) {
	mock := &clusterAccessMock{grants: []client.ClusterAccessGrant{
		grantFixture("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "member@example.com", "view"),
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "list", "--cluster", "my-cluster")
	})

	if !strings.Contains(stdoutOutput, "member@example.com") {
		t.Errorf("expected grant email in output, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Applied") {
		t.Errorf("expected reconcile status in output, got: %s", stdoutOutput)
	}
}

func TestClusterAccessListEmptyCommand(t *testing.T) {
	mock := &clusterAccessMock{grants: []client.ClusterAccessGrant{}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "list", "--cluster", "my-cluster")
	})

	if !strings.Contains(stdoutOutput, "No access grants found") {
		t.Errorf("expected empty-state message, got: %s", stdoutOutput)
	}
}

func TestClusterAccessGrantClusterScopeCommand(t *testing.T) {
	mock := &clusterAccessMock{}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "grant", "member@example.com", "--cluster", "my-cluster", "--role", "view", "--namespace", "")
	})

	if mock.createdRequest == nil {
		t.Fatal("expected a create grant request to be sent")
	}
	if mock.createdRequest.UserEmail != "member@example.com" {
		t.Errorf("expected user_email member@example.com, got: %s", mock.createdRequest.UserEmail)
	}
	if mock.createdRequest.Scope != "cluster" || mock.createdRequest.Namespace != nil {
		t.Errorf("expected cluster-wide scope, got scope=%s namespace=%v", mock.createdRequest.Scope, mock.createdRequest.Namespace)
	}
	if !strings.Contains(stdoutOutput, "Granted member@example.com") {
		t.Errorf("expected grant confirmation, got: %s", stdoutOutput)
	}
}

func TestClusterAccessGrantNamespaceScopeCommand(t *testing.T) {
	mock := &clusterAccessMock{}
	setMockClient(t, mock)

	captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "grant", "member@example.com", "--cluster", "my-cluster", "--role", "edit", "--namespace", "staging")
	})

	if mock.createdRequest == nil {
		t.Fatal("expected a create grant request to be sent")
	}
	if mock.createdRequest.Scope != "namespace" {
		t.Errorf("expected namespace scope, got: %s", mock.createdRequest.Scope)
	}
	if mock.createdRequest.Namespace == nil || *mock.createdRequest.Namespace != "staging" {
		t.Errorf("expected namespace staging, got: %v", mock.createdRequest.Namespace)
	}
	if mock.createdRequest.Role != "edit" {
		t.Errorf("expected role edit, got: %s", mock.createdRequest.Role)
	}
}

func TestClusterAccessRevokeByGrantIDCommand(t *testing.T) {
	mock := &clusterAccessMock{}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "revoke", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "--cluster", "my-cluster")
	})

	if len(mock.deletedGrantIDs) != 1 || mock.deletedGrantIDs[0] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("expected the grant ID to be deleted, got: %v", mock.deletedGrantIDs)
	}
	if !strings.Contains(stdoutOutput, "Revoked grant") {
		t.Errorf("expected revoke confirmation, got: %s", stdoutOutput)
	}
}

func TestClusterAccessRevokeByEmailCommand(t *testing.T) {
	mock := &clusterAccessMock{grants: []client.ClusterAccessGrant{
		grantFixture("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "member@example.com", "view"),
		grantFixture("bbbbbbbb-cccc-dddd-eeee-ffffffffffff", "member@example.com", "edit"),
		grantFixture("cccccccc-dddd-eeee-ffff-000000000000", "other@example.com", "view"),
	}}
	setMockClient(t, mock)

	captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "revoke", "member@example.com", "--cluster", "my-cluster")
	})

	if len(mock.deletedGrantIDs) != 2 {
		t.Fatalf("expected both grants for the email to be deleted, got: %v", mock.deletedGrantIDs)
	}
	for _, deletedGrantID := range mock.deletedGrantIDs {
		if deletedGrantID == "cccccccc-dddd-eeee-ffff-000000000000" {
			t.Errorf("deleted a grant belonging to another user: %v", mock.deletedGrantIDs)
		}
	}
}

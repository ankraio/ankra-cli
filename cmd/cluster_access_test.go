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
	elevateRequest  *client.ElevateClusterAccessRequest
	deletedGrantIDs []string
	policy          *client.ClusterAccessPolicy
}

func (m *clusterAccessMock) ElevateClusterAccess(ctx context.Context, clusterID string, request client.ElevateClusterAccessRequest) (*client.CreateClusterAccessGrantResponse, error) {
	m.elevateRequest = &request
	expires := "2026-10-09T20:00:00Z"
	reason := request.Reason
	return &client.CreateClusterAccessGrantResponse{Grant: client.ClusterAccessGrant{
		ID:              "dddddddd-bbbb-cccc-dddd-eeeeeeeeeeee",
		Scope:           request.Scope,
		Namespace:       request.Namespace,
		Role:            request.Role,
		ReconcileStatus: "pending",
		CreatedAt:       "2026-10-09T16:00:00Z",
		ExpiresAt:       &expires,
		Reason:          &reason,
	}}, nil
}

func (m *clusterAccessMock) GetClusterAccessPolicy(ctx context.Context) (*client.ClusterAccessPolicy, error) {
	return m.policy, nil
}

// resetAccessFlags clears the package-level flag variables the access
// commands share, so one test's --expires does not leak into the next.
func resetAccessFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		accessClusterFlag, accessRoleFlag, accessNamespaceFlag = "", "view", ""
		accessExpiresFlag, accessReasonFlag = "", ""
	}
	reset()
	t.Cleanup(reset)
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

func TestClusterAccessGrantSendsExpiryAndReason(t *testing.T) {
	resetAccessFlags(t)
	mock := &clusterAccessMock{}
	setMockClient(t, mock)

	captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "grant", "member@example.com", "--cluster", "my-cluster",
			"--role", "admin", "--expires", "4h", "--reason", "maintenance window")
	})
	request := mock.createdRequest
	if request == nil || request.ExpiresIn == nil || *request.ExpiresIn != "4h" || request.ExpiresAt != nil {
		t.Fatalf("expected expires_in 4h, got: %+v", request)
	}
	if request.Reason == nil || *request.Reason != "maintenance window" {
		t.Fatalf("expected the reason to be sent, got: %+v", request.Reason)
	}
}

func TestParseAccessExpiryTellsAnAbsoluteTimeFromAWindow(t *testing.T) {
	for _, testCase := range []struct {
		value                string
		wantIn, wantAt, fail bool
	}{
		{"", false, false, false},
		{"30m", true, false, false},
		{"1h30m", true, false, false},
		{"7d", true, false, false},
		{"2026-10-09T20:00:00Z", false, true, false},
		{"tomorrow", false, false, true},
		{"0d", false, false, true},
		{"-1h", false, false, true},
	} {
		expiresIn, expiresAt, err := parseAccessExpiry(testCase.value)
		if (err != nil) != testCase.fail || (expiresIn != nil) != testCase.wantIn || (expiresAt != nil) != testCase.wantAt {
			t.Fatalf("parseAccessExpiry(%q) = %v, %v, %v", testCase.value, expiresIn, expiresAt, err)
		}
	}
}

func TestClusterAccessElevateGrantsTheCallerWithinBounds(t *testing.T) {
	resetAccessFlags(t)
	mock := &clusterAccessMock{}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "elevate", "--cluster", "my-cluster",
			"--role", "edit", "--expires", "4h", "--reason", "incident 4711")
	})
	request := mock.elevateRequest
	if request == nil {
		t.Fatal("expected an elevate request to be sent")
	}
	if request.Role != "edit" || request.Scope != "cluster" || request.ExpiresIn == nil || *request.ExpiresIn != "4h" ||
		request.Reason != "incident 4711" {
		t.Fatalf("unexpected elevate request: %+v", request)
	}
	if !strings.Contains(stdoutOutput, `Elevated to "edit"`) || !strings.Contains(stdoutOutput, "ankra cluster access revoke dddddddd") {
		t.Errorf("expected the elevation and how to end it, got: %s", stdoutOutput)
	}
}

func TestClusterAccessElevateRefusesWithoutExpiryOrReason(t *testing.T) {
	for _, testCase := range []struct {
		name, want string
		args       []string
	}{
		{"no expiry", "--expires is required", []string{"--reason", "incident"}},
		{"no reason", "--reason is required", []string{"--expires", "4h"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetAccessFlags(t)
			mock := &clusterAccessMock{}
			setMockClient(t, mock)
			_, err := executeCommand(append([]string{"cluster", "access", "elevate", "--cluster", "my-cluster"}, testCase.args...)...)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("expected %q, got: %v", testCase.want, err)
			}
			if mock.elevateRequest != nil {
				t.Fatal("an elevation without its bounds must not reach the platform")
			}
		})
	}
}

func TestClusterAccessListShowsExpiryAndReason(t *testing.T) {
	resetAccessFlags(t)
	expires := "2026-10-09T20:00:00Z"
	reason := "incident 4711"
	elevated := grantFixture("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "member@example.com", "edit")
	elevated.ExpiresAt = &expires
	elevated.Reason = &reason
	mock := &clusterAccessMock{grants: []client.ClusterAccessGrant{
		elevated,
		grantFixture("bbbbbbbb-cccc-dddd-eeee-ffffffffffff", "other@example.com", "view"),
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "list", "--cluster", "my-cluster")
	})
	for _, want := range []string{"EXPIRES", "REASON", expires, reason, "standing"} {
		if !strings.Contains(stdoutOutput, want) {
			t.Errorf("expected %q in the grant table, got: %s", want, stdoutOutput)
		}
	}
}

func TestOrgAccessPolicyGetSaysWhatTheLimitsAre(t *testing.T) {
	fourHours := int64(4 * 3600)
	reasonFrom := "edit"
	mock := &clusterAccessMock{policy: &client.ClusterAccessPolicy{
		CreatorGrantRole: "view", MaxGrantRole: "edit", ElevatedMaxTTLSeconds: &fourHours,
		RequireReasonFromRole: &reasonFrom, IsConfigured: true,
	}}
	setMockClient(t, mock)

	var commandOutput string
	stdoutOutput := captureStdout(t, func() {
		commandOutput, _ = executeCommand("org", "access-policy", "get")
	})
	stdoutOutput += commandOutput
	for _, want := range []string{"Creator role:       view", "Ceiling:            edit", "Elevated lifetime:  4h0m0s", "Reason required:    edit and above"} {
		if !strings.Contains(stdoutOutput, want) {
			t.Errorf("expected %q, got: %s", want, stdoutOutput)
		}
	}

	mock.policy = &client.ClusterAccessPolicy{CreatorGrantRole: "cluster-admin", MaxGrantRole: "cluster-admin"}
	var unsetOutput string
	unset := captureStdout(t, func() {
		unsetOutput, _ = executeCommand("org", "access-policy", "get")
	})
	unset += unsetOutput
	if !strings.Contains(unset, "No policy is set") || !strings.Contains(unset, "no limit (break-glass: 4h)") {
		t.Errorf("expected the unset policy to say so, got: %s", unset)
	}
}

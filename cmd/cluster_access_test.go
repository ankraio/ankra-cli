package cmd

import (
	"context"
	"encoding/json"
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
	// organisation is the organisation the policy read names.
	organisation client.ClusterAccessPolicyOrganisation
	// refusal, when set, is what every access call answers instead.
	refusal error
}

func (m *clusterAccessMock) ElevateClusterAccess(ctx context.Context, clusterID string, request client.ElevateClusterAccessRequest) (*client.CreateClusterAccessGrantResponse, error) {
	m.elevateRequest = &request
	if m.refusal != nil {
		return nil, m.refusal
	}
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

func (m *clusterAccessMock) GetClusterAccessPolicy(ctx context.Context) (*client.ClusterAccessPolicyRead, error) {
	if m.refusal != nil {
		return nil, m.refusal
	}
	if m.policy == nil {
		return nil, nil
	}
	return &client.ClusterAccessPolicyRead{Policy: *m.policy, ClusterAccessPolicyOrganisation: m.organisation}, nil
}

// resetAccessFlags clears the package-level flag variables the access
// commands share, so one test's --expires does not leak into the next.
func resetAccessFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		accessClusterFlag, accessRoleFlag, accessNamespaceFlag = "", "view", ""
		accessExpiresFlag, accessReasonFlag = "", ""
		elevateClusterFlag, elevateRoleFlag, elevateNamespaceFlag = "", "edit", ""
		elevateExpiresFlag, elevateReasonFlag = "", ""
	}
	reset()
	t.Cleanup(reset)
}

func (m *clusterAccessMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "11111111-2222-3333-4444-555555555555", Name: name}, nil
}

func (m *clusterAccessMock) ListClusterAccessGrants(ctx context.Context, clusterID string) (*client.ListClusterAccessGrantsResponse, error) {
	if m.refusal != nil {
		return nil, m.refusal
	}
	return &client.ListClusterAccessGrantsResponse{Result: m.grants}, nil
}

func (m *clusterAccessMock) CreateClusterAccessGrant(ctx context.Context, clusterID string, request client.CreateClusterAccessGrantRequest) (*client.CreateClusterAccessGrantResponse, error) {
	m.createdRequest = &request
	if m.refusal != nil {
		return nil, m.refusal
	}
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
	if m.refusal != nil {
		return nil, m.refusal
	}
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

// TestGrantAndElevateRoleFlagsAreIndependent pins the defaults that matter:
// grant defaults to view and elevate to edit, and setting one never moves
// the other. Sharing one variable made elevate's later registration
// overwrite grant's default, so a plain 'grant <email>' asked for edit.
func TestGrantAndElevateRoleFlagsAreIndependent(t *testing.T) {
	grantRole := clusterAccessGrantCmd.Flags().Lookup("role")
	elevateRole := clusterAccessElevateCmd.Flags().Lookup("role")
	if grantRole.DefValue != "view" || elevateRole.DefValue != "edit" {
		t.Fatalf("defaults: grant %q, elevate %q", grantRole.DefValue, elevateRole.DefValue)
	}
	resetAccessFlags(t)
	if err := clusterAccessElevateCmd.Flags().Set("role", "admin"); err != nil {
		t.Fatal(err)
	}
	if grantRole.Value.String() != "view" {
		t.Fatalf("setting elevate's --role moved grant's to %q", grantRole.Value.String())
	}
	mock := &clusterAccessMock{}
	setMockClient(t, mock)
	resetAccessFlags(t)
	captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "grant", "member@example.com", "--cluster", "my-cluster")
	})
	if mock.createdRequest == nil || mock.createdRequest.Role != "view" {
		t.Fatalf("a grant without --role must ask for view, got: %+v", mock.createdRequest)
	}
}

// TestClusterAccessRefusalsExitWithTheirClass pins the scripting contract
// for the access commands: a policy refusal is fixed by changing the flags
// (exit 2), an RBAC refusal needs another role (exit 7), and only a rejected
// credential is exit 6. The readable sentence survives in every case.
func TestClusterAccessRefusalsExitWithTheirClass(t *testing.T) {
	invocations := map[string][]string{
		"grant":   {"cluster", "access", "grant", "member@example.com", "--cluster", "my-cluster", "--role", "admin"},
		"elevate": {"cluster", "access", "elevate", "--cluster", "my-cluster", "--expires", "4h", "--reason", "incident"},
		"revoke":  {"cluster", "access", "revoke", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "--cluster", "my-cluster"},
		"list":    {"cluster", "access", "list", "--cluster", "my-cluster"},
		"policy":  {"org", "access-policy", "get"},
	}
	for _, testCase := range []struct {
		name     string
		status   int
		body     string
		wantCode int
		wantText string
	}{
		{"ceiling", 403, `{"detail":"cluster_access_policy_violation","max_grant_role":"edit"}`, exitUsage, `the most access anyone can be given is "edit"`},
		{"expiry required", 403, `{"detail":"cluster_access_grant_expiry_required","max_lifetime_seconds":14400}`, exitUsage, "pass --expires"},
		{"lifetime exceeded", 403, `{"detail":"cluster_access_grant_lifetime_exceeded","max_lifetime_seconds":3600}`, exitUsage, "pass a shorter --expires"},
		{"reason required", 403, `{"detail":"cluster_access_grant_reason_required"}`, exitUsage, "pass --reason"},
		{"permission denied", 403, `{"detail":"permission_denied","permission":"kube_access.elevate"}`, exitForbidden, "this needs kube_access.elevate"},
		{"admin only", 403, `{"detail":"Only organisation admins can manage cluster access"}`, exitForbidden, "Only organisation admins"},
		{"credentials", 401, `{"detail":"Not authenticated"}`, exitAuth, "Not authenticated"},
	} {
		for command, args := range invocations {
			t.Run(testCase.name+"/"+command, func(t *testing.T) {
				resetAccessFlags(t)
				mock := &clusterAccessMock{refusal: client.ClusterAccessResponseError("op", testCase.status, []byte(testCase.body))}
				setMockClient(t, mock)
				var err error
				captureStdout(t, func() {
					_, err = executeCommand(args...)
				})
				if err == nil || !strings.Contains(err.Error(), testCase.wantText) {
					t.Fatalf("expected %q, got: %v", testCase.wantText, err)
				}
				if got := exitCodeFor(err); got != testCase.wantCode {
					t.Fatalf("exit code %d, want %d (%v)", got, testCase.wantCode, err)
				}
			})
		}
	}
}

func TestClusterAccessInvalidFlagsExitUsage(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{"elevate without expiry", []string{"cluster", "access", "elevate", "--cluster", "my-cluster", "--reason", "incident"}},
		{"elevate without reason", []string{"cluster", "access", "elevate", "--cluster", "my-cluster", "--expires", "4h"}},
		{"elevate bad expiry", []string{"cluster", "access", "elevate", "--cluster", "my-cluster", "--expires", "tomorrow", "--reason", "x"}},
		{"elevate bad role", []string{"cluster", "access", "elevate", "--cluster", "my-cluster", "--role", "root", "--expires", "4h", "--reason", "x"}},
		{"grant bad role", []string{"cluster", "access", "grant", "member@example.com", "--cluster", "my-cluster", "--role", "root"}},
		{"grant bad expiry", []string{"cluster", "access", "grant", "member@example.com", "--cluster", "my-cluster", "--expires", "soon"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetAccessFlags(t)
			mock := &clusterAccessMock{}
			setMockClient(t, mock)
			_, err := executeCommand(testCase.args...)
			if err == nil || exitCodeFor(err) != exitUsage {
				t.Fatalf("expected a usage error (exit %d), got exit %d: %v", exitUsage, exitCodeFor(err), err)
			}
			if mock.elevateRequest != nil || mock.createdRequest != nil {
				t.Fatal("an invalid invocation must not reach the platform")
			}
		})
	}
}

func TestClusterAccessListShowsSourceAndOverPolicy(t *testing.T) {
	resetAccessFlags(t)
	over, within := true, false
	role, manual := "role", "manual"
	fromRole := grantFixture("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "member@example.com", "admin")
	fromRole.OverPolicy, fromRole.Source = &over, &role
	given := grantFixture("bbbbbbbb-cccc-dddd-eeee-ffffffffffff", "other@example.com", "view")
	given.OverPolicy, given.Source = &within, &manual
	unevaluated := grantFixture("cccccccc-dddd-eeee-ffff-000000000000", "third@example.com", "view")
	mock := &clusterAccessMock{grants: []client.ClusterAccessGrant{fromRole, given, unevaluated}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "list", "--cluster", "my-cluster")
	})
	for _, want := range []string{"SOURCE", "OVER POLICY", "role", "manual", "unknown", "yes", "change the member's role"} {
		if !strings.Contains(stdoutOutput, want) {
			t.Errorf("expected %q in the grant table, got: %s", want, stdoutOutput)
		}
	}
	var thirdRow string
	for _, line := range strings.Split(stdoutOutput, "\n") {
		if strings.Contains(line, "third@example.com") {
			thirdRow = line
		}
	}
	if strings.Count(thirdRow, "unknown") != 2 || strings.Contains(thirdRow, " no ") {
		t.Errorf("an unevaluated grant must read unknown for both source and over policy, got: %q", thirdRow)
	}
}

func TestClusterAccessListJSONKeepsNullOverPolicyAndSource(t *testing.T) {
	resetAccessFlags(t)
	mock := &clusterAccessMock{grants: []client.ClusterAccessGrant{
		grantFixture("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "member@example.com", "view"),
	}}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = clusterAccessListCmd.Flags().Set("output", "") })

	var commandOutput string
	stdoutOutput := captureStdout(t, func() {
		commandOutput, _ = executeCommand("cluster", "access", "list", "--cluster", "my-cluster", "-o", "json")
	})
	stdoutOutput += commandOutput
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdoutOutput)), &decoded); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdoutOutput)
	}
	for _, key := range []string{"over_policy", "source"} {
		value, present := decoded[0][key]
		if !present || value != nil {
			t.Errorf("expected %s to be present and null, got %v (present %v)", key, value, present)
		}
	}
}

func TestClusterAccessElevateSaysWhenTheBindingLands(t *testing.T) {
	resetAccessFlags(t)
	mock := &clusterAccessMock{}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "elevate", "--cluster", "my-cluster",
			"--role", "edit", "--expires", "4h", "--reason", "incident 4711")
	})
	for _, want := range []string{"Status:", "Pending", "30 to 60 seconds", "403 Forbidden"} {
		if !strings.Contains(stdoutOutput, want) {
			t.Errorf("expected %q after an elevation, got: %s", want, stdoutOutput)
		}
	}
}

func TestOrgAccessPolicyGetStatesConfiguredAndYAMLKeys(t *testing.T) {
	mock := &clusterAccessMock{policy: &client.ClusterAccessPolicy{
		CreatorGrantRole: "view", MaxGrantRole: "edit", IsConfigured: true,
	}}
	setMockClient(t, mock)
	t.Cleanup(func() { _ = orgAccessPolicyGetCmd.Flags().Set("output", "") })

	var commandOutput string
	table := captureStdout(t, func() {
		commandOutput, _ = executeCommand("org", "access-policy", "get")
	})
	table += commandOutput
	if !strings.Contains(table, "Configured:         yes") {
		t.Errorf("expected the table to say the policy is configured, got: %s", table)
	}

	mock.policy = &client.ClusterAccessPolicy{CreatorGrantRole: "cluster-admin", MaxGrantRole: "cluster-admin"}
	unset := captureStdout(t, func() {
		commandOutput, _ = executeCommand("org", "access-policy", "get")
	})
	unset += commandOutput
	if !strings.Contains(unset, "Configured:         no") {
		t.Errorf("expected the table to say no policy is configured, got: %s", unset)
	}

	yamlOutput := captureStdout(t, func() {
		commandOutput, _ = executeCommand("org", "access-policy", "get", "-o", "yaml")
	})
	yamlOutput += commandOutput
	for _, want := range []string{"creator_grant_role: cluster-admin", "max_grant_role: cluster-admin", "is_configured: false", "elevated_max_ttl_seconds: null"} {
		if !strings.Contains(yamlOutput, want) {
			t.Errorf("expected %q in -o yaml, got: %s", want, yamlOutput)
		}
	}
	if strings.Contains(yamlOutput, "creatorgrantrole") || strings.Contains(yamlOutput, "isconfigured") {
		t.Errorf("-o yaml must use the API's key names, got: %s", yamlOutput)
	}
}

// TestClusterAccessRevokeOfAnUnknownGrantIDExitsNotFound pins the documented
// exit 3 for the grant-ID path: the platform's 404 ("Access grant not found")
// is classified by exitCodeFor, not by resolveGrantTargets.
func TestClusterAccessRevokeOfAnUnknownGrantIDExitsNotFound(t *testing.T) {
	resetAccessFlags(t)
	mock := &clusterAccessMock{refusal: client.ClusterAccessResponseError("revoke access grant failed", 404,
		[]byte(`{"detail":"Access grant not found"}`))}
	setMockClient(t, mock)
	var err error
	captureStdout(t, func() {
		_, err = executeCommand("cluster", "access", "revoke", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "--cluster", "my-cluster")
	})
	if err == nil || exitCodeFor(err) != exitNotFound || !strings.Contains(err.Error(), "Access grant not found") {
		t.Fatalf("expected exit %d with the platform's sentence, got exit %d: %v", exitNotFound, exitCodeFor(err), err)
	}
}

// TestClusterAccessElevateRetryHintOnlyWhilePending: a reconcile that
// already failed, or a cluster that is offline, must not be told to retry
// kubectl in 30 to 60 seconds.
func TestClusterAccessElevateRetryHintOnlyWhilePending(t *testing.T) {
	for _, testCase := range []struct{ status, want string }{
		{"failed", "could not be applied"},
		{"cluster_offline", "applied when it reconnects"},
	} {
		t.Run(testCase.status, func(t *testing.T) {
			resetAccessFlags(t)
			mock := &elevateStatusMock{status: testCase.status}
			setMockClient(t, mock)
			stdoutOutput := captureStdout(t, func() {
				_, _ = executeCommand("cluster", "access", "elevate", "--cluster", "my-cluster",
					"--expires", "4h", "--reason", "incident")
			})
			if !strings.Contains(stdoutOutput, testCase.want) || strings.Contains(stdoutOutput, "30 to 60 seconds") {
				t.Errorf("status %s: expected %q and no retry hint, got: %s", testCase.status, testCase.want, stdoutOutput)
			}
		})
	}
}

type elevateStatusMock struct {
	clusterAccessMock
	status string
}

func (m *elevateStatusMock) ElevateClusterAccess(ctx context.Context, clusterID string, request client.ElevateClusterAccessRequest) (*client.CreateClusterAccessGrantResponse, error) {
	created, err := m.clusterAccessMock.ElevateClusterAccess(ctx, clusterID, request)
	if err != nil {
		return nil, err
	}
	created.Grant.ReconcileStatus = m.status
	return created, nil
}

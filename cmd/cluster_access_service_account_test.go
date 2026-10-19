package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// roleGrantDetail is the platform's 409 detail (cluster#4761) for a revoke
// of a grant an organisation role carries.
const roleGrantDetail = "This access comes from the member's organisation role; change the role, or the role's Kubernetes access, to remove it."

const (
	serviceAccountUserID = "5a5a5a5a-0000-4000-8000-000000000001"
	personUserID         = "5a5a5a5a-0000-4000-8000-000000000002"
	rosterOrganisationID = "0e0e0e0e-0000-4000-8000-000000000000"
)

// orgRoster is a mixin answering the organisation's member list, so a test
// can say who the grantees and the caller are.
type orgRoster struct {
	members      []client.OrganisationUser
	rosterError  error
	rosterOrgIDs []string
}

func (r *orgRoster) OrganisationOverride() string { return rosterOrganisationID }

func (r *orgRoster) ListOrganisationUsers(orgID string) ([]client.OrganisationUser, error) {
	r.rosterOrgIDs = append(r.rosterOrgIDs, orgID)
	return r.members, r.rosterError
}

func serviceAccountMember(current bool) client.OrganisationUser {
	id, kind, name := serviceAccountUserID, "service_account", "ci-deployer"
	return client.OrganisationUser{ID: &id, ServicePrincipalKind: &kind, ServicePrincipalName: &name, UserCurrent: &current}
}

func personMember(current bool) client.OrganisationUser {
	id, email := personUserID, "member@example.com"
	return client.OrganisationUser{ID: &id, Email: &email, UserCurrent: &current}
}

// roleGrantRevokeMock refuses the revoke of the grants in roleGrants the way
// the platform does since cluster#4761, and revokes every other grant.
type roleGrantRevokeMock struct {
	clusterAccessMock
	orgRoster
	roleGrants map[string]bool
}

func (m *roleGrantRevokeMock) DeleteClusterAccessGrant(ctx context.Context, clusterID string, grantID string) (*client.DeleteClusterAccessGrantResponse, error) {
	m.deletedGrantIDs = append(m.deletedGrantIDs, grantID)
	if m.roleGrants[grantID] {
		return nil, client.ClusterAccessRevokeResponseError(409, []byte(`{"detail":"`+roleGrantDetail+`"}`))
	}
	return &client.DeleteClusterAccessGrantResponse{Deleted: true}, nil
}

func runAccessRevoke(t *testing.T, mock APIClient, target string) (string, error) {
	t.Helper()
	resetAccessFlags(t)
	setMockClient(t, mock)
	var err error
	stdoutOutput := captureStdout(t, func() {
		_, err = executeCommand("cluster", "access", "revoke", target, "--cluster", "my-cluster")
	})
	return stdoutOutput, err
}

// TestClusterAccessRevokeOfARoleGrantExitsUsageWithThePlatformSentence pins
// the 409 contract: a role grant is refused, the platform's sentence is the
// error, nothing claims it was revoked, and the exit code is 2.
func TestClusterAccessRevokeOfARoleGrantExitsUsageWithThePlatformSentence(t *testing.T) {
	grantID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	mock := &roleGrantRevokeMock{roleGrants: map[string]bool{grantID: true}}
	stdoutOutput, err := runAccessRevoke(t, mock, grantID)
	if err == nil {
		t.Fatal("a refused role grant revoke must fail")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Fatalf("exit code %d, want %d (%v)", got, exitUsage, err)
	}
	if !strings.Contains(err.Error(), "grant "+grantID+" was not revoked") || !strings.Contains(err.Error(), roleGrantDetail) {
		t.Fatalf("error must name the grant and carry the platform's sentence, got: %v", err)
	}
	if strings.Contains(stdoutOutput, "Revoked grant") {
		t.Fatalf("a refused grant must not be reported as revoked, got: %s", stdoutOutput)
	}
}

// TestClusterAccessRevokeByEmailRevokesTheRestAndNamesTheKeptRoleGrant: a
// member with a manual and a role grant loses the manual one; the role one
// is named and the command still exits 2, so the member's access is never
// read as gone.
func TestClusterAccessRevokeByEmailRevokesTheRestAndNamesTheKeptRoleGrant(t *testing.T) {
	role := "role"
	fromRole := grantFixture("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "member@example.com", "view")
	fromRole.Source = &role
	manual := grantFixture("bbbbbbbb-cccc-dddd-eeee-ffffffffffff", "member@example.com", "edit")
	mock := &roleGrantRevokeMock{roleGrants: map[string]bool{fromRole.ID: true}}
	mock.grants = []client.ClusterAccessGrant{fromRole, manual}

	stdoutOutput, err := runAccessRevoke(t, mock, "member@example.com")
	if len(mock.deletedGrantIDs) != 2 {
		t.Fatalf("both grants must be tried, the role grant's refusal must not stop the rest: %v", mock.deletedGrantIDs)
	}
	if !strings.Contains(stdoutOutput, "Revoked grant "+manual.ID) {
		t.Fatalf("the manual grant must be revoked, got: %s", stdoutOutput)
	}
	if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), fromRole.ID) {
		t.Fatalf("want exit %d naming the kept role grant, got exit %d: %v", exitUsage, exitCodeFor(err), err)
	}
	if strings.Contains(stdoutOutput, "re-creates it") {
		t.Fatalf("the old 'role sync re-creates it' claim must be gone, got: %s", stdoutOutput)
	}
}

// TestClusterAccessRevokeOtherFailuresStillStopTheLoop: only the role-grant
// refusal is collected; any other failure ends the command as before.
func TestClusterAccessRevokeOtherFailuresStillStopTheLoop(t *testing.T) {
	mock := &clusterAccessMock{
		grants: []client.ClusterAccessGrant{
			grantFixture("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "member@example.com", "view"),
			grantFixture("bbbbbbbb-cccc-dddd-eeee-ffffffffffff", "member@example.com", "edit"),
		},
	}
	// The listing must succeed, so the refusal is installed after it.
	listed := &revokeFailsMock{clusterAccessMock: mock}
	_, err := runAccessRevoke(t, listed, "member@example.com")
	if err == nil || exitCodeFor(err) != exitForbidden {
		t.Fatalf("an RBAC refusal must surface as exit %d, got %d: %v", exitForbidden, exitCodeFor(err), err)
	}
	if len(mock.deletedGrantIDs) != 1 {
		t.Fatalf("a non-role refusal must stop at the first grant, tried %v", mock.deletedGrantIDs)
	}
}

type revokeFailsMock struct {
	*clusterAccessMock
}

func (m *revokeFailsMock) DeleteClusterAccessGrant(ctx context.Context, clusterID string, grantID string) (*client.DeleteClusterAccessGrantResponse, error) {
	m.deletedGrantIDs = append(m.deletedGrantIDs, grantID)
	return nil, client.ClusterAccessRevokeResponseError(403, []byte(`{"detail":"Only organisation admins can manage cluster access"}`))
}

func TestExitCodeForARoleGrantRefusalIsUsage(t *testing.T) {
	refusal := client.ClusterAccessRevokeResponseError(409, []byte(`{"detail":"`+roleGrantDetail+`"}`))
	if got := exitCodeFor(refusal); got != exitUsage {
		t.Fatalf("exit code %d, want %d", got, exitUsage)
	}
	if got := exitCodeFor(errors.New("wrapped: " + refusal.Error())); got != exitError {
		t.Fatalf("only the typed refusal maps to usage, a bare string is %d", got)
	}
}

// TestClusterAccessHelpNoLongerSaysARevokeOfARoleGrantSucceeds pins the
// wording of every place v0.37.0 claimed the revoke works and the role sync
// undoes it.
func TestClusterAccessHelpNoLongerSaysARevokeOfARoleGrantSucceeds(t *testing.T) {
	for name, help := range map[string]string{
		"access": clusterAccessCmd.Long,
		"revoke": clusterAccessRevokeCmd.Long,
	} {
		if strings.Contains(help, "undone by the role sync") || strings.Contains(help, "re-creates it after a revoke") {
			t.Errorf("%s help still says a revoke of a role grant succeeds:\n%s", name, help)
		}
		if !strings.Contains(help, "cannot be revoked here") {
			t.Errorf("%s help must say a role grant cannot be revoked here:\n%s", name, help)
		}
	}
	if !strings.Contains(clusterAccessCmd.Long, "grant that comes from an organisation role") {
		t.Errorf("the exit code table must list the role-grant refusal under 2:\n%s", clusterAccessCmd.Long)
	}
}

func TestClusterAccessListFooterSaysARoleGrantCannotBeRevoked(t *testing.T) {
	resetAccessFlags(t)
	role := "role"
	fromRole := grantFixture("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "member@example.com", "view")
	fromRole.Source = &role
	setMockClient(t, &clusterAccessMock{grants: []client.ClusterAccessGrant{fromRole}})
	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "list", "--cluster", "my-cluster")
	})
	if strings.Contains(stdoutOutput, "undone by the role sync") || !strings.Contains(stdoutOutput, "cannot be revoked here") {
		t.Fatalf("list footer must say a role grant cannot be revoked here, got: %s", stdoutOutput)
	}
}

// rosterAccessMock is the access mock with a member list.
type rosterAccessMock struct {
	clusterAccessMock
	orgRoster
}

// TestClusterAccessListNamesAServiceAccountGrantee: a grant without an email
// whose grantee is a service account reads "service account <name>", and
// the roster is read in the grant's organisation.
func TestClusterAccessListNamesAServiceAccountGrantee(t *testing.T) {
	resetAccessFlags(t)
	grant := grantFixture("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "", "edit")
	grant.UserEmail = nil
	grant.AnkraUserID = strings.ToUpper(serviceAccountUserID)
	grant.OrganisationID = "0f0f0f0f-0000-4000-8000-000000000000"
	mock := &rosterAccessMock{}
	mock.grants = []client.ClusterAccessGrant{grant}
	mock.members = []client.OrganisationUser{serviceAccountMember(false), personMember(true)}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "list", "--cluster", "my-cluster")
	})
	if !strings.Contains(stdoutOutput, "service account ci-deployer") {
		t.Fatalf("expected the service account's name, got: %s", stdoutOutput)
	}
	if len(mock.rosterOrgIDs) != 1 || mock.rosterOrgIDs[0] != grant.OrganisationID {
		t.Fatalf("the roster must be read once, in the grant's organisation, got %v", mock.rosterOrgIDs)
	}
}

// TestClusterAccessListKeepsTheIDWhenTheRosterCannotTell: no roster (or no
// name in it) falls back to the id, and grants that all carry an email never
// read the roster.
func TestClusterAccessListKeepsTheIDWhenTheRosterCannotTell(t *testing.T) {
	resetAccessFlags(t)
	grant := grantFixture("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "", "edit")
	grant.UserEmail = nil
	grant.AnkraUserID = serviceAccountUserID
	mock := &rosterAccessMock{}
	mock.grants = []client.ClusterAccessGrant{grant}
	mock.rosterError = errors.New("forbidden")
	setMockClient(t, mock)
	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "list", "--cluster", "my-cluster")
	})
	if !strings.Contains(stdoutOutput, serviceAccountUserID) {
		t.Fatalf("expected the id when the roster cannot be read, got: %s", stdoutOutput)
	}

	withEmail := &rosterAccessMock{}
	withEmail.grants = []client.ClusterAccessGrant{grantFixture("bbbbbbbb-cccc-dddd-eeee-ffffffffffff", "member@example.com", "view")}
	setMockClient(t, withEmail)
	captureStdout(t, func() {
		_, _ = executeCommand("cluster", "access", "list", "--cluster", "my-cluster")
	})
	if len(withEmail.rosterOrgIDs) != 0 {
		t.Fatalf("grants that all carry an email must not read the roster, read %v", withEmail.rosterOrgIDs)
	}
}

type rosterSecurityAccessMock struct {
	securityAccessMock
	orgRoster
}

func TestSecurityAccessNamesAServiceAccountGrantee(t *testing.T) {
	posture := accessPostureWithFindings()
	posture.Findings[0].AnkraUserID = serviceAccountUserID
	mock := &rosterSecurityAccessMock{securityAccessMock: securityAccessMock{posture: posture}}
	mock.members = []client.OrganisationUser{serviceAccountMember(false)}
	output, executeError := runSecurityCommand(t, mock, "security", "access", "--cluster", securityTestClusterID)
	if executeError != nil {
		t.Fatalf("security access failed: %v", executeError)
	}
	if !strings.Contains(output, "grant 11111111-1111-1111-1111-111111111111 · service account ci-deployer · edit") {
		t.Fatalf("expected the service account's name, got:\n%s", output)
	}
	if strings.Contains(output, "user "+serviceAccountUserID) {
		t.Fatalf("a named service account must not read as a user id:\n%s", output)
	}
}

// kubeTokenRosterMock answers the caller's own roster row. The roster sits
// one level above baseMock (nested in the access mock), so its methods win.
type kubeTokenRosterMock struct {
	clusterAccessMock
	orgRoster
}

func TestKubeTokenDeniedHintFitsTheCaller(t *testing.T) {
	denied := client.NewUnexpectedResponseError(403, `kube token request failed: status 403, body: {"detail":"You do not have access to this cluster"}`)
	grant := "ankra cluster access grant <your-email> --cluster prod --role view"
	elevate := `ankra cluster access elevate --cluster prod --role view --expires 1h --reason "<why>"`
	for _, testCase := range []struct {
		name            string
		members         []client.OrganisationUser
		rosterError     error
		want, wantNotIn []string
	}{
		{"service account", []client.OrganisationUser{personMember(false), serviceAccountMember(true)}, nil,
			[]string{"This service account has no access grant", elevate}, []string{grant}},
		{"person", []client.OrganisationUser{personMember(true), serviceAccountMember(false)}, nil,
			[]string{"Ask an organisation admin", grant}, []string{elevate}},
		{"unknown", nil, errors.New("roster unavailable"),
			[]string{grant, elevate}, nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &kubeTokenRosterMock{}
			mock.members, mock.rosterError = testCase.members, testCase.rosterError
			setMockClient(t, mock)
			err := suggestAccessOnKubeTokenDenied(denied, "prod")
			for _, want := range testCase.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("hint lacks %q:\n%v", want, err)
				}
			}
			for _, unwanted := range testCase.wantNotIn {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("hint must not carry %q:\n%v", unwanted, err)
				}
			}
			if exitCodeFor(err) != exitAuth {
				t.Errorf("the hint must not change the exit code, got %d", exitCodeFor(err))
			}
		})
	}
}

// TestKubeconfigAddHelpSaysWhichCredentialTheExecPluginUses pins the
// automation note: the plugin runs kube-token, a saved login wins over
// ANKRA_API_TOKEN, and a service account must run where nothing is logged in.
func TestKubeconfigAddHelpSaysWhichCredentialTheExecPluginUses(t *testing.T) {
	for _, want := range []string{
		"ankra cluster kube-token",
		"ANKRA_API_TOKEN",
		"~/.ankra.yaml",
		"only when there is no saved login",
		"ankra cluster access elevate",
	} {
		if !strings.Contains(clusterKubeconfigAddCmd.Long, want) {
			t.Errorf("kubeconfig add help lacks %q:\n%s", want, clusterKubeconfigAddCmd.Long)
		}
	}
}

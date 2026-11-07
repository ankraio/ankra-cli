package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

// tokenRemovalMock records which revoke and delete routes a command took.
type tokenRemovalMock struct {
	baseMock
	personalRevoked, serviceRevoked []string
	personalDeleted, serviceDeleted []string
	personalError, serviceError     error
	serviceTokens                   []client.ServiceToken
	listError                       error
}

func (m *tokenRemovalMock) RevokeAPIToken(tokenID string) (*client.RevokeAPITokenResponse, error) {
	m.personalRevoked = append(m.personalRevoked, tokenID)
	if m.personalError != nil {
		return nil, m.personalError
	}
	return &client.RevokeAPITokenResponse{Success: true}, nil
}

func (m *tokenRemovalMock) DeleteAPIToken(tokenID string) (*client.DeleteAPITokenResponse, error) {
	m.personalDeleted = append(m.personalDeleted, tokenID)
	if m.personalError != nil {
		return nil, m.personalError
	}
	return &client.DeleteAPITokenResponse{Success: true}, nil
}

func (m *tokenRemovalMock) RevokeServiceToken(tokenID string) (*client.RevokeAPITokenResponse, error) {
	m.serviceRevoked = append(m.serviceRevoked, tokenID)
	if m.serviceError != nil {
		return nil, m.serviceError
	}
	return &client.RevokeAPITokenResponse{Success: true}, nil
}

func (m *tokenRemovalMock) DeleteServiceToken(tokenID string) (*client.DeleteAPITokenResponse, error) {
	m.serviceDeleted = append(m.serviceDeleted, tokenID)
	if m.serviceError != nil {
		return nil, m.serviceError
	}
	return &client.DeleteAPITokenResponse{Success: true}, nil
}

func (m *tokenRemovalMock) ListServiceTokens() ([]client.ServiceToken, error) {
	return m.serviceTokens, m.listError
}

func TestTokensRevokeServiceTakesTheOrganisationRoute(t *testing.T) {
	mock := &tokenRemovalMock{}
	stdout, _, err := runTokens(t, mock, "revoke", "svc-1", "--service")
	if err != nil {
		t.Fatalf("tokens revoke --service: %v", err)
	}
	if len(mock.serviceRevoked) != 1 || mock.serviceRevoked[0] != "svc-1" || len(mock.personalRevoked) != 0 {
		t.Fatalf("--service must revoke on the service route only: service %v personal %v",
			mock.serviceRevoked, mock.personalRevoked)
	}
	for _, want := range []string{"Service token revoked.", "ankra tokens delete svc-1 --service"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestTokensDeleteServiceTakesTheOrganisationRoute(t *testing.T) {
	mock := &tokenRemovalMock{serviceTokens: []client.ServiceToken{
		{ID: "svc-1", AccountID: serviceAccountUserID, AccountName: "ci-deployer", Revoked: true},
		{ID: "svc-2", AccountID: serviceAccountUserID, AccountName: "ci-deployer"},
	}}
	stdout, stderr, err := runTokens(t, mock, "delete", "svc-1", "--service")
	if err != nil {
		t.Fatalf("tokens delete --service: %v", err)
	}
	if len(mock.serviceDeleted) != 1 || mock.serviceDeleted[0] != "svc-1" || len(mock.personalDeleted) != 0 {
		t.Fatalf("--service must delete on the service route only: service %v personal %v",
			mock.serviceDeleted, mock.personalDeleted)
	}
	if !strings.Contains(stdout, "Service token deleted.") {
		t.Errorf("output lacks the confirmation:\n%s", stdout)
	}
	if strings.Contains(stderr, "ankra org remove") {
		t.Errorf("the account still has a token, so it must not be offered for removal: %q", stderr)
	}
}

func TestTokensDeleteServiceNamesTheAccountRemovalAfterItsLastToken(t *testing.T) {
	mock := &tokenRemovalMock{serviceTokens: []client.ServiceToken{
		{ID: "svc-1", AccountID: serviceAccountUserID, AccountName: "ci-deployer", Revoked: true},
		{ID: "other", AccountID: "another-account", AccountName: "nightly"},
	}}
	_, stderr, err := runTokens(t, mock, "delete", "svc-1", "--service")
	if err != nil {
		t.Fatalf("tokens delete --service: %v", err)
	}
	for _, want := range []string{`service account "ci-deployer"`, "ankra org remove " + serviceAccountUserID} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
}

func TestTokensDeleteServiceStillDeletesWhenTheListingFails(t *testing.T) {
	mock := &tokenRemovalMock{listError: client.NewUnexpectedResponseError(500, "boom")}
	stdout, stderr, err := runTokens(t, mock, "delete", "svc-1", "--service")
	if err != nil {
		t.Fatalf("tokens delete --service: %v", err)
	}
	if len(mock.serviceDeleted) != 1 || !strings.Contains(stdout, "Service token deleted.") {
		t.Fatalf("the listing is only for the hint: deleted %v, stdout %q", mock.serviceDeleted, stdout)
	}
	if strings.Contains(stderr, "ankra org remove") {
		t.Errorf("without the listing nothing is known about the account: %q", stderr)
	}
}

// TestTokensRevokeAndDeleteNameServiceWhenThePersonalRouteMisses pins the
// hint: the personal routes refuse a service token's id exactly like an
// unknown one, so the failure says how to act on a service token, and the
// exit code stays the platform's.
func TestTokensRevokeAndDeleteNameServiceWhenThePersonalRouteMisses(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		status   int
		message  string
		wantHint string
		wantCode int
	}{
		{"revoke 409", []string{"revoke", "svc-1"}, 409,
			`revoke failed: status 409, body: {"detail":"Failed to revoke user token"}`,
			"add --service: ankra tokens revoke svc-1 --service", exitError},
		{"revoke 404", []string{"revoke", "svc-1"}, 404, "revoke failed: status 404, body: not found",
			"add --service: ankra tokens revoke svc-1 --service", exitNotFound},
		{"delete 404", []string{"delete", "svc-1"}, 404,
			`delete failed: status 404, body: {"detail":"Failed to delete user token"}`,
			"add --service: ankra tokens delete svc-1 --service", exitNotFound},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &tokenRemovalMock{personalError: client.NewUnexpectedResponseError(testCase.status, testCase.message)}
			_, _, err := runTokens(t, mock, testCase.args...)
			if err == nil || !strings.Contains(err.Error(), testCase.wantHint) || !strings.Contains(err.Error(), testCase.message) {
				t.Fatalf("want the platform's answer and %q, got %v", testCase.wantHint, err)
			}
			if exitCodeFor(err) != testCase.wantCode {
				t.Errorf("exit code = %d, want %d", exitCodeFor(err), testCase.wantCode)
			}
			if len(mock.serviceRevoked)+len(mock.serviceDeleted) != 0 {
				t.Errorf("without --service the service routes must not be called")
			}
		})
	}
}

func TestTokensDeleteOfALivePersonalTokenDoesNotNameService(t *testing.T) {
	mock := &tokenRemovalMock{personalError: client.NewUnexpectedResponseError(409,
		`delete failed: status 409, body: {"detail":"Token must be revoked before deletion"}`)}
	_, _, err := runTokens(t, mock, "delete", "personal-1")
	if err == nil || !strings.Contains(err.Error(), "Token must be revoked before deletion") {
		t.Fatalf("want the platform's refusal, got %v", err)
	}
	if strings.Contains(err.Error(), "--service") {
		t.Errorf("a live personal token is not a service token: %v", err)
	}
}

func TestTokensRevokeServiceRefusalIsARoleProblem(t *testing.T) {
	for _, verb := range []string{"revoke", "delete"} {
		mock := &tokenRemovalMock{serviceError: &client.PermissionDeniedError{Detail: "You do not have permission to manage service tokens"}}
		_, _, err := runTokens(t, mock, verb, "svc-1", "--service")
		if err == nil || exitCodeFor(err) != exitForbidden {
			t.Fatalf("%s: want exit %d, got %d: %v", verb, exitForbidden, exitCodeFor(err), err)
		}
		if strings.Contains(err.Error(), "add --service") {
			t.Errorf("%s: a --service refusal must not suggest --service: %v", verb, err)
		}
	}
}

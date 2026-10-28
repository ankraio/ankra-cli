package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type serviceTokensMock struct {
	baseMock
	serviceTokens          []client.ServiceToken
	personalListed         int
	serviceListed          int
	createdService         *client.CreateServiceTokenRequest
	createdPersonal        int
	createdPersonalRequest *client.CreateAPITokenRequest
	createError            error
}

func (m *serviceTokensMock) ListAPITokens() ([]client.APIToken, error) {
	m.personalListed++
	return []client.APIToken{}, nil
}

func (m *serviceTokensMock) ListServiceTokens() ([]client.ServiceToken, error) {
	m.serviceListed++
	return m.serviceTokens, nil
}

func (m *serviceTokensMock) CreateAPIToken(request client.CreateAPITokenRequest) (*client.CreateAPITokenResponse, error) {
	m.createdPersonal++
	m.createdPersonalRequest = &request
	return &client.CreateAPITokenResponse{ID: "personal-id", Token: "ankra_personal", ExpiresAt: "2027-01-01T00:00:00Z"}, nil
}

func (m *serviceTokensMock) CreateServiceToken(request client.CreateServiceTokenRequest) (*client.CreateServiceTokenResponse, error) {
	m.createdService = &request
	if m.createError != nil {
		return nil, m.createError
	}
	return &client.CreateServiceTokenResponse{
		ID: "svc-token-id", Token: "ankra_service_secret", AccountID: serviceAccountUserID,
		AccountName: request.Name, Label: "github-actions", RoleSlug: request.RoleSlug,
		ExpiresAt: "2027-01-31T00:00:00Z", Type: "Bearer",
	}, nil
}

// runTokens runs a tokens command and returns stdout (human output and the
// command writer's -o output), stderr and the error.
func runTokens(t *testing.T, mock APIClient, args ...string) (string, string, error) {
	t.Helper()
	setMockClient(t, mock)
	t.Cleanup(func() { resetTreeFlags(t, tokensListCmd, tokensCreateCmd) })
	var commandOutput, stdout string
	var err error
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			commandOutput, err = executeCommand(append([]string{"tokens"}, args...)...)
		})
	})
	return stdout + commandOutput, stderr, err
}

func TestTokensListServiceListsTheOrganisationsServiceTokens(t *testing.T) {
	daysLeft := 3
	mock := &serviceTokensMock{serviceTokens: []client.ServiceToken{
		{ID: "svc-1", AccountID: serviceAccountUserID, AccountName: "ci-deployer", Label: "github-actions",
			RoleSlug: "member", ExpiresAt: "2026-10-20T00:00:00", DaysLeft: &daysLeft, CreatedAt: "2026-10-01T00:00:00"},
		{ID: "svc-2", AccountID: serviceAccountUserID, AccountName: "stood-down", Label: "old",
			RoleSlug: "", ExpiresAt: "2027-10-20T00:00:00", CreatedAt: "2026-10-01T00:00:00"},
	}}
	stdout, stderr, err := runTokens(t, mock, "list", "--service")
	if err != nil {
		t.Fatalf("tokens list --service: %v", err)
	}
	if mock.serviceListed != 1 || mock.personalListed != 0 {
		t.Fatalf("--service must list service tokens only: service %d personal %d", mock.serviceListed, mock.personalListed)
	}
	for _, want := range []string{"SERVICE ACCOUNT", "ci-deployer", "github-actions", "member", "Expiring", "none"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table lacks %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "ankra tokens rotate <id> --service") {
		t.Errorf("the expiry note must say how to renew a service token, got %q", stderr)
	}
}

func TestTokensListServiceJSONIsTheListing(t *testing.T) {
	mock := &serviceTokensMock{serviceTokens: []client.ServiceToken{
		{ID: "svc-1", AccountName: "ci-deployer", Label: "github-actions", RoleSlug: "member", ExpiresAt: "2027-10-20T00:00:00"},
	}}
	stdout, _, err := runTokens(t, mock, "list", "--service", "-o", "json")
	if err != nil {
		t.Fatalf("tokens list --service -o json: %v", err)
	}
	var decoded []map[string]any
	if decodeError := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &decoded); decodeError != nil {
		t.Fatalf("not JSON: %v\n%s", decodeError, stdout)
	}
	if len(decoded) != 1 || decoded[0]["account_name"] != "ci-deployer" {
		t.Fatalf("unexpected listing: %v", decoded)
	}
}

func TestTokensCreateServiceMintsForTheServiceAccount(t *testing.T) {
	mock := &serviceTokensMock{}
	stdout, _, err := runTokens(t, mock, "create", "ci-deployer", "--service", "--role", "member",
		"--label", "github-actions", "--expires", "2027-01-31T00:00:00Z")
	if err != nil {
		t.Fatalf("tokens create --service: %v", err)
	}
	if mock.createdPersonal != 0 || mock.createdService == nil {
		t.Fatalf("--service must mint a service token, personal %d service %v", mock.createdPersonal, mock.createdService)
	}
	got := *mock.createdService
	if got.Name != "ci-deployer" || got.RoleSlug != "member" || got.Label != "github-actions" ||
		got.ExpiresAt == nil || *got.ExpiresAt != "2027-01-31T00:00:00Z" {
		t.Fatalf("request = %+v", got)
	}
	for _, want := range []string{`service account "ci-deployer"`, "ankra_service_secret", "export ANKRA_API_TOKEN="} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestTokensCreateServiceFlagsAreChecked(t *testing.T) {
	for name, args := range map[string][]string{
		"service without role":  {"create", "ci-deployer", "--service"},
		"service with bad time": {"create", "ci-deployer", "--service", "--role", "member", "--expires", "2027-01-31"},
		"role without service":  {"create", "laptop", "--role", "member"},
		"label without service": {"create", "laptop", "--label", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := &serviceTokensMock{}
			_, _, err := runTokens(t, mock, args...)
			if err == nil || exitCodeFor(err) != exitUsage {
				t.Fatalf("want exit %d, got %d: %v", exitUsage, exitCodeFor(err), err)
			}
			if mock.createdService != nil || mock.createdPersonal != 0 {
				t.Fatalf("an invalid invocation must not mint anything")
			}
		})
	}
}

func TestTokensCreateServiceRefusalIsARoleProblem(t *testing.T) {
	mock := &serviceTokensMock{createError: &client.PermissionDeniedError{Detail: "You do not have permission to create service tokens"}}
	_, _, err := runTokens(t, mock, "create", "ci-deployer", "--service", "--role", "member")
	if err == nil || exitCodeFor(err) != exitForbidden {
		t.Fatalf("want exit %d, got %d: %v", exitForbidden, exitCodeFor(err), err)
	}
}

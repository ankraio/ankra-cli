package cmd

import (
	"context"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func resetOIDCTrustFlags(t *testing.T) {
	t.Helper()
	for _, command := range []*cobra.Command{orgOIDCTrustListCmd, orgOIDCTrustCreateCmd, orgOIDCTrustDeleteCmd} {
		command.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	}
}

type oidcTrustMock struct {
	baseMock
	created client.OIDCTrustPolicyRequest
	deleted string
}

func (m *oidcTrustMock) ListOIDCTrustPolicies(context.Context) ([]client.OIDCTrustPolicy, error) {
	ref := "refs/heads/main"
	return []client.OIDCTrustPolicy{{ID: "p1", Name: "deploy", RepositoryOwner: "acme", RepositoryName: "deploy",
		Ref: &ref, RoleSlug: "operator", TTLSeconds: 900}}, nil
}

func (m *oidcTrustMock) CreateOIDCTrustPolicy(_ context.Context, request client.OIDCTrustPolicyRequest) (*client.OIDCTrustPolicy, error) {
	m.created = request
	return &client.OIDCTrustPolicy{ID: "p2", Name: request.Name, RepositoryOwner: request.RepositoryOwner,
		RepositoryName: request.RepositoryName}, nil
}

func (m *oidcTrustMock) DeleteOIDCTrustPolicy(_ context.Context, id string) error {
	m.deleted = id
	return nil
}

func TestOrgOIDCTrustListCreateDelete(t *testing.T) {
	mock := &oidcTrustMock{}
	setMockClient(t, mock)
	resetOIDCTrustFlags(t)

	output, listError := executeCommand("org", "oidc-trust", "list")
	if listError != nil || !strings.Contains(output, "acme/deploy") || !strings.Contains(output, "refs/heads/main") {
		t.Fatalf("list: %v\n%s", listError, output)
	}

	output, createError := executeCommand("org", "oidc-trust", "create", "deploy-main",
		"--repository", "acme/deploy", "--repository-id", "42", "--repository-owner-id", "7",
		"--ref", "refs/heads/main", "--role", "operator", "--ttl", "10m")
	if createError != nil {
		t.Fatalf("create: %v\n%s", createError, output)
	}
	request := mock.created
	if request.Name != "deploy-main" || request.RepositoryOwner != "acme" || request.RepositoryName != "deploy" ||
		request.RepositoryID != 42 || request.RepositoryOwnerID != 7 || request.TTLSeconds != 600 ||
		request.Ref == nil || *request.Ref != "refs/heads/main" || request.Environment != nil || request.AllowPullRequest {
		t.Fatalf("create request: %+v", request)
	}

	if _, deleteError := executeCommand("org", "oidc-trust", "delete", "p1", "--yes"); deleteError != nil || mock.deleted != "p1" {
		t.Fatalf("delete: %v %q", deleteError, mock.deleted)
	}
}

func TestOrgOIDCTrustCreateRejectsABadRepository(t *testing.T) {
	setMockClient(t, &oidcTrustMock{})
	resetOIDCTrustFlags(t)
	_, createError := executeCommand("org", "oidc-trust", "create", "x", "--repository", "no-slash",
		"--repository-id", "1", "--repository-owner-id", "1", "--role", "viewer")
	if createError == nil || exitCodeFor(createError) != exitUsage {
		t.Fatalf("want a usage error, got %v", createError)
	}
}

func TestOrgOIDCTrustCreateRejectsZeroIdsAndAnOutOfRangeTTL(t *testing.T) {
	for name, arguments := range map[string][]string{
		"zero repository id":  {"--repository-id", "0", "--repository-owner-id", "1"},
		"zero owner id":       {"--repository-id", "1", "--repository-owner-id", "0"},
		"ttl under a minute":  {"--repository-id", "1", "--repository-owner-id", "1", "--ttl", "30s"},
		"ttl over an hour":    {"--repository-id", "1", "--repository-owner-id", "1", "--ttl", "2h"},
		"sub-second ttl part": {"--repository-id", "1", "--repository-owner-id", "1", "--ttl", "90.5s"},
	} {
		t.Run(name, func(t *testing.T) {
			setMockClient(t, &oidcTrustMock{})
			resetOIDCTrustFlags(t)
			command := append([]string{"org", "oidc-trust", "create", "x", "--repository", "acme/deploy",
				"--role", "viewer"}, arguments...)
			if _, createError := executeCommand(command...); createError == nil || exitCodeFor(createError) != exitUsage {
				t.Fatalf("want a usage error, got %v", createError)
			}
		})
	}
}

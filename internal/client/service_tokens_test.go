package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestListServiceTokensReadsTheOrganisationListing(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/org/organisation/service-tokens" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("missing bearer token")
		}
		_, _ = writer.Write([]byte(`{"tokens":[{"id":"t-1","account_id":"a-1","account_name":"ci-deployer",` +
			`"label":"github-actions","role_slug":"member","expires_at":"2027-01-01T00:00:00","days_left":76,` +
			`"created_at":"2026-10-01T00:00:00","revoked_at":null,"last_used_at":null,"revoked":false,"scopes":[]}]}`))
	})
	tokens, err := testClient.ListServiceTokens()
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].AccountName != "ci-deployer" || tokens[0].RoleSlug != "member" ||
		tokens[0].DaysLeft == nil || *tokens[0].DaysLeft != 76 {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestServiceTokenRefusalIsARoleDenial(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusForbidden, map[string]string{"detail": "You do not have permission to view service tokens"})
	})
	_, err := testClient.ListServiceTokens()
	var denied *PermissionDeniedError
	if !errors.As(err, &denied) || denied.Detail != "You do not have permission to view service tokens" {
		t.Fatalf("expected the platform's sentence as an RBAC denial, got %T %v", err, err)
	}
}

func TestCreateServiceTokenPostsTheAccountAndRole(t *testing.T) {
	var body map[string]any
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/organisation/service-tokens" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		jsonResponse(t, writer, http.StatusCreated, map[string]string{"id": "t-1", "token": "ankra_secret", "account_name": "ci-deployer"})
	})
	expires := "2027-01-31T00:00:00Z"
	created, err := testClient.CreateServiceToken(CreateServiceTokenRequest{Name: "ci-deployer", RoleSlug: "member", ExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	if created.Token != "ankra_secret" {
		t.Fatalf("created = %+v", created)
	}
	if body["name"] != "ci-deployer" || body["role_slug"] != "member" || body["expires_at"] != expires {
		t.Fatalf("body = %v", body)
	}
	if _, present := body["label"]; present {
		t.Fatalf("an empty label must be left to the platform's default, body = %v", body)
	}
}

// TestDeleteClusterAccessGrantRoleGrantRefusalIsTyped pins the 409 since
// cluster#4761: typed, the platform's sentence as the message, the status in
// the chain; a 404 stays a plain not-found.
func TestDeleteClusterAccessGrantRoleGrantRefusalIsTyped(t *testing.T) {
	const detail = "This access comes from the member's organisation role; change the role, or the role's Kubernetes access, to remove it."
	status := http.StatusConflict
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/api/v1/clusters/c-1/access/grants/g-1" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		jsonResponse(t, writer, status, map[string]string{"detail": detail})
	})
	_, err := testClient.DeleteClusterAccessGrant(context.Background(), "c-1", "g-1")
	var roleGrant *ClusterAccessRoleGrantError
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &roleGrant) || err.Error() != detail {
		t.Fatalf("expected a typed role-grant refusal carrying the platform's sentence, got %T %v", err, err)
	}
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusConflict {
		t.Fatalf("the 409 must stay in the chain, got %v", err)
	}

	status = http.StatusNotFound
	_, err = testClient.DeleteClusterAccessGrant(context.Background(), "c-1", "g-1")
	if errors.As(err, &roleGrant) {
		t.Fatalf("a 404 is not a role-grant refusal: %v", err)
	}

	// A 409 for another cause keeps the platform's words but is not read
	// as "change the member's role".
	err = revokeResponseError(http.StatusConflict, []byte(`{"detail":"The grant changed while it was being revoked"}`))
	if errors.As(err, &roleGrant) || err.Error() != "The grant changed while it was being revoked" {
		t.Fatalf("an unrelated 409 must stay a plain failure, got %T %v", err, err)
	}
}

func TestListOrganisationUsersKeepsServicePrincipalFields(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"users":[{"id":"u-1","email":null,"service_principal_kind":"service_account",` +
			`"service_principal_name":"ci-deployer","user_current":true}]}`))
	})
	users, err := testClient.ListOrganisationUsers("o-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].ServicePrincipalName == nil || *users[0].ServicePrincipalName != "ci-deployer" ||
		users[0].ServicePrincipalKind == nil || *users[0].ServicePrincipalKind != "service_account" {
		t.Fatalf("users = %+v", users)
	}
}

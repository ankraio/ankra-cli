package client

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// There is no /api/v1/credentials/aws listing: the AWS credential is the
// platform's generic aws credential, so the listing goes through the bearer
// credentials listing filtered by provider.
func TestListAwsCredentials_UsesTheProviderFilter(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.URL.Path != "/api/v1/org/credentials" {
			t.Errorf("path = %s, want /api/v1/org/credentials", request.URL.Path)
		}
		if provider := request.URL.Query().Get("provider"); provider != "aws" {
			t.Errorf("provider = %q, want aws", provider)
		}
		jsonResponse(t, responseWriter, http.StatusOK, []Credential{
			{ID: "cred-aws", Name: "aws-prod", Provider: "aws", Available: true},
		})
	})

	credentials, listError := testClient.ListAwsCredentials()
	if listError != nil {
		t.Fatalf("ListAwsCredentials: %v", listError)
	}
	if len(credentials) != 1 || credentials[0].ID != "cred-aws" || credentials[0].Provider != "aws" {
		t.Errorf("credentials = %+v, want the one aws credential", credentials)
	}
}

func TestGetAwsOnboarding(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.URL.Path != "/api/v1/credentials/aws/onboarding" {
			t.Errorf("path = %s, want /api/v1/credentials/aws/onboarding", request.URL.Path)
		}
		if scope := request.URL.Query().Get("scope"); scope != "self_managed" {
			t.Errorf("scope = %q, want self_managed", scope)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"configured":true,"scope":"self_managed","external_id":"ext-123","region":"us-east-1",` +
			`"launch_stack_url":"https://console.aws.amazon.com/cloudformation/home#/stacks/create/review?param_ExternalId=ext-123",` +
			`"trust_principal_arn":"arn:aws:iam::111122223333:root","template_url":"https://templates.example/self_managed.yaml"}`))
	})

	result, getError := testClient.GetAwsOnboarding("self_managed")
	if getError != nil {
		t.Fatalf("GetAwsOnboarding: %v", getError)
	}
	if !result.Configured || result.Scope != "self_managed" || result.ExternalID != "ext-123" || result.Region != "us-east-1" {
		t.Errorf("onboarding = %+v", result)
	}
	if result.LaunchStackURL == nil || result.TrustPrincipalARN == nil || *result.TrustPrincipalARN != "arn:aws:iam::111122223333:root" || result.TemplateURL == nil {
		t.Errorf("urls = %v/%v/%v, want all set", result.LaunchStackURL, result.TrustPrincipalARN, result.TemplateURL)
	}
}

// An unconfigured scope answers null URLs; they must decode as absent, not
// as empty strings a caller could mistake for a URL.
func TestGetAwsOnboarding_UnconfiguredScopeHasNullURLs(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.RawQuery != "" {
			t.Errorf("query = %q, want none when no scope is given", request.URL.RawQuery)
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"configured":false,"scope":"cost","external_id":"ext-456","region":"us-east-1","launch_stack_url":null,"trust_principal_arn":null,"template_url":null}`))
	})
	result, getError := testClient.GetAwsOnboarding("")
	if getError != nil {
		t.Fatalf("GetAwsOnboarding: %v", getError)
	}
	if result.Configured || result.LaunchStackURL != nil || result.TrustPrincipalARN != nil || result.TemplateURL != nil {
		t.Errorf("unconfigured onboarding = %+v, want nil URLs", result)
	}
}

func TestCreateAwsRoleCredential_PostsSnakeCaseBody(t *testing.T) {
	var received map[string]any
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/credentials/aws/role" {
			t.Errorf("path = %s, want /api/v1/credentials/aws/role", request.URL.Path)
		}
		body, _ := io.ReadAll(request.Body)
		if decodeError := json.Unmarshal(body, &received); decodeError != nil {
			t.Fatalf("body is not JSON: %v", decodeError)
		}
		jsonResponse(t, responseWriter, http.StatusCreated, AwsCredentialCreateResponse{
			ID: "cred-aws", Name: "aws-prod", Provider: "aws", OrganisationID: "org-1", Available: true,
		})
	})

	result, createError := testClient.CreateAwsRoleCredential(AwsRoleCredentialCreateRequest{
		Name: "aws-prod", RoleARN: "arn:aws:iam::123456789012:role/Ankra", ExternalID: "ext-123", Region: "eu-north-1", Scope: "self_managed",
	})
	if createError != nil {
		t.Fatalf("CreateAwsRoleCredential: %v", createError)
	}
	if result.ID != "cred-aws" || result.Provider != "aws" || !result.Available {
		t.Errorf("result = %+v", result)
	}
	for key, want := range map[string]any{
		"name": "aws-prod", "role_arn": "arn:aws:iam::123456789012:role/Ankra", "external_id": "ext-123", "region": "eu-north-1", "scope": "self_managed",
	} {
		if got := received[key]; got != want {
			t.Errorf("body[%q] = %v, want %v", key, got, want)
		}
	}
}

// Region and scope are omitted when blank so the server defaults (us-east-1,
// cost) apply rather than an empty string being refused.
func TestCreateAwsRoleCredential_OmitsBlankDefaults(t *testing.T) {
	var received map[string]any
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(body, &received)
		jsonResponse(t, responseWriter, http.StatusCreated, AwsCredentialCreateResponse{ID: "cred-aws"})
	})
	if _, createError := testClient.CreateAwsRoleCredential(AwsRoleCredentialCreateRequest{Name: "aws-cost", RoleARN: "arn", ExternalID: "ext"}); createError != nil {
		t.Fatalf("CreateAwsRoleCredential: %v", createError)
	}
	for _, absent := range []string{"region", "scope"} {
		if _, present := received[absent]; present {
			t.Errorf("body[%q] must be omitted when blank, got %v", absent, received[absent])
		}
	}
}

func TestCreateAwsKeysCredential_PostsSnakeCaseBody(t *testing.T) {
	var received map[string]any
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/credentials/aws/keys" {
			t.Errorf("path = %s, want /api/v1/credentials/aws/keys", request.URL.Path)
		}
		body, _ := io.ReadAll(request.Body)
		if decodeError := json.Unmarshal(body, &received); decodeError != nil {
			t.Fatalf("body is not JSON: %v", decodeError)
		}
		jsonResponse(t, responseWriter, http.StatusCreated, AwsCredentialCreateResponse{ID: "cred-aws", Name: "aws-keys", Provider: "aws"})
	})

	result, createError := testClient.CreateAwsKeysCredential(AwsKeysCredentialCreateRequest{
		Name: "aws-keys", AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "s3cr3t", Region: "eu-north-1",
	})
	if createError != nil {
		t.Fatalf("CreateAwsKeysCredential: %v", createError)
	}
	if result.ID != "cred-aws" || result.Name != "aws-keys" {
		t.Errorf("result = %+v", result)
	}
	for key, want := range map[string]any{
		"name": "aws-keys", "access_key_id": "AKIAEXAMPLE", "secret_access_key": "s3cr3t", "region": "eu-north-1",
	} {
		if got := received[key]; got != want {
			t.Errorf("body[%q] = %v, want %v", key, got, want)
		}
	}
}

// A 409 (name already taken) surfaces as an error carrying the detail.
func TestCreateAwsKeysCredential_SurfacesConflict(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		jsonResponse(t, responseWriter, http.StatusConflict, map[string]string{"detail": "A credential named aws-keys already exists."})
	})
	_, createError := testClient.CreateAwsKeysCredential(AwsKeysCredentialCreateRequest{Name: "aws-keys", AccessKeyID: "a", SecretAccessKey: "b"})
	if createError == nil {
		t.Fatal("a 409 must surface as an error")
	}
}

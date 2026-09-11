package client

import (
	"net/http"
	"testing"
)

// There is no /api/v1/credentials/aws route: the AWS credential is the
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

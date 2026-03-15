package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestListCredentials(t *testing.T) {
	tests := []struct {
		name     string
		provider *string
		handler  http.HandlerFunc
	}{
		{
			name:     "without provider",
			provider: nil,
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/org/credentials" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, []Credential{
					{ID: "cred1", Name: "cred1", Provider: "github"},
				})
			},
		},
		{
			name:     "with provider",
			provider: strPtr("github"),
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/org/credentials" || !strings.Contains(r.URL.RawQuery, "provider=github") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, []Credential{
					{ID: "cred1", Name: "cred1", Provider: "github"},
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.ListCredentials(tt.provider)
			if err != nil {
				t.Fatalf("ListCredentials() error = %v", err)
			}
			if len(got) != 1 || got[0].Provider != "github" {
				t.Errorf("ListCredentials() got = %v", got)
			}
		})
	}
}

func TestGetCredential(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/credentials/cred-123") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, CredentialDetail{
			ID:       "cred-123",
			Name:     "my-cred",
			Provider: "github",
		})
	})
	got, err := testClient.GetCredential("cred-123")
	if err != nil {
		t.Fatalf("GetCredential() error = %v", err)
	}
	if got.Name != "my-cred" {
		t.Errorf("GetCredential() got.Name = %v, want my-cred", got.Name)
	}
}

func TestValidateCredentialName(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "credential_name=my-cred") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		jsonResponse(t, w, http.StatusOK, CredentialValidationResult{Valid: true})
	})
	got, err := testClient.ValidateCredentialName("my-cred")
	if err != nil {
		t.Fatalf("ValidateCredentialName() error = %v", err)
	}
	if !got.Valid {
		t.Errorf("ValidateCredentialName() got.Valid = %v, want true", got.Valid)
	}
}

func TestDeleteCredential(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	got, err := testClient.DeleteCredential(context.Background(), "cred-123", "org-123")
	if err != nil {
		t.Fatalf("DeleteCredential() error = %v", err)
	}
	if !got.Success {
		t.Errorf("DeleteCredential() got.Success = %v, want true", got.Success)
	}
}

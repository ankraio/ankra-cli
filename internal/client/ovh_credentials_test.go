package client

import (
	"net/http"
	"testing"
)

func TestListOvhCredentials_Success(t *testing.T) {
	expectedCreds := []OvhCredentialListItem{{ID: "ovh-cred-1", Name: "ovh-prod", Provider: "ovh", Available: true}}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/credentials/ovh" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, expectedCreds)
	})
	result, err := testClient.ListOvhCredentials()
	if err != nil {
		t.Fatalf("ListOvhCredentials: %v", err)
	}
	if len(result) != 1 || result[0].ID != "ovh-cred-1" {
		t.Errorf("ListOvhCredentials() got = %v", result)
	}
}

func TestListOvhCredentials_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusUnauthorized, nil)
	})
	_, err := testClient.ListOvhCredentials()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCreateOvhCredential_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/credentials/ovh" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusCreated, CreateOvhCredentialResponse{Success: true})
	})
	req := CreateOvhCredentialRequest{Name: "ovh-cred", ApplicationKey: "key", ApplicationSecret: "secret", ConsumerKey: "consumer", ProjectID: "proj-123"}
	result, err := testClient.CreateOvhCredential(req)
	if err != nil {
		t.Fatalf("CreateOvhCredential: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestCreateOvhCredential_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, CreateOvhCredentialResponse{Success: false, Errors: []ResourceError{{Key: "api", Message: "invalid"}}})
	})
	_, err := testClient.CreateOvhCredential(CreateOvhCredentialRequest{Name: "bad"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListOvhSSHKeyCredentials_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/ovh/ssh-keys" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, []OvhCredentialListItem{{ID: "ovh-ssh-1", Name: "ovh-key", Provider: "ovh", Available: true}})
	})
	result, err := testClient.ListOvhSSHKeyCredentials()
	if err != nil {
		t.Fatalf("ListOvhSSHKeyCredentials: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
}

func TestCreateOvhSSHKeyCredential_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/ovh/ssh-key" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusCreated, CreateSSHKeyCredentialResponse{Success: true})
	})
	result, err := testClient.CreateOvhSSHKeyCredential(CreateSSHKeyCredentialRequest{Name: "ovh-ssh", Generate: true})
	if err != nil {
		t.Fatalf("CreateOvhSSHKeyCredential: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

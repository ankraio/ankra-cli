package client

import (
	"net/http"
	"testing"
)

func TestListUpcloudCredentials_Success(t *testing.T) {
	expectedCreds := []UpcloudCredentialListItem{{ID: "upcloud-cred-1", Name: "upcloud-prod", Provider: "upcloud", Available: true}}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/credentials/upcloud" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, expectedCreds)
	})
	result, err := testClient.ListUpcloudCredentials()
	if err != nil {
		t.Fatalf("ListUpcloudCredentials: %v", err)
	}
	if len(result) != 1 || result[0].ID != "upcloud-cred-1" {
		t.Errorf("ListUpcloudCredentials() got = %v", result)
	}
}

func TestListUpcloudCredentials_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusUnauthorized, nil)
	})
	_, err := testClient.ListUpcloudCredentials()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCreateUpcloudCredential_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/credentials/upcloud" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusCreated, CreateUpcloudCredentialResponse{Success: true})
	})
	result, err := testClient.CreateUpcloudCredential(CreateUpcloudCredentialRequest{Name: "upcloud-cred", APIToken: "secret-token"})
	if err != nil {
		t.Fatalf("CreateUpcloudCredential: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestCreateUpcloudCredential_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, CreateUpcloudCredentialResponse{Success: false, Errors: []ResourceError{{Key: "api_token", Message: "invalid"}}})
	})
	_, err := testClient.CreateUpcloudCredential(CreateUpcloudCredentialRequest{Name: "bad", APIToken: ""})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListUpcloudSSHKeyCredentials_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/upcloud/ssh-keys" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, []UpcloudCredentialListItem{{ID: "upcloud-ssh-1", Name: "upcloud-key", Provider: "upcloud", Available: true}})
	})
	result, err := testClient.ListUpcloudSSHKeyCredentials()
	if err != nil {
		t.Fatalf("ListUpcloudSSHKeyCredentials: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
}

func TestCreateUpcloudSSHKeyCredential_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/upcloud/ssh-key" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusCreated, CreateSSHKeyCredentialResponse{Success: true})
	})
	result, err := testClient.CreateUpcloudSSHKeyCredential(CreateSSHKeyCredentialRequest{Name: "upcloud-ssh", Generate: true})
	if err != nil {
		t.Fatalf("CreateUpcloudSSHKeyCredential: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

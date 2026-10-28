package client

import (
	"net/http"
	"testing"
)

func TestListHetznerCredentials_Success(t *testing.T) {
	expectedCreds := []HetznerCredentialListItem{
		{ID: "cred-1", Name: "prod-hetzner", Provider: "hetzner", Available: true},
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/credentials/hetzner" {
			t.Errorf("path = %s, want /api/v1/credentials/hetzner", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedCreds)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListHetznerCredentials()
	if err != nil {
		t.Fatalf("ListHetznerCredentials: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].ID != "cred-1" {
		t.Errorf("result[0].ID = %s, want cred-1", result[0].ID)
	}
}

func TestListHetznerCredentials_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusUnauthorized, nil)
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.ListHetznerCredentials()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCreateHetznerCredential_Success(t *testing.T) {
	expectedResponse := CreateHetznerCredentialResponse{Success: true}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/credentials/hetzner" {
			t.Errorf("path = %s, want /api/v1/credentials/hetzner", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	req := CreateHetznerCredentialRequest{
		Name:     "prod-cred",
		APIToken: "secret-token",
	}
	result, err := testClient.CreateHetznerCredential(req)
	if err != nil {
		t.Fatalf("CreateHetznerCredential: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestCreateHetznerCredential_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, CreateHetznerCredentialResponse{
			Success: false,
			Errors:  []ResourceError{{Key: "api_token", Message: "invalid"}},
		})
	}
	testClient := newTestClient(t, handler)
	req := CreateHetznerCredentialRequest{Name: "bad", APIToken: ""}
	_, err := testClient.CreateHetznerCredential(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListSSHKeyCredentials_Success(t *testing.T) {
	expectedCreds := []HetznerCredentialListItem{
		{ID: "ssh-1", Name: "default-key", Provider: "hetzner", Available: true},
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/hetzner/ssh-keys" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, expectedCreds)
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListSSHKeyCredentials()
	if err != nil {
		t.Fatalf("ListSSHKeyCredentials: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].Name != "default-key" {
		t.Errorf("result[0].Name = %s, want default-key", result[0].Name)
	}
}

func TestCreateSSHKeyCredential_Success(t *testing.T) {
	privateKey := "-----BEGIN OPENSSH PRIVATE KEY-----"
	expectedResponse := CreateSSHKeyCredentialResponse{
		Success:    true,
		PrivateKey: &privateKey,
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/hetzner/ssh-key" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusCreated, expectedResponse)
	}
	testClient := newTestClient(t, handler)
	req := CreateSSHKeyCredentialRequest{Name: "my-key", Generate: true}
	result, err := testClient.CreateSSHKeyCredential(req)
	if err != nil {
		t.Fatalf("CreateSSHKeyCredential: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
	if result.PrivateKey == nil || *result.PrivateKey != privateKey {
		t.Error("PrivateKey not set correctly")
	}
}

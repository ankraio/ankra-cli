package client

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestListProxmoxCredentials_Success(t *testing.T) {
	expectedCredentials := []ProxmoxCredentialListItem{{ID: "proxmox-cred-1", Name: "proxmox-lab", Provider: "proxmox", Available: true}}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/credentials/proxmox" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, expectedCredentials)
	})
	result, listError := testClient.ListProxmoxCredentials()
	if listError != nil {
		t.Fatalf("ListProxmoxCredentials: %v", listError)
	}
	if len(result) != 1 || result[0].ID != "proxmox-cred-1" {
		t.Errorf("ListProxmoxCredentials() got = %v", result)
	}
}

func TestListProxmoxCredentials_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusUnauthorized, nil)
	})
	_, listError := testClient.ListProxmoxCredentials()
	if listError == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCreateProxmoxCredential_Success(t *testing.T) {
	var receivedBody map[string]any
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/credentials/proxmox" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rawBody, _ := io.ReadAll(r.Body)
		if decodeError := json.Unmarshal(rawBody, &receivedBody); decodeError != nil {
			t.Fatalf("decode request body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusCreated, CreateProxmoxCredentialResponse{Success: true})
	})
	result, createError := testClient.CreateProxmoxCredential(CreateProxmoxCredentialRequest{
		Name:        "proxmox-cred",
		APIURL:      "https://proxmox.example:8006",
		TokenID:     "root@pam!ankra",
		TokenSecret: "secret-token",
		TLSInsecure: true,
		Jumphost:    &CredentialJumphost{Host: "10.0.0.1", Port: 2222, Username: "ops", PrivateKey: "PRIVATE KEY"},
	})
	if createError != nil {
		t.Fatalf("CreateProxmoxCredential: %v", createError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
	if receivedBody["api_url"] != "https://proxmox.example:8006" ||
		receivedBody["token_id"] != "root@pam!ankra" ||
		receivedBody["token_secret"] != "secret-token" ||
		receivedBody["tls_insecure"] != true {
		t.Errorf("request body = %v", receivedBody)
	}
	jumphost, isMap := receivedBody["jumphost"].(map[string]any)
	if !isMap || jumphost["host"] != "10.0.0.1" || jumphost["port"] != float64(2222) ||
		jumphost["username"] != "ops" || jumphost["private_key"] != "PRIVATE KEY" {
		t.Errorf("jumphost body = %v", receivedBody["jumphost"])
	}
}

func TestCreateProxmoxCredential_OmitsAbsentJumphost(t *testing.T) {
	var receivedBody map[string]any
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := io.ReadAll(r.Body)
		if decodeError := json.Unmarshal(rawBody, &receivedBody); decodeError != nil {
			t.Fatalf("decode request body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusCreated, CreateProxmoxCredentialResponse{Success: true})
	})
	_, createError := testClient.CreateProxmoxCredential(CreateProxmoxCredentialRequest{
		Name:        "proxmox-cred",
		APIURL:      "https://proxmox.example:8006",
		TokenID:     "root@pam!ankra",
		TokenSecret: "secret-token",
	})
	if createError != nil {
		t.Fatalf("CreateProxmoxCredential: %v", createError)
	}
	if _, present := receivedBody["jumphost"]; present {
		t.Errorf("jumphost should be omitted when nil, body = %v", receivedBody)
	}
}

func TestCreateProxmoxCredential_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, CreateProxmoxCredentialResponse{Success: false, Errors: []ResourceError{{Key: "token_secret", Message: "invalid"}}})
	})
	_, createError := testClient.CreateProxmoxCredential(CreateProxmoxCredentialRequest{Name: "bad"})
	if createError == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListProxmoxSSHKeyCredentials_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/proxmox/ssh-keys" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, []ProxmoxCredentialListItem{{ID: "proxmox-ssh-1", Name: "proxmox-key", Provider: "ssh-key", Available: true}})
	})
	result, listError := testClient.ListProxmoxSSHKeyCredentials()
	if listError != nil {
		t.Fatalf("ListProxmoxSSHKeyCredentials: %v", listError)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
}

func TestCreateProxmoxSSHKeyCredential_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/proxmox/ssh-key" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusCreated, CreateSSHKeyCredentialResponse{Success: true})
	})
	result, createError := testClient.CreateProxmoxSSHKeyCredential(CreateSSHKeyCredentialRequest{Name: "proxmox-ssh", Generate: true})
	if createError != nil {
		t.Fatalf("CreateProxmoxSSHKeyCredential: %v", createError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

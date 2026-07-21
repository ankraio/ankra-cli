package client

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestListMorpheusCredentials_Success(t *testing.T) {
	expectedCredentials := []MorpheusCredentialListItem{{ID: "morpheus-cred-1", Name: "morpheus-prod", Provider: "morpheus", Available: true}}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/credentials/morpheus" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, expectedCredentials)
	})
	result, listError := testClient.ListMorpheusCredentials()
	if listError != nil {
		t.Fatalf("ListMorpheusCredentials: %v", listError)
	}
	if len(result) != 1 || result[0].ID != "morpheus-cred-1" {
		t.Errorf("ListMorpheusCredentials() got = %v", result)
	}
}

func TestListMorpheusCredentials_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusUnauthorized, nil)
	})
	_, listError := testClient.ListMorpheusCredentials()
	if listError == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCreateMorpheusCredential_Success(t *testing.T) {
	var receivedBody map[string]any
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/credentials/morpheus" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rawBody, _ := io.ReadAll(r.Body)
		if decodeError := json.Unmarshal(rawBody, &receivedBody); decodeError != nil {
			t.Fatalf("decode request body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusCreated, CreateMorpheusCredentialResponse{Success: true})
	})
	result, createError := testClient.CreateMorpheusCredential(CreateMorpheusCredentialRequest{
		Name:        "morpheus-cred",
		APIURL:      "https://morpheus.example",
		AccessToken: "secret-token",
		TLSInsecure: true,
		Jumphost:    &CredentialJumphost{Host: "10.0.0.1", Port: 2222, Username: "ops", PrivateKey: "PRIVATE KEY"},
	})
	if createError != nil {
		t.Fatalf("CreateMorpheusCredential: %v", createError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
	if receivedBody["api_url"] != "https://morpheus.example" ||
		receivedBody["access_token"] != "secret-token" ||
		receivedBody["tls_insecure"] != true {
		t.Errorf("request body = %v", receivedBody)
	}
	jumphost, isMap := receivedBody["jumphost"].(map[string]any)
	if !isMap || jumphost["host"] != "10.0.0.1" || jumphost["port"] != float64(2222) ||
		jumphost["username"] != "ops" || jumphost["private_key"] != "PRIVATE KEY" {
		t.Errorf("jumphost body = %v", receivedBody["jumphost"])
	}
}

func TestCreateMorpheusCredential_OmitsAbsentJumphost(t *testing.T) {
	var receivedBody map[string]any
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := io.ReadAll(r.Body)
		if decodeError := json.Unmarshal(rawBody, &receivedBody); decodeError != nil {
			t.Fatalf("decode request body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusCreated, CreateMorpheusCredentialResponse{Success: true})
	})
	_, createError := testClient.CreateMorpheusCredential(CreateMorpheusCredentialRequest{
		Name:        "morpheus-cred",
		APIURL:      "https://morpheus.example",
		AccessToken: "secret-token",
	})
	if createError != nil {
		t.Fatalf("CreateMorpheusCredential: %v", createError)
	}
	if _, present := receivedBody["jumphost"]; present {
		t.Errorf("jumphost should be omitted when nil, body = %v", receivedBody)
	}
}

func TestCreateMorpheusCredential_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, CreateMorpheusCredentialResponse{Success: false, Errors: []ResourceError{{Key: "access_token", Message: "invalid"}}})
	})
	_, createError := testClient.CreateMorpheusCredential(CreateMorpheusCredentialRequest{Name: "bad"})
	if createError == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListMorpheusSSHKeyCredentials_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/morpheus/ssh-keys" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, []MorpheusCredentialListItem{{ID: "morpheus-ssh-1", Name: "morpheus-key", Provider: "ssh-key", Available: true}})
	})
	result, listError := testClient.ListMorpheusSSHKeyCredentials()
	if listError != nil {
		t.Fatalf("ListMorpheusSSHKeyCredentials: %v", listError)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
}

func TestCreateMorpheusSSHKeyCredential_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/morpheus/ssh-key" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusCreated, CreateSSHKeyCredentialResponse{Success: true})
	})
	result, createError := testClient.CreateMorpheusSSHKeyCredential(CreateSSHKeyCredentialRequest{Name: "morpheus-ssh", Generate: true})
	if createError != nil {
		t.Fatalf("CreateMorpheusSSHKeyCredential: %v", createError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

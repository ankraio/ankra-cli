package client

import (
	"net/http"
	"testing"
)

func TestListDigitaloceanCredentials_Success(t *testing.T) {
	expectedCreds := []DigitaloceanCredentialListItem{{ID: "digitalocean-cred-1", Name: "digitalocean-prod", Provider: "digitalocean", Available: true}}
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/credentials/digitalocean" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, expectedCreds)
	})
	result, err := testClient.ListDigitaloceanCredentials()
	if err != nil {
		t.Fatalf("ListDigitaloceanCredentials: %v", err)
	}
	if len(result) != 1 || result[0].ID != "digitalocean-cred-1" {
		t.Errorf("ListDigitaloceanCredentials() got = %v", result)
	}
}

func TestListDigitaloceanCredentials_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusUnauthorized, nil)
	})
	_, err := testClient.ListDigitaloceanCredentials()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCreateDigitaloceanCredential_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/credentials/digitalocean" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusCreated, CreateDigitaloceanCredentialResponse{Success: true})
	})
	result, err := testClient.CreateDigitaloceanCredential(CreateDigitaloceanCredentialRequest{Name: "digitalocean-cred", APIToken: "secret-token"})
	if err != nil {
		t.Fatalf("CreateDigitaloceanCredential: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestCreateDigitaloceanCredential_Error(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, CreateDigitaloceanCredentialResponse{Success: false, Errors: []ResourceError{{Key: "api_token", Message: "invalid"}}})
	})
	_, err := testClient.CreateDigitaloceanCredential(CreateDigitaloceanCredentialRequest{Name: "bad", APIToken: ""})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListDigitaloceanSSHKeyCredentials_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/digitalocean/ssh-keys" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, []DigitaloceanCredentialListItem{{ID: "digitalocean-ssh-1", Name: "digitalocean-key", Provider: "digitalocean", Available: true}})
	})
	result, err := testClient.ListDigitaloceanSSHKeyCredentials()
	if err != nil {
		t.Fatalf("ListDigitaloceanSSHKeyCredentials: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
}

func TestCreateDigitaloceanSSHKeyCredential_Success(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/digitalocean/ssh-key" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusCreated, CreateSSHKeyCredentialResponse{Success: true})
	})
	result, err := testClient.CreateDigitaloceanSSHKeyCredential(CreateSSHKeyCredentialRequest{Name: "digitalocean-ssh", Generate: true})
	if err != nil {
		t.Fatalf("CreateDigitaloceanSSHKeyCredential: %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

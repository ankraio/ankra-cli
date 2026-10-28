package client

import (
	"net/http"
	"testing"
)

func TestCreateAnkraCloudCredentialSendsTokenAndEndpoint(t *testing.T) {
	var receivedBody map[string]any
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/credentials/ankracloud" {
			t.Errorf("request = %s %s, want POST /api/v1/credentials/ankracloud", request.Method, request.URL.Path)
		}
		receivedBody = decodeAnkraCloudBody(t, request)
		jsonResponse(t, responseWriter, http.StatusCreated, CreateAnkraCloudCredentialResponse{Success: true})
	})

	result, createError := testClient.CreateAnkraCloudCredential(CreateAnkraCloudCredentialRequest{
		Name:     "ankra-cloud",
		APIToken: "act_secret",
		Endpoint: "https://cloud.ankra.dev",
	})
	if createError != nil {
		t.Fatalf("CreateAnkraCloudCredential: %v", createError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
	if receivedBody["name"] != "ankra-cloud" || receivedBody["api_token"] != "act_secret" || receivedBody["endpoint"] != "https://cloud.ankra.dev" {
		t.Errorf("body = %v, want name, api_token and endpoint", receivedBody)
	}
}

func TestCreateAnkraCloudCredentialOmitsDefaultEndpoint(t *testing.T) {
	var receivedBody map[string]any
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		receivedBody = decodeAnkraCloudBody(t, request)
		jsonResponse(t, responseWriter, http.StatusOK, CreateAnkraCloudCredentialResponse{Success: true})
	})

	if _, createError := testClient.CreateAnkraCloudCredential(CreateAnkraCloudCredentialRequest{Name: "ankra-cloud", APIToken: "act_secret"}); createError != nil {
		t.Fatalf("CreateAnkraCloudCredential: %v", createError)
	}
	if _, present := receivedBody["endpoint"]; present {
		t.Errorf("body carries endpoint = %v, want it omitted", receivedBody["endpoint"])
	}
}

func TestCreateAnkraCloudCredentialRedactsTheTokenOnFailure(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		jsonResponse(t, responseWriter, http.StatusBadRequest, map[string]string{"detail": "Ankra Cloud rejected the token"})
	})

	_, createError := testClient.CreateAnkraCloudCredential(CreateAnkraCloudCredentialRequest{Name: "ankra-cloud", APIToken: "act_secret"})
	if createError == nil {
		t.Fatal("CreateAnkraCloudCredential succeeded on a 400, want an error")
	}
}

func TestListAnkraCloudCredentials(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/credentials/ankracloud" {
			t.Errorf("request = %s %s, want GET /api/v1/credentials/ankracloud", request.Method, request.URL.Path)
		}
		jsonResponse(t, responseWriter, http.StatusOK, []Credential{{ID: "credential-1", Name: "ankra-cloud", Provider: "ankracloud", Available: true}})
	})

	credentials, listError := testClient.ListAnkraCloudCredentials()
	if listError != nil {
		t.Fatalf("ListAnkraCloudCredentials: %v", listError)
	}
	if len(credentials) != 1 || credentials[0].ID != "credential-1" {
		t.Errorf("credentials = %+v, want credential-1", credentials)
	}
}

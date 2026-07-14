package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestCreateApplication(t *testing.T) {
	applicationID := "application-id"
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/org/applications" {
			t.Errorf("path = %s, want /api/v1/org/applications", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q, want application/json", request.Header.Get("Content-Type"))
		}

		var applicationRequest map[string]string
		if decodeError := json.NewDecoder(request.Body).Decode(&applicationRequest); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		if len(applicationRequest) != 5 {
			t.Errorf("request keys = %v, want exactly five application fields", applicationRequest)
		}
		if applicationRequest["name"] != "payments" {
			t.Errorf("name = %q, want payments", applicationRequest["name"])
		}
		if applicationRequest["app_repo_credential_name"] != "github-acme" {
			t.Errorf("credential = %q, want github-acme", applicationRequest["app_repo_credential_name"])
		}
		if applicationRequest["app_repo_owner"] != "acme" ||
			applicationRequest["app_repo_name"] != "payments" {
			t.Errorf(
				"repository = %s/%s, want acme/payments",
				applicationRequest["app_repo_owner"],
				applicationRequest["app_repo_name"],
			)
		}
		if applicationRequest["app_repo_branch"] != "main" {
			t.Errorf("branch = %q, want main", applicationRequest["app_repo_branch"])
		}

		jsonResponse(t, writer, http.StatusOK, CreateApplicationResponse{
			ID:     &applicationID,
			Errors: []ApplicationResourceError{},
		})
	})

	applicationResponse, createError := testClient.CreateApplication(context.Background(), CreateApplicationRequest{
		Name:                     "payments",
		RepositoryCredentialName: "github-acme",
		RepositoryOwner:          "acme",
		RepositoryName:           "payments",
		RepositoryBranch:         "main",
	})
	if createError != nil {
		t.Fatalf("CreateApplication() error = %v", createError)
	}
	if applicationResponse.ID == nil || *applicationResponse.ID != applicationID {
		t.Errorf("CreateApplication() ID = %v, want %s", applicationResponse.ID, applicationID)
	}
}

func TestCreateApplicationReturnsPermissionDenied(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusForbidden, map[string]string{
			"detail":     "permission_denied",
			"permission": "applications.write",
		})
	})

	_, createError := testClient.CreateApplication(context.Background(), CreateApplicationRequest{})
	var permissionDenied *PermissionDeniedError
	if !errors.As(createError, &permissionDenied) {
		t.Fatalf("CreateApplication() error = %v, want PermissionDeniedError", createError)
	}
	if permissionDenied.Permission != "applications.write" {
		t.Errorf("permission = %q, want applications.write", permissionDenied.Permission)
	}
}

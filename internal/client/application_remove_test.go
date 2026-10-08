package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// The removal posts exactly the cluster and namespace to the bearer route and
// reads the 202 into the accepted removal.
func TestRemoveApplicationDeploymentPostsTheDeploymentAndReadsTheAcceptance(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/api/v1/org/applications/app-1/deployments/remove" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Fatalf("missing bearer token")
		}
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		if len(body) != 2 || body["cluster_id"] != "c-1" || body["namespace"] != "shop" {
			t.Fatalf("body = %v", body)
		}
		jsonResponse(t, writer, http.StatusAccepted, map[string]any{
			"cluster_id": "c-1", "namespace": "shop", "installation_id": "i-1",
			"operation_id": "o-1", "status": "removing",
		})
	})
	removed, removeError := testClient.RemoveApplicationDeployment(context.Background(), "app-1",
		RemoveApplicationDeploymentRequest{ClusterID: "c-1", Namespace: "shop"})
	if removeError != nil {
		t.Fatalf("RemoveApplicationDeployment error = %v", removeError)
	}
	if *removed != (RemoveApplicationDeploymentResult{ClusterID: "c-1", Namespace: "shop", InstallationID: "i-1",
		OperationID: "o-1", Status: "removing"}) {
		t.Fatalf("removed = %+v", removed)
	}
}

// A refusal is the platform's own sentence, with its status kept for the exit code.
func TestRemoveApplicationDeploymentPassesOnThePlatformsRefusal(t *testing.T) {
	for _, refusal := range []struct {
		status int
		detail string
	}{
		{http.StatusConflict, "This application's deployment on that cluster is already being removed."},
		{http.StatusUnprocessableEntity, "This application is not deployed to that cluster and namespace, so there is nothing to remove."},
	} {
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			jsonResponse(t, writer, refusal.status, map[string]any{"detail": refusal.detail})
		})
		_, removeError := testClient.RemoveApplicationDeployment(context.Background(), "app-1",
			RemoveApplicationDeploymentRequest{ClusterID: "c-1", Namespace: "shop"})
		var unexpected *UnexpectedResponseError
		if !errors.As(removeError, &unexpected) || unexpected.StatusCode != refusal.status ||
			removeError.Error() != refusal.detail {
			t.Fatalf("status %d: error = %v", refusal.status, removeError)
		}
	}
}

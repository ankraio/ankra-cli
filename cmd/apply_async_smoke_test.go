package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ankra/internal/client"
)

func TestApplyClusterAsyncSubmitAgainstMockAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("wait") != "false" {
			responseWriter.WriteHeader(http.StatusBadRequest)
			return
		}
		responseWriter.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(responseWriter).Encode(map[string]string{"status": "accepted"})
	}))
	defer server.Close()

	previousClient := apiClient
	previousBaseURL := baseURL
	apiClient = client.New("test-token", server.URL)
	baseURL = server.URL
	t.Cleanup(func() {
		apiClient = previousClient
		baseURL = previousBaseURL
	})

	requestContext, cancelRequestContext := context.WithCancel(context.Background())
	defer cancelRequestContext()

	_, submitted, err := apiClient.ApplyCluster(
		requestContext,
		client.CreateImportClusterRequest{Name: "smoke", Spec: client.CreateResourceSpec{Stacks: []client.Stack{}}},
		false,
	)
	if err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}
	if !submitted {
		t.Fatal("expected submitted=true for 202 response")
	}
}

func TestApplyClusterAsyncWaitAgainstMockAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("wait") != "true" {
			responseWriter.WriteHeader(http.StatusBadRequest)
			return
		}
		responseWriter.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(responseWriter).Encode(client.ImportResponse{
			Name:      "smoke",
			ClusterId: "cluster-smoke",
		})
	}))
	defer server.Close()

	previousClient := apiClient
	previousBaseURL := baseURL
	apiClient = client.New("test-token", server.URL)
	baseURL = server.URL
	t.Cleanup(func() {
		apiClient = previousClient
		baseURL = previousBaseURL
	})

	requestContext, cancelRequestContext := context.WithCancel(context.Background())
	defer cancelRequestContext()

	response, submitted, err := apiClient.ApplyCluster(
		requestContext,
		client.CreateImportClusterRequest{Name: "smoke", Spec: client.CreateResourceSpec{Stacks: []client.Stack{}}},
		true,
	)
	if err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}
	if submitted {
		t.Fatal("expected submitted=false for synchronous response")
	}
	if response == nil || response.ClusterId != "cluster-smoke" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

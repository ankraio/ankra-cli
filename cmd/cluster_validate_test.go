package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ankra/internal/client"
)

const validateTestClusterID = "bb549585-5bd3-49e5-87d7-b7f7fa258dcd"

func newValidateTestServer(t *testing.T, receivedClusterID *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/clusters":
			_ = json.NewEncoder(responseWriter).Encode(client.ClusterListResponse{
				Result: []client.ClusterListItem{{
					ID:   validateTestClusterID,
					Name: "chat-ai-l4",
				}},
				Pagination: client.Pagination{TotalPages: 1},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/clusters/validate":
			*receivedClusterID = request.URL.Query().Get("cluster_id")
			_ = json.NewEncoder(responseWriter).Encode(client.ValidateClusterResponse{})
		default:
			responseWriter.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestClusterValidateResolvesClusterNameToID(t *testing.T) {
	receivedClusterID := ""
	server := newValidateTestServer(t, &receivedClusterID)
	defer server.Close()

	previousClient := apiClient
	apiClient = client.New("test-token", server.URL)
	t.Cleanup(func() { apiClient = previousClient })

	_, err := executeCommand("cluster", "validate", "--file", writeMinimalImportCluster(t), "--cluster", "chat-ai-l4")
	if err != nil {
		t.Fatalf("cluster validate: %v", err)
	}
	if receivedClusterID != validateTestClusterID {
		t.Errorf("cluster_id query = %q, want the resolved UUID %q", receivedClusterID, validateTestClusterID)
	}
}

func TestClusterValidatePassesClusterIDThrough(t *testing.T) {
	receivedClusterID := ""
	server := newValidateTestServer(t, &receivedClusterID)
	defer server.Close()

	previousClient := apiClient
	apiClient = client.New("test-token", server.URL)
	t.Cleanup(func() { apiClient = previousClient })

	_, err := executeCommand("cluster", "validate", "--file", writeMinimalImportCluster(t), "--cluster", validateTestClusterID)
	if err != nil {
		t.Fatalf("cluster validate: %v", err)
	}
	if receivedClusterID != validateTestClusterID {
		t.Errorf("cluster_id query = %q, want %q untouched", receivedClusterID, validateTestClusterID)
	}
}

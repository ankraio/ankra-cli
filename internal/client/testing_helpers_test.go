package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testToken = "test-token"

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	httpClient := server.Client()
	return &Client{
		Token:         testToken,
		BaseURL:       server.URL,
		HTTP:          httpClient,
		StreamingHTTP: httpClient,
	}
}

func jsonResponse(t *testing.T, w http.ResponseWriter, statusCode int, body interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("failed to encode response: %v", err)
	}
}

func strPtr(s string) *string {
	return &s
}

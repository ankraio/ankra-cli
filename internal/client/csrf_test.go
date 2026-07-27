package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostCSRFJSONSendsMatchingHeaderAndCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization header = %q", request.Header.Get("Authorization"))
		}
		headerToken := request.Header.Get(csrfHeaderName)
		if headerToken == "" {
			t.Fatal("missing csrf header")
		}
		cookie, err := request.Cookie("ankra_csrf")
		if err != nil {
			t.Fatalf("missing csrf cookie: %v", err)
		}
		if cookie.Value != headerToken {
			t.Fatalf("csrf cookie = %q, header = %q", cookie.Value, headerToken)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	apiClient := New("test-token", server.URL)
	var response struct {
		Status string `json:"status"`
	}
	if err := apiClient.postCSRFJSON(server.URL+"/op", nil, &response, "test operation"); err != nil {
		t.Fatalf("postCSRFJSON() error = %v", err)
	}
	if response.Status != "ok" {
		t.Fatalf("status = %q", response.Status)
	}
}

func TestDeleteCSRFJSONSurfacesBackendDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete {
			t.Fatalf("method = %s", request.Method)
		}
		if request.Header.Get(csrfHeaderName) == "" {
			t.Fatal("missing csrf header")
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(writer).Encode(map[string]string{"detail": "cannot delete right now"})
	}))
	defer server.Close()

	apiClient := New("test-token", server.URL)
	err := apiClient.deleteCSRFJSON(server.URL+"/op", nil, "test operation")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "cannot delete right now") {
		t.Fatalf("error = %q, want backend detail surfaced", got)
	}
}

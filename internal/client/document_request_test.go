package client

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestRequestDocument_ReturnsThePlatformsAnswerAsSent(t *testing.T) {
	const answer = `{"id":"1","field_from_a_newer_platform":{"kept":true}}`
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/example" {
			t.Errorf("request = %s %s, want POST /api/v1/org/example", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("authorization = %q", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(answer))
	}
	document, requestError := newTestClient(t, handler).requestDocument(context.Background(), http.MethodPost,
		"/api/v1/org/example", map[string]any{"name": "x"})
	if requestError != nil || string(document) != answer {
		t.Fatalf("requestDocument = %s, %v; want the body as answered", document, requestError)
	}
}

func TestRequestDocument_SurfacesARefusalWithItsStatus(t *testing.T) {
	for status, body := range map[int]map[string]any{
		http.StatusNotFound: {"detail": "Task not found."},
		http.StatusConflict: {"detail": "Re-apply refused.", "reason": "customised"},
	} {
		handler := func(w http.ResponseWriter, r *http.Request) { jsonResponse(t, w, status, body) }
		_, requestError := newTestClient(t, handler).requestDocument(context.Background(), http.MethodGet, "/api/v1/org/example", nil)
		var unexpected *UnexpectedResponseError
		if !errors.As(requestError, &unexpected) || unexpected.StatusCode != status || requestError.Error() != body["detail"] {
			t.Errorf("status %d: error = %v, want the platform's detail with its status", status, requestError)
		}
	}
	unauthorised := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	if _, requestError := newTestClient(t, unauthorised).requestDocument(context.Background(), http.MethodGet, "/api/v1/org/example", nil); !errors.Is(requestError, ErrUnauthorized) {
		t.Fatalf("401 error = %v, want ErrUnauthorized", requestError)
	}
	denied := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusForbidden, map[string]any{"detail": "permission_denied", "permission": "ai.manage"})
	}
	var permissionDenied *PermissionDeniedError
	if _, requestError := newTestClient(t, denied).requestDocument(context.Background(), http.MethodGet, "/api/v1/org/example", nil); !errors.As(requestError, &permissionDenied) {
		t.Fatalf("403 error = %v, want a PermissionDeniedError", requestError)
	}
}

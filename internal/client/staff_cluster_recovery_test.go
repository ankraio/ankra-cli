package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type recordedStaffRequest struct {
	method        string
	path          string
	authorization string
	session       string
	csrfHeader    string
	csrfCookie    string
	body          map[string]any
}

func newStaffRecoveryServer(t *testing.T, statusCode int, responseBody string) (*httptest.Server, *[]recordedStaffRequest) {
	t.Helper()
	recorded := &[]recordedStaffRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		entry := recordedStaffRequest{method: request.Method, path: request.URL.Path,
			authorization: request.Header.Get("Authorization"), csrfHeader: request.Header.Get(csrfHeaderName)}
		if cookie, cookieError := request.Cookie("ankra_session"); cookieError == nil {
			entry.session = cookie.Value
		}
		if cookie, cookieError := request.Cookie("ankra_csrf"); cookieError == nil {
			entry.csrfCookie = cookie.Value
		}
		if payload, _ := io.ReadAll(request.Body); len(payload) > 0 {
			_ = json.Unmarshal(payload, &entry.body)
		}
		*recorded = append(*recorded, entry)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(statusCode)
		_, _ = writer.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)
	return server, recorded
}

func TestStaffSessionClientSendsTheSessionCookieAndNeverAToken(t *testing.T) {
	server, recorded := newStaffRecoveryServer(t, http.StatusOK,
		`{"cluster_id":"c1","held":true,"blocked_until":"2026-10-27T16:00:00Z","remaining_seconds":14400,"source":"staff_hold","set_by":"staff@ankra.io"}`)
	staffClient := NewStaffSessionClient(server.URL, "staff-session")

	hold, setError := staffClient.SetOperationsHold(context.Background(), "c1", 4*time.Hour, "hop failed")
	if setError != nil {
		t.Fatalf("set: %v", setError)
	}
	if !hold.Held || hold.SetBy == nil || *hold.SetBy != "staff@ankra.io" || len(hold.RawJSON()) == 0 {
		t.Fatalf("hold = %+v", hold)
	}
	if _, liftError := staffClient.LiftOperationsHold(context.Background(), "c1", "done", true); liftError != nil {
		t.Fatalf("lift: %v", liftError)
	}
	if _, repairError := staffClient.RollbackK8sVersionRecord(context.Background(), "c1", "v1.34.5+k3s1", "rolled back"); repairError != nil {
		t.Fatalf("repair: %v", repairError)
	}
	if _, getError := staffClient.GetOperationsHold(context.Background(), "c1"); getError != nil {
		t.Fatalf("get: %v", getError)
	}

	want := []struct{ method, path string }{
		{http.MethodPost, "/admin/api/clusters/c1/operations-hold"},
		{http.MethodDelete, "/admin/api/clusters/c1/operations-hold"},
		{http.MethodPost, "/admin/api/clusters/c1/k8s-version/rollback-record"},
		{http.MethodGet, "/admin/api/clusters/c1/operations-hold"},
	}
	if len(*recorded) != len(want) {
		t.Fatalf("requests = %+v", *recorded)
	}
	for index, request := range *recorded {
		if request.method != want[index].method || request.path != want[index].path {
			t.Fatalf("request %d = %s %s, want %s %s", index, request.method, request.path, want[index].method, want[index].path)
		}
		if request.authorization != "" {
			t.Fatalf("request %d sent an Authorization header: %q", index, request.authorization)
		}
		if request.session != "staff-session" || request.csrfHeader == "" || request.csrfHeader != request.csrfCookie {
			t.Fatalf("request %d = %+v, want the staff session and a matching CSRF pair", index, request)
		}
	}
	if body := (*recorded)[0].body; body["duration_seconds"] != float64(14400) || body["reason"] != "hop failed" {
		t.Fatalf("set body = %v", body)
	}
	if body := (*recorded)[1].body; body["force"] != true || body["reason"] != "done" {
		t.Fatalf("lift body = %v", body)
	}
	if body := (*recorded)[2].body; body["version"] != "v1.34.5+k3s1" || body["reason"] != "rolled back" {
		t.Fatalf("repair body = %v", body)
	}
}

func TestStaffSessionClientSurfacesTheRefusalDetail(t *testing.T) {
	server, _ := newStaffRecoveryServer(t, http.StatusConflict,
		`{"detail":"An operations hold must be in force before the version record is repaired"}`)
	_, repairError := NewStaffSessionClient(server.URL, "staff-session").
		RollbackK8sVersionRecord(context.Background(), "c1", "v1.34.5+k3s1", "x")
	var unexpected *UnexpectedResponseError
	if !errors.As(repairError, &unexpected) || unexpected.StatusCode != http.StatusConflict ||
		unexpected.Detail != "An operations hold must be in force before the version record is repaired" {
		t.Fatalf("error = %v, want the 409 detail", repairError)
	}

	unauthorised, _ := newStaffRecoveryServer(t, http.StatusUnauthorized, `{"detail":"Authentication required"}`)
	_, getError := NewStaffSessionClient(unauthorised.URL, "expired").GetOperationsHold(context.Background(), "c1")
	if !errors.Is(getError, ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", getError)
	}
}

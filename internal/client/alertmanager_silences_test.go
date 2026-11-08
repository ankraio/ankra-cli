package client

import (
	"errors"
	"net/http"
	"testing"
)

const silenceTestClusterID = "0d5c9e4e-4c2a-4f39-9a1e-2f1f5f0b7c11"

func TestListAlertmanagerSilencesSendsStateAndLocation(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]any{
		"alertmanager": map[string]any{"namespace": "monitoring", "service": "alertmanager-operated", "port": 9093},
		"state":        "all",
		"silences": []map[string]any{{
			"id": "35833f27-aaed-47c3-956e-9b071140f062", "state": "active",
			"matchers":  []map[string]any{{"name": "alertname", "value": "KubeVersionMismatch", "is_regex": false}},
			"starts_at": "2026-08-15T11:59:00Z", "ends_at": "2026-08-29T12:00:00Z",
			"created_by": "ankra-api:auth0|abc", "comment": "version alignment",
		}},
		"count": 1,
	})
	testClient := newTestClient(t, handler)
	list, listError := testClient.ListAlertmanagerSilences(silenceTestClusterID, "all",
		AlertmanagerLocation{Namespace: "monitoring"})
	if listError != nil {
		t.Fatalf("ListAlertmanagerSilences: %v", listError)
	}
	recorder.assert(t, http.MethodGet, "/api/v1/clusters/"+silenceTestClusterID+"/alertmanager/silences")
	if recorder.query != "namespace=monitoring&state=all" {
		t.Errorf("query = %q", recorder.query)
	}
	if list.Count != 1 || list.Silences[0].Matchers[0].Name != "alertname" || list.Alertmanager.Port != 9093 {
		t.Errorf("list = %+v", list)
	}
}

func TestCreateAlertmanagerSilenceSendsTheBody(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusCreated, map[string]any{
		"silence_id": "9b2f4c46-0000-4000-8000-000000000001",
		"matchers":   []map[string]any{{"name": "alertname", "value": "X", "is_regex": false}},
		"starts_at":  "2026-08-15T11:59:00Z", "ends_at": "2026-08-15T14:00:00Z",
		"created_by": "ankra-api:auth0|abc", "comment": "maintenance",
		"alertmanager": map[string]any{"namespace": "monitoring", "service": "alertmanager-operated", "port": 9093},
	})
	testClient := newTestClient(t, handler)
	created, createError := testClient.CreateAlertmanagerSilence(silenceTestClusterID, CreateAlertmanagerSilenceRequest{
		Matchers: []AlertmanagerSilenceMatcher{{Name: "alertname", Value: "X"}},
		Duration: "2h",
		Comment:  "maintenance",
	})
	if createError != nil {
		t.Fatalf("CreateAlertmanagerSilence: %v", createError)
	}
	recorder.assert(t, http.MethodPost, "/api/v1/clusters/"+silenceTestClusterID+"/alertmanager/silences")
	body := recorder.decodedBody(t)
	if body["duration"] != "2h" || body["comment"] != "maintenance" || body["namespace"] != nil || body["port"] != nil {
		t.Errorf("body = %+v, want duration and comment and no unset location", body)
	}
	if created.SilenceID != "9b2f4c46-0000-4000-8000-000000000001" {
		t.Errorf("created = %+v", created)
	}
}

func TestExpireAlertmanagerSilenceDeletesTheSilence(t *testing.T) {
	recorder, handler := recordAlertsRequest(t, http.StatusOK, map[string]any{
		"silence_id": "35833f27-aaed-47c3-956e-9b071140f062", "expired": true,
		"alertmanager": map[string]any{"namespace": "custom", "service": "am", "port": 9094},
	})
	testClient := newTestClient(t, handler)
	expired, expireError := testClient.ExpireAlertmanagerSilence(silenceTestClusterID, "35833f27-aaed-47c3-956e-9b071140f062",
		AlertmanagerLocation{Namespace: "custom", Service: "am", Port: 9094})
	if expireError != nil {
		t.Fatalf("ExpireAlertmanagerSilence: %v", expireError)
	}
	recorder.assert(t, http.MethodDelete,
		"/api/v1/clusters/"+silenceTestClusterID+"/alertmanager/silences/35833f27-aaed-47c3-956e-9b071140f062")
	if recorder.query != "namespace=custom&port=9094&service=am" {
		t.Errorf("query = %q", recorder.query)
	}
	if !expired.Expired {
		t.Errorf("expired = %+v", expired)
	}
}

func TestAlertmanagerSilenceRefusalsKeepTheirClass(t *testing.T) {
	_, handler := recordAlertsRequest(t, http.StatusForbidden, map[string]any{
		"detail": "permission_denied", "permission": "alerts.write",
		"scope_type": "cluster", "scope_id": silenceTestClusterID,
	})
	testClient := newTestClient(t, handler)
	_, expireError := testClient.ExpireAlertmanagerSilence(silenceTestClusterID, "35833f27-aaed-47c3-956e-9b071140f062",
		AlertmanagerLocation{})
	var denied *PermissionDeniedError
	if !errors.As(expireError, &denied) || denied.Permission != "alerts.write" {
		t.Fatalf("a 403 must be a PermissionDeniedError naming alerts.write, got %v", expireError)
	}

	_, handler = recordAlertsRequest(t, http.StatusUnauthorized, map[string]any{"detail": "Invalid token"})
	testClient = newTestClient(t, handler)
	if _, listError := testClient.ListAlertmanagerSilences(silenceTestClusterID, "", AlertmanagerLocation{}); !errors.Is(listError, ErrUnauthorized) {
		t.Fatalf("a 401 must be ErrUnauthorized, got %v", listError)
	}
}

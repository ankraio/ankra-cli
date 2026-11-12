package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const (
	adoptTestClusterID = "5f0c3b8e-1d2a-4c6b-9e7f-0a1b2c3d4e5f"
	// adoptTestValue stands in for the value of the adopted key. A fake
	// platform that misbehaves puts it in its answer; nothing the client
	// returns may carry it.
	adoptTestValue = "adopted-VALUE-never-returned"
)

func adoptTestRequest() SecretSlotAdoptRequest {
	return SecretSlotAdoptRequest{
		Label:     "db-password",
		Hint:      "Postgres app user",
		ClusterID: adoptTestClusterID,
		Namespace: "prod",
		Name:      "db-credentials",
		Key:       "password",
	}
}

// TestAdoptSecretSlot_SendsTheContractBody: the adopt is one POST to
// /api/v1/org/secret-slots/adopt whose body carries exactly the members of
// the platform's SecretSlotAdoptRequest, never a value, and strip_trailing_
// line_ending is always sent so the platform never guesses.
func TestAdoptSecretSlot_SendsTheContractBody(t *testing.T) {
	var received map[string]any
	requests := 0
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/secret-slots/adopt" {
			t.Errorf("request = %s %s, want POST /api/v1/org/secret-slots/adopt", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		raw, _ := io.ReadAll(r.Body)
		if unmarshalError := json.Unmarshal(raw, &received); unmarshalError != nil {
			t.Fatalf("request body is not json: %v", unmarshalError)
		}
		jsonResponse(t, w, http.StatusOK, secretSlotTestBody("db-password", "user"))
	})

	request := adoptTestRequest()
	request.StripTrailingLineEnding = true
	slot, adoptError := testClient.AdoptSecretSlot(context.Background(), request)
	if adoptError != nil {
		t.Fatalf("AdoptSecretSlot: %v", adoptError)
	}
	want := map[string]any{
		"label": "db-password", "hint": "Postgres app user", "cluster_id": adoptTestClusterID,
		"namespace": "prod", "name": "db-credentials", "key": "password", "strip_trailing_line_ending": true,
	}
	if len(received) != len(want) {
		t.Errorf("body = %v, want exactly %v", received, want)
	}
	for member, value := range want {
		if received[member] != value {
			t.Errorf("body[%q] = %v, want %v", member, received[member], value)
		}
	}
	if requests != 1 {
		t.Errorf("sent %d requests, want 1", requests)
	}
	if slot.SlotID != secretSlotTestID || slot.Sentinel != "${SECRET_SLOT:"+secretSlotTestID+"}" {
		t.Errorf("slot = %+v", slot)
	}
}

// TestAdoptSecretSlot_OmitsAnEmptyHintAndSendsStripFalse: no --hint sends
// no hint member (the platform's hint is a string, not null), and the strip
// switch is sent as false rather than left out.
func TestAdoptSecretSlot_OmitsAnEmptyHintAndSendsStripFalse(t *testing.T) {
	var received map[string]any
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &received)
		jsonResponse(t, w, http.StatusOK, secretSlotTestBody("db-password", "user"))
	})
	request := adoptTestRequest()
	request.Hint = ""
	if _, adoptError := testClient.AdoptSecretSlot(context.Background(), request); adoptError != nil {
		t.Fatalf("AdoptSecretSlot: %v", adoptError)
	}
	if _, hasHint := received["hint"]; hasHint {
		t.Errorf("body = %v, want no hint member", received)
	}
	if strip, present := received["strip_trailing_line_ending"]; !present || strip != false {
		t.Errorf("strip_trailing_line_ending = %v (present %v), want false", strip, present)
	}
}

// TestAdoptSecretSlot_DropsMembersTheContractDoesNotName: whatever else a
// platform puts in its answer is not decoded, so a value it should never
// have returned cannot reach the caller.
func TestAdoptSecretSlot_DropsMembersTheContractDoesNotName(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := secretSlotTestBody("db-password", "user")
		body["value"] = adoptTestValue
		body["data"] = map[string]any{"password": adoptTestValue}
		jsonResponse(t, w, http.StatusOK, body)
	})
	slot, adoptError := testClient.AdoptSecretSlot(context.Background(), adoptTestRequest())
	if adoptError != nil {
		t.Fatalf("AdoptSecretSlot: %v", adoptError)
	}
	encoded, _ := json.Marshal(slot)
	if strings.Contains(string(encoded), adoptTestValue) {
		t.Errorf("the decoded slot carries the value: %s", encoded)
	}
}

// TestAdoptSecretSlot_RefusalsAreTyped: each answer of the adopt route
// (cluster#6139) arrives as the error type the CLI's exit codes key on, with
// the platform's sentence as the message.
func TestAdoptSecretSlot_RefusalsAreTyped(t *testing.T) {
	type check func(t *testing.T, adoptError error)
	unexpected := func(status int, detail string) check {
		return func(t *testing.T, adoptError error) {
			var refusal *UnexpectedResponseError
			if !errors.As(adoptError, &refusal) || refusal.StatusCode != status {
				t.Fatalf("error = %#v, want an UnexpectedResponseError with status %d", adoptError, status)
			}
			if refusal.Detail != detail || adoptError.Error() != detail {
				t.Errorf("error = %q (detail %q), want the platform's sentence %q", adoptError, refusal.Detail, detail)
			}
		}
	}
	unavailable := func(code, detail string, retryAfter int) check {
		return func(t *testing.T, adoptError error) {
			agentUnavailable, isUnavailable := AsClusterAgentUnavailable(adoptError)
			if !isUnavailable {
				t.Fatalf("error = %#v, want a ClusterAgentUnavailableError", adoptError)
			}
			if agentUnavailable.Code != code || agentUnavailable.Detail != detail || agentUnavailable.RetryAfter != retryAfter {
				t.Errorf("unavailable = %+v, want %s %q retry %d", agentUnavailable, code, detail, retryAfter)
			}
		}
	}
	for _, testCase := range []struct {
		name       string
		status     int
		retryAfter string
		body       any
		check      check
	}{
		{"401 rejected token", http.StatusUnauthorized, "", map[string]any{"detail": "Invalid token"},
			func(t *testing.T, adoptError error) {
				if !errors.Is(adoptError, ErrUnauthorized) {
					t.Errorf("error = %v, want ErrUnauthorized", adoptError)
				}
			}},
		{"403 token lacks variables.write", http.StatusForbidden, "",
			map[string]any{"detail": "permission_denied", "permission": "variables.write", "scope_type": "organisation"},
			func(t *testing.T, adoptError error) {
				var denied *PermissionDeniedError
				if !errors.As(adoptError, &denied) || denied.Permission != "variables.write" {
					t.Errorf("error = %#v, want a PermissionDeniedError naming variables.write", adoptError)
				}
			}},
		{"403 caller lacks kubernetes.secrets_reveal", http.StatusForbidden, "",
			map[string]any{"detail": "Adopting a Secret value needs the kubernetes.secrets_reveal permission on the cluster"},
			func(t *testing.T, adoptError error) {
				var denied *PermissionDeniedError
				if !errors.As(adoptError, &denied) ||
					denied.Detail != "Adopting a Secret value needs the kubernetes.secrets_reveal permission on the cluster" {
					t.Errorf("error = %#v, want a PermissionDeniedError carrying the platform's sentence", adoptError)
				}
			}},
		{"404 cluster", http.StatusNotFound, "", map[string]any{"detail": "Cluster not found"},
			unexpected(http.StatusNotFound, "Cluster not found")},
		{"404 Secret", http.StatusNotFound, "", map[string]any{"detail": "Secret not found"},
			unexpected(http.StatusNotFound, "Secret not found")},
		{"404 key", http.StatusNotFound, "", map[string]any{"detail": "The Secret has no such key"},
			unexpected(http.StatusNotFound, "The Secret has no such key")},
		{"409 label taken", http.StatusConflict, "", map[string]any{"detail": "A secret slot with this label already exists"},
			unexpected(http.StatusConflict, "A secret slot with this label already exists")},
		{"409 synced cache", http.StatusConflict, "",
			map[string]any{"detail": "The cluster answered from its synced cache, which holds no Secret values. Adopting needs a live read from the cluster's agent"},
			unexpected(http.StatusConflict, "The cluster answered from its synced cache, which holds no Secret values. Adopting needs a live read from the cluster's agent")},
		{"400 empty value", http.StatusBadRequest, "",
			map[string]any{"detail": "The Secret key's value is empty, so there is nothing to adopt"},
			unexpected(http.StatusBadRequest, "The Secret key's value is empty, so there is nothing to adopt")},
		{"422 validation", http.StatusUnprocessableEntity, "",
			map[string]any{"detail": []any{map[string]any{
				"type": "string_too_long", "loc": []any{"body", "label"}, "msg": "String should have at most 120 characters",
			}}},
			unexpected(http.StatusUnprocessableEntity, "label: String should have at most 120 characters")},
		{"502 agent could not read", http.StatusBadGateway, "",
			map[string]any{"detail": "The cluster's agent could not read the Secret: secrets \"db-credentials\" is forbidden"},
			unexpected(http.StatusBadGateway, "The cluster's agent could not read the Secret: secrets \"db-credentials\" is forbidden")},
		{"503 NO_AGENT", http.StatusServiceUnavailable, "30",
			map[string]any{"error_code": "NO_AGENT", "detail": "No agent is connected for this cluster", "retry_after": 30},
			unavailable("NO_AGENT", "No agent is connected for this cluster", 30)},
		{"503 AGENT_TIMEOUT", http.StatusServiceUnavailable, "10",
			map[string]any{"error_code": "AGENT_TIMEOUT", "detail": "The agent did not answer in time", "retry_after": 10},
			unavailable("AGENT_TIMEOUT", "The agent did not answer in time", 10)},
		{"503 CLUSTER_OFFLINE without retry_after in the body", http.StatusServiceUnavailable, "30",
			map[string]any{"error_code": "CLUSTER_OFFLINE", "detail": "Cluster is offline"},
			unavailable("CLUSTER_OFFLINE", "Cluster is offline", 30)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requests := 0
			testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if testCase.retryAfter != "" {
					w.Header().Set("Retry-After", testCase.retryAfter)
				}
				jsonResponse(t, w, testCase.status, testCase.body)
			})
			slot, adoptError := testClient.AdoptSecretSlot(context.Background(), adoptTestRequest())
			if adoptError == nil || slot != nil {
				t.Fatalf("slot = %+v, error = %v, want the refusal", slot, adoptError)
			}
			testCase.check(t, adoptError)
			if requests != 1 {
				t.Errorf("sent %d requests, want 1: an adopt is never retried", requests)
			}
		})
	}
}

// TestAdoptSecretSlot_ARefusalWithoutADetailKeepsTheStatus: a body that is
// not the platform's error shape (a proxy page) still says which status it
// was, and a 503 without the envelope is not mistaken for an agent outage.
func TestAdoptSecretSlot_ARefusalWithoutADetailKeepsTheStatus(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError} {
		testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("<html>upstream error</html>"))
		})
		_, adoptError := testClient.AdoptSecretSlot(context.Background(), adoptTestRequest())
		var refusal *UnexpectedResponseError
		if !errors.As(adoptError, &refusal) || refusal.StatusCode != status || refusal.Detail != "" {
			t.Errorf("status %d: error = %#v, want an UnexpectedResponseError without a detail", status, adoptError)
		}
		if _, isUnavailable := AsClusterAgentUnavailable(adoptError); isUnavailable {
			t.Errorf("status %d: a bare 503 was reported as an agent outage", status)
		}
	}
}

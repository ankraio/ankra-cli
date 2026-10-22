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

const secretSlotTestID = "0b6f2c1e-3c44-4b9e-9a51-2f1f6f0f9a10"

func secretSlotTestBody(label, createdVia string) map[string]any {
	return map[string]any{
		"slot_id":            secretSlotTestID,
		"label":              label,
		"hint":               nil,
		"created_at":         "2026-10-22T10:00:00Z",
		"expires_at":         "2026-10-23T10:00:00Z",
		"last_accessed_at":   nil,
		"access_count":       0,
		"sentinel":           "${SECRET_SLOT:" + secretSlotTestID + "}",
		"created_via":        createdVia,
		"pinned_at":          nil,
		"references":         []any{},
		"created_by_user_id": "user-1",
	}
}

func TestCreateSecretSlotWith_GenerateSendsNoValue(t *testing.T) {
	var received map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/org/secret-slots" {
			t.Errorf("request = %s %s, want POST /api/v1/org/secret-slots", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if unmarshalError := json.Unmarshal(raw, &received); unmarshalError != nil {
			t.Fatalf("request body is not json: %v", unmarshalError)
		}
		jsonResponse(t, w, http.StatusOK, secretSlotTestBody("db-password", "generated"))
	}
	testClient := newTestClient(t, handler)

	slot, createError := testClient.CreateSecretSlotWith(context.Background(), SecretSlotCreateRequest{
		Label: "db-password", Generate: &SecretSlotGenerate{Length: 48}, TTLSeconds: 3600,
	})
	if createError != nil {
		t.Fatalf("CreateSecretSlotWith: %v", createError)
	}
	if _, hasValue := received["value"]; hasValue {
		t.Errorf("a generate request must not carry a value key: %v", received)
	}
	generate, _ := received["generate"].(map[string]any)
	if generate["length"] != float64(48) {
		t.Errorf("generate = %v, want length 48", received["generate"])
	}
	if received["ttl_seconds"] != float64(3600) {
		t.Errorf("ttl_seconds = %v, want 3600", received["ttl_seconds"])
	}
	if slot.CreatedVia != "generated" || slot.Sentinel != "${SECRET_SLOT:"+secretSlotTestID+"}" || slot.Hint != nil {
		t.Errorf("slot = %+v", slot)
	}
}

func TestCreateSecretSlotWith_RefusesBothOrNeither(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("nothing may be sent for an invalid request")
	})
	for _, request := range []SecretSlotCreateRequest{
		{Label: "x"},
		{Label: "x", Value: "v", Generate: &SecretSlotGenerate{}},
	} {
		if _, createError := testClient.CreateSecretSlotWith(context.Background(), request); createError == nil {
			t.Errorf("request %+v was accepted", request.Label)
		}
	}
}

func TestReplaceSecretSlot_PutsTheValueAtTheSlotID(t *testing.T) {
	var received map[string]any
	var organisationHeader string
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/org/secret-slots/"+secretSlotTestID {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		organisationHeader = r.Header.Get(orgOverrideHeader)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &received)
		jsonResponse(t, w, http.StatusOK, secretSlotTestBody("stripe-key", "user"))
	}
	testClient := newTestClient(t, handler)
	testClient.HTTP.Transport = &orgOverrideTransport{base: http.DefaultTransport, orgID: &testClient.orgOverride}
	testClient.SetOrganisationOverride("49b7576d-35ed-415e-9580-515a7b56e308")

	if _, replaceError := testClient.ReplaceSecretSlot(context.Background(), secretSlotTestID,
		SecretSlotReplaceRequest{Value: "new-value"}); replaceError != nil {
		t.Fatalf("ReplaceSecretSlot: %v", replaceError)
	}
	if received["value"] != "new-value" || received["generate"] != nil {
		t.Errorf("body = %v, want only the value", received)
	}
	if organisationHeader != "49b7576d-35ed-415e-9580-515a7b56e308" {
		t.Errorf("organisation header = %q, want the --org override", organisationHeader)
	}
}

func TestReplaceSecretSlot_NotFoundIsTyped(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]any{"detail": "Secret slot not found"})
	})
	_, replaceError := testClient.ReplaceSecretSlot(context.Background(), secretSlotTestID,
		SecretSlotReplaceRequest{Generate: &SecretSlotGenerate{}})
	if !errors.Is(replaceError, ErrSecretSlotNotFound) {
		t.Errorf("error = %v, want ErrSecretSlotNotFound", replaceError)
	}
}

func TestDeleteSecretSlot_ReferencedIsTyped(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{
			"detail": "Secret slot is referenced by addon grafana on cluster prod",
		})
	})
	_, deleteError := testClient.DeleteSecretSlot(context.Background(), secretSlotTestID)
	var referenced *SecretSlotReferencedError
	if !errors.As(deleteError, &referenced) || !strings.Contains(referenced.Detail, "grafana") {
		t.Errorf("error = %v, want a SecretSlotReferencedError naming the consumer", deleteError)
	}
}

func TestListSecretSlots_DecodesReferences(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := secretSlotTestBody("db-password", "generated")
		body["pinned_at"] = "2026-10-22T11:00:00Z"
		body["references"] = []map[string]any{{
			"cluster_id": "c-1", "cluster_name": "prod", "resource_id": "r-1",
			"resource_kind": "addon", "resource_name": "grafana",
			"document_path": "adminPassword", "last_seen_at": "2026-10-22T11:00:00Z",
		}}
		jsonResponse(t, w, http.StatusOK, map[string]any{"slots": []any{body}})
	})
	listing, listError := testClient.ListSecretSlots(context.Background())
	if listError != nil {
		t.Fatalf("ListSecretSlots: %v", listError)
	}
	slot := listing.Slots[0]
	if slot.PinnedAt == nil || len(slot.References) != 1 || slot.References[0].ResourceName != "grafana" {
		t.Errorf("slot = %+v", slot)
	}
}

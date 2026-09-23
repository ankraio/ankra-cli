package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestDeprecateStackProfileVersion_PostsTheReasonNoteAndReferences(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/stack-profiles/profile-1/versions/3/deprecation" {
			t.Errorf("path = %s, want /api/v1/org/stack-profiles/profile-1/versions/3/deprecation", r.URL.Path)
		}
		if decodeError := json.NewDecoder(r.Body).Decode(&receivedBody); decodeError != nil {
			t.Fatalf("decode request body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"id": "version-3", "version": 3, "channel": "stable",
			"deprecation": map[string]any{"reason": "cve", "references": []string{"CVE-2026-12345"}},
		})
	}
	testClient := newTestClient(t, handler)

	payload, requestError := testClient.DeprecateStackProfileVersion(context.Background(), "profile-1", 3,
		DeprecateStackProfileVersionRequest{
			Reason:     "cve",
			Note:       "Fixed in v11; see the advisory.",
			References: []string{"CVE-2026-12345"},
		})
	if requestError != nil {
		t.Fatalf("DeprecateStackProfileVersion: %v", requestError)
	}
	if receivedBody["reason"] != "cve" {
		t.Errorf("reason = %v, want cve", receivedBody["reason"])
	}
	if receivedBody["note"] != "Fixed in v11; see the advisory." {
		t.Errorf("note = %v", receivedBody["note"])
	}
	references, ok := receivedBody["references"].([]any)
	if !ok || len(references) != 1 || references[0] != "CVE-2026-12345" {
		t.Errorf("references = %v", receivedBody["references"])
	}
	if !strings.Contains(string(payload), `"deprecation"`) {
		t.Errorf("payload = %s, want the updated version summary", payload)
	}
}

func TestDeprecateStackProfileVersion_OmitsAnEmptyNoteAndReferenceList(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if decodeError := json.NewDecoder(r.Body).Decode(&receivedBody); decodeError != nil {
			t.Fatalf("decode request body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"id": "version-3", "version": 3})
	}
	testClient := newTestClient(t, handler)

	if _, requestError := testClient.DeprecateStackProfileVersion(context.Background(), "profile-1", 3,
		DeprecateStackProfileVersionRequest{Reason: "critical_bug"}); requestError != nil {
		t.Fatalf("DeprecateStackProfileVersion: %v", requestError)
	}
	if _, present := receivedBody["note"]; present {
		t.Error("an empty note must be omitted so the stored note is not cleared by accident")
	}
	if _, present := receivedBody["references"]; present {
		t.Error("an empty reference list must be omitted")
	}
}

func TestUndeprecateStackProfileVersion_DeletesTheDeprecationRoute(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/org/stack-profiles/profile-1/versions/3/deprecation" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"id": "version-3", "version": 3, "deprecation": nil})
	}
	testClient := newTestClient(t, handler)

	payload, requestError := testClient.UndeprecateStackProfileVersion(context.Background(), "profile-1", 3)
	if requestError != nil {
		t.Fatalf("UndeprecateStackProfileVersion: %v", requestError)
	}
	if !strings.Contains(string(payload), `"deprecation":null`) {
		t.Errorf("payload = %s, want a summary with a cleared deprecation", payload)
	}
}

func TestDeprecateStackProfileVersion_SurfacesTheBackendRefusal(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{
			"detail": "The current version cannot be deprecated. Make another version current first.",
		})
	}
	testClient := newTestClient(t, handler)

	_, requestError := testClient.DeprecateStackProfileVersion(context.Background(), "profile-1", 3,
		DeprecateStackProfileVersionRequest{Reason: "cve"})
	if requestError == nil {
		t.Fatal("expected the 409 to be an error")
	}
	if !strings.Contains(requestError.Error(), "Make another version current first.") {
		t.Errorf("error = %q, want the backend detail", requestError)
	}
}

func TestGetStackProfile_ReadsAVersionsDeprecation(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"profile": map[string]any{"id": "profile-1", "name": "postgres-ha", "current_version": 4},
			"versions": []map[string]any{
				{"id": "version-3", "version": 3, "channel": "stable", "created_at": "2026-09-10T19:49:37Z",
					"deprecation": map[string]any{
						"reason": "cve", "note": "Fixed in v11; see the advisory.",
						"references":            []string{"CVE-2026-12345"},
						"deprecated_at":         "2026-09-21T22:10:00.123456Z",
						"deprecated_by_user_id": "auth0|1",
					}},
				{"id": "version-4", "version": 4, "channel": "stable", "created_at": "2026-09-11T08:00:00Z", "deprecation": nil},
			},
			"update_status": map[string]any{
				"outdated_instantiation_count": 2, "has_update_available": true,
				"deprecated_instantiation_count": 1,
			},
		})
	}
	testClient := newTestClient(t, handler)

	detail, requestError := testClient.GetStackProfile("profile-1")
	if requestError != nil {
		t.Fatalf("GetStackProfile: %v", requestError)
	}
	deprecation := detail.Versions[0].Deprecation
	if deprecation == nil {
		t.Fatal("expected v3 to carry a deprecation")
	}
	if deprecation.Reason != "cve" || deprecation.Note == nil || *deprecation.Note != "Fixed in v11; see the advisory." {
		t.Errorf("deprecation = %+v", deprecation)
	}
	if len(deprecation.References) != 1 || deprecation.References[0] != "CVE-2026-12345" {
		t.Errorf("references = %v", deprecation.References)
	}
	if deprecation.DeprecatedAt != "2026-09-21T22:10:00.123456Z" {
		t.Errorf("deprecated_at = %q", deprecation.DeprecatedAt)
	}
	if detail.Versions[1].Deprecation != nil {
		t.Errorf("expected v4 to carry no deprecation, got %+v", detail.Versions[1].Deprecation)
	}
	if detail.UpdateStatus.DeprecatedInstantiationCount != 1 {
		t.Errorf("deprecated_instantiation_count = %d, want 1", detail.UpdateStatus.DeprecatedInstantiationCount)
	}
}

func TestInstantiateStackProfile_SurfacesTheDeprecationRefusalVerbatim(t *testing.T) {
	const refusal = "Version 3 of this profile is deprecated (CVE): Fixed in v11; see the advisory. Deploy another version."
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{"detail": refusal})
	}
	testClient := newTestClient(t, handler)

	_, requestError := testClient.InstantiateStackProfile(context.Background(), "cluster-1",
		InstantiateStackProfileRequest{ProfileID: "profile-1"})
	if requestError == nil {
		t.Fatal("expected the 409 to be an error")
	}
	if requestError.Error() != refusal {
		t.Errorf("error = %q, want the detail verbatim (%q)", requestError, refusal)
	}
	var unexpectedResponse *UnexpectedResponseError
	if !errors.As(requestError, &unexpectedResponse) {
		t.Fatalf("error type = %T, want *UnexpectedResponseError", requestError)
	}
	if unexpectedResponse.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", unexpectedResponse.StatusCode)
	}
	if unexpectedResponse.Detail != refusal {
		t.Errorf("detail = %q, want the backend sentence", unexpectedResponse.Detail)
	}
}

func TestInstantiateStackProfile_KeepsTheBodyWhenThereIsNoDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>proxy error</html>"))
	}
	testClient := newTestClient(t, handler)

	_, requestError := testClient.InstantiateStackProfile(context.Background(), "cluster-1",
		InstantiateStackProfileRequest{ProfileID: "profile-1"})
	if requestError == nil {
		t.Fatal("expected the 502 to be an error")
	}
	if !strings.Contains(requestError.Error(), "instantiate stack profile failed") {
		t.Errorf("error = %q, want the wrapped fallback for a bodiless refusal", requestError)
	}
}

package client

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestImportStackProfileAsDraft_PostsAndDecodes(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/stack-profiles/import-draft" {
			t.Errorf("path = %s, want /api/v1/org/stack-profiles/import-draft", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		jsonResponse(t, w, http.StatusCreated, map[string]any{
			"draft": map[string]any{
				"id": "draft-1", "name": "gpu-inference", "version": 1,
				"profile_id": "profile-1", "base_version": 3,
				"parameters": []map[string]any{{"name": "api_key", "type": "secret"}},
			},
			"warnings": []string{"1 secret value(s) were removed and must be provided when the profile is used."},
		})
	}
	testClient := newTestClient(t, handler)

	name := "gpu-inference"
	result, err := testClient.ImportStackProfileAsDraft(ImportStackProfileDraftRequest{
		Name:          &name,
		ContentBase64: "ZG9jdW1lbnQ=",
	})
	if err != nil {
		t.Fatalf("ImportStackProfileAsDraft: %v", err)
	}
	if result.Draft.ID != "draft-1" || result.Draft.ProfileID == nil || *result.Draft.ProfileID != "profile-1" ||
		result.Draft.BaseVersion == nil || *result.Draft.BaseVersion != 3 || len(result.Draft.Parameters) != 1 {
		t.Errorf("draft = %+v", result.Draft)
	}
	if len(result.Warnings) != 1 {
		t.Errorf("warnings = %v", result.Warnings)
	}
	if receivedBody["name"] != "gpu-inference" || receivedBody["content_base64"] != "ZG9jdW1lbnQ=" {
		t.Errorf("body = %v", receivedBody)
	}
	// Unset metadata must be omitted, never sent as a default: a draft
	// attaching to an existing profile applies non-null metadata at
	// publish, so a defaulted category would overwrite the profile's own.
	for _, key := range []string{"category", "tags", "description"} {
		if _, present := receivedBody[key]; present {
			t.Errorf("unset %s must be omitted from the body, got %v", key, receivedBody)
		}
	}
}

func TestImportStackProfileAsDraft_SurfacesBackendDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{
			"detail": "A draft is already open for this profile. Continue it in the builder, or publish or discard it before importing another.",
		})
	}
	testClient := newTestClient(t, handler)

	if _, err := testClient.ImportStackProfileAsDraft(ImportStackProfileDraftRequest{ContentBase64: "ZG9jdW1lbnQ="}); err == nil {
		t.Fatal("expected the conflict detail to surface as an error")
	}
}

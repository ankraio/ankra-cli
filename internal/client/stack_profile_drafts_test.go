package client

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCreateStackProfileDraft_PostsAndDecodes(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/stack-profiles/drafts" {
			t.Errorf("path = %s, want /api/v1/org/stack-profiles/drafts", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		jsonResponse(t, w, http.StatusCreated, map[string]any{
			"id": "draft-1", "name": "hermes-agent", "version": 1,
			"profile_id": "profile-1",
			"parameters": []map[string]any{{"name": "model_name", "type": "string"}},
		})
	}
	testClient := newTestClient(t, handler)

	draft, err := testClient.CreateStackProfileDraft(CreateStackProfileDraftRequest{ProfileID: "profile-1"})
	if err != nil {
		t.Fatalf("CreateStackProfileDraft: %v", err)
	}
	if draft.ID != "draft-1" || len(draft.Parameters) != 1 {
		t.Errorf("draft = %+v", draft)
	}
	if receivedBody["profile_id"] != "profile-1" {
		t.Errorf("body = %v, want profile_id profile-1", receivedBody)
	}
	if _, present := receivedBody["name"]; present {
		t.Error("empty name must be omitted from the body")
	}
}

func TestListStackProfileDrafts_UnwrapsResultEnvelope(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/stack-profiles/drafts" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{"result": []map[string]any{
			{"id": "draft-1", "name": "hermes-agent", "version": 2},
			{"id": "draft-2", "name": "scout", "version": 1},
		}})
	}
	testClient := newTestClient(t, handler)

	drafts, err := testClient.ListStackProfileDrafts()
	if err != nil {
		t.Fatalf("ListStackProfileDrafts: %v", err)
	}
	if len(drafts) != 2 || drafts[1].Name != "scout" {
		t.Errorf("drafts = %+v", drafts)
	}
}

func TestUpdateStackProfileDraft_RoundTripsUnknownParameterFields(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			jsonResponse(t, w, http.StatusOK, map[string]any{
				"id": "draft-1", "name": "hermes-agent", "version": 3,
				"spec": map[string]any{"stacks": []any{}},
				"parameters": []map[string]any{{
					"name": "model_name", "type": "string",
					"resource_kind": "manifest", "enum_values": []string{"a"},
				}},
			})
		case r.Method == http.MethodPost:
			if r.URL.Path != "/api/v1/org/stack-profiles/drafts/draft-1" {
				t.Errorf("path = %s", r.URL.Path)
			}
			if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			jsonResponse(t, w, http.StatusOK, map[string]any{"id": "draft-1", "name": "hermes-agent", "version": 4})
		}
	}
	testClient := newTestClient(t, handler)

	draft, err := testClient.GetStackProfileDraft("draft-1")
	if err != nil {
		t.Fatalf("GetStackProfileDraft: %v", err)
	}
	draft.Parameters[0]["description"] = "Pick the model."
	if _, err := testClient.UpdateStackProfileDraft("draft-1", UpdateStackProfileDraftRequest{
		Spec: draft.Spec, Parameters: draft.Parameters, Version: draft.Version,
	}); err != nil {
		t.Fatalf("UpdateStackProfileDraft: %v", err)
	}

	parameters := receivedBody["parameters"].([]any)
	parameter := parameters[0].(map[string]any)
	if parameter["description"] != "Pick the model." {
		t.Errorf("description not sent: %v", parameter)
	}
	if parameter["resource_kind"] != "manifest" {
		t.Errorf("unknown fields must round-trip untouched, got %v", parameter)
	}
	if receivedBody["version"] != float64(3) {
		t.Errorf("version = %v, want 3", receivedBody["version"])
	}
}

func TestPublishStackProfileDraft_PostsChannelAndChangelog(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/stack-profiles/drafts/draft-1/publish" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		jsonResponse(t, w, http.StatusCreated, map[string]any{
			"profile": map[string]any{"name": "hermes-agent", "latest_version": 3},
			"version": map[string]any{"version": 3},
		})
	}
	testClient := newTestClient(t, handler)

	result, err := testClient.PublishStackProfileDraft("draft-1", PublishStackProfileDraftRequest{
		Channel: "stable", Changelog: "tooltips"})
	if err != nil {
		t.Fatalf("PublishStackProfileDraft: %v", err)
	}
	if result.Profile["name"] != "hermes-agent" {
		t.Errorf("result = %+v", result)
	}
	if receivedBody["channel"] != "stable" || receivedBody["changelog"] != "tooltips" {
		t.Errorf("body = %v", receivedBody)
	}
	if _, present := receivedBody["visibility"]; present {
		t.Error("empty visibility must be omitted from the body")
	}
}

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const draftOnlyStackListing = `{
  "stacks": [
    {
      "name": "notes",
      "description": null,
      "variables": {"replicas": "2"},
      "draft_id": "9f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f",
      "is_draft_only": true,
      "lifecycle": "draft_only",
      "has_encrypted_values": true,
      "deploy_wave": null,
      "state": "draft",
      "delete_permanently": false,
      "jobs": [],
      "manifests": [
        {
          "name": "api",
          "manifest_base64": "a2luZDogSW5ncmVzcwo=",
          "encrypted_paths": ["data.PGPASSWORD"],
          "parents": [{"name": "db", "kind": "manifest"}],
          "force": false,
          "auto_remediate": false,
          "group": null,
          "agents_md": null,
          "state": "draft",
          "resource_id": "11111111-1111-1111-1111-111111111111",
          "version_history": []
        }
      ],
      "addons": [
        {
          "name": "grafana",
          "chart_name": "grafana",
          "chart_version": "8.5.2",
          "registry_name": "grafana",
          "registry_url": "https://grafana.github.io/helm-charts",
          "namespace": "monitoring",
          "configuration": {"values_base64": "aW5ncmVzczoK", "from_file": null, "encrypted_paths": ["admin.password"]},
          "state": "draft"
        }
      ],
      "applications": []
    },
    {
      "name": "platform",
      "draft_id": null,
      "is_draft_only": false,
      "lifecycle": "deployed_clean",
      "state": "up",
      "manifests": [],
      "addons": [],
      "applications": []
    }
  ],
  "pagination": {"total_pages": 1}
}`

func decodeListing(t *testing.T) []ClusterStackDocument {
	t.Helper()
	var response listClusterStackDocumentsResponse
	if unmarshalError := json.Unmarshal([]byte(draftOnlyStackListing), &response); unmarshalError != nil {
		t.Fatalf("decoding the listing fixture: %v", unmarshalError)
	}
	return response.Stacks
}

func TestClusterStackDocumentReadsTheDraftMembersTheListingCarries(t *testing.T) {
	stacks := decodeListing(t)
	if len(stacks) != 2 {
		t.Fatalf("expected two stacks, got %d", len(stacks))
	}
	draft, deployed := stacks[0], stacks[1]
	if draft.Name() != "notes" || draft.DraftID() != "9f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f" ||
		!draft.IsDraftOnly() || draft.Lifecycle() != "draft_only" {
		t.Fatalf("draft-only stack misread: name=%q draft=%q only=%v lifecycle=%q",
			draft.Name(), draft.DraftID(), draft.IsDraftOnly(), draft.Lifecycle())
	}
	if deployed.DraftID() != "" || deployed.IsDraftOnly() {
		t.Fatalf("a deployed stack must report no draft, got draft=%q only=%v",
			deployed.DraftID(), deployed.IsDraftOnly())
	}
}

func TestAsDeployableSpecDropsOnlyTheRenderOnlyState(t *testing.T) {
	specification, specificationError := decodeListing(t)[0].asDeployableSpec()
	if specificationError != nil {
		t.Fatalf("asDeployableSpec: %v", specificationError)
	}
	if _, present := specification["state"]; present {
		t.Fatal("the stack's render-only state must be dropped: the write refuses the value 'draft'")
	}

	encoded, marshalError := json.Marshal(specification)
	if marshalError != nil {
		t.Fatalf("marshalling the spec: %v", marshalError)
	}
	body := string(encoded)
	if strings.Contains(body, `"state"`) {
		t.Fatalf("no member may keep its render-only state: %s", body)
	}
	// Everything else rides through untouched. encrypted_paths is the one
	// this design exists for: a struct round-trip dropped it, and a stack
	// deployed without its SOPS declaration seals nothing.
	for _, fragment := range []string{
		`"draft_id":"9f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f"`,
		`"encrypted_paths":["data.PGPASSWORD"]`,
		`"encrypted_paths":["admin.password"]`,
		`"manifest_base64":"a2luZDogSW5ncmVzcwo="`,
		`"values_base64":"aW5ncmVzczoK"`,
		`"registry_url":"https://grafana.github.io/helm-charts"`,
		`"parents":[{"name":"db","kind":"manifest"}]`,
		`"variables":{"replicas":"2"}`,
		`"resource_id":"11111111-1111-1111-1111-111111111111"`,
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("the spec must carry %s verbatim, got: %s", fragment, body)
		}
	}
}

func TestDeployClusterStackDraftPostsTheDraftBoundSpec(t *testing.T) {
	var receivedPath string
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if decodeError := json.NewDecoder(r.Body).Decode(&receivedBody); decodeError != nil {
			t.Fatalf("decode request body: %v", decodeError)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"stack_name": "notes", "errors": []any{}, "job_count": 4,
			"operation_id": "22222222-2222-2222-2222-222222222222",
			"warnings":     []string{"Manifest 'api' carries the source cluster's generated hostname."},
		})
	}
	testClient := newTestClient(t, handler)

	result, deployError := testClient.DeployClusterStackDraft(context.Background(), "cluster-1", decodeListing(t)[0])
	if deployError != nil {
		t.Fatalf("DeployClusterStackDraft: %v", deployError)
	}
	if receivedPath != "/api/v1/org/clusters/imported/cluster-1/stacks" {
		t.Fatalf("path = %s", receivedPath)
	}
	specification, isMap := receivedBody["spec"].(map[string]any)
	if !isMap {
		t.Fatalf("body must carry a spec, got %+v", receivedBody)
	}
	stacks, isList := specification["stacks"].([]any)
	if !isList || len(stacks) != 1 {
		t.Fatalf("spec must carry exactly one stack, got %+v", specification["stacks"])
	}
	stack, isStackMap := stacks[0].(map[string]any)
	if !isStackMap || stack["draft_id"] != "9f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f" {
		t.Fatalf("the stack must carry its draft_id, got %+v", stacks[0])
	}
	if result.JobCount != 4 || result.OperationID == nil || len(result.Warnings) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestDeployClusterStackDraftSurfacesARefusal(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"stack_name": "notes",
			"errors": []any{map[string]any{
				"name": "api", "kind": "manifest",
				"errors": []any{map[string]any{"key": "name", "message": "A manifest named 'api' already exists."}},
			}},
			"job_count": 0,
		})
	}
	testClient := newTestClient(t, handler)

	result, deployError := testClient.DeployClusterStackDraft(context.Background(), "cluster-1", decodeListing(t)[0])
	if deployError != nil {
		t.Fatalf("a resource-level refusal is a 200, not a transport error: %v", deployError)
	}
	if len(result.Errors) != 1 || len(result.Errors[0].Errors) != 1 ||
		result.Errors[0].Errors[0].Message != "A manifest named 'api' already exists." {
		t.Fatalf("the refusal must survive decoding: %+v", result.Errors)
	}
}

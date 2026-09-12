package client

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Pins the lane-model routes on the request itself: a path or method typo
// passes every command-wiring test and fails as a 404 against the platform.
func TestAILaneModelRoutes(t *testing.T) {
	var seenMethod, seenPath string
	var seenBody map[string]any
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		seenMethod, seenPath, seenBody = request.Method, request.URL.Path, nil
		if request.Method != http.MethodGet {
			if decodeError := json.NewDecoder(request.Body).Decode(&seenBody); decodeError != nil {
				t.Fatalf("decode request body: %v", decodeError)
			}
		}
		jsonResponse(t, writer, http.StatusOK, map[string]any{
			"lanes": []AILaneModel{{Lane: "pr_review", DefaultTier: "think", EffectiveModelID: "z-ai/glm-5.2"}},
		})
	})

	lanes, listError := testClient.ListAILaneModels()
	if listError != nil || len(lanes) != 1 || lanes[0].EffectiveModelID != "z-ai/glm-5.2" {
		t.Fatalf("ListAILaneModels = %+v, %v", lanes, listError)
	}
	if seenMethod != http.MethodGet || seenPath != "/api/v1/org/ai-settings/lane-models" {
		t.Errorf("list request = %s %s", seenMethod, seenPath)
	}

	if _, setError := testClient.SetAILaneModel("pr_review", "think"); setError != nil {
		t.Fatalf("SetAILaneModel: %v", setError)
	}
	if seenMethod != http.MethodPut || seenPath != "/api/v1/org/ai-settings/lane-models/pr_review" {
		t.Errorf("set request = %s %s", seenMethod, seenPath)
	}
	if seenBody["model_key"] != "think" {
		t.Errorf("set body = %v, want model_key think", seenBody)
	}

	// Clearing must send an explicit null: the platform reads a null or
	// empty model_key as "back to the default tier".
	if _, clearError := testClient.SetAILaneModel("pr_review", ""); clearError != nil {
		t.Fatalf("clearing SetAILaneModel: %v", clearError)
	}
	if modelKey, isPresent := seenBody["model_key"]; !isPresent || modelKey != nil {
		t.Errorf("clear body = %v, want an explicit null model_key", seenBody)
	}
}

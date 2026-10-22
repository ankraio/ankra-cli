package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// An autoscaling write the platform finished inside its verdict window
// answers 202 with what it does; the client keeps that instead of reducing
// it to a bare submission.
func TestUpdateNodeGroupAutoscalingReadsTheOutcomeOfAnAcceptedWrite(t *testing.T) {
	for name, testCase := range map[string]struct {
		body            map[string]any
		wantResult      bool
		wantOperationID string
	}{
		"the platform says what happens": {map[string]any{"status": "accepted", "operation_id": "op-1",
			"message": "Node group 'ci-large' will scale down from 5 to 3 nodes as running pipeline steps finish.",
			"node_count": 5, "target_count": 3}, true, "op-1"},
		"an older platform answers a bare acceptance": {map[string]any{"status": "accepted"}, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
				responseWriter.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(responseWriter).Encode(testCase.body)
			}))
			defer server.Close()

			result, submitted, updateError := New("test-token", server.URL).UpdateHetznerNodeGroupAutoscaling(
				context.Background(), "cluster-1", "ci-large",
				NodeGroupAutoscalingRequest{Enabled: true, MinCount: 1, MaxCount: 3}, false)
			if updateError != nil || !submitted {
				t.Fatalf("submitted=%v error=%v, want an accepted write", submitted, updateError)
			}
			if !testCase.wantResult {
				if result != nil {
					t.Fatalf("a bare acceptance carries no result, got %+v", result)
				}
				return
			}
			if result == nil || result.OperationID == nil || *result.OperationID != testCase.wantOperationID ||
				!result.ChangesNodeCount() || *result.TargetCount != 3 || result.Message == "" {
				t.Fatalf("unexpected result %+v", result)
			}
		})
	}
}

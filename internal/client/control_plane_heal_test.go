package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// The route is pinned on the request itself, and false must be sent rather
// than dropped as a zero value.
func TestControlPlaneHealLaneParity(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		var seenMethod, seenPath string
		var seenBody map[string]any
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			seenMethod, seenPath = request.Method, request.URL.EscapedPath()
			_ = json.NewDecoder(request.Body).Decode(&seenBody)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"cluster_id":"c1","enabled":false,"latest_incident":null}`))
		})
		if _, err := testClient.SetControlPlaneHeal(context.Background(), "c1", enabled); err != nil {
			t.Fatal(err)
		}
		if seenMethod != http.MethodPut || seenPath != "/api/v1/org/clusters/c1/control-plane-self-heal" || seenBody["enabled"] != enabled {
			t.Errorf("got %s %s %v", seenMethod, seenPath, seenBody)
		}
	}
	state, err := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s", request.Method)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"cluster_id":"c1","enabled":true,"latest_incident":{"node_name":"cp-1","mode":"shadow"}}`))
	}).GetControlPlaneHeal(context.Background(), "c1")
	if err != nil || !state.Enabled || state.LatestIncident == nil || state.LatestIncident.NodeName != "cp-1" {
		t.Errorf("state = %+v err = %v", state, err)
	}
}

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestStateSnapshotRetentionLaneParity(t *testing.T) {
	var seenMethod, seenPath string
	var seenBody map[string]any
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		seenMethod, seenPath = request.Method, request.URL.EscapedPath()
		_ = json.NewDecoder(request.Body).Decode(&seenBody)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"cluster_id":"c1","retention_days":30,"is_default":false}`))
	})
	retention, setError := testClient.SetStateSnapshotRetention(context.Background(), "c1", 30)
	if setError != nil {
		t.Fatal(setError)
	}
	if seenMethod != http.MethodPut || seenPath != "/api/v1/org/clusters/c1/state-snapshot-retention" ||
		seenBody["retention_days"] != float64(30) || retention.RetentionDays != 30 {
		t.Errorf("got %s %s %v -> %+v", seenMethod, seenPath, seenBody, retention)
	}
	retention, getError := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s", request.Method)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"cluster_id":"c1","retention_days":7,"is_default":true,"keep_latest":4}`))
	}).GetStateSnapshotRetention(context.Background(), "c1")
	if getError != nil || retention.RetentionDays != 7 || !retention.IsDefault || retention.KeepLatest != 4 {
		t.Errorf("retention = %+v err = %v", retention, getError)
	}
}

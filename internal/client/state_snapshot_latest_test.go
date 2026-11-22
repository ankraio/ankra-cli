package client

import (
	"net/http"
	"testing"
)

func TestGetLatestStateSnapshotReadsTheProviderRoute(t *testing.T) {
	var requestedPath string
	latest, readError := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		requestedPath = request.URL.Path
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"cluster_id":"c1","snapshot":{"id":"s1","trigger":"scheduled","captured_at":"2026-11-03T00:08:27Z","kube_version":"v1.37.1"}}`))
	}).GetLatestStateSnapshot("ankracloud", "c1")
	if readError != nil {
		t.Fatalf("read: %v", readError)
	}
	if requestedPath != "/api/v1/clusters/ankracloud/c1/state-snapshots/latest" {
		t.Fatalf("path = %q", requestedPath)
	}
	if latest.Snapshot == nil || latest.Snapshot.ID != "s1" || latest.Snapshot.Trigger != "scheduled" {
		t.Fatalf("snapshot = %+v", latest.Snapshot)
	}
}

func TestGetLatestStateSnapshotWithNoRestorableCapture(t *testing.T) {
	latest, readError := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"cluster_id":"c1","snapshot":null}`))
	}).GetLatestStateSnapshot("hetzner", "c1")
	if readError != nil {
		t.Fatalf("read: %v", readError)
	}
	if latest.Snapshot != nil {
		t.Fatalf("snapshot = %+v, want none", latest.Snapshot)
	}
}

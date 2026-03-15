package client

import (
	"net/http"
	"strings"
	"testing"
)

func TestListClusterManifests(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/manifests") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, ListClusterManifestsResponse{
			Manifests: []ClusterManifestListItem{
				{Name: "manifest1", Namespace: "default", State: "synced"},
			},
		})
	})
	got, err := testClient.ListClusterManifests("cluster-id")
	if err != nil {
		t.Fatalf("ListClusterManifests() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "manifest1" {
		t.Errorf("ListClusterManifests() got = %v", got)
	}
}

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
		stackName := "core"
		jsonResponse(t, w, http.StatusOK, ListClusterManifestsResponse{
			Result: []ClusterManifestListItem{
				{Name: "manifest1", State: "synced", StackName: &stackName},
			},
			Pagination: Pagination{TotalCount: 1, Page: 1, PageSize: 1000, TotalPages: 1},
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

func TestListClusterManifestsWalksAllPages(t *testing.T) {
	var requestedPages []string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		requestedPages = append(requestedPages, page)
		if pageSize := r.URL.Query().Get("page_size"); pageSize != "100" {
			t.Errorf("expected page_size=100, got %q", pageSize)
		}
		switch page {
		case "1":
			jsonResponse(t, w, http.StatusOK, ListClusterManifestsResponse{
				Result: []ClusterManifestListItem{
					{Name: "manifest1", State: "synced"},
					{Name: "manifest2", State: "synced"},
				},
				Pagination: Pagination{TotalCount: 3, Page: 1, PageSize: 100, TotalPages: 2},
			})
		default:
			jsonResponse(t, w, http.StatusOK, ListClusterManifestsResponse{
				Result: []ClusterManifestListItem{
					{Name: "manifest3", State: "synced"},
				},
				Pagination: Pagination{TotalCount: 3, Page: 2, PageSize: 100, TotalPages: 2},
			})
		}
	})
	got, err := testClient.ListClusterManifests("cluster-id")
	if err != nil {
		t.Fatalf("ListClusterManifests() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListClusterManifests() got %d manifests, want 3 (silent truncation regression)", len(got))
	}
	if got[0].Name != "manifest1" || got[2].Name != "manifest3" {
		t.Errorf("ListClusterManifests() merged pages out of order: %v", got)
	}
	if len(requestedPages) != 2 || requestedPages[0] != "1" || requestedPages[1] != "2" {
		t.Errorf("expected pages 1 and 2 to be requested, got %v", requestedPages)
	}
}

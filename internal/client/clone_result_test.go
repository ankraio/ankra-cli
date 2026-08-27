package client

import (
	"context"
	"net/http"
	"testing"
)

// TestCloneStackToClusterDecodesApplicationsCloned pins the parity fix: the
// platform reports applications_cloned alongside the addon and manifest
// counts (cluster#1971) and the client used to drop it on decode, so a
// stack whose only member was an application read as if nothing had been
// cloned.
func TestCloneStackToClusterDecodesApplicationsCloned(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/org/clusters/imported/target-1/stacks/clone" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"draft_id":            "draft-1",
			"stack_name":          "deploy-notes",
			"warnings":            []string{},
			"addons_cloned":       0,
			"manifests_cloned":    1,
			"applications_cloned": 1,
		})
	}
	testClient := newTestClient(t, handler)

	result, cloneError := testClient.CloneStackToCluster(context.Background(), "target-1", CloneStackToClusterRequest{
		SourceClusterID: "source-1",
		StackName:       "deploy-notes",
	})
	if cloneError != nil {
		t.Fatalf("CloneStackToCluster: %v", cloneError)
	}
	if result.ApplicationsCloned != 1 || result.ManifestsCloned != 1 || result.AddonsCloned != 0 {
		t.Fatalf("counts drifted: %+v", result)
	}
}

// TestCloneStackToClusterToleratesPlatformsWithoutApplicationCounts pins the
// forward-compatibility half: a platform that predates application cloning
// answers without the field, and the count reads as zero - which is also
// what such a platform cloned.
func TestCloneStackToClusterToleratesPlatformsWithoutApplicationCounts(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"draft_id":         "draft-1",
			"stack_name":       "deploy-notes",
			"warnings":         []string{},
			"addons_cloned":    2,
			"manifests_cloned": 1,
		})
	}
	testClient := newTestClient(t, handler)

	result, cloneError := testClient.CloneStackToCluster(context.Background(), "target-1", CloneStackToClusterRequest{
		SourceClusterID: "source-1",
		StackName:       "deploy-notes",
	})
	if cloneError != nil {
		t.Fatalf("CloneStackToCluster: %v", cloneError)
	}
	if result.ApplicationsCloned != 0 || result.AddonsCloned != 2 {
		t.Fatalf("counts drifted: %+v", result)
	}
}

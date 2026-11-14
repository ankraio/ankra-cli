package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// A rename is a PATCH of the new name to the cluster's id, and the answer
// carries both names and the GitOps directory.
func TestRenameClusterSendsTheNewNameToTheClustersID(t *testing.T) {
	var receivedMethod, receivedPath string
	var receivedBody []byte
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		receivedMethod, receivedPath = r.Method, r.URL.Path
		receivedBody, _ = io.ReadAll(r.Body)
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"cluster_id": "cluster-id", "name": "socloud-dev", "previous_name": "socloud-cheap",
			"iac_directory": "clusters/socloud-cheap-qa234i7ojs", "changed": true,
		})
	})
	result, renameError := testClient.RenameCluster(context.Background(), "cluster-id", "socloud-dev")
	if renameError != nil {
		t.Fatalf("unexpected error %v", renameError)
	}
	if receivedMethod != http.MethodPatch || receivedPath != "/api/v1/org/clusters/imported/cluster-id/name" {
		t.Errorf("request = %s %s, want PATCH /api/v1/org/clusters/imported/cluster-id/name", receivedMethod, receivedPath)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal(receivedBody, &decoded); decodeError != nil {
		t.Fatalf("body is not JSON: %v", decodeError)
	}
	if !reflect.DeepEqual(decoded, map[string]any{"name": "socloud-dev"}) {
		t.Errorf("body = %v, want only the new name", decoded)
	}
	want := ClusterRenameResult{ClusterID: "cluster-id", Name: "socloud-dev", PreviousName: "socloud-cheap",
		IacDirectory: "clusters/socloud-cheap-qa234i7ojs", Changed: true}
	if *result != want {
		t.Errorf("result = %+v, want %+v", *result, want)
	}
}

// A refusal carries the platform's detail.
func TestRenameClusterRefusalCarriesTheDetail(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusConflict, map[string]any{
			"detail": "Cluster with this name already exists. Please choose a different name."})
	})
	_, renameError := testClient.RenameCluster(context.Background(), "cluster-id", "socloud-dev")
	if renameError == nil || !strings.Contains(renameError.Error(), "already exists") {
		t.Fatalf("expected the platform detail, got %v", renameError)
	}
}

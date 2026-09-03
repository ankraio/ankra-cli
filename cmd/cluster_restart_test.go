package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type patchResourceMock struct {
	baseMock
	requests  []client.PatchResourceRequest
	responses map[string]*client.ResourceMutationResponse
}

func (m *patchResourceMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "cluster-abc", Name: name}, nil
}

func (m *patchResourceMock) PatchResource(clusterID string, request client.PatchResourceRequest) (*client.ResourceMutationResponse, error) {
	m.requests = append(m.requests, request)
	if response, ok := m.responses[request.Name]; ok {
		return response, nil
	}
	return &client.ResourceMutationResponse{Status: "success"}, nil
}

func runRestart(t *testing.T, mock APIClient, input string, extraArgs ...string) (string, error) {
	t.Helper()
	resetConfirmFlag(t, clusterRestartCmd, clusterCmd)
	args := append([]string{"cluster", "restart"}, extraArgs...)
	return runWithInput(t, mock, input, args...)
}

func restartedAtFromPatch(t *testing.T, patch interface{}) string {
	t.Helper()
	spec, _ := patch.(map[string]interface{})["spec"].(map[string]interface{})
	template, _ := spec["template"].(map[string]interface{})
	metadata, _ := template["metadata"].(map[string]interface{})
	annotations, _ := metadata["annotations"].(map[string]interface{})
	value, _ := annotations[restartedAtAnnotation].(string)
	return value
}

func TestRestart_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &patchResourceMock{}
	_, err := runRestart(t, mock, "n\n", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if len(mock.requests) != 0 {
		t.Errorf("expected no patch when declined, got %d", len(mock.requests))
	}
}

func TestRestart_YesPatchesRestartedAtAnnotation(t *testing.T) {
	mock := &patchResourceMock{}
	out, err := runRestart(t, mock, "", "sts", "postgres", "-n", "data", "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("expected one patch, got %d", len(mock.requests))
	}
	request := mock.requests[0]
	if request.Kind != "StatefulSet" || request.Group != "apps" || request.Version != "v1" || request.Namespace != "data" || request.Name != "postgres" {
		t.Errorf("request = %+v, want apps/v1 StatefulSet data/postgres", request)
	}
	if request.PatchType != "strategic" {
		t.Errorf("patch_type should be strategic, got %q", request.PatchType)
	}
	if restartedAtFromPatch(t, request.Patch) == "" {
		t.Errorf("patch should set the %s annotation, got %+v", restartedAtAnnotation, request.Patch)
	}
	if !strings.Contains(out, `statefulset "postgres" restarted in namespace "data"`) {
		t.Errorf("expected a restarted line, got: %s", out)
	}
}

func TestRestart_UnsupportedKindExitsUsage(t *testing.T) {
	mock := &patchResourceMock{}
	_, err := runRestart(t, mock, "", "pod", "web-0", "-n", "prod", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("restarting a pod should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.requests) != 0 {
		t.Error("an unsupported kind must be rejected before any API call")
	}
}

func TestRestart_MissingNamespaceExitsUsage(t *testing.T) {
	mock := &patchResourceMock{}
	_, err := runRestart(t, mock, "", "deployment", "web", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("missing namespace should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
}

func TestRestart_DryRunSkipsPrompt(t *testing.T) {
	mock := &patchResourceMock{responses: map[string]*client.ResourceMutationResponse{"web": {Status: "dry_run"}}}
	out, err := runRestart(t, mock, "", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster", "--dry-run")
	if err != nil {
		t.Fatalf("dry run must not prompt or fail: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 1 || !mock.requests[0].DryRun {
		t.Fatalf("expected one dry_run=true patch, got %+v", mock.requests)
	}
	if !strings.Contains(out, "would be restarted") {
		t.Errorf("expected a dry-run line, got: %s", out)
	}
}

func TestRestart_NotFoundExitsNotFound(t *testing.T) {
	mock := &patchResourceMock{responses: map[string]*client.ResourceMutationResponse{"gone": {Status: "not_found"}}}
	_, err := runRestart(t, mock, "", "deployment", "gone", "-n", "prod", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitNotFound {
		t.Errorf("a missing deployment should exit %d, got %d (err=%v)", exitNotFound, got, err)
	}
}

package cmd

import (
	"testing"

	"ankra/internal/client"
)

// k8sResourcesMock returns two same-named resources with no formatRow-friendly
// shape. Under the old code, `-o wide` on a named lookup fell through to the
// table renderer and panicked on the named-pod path's nil formatRow; the up-front
// output-format validation must reject the value before any API call.
type k8sResourcesMock struct {
	baseMock
	called bool
}

func (m *k8sResourcesMock) GetResources(clusterID string, req client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	m.called = true
	return &client.GetResourcesResponse{
		ResourceResponses: []client.ResourceResponseItem{
			{Status: "ok", Kind: "Pod", Version: "v1", Items: []interface{}{
				map[string]interface{}{"metadata": map[string]interface{}{"name": "web-1"}},
				map[string]interface{}{"metadata": map[string]interface{}{"name": "web-2"}},
			}},
		},
	}, nil
}

func (m *k8sResourcesMock) ListPods(clusterID string, opts *client.ListPodsOptions) (*client.ListPodsResponse, error) {
	m.called = true
	return &client.ListPodsResponse{}, nil
}

func TestValidateK8sOutputFormat(t *testing.T) {
	for _, ok := range []string{"table", "json", "yaml"} {
		if err := validateK8sOutputFormat(ok); err != nil {
			t.Errorf("validateK8sOutputFormat(%q) = %v, want nil", ok, err)
		}
	}
	err := validateK8sOutputFormat("wide")
	if err == nil {
		t.Fatal("expected an error for -o wide")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("-o wide should classify as exitUsage (%d), got %d", exitUsage, got)
	}
}

// TestGetPodsNamedWideExitsUsageNoPanic drives `cluster get pods <name> -o wide`
// end-to-end. It must exit usage (2) and must not panic; the mock records
// whether the API was reached so we can confirm the value is rejected up front.
func TestGetPodsNamedWideExitsUsageNoPanic(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &k8sResourcesMock{}
	setMockClient(t, mock)

	_, err := executeCommand("cluster", "get", "pods", "web", "-o", "wide")
	if err == nil {
		t.Fatal("expected a usage error for -o wide on a named pod")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("-o wide on get pods <name> should exit %d, got %d", exitUsage, got)
	}
	if mock.called {
		t.Error("invalid output format should be rejected before any API call")
	}
}

// TestGetDeploymentsNamedWideExitsUsageNoPanic exercises the registerKindCommand
// family (deployments) on the same panic-prone named-lookup path.
func TestGetDeploymentsNamedWideExitsUsageNoPanic(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &k8sResourcesMock{}
	setMockClient(t, mock)

	_, err := executeCommand("cluster", "get", "deployments", "web", "-o", "wide")
	if err == nil {
		t.Fatal("expected a usage error for -o wide on a named deployment")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("-o wide on get deployments <name> should exit %d, got %d", exitUsage, got)
	}
	if mock.called {
		t.Error("invalid output format should be rejected before any API call")
	}
}

// TestResourcesWideExitsUsage confirms the generic `cluster resources` command
// rejects an unsupported -o value up front with exitUsage, matching the
// get/pods family rather than silently rendering a table.
func TestResourcesWideExitsUsage(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &k8sResourcesMock{}
	setMockClient(t, mock)

	_, err := executeCommand("cluster", "resources", "PersistentVolumeClaim", "-o", "wide")
	if err == nil {
		t.Fatal("expected a usage error for -o wide on cluster resources")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("-o wide on cluster resources should exit %d, got %d", exitUsage, got)
	}
	if mock.called {
		t.Error("invalid output format should be rejected before any API call")
	}
}

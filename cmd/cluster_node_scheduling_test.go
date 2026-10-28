package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type nodeSchedulingMock struct {
	baseMock
	patches      []client.PatchResourceRequest
	deletes      []client.DeleteResourceRequest
	pods         []interface{}
	listStatus   string
	deleteStatus map[string]string
}

func (m *nodeSchedulingMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "cluster-abc", Name: name}, nil
}

func (m *nodeSchedulingMock) PatchResource(clusterID string, request client.PatchResourceRequest) (*client.ResourceMutationResponse, error) {
	m.patches = append(m.patches, request)
	return &client.ResourceMutationResponse{Status: "success"}, nil
}

func (m *nodeSchedulingMock) DeleteResource(clusterID string, request client.DeleteResourceRequest) (*client.ResourceMutationResponse, error) {
	m.deletes = append(m.deletes, request)
	if m.deleteStatus != nil {
		if status, ok := m.deleteStatus[request.Name]; ok {
			return &client.ResourceMutationResponse{Status: status}, nil
		}
	}
	return &client.ResourceMutationResponse{Status: "success"}, nil
}

func (m *nodeSchedulingMock) GetResources(clusterID string, request client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	status := m.listStatus
	if status == "" {
		status = "success"
	}
	return &client.GetResourcesResponse{ResourceResponses: []client.ResourceResponseItem{
		{Status: status, Kind: "Pod", Version: "v1", Items: m.pods},
	}}, nil
}

func podOnNode(namespace, name, nodeName string, ownerKind string, annotations map[string]interface{}) map[string]interface{} {
	metadata := map[string]interface{}{"name": name, "namespace": namespace}
	if ownerKind != "" {
		metadata["ownerReferences"] = []interface{}{map[string]interface{}{"kind": ownerKind, "name": "owner"}}
	}
	if annotations != nil {
		metadata["annotations"] = annotations
	}
	return map[string]interface{}{
		"metadata": metadata,
		"spec":     map[string]interface{}{"nodeName": nodeName},
	}
}

func unschedulableFromPatch(t *testing.T, patch interface{}) (bool, bool) {
	t.Helper()
	spec, _ := patch.(map[string]interface{})["spec"].(map[string]interface{})
	value, isBool := spec["unschedulable"].(bool)
	return value, isBool
}

func TestCordon_PatchesNodeUnschedulable(t *testing.T) {
	mock := &nodeSchedulingMock{}
	out, err := runWithInput(t, mock, "", "cluster", "cordon", "worker-3", "--cluster", "prod-cluster")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.patches) != 1 {
		t.Fatalf("expected one patch, got %d", len(mock.patches))
	}
	request := mock.patches[0]
	if request.Kind != "Node" || request.Version != "v1" || request.Name != "worker-3" || request.Namespace != "" {
		t.Errorf("request = %+v, want cluster-scoped v1 Node worker-3", request)
	}
	if value, isBool := unschedulableFromPatch(t, request.Patch); !isBool || !value {
		t.Errorf("cordon must set spec.unschedulable=true, got %+v", request.Patch)
	}
	if !strings.Contains(out, `node "worker-3" cordoned`) {
		t.Errorf("expected a cordoned line, got: %s", out)
	}
}

func TestUncordon_PatchesNodeSchedulable(t *testing.T) {
	mock := &nodeSchedulingMock{}
	out, err := runWithInput(t, mock, "", "cluster", "uncordon", "worker-3", "--cluster", "prod-cluster")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if value, isBool := unschedulableFromPatch(t, mock.patches[0].Patch); !isBool || value {
		t.Errorf("uncordon must set spec.unschedulable=false, got %+v", mock.patches[0].Patch)
	}
	if !strings.Contains(out, `node "worker-3" uncordoned`) {
		t.Errorf("expected an uncordoned line, got: %s", out)
	}
}

func drainPods() []interface{} {
	return []interface{}{
		podOnNode("prod", "web-7d9f-2xkvp", "worker-3", "ReplicaSet", nil),
		podOnNode("monitoring", "node-exporter-abc12", "worker-3", "DaemonSet", nil),
		podOnNode("kube-system", "kube-apiserver-worker-3", "worker-3", "Node", nil),
		podOnNode("kube-system", "etcd-worker-3", "worker-3", "", map[string]interface{}{"kubernetes.io/config.mirror": "abc"}),
		podOnNode("prod", "web-7d9f-other", "worker-1", "ReplicaSet", nil),
		podOnNode("batch", "one-off", "worker-3", "", nil),
	}
}

func TestDrain_CordonsThenDeletesOnlyEvictablePods(t *testing.T) {
	mock := &nodeSchedulingMock{pods: drainPods()}
	resetConfirmFlag(t, clusterDrainCmd, clusterCmd)
	out, err := runWithInput(t, mock, "y\n", "cluster", "drain", "worker-3", "--cluster", "prod-cluster", "--grace-period", "10")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.patches) != 1 {
		t.Fatalf("expected the node to be cordoned once, got %d patches", len(mock.patches))
	}
	if value, isBool := unschedulableFromPatch(t, mock.patches[0].Patch); !isBool || !value {
		t.Errorf("drain must cordon first, got %+v", mock.patches[0].Patch)
	}
	if len(mock.deletes) != 2 {
		t.Fatalf("expected exactly the two evictable pods to be deleted, got %+v", mock.deletes)
	}
	deleted := map[string]bool{}
	for _, request := range mock.deletes {
		deleted[request.Namespace+"/"+request.Name] = true
		if request.Kind != "Pod" || request.GracePeriodSeconds == nil || *request.GracePeriodSeconds != 10 {
			t.Errorf("each delete should be a Pod with grace_period_seconds=10, got %+v", request)
		}
	}
	if !deleted["prod/web-7d9f-2xkvp"] || !deleted["batch/one-off"] {
		t.Errorf("expected prod/web-7d9f-2xkvp and batch/one-off, got %v", deleted)
	}
	if !strings.Contains(out, "Skipping 1 (DaemonSet pod): monitoring/node-exporter-abc12") {
		t.Errorf("expected the DaemonSet skip to be reported, got: %s", out)
	}
	if !strings.Contains(out, "Skipping 2 (static pod)") {
		t.Errorf("expected both static pods to be reported, got: %s", out)
	}
	if !strings.Contains(out, "Drain node \"worker-3\" on cluster \"prod-cluster\" (cordon it and delete 2 pods)") {
		t.Errorf("expected the prompt to state the count, got: %s", out)
	}
	if !strings.Contains(out, `node "worker-3" drained: 2 pods deleted`) {
		t.Errorf("expected a drained summary, got: %s", out)
	}
}

func TestDrain_DeclineLeavesNodeUntouched(t *testing.T) {
	mock := &nodeSchedulingMock{pods: drainPods()}
	resetConfirmFlag(t, clusterDrainCmd, clusterCmd)
	_, err := runWithInput(t, mock, "n\n", "cluster", "drain", "worker-3", "--cluster", "prod-cluster")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if len(mock.patches) != 0 || len(mock.deletes) != 0 {
		t.Errorf("a declined drain must neither cordon nor delete, got %d patches %d deletes", len(mock.patches), len(mock.deletes))
	}
}

func TestDrain_DryRunPrintsPlanOnly(t *testing.T) {
	mock := &nodeSchedulingMock{pods: drainPods()}
	resetConfirmFlag(t, clusterDrainCmd, clusterCmd)
	out, err := runWithInput(t, mock, "", "cluster", "drain", "worker-3", "--cluster", "prod-cluster", "--dry-run")
	if err != nil {
		t.Fatalf("dry run must not prompt or fail: %v\noutput: %s", err, out)
	}
	if len(mock.patches) != 0 || len(mock.deletes) != 0 {
		t.Errorf("dry run must not cordon or delete, got %d patches %d deletes", len(mock.patches), len(mock.deletes))
	}
	if !strings.Contains(out, `2 pods on node "worker-3" will be deleted:`) || !strings.Contains(out, "  prod/web-7d9f-2xkvp") {
		t.Errorf("expected the plan to list the evictable pods, got: %s", out)
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("dry run must not prompt, got: %s", out)
	}
}

func TestDrain_EmptyNodeStillCordons(t *testing.T) {
	mock := &nodeSchedulingMock{}
	resetConfirmFlag(t, clusterDrainCmd, clusterCmd)
	out, err := runWithInput(t, mock, "", "cluster", "drain", "worker-3", "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.patches) != 1 || len(mock.deletes) != 0 {
		t.Errorf("an empty node should be cordoned and nothing deleted, got %d patches %d deletes", len(mock.patches), len(mock.deletes))
	}
	if !strings.Contains(out, "nothing to delete") {
		t.Errorf("expected the empty summary, got: %s", out)
	}
}

func TestDrain_RefusedPodListStopsBeforeCordon(t *testing.T) {
	mock := &nodeSchedulingMock{listStatus: "error"}
	resetConfirmFlag(t, clusterDrainCmd, clusterCmd)
	_, err := runWithInput(t, mock, "", "cluster", "drain", "worker-3", "--cluster", "prod-cluster", "--yes")
	if err == nil {
		t.Fatal("a refused pod list must fail the drain, not read as an empty node")
	}
	if !strings.Contains(err.Error(), `status "error"`) || !strings.Contains(err.Error(), "nothing was cordoned") {
		t.Errorf("error should name the status and say the node is untouched, got: %v", err)
	}
	if len(mock.patches) != 0 || len(mock.deletes) != 0 {
		t.Errorf("a refused list must neither cordon nor delete, got %d patches %d deletes", len(mock.patches), len(mock.deletes))
	}
}

func TestDrain_ReportsAlreadyGonePodsHonestly(t *testing.T) {
	mock := &nodeSchedulingMock{pods: drainPods(), deleteStatus: map[string]string{"one-off": "not_found"}}
	resetConfirmFlag(t, clusterDrainCmd, clusterCmd)
	out, err := runWithInput(t, mock, "", "cluster", "drain", "worker-3", "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "pod batch/one-off already gone") || strings.Contains(out, "pod batch/one-off deleted") {
		t.Errorf("a not_found verdict must read as already gone, not deleted, got: %s", out)
	}
}

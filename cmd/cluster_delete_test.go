package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type deleteResourceMock struct {
	baseMock
	requests  []client.DeleteResourceRequest
	responses map[string]*client.ResourceMutationResponse
}

func (m *deleteResourceMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "cluster-abc", Name: name}, nil
}

func (m *deleteResourceMock) DeleteResource(clusterID string, request client.DeleteResourceRequest) (*client.ResourceMutationResponse, error) {
	m.requests = append(m.requests, request)
	if response, ok := m.responses[request.Name]; ok {
		return response, nil
	}
	return &client.ResourceMutationResponse{Status: "success"}, nil
}

func runDelete(t *testing.T, mock APIClient, input string, extraArgs ...string) (string, error) {
	t.Helper()
	resetConfirmFlag(t, clusterDeleteCmd, clusterCmd)
	args := append([]string{"cluster", "delete"}, extraArgs...)
	return runWithInput(t, mock, input, args...)
}

func TestDelete_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &deleteResourceMock{}
	_, err := runDelete(t, mock, "n\n", "pod", "web-0", "--namespace", "prod", "--cluster", "prod-cluster")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if len(mock.requests) != 0 {
		t.Errorf("expected no delete call when declined, got %d", len(mock.requests))
	}
}

func TestDelete_PodSendsPodDelete(t *testing.T) {
	mock := &deleteResourceMock{}
	out, err := runDelete(t, mock, "", "pod", "web-0", "-n", "prod", "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("expected one delete call, got %d", len(mock.requests))
	}
	request := mock.requests[0]
	if request.Kind != "Pod" || request.Version != "v1" || request.Group != "" || request.Namespace != "prod" || request.Name != "web-0" {
		t.Errorf("request = %+v, want kind=Pod version=v1 namespace=prod name=web-0", request)
	}
	if request.DryRun {
		t.Error("dry_run must be false without --dry-run")
	}
	if request.GracePeriodSeconds != nil {
		t.Errorf("grace_period_seconds must stay unset without --grace-period, got %d", *request.GracePeriodSeconds)
	}
	if !strings.Contains(out, `pod "web-0" deleted in namespace "prod"`) {
		t.Errorf("expected a deleted line, got: %s", out)
	}
}

func TestDelete_KindSpellingsResolveGroupAndPlural(t *testing.T) {
	mock := &deleteResourceMock{}
	out, err := runDelete(t, mock, "", "deploy", "web", "-n", "prod", "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	request := mock.requests[0]
	if request.Kind != "Deployment" || request.Group != "apps" || request.Version != "v1" {
		t.Errorf("deploy should resolve to apps/v1 Deployment, got %+v", request)
	}

	mock = &deleteResourceMock{}
	out, err = runDelete(t, mock, "", "ingresses", "web", "-n", "prod", "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	request = mock.requests[0]
	if request.Kind != "Ingress" || request.Group != "networking.k8s.io" || request.Resource != "ingresses" {
		t.Errorf("ingresses should pin the irregular plural, got %+v", request)
	}
}

func TestDelete_ClusterScopedKindNeedsNoNamespaceAndWarns(t *testing.T) {
	mock := &deleteResourceMock{}
	out, err := runDelete(t, mock, "y\n", "namespace", "scratch", "--cluster", "prod-cluster")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 1 || mock.requests[0].Namespace != "" || mock.requests[0].Kind != "Namespace" {
		t.Fatalf("expected one cluster-scoped Namespace delete, got %+v", mock.requests)
	}
	if !strings.Contains(out, "removes every object inside it") {
		t.Errorf("namespace prompt should warn about the contents, got: %s", out)
	}
	if !strings.Contains(out, `namespace "scratch" deleted`) {
		t.Errorf("expected a deleted line without a namespace suffix, got: %s", out)
	}
}

func TestDelete_CustomResourcePassesGroupAndVersion(t *testing.T) {
	mock := &deleteResourceMock{}
	out, err := runDelete(t, mock, "", "Certificate", "web-tls", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--group", "cert-manager.io", "--api-version", "v1")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	request := mock.requests[0]
	if request.Kind != "Certificate" || request.Group != "cert-manager.io" || request.Version != "v1" {
		t.Errorf("custom resource should pass the overrides through, got %+v", request)
	}
}

func TestDelete_UnknownKindExitsUsage(t *testing.T) {
	mock := &deleteResourceMock{}
	_, err := runDelete(t, mock, "", "Certificate", "web-tls", "-n", "prod", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("unknown kind without --group should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.requests) != 0 {
		t.Error("unknown kind must be rejected before any API call")
	}
}

func TestDelete_MissingNamespaceExitsUsage(t *testing.T) {
	mock := &deleteResourceMock{}
	_, err := runDelete(t, mock, "", "pod", "web-0", "--cluster", "prod-cluster", "--yes")
	if err == nil {
		t.Fatal("expected a usage error without --namespace")
	}
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("missing namespace should exit %d, got %d", exitUsage, got)
	}
	if len(mock.requests) != 0 {
		t.Error("missing namespace must be rejected before any API call")
	}
}

func TestDelete_NegativeGracePeriodExitsUsage(t *testing.T) {
	mock := &deleteResourceMock{}
	_, err := runDelete(t, mock, "", "pod", "web-0", "-n", "prod", "--cluster", "prod-cluster", "--yes", "--grace-period", "-5")
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("negative grace period should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.requests) != 0 {
		t.Error("negative grace period must be rejected before any API call")
	}
}

func TestDelete_GracePeriodForwarded(t *testing.T) {
	mock := &deleteResourceMock{}
	out, err := runDelete(t, mock, "", "pod", "stuck", "-n", "prod", "--cluster", "prod-cluster", "--yes", "--grace-period", "0")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 1 || mock.requests[0].GracePeriodSeconds == nil || *mock.requests[0].GracePeriodSeconds != 0 {
		t.Fatalf("expected grace_period_seconds=0 on the wire, got %+v", mock.requests)
	}
}

func TestDelete_DryRunSkipsPromptAndSendsDryRun(t *testing.T) {
	mock := &deleteResourceMock{responses: map[string]*client.ResourceMutationResponse{
		"web-0": {Status: "dry_run"},
	}}
	out, err := runDelete(t, mock, "", "pod", "web-0", "-n", "prod", "--cluster", "prod-cluster", "--dry-run")
	if err != nil {
		t.Fatalf("dry run must not prompt or fail: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 1 || !mock.requests[0].DryRun {
		t.Fatalf("expected one dry_run=true request, got %+v", mock.requests)
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("dry run must not show the confirmation prompt, got: %s", out)
	}
	if !strings.Contains(out, "would be deleted") {
		t.Errorf("expected a dry-run line, got: %s", out)
	}
}

func TestDelete_MultipleObjectsEachDeleted(t *testing.T) {
	mock := &deleteResourceMock{}
	out, err := runDelete(t, mock, "y\n", "pods", "web-0", "web-1", "-n", "prod", "--cluster", "prod-cluster")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 2 || mock.requests[0].Name != "web-0" || mock.requests[1].Name != "web-1" {
		t.Fatalf("expected deletes for web-0 and web-1 in order, got %+v", mock.requests)
	}
	if !strings.Contains(out, "Delete 2 pods (web-0, web-1)") {
		t.Errorf("expected the bulk prompt to name every pod, got: %s", out)
	}
}

func TestDelete_NotFoundExitsNotFound(t *testing.T) {
	mock := &deleteResourceMock{responses: map[string]*client.ResourceMutationResponse{
		"gone": {Status: "not_found"},
	}}
	out, err := runDelete(t, mock, "", "pod", "web-0", "gone", "-n", "prod", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitNotFound {
		t.Fatalf("a missing pod should exit %d, got %d (err=%v)", exitNotFound, got, err)
	}
	if len(mock.requests) != 2 {
		t.Errorf("a missing pod must not stop the remaining deletes, got %d requests", len(mock.requests))
	}
	if !strings.Contains(out, `pod "web-0" deleted`) || !strings.Contains(out, `pod "gone" not found`) {
		t.Errorf("expected per-pod outcomes, got: %s", out)
	}
}

func TestDelete_RefusedVerdictExitsError(t *testing.T) {
	message := "pods is forbidden: User cannot delete resource"
	mock := &deleteResourceMock{responses: map[string]*client.ResourceMutationResponse{
		"web-0": {Status: "error", Message: &message},
	}}
	_, err := runDelete(t, mock, "", "pod", "web-0", "-n", "prod", "--cluster", "prod-cluster", "--yes")
	if err == nil {
		t.Fatal("an error verdict must not be reported as success")
	}
	if got := exitCodeFor(err); got != exitError {
		t.Errorf("refused delete should exit %d, got %d", exitError, got)
	}
	if !strings.Contains(err.Error(), message) {
		t.Errorf("error should carry the agent's reason, got: %v", err)
	}
}

func TestDelete_TransportErrorStopsRun(t *testing.T) {
	mock := &deleteTransportErrorMock{}
	_, err := runDelete(t, mock, "", "pod", "web-0", "web-1", "-n", "prod", "--cluster", "prod-cluster", "--yes")
	if err == nil {
		t.Fatal("expected the cluster error to surface")
	}
	if !strings.Contains(err.Error(), "Cluster is offline") {
		t.Errorf("expected the offline reason, got: %v", err)
	}
	if mock.calls != 1 {
		t.Errorf("an offline cluster must stop the run after the first call, got %d calls", mock.calls)
	}
}

type deleteTransportErrorMock struct {
	baseMock
	calls int
}

func (m *deleteTransportErrorMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "cluster-abc", Name: name}, nil
}

func (m *deleteTransportErrorMock) DeleteResource(clusterID string, request client.DeleteResourceRequest) (*client.ResourceMutationResponse, error) {
	m.calls++
	return nil, &client.ClusterUnavailableError{ErrorCode: "CLUSTER_OFFLINE"}
}

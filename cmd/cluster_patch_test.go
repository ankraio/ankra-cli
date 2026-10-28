package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

type patchCommandMock struct {
	baseMock
	requests  []client.PatchResourceRequest
	responses map[string]*client.ResourceMutationResponse
}

func (m *patchCommandMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "cluster-abc", Name: name}, nil
}

func (m *patchCommandMock) PatchResource(clusterID string, request client.PatchResourceRequest) (*client.ResourceMutationResponse, error) {
	m.requests = append(m.requests, request)
	if response, ok := m.responses[request.Name]; ok {
		return response, nil
	}
	return &client.ResourceMutationResponse{Status: "success"}, nil
}

func runPatch(t *testing.T, mock APIClient, input string, extraArgs ...string) (string, error) {
	t.Helper()
	resetConfirmFlag(t, clusterPatchCmd, clusterCmd)
	args := append([]string{"cluster", "patch"}, extraArgs...)
	return runWithInput(t, mock, input, args...)
}

func TestPatch_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &patchCommandMock{}
	_, err := runPatch(t, mock, "n\n", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster",
		"--patch", `{"spec":{"replicas":3}}`)
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if len(mock.requests) != 0 {
		t.Errorf("expected no patch call when declined, got %d", len(mock.requests))
	}
}

func TestPatch_DefaultIsStrategicWithParsedDocument(t *testing.T) {
	mock := &patchCommandMock{}
	out, err := runPatch(t, mock, "y\n", "deploy", "web", "-n", "prod", "--cluster", "prod-cluster",
		"--patch", `{"spec":{"replicas":3}}`)
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("expected one patch call, got %d", len(mock.requests))
	}
	request := mock.requests[0]
	if request.Kind != "Deployment" || request.Group != "apps" || request.Version != "v1" || request.Namespace != "prod" || request.Name != "web" {
		t.Errorf("request = %+v, want apps/v1 Deployment web in prod", request)
	}
	if request.PatchType != "strategic" {
		t.Errorf("patch_type should default to strategic, got %q", request.PatchType)
	}
	if request.DryRun {
		t.Error("dry_run must be false without --dry-run")
	}
	spec, _ := request.Patch.(map[string]interface{})["spec"].(map[string]interface{})
	if replicas, _ := spec["replicas"].(int); replicas != 3 {
		t.Errorf("patch should carry spec.replicas=3 as a decoded document, got %#v", request.Patch)
	}
	if !strings.Contains(out, "Apply a strategic merge patch to deployment \"web\" in namespace \"prod\"") {
		t.Errorf("prompt should name the patch type and the object, got: %s", out)
	}
	if !strings.Contains(out, `deployment "web" patched in namespace "prod"`) {
		t.Errorf("expected a patched line, got: %s", out)
	}
}

func TestPatch_MergeTypeAcceptsYAMLDocument(t *testing.T) {
	mock := &patchCommandMock{}
	out, err := runPatch(t, mock, "", "svc", "redis-headless", "-n", "data", "--cluster", "prod-cluster", "--yes",
		"--type", "merge", "--patch", "spec:\n  ipFamilyPolicy: SingleStack\n  ipFamilies: [IPv4]\n")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	request := mock.requests[0]
	if request.Kind != "Service" || request.PatchType != "merge" {
		t.Errorf("expected a merge patch on a Service, got %+v", request)
	}
	spec, _ := request.Patch.(map[string]interface{})["spec"].(map[string]interface{})
	if policy, _ := spec["ipFamilyPolicy"].(string); policy != "SingleStack" {
		t.Errorf("YAML patch should decode to the same document as JSON, got %#v", request.Patch)
	}
	if !strings.Contains(out, `service "redis-headless" patched in namespace "data"`) {
		t.Errorf("expected a patched line, got: %s", out)
	}
}

func TestPatch_JSONTypeSendsOperationList(t *testing.T) {
	mock := &patchCommandMock{}
	out, err := runPatch(t, mock, "", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--type", "json", "--patch", `[{"op":"remove","path":"/metadata/annotations/deprecated"}]`)
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	request := mock.requests[0]
	if request.PatchType != "json" {
		t.Errorf("patch_type should be json, got %q", request.PatchType)
	}
	operations, isList := request.Patch.([]interface{})
	if !isList || len(operations) != 1 {
		t.Fatalf("a JSON patch must travel as the list of operations, got %#v", request.Patch)
	}
	if operationName, _ := operations[0].(map[string]interface{})["op"].(string); operationName != "remove" {
		t.Errorf("expected the remove operation, got %#v", operations[0])
	}
	if !strings.Contains(out, "Apply a JSON patch to") && !strings.Contains(out, `deployment "web" patched`) {
		t.Errorf("expected a patched line, got: %s", out)
	}
}

func TestPatch_JSONTypeRejectsObjectDocument(t *testing.T) {
	mock := &patchCommandMock{}
	_, err := runPatch(t, mock, "", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--type", "json", "--patch", `{"spec":{"replicas":3}}`)
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("an object with --type json should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "list of operations") {
		t.Errorf("error should explain the JSON patch shape, got %v", err)
	}
	if len(mock.requests) != 0 {
		t.Error("a malformed patch must be rejected before any API call")
	}
}

func TestPatch_MergeTypeRejectsOperationList(t *testing.T) {
	mock := &patchCommandMock{}
	_, err := runPatch(t, mock, "", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--patch", `[{"op":"remove","path":"/metadata/annotations/deprecated"}]`)
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("a list without --type json should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "--type json") {
		t.Errorf("error should point at --type json, got %v", err)
	}
	if len(mock.requests) != 0 {
		t.Error("a malformed patch must be rejected before any API call")
	}
}

func TestPatch_InvalidDocumentExitsUsage(t *testing.T) {
	mock := &patchCommandMock{}
	_, err := runPatch(t, mock, "", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--patch", `{"spec": [}`)
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("an unparseable patch should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.requests) != 0 {
		t.Error("an unparseable patch must be rejected before any API call")
	}
}

func TestPatch_RequiresExactlyOnePatchSource(t *testing.T) {
	mock := &patchCommandMock{}
	_, err := runPatch(t, mock, "", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("no patch should exit %d, got %d (err=%v)", exitUsage, got, err)
	}

	patchPath := filepath.Join(t.TempDir(), "patch.json")
	if writeError := os.WriteFile(patchPath, []byte(`{"spec":{"replicas":2}}`), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	_, err = runPatch(t, mock, "", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--patch", `{"spec":{"replicas":3}}`, "--patch-file", patchPath)
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("both sources should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.requests) != 0 {
		t.Error("a missing or ambiguous patch must be rejected before any API call")
	}
}

func TestPatch_PatchFileIsRead(t *testing.T) {
	patchPath := filepath.Join(t.TempDir(), "settings-patch.yaml")
	if writeError := os.WriteFile(patchPath, []byte("data:\n  LOG_LEVEL: debug\n"), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	mock := &patchCommandMock{}
	out, err := runPatch(t, mock, "", "cm", "settings", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--patch-file", patchPath)
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	request := mock.requests[0]
	if request.Kind != "ConfigMap" {
		t.Errorf("cm should resolve to ConfigMap, got %+v", request)
	}
	data, _ := request.Patch.(map[string]interface{})["data"].(map[string]interface{})
	if level, _ := data["LOG_LEVEL"].(string); level != "debug" {
		t.Errorf("patch file content should be the document sent, got %#v", request.Patch)
	}
}

func TestPatch_MissingPatchFileExitsUsage(t *testing.T) {
	mock := &patchCommandMock{}
	_, err := runPatch(t, mock, "", "cm", "settings", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--patch-file", filepath.Join(t.TempDir(), "absent.yaml"))
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("an unreadable patch file should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.requests) != 0 {
		t.Error("an unreadable patch file must be rejected before any API call")
	}
}

func TestPatch_UnknownTypeExitsUsage(t *testing.T) {
	mock := &patchCommandMock{}
	_, err := runPatch(t, mock, "", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--type", "apply", "--patch", `{"spec":{"replicas":3}}`)
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("an unknown --type should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "strategic, merge or json") {
		t.Errorf("error should list the accepted types, got %v", err)
	}
	if len(mock.requests) != 0 {
		t.Error("an unknown patch type must be rejected before any API call")
	}
}

func TestPatch_MissingNamespaceExitsUsage(t *testing.T) {
	mock := &patchCommandMock{}
	_, err := runPatch(t, mock, "", "deployment", "web", "--cluster", "prod-cluster", "--yes",
		"--patch", `{"spec":{"replicas":3}}`)
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("missing namespace should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.requests) != 0 {
		t.Error("missing namespace must be rejected before any API call")
	}
}

func TestPatch_ClusterScopedKindNeedsNoNamespace(t *testing.T) {
	mock := &patchCommandMock{}
	out, err := runPatch(t, mock, "y\n", "node", "worker-1", "--cluster", "prod-cluster",
		"--patch", `{"metadata":{"labels":{"tier":"gpu"}}}`)
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 1 || mock.requests[0].Namespace != "" || mock.requests[0].Kind != "Node" {
		t.Fatalf("expected one cluster-scoped Node patch, got %+v", mock.requests)
	}
	if !strings.Contains(out, `node "worker-1" patched`) || strings.Contains(out, "in namespace") {
		t.Errorf("expected a patched line without a namespace suffix, got: %s", out)
	}
}

func TestPatch_CustomResourcePassesGroupAndVersion(t *testing.T) {
	mock := &patchCommandMock{}
	out, err := runPatch(t, mock, "", "Certificate", "web-tls", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--group", "cert-manager.io", "--api-version", "v1", "--patch", `{"spec":{"renewBefore":"720h"}}`)
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	request := mock.requests[0]
	if request.Kind != "Certificate" || request.Group != "cert-manager.io" || request.Version != "v1" {
		t.Errorf("custom resource should pass the overrides through, got %+v", request)
	}
}

func TestPatch_DryRunSkipsPromptAndSendsDryRun(t *testing.T) {
	mock := &patchCommandMock{responses: map[string]*client.ResourceMutationResponse{
		"web": {Status: "dry_run"},
	}}
	out, err := runPatch(t, mock, "", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster", "--dry-run",
		"--patch", `{"spec":{"replicas":3}}`)
	if err != nil {
		t.Fatalf("dry run must not prompt or fail: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 1 || !mock.requests[0].DryRun {
		t.Fatalf("expected one dry_run=true request, got %+v", mock.requests)
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("dry run must not show the confirmation prompt, got: %s", out)
	}
	if !strings.Contains(out, "would be patched") {
		t.Errorf("expected a dry-run line, got: %s", out)
	}
}

func TestPatch_MultipleObjectsEachPatched(t *testing.T) {
	mock := &patchCommandMock{}
	out, err := runPatch(t, mock, "y\n", "deployments", "web", "api", "-n", "prod", "--cluster", "prod-cluster",
		"--patch", `{"spec":{"replicas":2}}`)
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 2 || mock.requests[0].Name != "web" || mock.requests[1].Name != "api" {
		t.Fatalf("expected patches for web and api in order, got %+v", mock.requests)
	}
	if !strings.Contains(out, "Apply a strategic merge patch to 2 deployments (web, api)") {
		t.Errorf("expected the bulk prompt to name every object, got: %s", out)
	}
}

func TestPatch_NotFoundExitsNotFound(t *testing.T) {
	mock := &patchCommandMock{responses: map[string]*client.ResourceMutationResponse{
		"gone": {Status: "not_found"},
	}}
	out, err := runPatch(t, mock, "", "deployment", "web", "gone", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--patch", `{"spec":{"replicas":2}}`)
	if got := exitCodeFor(err); got != exitNotFound {
		t.Fatalf("a missing object should exit %d, got %d (err=%v)", exitNotFound, got, err)
	}
	if len(mock.requests) != 2 {
		t.Errorf("a missing object must not stop the remaining patches, got %d requests", len(mock.requests))
	}
	if !strings.Contains(out, `deployment "web" patched`) || !strings.Contains(out, `deployment "gone" not found`) {
		t.Errorf("expected per-object outcomes, got: %s", out)
	}
}

func TestPatch_RefusedVerdictExitsError(t *testing.T) {
	message := "deployments.apps is forbidden: User cannot patch resource"
	mock := &patchCommandMock{responses: map[string]*client.ResourceMutationResponse{
		"web": {Status: "error", Message: &message},
	}}
	_, err := runPatch(t, mock, "", "deployment", "web", "-n", "prod", "--cluster", "prod-cluster", "--yes",
		"--patch", `{"spec":{"replicas":2}}`)
	if err == nil {
		t.Fatal("an error verdict must not be reported as success")
	}
	if got := exitCodeFor(err); got != exitError {
		t.Errorf("refused patch should exit %d, got %d", exitError, got)
	}
	if !strings.Contains(err.Error(), message) {
		t.Errorf("error should carry the agent's reason, got: %v", err)
	}
}

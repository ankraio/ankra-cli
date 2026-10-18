package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

// The reported case: leftover Strimzi CRDs blocking a stack profile install.
// A CRD is cluster-scoped, so neither spelling the user reached for - the
// short name or the CamelCase kind with its explicit group - may demand a
// namespace or send one.
func TestDelete_CustomResourceDefinitionIsClusterScoped(t *testing.T) {
	for _, arguments := range [][]string{
		{"crd", "kafkas.kafka.strimzi.io"},
		{"crds", "kafkas.kafka.strimzi.io"},
		{"customresourcedefinitions", "kafkas.kafka.strimzi.io"},
		{"CustomResourceDefinition", "kafkas.kafka.strimzi.io", "--group", "apiextensions.k8s.io", "--api-version", "v1"},
	} {
		mock := &deleteResourceMock{}
		out, err := runDelete(t, mock, "", append(arguments, "--cluster", "markshine-orb", "--yes")...)
		if err != nil {
			t.Fatalf("%v: execute failed: %v\noutput: %s", arguments, err, out)
		}
		if len(mock.requests) != 1 {
			t.Fatalf("%v: expected one delete, got %+v", arguments, mock.requests)
		}
		request := mock.requests[0]
		if request.Kind != "CustomResourceDefinition" || request.Group != "apiextensions.k8s.io" ||
			request.Version != "v1" || request.Resource != "customresourcedefinitions" || request.Namespace != "" {
			t.Errorf("%v: request = %+v, want a cluster-scoped apiextensions.k8s.io/v1 CRD delete", arguments, request)
		}
	}
}

func TestDelete_CustomResourceDefinitionDeletesSeveralInOneCall(t *testing.T) {
	mock := &deleteResourceMock{}
	out, err := runDelete(t, mock, "", "crd", "kafkas.kafka.strimzi.io", "kafkatopics.kafka.strimzi.io",
		"--cluster", "markshine-orb", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.requests) != 2 {
		t.Fatalf("expected two deletes, got %+v", mock.requests)
	}
	if !strings.Contains(out, `customresourcedefinition "kafkatopics.kafka.strimzi.io" deleted`) {
		t.Errorf("expected a deleted line without a namespace suffix, got: %s", out)
	}
}

func TestDelete_ClusterScopedFlagDropsNamespaceForCustomKind(t *testing.T) {
	mock := &deleteResourceMock{}
	out, err := runDelete(t, mock, "", "ClusterIssuer", "letsencrypt-staging", "--group", "cert-manager.io",
		"--api-version", "v1", "--cluster-scoped", "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	request := mock.requests[0]
	if request.Kind != "ClusterIssuer" || request.Group != "cert-manager.io" || request.Namespace != "" {
		t.Errorf("request = %+v, want a cluster-scoped cert-manager.io ClusterIssuer delete", request)
	}
}

func TestDelete_CustomKindWithoutNamespaceSuggestsClusterScoped(t *testing.T) {
	mock := &deleteResourceMock{}
	_, err := runDelete(t, mock, "", "ClusterPolicy", "require-labels", "--group", "kyverno.io",
		"--api-version", "v1", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitUsage {
		t.Fatalf("a custom kind without --namespace should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if !strings.Contains(err.Error(), "--cluster-scoped") {
		t.Errorf("the usage error should point at --cluster-scoped, got: %v", err)
	}
	if len(mock.requests) != 0 {
		t.Error("the delete must be refused before any API call")
	}
}

func TestDelete_BuiltinNamespacedKindErrorDoesNotSuggestClusterScoped(t *testing.T) {
	_, err := runDelete(t, &deleteResourceMock{}, "", "pod", "web-0", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitUsage {
		t.Fatalf("a pod without --namespace should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if strings.Contains(err.Error(), "--cluster-scoped") {
		t.Errorf("a built-in namespaced kind has a known scope, got: %v", err)
	}
}

func TestPatch_ClusterScopedFlagPatchesCustomKindWithoutNamespace(t *testing.T) {
	mock := &patchCommandMock{}
	out, err := runPatch(t, mock, "", "ClusterPolicy", "require-labels", "--group", "kyverno.io",
		"--api-version", "v1", "--cluster-scoped", "--type", "merge",
		"--patch", `{"spec":{"validationFailureAction":"Audit"}}`, "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	request := mock.requests[0]
	if request.Kind != "ClusterPolicy" || request.Group != "kyverno.io" || request.Namespace != "" || request.PatchType != "merge" {
		t.Errorf("request = %+v, want a cluster-scoped kyverno.io ClusterPolicy merge patch", request)
	}
}

func TestPatch_CustomResourceDefinitionNeedsNoNamespace(t *testing.T) {
	mock := &patchCommandMock{}
	out, err := runPatch(t, mock, "", "crd", "kafkas.kafka.strimzi.io", "--type", "merge",
		"--patch", `{"metadata":{"labels":{"stale":"true"}}}`, "--cluster", "markshine-orb", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	request := mock.requests[0]
	if request.Kind != "CustomResourceDefinition" || request.Namespace != "" {
		t.Errorf("request = %+v, want a cluster-scoped CRD patch", request)
	}
}

func TestDescribe_ClusterScopedFlagReadsCustomKindWithoutNamespace(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	mock.responses["ClusterIssuer"] = client.ResourceResponseItem{
		Status: "success", Kind: "ClusterIssuer", Version: "v1",
		Items: []interface{}{objectMap(map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"metadata":   map[string]interface{}{"name": "letsencrypt"},
		})},
	}
	mock.responses["Event"] = client.ResourceResponseItem{Items: []interface{}{}}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "describe", "ClusterIssuer", "letsencrypt",
			"--group", "cert-manager.io", "--api-version", "v1", "--cluster-scoped"); executeError != nil {
			t.Fatalf("describe with --cluster-scoped failed: %v", executeError)
		}
	})
	if !strings.Contains(stripANSICodes(output), "ClusterIssuer/letsencrypt") {
		t.Errorf("cluster issuer description missing:\n%s", output)
	}
	if mock.requests[0].Namespace != "" || mock.requests[0].Kind != "ClusterIssuer" {
		t.Errorf("request = %+v, want a cluster-scoped ClusterIssuer read", mock.requests[0])
	}
}

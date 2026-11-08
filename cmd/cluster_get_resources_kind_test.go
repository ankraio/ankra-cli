package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

func resetGenericResourcesFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		flags := clusterGenericResourcesCmd.Flags()
		_ = flags.Set("group", "")
		_ = flags.Set("api-version", "")
		_ = flags.Set("namespace", "")
		_ = flags.Set("output", "table")
	})
}

func TestGetResourcesPlacesATableKind(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantKind     string
		wantGroup    string
		wantVersion  string
		wantResource string
	}{
		{name: "runtime_class", args: []string{"RuntimeClass"}, wantKind: "RuntimeClass", wantGroup: "node.k8s.io", wantVersion: "v1", wantResource: "runtimeclasses"},
		{name: "priority_class", args: []string{"PriorityClass"}, wantKind: "PriorityClass", wantGroup: "scheduling.k8s.io", wantVersion: "v1", wantResource: "priorityclasses"},
		{name: "plural_spelling", args: []string{"runtimeclasses"}, wantKind: "RuntimeClass", wantGroup: "node.k8s.io", wantVersion: "v1", wantResource: "runtimeclasses"},
		{name: "storage_class", args: []string{"StorageClass"}, wantKind: "StorageClass", wantGroup: "storage.k8s.io", wantVersion: "v1", wantResource: "storageclasses"},
		{name: "explicit_version_wins", args: []string{"HorizontalPodAutoscaler", "--api-version", "v1"}, wantKind: "HorizontalPodAutoscaler", wantGroup: "autoscaling", wantVersion: "v1", wantResource: "horizontalpodautoscalers"},
		{name: "explicit_group_wins", args: []string{"RuntimeClass", "--group", "example.test"}, wantKind: "RuntimeClass", wantGroup: "example.test", wantVersion: "v1", wantResource: "runtimeclasses"},
		{name: "custom_kind_with_group", args: []string{"Certificate", "--group", "cert-manager.io"}, wantKind: "Certificate", wantGroup: "cert-manager.io", wantVersion: "v1"},
		{name: "custom_kind_keeps_explicit_version", args: []string{"Widget", "--group", "example.test", "--api-version", "v1beta1"}, wantKind: "Widget", wantGroup: "example.test", wantVersion: "v1beta1"},
		{name: "core_kind_outside_the_table", args: []string{"PodTemplate"}, wantKind: "PodTemplate", wantVersion: "v1"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			writeSelectedClusterJSON(t)
			mock := &capturedResourcesMock{response: emptyResourcesResponse(testCase.wantKind)}
			setMockClient(t, mock)
			resetGenericResourcesFlags(t)

			arguments := append([]string{"cluster", "get", "resources"}, testCase.args...)
			captureStdout(t, func() {
				if _, executeError := executeCommand(arguments...); executeError != nil {
					t.Fatalf("unexpected error: %v", executeError)
				}
			})

			requested := mock.lastRequest.ResourceRequests[0]
			if requested.Kind != testCase.wantKind || requested.Group != testCase.wantGroup ||
				requested.Version != testCase.wantVersion || requested.Resource != testCase.wantResource {
				t.Errorf("request = %+v, want %+v", requested, testCase)
			}
		})
	}
}

func TestGetResourcesDropsTheNamespaceOfAClusterScopedTableKind(t *testing.T) {
	writeSelectedClusterJSON(t)
	mock := &capturedResourcesMock{response: emptyResourcesResponse("RuntimeClass")}
	setMockClient(t, mock)
	resetGenericResourcesFlags(t)

	captureStdout(t, func() {
		_, _ = executeCommand("cluster", "get", "resources", "RuntimeClass", "-n", "default")
	})

	if requested := mock.lastRequest.ResourceRequests[0]; requested.Namespace != "" {
		t.Errorf("a cluster-scoped kind must not carry a namespace, got %+v", requested)
	}
}

func notFoundResourcesResponse(kind string) *client.GetResourcesResponse {
	notFound := agentNotFoundText
	return &client.GetResourcesResponse{
		ResourceResponses: []client.ResourceResponseItem{
			{Status: "error", Kind: kind, Version: "v1", ErrorResponse: &notFound},
		},
	}
}

func TestGetResourcesNamesGroupWhenACustomKindCannotBePlaced(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &capturedResourcesMock{response: notFoundResourcesResponse("Certificate")})
	resetGenericResourcesFlags(t)

	var commandError error
	captureStdout(t, func() {
		_, commandError = executeCommand("cluster", "get", "resources", "Certificate")
	})

	if commandError == nil || !strings.Contains(commandError.Error(), "--group") {
		t.Fatalf("expected an error naming --group, got %v", commandError)
	}
	if code := exitCodeFor(commandError); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestGetResourcesKeepsANotFoundWithAGroupAFailedRead(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &capturedResourcesMock{response: notFoundResourcesResponse("Certificate")})
	resetGenericResourcesFlags(t)

	var commandError error
	captureStdout(t, func() {
		_, commandError = executeCommand("cluster", "get", "resources", "Certificate", "--group", "cert-manager.io")
	})

	if commandError == nil {
		t.Fatal("expected the failed read to surface")
	}
	if strings.Contains(commandError.Error(), "pass --group") {
		t.Errorf("a caller who passed --group must not be told to pass it: %v", commandError)
	}
	if code := exitCodeFor(commandError); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
}

func TestGenericResourcesGroupErrorLeavesOtherFailuresAlone(t *testing.T) {
	otherFailure := errors.New("reading Certificate failed: forbidden")
	got := genericResourcesGroupError(otherFailure, k8sKind{kind: "Certificate"}, false)
	if !errors.Is(got, otherFailure) || exitCodeFor(got) != exitError {
		t.Errorf("an unrelated failure must pass through unchanged, got %v", got)
	}
}

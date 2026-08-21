package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"ankra/internal/client"
)

// debugVerbsMock answers resources/get from a per-kind table and records
// every request, so the tests can assert what was asked of the platform -
// the field selectors that scope an event listing, and the explicit
// resource plural the metrics API needs - not only what was rendered.
type debugVerbsMock struct {
	baseMock
	responses     map[string]client.ResourceResponseItem
	requests      []client.ResourceRequestItem
	skipCacheSeen []bool
	requestError  error

	logOptions []client.PodLogOptions
	logOutput  map[string]string
	logError   error
}

func (mock *debugVerbsMock) GetResources(clusterID string, request client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	if mock.requestError != nil {
		return nil, mock.requestError
	}
	response := client.GetResourcesResponse{}
	for _, item := range request.ResourceRequests {
		mock.requests = append(mock.requests, item)
		mock.skipCacheSeen = append(mock.skipCacheSeen, request.SkipCache)
		response.ResourceResponses = append(response.ResourceResponses, mock.responses[item.Kind])
	}
	return &response, nil
}

func (mock *debugVerbsMock) StreamPodLogs(_ context.Context, _ string, options client.PodLogOptions, writer io.Writer) error {
	mock.logOptions = append(mock.logOptions, options)
	if mock.logError != nil {
		return mock.logError
	}
	key := options.PodName
	if options.ContainerName != "" {
		key += "/" + options.ContainerName
	}
	if _, writeError := io.WriteString(writer, mock.logOutput[key]); writeError != nil {
		return writeError
	}
	return nil
}

func objectMap(entries map[string]interface{}) map[string]interface{} { return entries }

func newDebugVerbsMock() *debugVerbsMock {
	return &debugVerbsMock{
		responses: map[string]client.ResourceResponseItem{},
		logOutput: map[string]string{},
	}
}

func crashLoopingPod() map[string]interface{} {
	return objectMap(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]interface{}{
			"name":              "web-7d9f-2xkvp",
			"namespace":         "default",
			"creationTimestamp": "2026-08-22T09:00:00Z",
			"labels":            map[string]interface{}{"app": "web"},
		},
		"spec": map[string]interface{}{
			"nodeName": "worker-1",
			"initContainers": []interface{}{
				map[string]interface{}{"name": "wait-for-db"},
			},
			"containers": []interface{}{
				map[string]interface{}{"name": "web"},
				map[string]interface{}{"name": "sidecar"},
			},
		},
		"status": map[string]interface{}{
			"phase": "Running",
			"conditions": []interface{}{
				map[string]interface{}{
					"type": "Ready", "status": "False",
					"reason":             "ContainersNotReady",
					"message":            "containers with unready status: [web]",
					"lastTransitionTime": "2026-08-22T09:05:00Z",
				},
			},
			"containerStatuses": []interface{}{
				map[string]interface{}{
					"name": "web", "ready": false, "restartCount": float64(7),
					"image": "registry.example/web:1.2.3",
					"state": map[string]interface{}{
						"waiting": map[string]interface{}{
							"reason":  "CrashLoopBackOff",
							"message": "back-off 5m0s restarting failed container",
						},
					},
					"lastState": map[string]interface{}{
						"terminated": map[string]interface{}{
							"reason": "Error", "exitCode": float64(1),
						},
					},
				},
			},
		},
	})
}

func scopedEvent() map[string]interface{} {
	return objectMap(map[string]interface{}{
		"type":          "Warning",
		"reason":        "BackOff",
		"message":       "Back-off restarting failed container web",
		"count":         float64(12),
		"lastTimestamp": "2026-08-22T09:06:00Z",
		"involvedObject": map[string]interface{}{
			"kind": "Pod", "name": "web-7d9f-2xkvp", "namespace": "default",
		},
	})
}

func fieldSelectorValue(request client.ResourceRequestItem, field string) string {
	for _, selector := range request.FieldSelectors {
		if selector.Field == field {
			return selector.Value
		}
	}
	return ""
}

// --- describe ---

func TestDescribePodShowsConditionsContainerStateAndScopedEvents(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = client.ResourceResponseItem{
		Status: "success", Kind: "Pod", Version: "v1",
		Items: []interface{}{crashLoopingPod()},
	}
	mock.responses["Event"] = client.ResourceResponseItem{
		Status: "success", Kind: "Event", Version: "v1",
		Items: []interface{}{scopedEvent()},
	}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "describe", "pod", "web-7d9f-2xkvp", "-n", "default"); executeError != nil {
			t.Fatalf("describe failed: %v", executeError)
		}
	})
	plain := stripANSICodes(output)

	for _, expected := range []string{
		"Pod/web-7d9f-2xkvp", "worker-1", "ContainersNotReady",
		"CrashLoopBackOff", "exit 1", "registry.example/web:1.2.3", "BackOff",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("describe output is missing %q\n%s", expected, plain)
		}
	}

	if len(mock.requests) != 2 {
		t.Fatalf("expected a pod read and an event read, got %d requests", len(mock.requests))
	}
	eventRequest := mock.requests[1]
	if eventRequest.Kind != "Event" {
		t.Fatalf("second request should read Events, got %q", eventRequest.Kind)
	}
	if got := fieldSelectorValue(eventRequest, "involvedObject.name"); got != "web-7d9f-2xkvp" {
		t.Errorf("events must be scoped by involvedObject.name, got %q", got)
	}
	if got := fieldSelectorValue(eventRequest, "involvedObject.kind"); got != "Pod" {
		t.Errorf("events must be scoped by involvedObject.kind, got %q", got)
	}
	if got := fieldSelectorValue(eventRequest, "involvedObject.namespace"); got != "default" {
		t.Errorf("events must be scoped by involvedObject.namespace, got %q", got)
	}
}

func TestDescribeJSONCarriesObjectAndEvents(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = client.ResourceResponseItem{
		Items: []interface{}{crashLoopingPod()},
	}
	mock.responses["Event"] = client.ResourceResponseItem{
		Items: []interface{}{scopedEvent()},
	}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "describe", "pod", "web-7d9f-2xkvp", "-n", "default", "-o", "json"); executeError != nil {
			t.Fatalf("describe -o json failed: %v", executeError)
		}
	})

	var decoded describeResult
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("describe -o json must emit parseable JSON: %v\n%s", unmarshalError, output)
	}
	if getNestedString(decoded.Object, "metadata", "name") != "web-7d9f-2xkvp" {
		t.Errorf("object is missing from the structured output: %+v", decoded.Object)
	}
	if len(decoded.Events) != 1 {
		t.Errorf("expected the scoped event in the structured output, got %d", len(decoded.Events))
	}
}

func TestDescribeMissingResourceExitsNotFound(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = client.ResourceResponseItem{Items: []interface{}{}}
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "describe", "pod", "ghost", "-n", "default")
	if executeError == nil {
		t.Fatal("describing a missing pod must fail")
	}
	if got := exitCodeFor(executeError); got != exitNotFound {
		t.Errorf("missing resource should exit %d, got %d (%v)", exitNotFound, got, executeError)
	}
}

func TestDescribeNamespacedKindRequiresNamespace(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "describe", "pod", "web-1")
	if executeError == nil {
		t.Fatal("a namespaced describe without -n must fail")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("missing namespace should exit %d, got %d", exitUsage, got)
	}
	if len(mock.requests) != 0 {
		t.Error("the namespace check must run before any API call")
	}
}

func TestDescribeClusterScopedKindNeedsNoNamespace(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	mock.responses["Node"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"apiVersion": "v1",
			"metadata":   map[string]interface{}{"name": "worker-1"},
		})},
	}
	mock.responses["Event"] = client.ResourceResponseItem{Items: []interface{}{}}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "describe", "node", "worker-1"); executeError != nil {
			t.Fatalf("describing a node failed: %v", executeError)
		}
	})
	if !strings.Contains(stripANSICodes(output), "Node/worker-1") {
		t.Errorf("node description missing:\n%s", output)
	}
	if mock.requests[0].Namespace != "" {
		t.Errorf("a cluster-scoped read must not carry a namespace, got %q", mock.requests[0].Namespace)
	}
}

func TestDescribeUnknownKindWithoutGroupExitsUsage(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "describe", "widget", "thing", "-n", "default")
	if executeError == nil {
		t.Fatal("an unknown kind with no --group must fail")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("unknown kind should exit %d, got %d", exitUsage, got)
	}
	if !strings.Contains(executeError.Error(), "--group") {
		t.Errorf("the error should point at --group, got %q", executeError.Error())
	}
}

// --- events --for ---

func TestEventsForScopesByInvolvedObject(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterEventsCmd)
	mock := newDebugVerbsMock()
	mock.responses["Event"] = client.ResourceResponseItem{
		Items: []interface{}{scopedEvent()},
	}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "events", "--for", "pod/web-7d9f-2xkvp", "-n", "default"); executeError != nil {
			t.Fatalf("events --for failed: %v", executeError)
		}
	})
	if !strings.Contains(stripANSICodes(output), "BackOff") {
		t.Errorf("scoped event missing from output:\n%s", output)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("expected one event read, got %d", len(mock.requests))
	}
	if got := fieldSelectorValue(mock.requests[0], "involvedObject.name"); got != "web-7d9f-2xkvp" {
		t.Errorf("--for must scope by involvedObject.name, got %q", got)
	}
	if got := fieldSelectorValue(mock.requests[0], "involvedObject.kind"); got != "Pod" {
		t.Errorf("--for must scope by involvedObject.kind, got %q", got)
	}
}

func TestEventsForIsAlsoReachableUnderGet(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterGetEventsCmd)
	mock := newDebugVerbsMock()
	mock.responses["Event"] = client.ResourceResponseItem{Items: []interface{}{scopedEvent()}}
	setMockClient(t, mock)

	captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "get", "events", "--for", "deployment/web", "-n", "default"); executeError != nil {
			t.Fatalf("get events --for failed: %v", executeError)
		}
	})
	if got := fieldSelectorValue(mock.requests[0], "involvedObject.kind"); got != "Deployment" {
		t.Errorf("kind/name must resolve the kind, got %q", got)
	}
}

func TestEventsForRejectsBareName(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterEventsCmd)
	mock := newDebugVerbsMock()
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "events", "--for", "web-7d9f", "-n", "default")
	if executeError == nil {
		t.Fatal("--for without a kind must fail")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("bad --for should exit %d, got %d", exitUsage, got)
	}
	if len(mock.requests) != 0 {
		t.Error("--for must be validated before any API call")
	}
}

func TestEventsRejectsUnknownType(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterEventsCmd)
	setMockClient(t, newDebugVerbsMock())

	_, executeError := executeCommand("cluster", "events", "-n", "default", "--type", "Critical")
	if executeError == nil {
		t.Fatal("an unknown --type must fail")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("unknown --type should exit %d, got %d", exitUsage, got)
	}
}

func TestEventsEmptyListingSaysSo(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterEventsCmd)
	mock := newDebugVerbsMock()
	mock.responses["Event"] = client.ResourceResponseItem{Items: []interface{}{}}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "events", "-n", "default"); executeError != nil {
			t.Fatalf("events failed: %v", executeError)
		}
	})
	if !strings.Contains(output, "No events found.") {
		t.Errorf("an empty listing should say so, got:\n%s", output)
	}
}

func TestEventsSurfacesAPIFailure(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterEventsCmd)
	mock := newDebugVerbsMock()
	mock.requestError = fmt.Errorf("cluster is offline")
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "events", "-n", "default")
	if executeError == nil || !strings.Contains(executeError.Error(), "cluster is offline") {
		t.Fatalf("an API failure must surface, got %v", executeError)
	}
}

// --- top ---

func podMetrics(name string, cpu string, memory string) map[string]interface{} {
	return objectMap(map[string]interface{}{
		"metadata": map[string]interface{}{"name": name, "namespace": "default"},
		"containers": []interface{}{
			map[string]interface{}{
				"name":  "web",
				"usage": map[string]interface{}{"cpu": cpu, "memory": memory},
			},
		},
	})
}

func TestTopPodsRendersLiveUsage(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopPodsCmd)
	mock := newDebugVerbsMock()
	mock.responses["PodMetrics"] = client.ResourceResponseItem{
		Items: []interface{}{
			podMetrics("web-1", "250000000n", "128Mi"),
			podMetrics("web-2", "1500000000n", "512Mi"),
		},
	}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "top", "pods", "-n", "default"); executeError != nil {
			t.Fatalf("top pods failed: %v", executeError)
		}
	})
	plain := stripANSICodes(output)
	for _, expected := range []string{"web-1", "250m", "128Mi", "web-2", "1500m", "512Mi"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("top pods output is missing %q\n%s", expected, plain)
		}
	}
	if mock.requests[0].Resource != "pods" || mock.requests[0].Group != metricsAPIGroup {
		t.Errorf("the metrics read must name group %q and resource \"pods\", got %+v",
			metricsAPIGroup, mock.requests[0])
	}
	if !mock.skipCacheSeen[0] {
		t.Error("a live measurement must be read with skip_cache")
	}
}

func TestTopPodsSortByMemoryOrdersDescending(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopPodsCmd)
	mock := newDebugVerbsMock()
	mock.responses["PodMetrics"] = client.ResourceResponseItem{
		Items: []interface{}{
			podMetrics("small", "100000000n", "64Mi"),
			podMetrics("large", "100000000n", "1Gi"),
		},
	}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "top", "pods", "-n", "default", "--sort-by", "memory"); executeError != nil {
			t.Fatalf("top pods --sort-by memory failed: %v", executeError)
		}
	})
	plain := stripANSICodes(output)
	if strings.Index(plain, "large") > strings.Index(plain, "small") {
		t.Errorf("--sort-by memory must put the largest first:\n%s", plain)
	}
}

func TestTopRefusesACachedAnswer(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopPodsCmd)
	mock := newDebugVerbsMock()
	mock.responses["PodMetrics"] = client.ResourceResponseItem{
		CacheMetadata: &client.ResourceCacheMetadata{
			ServedFromCache: true, SyncStatus: "cluster_offline",
		},
		Items: []interface{}{objectMap(map[string]interface{}{
			"kind":     "Pod",
			"metadata": map[string]interface{}{"name": "web-1", "namespace": "default"},
		})},
	}
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "top", "pods", "-n", "default")
	if executeError == nil {
		t.Fatal("a cached answer must not be rendered as a live measurement")
	}
	if !strings.Contains(executeError.Error(), "cluster_offline") {
		t.Errorf("the refusal should name the cache state, got %q", executeError.Error())
	}
}

func TestTopDropsObjectsWithoutUsage(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopPodsCmd)
	mock := newDebugVerbsMock()
	mock.responses["PodMetrics"] = client.ResourceResponseItem{
		Items: []interface{}{
			objectMap(map[string]interface{}{
				"kind":     "Pod",
				"metadata": map[string]interface{}{"name": "not-a-measurement"},
			}),
		},
	}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "top", "pods", "-n", "default"); executeError != nil {
			t.Fatalf("top pods failed: %v", executeError)
		}
	})
	if strings.Contains(output, "not-a-measurement") {
		t.Errorf("a plain Pod must never be rendered as usage:\n%s", output)
	}
	if !strings.Contains(output, "metrics-server") {
		t.Errorf("an empty measurement set should explain metrics-server:\n%s", output)
	}
}

func TestTopNodesShowsPercentagesAgainstAllocatable(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopNodesCmd)
	mock := newDebugVerbsMock()
	mock.responses["NodeMetrics"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"metadata": map[string]interface{}{"name": "worker-1"},
			"usage":    map[string]interface{}{"cpu": "500m", "memory": "1Gi"},
		})},
	}
	mock.responses["Node"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"metadata": map[string]interface{}{"name": "worker-1"},
			"status": map[string]interface{}{
				"allocatable": map[string]interface{}{"cpu": "2", "memory": "4Gi"},
			},
		})},
	}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "top", "nodes"); executeError != nil {
			t.Fatalf("top nodes failed: %v", executeError)
		}
	})
	plain := stripANSICodes(output)
	for _, expected := range []string{"worker-1", "500m", "25%", "1024Mi"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("top nodes output is missing %q\n%s", expected, plain)
		}
	}
}

func TestTopNodesWithoutCapacityOmitsPercentages(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopNodesCmd)
	mock := newDebugVerbsMock()
	mock.responses["NodeMetrics"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"metadata": map[string]interface{}{"name": "worker-1"},
			"usage":    map[string]interface{}{"cpu": "500m", "memory": "1Gi"},
		})},
	}
	mock.responses["Node"] = client.ResourceResponseItem{Items: []interface{}{}}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "top", "nodes"); executeError != nil {
			t.Fatalf("top nodes failed: %v", executeError)
		}
	})
	plain := stripANSICodes(output)
	nodeRow := ""
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "worker-1") {
			nodeRow = line
		}
	}
	if nodeRow == "" {
		t.Fatalf("the node row is missing:\n%s", plain)
	}
	if strings.Contains(nodeRow, "%") {
		t.Errorf("an unknown capacity must not be guessed at as a percentage: %q", nodeRow)
	}
	if !strings.Contains(nodeRow, "500m") || !strings.Contains(nodeRow, "1024Mi") {
		t.Errorf("the measurement itself should still render: %q", nodeRow)
	}
}

func TestTopRejectsUnknownSortBy(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopPodsCmd)
	mock := newDebugVerbsMock()
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "top", "pods", "-n", "default", "--sort-by", "disk")
	if executeError == nil {
		t.Fatal("an unknown --sort-by must fail")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("unknown --sort-by should exit %d, got %d", exitUsage, got)
	}
	if len(mock.requests) != 0 {
		t.Error("--sort-by must be validated before any API call")
	}
}

func TestParseKubernetesQuantity(t *testing.T) {
	cases := []struct {
		raw      string
		expected float64
		isValid  bool
	}{
		{"250000000n", 0.25, true},
		{"500m", 0.5, true},
		{"2", 2, true},
		{"128Mi", 128 * 1024 * 1024, true},
		{"1Gi", 1024 * 1024 * 1024, true},
		{"1k", 1000, true},
		{"", 0, false},
		{"<nil>", 0, false},
		{"lots", 0, false},
	}
	for _, testCase := range cases {
		parsed, isValid := parseKubernetesQuantity(testCase.raw)
		if isValid != testCase.isValid {
			t.Errorf("parseKubernetesQuantity(%q) valid = %v, want %v", testCase.raw, isValid, testCase.isValid)
			continue
		}
		if isValid && parsed != testCase.expected {
			t.Errorf("parseKubernetesQuantity(%q) = %v, want %v", testCase.raw, parsed, testCase.expected)
		}
	}
}

// --- logs ---

func TestLogsPreviousRequestsTheTerminatedContainerAndBoundsTheRead(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	mock.logOutput["web-1"] = "panic: config missing\n"
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "logs", "web-1", "-n", "default", "--previous"); executeError != nil {
			t.Fatalf("logs --previous failed: %v", executeError)
		}
	})
	if !strings.Contains(output, "panic: config missing") {
		t.Errorf("the previous log should be printed, got:\n%s", output)
	}
	if len(mock.logOptions) != 1 {
		t.Fatalf("expected one log read, got %d", len(mock.logOptions))
	}
	if !mock.logOptions[0].Previous {
		t.Error("--previous must reach the platform as Previous")
	}
	if mock.logOptions[0].Follow {
		t.Error("--previous must bound the read: the terminated log cannot stream")
	}
}

func TestLogsSelectorReadsEveryMatchingPodWithPrefixes(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = client.ResourceResponseItem{
		Items: []interface{}{
			objectMap(map[string]interface{}{
				"metadata": map[string]interface{}{"name": "web-b"},
				"spec": map[string]interface{}{
					"containers": []interface{}{map[string]interface{}{"name": "web"}},
				},
			}),
			objectMap(map[string]interface{}{
				"metadata": map[string]interface{}{"name": "web-a"},
				"spec": map[string]interface{}{
					"containers": []interface{}{map[string]interface{}{"name": "web"}},
				},
			}),
		},
	}
	mock.logOutput["web-a"] = "from a\n"
	mock.logOutput["web-b"] = "from b\n"
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "logs", "-l", "app=web", "-n", "default", "--follow=false"); executeError != nil {
			t.Fatalf("logs -l failed: %v", executeError)
		}
	})
	if !strings.Contains(output, "[web-a] from a") || !strings.Contains(output, "[web-b] from b") {
		t.Errorf("multi-pod output must be prefixed per pod, got:\n%s", output)
	}
	if len(mock.logOptions) != 2 {
		t.Fatalf("expected one read per matching pod, got %d", len(mock.logOptions))
	}
	if mock.requests[0].LabelSelector != "app=web" {
		t.Errorf("the pod lookup must carry the selector, got %q", mock.requests[0].LabelSelector)
	}
}

func TestLogsAllContainersExpandsInitAndAppContainers(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = client.ResourceResponseItem{
		Items: []interface{}{crashLoopingPod()},
	}
	mock.logOutput["web-7d9f-2xkvp/wait-for-db"] = "waiting\n"
	mock.logOutput["web-7d9f-2xkvp/web"] = "boom\n"
	mock.logOutput["web-7d9f-2xkvp/sidecar"] = "idle\n"
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "logs", "web-7d9f-2xkvp",
			"-n", "default", "--all-containers", "--follow=false"); executeError != nil {
			t.Fatalf("logs --all-containers failed: %v", executeError)
		}
	})
	for _, expected := range []string{
		"[web-7d9f-2xkvp/wait-for-db] waiting",
		"[web-7d9f-2xkvp/web] boom",
		"[web-7d9f-2xkvp/sidecar] idle",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("--all-containers output is missing %q\n%s", expected, output)
		}
	}
}

func TestLogsStructuredOutputGroupsByTarget(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	mock.logOutput["web-1"] = "line one\nline two\n"
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "logs", "web-1", "-n", "default", "-o", "json"); executeError != nil {
			t.Fatalf("logs -o json failed: %v", executeError)
		}
	})
	var groups []podLogGroup
	if unmarshalError := json.Unmarshal([]byte(output), &groups); unmarshalError != nil {
		t.Fatalf("logs -o json must emit parseable JSON: %v\n%s", unmarshalError, output)
	}
	if len(groups) != 1 || groups[0].Pod != "web-1" {
		t.Fatalf("unexpected structured shape: %+v", groups)
	}
	if len(groups[0].Lines) != 2 || groups[0].Lines[1] != "line two" {
		t.Errorf("lines were not split correctly: %+v", groups[0].Lines)
	}
	if mock.logOptions[0].Follow {
		t.Error("a structured read must be bounded")
	}
}

func TestLogsStructuredOutputRefusesExplicitFollow(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "logs", "web-1", "-n", "default", "-o", "json", "--follow=true")
	if executeError == nil {
		t.Fatal("-o json with --follow must fail rather than emit an unterminated document")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("-o json --follow should exit %d, got %d", exitUsage, got)
	}
}

func TestLogsRejectsPodNameAndSelectorTogether(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	setMockClient(t, newDebugVerbsMock())

	_, executeError := executeCommand("cluster", "logs", "web-1", "-l", "app=web", "-n", "default")
	if executeError == nil {
		t.Fatal("a pod name and a selector together must fail")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("should exit %d, got %d", exitUsage, got)
	}
}

func TestLogsWithoutPodOrSelectorExitsUsage(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	setMockClient(t, newDebugVerbsMock())

	_, executeError := executeCommand("cluster", "logs", "-n", "default")
	if executeError == nil {
		t.Fatal("logs with no target must fail")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("should exit %d, got %d", exitUsage, got)
	}
}

func TestLogsSelectorWithNoMatchExitsNotFound(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = client.ResourceResponseItem{Items: []interface{}{}}
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "logs", "-l", "app=nothing", "-n", "default", "--follow=false")
	if executeError == nil {
		t.Fatal("a selector matching nothing must fail")
	}
	if got := exitCodeFor(executeError); got != exitNotFound {
		t.Errorf("should exit %d, got %d", exitNotFound, got)
	}
}

func TestLogsFollowRefusesTooManyTargets(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	pods := []interface{}{}
	for index := 0; index < maxConcurrentLogFollows+1; index++ {
		pods = append(pods, objectMap(map[string]interface{}{
			"metadata": map[string]interface{}{"name": fmt.Sprintf("web-%d", index)},
			"spec": map[string]interface{}{
				"containers": []interface{}{map[string]interface{}{"name": "web"}},
			},
		}))
	}
	mock.responses["Pod"] = client.ResourceResponseItem{Items: pods}
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "logs", "-l", "app=web", "-n", "default")
	if executeError == nil {
		t.Fatal("following more targets than the cap must fail")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("should exit %d, got %d", exitUsage, got)
	}
	if len(mock.logOptions) != 0 {
		t.Error("no stream should be opened once the cap is exceeded")
	}
}

func TestLogsSurfacesAStreamFailureWithItsTarget(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = client.ResourceResponseItem{
		Items: []interface{}{
			objectMap(map[string]interface{}{
				"metadata": map[string]interface{}{"name": "web-a"},
				"spec": map[string]interface{}{
					"containers": []interface{}{map[string]interface{}{"name": "web"}},
				},
			}),
			objectMap(map[string]interface{}{
				"metadata": map[string]interface{}{"name": "web-b"},
				"spec": map[string]interface{}{
					"containers": []interface{}{map[string]interface{}{"name": "web"}},
				},
			}),
		},
	}
	mock.logError = fmt.Errorf("container gone")
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "logs", "-l", "app=web", "-n", "default", "--follow=false")
	if executeError == nil {
		t.Fatal("a failing stream must surface")
	}
	if !strings.Contains(executeError.Error(), "web-a") {
		t.Errorf("the failure should name its target, got %q", executeError.Error())
	}
}

func TestLogsRejectsContainerWithAllContainers(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	setMockClient(t, newDebugVerbsMock())

	_, executeError := executeCommand(
		"cluster", "logs", "web-1", "-n", "default", "-c", "web", "--all-containers")
	if executeError == nil {
		t.Fatal("--container with --all-containers must fail")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("should exit %d, got %d", exitUsage, got)
	}
}

// --- kind resolution ---

func TestResolveK8sKindAcceptsKubectlSpellings(t *testing.T) {
	cases := map[string]k8sKind{
		"pod":         {kind: "Pod", version: "v1"},
		"pods":        {kind: "Pod", version: "v1"},
		"po":          {kind: "Pod", version: "v1"},
		"Pod":         {kind: "Pod", version: "v1"},
		"deploy":      {kind: "Deployment", group: "apps", version: "v1"},
		"deployments": {kind: "Deployment", group: "apps", version: "v1"},
		"ingresses":   {kind: "Ingress", group: "networking.k8s.io", version: "v1", resource: "ingresses"},
		"endpoints":   {kind: "Endpoints", version: "v1", resource: "endpoints"},
	}
	for spelling, expected := range cases {
		resolved, resolveError := resolveK8sKind(spelling, "", "")
		if resolveError != nil {
			t.Errorf("resolveK8sKind(%q) failed: %v", spelling, resolveError)
			continue
		}
		if resolved != expected {
			t.Errorf("resolveK8sKind(%q) = %+v, want %+v", spelling, resolved, expected)
		}
	}
}

func TestResolveK8sKindHonoursOverrides(t *testing.T) {
	resolved, resolveError := resolveK8sKind("Widget", "example.com", "v1alpha1")
	if resolveError != nil {
		t.Fatalf("an overridden kind must resolve: %v", resolveError)
	}
	if resolved.kind != "Widget" || resolved.group != "example.com" || resolved.version != "v1alpha1" {
		t.Errorf("overrides were not applied: %+v", resolved)
	}
}

func TestResolveK8sKindRejectsEmpty(t *testing.T) {
	if _, resolveError := resolveK8sKind("  ", "", ""); resolveError == nil {
		t.Fatal("an empty kind must fail")
	}
}

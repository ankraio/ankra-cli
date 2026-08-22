package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
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

	logOptions     []client.PodLogOptions
	logOptionsLock sync.Mutex
	logOutput      map[string]string
	logError       error
	// failLogsForPod and failContainer fail only the named targets, so a test
	// can drive a partially broken selector, or a --previous read where only
	// some containers have a terminated instance, rather than an
	// all-or-nothing one.
	failLogsForPod map[string]error
	failContainer  map[string]error
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
	mock.logOptionsLock.Lock()
	mock.logOptions = append(mock.logOptions, options)
	mock.logOptionsLock.Unlock()
	if podError, shouldFail := mock.failLogsForPod[options.PodName]; shouldFail {
		return podError
	}
	if containerError, shouldFail := mock.failContainer[options.ContainerName]; shouldFail {
		return containerError
	}
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

// getEventsCommand finds the events command the get family generates, so the
// test can reset its flags without the production code exporting a handle to
// a command it only ever mounts.
func getEventsCommand(t *testing.T) *cobra.Command {
	t.Helper()
	for _, command := range clusterGetCmd.Commands() {
		if command.Name() == "events" {
			return command
		}
	}
	t.Fatal("cluster get events is not registered")
	return nil
}

func TestEventsForIsAlsoReachableUnderGet(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, getEventsCommand(t))
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

// TestEventsTypeFiltersInTheCLINotAsAFieldSelector pins the fix for a
// silently-wrong filter. The platform answers an event listing from its
// resource cache whenever the cache is fresh, and the cache honours only a
// whitelist of field selectors, dropping a type selector without saying so.
// The same command would then have filtered against an unreachable cluster
// and not filtered against a healthy one.
func TestEventsTypeFiltersInTheCLINotAsAFieldSelector(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterEventsCmd)
	normalEvent := objectMap(map[string]interface{}{
		"type": "Normal", "reason": "Pulled", "message": "pulled image",
		"lastTimestamp":  "2026-08-22T09:00:00Z",
		"involvedObject": map[string]interface{}{"kind": "Pod", "name": "web-1"},
	})
	mock := newDebugVerbsMock()
	mock.responses["Event"] = client.ResourceResponseItem{
		Items: []interface{}{normalEvent, scopedEvent()},
	}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "events", "-n", "default", "--type", "Warning"); executeError != nil {
			t.Fatalf("events --type failed: %v", executeError)
		}
	})
	plain := stripANSICodes(output)
	if strings.Contains(plain, "Pulled") {
		t.Errorf("--type Warning must drop the Normal event:\n%s", plain)
	}
	if !strings.Contains(plain, "BackOff") {
		t.Errorf("--type Warning must keep the Warning event:\n%s", plain)
	}
	if got := fieldSelectorValue(mock.requests[0], "type"); got != "" {
		t.Errorf("type must not ride as a field selector, got %q", got)
	}
}

func TestGetEventsKeepsItsEnvelopeAndPositionalName(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, getEventsCommand(t))
	mock := newDebugVerbsMock()
	mock.responses["Event"] = client.ResourceResponseItem{
		Status: "success", Kind: "Event", Version: "v1",
		Items: []interface{}{scopedEvent()},
	}
	setMockClient(t, mock)

	// The get family's structured output is the resource_responses envelope,
	// and scripts read it. Sharing an implementation with `cluster events`
	// must not quietly turn it into a bare array.
	output := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "get", "events", "-n", "default", "-o", "json"); executeError != nil {
			t.Fatalf("get events -o json failed: %v", executeError)
		}
	})
	var envelope client.GetResourcesResponse
	if unmarshalError := json.Unmarshal([]byte(output), &envelope); unmarshalError != nil {
		t.Fatalf("get events -o json must stay parseable: %v\n%s", unmarshalError, output)
	}
	if len(envelope.ResourceResponses) != 1 || len(envelope.ResourceResponses[0].Items) != 1 {
		t.Fatalf("the resource_responses envelope was lost: %s", output)
	}

	// A positional name still fetches that one object and renders it as a
	// manifest, like every other get subcommand.
	resetCommandFlags(t, getEventsCommand(t))
	manifest := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "get", "events", "web-1.17abc", "-n", "default"); executeError != nil {
			t.Fatalf("get events <name> failed: %v", executeError)
		}
	})
	if !strings.Contains(manifest, "involvedObject") {
		t.Errorf("a named get events should print the manifest, got:\n%s", manifest)
	}
	if mock.requests[1].Name != "web-1.17abc" {
		t.Errorf("the positional name must reach the request, got %q", mock.requests[1].Name)
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

// TestTopRendersUnmeasuredUsageAsDash pins the fix for a confident zero. A
// container whose usage is missing or unparseable used to render as 0m/0Mi,
// which is indistinguishable from a genuinely idle container.
func TestTopRendersUnmeasuredUsageAsDash(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopNodesCmd)
	mock := newDebugVerbsMock()
	mock.responses["NodeMetrics"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"metadata": map[string]interface{}{"name": "worker-1"},
			"usage":    map[string]interface{}{"cpu": "not-a-quantity", "memory": "1Gi"},
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
	if strings.Contains(plain, "0m") || strings.Contains(plain, "0Mi") {
		t.Errorf("an unparseable measurement must not render as zero:\n%s", plain)
	}
	if !strings.Contains(plain, "worker-1") {
		t.Errorf("the node should still be listed:\n%s", plain)
	}
}

// TestTopSurfacesAnErroredResponseInsteadOfBlamingMetricsServer pins the
// other silently-wrong path: an errored response carries no items, which
// looked exactly like "metrics-server is not installed" and exited zero.
func TestTopSurfacesAnErroredResponseInsteadOfBlamingMetricsServer(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopPodsCmd)
	mock := newDebugVerbsMock()
	mock.responses["PodMetrics"] = client.ResourceResponseItem{
		Status: "error", Items: []interface{}{},
	}
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "top", "pods", "-n", "default")
	if executeError == nil {
		t.Fatal("an errored metrics read must not be reported as a missing metrics-server")
	}
	if !strings.Contains(executeError.Error(), "could not answer") {
		t.Errorf("the error should say the read failed, got %q", executeError.Error())
	}
}

func TestTopStructuredOutputIsAnArrayWhenEmpty(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopPodsCmd)
	mock := newDebugVerbsMock()
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand(
			"cluster", "top", "pods", "-n", "default", "-o", "json"); executeError != nil {
			t.Fatalf("top pods -o json failed: %v", executeError)
		}
	})
	if strings.TrimSpace(output) != "[]" {
		t.Errorf("an empty structured read must encode as [], got %q", strings.TrimSpace(output))
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

// TestFollowingTargetsReportEachFailureAndSurviveAPartialOne drives the
// concurrent follow path directly. A following read only ends when the caller
// interrupts it, so a target that dies must reach stderr immediately rather
// than being held until interrupt - at which point the cancelled context makes
// the caller discard it. A target that survives means the command still
// succeeded.
func TestFollowingTargetsReportEachFailureAndSurviveAPartialOne(t *testing.T) {
	mock := newDebugVerbsMock()
	mock.logOutput["web-a"] = "from a\n"
	mock.failLogsForPod = map[string]error{"web-b": fmt.Errorf("container gone")}
	setMockClient(t, mock)

	targets := []logTarget{{podName: "web-a"}, {podName: "web-b"}}
	var output bytes.Buffer
	var streamError error
	stderrText := captureStderr(t, func() {
		streamError = streamLogTargets(context.Background(), "cluster-1", targets,
			client.PodLogOptions{Namespace: "default", Follow: true}, true, &output)
	})
	if streamError != nil {
		t.Errorf("a partial failure must not fail the command, got %v", streamError)
	}
	if !strings.Contains(stderrText, "web-b") || !strings.Contains(stderrText, "container gone") {
		t.Errorf("the failing target must be named on stderr, got %q", stderrText)
	}
	if !strings.Contains(output.String(), "[web-a] from a") {
		t.Errorf("the surviving target must still stream, got %q", output.String())
	}
	if strings.Contains(output.String(), "container gone") {
		t.Error("a failure must not be written into the log stream on stdout")
	}
}

func TestFollowingTargetsFailWhenEveryTargetFails(t *testing.T) {
	mock := newDebugVerbsMock()
	mock.failLogsForPod = map[string]error{
		"web-a": fmt.Errorf("container gone"),
		"web-b": fmt.Errorf("container gone"),
	}
	setMockClient(t, mock)

	targets := []logTarget{{podName: "web-a"}, {podName: "web-b"}}
	var output bytes.Buffer
	var streamError error
	captureStderr(t, func() {
		streamError = streamLogTargets(context.Background(), "cluster-1", targets,
			client.PodLogOptions{Namespace: "default", Follow: true}, true, &output)
	})
	if streamError == nil {
		t.Fatal("no target read anything, so the command must fail")
	}
	if !strings.Contains(streamError.Error(), "container gone") {
		t.Errorf("the failure should carry the cause, got %v", streamError)
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

// TestResolveK8sKindSingularisesACustomResourcePlural pins the fix for a
// false not-found. The platform derives the API plural from the kind, so a
// plural spelling became a double plural ("certificates" -> "certificateses")
// and the read came back empty - reported as "not found" for an object that
// exists.
func TestResolveK8sKindSingularisesACustomResourcePlural(t *testing.T) {
	// The caller's capitalisation is preserved rather than guessed at: the
	// CamelCase of a multi-word custom resource cannot be reconstructed from
	// a lowercase plural (clusterissuers is ClusterIssuer, not
	// Clusterissuer), and the platform lowercases the kind to derive the
	// plural anyway, so only the singular form is load-bearing.
	cases := map[string]string{
		"certificates":   "certificate",
		"Certificates":   "Certificate",
		"Certificate":    "Certificate",
		"ClusterIssuers": "ClusterIssuer",
		"gateways":       "gateway",
		"policies":       "policy",
		"Policies":       "Policy",
	}
	for spelling, expected := range cases {
		resolved, resolveError := resolveK8sKind(spelling, "example.com", "v1")
		if resolveError != nil {
			t.Errorf("resolveK8sKind(%q) failed: %v", spelling, resolveError)
			continue
		}
		if resolved.kind != expected {
			t.Errorf("resolveK8sKind(%q).kind = %q, want %q", spelling, resolved.kind, expected)
		}
	}
}

// TestLogsAllContainersPreviousPrintsTheContainersThatHaveOne pins the fix for
// the case the --previous flag exists to serve: a container that never
// terminated has no previous log and the apiserver answers an error, which
// used to abandon the remaining containers - including the crash-looping one.
func TestLogsAllContainersPreviousPrintsTheContainersThatHaveOne(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = client.ResourceResponseItem{
		Items: []interface{}{crashLoopingPod()},
	}
	mock.failLogsForPod = map[string]error{}
	mock.logOutput["web-7d9f-2xkvp/web"] = "panic: config missing\n"
	mock.failContainer = map[string]error{
		"wait-for-db": fmt.Errorf("previous terminated container not found"),
		"sidecar":     fmt.Errorf("previous terminated container not found"),
	}
	setMockClient(t, mock)

	var executeError error
	stderrText := captureStderr(t, func() {
		output := captureStdout(t, func() {
			_, executeError = executeCommand("cluster", "logs", "web-7d9f-2xkvp",
				"-n", "default", "--all-containers", "--previous")
		})
		if !strings.Contains(output, "panic: config missing") {
			t.Errorf("the container that does have a previous log must be printed:\n%s", output)
		}
	})
	if executeError != nil {
		t.Errorf("a partial failure must not lose the logs that were read: %v", executeError)
	}
	if !strings.Contains(stderrText, "wait-for-db") {
		t.Errorf("the containers with no previous log should be named on stderr, got %q", stderrText)
	}
}

func TestResolveK8sKindRejectsEmpty(t *testing.T) {
	if _, resolveError := resolveK8sKind("  ", "", ""); resolveError == nil {
		t.Fatal("an empty kind must fail")
	}
}

// --- review findings ---

// TestDescribeSecretRedactsItsValues is the regression test for the review's
// one high-severity finding. This command is pitched as the narrower
// alternative to handing out a kubectl grant, so it must not be an easier way
// to read secret material than the tool it replaces. kubectl describe redacts
// a Secret to key lengths; so does this, in the structured output too, which
// is where the full base64 payload would otherwise land in CI logs.
func TestDescribeSecretRedactsItsValues(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	mock.responses["Secret"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata":   map[string]interface{}{"name": "db", "namespace": "default"},
			"data": map[string]interface{}{
				"password": "c3VwZXItc2VjcmV0LXZhbHVl",
			},
			"stringData": map[string]interface{}{"token": "plaintext-token"},
		})},
	}
	mock.responses["Event"] = client.ResourceResponseItem{Items: []interface{}{}}
	setMockClient(t, mock)

	for _, outputFormat := range []string{"table", "json", "yaml"} {
		resetCommandFlags(t, clusterDescribeCmd)
		output := captureStdout(t, func() {
			if _, executeError := executeCommand("cluster", "describe", "secret", "db",
				"-n", "default", "-o", outputFormat); executeError != nil {
				t.Fatalf("describe secret -o %s failed: %v", outputFormat, executeError)
			}
		})
		if strings.Contains(output, "c3VwZXItc2VjcmV0LXZhbHVl") {
			t.Errorf("-o %s leaked the secret value:\n%s", outputFormat, output)
		}
		if strings.Contains(output, "plaintext-token") {
			t.Errorf("-o %s leaked the stringData value:\n%s", outputFormat, output)
		}
		if !strings.Contains(output, "redacted") {
			t.Errorf("-o %s should say the value was redacted:\n%s", outputFormat, output)
		}
		if !strings.Contains(output, "password") {
			t.Errorf("-o %s should still name the keys:\n%s", outputFormat, output)
		}
	}
}

// TestTopRefusesSandboxDataThatIsNotFlaggedAsCached pins why the cache guard
// checks the pointer rather than CacheMetadata.ServedFromCache. The platform
// attaches cache metadata on three paths and the sandbox one sets
// ServedFromCache false while still returning synthesised objects, so gating
// on the flag would render a sandbox cluster's fabricated pods as live
// measurements.
func TestTopRefusesSandboxDataThatIsNotFlaggedAsCached(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopPodsCmd)
	mock := newDebugVerbsMock()
	mock.responses["PodMetrics"] = client.ResourceResponseItem{
		CacheMetadata: &client.ResourceCacheMetadata{
			ServedFromCache: false, SyncStatus: "sandbox",
		},
		Items: []interface{}{podMetrics("fabricated", "100m", "64Mi")},
	}
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "top", "pods", "-n", "default")
	if executeError == nil {
		t.Fatal("sandbox data must not render as a live measurement even though ServedFromCache is false")
	}
	if !strings.Contains(executeError.Error(), "sandbox") {
		t.Errorf("the refusal should name the sandbox, got %q", executeError.Error())
	}
}

func TestLogsPreviousRejectsAnExplicitFollow(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "logs", "web-1", "-n", "default", "--previous", "--follow=true")
	if executeError == nil {
		t.Fatal("--previous with an explicit --follow=true must be rejected, not silently overridden")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("should exit %d, got %d", exitUsage, got)
	}
	if len(mock.logOptions) != 0 {
		t.Error("the contradiction must be caught before any API call")
	}
}

func TestLogsPreviousStillWorksWithTheDefaultFollow(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	mock.logOutput["web-1"] = "panic\n"
	setMockClient(t, mock)

	// --follow defaults to true in this CLI, so rejecting the explicit form
	// must not reject the ordinary one.
	if _, executeError := executeCommand("cluster", "logs", "web-1", "-n", "default", "--previous"); executeError != nil {
		t.Fatalf("--previous without an explicit --follow must work: %v", executeError)
	}
}

func TestLogsBoundedReadIsAlsoCapped(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	pods := []interface{}{}
	for index := 0; index < maxBoundedLogTargets+1; index++ {
		pods = append(pods, objectMap(map[string]interface{}{
			"metadata": map[string]interface{}{"name": fmt.Sprintf("web-%03d", index)},
			"spec": map[string]interface{}{
				"containers": []interface{}{map[string]interface{}{"name": "web"}},
			},
		}))
	}
	mock.responses["Pod"] = client.ResourceResponseItem{Items: pods}
	setMockClient(t, mock)

	// The cap used to apply only to following reads, so a selector matching a
	// busy namespace fanned out unbounded - and -o json buffered all of it.
	_, executeError := executeCommand("cluster", "logs", "-l", "app=web", "-n", "default", "--follow=false")
	if executeError == nil {
		t.Fatal("an unbounded bounded-mode fan-out must be refused")
	}
	if got := exitCodeFor(executeError); got != exitUsage {
		t.Errorf("should exit %d, got %d", exitUsage, got)
	}
	if len(mock.logOptions) != 0 {
		t.Error("no stream should be opened once the cap is exceeded")
	}
}

func TestTopNodesRefusesCachedCapacityRatherThanPairingItWithLiveUsage(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterTopNodesCmd)
	mock := newDebugVerbsMock()
	mock.responses["NodeMetrics"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"metadata": map[string]interface{}{"name": "worker-1"},
			"usage":    map[string]interface{}{"cpu": "500m", "memory": "1Gi"},
		})},
	}
	// Nodes are a cached kind, so the capacity read has to ask for a live
	// answer; a cached one must drop the percentage rather than pair a live
	// measurement with a stale denominator.
	mock.responses["Node"] = client.ResourceResponseItem{
		CacheMetadata: &client.ResourceCacheMetadata{
			ServedFromCache: true, SyncStatus: "cluster_offline",
		},
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
	nodeRow := ""
	for _, line := range strings.Split(stripANSICodes(output), "\n") {
		if strings.Contains(line, "worker-1") {
			nodeRow = line
		}
	}
	if strings.Contains(nodeRow, "%") {
		t.Errorf("a cached capacity must not become a percentage: %q", nodeRow)
	}
	if !strings.Contains(nodeRow, "500m") {
		t.Errorf("the live measurement should still render: %q", nodeRow)
	}
	// The capacity read must have asked for a live answer in the first place.
	sawSkipCacheNodeRead := false
	for index, request := range mock.requests {
		if request.Kind == "Node" && mock.skipCacheSeen[index] {
			sawSkipCacheNodeRead = true
		}
	}
	if !sawSkipCacheNodeRead {
		t.Error("the node capacity read must set skip_cache, like the measurement it divides")
	}
}

func TestResolveK8sKindKeepsAnIrregularPluralWhenGroupIsOverridden(t *testing.T) {
	// Clearing the explicit plural on --group handed the platform's heuristic
	// kinds it cannot pluralise from the name alone, which 404s against a
	// real object. The plural belongs to the kind, not to the group.
	for _, spelling := range []string{"ingress", "endpoints", "networkpolicy", "storageclass"} {
		withoutOverride, _ := resolveK8sKind(spelling, "", "")
		withOverride, resolveError := resolveK8sKind(spelling, "example.com", "")
		if resolveError != nil {
			t.Errorf("resolveK8sKind(%q, group) failed: %v", spelling, resolveError)
			continue
		}
		if withOverride.resource != withoutOverride.resource {
			t.Errorf("resolveK8sKind(%q) lost its plural under --group: %q vs %q",
				spelling, withOverride.resource, withoutOverride.resource)
		}
		if withOverride.group != "example.com" {
			t.Errorf("resolveK8sKind(%q) should still take the group override", spelling)
		}
	}
}

func TestResolveK8sKindRequiresBothOverridesForAnUnknownKind(t *testing.T) {
	// Defaulting an unknown CRD to v1 produces a confusing empty read when the
	// resource does not serve v1, rather than a clear usage message.
	if _, resolveError := resolveK8sKind("Widget", "example.com", ""); resolveError == nil {
		t.Fatal("--group without --api-version must be a usage error for an unknown kind")
	} else if got := exitCodeFor(resolveError); got != exitUsage {
		t.Errorf("should exit %d, got %d", exitUsage, got)
	}
	if _, resolveError := resolveK8sKind("Widget", "", "v1alpha1"); resolveError == nil {
		t.Fatal("--api-version without --group must be a usage error for an unknown kind")
	}
	if _, resolveError := resolveK8sKind("Widget", "example.com", "v1alpha1"); resolveError != nil {
		t.Errorf("both overrides together must resolve: %v", resolveError)
	}
}

func TestParseKubernetesQuantityRejectsAnUnknownUnit(t *testing.T) {
	// The review suggested a bare number could fall through for an unknown
	// unit. ParseFloat rejects the trailing garbage, so it cannot.
	for _, malformed := range []string{"100x", "12abc", "5Gib", "1..2", "--3"} {
		if parsed, isValid := parseKubernetesQuantity(malformed); isValid {
			t.Errorf("parseKubernetesQuantity(%q) = %v, want rejected", malformed, parsed)
		}
	}
}

func TestGetEventsNamedLookupFilteredOutDegradesToAnEmptyListing(t *testing.T) {
	// The review asked whether a postFilter dropping the only match confuses
	// the named-lookup path. It does not: the listing renderer reports that
	// nothing matched, which is exactly what happened.
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, getEventsCommand(t))
	mock := newDebugVerbsMock()
	mock.responses["Event"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"type": "Normal", "reason": "Pulled",
			"metadata":       map[string]interface{}{"name": "web-1.17abc"},
			"involvedObject": map[string]interface{}{"kind": "Pod", "name": "web-1"},
		})},
	}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "get", "events", "web-1.17abc",
			"-n", "default", "--type", "Warning"); executeError != nil {
			t.Fatalf("get events <name> --type failed: %v", executeError)
		}
	})
	// A named get lookup that matches nothing renders the empty envelope,
	// which is what every other named lookup in the get family already does
	// when the object is absent - the filter reaches the same place by a
	// different route rather than a new confusing one.
	if strings.Contains(output, "Pulled") {
		t.Errorf("the filtered-out event must not render:\n%s", output)
	}
	if !strings.Contains(output, "items: []") {
		t.Errorf("the empty envelope should still be well-formed, got:\n%s", output)
	}
}

// TestDescribeSecretRedactsTheAppliedManifestAnnotation covers the bypass the
// re-review found: kubectl apply stores the whole submitted manifest, base64
// values and all, in an annotation, so redacting only data/stringData left
// the values readable one field away.
func TestDescribeSecretRedactsTheAppliedManifestAnnotation(t *testing.T) {
	writeSelectedClusterJSON(t)
	const secretValue = "c3VwZXItc2VjcmV0LXZhbHVl"
	mock := newDebugVerbsMock()
	mock.responses["Secret"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name": "db", "namespace": "default",
				"annotations": map[string]interface{}{
					lastAppliedConfigurationAnnotation: `{"data":{"password":"` + secretValue + `"}}`,
					"backup.example/copy":              "password=" + secretValue,
					"harmless":                         "managed-by=controller",
				},
			},
			"data": map[string]interface{}{"password": secretValue},
		})},
	}
	mock.responses["Event"] = client.ResourceResponseItem{Items: []interface{}{}}
	setMockClient(t, mock)

	for _, outputFormat := range []string{"table", "json", "yaml"} {
		resetCommandFlags(t, clusterDescribeCmd)
		output := captureStdout(t, func() {
			if _, executeError := executeCommand("cluster", "describe", "secret", "db",
				"-n", "default", "-o", outputFormat); executeError != nil {
				t.Fatalf("describe secret -o %s failed: %v", outputFormat, executeError)
			}
		})
		if strings.Contains(output, secretValue) {
			t.Errorf("-o %s leaked the secret value through an annotation:\n%s", outputFormat, output)
		}
		if outputFormat != "table" && !strings.Contains(output, "harmless") {
			t.Errorf("-o %s should keep annotations that carry no secret material:\n%s", outputFormat, output)
		}
	}
}

func TestDescribeSecretKeyListingUsesTheResolvedKind(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	// A cached item need not carry its own kind field. Redaction and the key
	// listing must agree about what this object is, or one fires and the
	// other silently does not.
	mock.responses["Secret"] = client.ResourceResponseItem{
		Items: []interface{}{objectMap(map[string]interface{}{
			"apiVersion": "v1",
			"metadata":   map[string]interface{}{"name": "db", "namespace": "default"},
			"data":       map[string]interface{}{"password": "c3VwZXItc2VjcmV0LXZhbHVl"},
		})},
	}
	mock.responses["Event"] = client.ResourceResponseItem{Items: []interface{}{}}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "describe", "secret", "db", "-n", "default"); executeError != nil {
			t.Fatalf("describe secret failed: %v", executeError)
		}
	})
	plain := stripANSICodes(output)
	if strings.Contains(plain, "c3VwZXItc2VjcmV0LXZhbHVl") {
		t.Errorf("the value leaked:\n%s", plain)
	}
	if !strings.Contains(plain, "password") || !strings.Contains(plain, "redacted") {
		t.Errorf("the key listing must fire on the resolved kind too:\n%s", plain)
	}
}

func TestEventsShowTheInvolvedObjectNamespace(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterEventsCmd)
	mock := newDebugVerbsMock()
	// --for omits involvedObject.namespace under --all-namespaces, so a
	// same-named object elsewhere also matches. The column is what stops one
	// namespace's events being read as another's.
	mock.responses["Event"] = client.ResourceResponseItem{
		Items: []interface{}{
			scopedEvent(),
			objectMap(map[string]interface{}{
				"type": "Warning", "reason": "BackOff", "message": "other namespace",
				"lastTimestamp": "2026-08-22T09:07:00Z",
				"involvedObject": map[string]interface{}{
					"kind": "Pod", "name": "web-7d9f-2xkvp", "namespace": "staging",
				},
			}),
		},
	}
	setMockClient(t, mock)

	var output string
	stderrText := captureStderr(t, func() {
		output = captureStdout(t, func() {
			if _, executeError := executeCommand("cluster", "events",
				"--for", "pod/web-7d9f-2xkvp", "-A"); executeError != nil {
				t.Fatalf("events --for -A failed: %v", executeError)
			}
		})
	})
	plain := stripANSICodes(output)
	if !strings.Contains(plain, "default") || !strings.Contains(plain, "staging") {
		t.Errorf("both namespaces must be distinguishable in the output:\n%s", plain)
	}
	if !strings.Contains(stderrText, "any namespace") {
		t.Errorf("the cross-namespace match should be called out on stderr, got %q", stderrText)
	}
}

// TestEventsNameFlagTargetsTheEventResourceNotTheWorkload pins what --name
// actually does. The platform matches it against the Event resource's own
// generated name, so a user reaching for it to find a pod's events gets an
// empty result that reads as "no events" rather than "wrong flag" - which is
// why the help text now says so and points at --for.
func TestEventsNameFlagTargetsTheEventResourceNotTheWorkload(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterEventsCmd)
	mock := newDebugVerbsMock()
	mock.responses["Event"] = client.ResourceResponseItem{Items: []interface{}{scopedEvent()}}
	setMockClient(t, mock)

	captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "events", "-n", "default",
			"--name", "web-1.17abc"); executeError != nil {
			t.Fatalf("events --name failed: %v", executeError)
		}
	})
	if mock.requests[0].Name != "web-1.17abc" {
		t.Errorf("--name must reach the request as the resource name, got %q", mock.requests[0].Name)
	}
	if fieldSelectorValue(mock.requests[0], "involvedObject.name") != "" {
		t.Error("--name must not masquerade as an involvedObject scope; that is --for")
	}

	for _, command := range []*cobra.Command{clusterEventsCmd, getEventsCommand(t)} {
		usage := command.Flags().Lookup("name").Usage
		if !strings.Contains(usage, "Event resource's own name") || !strings.Contains(usage, "--for") {
			t.Errorf("%s --name help must say what it matches and point at --for, got %q",
				command.CommandPath(), usage)
		}
	}
}

package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// podAnsweredWith is a pod read whose reply carries the given cache metadata.
func podAnsweredWith(metadata *client.ResourceCacheMetadata) client.ResourceResponseItem {
	return client.ResourceResponseItem{
		Status: "success",
		Kind:   "Pod",
		Items: []interface{}{map[string]interface{}{
			"kind":     "Pod",
			"metadata": map[string]interface{}{"name": "web", "namespace": "default"},
			"status":   map[string]interface{}{"phase": "Running"},
		}},
		CacheMetadata: metadata,
	}
}

func frozenPodMetadata() *client.ResourceCacheMetadata {
	age := 1461
	return &client.ResourceCacheMetadata{
		ServedFromCache: true, Stale: true, StalenessSeconds: 7, CacheAgeSeconds: &age, SyncStatus: "agent_timeout",
	}
}

func freshPodMetadata() *client.ResourceCacheMetadata {
	age := 9
	return &client.ResourceCacheMetadata{
		ServedFromCache: true, StalenessSeconds: 9, CacheAgeSeconds: &age, SyncStatus: "synced",
	}
}

// runBareObjectRead runs one read and returns what reached stdout and stderr.
func runBareObjectRead(t *testing.T, metadata *client.ResourceCacheMetadata, arguments ...string) (string, string) {
	t.Helper()
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, getPodsCommand(t))
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = podAnsweredWith(metadata)
	setMockClient(t, mock)

	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if _, executeError := executeCommand(arguments...); executeError != nil {
				t.Fatalf("%v failed: %v", arguments, executeError)
			}
		})
	})
	return stdout, stderr
}

// The read behind an incident report: during an API server outage, a named
// -o json read printed a pod frozen 24 minutes earlier with nothing to say so,
// and the reader took it for the pod's current state.
func TestFrozenNamedObjectSaysSoOnStderrAndKeepsStdoutTheObject(t *testing.T) {
	commands := map[string][]string{
		"get json":      {"cluster", "get", "pods", "web", "-n", "default", "-o", "json"},
		"get yaml":      {"cluster", "get", "pods", "web", "-n", "default", "-o", "yaml"},
		"describe json": {"cluster", "describe", "pod", "web", "-n", "default", "-o", "json"},
		"describe yaml": {"cluster", "describe", "pod", "web", "-n", "default", "-o", "yaml"},
	}
	for name, arguments := range commands {
		t.Run(name, func(t *testing.T) {
			stdout, stderr := runBareObjectRead(t, frozenPodMetadata(), arguments...)
			if !strings.Contains(stderr, "Served from the agent's cache, 24m") ||
				!strings.Contains(stderr, "the cluster's agent did not respond") {
				t.Errorf("a frozen object must say so on stderr, got %q", stderr)
			}
			if strings.Contains(stdout, "Served from") {
				t.Errorf("the notice must not reach stdout:\n%s", stdout)
			}
			if !strings.Contains(stdout, "Running") {
				t.Errorf("the object must still be printed:\n%s", stdout)
			}
		})
	}
}

func TestFreshNamedObjectStaysSilentOnStderr(t *testing.T) {
	stdout, stderr := runBareObjectRead(t, freshPodMetadata(), "cluster", "get", "pods", "web", "-n", "default", "-o", "json")
	if strings.Contains(stderr, "Served from") || strings.Contains(stderr, "did not respond") {
		t.Errorf("a fresh cache hit must not disturb a script reading stderr, got %q", stderr)
	}
	var object map[string]interface{}
	if unmarshalError := json.Unmarshal([]byte(stdout), &object); unmarshalError != nil {
		t.Fatalf("stdout must stay the bare object, got %v:\n%s", unmarshalError, stdout)
	}
	if object["kind"] != "Pod" {
		t.Errorf("stdout must be the object itself, got %v", object)
	}
}

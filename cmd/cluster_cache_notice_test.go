package cmd

// ankra-306z5f: `cluster get` reads answered from the agent's cache say so,
// with their age, on stderr above the rows. Structured output is unchanged
// apart from the cache fields it carries, and stdout stays the rows alone.

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

func cachedNodesResponse(metadata *client.ResourceCacheMetadata) *client.GetResourcesResponse {
	return &client.GetResourcesResponse{ResourceResponses: []client.ResourceResponseItem{{
		Status: "success", Kind: "Node", Version: "v1", CacheMetadata: metadata,
		Items: []interface{}{map[string]interface{}{
			"metadata": map[string]interface{}{"name": "cp-0"},
			"status": map[string]interface{}{"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
	}}}
}

func intValue(value int) *int { return &value }

// runCaptured runs one command and returns what it printed on each stream.
func runCaptured(t *testing.T, args ...string) (string, string) {
	t.Helper()
	var stdoutOutput string
	stderrOutput := captureStderr(t, func() {
		stdoutOutput = captureStdout(t, func() {
			_, _ = executeCommand(args...)
		})
	})
	return stdoutOutput, stderrOutput
}

func TestGetNodesTableAnnouncesAStaleCachedAnswerWithItsAgeAndReason(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &capturedResourcesMock{response: cachedNodesResponse(&client.ResourceCacheMetadata{
		ServedFromCache: true, Stale: true, StalenessSeconds: 3, CacheAgeSeconds: intValue(95),
		SyncStatus: "agent_timeout",
	})})
	nodesCmd := findClusterGetSubcommand(t, "nodes")
	t.Cleanup(func() { _ = nodesCmd.Flags().Set("output", "table") })

	stdoutOutput, stderrOutput := runCaptured(t, "cluster", "get", "nodes")

	want := "Served from the agent's cache, 1m35s old: the cluster's agent did not respond."
	if !strings.Contains(stderrOutput, want) {
		t.Errorf("stderr must carry the cache notice %q, got: %q", want, stderrOutput)
	}
	if strings.Contains(stdoutOutput, "Served from the agent's cache") {
		t.Errorf("the notice must stay off stdout so piped rows are unchanged, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "cp-0") {
		t.Errorf("the rows must still render, got: %s", stdoutOutput)
	}
}

func TestGetNodesTableAnnouncesAHealthyCachedAnswerWithItsAge(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &capturedResourcesMock{response: cachedNodesResponse(&client.ResourceCacheMetadata{
		ServedFromCache: true, StalenessSeconds: 20, CacheAgeSeconds: intValue(20), SyncStatus: "synced",
	})})
	nodesCmd := findClusterGetSubcommand(t, "nodes")
	t.Cleanup(func() { _ = nodesCmd.Flags().Set("output", "table") })

	_, stderrOutput := runCaptured(t, "cluster", "get", "nodes")

	if !strings.Contains(stderrOutput, "Served from the agent's cache, 20s old.") {
		t.Errorf("a healthy cached answer still says how old it is, got: %q", stderrOutput)
	}
}

func TestGetNodesLiveAnswerPrintsNoNotice(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &capturedResourcesMock{response: cachedNodesResponse(nil)})
	nodesCmd := findClusterGetSubcommand(t, "nodes")
	t.Cleanup(func() { _ = nodesCmd.Flags().Set("output", "table") })

	_, stderrOutput := runCaptured(t, "cluster", "get", "nodes")

	if strings.Contains(stderrOutput, "agent's cache") {
		t.Errorf("a live answer must print no cache notice, got: %q", stderrOutput)
	}
}

func TestGetNodesJSONKeepsTheCacheFieldsAndPrintsNoNotice(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &capturedResourcesMock{response: cachedNodesResponse(&client.ResourceCacheMetadata{
		ServedFromCache: true, Stale: true, StalenessSeconds: 3, CacheAgeSeconds: intValue(95),
		SyncStatus: "agent_timeout", ReconcilePending: true,
	})})
	nodesCmd := findClusterGetSubcommand(t, "nodes")
	t.Cleanup(func() { _ = nodesCmd.Flags().Set("output", "table") })

	stdoutOutput, stderrOutput := runCaptured(t, "cluster", "get", "nodes", "-o", "json")

	if strings.Contains(stderrOutput, "agent's cache") {
		t.Errorf("-o json carries the metadata itself and prints no notice, got: %q", stderrOutput)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(stdoutOutput), &decoded); err != nil {
		t.Fatalf("-o json must stay parseable: %v: %s", err, stdoutOutput)
	}
	metadata := decoded["resource_responses"].([]interface{})[0].(map[string]interface{})["cache_metadata"].(map[string]interface{})
	for field, want := range map[string]interface{}{
		"served_from_cache": true, "stale": true, "staleness_seconds": float64(3),
		"cache_age_seconds": float64(95), "sync_status": "agent_timeout", "reconcile_pending": true,
	} {
		if metadata[field] != want {
			t.Errorf("cache_metadata.%s = %v, want %v", field, metadata[field], want)
		}
	}
}

// A named read renders YAML by default, but a person reads it, so a cached
// answer is announced there too; an explicit -o yaml is a machine read.
func TestGetNodesNamedReadAnnouncesOnlyWhenNoFormatWasAsked(t *testing.T) {
	metadata := &client.ResourceCacheMetadata{
		ServedFromCache: true, StalenessSeconds: 150, CacheAgeSeconds: intValue(150), SyncStatus: "synced",
	}
	for _, testCase := range []struct {
		args       []string
		wantNotice bool
	}{
		{[]string{"cluster", "get", "nodes", "cp-0"}, true},
		{[]string{"cluster", "get", "nodes", "cp-0", "-o", "yaml"}, false},
	} {
		writeSelectedClusterJSON(t)
		setMockClient(t, &capturedResourcesMock{response: cachedNodesResponse(metadata)})
		nodesCmd := findClusterGetSubcommand(t, "nodes")
		stdoutOutput, stderrOutput := runCaptured(t, testCase.args...)
		_ = nodesCmd.Flags().Set("output", "table")

		hasNotice := strings.Contains(stderrOutput,
			"Served from the agent's cache, 2m30s old: nothing newer has come from the cluster since")
		if hasNotice != testCase.wantNotice {
			t.Errorf("%v: notice printed=%v, want %v (stderr %q)", testCase.args, hasNotice, testCase.wantNotice, stderrOutput)
		}
		if !strings.Contains(stdoutOutput, "name: cp-0") {
			t.Errorf("%v: the object must render as YAML, got: %s", testCase.args, stdoutOutput)
		}
	}
}

type cachedPodsMock struct {
	baseMock
	cacheInfo client.CacheInfo
}

func (m *cachedPodsMock) ListPods(clusterID string, opts *client.ListPodsOptions) (*client.ListPodsResponse, error) {
	return &client.ListPodsResponse{
		Pods:       []client.PodSummary{{Name: "web-1", Phase: "Running"}},
		TotalCount: 1, Page: 1, PageSize: 100, TotalPages: 1,
		CacheInfo: m.cacheInfo,
	}, nil
}

func TestGetPodsTableAnnouncesTheCacheAndAPendingResync(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &cachedPodsMock{cacheInfo: client.CacheInfo{
		ServedFromCache: true, StalenessSeconds: 7, SyncStatus: "synced", ReconcilePending: true,
	}})
	t.Cleanup(func() { _ = clusterPodsCmd.Flags().Set("output", "table") })

	want := "Served from the agent's cache, 7s old: it is resyncing after an agent restart, " +
		"so resources deleted in the meantime may still be listed."
	for _, args := range [][]string{{"cluster", "get", "pods"}, {"cluster", "get", "pods", "-A"}} {
		stdoutOutput, stderrOutput := runCaptured(t, args...)
		_ = clusterPodsCmd.Flags().Set("all-namespaces", "false")
		if !strings.Contains(stderrOutput, want) {
			t.Errorf("%v: stderr must carry %q, got: %q", args, want, stderrOutput)
		}
		if !strings.Contains(stdoutOutput, "web-1") || strings.Contains(stdoutOutput, "agent's cache") {
			t.Errorf("%v: stdout must be the pod rows alone, got: %s", args, stdoutOutput)
		}
	}
}

func TestGetPodsJSONCarriesReconcilePendingAndPrintsNoNotice(t *testing.T) {
	writeSelectedClusterJSON(t)
	setMockClient(t, &cachedPodsMock{cacheInfo: client.CacheInfo{
		ServedFromCache: true, StalenessSeconds: 7, SyncStatus: "synced", ReconcilePending: true,
	}})
	t.Cleanup(func() { _ = clusterPodsCmd.Flags().Set("output", "table") })

	stdoutOutput, stderrOutput := runCaptured(t, "cluster", "get", "pods", "-o", "json")

	if strings.Contains(stderrOutput, "agent's cache") {
		t.Errorf("-o json prints no notice, got: %q", stderrOutput)
	}
	var decoded client.ListPodsResponse
	if err := json.Unmarshal([]byte(stdoutOutput), &decoded); err != nil {
		t.Fatalf("-o json must stay parseable: %v: %s", err, stdoutOutput)
	}
	if !decoded.CacheInfo.ReconcilePending || decoded.CacheInfo.StalenessSeconds != 7 {
		t.Errorf("cache_info must keep its fields, got %+v", decoded.CacheInfo)
	}
}

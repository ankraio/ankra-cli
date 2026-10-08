package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// `ankra pipeline get` says how long each step waited for a CI slot - the
// time between Ankra handing it to the agent and the agent starting it - and
// which steps were handed back to the queue. These are the numbers that say
// whether a slow pipeline needs more workers or more nodes.
func TestPipelineRunDetailPrintsQueueWaitAndDeferrals(t *testing.T) {
	dispatchedAt := "2026-10-02T10:00:00Z"
	startedAt := "2026-10-02T10:02:03Z"
	waitingSince := "2026-10-02T10:05:00Z"
	deferredTwice := 2
	deferredOnce := 1
	never := 0
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 7, ID: "run-7", Status: "running"},
		Steps: []client.PipelineStep{
			{StepKey: "lint", Status: "concluded", DispatchedAt: &dispatchedAt, StartedAt: &startedAt,
				DeferredCount: &never},
			{StepKey: "unit", Status: "running", DispatchedAt: &waitingSince},
			{StepKey: "race", Status: "pending", DeferredCount: &deferredTwice},
			{StepKey: "vet", Status: "pending", DeferredCount: &deferredOnce},
		},
	}, client.PipelineSelector{})
	rendered := output.String()
	for _, expected := range []string{
		"Queueing:",
		"  lint: waited 2m3s for a CI slot",
		"  unit: waiting for a CI slot since",
		"  race: returned to the queue 2 times (its node was full)",
		"  vet: returned to the queue once (its node was full)",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("output missing %q:\n%s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "lint: returned") {
		t.Errorf("a step never deferred says nothing about deferral:\n%s", rendered)
	}
}

// A platform older than dispatched_at and deferred_count sends neither, and the
// run detail prints exactly what it printed before - no empty section, and
// no wait computed from started_at alone.
func TestPipelineRunDetailOnAnOlderPlatformPrintsNoQueueing(t *testing.T) {
	startedAt := "2026-10-02T10:02:03Z"
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 8, ID: "run-8", Status: "running"},
		Steps: []client.PipelineStep{
			{StepKey: "lint", Status: "running", StartedAt: &startedAt},
			{StepKey: "unit", Status: "pending"},
		},
	}, client.PipelineSelector{})
	if strings.Contains(output.String(), "Queueing") || strings.Contains(output.String(), "CI slot") {
		t.Errorf("an older platform's run detail is unchanged:\n%s", output.String())
	}
}

// A start before the hand-off is two clocks disagreeing, not a negative wait.
func TestPipelineStepQueueWaitIsUnmeasuredWhenTheClocksDisagree(t *testing.T) {
	dispatchedAt := "2026-10-02T10:02:03Z"
	startedAt := "2026-10-02T10:00:00Z"
	if wait, isMeasured := pipelineStepQueueWait(client.PipelineStep{
		DispatchedAt: &dispatchedAt, StartedAt: &startedAt,
	}); isMeasured {
		t.Fatalf("a start before the hand-off is unmeasured, got %s", wait)
	}
}

// The agent reports when a step's pod was created, placed, had its workspace
// attached, pulled its image and started the step: `ankra pipeline get` says
// how long the step sat Pending in the cluster and where that time went
// (ankra-q573dh.1).
func TestPipelineRunDetailPrintsTheInClusterPendingWait(t *testing.T) {
	podCreatedAt := "2026-10-08T09:00:03Z"
	scheduledAt := "2026-10-08T09:04:33Z"
	attachedAt := "2026-10-08T09:04:41Z"
	pullStartedAt := "2026-10-08T09:04:51.25Z"
	pulledAt := "2026-10-08T09:05:10.5Z"
	containerStartedAt := "2026-10-08T09:05:12Z"
	stuckFinishedAt := "2026-10-08T09:15:03Z"
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 9, ID: "run-9", Status: "concluded"},
		Steps: []client.PipelineStep{
			{StepKey: "test", Status: "concluded", PodCreatedAt: &podCreatedAt, PodScheduledAt: &scheduledAt,
				WorkspaceAttachedAt: &attachedAt, ImagePullStartedAt: &pullStartedAt, ImagePulledAt: &pulledAt,
				ContainerStartedAt: &containerStartedAt},
			{StepKey: "cached", Status: "concluded", PodCreatedAt: &podCreatedAt, ImagePulledAt: &pulledAt,
				ContainerStartedAt: &containerStartedAt},
			{StepKey: "stuck", Status: "concluded", Outcome: strPipelinePtr("timed_out"),
				PodCreatedAt: &podCreatedAt, PodScheduledAt: &scheduledAt, FinishedAt: &stuckFinishedAt},
			{StepKey: "lost", Status: "concluded", Outcome: strPipelinePtr("cancelled"), PodCreatedAt: &podCreatedAt},
			{StepKey: "unseen", Status: "concluded", Outcome: strPipelinePtr("success"),
				PodCreatedAt: &podCreatedAt, FinishedAt: &stuckFinishedAt},
			{StepKey: "verdictless", Status: "concluded", PodCreatedAt: &podCreatedAt, FinishedAt: &stuckFinishedAt},
			{StepKey: "old", Status: "concluded"},
			{StepKey: "live", Status: "running", PodCreatedAt: &podCreatedAt},
		},
	}, client.PipelineSelector{})
	rendered := output.String()
	for _, expected := range []string{
		"Queueing:",
		"  test: Pending 5m9s in the cluster before it ran (scheduling 4m30s, volumes 8s, image pull 19s)",
		"  cached: Pending 5m9s in the cluster before it ran (image already on the node)",
		"  stuck: Pending at least 15m0s in the cluster; its container was never seen to start",
		"  lost: its container was never seen to start",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("output missing %q:\n%s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "live:") {
		t.Errorf("a step still in flight is not called a pod that never started:\n%s", rendered)
	}
	if strings.Contains(rendered, "old:") {
		t.Errorf("a step whose agent reported no pod says nothing about one:\n%s", rendered)
	}
	// A step that succeeded ran its container; a start the agent did not
	// observe is not a container that never started.
	if strings.Contains(rendered, "unseen:") {
		t.Errorf("a successful step is not called a pod that never started:\n%s", rendered)
	}
	if strings.Contains(rendered, "verdictless:") {
		t.Errorf("a step with no outcome is not called a pod that never started:\n%s", rendered)
	}
}

// A platform or an agent older than the pod timing sends none of the six
// fields, and older agents send some as null: the run detail prints no pod
// line, and -o json carries exactly what the platform sent.
func TestPipelineStepPodTimingRoundTripsThroughJSON(t *testing.T) {
	body := []byte(`{"id":"run-9","run_number":9,"status":"concluded","steps":[
		{"step_key":"test","status":"concluded","pod_created_at":"2026-10-08T09:00:03Z",
		 "pod_scheduled_at":"2026-10-08T09:04:33Z","workspace_attached_at":"2026-10-08T09:04:41Z",
		 "image_pull_started_at":"2026-10-08T09:04:51.25Z","image_pulled_at":"2026-10-08T09:05:10.5Z",
		 "container_started_at":"2026-10-08T09:05:12Z"},
		{"step_key":"older","status":"concluded","pod_created_at":null,"pod_scheduled_at":null,
		 "workspace_attached_at":null,"image_pull_started_at":null,"image_pulled_at":null,
		 "container_started_at":null}]}`)
	var detail client.PipelineRunDetail
	if decodeError := json.Unmarshal(body, &detail); decodeError != nil {
		t.Fatalf("decode: %v", decodeError)
	}
	var encoded bytes.Buffer
	if encodeError := encodeStructured(&encoded, outputJSON, detail); encodeError != nil {
		t.Fatalf("encode: %v", encodeError)
	}
	var steps struct {
		Steps []map[string]any `json:"steps"`
	}
	if decodeError := json.Unmarshal(encoded.Bytes(), &steps); decodeError != nil {
		t.Fatalf("re-decode: %v", decodeError)
	}
	for field, want := range map[string]string{
		"pod_created_at":        "2026-10-08T09:00:03Z",
		"pod_scheduled_at":      "2026-10-08T09:04:33Z",
		"workspace_attached_at": "2026-10-08T09:04:41Z",
		"image_pull_started_at": "2026-10-08T09:04:51.25Z",
		"image_pulled_at":       "2026-10-08T09:05:10.5Z",
		"container_started_at":  "2026-10-08T09:05:12Z",
	} {
		if got := steps.Steps[0][field]; got != want {
			t.Errorf("-o json %s = %v, want %s", field, got, want)
		}
		if _, isPresent := steps.Steps[1][field]; isPresent {
			t.Errorf("-o json invents %s for a step whose agent did not report it", field)
		}
	}
	var output bytes.Buffer
	printPipelineRunDetail(&output, detail, client.PipelineSelector{})
	if strings.Contains(output.String(), "older:") {
		t.Errorf("a step with null pod timing prints no pod line:\n%s", output.String())
	}
}

// What each step used against what it asked for (ankra-q573dh.5): the memory
// peak against its request, an at-limit peak called what it is, and the CPU
// average only when the agent reported the script's run time.
func TestPipelineRunDetailPrintsWhatEachStepUsedAgainstItsRequests(t *testing.T) {
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 9, ID: "run-9", Status: "concluded"},
		Steps: []client.PipelineStep{
			{StepKey: "e2e", Status: "concluded", MemoryRequestBytes: int64PipelinePtr(14 << 30),
				MemoryPeakBytes: int64PipelinePtr(8 << 30), CPURequestMillicores: int64PipelinePtr(2000),
				CPUUsageMicroseconds: int64PipelinePtr(480_000_000), UsageElapsedMicroseconds: int64PipelinePtr(600_000_000),
				CPUThrottledMicroseconds: int64PipelinePtr(65_000_000)},
			{StepKey: "build", Status: "concluded", MemoryRequestBytes: int64PipelinePtr(10 << 30),
				MemoryPeakBytes: int64PipelinePtr(10 << 30), CPUUsageMicroseconds: int64PipelinePtr(480_000_000)},
			{StepKey: "old", Status: "concluded"},
		},
	}, client.PipelineSelector{})
	rendered := output.String()
	for _, expected := range []string{
		"Resources used:",
		"  e2e: memory peak 8.0 GiB of 14.0 GiB requested (57%); CPU 0.80 cores on average of 2.00 requested, " +
			"held at its CPU limit for 1m5s",
		"  build: memory peak 10.0 GiB of 10.0 GiB requested (100%), at its limit: page cache fills spare room, " +
			"so this is not what it needed",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("output missing %q:\n%s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "build: memory peak 10.0 GiB of 10.0 GiB requested (100%), at its limit: page cache "+
		"fills spare room, so this is not what it needed; CPU") {
		t.Errorf("a CPU total without the script's run time is not averaged:\n%s", rendered)
	}
	if strings.Contains(rendered, "old:") {
		t.Errorf("a step with no usage report says nothing:\n%s", rendered)
	}
}

// A run whose steps carry no usage report - an older platform or agent -
// prints no resources section at all.
func TestPipelineRunDetailPrintsNoResourcesSectionWithoutUsage(t *testing.T) {
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 9, ID: "run-9", Status: "concluded"},
		Steps:       []client.PipelineStep{{StepKey: "old", Status: "concluded"}},
	}, client.PipelineSelector{})
	if strings.Contains(output.String(), "Resources used:") {
		t.Errorf("no usage, no section:\n%s", output.String())
	}
}

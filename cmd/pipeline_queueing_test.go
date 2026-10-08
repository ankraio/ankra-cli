package cmd

import (
	"bytes"
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
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 9, ID: "run-9", Status: "concluded"},
		Steps: []client.PipelineStep{
			{StepKey: "test", Status: "concluded", PodCreatedAt: &podCreatedAt, PodScheduledAt: &scheduledAt,
				WorkspaceAttachedAt: &attachedAt, ImagePullStartedAt: &pullStartedAt, ImagePulledAt: &pulledAt,
				ContainerStartedAt: &containerStartedAt},
			{StepKey: "cached", Status: "concluded", PodCreatedAt: &podCreatedAt, ImagePulledAt: &pulledAt,
				ContainerStartedAt: &containerStartedAt},
			{StepKey: "stuck", Status: "concluded", PodCreatedAt: &podCreatedAt, PodScheduledAt: &scheduledAt},
			{StepKey: "old", Status: "concluded"},
		},
	}, client.PipelineSelector{})
	rendered := output.String()
	for _, expected := range []string{
		"Queueing:",
		"  test: Pending 5m9s in the cluster before it ran (scheduling 4m30s, volumes 8s, image pull 19s)",
		"  cached: Pending 5m9s in the cluster before it ran (image already on the node)",
		"  stuck: its pod never started its container",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("output missing %q:\n%s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "old:") {
		t.Errorf("a step whose agent reported no pod says nothing about one:\n%s", rendered)
	}
}

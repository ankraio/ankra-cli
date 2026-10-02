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

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"ankra/internal/client"
)

// `ankra pipeline run get` names the node each step ran on, as the agent
// reported it, so a run whose steps hopped between nodes - and re-attached
// their workspace each time - reads as such (ankra-alhkef.1, PLA-900).
func TestPipelineRunDetailPrintsTheNodeEachStepRanOn(t *testing.T) {
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 9, ID: "run-9", Status: "running"},
		Steps: []client.PipelineStep{
			{StepKey: "checkout", Status: "concluded", NodeName: "ci-pool-7f9c2"},
			{StepKey: "unit", Status: "pending"},
		},
	}, client.PipelineSelector{})
	rendered := output.String()
	if !strings.Contains(rendered, "RAN ON") {
		t.Fatalf("the step table has no RAN ON column:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		switch {
		case strings.Contains(line, "checkout") && !strings.Contains(line, "ci-pool-7f9c2"):
			t.Errorf("the checkout row does not name its node: %q", line)
		case strings.Contains(line, "unit") && strings.Contains(line, "ci-pool-7f9c2"):
			t.Errorf("a step that never ran claims a node: %q", line)
		}
	}
}

func TestRenderPipelineStepNodeReadsADashForNoNode(t *testing.T) {
	if rendered := renderPipelineStepNode(client.PipelineStep{NodeName: "  "}); rendered != "-" {
		t.Errorf("a step with no node renders %q, want -", rendered)
	}
	if rendered := renderPipelineStepNode(client.PipelineStep{NodeName: "ci-pool-1"}); rendered != "ci-pool-1" {
		t.Errorf("a step with a node renders %q, want ci-pool-1", rendered)
	}
}

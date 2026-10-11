package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// `ankra pipeline get` says what a push run did about promotion
// (ankra-q573dh.27): the platform's sentence, and the run it published from.
func TestPipelineRunDetailPrintsThePromotionDecision(t *testing.T) {
	body := `{"id":"run-9","run_number":9,"status":"concluded","trigger":"push","trigger_ref":"refs/heads/main",
		"head_sha":"abc","queued_at":"2026-10-10T10:00:00Z","steps":[],
		"promotion_outcome":"promoted",
		"promotion_message":"Promoted: a pull request run already built, scanned and tested exactly this tree, so its image was published instead of rebuilt.",
		"promoted_from_run_id":"11111111-1111-1111-1111-111111111111","promoted_from_run_number":8,
		"head_tree_sha":"7777777777777777777777777777777777777777"}`
	var detail client.PipelineRunDetail
	if decodeError := json.Unmarshal([]byte(body), &detail); decodeError != nil {
		t.Fatalf("decoding the run detail: %v", decodeError)
	}
	var output bytes.Buffer
	printPipelineRunDetail(&output, detail, client.PipelineSelector{})
	expected := "  Promotion: Promoted: a pull request run already built, scanned and tested exactly this tree, " +
		"so its image was published instead of rebuilt. (from run #8)"
	if !strings.Contains(output.String(), expected) {
		t.Fatalf("output missing %q:\n%s", expected, output.String())
	}

	fallback := "no_tested_run"
	sentence := "Built in full: no pull request or merge train run tested exactly this tree."
	output.Reset()
	printPipelineRunDetail(&output, client.PipelineRunDetail{PipelineRun: client.PipelineRun{
		RunNumber: 10, ID: "run-10", Status: "running", PromotionOutcome: &fallback, PromotionMessage: &sentence,
	}}, client.PipelineSelector{})
	if !strings.Contains(output.String(), "  Promotion: "+sentence+"\n") {
		t.Fatalf("expected the fallback's sentence alone, got:\n%s", output.String())
	}
}

// A run that never asked to be promoted, or one read from a server older than
// the fields, prints no promotion line at all.
func TestPipelineRunDetailPrintsNoPromotionLineWhenNoneWasReported(t *testing.T) {
	var output bytes.Buffer
	printPipelineRunDetail(&output, client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{RunNumber: 11, ID: "run-11", Status: "running"},
	}, client.PipelineSelector{})
	if strings.Contains(output.String(), "Promotion:") {
		t.Fatalf("expected no promotion line, got:\n%s", output.String())
	}
}

package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
)

// TestRenderPipelineStateGlyphs pins the glyph each run/step state renders
// with (PLA-856, support #1178): ⟳ belongs to work that has not concluded
// and to nothing else, so a concluded failure never looks in progress.
func TestRenderPipelineStateGlyphs(t *testing.T) {
	cases := []struct {
		name    string
		status  string
		outcome *string
		want    string
	}{
		{name: "queued run", status: "queued", want: "⟳ queued"},
		{name: "running run", status: "running", want: "⟳ running"},
		{name: "pending step", status: "pending", want: "⟳ pending"},
		{name: "running step", status: "running", want: "⟳ running"},
		{name: "blocked step", status: "blocked", want: "○ blocked"},
		{name: "success", status: "concluded", outcome: strPipelinePtr("success"), want: "✓ success"},
		{name: "failure", status: "concluded", outcome: strPipelinePtr("failure"), want: "✗ failure"},
		{name: "timed out", status: "concluded", outcome: strPipelinePtr("timed_out"), want: "✗ timed_out"},
		{name: "infra error", status: "concluded", outcome: strPipelinePtr("infra_error"), want: "✗ infra_error"},
		{name: "cancelled", status: "concluded", outcome: strPipelinePtr("cancelled"), want: "⊘ cancelled"},
		{name: "skipped", status: "concluded", outcome: strPipelinePtr("skipped"), want: "○ skipped"},
		// The server says a concluded row always carries an outcome; should
		// one not, the word is printed with no glyph rather than a spinner.
		{name: "concluded without outcome", status: "concluded", want: "concluded"},
		{name: "empty outcome is no outcome", status: "running", outcome: strPipelinePtr(""), want: "⟳ running"},
		// A vocabulary word the CLI does not know is printed bare: a glyph
		// would claim a meaning it does not have.
		{name: "unknown outcome", status: "concluded", outcome: strPipelinePtr("exploded"), want: "exploded"},
		{name: "unknown status", status: "hibernating", want: "hibernating"},
		{name: "case-insensitive", status: "concluded", outcome: strPipelinePtr("FAILURE"), want: "✗ FAILURE"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := text.StripEscape(renderPipelineState(testCase.status, testCase.outcome))
			if got != testCase.want {
				t.Errorf("renderPipelineState(%q, %v) = %q, want %q", testCase.status, testCase.outcome, got, testCase.want)
			}
		})
	}
}

// TestRenderPipelineStateSpinnerOnlyWhileLive walks the whole outcome
// vocabulary and refuses the spinner on every one of them, so a new outcome
// added to the switch can never fall back to "in progress".
func TestRenderPipelineStateSpinnerOnlyWhileLive(t *testing.T) {
	for _, outcome := range []string{
		pipelineOutcomeSuccess, pipelineOutcomeFailure, pipelineOutcomeCancelled,
		pipelineOutcomeTimedOut, pipelineOutcomeSkipped, pipelineOutcomeInfraError,
	} {
		rendered := text.StripEscape(renderPipelineState(pipelineRunStatusConcluded, strPipelinePtr(outcome)))
		if strings.Contains(rendered, "⟳") {
			t.Errorf("outcome %q rendered with the in-progress spinner: %q", outcome, rendered)
		}
		if !strings.HasSuffix(rendered, " "+outcome) {
			t.Errorf("outcome %q rendered as %q, want a glyph then the word", outcome, rendered)
		}
	}
}

// TestPipelineGetConcludedFailureDoesNotSpin is the ticket's own scenario end
// to end: `pipeline get` on a concluded, failed run prints the run's Status
// line and every terminal step without the spinner.
func TestPipelineGetConcludedFailureDoesNotSpin(t *testing.T) {
	mockClient := &pipelineLaneMock{getResult: &client.PipelineRunDetail{
		PipelineRun: client.PipelineRun{
			ID: "run-1", RunNumber: 7, Status: "concluded", Outcome: strPipelinePtr("failure"),
			Trigger: "push", TriggerRef: "refs/heads/main", HeadSHA: strings.Repeat("a", 40),
			AuthorityState: strPipelinePtr("approved"),
			QueuedAt:       "2026-09-01T00:00:00Z",
		},
		Steps: []client.PipelineStep{
			{StepKey: "checkout", Stage: "checkout", Kind: "checkout", Status: "concluded", Outcome: strPipelinePtr("success")},
			{StepKey: "build", Stage: "build", Kind: "build", Status: "concluded", Outcome: strPipelinePtr("infra_error")},
			{StepKey: "test", Stage: "test", Kind: "test", Status: "concluded", Outcome: strPipelinePtr("failure"), ExitCode: int32PipelinePtr(1)},
			{StepKey: "publish", Stage: "publish", Kind: "build", Status: "concluded", Outcome: strPipelinePtr("skipped")},
		},
	}}
	output, executeError := runPipelineCommand(t, mockClient, "get", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("get error = %v", executeError)
	}
	output = text.StripEscape(output)
	if strings.Contains(output, "⟳") {
		t.Errorf("a concluded run rendered with the in-progress spinner:\n%s", output)
	}
	for _, want := range []string{"Status:    ✗ failure", "✓ success", "✗ infra_error", "✗ failure", "○ skipped", "Authority: approved"} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}

// TestPipelineListSpinsOnlyForLiveRuns pins the same mapping on the listing,
// where a queued run and a concluded one sit in the same column.
func TestPipelineListSpinsOnlyForLiveRuns(t *testing.T) {
	mockClient := &pipelineLaneMock{listResult: &client.PipelineRunList{Runs: []client.PipelineRun{
		{ID: "run-live", RunNumber: 13, Status: "running", Trigger: "push", QueuedAt: "2026-09-01T00:00:00Z"},
		{ID: "run-done", RunNumber: 12, Status: "concluded", Outcome: strPipelinePtr("cancelled"), Trigger: "push", QueuedAt: "2026-09-01T00:00:00Z"},
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "list", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("list error = %v", executeError)
	}
	output = text.StripEscape(output)
	if !strings.Contains(output, "⟳ running") || !strings.Contains(output, "⊘ cancelled") {
		t.Errorf("output = %q", output)
	}
	if strings.Count(output, "⟳") != 1 {
		t.Errorf("want exactly one spinner, for the running run:\n%s", output)
	}
}

// TestPipelineGetHelpDocumentsStatusOutcomeAndAuthority pins that the help
// names the split and every value, since that is where the ticket asked for
// it to be written down.
func TestPipelineGetHelpDocumentsStatusOutcomeAndAuthority(t *testing.T) {
	for _, want := range []string{
		"'status' is the lifecycle", "queued, running or concluded", "blocked", "pending",
		"'outcome'", "null until the\nstatus is concluded",
		"success", "failure", "cancelled", "timed_out", "skipped", "infra_error",
		"status/conclusion",
		"'authority_state'", "'approved'", "'unapproved'", "'changed_on_head'", "null - ",
	} {
		if !strings.Contains(pipelineGetLongHelp, want) {
			t.Errorf("pipeline get help lacks %q", want)
		}
	}
	listHelp := newPipelineListCommand().Long
	for _, want := range []string{"queued, running or concluded", "'outcome'", "infra_error"} {
		if !strings.Contains(listHelp, want) {
			t.Errorf("pipeline list help lacks %q", want)
		}
	}
}

func int32PipelinePtr(value int32) *int32 { return &value }

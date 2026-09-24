package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

type costDecisionsMock struct {
	baseMock
	listing        *client.DecisionProposalList
	listError      error
	filters        []client.DecisionListFilter
	proposal       *client.DecisionProposal
	getError       error
	activity       *client.DecisionActivity
	written        *client.DecisionProposal
	writeError     error
	approvedNotes  []*string
	setAsideNotes  []*string
	executeOptions []client.DecisionExecuteOptions
	clusters       []client.ClusterListItem
}

func (m *costDecisionsMock) ListDecisions(filter client.DecisionListFilter) (*client.DecisionProposalList, error) {
	m.filters = append(m.filters, filter)
	if m.listError != nil {
		return nil, m.listError
	}
	return m.listing, nil
}

func (m *costDecisionsMock) GetDecision(string) (*client.DecisionProposal, error) {
	if m.getError != nil {
		return nil, m.getError
	}
	return m.proposal, nil
}

func (m *costDecisionsMock) GetDecisionActivity(string) (*client.DecisionActivity, error) {
	if m.getError != nil {
		return nil, m.getError
	}
	return m.activity, nil
}

func (m *costDecisionsMock) ApproveDecision(_ string, note *string) (*client.DecisionProposal, error) {
	m.approvedNotes = append(m.approvedNotes, note)
	if m.writeError != nil {
		return nil, m.writeError
	}
	return m.written, nil
}

func (m *costDecisionsMock) SetAsideDecision(_ string, note *string) (*client.DecisionProposal, error) {
	m.setAsideNotes = append(m.setAsideNotes, note)
	if m.writeError != nil {
		return nil, m.writeError
	}
	return m.written, nil
}

func (m *costDecisionsMock) ExecuteDecision(_ string, options client.DecisionExecuteOptions) (*client.DecisionProposal, error) {
	m.executeOptions = append(m.executeOptions, options)
	if m.writeError != nil {
		return nil, m.writeError
	}
	return m.written, nil
}

func (m *costDecisionsMock) ListClusters(int, int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{Result: m.clusters, Pagination: client.Pagination{TotalPages: 1, Page: 1}}, nil
}

// runCostDecisionsCommand runs the command with stdin, returning stdout and
// stderr apart so a test can tell the parseable output from the prompt.
func runCostDecisionsCommand(t *testing.T, mock APIClient, input string, args ...string) (string, string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(args)
	commands := []*cobra.Command{costDecisionsListCmd, costDecisionsGetCmd, costDecisionsActivityCmd, costDecisionsApproveCmd,
		costDecisionsSetAsideCmd, costDecisionsExecuteCmd}
	// Reset before the run as well as after the test: a test that runs the
	// command more than once must not carry one run's flags into the next.
	resetTreeFlags(t, commands...)
	t.Cleanup(func() { resetTreeFlags(t, commands...) })
	executeError := rootCmd.Execute()
	return stdout.String(), stderr.String(), executeError
}

const (
	decisionsLadderID  = "0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e"
	decisionsWave1ID   = "1c8d5e2f-6a7b-4c9d-8e1f-2a3b4c5d6e7f"
	decisionsWave2ID   = "5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3"
	decisionsWasteID   = "2d9e6f3a-7b8c-4d0e-9f2a-3b4c5d6e7f80"
	decisionsOrphanID  = "3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091"
	decisionsClusterID = "11111111-1111-4111-8111-111111111111"
	decisionsOtherID   = "22222222-2222-4222-8222-222222222222"
	decisionsOperation = "99999999-9999-4999-8999-999999999999"
)

func decisionsPointer[T any](value T) *T {
	return &value
}

func decisionFixture(id, kind, status, summary, clusterID string) client.DecisionProposal {
	return client.DecisionProposal{ID: id, Area: "cost", Kind: kind, Summary: summary, Status: status, Source: "surface",
		Subject: map[string]any{"cluster_id": clusterID}, Plan: map[string]any{}, Evidence: map[string]any{},
		Executable: true, MeasurementStatus: "not_applicable", VerificationStatus: "not_applicable",
		VerificationDays: []client.DecisionVerificationDay{}, SubjectClusterID: decisionsPointer(clusterID),
		CreatedAt: "2026-09-20T08:00:00Z", UpdatedAt: "2026-09-21T08:00:00Z", DedupeKey: kind + ":" + id}
}

// costDecisionsFixture is a page as the platform sends it, newest first: a
// wave still running (unsettled on this read) and a verified one, their
// ladder, a waste cleanup the autopilot approved with its run-after time, and
// a wave whose ladder is not on the page.
func costDecisionsFixture() *client.DecisionProposalList {
	wave2 := decisionFixture(decisionsWave2ID, "right_size", "running", "Resize prod-eu workers, wave 2 of 3", decisionsClusterID)
	wave2.ParentID = decisionsPointer(decisionsLadderID)
	wave2.OperationID = decisionsPointer(decisionsOperation)
	wave1 := decisionFixture(decisionsWave1ID, "right_size", "succeeded", "Resize prod-eu workers, wave 1 of 3", decisionsClusterID)
	wave1.ParentID = decisionsPointer(decisionsLadderID)
	ladder := decisionFixture(decisionsLadderID, "right_size_ladder", "approved", "Right-size prod-eu in three waves", decisionsClusterID)
	waste := decisionFixture(decisionsWasteID, "waste_cleanup", "approved", "Delete unattached volume vol-1", decisionsOtherID)
	waste.RunAfter = decisionsPointer("2026-09-25T06:00:00Z")
	orphan := decisionFixture(decisionsOrphanID, "right_size", "set_aside", "Resize batch-eu workers, wave 1 of 2", decisionsOtherID)
	orphan.ParentID = decisionsPointer("44444444-4444-4444-8444-444444444444")
	return &client.DecisionProposalList{
		Proposals:            []client.DecisionProposal{wave2, wave1, ladder, waste, orphan},
		UnsettledProposalIDs: []string{decisionsWave2ID},
	}
}

func flattenDecisionsOutput(output string) string {
	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		lines = append(lines, strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│")))
	}
	return strings.Join(lines, " ")
}

func TestCostDecisionsListPutsWavesUnderTheirLadder(t *testing.T) {
	mock := &costDecisionsMock{listing: costDecisionsFixture()}
	output, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "list")
	if executeError != nil {
		t.Fatalf("cost decisions list failed: %v", executeError)
	}
	if len(mock.filters) != 1 || mock.filters[0] != (client.DecisionListFilter{Area: "cost"}) {
		t.Fatalf("the list asks for the cost area only, got %+v", mock.filters)
	}
	flat := flattenDecisionsOutput(output)
	for _, expected := range []string{
		"Cost decisions: 5 proposals · 2 approved · 1 running · 1 set aside · 1 succeeded",
		"↳ Could not be checked against its platform operation on this read; it may already have finished.",
		"↳ The cost autopilot runs it after 2026-09-25T06:00:00Z unless it is held.",
		"↳ A wave of ladder 44444444-4444-4444-8444-444444444444.",
	} {
		if !strings.Contains(flat, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	ladderAt := strings.Index(output, "│ "+decisionsLadderID)
	wave2At := strings.Index(output, "│ └ "+decisionsWave2ID)
	wave1At := strings.Index(output, "│ └ "+decisionsWave1ID)
	wasteAt := strings.Index(output, "│ "+decisionsWasteID)
	if ladderAt < 0 || wave2At < ladderAt || wave1At < wave2At || wasteAt < wave1At {
		t.Fatalf("each wave sits directly under its ladder (ladder %d, waves %d %d, next %d):\n%s", ladderAt, wave2At, wave1At, wasteAt, output)
	}
	if !strings.Contains(output, "right_size (wave)") || strings.Contains(output, "│ └ "+decisionsOrphanID) {
		t.Fatalf("a wave whose ladder is on the page is marked as a wave; one whose ladder is not stays top level:\n%s", output)
	}
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if width := text.StringWidthWithoutEscSequences(line); width > 100 {
			t.Fatalf("line is %d columns wide, over 100: %q\n%s", width, line, output)
		}
	}
	if strings.Contains(output, "older proposals may exist") {
		t.Fatalf("a short page must not say it was cut:\n%s", output)
	}
}

func TestCostDecisionsListFiltersByStatusKindAndCluster(t *testing.T) {
	mock := &costDecisionsMock{listing: costDecisionsFixture(),
		clusters: []client.ClusterListItem{{ID: decisionsOtherID, Name: "batch-eu"}}}
	output, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "list",
		"--status", "Approved", "--kind", "waste_cleanup", "--cluster", "batch-eu", "--limit", "50", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost decisions list failed: %v", executeError)
	}
	if mock.filters[0] != (client.DecisionListFilter{Area: "cost", Status: "approved", Limit: 50}) {
		t.Fatalf("status and limit go to the platform, got %+v", mock.filters[0])
	}
	var decoded client.DecisionProposalList
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	if len(decoded.Proposals) != 1 || decoded.Proposals[0].ID != decisionsWasteID || len(decoded.UnsettledProposalIDs) != 0 {
		t.Fatalf("kind and cluster narrow the page, got %+v", decoded)
	}

	_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{listing: costDecisionsFixture()}, "", "cost", "decisions", "list", "--limit", "501")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("--limit over 500 is a usage error, got %v", executeError)
	}
}

func TestCostDecisionsListSaysWhenThePageIsFull(t *testing.T) {
	listing := costDecisionsFixture()
	output, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{listing: listing}, "", "cost", "decisions", "list", "--limit", "5")
	if executeError != nil {
		t.Fatalf("cost decisions list failed: %v", executeError)
	}
	if !strings.Contains(output, "(the platform returned its newest 5; older proposals may exist, pass --limit up to 500)") {
		t.Fatalf("a full page says older proposals may exist:\n%s", output)
	}
	output, _, _ = runCostDecisionsCommand(t, &costDecisionsMock{listing: &client.DecisionProposalList{
		Proposals: []client.DecisionProposal{}, UnsettledProposalIDs: []string{}}}, "", "cost", "decisions", "list")
	if strings.TrimSpace(output) != "No cost decision matches." {
		t.Fatalf("an empty page says so in one line:\n%s", output)
	}
}

func TestCostDecisionsListStructuredOutputKeepsNulls(t *testing.T) {
	output, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{listing: costDecisionsFixture()}, "", "cost", "decisions", "list", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost decisions list -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	proposals, _ := decoded["proposals"].([]any)
	ladder := proposals[2].(map[string]any)
	for _, field := range []string{"expected_monthly_cents", "measured_monthly_cents", "parent_id", "receipt", "run_after"} {
		if value, present := ladder[field]; !present || value != nil {
			t.Fatalf("%s must stay null on the wire: %+v", field, ladder)
		}
	}
	if proposals[0].(map[string]any)["parent_id"] != decisionsLadderID {
		t.Fatalf("a wave keeps its parent_id: %+v", proposals[0])
	}
	if unsettled, _ := decoded["unsettled_proposal_ids"].([]any); len(unsettled) != 1 {
		t.Fatalf("unsettled ids pass through: %+v", decoded)
	}
}

func TestCostDecisionsGetShowsPlanOutcomeReceiptAndWaves(t *testing.T) {
	ladder := decisionFixture(decisionsLadderID, "right_size_ladder", "running", "Right-size prod-eu in three waves", decisionsClusterID)
	ladder.Plan = map[string]any{"parameters": map[string]any{"waves": 3}}
	ladder.DecidedBy = decisionsPointer("77777777-7777-4777-8777-777777777777")
	ladder.DecidedAt = decisionsPointer("2026-09-20T09:00:00Z")
	ladder.DecisionNote = decisionsPointer("Go")
	ladder.OperationID = decisionsPointer(decisionsOperation)
	ladder.ExpectedMonthlyCents = decisionsPointer(int64(12000))
	ladder.MeasurementStatus = "pending"
	ladder.VerifyUntil = decisionsPointer("2026-09-27T10:00:00Z")
	ladder.VerificationStatus = "verifying"
	ladder.VerificationDays = []client.DecisionVerificationDay{{Day: 1, State: "clear", CPUP95Share: decisionsPointer(0.41), Nodes: 3, Reporting: 2, HottestNode: "w-2"}}
	ladder.Receipt = &client.DecisionReceipt{ExecutedBy: "77777777-7777-4777-8777-777777777777", DispatchedAt: "2026-09-20T10:00:00Z",
		Steps: []client.DecisionReceiptStep{{Name: "wave 2", Outcome: "dispatched", Detail: "Filed wave 2 of 3"}}}
	mock := &costDecisionsMock{proposal: &ladder, listing: costDecisionsFixture()}
	output, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "get", decisionsLadderID)
	if executeError != nil {
		t.Fatalf("cost decisions get failed: %v", executeError)
	}
	for _, expected := range []string{
		"Decision " + decisionsLadderID, "cost · right_size_ladder · running · executable: yes",
		`decided by 77777777-7777-4777-8777-777777777777 at 2026-09-20T09:00:00Z: "Go"`,
		"operation " + decisionsOperation + " (follow it with: ankra cluster operations list " + decisionsOperation + ")",
		`"waves": 3`,
		"expected $120.00/mo · baseline unknown · measured unknown",
		"verifying (inside the seven-day window) until 2026-09-27T10:00:00Z",
		"usage verification: verifying", "41%", "no data", "2 of 3",
		"Receipt: run by 77777777-7777-4777-8777-777777777777 · dispatched 2026-09-20T10:00:00Z", "Filed wave 2 of 3",
		"Waves (2):", decisionsWave2ID, decisionsWave1ID,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "$0.00") {
		t.Fatalf("an unknown figure must never print as zero:\n%s", output)
	}
	if len(mock.filters) != 1 || mock.filters[0] != (client.DecisionListFilter{Area: "cost", Limit: 500}) {
		t.Fatalf("a ladder's waves are read from the widest page, got %+v", mock.filters)
	}
}

func TestCostDecisionsGetSaysWhenTheWavesCannotBeRead(t *testing.T) {
	ladder := decisionFixture(decisionsLadderID, "right_size_ladder", "approved", "Right-size prod-eu in three waves", decisionsClusterID)
	mock := &costDecisionsMock{proposal: &ladder, listError: client.NewUnexpectedResponseError(500, "boom")}
	output, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "get", decisionsLadderID)
	if executeError != nil {
		t.Fatalf("cost decisions get failed: %v", executeError)
	}
	if !strings.Contains(output, "Waves: could not be read (boom).") || strings.Contains(output, "none filed yet") {
		t.Fatalf("an unreadable wave list is said, not shown as no waves:\n%s", output)
	}
}

func TestCostDecisionsActivityRendersEvents(t *testing.T) {
	activity := &client.DecisionActivity{Events: []client.DecisionEvent{
		{ID: "e2", ProposalID: decisionsWasteID, EventType: "set_aside", Actor: decisionsPointer("u1"), Note: decisionsPointer("Needed for launch"),
			Detail: map[string]any{}, CreatedAt: "2026-09-21T09:00:00Z"},
		{ID: "e1", ProposalID: decisionsWasteID, EventType: "proposed", Detail: map[string]any{"source": "surface"}, CreatedAt: "2026-09-20T08:00:00Z"},
	}}
	output, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{activity: activity}, "", "cost", "decisions", "activity", decisionsWasteID)
	if executeError != nil {
		t.Fatalf("cost decisions activity failed: %v", executeError)
	}
	for _, expected := range []string{"set aside", "Needed for launch", "proposed", `↳ {"source":"surface"}`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestCostDecisionsApproveSendsTheNoteOnlyWhenGiven(t *testing.T) {
	approved := decisionFixture(decisionsWasteID, "waste_cleanup", "approved", "Delete unattached volume vol-1", decisionsOtherID)
	mock := &costDecisionsMock{written: &approved}
	output, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID)
	if executeError != nil {
		t.Fatalf("cost decisions approve failed: %v", executeError)
	}
	if len(mock.approvedNotes) != 1 || mock.approvedNotes[0] != nil {
		t.Fatalf("no --note sends no note, got %v", mock.approvedNotes)
	}
	if !strings.Contains(output, "Approved "+decisionsWasteID) || !strings.Contains(output, "ankra cost decisions execute "+decisionsWasteID) {
		t.Fatalf("an approval says how to run it:\n%s", output)
	}
	mock = &costDecisionsMock{written: &approved}
	if _, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID, "--note", "Checked"); executeError != nil {
		t.Fatalf("cost decisions approve --note failed: %v", executeError)
	}
	if mock.approvedNotes[0] == nil || *mock.approvedNotes[0] != "Checked" {
		t.Fatalf("--note is sent, got %v", mock.approvedNotes)
	}
}

func TestCostDecisionsSetAsideAsksFirstOnStderr(t *testing.T) {
	setAside := decisionFixture(decisionsWasteID, "waste_cleanup", "set_aside", "Delete unattached volume vol-1", decisionsOtherID)
	mock := &costDecisionsMock{written: &setAside}
	stdout, stderr, executeError := runCostDecisionsCommand(t, mock, "n\n", "cost", "decisions", "set-aside", decisionsWasteID, "--note", "Needed")
	if executeError == nil || exitCodeFor(executeError) != exitCancelled || len(mock.setAsideNotes) != 0 {
		t.Fatalf("a declined prompt sets nothing aside and exits %d, got %v", exitCancelled, executeError)
	}
	if !strings.Contains(stderr, "Set aside decision "+decisionsWasteID+"?") || strings.Contains(stdout, "[y/N]") {
		t.Fatalf("the prompt goes to stderr: stdout=%q stderr=%q", stdout, stderr)
	}
	mock = &costDecisionsMock{written: &setAside}
	stdout, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "set-aside", decisionsWasteID, "--note", "Needed", "--yes", "-o", "json")
	if executeError != nil || len(mock.setAsideNotes) != 1 || *mock.setAsideNotes[0] != "Needed" {
		t.Fatalf("--yes sets it aside with the note, got %v", executeError)
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(stdout), &decoded) != nil || decoded["status"] != "set_aside" {
		t.Fatalf("-o json stays parseable: %s", stdout)
	}
}

// decisions execute must never default the consent to a final deletion: it
// sends what the flags say and nothing else.
func TestCostDecisionsExecuteSendsConsentOnlyWhenGiven(t *testing.T) {
	running := decisionFixture(decisionsWasteID, "waste_cleanup", "running", "Delete unattached volume vol-1", decisionsOtherID)
	running.OperationID = decisionsPointer(decisionsOperation)
	for _, testCase := range []struct {
		args []string
		want client.DecisionExecuteOptions
	}{
		{nil, client.DecisionExecuteOptions{}},
		{[]string{"--min-unattached-days", "60"}, client.DecisionExecuteOptions{MinUnattachedDays: decisionsPointer(60)}},
		{[]string{"--acknowledge-no-snapshot"}, client.DecisionExecuteOptions{Consent: "no_snapshot_acknowledged"}},
		{[]string{"--acknowledge-no-snapshot", "--min-unattached-days", "0"},
			client.DecisionExecuteOptions{Consent: "no_snapshot_acknowledged", MinUnattachedDays: decisionsPointer(0)}},
	} {
		mock := &costDecisionsMock{written: &running}
		output, _, executeError := runCostDecisionsCommand(t, mock, "",
			append([]string{"cost", "decisions", "execute", decisionsWasteID, "--yes"}, testCase.args...)...)
		if executeError != nil {
			t.Fatalf("%v: cost decisions execute failed: %v", testCase.args, executeError)
		}
		got := mock.executeOptions[0]
		if got.Consent != testCase.want.Consent || (got.MinUnattachedDays == nil) != (testCase.want.MinUnattachedDays == nil) ||
			(got.MinUnattachedDays != nil && *got.MinUnattachedDays != *testCase.want.MinUnattachedDays) {
			t.Fatalf("%v: options = %+v, want %+v", testCase.args, got, testCase.want)
		}
		if testCase.args == nil && got.Body() != nil {
			t.Fatalf("a run with no consent flag sends no body at all, got %v", got.Body())
		}
		if !strings.Contains(output, "is running as operation "+decisionsOperation+". Follow it with: ankra cluster operations list "+decisionsOperation) {
			t.Fatalf("%v: a running proposal names its operation:\n%s", testCase.args, output)
		}
	}
	_, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{}, "", "cost", "decisions", "execute", decisionsWasteID, "--yes",
		"--min-unattached-days", "4000")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("--min-unattached-days out of range is a usage error, got %v", executeError)
	}
}

func TestCostDecisionsExecuteAsksFirst(t *testing.T) {
	mock := &costDecisionsMock{}
	_, stderr, executeError := runCostDecisionsCommand(t, mock, "\n", "cost", "decisions", "execute", decisionsWasteID)
	if executeError == nil || exitCodeFor(executeError) != exitCancelled || len(mock.executeOptions) != 0 {
		t.Fatalf("an unanswered prompt runs nothing and exits %d, got %v", exitCancelled, executeError)
	}
	if !strings.Contains(stderr, "Run decision "+decisionsWasteID+" now?") {
		t.Fatalf("the prompt names the decision on stderr: %q", stderr)
	}
}

func TestCostDecisionsExecuteReportsConsentAndNotReadyAsSuch(t *testing.T) {
	consent := &client.DecisionConsentRequiredError{Detail: "Written consent is required: no snapshot exists for what this change deletes, so it is final."}
	_, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{writeError: consent}, "", "cost", "decisions", "execute", decisionsWasteID, "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "not run: Written consent is required") ||
		!strings.Contains(executeError.Error(), "the proposal stays approved") ||
		!strings.Contains(executeError.Error(), "run it again with --acknowledge-no-snapshot") {
		t.Fatalf("a consent refusal says how to give it, got %v", executeError)
	}

	notReady := client.NewUnexpectedResponseError(409, "Wave 2 of 3 is inside its seven-day verification; the next wave runs once it has passed.")
	notReady.Detail = notReady.Error()
	_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{writeError: notReady}, "", "cost", "decisions", "execute", decisionsLadderID, "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "not run: Wave 2 of 3 is inside its seven-day verification") ||
		!strings.Contains(executeError.Error(), "running it again before that changes is refused the same way") {
		t.Fatalf("a ladder not ready is reported as such, got %v", executeError)
	}

	failed := decisionFixture(decisionsWasteID, "waste_cleanup", "failed", "Delete unattached volume vol-1", decisionsOtherID)
	failed.Receipt = &client.DecisionReceipt{Steps: []client.DecisionReceiptStep{{Name: "cleanup", Outcome: "refused", Detail: "The volume is attached again."}}}
	output, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{written: &failed}, "", "cost", "decisions", "execute", decisionsWasteID, "--yes")
	if executeError == nil || exitCodeFor(executeError) != exitError || !strings.Contains(output, "The volume is attached again.") {
		t.Fatalf("a failed run shows its receipt and exits 1, got %v:\n%s", executeError, output)
	}
}

func TestCostDecisionsRefusalsKeepTheirMeaning(t *testing.T) {
	denied := &client.PermissionDeniedError{Permission: "billing.manage"}
	_, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{writeError: denied}, "", "cost", "decisions", "approve", decisionsWasteID)
	if executeError == nil || !strings.Contains(executeError.Error(), `"billing.manage"`) || exitCodeFor(executeError) != exitForbidden {
		t.Fatalf("a permission refusal names it and exits 7, got %v", executeError)
	}
	_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{writeError: &client.PermissionDeniedError{Permission: "clusters.write"}},
		"", "cost", "decisions", "execute", decisionsWave2ID, "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), `"clusters.write"`) || exitCodeFor(executeError) != exitForbidden {
		t.Fatalf("a right-size run without clusters.write names it, got %v", executeError)
	}

	notFound := &client.UnexpectedResponseError{StatusCode: 404, Detail: "Decision proposal not found"}
	_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{getError: notFound}, "", "cost", "decisions", "get", decisionsWasteID)
	if executeError == nil || strings.Contains(executeError.Error(), "predates") || exitCodeFor(executeError) != exitNotFound {
		t.Fatalf("a proposal that is not the organisation's is not found (exit 3), got %v", executeError)
	}

	for _, routeError := range []error{
		client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found"),
		&client.UnexpectedResponseError{StatusCode: 404, Detail: "Not Found"},
		client.NewUnexpectedResponseError(405, "Method Not Allowed"),
	} {
		_, _, listError := runCostDecisionsCommand(t, &costDecisionsMock{listError: routeError}, "", "cost", "decisions", "list")
		if listError == nil || !strings.Contains(listError.Error(), "this platform does not serve the decision ledger: GET /api/v1/org/decisions is not registered") ||
			exitCodeFor(listError) != exitError {
			t.Fatalf("list error = %v (exit %d)", listError, exitCodeFor(listError))
		}
	}
	// On an item route, a 405 is the route missing; a 404 that does not name
	// the proposal could be a proposal that is gone or a platform without the
	// ledger, and says both rather than pick one.
	_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{writeError: client.NewUnexpectedResponseError(405, "Method Not Allowed")},
		"", "cost", "decisions", "execute", decisionsWasteID, "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "POST /api/v1/org/decisions/{decision_id}/execute is not registered") {
		t.Fatalf("execute 405 error = %v", executeError)
	}
	for _, itemError := range []error{
		client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found"),
		&client.UnexpectedResponseError{StatusCode: 404, Detail: "Not Found"},
	} {
		_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{writeError: itemError}, "", "cost", "decisions", "execute", decisionsWasteID, "--yes")
		if executeError == nil || !strings.Contains(executeError.Error(),
			"POST /api/v1/org/decisions/{decision_id}/execute answered 404 without naming the proposal, so either the proposal is not one of this organisation's") ||
			!strings.Contains(executeError.Error(), "or this platform predates the decision ledger") || exitCodeFor(executeError) != exitError {
			t.Fatalf("an item 404 that does not name the proposal must admit both causes, got %v (exit %d)", executeError, exitCodeFor(executeError))
		}
	}
	// "Decision not found" names the proposal as well as "Decision proposal
	// not found" does.
	for _, detail := range []string{"Decision proposal not found", "Decision not found"} {
		_, _, getError := runCostDecisionsCommand(t, &costDecisionsMock{getError: &client.UnexpectedResponseError{StatusCode: 404, Detail: detail}},
			"", "cost", "decisions", "activity", decisionsWasteID)
		if getError == nil || strings.Contains(getError.Error(), "predates") || exitCodeFor(getError) != exitNotFound {
			t.Fatalf("detail %q is a proposal that is not found (exit 3), got %v (exit %d)", detail, getError, exitCodeFor(getError))
		}
	}

	_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{}, "", "cost", "decisions", "get", "waste-1")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("a reference that is not an id is a usage error, got %v", executeError)
	}
	conflict := client.NewUnexpectedResponseError(409, "The proposal's status does not allow this action.")
	_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{writeError: conflict}, "", "cost", "decisions", "approve", decisionsWasteID)
	if executeError == nil || !strings.Contains(executeError.Error(), "approving the decision: The proposal's status does not allow this action.") {
		t.Fatalf("an approval the status refuses relays the detail, got %v", executeError)
	}
}

// fullDecisionsPage is a page of exactly the most the list route serves: the
// ladder's waves, if any, among filler proposals.
func fullDecisionsPage(waves ...client.DecisionProposal) *client.DecisionProposalList {
	proposals := append([]client.DecisionProposal{}, waves...)
	for index := len(proposals); index < costDecisionsMaxLimit; index++ {
		filler := decisionFixture(fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", index), "off_hours_schedule", "succeeded",
			"Stop a cluster weeknights", decisionsOtherID)
		proposals = append(proposals, filler)
	}
	return &client.DecisionProposalList{Proposals: proposals, UnsettledProposalIDs: []string{}}
}

// A ladder's waves are read from the newest 500 proposals. A full page is a
// capped read, so neither "none" nor the waves found may be reported as the
// whole answer.
func TestCostDecisionsGetSaysAFullPageMayHideWaves(t *testing.T) {
	ladder := decisionFixture(decisionsLadderID, "right_size_ladder", "running", "Right-size prod-eu in three waves", decisionsClusterID)

	output, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{proposal: &ladder, listing: fullDecisionsPage()},
		"", "cost", "decisions", "get", decisionsLadderID)
	if executeError != nil {
		t.Fatalf("cost decisions get failed: %v", executeError)
	}
	if strings.Contains(output, "none filed yet") ||
		!strings.Contains(output, "Waves: none among the newest 500 proposals, which is not the same as none filed:") ||
		!strings.Contains(output, "ankra cost decisions list --status succeeded --kind right_size --limit 500") {
		t.Fatalf("a full page with no wave must not claim none were filed, and says how to look further:\n%s", output)
	}

	wave := costDecisionsFixture().Proposals[0]
	output, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{proposal: &ladder, listing: fullDecisionsPage(wave)},
		"", "cost", "decisions", "get", decisionsLadderID)
	if executeError != nil {
		t.Fatalf("cost decisions get failed: %v", executeError)
	}
	if !strings.Contains(output, "Waves (1 found among the newest 500 proposals; there may be more):") ||
		!strings.Contains(output, decisionsWave2ID) || strings.Contains(output, "Waves (1):") ||
		!strings.Contains(output, "(the platform lists at most the newest 500 proposals") {
		t.Fatalf("a full page with waves lists them and says there may be more:\n%s", output)
	}

	// A page short of the cap is the whole ledger, so its answer stands.
	output, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{proposal: &ladder,
		listing: &client.DecisionProposalList{Proposals: []client.DecisionProposal{}, UnsettledProposalIDs: []string{}}},
		"", "cost", "decisions", "get", decisionsLadderID)
	if executeError != nil || !strings.Contains(output, "Waves: none filed yet.") || strings.Contains(output, "may be more") {
		t.Fatalf("a short page with no wave says none were filed, got %v:\n%s", executeError, output)
	}
}

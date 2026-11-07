package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	approvedPlans  []string
	reads          int
	setAsideNotes  []*string
	heldNotes      []*string
	releasedNotes  []*string
	executeOptions []client.DecisionExecuteOptions
	clusters       []client.ClusterListItem
	waste          *client.CloudWaste
	wasteError     error
	wasteReads     int
}

func (m *costDecisionsMock) ListDecisions(filter client.DecisionListFilter) (*client.DecisionProposalList, error) {
	m.filters = append(m.filters, filter)
	if m.listError != nil {
		return nil, m.listError
	}
	return m.listing, nil
}

func (m *costDecisionsMock) GetDecision(string) (*client.DecisionProposal, error) {
	m.reads++
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

func (m *costDecisionsMock) ApproveDecision(_ string, note *string, planHash string) (*client.DecisionProposal, error) {
	m.approvedNotes = append(m.approvedNotes, note)
	m.approvedPlans = append(m.approvedPlans, planHash)
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

func (m *costDecisionsMock) HoldDecision(_ string, note *string) (*client.DecisionProposal, error) {
	m.heldNotes = append(m.heldNotes, note)
	if m.writeError != nil {
		return nil, m.writeError
	}
	return m.written, nil
}

func (m *costDecisionsMock) ReleaseDecision(_ string, note *string) (*client.DecisionProposal, error) {
	m.releasedNotes = append(m.releasedNotes, note)
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

func (m *costDecisionsMock) GetCloudWaste() (*client.CloudWaste, error) {
	m.wasteReads++
	if m.wasteError != nil {
		return nil, m.wasteError
	}
	return m.waste, nil
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
		costDecisionsSetAsideCmd, costDecisionsHoldCmd, costDecisionsReleaseCmd, costDecisionsExecuteCmd}
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
	output, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID, "--plan-hash", decisionsPlanHash)
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
	if _, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID,
		"--plan-hash", decisionsPlanHash, "--note", "Checked"); executeError != nil {
		t.Fatalf("cost decisions approve --note failed: %v", executeError)
	}
	if mock.approvedNotes[0] == nil || *mock.approvedNotes[0] != "Checked" {
		t.Fatalf("--note is sent, got %v", mock.approvedNotes)
	}
}

const (
	decisionsPlanHash      = "3c1f0d8e9a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b2c1d"
	decisionsOtherPlanHash = "9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b1a0f9e8d"
)

// answerApprovalsOn makes the approval prompt see a terminal (true) or not
// (false) for one test.
func answerApprovalsOn(t *testing.T, terminal bool) {
	t.Helper()
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return terminal }
	t.Cleanup(func() { promptIsInteractive = previous })
}

// An approval is of one plan (ankra-bn86k4.6.13): --plan-hash names the one
// the person reviewed and is sent as given, without a read; without it the
// proposal is read first and the plan that read served, once shown and
// agreed to, is the one named.
func TestCostDecisionsApproveIsOfThePlanItNames(t *testing.T) {
	read := decisionFixture(decisionsWasteID, "waste_cleanup", "proposed", "Delete unattached volume vol-1", decisionsOtherID)
	read.PlanHash = decisionsPlanHash
	approved := read
	approved.Status = "approved"

	answerApprovalsOn(t, true)
	mock := &costDecisionsMock{proposal: &read, written: &approved}
	output, _, executeError := runCostDecisionsCommand(t, mock, "y\n", "cost", "decisions", "approve", decisionsWasteID)
	if executeError != nil {
		t.Fatalf("cost decisions approve failed: %v", executeError)
	}
	if mock.reads != 1 || len(mock.approvedPlans) != 1 || mock.approvedPlans[0] != decisionsPlanHash {
		t.Fatalf("without --plan-hash the proposal is read and its plan approved: %d reads, approved %v", mock.reads, mock.approvedPlans)
	}
	if !strings.Contains(output, "The platform approved the plan with plan_hash "+decisionsPlanHash+".") {
		t.Fatalf("the approval names the plan it approved:\n%s", output)
	}

	mock = &costDecisionsMock{proposal: &read, written: &approved}
	if _, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID,
		"--plan-hash", decisionsOtherPlanHash); executeError != nil {
		t.Fatalf("cost decisions approve --plan-hash failed: %v", executeError)
	}
	if mock.reads != 0 || len(mock.approvedPlans) != 1 || mock.approvedPlans[0] != decisionsOtherPlanHash {
		t.Fatalf("--plan-hash is sent as given, without a read: %d reads, approved %v", mock.reads, mock.approvedPlans)
	}

	// A platform that predates plan_hash reads with none: the approval names
	// no plan, as it did before.
	legacy := read
	legacy.PlanHash = ""
	mock = &costDecisionsMock{proposal: &legacy, written: &approved}
	if _, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID, "--yes"); executeError != nil {
		t.Fatalf("cost decisions approve on an older platform failed: %v", executeError)
	}
	if len(mock.approvedPlans) != 1 || mock.approvedPlans[0] != "" {
		t.Fatalf("a read with no plan_hash names no plan, got %v", mock.approvedPlans)
	}

	// A read that fails approves nothing.
	mock = &costDecisionsMock{getError: client.NewUnexpectedResponseError(500, "Internal Server Error"), written: &approved}
	_, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID, "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "reading the decision before approving its plan") || len(mock.approvedPlans) != 0 {
		t.Fatalf("a failed read approves nothing, got %v and approvals %v", executeError, mock.approvedPlans)
	}
}

// The output names the plan the platform's answer says it approved, never
// the plan_hash the command sent: only the answer says what was approved. A
// platform that predates plan_hash answers none (and ignores the one sent),
// so the output says it could not confirm the plan instead of naming one.
func TestCostDecisionsApproveNamesThePlanThePlatformApproved(t *testing.T) {
	read := decisionFixture(decisionsWasteID, "waste_cleanup", "proposed", "Delete unattached volume vol-1", decisionsOtherID)
	answered := read
	answered.Status = "approved"
	answered.PlanHash = decisionsPlanHash
	mock := &costDecisionsMock{written: &answered}
	output, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID,
		"--plan-hash", decisionsOtherPlanHash)
	if executeError != nil {
		t.Fatalf("cost decisions approve --plan-hash failed: %v", executeError)
	}
	if !strings.Contains(output, "The platform approved the plan with plan_hash "+decisionsPlanHash+".") ||
		strings.Contains(output, decisionsOtherPlanHash) {
		t.Fatalf("the output names the plan_hash the platform answered, not the one sent:\n%s", output)
	}

	unconfirmed := answered
	unconfirmed.PlanHash = ""
	mock = &costDecisionsMock{written: &unconfirmed}
	output, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID,
		"--plan-hash", decisionsPlanHash)
	if executeError != nil {
		t.Fatalf("cost decisions approve on an older platform failed: %v", executeError)
	}
	if strings.Contains(output, "approved the plan with plan_hash") || !strings.Contains(output,
		"The platform's answer names no plan_hash (it predates plan_hash), so it could not confirm that the plan it "+
			"approved is the one with plan_hash "+decisionsPlanHash+".") {
		t.Fatalf("an answer with no plan_hash confirms no plan:\n%s", output)
	}

	legacy := read
	legacy.PlanHash = ""
	mock = &costDecisionsMock{proposal: &legacy, written: &unconfirmed}
	output, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID, "--yes")
	if executeError != nil {
		t.Fatalf("cost decisions approve on an older platform failed: %v", executeError)
	}
	if strings.Contains(output, "plan_hash") {
		t.Fatalf("an approval that named no plan on an older platform says nothing about one:\n%s", output)
	}
}

func TestCostDecisionsApproveRefusesAPlanHashThatIsNotOne(t *testing.T) {
	for _, planHash := range []string{"abc", strings.ToUpper(decisionsPlanHash), decisionsPlanHash + "0"} {
		mock := &costDecisionsMock{}
		_, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID, "--plan-hash", planHash)
		if executeError == nil || exitCodeFor(executeError) != exitUsage || len(mock.approvedPlans) != 0 || mock.reads != 0 {
			t.Fatalf("--plan-hash %q is a usage error before anything is sent, got %v (exit %d), approvals %v",
				planHash, executeError, exitCodeFor(executeError), mock.approvedPlans)
		}
	}
}

// A plan proposed again after it was read approves nothing, and the refusal
// names the plan the proposal holds now and how to approve that one.
func TestCostDecisionsApproveSaysThePlanChanged(t *testing.T) {
	current := decisionFixture(decisionsWasteID, "waste_cleanup", "proposed", "Delete unattached volume vol-1", decisionsOtherID)
	current.PlanHash = decisionsOtherPlanHash
	drifted := &client.DecisionPlanDriftedError{
		Detail: "This plan changed since you opened it: the proposal was recomputed with another plan, so nothing was approved. " +
			"Read the plan it holds now and approve that one.",
		Current: &current,
	}
	mock := &costDecisionsMock{writeError: drifted}
	_, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWasteID, "--plan-hash", decisionsPlanHash)
	if executeError == nil || exitCodeFor(executeError) != exitError {
		t.Fatalf("a plan that changed is a refusal (exit 1), got %v", executeError)
	}
	for _, expected := range []string{
		"not approved: This plan changed since you opened it",
		"Its plan now has plan_hash " + decisionsOtherPlanHash,
		"ankra cost decisions get " + decisionsWasteID,
		"--plan-hash " + decisionsOtherPlanHash,
	} {
		if !strings.Contains(executeError.Error(), expected) {
			t.Fatalf("the refusal lacks %q: %v", expected, executeError)
		}
	}

	_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{writeError: &client.DecisionPlanDriftedError{Detail: drifted.Detail}},
		"", "cost", "decisions", "approve", decisionsWasteID, "--plan-hash", decisionsPlanHash)
	if executeError == nil || !strings.Contains(executeError.Error(), "approve it again if it is still what you want") ||
		strings.Contains(executeError.Error(), "plan_hash ") {
		t.Fatalf("without the current proposal the refusal points at get, got %v", executeError)
	}
}

// decisionsRightSizeToApprove is a right-size proposal still to decide, with
// the plan the platform builds: two of the group's nodes from cx42 to cx32.
func decisionsRightSizeToApprove() client.DecisionProposal {
	proposal := decisionFixture(decisionsWave1ID, "right_size", "proposed", "Resize prod-eu workers, wave 1 of 3", decisionsClusterID)
	proposal.PlanHash = decisionsPlanHash
	proposal.ExpectedMonthlyCents = decisionsPointer(int64(4200))
	proposal.Plan = map[string]any{"parameters": map[string]any{"cluster_id": decisionsClusterID, "group_name": "workers",
		"from_instance_type": "cx42", "to_instance_type": "cx32", "node_count": float64(2)}}
	return proposal
}

// Approving without --plan-hash shows the plan in plain words and asks
// (ankra-bn86k4.6.21): the person approves a plan they saw, its plan_hash is
// the one sent, and the prompt and plan stay off stdout. Declining approves
// nothing and exits 4.
func TestCostDecisionsApproveWithoutAPlanHashShowsThePlanAndAsks(t *testing.T) {
	read := decisionsRightSizeToApprove()
	approved := read
	approved.Status = "approved"
	answerApprovalsOn(t, true)

	mock := &costDecisionsMock{proposal: &read, written: &approved}
	output, prompt, executeError := runCostDecisionsCommand(t, mock, "y\n", "cost", "decisions", "approve", decisionsWave1ID)
	if executeError != nil {
		t.Fatalf("cost decisions approve failed: %v", executeError)
	}
	for _, expected := range []string{
		"Approving decision " + decisionsWave1ID + ": Resize prod-eu workers, wave 1 of 3\n",
		"  right_size · proposed · on cluster " + decisionsClusterID + " · expected saving $42.00/mo\n",
		"  Moves node group workers of cluster " + decisionsClusterID + " from cx42 to cx32: 2 of its nodes still on cx42\n",
		"Plan (plan_hash " + decisionsPlanHash + "):\n",
		`"to_instance_type": "cx32"`,
		"Approve exactly this plan? [y/N]: ",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("stderr lacks %q:\n%s", expected, prompt)
		}
	}
	if strings.Contains(output, "Approve exactly this plan?") || strings.Contains(output, "Moves node group") {
		t.Fatalf("the plan and the question go to stderr, never stdout:\n%s", output)
	}
	if len(mock.approvedPlans) != 1 || mock.approvedPlans[0] != decisionsPlanHash {
		t.Fatalf("the plan shown is the plan approved, got %v", mock.approvedPlans)
	}

	mock = &costDecisionsMock{proposal: &read, written: &approved}
	_, _, executeError = runCostDecisionsCommand(t, mock, "n\n", "cost", "decisions", "approve", decisionsWave1ID)
	if !errors.Is(executeError, errCancelled) || exitCodeFor(executeError) != exitCancelled || len(mock.approvedPlans) != 0 {
		t.Fatalf("declining approves nothing and exits 4, got %v (exit %d), approvals %v",
			executeError, exitCodeFor(executeError), mock.approvedPlans)
	}

	// Platform text that hides characters is shown without them, and the
	// person is told.
	hiding := read
	hiding.Summary = "Resize prod-eu\u200b workers"
	mock = &costDecisionsMock{proposal: &hiding, written: &approved}
	_, prompt, executeError = runCostDecisionsCommand(t, mock, "y\n", "cost", "decisions", "approve", decisionsWave1ID)
	if executeError != nil || !strings.Contains(prompt, ": Resize prod-eu workers\n") ||
		!strings.Contains(prompt, "1 invisible character(s) were removed before display") {
		t.Fatalf("hidden characters are stripped and named (%v):\n%s", executeError, prompt)
	}
}

// Without a terminal, or with -o json|yaml, there is no one to ask: approving
// needs --plan-hash or --yes, and without either it refuses before reading or
// approving anything (ankra-bn86k4.6.21).
func TestCostDecisionsApproveWithNoOneToAskNeedsAPlanHashOrYes(t *testing.T) {
	read := decisionsRightSizeToApprove()
	approved := read
	approved.Status = "approved"
	for name, scenario := range map[string]struct {
		terminal bool
		args     []string
		reason   string
	}{
		"off a terminal":             {false, nil, "asks you first, and there is no terminal to ask on. "},
		"-o json on a terminal":      {true, []string{"-o", "json"}, "and -o json output is for a script, so there is no one to ask. "},
		"-o yaml off a terminal":     {false, []string{"-o", "yaml"}, "and -o yaml output is for a script, so there is no one to ask. "},
		"off a terminal with a note": {false, []string{"--note", "Checked"}, "asks you first, and there is no terminal to ask on. "},
	} {
		answerApprovalsOn(t, scenario.terminal)
		mock := &costDecisionsMock{proposal: &read, written: &approved}
		output, _, executeError := runCostDecisionsCommand(t, mock, "y\n",
			append([]string{"cost", "decisions", "approve", decisionsWave1ID}, scenario.args...)...)
		if executeError == nil || exitCodeFor(executeError) != exitUsage || mock.reads != 0 || len(mock.approvedPlans) != 0 || output != "" {
			t.Fatalf("%s: refused before anything is read or sent, got %v (exit %d), %d reads, approvals %v, stdout %q",
				name, executeError, exitCodeFor(executeError), mock.reads, mock.approvedPlans, output)
		}
		for _, expected := range []string{scenario.reason, "ankra cost decisions get " + decisionsWave1ID, "--plan-hash <plan_hash>", "--yes"} {
			if !strings.Contains(executeError.Error(), expected) {
				t.Fatalf("%s: the refusal lacks %q: %v", name, expected, executeError)
			}
		}
	}
}

// --yes approves the plan the proposal holds now without asking, still shows
// it on stderr, and still sends its plan_hash: the platform's check that the
// plan did not change after it was shown stays on (ankra-bn86k4.6.21).
func TestCostDecisionsApproveYesSendsThePlanItShowed(t *testing.T) {
	read := decisionsRightSizeToApprove()
	approved := read
	approved.Status = "approved"
	answerApprovalsOn(t, false)

	mock := &costDecisionsMock{proposal: &read, written: &approved}
	output, prompt, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWave1ID, "--yes", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost decisions approve --yes -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil || decoded["id"] != decisionsWave1ID {
		t.Fatalf("stdout stays the approved proposal as JSON (%v):\n%s", unmarshalError, output)
	}
	if !strings.Contains(prompt, "Plan (plan_hash "+decisionsPlanHash+"):") || strings.Contains(prompt, "[y/N]") {
		t.Fatalf("--yes shows the plan on stderr and asks nothing:\n%s", prompt)
	}
	if mock.reads != 1 || len(mock.approvedPlans) != 1 || mock.approvedPlans[0] != decisionsPlanHash {
		t.Fatalf("--yes sends the plan_hash it read: %d reads, approved %v", mock.reads, mock.approvedPlans)
	}

	// The plan changed between the read and the approval: the platform
	// refuses, because the plan_hash shown was sent.
	current := read
	current.PlanHash = decisionsOtherPlanHash
	mock = &costDecisionsMock{proposal: &read, writeError: &client.DecisionPlanDriftedError{
		Detail: "This plan changed since you opened it, so nothing was approved.", Current: &current}}
	_, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWave1ID, "--yes")
	if executeError == nil || !strings.Contains(executeError.Error(), "not approved: This plan changed") ||
		len(mock.approvedPlans) != 1 || mock.approvedPlans[0] != decisionsPlanHash {
		t.Fatalf("--yes never skips the platform's drift check, got %v, approvals %v", executeError, mock.approvedPlans)
	}

	// No proposal to show is no approval.
	mock = &costDecisionsMock{written: &approved}
	_, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "approve", decisionsWave1ID, "--yes")
	if executeError == nil || exitCodeFor(executeError) != exitError || len(mock.approvedPlans) != 0 {
		t.Fatalf("no proposal approves nothing, got %v (exit %d), approvals %v", executeError, exitCodeFor(executeError), mock.approvedPlans)
	}
}

func TestCostDecisionPlanInWords(t *testing.T) {
	parameters := func(extra map[string]any) map[string]any {
		values := map[string]any{"cluster_id": decisionsClusterID, "group_name": "workers",
			"from_instance_type": "cx42", "to_instance_type": "cx32"}
		for key, value := range extra {
			values[key] = value
		}
		return map[string]any{"parameters": values}
	}
	moves := "Moves node group workers of cluster " + decisionsClusterID + " from cx42 to cx32: "
	for name, scenario := range map[string]struct {
		plan map[string]any
		want string
	}{
		"the whole group":     {parameters(nil), moves + "every node still on cx42"},
		"a count":             {parameters(map[string]any{"node_count": float64(3)}), moves + "3 of its nodes still on cx42"},
		"named nodes":         {parameters(map[string]any{"nodes": []any{"w-1", "w-2"}, "node_count": float64(2)}), moves + "the nodes w-1, w-2"},
		"hidden characters":   {parameters(map[string]any{"group_name": "work\u200bers"}), moves + "every node still on cx42"},
		"another kind":        {map[string]any{"parameters": map[string]any{"waves": float64(3)}}, ""},
		"no parameters":       {map[string]any{}, ""},
		"parameters unshaped": {map[string]any{"parameters": "workers"}, ""},
	} {
		if got := costDecisionPlanInWords(scenario.plan); got != scenario.want {
			t.Fatalf("%s: got %q, want %q", name, got, scenario.want)
		}
	}
}

// Narrowing the list by --kind or --cluster narrows the platform's own
// document (ankra-bn86k4.8.25): -o json|yaml stays a passthrough of what the
// platform sent, fields the CLI does not know included, less the proposals
// filtered out.
func TestCostDecisionsListNarrowingKeepsThePlatformDocument(t *testing.T) {
	listing := costDecisionsFixture()
	encoded, _ := json.Marshal(listing)
	var document map[string]any
	_ = json.Unmarshal(encoded, &document)
	for _, proposal := range document["proposals"].([]any) {
		proposal.(map[string]any)["future_field"] = "kept"
	}
	document["next_cursor"] = "page-2"
	raw, _ := json.Marshal(document)
	listing.RawDocument = client.NewRawDocument(raw)

	mock := &costDecisionsMock{listing: listing, clusters: []client.ClusterListItem{{ID: decisionsClusterID, Name: "prod-eu"}}}
	output, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "list", "--kind", "right_size", "--cluster", "prod-eu", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost decisions list failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	proposals, _ := decoded["proposals"].([]any)
	if decoded["next_cursor"] != "page-2" || len(proposals) != 2 {
		t.Fatalf("the document keeps its own members and only the kept proposals: %+v", decoded)
	}
	for index, want := range []string{decisionsWave2ID, decisionsWave1ID} {
		proposal := proposals[index].(map[string]any)
		if proposal["id"] != want || proposal["future_field"] != "kept" {
			t.Fatalf("proposal %d is %v with future_field %v, want %s kept whole", index, proposal["id"], proposal["future_field"], want)
		}
	}
	if unsettled, _ := decoded["unsettled_proposal_ids"].([]any); len(unsettled) != 1 || unsettled[0] != decisionsWave2ID {
		t.Fatalf("unsettled ids are narrowed with the proposals: %+v", decoded["unsettled_proposal_ids"])
	}

	output, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{listing: listing}, "", "cost", "decisions", "list",
		"--kind", "waste_cleanup", "-o", "yaml")
	if executeError != nil || !strings.Contains(output, "future_field: kept") || !strings.Contains(output, "next_cursor: page-2") ||
		strings.Contains(output, decisionsWave1ID) {
		t.Fatalf("-o yaml narrows the same document (%v):\n%s", executeError, output)
	}
}

// A document that is not the shape the listing was decoded from is not
// narrowed by position: the narrowed listing prints as the CLI's own
// encoding of it instead.
func TestCostDecisionsNarrowDocumentRefusesADocumentOfAnotherShape(t *testing.T) {
	keep := []bool{true, false}
	kept := map[string]bool{"a": true}
	for name, document := range map[string]string{
		"empty":              ``,
		"not an object":      `[1, 2]`,
		"count differs":      `{"proposals": [{"id": "a"}], "unsettled_proposal_ids": []}`,
		"proposals unshaped": `{"proposals": {"id": "a"}}`,
		"ids unshaped":       `{"proposals": [{"id": "a"}, {"id": "b"}], "unsettled_proposal_ids": "a"}`,
		"truncated":          `{"proposals": [{"id": "a"}, {"id": "b"}]`,
	} {
		if narrowed := costDecisionsNarrowDocument(json.RawMessage(document), keep, kept); narrowed != nil {
			t.Fatalf("%s: got %s, want no document", name, narrowed)
		}
	}
	narrowed := costDecisionsNarrowDocument(json.RawMessage(`{"z": 1, "proposals": [{"id": "a", "x": "<&>"}, {"id": "b"}],
		"unsettled_proposal_ids": ["b", "a"], "a": null}`), keep, kept)
	if string(narrowed) != `{"z":1,"proposals":[{"id": "a", "x": "<&>"}],"unsettled_proposal_ids":["a"],"a":null}` {
		t.Fatalf("members keep their order and kept elements their bytes, got %s", narrowed)
	}
}

// get names the plan_hash of the plan it shows, and for a proposal still to
// decide, the approval of exactly that plan.
func TestCostDecisionsGetNamesThePlanHash(t *testing.T) {
	proposed := decisionFixture(decisionsWasteID, "waste_cleanup", "proposed", "Delete unattached volume vol-1", decisionsOtherID)
	proposed.PlanHash = decisionsPlanHash
	output, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{proposal: &proposed}, "", "cost", "decisions", "get", decisionsWasteID)
	if executeError != nil {
		t.Fatalf("cost decisions get failed: %v", executeError)
	}
	for _, expected := range []string{
		"Plan (plan_hash " + decisionsPlanHash + "):",
		"Approve exactly this plan with: ankra cost decisions approve " + decisionsWasteID + " --plan-hash " + decisionsPlanHash,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	running := proposed
	running.Status = "running"
	output, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{proposal: &running}, "", "cost", "decisions", "get", decisionsWasteID)
	if executeError != nil || strings.Contains(output, "Approve exactly this plan") || !strings.Contains(output, "Plan (plan_hash "+decisionsPlanHash+"):") {
		t.Fatalf("a running proposal names its plan_hash but offers no approval (%v):\n%s", executeError, output)
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

// A hold sends the note only when one is given, says that nothing runs the
// proposal until it is released or set aside, and keeps -o json parseable.
func TestCostDecisionsHoldSendsTheNoteAndSaysNothingRunsIt(t *testing.T) {
	held := decisionFixture(decisionsWasteID, "waste_cleanup", "held", "Delete unattached volume vol-1", decisionsOtherID)
	mock := &costDecisionsMock{written: &held}
	output, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "hold", decisionsWasteID)
	if executeError != nil {
		t.Fatalf("cost decisions hold failed: %v", executeError)
	}
	if len(mock.heldNotes) != 1 || mock.heldNotes[0] != nil {
		t.Fatalf("no --note sends no note, got %v", mock.heldNotes)
	}
	if !strings.Contains(output, "Held "+decisionsWasteID) ||
		!strings.Contains(output, "ankra cost decisions release "+decisionsWasteID) {
		t.Fatalf("a hold says how it ends:\n%s", output)
	}
	mock = &costDecisionsMock{written: &held}
	stdout, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "hold", decisionsWasteID, "--note", "After the launch", "-o", "json")
	if executeError != nil || len(mock.heldNotes) != 1 || mock.heldNotes[0] == nil || *mock.heldNotes[0] != "After the launch" {
		t.Fatalf("--note is sent, got %v, %v", mock.heldNotes, executeError)
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(stdout), &decoded) != nil || decoded["status"] != "held" {
		t.Fatalf("-o json stays parseable: %s", stdout)
	}
}

// A release says who runs the proposal next: the cost autopilot after its
// run_after when it approved the proposal itself, and a person otherwise.
func TestCostDecisionsReleaseSaysWhoRunsItNext(t *testing.T) {
	autopilot := decisionFixture(decisionsWasteID, "waste_cleanup", "approved", "Delete unattached volume vol-1", decisionsOtherID)
	autopilot.RunAfter = decisionsPointer("2026-09-26T06:00:00Z")
	mock := &costDecisionsMock{written: &autopilot}
	output, _, executeError := runCostDecisionsCommand(t, mock, "", "cost", "decisions", "release", decisionsWasteID, "--note", "Launch is over")
	if executeError != nil || len(mock.releasedNotes) != 1 || *mock.releasedNotes[0] != "Launch is over" {
		t.Fatalf("cost decisions release failed or dropped the note: %v %v", executeError, mock.releasedNotes)
	}
	if !strings.Contains(output, "Released "+decisionsWasteID) || !strings.Contains(output, "once 2026-09-26T06:00:00Z has passed") {
		t.Fatalf("an autopilot proposal's release says when the autopilot runs it:\n%s", output)
	}
	personal := decisionFixture(decisionsWasteID, "waste_cleanup", "approved", "Delete unattached volume vol-1", decisionsOtherID)
	mock = &costDecisionsMock{written: &personal}
	output, _, executeError = runCostDecisionsCommand(t, mock, "", "cost", "decisions", "release", decisionsWasteID)
	if executeError != nil || !strings.Contains(output, "Run it with: ankra cost decisions execute "+decisionsWasteID) {
		t.Fatalf("a person's proposal's release says how to run it: %v\n%s", executeError, output)
	}
}

// A hold or a release the proposal's status does not allow is the platform's
// 409: said as such, exit 1, never read as a missing route; a missing
// permission keeps its exit 7.
func TestCostDecisionsHoldAndReleaseReportRefusalsAsSuch(t *testing.T) {
	for _, testCase := range []struct {
		command string
		verb    string
	}{{"hold", "not held"}, {"release", "not released"}} {
		conflict := &client.UnexpectedResponseError{StatusCode: 409, Detail: "Only an approved proposal can be held."}
		_, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{writeError: conflict}, "", "cost", "decisions", testCase.command, decisionsWasteID)
		if executeError == nil || !strings.Contains(executeError.Error(), testCase.verb+": Only an approved proposal can be held.") ||
			strings.Contains(executeError.Error(), "predates") || exitCodeFor(executeError) != exitError {
			t.Fatalf("%s: a 409 says the proposal stays as it stands (exit 1), got %v", testCase.command, executeError)
		}
		denied := &client.PermissionDeniedError{Permission: "billing.manage"}
		_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{writeError: denied}, "", "cost", "decisions", testCase.command, decisionsWasteID)
		if executeError == nil || !strings.Contains(executeError.Error(), `"billing.manage"`) || exitCodeFor(executeError) != exitForbidden {
			t.Fatalf("%s: a permission refusal names it and exits 7, got %v", testCase.command, executeError)
		}
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

const (
	decisionsServerFindingID = "6f1a2b3c-4d5e-4f60-8a7b-9c0d1e2f3a4b"
	decisionsServerSentence  = "I understand this server and the data on its disks are deleted, and the snapshot taken first is deleted on its retention date."
)

// decisionsWasteListing is the waste listing of a platform with cluster#5646
// when consentByKind is set, and of one before it when it is not.
func decisionsWasteListing(resourceName string, consentByKind bool) *client.CloudWaste {
	waste := &client.CloudWaste{
		Findings: []client.CloudWasteFinding{
			{ID: "7a2b3c4d-5e6f-4a70-9b8c-0d1e2f3a4b5c", Kind: "unassigned_ip", ResourceName: "203.0.113.7"},
			{ID: decisionsServerFindingID, Kind: "stopped_server", ResourceName: resourceName},
		},
		Consent: client.CloudWasteConsentPolicy{SentenceByKind: map[string]string{"stopped_server": decisionsServerSentence}},
	}
	if consentByKind {
		waste.Consent.ConsentByKind = map[string]string{
			"stopped_server": "delete_stopped_server_acknowledged", "unassigned_ip": "release_unassigned_ip_acknowledged"}
	}
	return waste
}

func decisionsServerCleanup() *client.DecisionProposal {
	approved := decisionFixture(decisionsWasteID, "waste_cleanup", "approved", "Delete stopped server web-1", decisionsOtherID)
	approved.Plan = map[string]any{"consent_required": true, "parameters": map[string]any{"waste_finding_id": decisionsServerFindingID}}
	return &approved
}

// A consented waste cleanup sends the consent naming its finding's kind
// where the platform names one, and the prompt shows the resource and what
// the person agrees to (ankra-bn86k4.6.16.2). A platform that names none, a
// listing that could not be read and a proposal of another kind send the
// general consent the platform takes for one finding.
func TestCostDecisionsExecuteSendsTheConsentNamingTheFindingsKind(t *testing.T) {
	running := decisionFixture(decisionsWasteID, "waste_cleanup", "running", "Delete stopped server web-1", decisionsOtherID)
	running.OperationID = decisionsPointer(decisionsOperation)

	mock := &costDecisionsMock{proposal: decisionsServerCleanup(), waste: decisionsWasteListing("web-1\x1b[2K", true), written: &running}
	_, stderr, executeError := runCostDecisionsCommand(t, mock, "y\n", "cost", "decisions", "execute", decisionsWasteID, "--acknowledge-no-snapshot")
	if executeError != nil {
		t.Fatalf("cost decisions execute failed: %v", executeError)
	}
	if len(mock.executeOptions) != 1 || mock.executeOptions[0].Consent != "delete_stopped_server_acknowledged" {
		t.Fatalf("a stopped server's cleanup sends its kind's consent, got %+v", mock.executeOptions)
	}
	if !strings.Contains(stderr, "with your written consent to remove web-1[2K: "+decisionsServerSentence+" [y/N]: ") ||
		strings.Contains(stderr, "\x1b") {
		t.Fatalf("the prompt names the resource and the sentence, with no control characters: %q", stderr)
	}

	// An unreadable proposal or listing says why the general consent is sent;
	// a platform that names none and a proposal of another kind say nothing.
	for _, testCase := range []struct {
		name          string
		mock          *costDecisionsMock
		wantNote      string
		wantListReads int
	}{
		{"a platform before consent_by_kind",
			&costDecisionsMock{proposal: decisionsServerCleanup(), waste: decisionsWasteListing("web-1", false), written: &running}, "", 1},
		{"a listing that could not be read",
			&costDecisionsMock{proposal: decisionsServerCleanup(), wasteError: errors.New("permission denied"), written: &running},
			"The waste listing could not be read (permission denied), so the general consent (no_snapshot_acknowledged) is sent", 1},
		{"a proposal that could not be read",
			&costDecisionsMock{getError: errors.New("connection reset"), waste: decisionsWasteListing("web-1", true), written: &running},
			"The proposal could not be read (connection reset), so the general consent (no_snapshot_acknowledged) is sent", 0},
		{"a proposal of another kind",
			&costDecisionsMock{proposal: decisionsPointer(decisionFixture(decisionsWasteID, "off_hours_schedule", "approved", "Stop staging at night", decisionsOtherID)),
				waste: decisionsWasteListing("web-1", true), written: &running}, "", 0},
	} {
		_, stderr, executeError := runCostDecisionsCommand(t, testCase.mock, "", "cost", "decisions", "execute", decisionsWasteID,
			"--acknowledge-no-snapshot", "--yes")
		if executeError != nil {
			t.Fatalf("%s: cost decisions execute failed: %v", testCase.name, executeError)
		}
		if len(testCase.mock.executeOptions) != 1 || testCase.mock.executeOptions[0].Consent != "no_snapshot_acknowledged" {
			t.Fatalf("%s: the general consent is sent, got %+v", testCase.name, testCase.mock.executeOptions)
		}
		if testCase.mock.wasteReads != testCase.wantListReads {
			t.Fatalf("%s: the waste listing was read %d times, want %d", testCase.name, testCase.mock.wasteReads, testCase.wantListReads)
		}
		if testCase.wantNote == "" && strings.Contains(stderr, "could not be read") {
			t.Fatalf("%s: nothing went unread, so nothing is said: %q", testCase.name, stderr)
		}
		if testCase.wantNote != "" && !strings.Contains(stderr, testCase.wantNote) {
			t.Fatalf("%s: stderr = %q, want %q", testCase.name, stderr, testCase.wantNote)
		}
	}

	quiet := &costDecisionsMock{proposal: decisionsServerCleanup(), waste: decisionsWasteListing("web-1", true), written: &running}
	if _, _, executeError := runCostDecisionsCommand(t, quiet, "", "cost", "decisions", "execute", decisionsWasteID, "--yes"); executeError != nil {
		t.Fatalf("cost decisions execute failed: %v", executeError)
	}
	if quiet.reads != 0 || quiet.wasteReads != 0 || quiet.executeOptions[0].Consent != "" {
		t.Fatalf("without --acknowledge-no-snapshot nothing is looked up and no consent is sent: %d reads, %d listings, %+v",
			quiet.reads, quiet.wasteReads, quiet.executeOptions)
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
	_, _, executeError := runCostDecisionsCommand(t, &costDecisionsMock{writeError: denied}, "", "cost", "decisions", "approve", decisionsWasteID,
		"--plan-hash", decisionsPlanHash)
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
	_, _, executeError = runCostDecisionsCommand(t, &costDecisionsMock{writeError: conflict}, "", "cost", "decisions", "approve", decisionsWasteID,
		"--plan-hash", decisionsPlanHash)
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

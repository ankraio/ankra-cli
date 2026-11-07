package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

const summaryTestID = "11111111-1111-4111-8111-111111111111"

type profileSummaryMock struct {
	baseMock
	calls      int
	review     client.ProfileSummaryReviewRequest
	edit       client.ProfileSummaryCandidateRequest
	withdrawal client.ProfileSummaryWithdrawalRequest
	payload    json.RawMessage
	failure    error
}

func (m *profileSummaryMock) GetProfileSummary(context.Context, string, int) (json.RawMessage, error) {
	m.calls++
	return m.payload, m.failure
}
func (m *profileSummaryMock) GetProfileSummaryEvidence(context.Context, string, int, string) (json.RawMessage, error) {
	m.calls++
	return m.payload, m.failure
}
func (m *profileSummaryMock) GetProfileSummaryGeneration(context.Context, string) (json.RawMessage, error) {
	m.calls++
	return m.payload, m.failure
}
func (m *profileSummaryMock) ReviewProfileSummary(_ context.Context, _ string, r client.ProfileSummaryReviewRequest) (json.RawMessage, error) {
	m.calls++
	m.review = r
	return m.payload, m.failure
}
func (m *profileSummaryMock) EditProfileSummaryCandidate(_ context.Context, _ string, r client.ProfileSummaryCandidateRequest) (json.RawMessage, error) {
	m.calls++
	m.edit = r
	return m.payload, m.failure
}
func (m *profileSummaryMock) WithdrawProfileSummary(_ context.Context, _ string, _ int, r client.ProfileSummaryWithdrawalRequest) (json.RawMessage, error) {
	m.calls++
	m.withdrawal = r
	return m.payload, m.failure
}

func runSummaryCommand(t *testing.T, mock *profileSummaryMock, input string, args ...string) (string, string, error) {
	t.Helper()
	setMockClient(t, mock)
	command := newStackProfileSummaryCommand()
	var output, diagnostics bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&diagnostics)
	command.SetIn(strings.NewReader(input))
	command.SetArgs(args)
	command.SilenceErrors = true
	command.SilenceUsage = true
	failure := command.Execute()
	return output.String(), diagnostics.String(), failure
}

func TestProfileSummaryRequiresTheInspectedRevisions(t *testing.T) {
	for _, args := range [][]string{
		{"review", summaryTestID, "--decision", "approve", "--source-revision", "3", "--candidate-revision", "2"},
		{"review", summaryTestID, "--decision", "anything", "--source-revision", "3", "--candidate-revision", "2", "--summary-revision", "0"},
		{"review", summaryTestID, "--decision", "approve", "--source-revision", "0", "--candidate-revision", "2", "--summary-revision", "0"},
		{"edit", summaryTestID, "--candidate-revision", "1"},
		{"edit", summaryTestID, "--candidate-revision", "1", "--clear=false"},
		{"withdraw", summaryTestID, "3", "--reason", "incorrect_claim"},
		{"get", summaryTestID, "0"},
		{"generation", "00000000-0000-0000-0000-000000000000"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			mock := &profileSummaryMock{}
			_, _, failure := runSummaryCommand(t, mock, "", args...)
			if failure == nil || mock.calls != 0 {
				t.Fatalf("invalid request reached API: calls=%d error=%v", mock.calls, failure)
			}
		})
	}
}

func TestProfileSummaryReviewPreservesExplicitZeroAndDoesNotRetry(t *testing.T) {
	mock := &profileSummaryMock{payload: json.RawMessage(`{"decision":"approve","summary_revision":1}`)}
	output, diagnostics, failure := runSummaryCommand(t, mock, "", "review", summaryTestID, "--decision", "approve", "--source-revision", "3", "--candidate-revision", "2", "--summary-revision", "0", "-o", "json")
	if failure != nil || !json.Valid([]byte(output)) || diagnostics != "" || mock.calls != 1 || mock.review.ExpectedApprovedRevision != 0 || mock.review.ExpectedCandidateRevision != 2 || mock.review.ExpectedSourceRevision != 3 {
		t.Fatalf("review lost inspected values: %s %s %v %+v", output, diagnostics, failure, mock)
	}
	mock.failure = errors.New("outcome unknown; read back before retrying")
	mock.calls = 0
	_, _, failure = runSummaryCommand(t, mock, "", "review", summaryTestID, "--decision", "approve", "--source-revision", "3", "--candidate-revision", "2", "--summary-revision", "0")
	if !errors.Is(failure, mock.failure) || mock.calls != 1 {
		t.Fatalf("ambiguous write retried or hidden: %v, %d calls", failure, mock.calls)
	}
}

func TestProfileSummaryCandidateClearingIsExplicit(t *testing.T) {
	mock := &profileSummaryMock{payload: json.RawMessage(`{"candidate_revision":2}`)}
	_, _, failure := runSummaryCommand(t, mock, "", "edit", summaryTestID, "--candidate-revision", "1", "--clear")
	if failure != nil || mock.edit.Outcomes == nil || len(mock.edit.Outcomes) != 0 {
		t.Fatalf("clear became omission: %v %+v", failure, mock.edit)
	}
	mock.calls = 0
	_, _, failure = runSummaryCommand(t, mock, "", "edit", summaryTestID, "--candidate-revision", "2", "--outcome", "logging=f0,f1", "--outcome", "storage=f2")
	if failure != nil || len(mock.edit.Outcomes) != 2 || mock.edit.Outcomes[0].FactIDs[1] != "f1" {
		t.Fatalf("selection changed: %v %+v", failure, mock.edit)
	}
}

func TestProfileSummarySelectionBounds(t *testing.T) {
	for _, selection := range [][]string{{"logging="}, {"logging=f0,f0"}, {"logging=f0", "storage=f0"}, {"madeup=f0"}, {"logging=f0", "logging=f1"}, {"logging=" + strings.Repeat("x", 1100)}, {"logging=" + strings.Repeat("f0,", 17) + "f1"}, {"logging=secret/path"}} {
		if _, failure := parseSummarySelections(selection, false); failure == nil {
			t.Fatalf("accepted invalid selection: %q", selection)
		}
	}
}

func TestProfileSummaryWithdrawalConfirmationKeepsStdoutClean(t *testing.T) {
	for _, answer := range []string{"no\n", "yes\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			mock := &profileSummaryMock{payload: json.RawMessage(`{"state":"withdrawn","summary_revision":2}`)}
			output, diagnostics, failure := runSummaryCommand(t, mock, answer, "withdraw", summaryTestID, "3", "--summary-revision", "1", "--reason", "incorrect_claim", "-o", "json")
			if !strings.Contains(diagnostics, "[y/N]") {
				t.Fatalf("missing prompt on stderr: %q", diagnostics)
			}
			if answer == "no\n" {
				if !errors.Is(failure, errCancelled) || mock.calls != 0 || output != "" {
					t.Fatalf("decline made a write: %v %d %q", failure, mock.calls, output)
				}
				return
			}
			if failure != nil || !json.Valid([]byte(output)) || mock.calls != 1 || mock.withdrawal.ExpectedSummaryRevision != 1 {
				t.Fatalf("bad confirmed write: %v %q %+v", failure, output, mock.withdrawal)
			}
		})
	}
}

func TestProfileSummaryReadPreservesUnknownAndConditions(t *testing.T) {
	for _, payload := range []string{`{"state":"unsupported_policy","summary_revision":2}`, `{"state":"available","outcomes":[{"kind":"logging","claims":[{"condition":{"parameter_id":"p0","equals":true}}]}]}`, `{"state":"absent","summary_revision":0}`} {
		mock := &profileSummaryMock{payload: json.RawMessage(payload)}
		output, _, failure := runSummaryCommand(t, mock, "", "get", summaryTestID, "3", "-o", "json")
		var actual, want any
		_ = json.Unmarshal([]byte(output), &actual)
		_ = json.Unmarshal([]byte(payload), &want)
		actualJSON, _ := json.Marshal(actual)
		wantJSON, _ := json.Marshal(want)
		if failure != nil || !bytes.Equal(actualJSON, wantJSON) {
			t.Fatalf("read changed meaning: %v %s", failure, output)
		}
	}
	mock := &profileSummaryMock{payload: json.RawMessage(`{"state":"withdrawn"}`)}
	output, _, failure := runSummaryCommand(t, mock, "", "get", summaryTestID, "3", "-o", "yaml")
	if failure != nil || !strings.Contains(output, "state: withdrawn") {
		t.Fatalf("YAML: %v %s", failure, output)
	}
	for _, invalid := range []string{"", "null", "{}", "<html>"} {
		mock.payload = json.RawMessage(invalid)
		output, _, failure = runSummaryCommand(t, mock, "", "get", summaryTestID, "3")
		if failure == nil || output != "" {
			t.Fatalf("invalid read looked successful: %q %v", output, failure)
		}
	}
}

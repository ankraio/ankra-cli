package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type supportMock struct {
	baseMock

	createReq   *client.CreateSupportTicketRequest
	createErr   error
	created     *client.SupportTicket
	reviewReq   *client.ReviewSupportTicketRequest
	review      *client.SupportTicketReview
	reviewErr   error
	list        *client.SupportTicketListResponse
	got         *client.SupportTicket
	getErr      error
	commentBody string
	attachCalls []string
}

func (m *supportMock) CreateSupportTicket(ctx context.Context, req client.CreateSupportTicketRequest) (*client.SupportTicket, error) {
	captured := req
	m.createReq = &captured
	if m.createErr != nil {
		return nil, m.createErr
	}
	if m.created != nil {
		return m.created, nil
	}
	return &client.SupportTicket{ID: "ticket-1", Subject: req.Subject, Status: "open", Category: req.Category, CreatedAt: time.Now()}, nil
}

func (m *supportMock) ReviewSupportTicket(ctx context.Context, req client.ReviewSupportTicketRequest) (*client.SupportTicketReview, error) {
	captured := req
	m.reviewReq = &captured
	if m.reviewErr != nil {
		return nil, m.reviewErr
	}
	if m.review != nil {
		return m.review, nil
	}
	return &client.SupportTicketReview{ReviewID: "review-1", Quality: "pass"}, nil
}

func (m *supportMock) ListSupportTickets(ctx context.Context, opts client.ListSupportTicketsOptions) (*client.SupportTicketListResponse, error) {
	if m.list != nil {
		return m.list, nil
	}
	return &client.SupportTicketListResponse{}, nil
}

func (m *supportMock) GetSupportTicket(ctx context.Context, ticketID string) (*client.SupportTicket, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.got != nil {
		return m.got, nil
	}
	return &client.SupportTicket{ID: ticketID, Subject: "Sample", Status: "open", CreatedAt: time.Now()}, nil
}

func (m *supportMock) CommentSupportTicket(ctx context.Context, ticketID, comment string) (*client.SupportTicket, error) {
	m.commentBody = comment
	return &client.SupportTicket{ID: ticketID, Subject: "Sample", Status: "in_progress", CreatedAt: time.Now()}, nil
}

func (m *supportMock) CloseSupportTicket(ctx context.Context, ticketID string) (*client.SupportTicket, error) {
	return &client.SupportTicket{ID: ticketID, Subject: "Sample", Status: "closed", CreatedAt: time.Now()}, nil
}

func (m *supportMock) UploadSupportAttachment(ctx context.Context, ticketID, filePath string) (*client.SupportTicket, error) {
	m.attachCalls = append(m.attachCalls, filePath)
	return &client.SupportTicket{ID: ticketID, Subject: "Sample", Status: "open", CreatedAt: time.Now()}, nil
}

func resetSupportFlags(t *testing.T) {
	t.Helper()
	for _, command := range []*cobra.Command{
		supportCreateCmd, supportListCmd, supportGetCmd, supportCommentCmd, supportAttachCmd, supportCloseCmd,
	} {
		command.Flags().VisitAll(func(flag *pflag.Flag) {
			if sliceValue, ok := flag.Value.(pflag.SliceValue); ok {
				_ = sliceValue.Replace(nil)
				flag.Changed = false
				return
			}
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	}
}

func runSupport(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	return runSupportWithInput(t, mock, "y\n", args...)
}

func runSupportWithInput(t *testing.T, mock APIClient, input string, args ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	resetSupportFlags(t)
	out := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

func TestSupportCreate_ForceSkipsPromptAndAcknowledges(t *testing.T) {
	mock := &supportMock{review: &client.SupportTicketReview{
		ReviewID:     "review-9",
		Quality:      "flag",
		QualityFlags: []string{"Add the exact error message"},
	}}
	out, err := runSupportWithInput(t, mock, "", "support", "create", "--subject", "Nodes NotReady", "--description", "All nodes down", "--severity", "high", "--force")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if mock.reviewReq == nil {
		t.Fatal("expected a review call before create")
	}
	if mock.reviewReq.Source != "cli" {
		t.Errorf("expected review source cli, got %q", mock.reviewReq.Source)
	}
	if mock.createReq == nil {
		t.Fatal("expected a create call")
	}
	if !mock.createReq.Acknowledged {
		t.Error("expected --force to set Acknowledged=true")
	}
	if mock.createReq.ReviewID == nil || *mock.createReq.ReviewID != "review-9" {
		t.Errorf("expected review id forwarded to create, got %+v", mock.createReq.ReviewID)
	}
	if mock.createReq.Severity == nil || *mock.createReq.Severity != "high" {
		t.Errorf("expected severity high, got %+v", mock.createReq.Severity)
	}
	if mock.createReq.Source != "cli" {
		t.Errorf("expected source cli, got %q", mock.createReq.Source)
	}
	if !strings.Contains(out, "Add the exact error message") {
		t.Errorf("expected flag reason in output, got: %s", out)
	}
	if !strings.Contains(out, "ticket-1") {
		t.Errorf("expected created ticket id in output, got: %s", out)
	}
}

func TestSupportCreate_FlaggedPromptsAndSubmitsOnYes(t *testing.T) {
	mock := &supportMock{review: &client.SupportTicketReview{
		ReviewID:            "review-2",
		Quality:             "flag",
		QualityFlags:        []string{"Describe the symptom you are seeing"},
		ClarifyingQuestions: []string{"What is the exact error message?"},
	}}
	out, err := runSupportWithInput(t, mock, "y\n", "support", "create", "--subject", "x", "--description", "y")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Describe the symptom you are seeing") {
		t.Errorf("expected flag reason in output, got: %s", out)
	}
	if !strings.Contains(out, "What is the exact error message?") {
		t.Errorf("expected clarifying question in output, got: %s", out)
	}
	if mock.createReq == nil {
		t.Fatal("expected a create call after confirmation")
	}
	if !mock.createReq.Acknowledged {
		t.Error("expected confirmation to set Acknowledged=true")
	}
	if mock.createReq.ReviewID == nil || *mock.createReq.ReviewID != "review-2" {
		t.Errorf("expected review id forwarded to create, got %+v", mock.createReq.ReviewID)
	}
}

func TestSupportCreate_FlaggedDeclineDoesNotSubmit(t *testing.T) {
	mock := &supportMock{review: &client.SupportTicketReview{
		ReviewID:     "review-3",
		Quality:      "flag",
		QualityFlags: []string{"This looks like a test submission"},
	}}
	_, err := runSupportWithInput(t, mock, "n\n", "support", "create", "--subject", "x", "--description", "this is a test")
	if err == nil {
		t.Fatal("expected an error when the user declines")
	}
	if !strings.Contains(err.Error(), "not submitted") {
		t.Errorf("expected not-submitted message, got: %v", err)
	}
	if mock.createReq != nil {
		t.Error("expected no create call when the user declines")
	}
}

func TestSupportCreate_StructuredFlaggedRequiresForce(t *testing.T) {
	mock := &supportMock{review: &client.SupportTicketReview{
		ReviewID:     "review-4",
		Quality:      "flag",
		QualityFlags: []string{"Too vague to act on"},
	}}
	_, err := runSupportWithInput(t, mock, "", "support", "create", "--subject", "x", "--description", "y", "-o", "json")
	if err == nil {
		t.Fatal("expected an error when a flagged ticket is created with structured output and no --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("expected guidance to use --force, got: %v", err)
	}
	if mock.createReq != nil {
		t.Error("expected no create call when flagged in structured mode without --force")
	}
}

func TestSupportCreate_PassSubmitsWithoutPrompt(t *testing.T) {
	mock := &supportMock{review: &client.SupportTicketReview{ReviewID: "review-5", Quality: "pass"}}
	out, err := runSupportWithInput(t, mock, "", "support", "create", "--subject", "Nodes NotReady", "--description", "All worker nodes went NotReady at 14:00")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "Submit this request anyway?") {
		t.Errorf("did not expect a confirmation prompt for a passing ticket, got: %s", out)
	}
	if mock.createReq == nil {
		t.Fatal("expected a create call")
	}
	if mock.createReq.ReviewID == nil || *mock.createReq.ReviewID != "review-5" {
		t.Errorf("expected review id forwarded to create, got %+v", mock.createReq.ReviewID)
	}
	if mock.createReq.Acknowledged {
		t.Error("expected Acknowledged=false for a passing ticket without --force")
	}
}

func TestSupportCreate_DuplicateShownAndConfirmed(t *testing.T) {
	mock := &supportMock{review: &client.SupportTicketReview{
		ReviewID: "review-6",
		Quality:  "pass",
		DuplicateCandidates: []client.SupportDuplicateCandidate{
			{CandidateID: "c1", Summary: "Worker nodes flap to NotReady after upgrade", StatusLabel: "Being worked on", Confidence: "high"},
		},
	}}
	out, err := runSupportWithInput(t, mock, "y\n", "support", "create", "--subject", "Nodes NotReady", "--description", "nodes keep going NotReady")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "This may already be tracked") || !strings.Contains(out, "Worker nodes flap to NotReady after upgrade") {
		t.Errorf("expected duplicate candidate in output, got: %s", out)
	}
	if mock.createReq == nil {
		t.Fatal("expected a create call after confirmation")
	}
}

func TestSupportCreate_ReviewFailureSurfacesError(t *testing.T) {
	mock := &supportMock{reviewErr: errors.New("boom")}
	_, err := runSupportWithInput(t, mock, "", "support", "create", "--subject", "x", "--description", "y")
	if err == nil {
		t.Fatal("expected an error when the review call fails")
	}
	if !strings.Contains(err.Error(), "review support ticket") {
		t.Errorf("expected review error context, got: %v", err)
	}
	if mock.createReq != nil {
		t.Error("expected no create call when review fails")
	}
}

func TestSupportCreate_RequiresSubjectAndDescription(t *testing.T) {
	mock := &supportMock{}
	_, err := runSupport(t, mock, "support", "create", "--subject", "only subject")
	if err == nil {
		t.Fatal("expected an error when description is missing")
	}
	if mock.reviewReq != nil {
		t.Error("expected no review call when validation fails")
	}
	if mock.createReq != nil {
		t.Error("expected no create call when validation fails")
	}
}

func TestSupportList_RendersJSON(t *testing.T) {
	mock := &supportMock{list: &client.SupportTicketListResponse{
		Result:     []client.SupportTicketSummary{{ID: "t-1", Subject: "Disk full", Status: "open", IsTrackedByTeam: true, CreatedAt: time.Now()}},
		Pagination: client.SupportTicketPagination{Page: 1, PageSize: 25, TotalPages: 1, TotalCount: 1},
	}}
	out, err := runSupport(t, mock, "support", "list", "-o", "json")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "\"id\": \"t-1\"") || !strings.Contains(out, "Disk full") {
		t.Errorf("expected ticket in json output, got: %s", out)
	}
}

func TestSupportGet_NotFound(t *testing.T) {
	mock := &supportMock{getErr: client.ErrSupportTicketNotFound}
	_, err := runSupport(t, mock, "support", "get", "missing-id")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found message, got: %v", err)
	}
}

func TestSupportComment_SendsBody(t *testing.T) {
	mock := &supportMock{}
	out, err := runSupport(t, mock, "support", "comment", "t-1", "--message", "Any update?")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if mock.commentBody != "Any update?" {
		t.Errorf("expected comment body to be forwarded, got %q", mock.commentBody)
	}
}

func TestSupportComment_RequiresMessage(t *testing.T) {
	mock := &supportMock{}
	_, err := runSupport(t, mock, "support", "comment", "t-1")
	if err == nil {
		t.Fatal("expected an error when --message is missing")
	}
}

func TestSupportAttach_UploadsEachFile(t *testing.T) {
	mock := &supportMock{}
	out, err := runSupport(t, mock, "support", "attach", "t-1", "first.png", "second.png")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.attachCalls) != 2 {
		t.Fatalf("expected 2 uploads, got %d", len(mock.attachCalls))
	}
	if mock.attachCalls[0] != "first.png" || mock.attachCalls[1] != "second.png" {
		t.Errorf("unexpected attach calls: %v", mock.attachCalls)
	}
}

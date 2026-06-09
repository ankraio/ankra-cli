package cmd

import (
	"bytes"
	"context"
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
	setMockClient(t, mock)
	resetSupportFlags(t)
	out := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader("y\n"))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

func TestSupportCreate_ForceSetsAcknowledged(t *testing.T) {
	mock := &supportMock{}
	out, err := runSupport(t, mock, "support", "create", "--subject", "Nodes NotReady", "--description", "All nodes down", "--severity", "high", "--force")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if mock.createReq == nil {
		t.Fatal("expected a create call")
	}
	if !mock.createReq.Acknowledged {
		t.Error("expected --force to set Acknowledged=true")
	}
	if mock.createReq.Severity == nil || *mock.createReq.Severity != "high" {
		t.Errorf("expected severity high, got %+v", mock.createReq.Severity)
	}
	if mock.createReq.Source != "cli" {
		t.Errorf("expected source cli, got %q", mock.createReq.Source)
	}
	if !strings.Contains(out, "ticket-1") {
		t.Errorf("expected created ticket id in output, got: %s", out)
	}
}

func TestSupportCreate_ReviewRequiredSurfacesGuidance(t *testing.T) {
	mock := &supportMock{createErr: client.ErrSupportReviewRequired}
	out, err := runSupport(t, mock, "support", "create", "--subject", "x", "--description", "y")
	if err == nil {
		t.Fatal("expected an error when review is required")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("expected guidance to use --force, got: %v (output: %s)", err, out)
	}
}

func TestSupportCreate_RequiresSubjectAndDescription(t *testing.T) {
	mock := &supportMock{}
	_, err := runSupport(t, mock, "support", "create", "--subject", "only subject")
	if err == nil {
		t.Fatal("expected an error when description is missing")
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

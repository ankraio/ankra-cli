package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type securityTrivyMock struct {
	baseMock
	history         *client.SecurityHistory
	historyDays     int
	historyCluster  string
	schedule        *client.SecurityReportScheduleStatus
	scheduleRequest *client.SecurityReportScheduleRequest
	scheduleCluster string
	sendResult      *client.SecurityReportSendNowResult
	sendCluster     string
}

func (m *securityTrivyMock) GetSecurityHistory(clusterID string, days int) (*client.SecurityHistory, error) {
	m.historyCluster = clusterID
	m.historyDays = days
	return m.history, nil
}

func (m *securityTrivyMock) GetSecurityReportSchedule(string) (*client.SecurityReportScheduleStatus, error) {
	return m.schedule, nil
}

func (m *securityTrivyMock) SetSecurityReportSchedule(clusterID string, request client.SecurityReportScheduleRequest) (*client.SecurityReportScheduleStatus, error) {
	m.scheduleCluster = clusterID
	m.scheduleRequest = &request
	return m.schedule, nil
}

func (m *securityTrivyMock) SendSecurityReportNow(clusterID string) (*client.SecurityReportSendNowResult, error) {
	m.sendCluster = clusterID
	return m.sendResult, nil
}

func TestSecurityHistory_RefusesMissingCluster(t *testing.T) {
	mock := &securityTrivyMock{}
	_, err := runSecurityCommand(t, mock, "security", "history")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestSecurityHistory_RefusesZeroDays(t *testing.T) {
	mock := &securityTrivyMock{}
	_, err := runSecurityCommand(t, mock, "security", "history", "--cluster", securityTestClusterID, "--days", "0")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestSecurityHistory_RendersRowsAndTrend(t *testing.T) {
	mock := &securityTrivyMock{history: &client.SecurityHistory{Days: 30, Items: []client.SecurityHistoryPoint{
		{Date: "2026-08-10", Findings: 40, Critical: 3, High: 12, Medium: 20, Low: 5, ActionableFindings: 30, FixableCritical: 2, FixableHigh: 4, Workloads: 18, Namespaces: 6, ActionableRiskScore: 72.4},
		{Date: "2026-09-06", Findings: 31, Critical: 1, High: 9, Medium: 16, Low: 5, ActionableFindings: 22, FixableCritical: 1, FixableHigh: 3, Workloads: 19, Namespaces: 6, ActionableRiskScore: 55.0},
	}}}
	output, err := runSecurityCommand(t, mock, "security", "history", "--cluster", securityTestClusterID, "--days", "30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.historyCluster != securityTestClusterID || mock.historyDays != 30 {
		t.Fatalf("expected the cluster and window forwarded, got %q %d", mock.historyCluster, mock.historyDays)
	}
	plain := stripANSICodes(output)
	for _, fragment := range []string{"2026-08-10", "2026-09-06", "2 of 30 days have a snapshot", "down 8", "2026-08-10 (30) to 2026-09-06 (22)", "risk 72.4 -> 55.0"} {
		if !strings.Contains(plain, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, plain)
		}
	}
}

func TestSecurityHistory_EmptyWindowIsUnknownNotClean(t *testing.T) {
	mock := &securityTrivyMock{history: &client.SecurityHistory{Days: 90, Items: []client.SecurityHistoryPoint{}}}
	output, err := runSecurityCommand(t, mock, "security", "history", "--cluster", securityTestClusterID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.historyDays != securityHistoryDefaultDays {
		t.Fatalf("expected the default window forwarded, got %d", mock.historyDays)
	}
	if !strings.Contains(output, "No security snapshots in the last 90 days") || !strings.Contains(output, "unknown, not clean") {
		t.Errorf("expected the absent-is-not-clean wording:\n%s", output)
	}
	if strings.Contains(output, " 0 ") {
		t.Errorf("an empty window must not render zero counts:\n%s", output)
	}
}

func TestSecurityReportSchedule_NotConfigured(t *testing.T) {
	mock := &securityTrivyMock{schedule: &client.SecurityReportScheduleStatus{Configured: false}}
	output, err := runSecurityCommand(t, mock, "security", "report-schedule", "--cluster", securityTestClusterID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "not configured") || !strings.Contains(output, "report-schedule set") {
		t.Errorf("expected the not-configured hint:\n%s", output)
	}
}

func TestSecurityReportSchedule_RendersSchedule(t *testing.T) {
	mock := &securityTrivyMock{schedule: &client.SecurityReportScheduleStatus{Configured: true, Schedule: &client.SecurityReportSchedule{
		Frequency: "weekly", Recipients: []string{"sec@acme.io", "ops@acme.io"}, Enabled: true,
		LastSentAt: stringPointer("2026-09-01T06:00:00Z"), NextDueAt: nil, UpdatedAt: "2026-08-30T10:00:00Z"}}}
	output, err := runSecurityCommand(t, mock, "security", "report-schedule", "--cluster", securityTestClusterID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plain := stripANSICodes(output)
	for _, fragment := range []string{"weekly, enabled", "sec@acme.io, ops@acme.io", "last sent:  2026-09-01T06:00:00Z", "next due:   not scheduled"} {
		if !strings.Contains(plain, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, plain)
		}
	}
}

func TestSecurityReportScheduleSet_RefusesBadFrequency(t *testing.T) {
	mock := &securityTrivyMock{}
	_, err := runSecurityCommand(t, mock, "security", "report-schedule", "set", "--cluster", securityTestClusterID, "--frequency", "daily", "--recipient", "sec@acme.io", "--yes")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if mock.scheduleRequest != nil {
		t.Fatalf("expected no write, got %+v", mock.scheduleRequest)
	}
}

func TestSecurityReportScheduleSet_RefusesEnabledWithoutRecipients(t *testing.T) {
	mock := &securityTrivyMock{}
	_, err := runSecurityCommand(t, mock, "security", "report-schedule", "set", "--cluster", securityTestClusterID, "--frequency", "weekly", "--yes")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestSecurityReportScheduleSet_RefusesNonEmailRecipient(t *testing.T) {
	mock := &securityTrivyMock{}
	_, err := runSecurityCommand(t, mock, "security", "report-schedule", "set", "--cluster", securityTestClusterID, "--frequency", "weekly", "--recipient", "not-an-address", "--yes")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestSecurityReportScheduleSet_DeclineWritesNothing(t *testing.T) {
	mock := &securityTrivyMock{}
	_, _, err := runSecurityCommandWithInput(t, mock, "n\n", "security", "report-schedule", "set", "--cluster", securityTestClusterID, "--frequency", "monthly", "--recipient", "sec@acme.io")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled, got %v", err)
	}
	if mock.scheduleRequest != nil {
		t.Fatalf("expected no write after a decline, got %+v", mock.scheduleRequest)
	}
}

func TestSecurityReportScheduleSet_YesForwardsTheSchedule(t *testing.T) {
	mock := &securityTrivyMock{schedule: &client.SecurityReportScheduleStatus{Configured: true, Schedule: &client.SecurityReportSchedule{
		Frequency: "monthly", Recipients: []string{"sec@acme.io", "ops@acme.io"}, Enabled: true, UpdatedAt: "2026-09-07T10:00:00Z"}}}
	output, err := runSecurityCommand(t, mock, "security", "report-schedule", "set", "--cluster", securityTestClusterID,
		"--frequency", "Monthly", "--recipient", "sec@acme.io", "--recipient", " ops@acme.io ", "--recipient", "SEC@acme.io", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.scheduleCluster != securityTestClusterID || mock.scheduleRequest == nil {
		t.Fatalf("expected the write forwarded, got %q %+v", mock.scheduleCluster, mock.scheduleRequest)
	}
	if mock.scheduleRequest.Frequency != "monthly" || !mock.scheduleRequest.Enabled || strings.Join(mock.scheduleRequest.Recipients, ",") != "sec@acme.io,ops@acme.io" {
		t.Fatalf("expected a normalised, de-duplicated request, got %+v", mock.scheduleRequest)
	}
	if !strings.Contains(stripANSICodes(output), "monthly, enabled") {
		t.Errorf("expected the stored schedule rendered:\n%s", output)
	}
}

func TestSecurityReportScheduleSet_DisabledStoresPausedWithoutRecipients(t *testing.T) {
	mock := &securityTrivyMock{schedule: &client.SecurityReportScheduleStatus{Configured: true, Schedule: &client.SecurityReportSchedule{
		Frequency: "weekly", Recipients: []string{}, Enabled: false, UpdatedAt: "2026-09-07T10:00:00Z"}}}
	output, err := runSecurityCommand(t, mock, "security", "report-schedule", "set", "--cluster", securityTestClusterID, "--frequency", "weekly", "--disabled", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.scheduleRequest == nil || mock.scheduleRequest.Enabled || len(mock.scheduleRequest.Recipients) != 0 {
		t.Fatalf("expected a paused schedule with no recipients, got %+v", mock.scheduleRequest)
	}
	if !strings.Contains(stripANSICodes(output), "weekly, paused") || !strings.Contains(output, "recipients: none") {
		t.Errorf("expected the paused schedule rendered:\n%s", output)
	}
}

func TestSecurityReportScheduleSend_DeclineSendsNothing(t *testing.T) {
	mock := &securityTrivyMock{}
	_, _, err := runSecurityCommandWithInput(t, mock, "n\n", "security", "report-schedule", "send", "--cluster", securityTestClusterID)
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled, got %v", err)
	}
	if mock.sendCluster != "" {
		t.Fatalf("expected no send after a decline, got %q", mock.sendCluster)
	}
}

func TestSecurityReportScheduleSend_YesQueues(t *testing.T) {
	mock := &securityTrivyMock{sendResult: &client.SecurityReportSendNowResult{Queued: true, Message: "Report queued for 2 recipients"}}
	output, err := runSecurityCommand(t, mock, "security", "report-schedule", "send", "--cluster", securityTestClusterID, "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.sendCluster != securityTestClusterID || !strings.Contains(output, "Security report queued: Report queued for 2 recipients") {
		t.Errorf("expected the send forwarded and rendered, got %q:\n%s", mock.sendCluster, output)
	}
}

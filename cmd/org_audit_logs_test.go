package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

const auditLogTestClusterID = "6f1c2a3b-4d5e-4f60-8a7b-9c0d1e2f3a4b"

type auditLogsMock struct {
	baseMock
	trail          client.AuditLogTrail
	exportBody     string
	exportResult   client.AuditLogExportResult
	requestError   error
	listFilters    *client.AuditLogFilters
	exportFilters  *client.AuditLogFilters
	exportFormat   string
	clusterLookups []string
}

func (mock *auditLogsMock) ListAuditLogs(_ context.Context, filters client.AuditLogFilters) (*client.AuditLogTrail, error) {
	mock.listFilters = &filters
	if mock.requestError != nil {
		return nil, mock.requestError
	}
	trail := mock.trail
	return &trail, nil
}

func (mock *auditLogsMock) ExportAuditLogs(_ context.Context, filters client.AuditLogFilters, format string,
	destination io.Writer) (*client.AuditLogExportResult, error) {
	mock.exportFilters, mock.exportFormat = &filters, format
	if mock.requestError != nil {
		_, _ = io.WriteString(destination, "partial")
		return nil, mock.requestError
	}
	written, _ := io.WriteString(destination, mock.exportBody)
	result := mock.exportResult
	result.Format, result.BytesWritten = format, int64(written)
	return &result, nil
}

func (mock *auditLogsMock) GetCluster(name string) (client.ClusterListItem, error) {
	mock.clusterLookups = append(mock.clusterLookups, name)
	if name != "prod" {
		return client.ClusterListItem{}, client.NewUnexpectedResponseError(404, "cluster not found")
	}
	return client.ClusterListItem{ID: auditLogTestClusterID, Name: "prod"}, nil
}

func runAuditLogsCommand(t *testing.T, mock APIClient, args ...string) (string, string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	pinnedNow := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	previousNow := auditLogNow
	auditLogNow = func() time.Time { return pinnedNow }
	t.Cleanup(func() { auditLogNow = previousNow })
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(args)
	resetTreeFlags(t, orgAuditLogsListCmd, orgAuditLogsExportCmd, clusterAuditLogsCmd)
	t.Cleanup(func() { resetTreeFlags(t, orgAuditLogsListCmd, orgAuditLogsExportCmd, clusterAuditLogsCmd) })
	executeError := rootCmd.Execute()
	return stdout.String(), stderr.String(), executeError
}

func sampleAuditTrail() client.AuditLogTrail {
	clusterName, resourceID := "prod", "res-1"
	return client.AuditLogTrail{Entries: []client.AuditLogEntry{
		{CreatedAt: "2026-11-08T11:00:00Z", UserEmail: "mark@ankra.ai", ActorType: "agent", ActionType: "delete",
			ResourceType: "pod", TargetKind: "Pod", TargetName: "web-1", ClusterName: &clusterName, Outcome: "failed"},
		{CreatedAt: "2026-11-08T10:00:00Z", UserEmail: "ops@ankra.ai", ActorType: "human", ActionType: "create",
			ResourceType: "token", ResourceID: &resourceID, Outcome: "succeeded"},
		{CreatedAt: "2026-11-08T09:00:00Z", UserEmail: "", ActorType: "system", ActionType: "sync",
			ResourceType: "stack", Outcome: "unknown"},
	}}
}

func TestOrgAuditLogsListRendersTheNewestEntriesAndSendsTheFilters(t *testing.T) {
	mock := &auditLogsMock{trail: sampleAuditTrail()}
	stdout, stderr, runError := runAuditLogsCommand(t, mock, "org", "audit-logs", "list",
		"--actor-type", "Agent", "--outcome", "failed", "--action", "delete", "--cluster", auditLogTestClusterID,
		"--search", "web", "--limit", "2")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	filters := mock.listFilters
	if filters == nil || filters.ActorType != "agent" || filters.Outcome != "failed" || filters.ActionType != "delete" ||
		filters.ClusterID != auditLogTestClusterID || filters.Search != "web" {
		t.Fatalf("filters = %+v", filters)
	}
	wantStart := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if filters.StartDate == nil || !filters.StartDate.Equal(wantStart) || filters.EndDate != nil {
		t.Fatalf("without --since or --until the list reads the last 7 days, got %v / %v", filters.StartDate, filters.EndDate)
	}
	for _, fragment := range []string{"mark@ankra.ai (agent)", "Pod/web-1", "prod", "failed", "token/res-1"} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("expected the table to contain %q, got:\n%s", fragment, stdout)
		}
	}
	if strings.Contains(stdout, "sync") {
		t.Errorf("--limit 2 keeps only the two newest entries:\n%s", stdout)
	}
	if !strings.Contains(stderr, "newest 2 of 3") {
		t.Errorf("the cut is said on stderr, got %q", stderr)
	}
}

func TestOrgAuditLogsListStructuredOutputAndTruncation(t *testing.T) {
	trail := sampleAuditTrail()
	trail.Truncated = true
	mock := &auditLogsMock{trail: trail}
	stdout, stderr, runError := runAuditLogsCommand(t, mock, "org", "audit-logs", "list", "--since", "all", "--limit", "0", "-o", "json")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	if mock.listFilters.StartDate != nil {
		t.Fatalf("--since all reads the whole trail, got %v", mock.listFilters.StartDate)
	}
	var decoded auditLogList
	if decodeError := json.Unmarshal([]byte(stdout), &decoded); decodeError != nil {
		t.Fatalf("stdout must be clean JSON: %v\n%s", decodeError, stdout)
	}
	if decoded.Shown != 3 || decoded.Matched != 3 || !decoded.Truncated || len(decoded.Entries) != 3 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if stderr != "" {
		t.Errorf("structured output prints no hints, got %q", stderr)
	}

	mock = &auditLogsMock{trail: trail}
	_, stderr, runError = runAuditLogsCommand(t, mock, "org", "audit-logs", "list")
	if runError != nil || !strings.Contains(stderr, "cut the trail short") {
		t.Fatalf("a capped trail is reported on stderr: %v %q", runError, stderr)
	}
}

func TestOrgAuditLogsListEmpty(t *testing.T) {
	mock := &auditLogsMock{trail: client.AuditLogTrail{Entries: []client.AuditLogEntry{}}}
	stdout, _, runError := runAuditLogsCommand(t, mock, "org", "audit-logs", "list")
	if runError != nil || !strings.Contains(stdout, "No audit log entries match.") {
		t.Fatalf("empty list: %v %q", runError, stdout)
	}
	stdout, _, runError = runAuditLogsCommand(t, &auditLogsMock{trail: client.AuditLogTrail{Entries: []client.AuditLogEntry{}}},
		"org", "audit-logs", "list", "-o", "json")
	if runError != nil || !strings.Contains(stdout, `"entries": []`) {
		t.Fatalf("an empty structured list keeps an empty array: %v %q", runError, stdout)
	}
}

func TestOrgAuditLogsRefusalsKeepTheirExitCodes(t *testing.T) {
	cases := []struct {
		name         string
		requestError error
		wantCode     int
	}{
		{"unauthorized exits 6", client.ErrUnauthorized, exitAuth},
		{"missing audit.read exits 7", &client.PermissionDeniedError{Permission: "audit.read"}, exitForbidden},
		{"a platform failure exits 1", client.NewUnexpectedResponseError(500, "boom"), exitError},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, runError := runAuditLogsCommand(t, &auditLogsMock{requestError: testCase.requestError},
				"org", "audit-logs", "list")
			if runError == nil || exitCodeFor(runError) != testCase.wantCode {
				t.Fatalf("exit = %d (%v), want %d", exitCodeFor(runError), runError, testCase.wantCode)
			}
		})
	}
}

func TestOrgAuditLogsRejectsBadFlagsBeforeSending(t *testing.T) {
	for _, args := range [][]string{
		{"org", "audit-logs", "list", "--actor-type", "robot"},
		{"org", "audit-logs", "list", "--outcome", "maybe"},
		{"org", "audit-logs", "list", "--user", "mark"},
		{"org", "audit-logs", "list", "--since", "yesterday"},
		{"org", "audit-logs", "list", "--since", "2026-11-08", "--until", "2026-11-01"},
		{"org", "audit-logs", "list", "--limit", "-1"},
		{"org", "audit-logs", "list", "-o", "jsn"},
		{"org", "audit-logs", "export", "--format", "xml"},
		{"cluster", "audit-logs", "prod", "--outcome", "maybe"},
	} {
		mock := &auditLogsMock{trail: sampleAuditTrail()}
		_, _, runError := runAuditLogsCommand(t, mock, args...)
		if runError == nil || exitCodeFor(runError) != exitUsage {
			t.Errorf("%v: exit = %d (%v), want %d", args, exitCodeFor(runError), runError, exitUsage)
		}
		if mock.listFilters != nil || mock.exportFilters != nil || len(mock.clusterLookups) != 0 {
			t.Errorf("%v: nothing may be sent or looked up", args)
		}
	}
}

func TestOrgAuditLogsExportWritesTheFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "audit.json")
	mock := &auditLogsMock{exportBody: `{"entries":[],"truncated":true}`, exportResult: client.AuditLogExportResult{Truncated: true}}
	stdout, stderr, runError := runAuditLogsCommand(t, mock, "org", "audit-logs", "export",
		"--format", "JSON", "--file", filePath, "--since", "2026-11-01", "--until", "2026-11-08T00:00:00Z")
	if runError != nil {
		t.Fatalf("export failed: %v", runError)
	}
	written, readError := os.ReadFile(filePath)
	if readError != nil || string(written) != `{"entries":[],"truncated":true}` {
		t.Fatalf("file = %q (%v)", written, readError)
	}
	if mock.exportFormat != "json" || mock.exportFilters.StartDate == nil ||
		!mock.exportFilters.StartDate.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) ||
		mock.exportFilters.EndDate == nil {
		t.Fatalf("format %q filters %+v", mock.exportFormat, mock.exportFilters)
	}
	if stdout != "" || !strings.Contains(stderr, "Wrote the audit log (json") || !strings.Contains(stderr, "100,000-row cap") {
		t.Fatalf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestOrgAuditLogsExportToStdoutReadsTheWholeTrail(t *testing.T) {
	mock := &auditLogsMock{exportBody: "created_at,user_email\n"}
	stdout, stderr, runError := runAuditLogsCommand(t, mock, "org", "audit-logs", "export")
	if runError != nil {
		t.Fatalf("export failed: %v", runError)
	}
	if stdout != "created_at,user_email\n" || stderr != "" {
		t.Fatalf("stdout %q stderr %q", stdout, stderr)
	}
	if mock.exportFormat != "csv" || mock.exportFilters.StartDate != nil || mock.exportFilters.EndDate != nil {
		t.Fatalf("the export defaults to the whole trail as CSV, got %q %+v", mock.exportFormat, mock.exportFilters)
	}
}

func TestOrgAuditLogsExportFailureRemovesThePartialFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "audit.csv")
	mock := &auditLogsMock{requestError: &client.PermissionDeniedError{Permission: "audit.read"}}
	_, _, runError := runAuditLogsCommand(t, mock, "org", "audit-logs", "export", "--file", filePath)
	if runError == nil || exitCodeFor(runError) != exitForbidden {
		t.Fatalf("exit = %d (%v)", exitCodeFor(runError), runError)
	}
	if _, statError := os.Stat(filePath); !errors.Is(statError, os.ErrNotExist) {
		t.Fatalf("the partial file must be removed, stat = %v", statError)
	}
}

func TestClusterAuditLogsNarrowsToTheCluster(t *testing.T) {
	mock := &auditLogsMock{trail: sampleAuditTrail()}
	stdout, _, runError := runAuditLogsCommand(t, mock, "cluster", "audit-logs", "prod", "--since", "24h")
	if runError != nil {
		t.Fatalf("cluster audit-logs failed: %v", runError)
	}
	if mock.listFilters == nil || mock.listFilters.ClusterID != auditLogTestClusterID {
		t.Fatalf("filters = %+v", mock.listFilters)
	}
	if mock.listFilters.StartDate == nil || !mock.listFilters.StartDate.Equal(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("--since 24h = %v", mock.listFilters.StartDate)
	}
	if !strings.Contains(stdout, "Pod/web-1") {
		t.Fatalf("stdout = %s", stdout)
	}

	mock = &auditLogsMock{trail: sampleAuditTrail()}
	_, _, runError = runAuditLogsCommand(t, mock, "cluster", "audit-logs", "staging")
	if runError == nil || exitCodeFor(runError) != exitNotFound || mock.listFilters != nil {
		t.Fatalf("an unknown cluster exits 3 before reading: %d (%v)", exitCodeFor(runError), runError)
	}
}

func TestParseAuditTimeBound(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"90m":                       now.Add(-90 * time.Minute),
		"7d":                        now.AddDate(0, 0, -7),
		"2w":                        now.AddDate(0, 0, -14),
		"2026-11-01":                time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		"2026-11-01T10:00:00+02:00": time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
	}
	for raw, want := range cases {
		bound, parseError := parseAuditTimeBound(raw, now)
		if parseError != nil || bound == nil || !bound.Equal(want) {
			t.Errorf("%q = %v (%v), want %v", raw, bound, parseError, want)
		}
	}
	for _, unbounded := range []string{"", "all", "ALL"} {
		if bound, parseError := parseAuditTimeBound(unbounded, now); bound != nil || parseError != nil {
			t.Errorf("%q = %v (%v), want no bound", unbounded, bound, parseError)
		}
	}
	for _, invalid := range []string{"0d", "-3h", "xd", "soon"} {
		if _, parseError := parseAuditTimeBound(invalid, now); parseError == nil {
			t.Errorf("%q must be refused", invalid)
		}
	}
}

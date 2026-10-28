package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"

	"ankra/internal/client"
)

func TestIsTerminalExecutionStatus(t *testing.T) {
	cases := []struct {
		status       string
		wantTerminal bool
	}{
		{"success", true},
		{"failed", true},
		{"critical", true},
		{"cancelled", true},
		{"timeout", true},
		{"SUCCESS", true},
		{"  Failed  ", true},
		{"running", false},
		{"stopping", false},
		{"cleanup", false},
		{"pending", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isTerminalExecutionStatus(tc.status); got != tc.wantTerminal {
			t.Errorf("isTerminalExecutionStatus(%q) = %v, want %v", tc.status, got, tc.wantTerminal)
		}
	}
}

func TestNormaliseAttentionFlag(t *testing.T) {
	for _, value := range []string{"", "open", "Resolved", " open "} {
		if _, err := normaliseAttentionFlag(value); err != nil {
			t.Errorf("normaliseAttentionFlag(%q) = %v, want nil", value, err)
		}
	}
	if _, err := normaliseAttentionFlag("dismissed"); err == nil {
		t.Error("normaliseAttentionFlag(\"dismissed\") = nil, want an error")
	}
}

func TestRenderAttention(t *testing.T) {
	resolvedBy := "1471b248-b9e1-4106-aafd-d127fbad9e0b"
	cases := []struct {
		summary client.ExecutionSummary
		want    string
	}{
		{client.ExecutionSummary{Status: "success"}, ""},
		{client.ExecutionSummary{Status: "failed", AttentionState: "open"}, "open"},
		{client.ExecutionSummary{Status: "failed", AttentionState: "resolved"}, "resolved"},
		{client.ExecutionSummary{Status: "failed", AttentionState: "resolved", ResolvedByExecutionID: &resolvedBy}, "resolved by " + resolvedBy},
	}
	for _, testCase := range cases {
		if got := text.StripEscape(renderAttention(testCase.summary)); got != testCase.want {
			t.Errorf("renderAttention(%+v) = %q, want %q", testCase.summary, got, testCase.want)
		}
	}
}

func TestClampWatchInterval(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, defaultWatchInterval},
		{-5 * time.Second, defaultWatchInterval},
		{10 * time.Millisecond, minWatchInterval},
		{minWatchInterval, minWatchInterval},
		{10 * time.Second, 10 * time.Second},
	}
	for _, tc := range cases {
		if got := clampWatchInterval(tc.in); got != tc.want {
			t.Errorf("clampWatchInterval(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestEncodeStructured(t *testing.T) {
	value := map[string]string{"name": "demo", "status": "running"}

	t.Run("json", func(t *testing.T) {
		buf := new(bytes.Buffer)
		if err := encodeStructured(buf, outputJSON, value); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, `"name": "demo"`) || !strings.Contains(out, `"status": "running"`) {
			t.Errorf("json output missing fields: %q", out)
		}
	})

	t.Run("yaml", func(t *testing.T) {
		buf := new(bytes.Buffer)
		if err := encodeStructured(buf, outputYAML, value); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "name: demo") || !strings.Contains(out, "status: running") {
			t.Errorf("yaml output missing fields: %q", out)
		}
	})

	t.Run("default is a no-op", func(t *testing.T) {
		buf := new(bytes.Buffer)
		if err := encodeStructured(buf, outputDefault, value); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if buf.Len() != 0 {
			t.Errorf("expected no output for default format, got %q", buf.String())
		}
	})
}

func TestDeleteClusterDryRun(t *testing.T) {
	originalDryRun := dryRunDelete
	dryRunDelete = true
	t.Cleanup(func() { dryRunDelete = originalDryRun })

	output := captureStdout(t, func() {
		if err := deleteClusterCmd.RunE(deleteClusterCmd, []string{"my-cluster"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(output, "my-cluster") || !strings.Contains(output, "--dry-run") {
		t.Errorf("expected dry-run notice naming the cluster, got: %q", output)
	}
	if !strings.Contains(output, "Would delete") {
		t.Errorf("expected 'Would delete' notice, got: %q", output)
	}
}

// executionSummaryFixture is one row of the executions listing, named and
// timed the way the API sends them.
func executionSummaryFixture(id string, displayName string, status string, createdAt string) client.ExecutionSummary {
	created := createdAt
	updated := createdAt
	return client.ExecutionSummary{
		ID:          id,
		Name:        strings.ToLower(strings.ReplaceAll(displayName, " ", "_")),
		DisplayName: displayName,
		Type:        "write",
		Status:      status,
		StepSummary: client.StepSummary{Total: 1, Succeeded: 1},
		CreatedAt:   &created,
		UpdatedAt:   &updated,
	}
}

// TestCollapseRepeatedExecutionsFoldsARunOfIdenticalSuccesses pins the fold
// the customer asked for: an add-on reconciling every two minutes is one row
// carrying the count and the window it spans, not sixteen rows pushing the
// failures that explain an outage off the page (PLA-863).
func TestCollapseRepeatedExecutionsFoldsARunOfIdenticalSuccesses(t *testing.T) {
	executions := []client.ExecutionSummary{
		executionSummaryFixture("execution-3", "Update addon defectdojo", "success", "2026-09-14T10:04:00Z"),
		executionSummaryFixture("execution-2", "Update addon defectdojo", "success", "2026-09-14T10:02:00Z"),
		executionSummaryFixture("execution-1", "Update addon defectdojo", "success", "2026-09-14T10:00:00Z"),
		executionSummaryFixture("execution-0", "Deploy stack commerce", "failed", "2026-09-14T09:58:00Z"),
	}

	runs := collapseRepeatedExecutions(executions)

	if len(runs) != 2 {
		t.Fatalf("collapsed rows = %d, want 2", len(runs))
	}
	if runs[0].count != 3 || runs[0].execution.ID != "execution-3" {
		t.Errorf("first row = %+v, want 3 folded executions keeping the newest id", runs[0])
	}
	if runs[0].earliest == nil || *runs[0].earliest != "2026-09-14T10:00:00Z" {
		t.Errorf("first row spans from %v, want the oldest execution in the run", runs[0].earliest)
	}
	if runs[0].latest == nil || *runs[0].latest != "2026-09-14T10:04:00Z" {
		t.Errorf("first row spans to %v, want the newest execution in the run", runs[0].latest)
	}
	if runs[1].count != 1 || runs[1].execution.ID != "execution-0" {
		t.Errorf("second row = %+v, want the failure kept on its own", runs[1])
	}
}

// TestCollapseRepeatedExecutionsKeepsEveryFailureAndEveryOtherName pins what
// must never be folded away: a failure, and a success with a different name
// beside it.
func TestCollapseRepeatedExecutionsKeepsEveryFailureAndEveryOtherName(t *testing.T) {
	executions := []client.ExecutionSummary{
		executionSummaryFixture("execution-4", "Update addon kyverno", "failed", "2026-09-14T10:08:00Z"),
		executionSummaryFixture("execution-3", "Update addon kyverno", "failed", "2026-09-14T10:06:00Z"),
		executionSummaryFixture("execution-2", "Update addon kyverno", "success", "2026-09-14T10:04:00Z"),
		executionSummaryFixture("execution-1", "Update addon defectdojo", "success", "2026-09-14T10:02:00Z"),
		executionSummaryFixture("execution-0", "Update addon kyverno", "success", "2026-09-14T10:00:00Z"),
	}

	runs := collapseRepeatedExecutions(executions)

	if len(runs) != 5 {
		t.Fatalf("collapsed rows = %d, want every row kept", len(runs))
	}
	for _, run := range runs {
		if run.count != 1 {
			t.Errorf("row %s folded %d executions, want 1", run.execution.ID, run.count)
		}
	}
}

func TestExecutionMatchesName(t *testing.T) {
	execution := executionSummaryFixture("execution-1", "Update addon defectdojo", "success", "2026-09-14T10:00:00Z")
	for _, nameFilter := range []string{"", "defectdojo", "DEFECTDOJO", "addon defect", "update_addon"} {
		if !executionMatchesName(execution, nameFilter) {
			t.Errorf("executionMatchesName(%q) = false, want true", nameFilter)
		}
	}
	if executionMatchesName(execution, "kyverno") {
		t.Error("executionMatchesName(\"kyverno\") = true, want false")
	}
}

func TestPrintCollapsedExecutionsNoticeNamesALoop(t *testing.T) {
	runs := []collapsedExecutionRun{
		{execution: executionSummaryFixture("execution-1", "Update addon defectdojo", "success", "2026-09-14T10:00:00Z"), count: 16},
		{execution: executionSummaryFixture("execution-0", "Deploy stack commerce", "success", "2026-09-14T09:00:00Z"), count: 1},
	}
	notice := new(bytes.Buffer)

	printCollapsedExecutionsNotice(notice, runs)

	if !strings.Contains(notice.String(), "15 repeated successful execution(s) collapsed") {
		t.Errorf("notice = %q, want the number of folded rows", notice.String())
	}
	if !strings.Contains(notice.String(), "Update addon defectdojo") ||
		!strings.Contains(notice.String(), "without a change in desired state") {
		t.Errorf("notice = %q, want the repeating add-on named as a reapply loop", notice.String())
	}
}

func TestPrintCollapsedExecutionsNoticeSaysNothingWhenNothingFolded(t *testing.T) {
	runs := []collapsedExecutionRun{
		{execution: executionSummaryFixture("execution-0", "Deploy stack commerce", "success", "2026-09-14T09:00:00Z"), count: 1},
	}
	notice := new(bytes.Buffer)

	printCollapsedExecutionsNotice(notice, runs)

	if notice.Len() != 0 {
		t.Errorf("notice = %q, want nothing when every row stands on its own", notice.String())
	}
}

// executionsPageMock serves a fixed set of pages, recording what each call
// asked for.
type executionsPageMock struct {
	baseMock
	pages            [][]client.ExecutionSummary
	requestedOptions []client.ListExecutionsOptions
}

func (mock *executionsPageMock) ListExecutions(options client.ListExecutionsOptions) (client.ExecutionListResponse, error) {
	mock.requestedOptions = append(mock.requestedOptions, options)
	pagination := client.Pagination{Page: options.Page, PageSize: options.PageSize, TotalPages: len(mock.pages)}
	if options.Page < 1 || options.Page > len(mock.pages) {
		return client.ExecutionListResponse{Pagination: pagination}, nil
	}
	return client.ExecutionListResponse{Result: mock.pages[options.Page-1], Pagination: pagination}, nil
}

// TestExecutionsQueryWalksPagesForANameFilter pins that --name is not bounded
// by one page: the executions API takes no name filter, and the rows a person
// is looking for on a cluster with a reconcile loop are pages back (PLA-863).
func TestExecutionsQueryWalksPagesForANameFilter(t *testing.T) {
	mock := &executionsPageMock{pages: [][]client.ExecutionSummary{
		{
			executionSummaryFixture("execution-3", "Update addon defectdojo", "success", "2026-09-14T10:04:00Z"),
			executionSummaryFixture("execution-2", "Update addon defectdojo", "success", "2026-09-14T10:02:00Z"),
		},
		{
			executionSummaryFixture("execution-1", "Update addon kyverno", "failed", "2026-09-14T09:02:00Z"),
			executionSummaryFixture("execution-0", "Update addon defectdojo", "success", "2026-09-14T09:00:00Z"),
		},
	}}
	setMockClient(t, mock)

	query := executionsQuery{
		options:    client.ListExecutionsOptions{ClusterID: "cluster-uuid", Page: 1, PageSize: 50},
		nameFilter: "kyverno",
		limit:      50,
	}
	page, fetchError := query.fetch()

	if fetchError != nil {
		t.Fatalf("fetch error = %v", fetchError)
	}
	if len(page.executions) != 1 || page.executions[0].ID != "execution-1" {
		t.Fatalf("matched = %+v, want only the kyverno execution from the second page", page.executions)
	}
	if len(mock.requestedOptions) != 2 {
		t.Fatalf("API calls = %d, want both pages read", len(mock.requestedOptions))
	}
	if mock.requestedOptions[0].PageSize != maxExecutionsPageSize {
		t.Errorf("page size = %d, want the largest page the API serves", mock.requestedOptions[0].PageSize)
	}
}

// TestExecutionsQueryStopsAtTheLimit pins that the walk stops as soon as it
// has the rows that were asked for, rather than reading every page.
func TestExecutionsQueryStopsAtTheLimit(t *testing.T) {
	mock := &executionsPageMock{pages: [][]client.ExecutionSummary{
		{
			executionSummaryFixture("execution-3", "Update addon defectdojo", "success", "2026-09-14T10:04:00Z"),
			executionSummaryFixture("execution-2", "Update addon defectdojo", "success", "2026-09-14T10:02:00Z"),
		},
		{executionSummaryFixture("execution-1", "Update addon defectdojo", "success", "2026-09-14T09:02:00Z")},
	}}
	setMockClient(t, mock)

	query := executionsQuery{
		options:    client.ListExecutionsOptions{ClusterID: "cluster-uuid", Page: 1, PageSize: 2},
		nameFilter: "defectdojo",
		limit:      2,
	}
	page, fetchError := query.fetch()

	if fetchError != nil {
		t.Fatalf("fetch error = %v", fetchError)
	}
	if len(page.executions) != 2 {
		t.Fatalf("matched = %d rows, want the limit", len(page.executions))
	}
	if len(mock.requestedOptions) != 1 {
		t.Errorf("API calls = %d, want the walk to stop once the limit was reached", len(mock.requestedOptions))
	}
}

// TestExecutionsQueryReadsOnePageWhenItCan pins that an unfiltered listing
// still costs exactly one call, asking for the page size the caller set.
func TestExecutionsQueryReadsOnePageWhenItCan(t *testing.T) {
	mock := &executionsPageMock{pages: [][]client.ExecutionSummary{
		{executionSummaryFixture("execution-1", "Update addon defectdojo", "success", "2026-09-14T10:04:00Z")},
	}}
	setMockClient(t, mock)

	query := executionsQuery{
		options: client.ListExecutionsOptions{ClusterID: "cluster-uuid", Page: 1, PageSize: 50},
		limit:   50,
	}
	page, fetchError := query.fetch()

	if fetchError != nil {
		t.Fatalf("fetch error = %v", fetchError)
	}
	if len(page.executions) != 1 {
		t.Fatalf("matched = %d rows, want the single page", len(page.executions))
	}
	if len(mock.requestedOptions) != 1 || mock.requestedOptions[0].PageSize != 50 {
		t.Errorf("requested options = %+v, want one call at the caller's page size", mock.requestedOptions)
	}
}

// resetExecutionsListFlags puts the shared list command's flags back after a
// test sets them: the command is a package-level variable, so a flag one test
// sets stays set for every test that runs after it.
func resetExecutionsListFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = clusterOperationsListCmd.Flags().Set("name", "")
		_ = clusterOperationsListCmd.Flags().Set("no-collapse", "false")
		_ = clusterOperationsListCmd.Flags().Set("limit", fmt.Sprintf("%d", defaultExecutionsPageSize))
	})
}

// TestClusterOperationsListCollapsesRepeatsUnlessAskedNotTo drives the whole
// command: the reconcile loop is one row with a count by default, and
// --no-collapse still prints every execution.
func TestClusterOperationsListCollapsesRepeatsUnlessAskedNotTo(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetExecutionsListFlags(t)
	mock := &executionsPageMock{pages: [][]client.ExecutionSummary{{
		executionSummaryFixture("execution-2", "Update addon defectdojo", "success", "2026-09-14T10:04:00Z"),
		executionSummaryFixture("execution-1", "Update addon defectdojo", "success", "2026-09-14T10:02:00Z"),
	}}}
	setMockClient(t, mock)

	collapsedOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "operations", "list")
	})
	if !strings.Contains(collapsedOutput, "(x2)") {
		t.Errorf("output = %q, want the repeat folded into one row with a count", collapsedOutput)
	}
	if strings.Contains(collapsedOutput, "execution-1") {
		t.Errorf("output = %q, want only the newest execution of the run listed", collapsedOutput)
	}

	expandedOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "operations", "list", "--no-collapse")
	})
	if !strings.Contains(expandedOutput, "execution-1") || !strings.Contains(expandedOutput, "execution-2") {
		t.Errorf("output = %q, want every execution listed under --no-collapse", expandedOutput)
	}
}

// TestClusterOperationsListNameFilterKeepsOnlyMatchingRows drives --name
// through the command surface.
func TestClusterOperationsListNameFilterKeepsOnlyMatchingRows(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetExecutionsListFlags(t)
	mock := &executionsPageMock{pages: [][]client.ExecutionSummary{{
		executionSummaryFixture("execution-2", "Update addon defectdojo", "success", "2026-09-14T10:04:00Z"),
		executionSummaryFixture("execution-1", "Deploy stack commerce", "failed", "2026-09-14T10:02:00Z"),
	}}}
	setMockClient(t, mock)

	output := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "operations", "list", "--name", "commerce")
	})

	if !strings.Contains(output, "execution-1") {
		t.Errorf("output = %q, want the matching execution", output)
	}
	if strings.Contains(output, "execution-2") {
		t.Errorf("output = %q, want the non-matching execution filtered out", output)
	}
}

// TestCollapseRepeatedExecutionsKeepsTwoOperationsSharingADisplayName pins
// that the display name alone never merges two rows: two different pieces of
// work can be shown under the same label, and folding them would put a count
// on a row that does not speak for the other.
func TestCollapseRepeatedExecutionsKeepsTwoOperationsSharingADisplayName(t *testing.T) {
	first := executionSummaryFixture("execution-1", "Update addon", "success", "2026-09-14T10:02:00Z")
	second := executionSummaryFixture("execution-0", "Update addon", "success", "2026-09-14T10:00:00Z")
	second.Name = "update_addon_kyverno"

	runs := collapseRepeatedExecutions([]client.ExecutionSummary{first, second})

	if len(runs) != 2 {
		t.Fatalf("collapsed rows = %d, want both kept when the stored names differ", len(runs))
	}
}

// TestCollapsedRunSpansAMixOfTimestampOffsets pins that the window a
// collapsed row reports is built from parsed instants, so a listing mixing
// "Z" and "+02:00" is not ordered by the text of the offset.
func TestCollapsedRunSpansAMixOfTimestampOffsets(t *testing.T) {
	newest := executionSummaryFixture("execution-1", "Update addon defectdojo", "success", "2026-09-14T12:30:00+02:00")
	oldest := executionSummaryFixture("execution-0", "Update addon defectdojo", "success", "2026-09-14T10:00:00Z")

	runs := collapseRepeatedExecutions([]client.ExecutionSummary{newest, oldest})

	if len(runs) != 1 || runs[0].count != 2 {
		t.Fatalf("collapsed rows = %+v, want one row folding both", runs)
	}
	if runs[0].earliest == nil || *runs[0].earliest != "2026-09-14T10:00:00Z" {
		t.Errorf("run starts at %v, want the earlier instant rather than the earlier text", runs[0].earliest)
	}
	if runs[0].latest == nil || *runs[0].latest != "2026-09-14T12:30:00+02:00" {
		t.Errorf("run ends at %v, want the later instant rather than the later text", runs[0].latest)
	}
}

// fullExecutionsPage is a page of the largest size the API serves, so a test
// can make the walk keep going.
func fullExecutionsPage(prefix string, displayName string) []client.ExecutionSummary {
	page := make([]client.ExecutionSummary, 0, maxExecutionsPageSize)
	for index := 0; index < maxExecutionsPageSize; index++ {
		page = append(page, executionSummaryFixture(fmt.Sprintf("%s-%d", prefix, index), displayName, "success",
			"2026-09-14T10:00:00Z"))
	}
	return page
}

// TestExecutionsQueryReportsACappedWalk pins that a walk which stopped at its
// page cap says so, because "nothing matched in the pages I read" is not the
// same answer as "nothing matched", and reporting the second would send a
// person away from rows that do exist.
func TestExecutionsQueryReportsACappedWalk(t *testing.T) {
	pages := make([][]client.ExecutionSummary, 0, maxExecutionsPagesWalked+1)
	for pageNumber := 0; pageNumber <= maxExecutionsPagesWalked; pageNumber++ {
		pages = append(pages, fullExecutionsPage(fmt.Sprintf("page-%d", pageNumber), "Update addon defectdojo"))
	}
	mock := &executionsPageMock{pages: pages}
	setMockClient(t, mock)

	query := executionsQuery{
		options:    client.ListExecutionsOptions{ClusterID: "cluster-uuid", Page: 1, PageSize: 50},
		nameFilter: "kyverno",
		limit:      50,
	}
	page, fetchError := query.fetch()

	if fetchError != nil {
		t.Fatalf("fetch error = %v", fetchError)
	}
	if len(page.executions) != 0 {
		t.Fatalf("matched = %d rows, want none", len(page.executions))
	}
	if !page.stoppedAtCap {
		t.Error("stoppedAtCap = false, want the walk to report that it stopped short of the data")
	}
	if len(mock.requestedOptions) != maxExecutionsPagesWalked {
		t.Errorf("API calls = %d, want the walk bounded at %d pages", len(mock.requestedOptions), maxExecutionsPagesWalked)
	}
}

// TestExecutionsQueryDoesNotReportACappedWalkWhenItReadEverything pins the
// other side: a walk that reached the end of the listing reports nothing.
func TestExecutionsQueryDoesNotReportACappedWalkWhenItReadEverything(t *testing.T) {
	mock := &executionsPageMock{pages: [][]client.ExecutionSummary{
		{executionSummaryFixture("execution-1", "Update addon defectdojo", "success", "2026-09-14T10:04:00Z")},
	}}
	setMockClient(t, mock)

	query := executionsQuery{
		options:    client.ListExecutionsOptions{ClusterID: "cluster-uuid", Page: 1, PageSize: 50},
		nameFilter: "kyverno",
		limit:      50,
	}
	page, fetchError := query.fetch()

	if fetchError != nil {
		t.Fatalf("fetch error = %v", fetchError)
	}
	if page.stoppedAtCap {
		t.Error("stoppedAtCap = true, want a walk that reached the end of the listing to say nothing")
	}
}

func TestPrintExecutionsReadCapNoticeOnlySpeaksForACappedWalk(t *testing.T) {
	query := executionsQuery{nameFilter: "kyverno"}
	notice := new(bytes.Buffer)

	printExecutionsReadCapNotice(notice, query, executionsPage{stoppedAtCap: false})
	if notice.Len() != 0 {
		t.Errorf("notice = %q, want nothing when the whole listing was read", notice.String())
	}

	printExecutionsReadCapNotice(notice, query, executionsPage{stoppedAtCap: true})
	if !strings.Contains(notice.String(), fmt.Sprintf("%d most recent executions", executionsReadCap())) {
		t.Errorf("notice = %q, want how far back the listing read", notice.String())
	}
	if !strings.Contains(notice.String(), "kyverno") {
		t.Errorf("notice = %q, want the filter named in the advice", notice.String())
	}
}

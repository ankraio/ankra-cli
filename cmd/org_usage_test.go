package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"ankra/internal/client"
)

// `ankra org usage` runs here against an in-memory platform serving GET
// /api/v1/org/billing/usage through the real client, so each test sees the
// query the platform would, and renders what the platform answers.

const usageTestApplicationID = "0a000000-0000-4000-8000-000000000002"

// usageFakePlatform answers the usage read with one fixed status and body,
// and records every query it was asked. It also serves the applications
// listing the application grouping reads names from.
type usageFakePlatform struct {
	t      *testing.T
	server *httptest.Server
	mutex  sync.Mutex
	status int
	body   any
	calls  []url.Values
	// applicationsStatus is what the applications listing answers; 0 is 200
	// with one application named storefront.
	applicationsStatus int
	applicationReads   int
}

func newUsageFakePlatform(t *testing.T, status int, body any) *usageFakePlatform {
	t.Helper()
	platform := &usageFakePlatform{t: t, status: status, body: body}
	platform.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/org/applications":
			platform.mutex.Lock()
			platform.applicationReads++
			platform.mutex.Unlock()
			if platform.applicationsStatus != 0 {
				writer.WriteHeader(platform.applicationsStatus)
				_, _ = writer.Write([]byte(`{"detail":"permission_denied","permission":"applications.read"}`))
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"result":     []any{map[string]any{"id": usageTestApplicationID, "name": "storefront"}},
				"pagination": map[string]any{"total_pages": 1, "page": 1, "page_size": 100, "total_count": 1},
			})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/org/billing/usage":
			platform.mutex.Lock()
			platform.calls = append(platform.calls, request.URL.Query())
			platform.mutex.Unlock()
			writer.WriteHeader(platform.status)
			if encodeError := json.NewEncoder(writer).Encode(platform.body); encodeError != nil {
				t.Errorf("encode: %v", encodeError)
			}
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(platform.server.Close)
	return platform
}

func (platform *usageFakePlatform) queries() []url.Values {
	platform.mutex.Lock()
	defer platform.mutex.Unlock()
	return append([]url.Values(nil), platform.calls...)
}

// runOrgUsageCommand runs `ankra org usage <arguments>` against the fake
// platform on a fresh command, so no flag leaks between tests, with the
// clock pinned to a known day.
func runOrgUsageCommand(t *testing.T, platform *usageFakePlatform, arguments ...string) (string, string, error) {
	t.Helper()
	useTestClient(t, platform.server.URL)
	previousNow := usageNow
	usageNow = func() time.Time { return time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { usageNow = previousNow })
	command := newOrgUsageCommand()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs(arguments)
	runError := command.Execute()
	return stdout.String(), stderr.String(), runError
}

// usageLineFixture is one line as the platform sends it. Every field the
// contract makes required is present, so the test documents the shape.
func usageLineFixture(meter string, unit string, quantity *int64, coverage string, priceState string, priceBasis string, amountMinor *int64) map[string]any {
	var currency any
	if amountMinor != nil {
		currency = "EUR"
	}
	return map[string]any{
		"meter": meter, "unit": unit,
		"period_start": "2026-10-01T00:00:00Z", "period_end": "2026-10-08T12:00:00Z",
		"cluster_id": nil, "cluster_name": nil, "application_id": nil, "instance_id": nil, "instance_name": nil,
		"is_shared": false, "quantity": quantity, "coverage": coverage,
		"price_state": priceState, "price_basis": priceBasis, "amount_minor": amountMinor, "currency": currency,
	}
}

func usageReportFixture(groupBy string, granularity string, lines ...map[string]any) map[string]any {
	if lines == nil {
		lines = []map[string]any{}
	}
	return map[string]any{
		"from": "2026-10-01T00:00:00Z", "to": "2026-10-08T12:00:00Z",
		"granularity": granularity, "group_by": groupBy, "lines": lines,
	}
}

// usageRowFor returns the table row that holds label, so an assertion is
// about that line and not about text elsewhere in the output.
func usageRowFor(t *testing.T, output string, label string) string {
	t.Helper()
	for _, row := range strings.Split(output, "\n") {
		if strings.Contains(row, label) {
			return row
		}
	}
	t.Fatalf("no row for %q in:\n%s", label, output)
	return ""
}

// Every coverage and price state the platform can answer renders as the
// contract says: unknown is 'not measured' and never 0, partial is a lower
// bound, included is the pilot, priced is the EUR amount, unpriced is the
// reason. Quantities read in human units.
func TestOrgUsageRendersEveryCoverageAndPriceState(t *testing.T) {
	platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("meter", "month",
		usageLineFixture("hosted_logs_ingested_bytes", "bytes", int64Pointer(1_500_000_000), "complete", "included", "pilot", nil),
		usageLineFixture("service_instance_seconds", "seconds", int64Pointer(7_200), "partial", "included", "pilot", nil),
		usageLineFixture("service_storage_byte_hours", "byte_hours", nil, "unknown", "included", "pilot", nil),
		usageLineFixture("worker_vcpu_hours", "vcpu_hours", int64Pointer(1_234), "complete", "priced", "plan_rate", int64Pointer(12_345)),
		usageLineFixture("ai_requests", "requests", int64Pointer(42), "complete", "unpriced", "no_rate", nil),
		usageLineFixture("playground_seconds", "seconds", int64Pointer(3_600), "complete", "unpriced", "other_currency", nil),
	))
	stdout, stderr, runError := runOrgUsageCommand(t, platform)
	if runError != nil {
		t.Fatalf("org usage: %v\nstderr: %s", runError, stderr)
	}
	if queries := platform.queries(); len(queries) != 1 {
		t.Fatalf("expected one usage read, got %d", len(queries))
	} else {
		query := queries[0]
		if query.Get("granularity") != "month" || query.Get("group_by") != "meter" {
			t.Errorf("default query should ask for month periods by meter, got %s", query.Encode())
		}
		if query.Has("from") || query.Has("to") {
			t.Errorf("the default window is the platform's (month to date); the query must not pin it: %s", query.Encode())
		}
	}
	output := stripANSICodes(stdout)
	if !strings.Contains(output, "Usage from 2026-10-01 to 2026-10-08 12:00 UTC (month periods, by meter)") {
		t.Errorf("the header should name the window as the platform bounded it:\n%s", output)
	}
	rows := []struct{ label, quantity, price string }{
		{"Hosted logs (bytes)", "1.5 GB", "included (pilot)"},
		{"Managed service time", ">= 2.0 hours", "included (pilot)"},
		{"Managed service storage", "not measured", "included (pilot)"},
		{"Worker vCPUs", "1,234 vCPU-hours", "€123.45"},
		{"AI requests", "42 requests", "no rate published"},
		{"Playgrounds", "1.0 hours", "price not in EUR"},
	}
	for _, expected := range rows {
		row := usageRowFor(t, output, expected.label)
		if !strings.Contains(row, expected.quantity) || !strings.Contains(row, expected.price) {
			t.Errorf("row %q should read %q and %q: %q", expected.label, expected.quantity, expected.price, row)
		}
	}
	if row := usageRowFor(t, output, "Managed service storage"); strings.Contains(row, " 0 ") || strings.Contains(row, "0.0") {
		t.Errorf("an unknown period must never read as zero: %q", row)
	}
	for _, legend := range []string{
		"not measured: no hour of that period was measured",
		">=: only part of that period was measured",
		"included (pilot): Ankra publishes no rate",
	} {
		if !strings.Contains(output, legend) {
			t.Errorf("output should explain %q:\n%s", legend, output)
		}
	}
	if strings.Contains(output, "do not add up") {
		t.Errorf("the shared-usage note belongs to the application grouping only:\n%s", output)
	}
	if stderr != "" {
		t.Errorf("nothing belongs on stderr for a successful human read, got %q", stderr)
	}
}

// A line the platform marks unknown reads 'not measured' even if it carried
// a number, and the legend explains only the markers the table shows.
func TestOrgUsageUnknownCoverageWinsOverAQuantity(t *testing.T) {
	platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("meter", "month",
		usageLineFixture("hosted_metrics_samples", "samples", int64Pointer(0), "unknown", "included", "pilot", nil),
	))
	stdout, _, runError := runOrgUsageCommand(t, platform)
	if runError != nil {
		t.Fatalf("org usage: %v", runError)
	}
	output := stripANSICodes(stdout)
	if row := usageRowFor(t, output, "Hosted metrics (samples)"); !strings.Contains(row, "not measured") || strings.Contains(row, "0 samples") {
		t.Errorf("an unknown period must read 'not measured', never its number: %q", row)
	}
	if strings.Contains(output, ">=:") {
		t.Errorf("no partial line, so no lower-bound legend:\n%s", output)
	}
}

// -o json carries the platform's own values: the raw quantity and unit, a
// null quantity for an unknown period, and nothing else on stdout. -o yaml
// does the same, and -o table is the human rendering.
func TestOrgUsageStructuredOutputKeepsRawValuesAndNullQuantity(t *testing.T) {
	platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("meter", "month",
		usageLineFixture("hosted_logs_ingested_bytes", "bytes", int64Pointer(1_500_000_000), "complete", "included", "pilot", nil),
		usageLineFixture("service_storage_byte_hours", "byte_hours", nil, "unknown", "included", "pilot", nil),
	))
	stdout, stderr, runError := runOrgUsageCommand(t, platform, "-o", "json")
	if runError != nil {
		t.Fatalf("org usage -o json: %v", runError)
	}
	var report client.UsageReport
	if unmarshalError := json.Unmarshal([]byte(stdout), &report); unmarshalError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", unmarshalError, stdout)
	}
	if len(report.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(report.Lines))
	}
	if report.Lines[0].Quantity == nil || *report.Lines[0].Quantity != 1_500_000_000 || report.Lines[0].Unit != "bytes" {
		t.Errorf("JSON must keep the raw quantity and unit, got %+v", report.Lines[0])
	}
	if report.Lines[1].Quantity != nil {
		t.Errorf("an unknown period's quantity must stay null in JSON, got %d", *report.Lines[1].Quantity)
	}
	if !strings.Contains(stdout, `"quantity": null`) {
		t.Errorf("the null quantity must be written out, not omitted:\n%s", stdout)
	}
	if strings.Contains(stdout, "not measured") || strings.Contains(stdout, "GB") || stderr != "" {
		t.Errorf("human renderings do not belong in structured output:\n%s\nstderr: %s", stdout, stderr)
	}

	stdout, _, runError = runOrgUsageCommand(t, platform, "-o", "yaml")
	if runError != nil {
		t.Fatalf("org usage -o yaml: %v", runError)
	}
	if !strings.Contains(stdout, "quantity: null") || !strings.Contains(stdout, "quantity: 1500000000") {
		t.Errorf("-o yaml must keep the raw and null quantities:\n%s", stdout)
	}

	stdout, _, runError = runOrgUsageCommand(t, platform, "-o", "table")
	if runError != nil {
		t.Fatalf("org usage -o table: %v", runError)
	}
	if !strings.Contains(stdout, "1.5 GB") || !strings.Contains(stdout, "not measured") {
		t.Errorf("-o table is the human rendering:\n%s", stdout)
	}
}

// --period names a ready-made window: this month is month to date (the
// platform's own default end), last month is the whole previous calendar
// month.
func TestOrgUsagePeriodPicksTheWindow(t *testing.T) {
	cases := []struct {
		period   string
		wantFrom string
		wantTo   string
	}{
		{"this-month", "2026-10-01", ""},
		{"last-month", "2026-09-01", "2026-10-01"},
		{"LAST-MONTH", "2026-09-01", "2026-10-01"},
	}
	for _, testCase := range cases {
		t.Run(testCase.period, func(t *testing.T) {
			platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("meter", "month"))
			if _, _, runError := runOrgUsageCommand(t, platform, "--period", testCase.period); runError != nil {
				t.Fatalf("org usage --period %s: %v", testCase.period, runError)
			}
			query := platform.queries()[0]
			if query.Get("from") != testCase.wantFrom {
				t.Errorf("from = %q, want %q", query.Get("from"), testCase.wantFrom)
			}
			if query.Get("to") != testCase.wantTo {
				t.Errorf("to = %q, want %q", query.Get("to"), testCase.wantTo)
			}
		})
	}
}

// Last month in January is December of the year before.
func TestUsagePeriodWindowCrossesTheYear(t *testing.T) {
	from, to, periodError := usagePeriodWindow(usagePeriodLastMonth, time.Date(2027, time.January, 3, 0, 0, 0, 0, time.UTC))
	if periodError != nil || from != "2026-12-01" || to != "2027-01-01" {
		t.Errorf("last-month in January = %q..%q (%v), want 2026-12-01..2027-01-01", from, to, periodError)
	}
}

// --from and --to travel as given; granularity and group-by travel as the
// platform spells them. A window that ends at midnight is named by its last
// day.
func TestOrgUsageExplicitWindowAndShapeTravel(t *testing.T) {
	report := usageReportFixture("cluster", "day")
	report["from"], report["to"] = "2026-09-01T00:00:00Z", "2026-10-01T00:00:00Z"
	platform := newUsageFakePlatform(t, http.StatusOK, report)
	stdout, _, runError := runOrgUsageCommand(t, platform, "--from", "2026-09-01", "--to", "2026-10-01",
		"--granularity", "day", "--group-by", "cluster")
	if runError != nil {
		t.Fatalf("org usage: %v", runError)
	}
	query := platform.queries()[0]
	if got := query.Encode(); got != "from=2026-09-01&granularity=day&group_by=cluster&to=2026-10-01" {
		t.Errorf("query = %s", got)
	}
	if !strings.Contains(stdout, "Usage from 2026-09-01 through 2026-09-30 (day periods, by cluster)") {
		t.Errorf("an exclusive midnight end must read as the last day through which usage was read:\n%s", stdout)
	}
}

// An invocation mistake exits 2 and names the flag, before anything is
// asked of the platform.
func TestOrgUsageRejectsBadFlagsBeforeReading(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		wantText  string
	}{
		{"period with from", []string{"--period", "last-month", "--from", "2026-09-01"}, "--period cannot be combined with --from or --to"},
		{"unknown period", []string{"--period", "last-week"}, "--period must be this-month or last-month"},
		{"bad from", []string{"--from", "2026-13-01"}, "--from must be a UTC day as YYYY-MM-DD"},
		{"bad to", []string{"--to", "yesterday"}, "--to must be a UTC day as YYYY-MM-DD"},
		{"to not after from", []string{"--from", "2026-10-01", "--to", "2026-10-01"}, "--to (2026-10-01) must be after --from (2026-10-01)"},
		{"bad granularity", []string{"--granularity", "week"}, "--granularity must be one of month, day"},
		{"bad group-by", []string{"--group-by", "namespace"}, "--group-by must be one of meter, cluster, application, instance"},
		{"bad output", []string{"-o", "wide"}, "expected table, json or yaml"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("meter", "month"))
			_, _, runError := runOrgUsageCommand(t, platform, testCase.arguments...)
			if runError == nil {
				t.Fatal("expected a usage error")
			}
			if got := exitCodeFor(runError); got != exitUsage {
				t.Errorf("exit code = %d, want %d (usage): %v", got, exitUsage, runError)
			}
			if !strings.Contains(runError.Error(), testCase.wantText) {
				t.Errorf("error should say %q, got %q", testCase.wantText, runError.Error())
			}
			if len(platform.queries()) != 0 {
				t.Errorf("a refused invocation must not reach the platform")
			}
		})
	}
}

// A platform refusal of a query parameter (422, one pydantic entry per
// parameter) exits 2 and names the flag that set it, with the platform's
// reason and without pydantic's "Value error, " prefix.
func TestOrgUsageRefusedQueryNamesTheFlag(t *testing.T) {
	platform := newUsageFakePlatform(t, http.StatusUnprocessableEntity, map[string]any{"detail": []any{
		map[string]any{"loc": []any{"query", "to"}, "type": "value_error", "input": "2026-06-01",
			"msg": "Value error, the window from from to to must be at most 93 days; read a longer stretch in parts"},
		map[string]any{"loc": []any{"query", "group_by"}, "type": "literal_error", "input": "team",
			"msg": "Input should be 'meter', 'cluster', 'application' or 'instance'"},
	}})
	_, _, runError := runOrgUsageCommand(t, platform, "--from", "2026-01-01", "--to", "2026-06-01")
	if runError == nil {
		t.Fatal("expected the refusal")
	}
	if got := exitCodeFor(runError); got != exitUsage {
		t.Errorf("exit code = %d, want %d (usage): %v", got, exitUsage, runError)
	}
	for _, expected := range []string{
		"--to: the window from from to to must be at most 93 days",
		"--group-by: Input should be 'meter'",
	} {
		if !strings.Contains(runError.Error(), expected) {
			t.Errorf("the refusal should name the flag and carry the reason %q, got %q", expected, runError.Error())
		}
	}
	if strings.Contains(runError.Error(), "Value error,") {
		t.Errorf("pydantic's prefix is noise to a person: %q", runError.Error())
	}
}

// A role without billing.read exits 7: a role problem, not a login problem.
func TestOrgUsageWithoutBillingReadExitsForbidden(t *testing.T) {
	platform := newUsageFakePlatform(t, http.StatusForbidden, map[string]any{"detail": "permission_denied", "permission": "billing.read"})
	_, _, runError := runOrgUsageCommand(t, platform)
	if runError == nil {
		t.Fatal("expected the refusal")
	}
	if got := exitCodeFor(runError); got != exitForbidden {
		t.Errorf("exit code = %d, want %d (forbidden): %v", got, exitForbidden, runError)
	}
	if !strings.Contains(runError.Error(), "billing.read") {
		t.Errorf("the refusal should name the permission, got %q", runError.Error())
	}
}

// A platform that predates the usage read answers 404. That is not 'no
// usage' and not a missing resource (exit 3): it exits 1 and says the
// platform does not serve the read.
func TestOrgUsageOnAPlatformWithoutTheReadSaysSo(t *testing.T) {
	platform := newUsageFakePlatform(t, http.StatusNotFound, map[string]any{"detail": "Not Found"})
	_, _, runError := runOrgUsageCommand(t, platform)
	if runError == nil {
		t.Fatal("expected an error")
	}
	if got := exitCodeFor(runError); got != exitError {
		t.Errorf("exit code = %d, want %d: %v", got, exitError, runError)
	}
	if !strings.Contains(runError.Error(), "this platform does not serve the usage read") {
		t.Errorf("the error should say the platform predates the read, got %q", runError.Error())
	}
}

// Grouped by application, lines show the application's name, shared usage
// is marked and the output says those lines do not add up to the
// organisation's total. A service line with no application, and a meter
// not attributed to applications, say so rather than showing a blank.
func TestOrgUsageByApplicationMarksSharedLines(t *testing.T) {
	shared := usageLineFixture("service_instance_seconds", "seconds", int64Pointer(36_000), "complete", "included", "pilot", nil)
	shared["application_id"] = usageTestApplicationID
	shared["is_shared"] = true
	unattached := usageLineFixture("service_storage_byte_hours", "byte_hours", int64Pointer(2_000_000_000), "complete", "included", "pilot", nil)
	organisation := usageLineFixture("worker_vcpu_hours", "vcpu_hours", int64Pointer(10), "complete", "priced", "plan_rate", int64Pointer(0))
	platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("application", "month", shared, unattached, organisation))
	stdout, stderr, runError := runOrgUsageCommand(t, platform, "--group-by", "application")
	if runError != nil {
		t.Fatalf("org usage: %v", runError)
	}
	output := stripANSICodes(stdout)
	if header := usageRowFor(t, output, "METER"); !strings.Contains(header, "APPLICATION") || !strings.Contains(header, "SHARED") {
		t.Errorf("the application grouping needs its columns: %q", header)
	}
	if row := usageRowFor(t, output, "Managed service time"); !strings.Contains(row, "storefront") ||
		!strings.Contains(row, "yes") || !strings.Contains(row, "10.0 hours") {
		t.Errorf("the shared line should name the application and be marked shared: %q", row)
	}
	if row := usageRowFor(t, output, "Managed service storage"); !strings.Contains(row, "(no application)") || !strings.Contains(row, "2.0 GB-hours") {
		t.Errorf("a service line that serves no application should say so: %q", row)
	}
	if row := usageRowFor(t, output, "Worker vCPUs"); !strings.Contains(row, "(organisation)") || !strings.Contains(row, "€0.00") {
		t.Errorf("a meter not attributed to applications keeps one organisation line: %q", row)
	}
	if !strings.Contains(output, "application lines do not add up to the organisation's total") {
		t.Errorf("the application grouping must say its lines do not sum to the total:\n%s", output)
	}
	if stderr != "" {
		t.Errorf("a successful name lookup leaves stderr empty, got %q", stderr)
	}
}

// The application names are best effort: a listing the role may not read
// leaves the ids in place and says so once on stderr, and the usage still
// prints.
func TestOrgUsageByApplicationFallsBackToIDs(t *testing.T) {
	line := usageLineFixture("service_instance_seconds", "seconds", int64Pointer(3_600), "complete", "included", "pilot", nil)
	line["application_id"] = usageTestApplicationID
	platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("application", "month", line))
	platform.applicationsStatus = http.StatusForbidden
	stdout, stderr, runError := runOrgUsageCommand(t, platform, "--group-by", "application")
	if runError != nil {
		t.Fatalf("a failed name lookup must not fail the read: %v", runError)
	}
	if row := usageRowFor(t, stripANSICodes(stdout), "Managed service time"); !strings.Contains(row, usageTestApplicationID) {
		t.Errorf("the id should stand in for the name: %q", row)
	}
	if strings.Count(stderr, "Note: application names could not be read") != 1 {
		t.Errorf("the failed lookup should be noted once on stderr, got %q", stderr)
	}
}

// Only the application grouping reads application names.
func TestOrgUsageReadsApplicationNamesOnlyWhenGroupedByApplication(t *testing.T) {
	platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("meter", "month",
		usageLineFixture("ai_requests", "requests", int64Pointer(1), "complete", "unpriced", "no_rate", nil)))
	if _, _, runError := runOrgUsageCommand(t, platform); runError != nil {
		t.Fatalf("org usage: %v", runError)
	}
	if platform.applicationReads != 0 {
		t.Errorf("grouped by meter, the applications listing must not be read (read %d times)", platform.applicationReads)
	}
}

// Grouped by cluster, a line shows the cluster's name, its id when the
// name is gone, and '(organisation)' for a meter with no cluster. Day
// periods are labelled by their day.
func TestOrgUsageByClusterShowsNamesAndDays(t *testing.T) {
	named := usageLineFixture("hosted_logs_ingested_lines", "lines", int64Pointer(1_234_567), "complete", "included", "pilot", nil)
	named["cluster_id"], named["cluster_name"] = "c1a2b3c4-0000-4000-8000-000000000001", "prod"
	named["period_start"], named["period_end"] = "2026-10-03T00:00:00Z", "2026-10-04T00:00:00Z"
	nameless := usageLineFixture("hosted_logs_ingested_lines", "lines", int64Pointer(5), "complete", "included", "pilot", nil)
	nameless["cluster_id"] = "c1a2b3c4-0000-4000-8000-000000000009"
	nameless["period_start"], nameless["period_end"] = "2026-10-04T00:00:00Z", "2026-10-05T00:00:00Z"
	aiUsage := usageLineFixture("ai_input_tokens", "tokens", int64Pointer(2_500), "complete", "unpriced", "no_rate", nil)
	aiUsage["period_start"], aiUsage["period_end"] = "2026-10-04T00:00:00Z", "2026-10-05T00:00:00Z"
	platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("cluster", "day", named, nameless, aiUsage))
	stdout, _, runError := runOrgUsageCommand(t, platform, "--group-by", "cluster", "--granularity", "day")
	if runError != nil {
		t.Fatalf("org usage: %v", runError)
	}
	output := stripANSICodes(stdout)
	for _, expected := range []struct{ label, want string }{
		{"1,234,567 lines", "2026-10-03"},
		{"1,234,567 lines", "prod"},
		{"5 lines", "c1a2b3c4-0000-4000-8000-000000000009"},
		{"5 lines", "2026-10-04"},
		{"2,500 tokens", "(organisation)"},
		{"2,500 tokens", "no rate published"},
	} {
		if row := usageRowFor(t, output, expected.label); !strings.Contains(row, expected.want) {
			t.Errorf("row %q should contain %q: %q", expected.label, expected.want, row)
		}
	}
}

// Grouped by instance, a line names its instance. The platform attributes
// an instance line to no cluster, so there is no cluster column to read
// '(organisation)' on every row. Two instances of one name (a name is free
// again once its service is retired) are told apart by their ids.
func TestOrgUsageByInstanceTellsSameNamedInstancesApart(t *testing.T) {
	first := usageLineFixture("service_storage_byte_hours", "byte_hours", int64Pointer(20_000_000_000), "partial", "included", "pilot", nil)
	first["instance_id"], first["instance_name"] = "c9b96bb8-0000-4000-8000-000000000003", "orders-db"
	second := usageLineFixture("service_storage_byte_hours", "byte_hours", nil, "unknown", "included", "pilot", nil)
	second["instance_id"], second["instance_name"] = "7d3c1f0a-0000-4000-8000-000000000004", "orders-db"
	unique := usageLineFixture("service_instance_seconds", "seconds", int64Pointer(60), "complete", "included", "pilot", nil)
	unique["instance_id"], unique["instance_name"] = "5b1f0c7e-0000-4000-8000-000000000005", "app-cache"
	platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("instance", "month", first, second, unique))
	stdout, _, runError := runOrgUsageCommand(t, platform, "--group-by", "instance")
	if runError != nil {
		t.Fatalf("org usage: %v", runError)
	}
	output := stripANSICodes(stdout)
	if header := usageRowFor(t, output, "METER"); !strings.Contains(header, "INSTANCE") || strings.Contains(header, "CLUSTER") {
		t.Errorf("the instance grouping has an instance column and no cluster column: %q", header)
	}
	if row := usageRowFor(t, output, "orders-db (c9b96bb8)"); !strings.Contains(row, ">= 20.0 GB-hours") || !strings.Contains(row, "included (pilot)") {
		t.Errorf("the first orders-db should read its own quantity: %q", row)
	}
	if row := usageRowFor(t, output, "orders-db (7d3c1f0a)"); !strings.Contains(row, "not measured") {
		t.Errorf("the second orders-db should read its own quantity: %q", row)
	}
	if row := usageRowFor(t, output, "app-cache"); strings.Contains(row, "(5b1f0c7e)") || !strings.Contains(row, "1 minute") {
		t.Errorf("a name no other instance has needs no id: %q", row)
	}
}

// Grouped by cluster, clusters of one name (every released playground is
// named playground) are told apart by their ids.
func TestOrgUsageByClusterTellsSameNamedClustersApart(t *testing.T) {
	first := usageLineFixture("service_instance_seconds", "seconds", int64Pointer(7_200), "complete", "included", "pilot", nil)
	first["cluster_id"], first["cluster_name"] = "aaaaaaaa-0000-4000-8000-000000000001", "playground"
	second := usageLineFixture("service_instance_seconds", "seconds", nil, "unknown", "included", "pilot", nil)
	second["cluster_id"], second["cluster_name"] = "bbbbbbbb-0000-4000-8000-000000000002", "playground"
	platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("cluster", "month", first, second))
	stdout, _, runError := runOrgUsageCommand(t, platform, "--group-by", "cluster")
	if runError != nil {
		t.Fatalf("org usage: %v", runError)
	}
	output := stripANSICodes(stdout)
	if row := usageRowFor(t, output, "playground (aaaaaaaa)"); !strings.Contains(row, "2.0 hours") {
		t.Errorf("the first playground should read its own quantity: %q", row)
	}
	if row := usageRowFor(t, output, "playground (bbbbbbbb)"); !strings.Contains(row, "not measured") {
		t.Errorf("the second playground should read its own quantity: %q", row)
	}
}

// A window nothing was used in, grouped by something other than meter,
// has no lines; the output says so instead of printing an empty table.
func TestOrgUsageSaysWhenThereAreNoLines(t *testing.T) {
	platform := newUsageFakePlatform(t, http.StatusOK, usageReportFixture("instance", "month"))
	stdout, _, runError := runOrgUsageCommand(t, platform, "--group-by", "instance")
	if runError != nil {
		t.Fatalf("org usage: %v", runError)
	}
	if !strings.Contains(stdout, "No usage lines for this window.") {
		t.Errorf("output should say there are no lines:\n%s", stdout)
	}
}

// The command sits under `ankra org` with its flags, and its help says
// what the markers mean.
func TestOrgUsageIsRegisteredWithItsFlags(t *testing.T) {
	found, _, findError := rootCmd.Find([]string{"org", "usage"})
	if findError != nil || found.Name() != "usage" {
		t.Fatalf("org usage is not registered: %v", findError)
	}
	for _, flag := range []string{"from", "to", "period", "granularity", "group-by", "output"} {
		if found.Flags().Lookup(flag) == nil {
			t.Errorf("org usage lacks the --%s flag", flag)
		}
	}
	if output := found.Flags().Lookup("output"); output == nil || output.Shorthand != "o" || output.DefValue != "table" {
		t.Errorf("-o should default to table")
	}
	var help bytes.Buffer
	command := newOrgUsageCommand()
	command.SetOut(&help)
	command.SetArgs([]string{"--help"})
	if helpError := command.Execute(); helpError != nil {
		t.Fatalf("--help: %v", helpError)
	}
	for _, expected := range []string{"not measured", "included (pilot)", "billing.read", "--period", "do not add"} {
		if !strings.Contains(help.String(), expected) {
			t.Errorf("help should mention %q:\n%s", expected, help.String())
		}
	}
}

func TestUsageHumanQuantity(t *testing.T) {
	cases := []struct {
		quantity int64
		unit     string
		want     string
	}{
		{999, "bytes", "999 B"},
		{1_000, "bytes", "1.0 KB"},
		{1_500_000, "bytes", "1.5 MB"},
		{999_960, "bytes", "1.0 MB"},
		{1_500_000_000, "bytes", "1.5 GB"},
		{2_000_000_000_000, "bytes", "2.0 TB"},
		{2_000_000_000, "byte_hours", "2.0 GB-hours"},
		{500, "byte_hours", "500 B-hours"},
		{0, "seconds", "0 hours"},
		{1, "seconds", "1 second"},
		{45, "seconds", "45 seconds"},
		{90, "seconds", "1 minute"},
		{1_800, "seconds", "30 minutes"},
		{5_400, "seconds", "1.5 hours"},
		{31_536_000, "seconds", "8,760.0 hours"},
		{1_234, "vcpu_hours", "1,234 vCPU-hours"},
		{12, "series_hours", "12 series-hours"},
		{1_234_567, "tokens", "1,234,567 tokens"},
		{7, "samples", "7 samples"},
		{7, "", "7"},
	}
	for _, testCase := range cases {
		if got := usageHumanQuantity(testCase.quantity, testCase.unit); got != testCase.want {
			t.Errorf("usageHumanQuantity(%d, %q) = %q, want %q", testCase.quantity, testCase.unit, got, testCase.want)
		}
	}
}

func TestUsagePriceLabel(t *testing.T) {
	cases := []struct {
		line client.UsageLine
		want string
	}{
		{client.UsageLine{PriceState: "included", PriceBasis: "pilot"}, "included (pilot)"},
		{client.UsageLine{PriceState: "priced", PriceBasis: "plan_rate", AmountMinor: int64Pointer(123_456), Currency: stringPointer("EUR")}, "€1,234.56"},
		{client.UsageLine{PriceState: "priced", PriceBasis: "price_of_record", AmountMinor: int64Pointer(5), Currency: stringPointer("EUR")}, "€0.05"},
		{client.UsageLine{PriceState: "priced", PriceBasis: "price_of_record", AmountMinor: int64Pointer(250), Currency: stringPointer("USD")}, "2.50 USD"},
		{client.UsageLine{PriceState: "priced", PriceBasis: "plan_rate", AmountMinor: int64Pointer(-5), Currency: stringPointer("EUR")}, "-€0.05"},
		{client.UsageLine{PriceState: "priced", PriceBasis: "plan_rate", AmountMinor: int64Pointer(-123_456), Currency: stringPointer("EUR")}, "-€1,234.56"},
		{client.UsageLine{PriceState: "priced", PriceBasis: "plan_rate"}, "priced, amount not reported"},
		{client.UsageLine{PriceState: "unpriced", PriceBasis: "no_rate"}, "no rate published"},
		{client.UsageLine{PriceState: "unpriced", PriceBasis: "flat_fee"}, "covered by the plan's flat fee"},
		{client.UsageLine{PriceState: "unpriced", PriceBasis: "not_invoiced"}, "plan not invoiced"},
		{client.UsageLine{PriceState: "unpriced", PriceBasis: "organisation_total"}, "priced on the organisation total"},
		{client.UsageLine{PriceState: "unpriced", PriceBasis: "other_currency"}, "price not in EUR"},
		{client.UsageLine{PriceState: "unpriced", PriceBasis: "something_new"}, "unpriced (something_new)"},
		{client.UsageLine{PriceState: ""}, "price state not reported"},
	}
	for _, testCase := range cases {
		if got := usagePriceLabel(testCase.line); got != testCase.want {
			t.Errorf("usagePriceLabel(%s/%s) = %q, want %q", testCase.line.PriceState, testCase.line.PriceBasis, got, testCase.want)
		}
	}
}

func TestUsageWindowLabel(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{"2026-09-01T00:00:00Z", "2026-10-01T00:00:00Z", "from 2026-09-01 through 2026-09-30"},
		{"2026-10-01T00:00:00Z", "2026-10-08T12:34:56Z", "from 2026-10-01 to 2026-10-08 12:34 UTC"},
		{"2026-10-01T00:00:00Z", "soon", "from 2026-10-01 to soon"},
	}
	for _, testCase := range cases {
		if got := usageWindowLabel(testCase.from, testCase.to); got != testCase.want {
			t.Errorf("usageWindowLabel(%q, %q) = %q, want %q", testCase.from, testCase.to, got, testCase.want)
		}
	}
}

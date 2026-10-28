package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
)

func ledgerPointer[T any](value T) *T {
	return &value
}

// cloudLedgerFixture is a ledger as the platform sends it: a measured
// off-hours schedule, a right-size seven days into verification whose usage
// check failed, a waste cleanup with no expectation whose coverage moved, a
// reverted schedule, a measured right-size whose run rate rose, and a
// change approved but not run yet.
func cloudLedgerFixture() *client.CloudLedger {
	return &client.CloudLedger{
		Currency:               "usd",
		GeneratedAt:            "2026-09-23T21:33:14Z",
		MeasuredTotalCents:     19840 - 4200,
		Month:                  "2026-09",
		MeasuredThisMonthCents: 19840,
		RunningTotalCents:      12000 + 8800,
		Counts:                 client.CloudLedgerCounts{Pending: 1, Measured: 2, Unmeasured: 1, Reverted: 1},
		Rows: []client.CloudLedgerRow{
			{
				DecisionID: "0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e", ClusterID: ledgerPointer("33333333-3333-4333-8333-333333333333"),
				ClusterName: ledgerPointer("staging-1"), Lever: "off_hours_schedule", Summary: "Stop staging-1 weeknights and weekends",
				Status: "succeeded", ExpectedMonthlyCents: ledgerPointer(int64(23100)), BaselineMonthlyCents: ledgerPointer(int64(36000)),
				MeasuredMonthlyCents: ledgerPointer(int64(19840)), MeasurementStatus: "measured", Days: ledgerPointer(7),
				VerificationStatus: ledgerPointer("not_applicable"), VerificationDays: &[]client.CloudLedgerVerificationDay{},
			},
			{
				DecisionID: "1c8d5e2f-6a7b-4c9d-8e1f-2a3b4c5d6e7f", ClusterID: ledgerPointer("11111111-1111-4111-8111-111111111111"),
				ClusterName: ledgerPointer("prod-eu"), Lever: "right_size", Summary: "Resize workers from cpx51 to cpx41",
				Status: "succeeded", ExpectedMonthlyCents: ledgerPointer(int64(12000)), BaselineMonthlyCents: ledgerPointer(int64(200000)),
				MeasurementStatus: "pending", Days: ledgerPointer(3),
				VerificationStatus: ledgerPointer("failed"),
				VerificationDays: &[]client.CloudLedgerVerificationDay{{Day: 1, From: "2026-09-20T10:00:00Z", To: "2026-09-21T10:00:00Z",
					State: "breach", CPUP95Share: ledgerPointer(0.71), MemoryP95Share: ledgerPointer(0.4), HottestNode: "prod-eu-worker-2",
					Nodes: 3, Reporting: 3, Reason: "p95 CPU 71% on prod-eu-worker-2"}},
			},
			{
				DecisionID: "2d9e6f3a-7b8c-4d0e-9f2a-3b4c5d6e7f80", ClusterID: ledgerPointer("22222222-2222-4222-8222-222222222222"),
				ClusterName: ledgerPointer("data-platform"), Lever: "waste_cleanup", Summary: "Delete 4 unattached volumes",
				Status: "succeeded", MeasurementStatus: "unmeasured_coverage_moved", Days: ledgerPointer(7),
				MeasurementReason: ledgerPointer("The cluster was not priced the same way before and after the change, so the two run rates are not comparable."),
			},
			{
				DecisionID: "3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091", ClusterID: ledgerPointer("44444444-4444-4444-8444-444444444444"),
				ClusterName: ledgerPointer("qa-2"), Lever: "off_hours_schedule", Summary: "Stop qa-2 weeknights and weekends",
				Status: "succeeded", ExpectedMonthlyCents: ledgerPointer(int64(5400)), MeasurementStatus: "reverted", Days: ledgerPointer(2),
				MeasurementReason: ledgerPointer("The power schedule was deleted two days after it ran."),
			},
			{
				DecisionID: "4f1a8b5c-9d0e-4f2a-9b4c-5d6e7f8091a2", ClusterID: ledgerPointer("55555555-5555-4555-8555-555555555555"),
				ClusterName: ledgerPointer("batch-eu"), Lever: "right_size", Summary: "Resize workers from cx42 to cx32",
				Status: "succeeded", ExpectedMonthlyCents: ledgerPointer(int64(3100)), BaselineMonthlyCents: ledgerPointer(int64(41000)),
				MeasuredMonthlyCents: ledgerPointer(int64(-4200)), MeasurementStatus: "measured", Days: ledgerPointer(7),
				VerificationStatus: ledgerPointer("passed"),
			},
			{
				DecisionID: "5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3", ClusterID: ledgerPointer("66666666-6666-4666-8666-666666666666"),
				ClusterName: ledgerPointer("dev-sandbox"), Lever: "off_hours_schedule", Summary: "Stop dev-sandbox weeknights and weekends",
				Status: "approved", ExpectedMonthlyCents: ledgerPointer(int64(8800)), MeasurementStatus: "not_applicable",
			},
		},
	}
}

// ledgerRowLine returns the rendered table line that carries a cluster.
func ledgerRowLine(t *testing.T, output string, cluster string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "│ "+cluster+" ") {
			return line
		}
	}
	t.Fatalf("no table row for %s:\n%s", cluster, output)
	return ""
}

func TestCostLedgerRendersHeaderRowsAndReasons(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{ledger: cloudLedgerFixture()}, "cost", "ledger")
	if executeError != nil {
		t.Fatalf("cost ledger failed: %v", executeError)
	}
	for _, expected := range []string{
		"Cost ledger (USD): $198.40/mo measured in September 2026 · $156.40/mo measured in all",
		"$208.00/mo expected from changes approved, running or verifying (not saved until measured)",
		"2 measured · 1 verifying · 1 unmeasured · 1 reverted · generated 2026-09-23T21:33:14Z",
		"CLUSTER", "LEVER", "STATUS", "EXPECTED/MO", "MEASURED/MO",
		"↳ The cluster was not priced the same way before and after the change",
		"↳ The power schedule was deleted two days after it ran.",
		"↳ Usage verification failed; a rollback is proposed.",
		"↳ Usage verification passed.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	for cluster, cells := range map[string][]string{
		"staging-1":     {"Off-hours schedule", "measured", "$231.00", "$198.40"},
		"prod-eu":       {"Right-size", "verifying · day 3 of 7", "$120.00", "—"},
		"data-platform": {"Waste cleanup", "unmeasured · coverage moved", "—"},
		"qa-2":          {"Off-hours schedule", "reverted", "$54.00"},
		"batch-eu":      {"Right-size", "measured", "$31.00", "-$42.00"},
		"dev-sandbox":   {"Off-hours schedule", "approved · not run yet", "$88.00", "—"},
	} {
		line := ledgerRowLine(t, output, cluster)
		for _, cell := range cells {
			if !strings.Contains(line, cell) {
				t.Fatalf("row %s lacks %q: %s", cluster, cell, line)
			}
		}
	}
	// A right-size that is not a right-size's verification, and a lever
	// with no verification at all, add no note.
	if strings.Contains(output, "not applicable") || strings.Contains(output, "Usage verification in progress") {
		t.Fatalf("a row without a verification must not grow a note:\n%s", output)
	}
	if strings.Contains(output, "(showing the newest") {
		t.Fatalf("an untruncated ledger must not say it is truncated:\n%s", output)
	}
}

func TestCostLedgerNeverRendersAnUnknownAsZero(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{ledger: cloudLedgerFixture()}, "cost", "ledger")
	if executeError != nil {
		t.Fatalf("cost ledger failed: %v", executeError)
	}
	line := ledgerRowLine(t, output, "data-platform")
	if strings.Count(line, "—") != 2 || strings.Contains(line, "0.00") {
		t.Fatalf("a null expected and a null measured must both read as —, never as zero: %s", line)
	}
	if strings.Contains(output, "$0.00") {
		t.Fatalf("no figure in this ledger is zero, so none may print as $0.00:\n%s", output)
	}
	if strings.Contains(output, "$-") {
		t.Fatalf("a negative amount carries its sign before the symbol:\n%s", output)
	}
}

// The table stays readable in a 100-column terminal: the notes are wrapped
// to the table's own width instead of stretching it.
func TestCostLedgerFitsA100ColumnTerminal(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{ledger: cloudLedgerFixture()}, "cost", "ledger")
	if executeError != nil {
		t.Fatalf("cost ledger failed: %v", executeError)
	}
	tableWidth := 0
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		width := text.StringWidthWithoutEscSequences(line)
		if width > 100 {
			t.Fatalf("line is %d columns wide, over 100: %q\n%s", width, line, output)
		}
		if strings.HasPrefix(line, "│") || strings.HasPrefix(line, "╭") || strings.HasPrefix(line, "╰") || strings.HasPrefix(line, "├") {
			if tableWidth == 0 {
				tableWidth = width
			}
			if width != tableWidth {
				t.Fatalf("every table line must be as wide as the table (%d), got %d: %q\n%s", tableWidth, width, line, output)
			}
		}
	}
	notesStartAtTheirRow := strings.Index(output, "data-platform") < strings.Index(output, "↳ The cluster was not priced") &&
		strings.Index(output, "↳ The cluster was not priced") < strings.Index(output, "qa-2")
	if !notesStartAtTheirRow {
		t.Fatalf("a reason must sit directly under its own row:\n%s", output)
	}
}

func TestCostLedgerSaysWhenTheRowsAreTruncated(t *testing.T) {
	ledger := cloudLedgerFixture()
	ledger.Truncated = true
	output, executeError := runCostCommand(t, &costMock{ledger: ledger}, "cost", "ledger")
	if executeError != nil {
		t.Fatalf("cost ledger failed: %v", executeError)
	}
	if !strings.Contains(output, "(showing the newest 6 changes of more; the totals above cover all of them)") {
		t.Fatalf("a truncated ledger must say the totals still cover every row:\n%s", output)
	}
}

func TestCostLedgerWithNothingDecidedSaysSoInOneSentence(t *testing.T) {
	empty := &client.CloudLedger{Currency: "eur", GeneratedAt: "2026-09-23T21:33:14Z", Month: "2026-09", Rows: []client.CloudLedgerRow{}}
	output, executeError := runCostCommand(t, &costMock{ledger: empty}, "cost", "ledger")
	if executeError != nil {
		t.Fatalf("cost ledger failed: %v", executeError)
	}
	if !strings.HasPrefix(output, "No cost decision has run through the ledger yet.\n") {
		t.Fatalf("an empty ledger must lead with one clear sentence:\n%s", output)
	}
	if strings.Contains(output, "€0.00") || strings.Contains(output, "CLUSTER") || strings.Contains(output, "╭") {
		t.Fatalf("an empty ledger must not render zero totals or an empty table:\n%s", output)
	}
}

func TestCostLedgerOnAPlatformWithoutVerificationFieldsAddsNoNote(t *testing.T) {
	ledger := cloudLedgerFixture()
	for index := range ledger.Rows {
		ledger.Rows[index].VerificationStatus = nil
		ledger.Rows[index].VerificationDays = nil
	}
	output, executeError := runCostCommand(t, &costMock{ledger: ledger}, "cost", "ledger")
	if executeError != nil {
		t.Fatalf("cost ledger failed: %v", executeError)
	}
	if strings.Contains(output, "Usage verification") {
		t.Fatalf("a platform that sends no verification must not print one:\n%s", output)
	}
	if !strings.Contains(output, "↳ The power schedule was deleted two days after it ran.") {
		t.Fatalf("the measurement reasons stand without the verification fields:\n%s", output)
	}
}

func TestCostLedgerStructuredOutputIsTheApiDocument(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{ledger: cloudLedgerFixture()}, "cost", "ledger", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost ledger -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	if decoded["currency"] != "usd" || decoded["measured_total_cents"] != float64(15640) ||
		decoded["measured_this_month_cents"] != float64(19840) || decoded["running_total_cents"] != float64(20800) ||
		decoded["month"] != "2026-09" || decoded["truncated"] != false {
		t.Fatalf("structured document lacks the wire totals: %+v", decoded)
	}
	counts, _ := decoded["counts"].(map[string]any)
	if counts["pending"] != float64(1) || counts["measured"] != float64(2) || counts["unmeasured"] != float64(1) || counts["reverted"] != float64(1) {
		t.Fatalf("counts did not pass through: %+v", counts)
	}
	rows, _ := decoded["rows"].([]any)
	if len(rows) != 6 {
		t.Fatalf("rows missing: %+v", decoded["rows"])
	}
	waste := rows[2].(map[string]any)
	if value, present := waste["expected_monthly_cents"]; !present || value != nil {
		t.Fatalf("an unknown expectation must stay null on the wire, not vanish or become 0: %+v", waste)
	}
	if value, present := waste["measured_monthly_cents"]; !present || value != nil {
		t.Fatalf("an unknown measurement must stay null on the wire: %+v", waste)
	}
	if _, present := waste["verification_status"]; present {
		t.Fatalf("a row the platform sent without verification must not gain one: %+v", waste)
	}
	if rows[4].(map[string]any)["measured_monthly_cents"] != float64(-4200) {
		t.Fatalf("a negative measurement must pass through as negative: %+v", rows[4])
	}
	rightSize := rows[1].(map[string]any)
	days, _ := rightSize["verification_days"].([]any)
	if rightSize["verification_status"] != "failed" || len(days) != 1 || days[0].(map[string]any)["state"] != "breach" {
		t.Fatalf("the right-size verification must pass through: %+v", rightSize)
	}
	if schedule, _ := rows[0].(map[string]any)["verification_days"].([]any); schedule == nil {
		t.Fatalf("an empty verification list must stay an empty list: %+v", rows[0])
	}
}

// The route is served to API tokens on every platform that has the ledger, so
// a 404 can only mean an older platform. Reporting it as exit 3 would read as
// "this organisation has no ledger".
func TestCostLedgerReportsAMissingRouteAsSuch(t *testing.T) {
	for _, readError := range []error{
		client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found"),
		&client.UnexpectedResponseError{StatusCode: 404, Detail: "Not Found."},
	} {
		_, executeError := runCostCommand(t, &costMock{ledgerError: readError}, "cost", "ledger")
		if executeError == nil || !strings.Contains(executeError.Error(), "this platform does not serve the cost ledger") ||
			!strings.Contains(executeError.Error(), "GET /api/v1/org/cloud-cost/ledger is not registered") {
			t.Fatalf("error = %v", executeError)
		}
		if got := exitCodeFor(executeError); got != exitError {
			t.Fatalf("exit code = %d, want %d (not the not-found code)", got, exitError)
		}
	}
}

func TestCostLedgerRelaysOtherFailures(t *testing.T) {
	_, executeError := runCostCommand(t, &costMock{ledgerError: &client.PermissionDeniedError{Permission: "billing:read"}}, "cost", "ledger")
	if executeError == nil || !strings.Contains(executeError.Error(), "reading the cost ledger") {
		t.Fatalf("error = %v", executeError)
	}
	if got := exitCodeFor(executeError); got != exitForbidden {
		t.Fatalf("exit code = %d, want %d", got, exitForbidden)
	}
}

func TestFormatCostCentsSignsBeforeTheSymbol(t *testing.T) {
	cases := map[int64]string{0: "$0.00", 5: "$0.05", 19840: "$198.40", -4200: "-$42.00", -5: "-$0.05", 123456789: "$1234567.89"}
	for cents, want := range cases {
		if got := formatCostCents(cents, "usd"); got != want {
			t.Errorf("formatCostCents(%d) = %q, want %q", cents, got, want)
		}
	}
	if got := formatOptionalCostCents(nil, "eur"); got != "—" {
		t.Errorf("an unknown amount = %q, want —", got)
	}
	if got := formatOptionalCostCents(ledgerPointer(int64(0)), "eur"); got != "€0.00" {
		t.Errorf("a known zero = %q, want €0.00", got)
	}
}

func TestCostLedgerLeverNamesALeverThisCLIPredates(t *testing.T) {
	for lever, want := range map[string]string{
		"right_size":       "Right-size",
		"spot_migration":   "Spot migration",
		"égress_reduction": "Égress reduction",
		"":                 "—",
	} {
		if got := costLedgerLever(lever); got != want {
			t.Errorf("costLedgerLever(%q) = %q, want %q", lever, got, want)
		}
	}
}

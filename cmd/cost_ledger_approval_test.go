package cmd

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"gopkg.in/yaml.v3"
)

// ledgerApprovalDocument is a ledger as a platform that sends
// approval_expired (cluster#5779) answers it, one row per answer: an approval
// that lapsed, one that stands, a rollback's the platform could not judge
// (null), and a row without the key, as an older platform sends every row.
// It is decoded from JSON rather than built in Go, so an absent key is absent
// exactly as on the wire. running_total_cents leaves the lapsed row out, as
// the platform does: 5000 + 8800.
const ledgerApprovalDocument = `{
  "currency": "usd",
  "generated_at": "2026-11-03T09:00:00Z",
  "measured_total_cents": 0,
  "month": "2026-11",
  "measured_this_month_cents": 0,
  "running_total_cents": 13800,
  "counts": {"pending": 0, "measured": 0, "unmeasured": 0, "reverted": 0},
  "rows": [
    {"decision_id": "7c4d1e0b-5f6a-4b8c-9d0e-1f2a3b4c5d60", "cluster_id": "77777777-7777-4777-8777-777777777777",
     "cluster_name": "ci-runners", "lever": "off_hours_schedule", "summary": "Stop ci-runners weeknights and weekends",
     "status": "approved", "approval_expired": true, "expected_monthly_cents": 4500, "baseline_monthly_cents": null,
     "measured_monthly_cents": null, "measurement_status": "not_applicable", "measurement_reason": null, "days": null,
     "decided_at": "2026-10-20T09:00:00Z", "executed_at": null, "verify_until": null, "measured_at": null},
    {"decision_id": "8d5e2f1c-6a7b-4c9d-8e1f-2a3b4c5d6e71", "cluster_id": "88888888-8888-4888-8888-888888888888",
     "cluster_name": "web-eu", "lever": "waste_cleanup", "summary": "Delete 2 unattached volumes",
     "status": "approved", "approval_expired": false, "expected_monthly_cents": 5000, "baseline_monthly_cents": null,
     "measured_monthly_cents": null, "measurement_status": "not_applicable", "measurement_reason": null, "days": null,
     "decided_at": "2026-11-02T09:00:00Z", "executed_at": null, "verify_until": null, "measured_at": null},
    {"decision_id": "9e6f3a2d-7b8c-4d0e-9f2a-3b4c5d6e7f82", "cluster_id": "99999999-9999-4999-8999-999999999999",
     "cluster_name": "prod-us", "lever": "right_size_rollback", "summary": "Resize workers back from cx32 to cx42",
     "status": "approved", "approval_expired": null, "expected_monthly_cents": null, "baseline_monthly_cents": null,
     "measured_monthly_cents": null, "measurement_status": "not_applicable", "measurement_reason": null, "days": null,
     "decided_at": "2026-10-21T09:00:00Z", "executed_at": null, "verify_until": null, "measured_at": null},
    {"decision_id": "5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3", "cluster_id": "66666666-6666-4666-8666-666666666666",
     "cluster_name": "dev-sandbox", "lever": "off_hours_schedule", "summary": "Stop dev-sandbox weeknights and weekends",
     "status": "approved", "expected_monthly_cents": 8800, "baseline_monthly_cents": null,
     "measured_monthly_cents": null, "measurement_status": "not_applicable", "measurement_reason": null, "days": null,
     "decided_at": "2026-11-01T09:00:00Z", "executed_at": null, "verify_until": null, "measured_at": null}
  ],
  "truncated": false
}`

func decodeLedgerDocument(t *testing.T, document string) *client.CloudLedger {
	t.Helper()
	var ledger client.CloudLedger
	if decodeError := json.Unmarshal([]byte(document), &ledger); decodeError != nil {
		t.Fatalf("decoding the ledger fixture: %v", decodeError)
	}
	return &ledger
}

// decodeLedgerRow decodes one row; overrides replace or add keys of the
// lapsed row in ledgerApprovalDocument, and a nil override removes the key.
func decodeLedgerRow(t *testing.T, overrides map[string]any) client.CloudLedgerRow {
	t.Helper()
	var document struct {
		Rows []map[string]any `json:"rows"`
	}
	if decodeError := json.Unmarshal([]byte(ledgerApprovalDocument), &document); decodeError != nil {
		t.Fatalf("decoding the ledger fixture: %v", decodeError)
	}
	fields := document.Rows[0]
	for key, value := range overrides {
		if value == nil {
			delete(fields, key)
			continue
		}
		fields[key] = value
	}
	encoded, encodeError := json.Marshal(fields)
	if encodeError != nil {
		t.Fatalf("encoding the row: %v", encodeError)
	}
	var row client.CloudLedgerRow
	if decodeError := json.Unmarshal(encoded, &row); decodeError != nil {
		t.Fatalf("decoding the row: %v", decodeError)
	}
	return row
}

// ledgerFlatText is the rendered ledger with the table's borders removed and
// its wrapped lines joined, so a note reads as the one sentence it is.
func ledgerFlatText(output string) string {
	replacer := strings.NewReplacer("│", " ", "╭", " ", "╰", " ", "├", " ", "┤", " ", "─", " ", "┬", " ", "┴", " ", "┼", " ", "╮", " ", "╯", " ")
	return strings.Join(strings.Fields(replacer.Replace(output)), " ")
}

const (
	lapsedApprovalNote = "Approved more than 7 days ago and never started, so the approval has lapsed and the change will not run. " +
		"Its expected saving is left out of the total above. " +
		"Read it with 'ankra cost decisions get 7c4d1e0b-5f6a-4b8c-9d0e-1f2a3b4c5d60' and approve it again if it is still what you want."
	unknownApprovalNote = "Approved more than 7 days ago and not run yet. A rollback keeps its approval past that only when it is " +
		"confirmed to undo its right-size, which the ledger does not check, so whether this approval still stands is " +
		"unknown: 'ankra cost decisions get 7c4d1e0b-5f6a-4b8c-9d0e-1f2a3b4c5d60' says."
)

// The running total leaves a lapsed approval out, so the row must not read
// "approved · not run yet" beside it: it says the approval lapsed, how to
// approve it again, and that its expectation is not in the total. A null is
// said to be unknown, never lapsed and never standing; false and an absent
// key keep today's words.
func TestCostLedgerSaysWhetherAnApprovalStillStands(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{ledger: decodeLedgerDocument(t, ledgerApprovalDocument)}, "cost", "ledger")
	if executeError != nil {
		t.Fatalf("cost ledger failed: %v", executeError)
	}
	if !strings.Contains(output, "$138.00/mo expected from changes approved, running or verifying (not saved until measured)") {
		t.Fatalf("the header must state the platform's running total:\n%s", output)
	}
	for cluster, want := range map[string]struct {
		cells  []string
		absent []string
	}{
		"ci-runners":  {cells: []string{"Off-hours schedule", "approval lapsed", "$45.00"}, absent: []string{"approved"}},
		"web-eu":      {cells: []string{"Waste cleanup", "approved · not run yet", "$50.00"}, absent: []string{"lapsed"}},
		"prod-us":     {cells: []string{"Right-size rollback", "approved · may have lapsed"}, absent: []string{"not run yet"}},
		"dev-sandbox": {cells: []string{"Off-hours schedule", "approved · not run yet", "$88.00"}, absent: []string{"lapsed"}},
	} {
		line := ledgerRowLine(t, output, cluster)
		for _, cell := range want.cells {
			if !strings.Contains(line, cell) {
				t.Errorf("row %s lacks %q: %s", cluster, cell, line)
			}
		}
		for _, cell := range want.absent {
			if strings.Contains(line, cell) {
				t.Errorf("row %s must not say %q: %s", cluster, cell, line)
			}
		}
	}

	flat := ledgerFlatText(output)
	// prod-us is another decision, with no expectation to leave out.
	unknown := strings.ReplaceAll(unknownApprovalNote, "7c4d1e0b-5f6a-4b8c-9d0e-1f2a3b4c5d60", "9e6f3a2d-7b8c-4d0e-9f2a-3b4c5d6e7f82")
	for _, note := range []string{"↳ " + lapsedApprovalNote, "↳ " + unknown} {
		if !strings.Contains(flat, note) {
			t.Errorf("output lacks the note %q:\n%s", note, output)
		}
	}
	// Each note sits under its own row, and only those two rows get one.
	notesSitUnderTheirRows := strings.Index(flat, "ci-runners") < strings.Index(flat, "↳ Approved more than 7 days ago and never started") &&
		strings.Index(flat, "↳ Approved more than 7 days ago and never started") < strings.Index(flat, "web-eu") &&
		strings.Index(flat, "prod-us") < strings.Index(flat, "↳ Approved more than 7 days ago and not run yet") &&
		strings.Index(flat, "↳ Approved more than 7 days ago and not run yet") < strings.Index(flat, "dev-sandbox")
	if !notesSitUnderTheirRows {
		t.Errorf("an approval note must sit directly under its own row:\n%s", output)
	}
	if count := strings.Count(flat, "↳ Approved more than"); count != 2 {
		t.Errorf("%d approval notes, want 2 (a standing approval and an older platform's row get none):\n%s", count, output)
	}
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if width := text.StringWidthWithoutEscSequences(line); width > 100 {
			t.Fatalf("line is %d columns wide, over 100: %q\n%s", width, line, output)
		}
	}
}

func TestCostLedgerApprovalNotePerAnswer(t *testing.T) {
	for name, testCase := range map[string]struct {
		overrides map[string]any
		status    string
		notes     []string
	}{
		"lapsed": {
			status: "approval lapsed",
			notes:  []string{lapsedApprovalNote},
		},
		"lapsed with no expectation": {
			// No figure to leave out of the total, so the note does not claim one.
			overrides: map[string]any{"expected_monthly_cents": json.RawMessage("null")},
			status:    "approval lapsed",
			notes:     []string{strings.Replace(lapsedApprovalNote, "Its expected saving is left out of the total above. ", "", 1)},
		},
		"unknown": {
			overrides: map[string]any{"approval_expired": json.RawMessage("null"), "lever": "right_size_rollback"},
			status:    "approved · may have lapsed",
			notes:     []string{unknownApprovalNote + " Its expected saving is left out of the total above."},
		},
		"standing": {
			overrides: map[string]any{"approval_expired": false},
			status:    "approved · not run yet",
		},
		"absent (an older platform)": {
			overrides: map[string]any{"approval_expired": nil},
			status:    "approved · not run yet",
		},
		"measured, whatever the flag says": {
			// A row the ledger measured ran, so its measurement is what it says.
			overrides: map[string]any{"measurement_status": "measured", "status": "succeeded", "days": 7},
			status:    "measured",
		},
		"running, whatever the flag says": {
			// A change that started ran on its approval; its status is what
			// the row says, even with nothing measuring it.
			overrides: map[string]any{"status": "running"},
			status:    "running",
		},
		"succeeded unmeasured, whatever the flag says": {
			overrides: map[string]any{"status": "succeeded", "approval_expired": json.RawMessage("null")},
			status:    "not measured",
		},
		"a status this CLI predates takes the verdict": {
			// The platform judges held approvals too; its verdict stands.
			overrides: map[string]any{"status": "held"},
			status:    "approval lapsed",
			notes:     []string{lapsedApprovalNote},
		},
	} {
		t.Run(name, func(t *testing.T) {
			row := decodeLedgerRow(t, testCase.overrides)
			if got := costLedgerStatus(row); got != testCase.status {
				t.Errorf("status = %q, want %q", got, testCase.status)
			}
			got := costLedgerNotes(row)
			if len(got) != len(testCase.notes) {
				t.Fatalf("notes = %q, want %q", got, testCase.notes)
			}
			for index := range got {
				if got[index] != testCase.notes[index] {
					t.Errorf("note %d =\n  %q\nwant\n  %q", index, got[index], testCase.notes[index])
				}
			}
		})
	}
}

// -o json and -o yaml keep approval_expired exactly as the platform sent it:
// true, false and null stay, and a row without the key does not gain one,
// whether the output is the platform's own document or, for a ledger that
// carries none, the decoded struct.
func TestCostLedgerStructuredOutputKeepsApprovalExpiredAsSent(t *testing.T) {
	want := []struct {
		present bool
		value   any
	}{{true, true}, {true, false}, {true, nil}, {false, nil}}
	check := func(t *testing.T, rows []any) {
		t.Helper()
		if len(rows) != len(want) {
			t.Fatalf("%d rows, want %d: %+v", len(rows), len(want), rows)
		}
		for index, row := range rows {
			value, present := row.(map[string]any)["approval_expired"]
			if present != want[index].present || value != want[index].value {
				t.Errorf("row %d approval_expired = %v (present %v), want %v (present %v)",
					index, value, present, want[index].value, want[index].present)
			}
		}
	}
	for _, carriesDocument := range []bool{false, true} {
		ledger := func() *client.CloudLedger {
			ledger := decodeLedgerDocument(t, ledgerApprovalDocument)
			if carriesDocument {
				ledger.RawDocument = client.NewRawDocument([]byte(ledgerApprovalDocument))
			}
			return ledger
		}
		name := "struct"
		if carriesDocument {
			name = "platform document"
		}
		t.Run(name+"/json", func(t *testing.T) {
			output, executeError := runCostCommand(t, &costMock{ledger: ledger()}, "cost", "ledger", "-o", "json")
			if executeError != nil {
				t.Fatalf("cost ledger -o json failed: %v", executeError)
			}
			var decoded map[string]any
			if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
				t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
			}
			rows, _ := decoded["rows"].([]any)
			check(t, rows)
		})
		t.Run(name+"/yaml", func(t *testing.T) {
			output, executeError := runCostCommand(t, &costMock{ledger: ledger()}, "cost", "ledger", "-o", "yaml")
			if executeError != nil {
				t.Fatalf("cost ledger -o yaml failed: %v", executeError)
			}
			var decoded map[string]any
			if decodeError := yaml.Unmarshal([]byte(output), &decoded); decodeError != nil {
				t.Fatalf("output is not YAML: %v\n%s", decodeError, output)
			}
			rows, _ := decoded["rows"].([]any)
			check(t, rows)
			if !regexp.MustCompile(`(?m)^\s+approval_expired: null$`).MatchString(output) {
				t.Errorf("a null approval_expired must stay a null key in YAML:\n%s", output)
			}
		})
	}
}

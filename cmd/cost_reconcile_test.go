package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type costReconcileMock struct {
	baseMock
	reconciliation *client.CostReconciliation
	readError      error
	months         []string
}

func (m *costReconcileMock) GetCostReconciliation(month string) (*client.CostReconciliation, error) {
	m.months = append(m.months, month)
	if m.readError != nil {
		return nil, m.readError
	}
	return m.reconciliation, nil
}

func runCostReconcileCommand(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs(args)
	t.Cleanup(func() { resetTreeFlags(t, costReconcileCmd) })
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

func reconcileCents(cents int64) *int64 { return &cents }

func reconcileText(value string) *string { return &value }

// costReconciliationFixture is one credential per state the route answers:
// a stated difference with its caveat, tax and credits; final figures not
// yet in (month to date after a failed refresh); a credential never
// imported; mixed currencies; and a known zero.
func costReconciliationFixture() *client.CostReconciliation {
	difference, complete, partial, isComplete := 3.26, "complete", "partial", true
	return &client.CostReconciliation{
		Month: "2026-08", MonthClosed: true, PeriodHours: 744, GeneratedAt: "2026-09-26T08:00:00Z",
		Credentials: []client.CostReconciliationCredential{
			{CredentialID: "aaaaaaaa-0000-4000-8000-000000000001", CredentialName: reconcileText("ovh-prod"), Provider: "ovh",
				State: "complete", Currency: reconcileText("eur"), InvoiceMinor: reconcileCents(9800), TaxMinor: reconcileCents(1805),
				CreditMinor: reconcileCents(-500), MatchedMinor: reconcileCents(9500), UnmatchedMinor: reconcileCents(300),
				EstimateMinor: reconcileCents(9200), EstimateComplete: &isComplete, DifferencePct: &difference,
				EstimateFX:          &client.CostReconciliationFX{From: "usd", To: "eur", RateFromUSD: 0.92, AsOf: reconcileText("2026-09-25T00:00:00Z")},
				UnplacedPricedMinor: reconcileCents(300), DifferenceCaveat: reconcileText("1 billed line(s) in categories the estimate prices are not placed on any cluster."),
				UnmatchedLineCount: 1,
				Imports:            []client.CostReconciliationImport{{SourceDocumentID: "usage-2026-08", State: "complete", LinesState: &complete}}},
			{CredentialID: "aaaaaaaa-0000-4000-8000-000000000002", CredentialName: reconcileText("upcloud-prod"), Provider: "upcloud",
				State: "failed", Currency: reconcileText("eur"), InvoiceMinor: reconcileCents(9000), MatchedMinor: reconcileCents(9000),
				EstimateMinor: reconcileCents(9200), EstimateComplete: &isComplete,
				DifferenceReason: reconcileText("The provider's figures for this month are not final yet, so the difference is not stated until they are."),
				Imports: []client.CostReconciliationImport{{SourceDocumentID: "usage-2026-08", State: "failed", LinesState: &partial,
					Reason: reconcileText("Reading UpCloud's billing summary for 2026-08 failed (502): Bad gateway")}}},
			{CredentialID: "aaaaaaaa-0000-4000-8000-000000000003", Provider: "hetzner", State: "absent",
				DifferenceReason: reconcileText("No billing document has been imported for this month, so what the provider billed is unknown, not zero.")},
			{CredentialID: "aaaaaaaa-0000-4000-8000-000000000004", CredentialName: reconcileText("do-legacy"), Provider: "digitalocean",
				State: "complete", CurrenciesMixed: true, UnmatchedLineCount: 2,
				DifferenceReason: reconcileText("The imported lines are in more than one currency, so they are not summed.")},
			{CredentialID: "aaaaaaaa-0000-4000-8000-000000000005", CredentialName: reconcileText("ovh-idle"), Provider: "ovh",
				State: "complete", Currency: reconcileText("eur"), InvoiceMinor: reconcileCents(0), MatchedMinor: reconcileCents(0),
				DifferenceReason: reconcileText("The provider billed nothing on this credential for this month, so there is nothing to compare with the estimate.")},
		},
	}
}

func TestCostReconcileRendersEveryStateWithoutReadingUnknownAsZero(t *testing.T) {
	mock := &costReconcileMock{reconciliation: costReconciliationFixture()}
	output, err := runCostReconcileCommand(t, mock, "cost", "reconcile", "--month", "2026-08")
	if err != nil {
		t.Fatalf("cost reconcile: %v", err)
	}
	if len(mock.months) != 1 || mock.months[0] != "2026-08" {
		t.Fatalf("months requested = %v, want [2026-08]", mock.months)
	}
	for _, want := range []string{
		"Invoice reconciliation for 2026-08 (closed)",
		"ovh-prod (OVHcloud)", "€98.00", "€95.00", "€92.00", "+3.26%",
		"upcloud-prod (UpCloud)", "failed; month-to-date lines kept", "not stated",
		"deleted credential aaaaaaaa", "none imported", "unknown",
		"do-legacy (DigitalOcean)", "mixed currencies",
		"ovh-idle (OVHcloud)", "€0.00",
		"Tax €18.05 and credits -€5.00 are set apart",
		"1 billed line placed on no cluster, €3.00 in total.",
		"converted from USD at 0.92 (the rate stored 2026-09-25T00:00:00Z)",
		"not final yet",
		"Import usage-2026-08 (failed): Reading UpCloud's billing summary for 2026-08 failed (502): Bad gateway",
		"No billing document has been imported for this month",
		"2 billed lines placed on no cluster, in more than one currency.",
		"billed nothing on this credential",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
	absentRow := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "deleted credential aaaaaaaa") && strings.Contains(line, "none imported") {
			absentRow = line
		}
	}
	if absentRow == "" || strings.Contains(absentRow, "0.00") {
		t.Errorf("a never-imported credential must read unknown, never zero: %q", absentRow)
	}
}

func TestCostReconcileLeavesTheMonthToThePlatformAndPassesJSONThrough(t *testing.T) {
	mock := &costReconcileMock{reconciliation: costReconciliationFixture()}
	output, err := runCostReconcileCommand(t, mock, "cost", "reconcile", "-o", "json")
	if err != nil {
		t.Fatalf("cost reconcile -o json: %v", err)
	}
	if len(mock.months) != 1 || mock.months[0] != "" {
		t.Fatalf("months requested = %v, want the platform's default", mock.months)
	}
	var decoded client.CostReconciliation
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("-o json is not the document: %v\n%s", decodeError, output)
	}
	if len(decoded.Credentials) != 5 || decoded.Credentials[2].InvoiceMinor != nil || !strings.Contains(output, `"invoice_minor": null`) {
		t.Fatalf("-o json must keep unknown as null: %s", output)
	}
}

func TestCostReconcileRefusesAMonthItCannotRead(t *testing.T) {
	for _, month := range []string{"2026-13", "2026-8", "august", "2026-08-01", ""} {
		mock := &costReconcileMock{reconciliation: costReconciliationFixture()}
		_, err := runCostReconcileCommand(t, mock, "cost", "reconcile", "--month", month)
		if exitCodeFor(err) != exitUsage || len(mock.months) != 0 {
			t.Errorf("--month %q = %v (exit %d, requested %v), want a usage error before any request", month, err,
				exitCodeFor(err), mock.months)
		}
	}
	future := &costReconcileMock{readError: &client.UnexpectedResponseError{StatusCode: 400,
		Detail: "month must be a calendar month as YYYY-MM, not in the future"}}
	_, err := runCostReconcileCommand(t, future, "cost", "reconcile", "--month", "2099-01")
	if exitCodeFor(err) != exitUsage || err == nil || !strings.Contains(err.Error(), "not in the future") {
		t.Errorf("a month the platform refuses = %v (exit %d), want a usage error in its words", err, exitCodeFor(err))
	}
	predates := &costReconcileMock{readError: client.NewUnexpectedResponseError(404, "Not Found")}
	_, err = runCostReconcileCommand(t, predates, "cost", "reconcile")
	if err == nil || !strings.Contains(err.Error(), "predates it") {
		t.Errorf("a platform without the route = %v, want it named", err)
	}
}

func TestCostReconcileSaysWhenThereIsNothingToReconcile(t *testing.T) {
	mock := &costReconcileMock{reconciliation: &client.CostReconciliation{Month: "2026-08", MonthClosed: false,
		Credentials: []client.CostReconciliationCredential{}}}
	output, err := runCostReconcileCommand(t, mock, "cost", "reconcile")
	if err != nil || !strings.Contains(output, "(month to date)") || !strings.Contains(output, "nothing to reconcile") {
		t.Fatalf("output = %q, %v", output, err)
	}
}

// TestCostReconcileImportCellJudgesEveryKeptDocument: a failed attempt's
// cell says the kept lines are final only when every document that kept
// lines kept final ones, whichever document is listed first.
func TestCostReconcileImportCellJudgesEveryKeptDocument(t *testing.T) {
	complete, partial := "complete", "partial"
	for name, testCase := range map[string]struct {
		imports []client.CostReconciliationImport
		want    string
	}{
		"final first, month to date after": {[]client.CostReconciliationImport{{LinesState: &complete}, {LinesState: &partial}},
			"failed; month-to-date lines kept"},
		"month to date first, final after": {[]client.CostReconciliationImport{{LinesState: &partial}, {LinesState: &complete}},
			"failed; month-to-date lines kept"},
		"every kept document final": {[]client.CostReconciliationImport{{}, {LinesState: &complete}, {LinesState: &complete}},
			"failed; final lines kept"},
		"nothing kept": {[]client.CostReconciliationImport{{}}, "failed"},
	} {
		got := costReconcileImportCell(client.CostReconciliationCredential{State: "failed", Imports: testCase.imports})
		if got != testCase.want {
			t.Errorf("%s = %q, want %q", name, got, testCase.want)
		}
	}
	if estimate := costReconcileEstimate(client.CostReconciliationCredential{}); estimate != "unknown" {
		t.Errorf("an estimate the platform could not give = %q, want unknown", estimate)
	}
}

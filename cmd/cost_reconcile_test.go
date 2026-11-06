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

func reconcileRate(value float64) *float64 { return &value }

// costReconciliationFixture is one credential per state the route answers:
// a stated difference with its caveat, tax and credits, over every cluster;
// final figures not yet in (month to date after a failed refresh), over one
// cluster of two with the hours share unknown; a credential never imported,
// where no cluster was fully metered; mixed currencies, from a platform that
// predates difference_coverage; and a known zero.
func costReconciliationFixture() *client.CostReconciliation {
	difference, complete, partial, isComplete := 3.26, "complete", "partial", true
	whole, invoiceShare := 100.0, 55.5
	return &client.CostReconciliation{
		Month: "2026-08", MonthClosed: true, PeriodHours: 744, GeneratedAt: "2026-09-26T08:00:00Z",
		Credentials: []client.CostReconciliationCredential{
			{CredentialID: reconcileText("aaaaaaaa-0000-4000-8000-000000000001"), CredentialName: reconcileText("ovh-prod"), Provider: "ovh",
				State: "complete", Currency: reconcileText("eur"), InvoiceMinor: reconcileCents(9800), TaxMinor: reconcileCents(1805),
				CreditMinor: reconcileCents(-500), MatchedMinor: reconcileCents(9500), UnmatchedMinor: reconcileCents(300),
				EstimateMinor: reconcileCents(9200), EstimateComplete: &isComplete, DifferencePct: &difference,
				EstimateFX:          &client.CostReconciliationFX{From: "usd", To: "eur", RateFromUSD: reconcileRate(0.92), AsOf: reconcileText("2026-09-25T00:00:00Z")},
				UnplacedPricedMinor: reconcileCents(300), DifferenceCaveat: reconcileText("1 billed line(s) in categories the estimate prices are not placed on any cluster."),
				UnmatchedLineCount: 1, CoverageReported: true,
				DifferenceCoverage: &client.CostReconciliationCoverage{ClustersCovered: 2, ClustersNamed: 2, MatchedMinor: 9500,
					EstimateMinor: 9200, InvoiceSharePct: &whole, HoursSharePct: &whole},
				Imports: []client.CostReconciliationImport{{SourceDocumentID: "usage-2026-08", State: "complete", LinesState: &complete}}},
			{CredentialID: reconcileText("aaaaaaaa-0000-4000-8000-000000000002"), CredentialName: reconcileText("upcloud-prod"), Provider: "upcloud",
				State: "failed", Currency: reconcileText("eur"), InvoiceMinor: reconcileCents(9000), MatchedMinor: reconcileCents(9000),
				EstimateMinor: reconcileCents(9200), EstimateComplete: &isComplete, CoverageReported: true,
				DifferenceCoverage: &client.CostReconciliationCoverage{ClustersCovered: 1, ClustersNamed: 2, MatchedMinor: 5000,
					EstimateMinor: 5100, InvoiceSharePct: &invoiceShare},
				DifferenceReason: reconcileText("The provider's figures for this month are not final yet, so the difference is not stated until they are."),
				Imports: []client.CostReconciliationImport{{SourceDocumentID: "usage-2026-08", State: "failed", LinesState: &partial,
					Reason: reconcileText("Reading UpCloud's billing summary for 2026-08 failed (502): Bad gateway")}}},
			{CredentialID: reconcileText("aaaaaaaa-0000-4000-8000-000000000003"), Provider: "hetzner", State: "absent", CoverageReported: true,
				DifferenceReason: reconcileText("No billing document has been imported for this month, so what the provider billed is unknown, not zero.")},
			{CredentialID: reconcileText("aaaaaaaa-0000-4000-8000-000000000004"), CredentialName: reconcileText("do-legacy"), Provider: "digitalocean",
				State: "complete", CurrenciesMixed: true, UnmatchedLineCount: 2,
				DifferenceReason: reconcileText("The imported lines are in more than one currency, so they are not summed.")},
			{CredentialID: reconcileText("aaaaaaaa-0000-4000-8000-000000000005"), CredentialName: reconcileText("ovh-idle"), Provider: "ovh",
				State: "complete", Currency: reconcileText("eur"), InvoiceMinor: reconcileCents(0), MatchedMinor: reconcileCents(0),
				CoverageReported: true,
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

// TestCostReconcileConversionNoteNamesTheRateAppliedAndItsDay pins the note
// for each conversion cluster#6006 sends, and the older platform's sentence
// byte for byte. A GBP invoice is compared with the EUR figure, so
// rate_from_usd is null there: the note reads rate, and never says "at 0"
// as it did when it read rate_from_usd. A rate or basis the payload does not
// carry, or carries beside the wrong figure, is never named.
func TestCostReconcileConversionNoteNamesTheRateAppliedAndItsDay(t *testing.T) {
	captured, approximated, today := "captured", "approximated", "today"
	for _, testCase := range []struct {
		name string
		fx   client.CostReconciliationFX
		want string
	}{
		{"gbp from the EUR figure", client.CostReconciliationFX{From: "eur", To: "gbp", Rate: reconcileRate(0.8612),
			AsOf: reconcileText("2026-08-29T00:00:00Z"), Basis: &captured},
			"The estimate is converted from EUR into GBP at 0.8612 (the ECB rates of 2026-08-29)."},
		{"approximated", client.CostReconciliationFX{From: "eur", To: "sek", Rate: reconcileRate(11.42),
			AsOf: reconcileText("2026-08-29T00:00:00Z"), Basis: &approximated},
			"The estimate is converted from EUR into SEK at 11.42 (the ECB rates of 2026-08-29). " +
				"At least one of the month's EUR figures was made later from older data."},
		{"the built-in rate", client.CostReconciliationFX{From: "eur", To: "gbp", Rate: reconcileRate(0.85), Basis: &captured},
			"The estimate is converted from EUR into GBP at 0.85 (the built-in rate)."},
		{"today's rate", client.CostReconciliationFX{From: "usd", To: "eur", Rate: reconcileRate(0.93), RateFromUSD: reconcileRate(0.93),
			AsOf: reconcileText("2026-11-06T06:00:00Z"), Basis: &today},
			"A cluster's month recorded no EUR figure, so the estimate is the USD figure converted into EUR at today's rate, 0.93 " +
				"(the rate stored 2026-11-06T06:00:00Z), and the difference includes the rate's move since the month."},
		{"today's rate, built in", client.CostReconciliationFX{From: "usd", To: "gbp", Rate: reconcileRate(0.79), RateFromUSD: reconcileRate(0.79),
			Basis: &today},
			"A cluster's month recorded no EUR figure, so the estimate is the USD figure converted into GBP at today's rate, 0.79 " +
				"(the built-in rate), and the difference includes the rate's move since the month."},
		{"an older platform", client.CostReconciliationFX{From: "usd", To: "eur", RateFromUSD: reconcileRate(0.92),
			AsOf: reconcileText("2026-09-25T00:00:00Z")},
			"The estimate is converted from USD at 0.92 (the rate stored 2026-09-25T00:00:00Z), not the month's own rate."},
		{"an older platform, built in", client.CostReconciliationFX{From: "usd", To: "eur", RateFromUSD: reconcileRate(0.92)},
			"The estimate is converted from USD at 0.92 (the built-in rate), not the month's own rate."},
		// No platform sends a conversion without a rate, so one that arrives
		// names none: not the built-in rate, not a stored day, not a basis.
		{"no rate at all", client.CostReconciliationFX{From: "usd", To: "eur"},
			"The estimate is converted from USD at a rate the platform did not report."},
		{"no rate, but a day", client.CostReconciliationFX{From: "usd", To: "eur", AsOf: reconcileText("2026-09-25T00:00:00Z")},
			"The estimate is converted from USD at a rate the platform did not report."},
		{"an older platform's rate in upper case", client.CostReconciliationFX{From: "USD", To: "eur", RateFromUSD: reconcileRate(0.92)},
			"The estimate is converted from USD at 0.92 (the built-in rate), not the month's own rate."},
		// rate_from_usd is the rate from USD, so it is never named next to the
		// EUR figure: a payload mixing the two shapes gets no rate at all.
		{"a USD rate beside the EUR figure", client.CostReconciliationFX{From: "eur", To: "gbp", RateFromUSD: reconcileRate(0.92),
			AsOf: reconcileText("2026-09-25T00:00:00Z")},
			"The estimate is converted from EUR at a rate the platform did not report."},
		// Basis today means the month had no EUR figure, so beside the EUR
		// figure it would contradict itself: the note is the plain one.
		{"today beside the EUR figure", client.CostReconciliationFX{From: "eur", To: "gbp", Rate: reconcileRate(0.8612),
			AsOf: reconcileText("2026-08-29T00:00:00Z"), Basis: &today},
			"The estimate is converted from EUR into GBP at 0.8612 (the ECB rates of 2026-08-29)."},
		{"today's rate in upper case", client.CostReconciliationFX{From: "USD", To: "eur", Rate: reconcileRate(0.93), RateFromUSD: reconcileRate(0.93),
			Basis: &today},
			"A cluster's month recorded no EUR figure, so the estimate is the USD figure converted into EUR at today's rate, 0.93 " +
				"(the built-in rate), and the difference includes the rate's move since the month."},
	} {
		if got := costReconcileConversionNote(testCase.fx); got != testCase.want {
			t.Errorf("%s:\n got %q\nwant %q", testCase.name, got, testCase.want)
		}
	}
}

// Through the command: a GBP invoice's note names the rate applied, and an
// invoice in the figure's own currency (rate 1, from equal to to) has no
// conversion note.
func TestCostReconcilePrintsTheConversionTheEstimateRestsOn(t *testing.T) {
	captured := "captured"
	reconciliation := costReconciliationFixture()
	reconciliation.Credentials[0].Currency = reconcileText("gbp")
	reconciliation.Credentials[0].EstimateFX = &client.CostReconciliationFX{From: "eur", To: "gbp", Rate: reconcileRate(0.8612),
		AsOf: reconcileText("2026-08-29T00:00:00Z"), Basis: &captured}
	reconciliation.Credentials[1].EstimateFX = &client.CostReconciliationFX{From: "eur", To: "eur", Rate: reconcileRate(1),
		AsOf: nil, Basis: &captured}
	output, err := runCostReconcileCommand(t, &costReconcileMock{reconciliation: reconciliation}, "cost", "reconcile", "--month", "2026-08")
	if err != nil {
		t.Fatalf("cost reconcile: %v", err)
	}
	if !strings.Contains(output, "    - The estimate is converted from EUR into GBP at 0.8612 (the ECB rates of 2026-08-29).\n") {
		t.Fatalf("the GBP invoice's note must name the rate applied and its day:\n%s", output)
	}
	if strings.Count(output, "converted from") != 1 || strings.Contains(output, " at 0 ") {
		t.Fatalf("only the converted estimate carries a note, and no rate reads 0:\n%s", output)
	}
}

// The currencies are compared as currencies, whatever their case: USD against
// usd is no conversion and has no note, as usd against usd always had.
func TestCostReconcileReadsTheFigureAndInvoiceCurrencyWithoutCase(t *testing.T) {
	reconciliation := costReconciliationFixture()
	reconciliation.Credentials[0].Currency = reconcileText("usd")
	reconciliation.Credentials[0].EstimateFX = &client.CostReconciliationFX{From: "USD", To: "usd", Rate: reconcileRate(1)}
	output, err := runCostReconcileCommand(t, &costReconcileMock{reconciliation: reconciliation}, "cost", "reconcile", "--month", "2026-08")
	if err != nil {
		t.Fatalf("cost reconcile: %v", err)
	}
	if strings.Contains(output, "converted from") {
		t.Fatalf("USD against usd is the same currency and must carry no conversion note:\n%s", output)
	}
}

// costReconcileRow is the table row whose text holds every one of the
// given fragments, or "" when none does.
func costReconcileRow(output string, fragments ...string) string {
	for _, line := range strings.Split(output, "\n") {
		holdsAll := true
		for _, fragment := range fragments {
			holdsAll = holdsAll && strings.Contains(line, fragment)
		}
		if holdsAll {
			return line
		}
	}
	return ""
}

func TestCostReconcileSaysWhatTheStatedDifferenceCovers(t *testing.T) {
	mock := &costReconcileMock{reconciliation: costReconciliationFixture()}
	output, err := runCostReconcileCommand(t, mock, "cost", "reconcile", "--month", "2026-08")
	if err != nil {
		t.Fatalf("cost reconcile: %v", err)
	}
	for _, row := range []struct{ credential, coverage string }{
		{"ovh-prod (OVHcloud)", "2/2 clusters · 100% billed · 100% hours"},
		{"upcloud-prod (UpCloud)", "1/2 clusters · 55.5% billed · unknown hours"},
		{"deleted credential aaaaaaaa", "no cluster fully metered"},
		{"do-legacy (DigitalOcean)", "not reported"},
		{"ovh-idle (OVHcloud)", "no cluster fully metered"},
	} {
		if costReconcileRow(output, row.credential, row.coverage) == "" {
			t.Errorf("the %s row does not say its difference covers %q:\n%s", row.credential, row.coverage, output)
		}
	}
	if !strings.Contains(strings.ToUpper(output), "DIFFERENCE COVERS") {
		t.Errorf("the table has no Difference covers column:\n%s", output)
	}
	if strings.Contains(costReconcileRow(output, "upcloud-prod (UpCloud)"), "0% hours") {
		t.Errorf("an hours share the platform could not give must read unknown, never 0%%:\n%s", output)
	}
}

// A share prints at two decimals at most whatever precision arrives, and
// rounding never reads a part as the whole or a sliver as nothing.
func TestCostReconcileShareRoundsWithoutReachingTheWholeOrNothing(t *testing.T) {
	cases := []struct {
		share float64
		want  string
	}{
		{90.18, "90.18%"},
		{90.18000001, "90.18%"},
		{33.33333333333333, "33.33%"},
		{99.996, "99.99%"},
		{100, "100%"},
		{0.004, "0.01%"},
		{0, "0%"},
	}
	for _, testCase := range cases {
		share := testCase.share
		if got := costReconcileShare(&share); got != testCase.want {
			t.Errorf("share %v renders %q, want %q", testCase.share, got, testCase.want)
		}
	}
	if got := costReconcileShare(nil); got != "unknown" {
		t.Errorf("an absent share renders %q, want unknown", got)
	}
}

func TestCostReconcileSaysWhereAnInvoiceIsCountedAndWhenItIsWithheld(t *testing.T) {
	counter := "aaaaaaaa-0000-4000-8000-000000000001"
	unlisted := "bbbbbbbb-0000-4000-8000-000000000009"
	mock := &costReconcileMock{reconciliation: &client.CostReconciliation{Month: "2026-10", MonthClosed: true,
		Credentials: []client.CostReconciliationCredential{
			{CredentialID: reconcileText(counter), CredentialName: reconcileText("ovh-scoped"), Provider: "ovh", State: "complete",
				Currency: reconcileText("eur"), MatchedMinor: reconcileCents(4000), InvoiceWithheld: true},
			{CredentialID: reconcileText("aaaaaaaa-0000-4000-8000-000000000002"), CredentialName: reconcileText("ovh-twin"), Provider: "ovh",
				State: "complete", SameInvoiceAs: &counter},
			{CredentialID: reconcileText("aaaaaaaa-0000-4000-8000-000000000003"), CredentialName: reconcileText("ovh-other"), Provider: "ovh",
				State: "complete", SameInvoiceAs: &unlisted},
		}}}
	output, err := runCostReconcileCommand(t, mock, "cost", "reconcile")
	if err != nil {
		t.Fatalf("cost reconcile: %v", err)
	}
	for _, row := range [][]string{
		{"ovh-scoped (OVHcloud)", "withheld (scoped read)"},
		{"ovh-twin (OVHcloud)", "counted under ovh-scoped (OVHcloud)"},
		{"ovh-other (OVHcloud)", "counted under bbbbbbbb"},
	} {
		if costReconcileRow(output, row...) == "" {
			t.Errorf("no row holds %q:\n%s", row, output)
		}
	}
	if !strings.Contains(output, "the invoice total, tax, credits and unplaced lines are withheld") {
		t.Errorf("a scoped read does not say what it withheld:\n%s", output)
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

// TestCostReconcileImportCellFollowsTheCountedDocument: when the platform
// marks the document it sums, the kept lines are judged by that document
// alone, since any other document holding lines is another reading of the
// same month that is not added to it.
func TestCostReconcileImportCellFollowsTheCountedDocument(t *testing.T) {
	complete, partial := "complete", "partial"
	for name, testCase := range map[string]struct {
		imports []client.CostReconciliationImport
		want    string
	}{
		"a counted final upload beside month-to-date API lines": {[]client.CostReconciliationImport{{},
			{LinesState: &partial}, {LinesState: &complete, Counted: true}}, "failed; final lines kept"},
		"counted month-to-date lines beside a final document": {[]client.CostReconciliationImport{
			{LinesState: &complete}, {LinesState: &partial, Counted: true}}, "failed; month-to-date lines kept"},
	} {
		got := costReconcileImportCell(client.CostReconciliationCredential{State: "failed", Imports: testCase.imports})
		if got != testCase.want {
			t.Errorf("%s = %q, want %q", name, got, testCase.want)
		}
	}
}

// costReconcileOutsideFixture is a read for a reader confined to a project
// (ankra-bn86k4.64): its own credential in full, two OVHcloud credentials
// and one UpCloud credential outside its projects that bill its clusters,
// each with no id, no name and the marker, and an in-reach credential
// counted under one of them, which the platform does not say.
func costReconcileOutsideFixture() *client.CostReconciliation {
	countedUnderOutside := "This credential reads the same provider account as a credential outside your projects: " +
		"both are billed this month for mostly the same servers, volumes and load balancers, so the invoice is counted " +
		"once, under that credential, and not here."
	return &client.CostReconciliation{Month: "2026-10", MonthClosed: true,
		Credentials: []client.CostReconciliationCredential{
			{CredentialID: reconcileText("aaaaaaaa-0000-4000-8000-000000000001"), CredentialName: reconcileText("ovh-a"),
				Provider: "ovh", State: "complete", Currency: reconcileText("eur"), MatchedMinor: reconcileCents(4000),
				InvoiceWithheld: true},
			{CredentialID: reconcileText("aaaaaaaa-0000-4000-8000-000000000002"), CredentialName: reconcileText("ovh-twin"),
				Provider: "ovh", State: "complete", InvoiceWithheld: true, DifferenceReason: &countedUnderOutside},
			{CredentialOutsideProjects: true, Provider: "ovh", State: "failed", Currency: reconcileText("eur"),
				MatchedMinor: reconcileCents(1200), InvoiceWithheld: true,
				Imports: []client.CostReconciliationImport{{State: "failed"}}},
			{CredentialOutsideProjects: true, Provider: "ovh", State: "complete", Currency: reconcileText("eur"),
				MatchedMinor: reconcileCents(1700), InvoiceWithheld: true},
			{CredentialOutsideProjects: true, Provider: "upcloud", State: "complete", Currency: reconcileText("eur"),
				MatchedMinor: reconcileCents(900), InvoiceWithheld: true},
		}}
}

func TestCostReconcileNamesACredentialOutsideTheReadersProjectsWithoutCallingItDeleted(t *testing.T) {
	mock := &costReconcileMock{reconciliation: costReconcileOutsideFixture()}
	output, err := runCostReconcileCommand(t, mock, "cost", "reconcile")
	if err != nil {
		t.Fatalf("cost reconcile: %v", err)
	}
	for _, row := range [][]string{
		{"ovh-a (OVHcloud)", "€40.00"},
		{"a credential outside your projects, 1 of 2 (OVHcloud)", "failed", "€12.00"},
		{"a credential outside your projects, 2 of 2 (OVHcloud)", "€17.00"},
		{"a credential outside your projects (UpCloud)", "€9.00"},
	} {
		if costReconcileRow(output, row...) == "" {
			t.Errorf("no row holds %q:\n%s", row, output)
		}
	}
	if strings.Contains(output, "deleted credential") {
		t.Errorf("a credential the platform withheld must never read as deleted:\n%s", output)
	}
	if got := strings.Count(output, costReconcileOutsideNote); got != 3 {
		t.Errorf("each of the three withheld credentials needs the note saying what was withheld, got %d:\n%s", got, output)
	}
	notes := output[strings.Index(output, "Notes:"):]
	for _, label := range []string{"a credential outside your projects, 1 of 2 (OVHcloud)",
		"a credential outside your projects, 2 of 2 (OVHcloud)", "a credential outside your projects (UpCloud)"} {
		if !strings.Contains(notes, "  "+label+"\n") {
			t.Errorf("the notes must head each withheld credential's note with its own label %q:\n%s", label, notes)
		}
	}
	if !strings.Contains(output, "counted once, under that credential, and not here") {
		t.Errorf("an in-reach credential counted under a withheld one must say so in the platform's words:\n%s", output)
	}
}

// TestCostReconcileKeysRowsByPositionWhenTheIDIsWithheld: two withheld
// credentials of one provider share every field that once keyed a row (no
// id, no name, one provider), and each still gets its own label.
func TestCostReconcileKeysRowsByPositionWhenTheIDIsWithheld(t *testing.T) {
	credentials := costReconcileOutsideFixture().Credentials
	labels := costReconcileCredentialLabels(credentials)
	if len(labels) != len(credentials) {
		t.Fatalf("%d labels for %d credentials", len(labels), len(credentials))
	}
	seen := map[string]int{}
	for index, label := range labels {
		if label == "" {
			t.Errorf("credential %d has an empty label", index)
		}
		if previous, isTaken := seen[label]; isTaken {
			t.Errorf("credentials %d and %d share the label %q", previous, index, label)
		}
		seen[label] = index
	}
}

// TestCostReconcileLabelsAReadThatNamesEveryCredentialAsBefore: when the
// platform names every credential (an organisation-wide reader, or one that
// predates the marker), every label is the one the id-keyed rendering
// printed: the name, or "deleted credential" and the id's first block.
func TestCostReconcileLabelsAReadThatNamesEveryCredentialAsBefore(t *testing.T) {
	credentials := costReconciliationFixture().Credentials
	labels := costReconcileCredentialLabels(credentials)
	for index, credential := range credentials {
		want := "deleted credential " + strings.SplitN(*credential.CredentialID, "-", 2)[0]
		if credential.CredentialName != nil && *credential.CredentialName != "" {
			want = *credential.CredentialName
		}
		want += " (" + costProviderLabel(credential.Provider) + ")"
		if labels[index] != want {
			t.Errorf("credential %d is labelled %q, want %q as before", index, labels[index], want)
		}
	}
	mock := &costReconcileMock{reconciliation: costReconciliationFixture()}
	output, err := runCostReconcileCommand(t, mock, "cost", "reconcile")
	if err != nil {
		t.Fatalf("cost reconcile: %v", err)
	}
	if strings.Contains(output, "outside your projects") || strings.Contains(output, "did not name") {
		t.Errorf("a read that names every credential must carry no withheld label or note:\n%s", output)
	}
}

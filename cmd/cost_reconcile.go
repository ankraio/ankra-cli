package cmd

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// costReconcileMonthPattern is a calendar month as the route takes it.
var costReconcileMonthPattern = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

var costReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "What each cloud credential's provider billed for a month, against the estimate",
	Long: `Set what each cloud credential's provider billed for a month against what
Ankra's metering estimated for the same clusters.

The provider's figures are imported from its billing API (OVHcloud and
UpCloud today) and stay in the provider's own currency; the estimate is
converted into it at the rate shown. Each billed line is placed on a cluster
where the organisation's own records say which (a label Ankra set, a server
it provisioned, a node's provider id, a volume's handle). Tax and credits are
reported apart, since the estimate carries neither.

Nothing unknown reads as agreement. A credential no billing document was
imported for is unknown, not zero; a failed import says why. A difference is
stated only when the provider's figures are final, and then only over the
clusters the metering covered for the hours they existed that month (a missed
hour or so is allowed), never as the accuracy of the whole invoice: the
Difference covers column says how many of the invoice's clusters that is,
the share of the billed amount they hold, and the share of all the named
clusters' hours that was metered for them. When no cluster qualifies, or the
share is too small, no difference is stated and the reason is shown. Billed lines no cluster could
be placed on are counted with their amount.

--month reads a UTC calendar month as YYYY-MM (the last closed month when
omitted; the running month is month to date on both sides). Pass -o json (or
yaml) for the full document, including every cluster and unplaced line.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost reconcile
  ankra cost reconcile --month 2026-08
  ankra cost reconcile -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		month, _ := cmd.Flags().GetString("month")
		month = strings.TrimSpace(month)
		if cmd.Flags().Changed("month") && !costReconcileMonthPattern.MatchString(month) {
			return withExitCode(exitUsage, fmt.Errorf("--month must be a calendar month as YYYY-MM, got %q", month))
		}
		applyCostProjectScope(cmd)
		reconciliation, err := apiClient.GetCostReconciliation(month)
		if err != nil {
			return costReconcileReadError(err)
		}
		if rendered, err := renderStructured(cmd, reconciliation); rendered || err != nil {
			return err
		}
		renderCostReconciliation(cmd.OutOrStdout(), reconciliation)
		return nil
	},
}

func init() {
	costReconcileCmd.Flags().String("month", "", "UTC calendar month as YYYY-MM (the last closed month when omitted)")
	registerStructuredOutputFlags(costReconcileCmd)
	registerCostProjectScopeFlag(costReconcileCmd)
	costCmd.AddCommand(costReconcileCmd)
}

// costReconcileReadError maps the route's two refusals the user can act on:
// a month it will not read (400, a month in the future) is a usage error in
// the platform's words, and a platform that predates the route answers 404.
func costReconcileReadError(readError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) && unexpected.StatusCode == http.StatusBadRequest {
		detail := unexpected.Detail
		if detail == "" {
			detail = readError.Error()
		}
		return withExitCode(exitUsage, fmt.Errorf("the platform refused the month: %s", detail))
	}
	return costTrendReadError(readError, "GET /api/v1/org/cloud-cost/reconciliation", "the invoice reconciliation",
		"reading the invoice reconciliation")
}

// costReconcileOutsideLabel is how a credential outside the reader's
// projects is named: the platform withheld which credential it is.
const costReconcileOutsideLabel = "a credential outside your projects"

// costReconcileOutsideNote is the note beside such a credential's row.
const costReconcileOutsideNote = "Outside the projects you can read: which credential this is, its invoice numbers and " +
	"its provider's errors are withheld. Its cost on your clusters is shown."

// costReconcileCredentialLabel names one credential: its name; "deleted
// credential" and the first block of its id when the platform kept the id
// but the name is gone; or costReconcileOutsideLabel when the platform
// withheld which credential it is. A withheld credential is never called
// deleted, since withholding it says nothing about whether it still exists.
func costReconcileCredentialLabel(credential client.CostReconciliationCredential) string {
	var name string
	switch {
	case credential.CredentialOutsideProjects:
		name = costReconcileOutsideLabel
	case credential.CredentialName != nil && *credential.CredentialName != "":
		name = *credential.CredentialName
	case credential.CredentialID != nil:
		name = "deleted credential " + costShortID(*credential.CredentialID)
	default:
		name = "a credential the platform did not name"
	}
	return name + " (" + costProviderLabel(credential.Provider) + ")"
}

// costReconcileCredentialLabels names every row, in the order the platform
// listed the credentials. A row is keyed by its position, never by its
// credential id: a credential outside the reader's projects comes with no
// id, and two of them are still two rows. When one provider has more than
// one such credential, each is numbered ("1 of 2") so its notes can be told
// apart; the number says nothing about which credential it is.
func costReconcileCredentialLabels(credentials []client.CostReconciliationCredential) []string {
	outsideCount := map[string]int{}
	for _, credential := range credentials {
		if credential.CredentialOutsideProjects {
			outsideCount[credential.Provider]++
		}
	}
	outsideSeen := map[string]int{}
	labels := make([]string, len(credentials))
	for index, credential := range credentials {
		if credential.CredentialOutsideProjects && outsideCount[credential.Provider] > 1 {
			outsideSeen[credential.Provider]++
			labels[index] = fmt.Sprintf("%s, %d of %d (%s)", costReconcileOutsideLabel, outsideSeen[credential.Provider],
				outsideCount[credential.Provider], costProviderLabel(credential.Provider))
			continue
		}
		labels[index] = costReconcileCredentialLabel(credential)
	}
	return labels
}

// costShortID is the first block of an id, enough to tell rows apart.
func costShortID(id string) string {
	if index := strings.Index(id, "-"); index > 0 {
		return id[:index]
	}
	return id
}

// costReconcileImportLabel says what the last import attempt came to.
func costReconcileImportLabel(state string) string {
	switch state {
	case "absent":
		return "none imported"
	case "complete":
		return "final"
	case "partial":
		return "month to date"
	case "failed":
		return "failed"
	case "missing_billing_scope":
		return "no billing access"
	case "not_supported":
		return "not supported"
	default:
		return state
	}
}

// costReconcileImportCell is the Import column: the last attempt, and when
// it stored nothing but earlier lines were kept, whether those are final.
// The platform sums one document per month and marks it counted, so that
// document's lines say it; against a platform that marks none, the lines
// are final only when every document that kept lines kept final ones.
func costReconcileImportCell(credential client.CostReconciliationCredential) string {
	label := costReconcileImportLabel(credential.State)
	if credential.State == "complete" || credential.State == "partial" || credential.State == "absent" {
		return label
	}
	for _, document := range credential.Imports {
		switch {
		case !document.Counted || document.LinesState == nil:
			continue
		case *document.LinesState == "complete":
			return label + "; final lines kept"
		default:
			return label + "; month-to-date lines kept"
		}
	}
	keptLines, keptFinal := 0, 0
	for _, document := range credential.Imports {
		if document.LinesState == nil {
			continue
		}
		keptLines++
		if *document.LinesState == "complete" {
			keptFinal++
		}
	}
	switch {
	case keptLines == 0:
		return label
	case keptFinal == keptLines:
		return label + "; final lines kept"
	default:
		return label + "; month-to-date lines kept"
	}
}

// costReconcileAmount renders an amount in the credential's currency, or
// says why there is none: unknown is never printed as zero.
func costReconcileAmount(cents *int64, credential client.CostReconciliationCredential) string {
	switch {
	case credential.CurrenciesMixed:
		return "mixed currencies"
	case cents == nil || credential.Currency == nil:
		return "unknown"
	default:
		return formatCostCents(*cents, *credential.Currency)
	}
}

// costReconcileEstimate renders the estimate, marked as a floor when a named
// cluster was metered for fewer hours than it existed. An estimate the
// platform could not give reads unknown, like an amount, never a dash that
// could pass for none.
func costReconcileEstimate(credential client.CostReconciliationCredential) string {
	if credential.EstimateMinor == nil || credential.Currency == nil {
		return "unknown"
	}
	estimate := formatCostCents(*credential.EstimateMinor, *credential.Currency)
	if credential.EstimateComplete != nil && !*credential.EstimateComplete {
		estimate += " (floor)"
	}
	return estimate
}

// costReconcileDifference renders the stated difference with its sign, or
// "not stated" (the reason is in the notes).
func costReconcileDifference(credential client.CostReconciliationCredential) string {
	if credential.DifferencePct == nil {
		return "not stated"
	}
	if *credential.DifferencePct > 0 {
		return fmt.Sprintf("+%.2f%%", *credential.DifferencePct)
	}
	return fmt.Sprintf("%.2f%%", *credential.DifferencePct)
}

// costReconcileCoverage is the Difference covers column: how many of the
// clusters the invoice's lines are placed on the difference is taken over,
// the share of the billed amount they hold, and the share of every named
// cluster's hours that was metered for them (under 100 when the platform
// allowed a missed hour, or left a cluster out). A
// share the platform could not give reads unknown, never 0%. A platform
// that predates the field said nothing, which is not the same as saying no
// cluster qualified.
func costReconcileCoverage(credential client.CostReconciliationCredential) string {
	if !credential.CoverageReported {
		return "not reported"
	}
	coverage := credential.DifferenceCoverage
	if coverage == nil {
		return "no cluster fully metered"
	}
	return fmt.Sprintf("%d/%d clusters · %s billed · %s hours", coverage.ClustersCovered, coverage.ClustersNamed,
		costReconcileShare(coverage.InvoiceSharePct), costReconcileShare(coverage.HoursSharePct))
}

// costReconcileShare renders a share at two decimals at most, or unknown.
// Rounding never turns a part into the whole or a sliver into nothing: a
// share under 100 prints 99.99% at most, and one above 0 prints 0.01% at
// least, whatever precision the platform sent.
func costReconcileShare(percent *float64) string {
	if percent == nil {
		return "unknown"
	}
	rounded := math.Round(*percent*100) / 100
	switch {
	case *percent < 100 && rounded >= 100:
		rounded = 99.99
	case *percent > 0 && rounded <= 0:
		rounded = 0.01
	}
	return strconv.FormatFloat(rounded, 'f', -1, 64) + "%"
}

// costReconcileBilled is the Billed column. An invoice counted under another
// credential that reads the same provider account names that credential
// rather than reading unknown, and a read for a project or a set of
// clusters says the total was withheld. labelsByID holds the label of every
// credential the platform named by id.
func costReconcileBilled(credential client.CostReconciliationCredential, labelsByID map[string]string) string {
	switch {
	case credential.SameInvoiceAs != nil && *credential.SameInvoiceAs != "":
		if label, known := labelsByID[*credential.SameInvoiceAs]; known {
			return "counted under " + label
		}
		return "counted under " + costShortID(*credential.SameInvoiceAs)
	case credential.InvoiceWithheld:
		return "withheld (scoped read)"
	default:
		return costReconcileAmount(credential.InvoiceMinor, credential)
	}
}

// costReconcileConversionNote says how the estimate was put in the invoice's
// currency, for a credential whose estimate was converted. A platform that
// predates rate and basis (cluster#6006) always converted the USD figure at
// the latest stored rate, and gets the sentence it always got. Otherwise the
// note names the rate applied and the day of its rates. For basis today it
// says a cluster's month recorded no EUR figure, so the estimate is the USD
// figure at today's rate and the difference includes the rate's move since
// the month; for approximated, that a EUR figure was made later from older
// data. No rate on file is the built-in one, never a rate of 0.
func costReconcileConversionNote(fx client.CostReconciliationFX) string {
	from, to := strings.ToUpper(fx.From), strings.ToUpper(fx.To)
	storedRate := "the built-in rate"
	if fx.AsOf != nil && *fx.AsOf != "" {
		storedRate = "the rate stored " + *fx.AsOf
	}
	if fx.Rate == nil {
		if fx.RateFromUSD == nil {
			return fmt.Sprintf("The estimate is converted from %s (%s), not the month's own rate.", from, storedRate)
		}
		return fmt.Sprintf("The estimate is converted from %s at %g (%s), not the month's own rate.", from, *fx.RateFromUSD, storedRate)
	}
	basis := ""
	if fx.Basis != nil {
		basis = *fx.Basis
	}
	if basis == "today" {
		return fmt.Sprintf("A cluster's month recorded no EUR figure, so the estimate is the %s figure converted into %s at today's rate, %g (%s), "+
			"and the difference includes the rate's move since the month.", from, to, *fx.Rate, storedRate)
	}
	rateDay := "the built-in rate"
	if fx.AsOf != nil && *fx.AsOf != "" {
		day := *fx.AsOf
		if cut := strings.IndexByte(day, 'T'); cut > 0 {
			day = day[:cut]
		}
		rateDay = "the ECB rates of " + day
	}
	note := fmt.Sprintf("The estimate is converted from %s into %s at %g (%s).", from, to, *fx.Rate, rateDay)
	if basis == "approximated" {
		note += " At least one of the month's EUR figures was made later from older data."
	}
	return note
}

// costReconcileNotes is what a reader needs beside a credential's row: why
// no difference is stated, the caveat on one that is, what was set apart,
// the unplaced lines, failed imports in the provider's words, and the
// conversion basis. A credential outside the reader's projects says first
// what was withheld about it.
func costReconcileNotes(credential client.CostReconciliationCredential) []string {
	var notes []string
	if credential.CredentialOutsideProjects {
		notes = append(notes, costReconcileOutsideNote)
	}
	if credential.DifferenceReason != nil && *credential.DifferenceReason != "" {
		notes = append(notes, *credential.DifferenceReason)
	}
	if credential.DifferenceCaveat != nil && *credential.DifferenceCaveat != "" {
		notes = append(notes, *credential.DifferenceCaveat)
	}
	if credential.InvoiceWithheld {
		notes = append(notes, "Read for the project you act in: the invoice total, tax, credits and unplaced lines are "+
			"withheld, and the difference and what it covers are that project's clusters alone. --all-projects reads the "+
			"whole invoice where your access allows it.")
	}
	if credential.Currency != nil && !credential.CurrenciesMixed {
		currency := *credential.Currency
		if (credential.TaxMinor != nil && *credential.TaxMinor != 0) || (credential.CreditMinor != nil && *credential.CreditMinor != 0) {
			notes = append(notes, fmt.Sprintf("Tax %s and credits %s are set apart: the estimate carries neither.",
				formatOptionalCostCents(credential.TaxMinor, currency), formatOptionalCostCents(credential.CreditMinor, currency)))
		}
		if credential.UnmatchedLineCount > 0 {
			note := fmt.Sprintf("%s placed on no cluster, %s in total.", pluralCount(credential.UnmatchedLineCount, "billed line"),
				formatOptionalCostCents(credential.UnmatchedMinor, currency))
			if credential.UnmatchedTruncated {
				note += fmt.Sprintf(" The largest %d are listed with -o json.", len(credential.UnmatchedLines))
			}
			notes = append(notes, note)
		}
		if credential.EstimateFX != nil && credential.EstimateFX.From != credential.EstimateFX.To {
			notes = append(notes, costReconcileConversionNote(*credential.EstimateFX))
		}
	} else if credential.CurrenciesMixed && credential.UnmatchedLineCount > 0 {
		notes = append(notes, pluralCount(credential.UnmatchedLineCount, "billed line")+" placed on no cluster, in more than one currency.")
	}
	for _, document := range credential.Imports {
		if document.Reason == nil || *document.Reason == "" {
			continue
		}
		notes = append(notes, fmt.Sprintf("Import %s (%s): %s", document.SourceDocumentID,
			costReconcileImportLabel(document.State), *document.Reason))
	}
	return notes
}

func renderCostReconciliation(out io.Writer, reconciliation *client.CostReconciliation) {
	period := "closed"
	if !reconciliation.MonthClosed {
		period = "month to date"
	}
	_, _ = fmt.Fprintf(out, "Invoice reconciliation for %s (%s)", reconciliation.Month, period)
	if reconciliation.GeneratedAt != "" {
		_, _ = fmt.Fprintf(out, " · generated %s", reconciliation.GeneratedAt)
	}
	_, _ = fmt.Fprintln(out)
	if len(reconciliation.Credentials) == 0 {
		_, _ = fmt.Fprintln(out, "This organisation has no cloud credential of a provider that bills, so there is nothing to reconcile.")
		return
	}
	_, _ = fmt.Fprintln(out)
	labels := costReconcileCredentialLabels(reconciliation.Credentials)
	labelsByID := make(map[string]string, len(reconciliation.Credentials))
	for index, credential := range reconciliation.Credentials {
		if credential.CredentialID != nil {
			labelsByID[*credential.CredentialID] = labels[index]
		}
	}
	writer := newCostTable(out)
	writer.SetColumnConfigs([]table.ColumnConfig{
		{Number: 3, Align: text.AlignRight},
		{Number: 4, Align: text.AlignRight},
		{Number: 5, Align: text.AlignRight},
		{Number: 6, Align: text.AlignRight},
	})
	writer.AppendHeader(table.Row{"Credential", "Import", "Billed", "On clusters", "Estimate", "Difference", "Difference covers"})
	for index, credential := range reconciliation.Credentials {
		writer.AppendRow(table.Row{
			labels[index],
			costReconcileImportCell(credential),
			costReconcileBilled(credential, labelsByID),
			costReconcileAmount(credential.MatchedMinor, credential),
			costReconcileEstimate(credential),
			costReconcileDifference(credential),
			costReconcileCoverage(credential),
		})
	}
	writer.Render()

	hasNotes := false
	for index, credential := range reconciliation.Credentials {
		notes := costReconcileNotes(credential)
		if len(notes) == 0 {
			continue
		}
		if !hasNotes {
			_, _ = fmt.Fprintln(out)
			_, _ = fmt.Fprintln(out, "Notes:")
			hasNotes = true
		}
		_, _ = fmt.Fprintf(out, "  %s\n", labels[index])
		for _, note := range notes {
			_, _ = fmt.Fprintf(out, "    - %s\n", note)
		}
	}
}

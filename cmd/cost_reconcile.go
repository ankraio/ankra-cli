package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
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
imported for is unknown, not zero; a failed import says why; a difference is
stated only when the provider's figures are final and the estimate covers
every hour each named cluster existed that month, and otherwise the reason is
shown. Billed lines no cluster could be placed on are counted with their
amount.

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

// costReconcileCredentialLabel names a credential, or says it was deleted
// since the month it billed.
func costReconcileCredentialLabel(credential client.CostReconciliationCredential) string {
	name := "deleted credential " + costShortID(credential.CredentialID)
	if credential.CredentialName != nil && *credential.CredentialName != "" {
		name = *credential.CredentialName
	}
	return name + " (" + costProviderLabel(credential.Provider) + ")"
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
// it stored nothing but earlier ones did, whether the lines kept from them
// are final - only when every document that kept lines kept final ones.
func costReconcileImportCell(credential client.CostReconciliationCredential) string {
	label := costReconcileImportLabel(credential.State)
	if credential.State == "complete" || credential.State == "partial" || credential.State == "absent" {
		return label
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

// costReconcileNotes is what a reader needs beside a credential's row: why
// no difference is stated, the caveat on one that is, what was set apart,
// the unplaced lines, failed imports in the provider's words, and the
// conversion basis.
func costReconcileNotes(credential client.CostReconciliationCredential) []string {
	var notes []string
	if credential.DifferenceReason != nil && *credential.DifferenceReason != "" {
		notes = append(notes, *credential.DifferenceReason)
	}
	if credential.DifferenceCaveat != nil && *credential.DifferenceCaveat != "" {
		notes = append(notes, *credential.DifferenceCaveat)
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
			basis := "the built-in rate"
			if credential.EstimateFX.AsOf != nil && *credential.EstimateFX.AsOf != "" {
				basis = "the rate stored " + *credential.EstimateFX.AsOf
			}
			notes = append(notes, fmt.Sprintf("The estimate is converted from %s at %g (%s), not the month's own rate.",
				strings.ToUpper(credential.EstimateFX.From), credential.EstimateFX.RateFromUSD, basis))
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
	writer := newCostTable(out)
	writer.SetColumnConfigs([]table.ColumnConfig{
		{Number: 3, Align: text.AlignRight},
		{Number: 4, Align: text.AlignRight},
		{Number: 5, Align: text.AlignRight},
		{Number: 6, Align: text.AlignRight},
	})
	writer.AppendHeader(table.Row{"Credential", "Import", "Billed", "On clusters", "Estimate", "Difference"})
	for _, credential := range reconciliation.Credentials {
		writer.AppendRow(table.Row{
			costReconcileCredentialLabel(credential),
			costReconcileImportCell(credential),
			costReconcileAmount(credential.InvoiceMinor, credential),
			costReconcileAmount(credential.MatchedMinor, credential),
			costReconcileEstimate(credential),
			costReconcileDifference(credential),
		})
	}
	writer.Render()

	hasNotes := false
	for _, credential := range reconciliation.Credentials {
		notes := costReconcileNotes(credential)
		if len(notes) == 0 {
			continue
		}
		if !hasNotes {
			_, _ = fmt.Fprintln(out)
			_, _ = fmt.Fprintln(out, "Notes:")
			hasNotes = true
		}
		_, _ = fmt.Fprintf(out, "  %s\n", costReconcileCredentialLabel(credential))
		for _, note := range notes {
			_, _ = fmt.Fprintf(out, "    - %s\n", note)
		}
	}
}

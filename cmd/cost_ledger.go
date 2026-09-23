package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// costLedgerVerificationDays is the length of the platform's measurement
// window (decisionledger.VerificationWindow): a change is measured seven days
// after it ran. The ledger sends how many days have passed, not the length.
const costLedgerVerificationDays = 7

// The widest the cluster and lever cells grow before they wrap, so a long
// name cannot push the table past a 100-column terminal. The cells are
// wrapped here rather than through a column WidthMax: go-pretty applies a
// column's WidthMax to a merged cell that starts in it too, which would fold
// every note under a row into the first column's width.
const (
	costLedgerClusterWidthMax = 24
	costLedgerLeverWidthMax   = 22
)

var costLedgerCmd = &cobra.Command{
	Use:   "ledger",
	Short: "Measured outcomes of cost decisions: what each approved change was expected to save and what it saved",
	Long: `Read the organisation's cost ledger - every cost decision that was approved,
is running or has run, with the monthly saving it was expected to bring and
the saving that was measured.

Seven days after a change runs, Ankra compares the cluster's run rate before
the change with its run rate at day seven, both read from the cluster's
hourly cost snapshots, and records the difference as the measured saving. A
negative measured figure is a measurement too: the run rate rose. A saving
that could not be measured stays unknown, never zero, and the row says why:
the cluster was not priced the same way on both sides of the change
(coverage moved), or there was no snapshot to read. A change undone inside
its window reads reverted. A right-size also carries its usage verification
(in progress, passed, failed with a rollback proposed, or unverified when
metrics were missing), shown under its row.

The header totals cover the whole ledger even when the row list stops at the
newest changes: the savings measured this calendar month (UTC), everything
measured so far, and the expected saving of the changes still approved,
running or verifying, which counts as saved only once it is measured.

Every figure is a monthly run rate in the organisation's display currency.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost ledger
  ankra cost ledger -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ledger, err := apiClient.GetCloudLedger()
		if err != nil {
			return cloudLedgerReadError(err)
		}
		if rendered, err := renderStructured(cmd, ledger); rendered || err != nil {
			return err
		}
		renderCloudLedger(cmd.OutOrStdout(), ledger)
		return nil
	},
}

func init() {
	registerStructuredOutputFlags(costLedgerCmd)
	costCmd.AddCommand(costLedgerCmd)
}

// cloudLedgerReadError maps the one 404 this route can answer with: a
// platform that predates the measured ledger serves no /api/v1/org/cloud-cost/
// ledger at all, and "request failed: status 404" reads as an auth or token
// problem. The status alone decides, as in cloudSavingsReadError.
func cloudLedgerReadError(readError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) && unexpected.StatusCode == http.StatusNotFound {
		return withExitCode(exitError, errors.New(
			"this platform does not serve the cost ledger: "+
				"GET /api/v1/org/cloud-cost/ledger is not registered, so this platform predates "+
				"the measured outcomes of cost decisions"))
	}
	return fmt.Errorf("reading the cost ledger: %w", readError)
}

// costLedgerLever names a ledger row's lever in plain words.
func costLedgerLever(lever string) string {
	switch lever {
	case "off_hours_schedule":
		return "Off-hours schedule"
	case "right_size":
		return "Right-size"
	case "right_size_rollback":
		return "Right-size rollback"
	case "waste_cleanup":
		return "Waste cleanup"
	case "":
		return "—"
	default:
		words := strings.ReplaceAll(lever, "_", " ")
		return strings.ToUpper(words[:1]) + words[1:]
	}
}

// costLedgerStatus says where a row's measurement stands, in plain words.
func costLedgerStatus(row client.CloudLedgerRow) string {
	switch row.MeasurementStatus {
	case "measured":
		return "measured"
	case "pending":
		if row.Days == nil {
			return "verifying"
		}
		return fmt.Sprintf("verifying · day %d of %d", min(max(*row.Days, 0), costLedgerVerificationDays), costLedgerVerificationDays)
	case "unmeasured_coverage_moved":
		return "unmeasured · coverage moved"
	case "unmeasured_no_snapshots":
		return "unmeasured · no snapshots"
	case "reverted":
		return "reverted"
	case "not_applicable", "":
		// Nothing is being measured: the change has not run yet, or it names
		// no cluster whose snapshots could measure it.
		switch row.Status {
		case "approved":
			return "approved · not run yet"
		case "running":
			return "running"
		default:
			return "not measured"
		}
	default:
		return strings.ReplaceAll(row.MeasurementStatus, "_", " ")
	}
}

// costLedgerVerification words a right-size's usage verification, or returns
// "" when the row carries none (another lever, or a platform that predates it).
func costLedgerVerification(row client.CloudLedgerRow) string {
	if row.VerificationStatus == nil {
		return ""
	}
	switch *row.VerificationStatus {
	case "", "not_applicable":
		return ""
	case "verifying":
		return "Usage verification in progress."
	case "passed":
		return "Usage verification passed."
	case "failed":
		return "Usage verification failed; a rollback is proposed."
	case "unverified_no_metrics":
		return "Usage verification unverified: metrics were missing on at least one day."
	default:
		return "Usage verification: " + strings.ReplaceAll(*row.VerificationStatus, "_", " ") + "."
	}
}

// costLedgerNotes is what goes under a row: the platform's reason for an
// unmeasured or reverted row, then the usage verification.
func costLedgerNotes(row client.CloudLedgerRow) []string {
	var notes []string
	if row.MeasurementReason != nil && strings.TrimSpace(*row.MeasurementReason) != "" {
		notes = append(notes, strings.TrimSpace(*row.MeasurementReason))
	}
	if verification := costLedgerVerification(row); verification != "" {
		notes = append(notes, verification)
	}
	return notes
}

func costLedgerCluster(row client.CloudLedgerRow) string {
	switch {
	case row.ClusterName != nil && *row.ClusterName != "":
		return *row.ClusterName
	case row.ClusterID != nil && *row.ClusterID != "":
		// The cluster is gone, so there is no name to show; the id prefix is
		// enough to find the row in -o json.
		return "cluster " + (*row.ClusterID)[:min(8, len(*row.ClusterID))]
	default:
		return "—"
	}
}

// costLedgerMonth turns the ledger's YYYY-MM into "September 2026".
func costLedgerMonth(month string) string {
	parsed, parseError := time.Parse("2006-01", month)
	if parseError != nil {
		if month == "" {
			return "this month"
		}
		return month
	}
	return parsed.Format("January 2006")
}

// wrapCostLedgerNote wraps a note to width and marks it as belonging to the
// row above it.
func wrapCostLedgerNote(note string, width int) string {
	lines := strings.Split(text.WrapSoft(note, width-2), "\n")
	for index, line := range lines {
		prefix := "  "
		if index == 0 {
			prefix = "↳ "
		}
		lines[index] = prefix + strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

func costLedgerIsEmpty(ledger *client.CloudLedger) bool {
	return len(ledger.Rows) == 0 && !ledger.Truncated &&
		ledger.Counts == (client.CloudLedgerCounts{}) &&
		ledger.MeasuredTotalCents == 0 && ledger.MeasuredThisMonthCents == 0 && ledger.RunningTotalCents == 0
}

func renderCloudLedger(out io.Writer, ledger *client.CloudLedger) {
	if costLedgerIsEmpty(ledger) {
		_, _ = fmt.Fprintln(out, "No cost decision has run through the ledger yet.")
		_, _ = fmt.Fprintln(out, "A cost change appears here once it is approved, and its saving is measured seven days after it runs.")
		return
	}
	currency := ledger.Currency
	_, _ = fmt.Fprintf(out, "Cost ledger (%s): %s/mo measured in %s · %s/mo measured in all\n",
		strings.ToUpper(currency), formatCostCents(ledger.MeasuredThisMonthCents, currency),
		costLedgerMonth(ledger.Month), formatCostCents(ledger.MeasuredTotalCents, currency))
	_, _ = fmt.Fprintf(out, "  %s/mo expected from changes approved, running or verifying (not saved until measured)\n",
		formatCostCents(ledger.RunningTotalCents, currency))
	_, _ = fmt.Fprintf(out, "  %d measured · %d verifying · %d unmeasured · %d reverted",
		ledger.Counts.Measured, ledger.Counts.Pending, ledger.Counts.Unmeasured, ledger.Counts.Reverted)
	if ledger.GeneratedAt != "" {
		_, _ = fmt.Fprintf(out, " · generated %s", ledger.GeneratedAt)
	}
	_, _ = fmt.Fprintln(out)
	if len(ledger.Rows) == 0 {
		return
	}

	header := table.Row{"Cluster", "Lever", "Status", "Expected/mo", "Measured/mo"}
	rows := make([]table.Row, 0, len(ledger.Rows))
	for _, row := range ledger.Rows {
		rows = append(rows, table.Row{
			text.WrapSoft(costLedgerCluster(row), costLedgerClusterWidthMax),
			text.WrapSoft(costLedgerLever(row.Lever), costLedgerLeverWidthMax),
			costLedgerStatus(row),
			formatOptionalCostCents(row.ExpectedMonthlyCents, currency),
			formatOptionalCostCents(row.MeasuredMonthlyCents, currency),
		})
	}

	// A note spans the whole row under the one it explains. go-pretty widens
	// the columns for a merged cell longer than their sum plus one per
	// separator, so the note is wrapped to exactly that and never moves the
	// table's width.
	noteWidth := len(header) - 1
	for column := range header {
		width := text.LongestLineLen(fmt.Sprint(header[column]))
		for _, row := range rows {
			width = max(width, text.LongestLineLen(fmt.Sprint(row[column])))
		}
		noteWidth += width
	}

	_, _ = fmt.Fprintln(out)
	writer := newCostTable(out)
	writer.SetColumnConfigs([]table.ColumnConfig{
		{Number: 4, Align: text.AlignRight},
		{Number: 5, Align: text.AlignRight},
	})
	writer.AppendHeader(header)
	for index, row := range ledger.Rows {
		writer.AppendRow(rows[index])
		for _, note := range costLedgerNotes(row) {
			wrapped := wrapCostLedgerNote(note, noteWidth)
			writer.AppendRow(table.Row{wrapped, wrapped, wrapped, wrapped, wrapped},
				table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignLeft})
		}
	}
	writer.Render()
	if ledger.Truncated {
		_, _ = fmt.Fprintf(out, "(showing the newest %d changes of more; the totals above cover all of them)\n", len(ledger.Rows))
	}
}

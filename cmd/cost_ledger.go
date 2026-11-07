package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// costLedgerVerificationDays is the length of the platform's measurement
// window (decisionledger.VerificationWindow): a change is measured seven days
// after it ran. The ledger sends how many days have passed, not the length.
const costLedgerVerificationDays = 7

// costLedgerApprovalDays is how long a cost approval lasts on the platform
// (decisionledger.CostApprovalTTL): a change approved longer ago that never
// started approves nothing any more. The ledger sends the verdict
// (approval_expired), not the length.
const costLedgerApprovalDays = 7

// The widest the cluster, lever and status cells grow before they wrap, so a
// long name or status cannot push the table past a 100-column terminal. The
// cells are wrapped here rather than through a column WidthMax: go-pretty
// applies a column's WidthMax to a merged cell that starts in it too, which
// would fold every note under a row into the first column's width. The status
// width is that of "unmeasured · coverage moved", so only the longer statuses
// wrap.
const (
	costLedgerClusterWidthMax = 24
	costLedgerLeverWidthMax   = 22
	costLedgerStatusWidthMax  = 27
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

An approval lasts seven days. A change approved longer ago that never started
reads "approval lapsed": it will not run until it is approved again. A
rollback approved that long ago reads "approved · may have lapsed", because
its approval stands only if it is confirmed to undo its right-size, which
'ankra cost decisions get' tells.

The header totals cover the whole ledger even when the row list stops at the
newest changes: the savings measured this calendar month (UTC), everything
measured so far, and the expected saving of the changes still approved,
running or verifying, which counts as saved only once it is measured. A
lapsed approval, or one that may have lapsed, is not in that total.

Every figure is a monthly run rate in the organisation's display currency.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost ledger
  ankra cost ledger -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		applyCostProjectScope(cmd)
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
	registerCostProjectScopeFlag(costLedgerCmd)
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
		// A lever this CLI predates reads as its words, capitalised by rune
		// so a multibyte first letter is never split.
		words := []rune(strings.ReplaceAll(lever, "_", " "))
		return string(unicode.ToUpper(words[0])) + string(words[1:])
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
	case "unmeasured_confounded":
		return "unmeasured · another change overlapped"
	case "unmeasured_within_noise":
		return "unmeasured · within normal day-to-day swings"
	case "reverted":
		return "reverted"
	case "not_applicable", "":
		// Nothing is being measured: the change has not run yet, or it names
		// no cluster whose snapshots could measure it. An approval that lapsed
		// before the change started approves nothing any more, so the row is
		// not "approved"; one the platform could not judge is said to be
		// unknown, never lapsed and never standing. A platform that predates
		// approval_expired keeps the words below.
		switch costLedgerApprovalState(row) {
		case costLedgerApprovalLapsed:
			return "approval lapsed"
		case costLedgerApprovalUnknown:
			return "approved · may have lapsed"
		}
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

// The approval verdicts a ledger row can show besides "it stands".
const (
	costLedgerApprovalStands = ""
	costLedgerApprovalLapsed = "lapsed"
	// costLedgerApprovalUnknown is approval_expired sent as null: the
	// platform could not judge the approval. Absent is an older platform,
	// not an unknown.
	costLedgerApprovalUnknown = "unknown"
)

// costLedgerApprovalState is the verdict a row's approval_expired gives, for a
// change that has not started. A running or succeeded change ran on its
// approval, which the platform answers false for; were it to send anything
// else, the change's own status is what the row says. Any other status,
// including one this CLI predates (the platform judges held approvals too),
// takes the platform's verdict.
func costLedgerApprovalState(row client.CloudLedgerRow) string {
	if row.Status == "running" || row.Status == "succeeded" || !row.ApprovalExpired.Present {
		return costLedgerApprovalStands
	}
	switch {
	case row.ApprovalExpired.Value == nil:
		return costLedgerApprovalUnknown
	case *row.ApprovalExpired.Value:
		return costLedgerApprovalLapsed
	default:
		return costLedgerApprovalStands
	}
}

// costLedgerApprovalNote explains a lapsed approval, or one the platform could
// not judge, and says the expectation beside it is not in the running total.
// It returns "" for any other row. A measured, running or reverted row is
// judged by its measurement, so only a row nothing measures gets the note.
func costLedgerApprovalNote(row client.CloudLedgerRow) string {
	if row.MeasurementStatus != "not_applicable" && row.MeasurementStatus != "" {
		return ""
	}
	leftOut := ""
	if row.ExpectedMonthlyCents != nil {
		leftOut = " Its expected saving is left out of the total above."
	}
	switch costLedgerApprovalState(row) {
	case costLedgerApprovalLapsed:
		return fmt.Sprintf("Approved more than %d days ago and never started, so the approval has lapsed and the change will not run.%s "+
			"Read it with 'ankra cost decisions get %s' and approve it again if it is still what you want.",
			costLedgerApprovalDays, leftOut, row.DecisionID)
	case costLedgerApprovalUnknown:
		return fmt.Sprintf("Approved more than %d days ago and not run yet. A rollback keeps its approval past that only when it is "+
			"confirmed to undo its right-size, which the ledger does not check, so whether this approval still stands is "+
			"unknown: 'ankra cost decisions get %s' says.%s", costLedgerApprovalDays, row.DecisionID, leftOut)
	default:
		return ""
	}
}

// costLedgerNotes is what goes under a row: why its approval lapsed or may
// have, the platform's reason for an unmeasured or reverted row, then the
// usage verification.
func costLedgerNotes(row client.CloudLedgerRow) []string {
	var notes []string
	if approval := costLedgerApprovalNote(row); approval != "" {
		notes = append(notes, approval)
	}
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
			text.WrapSoft(costLedgerStatus(row), costLedgerStatusWidthMax),
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

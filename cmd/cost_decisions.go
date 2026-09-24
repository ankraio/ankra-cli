package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

const (
	// costDecisionsArea is the ledger area this command family lists.
	costDecisionsArea = "cost"
	// costDecisionsDefaultLimit is the list route's page when no limit is
	// sent (openapi: default 200, at most 500).
	costDecisionsDefaultLimit = 200
	// costDecisionsMaxLimit is the most the list route serves in one page.
	costDecisionsMaxLimit = 500
	// costDecisionsSummaryWidthMax is the widest the summary cell grows
	// before it wraps, so the table fits a 100-column terminal.
	costDecisionsSummaryWidthMax = 40
	// costDecisionsNoSnapshotConsent is the only consent execute accepts.
	costDecisionsNoSnapshotConsent = "no_snapshot_acknowledged"
)

var costDecisionsCmd = &cobra.Command{
	Use:   "decisions",
	Short: "The cost decision ledger: each proposed change, approving or setting it aside, and running it",
	Long: `The decision ledger holds every cost change Ankra proposes - a right-size, a
right-size ladder, an off-hours schedule, a waste cleanup - with its plan,
where it stands, who decided and what running it did.

A proposal is proposed until a person approves or sets it aside; an approved
one runs when a person executes it (or, for one the cost autopilot approved,
after its pre-notice unless it is held). A right-size ladder runs one wave at
a time: each wave is its own right_size proposal under the ladder, and the
next wave runs only once the last one has passed its seven-day verification.

Reading is open to every member. Approving, setting aside and running need
billing.manage, and running a right-size also needs clusters.write.`,
}

var costDecisionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the cost proposals, newest first, with a ladder's waves under it",
	Long: `List the organisation's cost proposals, newest first. A right-size ladder's
waves sit under the ladder when it is on the same page.

--status is applied by the platform (proposed, approved, held, set_aside,
running, succeeded, failed, superseded). --kind and --cluster narrow the page
the platform returned, by the proposal's kind and by the cluster its change
is measured on; -o json returns that narrowed page. The platform serves the
newest 200 by default; --limit asks for up to 500.

A running proposal the platform could not check against its operation on this
read is marked as such: it may already have finished.`,
	Args: cobra.NoArgs,
	Example: `  ankra cost decisions list
  ankra cost decisions list --status approved
  ankra cost decisions list --kind right_size_ladder --cluster prod-eu
  ankra cost decisions list -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		flags := cmd.Flags()
		filter := client.DecisionListFilter{Area: costDecisionsArea}
		if flags.Changed("status") {
			status, _ := flags.GetString("status")
			filter.Status = strings.ToLower(strings.TrimSpace(status))
		}
		if flags.Changed("limit") {
			limit, _ := flags.GetInt("limit")
			if limit < 1 || limit > costDecisionsMaxLimit {
				return withExitCode(exitUsage, fmt.Errorf("--limit must be between 1 and %d", costDecisionsMaxLimit))
			}
			filter.Limit = limit
		}
		kind, _ := flags.GetString("kind")
		kind = strings.ToLower(strings.TrimSpace(kind))
		clusterID := ""
		if flags.Changed("cluster") {
			reference, _ := flags.GetString("cluster")
			resolved, resolveError := resolveClusterID(strings.TrimSpace(reference))
			if resolveError != nil {
				return resolveError
			}
			clusterID = resolved
		}
		listing, err := apiClient.ListDecisions(filter)
		if err != nil {
			return costDecisionsError(err, "reading the cost decisions", "GET /api/v1/org/decisions", false)
		}
		pageSize := len(listing.Proposals)
		narrowed := costDecisionsNarrow(listing, kind, clusterID)
		if rendered, err := renderStructured(cmd, narrowed); rendered || err != nil {
			return err
		}
		limit := filter.Limit
		if limit == 0 {
			limit = costDecisionsDefaultLimit
		}
		renderCostDecisions(cmd.OutOrStdout(), narrowed, pageSize >= limit, limit)
		return nil
	},
}

var costDecisionsGetCmd = &cobra.Command{
	Use:   "get <decision-id>",
	Short: "Show one proposal: its plan, decision, outcome, receipt and, for a ladder, its waves",
	Args:  cobra.ExactArgs(1),
	Example: `  ankra cost decisions get 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e
  ankra cost decisions get 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		decisionID := strings.TrimSpace(args[0])
		if err := costDecisionRequireID(decisionID); err != nil {
			return err
		}
		proposal, err := apiClient.GetDecision(decisionID)
		if err != nil {
			return costDecisionsError(err, "reading the decision", "GET /api/v1/org/decisions/{decision_id}", true)
		}
		if rendered, err := renderStructured(cmd, proposal); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		renderCostDecision(out, proposal)
		if proposal.Kind == "right_size_ladder" {
			renderCostDecisionWaves(out, proposal)
		}
		return nil
	},
}

var costDecisionsActivityCmd = &cobra.Command{
	Use:   "activity <decision-id>",
	Short: "Everything that happened to a proposal, newest first",
	Args:  cobra.ExactArgs(1),
	Example: `  ankra cost decisions activity 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e
  ankra cost decisions activity 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		decisionID := strings.TrimSpace(args[0])
		if err := costDecisionRequireID(decisionID); err != nil {
			return err
		}
		activity, err := apiClient.GetDecisionActivity(decisionID)
		if err != nil {
			return costDecisionsError(err, "reading the decision's activity", "GET /api/v1/org/decisions/{decision_id}/activity", true)
		}
		if rendered, err := renderStructured(cmd, activity); rendered || err != nil {
			return err
		}
		renderCostDecisionActivity(cmd.OutOrStdout(), activity)
		return nil
	},
}

var costDecisionsApproveCmd = &cobra.Command{
	Use:   "approve <decision-id>",
	Short: "Approve a proposal, with an optional note (a set-aside or failed one is re-opened or retried)",
	Args:  cobra.ExactArgs(1),
	Example: `  ankra cost decisions approve 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e
  ankra cost decisions approve 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e --note "Checked with the platform team"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		decisionID := strings.TrimSpace(args[0])
		if err := costDecisionRequireID(decisionID); err != nil {
			return err
		}
		proposal, err := apiClient.ApproveDecision(decisionID, costDecisionNote(cmd))
		if err != nil {
			return costDecisionsError(err, "approving the decision", "POST /api/v1/org/decisions/{decision_id}/approve", true)
		}
		if rendered, err := renderStructured(cmd, proposal); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Approved %s: %s\n", proposal.ID, proposal.Summary)
		if proposal.Executable {
			_, _ = fmt.Fprintf(out, "Run it with: ankra cost decisions execute %s\n", proposal.ID)
		} else {
			_, _ = fmt.Fprintf(out, "The platform cannot run a %s proposal yet, so it stays approved until it is carried out another way.\n", proposal.Kind)
		}
		return nil
	},
}

var costDecisionsSetAsideCmd = &cobra.Command{
	Use:   "set-aside <decision-id>",
	Short: "Decline a proposal, with your reason (an approved or held one is withdrawn before it runs)",
	Args:  cobra.ExactArgs(1),
	Example: `  ankra cost decisions set-aside 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e --note "Needed for the launch"
  ankra cost decisions set-aside 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e --note "Needed for the launch" --yes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		decisionID := strings.TrimSpace(args[0])
		if err := costDecisionRequireID(decisionID); err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		// The prompt goes to stderr so -o json keeps stdout parseable.
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Set aside decision %s? It will not run unless someone approves it again. [y/N]: ", decisionID), yes); confirmError != nil {
			return confirmError
		}
		proposal, err := apiClient.SetAsideDecision(decisionID, costDecisionNote(cmd))
		if err != nil {
			return costDecisionsError(err, "setting the decision aside", "POST /api/v1/org/decisions/{decision_id}/set-aside", true)
		}
		if rendered, err := renderStructured(cmd, proposal); rendered || err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Set aside %s: %s\n", proposal.ID, proposal.Summary)
		return nil
	},
}

var costDecisionsExecuteCmd = &cobra.Command{
	Use:   "execute <decision-id>",
	Short: "Run an approved proposal: dispatch the platform operation its plan names",
	Long: `Run an approved proposal as you: the platform dispatches the operation its
kind maps to (an off-hours schedule creates the stop and start power
schedules, a waste cleanup files the cloud cleanup job, a right-size ladder
files and runs its next wave) and records the receipt. The answer is the
proposal as the run left it: running with the operation to follow,
succeeded, or failed with the receipt saying why (exit 1).

A plan that deletes something no snapshot keeps (an unattached volume) runs
only with written consent: pass --acknowledge-no-snapshot once you accept
that the deletion is final. Without it the platform refuses and nothing is
attempted. --min-unattached-days sets the minimum age of what a waste
cleanup deletes (the platform raises anything under 30). Neither is ever sent
unless you pass it.

A ladder whose latest wave is still running, inside its seven-day
verification or tripped by it is not run: the platform says which, and
running it again before that changes is refused the same way.`,
	Args: cobra.ExactArgs(1),
	Example: `  ankra cost decisions execute 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e
  ankra cost decisions execute 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e --acknowledge-no-snapshot --min-unattached-days 60 --yes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		decisionID := strings.TrimSpace(args[0])
		if err := costDecisionRequireID(decisionID); err != nil {
			return err
		}
		options, optionsError := costDecisionExecuteOptions(cmd)
		if optionsError != nil {
			return optionsError
		}
		yes, _ := cmd.Flags().GetBool("yes")
		// The prompt goes to stderr so -o json keeps stdout parseable.
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Run decision %s now? The platform operation its plan names is dispatched as you. [y/N]: ", decisionID), yes); confirmError != nil {
			return confirmError
		}
		proposal, err := apiClient.ExecuteDecision(decisionID, options)
		if err != nil {
			return costDecisionExecuteError(err, decisionID)
		}
		if rendered, renderError := renderStructured(cmd, proposal); rendered || renderError != nil {
			if renderError == nil && proposal.Status == "failed" {
				return withExitCode(exitError, fmt.Errorf("decision %s failed; its receipt says why", proposal.ID))
			}
			return renderError
		}
		return renderCostDecisionRun(cmd.OutOrStdout(), proposal)
	},
}

func init() {
	costDecisionsListCmd.Flags().String("status", "", "Only proposals in this status: proposed, approved, held, set_aside, running, succeeded, failed or superseded")
	costDecisionsListCmd.Flags().String("kind", "", "Only proposals of this kind, e.g. right_size, right_size_ladder, off_hours_schedule, waste_cleanup")
	costDecisionsListCmd.Flags().String("cluster", "", "Only proposals whose change is measured on this cluster (name or id)")
	costDecisionsListCmd.Flags().Int("limit", 0, "How many of the newest proposals the platform returns (1-500; 200 when omitted)")
	for _, command := range []*cobra.Command{costDecisionsApproveCmd, costDecisionsSetAsideCmd} {
		command.Flags().String("note", "", "A note recorded with the decision (up to 2000 characters)")
	}
	costDecisionsSetAsideCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	costDecisionsExecuteCmd.Flags().Bool("acknowledge-no-snapshot", false,
		"Give the written consent a plan that deletes something no snapshot keeps needs: the deletion is final")
	costDecisionsExecuteCmd.Flags().Int("min-unattached-days", 0, "The minimum age, in days, of what a waste cleanup deletes (0-3650; the platform raises anything under 30)")
	costDecisionsExecuteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(costDecisionsListCmd, costDecisionsGetCmd, costDecisionsActivityCmd, costDecisionsApproveCmd,
		costDecisionsSetAsideCmd, costDecisionsExecuteCmd)
	costDecisionsCmd.AddCommand(costDecisionsListCmd)
	costDecisionsCmd.AddCommand(costDecisionsGetCmd)
	costDecisionsCmd.AddCommand(costDecisionsActivityCmd)
	costDecisionsCmd.AddCommand(costDecisionsApproveCmd)
	costDecisionsCmd.AddCommand(costDecisionsSetAsideCmd)
	costDecisionsCmd.AddCommand(costDecisionsExecuteCmd)
	costCmd.AddCommand(costDecisionsCmd)
}

// costDecisionsError maps what a ledger route answers into what the user
// reads. A 404 or 405 is the route missing on a platform that predates the
// ledger, except an item route's own 404, which says the proposal is not the
// organisation's (exit 3). A refusal for the area's permission arrives in the
// RBAC shape and keeps its exit 7 and the permission it names.
func costDecisionsError(routeError error, operation string, route string, itemRoute bool) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(routeError, &unexpected) &&
		(unexpected.StatusCode == http.StatusNotFound || unexpected.StatusCode == http.StatusMethodNotAllowed) {
		if itemRoute && unexpected.StatusCode == http.StatusNotFound && strings.Contains(strings.ToLower(unexpected.Detail), "proposal") {
			return fmt.Errorf("%s: %w", operation, routeError)
		}
		return withExitCode(exitError, fmt.Errorf(
			"this platform does not serve the decision ledger: %s is not registered, so this platform predates it", route))
	}
	return fmt.Errorf("%s: %w", operation, routeError)
}

// costDecisionExecuteError reports why a run did not happen. Written consent
// that was not given, and a proposal that is not ready (a ladder wave still
// running or verifying), are refusals the proposal survives unchanged: they
// say so, so nobody retries them blindly.
func costDecisionExecuteError(executeError error, decisionID string) error {
	var consent *client.DecisionConsentRequiredError
	if errors.As(executeError, &consent) {
		return withExitCode(exitError, fmt.Errorf(
			"not run: %s Nothing was attempted and the proposal stays approved. Read its plan (ankra cost decisions get %s) "+
				"and, if you accept that the deletion is final, run it again with --acknowledge-no-snapshot",
			strings.TrimSpace(consent.Detail), decisionID))
	}
	var unexpected *client.UnexpectedResponseError
	if errors.As(executeError, &unexpected) && unexpected.StatusCode == http.StatusConflict && unexpected.Detail != "" {
		return withExitCode(exitError, fmt.Errorf(
			"not run: %s The proposal stays as it stands, so running it again before that changes is refused the same way",
			strings.TrimSpace(unexpected.Detail)))
	}
	return costDecisionsError(executeError, "running the decision", "POST /api/v1/org/decisions/{decision_id}/execute", true)
}

// costDecisionRequireID refuses a reference that is not a proposal id before
// it reaches the uuid-typed route, which would answer a validation list.
func costDecisionRequireID(decisionID string) error {
	if !looksLikeUUID(decisionID) {
		return withExitCode(exitUsage, fmt.Errorf("%q is not a decision id; 'ankra cost decisions list' shows each proposal's id", decisionID))
	}
	return nil
}

// costDecisionNote is --note when it was given, and nil so no note is sent
// otherwise.
func costDecisionNote(cmd *cobra.Command) *string {
	if !cmd.Flags().Changed("note") {
		return nil
	}
	note, _ := cmd.Flags().GetString("note")
	return &note
}

// costDecisionExecuteOptions reads the consent flags. Each is sent only when
// it was passed: the consent to a final deletion is never assumed.
func costDecisionExecuteOptions(cmd *cobra.Command) (client.DecisionExecuteOptions, error) {
	var options client.DecisionExecuteOptions
	flags := cmd.Flags()
	if acknowledged, _ := flags.GetBool("acknowledge-no-snapshot"); acknowledged {
		options.Consent = costDecisionsNoSnapshotConsent
	}
	if flags.Changed("min-unattached-days") {
		days, _ := flags.GetInt("min-unattached-days")
		if days < 0 || days > 3650 {
			return options, withExitCode(exitUsage, errors.New("--min-unattached-days must be between 0 and 3650"))
		}
		options.MinUnattachedDays = &days
	}
	return options, nil
}

// costDecisionsNarrow keeps the proposals of the kind and on the cluster
// asked for; an empty kind or cluster keeps every one.
func costDecisionsNarrow(listing *client.DecisionProposalList, kind string, clusterID string) *client.DecisionProposalList {
	if kind == "" && clusterID == "" {
		return listing
	}
	kept := map[string]bool{}
	narrowed := &client.DecisionProposalList{Proposals: []client.DecisionProposal{}, UnsettledProposalIDs: []string{}}
	for _, proposal := range listing.Proposals {
		if kind != "" && proposal.Kind != kind {
			continue
		}
		if clusterID != "" && (proposal.SubjectClusterID == nil || !strings.EqualFold(*proposal.SubjectClusterID, clusterID)) {
			continue
		}
		kept[proposal.ID] = true
		narrowed.Proposals = append(narrowed.Proposals, proposal)
	}
	for _, unsettled := range listing.UnsettledProposalIDs {
		if kept[unsettled] {
			narrowed.UnsettledProposalIDs = append(narrowed.UnsettledProposalIDs, unsettled)
		}
	}
	return narrowed
}

func costDecisionStatus(status string) string {
	switch status {
	case "set_aside":
		return "set aside"
	case "":
		return "—"
	default:
		return strings.ReplaceAll(status, "_", " ")
	}
}

// costDecisionListNotes is what goes under a proposal's row: a run the
// platform could not check, the autopilot's run-after time, and what a wave
// or a rollback belongs to when that is not on the page.
func costDecisionListNotes(proposal client.DecisionProposal, unsettled map[string]bool, onPage map[string]bool) []string {
	notes := []string{}
	if unsettled[proposal.ID] {
		notes = append(notes, "Could not be checked against its platform operation on this read; it may already have finished.")
	}
	if proposal.Status == "approved" && proposal.RunAfter != nil && *proposal.RunAfter != "" {
		notes = append(notes, "The cost autopilot runs it after "+*proposal.RunAfter+" unless it is held.")
	}
	if proposal.ParentID != nil && *proposal.ParentID != "" && !onPage[*proposal.ParentID] {
		notes = append(notes, "A wave of ladder "+*proposal.ParentID+".")
	}
	if proposal.RollbackOf != nil && *proposal.RollbackOf != "" {
		notes = append(notes, "Rolls back "+*proposal.RollbackOf+".")
	}
	return notes
}

// costDecisionsOrdered puts each wave directly under its ladder when the
// ladder is on the page, keeping the platform's order otherwise. The bool
// says whether the proposal is shown as a wave under its ladder.
func costDecisionsOrdered(proposals []client.DecisionProposal) ([]client.DecisionProposal, []bool) {
	onPage := map[string]bool{}
	for _, proposal := range proposals {
		onPage[proposal.ID] = true
	}
	children := map[string][]client.DecisionProposal{}
	for _, proposal := range proposals {
		if proposal.ParentID != nil && onPage[*proposal.ParentID] {
			children[*proposal.ParentID] = append(children[*proposal.ParentID], proposal)
		}
	}
	ordered := make([]client.DecisionProposal, 0, len(proposals))
	isWave := make([]bool, 0, len(proposals))
	for _, proposal := range proposals {
		if proposal.ParentID != nil && onPage[*proposal.ParentID] {
			continue
		}
		ordered = append(ordered, proposal)
		isWave = append(isWave, false)
		for _, child := range children[proposal.ID] {
			ordered = append(ordered, child)
			isWave = append(isWave, true)
		}
	}
	return ordered, isWave
}

func renderCostDecisions(out io.Writer, listing *client.DecisionProposalList, pageFull bool, limit int) {
	if len(listing.Proposals) == 0 {
		_, _ = fmt.Fprintln(out, "No cost decision matches.")
		return
	}
	counts := map[string]int{}
	for _, proposal := range listing.Proposals {
		counts[costDecisionStatus(proposal.Status)]++
	}
	statuses := make([]string, 0, len(counts))
	for status := range counts {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
	}
	_, _ = fmt.Fprintf(out, "Cost decisions: %s · %s\n", pluralCount(len(listing.Proposals), "proposal"), strings.Join(parts, " · "))

	unsettled := map[string]bool{}
	for _, id := range listing.UnsettledProposalIDs {
		unsettled[id] = true
	}
	onPage := map[string]bool{}
	for _, proposal := range listing.Proposals {
		onPage[proposal.ID] = true
	}
	ordered, isWave := costDecisionsOrdered(listing.Proposals)
	renderCostDecisionTable(out, ordered, isWave, func(proposal client.DecisionProposal) []string {
		return costDecisionListNotes(proposal, unsettled, onPage)
	})
	if pageFull {
		_, _ = fmt.Fprintf(out, "(the platform returned its newest %d; older proposals may exist, pass --limit up to %d)\n", limit, costDecisionsMaxLimit)
	}
}

func renderCostDecisionTable(out io.Writer, proposals []client.DecisionProposal, isWave []bool, notesFor func(client.DecisionProposal) []string) {
	header := table.Row{"Decision", "Status", "Summary"}
	rows := make([]table.Row, 0, len(proposals))
	for index, proposal := range proposals {
		identity := proposal.ID + "\n" + proposal.Kind
		if isWave[index] {
			identity = "└ " + proposal.ID + "\n  " + proposal.Kind + " (wave)"
		}
		rows = append(rows, table.Row{identity, costDecisionStatus(proposal.Status), text.WrapSoft(proposal.Summary, costDecisionsSummaryWidthMax)})
	}
	// A note spans the whole row under the proposal it explains, wrapped to
	// the table's own width so it never widens it (see renderCloudLedger).
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
	writer.AppendHeader(header)
	for index, proposal := range proposals {
		writer.AppendRow(rows[index])
		for _, note := range notesFor(proposal) {
			wrapped := wrapCostLedgerNote(note, noteWidth)
			writer.AppendRow(table.Row{wrapped, wrapped, wrapped}, table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignLeft})
		}
	}
	writer.Render()
}

// costDecisionMeasurement words where a cost proposal's measured outcome
// stands.
func costDecisionMeasurement(status string) string {
	switch status {
	case "pending":
		return "verifying (inside the seven-day window)"
	case "measured":
		return "measured"
	case "unmeasured_coverage_moved":
		return "unmeasured: the cluster was not priced the same way on both sides"
	case "unmeasured_no_snapshots":
		return "unmeasured: no cost snapshot on one side"
	case "reverted":
		return "reverted inside the window"
	case "not_applicable", "":
		return "not measured (not run, or no cluster to measure)"
	default:
		return strings.ReplaceAll(status, "_", " ")
	}
}

// costDecisionUSD is a ledger money figure, which the platform keeps in USD
// cents; unknown is "unknown", never zero.
func costDecisionUSD(cents *int64) string {
	if cents == nil {
		return "unknown"
	}
	return formatCostCents(*cents, "usd") + "/mo"
}

func costDecisionOptional(value *string) string {
	if value == nil || *value == "" {
		return "—"
	}
	return *value
}

func costDecisionShare(share *float64) string {
	if share == nil {
		return "no data"
	}
	return fmt.Sprintf("%.0f%%", *share*100)
}

// indentedJSON renders a document for reading, each line indented; an empty
// or unencodable one is "{}".
func indentedJSON(document map[string]any, indent string) string {
	if len(document) == 0 {
		return indent + "{}"
	}
	encoded, encodeError := json.MarshalIndent(document, indent, "  ")
	if encodeError != nil {
		return indent + "{}"
	}
	return indent + string(encoded)
}

func renderCostDecision(out io.Writer, proposal *client.DecisionProposal) {
	_, _ = fmt.Fprintf(out, "Decision %s\n", proposal.ID)
	_, _ = fmt.Fprintf(out, "  %s\n", proposal.Summary)
	executable := "no, the platform has no operation for this kind yet"
	if proposal.Executable {
		executable = "yes"
	}
	_, _ = fmt.Fprintf(out, "  %s · %s · %s · executable: %s\n", proposal.Area, proposal.Kind, costDecisionStatus(proposal.Status), executable)
	proposed := "  proposed " + proposal.CreatedAt
	if proposal.CreatedBy != nil && *proposal.CreatedBy != "" {
		proposed += " by " + *proposal.CreatedBy
	}
	if proposal.Source != "" {
		proposed += " (source " + proposal.Source + ")"
	}
	_, _ = fmt.Fprintln(out, proposed)
	if proposal.DecidedBy != nil && *proposal.DecidedBy != "" {
		decided := fmt.Sprintf("  decided by %s at %s", *proposal.DecidedBy, costDecisionOptional(proposal.DecidedAt))
		if proposal.DecisionNote != nil && strings.TrimSpace(*proposal.DecisionNote) != "" {
			decided += fmt.Sprintf(": %q", strings.TrimSpace(*proposal.DecisionNote))
		}
		_, _ = fmt.Fprintln(out, decided)
	}
	if proposal.RunAfter != nil && *proposal.RunAfter != "" {
		_, _ = fmt.Fprintf(out, "  the cost autopilot may run it after %s; hold it before then to stop it\n", *proposal.RunAfter)
	}
	if proposal.SubjectClusterID != nil && *proposal.SubjectClusterID != "" {
		_, _ = fmt.Fprintf(out, "  measured on cluster %s\n", *proposal.SubjectClusterID)
	}
	if proposal.ParentID != nil && *proposal.ParentID != "" {
		_, _ = fmt.Fprintf(out, "  a wave of ladder %s\n", *proposal.ParentID)
	}
	if proposal.RollbackOf != nil && *proposal.RollbackOf != "" {
		_, _ = fmt.Fprintf(out, "  rolls back %s\n", *proposal.RollbackOf)
	}
	if proposal.OperationID != nil && *proposal.OperationID != "" {
		_, _ = fmt.Fprintf(out, "  operation %s (follow it with: ankra cluster operations list %s)\n", *proposal.OperationID, *proposal.OperationID)
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Plan:")
	_, _ = fmt.Fprintln(out, indentedJSON(proposal.Plan, "  "))

	if proposal.Area == costDecisionsArea {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Outcome (USD):")
		_, _ = fmt.Fprintf(out, "  expected %s · baseline %s · measured %s\n", costDecisionUSD(proposal.ExpectedMonthlyCents),
			costDecisionUSD(proposal.BaselineMonthlyCents), costDecisionUSD(proposal.MeasuredMonthlyCents))
		measurement := "  " + costDecisionMeasurement(proposal.MeasurementStatus)
		if proposal.VerifyUntil != nil && *proposal.VerifyUntil != "" && proposal.MeasurementStatus == "pending" {
			measurement += " until " + *proposal.VerifyUntil
		}
		if proposal.MeasuredAt != nil && *proposal.MeasuredAt != "" {
			measurement += " · settled " + *proposal.MeasuredAt
		}
		_, _ = fmt.Fprintln(out, measurement)
	}
	if proposal.VerificationStatus != "" && proposal.VerificationStatus != "not_applicable" {
		_, _ = fmt.Fprintf(out, "  usage verification: %s\n", strings.ReplaceAll(proposal.VerificationStatus, "_", " "))
		if len(proposal.VerificationDays) > 0 {
			writer := newCostTable(out)
			writer.AppendHeader(table.Row{"Day", "State", "CPU p95", "Memory p95", "Nodes reporting", "Hottest node"})
			for _, day := range proposal.VerificationDays {
				writer.AppendRow(table.Row{day.Day, day.State, costDecisionShare(day.CPUP95Share), costDecisionShare(day.MemoryP95Share),
					fmt.Sprintf("%d of %d", day.Reporting, day.Nodes), day.HottestNode})
			}
			writer.Render()
		}
	}
	renderCostDecisionReceipt(out, proposal.Receipt)
}

func renderCostDecisionReceipt(out io.Writer, receipt *client.DecisionReceipt) {
	if receipt == nil {
		return
	}
	_, _ = fmt.Fprintln(out)
	line := "Receipt:"
	if receipt.ExecutedBy != "" {
		line += " run by " + receipt.ExecutedBy
	}
	if receipt.DispatchedAt != "" {
		line += " · dispatched " + receipt.DispatchedAt
	}
	if receipt.CompletedAt != "" {
		line += " · completed " + receipt.CompletedAt
	}
	if receipt.ExecutionStatus != "" {
		line += " · execution " + receipt.ExecutionStatus
	}
	_, _ = fmt.Fprintln(out, line)
	if len(receipt.Steps) == 0 {
		return
	}
	writer := newCostTable(out)
	writer.AppendHeader(table.Row{"Step", "Outcome", "Detail"})
	for _, step := range receipt.Steps {
		writer.AppendRow(table.Row{text.WrapSoft(step.Name, 24), step.Outcome, text.WrapSoft(step.Detail, 50)})
	}
	writer.Render()
}

// renderCostDecisionWaves lists a ladder's waves: the proposals whose
// parent_id is the ladder. A listing that cannot be read is said, not
// rendered as a ladder with no waves.
func renderCostDecisionWaves(out io.Writer, ladder *client.DecisionProposal) {
	_, _ = fmt.Fprintln(out)
	listing, listError := apiClient.ListDecisions(client.DecisionListFilter{Area: ladder.Area, Limit: costDecisionsMaxLimit})
	if listError != nil {
		_, _ = fmt.Fprintf(out, "Waves: could not be read (%v).\n", listError)
		return
	}
	waves := []client.DecisionProposal{}
	for _, proposal := range listing.Proposals {
		if proposal.ParentID != nil && *proposal.ParentID == ladder.ID {
			waves = append(waves, proposal)
		}
	}
	if len(waves) == 0 {
		_, _ = fmt.Fprintln(out, "Waves: none filed yet.")
		return
	}
	_, _ = fmt.Fprintf(out, "Waves (%d):\n", len(waves))
	isWave := make([]bool, len(waves))
	unsettled := map[string]bool{}
	for _, id := range listing.UnsettledProposalIDs {
		unsettled[id] = true
	}
	renderCostDecisionTable(out, waves, isWave, func(proposal client.DecisionProposal) []string {
		return costDecisionListNotes(proposal, unsettled, map[string]bool{ladder.ID: true})
	})
}

func renderCostDecisionActivity(out io.Writer, activity *client.DecisionActivity) {
	if len(activity.Events) == 0 {
		_, _ = fmt.Fprintln(out, "No activity recorded.")
		return
	}
	header := table.Row{"When", "Event", "Actor", "Note"}
	rows := make([]table.Row, 0, len(activity.Events))
	for _, event := range activity.Events {
		note := ""
		if event.Note != nil {
			note = strings.TrimSpace(*event.Note)
		}
		rows = append(rows, table.Row{event.CreatedAt, strings.ReplaceAll(event.EventType, "_", " "),
			costDecisionOptional(event.Actor), text.WrapSoft(note, 30)})
	}
	noteWidth := len(header) - 1
	for column := range header {
		width := text.LongestLineLen(fmt.Sprint(header[column]))
		for _, row := range rows {
			width = max(width, text.LongestLineLen(fmt.Sprint(row[column])))
		}
		noteWidth += width
	}
	writer := newCostTable(out)
	writer.AppendHeader(header)
	for index, event := range activity.Events {
		writer.AppendRow(rows[index])
		if len(event.Detail) > 0 {
			if encoded, encodeError := json.Marshal(event.Detail); encodeError == nil {
				wrapped := wrapCostLedgerNote(string(encoded), noteWidth)
				writer.AppendRow(table.Row{wrapped, wrapped, wrapped, wrapped}, table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignLeft})
			}
		}
	}
	writer.Render()
}

// renderCostDecisionRun says how a run left the proposal. A failed run is an
// error (exit 1) after its receipt is shown.
func renderCostDecisionRun(out io.Writer, proposal *client.DecisionProposal) error {
	switch proposal.Status {
	case "running":
		line := fmt.Sprintf("Decision %s is running", proposal.ID)
		if proposal.OperationID != nil && *proposal.OperationID != "" {
			line += fmt.Sprintf(" as operation %s. Follow it with: ankra cluster operations list %s", *proposal.OperationID, *proposal.OperationID)
		}
		_, _ = fmt.Fprintln(out, line+".")
		_, _ = fmt.Fprintf(out, "Read where it stands with: ankra cost decisions get %s\n", proposal.ID)
		renderCostDecisionReceipt(out, proposal.Receipt)
		return nil
	case "succeeded":
		_, _ = fmt.Fprintf(out, "Decision %s succeeded.\n", proposal.ID)
		renderCostDecisionReceipt(out, proposal.Receipt)
		return nil
	case "failed":
		_, _ = fmt.Fprintf(out, "Decision %s failed.\n", proposal.ID)
		renderCostDecisionReceipt(out, proposal.Receipt)
		return withExitCode(exitError, fmt.Errorf("decision %s failed; its receipt says why", proposal.ID))
	default:
		_, _ = fmt.Fprintf(out, "Decision %s is %s.\n", proposal.ID, costDecisionStatus(proposal.Status))
		renderCostDecisionReceipt(out, proposal.Receipt)
		return nil
	}
}

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// costBudgetsBudgetWidthMax is the widest the budget cell (name over scope)
// grows before it wraps, so the table fits a 100-column terminal.
const costBudgetsBudgetWidthMax = 26

// costBudgetPermission is the permission every budget write is gated on.
const costBudgetPermission = "billing.manage"

var costBudgetsCmd = &cobra.Command{
	Use:   "budgets",
	Short: "Monthly cost budgets: each budget's month so far and where it lands, and adding, changing or removing one",
	Long: `Monthly cost budgets for the organisation, one cluster, one environment label
or one application - the budgets the portal shows under Cost.

Each budget carries its month as of the read: spent so far, the projected
month end at the current pace, the hour it crosses the budget, and where the
month lands once the changes already approved or running have landed. A
budget whose scope has no priced cluster reads "unknown", never zero, and a
budget whose scope is only partly priced says how much of it is in the
figures.

Reading budgets is open to every member. Adding, changing and removing one
needs the billing.manage permission.`,
}

var costBudgetsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the budgets with their month: spent, projected month end, status and crossing hour",
	Args:  cobra.NoArgs,
	Example: `  ankra cost budgets list
  ankra cost budgets list -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		budgets, err := apiClient.ListCostBudgets()
		if err != nil {
			return costBudgetsError(err, "reading the cost budgets", http.MethodGet, false)
		}
		if rendered, err := renderStructured(cmd, budgets); rendered || err != nil {
			return err
		}
		renderCostBudgets(cmd.OutOrStdout(), budgets)
		return nil
	},
}

var costBudgetsSetCmd = &cobra.Command{
	Use:   "set [budget-id]",
	Short: "Add a budget, or change the fields you pass on an existing one",
	Long: `Without a budget id, add a monthly budget for one scope: --scope, --name,
--amount and --currency are required, and --notify-at and --owner are
optional (the platform notifies at 80% of the budget unless told otherwise).
A scope holds at most one budget.

With a budget id ('ankra cost budgets list' shows them), change that budget.
Only the flags you pass are sent; every other field keeps its value.
--clear-owner removes the owner. A budget's scope never changes: delete it and
add one on the other scope instead.

--amount is the budget per month in the currency's major unit (1500 or
1500.50). --scope-id names the cluster (name or id), the application (name or
id) or the environment label; an organisation budget takes none.`,
	Args: cobra.MaximumNArgs(1),
	Example: `  ankra cost budgets set --scope organisation --name "Fleet" --amount 20000 --currency eur
  ankra cost budgets set --scope cluster --scope-id prod-eu --name "prod-eu" --amount 1500 --currency eur --notify-at 90
  ankra cost budgets set --scope environment --scope-id production --name "Production" --amount 8000 --currency usd
  ankra cost budgets set 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e --amount 1800
  ankra cost budgets set 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e --clear-owner`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return runCostBudgetUpdate(cmd, strings.TrimSpace(args[0]))
		}
		return runCostBudgetCreate(cmd)
	},
}

var costBudgetsDeleteCmd = &cobra.Command{
	Use:   "delete <budget-id>",
	Short: "Remove a budget (its card is no longer raised)",
	Args:  cobra.ExactArgs(1),
	Example: `  ankra cost budgets delete 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e
  ankra cost budgets delete 0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e --yes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		budgetID := strings.TrimSpace(args[0])
		if err := costBudgetRequireID(budgetID); err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete cost budget %s? Its card will no longer be raised. [y/N]: ", budgetID), yes); confirmError != nil {
			return confirmError
		}
		if err := apiClient.DeleteCostBudget(budgetID); err != nil {
			return costBudgetsError(err, "deleting the budget", http.MethodDelete, true)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Budget %s deleted.\n", budgetID)
		return nil
	},
}

func init() {
	costBudgetsSetCmd.Flags().String("scope", "", "What the new budget covers: organisation, cluster, environment or application")
	costBudgetsSetCmd.Flags().String("scope-id", "", "The cluster (name or id), the application (name or id) or the environment label the new budget covers")
	costBudgetsSetCmd.Flags().String("name", "", "The budget's name (1 to 120 characters)")
	costBudgetsSetCmd.Flags().String("amount", "", "The budget per month in the currency's major unit, e.g. 1500 or 1500.50")
	costBudgetsSetCmd.Flags().String("currency", "", "The budget's currency, e.g. usd, eur or gbp")
	costBudgetsSetCmd.Flags().Int("notify-at", 0, "The share of the budget, in percent (1-100), the projection must reach to raise the budget's card")
	costBudgetsSetCmd.Flags().String("owner", "", "The user id of the member the budget is for")
	costBudgetsSetCmd.Flags().Bool("clear-owner", false, "Remove the budget's owner (changing a budget only)")
	costBudgetsSetCmd.MarkFlagsMutuallyExclusive("owner", "clear-owner")
	costBudgetsDeleteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(costBudgetsListCmd, costBudgetsSetCmd)
	costBudgetsCmd.AddCommand(costBudgetsListCmd)
	costBudgetsCmd.AddCommand(costBudgetsSetCmd)
	costBudgetsCmd.AddCommand(costBudgetsDeleteCmd)
	costCmd.AddCommand(costBudgetsCmd)
}

// costBudgetsError maps what a budget route answers into what the user reads.
// A 404 or 405 on the collection is the route missing on a platform that
// predates budgets, as is a 405 on an item route. An item route's 404 whose
// detail names the budget ("Budget not found") is the budget that is not the
// organisation's (exit 3); any other item 404 could be either, so it says so
// rather than pick one. The budget writes' admin gate answers a bare-detail
// 403; it is a role problem (exit 7), and it names the permission it wants.
func costBudgetsError(routeError error, operation string, method string, itemRoute bool) error {
	var denied *client.PermissionDeniedError
	if errors.As(routeError, &denied) {
		return fmt.Errorf("%s: %w", operation, routeError)
	}
	var unexpected *client.UnexpectedResponseError
	if !errors.As(routeError, &unexpected) {
		return fmt.Errorf("%s: %w", operation, routeError)
	}
	switch unexpected.StatusCode {
	case http.StatusForbidden:
		if method == http.MethodGet {
			break
		}
		refusal := &client.PermissionDeniedError{Permission: costBudgetPermission}
		if detail := strings.TrimSpace(unexpected.Detail); detail != "" {
			refusal.Detail = fmt.Sprintf("%s (changing a budget needs the %s permission)", strings.TrimSuffix(detail, "."), costBudgetPermission)
		}
		return fmt.Errorf("%s: %w", operation, refusal)
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		route := "/api/v1/org/cloud-cost/budgets"
		if itemRoute {
			route += "/{budget_id}"
		}
		if itemRoute && unexpected.StatusCode == http.StatusNotFound {
			if strings.Contains(strings.ToLower(unexpected.Detail), "budget") {
				return fmt.Errorf("%s: %w", operation, routeError)
			}
			return withExitCode(exitError, fmt.Errorf(
				"%s: %s %s answered 404 without naming the budget, so either the budget is not one of this organisation's "+
					"('ankra cost budgets list' shows them) or this platform predates budgets", operation, method, route))
		}
		return withExitCode(exitError, fmt.Errorf(
			"this platform does not serve cost budgets: %s %s is not registered, so this platform predates them", method, route))
	}
	return fmt.Errorf("%s: %w", operation, routeError)
}

// costBudgetRequireID refuses a budget reference that is not an id before it
// reaches the uuid-typed route, which would answer a validation list.
func costBudgetRequireID(budgetID string) error {
	if !looksLikeUUID(budgetID) {
		return withExitCode(exitUsage, fmt.Errorf("%q is not a budget id; 'ankra cost budgets list' shows each budget's id", budgetID))
	}
	return nil
}

// parseCostBudgetAmount turns a major-unit amount ("1500", "1500.5",
// "1500.50") into minor units exactly, without a float. The platform keeps
// every currency's amounts in hundredths.
func parseCostBudgetAmount(raw string) (int64, error) {
	amount := strings.TrimSpace(raw)
	whole, fraction, hasFraction := strings.Cut(amount, ".")
	invalid := fmt.Errorf("--amount %q is not an amount; pass the budget per month like 1500 or 1500.50", raw)
	if whole == "" || strings.Trim(whole, "0123456789") != "" {
		return 0, withExitCode(exitUsage, invalid)
	}
	if hasFraction && (fraction == "" || len(fraction) > 2 || strings.Trim(fraction, "0123456789") != "") {
		return 0, withExitCode(exitUsage, invalid)
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	wholeUnits, wholeError := strconv.ParseInt(whole, 10, 64)
	if wholeError != nil || wholeUnits > (1<<62)/100 {
		return 0, withExitCode(exitUsage, invalid)
	}
	hundredths, _ := strconv.ParseInt(fraction, 10, 64)
	return wholeUnits*100 + hundredths, nil
}

// costBudgetFieldsFromFlags reads the fields both a create and an update can
// carry, each only when its flag was given.
func costBudgetFieldsFromFlags(cmd *cobra.Command) (client.CostBudgetWrite, error) {
	var write client.CostBudgetWrite
	flags := cmd.Flags()
	if flags.Changed("name") {
		name, _ := flags.GetString("name")
		write.Name = &name
	}
	if flags.Changed("amount") {
		raw, _ := flags.GetString("amount")
		cents, amountError := parseCostBudgetAmount(raw)
		if amountError != nil {
			return write, amountError
		}
		write.MonthlyCents = &cents
	}
	if flags.Changed("currency") {
		currency, _ := flags.GetString("currency")
		currency = strings.ToLower(strings.TrimSpace(currency))
		write.Currency = &currency
	}
	if flags.Changed("notify-at") {
		notifyAt, _ := flags.GetInt("notify-at")
		write.NotifyAtPct = &notifyAt
	}
	if flags.Changed("owner") {
		owner, _ := flags.GetString("owner")
		owner = strings.TrimSpace(owner)
		write.OwnerUserID = &owner
	}
	if flags.Changed("clear-owner") {
		write.ClearOwner, _ = flags.GetBool("clear-owner")
	}
	return write, nil
}

// costBudgetScopeID resolves --scope-id for the scope kind: a cluster or an
// application name becomes its id, an environment label passes as given. The
// application lookup runs under the command's context, so Ctrl-C or a
// deadline stops its listing requests.
func costBudgetScopeID(requestContext context.Context, scopeKind string, reference string) (string, error) {
	switch scopeKind {
	case "cluster":
		return resolveClusterID(reference)
	case "application":
		return resolveApplicationID(requestContext, apiClient, reference)
	default:
		return reference, nil
	}
}

func runCostBudgetCreate(cmd *cobra.Command) error {
	flags := cmd.Flags()
	if flags.Changed("clear-owner") {
		return withExitCode(exitUsage, errors.New("--clear-owner changes an existing budget; pass its id"))
	}
	missing := []string{}
	for _, required := range []string{"scope", "name", "amount", "currency"} {
		if !flags.Changed(required) {
			missing = append(missing, "--"+required)
		}
	}
	if len(missing) > 0 {
		return withExitCode(exitUsage, fmt.Errorf("a new budget needs %s (pass a budget id to change an existing one)", strings.Join(missing, ", ")))
	}
	write, fieldsError := costBudgetFieldsFromFlags(cmd)
	if fieldsError != nil {
		return fieldsError
	}
	scopeKind, _ := flags.GetString("scope")
	scopeKind = strings.ToLower(strings.TrimSpace(scopeKind))
	scopeReference, _ := flags.GetString("scope-id")
	scopeReference = strings.TrimSpace(scopeReference)
	switch scopeKind {
	case "organisation":
		if scopeReference != "" {
			return withExitCode(exitUsage, errors.New("an organisation budget names no --scope-id"))
		}
	case "cluster", "environment", "application":
		if scopeReference == "" {
			article := "a"
			if scopeKind != "cluster" {
				article = "an"
			}
			return withExitCode(exitUsage, fmt.Errorf("%s %s budget needs --scope-id", article, scopeKind))
		}
		scopeID, resolveError := costBudgetScopeID(cmd.Context(), scopeKind, scopeReference)
		if resolveError != nil {
			return resolveError
		}
		write.ScopeID = &scopeID
	default:
		return withExitCode(exitUsage, fmt.Errorf("--scope must be organisation, cluster, environment or application, not %q", scopeKind))
	}
	write.ScopeKind = &scopeKind
	budget, err := apiClient.CreateCostBudget(write)
	if err != nil {
		return costBudgetsError(err, "creating the budget", http.MethodPost, false)
	}
	if rendered, err := renderStructured(cmd, budget); rendered || err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Budget %s created.\n", budget.ID)
	renderCostBudgetTable(out, []client.CostBudget{*budget})
	return nil
}

func runCostBudgetUpdate(cmd *cobra.Command, budgetID string) error {
	if err := costBudgetRequireID(budgetID); err != nil {
		return err
	}
	flags := cmd.Flags()
	if flags.Changed("scope") || flags.Changed("scope-id") {
		return withExitCode(exitUsage, errors.New("a budget's scope cannot change; delete it and add one on the other scope"))
	}
	write, fieldsError := costBudgetFieldsFromFlags(cmd)
	if fieldsError != nil {
		return fieldsError
	}
	if len(write.Body()) == 0 {
		return withExitCode(exitUsage, errors.New("pass at least one of --name, --amount, --currency, --notify-at, --owner or --clear-owner"))
	}
	budget, err := apiClient.UpdateCostBudget(budgetID, write)
	if err != nil {
		return costBudgetsError(err, "changing the budget", http.MethodPut, true)
	}
	if rendered, err := renderStructured(cmd, budget); rendered || err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Budget %s updated.\n", budget.ID)
	renderCostBudgetTable(out, []client.CostBudget{*budget})
	return nil
}

// costBudgetFigure is a budget figure the platform may not know: while
// nothing in scope is priced it is unknown, never zero.
func costBudgetFigure(cents *int64, currency string) string {
	if cents == nil {
		return "unknown"
	}
	return formatCostCents(*cents, currency)
}

func costBudgetStatus(status string) string {
	switch status {
	case "under":
		return "under"
	case "approaching":
		return "approaching"
	case "crossing":
		return "crossing"
	case "over":
		return "over budget"
	case "unknown", "":
		return "unknown"
	default:
		return strings.ReplaceAll(status, "_", " ")
	}
}

// costBudgetScope names what a budget covers. A cluster or application the
// organisation no longer has has no name, so the id prefix stands in.
func costBudgetScope(budget client.CostBudget) string {
	scopeID := ""
	if budget.ScopeID != nil {
		scopeID = *budget.ScopeID
	}
	switch budget.ScopeKind {
	case "organisation":
		return "organisation"
	case "environment":
		name := budget.ScopeName
		if name == "" {
			name = scopeID
		}
		return "environment " + name
	default:
		if budget.ScopeName != "" {
			return budget.ScopeKind + " " + budget.ScopeName
		}
		return fmt.Sprintf("%s %s (gone)", budget.ScopeKind, scopeID[:min(8, len(scopeID))])
	}
}

// costBudgetNotes is what goes under a budget's row: its id and threshold,
// the crossing hour, where the running changes land it, and how much of the
// scope the figures cover.
func costBudgetNotes(budget client.CostBudget) []string {
	projection := budget.Projection
	currency := projection.Currency
	if currency == "" {
		currency = budget.Currency
	}
	identity := fmt.Sprintf("id %s · notifies at %d%%", budget.ID, budget.NotifyAtPct)
	if budget.OwnerUserID != nil && *budget.OwnerUserID != "" {
		identity += " · owner " + *budget.OwnerUserID
	}
	notes := []string{identity}
	if projection.Status == "crossing" {
		if projection.CrossingAt != nil && *projection.CrossingAt != "" {
			notes = append(notes, "Crosses the budget at "+*projection.CrossingAt+" at the current pace.")
		} else {
			notes = append(notes, "Crosses the budget this month at the current pace; the hour it crosses is unknown.")
		}
	}
	if projection.RunningSavingsMonthlyCents != nil && *projection.RunningSavingsMonthlyCents > 0 && projection.AfterRunningChangesCents != nil {
		notes = append(notes, fmt.Sprintf("Once the changes already approved or running land: %s projected (they save %s/mo).",
			formatCostCents(*projection.AfterRunningChangesCents, currency), formatCostCents(*projection.RunningSavingsMonthlyCents, currency)))
	}
	switch {
	case projection.Status == "unknown":
		notes = append(notes, "Nothing in scope is priced, so this month is unknown, not zero.")
	case !projection.Complete && projection.UnpricedClusterCount > 0:
		notes = append(notes, fmt.Sprintf("%d of %d clusters in scope are priced; the spend of the others is not in these figures.",
			projection.PricedClusterCount, projection.PricedClusterCount+projection.UnpricedClusterCount))
	}
	return notes
}

func renderCostBudgets(out io.Writer, budgets *client.CostBudgets) {
	if len(budgets.Budgets) == 0 {
		_, _ = fmt.Fprintln(out, "No cost budget yet.")
		_, _ = fmt.Fprintln(out, "Add one with: ankra cost budgets set --scope organisation --name <name> --amount <per month> --currency <currency>")
		return
	}
	_, _ = fmt.Fprintf(out, "Cost budgets for %s (UTC): %s\n", costLedgerMonth(budgets.Budgets[0].Month),
		pluralCount(len(budgets.Budgets), "budget"))
	counts := map[string]int{}
	for _, budget := range budgets.Budgets {
		counts[costBudgetStatus(budget.Projection.Status)]++
	}
	parts := []string{}
	for _, status := range []string{"over budget", "crossing", "approaching", "under", "unknown"} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
			delete(counts, status)
		}
	}
	for status, count := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", count, status))
	}
	_, _ = fmt.Fprintf(out, "  %s\n", strings.Join(parts, " · "))
	renderCostBudgetTable(out, budgets.Budgets)
}

func renderCostBudgetTable(out io.Writer, budgets []client.CostBudget) {
	header := table.Row{"Budget", "Per month", "Spent", "Projected", "Status"}
	rows := make([]table.Row, 0, len(budgets))
	for _, budget := range budgets {
		projection := budget.Projection
		currency := projection.Currency
		if currency == "" {
			currency = budget.Currency
		}
		projected := costBudgetFigure(projection.ProjectedMonthEndCents, currency)
		if projection.ProjectedMonthEndCents != nil && projection.PercentOfBudget != nil {
			projected += fmt.Sprintf(" (%d%%)", *projection.PercentOfBudget)
		}
		rows = append(rows, table.Row{
			text.WrapSoft(budget.Name, costBudgetsBudgetWidthMax) + "\n" + text.WrapSoft(costBudgetScope(budget), costBudgetsBudgetWidthMax),
			formatCostCents(budget.MonthlyCents, budget.Currency),
			costBudgetFigure(projection.MonthToDateCents, currency),
			projected,
			costBudgetStatus(projection.Status),
		})
	}
	// A note spans the whole row under the budget it explains, wrapped to the
	// table's own width so it never widens it (see renderCloudLedger). The
	// width is at least what keeps "id <36-character id>" on one line after
	// the note's two-column marker, so the id is never split.
	noteWidth := len(header) - 1
	for column := range header {
		width := text.LongestLineLen(fmt.Sprint(header[column]))
		for _, row := range rows {
			width = max(width, text.LongestLineLen(fmt.Sprint(row[column])))
		}
		noteWidth += width
	}
	noteWidth = max(noteWidth, 2+len("id ")+36)

	_, _ = fmt.Fprintln(out)
	writer := newCostTable(out)
	writer.SetColumnConfigs([]table.ColumnConfig{
		{Number: 2, Align: text.AlignRight},
		{Number: 3, Align: text.AlignRight},
		{Number: 4, Align: text.AlignRight},
	})
	writer.AppendHeader(header)
	for index, budget := range budgets {
		writer.AppendRow(rows[index])
		for _, note := range costBudgetNotes(budget) {
			wrapped := wrapCostLedgerNote(note, noteWidth)
			writer.AppendRow(table.Row{wrapped, wrapped, wrapped, wrapped, wrapped},
				table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignLeft})
		}
	}
	writer.Render()
}

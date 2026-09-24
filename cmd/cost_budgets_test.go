package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
)

type costBudgetsMock struct {
	baseMock
	budgets     *client.CostBudgets
	listError   error
	written     *client.CostBudget
	writeError  error
	creates     []client.CostBudgetWrite
	updates     []client.CostBudgetWrite
	updatedIDs  []string
	deletedIDs  []string
	deleteError error
	clusters    []client.ClusterListItem
}

func (m *costBudgetsMock) ListCostBudgets() (*client.CostBudgets, error) {
	if m.listError != nil {
		return nil, m.listError
	}
	return m.budgets, nil
}

func (m *costBudgetsMock) CreateCostBudget(write client.CostBudgetWrite) (*client.CostBudget, error) {
	m.creates = append(m.creates, write)
	if m.writeError != nil {
		return nil, m.writeError
	}
	return m.written, nil
}

func (m *costBudgetsMock) UpdateCostBudget(budgetID string, write client.CostBudgetWrite) (*client.CostBudget, error) {
	m.updatedIDs = append(m.updatedIDs, budgetID)
	m.updates = append(m.updates, write)
	if m.writeError != nil {
		return nil, m.writeError
	}
	return m.written, nil
}

func (m *costBudgetsMock) DeleteCostBudget(budgetID string) error {
	m.deletedIDs = append(m.deletedIDs, budgetID)
	return m.deleteError
}

func (m *costBudgetsMock) ListClusters(page int, pageSize int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{Result: m.clusters, Pagination: client.Pagination{TotalPages: 1, Page: 1}}, nil
}

func runCostBudgetsCommand(t *testing.T, mock APIClient, input string, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(args)
	// Reset before the run as well as after the test: a test that runs the
	// command more than once must not carry one run's flags into the next.
	resetTreeFlags(t, costBudgetsListCmd, costBudgetsSetCmd, costBudgetsDeleteCmd)
	t.Cleanup(func() { resetTreeFlags(t, costBudgetsListCmd, costBudgetsSetCmd, costBudgetsDeleteCmd) })
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

func budgetsPointer[T any](value T) *T {
	return &value
}

const (
	costBudgetProdID      = "0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e"
	costBudgetFleetID     = "1c8d5e2f-6a7b-4c9d-8e1f-2a3b4c5d6e7f"
	costBudgetStageID     = "2d9e6f3a-7b8c-4d0e-9f2a-3b4c5d6e7f80"
	costBudgetGoneID      = "3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091"
	costBudgetClusterUUID = "11111111-1111-4111-8111-111111111111"
)

// costBudgetsFixture is the budgets list as the platform sends it: a cluster
// budget crossing at a known hour with running changes, a partly priced
// organisation budget, an environment budget with nothing priced, and a
// budget on a cluster that is gone, crossing with no hour.
func costBudgetsFixture() *client.CostBudgets {
	return &client.CostBudgets{Budgets: []client.CostBudget{
		{ID: costBudgetProdID, Name: "prod-eu", ScopeKind: "cluster", ScopeID: budgetsPointer(costBudgetClusterUUID),
			ScopeName: "prod-eu", MonthlyCents: 150000, Currency: "eur", OwnerUserID: budgetsPointer("99999999-9999-4999-8999-999999999999"),
			NotifyAtPct: 80, Month: "2026-09",
			Projection: client.CostBudgetProjection{Status: "crossing", MonthToDateCents: budgetsPointer(int64(120000)),
				ProjectedMonthEndCents: budgetsPointer(int64(162000)), PercentOfBudget: budgetsPointer(108),
				CrossingAt: budgetsPointer("2026-09-27T14:00:00Z"), RunningSavingsMonthlyCents: budgetsPointer(int64(12000)),
				AfterRunningChangesCents: budgetsPointer(int64(158000)), PricedClusterCount: 1, Complete: true, Currency: "eur"}},
		{ID: costBudgetFleetID, Name: "Fleet", ScopeKind: "organisation", MonthlyCents: 2000000, Currency: "eur", NotifyAtPct: 90, Month: "2026-09",
			Projection: client.CostBudgetProjection{Status: "under", MonthToDateCents: budgetsPointer(int64(600000)),
				ProjectedMonthEndCents: budgetsPointer(int64(1100000)), PercentOfBudget: budgetsPointer(55),
				RunningSavingsMonthlyCents: budgetsPointer(int64(0)), AfterRunningChangesCents: budgetsPointer(int64(1100000)),
				PricedClusterCount: 4, UnpricedClusterCount: 1, Currency: "eur"}},
		{ID: costBudgetStageID, Name: "Staging", ScopeKind: "environment", ScopeID: budgetsPointer("staging"), ScopeName: "staging",
			MonthlyCents: 30000, Currency: "usd", NotifyAtPct: 80, Month: "2026-09",
			Projection: client.CostBudgetProjection{Status: "unknown", UnpricedClusterCount: 2, Currency: "usd"}},
		{ID: costBudgetGoneID, Name: "old-dev", ScopeKind: "cluster", ScopeID: budgetsPointer("44444444-4444-4444-8444-444444444444"),
			MonthlyCents: 20000, Currency: "eur", NotifyAtPct: 80, Month: "2026-09",
			Projection: client.CostBudgetProjection{Status: "crossing", MonthToDateCents: budgetsPointer(int64(15000)),
				ProjectedMonthEndCents: budgetsPointer(int64(21000)), PercentOfBudget: budgetsPointer(105),
				PricedClusterCount: 1, Complete: true, Currency: "eur"}},
	}}
}

// budgetRowLine returns the rendered table line whose first cell starts with
// marker.
func budgetRowLine(t *testing.T, output string, marker string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "│ "+marker) {
			return line
		}
	}
	t.Fatalf("no table row for %s:\n%s", marker, output)
	return ""
}

// flattenBudgetNotes joins the rendered lines with the table borders and
// padding stripped, so a note that wraps inside the table reads as one
// sentence.
func flattenBudgetNotes(output string) string {
	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		lines = append(lines, strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│")))
	}
	return strings.Join(lines, " ")
}

func TestCostBudgetsListRendersEveryStatusAndItsNotes(t *testing.T) {
	output, executeError := runCostBudgetsCommand(t, &costBudgetsMock{budgets: costBudgetsFixture()}, "", "cost", "budgets", "list")
	if executeError != nil {
		t.Fatalf("cost budgets list failed: %v", executeError)
	}
	flat := flattenBudgetNotes(output)
	for _, expected := range []string{
		"Cost budgets for September 2026 (UTC): 4 budgets",
		"2 crossing · 1 under · 1 unknown",
		"BUDGET", "PER MONTH", "SPENT", "PROJECTED", "STATUS",
		"↳ id " + costBudgetProdID + " · notifies at 80% · owner 99999999-9999-4999-8999-999999999999",
		"↳ Crosses the budget at 2026-09-27T14:00:00Z at the current pace.",
		"↳ Once the changes already approved or running land: €1580.00 projected (they save €120.00/mo).",
		"↳ 4 of 5 clusters in scope are priced; the spend of the others is not in these figures.",
		"↳ Nothing in scope is priced, so this month is unknown, not zero.",
		"↳ Crosses the budget this month at the current pace; the hour it crosses is unknown.",
	} {
		if !strings.Contains(flat, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	for marker, cells := range map[string][]string{
		"prod-eu ": {"€1500.00", "€1200.00", "€1620.00 (108%)", "crossing"},
		"Fleet ":   {"€20000.00", "€6000.00", "€11000.00 (55%)", "under"},
		"Staging ": {"$300.00", "unknown", "unknown"},
		"old-dev ": {"€200.00", "€210.00 (105%)", "crossing"},
	} {
		line := budgetRowLine(t, output, marker)
		for _, cell := range cells {
			if !strings.Contains(line, cell) {
				t.Fatalf("row %s lacks %q: %s", marker, cell, line)
			}
		}
	}
	for _, scope := range []string{"│ cluster prod-eu ", "│ organisation ", "│ environment staging ", "│ cluster 44444444 (gone) "} {
		if !strings.Contains(output, scope) {
			t.Fatalf("output lacks the scope %q:\n%s", scope, output)
		}
	}
	// The organisation budget saves nothing from running changes, so it
	// carries no such note; the unknown budget prints no zero anywhere.
	if strings.Count(output, "Once the changes already approved") != 1 {
		t.Fatalf("only a budget with running savings gets that note:\n%s", output)
	}
	if strings.Contains(output, "$0.00") || strings.Contains(output, "€0.00") {
		t.Fatalf("an unknown figure must never print as zero:\n%s", output)
	}
}

func TestCostBudgetsListFitsA100ColumnTerminal(t *testing.T) {
	output, executeError := runCostBudgetsCommand(t, &costBudgetsMock{budgets: costBudgetsFixture()}, "", "cost", "budgets", "list")
	if executeError != nil {
		t.Fatalf("cost budgets list failed: %v", executeError)
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
}

func TestCostBudgetsListWithNoBudgetSaysSo(t *testing.T) {
	output, executeError := runCostBudgetsCommand(t, &costBudgetsMock{budgets: &client.CostBudgets{Budgets: []client.CostBudget{}}},
		"", "cost", "budgets", "list")
	if executeError != nil {
		t.Fatalf("cost budgets list failed: %v", executeError)
	}
	if !strings.HasPrefix(output, "No cost budget yet.\n") || strings.Contains(output, "╭") {
		t.Fatalf("an empty list must lead with one sentence and no table:\n%s", output)
	}
}

func TestCostBudgetsListStructuredOutputIsTheApiDocument(t *testing.T) {
	output, executeError := runCostBudgetsCommand(t, &costBudgetsMock{budgets: costBudgetsFixture()}, "", "cost", "budgets", "list", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost budgets list -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	budgets, _ := decoded["budgets"].([]any)
	if len(budgets) != 4 {
		t.Fatalf("budgets missing: %+v", decoded)
	}
	organisation := budgets[1].(map[string]any)
	if value, present := organisation["scope_id"]; !present || value != nil {
		t.Fatalf("an organisation budget keeps a null scope_id: %+v", organisation)
	}
	unknown := budgets[2].(map[string]any)["projection"].(map[string]any)
	for _, field := range []string{"month_to_date_cents", "projected_month_end_cents", "percent_of_budget", "crossing_at",
		"running_savings_monthly_cents", "after_running_changes_cents"} {
		if value, present := unknown[field]; !present || value != nil {
			t.Fatalf("an unknown month keeps %s null on the wire, not absent or 0: %+v", field, unknown)
		}
	}
	if budgets[0].(map[string]any)["projection"].(map[string]any)["crossing_at"] != "2026-09-27T14:00:00Z" {
		t.Fatalf("the crossing hour must pass through: %+v", budgets[0])
	}
}

// The collection is served to API tokens on every platform that has budgets,
// so a 404 or 405 on it can only mean an older platform.
func TestCostBudgetsReportAMissingRouteAsSuch(t *testing.T) {
	for _, routeError := range []error{
		client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found"),
		&client.UnexpectedResponseError{StatusCode: 404, Detail: "Not Found"},
		client.NewUnexpectedResponseError(405, "unexpected status: 405 Method Not Allowed"),
	} {
		_, listError := runCostBudgetsCommand(t, &costBudgetsMock{listError: routeError}, "", "cost", "budgets", "list")
		if listError == nil || !strings.Contains(listError.Error(), "this platform does not serve cost budgets") ||
			!strings.Contains(listError.Error(), "GET /api/v1/org/cloud-cost/budgets is not registered") {
			t.Fatalf("list error = %v", listError)
		}
		if got := exitCodeFor(listError); got != exitError {
			t.Fatalf("exit code = %d, want %d (not the not-found code)", got, exitError)
		}
		_, createError := runCostBudgetsCommand(t, &costBudgetsMock{writeError: routeError}, "", "cost", "budgets", "set",
			"--scope", "organisation", "--name", "Fleet", "--amount", "20000", "--currency", "eur")
		if createError == nil || !strings.Contains(createError.Error(), "POST /api/v1/org/cloud-cost/budgets is not registered") {
			t.Fatalf("create error = %v", createError)
		}
	}
	// On the item routes, a 404 that does not name the budget could be a
	// budget that is gone or a platform without budgets; it says both rather
	// than pick one. A 405 there is still the route missing.
	for _, itemError := range []error{
		client.NewUnexpectedResponseError(404, "request failed"),
		&client.UnexpectedResponseError{StatusCode: 404, Detail: "Not Found"},
	} {
		_, ambiguousError := runCostBudgetsCommand(t, &costBudgetsMock{writeError: itemError},
			"", "cost", "budgets", "set", costBudgetProdID, "--amount", "1800")
		if ambiguousError == nil || !strings.Contains(ambiguousError.Error(),
			"PUT /api/v1/org/cloud-cost/budgets/{budget_id} answered 404 without naming the budget, so either the budget is not one of this organisation's") ||
			!strings.Contains(ambiguousError.Error(), "or this platform predates budgets") || exitCodeFor(ambiguousError) != exitError {
			t.Fatalf("an item 404 that does not name the budget must admit both causes, got %v (exit %d)", ambiguousError, exitCodeFor(ambiguousError))
		}
	}
	_, methodError := runCostBudgetsCommand(t, &costBudgetsMock{deleteError: client.NewUnexpectedResponseError(405, "Method Not Allowed")},
		"", "cost", "budgets", "delete", costBudgetProdID, "--yes")
	if methodError == nil || !strings.Contains(methodError.Error(), "DELETE /api/v1/org/cloud-cost/budgets/{budget_id} is not registered") {
		t.Fatalf("a 405 on an item route is the route missing, got %v", methodError)
	}
	notFound := &client.UnexpectedResponseError{StatusCode: 404, Detail: "Budget not found"}
	_, missingError := runCostBudgetsCommand(t, &costBudgetsMock{deleteError: notFound}, "", "cost", "budgets", "delete", costBudgetProdID, "--yes")
	if missingError == nil || strings.Contains(missingError.Error(), "predates") || exitCodeFor(missingError) != exitNotFound {
		t.Fatalf("a budget that is not the organisation's is not found (exit 3), got %v (exit %d)", missingError, exitCodeFor(missingError))
	}
}

func TestCostBudgetsSetCreatesWithOnlyTheGivenFields(t *testing.T) {
	mock := &costBudgetsMock{written: &costBudgetsFixture().Budgets[0],
		clusters: []client.ClusterListItem{{ID: costBudgetClusterUUID, Name: "prod-eu"}}}
	output, executeError := runCostBudgetsCommand(t, mock, "", "cost", "budgets", "set",
		"--scope", "cluster", "--scope-id", "prod-eu", "--name", "prod-eu", "--amount", "1500.5", "--currency", "EUR")
	if executeError != nil {
		t.Fatalf("cost budgets set failed: %v", executeError)
	}
	if len(mock.creates) != 1 || len(mock.updates) != 0 {
		t.Fatalf("a set without an id creates, got creates=%d updates=%d", len(mock.creates), len(mock.updates))
	}
	body := mock.creates[0].Body()
	want := map[string]any{"scope_kind": "cluster", "scope_id": costBudgetClusterUUID, "name": "prod-eu",
		"monthly_cents": int64(150050), "currency": "eur"}
	if len(body) != len(want) {
		t.Fatalf("body = %v, want exactly %v", body, want)
	}
	for field, value := range want {
		if body[field] != value {
			t.Fatalf("body[%s] = %v, want %v (body %v)", field, body[field], value, body)
		}
	}
	if !strings.Contains(output, "Budget "+costBudgetProdID+" created.") || !strings.Contains(output, "€1620.00 (108%)") {
		t.Fatalf("a created budget is shown with its month:\n%s", output)
	}
}

func TestCostBudgetsSetCreateRefusesAnIncompleteBudget(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{[]string{"--name", "Fleet"}, "a new budget needs --scope, --amount, --currency"},
		{[]string{"--scope", "organisation", "--scope-id", "x", "--name", "F", "--amount", "1", "--currency", "eur"}, "an organisation budget names no --scope-id"},
		{[]string{"--scope", "environment", "--name", "F", "--amount", "1", "--currency", "eur"}, "an environment budget needs --scope-id"},
		{[]string{"--scope", "team", "--name", "F", "--amount", "1", "--currency", "eur"}, "--scope must be organisation, cluster, environment or application"},
		{[]string{"--scope", "organisation", "--name", "F", "--amount", "12.345", "--currency", "eur"}, `--amount "12.345" is not an amount`},
		{[]string{"--scope", "organisation", "--name", "F", "--amount", "-5", "--currency", "eur"}, `--amount "-5" is not an amount`},
		{[]string{"--clear-owner"}, "--clear-owner changes an existing budget"},
	} {
		mock := &costBudgetsMock{}
		_, executeError := runCostBudgetsCommand(t, mock, "", append([]string{"cost", "budgets", "set"}, testCase.args...)...)
		if executeError == nil || !strings.Contains(executeError.Error(), testCase.want) || exitCodeFor(executeError) != exitUsage {
			t.Fatalf("%v: error = %v (exit %d), want usage error %q", testCase.args, executeError, exitCodeFor(executeError), testCase.want)
		}
		if len(mock.creates) != 0 {
			t.Fatalf("%v: a refused budget must not be sent", testCase.args)
		}
	}
}

// Omitted is not cleared: a change sends the flags given and nothing else,
// so the budget keeps every other field.
func TestCostBudgetsSetChangesOnlyTheGivenFields(t *testing.T) {
	mock := &costBudgetsMock{written: &costBudgetsFixture().Budgets[0]}
	output, executeError := runCostBudgetsCommand(t, mock, "", "cost", "budgets", "set", costBudgetProdID, "--amount", "1800")
	if executeError != nil {
		t.Fatalf("cost budgets set <id> failed: %v", executeError)
	}
	if len(mock.updates) != 1 || mock.updatedIDs[0] != costBudgetProdID || len(mock.creates) != 0 {
		t.Fatalf("a set with an id updates that budget, got %v", mock.updatedIDs)
	}
	if body := mock.updates[0].Body(); len(body) != 1 || body["monthly_cents"] != int64(180000) {
		t.Fatalf("only the amount was given, so only monthly_cents is sent: %v", body)
	}
	if !strings.Contains(output, "Budget "+costBudgetProdID+" updated.") {
		t.Fatalf("the change is confirmed:\n%s", output)
	}

	mock = &costBudgetsMock{written: &costBudgetsFixture().Budgets[0]}
	if _, executeError = runCostBudgetsCommand(t, mock, "", "cost", "budgets", "set", costBudgetProdID, "--clear-owner"); executeError != nil {
		t.Fatalf("cost budgets set --clear-owner failed: %v", executeError)
	}
	body := mock.updates[0].Body()
	if owner, present := body["owner_user_id"]; !present || owner != nil || len(body) != 1 {
		t.Fatalf("--clear-owner sends owner_user_id null and nothing else: %v", body)
	}

	mock = &costBudgetsMock{written: &costBudgetsFixture().Budgets[0]}
	if _, executeError = runCostBudgetsCommand(t, mock, "", "cost", "budgets", "set", costBudgetProdID,
		"--notify-at", "95", "--owner", "99999999-9999-4999-8999-999999999999", "--name", "prod"); executeError != nil {
		t.Fatalf("cost budgets set failed: %v", executeError)
	}
	body = mock.updates[0].Body()
	if len(body) != 3 || body["notify_at_pct"] != 95 || body["owner_user_id"] != "99999999-9999-4999-8999-999999999999" || body["name"] != "prod" {
		t.Fatalf("each given flag is sent and no other: %v", body)
	}
}

// TestCostBudgetsSetRefusesAnEmptyOwner pins that an explicit empty --owner
// (or one that is only whitespace) is refused rather than sent as "", on
// create and on change alike: removing the owner is --clear-owner.
func TestCostBudgetsSetRefusesAnEmptyOwner(t *testing.T) {
	for _, args := range [][]string{
		{costBudgetProdID, "--owner", ""},
		{costBudgetProdID, "--owner", "   "},
		{"--name", "Prod", "--scope", "organisation", "--amount", "10", "--currency", "eur", "--owner", ""},
	} {
		mock := &costBudgetsMock{}
		_, executeError := runCostBudgetsCommand(t, mock, "", append([]string{"cost", "budgets", "set"}, args...)...)
		if executeError == nil || !strings.Contains(executeError.Error(), "pass --clear-owner") || exitCodeFor(executeError) != exitUsage {
			t.Fatalf("%v: error = %v, want the usage error pointing at --clear-owner", args, executeError)
		}
		if len(mock.updates) != 0 || len(mock.creates) != 0 {
			t.Fatalf("%v: a refused write must not be sent", args)
		}
	}
}

func TestCostBudgetsSetChangeRefusesScopeAndEmptyChanges(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{[]string{costBudgetProdID, "--scope", "organisation"}, "a budget's scope cannot change"},
		{[]string{costBudgetProdID}, "pass at least one of --name, --amount, --currency, --notify-at, --owner or --clear-owner"},
		{[]string{"prod-eu", "--amount", "10"}, `"prod-eu" is not a budget id`},
	} {
		mock := &costBudgetsMock{}
		_, executeError := runCostBudgetsCommand(t, mock, "", append([]string{"cost", "budgets", "set"}, testCase.args...)...)
		if executeError == nil || !strings.Contains(executeError.Error(), testCase.want) || exitCodeFor(executeError) != exitUsage {
			t.Fatalf("%v: error = %v, want usage error %q", testCase.args, executeError, testCase.want)
		}
		if len(mock.updates) != 0 {
			t.Fatalf("%v: a refused change must not be sent", testCase.args)
		}
	}
}

func TestCostBudgetsSetStructuredOutputIsTheBudget(t *testing.T) {
	mock := &costBudgetsMock{written: &costBudgetsFixture().Budgets[2]}
	output, executeError := runCostBudgetsCommand(t, mock, "", "cost", "budgets", "set", costBudgetStageID, "--amount", "300", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost budgets set -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	if decoded["id"] != costBudgetStageID || decoded["projection"].(map[string]any)["month_to_date_cents"] != nil {
		t.Fatalf("the budget passes through with its nulls: %+v", decoded)
	}
}

// The admin gate answers a detail sentence, not the RBAC shape; it is still a
// role refusal (exit 7) and it names the permission.
func TestCostBudgetsWriteRefusalNamesThePermission(t *testing.T) {
	refusal := &client.UnexpectedResponseError{StatusCode: 403, Detail: "Only organisation admins can change cost budgets"}
	for _, args := range [][]string{
		{"cost", "budgets", "set", "--scope", "organisation", "--name", "Fleet", "--amount", "20000", "--currency", "eur"},
		{"cost", "budgets", "set", costBudgetProdID, "--amount", "1800"},
		{"cost", "budgets", "delete", costBudgetProdID, "--yes"},
	} {
		_, executeError := runCostBudgetsCommand(t, &costBudgetsMock{writeError: refusal, deleteError: refusal}, "", args...)
		if executeError == nil || !strings.Contains(executeError.Error(), "Only organisation admins can change cost budgets") ||
			!strings.Contains(executeError.Error(), "billing.manage") {
			t.Fatalf("%v: error = %v", args, executeError)
		}
		if got := exitCodeFor(executeError); got != exitForbidden {
			t.Fatalf("%v: exit code = %d, want %d", args, got, exitForbidden)
		}
	}
	_, rbacError := runCostBudgetsCommand(t, &costBudgetsMock{writeError: &client.PermissionDeniedError{Permission: "billing.manage"}},
		"", "cost", "budgets", "set", costBudgetProdID, "--amount", "1800")
	if rbacError == nil || !strings.Contains(rbacError.Error(), `"billing.manage"`) || exitCodeFor(rbacError) != exitForbidden {
		t.Fatalf("the RBAC shape keeps its own message, got %v", rbacError)
	}
}

func TestCostBudgetsWriteRelaysTheConflictDetail(t *testing.T) {
	conflict := client.NewUnexpectedResponseError(409, "This scope already has a budget; change that one instead.")
	_, executeError := runCostBudgetsCommand(t, &costBudgetsMock{writeError: conflict}, "", "cost", "budgets", "set",
		"--scope", "organisation", "--name", "Fleet", "--amount", "20000", "--currency", "eur")
	if executeError == nil || !strings.Contains(executeError.Error(), "creating the budget: This scope already has a budget") {
		t.Fatalf("error = %v", executeError)
	}
}

func TestCostBudgetsDeleteAsksFirst(t *testing.T) {
	mock := &costBudgetsMock{}
	output, executeError := runCostBudgetsCommand(t, mock, "n\n", "cost", "budgets", "delete", costBudgetProdID)
	if executeError == nil || exitCodeFor(executeError) != exitCancelled || len(mock.deletedIDs) != 0 {
		t.Fatalf("a declined prompt deletes nothing and exits %d, got %v (deleted %v)", exitCancelled, executeError, mock.deletedIDs)
	}
	if !strings.Contains(output, "Delete cost budget "+costBudgetProdID+"? Its card will no longer be raised. [y/N]: ") {
		t.Fatalf("the prompt names the budget:\n%s", output)
	}

	mock = &costBudgetsMock{}
	output, executeError = runCostBudgetsCommand(t, mock, "y\n", "cost", "budgets", "delete", costBudgetProdID)
	if executeError != nil || len(mock.deletedIDs) != 1 || mock.deletedIDs[0] != costBudgetProdID {
		t.Fatalf("a confirmed prompt deletes the budget, got %v (deleted %v)", executeError, mock.deletedIDs)
	}
	if !strings.Contains(output, "Budget "+costBudgetProdID+" deleted.") {
		t.Fatalf("the delete is confirmed:\n%s", output)
	}

	mock = &costBudgetsMock{}
	if _, executeError = runCostBudgetsCommand(t, mock, "", "cost", "budgets", "delete", costBudgetProdID, "--yes"); executeError != nil ||
		len(mock.deletedIDs) != 1 {
		t.Fatalf("--yes skips the prompt, got %v (deleted %v)", executeError, mock.deletedIDs)
	}

	mock = &costBudgetsMock{}
	_, executeError = runCostBudgetsCommand(t, mock, "", "cost", "budgets", "delete", "prod-eu", "--yes")
	if executeError == nil || exitCodeFor(executeError) != exitUsage || len(mock.deletedIDs) != 0 {
		t.Fatalf("a name is not a budget id, got %v", executeError)
	}
}

func TestParseCostBudgetAmountIsExact(t *testing.T) {
	for raw, want := range map[string]int64{"1500": 150000, "1500.5": 150050, "1500.50": 150050, "0.07": 7, " 20 ": 2000, "0": 0} {
		got, parseError := parseCostBudgetAmount(raw)
		if parseError != nil || got != want {
			t.Errorf("parseCostBudgetAmount(%q) = %d, %v; want %d", raw, got, parseError, want)
		}
	}
	for _, raw := range []string{"", "1,500", "1500.", ".5", "1.234", "-1", "1e3", "abc", "99999999999999999999"} {
		if _, parseError := parseCostBudgetAmount(raw); parseError == nil {
			t.Errorf("parseCostBudgetAmount(%q) should fail", raw)
		}
	}
}

type budgetsContextKey struct{}

// costBudgetsApplicationMock resolves an application name through the listing
// and records the context the lookup ran under.
type costBudgetsApplicationMock struct {
	costBudgetsMock
	lookupContexts []context.Context
}

func (m *costBudgetsApplicationMock) ListApplicationsRaw(requestContext context.Context, page int, pageSize int, search string) (json.RawMessage, error) {
	m.lookupContexts = append(m.lookupContexts, requestContext)
	return json.RawMessage(`{"result":[{"id":"66666666-6666-4666-8666-666666666666","name":"checkout"}],"pagination":{"total_pages":1}}`), nil
}

// The application lookup runs under the command's own context, so Ctrl-C or a
// deadline on the command stops its listing requests.
func TestCostBudgetsSetResolvesAnApplicationUnderTheCommandContext(t *testing.T) {
	mock := &costBudgetsApplicationMock{costBudgetsMock: costBudgetsMock{written: &costBudgetsFixture().Budgets[0]}}
	withTempHome(t)
	setMockClient(t, mock)
	rootCmd.SetOut(new(bytes.Buffer))
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"cost", "budgets", "set", "--scope", "application", "--scope-id", "checkout",
		"--name", "Checkout", "--amount", "900", "--currency", "eur"})
	resetTreeFlags(t, costBudgetsListCmd, costBudgetsSetCmd, costBudgetsDeleteCmd)
	t.Cleanup(func() { resetTreeFlags(t, costBudgetsListCmd, costBudgetsSetCmd, costBudgetsDeleteCmd) })
	// Cobra hands a subcommand the root's context only while its own is
	// unset, and an earlier run in this process has set it, so the command's
	// context is set on the command itself (and put back afterwards).
	commandContext := context.WithValue(context.Background(), budgetsContextKey{}, "the command's")
	costBudgetsSetCmd.SetContext(commandContext)
	t.Cleanup(func() { costBudgetsSetCmd.SetContext(context.Background()) })
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("cost budgets set --scope application failed: %v", executeError)
	}
	if len(mock.lookupContexts) == 0 || mock.lookupContexts[0].Value(budgetsContextKey{}) != "the command's" {
		t.Fatalf("the application lookup must run under the command's context, got %v", mock.lookupContexts)
	}
	if len(mock.creates) != 1 || mock.creates[0].Body()["scope_id"] != "66666666-6666-4666-8666-666666666666" {
		t.Fatalf("the application name resolves to its id: %+v", mock.creates)
	}
}

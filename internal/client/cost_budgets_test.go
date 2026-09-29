package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// costBudgetsGolden is a GET /api/v1/org/cloud-cost/budgets body in the
// shape the cluster's openapi.json declares (BudgetsResponse): a cluster
// budget crossing with its hour, an organisation budget (null scope_id) that
// is only partly priced, and an environment budget with nothing priced, whose
// figures are all null.
const costBudgetsGolden = `{"budgets":[` +
	`{"id":"0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e","name":"prod-eu","scope_kind":"cluster",` +
	`"scope_id":"11111111-1111-4111-8111-111111111111","scope_name":"prod-eu","monthly_cents":150000,"currency":"eur",` +
	`"owner_user_id":"99999999-9999-4999-8999-999999999999","notify_at_pct":80,"created_by":"alice@example.com",` +
	`"created_at":"2026-09-01T08:00:00Z","updated_at":"2026-09-10T08:00:00Z","month":"2026-09",` +
	`"projection":{"status":"crossing","month_to_date_cents":120000,"projected_month_end_cents":162000,` +
	`"percent_of_budget":108,"crossing_at":"2026-09-27T14:00:00Z","running_savings_monthly_cents":12000,` +
	`"after_running_changes_cents":158000,"priced_cluster_count":1,"unpriced_cluster_count":0,"complete":true,"currency":"eur"}},` +
	`{"id":"1c8d5e2f-6a7b-4c9d-8e1f-2a3b4c5d6e7f","name":"Fleet","scope_kind":"organisation","scope_id":null,` +
	`"scope_name":"","monthly_cents":2000000,"currency":"eur","owner_user_id":null,"notify_at_pct":90,` +
	`"created_by":"alice@example.com","created_at":"2026-09-01T08:00:00Z","updated_at":"2026-09-01T08:00:00Z","month":"2026-09",` +
	`"projection":{"status":"under","month_to_date_cents":600000,"projected_month_end_cents":1100000,"percent_of_budget":55,` +
	`"crossing_at":null,"running_savings_monthly_cents":0,"after_running_changes_cents":1100000,` +
	`"priced_cluster_count":4,"unpriced_cluster_count":1,"complete":false,"currency":"eur"}},` +
	`{"id":"2d9e6f3a-7b8c-4d0e-9f2a-3b4c5d6e7f80","name":"Staging","scope_kind":"environment","scope_id":"staging",` +
	`"scope_name":"staging","monthly_cents":30000,"currency":"usd","owner_user_id":null,"notify_at_pct":80,` +
	`"created_by":"bob@example.com","created_at":"2026-09-02T08:00:00Z","updated_at":"2026-09-02T08:00:00Z","month":"2026-09",` +
	`"projection":{"status":"unknown","month_to_date_cents":null,"projected_month_end_cents":null,"percent_of_budget":null,` +
	`"crossing_at":null,"running_savings_monthly_cents":null,"after_running_changes_cents":null,` +
	`"priced_cluster_count":0,"unpriced_cluster_count":2,"complete":false,"currency":"usd"}}]}`

func TestListCostBudgets_DecodesProjectionsAndNulls(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/cloud-cost/budgets" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(costBudgetsGolden))
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.ListCostBudgets()
	if err != nil {
		t.Fatalf("ListCostBudgets: %v", err)
	}
	if len(result.Budgets) != 3 {
		t.Fatalf("budgets did not decode: %+v", result)
	}
	crossing := result.Budgets[0]
	if crossing.ScopeKind != "cluster" || crossing.ScopeID == nil || *crossing.ScopeID != "11111111-1111-4111-8111-111111111111" ||
		crossing.MonthlyCents != 150000 || crossing.OwnerUserID == nil || crossing.NotifyAtPct != 80 || crossing.Month != "2026-09" {
		t.Fatalf("cluster budget did not decode: %+v", crossing)
	}
	projection := crossing.Projection
	if projection.Status != "crossing" || projection.CrossingAt == nil || *projection.CrossingAt != "2026-09-27T14:00:00Z" ||
		projection.PercentOfBudget == nil || *projection.PercentOfBudget != 108 || projection.AfterRunningChangesCents == nil ||
		*projection.AfterRunningChangesCents != 158000 || !projection.Complete {
		t.Fatalf("crossing projection did not decode: %+v", projection)
	}
	organisation := result.Budgets[1]
	if organisation.ScopeID != nil || organisation.OwnerUserID != nil || organisation.Projection.CrossingAt != nil ||
		organisation.Projection.Complete || organisation.Projection.UnpricedClusterCount != 1 {
		t.Fatalf("organisation budget did not decode: %+v", organisation)
	}
	unknown := result.Budgets[2].Projection
	if unknown.Status != "unknown" || unknown.MonthToDateCents != nil || unknown.ProjectedMonthEndCents != nil ||
		unknown.PercentOfBudget != nil || unknown.RunningSavingsMonthlyCents != nil || unknown.AfterRunningChangesCents != nil {
		t.Fatalf("an unknown month must decode as nil figures, not zero: %+v", unknown)
	}
}

func TestListCostBudgets_AbsentListDecodesAsEmpty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{})
	}
	result, err := newTestClient(t, handler).ListCostBudgets()
	if err != nil {
		t.Fatalf("ListCostBudgets: %v", err)
	}
	if result.Budgets == nil {
		t.Fatalf("an absent budgets list must decode as empty, not nil")
	}
}

func budgetString(value string) *string { return &value }

func budgetCents(value int64) *int64 { return &value }

func budgetInt(value int) *int { return &value }

// captureBudgetWrite serves one write and records its method, path and raw
// body.
func captureBudgetWrite(t *testing.T, status int, response string) (*Client, *struct {
	method, path, csrf string
	body               map[string]json.RawMessage
}) {
	t.Helper()
	captured := &struct {
		method, path, csrf string
		body               map[string]json.RawMessage
	}{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.csrf = r.Header.Get("X-Ankra-CSRF")
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &captured.body); decodeError != nil {
				t.Fatalf("body is not a JSON object: %v: %s", decodeError, raw)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}
	return newTestClient(t, handler), captured
}

const createdBudgetBody = `{"id":"3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091","name":"prod-eu","scope_kind":"cluster",` +
	`"scope_id":"11111111-1111-4111-8111-111111111111","scope_name":"prod-eu","monthly_cents":150000,"currency":"eur",` +
	`"owner_user_id":null,"notify_at_pct":80,"created_by":"alice@example.com","created_at":"2026-09-24T08:00:00Z",` +
	`"updated_at":"2026-09-24T08:00:00Z","month":"2026-09","projection":{"status":"under","month_to_date_cents":20000,` +
	`"projected_month_end_cents":90000,"percent_of_budget":60,"crossing_at":null,"running_savings_monthly_cents":0,` +
	`"after_running_changes_cents":90000,"priced_cluster_count":1,"unpriced_cluster_count":0,"complete":true,"currency":"eur"}}`

func TestCreateCostBudget_SendsOnlyTheGivenFields(t *testing.T) {
	testClient, captured := captureBudgetWrite(t, http.StatusCreated, createdBudgetBody)
	budget, err := testClient.CreateCostBudget(CostBudgetWrite{
		Name: budgetString("prod-eu"), ScopeKind: budgetString("cluster"), ScopeID: budgetString("11111111-1111-4111-8111-111111111111"),
		MonthlyCents: budgetCents(150000), Currency: budgetString("eur"),
	})
	if err != nil {
		t.Fatalf("CreateCostBudget: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/v1/org/cloud-cost/budgets" {
		t.Fatalf("request = %s %s", captured.method, captured.path)
	}
	if captured.csrf != "" {
		t.Fatalf("a bearer write must not send a CSRF header, got %q", captured.csrf)
	}
	if len(captured.body) != 5 || string(captured.body["monthly_cents"]) != "150000" || string(captured.body["scope_kind"]) != `"cluster"` {
		t.Fatalf("body = %v", captured.body)
	}
	for _, omitted := range []string{"notify_at_pct", "owner_user_id"} {
		if _, present := captured.body[omitted]; present {
			t.Fatalf("%s was not given, so it must not be sent: %v", omitted, captured.body)
		}
	}
	if budget.ID != "3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091" || budget.Projection.Status != "under" {
		t.Fatalf("created budget did not decode: %+v", budget)
	}
}

func TestUpdateCostBudget_OmittedIsNotCleared(t *testing.T) {
	testClient, captured := captureBudgetWrite(t, http.StatusOK, createdBudgetBody)
	if _, err := testClient.UpdateCostBudget("3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091", CostBudgetWrite{MonthlyCents: budgetCents(180000)}); err != nil {
		t.Fatalf("UpdateCostBudget: %v", err)
	}
	if captured.method != http.MethodPut || captured.path != "/api/v1/org/cloud-cost/budgets/3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091" {
		t.Fatalf("request = %s %s", captured.method, captured.path)
	}
	if len(captured.body) != 1 || string(captured.body["monthly_cents"]) != "180000" {
		t.Fatalf("an update sends only the given field, got %v", captured.body)
	}

	testClient, captured = captureBudgetWrite(t, http.StatusOK, createdBudgetBody)
	if _, err := testClient.UpdateCostBudget("3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091", CostBudgetWrite{ClearOwner: true, NotifyAtPct: budgetInt(95)}); err != nil {
		t.Fatalf("UpdateCostBudget: %v", err)
	}
	owner, present := captured.body["owner_user_id"]
	if !present || string(owner) != "null" || string(captured.body["notify_at_pct"]) != "95" || len(captured.body) != 2 {
		t.Fatalf("clearing the owner sends owner_user_id null and nothing else unasked, got %v", captured.body)
	}

	testClient, captured = captureBudgetWrite(t, http.StatusOK, createdBudgetBody)
	if _, err := testClient.UpdateCostBudget("3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091",
		CostBudgetWrite{OwnerUserID: budgetString("99999999-9999-4999-8999-999999999999")}); err != nil {
		t.Fatalf("UpdateCostBudget: %v", err)
	}
	if string(captured.body["owner_user_id"]) != `"99999999-9999-4999-8999-999999999999"` || len(captured.body) != 1 {
		t.Fatalf("setting the owner sends the id, got %v", captured.body)
	}
}

func TestDeleteCostBudget_SendsDeleteAndAcceptsNoContent(t *testing.T) {
	testClient, captured := captureBudgetWrite(t, http.StatusNoContent, "")
	if err := testClient.DeleteCostBudget("3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091"); err != nil {
		t.Fatalf("DeleteCostBudget: %v", err)
	}
	if captured.method != http.MethodDelete || captured.path != "/api/v1/org/cloud-cost/budgets/3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091" {
		t.Fatalf("request = %s %s", captured.method, captured.path)
	}
}

// The admin gate, the item routes' not-found and the scope conflict all
// answer a detail sentence; each reaches the caller with its status.
func TestCostBudgets_SurfaceTheBackendDetailAndStatus(t *testing.T) {
	for _, testCase := range []struct {
		status int
		detail string
		call   func(*Client) error
	}{
		{http.StatusForbidden, "Only organisation admins can change cost budgets", func(c *Client) error {
			_, err := c.CreateCostBudget(CostBudgetWrite{Name: budgetString("x")})
			return err
		}},
		{http.StatusNotFound, "Budget not found", func(c *Client) error {
			_, err := c.UpdateCostBudget("3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091", CostBudgetWrite{Name: budgetString("x")})
			return err
		}},
		{http.StatusConflict, "This scope already has a budget; change that one instead.", func(c *Client) error {
			_, err := c.CreateCostBudget(CostBudgetWrite{Name: budgetString("x")})
			return err
		}},
		{http.StatusBadRequest, "invalid budget: notify_at_pct must be between 1 and 100", func(c *Client) error {
			return c.DeleteCostBudget("3e0f7a4b-8c9d-4e1f-8a3b-4c5d6e7f8091")
		}},
	} {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(t, w, testCase.status, map[string]string{"detail": testCase.detail})
		}
		callError := testCase.call(newTestClient(t, handler))
		var unexpected *UnexpectedResponseError
		if !errors.As(callError, &unexpected) || unexpected.StatusCode != testCase.status || unexpected.Detail != testCase.detail ||
			!strings.Contains(callError.Error(), testCase.detail) {
			t.Fatalf("status %d: error = %v", testCase.status, callError)
		}
	}
}

// A platform that predates budgets answers the collection with a bare 404.
func TestListCostBudgets_SurfacesTheStatusCode(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	_, readError := newTestClient(t, handler).ListCostBudgets()
	var unexpected *UnexpectedResponseError
	if !errors.As(readError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "" {
		t.Fatalf("error = %v", readError)
	}
}

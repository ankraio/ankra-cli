package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
)

// CostBudgetProjection is a budget's month as of the read, in the budget's
// currency. Status is under, approaching, crossing (projected past the budget
// this month), over (already past it) or unknown (nothing in scope priced).
// Every figure is nil while nothing in scope is priced: an unread month is not
// a month with no spend. CrossingAt is set only while crossing.
type CostBudgetProjection struct {
	Status                     string  `json:"status" yaml:"status"`
	MonthToDateCents           *int64  `json:"month_to_date_cents" yaml:"month_to_date_cents"`
	ProjectedMonthEndCents     *int64  `json:"projected_month_end_cents" yaml:"projected_month_end_cents"`
	PercentOfBudget            *int    `json:"percent_of_budget" yaml:"percent_of_budget"`
	CrossingAt                 *string `json:"crossing_at" yaml:"crossing_at"`
	RunningSavingsMonthlyCents *int64  `json:"running_savings_monthly_cents" yaml:"running_savings_monthly_cents"`
	AfterRunningChangesCents   *int64  `json:"after_running_changes_cents" yaml:"after_running_changes_cents"`
	PricedClusterCount         int     `json:"priced_cluster_count" yaml:"priced_cluster_count"`
	UnpricedClusterCount       int     `json:"unpriced_cluster_count" yaml:"unpriced_cluster_count"`
	Complete                   bool    `json:"complete" yaml:"complete"`
	Currency                   string  `json:"currency" yaml:"currency"`
}

// CostBudget is a monthly cost budget for one scope (the organisation, a
// cluster, an environment label or an application) with its month as of the
// read. ScopeID is the cluster or application id or the environment label,
// nil for the organisation; ScopeName is empty for a cluster or application
// the organisation no longer has.
type CostBudget struct {
	ID           string               `json:"id" yaml:"id"`
	Name         string               `json:"name" yaml:"name"`
	ScopeKind    string               `json:"scope_kind" yaml:"scope_kind"`
	ScopeID      *string              `json:"scope_id" yaml:"scope_id"`
	ScopeName    string               `json:"scope_name" yaml:"scope_name"`
	MonthlyCents int64                `json:"monthly_cents" yaml:"monthly_cents"`
	Currency     string               `json:"currency" yaml:"currency"`
	OwnerUserID  *string              `json:"owner_user_id" yaml:"owner_user_id"`
	NotifyAtPct  int                  `json:"notify_at_pct" yaml:"notify_at_pct"`
	CreatedBy    string               `json:"created_by" yaml:"created_by"`
	CreatedAt    string               `json:"created_at" yaml:"created_at"`
	UpdatedAt    string               `json:"updated_at" yaml:"updated_at"`
	Month        string               `json:"month" yaml:"month"`
	Projection   CostBudgetProjection `json:"projection" yaml:"projection"`
}

// CostBudgets is GET /org/cloud-cost/budgets: every budget of the
// organisation, largest first.
type CostBudgets struct {
	Budgets []CostBudget `json:"budgets" yaml:"budgets"`
}

// CostBudgetWrite is the body of a budget create or update. Only the fields
// that are set are sent, so an update leaves every other field as it is. The
// owner has three states: OwnerUserID sets it, ClearOwner sends null to clear
// it, and neither leaves it alone.
type CostBudgetWrite struct {
	Name         *string
	ScopeKind    *string
	ScopeID      *string
	MonthlyCents *int64
	Currency     *string
	NotifyAtPct  *int
	OwnerUserID  *string
	ClearOwner   bool
}

// Body is the JSON object the write sends: the given fields and nothing else.
func (write CostBudgetWrite) Body() map[string]any {
	body := map[string]any{}
	if write.Name != nil {
		body["name"] = *write.Name
	}
	if write.ScopeKind != nil {
		body["scope_kind"] = *write.ScopeKind
	}
	if write.ScopeID != nil {
		body["scope_id"] = *write.ScopeID
	}
	if write.MonthlyCents != nil {
		body["monthly_cents"] = *write.MonthlyCents
	}
	if write.Currency != nil {
		body["currency"] = *write.Currency
	}
	if write.NotifyAtPct != nil {
		body["notify_at_pct"] = *write.NotifyAtPct
	}
	switch {
	case write.ClearOwner:
		body["owner_user_id"] = nil
	case write.OwnerUserID != nil:
		body["owner_user_id"] = *write.OwnerUserID
	}
	return body
}

// ListCostBudgets returns every budget of the organisation with its month.
// GET /api/v1/org/cloud-cost/budgets
func (c *Client) ListCostBudgets() (*CostBudgets, error) {
	var result CostBudgets
	if err := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/cloud-cost/budgets", nil, &result); err != nil {
		return nil, err
	}
	if result.Budgets == nil {
		result.Budgets = []CostBudget{}
	}
	return &result, nil
}

// CreateCostBudget adds a monthly budget for one scope. The bearer route
// needs billing.manage; the token stands in for the browser route's CSRF.
// POST /api/v1/org/cloud-cost/budgets
func (c *Client) CreateCostBudget(write CostBudgetWrite) (*CostBudget, error) {
	var result CostBudget
	if err := c.sendJSON(http.MethodPost, c.BaseURL+"/api/v1/org/cloud-cost/budgets", write.Body(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdateCostBudget changes the given fields of a budget; its scope never
// changes.
// PUT /api/v1/org/cloud-cost/budgets/{budget_id}
func (c *Client) UpdateCostBudget(budgetID string, write CostBudgetWrite) (*CostBudget, error) {
	url := fmt.Sprintf("%s/api/v1/org/cloud-cost/budgets/%s", c.BaseURL, neturl.PathEscape(budgetID))
	var result CostBudget
	if err := c.sendJSON(http.MethodPut, url, write.Body(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteCostBudget removes a budget.
// DELETE /api/v1/org/cloud-cost/budgets/{budget_id}
func (c *Client) DeleteCostBudget(budgetID string) error {
	url := fmt.Sprintf("%s/api/v1/org/cloud-cost/budgets/%s", c.BaseURL, neturl.PathEscape(budgetID))
	return c.sendJSON(http.MethodDelete, url, nil, nil)
}

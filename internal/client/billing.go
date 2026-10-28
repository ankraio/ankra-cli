package client

import (
	"encoding/json"
	"net/http"
)

// aiSpendCapPath is the bearer twin of the organisation's AI cap surface.
const aiSpendCapPath = "/api/v1/org/billing/ai-spend-cap"

// AISpendCap is the organisation's daily AI money and token caps with
// today's spend, as GET /api/v1/org/billing/ai-spend-cap returns it.
type AISpendCap struct {
	DailyChatUSDCap *float64 `json:"daily_chat_usd_cap"`
	PlanDefaultUSD  *float64 `json:"plan_default_usd"`
	EffectiveUSD    *float64 `json:"effective_usd"`
	SpentTodayUSD   float64  `json:"spent_today_usd"`
	// The daily token budget: past the soft cap unattended runs degrade to
	// the quick tier for the rest of the UTC day, past the hard cap new
	// unattended runs are refused until it resets.
	DailyTokenSoftCap *int64 `json:"daily_token_soft_cap"`
	DailyTokenHardCap *int64 `json:"daily_token_hard_cap"`
	// TokensToday is null when today's usage could not be read.
	TokensToday *int64 `json:"tokens_today"`
	// TokenBudgetState is unlimited, under, soft, hard or unknown.
	TokenBudgetState        string   `json:"token_budget_state"`
	MonthlyFreeUSD          *float64 `json:"monthly_free_usd"`
	MonthlyPlatformSpentUSD float64  `json:"monthly_platform_spent_usd"`
	MonthlyResetsAt         string   `json:"monthly_resets_at"`
	MonthlyExhausted        bool     `json:"monthly_exhausted"`
	AllowanceExempt         bool     `json:"allowance_exempt"`
	ByokConfigured          bool     `json:"byok_configured"`
}

// AISpendCapUpdate is a partial write of the caps: a field whose Set flag
// is false is not sent and so is kept by the platform; a Set field with a
// nil value is sent as null and clears the cap.
type AISpendCapUpdate struct {
	DailyChatUSDCapSet   bool
	DailyChatUSDCap      *float64
	DailyTokenSoftCapSet bool
	DailyTokenSoftCap    *int64
	DailyTokenHardCapSet bool
	DailyTokenHardCap    *int64
}

// MarshalJSON sends only the fields that were set.
func (update AISpendCapUpdate) MarshalJSON() ([]byte, error) {
	body := map[string]any{}
	if update.DailyChatUSDCapSet {
		body["daily_chat_usd_cap"] = update.DailyChatUSDCap
	}
	if update.DailyTokenSoftCapSet {
		body["daily_token_soft_cap"] = update.DailyTokenSoftCap
	}
	if update.DailyTokenHardCapSet {
		body["daily_token_hard_cap"] = update.DailyTokenHardCap
	}
	return json.Marshal(body)
}

// GetAISpendCap reads the organisation's daily AI caps.
func (c *Client) GetAISpendCap() (*AISpendCap, error) {
	var spendCap AISpendCap
	if getError := c.sendJSON(http.MethodGet, c.BaseURL+aiSpendCapPath, nil, &spendCap); getError != nil {
		return nil, getError
	}
	return &spendCap, nil
}

// UpdateAISpendCap applies a partial write of the organisation's daily AI
// caps and returns the resulting state.
func (c *Client) UpdateAISpendCap(update AISpendCapUpdate) (*AISpendCap, error) {
	var spendCap AISpendCap
	if putError := c.sendJSON(http.MethodPut, c.BaseURL+aiSpendCapPath, update, &spendCap); putError != nil {
		return nil, putError
	}
	return &spendCap, nil
}

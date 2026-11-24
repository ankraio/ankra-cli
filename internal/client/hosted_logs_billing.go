package client

// The organisation's paid hosted logs setting (ankra-t5jf5.34.11.20.5): GET
// and PUT /api/v1/org/billing/hosted-logs, the bearer twin of the routes the
// billing page uses (cluster ankra-t5jf5.34.11.20.3).
//
// From the organisation's priced-from month (1 January 2027 unless Ankra
// moved it), the first 5 GB of log text its clusters ship in a calendar
// month (UTC) are free. Past that, shipping pauses for the rest of the month
// unless a member holding billing.manage has turned on paid logs with a
// monthly limit: then logs cost EUR 0.25 per GB above the free 5 GB, up to
// the limit, where shipping pauses again. The GET reads the setting, the
// price and the month so far; the PUT stores the setting.

import (
	"context"
	"net/http"
)

// hostedLogsBillingPath is written out in full, so the route census check
// sees it.
const hostedLogsBillingPath = "/api/v1/org/billing/hosted-logs"

// The month states the platform answers in HostedLogsMonth.State: what the
// organisation's agents are told.
const (
	HostedLogsStateShipping     = "shipping"
	HostedLogsStatePausedFree   = "paused_free"
	HostedLogsStatePausedLimit  = "paused_limit"
	HostedLogsStatePausedCredit = "paused_credit"
)

// The pause reasons the platform answers in HostedLogsMonth.PausedReason and
// ClusterHostedLogs.PausedReason, one per paused state.
const (
	HostedLogsPausedFreeAllowanceUsed   = "free_allowance_used"
	HostedLogsPausedMonthlyLimitReached = "monthly_limit_reached"
	HostedLogsPausedNoCredit            = "no_credit"
)

// The readiness states of turning paid logs on, in
// HostedLogsBilling.PaidReadiness.
const (
	HostedLogsPaidReady   = "ready"
	HostedLogsPaidBlocked = "blocked"
	HostedLogsPaidUnknown = "unknown"
)

// HostedLogsBilling is the wire shape both the GET and the PUT answer with.
//
// MonthlyLimitCents is nil until a limit was ever set. PricedFrom is the
// first day (YYYY-MM-DD) of the first month the organisation's hosted logs
// are priced. PaidReadiness says whether paid logs can be turned on now:
// ready, blocked (PaidBlocker says why and what to do) or unknown (the
// billing account could not be checked; PaidBlocker says so). ChargeNote is
// the platform's sentence saying what turning paid logs on charges: every
// byte sent in a month above the free amount counts, including bytes sent
// before paid logs were turned on, up to the monthly limit, and nothing
// before PricedFrom.
type HostedLogsBilling struct {
	PaidEnabled       bool            `json:"paid_enabled" yaml:"paid_enabled"`
	MonthlyLimitCents *int64          `json:"monthly_limit_cents" yaml:"monthly_limit_cents"`
	UpdatedAt         *string         `json:"updated_at" yaml:"updated_at"`
	PricedFrom        string          `json:"priced_from" yaml:"priced_from"`
	FreeBytesPerMonth int64           `json:"free_bytes_per_month" yaml:"free_bytes_per_month"`
	CentsPerGB        int64           `json:"cents_per_gb" yaml:"cents_per_gb"`
	BytesPerGB        int64           `json:"bytes_per_gb" yaml:"bytes_per_gb"`
	Currency          string          `json:"currency" yaml:"currency"`
	PaidReadiness     string          `json:"paid_readiness" yaml:"paid_readiness"`
	PaidBlocker       *string         `json:"paid_blocker" yaml:"paid_blocker"`
	ChargeNote        string          `json:"charge_note" yaml:"charge_note"`
	Month             HostedLogsMonth `json:"month" yaml:"month"`
}

// HostedLogsMonth is the current calendar month (UTC) so far.
//
// FreeBytes is the part of Bytes inside the free amount, the smaller of Bytes
// and HostedLogsBilling.FreeBytesPerMonth, so FreeBytes plus BillableBytes is
// Bytes. The free amount itself is HostedLogsBilling.FreeBytesPerMonth.
//
// Priced is false before the organisation's priced-from month: hosted logs
// are then included, never paused and never charged. AmountCents is what the
// month comes to so far: 0 for a month that is not priced or in which paid
// logs were never on, otherwise the billable bytes at the price, capped at
// the highest monthly limit in effect while paid logs were on. PausedReason
// is nil while shipping is not paused.
type HostedLogsMonth struct {
	MonthStart    string  `json:"month_start" yaml:"month_start"`
	Priced        bool    `json:"priced" yaml:"priced"`
	Bytes         int64   `json:"bytes" yaml:"bytes"`
	FreeBytes     int64   `json:"free_bytes" yaml:"free_bytes"`
	BillableBytes int64   `json:"billable_bytes" yaml:"billable_bytes"`
	AmountCents   int64   `json:"amount_cents" yaml:"amount_cents"`
	State         string  `json:"state" yaml:"state"`
	PausedReason  *string `json:"paused_reason" yaml:"paused_reason"`
	ComputedAt    *string `json:"computed_at" yaml:"computed_at"`
}

// HostedLogsBillingUpdate is the PUT body. PaidEnabled is always sent: the
// route refuses a body without it (422). MonthlyLimitCents nil leaves it out,
// which keeps the stored limit; the route refuses an explicit null, so it is
// never sent as one.
type HostedLogsBillingUpdate struct {
	PaidEnabled       bool   `json:"paid_enabled"`
	MonthlyLimitCents *int64 `json:"monthly_limit_cents,omitempty"`
}

// GetHostedLogsBilling reads the organisation's paid hosted logs setting, the
// price and the month so far. It needs billing.read.
func (c *Client) GetHostedLogsBilling(ctx context.Context) (*HostedLogsBilling, error) {
	var view HostedLogsBilling
	if getError := c.sendJSONContext(ctx, http.MethodGet, c.BaseURL+hostedLogsBillingPath, nil, &view); getError != nil {
		return nil, getError
	}
	return &view, nil
}

// UpdateHostedLogsBilling stores the organisation's paid hosted logs setting
// and returns it with the month as it now stands. It needs billing.manage.
// The platform refuses turning paid logs on for an organisation whose billing
// cannot pay for them (402) and answers 503 when it could not check; each
// refusal carries the platform's sentence saying what to do.
func (c *Client) UpdateHostedLogsBilling(ctx context.Context, update HostedLogsBillingUpdate) (*HostedLogsBilling, error) {
	var view HostedLogsBilling
	if putError := c.sendJSONContext(ctx, http.MethodPut, c.BaseURL+hostedLogsBillingPath, update, &view); putError != nil {
		return nil, putError
	}
	return &view, nil
}

package client

import "net/http"

const (
	billingStandingPath = "/api/v1/org/billing/standing"
	billingWalletPath   = "/api/v1/org/billing/wallet"
)

// BillingStanding is the organisation's billing standing: whether overdue
// invoices, the prepaid wallet or the free tier restrict what it may do, and
// which actions that gates. -o json prints the platform's whole answer.
type BillingStanding struct {
	RawDocument       `json:"-" yaml:"-"`
	State             string   `json:"state" yaml:"state"`
	Reason            string   `json:"reason" yaml:"reason"`
	Message           string   `json:"message" yaml:"message"`
	Restricted        bool     `json:"restricted" yaml:"restricted"`
	Enforced          bool     `json:"enforced" yaml:"enforced"`
	OverdueInvoices   int      `json:"overdue_invoices" yaml:"overdue_invoices"`
	OldestOverdueDays int      `json:"oldest_overdue_days" yaml:"oldest_overdue_days"`
	OverdueCents      int64    `json:"overdue_cents" yaml:"overdue_cents"`
	GatedActions      []string `json:"gated_actions" yaml:"gated_actions"`
	WalletState       string   `json:"wallet_state" yaml:"wallet_state"`
	FreeTierStanding  string   `json:"free_tier_standing" yaml:"free_tier_standing"`
	CreditLimitCents  *int64   `json:"credit_limit_cents" yaml:"credit_limit_cents"`
}

// Wallet is the organisation's prepaid credit wallet. -o json prints the
// platform's whole answer, credit lots included.
type Wallet struct {
	RawDocument        `json:"-" yaml:"-"`
	Present            bool    `json:"present" yaml:"present"`
	State              string  `json:"state" yaml:"state"`
	Currency           string  `json:"currency" yaml:"currency"`
	BalanceMicros      int64   `json:"balance_micros" yaml:"balance_micros"`
	BalanceDisplay     string  `json:"balance_display" yaml:"balance_display"`
	OverdraftMicros    int64   `json:"overdraft_micros" yaml:"overdraft_micros"`
	NetPositionDisplay string  `json:"net_position_display" yaml:"net_position_display"`
	FrozenAt           *string `json:"frozen_at" yaml:"frozen_at"`
	FrozenReason       *string `json:"frozen_reason" yaml:"frozen_reason"`
	TopUpsEnabled      bool    `json:"top_ups_enabled" yaml:"top_ups_enabled"`
}

// GetBillingStanding reads the token organisation's billing standing.
func (c *Client) GetBillingStanding() (*BillingStanding, error) {
	var standing BillingStanding
	if getError := c.sendJSON(http.MethodGet, c.BaseURL+billingStandingPath, nil, &standing); getError != nil {
		return nil, getError
	}
	return &standing, nil
}

// GetWallet reads the token organisation's prepaid credit wallet.
func (c *Client) GetWallet() (*Wallet, error) {
	var wallet Wallet
	if getError := c.sendJSON(http.MethodGet, c.BaseURL+billingWalletPath, nil, &wallet); getError != nil {
		return nil, getError
	}
	return &wallet, nil
}

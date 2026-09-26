package client

import (
	"net/http"
	"net/url"
)

// CostReconciliationImport is one billing document's last import attempt.
// LinesState says whether the stored lines are the provider's final figures
// ("complete") or month to date ("partial"), and is nil when no attempt
// ever stored any; a failed attempt leaves it as it was. SucceededAt is when
// the stored lines were read.
type CostReconciliationImport struct {
	Source           string  `json:"source" yaml:"source"`
	SourceDocumentID string  `json:"source_document_id" yaml:"source_document_id"`
	State            string  `json:"state" yaml:"state"`
	LinesState       *string `json:"lines_state" yaml:"lines_state"`
	Reason           *string `json:"reason" yaml:"reason"`
	Currency         *string `json:"currency" yaml:"currency"`
	LineCount        int     `json:"line_count" yaml:"line_count"`
	AttemptedAt      string  `json:"attempted_at" yaml:"attempted_at"`
	SucceededAt      *string `json:"succeeded_at" yaml:"succeeded_at"`
}

// CostReconciliationFX is the rate the estimate (held in USD) was converted
// into the invoice's currency at: the latest stored rate, not the month's
// own. AsOf is nil when the rate is the platform's built-in one.
type CostReconciliationFX struct {
	From        string  `json:"from" yaml:"from"`
	To          string  `json:"to" yaml:"to"`
	RateFromUSD float64 `json:"rate_from_usd" yaml:"rate_from_usd"`
	AsOf        *string `json:"as_of" yaml:"as_of"`
}

// CostReconciliationCluster is one cluster the invoice's lines are placed
// on. A nil estimate is no estimate recorded for the month, not zero;
// MeteredHours below LifetimeHours makes the estimate a floor.
type CostReconciliationCluster struct {
	ClusterID     string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName   string `json:"cluster_name" yaml:"cluster_name"`
	InvoiceMinor  *int64 `json:"invoice_minor" yaml:"invoice_minor"`
	LineCount     int    `json:"line_count" yaml:"line_count"`
	EstimateMinor *int64 `json:"estimate_minor" yaml:"estimate_minor"`
	MeteredHours  *int   `json:"metered_hours" yaml:"metered_hours"`
	LifetimeHours *int   `json:"lifetime_hours" yaml:"lifetime_hours"`
	PeriodHours   int    `json:"period_hours" yaml:"period_hours"`
}

// CostReconciliationLine is one billed line nothing placed on a cluster.
type CostReconciliationLine struct {
	LineKey        string  `json:"line_key" yaml:"line_key"`
	ExternalID     *string `json:"external_id" yaml:"external_id"`
	ExternalIDKind *string `json:"external_id_kind" yaml:"external_id_kind"`
	Category       string  `json:"category" yaml:"category"`
	Description    string  `json:"description" yaml:"description"`
	AmountMinor    int64   `json:"amount_minor" yaml:"amount_minor"`
	Currency       string  `json:"currency" yaml:"currency"`
}

// CostReconciliationCredential is one cloud credential's month. Every
// amount is in Currency, the invoice's own. A nil amount is unknown, never
// zero, and DifferenceReason says why no difference is stated. State is the
// last import attempt's, or "absent" when no billing document was ever
// imported for the month.
type CostReconciliationCredential struct {
	CredentialID        string                      `json:"credential_id" yaml:"credential_id"`
	CredentialName      *string                     `json:"credential_name" yaml:"credential_name"`
	Provider            string                      `json:"provider" yaml:"provider"`
	State               string                      `json:"state" yaml:"state"`
	Imports             []CostReconciliationImport  `json:"imports" yaml:"imports"`
	Currency            *string                     `json:"currency" yaml:"currency"`
	CurrenciesMixed     bool                        `json:"currencies_mixed" yaml:"currencies_mixed"`
	InvoiceMinor        *int64                      `json:"invoice_minor" yaml:"invoice_minor"`
	TaxMinor            *int64                      `json:"tax_minor" yaml:"tax_minor"`
	CreditMinor         *int64                      `json:"credit_minor" yaml:"credit_minor"`
	MatchedMinor        *int64                      `json:"matched_minor" yaml:"matched_minor"`
	UnmatchedMinor      *int64                      `json:"unmatched_minor" yaml:"unmatched_minor"`
	EstimateMinor       *int64                      `json:"estimate_minor" yaml:"estimate_minor"`
	EstimateComplete    *bool                       `json:"estimate_complete" yaml:"estimate_complete"`
	EstimateFX          *CostReconciliationFX       `json:"estimate_fx" yaml:"estimate_fx"`
	DifferencePct       *float64                    `json:"difference_pct" yaml:"difference_pct"`
	DifferenceReason    *string                     `json:"difference_reason" yaml:"difference_reason"`
	UnplacedPricedMinor *int64                      `json:"unplaced_priced_minor" yaml:"unplaced_priced_minor"`
	DifferenceCaveat    *string                     `json:"difference_caveat" yaml:"difference_caveat"`
	Clusters            []CostReconciliationCluster `json:"clusters" yaml:"clusters"`
	UnmatchedLines      []CostReconciliationLine    `json:"unmatched_lines" yaml:"unmatched_lines"`
	UnmatchedLineCount  int                         `json:"unmatched_line_count" yaml:"unmatched_line_count"`
	UnmatchedTruncated  bool                        `json:"unmatched_truncated" yaml:"unmatched_truncated"`
}

// CostReconciliation is GET /org/cloud-cost/reconciliation: per cloud
// credential and UTC month, what the provider billed against what the
// metering estimated for the clusters those lines are placed on.
type CostReconciliation struct {
	Month       string                         `json:"month" yaml:"month"`
	MonthClosed bool                           `json:"month_closed" yaml:"month_closed"`
	PeriodHours int                            `json:"period_hours" yaml:"period_hours"`
	GeneratedAt string                         `json:"generated_at" yaml:"generated_at"`
	Credentials []CostReconciliationCredential `json:"credentials" yaml:"credentials"`
}

// GetCostReconciliation returns the provider invoice reconciliation for
// month (YYYY-MM); an empty month leaves it to the platform, which reads the
// last closed month.
// GET /api/v1/org/cloud-cost/reconciliation
func (c *Client) GetCostReconciliation(month string) (*CostReconciliation, error) {
	path := c.BaseURL + "/api/v1/org/cloud-cost/reconciliation"
	if month != "" {
		path += "?month=" + url.QueryEscape(month)
	}
	var result CostReconciliation
	if err := c.sendJSON(http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	if result.Credentials == nil {
		result.Credentials = []CostReconciliationCredential{}
	}
	for index := range result.Credentials {
		credential := &result.Credentials[index]
		if credential.Imports == nil {
			credential.Imports = []CostReconciliationImport{}
		}
		if credential.Clusters == nil {
			credential.Clusters = []CostReconciliationCluster{}
		}
		if credential.UnmatchedLines == nil {
			credential.UnmatchedLines = []CostReconciliationLine{}
		}
	}
	return &result, nil
}

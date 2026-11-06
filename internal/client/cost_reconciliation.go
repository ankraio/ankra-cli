package client

import (
	"encoding/json"
	"net/http"
	"net/url"
)

// CostReconciliationImport is one billing document's last import attempt.
// LinesState says whether the stored lines are the provider's final figures
// ("complete") or month to date ("partial"), and is nil when no attempt
// ever stored any; a failed attempt leaves it as it was. SucceededAt is when
// the stored lines were read. Counted marks the one document per credential
// and month whose lines the amounts are read from; a platform that does not
// say which leaves it false on every document.
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
	Counted          bool    `json:"counted" yaml:"counted"`
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
// MeteredHours below LifetimeHours makes the estimate a floor. InCoverage
// says the cluster is one of those the stated difference is taken over.
type CostReconciliationCluster struct {
	ClusterID     string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName   string `json:"cluster_name" yaml:"cluster_name"`
	InvoiceMinor  *int64 `json:"invoice_minor" yaml:"invoice_minor"`
	LineCount     int    `json:"line_count" yaml:"line_count"`
	EstimateMinor *int64 `json:"estimate_minor" yaml:"estimate_minor"`
	MeteredHours  *int   `json:"metered_hours" yaml:"metered_hours"`
	LifetimeHours *int   `json:"lifetime_hours" yaml:"lifetime_hours"`
	PeriodHours   int    `json:"period_hours" yaml:"period_hours"`
	InCoverage    bool   `json:"in_coverage" yaml:"in_coverage"`
}

// CostReconciliationCoverage is what a credential's difference speaks for:
// the clusters on its invoice that have a priced estimate and were metered
// for the hours they existed (the platform allows a missed hour or so). The
// difference is taken over these clusters alone, never over the whole
// invoice. HoursSharePct is their metered hours as a share of the hours
// every named cluster existed. A nil share is unknown, never 0%:
// InvoiceSharePct is nil when the invoice total is not positive, and
// HoursSharePct when a named cluster's record is gone.
type CostReconciliationCoverage struct {
	ClustersCovered int      `json:"clusters_covered" yaml:"clusters_covered"`
	ClustersNamed   int      `json:"clusters_named" yaml:"clusters_named"`
	MatchedMinor    int64    `json:"matched_minor" yaml:"matched_minor"`
	EstimateMinor   int64    `json:"estimate_minor" yaml:"estimate_minor"`
	InvoiceSharePct *float64 `json:"invoice_share_pct" yaml:"invoice_share_pct"`
	HoursSharePct   *float64 `json:"hours_share_pct" yaml:"hours_share_pct"`
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
//
// DifferenceCoverage is nil for two reasons a reader must not merge:
// CoverageReported false is a platform that predates the field, so what the
// difference covers was not said; true with a nil coverage is the
// platform saying no cluster was metered for every hour it existed.
// InvoiceWithheld marks a read for a project or a set of clusters: the
// invoice total, tax, credits and unplaced lines are left out, and the
// difference is the scope's clusters alone. SameInvoiceAs is the credential
// this one's invoice is counted under when both read one provider account;
// this credential's amounts are then nil.
//
// CredentialOutsideProjects marks a credential outside the projects the
// reader may read, for a reader confined to a project (a token pinned to one,
// or a member who only holds roles on projects). Its cost on the reader's
// clusters is still given, but which credential it is is withheld:
// CredentialID and CredentialName are nil, each import's SourceDocumentID is
// empty and its Reason nil, and SameInvoiceAs and DifferenceReason are nil.
// A nil CredentialID therefore says the platform did not name the
// credential, never that it was deleted. A platform that predates the field
// names every credential and leaves the marker false.
type CostReconciliationCredential struct {
	CredentialID              *string                     `json:"credential_id" yaml:"credential_id"`
	CredentialOutsideProjects bool                        `json:"credential_outside_projects" yaml:"credential_outside_projects"`
	CredentialName            *string                     `json:"credential_name" yaml:"credential_name"`
	Provider                  string                      `json:"provider" yaml:"provider"`
	State                     string                      `json:"state" yaml:"state"`
	Imports                   []CostReconciliationImport  `json:"imports" yaml:"imports"`
	Currency                  *string                     `json:"currency" yaml:"currency"`
	CurrenciesMixed           bool                        `json:"currencies_mixed" yaml:"currencies_mixed"`
	InvoiceMinor              *int64                      `json:"invoice_minor" yaml:"invoice_minor"`
	TaxMinor                  *int64                      `json:"tax_minor" yaml:"tax_minor"`
	CreditMinor               *int64                      `json:"credit_minor" yaml:"credit_minor"`
	MatchedMinor              *int64                      `json:"matched_minor" yaml:"matched_minor"`
	UnmatchedMinor            *int64                      `json:"unmatched_minor" yaml:"unmatched_minor"`
	EstimateMinor             *int64                      `json:"estimate_minor" yaml:"estimate_minor"`
	EstimateComplete          *bool                       `json:"estimate_complete" yaml:"estimate_complete"`
	EstimateFX                *CostReconciliationFX       `json:"estimate_fx" yaml:"estimate_fx"`
	DifferencePct             *float64                    `json:"difference_pct" yaml:"difference_pct"`
	DifferenceReason          *string                     `json:"difference_reason" yaml:"difference_reason"`
	DifferenceCoverage        *CostReconciliationCoverage `json:"difference_coverage" yaml:"difference_coverage"`
	CoverageReported          bool                        `json:"-" yaml:"-"`
	UnplacedPricedMinor       *int64                      `json:"unplaced_priced_minor" yaml:"unplaced_priced_minor"`
	DifferenceCaveat          *string                     `json:"difference_caveat" yaml:"difference_caveat"`
	Clusters                  []CostReconciliationCluster `json:"clusters" yaml:"clusters"`
	UnmatchedLines            []CostReconciliationLine    `json:"unmatched_lines" yaml:"unmatched_lines"`
	UnmatchedLineCount        int                         `json:"unmatched_line_count" yaml:"unmatched_line_count"`
	UnmatchedTruncated        bool                        `json:"unmatched_truncated" yaml:"unmatched_truncated"`
	InvoiceWithheld           bool                        `json:"invoice_withheld" yaml:"invoice_withheld"`
	SameInvoiceAs             *string                     `json:"same_invoice_as" yaml:"same_invoice_as"`
}

// UnmarshalJSON decodes a credential and records whether the platform sent
// difference_coverage at all (CoverageReported), which a nil pointer alone
// cannot say.
func (credential *CostReconciliationCredential) UnmarshalJSON(data []byte) error {
	type plainCredential CostReconciliationCredential
	var decoded plainCredential
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	_, decoded.CoverageReported = fields["difference_coverage"]
	*credential = CostReconciliationCredential(decoded)
	return nil
}

// CostReconciliation is GET /org/cloud-cost/reconciliation: per cloud
// credential and UTC month, what the provider billed against what the
// metering estimated for the clusters those lines are placed on.
type CostReconciliation struct {
	RawDocument `json:"-" yaml:"-"`

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
	if err := c.sendJSON(http.MethodGet, c.costScopedURL(path), nil, &result); err != nil {
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

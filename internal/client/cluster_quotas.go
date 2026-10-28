package client

// QuotaIncreasePath says how a provider's limits are raised: by the platform
// itself (automatable), in the provider's console, or through its support.
type QuotaIncreasePath struct {
	Mechanism       string `json:"mechanism" yaml:"mechanism"`
	Automatable     bool   `json:"automatable" yaml:"automatable"`
	LimitsReadable  bool   `json:"limits_readable" yaml:"limits_readable"`
	ConsoleURL      string `json:"console_url,omitempty" yaml:"console_url,omitempty"`
	DocsURL         string `json:"docs_url,omitempty" yaml:"docs_url,omitempty"`
	Instructions    string `json:"instructions" yaml:"instructions"`
	CostImplication string `json:"cost_implication,omitempty" yaml:"cost_implication,omitempty"`
}

// QuotaDimension is one limit-bearing resource. Limit, Remaining and
// UtilisationPercent are nil when the provider does not report the limit,
// which is a different fact from a limit of zero.
type QuotaDimension struct {
	Dimension          string   `json:"dimension" yaml:"dimension"`
	Scope              string   `json:"scope" yaml:"scope"`
	Unit               string   `json:"unit" yaml:"unit"`
	Used               float64  `json:"used" yaml:"used"`
	Location           string   `json:"location,omitempty" yaml:"location,omitempty"`
	Limit              *float64 `json:"limit" yaml:"limit"`
	Remaining          *float64 `json:"remaining,omitempty" yaml:"remaining,omitempty"`
	UtilisationPercent *float64 `json:"utilisation_percent,omitempty" yaml:"utilisation_percent,omitempty"`
	Exhausted          bool     `json:"exhausted" yaml:"exhausted"`
	ProviderQuotaName  string   `json:"provider_quota_name,omitempty" yaml:"provider_quota_name,omitempty"`
}

// ClusterQuotaReport is the platform's quota report for the account a
// provider-managed cluster draws on (GET /api/v1/clusters/{provider}/{id}/quotas).
type ClusterQuotaReport struct {
	Provider      string            `json:"provider" yaml:"provider"`
	Increase      QuotaIncreasePath `json:"increase" yaml:"increase"`
	Dimensions    []QuotaDimension  `json:"dimensions" yaml:"dimensions"`
	LimitsExposed bool              `json:"limits_exposed" yaml:"limits_exposed"`
	Pressured     []QuotaDimension  `json:"pressured" yaml:"pressured"`
}

// GetAnkraCloudQuotas reads the account quota of an Ankra Cloud cluster: each
// limit, what the account holds, and what is left.
func (c *Client) GetAnkraCloudQuotas(clusterID string) (*ClusterQuotaReport, error) {
	var report ClusterQuotaReport
	if getError := c.getJSON(c.providerClusterURL(ankraCloudKind, clusterID, "quotas"), &report); getError != nil {
		return nil, getError
	}
	return &report, nil
}

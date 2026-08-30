package client

import (
	"fmt"
	neturl "net/url"
	"strconv"
	"strings"
)

const securityBasePath = "/api/v1/org/security"

// SecurityExploitIntelligence is the CISA KEV listing and FIRST EPSS
// prediction the platform holds for a finding's CVE. KnownExploited is false
// both for an unlisted CVE and before the catalog was ever synced; the
// enclosing response's Intelligence block tells those apart.
type SecurityExploitIntelligence struct {
	KnownExploited       bool     `json:"known_exploited" yaml:"known_exploited"`
	KevDateAdded         *string  `json:"kev_date_added" yaml:"kev_date_added"`
	KevDueDate           *string  `json:"kev_due_date" yaml:"kev_due_date"`
	KevRansomwareUse     bool     `json:"kev_ransomware_use" yaml:"kev_ransomware_use"`
	EPSSScore            *float64 `json:"epss_score" yaml:"epss_score"`
	EPSSPercentile       *float64 `json:"epss_percentile" yaml:"epss_percentile"`
	KevVendorProject     *string  `json:"kev_vendor_project" yaml:"kev_vendor_project"`
	KevProduct           *string  `json:"kev_product" yaml:"kev_product"`
	KevVulnerabilityName *string  `json:"kev_vulnerability_name" yaml:"kev_vulnerability_name"`
	KevRequiredAction    *string  `json:"kev_required_action" yaml:"kev_required_action"`
}

// SecurityIntelligenceStatus reports when the platform last applied each
// public feed; a nil KevSyncedAt means the CISA catalog has never been synced.
type SecurityIntelligenceStatus struct {
	KevSyncedAt  *string `json:"kev_synced_at" yaml:"kev_synced_at"`
	EPSSSyncedAt *string `json:"epss_synced_at" yaml:"epss_synced_at"`
	KevListed    int     `json:"kev_listed" yaml:"kev_listed"`
}

// SecurityPagination is the shared list page envelope.
type SecurityPagination struct {
	Page       int `json:"page" yaml:"page"`
	PageSize   int `json:"page_size" yaml:"page_size"`
	TotalPages int `json:"total_pages" yaml:"total_pages"`
	TotalCount int `json:"total_count" yaml:"total_count"`
}

// SecurityTotals splits observed risk from the actionable and disposition subsets.
type SecurityTotals struct {
	Observed     int `json:"observed" yaml:"observed"`
	Actionable   int `json:"actionable" yaml:"actionable"`
	Acknowledged int `json:"acknowledged" yaml:"acknowledged"`
	AcceptedRisk int `json:"accepted_risk" yaml:"accepted_risk"`
	Resolved     int `json:"resolved" yaml:"resolved"`
}

// SecuritySeverityCounts groups occurrence counts by severity.
type SecuritySeverityCounts struct {
	Critical int `json:"critical" yaml:"critical"`
	High     int `json:"high" yaml:"high"`
	Medium   int `json:"medium" yaml:"medium"`
	Low      int `json:"low" yaml:"low"`
	Unknown  int `json:"unknown" yaml:"unknown"`
}

// SecurityCoverage reports scanner fleet coverage and freshness.
type SecurityCoverage struct {
	TotalClusters     int     `json:"total_clusters" yaml:"total_clusters"`
	ScannedClusters   int     `json:"scanned_clusters" yaml:"scanned_clusters"`
	UnscannedClusters int     `json:"unscanned_clusters" yaml:"unscanned_clusters"`
	StaleClusters     int     `json:"stale_clusters" yaml:"stale_clusters"`
	FreshClusters     int     `json:"fresh_clusters" yaml:"fresh_clusters"`
	LatestReportAt    *string `json:"latest_report_at" yaml:"latest_report_at"`
}

// SecurityScanner is the scoped scanner freshness state.
type SecurityScanner struct {
	Status            string  `json:"status" yaml:"status"`
	LastScan          *string `json:"last_scan" yaml:"last_scan"`
	FreshClusters     int     `json:"fresh_clusters" yaml:"fresh_clusters"`
	StaleClusters     int     `json:"stale_clusters" yaml:"stale_clusters"`
	UnscannedClusters int     `json:"unscanned_clusters" yaml:"unscanned_clusters"`
	StaleAfterSeconds int     `json:"stale_after_seconds" yaml:"stale_after_seconds"`
}

// SecurityRemediationCandidate is one high-impact current finding.
type SecurityRemediationCandidate struct {
	FindingID                   string  `json:"finding_id" yaml:"finding_id"`
	CVEID                       string  `json:"cve_id" yaml:"cve_id"`
	PackageType                 string  `json:"package_type" yaml:"package_type"`
	PackageName                 string  `json:"package_name" yaml:"package_name"`
	Severity                    string  `json:"severity" yaml:"severity"`
	Title                       *string `json:"title" yaml:"title"`
	ActionableCount             int     `json:"actionable_count" yaml:"actionable_count"`
	AffectedClusters            int     `json:"affected_clusters" yaml:"affected_clusters"`
	AffectedWorkloads           int     `json:"affected_workloads" yaml:"affected_workloads"`
	FixableOccurrences          int     `json:"fixable_occurrences" yaml:"fixable_occurrences"`
	LastSeenAt                  string  `json:"last_seen_at" yaml:"last_seen_at"`
	SecurityExploitIntelligence `yaml:",inline"`
}

// SecurityTrendPoint is one daily observed-findings point.
type SecurityTrendPoint struct {
	Date     string `json:"date" yaml:"date"`
	Observed int    `json:"observed" yaml:"observed"`
}

// SecurityOverview is the organisation Security Center summary. The
// KnownExploited* counts follow the CISA KEV catalog: occurrences, distinct
// findings, findings past CISA's remediation deadline, and findings CISA
// links to ransomware campaigns.
type SecurityOverview struct {
	Totals                   SecurityTotals                 `json:"totals" yaml:"totals"`
	Severity                 SecuritySeverityCounts         `json:"severity" yaml:"severity"`
	FixableSevere            int                            `json:"fixable_severe" yaml:"fixable_severe"`
	KnownExploited           int                            `json:"known_exploited" yaml:"known_exploited"`
	KnownExploitedFindings   int                            `json:"known_exploited_findings" yaml:"known_exploited_findings"`
	KnownExploitedOverdue    int                            `json:"known_exploited_overdue" yaml:"known_exploited_overdue"`
	KnownExploitedRansomware int                            `json:"known_exploited_ransomware" yaml:"known_exploited_ransomware"`
	Intelligence             SecurityIntelligenceStatus     `json:"intelligence" yaml:"intelligence"`
	Coverage                 SecurityCoverage               `json:"coverage" yaml:"coverage"`
	Scanner                  SecurityScanner                `json:"scanner" yaml:"scanner"`
	TopRemediation           []SecurityRemediationCandidate `json:"top_remediation_candidates" yaml:"top_remediation_candidates"`
	ObservedTrend            []SecurityTrendPoint           `json:"observed_trend" yaml:"observed_trend"`
	GeneratedAt              string                         `json:"generated_at" yaml:"generated_at"`
}

// SecurityDispositionCounts preserves mixed occurrence state on a finding.
type SecurityDispositionCounts struct {
	Open         int `json:"open" yaml:"open"`
	Acknowledged int `json:"acknowledged" yaml:"acknowledged"`
	AcceptedRisk int `json:"accepted_risk" yaml:"accepted_risk"`
	Resolved     int `json:"resolved" yaml:"resolved"`
}

// SecurityFinding is one logical CVE/package finding with occurrence counts.
type SecurityFinding struct {
	ID                          string                    `json:"id" yaml:"id"`
	Provider                    string                    `json:"provider" yaml:"provider"`
	CVEID                       string                    `json:"cve_id" yaml:"cve_id"`
	PackageType                 string                    `json:"package_type" yaml:"package_type"`
	PackageName                 string                    `json:"package_name" yaml:"package_name"`
	Severity                    string                    `json:"severity" yaml:"severity"`
	Title                       *string                   `json:"title" yaml:"title"`
	PrimaryLink                 *string                   `json:"primary_link" yaml:"primary_link"`
	Score                       *float64                  `json:"score" yaml:"score"`
	FirstSeenAt                 string                    `json:"first_seen_at" yaml:"first_seen_at"`
	LastSeenAt                  string                    `json:"last_seen_at" yaml:"last_seen_at"`
	Occurrences                 int                       `json:"occurrences" yaml:"occurrences"`
	AffectedClusters            int                       `json:"affected_clusters" yaml:"affected_clusters"`
	AffectedWorkloads           int                       `json:"affected_workloads" yaml:"affected_workloads"`
	FixableOccurrences          int                       `json:"fixable_occurrences" yaml:"fixable_occurrences"`
	DispositionCounts           SecurityDispositionCounts `json:"disposition_counts" yaml:"disposition_counts"`
	SecurityExploitIntelligence `yaml:",inline"`
}

// SecurityFacetCount is one filter-chip count.
type SecurityFacetCount struct {
	Value string `json:"value" yaml:"value"`
	Count int    `json:"count" yaml:"count"`
}

// SecurityFindingFacets carries the counts every list filter would return.
type SecurityFindingFacets struct {
	Severity  []SecurityFacetCount `json:"severity" yaml:"severity"`
	Status    []SecurityFacetCount `json:"status" yaml:"status"`
	Addons    []SecurityFacetCount `json:"addons" yaml:"addons"`
	Clusters  []SecurityFacetCount `json:"clusters" yaml:"clusters"`
	Exploited []SecurityFacetCount `json:"exploited" yaml:"exploited"`
}

// SecurityFindingList is the paginated findings collection.
type SecurityFindingList struct {
	Result       []SecurityFinding          `json:"result" yaml:"result"`
	Pagination   SecurityPagination         `json:"pagination" yaml:"pagination"`
	Facets       SecurityFindingFacets      `json:"facets" yaml:"facets"`
	Scanner      SecurityScanner            `json:"scanner" yaml:"scanner"`
	Intelligence SecurityIntelligenceStatus `json:"intelligence" yaml:"intelligence"`
}

// SecurityOccurrence is one scanner observation of a finding.
type SecurityOccurrence struct {
	ID                         string  `json:"id" yaml:"id"`
	ClusterID                  string  `json:"cluster_id" yaml:"cluster_id"`
	ClusterName                string  `json:"cluster_name" yaml:"cluster_name"`
	ReportScope                string  `json:"report_scope" yaml:"report_scope"`
	ReportName                 string  `json:"report_name" yaml:"report_name"`
	ReportNamespace            *string `json:"report_namespace" yaml:"report_namespace"`
	WorkloadKind               *string `json:"workload_kind" yaml:"workload_kind"`
	WorkloadNamespace          *string `json:"workload_namespace" yaml:"workload_namespace"`
	WorkloadName               *string `json:"workload_name" yaml:"workload_name"`
	ContainerName              *string `json:"container_name" yaml:"container_name"`
	ImageRef                   *string `json:"image_ref" yaml:"image_ref"`
	InstalledVersion           *string `json:"installed_version" yaml:"installed_version"`
	FixedVersion               *string `json:"fixed_version" yaml:"fixed_version"`
	AddonSlug                  *string `json:"addon_slug" yaml:"addon_slug"`
	AddonAttributionConfidence string  `json:"addon_attribution_confidence" yaml:"addon_attribution_confidence"`
	ScanState                  string  `json:"scan_state" yaml:"scan_state"`
	EffectiveDisposition       string  `json:"effective_disposition" yaml:"effective_disposition"`
	FirstSeenAt                string  `json:"first_seen_at" yaml:"first_seen_at"`
	LastSeenAt                 string  `json:"last_seen_at" yaml:"last_seen_at"`
}

// SecurityFindingDetail is one finding with its current occurrences.
type SecurityFindingDetail struct {
	Finding          SecurityFinding      `json:"finding" yaml:"finding"`
	Occurrences      []SecurityOccurrence `json:"occurrences" yaml:"occurrences"`
	MatchingPolicies []map[string]any     `json:"matching_policies" yaml:"matching_policies"`
}

// SecurityClusterPosture is one cluster's current security and scanner state.
type SecurityClusterPosture struct {
	ClusterID      string                 `json:"cluster_id" yaml:"cluster_id"`
	ClusterName    string                 `json:"cluster_name" yaml:"cluster_name"`
	Environment    *string                `json:"environment" yaml:"environment"`
	ScannerStatus  string                 `json:"scanner_status" yaml:"scanner_status"`
	PostureStatus  string                 `json:"posture_status" yaml:"posture_status"`
	LatestReportAt *string                `json:"latest_report_at" yaml:"latest_report_at"`
	Observed       int                    `json:"observed" yaml:"observed"`
	Actionable     int                    `json:"actionable" yaml:"actionable"`
	Acknowledged   int                    `json:"acknowledged" yaml:"acknowledged"`
	AcceptedRisk   int                    `json:"accepted_risk" yaml:"accepted_risk"`
	FixableSevere  int                    `json:"fixable_severe" yaml:"fixable_severe"`
	KnownExploited int                    `json:"known_exploited" yaml:"known_exploited"`
	Severity       SecuritySeverityCounts `json:"severity" yaml:"severity"`
}

// SecurityClusterList is the paginated fleet posture collection.
type SecurityClusterList struct {
	Result     []SecurityClusterPosture `json:"result" yaml:"result"`
	Pagination SecurityPagination       `json:"pagination" yaml:"pagination"`
}

// SecurityOverviewOptions scopes the overview to one cluster and/or add-on.
type SecurityOverviewOptions struct {
	ClusterID string
	AddonSlug string
}

// SecurityFindingsOptions mirrors the findings list filters and sorts:
// Severities and Statuses repeat the query key, the two booleans are
// tri-state (nil = not filtered), and Sort is one of the API's sort keys
// (exploitability, epss, known_exploited, severity, last_seen_at, ...).
type SecurityFindingsOptions struct {
	Page           int
	PageSize       int
	Search         string
	Severities     []string
	Statuses       []string
	Fixable        *bool
	KnownExploited *bool
	ClusterID      string
	AddonSlug      string
	Namespace      string
	Sort           string
	Order          string
}

// SecurityClustersOptions mirrors the cluster posture list controls.
type SecurityClustersOptions struct {
	Page     int
	PageSize int
	Search   string
	Status   string
	Sort     string
	Order    string
}

func securityURL(base string, path string, query neturl.Values) string {
	requestURL := base + securityBasePath + path
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	return requestURL
}

func setPaging(query neturl.Values, page int, pageSize int) {
	if page > 0 {
		query.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		query.Set("page_size", strconv.Itoa(pageSize))
	}
}

// GetSecurityOverview reads the organisation (or one cluster's) security
// summary: totals, the CISA KEV counts, coverage and remediation candidates.
func (c *Client) GetSecurityOverview(options SecurityOverviewOptions) (*SecurityOverview, error) {
	query := neturl.Values{}
	if options.ClusterID != "" {
		query.Set("cluster_id", options.ClusterID)
	}
	if options.AddonSlug != "" {
		query.Set("addon_slug", options.AddonSlug)
	}
	var overview SecurityOverview
	if err := c.getJSON(securityURL(c.BaseURL, "/overview", query), &overview); err != nil {
		return nil, fmt.Errorf("security overview request failed: %w", err)
	}
	return &overview, nil
}

// ListSecurityFindings pages through the organisation's logical findings.
func (c *Client) ListSecurityFindings(options SecurityFindingsOptions) (*SecurityFindingList, error) {
	query := neturl.Values{}
	setPaging(query, options.Page, options.PageSize)
	if search := strings.TrimSpace(options.Search); search != "" {
		query.Set("search", search)
	}
	for _, severity := range options.Severities {
		query.Add("severity", strings.ToLower(strings.TrimSpace(severity)))
	}
	for _, status := range options.Statuses {
		query.Add("status", strings.ToLower(strings.TrimSpace(status)))
	}
	if options.Fixable != nil {
		query.Set("fixable", strconv.FormatBool(*options.Fixable))
	}
	if options.KnownExploited != nil {
		query.Set("known_exploited", strconv.FormatBool(*options.KnownExploited))
	}
	if options.ClusterID != "" {
		query.Set("cluster_id", options.ClusterID)
	}
	if options.AddonSlug != "" {
		query.Set("addon_slug", options.AddonSlug)
	}
	if options.Namespace != "" {
		query.Set("namespace", options.Namespace)
	}
	if options.Sort != "" {
		query.Set("sort", options.Sort)
	}
	if options.Order != "" {
		query.Set("order", options.Order)
	}
	var list SecurityFindingList
	if err := c.getJSON(securityURL(c.BaseURL, "/findings", query), &list); err != nil {
		return nil, fmt.Errorf("security findings request failed: %w", err)
	}
	if list.Result == nil {
		list.Result = []SecurityFinding{}
	}
	return &list, nil
}

// GetSecurityFinding reads one finding with its current occurrences.
func (c *Client) GetSecurityFinding(findingID string) (*SecurityFindingDetail, error) {
	var detail SecurityFindingDetail
	requestURL := securityURL(c.BaseURL, "/findings/"+neturl.PathEscape(findingID), neturl.Values{})
	if err := c.getJSON(requestURL, &detail); err != nil {
		return nil, fmt.Errorf("security finding request failed: %w", err)
	}
	if detail.Occurrences == nil {
		detail.Occurrences = []SecurityOccurrence{}
	}
	return &detail, nil
}

// ListSecurityClusters pages through the fleet's per-cluster posture.
func (c *Client) ListSecurityClusters(options SecurityClustersOptions) (*SecurityClusterList, error) {
	query := neturl.Values{}
	setPaging(query, options.Page, options.PageSize)
	if search := strings.TrimSpace(options.Search); search != "" {
		query.Set("search", search)
	}
	if options.Status != "" {
		query.Set("status", options.Status)
	}
	if options.Sort != "" {
		query.Set("sort", options.Sort)
	}
	if options.Order != "" {
		query.Set("order", options.Order)
	}
	var list SecurityClusterList
	if err := c.getJSON(securityURL(c.BaseURL, "/clusters", query), &list); err != nil {
		return nil, fmt.Errorf("security clusters request failed: %w", err)
	}
	if list.Result == nil {
		list.Result = []SecurityClusterPosture{}
	}
	return &list, nil
}

// SecurityAdvisoryMetric is one CVSS assessment on the advisory (NVD's own
// Primary metric, the CNA's Secondary one, across CVSS versions).
type SecurityAdvisoryMetric struct {
	Source                string   `json:"source" yaml:"source"`
	Type                  string   `json:"type" yaml:"type"`
	Version               string   `json:"version" yaml:"version"`
	Vector                string   `json:"vector" yaml:"vector"`
	BaseScore             float64  `json:"base_score" yaml:"base_score"`
	BaseSeverity          string   `json:"base_severity" yaml:"base_severity"`
	ExploitabilityScore   *float64 `json:"exploitability_score" yaml:"exploitability_score"`
	ImpactScore           *float64 `json:"impact_score" yaml:"impact_score"`
	AttackVector          string   `json:"attack_vector,omitempty" yaml:"attack_vector,omitempty"`
	AttackComplexity      string   `json:"attack_complexity,omitempty" yaml:"attack_complexity,omitempty"`
	PrivilegesRequired    string   `json:"privileges_required,omitempty" yaml:"privileges_required,omitempty"`
	UserInteraction       string   `json:"user_interaction,omitempty" yaml:"user_interaction,omitempty"`
	Scope                 string   `json:"scope,omitempty" yaml:"scope,omitempty"`
	ConfidentialityImpact string   `json:"confidentiality_impact,omitempty" yaml:"confidentiality_impact,omitempty"`
	IntegrityImpact       string   `json:"integrity_impact,omitempty" yaml:"integrity_impact,omitempty"`
	AvailabilityImpact    string   `json:"availability_impact,omitempty" yaml:"availability_impact,omitempty"`
}

// SecurityAdvisoryReference is one link the sources publish, tagged the way
// NVD tags them (Patch, Exploit, Vendor Advisory, ...).
type SecurityAdvisoryReference struct {
	URL    string   `json:"url" yaml:"url"`
	Source string   `json:"source" yaml:"source"`
	Tags   []string `json:"tags" yaml:"tags"`
}

// SecurityAdvisoryVersionRange is one affected span; a `status` of
// unaffected names the build a fix ships in.
type SecurityAdvisoryVersionRange struct {
	Introduced   string `json:"introduced,omitempty" yaml:"introduced,omitempty"`
	Fixed        string `json:"fixed,omitempty" yaml:"fixed,omitempty"`
	LastAffected string `json:"last_affected,omitempty" yaml:"last_affected,omitempty"`
	Status       string `json:"status,omitempty" yaml:"status,omitempty"`
}

// SecurityAdvisoryAffected is one affected product (source nvd) or package
// (source osv).
type SecurityAdvisoryAffected struct {
	Source        string                         `json:"source" yaml:"source"`
	Vendor        string                         `json:"vendor,omitempty" yaml:"vendor,omitempty"`
	Product       string                         `json:"product,omitempty" yaml:"product,omitempty"`
	Package       string                         `json:"package,omitempty" yaml:"package,omitempty"`
	Ecosystem     string                         `json:"ecosystem,omitempty" yaml:"ecosystem,omitempty"`
	CollectionURL string                         `json:"collection_url,omitempty" yaml:"collection_url,omitempty"`
	Repository    string                         `json:"repository,omitempty" yaml:"repository,omitempty"`
	DefaultStatus string                         `json:"default_status,omitempty" yaml:"default_status,omitempty"`
	Ranges        []SecurityAdvisoryVersionRange `json:"ranges" yaml:"ranges"`
	Versions      []string                       `json:"versions,omitempty" yaml:"versions,omitempty"`
}

// SecurityAdvisorySSVC is CISA's SSVC decision as NVD republishes it.
type SecurityAdvisorySSVC struct {
	Exploitation    string  `json:"exploitation" yaml:"exploitation"`
	Automatable     string  `json:"automatable" yaml:"automatable"`
	TechnicalImpact string  `json:"technical_impact" yaml:"technical_impact"`
	Timestamp       *string `json:"timestamp" yaml:"timestamp"`
}

// SecurityAdvisoryRecord is the parsed public record, merged from NVD
// (authoritative) and OSV.
type SecurityAdvisoryRecord struct {
	Title               *string                     `json:"title" yaml:"title"`
	Description         *string                     `json:"description" yaml:"description"`
	SourceIdentifier    *string                     `json:"source_identifier" yaml:"source_identifier"`
	VulnerabilityStatus *string                     `json:"vulnerability_status" yaml:"vulnerability_status"`
	PublishedAt         *string                     `json:"published_at" yaml:"published_at"`
	LastModifiedAt      *string                     `json:"last_modified_at" yaml:"last_modified_at"`
	CVSSScore           *float64                    `json:"cvss_score" yaml:"cvss_score"`
	CVSSSeverity        *string                     `json:"cvss_severity" yaml:"cvss_severity"`
	CVSSVector          *string                     `json:"cvss_vector" yaml:"cvss_vector"`
	CVSSVersion         *string                     `json:"cvss_version" yaml:"cvss_version"`
	Metrics             []SecurityAdvisoryMetric    `json:"metrics" yaml:"metrics"`
	CWEIDs              []string                    `json:"cwe_ids" yaml:"cwe_ids"`
	References          []SecurityAdvisoryReference `json:"references" yaml:"references"`
	Affected            []SecurityAdvisoryAffected  `json:"affected" yaml:"affected"`
	Aliases             []string                    `json:"aliases" yaml:"aliases"`
	SSVC                *SecurityAdvisorySSVC       `json:"ssvc" yaml:"ssvc"`
}

// SecurityAdvisorySources says when each public source was last read and
// where its own page lives.
type SecurityAdvisorySources struct {
	NVDFetchedAt *string `json:"nvd_fetched_at" yaml:"nvd_fetched_at"`
	OSVFetchedAt *string `json:"osv_fetched_at" yaml:"osv_fetched_at"`
	NVDURL       string  `json:"nvd_url" yaml:"nvd_url"`
	OSVURL       string  `json:"osv_url" yaml:"osv_url"`
}

// SecurityAdvisoryFleet is the organisation's current findings for the CVE
// and the totals across them.
type SecurityAdvisoryFleet struct {
	Findings           []SecurityFinding `json:"findings" yaml:"findings"`
	Occurrences        int               `json:"occurrences" yaml:"occurrences"`
	FixableOccurrences int               `json:"fixable_occurrences" yaml:"fixable_occurrences"`
	AffectedClusters   int               `json:"affected_clusters" yaml:"affected_clusters"`
	AffectedWorkloads  int               `json:"affected_workloads" yaml:"affected_workloads"`
}

// SecurityAdvisory is the platform's advisory page for one CVE. Status is
// fetched (Advisory present), pending (not read yet - the request queued
// it, ask again shortly), missing (no public record) or error.
type SecurityAdvisory struct {
	CVEID              string                      `json:"cve_id" yaml:"cve_id"`
	Status             string                      `json:"status" yaml:"status"`
	FetchError         *string                     `json:"fetch_error" yaml:"fetch_error"`
	RequestedAt        *string                     `json:"requested_at" yaml:"requested_at"`
	LastAttemptAt      *string                     `json:"last_attempt_at" yaml:"last_attempt_at"`
	Sources            SecurityAdvisorySources     `json:"sources" yaml:"sources"`
	Advisory           *SecurityAdvisoryRecord     `json:"advisory" yaml:"advisory"`
	Intelligence       SecurityExploitIntelligence `json:"intelligence" yaml:"intelligence"`
	IntelligenceStatus SecurityIntelligenceStatus  `json:"intelligence_status" yaml:"intelligence_status"`
	Fleet              SecurityAdvisoryFleet       `json:"fleet" yaml:"fleet"`
}

// GetSecurityAdvisory reads the platform's advisory page for one CVE.
func (c *Client) GetSecurityAdvisory(cveID string) (*SecurityAdvisory, error) {
	var advisory SecurityAdvisory
	requestURL := securityURL(c.BaseURL, "/advisories/"+neturl.PathEscape(cveID), neturl.Values{})
	if err := c.getJSON(requestURL, &advisory); err != nil {
		return nil, fmt.Errorf("security advisory request failed: %w", err)
	}
	if advisory.Fleet.Findings == nil {
		advisory.Fleet.Findings = []SecurityFinding{}
	}
	return &advisory, nil
}

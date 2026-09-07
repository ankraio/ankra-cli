package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"
)

// SecurityOccurrencesOptions narrows one finding's occurrence list to one
// scan state (active or resolved) and/or one cluster.
type SecurityOccurrencesOptions struct {
	FindingID string
	Status    string
	ClusterID string
	Page      int
	PageSize  int
}

// SecurityOccurrenceList is the paginated occurrence collection of one finding.
type SecurityOccurrenceList struct {
	Result     []SecurityOccurrence `json:"result" yaml:"result"`
	Pagination SecurityPagination   `json:"pagination" yaml:"pagination"`
}

// ListSecurityFindingOccurrences pages through one finding's occurrences,
// including the resolved ones the finding detail leaves out.
func (c *Client) ListSecurityFindingOccurrences(options SecurityOccurrencesOptions) (*SecurityOccurrenceList, error) {
	query := neturl.Values{}
	setPaging(query, options.Page, options.PageSize)
	if status := strings.ToLower(strings.TrimSpace(options.Status)); status != "" {
		query.Set("status", status)
	}
	if options.ClusterID != "" {
		query.Set("cluster_id", options.ClusterID)
	}
	requestURL := securityURL(c.BaseURL, "/findings/"+neturl.PathEscape(strings.TrimSpace(options.FindingID))+"/occurrences", query)
	var list SecurityOccurrenceList
	if err := c.getJSON(requestURL, &list); err != nil {
		return nil, fmt.Errorf("security finding occurrences request failed: %w", err)
	}
	if list.Result == nil {
		list.Result = []SecurityOccurrence{}
	}
	return &list, nil
}

// SecurityAttributionSummary says how confidently a workload's images were
// attributed to an add-on.
type SecurityAttributionSummary struct {
	Status    string `json:"status" yaml:"status"`
	Matched   int    `json:"matched" yaml:"matched"`
	Unmatched int    `json:"unmatched" yaml:"unmatched"`
	Ambiguous int    `json:"ambiguous" yaml:"ambiguous"`
	Partial   bool   `json:"partial" yaml:"partial"`
}

// SecurityWorkload is one scanned workload container with its posture split
// by disposition. Priority and RiskScore are the platform's ranking.
type SecurityWorkload struct {
	ID                string                     `json:"id" yaml:"id"`
	ClusterID         string                     `json:"cluster_id" yaml:"cluster_id"`
	ClusterName       string                     `json:"cluster_name" yaml:"cluster_name"`
	WorkloadUID       *string                    `json:"workload_uid" yaml:"workload_uid"`
	WorkloadKind      *string                    `json:"workload_kind" yaml:"workload_kind"`
	WorkloadNamespace *string                    `json:"workload_namespace" yaml:"workload_namespace"`
	WorkloadName      *string                    `json:"workload_name" yaml:"workload_name"`
	ImageRef          *string                    `json:"image_ref" yaml:"image_ref"`
	ImageDigest       *string                    `json:"image_digest" yaml:"image_digest"`
	AddonSlug         *string                    `json:"addon_slug" yaml:"addon_slug"`
	AddonAttribution  SecurityAttributionSummary `json:"addon_attribution" yaml:"addon_attribution"`
	Observed          SecuritySeverityCounts     `json:"observed" yaml:"observed"`
	Actionable        SecuritySeverityCounts     `json:"actionable" yaml:"actionable"`
	Acknowledged      SecuritySeverityCounts     `json:"acknowledged" yaml:"acknowledged"`
	AcceptedRisk      SecuritySeverityCounts     `json:"accepted_risk" yaml:"accepted_risk"`
	FixableSevere     int                        `json:"fixable_severe" yaml:"fixable_severe"`
	KnownExploited    int                        `json:"known_exploited" yaml:"known_exploited"`
	Priority          string                     `json:"priority" yaml:"priority"`
	RiskScore         int                        `json:"risk_score" yaml:"risk_score"`
	LastScan          string                     `json:"last_scan" yaml:"last_scan"`
	ScannerStatus     string                     `json:"scanner_status" yaml:"scanner_status"`
}

// SecurityWorkloadList is the paginated workload posture collection.
type SecurityWorkloadList struct {
	Result     []SecurityWorkload `json:"result" yaml:"result"`
	Pagination SecurityPagination `json:"pagination" yaml:"pagination"`
	Scanner    SecurityScanner    `json:"scanner" yaml:"scanner"`
}

// ListSecurityWorkloads pages through the fleet's scanned workloads. The
// filters are the findings list's; Sort is one of the workload keys
// (risk_score, priority, severity, known_exploited, last_scan, ...).
func (c *Client) ListSecurityWorkloads(options SecurityFindingsOptions) (*SecurityWorkloadList, error) {
	query := neturl.Values{}
	setPaging(query, options.Page, options.PageSize)
	setListControls(query, options.Search, options.Sort, options.Order)
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
	var list SecurityWorkloadList
	if err := c.getJSON(securityURL(c.BaseURL, "/workloads", query), &list); err != nil {
		return nil, fmt.Errorf("security workloads request failed: %w", err)
	}
	if list.Result == nil {
		list.Result = []SecurityWorkload{}
	}
	return &list, nil
}

// SecurityDispositionSelector is what a disposition policy matches: always
// the organisation, then the exact members the policy pins.
type SecurityDispositionSelector struct {
	OrganisationID    string `json:"organisation_id" yaml:"organisation_id"`
	AddonSlug         string `json:"addon_slug,omitempty" yaml:"addon_slug,omitempty"`
	CVEID             string `json:"cve_id,omitempty" yaml:"cve_id,omitempty"`
	PackageType       string `json:"package_type,omitempty" yaml:"package_type,omitempty"`
	PackageName       string `json:"package_name,omitempty" yaml:"package_name,omitempty"`
	ClusterID         string `json:"cluster_id,omitempty" yaml:"cluster_id,omitempty"`
	WorkloadKind      string `json:"workload_kind,omitempty" yaml:"workload_kind,omitempty"`
	WorkloadNamespace string `json:"workload_namespace,omitempty" yaml:"workload_namespace,omitempty"`
	WorkloadName      string `json:"workload_name,omitempty" yaml:"workload_name,omitempty"`
	ImageDigest       string `json:"image_digest,omitempty" yaml:"image_digest,omitempty"`
	ExplicitBroad     bool   `json:"explicit_broad,omitempty" yaml:"explicit_broad,omitempty"`
}

// SecurityDisposition is one vulnerability-disposition policy: an
// acknowledgement or an accepted risk, its selector, lifecycle status and
// the occurrences it currently matches.
type SecurityDisposition struct {
	ID                     string                      `json:"id" yaml:"id"`
	Selector               SecurityDispositionSelector `json:"selector" yaml:"selector"`
	SelectorHash           string                      `json:"selector_hash" yaml:"selector_hash"`
	Disposition            string                      `json:"disposition" yaml:"disposition"`
	Reason                 string                      `json:"reason" yaml:"reason"`
	ExpiresAt              *string                     `json:"expires_at" yaml:"expires_at"`
	ExpireWhenFixAvailable bool                        `json:"expire_when_fix_available" yaml:"expire_when_fix_available"`
	CreatedBy              string                      `json:"created_by" yaml:"created_by"`
	UpdatedBy              string                      `json:"updated_by" yaml:"updated_by"`
	Status                 string                      `json:"status" yaml:"status"`
	MatchHealth            string                      `json:"match_health" yaml:"match_health"`
	MatchedOccurrenceCount int                         `json:"matched_occurrence_count" yaml:"matched_occurrence_count"`
	ActiveMatchCount       int                         `json:"active_match_count" yaml:"active_match_count"`
	FixAvailableMatchCount int                         `json:"fix_available_match_count" yaml:"fix_available_match_count"`
	LastEvaluatedAt        *string                     `json:"last_evaluated_at" yaml:"last_evaluated_at"`
	LastMatchedAt          *string                     `json:"last_matched_at" yaml:"last_matched_at"`
	RevokedAt              *string                     `json:"revoked_at" yaml:"revoked_at"`
	RevokedBy              *string                     `json:"revoked_by" yaml:"revoked_by"`
	RevokedReason          *string                     `json:"revoked_reason" yaml:"revoked_reason"`
	CreatedAt              string                      `json:"created_at" yaml:"created_at"`
	UpdatedAt              string                      `json:"updated_at" yaml:"updated_at"`
}

// SecurityDispositionList is the paginated policy collection.
type SecurityDispositionList struct {
	Result     []SecurityDisposition `json:"result" yaml:"result"`
	Pagination SecurityPagination    `json:"pagination" yaml:"pagination"`
}

// SecurityDispositionsOptions mirrors the policy list controls. Statuses
// are lifecycle states (active, expiring, fix_available, mitigated,
// unmatched, expired, revoked); Dispositions are acknowledged or
// accepted_risk.
type SecurityDispositionsOptions struct {
	Page         int
	PageSize     int
	Search       string
	Statuses     []string
	Dispositions []string
	Sort         string
	Order        string
}

// ListSecurityDispositions pages through the organisation's disposition policies.
func (c *Client) ListSecurityDispositions(options SecurityDispositionsOptions) (*SecurityDispositionList, error) {
	query := neturl.Values{}
	setPaging(query, options.Page, options.PageSize)
	setListControls(query, options.Search, options.Sort, options.Order)
	for _, status := range options.Statuses {
		if trimmed := strings.ToLower(strings.TrimSpace(status)); trimmed != "" {
			query.Add("status", trimmed)
		}
	}
	for _, disposition := range options.Dispositions {
		if trimmed := strings.ToLower(strings.TrimSpace(disposition)); trimmed != "" {
			query.Add("disposition", trimmed)
		}
	}
	var list SecurityDispositionList
	if err := c.getJSON(securityURL(c.BaseURL, "/vulnerability-dispositions", query), &list); err != nil {
		return nil, fmt.Errorf("security dispositions request failed: %w", err)
	}
	if list.Result == nil {
		list.Result = []SecurityDisposition{}
	}
	return &list, nil
}

// SecurityDispositionPreviewRequest asks what a disposition would cover
// before it is written: anchored on one occurrence (a new policy) or on an
// existing policy (an edit). Scope is organisation_addon, the only scope the
// platform accepts today.
type SecurityDispositionPreviewRequest struct {
	OccurrenceID           string     `json:"occurrence_id,omitempty"`
	PolicyID               string     `json:"policy_id,omitempty"`
	Scope                  string     `json:"scope,omitempty"`
	Disposition            string     `json:"disposition,omitempty"`
	ExpireWhenFixAvailable *bool      `json:"expire_when_fix_available,omitempty"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
}

// SecurityDispositionPreviewExclusions counts the occurrences the selector
// deliberately leaves out.
type SecurityDispositionPreviewExclusions struct {
	Ambiguous    int `json:"ambiguous" yaml:"ambiguous"`
	Unattributed int `json:"unattributed" yaml:"unattributed"`
	FixAvailable int `json:"fix_available" yaml:"fix_available"`
}

// SecurityDispositionOverlap is another policy whose selector meets this one.
type SecurityDispositionOverlap struct {
	PolicyID    string                      `json:"policy_id" yaml:"policy_id"`
	Kind        string                      `json:"kind" yaml:"kind"`
	Disposition string                      `json:"disposition" yaml:"disposition"`
	Selector    SecurityDispositionSelector `json:"selector" yaml:"selector"`
}

// SecurityActionableDelta is the observed and actionable totals before and
// after the disposition takes effect.
type SecurityActionableDelta struct {
	ObservedBefore   int `json:"observed_before" yaml:"observed_before"`
	ObservedAfter    int `json:"observed_after" yaml:"observed_after"`
	ActionableBefore int `json:"actionable_before" yaml:"actionable_before"`
	ActionableAfter  int `json:"actionable_after" yaml:"actionable_after"`
}

// SecurityDispositionPreview is the blast radius of a disposition.
type SecurityDispositionPreview struct {
	PolicyID            *string                              `json:"policy_id" yaml:"policy_id"`
	Selector            SecurityDispositionSelector          `json:"selector" yaml:"selector"`
	SelectorHash        string                               `json:"selector_hash" yaml:"selector_hash"`
	AffectedOccurrences int                                  `json:"affected_occurrences" yaml:"affected_occurrences"`
	AffectedFindings    int                                  `json:"affected_findings" yaml:"affected_findings"`
	AffectedClusters    int                                  `json:"affected_clusters" yaml:"affected_clusters"`
	AffectedAddons      int                                  `json:"affected_addons" yaml:"affected_addons"`
	Exclusions          SecurityDispositionPreviewExclusions `json:"exclusions" yaml:"exclusions"`
	Overlaps            []SecurityDispositionOverlap         `json:"overlaps" yaml:"overlaps"`
	Delta               SecurityActionableDelta              `json:"observed_to_actionable_delta" yaml:"observed_to_actionable_delta"`
}

// PreviewSecurityDisposition computes a disposition's blast radius without
// writing anything.
func (c *Client) PreviewSecurityDisposition(request SecurityDispositionPreviewRequest) (*SecurityDispositionPreview, error) {
	var preview SecurityDispositionPreview
	requestURL := securityURL(c.BaseURL, "/vulnerability-dispositions/preview", neturl.Values{})
	if err := c.sendJSON(http.MethodPost, requestURL, request, &preview); err != nil {
		return nil, fmt.Errorf("security disposition preview request failed: %w", err)
	}
	if preview.Overlaps == nil {
		preview.Overlaps = []SecurityDispositionOverlap{}
	}
	return &preview, nil
}

// SecurityDispositionCreateRequest writes a new policy anchored on one
// occurrence. AllowUnmatched lets a policy be stored even when the anchor
// no longer matches anything.
type SecurityDispositionCreateRequest struct {
	OccurrenceID           string     `json:"occurrence_id"`
	Scope                  string     `json:"scope,omitempty"`
	Disposition            string     `json:"disposition"`
	ExpireWhenFixAvailable bool       `json:"expire_when_fix_available"`
	Reason                 string     `json:"reason"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	AllowUnmatched         bool       `json:"allow_unmatched"`
}

// SecurityDispositionMutation is what every disposition write returns: the
// policy as stored and the preview of what it now covers.
type SecurityDispositionMutation struct {
	Policy  SecurityDisposition        `json:"policy" yaml:"policy"`
	Preview SecurityDispositionPreview `json:"preview" yaml:"preview"`
}

// CreateSecurityDisposition acknowledges or accepts the risk of a finding
// across the organisation's matching occurrences.
func (c *Client) CreateSecurityDisposition(request SecurityDispositionCreateRequest) (*SecurityDispositionMutation, error) {
	var mutation SecurityDispositionMutation
	requestURL := securityURL(c.BaseURL, "/vulnerability-dispositions", neturl.Values{})
	if err := c.sendJSON(http.MethodPost, requestURL, request, &mutation); err != nil {
		return nil, fmt.Errorf("security disposition create request failed: %w", err)
	}
	return &mutation, nil
}

// SecurityDispositionUpdateRequest edits a policy's reason and lifecycle;
// nil members are left unchanged.
type SecurityDispositionUpdateRequest struct {
	Reason                 *string    `json:"reason,omitempty"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	ExpireWhenFixAvailable *bool      `json:"expire_when_fix_available,omitempty"`
}

// UpdateSecurityDisposition edits one policy.
func (c *Client) UpdateSecurityDisposition(policyID string, request SecurityDispositionUpdateRequest) (*SecurityDispositionMutation, error) {
	var mutation SecurityDispositionMutation
	requestURL := securityURL(c.BaseURL, "/vulnerability-dispositions/"+neturl.PathEscape(strings.TrimSpace(policyID)), neturl.Values{})
	if err := c.sendJSON(http.MethodPatch, requestURL, request, &mutation); err != nil {
		return nil, fmt.Errorf("security disposition update request failed: %w", err)
	}
	return &mutation, nil
}

// SecurityDispositionRevokeRequest names why a policy is withdrawn.
type SecurityDispositionRevokeRequest struct {
	Reason string `json:"reason"`
}

// RevokeSecurityDisposition withdraws one policy; the occurrences it covered
// become actionable again.
func (c *Client) RevokeSecurityDisposition(policyID string, request SecurityDispositionRevokeRequest) (*SecurityDispositionMutation, error) {
	var mutation SecurityDispositionMutation
	requestURL := securityURL(c.BaseURL, "/vulnerability-dispositions/"+neturl.PathEscape(strings.TrimSpace(policyID))+"/revoke", neturl.Values{})
	if err := c.sendJSON(http.MethodPost, requestURL, request, &mutation); err != nil {
		return nil, fmt.Errorf("security disposition revoke request failed: %w", err)
	}
	return &mutation, nil
}

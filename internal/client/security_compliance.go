package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	neturl "net/url"
	"strings"
)

// ClusterAgentUnavailableError is the platform's 503 envelope for a live
// cluster read the agent could not serve: CLUSTER_OFFLINE, NO_AGENT or
// AGENT_TIMEOUT, with the seconds the platform suggests waiting before
// asking again.
type ClusterAgentUnavailableError struct {
	Code       string
	Detail     string
	RetryAfter int
}

func (e *ClusterAgentUnavailableError) Error() string {
	message := e.Detail
	if message == "" {
		message = "the cluster agent could not serve this read"
	}
	if e.Code != "" {
		message = e.Code + ": " + message
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s (retry in %ds)", message, e.RetryAfter)
	}
	return message
}

// AsClusterAgentUnavailable unwraps a ClusterAgentUnavailableError from an
// error chain.
func AsClusterAgentUnavailable(err error) (*ClusterAgentUnavailableError, bool) {
	var unavailable *ClusterAgentUnavailableError
	if errors.As(err, &unavailable) {
		return unavailable, true
	}
	return nil, false
}

// getSecurityLiveJSON is getJSON for the reads the platform relays to the
// cluster agent, so the frozen 503 envelope arrives as a typed error rather
// than as "unexpected status".
func (c *Client) getSecurityLiveJSON(requestURL string, target any) error {
	request, requestError := http.NewRequest(http.MethodGet, requestURL, nil)
	if requestError != nil {
		return requestError
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return doError
	}
	defer closeBody(response)
	if response.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	body, readError := readResponseBody(response)
	if readError != nil {
		return readError
	}
	if response.StatusCode == http.StatusServiceUnavailable {
		var envelope struct {
			ErrorCode  string `json:"error_code"`
			Detail     string `json:"detail"`
			RetryAfter int    `json:"retry_after"`
		}
		if parseError := json.Unmarshal(body, &envelope); parseError == nil && (envelope.ErrorCode != "" || envelope.Detail != "") {
			return &ClusterAgentUnavailableError{Code: envelope.ErrorCode, Detail: envelope.Detail, RetryAfter: envelope.RetryAfter}
		}
	}
	if response.StatusCode != http.StatusOK {
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return denied
		}
		if detail := detailFromBody(body); detail != "" {
			return newBackendDetailError(response.StatusCode, detail)
		}
		return newUnexpectedResponseErrorWithMessage(response.StatusCode, fmt.Sprintf("unexpected status: %s", response.Status))
	}
	return json.Unmarshal(body, target)
}

// clusterScopedURL appends an optional query to a fully built request URL.
func clusterScopedURL(requestURL string, query neturl.Values) string {
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	return requestURL
}

// SecurityComplianceBenchmarkTotals is one benchmark's pass/fail totals on
// one cluster in the fleet compliance overview.
type SecurityComplianceBenchmarkTotals struct {
	SpecID       string  `json:"spec_id" yaml:"spec_id"`
	Title        string  `json:"title" yaml:"title"`
	PassCount    int     `json:"pass_count" yaml:"pass_count"`
	FailCount    int     `json:"fail_count" yaml:"fail_count"`
	FailedChecks int     `json:"failed_checks" yaml:"failed_checks"`
	UpdatedAt    *string `json:"updated_at" yaml:"updated_at"`
}

// SecurityConfigAuditSeverityCounts groups config-audit failures by severity.
type SecurityConfigAuditSeverityCounts struct {
	Critical int `json:"critical" yaml:"critical"`
	High     int `json:"high" yaml:"high"`
	Medium   int `json:"medium" yaml:"medium"`
	Low      int `json:"low" yaml:"low"`
}

// SecurityComplianceConfigAuditTotals is one cluster's config-audit posture
// in the fleet overview.
type SecurityComplianceConfigAuditTotals struct {
	ReportCount int                               `json:"report_count" yaml:"report_count"`
	FailedCount int                               `json:"failed_count" yaml:"failed_count"`
	Severity    SecurityConfigAuditSeverityCounts `json:"severity" yaml:"severity"`
}

// SecurityComplianceCluster is one cluster in the fleet compliance overview.
// Status not_available carries a Detail (no scanner, agent unreachable)
// instead of empty benchmarks that would read as compliant.
type SecurityComplianceCluster struct {
	ClusterID   string                               `json:"cluster_id" yaml:"cluster_id"`
	ClusterName string                               `json:"cluster_name" yaml:"cluster_name"`
	Status      string                               `json:"status" yaml:"status"`
	Detail      *string                              `json:"detail" yaml:"detail"`
	Benchmarks  []SecurityComplianceBenchmarkTotals  `json:"benchmarks" yaml:"benchmarks"`
	ConfigAudit *SecurityComplianceConfigAuditTotals `json:"config_audit" yaml:"config_audit"`
}

// SecurityComplianceOverview is the fleet's benchmark compliance rollup.
type SecurityComplianceOverview struct {
	Clusters []SecurityComplianceCluster `json:"clusters" yaml:"clusters"`
}

// GetSecurityComplianceOverview reads every cluster's benchmark totals.
func (c *Client) GetSecurityComplianceOverview() (*SecurityComplianceOverview, error) {
	var overview SecurityComplianceOverview
	if err := c.getJSON(securityURL(c.BaseURL, "/compliance", neturl.Values{}), &overview); err != nil {
		return nil, fmt.Errorf("security compliance overview request failed: %w", err)
	}
	if overview.Clusters == nil {
		overview.Clusters = []SecurityComplianceCluster{}
	}
	return &overview, nil
}

// SecurityComplianceFramework is one framework in the catalogue (GDPR, ISO
// 27001, SOC 2, NIST CSF) with the organisation's switch and, when enabled,
// its live score. The scores are nil until the framework is enabled.
type SecurityComplianceFramework struct {
	Key                   string   `json:"key" yaml:"key"`
	Title                 string   `json:"title" yaml:"title"`
	ShortTitle            string   `json:"short_title" yaml:"short_title"`
	Version               string   `json:"version" yaml:"version"`
	Description           string   `json:"description" yaml:"description"`
	ControlCount          int      `json:"control_count" yaml:"control_count"`
	AutomatedControlCount int      `json:"automated_control_count" yaml:"automated_control_count"`
	Enabled               bool     `json:"enabled" yaml:"enabled"`
	EnabledAt             *string  `json:"enabled_at" yaml:"enabled_at"`
	Score                 *float64 `json:"score" yaml:"score"`
	SatisfiedControls     *int     `json:"satisfied_controls" yaml:"satisfied_controls"`
	AtRiskControls        *int     `json:"at_risk_controls" yaml:"at_risk_controls"`
	FailingControls       *int     `json:"failing_controls" yaml:"failing_controls"`
	ManualControls        *int     `json:"manual_controls" yaml:"manual_controls"`
}

// SecurityComplianceFrameworkList is the whole catalogue with enablement.
type SecurityComplianceFrameworkList struct {
	Frameworks  []SecurityComplianceFramework `json:"frameworks" yaml:"frameworks"`
	GeneratedAt string                        `json:"generated_at" yaml:"generated_at"`
}

// ListSecurityComplianceFrameworks reads the framework catalogue.
func (c *Client) ListSecurityComplianceFrameworks() (*SecurityComplianceFrameworkList, error) {
	var list SecurityComplianceFrameworkList
	if err := c.getJSON(securityURL(c.BaseURL, "/compliance/frameworks", neturl.Values{}), &list); err != nil {
		return nil, fmt.Errorf("security compliance frameworks request failed: %w", err)
	}
	if list.Frameworks == nil {
		list.Frameworks = []SecurityComplianceFramework{}
	}
	return &list, nil
}

// SetSecurityComplianceFrameworkEnabled switches one framework on or off
// for the organisation and returns its updated card.
func (c *Client) SetSecurityComplianceFrameworkEnabled(frameworkKey string, enabled bool) (*SecurityComplianceFramework, error) {
	var framework SecurityComplianceFramework
	requestURL := securityURL(c.BaseURL, "/compliance/frameworks/"+neturl.PathEscape(strings.TrimSpace(frameworkKey)), neturl.Values{})
	payload := struct {
		Enabled bool `json:"enabled"`
	}{Enabled: enabled}
	if err := c.sendJSON(http.MethodPatch, requestURL, payload, &framework); err != nil {
		return nil, fmt.Errorf("security compliance framework update request failed: %w", err)
	}
	return &framework, nil
}

// SecurityComplianceCheck is one automated check behind a control.
type SecurityComplianceCheck struct {
	Key      string `json:"key" yaml:"key"`
	Title    string `json:"title" yaml:"title"`
	Passed   bool   `json:"passed" yaml:"passed"`
	Evidence string `json:"evidence" yaml:"evidence"`
}

// SecurityComplianceControl is one control of a framework with its status:
// satisfied, at_risk, failing, manual or not_recorded.
type SecurityComplianceControl struct {
	ID          string                    `json:"id" yaml:"id"`
	Title       string                    `json:"title" yaml:"title"`
	Description string                    `json:"description" yaml:"description"`
	Status      string                    `json:"status" yaml:"status"`
	Checks      []SecurityComplianceCheck `json:"checks" yaml:"checks"`
}

// SecurityComplianceCategory groups a framework's controls.
type SecurityComplianceCategory struct {
	Key       string                      `json:"key" yaml:"key"`
	Title     string                      `json:"title" yaml:"title"`
	Score     *float64                    `json:"score" yaml:"score"`
	Satisfied int                         `json:"satisfied" yaml:"satisfied"`
	AtRisk    int                         `json:"at_risk" yaml:"at_risk"`
	Failing   int                         `json:"failing" yaml:"failing"`
	Manual    int                         `json:"manual" yaml:"manual"`
	Controls  []SecurityComplianceControl `json:"controls" yaml:"controls"`
}

// SecurityComplianceTrendPoint is one recorded monthly score.
type SecurityComplianceTrendPoint struct {
	Month      string  `json:"month" yaml:"month"`
	Score      float64 `json:"score" yaml:"score"`
	CapturedAt string  `json:"captured_at" yaml:"captured_at"`
}

// SecurityComplianceFrameworkIdentity names the framework a report is for.
type SecurityComplianceFrameworkIdentity struct {
	Key         string `json:"key" yaml:"key"`
	Title       string `json:"title" yaml:"title"`
	ShortTitle  string `json:"short_title" yaml:"short_title"`
	Version     string `json:"version" yaml:"version"`
	Description string `json:"description" yaml:"description"`
}

// SecurityComplianceFrameworkReport is one framework's control-by-control
// report, live or for one recorded month.
type SecurityComplianceFrameworkReport struct {
	Framework         SecurityComplianceFrameworkIdentity `json:"framework" yaml:"framework"`
	Period            string                              `json:"period" yaml:"period"`
	GeneratedAt       string                              `json:"generated_at" yaml:"generated_at"`
	Score             float64                             `json:"score" yaml:"score"`
	SatisfiedControls int                                 `json:"satisfied_controls" yaml:"satisfied_controls"`
	AtRiskControls    int                                 `json:"at_risk_controls" yaml:"at_risk_controls"`
	FailingControls   int                                 `json:"failing_controls" yaml:"failing_controls"`
	ManualControls    int                                 `json:"manual_controls" yaml:"manual_controls"`
	Categories        []SecurityComplianceCategory        `json:"categories" yaml:"categories"`
	Trend             []SecurityComplianceTrendPoint      `json:"trend" yaml:"trend"`
	AvailableMonths   []string                            `json:"available_months" yaml:"available_months"`
}

// GetSecurityComplianceFrameworkReport reads one framework's report; an
// empty month is the live report.
func (c *Client) GetSecurityComplianceFrameworkReport(frameworkKey string, month string) (*SecurityComplianceFrameworkReport, error) {
	query := neturl.Values{}
	if trimmed := strings.TrimSpace(month); trimmed != "" {
		query.Set("month", trimmed)
	}
	var report SecurityComplianceFrameworkReport
	requestURL := securityURL(c.BaseURL, "/compliance/frameworks/"+neturl.PathEscape(strings.TrimSpace(frameworkKey))+"/report", query)
	if err := c.getJSON(requestURL, &report); err != nil {
		return nil, fmt.Errorf("security compliance framework report request failed: %w", err)
	}
	if report.Categories == nil {
		report.Categories = []SecurityComplianceCategory{}
	}
	return &report, nil
}

// SecurityComplianceExportOptions selects the evidence report's format and
// window (RFC3339 or YYYY-MM-DD dates; the platform defaults the window).
type SecurityComplianceExportOptions struct {
	Format    string
	StartDate string
	EndDate   string
}

// SecurityComplianceExport is the downloaded evidence report.
type SecurityComplianceExport struct {
	FileName    string
	ContentType string
	Body        []byte
}

// ExportSecurityComplianceReport downloads the organisation's compliance
// evidence report (posture, findings, dispositions, audit summary and
// attestation) as CSV or JSON.
func (c *Client) ExportSecurityComplianceReport(options SecurityComplianceExportOptions) (*SecurityComplianceExport, error) {
	query := neturl.Values{}
	if trimmed := strings.TrimSpace(options.Format); trimmed != "" {
		query.Set("format", trimmed)
	}
	if trimmed := strings.TrimSpace(options.StartDate); trimmed != "" {
		query.Set("start_date", trimmed)
	}
	if trimmed := strings.TrimSpace(options.EndDate); trimmed != "" {
		query.Set("end_date", trimmed)
	}
	request, requestError := http.NewRequest(http.MethodGet, securityURL(c.BaseURL, "/compliance-report", query), nil)
	if requestError != nil {
		return nil, requestError
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return nil, fmt.Errorf("security compliance report request failed: %w", doError)
	}
	defer closeBody(response)
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("security compliance report read failed: %w", readError)
	}
	if response.StatusCode != http.StatusOK {
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return nil, denied
		}
		if detail := detailFromBody(body); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseErrorWithMessage(response.StatusCode,
			fmt.Sprintf("security compliance report failed: %s", redactedBodyForError(body, 300)))
	}
	fileName := ""
	if _, parameters, parseError := mime.ParseMediaType(response.Header.Get("Content-Disposition")); parseError == nil {
		fileName = parameters["filename"]
	}
	return &SecurityComplianceExport{
		FileName:    fileName,
		ContentType: response.Header.Get("Content-Type"),
		Body:        body,
	}, nil
}

// SecurityBenchmarkFailedCheck is one failing benchmark control on a cluster.
type SecurityBenchmarkFailedCheck struct {
	ID            string `json:"id" yaml:"id"`
	Title         string `json:"title" yaml:"title"`
	Severity      string `json:"severity" yaml:"severity"`
	Remediation   string `json:"remediation" yaml:"remediation"`
	AffectedCount int    `json:"affected_count" yaml:"affected_count"`
}

// SecurityBenchmarkPassedCheck is one passing benchmark control.
type SecurityBenchmarkPassedCheck struct {
	ID       string `json:"id" yaml:"id"`
	Title    string `json:"title" yaml:"title"`
	Severity string `json:"severity" yaml:"severity"`
}

// SecurityBenchmark is one ClusterComplianceReport (CIS, NSA/CISA, ...).
type SecurityBenchmark struct {
	SpecID       string                         `json:"spec_id" yaml:"spec_id"`
	Title        string                         `json:"title" yaml:"title"`
	PassCount    int                            `json:"pass_count" yaml:"pass_count"`
	FailCount    int                            `json:"fail_count" yaml:"fail_count"`
	UpdatedAt    *string                        `json:"updated_at" yaml:"updated_at"`
	FailedChecks []SecurityBenchmarkFailedCheck `json:"failed_checks" yaml:"failed_checks"`
	PassedChecks []SecurityBenchmarkPassedCheck `json:"passed_checks" yaml:"passed_checks"`
}

// SecurityConfigAuditNamespaceCount is one namespace's config-audit failures.
type SecurityConfigAuditNamespaceCount struct {
	Namespace string `json:"namespace" yaml:"namespace"`
	Critical  int    `json:"critical" yaml:"critical"`
	High      int    `json:"high" yaml:"high"`
	Medium    int    `json:"medium" yaml:"medium"`
	Low       int    `json:"low" yaml:"low"`
}

// SecurityConfigAuditCheckCount is one failing config-audit check with the
// number of reports it fails in.
type SecurityConfigAuditCheckCount struct {
	ID          string `json:"id" yaml:"id"`
	Title       string `json:"title" yaml:"title"`
	Severity    string `json:"severity" yaml:"severity"`
	Remediation string `json:"remediation" yaml:"remediation"`
	Count       int    `json:"count" yaml:"count"`
}

// SecurityConfigAuditAggregate is the cluster's config-audit rollup.
type SecurityConfigAuditAggregate struct {
	ReportCount     int                                 `json:"report_count" yaml:"report_count"`
	Severity        SecurityConfigAuditSeverityCounts   `json:"severity" yaml:"severity"`
	Namespaces      []SecurityConfigAuditNamespaceCount `json:"namespaces" yaml:"namespaces"`
	TopFailedChecks []SecurityConfigAuditCheckCount     `json:"top_failed_checks" yaml:"top_failed_checks"`
}

// SecurityClusterBenchmarks is one cluster's live benchmark compliance,
// read through the agent. Status not_available carries a Detail.
type SecurityClusterBenchmarks struct {
	ClusterID   string                        `json:"cluster_id" yaml:"cluster_id"`
	ClusterName string                        `json:"cluster_name" yaml:"cluster_name"`
	Status      string                        `json:"status" yaml:"status"`
	Detail      *string                       `json:"detail" yaml:"detail"`
	Benchmarks  []SecurityBenchmark           `json:"benchmarks" yaml:"benchmarks"`
	ConfigAudit *SecurityConfigAuditAggregate `json:"config_audit" yaml:"config_audit"`
	IsTruncated bool                          `json:"is_truncated" yaml:"is_truncated"`
}

// GetSecurityClusterBenchmarks reads one cluster's benchmark compliance.
func (c *Client) GetSecurityClusterBenchmarks(clusterID string) (*SecurityClusterBenchmarks, error) {
	var benchmarks SecurityClusterBenchmarks
	if err := c.getSecurityLiveJSON(fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/security/compliance", c.BaseURL, neturl.PathEscape(strings.TrimSpace(clusterID))), &benchmarks); err != nil {
		return nil, fmt.Errorf("cluster benchmark compliance request failed: %w", err)
	}
	if benchmarks.Benchmarks == nil {
		benchmarks.Benchmarks = []SecurityBenchmark{}
	}
	return &benchmarks, nil
}

// SecurityBenchmarkResourcesOptions names the failing control to drill into.
// Source is benchmark (BenchmarkID required) or config_audit.
type SecurityBenchmarkResourcesOptions struct {
	ClusterID   string
	CheckID     string
	Source      string
	BenchmarkID string
}

// SecurityComplianceResourceOwner is the Ankra resource a failing object
// belongs to, when the platform could match it.
type SecurityComplianceResourceOwner struct {
	ResourceID   *string `json:"resource_id" yaml:"resource_id"`
	ResourceKind *string `json:"resource_kind" yaml:"resource_kind"`
	Name         *string `json:"name" yaml:"name"`
	Namespace    *string `json:"namespace" yaml:"namespace"`
	StackID      *string `json:"stack_id" yaml:"stack_id"`
	StackName    *string `json:"stack_name" yaml:"stack_name"`
	MatchedBy    string  `json:"matched_by" yaml:"matched_by"`
}

// SecurityComplianceAffectedResource is one object failing a control.
type SecurityComplianceAffectedResource struct {
	Kind       string                          `json:"kind" yaml:"kind"`
	Name       string                          `json:"name" yaml:"name"`
	Namespace  *string                         `json:"namespace" yaml:"namespace"`
	CheckID    string                          `json:"check_id" yaml:"check_id"`
	Severity   string                          `json:"severity" yaml:"severity"`
	Message    string                          `json:"message" yaml:"message"`
	ReportKind string                          `json:"report_kind" yaml:"report_kind"`
	Owner      SecurityComplianceResourceOwner `json:"owner" yaml:"owner"`
}

// SecurityBenchmarkResources lists the objects failing one control.
type SecurityBenchmarkResources struct {
	ClusterID        string                               `json:"cluster_id" yaml:"cluster_id"`
	ClusterName      string                               `json:"cluster_name" yaml:"cluster_name"`
	CheckID          string                               `json:"check_id" yaml:"check_id"`
	BenchmarkID      string                               `json:"benchmark_id" yaml:"benchmark_id"`
	Source           string                               `json:"source" yaml:"source"`
	ControlTitle     string                               `json:"control_title" yaml:"control_title"`
	ResolvedCheckIDs []string                             `json:"resolved_check_ids" yaml:"resolved_check_ids"`
	Resources        []SecurityComplianceAffectedResource `json:"resources" yaml:"resources"`
	IsTruncated      bool                                 `json:"is_truncated" yaml:"is_truncated"`
	UnresolvedReason *string                              `json:"unresolved_reason" yaml:"unresolved_reason"`
}

// GetSecurityBenchmarkResources lists the objects failing one control.
func (c *Client) GetSecurityBenchmarkResources(options SecurityBenchmarkResourcesOptions) (*SecurityBenchmarkResources, error) {
	query := neturl.Values{}
	if trimmed := strings.TrimSpace(options.Source); trimmed != "" {
		query.Set("source", trimmed)
	}
	if trimmed := strings.TrimSpace(options.BenchmarkID); trimmed != "" {
		query.Set("benchmark_id", trimmed)
	}
	requestURL := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/security/compliance/checks/%s/resources",
		c.BaseURL, neturl.PathEscape(strings.TrimSpace(options.ClusterID)), neturl.PathEscape(strings.TrimSpace(options.CheckID)))
	var resources SecurityBenchmarkResources
	if err := c.getSecurityLiveJSON(clusterScopedURL(requestURL, query), &resources); err != nil {
		return nil, fmt.Errorf("cluster benchmark resources request failed: %w", err)
	}
	if resources.Resources == nil {
		resources.Resources = []SecurityComplianceAffectedResource{}
	}
	return &resources, nil
}

// SecurityPolicyTotals counts policy report results across the cluster.
type SecurityPolicyTotals struct {
	Pass  int `json:"pass" yaml:"pass"`
	Fail  int `json:"fail" yaml:"fail"`
	Warn  int `json:"warn" yaml:"warn"`
	Error int `json:"error" yaml:"error"`
	Skip  int `json:"skip" yaml:"skip"`
}

// SecurityPolicyNamespaceCount is one namespace's result count for a policy.
type SecurityPolicyNamespaceCount struct {
	Namespace string `json:"namespace" yaml:"namespace"`
	FailCount int    `json:"fail_count" yaml:"fail_count"`
}

// SecurityPolicyEntry is one Kyverno policy with its results.
type SecurityPolicyEntry struct {
	Policy        string                         `json:"policy" yaml:"policy"`
	Severity      string                         `json:"severity" yaml:"severity"`
	Category      string                         `json:"category" yaml:"category"`
	PassCount     int                            `json:"pass_count" yaml:"pass_count"`
	FailCount     int                            `json:"fail_count" yaml:"fail_count"`
	WarnCount     int                            `json:"warn_count" yaml:"warn_count"`
	TopNamespaces []SecurityPolicyNamespaceCount `json:"top_namespaces" yaml:"top_namespaces"`
}

// SecurityPlatformPosture is the platform's own evaluation of the cluster's
// workloads from the resource cache, present with or without Kyverno.
type SecurityPlatformPosture struct {
	Status             string                `json:"status" yaml:"status"`
	Detail             *string               `json:"detail" yaml:"detail"`
	EvaluatedWorkloads int                   `json:"evaluated_workloads" yaml:"evaluated_workloads"`
	ExemptNamespaces   []string              `json:"exempt_namespaces" yaml:"exempt_namespaces"`
	CacheAsOf          *string               `json:"cache_as_of" yaml:"cache_as_of"`
	Totals             SecurityPolicyTotals  `json:"totals" yaml:"totals"`
	Policies           []SecurityPolicyEntry `json:"policies" yaml:"policies"`
	PodSecurity        map[string]any        `json:"pod_security" yaml:"pod_security"`
}

// SecurityClusterPolicyViolations is one cluster's pod-security policy
// posture. PolicyMode is nil when the policy add-on is not installed,
// otherwise audit or enforce.
type SecurityClusterPolicyViolations struct {
	ClusterID       string                  `json:"cluster_id" yaml:"cluster_id"`
	ClusterName     string                  `json:"cluster_name" yaml:"cluster_name"`
	Status          string                  `json:"status" yaml:"status"`
	Detail          *string                 `json:"detail" yaml:"detail"`
	PolicyMode      *string                 `json:"policy_mode" yaml:"policy_mode"`
	ReportCount     int                     `json:"report_count" yaml:"report_count"`
	Totals          SecurityPolicyTotals    `json:"totals" yaml:"totals"`
	Policies        []SecurityPolicyEntry   `json:"policies" yaml:"policies"`
	PlatformPosture SecurityPlatformPosture `json:"platform_posture" yaml:"platform_posture"`
}

// GetSecurityClusterPolicyViolations reads one cluster's policy posture.
func (c *Client) GetSecurityClusterPolicyViolations(clusterID string) (*SecurityClusterPolicyViolations, error) {
	var violations SecurityClusterPolicyViolations
	if err := c.getSecurityLiveJSON(fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/security/policy-violations", c.BaseURL, neturl.PathEscape(strings.TrimSpace(clusterID))), &violations); err != nil {
		return nil, fmt.Errorf("cluster policy violations request failed: %w", err)
	}
	if violations.Policies == nil {
		violations.Policies = []SecurityPolicyEntry{}
	}
	return &violations, nil
}

// SecurityDirectionScore scores one traffic direction: 0-100, higher is
// tighter. A cluster with no workloads has nothing to score.
type SecurityDirectionScore struct {
	Score                int     `json:"score" yaml:"score"`
	OverPrivilegeRate    float64 `json:"over_privilege_rate" yaml:"over_privilege_rate"`
	UnprotectedWorkloads int     `json:"unprotected_workloads" yaml:"unprotected_workloads"`
	TotalWorkloads       int     `json:"total_workloads" yaml:"total_workloads"`
	TotalAdmittingRules  int     `json:"total_admitting_rules" yaml:"total_admitting_rules"`
	BroadRules           int     `json:"broad_rules" yaml:"broad_rules"`
}

// SecurityNetworkExposureFinding is one over-privilege observation.
type SecurityNetworkExposureFinding struct {
	ID            string `json:"id" yaml:"id"`
	Direction     string `json:"direction" yaml:"direction"`
	Title         string `json:"title" yaml:"title"`
	Severity      string `json:"severity" yaml:"severity"`
	Remediation   string `json:"remediation" yaml:"remediation"`
	Namespace     string `json:"namespace" yaml:"namespace"`
	Workload      string `json:"workload" yaml:"workload"`
	PolicyName    string `json:"policy_name" yaml:"policy_name"`
	Reason        string `json:"reason" yaml:"reason"`
	AffectedCount int    `json:"affected_count" yaml:"affected_count"`
}

// SecurityClusterNetworkExposure is one cluster's NetworkPolicy
// over-privilege report.
type SecurityClusterNetworkExposure struct {
	ClusterID         string                           `json:"cluster_id" yaml:"cluster_id"`
	ClusterName       string                           `json:"cluster_name" yaml:"cluster_name"`
	PolicyMode        *string                          `json:"policy_mode" yaml:"policy_mode"`
	Ingress           SecurityDirectionScore           `json:"ingress" yaml:"ingress"`
	Egress            SecurityDirectionScore           `json:"egress" yaml:"egress"`
	Findings          []SecurityNetworkExposureFinding `json:"findings" yaml:"findings"`
	FindingsTruncated int                              `json:"findings_truncated" yaml:"findings_truncated"`
}

// GetSecurityClusterNetworkExposure reads one cluster's NetworkPolicy
// over-privilege report.
func (c *Client) GetSecurityClusterNetworkExposure(clusterID string) (*SecurityClusterNetworkExposure, error) {
	var exposure SecurityClusterNetworkExposure
	if err := c.getSecurityLiveJSON(fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/security/network-policy-overprivilege", c.BaseURL, neturl.PathEscape(strings.TrimSpace(clusterID))), &exposure); err != nil {
		return nil, fmt.Errorf("cluster network exposure request failed: %w", err)
	}
	if exposure.Findings == nil {
		exposure.Findings = []SecurityNetworkExposureFinding{}
	}
	return &exposure, nil
}

// SecurityPolicyModeResult is the stored mode after a switch and the GitOps
// commit that carried it.
type SecurityPolicyModeResult struct {
	Mode      string  `json:"mode" yaml:"mode"`
	Valid     bool    `json:"valid" yaml:"valid"`
	CommitSHA *string `json:"commit_sha" yaml:"commit_sha"`
	CommitURL *string `json:"commit_url" yaml:"commit_url"`
}

// SetSecurityPolicyMode switches the cluster's pod-security policies between
// audit and enforce.
func (c *Client) SetSecurityPolicyMode(clusterID string, mode string) (*SecurityPolicyModeResult, error) {
	var result SecurityPolicyModeResult
	payload := struct {
		Mode string `json:"mode"`
	}{Mode: strings.ToLower(strings.TrimSpace(mode))}
	if err := c.sendJSON(http.MethodPost, fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/security/policy-mode", c.BaseURL, neturl.PathEscape(strings.TrimSpace(clusterID))), payload, &result); err != nil {
		return nil, fmt.Errorf("cluster policy mode request failed: %w", err)
	}
	return &result, nil
}

// SecurityBaselineResourceError is one resource the baseline stack refused.
type SecurityBaselineResourceError struct {
	Name   string              `json:"name" yaml:"name"`
	Kind   string              `json:"kind" yaml:"kind"`
	Errors []map[string]string `json:"errors" yaml:"errors"`
}

// SecurityBaselineResult is the stack write that installs the security
// baseline (Trivy Operator and the policy add-ons) on a cluster.
type SecurityBaselineResult struct {
	StackName         string                          `json:"stack_name" yaml:"stack_name"`
	Errors            []SecurityBaselineResourceError `json:"errors" yaml:"errors"`
	CommitSHA         *string                         `json:"commit_sha" yaml:"commit_sha"`
	CommitURL         *string                         `json:"commit_url" yaml:"commit_url"`
	OperationID       *string                         `json:"operation_id" yaml:"operation_id"`
	JobCount          int                             `json:"job_count" yaml:"job_count"`
	SettingsOnlyCount int                             `json:"settings_only_count" yaml:"settings_only_count"`
	NoopCount         int                             `json:"noop_count" yaml:"noop_count"`
}

// EnableSecurityBaseline installs the security baseline stack on a cluster.
func (c *Client) EnableSecurityBaseline(clusterID string) (*SecurityBaselineResult, error) {
	var result SecurityBaselineResult
	requestURL := securityURL(c.BaseURL, "/clusters/"+neturl.PathEscape(strings.TrimSpace(clusterID))+"/enable-baseline", neturl.Values{})
	if err := c.sendJSON(http.MethodPost, requestURL, nil, &result); err != nil {
		return nil, fmt.Errorf("enable security baseline request failed: %w", err)
	}
	if result.Errors == nil {
		result.Errors = []SecurityBaselineResourceError{}
	}
	return &result, nil
}

// SecurityAddonPosture is one add-on's security posture on one cluster:
// the findings attributed to its images, split by disposition.
type SecurityAddonPosture struct {
	ClusterID             string                         `json:"cluster_id" yaml:"cluster_id"`
	AddonName             string                         `json:"addon_name" yaml:"addon_name"`
	AddonResourceID       *string                        `json:"addon_resource_id" yaml:"addon_resource_id"`
	AddonSlug             *string                        `json:"addon_slug" yaml:"addon_slug"`
	ChartName             *string                        `json:"chart_name" yaml:"chart_name"`
	ReleaseName           *string                        `json:"release_name" yaml:"release_name"`
	Namespace             *string                        `json:"namespace" yaml:"namespace"`
	Status                string                         `json:"status" yaml:"status"`
	Attribution           SecurityAttributionSummary     `json:"attribution" yaml:"attribution"`
	Observed              SecuritySeverityCounts         `json:"observed" yaml:"observed"`
	Actionable            SecuritySeverityCounts         `json:"actionable" yaml:"actionable"`
	Acknowledged          SecuritySeverityCounts         `json:"acknowledged" yaml:"acknowledged"`
	AcceptedRisk          SecuritySeverityCounts         `json:"accepted_risk" yaml:"accepted_risk"`
	FixableSevere         int                            `json:"fixable_severe" yaml:"fixable_severe"`
	KnownExploited        int                            `json:"known_exploited" yaml:"known_exploited"`
	AffectedWorkloads     int                            `json:"affected_workloads" yaml:"affected_workloads"`
	AffectedImages        int                            `json:"affected_images" yaml:"affected_images"`
	LastScan              *string                        `json:"last_scan" yaml:"last_scan"`
	Scanner               SecurityScanner                `json:"scanner" yaml:"scanner"`
	TopActionableFindings []SecurityRemediationCandidate `json:"top_actionable_findings" yaml:"top_actionable_findings"`
}

// GetSecurityAddonPosture reads one add-on's security posture on a cluster.
func (c *Client) GetSecurityAddonPosture(clusterID string, addonName string) (*SecurityAddonPosture, error) {
	var posture SecurityAddonPosture
	requestURL := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s/security",
		c.BaseURL, neturl.PathEscape(strings.TrimSpace(clusterID)), neturl.PathEscape(strings.TrimSpace(addonName)))
	if err := c.getJSON(requestURL, &posture); err != nil {
		return nil, fmt.Errorf("add-on security posture request failed: %w", err)
	}
	if posture.TopActionableFindings == nil {
		posture.TopActionableFindings = []SecurityRemediationCandidate{}
	}
	return &posture, nil
}

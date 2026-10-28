package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
)

// SecurityHistoryPoint is one day's security snapshot of a cluster: what the
// scanner observed, what is actionable once dispositions are applied, and
// what was acknowledged or accepted. A day without a snapshot is absent from
// the list, never a row of zeros.
type SecurityHistoryPoint struct {
	Date                      string  `json:"date" yaml:"date"`
	Findings                  int     `json:"findings" yaml:"findings"`
	Critical                  int     `json:"critical" yaml:"critical"`
	High                      int     `json:"high" yaml:"high"`
	Medium                    int     `json:"medium" yaml:"medium"`
	Low                       int     `json:"low" yaml:"low"`
	Unknown                   int     `json:"unknown" yaml:"unknown"`
	FixableCritical           int     `json:"fixable_critical" yaml:"fixable_critical"`
	FixableHigh               int     `json:"fixable_high" yaml:"fixable_high"`
	Workloads                 int     `json:"workloads" yaml:"workloads"`
	Namespaces                int     `json:"namespaces" yaml:"namespaces"`
	RiskScore                 float64 `json:"risk_score" yaml:"risk_score"`
	ActionableFindings        int     `json:"actionable_findings" yaml:"actionable_findings"`
	ActionableCritical        int     `json:"actionable_critical" yaml:"actionable_critical"`
	ActionableHigh            int     `json:"actionable_high" yaml:"actionable_high"`
	ActionableMedium          int     `json:"actionable_medium" yaml:"actionable_medium"`
	ActionableLow             int     `json:"actionable_low" yaml:"actionable_low"`
	ActionableUnknown         int     `json:"actionable_unknown" yaml:"actionable_unknown"`
	ActionableFixableCritical int     `json:"actionable_fixable_critical" yaml:"actionable_fixable_critical"`
	ActionableFixableHigh     int     `json:"actionable_fixable_high" yaml:"actionable_fixable_high"`
	ActionableRiskScore       float64 `json:"actionable_risk_score" yaml:"actionable_risk_score"`
	AcknowledgedFindings      int     `json:"acknowledged_findings" yaml:"acknowledged_findings"`
	AcknowledgedCritical      int     `json:"acknowledged_critical" yaml:"acknowledged_critical"`
	AcknowledgedHigh          int     `json:"acknowledged_high" yaml:"acknowledged_high"`
	AcceptedRiskFindings      int     `json:"accepted_risk_findings" yaml:"accepted_risk_findings"`
	AcceptedRiskCritical      int     `json:"accepted_risk_critical" yaml:"accepted_risk_critical"`
	AcceptedRiskHigh          int     `json:"accepted_risk_high" yaml:"accepted_risk_high"`
}

// SecurityHistory is a cluster's daily security snapshots over a window.
type SecurityHistory struct {
	Days  int                    `json:"days" yaml:"days"`
	Items []SecurityHistoryPoint `json:"items" yaml:"items"`
}

// SecurityReportSchedule is the recurring security report a cluster emails.
type SecurityReportSchedule struct {
	Frequency  string   `json:"frequency" yaml:"frequency"`
	Recipients []string `json:"recipients" yaml:"recipients"`
	Enabled    bool     `json:"enabled" yaml:"enabled"`
	LastSentAt *string  `json:"last_sent_at" yaml:"last_sent_at"`
	NextDueAt  *string  `json:"next_due_at" yaml:"next_due_at"`
	UpdatedAt  string   `json:"updated_at" yaml:"updated_at"`
}

// SecurityReportScheduleStatus says whether a cluster has a report schedule
// and, when it does, what it is. Schedule is nil until one is configured.
type SecurityReportScheduleStatus struct {
	Configured bool                    `json:"configured" yaml:"configured"`
	Schedule   *SecurityReportSchedule `json:"schedule" yaml:"schedule"`
}

// SecurityReportScheduleRequest creates or replaces a cluster's report
// schedule: at most 20 recipients, and at least one while enabled.
type SecurityReportScheduleRequest struct {
	Frequency  string   `json:"frequency"`
	Recipients []string `json:"recipients"`
	Enabled    bool     `json:"enabled"`
}

// SecurityReportSendNowResult is the platform's answer to an immediate send.
type SecurityReportSendNowResult struct {
	Queued  bool   `json:"queued" yaml:"queued"`
	Message string `json:"message" yaml:"message"`
}

// GetSecurityHistory reads a cluster's daily security snapshots for the last
// days days; the platform clamps the window to 1..365.
func (c *Client) GetSecurityHistory(clusterID string, days int) (*SecurityHistory, error) {
	query := neturl.Values{}
	if days > 0 {
		query.Set("days", fmt.Sprintf("%d", days))
	}
	requestURL := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/security/trivy/history", c.BaseURL, neturl.PathEscape(strings.TrimSpace(clusterID)))
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	var history SecurityHistory
	if err := c.getJSON(requestURL, &history); err != nil {
		return nil, fmt.Errorf("security history request failed: %w", err)
	}
	if history.Items == nil {
		history.Items = []SecurityHistoryPoint{}
	}
	return &history, nil
}

// GetSecurityReportSchedule reads a cluster's recurring security report
// schedule.
func (c *Client) GetSecurityReportSchedule(clusterID string) (*SecurityReportScheduleStatus, error) {
	var status SecurityReportScheduleStatus
	if err := c.getJSON(fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/security/report-schedule", c.BaseURL, neturl.PathEscape(strings.TrimSpace(clusterID))), &status); err != nil {
		return nil, fmt.Errorf("security report schedule request failed: %w", err)
	}
	if status.Schedule != nil && status.Schedule.Recipients == nil {
		status.Schedule.Recipients = []string{}
	}
	return &status, nil
}

// SetSecurityReportSchedule creates or replaces a cluster's recurring
// security report schedule.
func (c *Client) SetSecurityReportSchedule(clusterID string, request SecurityReportScheduleRequest) (*SecurityReportScheduleStatus, error) {
	if request.Recipients == nil {
		request.Recipients = []string{}
	}
	var status SecurityReportScheduleStatus
	if err := c.sendJSON(http.MethodPut, fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/security/report-schedule", c.BaseURL, neturl.PathEscape(strings.TrimSpace(clusterID))), request, &status); err != nil {
		return nil, fmt.Errorf("security report schedule update failed: %w", err)
	}
	return &status, nil
}

// SendSecurityReportNow queues one security report for a cluster right away,
// outside its schedule.
func (c *Client) SendSecurityReportNow(clusterID string) (*SecurityReportSendNowResult, error) {
	var result SecurityReportSendNowResult
	if err := c.sendJSON(http.MethodPost, fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/security/report-schedule/send-now", c.BaseURL, neturl.PathEscape(strings.TrimSpace(clusterID))), nil, &result); err != nil {
		return nil, fmt.Errorf("security report send request failed: %w", err)
	}
	return &result, nil
}

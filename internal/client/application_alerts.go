package client

import (
	"context"
	"net/http"
	neturl "net/url"
)

// The alerts of one application, under
// /api/v1/org/applications/{application_id}/alerts. An application alert is
// stored as an organisation alert with one rule, so it also shows under
// `ankra alerts rules`; these routes are the ones that may change it.

// ApplicationAlertCondition is the one condition an application alert has:
// it fires when Metric reads above Threshold for HoldMinutes in a row.
// Metric is failed_share_pct (the share of requests that failed, in
// percent), p95_ms (the 95th percentile response time, in milliseconds) or
// restarts (container restarts in the last 15 minutes). Operator is gt, the
// only one the platform takes; an empty one is left out of the body and
// reads as gt.
type ApplicationAlertCondition struct {
	ConditionType string  `json:"condition_type" yaml:"condition_type"`
	Metric        string  `json:"metric" yaml:"metric"`
	Operator      string  `json:"operator,omitempty" yaml:"operator,omitempty"`
	Threshold     float64 `json:"threshold" yaml:"threshold"`
	HoldMinutes   int     `json:"hold_minutes" yaml:"hold_minutes"`
}

// ApplicationAlertDestination is one place an alert sends to: one of the
// organisation's alert destinations (`ankra alerts destinations list`).
type ApplicationAlertDestination struct {
	IntegrationID string `json:"integration_id" yaml:"integration_id"`
	Type          string `json:"type" yaml:"type"`
	Label         string `json:"label" yaml:"label"`
}

// ApplicationAlertAcknowledger is who acknowledged a firing first. Both
// members are nil for an acknowledgement recorded before they were kept.
type ApplicationAlertAcknowledger struct {
	UserID *string `json:"user_id" yaml:"user_id"`
	Name   *string `json:"name" yaml:"name"`
}

// ApplicationAlertFiring is an alert's last firing the caller may read.
// ResolvedAt is nil while the firing is open. ResolvedBy is itself, person,
// or nil while it is open or when it ended another way; ResolutionNote says
// why it ended when nothing reported that it cleared.
type ApplicationAlertFiring struct {
	TriggerID                string                        `json:"trigger_id" yaml:"trigger_id"`
	TriggeredAt              string                        `json:"triggered_at" yaml:"triggered_at"`
	ResolvedAt               *string                       `json:"resolved_at" yaml:"resolved_at"`
	AcknowledgedBy           *ApplicationAlertAcknowledger `json:"acknowledged_by" yaml:"acknowledged_by"`
	AcknowledgedAfterSeconds *int                          `json:"acknowledged_after_seconds" yaml:"acknowledged_after_seconds"`
	ResolvedBy               *string                       `json:"resolved_by" yaml:"resolved_by"`
	ResolutionNote           *string                       `json:"resolution_note" yaml:"resolution_note"`
}

// IsOpen reports whether the firing has not been resolved yet: the alert is
// telling someone now.
func (firing *ApplicationAlertFiring) IsOpen() bool {
	return firing != nil && firing.ResolvedAt == nil
}

// ApplicationAlert is one alert of an application's list. Own is true for an
// alert these routes made and may change; false for an organisation alert
// rule that watches one of the environment's clusters, which is changed
// under `ankra alerts rules` and has no Condition or EnvironmentID.
//
// Evaluation is ok (the rule was read on its last run), unknown (the
// evaluator holds it unread, UnknownReason says why) or not_evaluated (no
// run recorded a state yet).
type ApplicationAlert struct {
	RawDocument `json:"-" yaml:"-"`

	AlertID       string                        `json:"alert_id" yaml:"alert_id"`
	RuleID        string                        `json:"rule_id" yaml:"rule_id"`
	Name          string                        `json:"name" yaml:"name"`
	Own           bool                          `json:"own" yaml:"own"`
	EnvironmentID *string                       `json:"environment_id" yaml:"environment_id"`
	Says          string                        `json:"says" yaml:"says"`
	Condition     *ApplicationAlertCondition    `json:"condition" yaml:"condition"`
	Severity      string                        `json:"severity" yaml:"severity"`
	SendTo        []ApplicationAlertDestination `json:"send_to" yaml:"send_to"`
	Enabled       bool                          `json:"enabled" yaml:"enabled"`
	LastFiring    *ApplicationAlertFiring       `json:"last_firing" yaml:"last_firing"`
	EvaluatedAt   *string                       `json:"evaluated_at" yaml:"evaluated_at"`
	Evaluation    string                        `json:"evaluation" yaml:"evaluation"`
	UnknownReason *string                       `json:"unknown_reason" yaml:"unknown_reason"`
}

// ApplicationAlertList is an application's alerts, own alerts first and
// oldest first, at most Limit of them; Truncated says there were more.
// CoveredByOrganisationRules is nil when it was not read: no environment
// was asked for, or the environment's clusters could not be read.
type ApplicationAlertList struct {
	RawDocument `json:"-" yaml:"-"`

	Alerts                     []ApplicationAlert `json:"alerts" yaml:"alerts"`
	Truncated                  bool               `json:"truncated" yaml:"truncated"`
	Limit                      int                `json:"limit" yaml:"limit"`
	CoveredByOrganisationRules *bool              `json:"covered_by_organisation_rules" yaml:"covered_by_organisation_rules"`
}

// ApplicationAlertRequest is the body of a create or a full update. SendTo
// holds 1 to 5 alert destination ids. An empty Severity is left out: a
// create then stores warning, an update keeps the stored one.
type ApplicationAlertRequest struct {
	Name          string                    `json:"name"`
	EnvironmentID string                    `json:"environment_id"`
	Condition     ApplicationAlertCondition `json:"condition"`
	SendTo        []string                  `json:"send_to"`
	Severity      string                    `json:"severity,omitempty"`
}

// ApplicationAlertDeleteResult is the platform's answer to a delete.
type ApplicationAlertDeleteResult struct {
	RawDocument `json:"-" yaml:"-"`

	Success bool   `json:"success" yaml:"success"`
	Message string `json:"message" yaml:"message"`
}

func (client *Client) applicationAlertsURL(applicationID string) string {
	return client.BaseURL + applicationsAPIPath + "/" + neturl.PathEscape(applicationID) + "/alerts"
}

func (client *Client) applicationAlertURL(applicationID string, alertID string) string {
	return client.applicationAlertsURL(applicationID) + "/" + neturl.PathEscape(alertID)
}

// ListApplicationAlerts reads an application's alerts. A non-empty
// environmentID lists the alerts on that environment and the organisation
// rules that watch it.
// GET /api/v1/org/applications/{application_id}/alerts
func (client *Client) ListApplicationAlerts(requestContext context.Context, applicationID string, environmentID string) (*ApplicationAlertList, error) {
	requestURL := client.applicationAlertsURL(applicationID)
	if environmentID != "" {
		requestURL += "?" + neturl.Values{"environment_id": []string{environmentID}}.Encode()
	}
	var list ApplicationAlertList
	if listError := client.sendJSONContext(requestContext, http.MethodGet, requestURL, nil, &list); listError != nil {
		return nil, listError
	}
	if list.Alerts == nil {
		list.Alerts = []ApplicationAlert{}
	}
	return &list, nil
}

// CreateApplicationAlert adds an alert on one environment of the
// application. The platform answers 201 with the alert.
// POST /api/v1/org/applications/{application_id}/alerts
func (client *Client) CreateApplicationAlert(requestContext context.Context, applicationID string, alertRequest ApplicationAlertRequest) (*ApplicationAlert, error) {
	var created ApplicationAlert
	if createError := client.sendJSONContext(requestContext, http.MethodPost, client.applicationAlertsURL(applicationID),
		alertRequest, &created); createError != nil {
		return nil, createError
	}
	return &created, nil
}

// UpdateApplicationAlert replaces the name, environment, condition and
// destinations of one of the application's own alerts.
// PUT /api/v1/org/applications/{application_id}/alerts/{alert_id}
func (client *Client) UpdateApplicationAlert(requestContext context.Context, applicationID string, alertID string, alertRequest ApplicationAlertRequest) (*ApplicationAlert, error) {
	var updated ApplicationAlert
	if updateError := client.sendJSONContext(requestContext, http.MethodPut, client.applicationAlertURL(applicationID, alertID),
		alertRequest, &updated); updateError != nil {
		return nil, updateError
	}
	return &updated, nil
}

// DeleteApplicationAlert removes one of the application's own alerts with
// its rule.
// DELETE /api/v1/org/applications/{application_id}/alerts/{alert_id}
func (client *Client) DeleteApplicationAlert(requestContext context.Context, applicationID string, alertID string) (*ApplicationAlertDeleteResult, error) {
	var result ApplicationAlertDeleteResult
	if deleteError := client.sendJSONContext(requestContext, http.MethodDelete, client.applicationAlertURL(applicationID, alertID),
		nil, &result); deleteError != nil {
		return nil, deleteError
	}
	return &result, nil
}

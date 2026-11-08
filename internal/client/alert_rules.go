package client

import (
	"net/http"
	neturl "net/url"
	"strconv"
)

// alertRulesBasePath is the bearer surface of the organisation's alert
// definitions: the rules Ankra evaluates and opens incidents from.
const alertRulesBasePath = "/api/v1/org/alerts/alerts"

// alertActionsBasePath carries the per-alert actions a member with
// alerts.write may take: muting an alert and acting on its triggers. It ends
// in a slash because an alert id always follows it.
const alertActionsBasePath = "/api/v1/org/alerts/"

// AlertRule is one alert definition as the list, get, create and update
// routes return it.
type AlertRule struct {
	ID              string  `json:"id" yaml:"id"`
	Name            string  `json:"name" yaml:"name"`
	Description     *string `json:"description" yaml:"description"`
	Enabled         bool    `json:"enabled" yaml:"enabled"`
	State           string  `json:"state" yaml:"state"`
	Status          string  `json:"status,omitempty" yaml:"status,omitempty"`
	Severity        string  `json:"severity,omitempty" yaml:"severity,omitempty"`
	RuleCount       int     `json:"rule_count" yaml:"rule_count"`
	TriggerCount    int     `json:"trigger_count" yaml:"trigger_count"`
	LastTriggeredAt *string `json:"last_triggered_at" yaml:"last_triggered_at"`
	LastEvaluatedAt *string `json:"last_evaluated_at" yaml:"last_evaluated_at"`
	CreatedAt       string  `json:"created_at" yaml:"created_at"`
	UpdatedAt       string  `json:"updated_at" yaml:"updated_at"`
	Source          *string `json:"source,omitempty" yaml:"source,omitempty"`
}

// AlertRuleList is the GET /api/v1/org/alerts/alerts body.
type AlertRuleList struct {
	Items      []AlertRule    `json:"items" yaml:"items"`
	Pagination Pagination     `json:"pagination" yaml:"pagination"`
	Metrics    map[string]any `json:"metrics,omitempty" yaml:"metrics,omitempty"`
}

// AlertRuleDetail is the GET /api/v1/org/alerts/alerts/{alert_id} body: the
// alert and the tickets its incidents opened.
type AlertRuleDetail struct {
	Item    AlertRule        `json:"item" yaml:"item"`
	Tickets []map[string]any `json:"tickets" yaml:"tickets"`
}

// AlertRuleDeleteResult is the delete route's answer.
type AlertRuleDeleteResult struct {
	Success bool   `json:"success" yaml:"success"`
	Message string `json:"message" yaml:"message"`
}

// ListAlertRulesOptions narrows the alert listing. Enabled nil lists both
// enabled and muted alerts; an empty string filter is not sent.
type ListAlertRulesOptions struct {
	Page         int
	PageSize     int
	Search       string
	Enabled      *bool
	Severity     string
	ResourceType string
	ClusterID    string
}

type alertRuleEnvelope struct {
	Item AlertRule `json:"item"`
}

// ListAlertRules lists the organisation's alert definitions, one page at a
// time.
func (c *Client) ListAlertRules(options ListAlertRulesOptions) (*AlertRuleList, error) {
	query := neturl.Values{}
	if options.Page > 0 {
		query.Set("page", strconv.Itoa(options.Page))
	}
	if options.PageSize > 0 {
		query.Set("page_size", strconv.Itoa(options.PageSize))
	}
	for key, value := range map[string]string{
		"search":        options.Search,
		"severity":      options.Severity,
		"resource_type": options.ResourceType,
		"cluster_id":    options.ClusterID,
	} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if options.Enabled != nil {
		query.Set("enabled", strconv.FormatBool(*options.Enabled))
	}
	endpoint := c.BaseURL + alertRulesBasePath
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	var list AlertRuleList
	if listError := c.sendJSON(http.MethodGet, endpoint, nil, &list); listError != nil {
		return nil, listError
	}
	return &list, nil
}

// GetAlertRule reads one alert definition.
func (c *Client) GetAlertRule(alertID string) (*AlertRuleDetail, error) {
	var detail AlertRuleDetail
	if getError := c.sendJSON(http.MethodGet, c.alertRuleURL(alertID), nil, &detail); getError != nil {
		return nil, getError
	}
	return &detail, nil
}

// CreateAlertRule creates an alert definition from the platform's create
// body ({name, description, enabled, rules, integration_names}).
func (c *Client) CreateAlertRule(definition map[string]any) (*AlertRule, error) {
	var envelope alertRuleEnvelope
	if createError := c.sendJSON(http.MethodPost, c.BaseURL+alertRulesBasePath, definition, &envelope); createError != nil {
		return nil, createError
	}
	return &envelope.Item, nil
}

// UpdateAlertRule changes the members the body names and keeps the rest; a
// rules member replaces the alert's whole rule set.
func (c *Client) UpdateAlertRule(alertID string, changes map[string]any) (*AlertRule, error) {
	var envelope alertRuleEnvelope
	if updateError := c.sendJSON(http.MethodPut, c.alertRuleURL(alertID), changes, &envelope); updateError != nil {
		return nil, updateError
	}
	return &envelope.Item, nil
}

// DeleteAlertRule deletes an alert definition and its rules.
func (c *Client) DeleteAlertRule(alertID string) (*AlertRuleDeleteResult, error) {
	var result AlertRuleDeleteResult
	if deleteError := c.sendJSON(http.MethodDelete, c.alertRuleURL(alertID), nil, &result); deleteError != nil {
		return nil, deleteError
	}
	return &result, nil
}

// SetAlertRuleEnabled mutes (false) or re-enables (true) an alert. Muting
// keeps its history and settles any open incident; it needs alerts.write,
// not the alerts.manage an edit of the definition needs.
func (c *Client) SetAlertRuleEnabled(alertID string, enabled bool) (*AlertRule, error) {
	var envelope alertRuleEnvelope
	endpoint := c.BaseURL + alertActionsBasePath + neturl.PathEscape(alertID) + "/enabled"
	if setError := c.sendJSON(http.MethodPut, endpoint, map[string]any{"enabled": enabled}, &envelope); setError != nil {
		return nil, setError
	}
	return &envelope.Item, nil
}

// AcknowledgeAlertTrigger acknowledges one firing of an alert, with optional
// notes recorded on the trigger.
func (c *Client) AcknowledgeAlertTrigger(alertID string, triggerID string, notes string) (map[string]any, error) {
	payload := map[string]any{}
	if notes != "" {
		payload["notes"] = notes
	}
	var result map[string]any
	if actionError := c.sendJSON(http.MethodPost, c.alertTriggerURL(alertID, triggerID, "acknowledge"), payload, &result); actionError != nil {
		return nil, actionError
	}
	return result, nil
}

// SnoozeAlertTrigger silences one firing of an alert for durationMinutes.
func (c *Client) SnoozeAlertTrigger(alertID string, triggerID string, durationMinutes int, notes string) (map[string]any, error) {
	payload := map[string]any{"duration_minutes": durationMinutes}
	if notes != "" {
		payload["notes"] = notes
	}
	var result map[string]any
	if actionError := c.sendJSON(http.MethodPost, c.alertTriggerURL(alertID, triggerID, "snooze"), payload, &result); actionError != nil {
		return nil, actionError
	}
	return result, nil
}

func (c *Client) alertRuleURL(alertID string) string {
	return c.BaseURL + alertRulesBasePath + "/" + neturl.PathEscape(alertID)
}

func (c *Client) alertTriggerURL(alertID string, triggerID string, action string) string {
	return c.BaseURL + alertActionsBasePath + neturl.PathEscape(alertID) + "/triggers/" +
		neturl.PathEscape(triggerID) + "/" + action
}

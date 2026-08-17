package client

import (
	"net/http"
	neturl "net/url"
	"strconv"
)

const (
	alertDestinationsBasePath  = "/api/v1/org/alerts/integrations"
	notificationRoutesBasePath = "/api/v1/org/notifications/routes"
)

// AlertDestination mirrors the backend's IntegrationItem for the alert
// webhook integrations under /api/v1/org/alerts/integrations. The URL is
// masked ("https://***") on every read so a webhook secret never round-trips
// through the CLI; a channel-based Slack or Teams destination carries a
// channel_id instead and answers a null URL.
type AlertDestination struct {
	ID          string  `json:"id" yaml:"id"`
	Name        string  `json:"name" yaml:"name"`
	URL         *string `json:"url" yaml:"url"`
	ChannelID   *string `json:"channel_id" yaml:"channel_id"`
	ChannelName *string `json:"channel_name" yaml:"channel_name"`
	// IntegrationType is the receiver (slack, teams, discord, pagerduty,
	// custom); empty from a backend that predates the field. TeamsTenantID
	// travels with Teams channel destinations.
	IntegrationType string  `json:"integration_type,omitempty" yaml:"integration_type,omitempty"`
	TeamsTenantID   *string `json:"teams_tenant_id" yaml:"teams_tenant_id"`
	Description     *string `json:"description" yaml:"description"`
	Template        *string `json:"template" yaml:"template"`
	Enabled         bool    `json:"enabled" yaml:"enabled"`
	OrganisationID  *string `json:"organisation_id" yaml:"organisation_id"`
	CreatedAt       string  `json:"created_at" yaml:"created_at"`
	UpdatedAt       string  `json:"updated_at" yaml:"updated_at"`
}

// AlertDestinationPagination is the page envelope of the destinations list.
type AlertDestinationPagination struct {
	Page       int `json:"page" yaml:"page"`
	PageSize   int `json:"page_size" yaml:"page_size"`
	TotalCount int `json:"total_count" yaml:"total_count"`
	TotalPages int `json:"total_pages" yaml:"total_pages"`
}

// AlertDestinationList is the GET /api/v1/org/alerts/integrations body.
type AlertDestinationList struct {
	Items      []AlertDestination         `json:"items" yaml:"items"`
	Pagination AlertDestinationPagination `json:"pagination" yaml:"pagination"`
}

// ListAlertDestinationsOptions narrows the destinations list. Enabled nil
// lists both enabled and disabled destinations.
type ListAlertDestinationsOptions struct {
	Page     int
	PageSize int
	Search   string
	Enabled  *bool
}

// CreateAlertDestinationRequest is the POST body. URL is required unless
// ChannelID is set (a channel-based Slack or Teams destination); a Teams
// channel additionally needs TeamsTenantID. IntegrationType defaults to
// "slack" and Enabled to true when omitted.
type CreateAlertDestinationRequest struct {
	Name            string  `json:"name"`
	URL             *string `json:"url,omitempty"`
	ChannelID       *string `json:"channel_id,omitempty"`
	ChannelName     *string `json:"channel_name,omitempty"`
	TeamsTenantID   *string `json:"teams_tenant_id,omitempty"`
	IntegrationType *string `json:"integration_type,omitempty"`
	Description     *string `json:"description,omitempty"`
	Template        *string `json:"template,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

// UpdateAlertDestinationRequest is the PUT body; every member is optional
// and an omitted member keeps its current value.
type UpdateAlertDestinationRequest struct {
	Name          *string `json:"name,omitempty"`
	URL           *string `json:"url,omitempty"`
	ChannelID     *string `json:"channel_id,omitempty"`
	ChannelName   *string `json:"channel_name,omitempty"`
	TeamsTenantID *string `json:"teams_tenant_id,omitempty"`
	Description   *string `json:"description,omitempty"`
	Template      *string `json:"template,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

// DeleteAlertDestinationResult is the DELETE body.
type DeleteAlertDestinationResult struct {
	Success bool   `json:"success" yaml:"success"`
	Message string `json:"message" yaml:"message"`
}

// AlertDestinationTestResult is the outcome of firing a sample payload at a
// destination (POST .../{id}/test) or at an ad-hoc URL (POST .../test-url).
// StatusCode and ResponseTimeMS are null when the request never completed.
type AlertDestinationTestResult struct {
	Success        bool     `json:"success" yaml:"success"`
	StatusCode     *int     `json:"status_code" yaml:"status_code"`
	ResponseTimeMS *float64 `json:"response_time_ms" yaml:"response_time_ms"`
	Error          *string  `json:"error" yaml:"error"`
}

// TestAlertDestinationURLRequest is the POST .../test-url body.
type TestAlertDestinationURLRequest struct {
	URL      string  `json:"url"`
	Template *string `json:"template,omitempty"`
}

// SlackChannel is one conversation the Ankra Slack bot can post to.
type SlackChannel struct {
	ID        string `json:"id" yaml:"id"`
	Name      string `json:"name" yaml:"name"`
	IsPrivate bool   `json:"is_private" yaml:"is_private"`
}

// SlackChannelList is the GET .../integrations/slack/channels body. The
// endpoint answers 404 when no Slack workspace is connected to the
// organisation and 503 when the Slack bot service is not configured.
type SlackChannelList struct {
	TeamID   string         `json:"team_id" yaml:"team_id"`
	TeamName *string        `json:"team_name" yaml:"team_name"`
	Channels []SlackChannel `json:"channels" yaml:"channels"`
}

// TeamsChannel is one channel of a team the Ankra Teams bot is installed in.
type TeamsChannel struct {
	ID       string `json:"id" yaml:"id"`
	Name     string `json:"name" yaml:"name"`
	TeamID   string `json:"team_id" yaml:"team_id"`
	TeamName string `json:"team_name" yaml:"team_name"`
	TenantID string `json:"tenant_id" yaml:"tenant_id"`
}

// TeamsChannelList is the GET .../integrations/teams/channels body. The
// endpoint answers 404 when no Teams tenant is connected and 503 when the
// Teams bot service is not configured.
type TeamsChannelList struct {
	Channels []TeamsChannel `json:"channels" yaml:"channels"`
}

// NotificationRoute mirrors one routing rule under
// /api/v1/org/notifications/routes: a notification matching every non-null
// filter (kind, severity, cluster_id, source_id) is delivered to (mode
// "include") or withheld from (mode "exclude") the destination. Routes are
// evaluated in ascending priority; StopOnMatch ends the walk at this rule.
type NotificationRoute struct {
	ID             string  `json:"id" yaml:"id"`
	OrganisationID string  `json:"organisation_id" yaml:"organisation_id"`
	Kind           *string `json:"kind" yaml:"kind"`
	Severity       *string `json:"severity" yaml:"severity"`
	ClusterID      *string `json:"cluster_id" yaml:"cluster_id"`
	SourceID       *string `json:"source_id" yaml:"source_id"`
	DestinationID  string  `json:"destination_id" yaml:"destination_id"`
	Priority       int     `json:"priority" yaml:"priority"`
	StopOnMatch    bool    `json:"stop_on_match" yaml:"stop_on_match"`
	Mode           string  `json:"mode" yaml:"mode"`
	Enabled        bool    `json:"enabled" yaml:"enabled"`
	CreatedAt      string  `json:"created_at" yaml:"created_at"`
	UpdatedAt      string  `json:"updated_at" yaml:"updated_at"`
}

// NotificationRouteList is the GET /api/v1/org/notifications/routes body.
type NotificationRouteList struct {
	Items []NotificationRoute `json:"items" yaml:"items"`
}

// CreateNotificationRouteRequest is the POST body. Only DestinationID is
// required; the backend defaults priority to 100, stop_on_match to false,
// mode to "include", and enabled to true for omitted members.
type CreateNotificationRouteRequest struct {
	DestinationID string  `json:"destination_id"`
	Kind          *string `json:"kind,omitempty"`
	Severity      *string `json:"severity,omitempty"`
	ClusterID     *string `json:"cluster_id,omitempty"`
	SourceID      *string `json:"source_id,omitempty"`
	Priority      *int    `json:"priority,omitempty"`
	StopOnMatch   *bool   `json:"stop_on_match,omitempty"`
	Mode          *string `json:"mode,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

// UpdateNotificationRouteRequest is the PATCH body. The backend applies
// only the members present in the JSON document, so a nil member is left
// out of the wire body entirely rather than sent as null.
type UpdateNotificationRouteRequest struct {
	DestinationID *string `json:"destination_id,omitempty"`
	Kind          *string `json:"kind,omitempty"`
	Severity      *string `json:"severity,omitempty"`
	ClusterID     *string `json:"cluster_id,omitempty"`
	SourceID      *string `json:"source_id,omitempty"`
	Priority      *int    `json:"priority,omitempty"`
	StopOnMatch   *bool   `json:"stop_on_match,omitempty"`
	Mode          *string `json:"mode,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

// NotificationRouteTestResult is the 202 body of POST .../routes/{id}/test:
// the id of the sample delivery queued through the notification outbox.
type NotificationRouteTestResult struct {
	DeliveryID string `json:"delivery_id" yaml:"delivery_id"`
}

type alertDestinationEnvelope struct {
	Item AlertDestination `json:"item"`
}

func (c *Client) alertDestinationURL(destinationID string) string {
	return c.BaseURL + alertDestinationsBasePath + "/" + neturl.PathEscape(destinationID)
}

func (c *Client) notificationRouteURL(routeID string) string {
	return c.BaseURL + notificationRoutesBasePath + "/" + neturl.PathEscape(routeID)
}

// ListAlertDestinations pages through the organisation's alert destinations.
func (c *Client) ListAlertDestinations(options ListAlertDestinationsOptions) (*AlertDestinationList, error) {
	query := neturl.Values{}
	if options.Page > 0 {
		query.Set("page", strconv.Itoa(options.Page))
	}
	if options.PageSize > 0 {
		query.Set("page_size", strconv.Itoa(options.PageSize))
	}
	if options.Search != "" {
		query.Set("search", options.Search)
	}
	if options.Enabled != nil {
		query.Set("enabled", strconv.FormatBool(*options.Enabled))
	}
	requestURL := c.BaseURL + alertDestinationsBasePath
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	var list AlertDestinationList
	if listError := c.sendJSON(http.MethodGet, requestURL, nil, &list); listError != nil {
		return nil, listError
	}
	return &list, nil
}

// GetAlertDestination fetches one destination by id.
func (c *Client) GetAlertDestination(destinationID string) (*AlertDestination, error) {
	var envelope alertDestinationEnvelope
	if getError := c.sendJSON(http.MethodGet, c.alertDestinationURL(destinationID), nil, &envelope); getError != nil {
		return nil, getError
	}
	return &envelope.Item, nil
}

// CreateAlertDestination registers a new destination. A duplicate name
// answers 400 with the backend's detail.
func (c *Client) CreateAlertDestination(request CreateAlertDestinationRequest) (*AlertDestination, error) {
	var envelope alertDestinationEnvelope
	if createError := c.sendJSON(http.MethodPost, c.BaseURL+alertDestinationsBasePath, request, &envelope); createError != nil {
		return nil, createError
	}
	return &envelope.Item, nil
}

// UpdateAlertDestination changes the members set on request and leaves the
// rest untouched.
func (c *Client) UpdateAlertDestination(destinationID string, request UpdateAlertDestinationRequest) (*AlertDestination, error) {
	var envelope alertDestinationEnvelope
	if updateError := c.sendJSON(http.MethodPut, c.alertDestinationURL(destinationID), request, &envelope); updateError != nil {
		return nil, updateError
	}
	return &envelope.Item, nil
}

// DeleteAlertDestination removes a destination.
func (c *Client) DeleteAlertDestination(destinationID string) (*DeleteAlertDestinationResult, error) {
	var result DeleteAlertDestinationResult
	if deleteError := c.sendJSON(http.MethodDelete, c.alertDestinationURL(destinationID), nil, &result); deleteError != nil {
		return nil, deleteError
	}
	return &result, nil
}

// TestAlertDestination fires a sample payload at a stored destination. A
// delivery failure is reported inside the result, not as an error.
func (c *Client) TestAlertDestination(destinationID string) (*AlertDestinationTestResult, error) {
	var result AlertDestinationTestResult
	if testError := c.sendJSON(http.MethodPost, c.alertDestinationURL(destinationID)+"/test", nil, &result); testError != nil {
		return nil, testError
	}
	return &result, nil
}

// TestAlertDestinationURL fires a sample payload at an ad-hoc webhook URL
// before it is stored as a destination.
func (c *Client) TestAlertDestinationURL(request TestAlertDestinationURLRequest) (*AlertDestinationTestResult, error) {
	var result AlertDestinationTestResult
	if testError := c.sendJSON(http.MethodPost, c.BaseURL+alertDestinationsBasePath+"/test-url", request, &result); testError != nil {
		return nil, testError
	}
	return &result, nil
}

// ListSlackChannels lists the conversations the Ankra Slack bot can post to
// in the organisation's connected workspace.
func (c *Client) ListSlackChannels() (*SlackChannelList, error) {
	var list SlackChannelList
	if listError := c.sendJSON(http.MethodGet, c.BaseURL+alertDestinationsBasePath+"/slack/channels", nil, &list); listError != nil {
		return nil, listError
	}
	return &list, nil
}

// ListTeamsChannels lists the channels of every team the Ankra Teams bot is
// installed in across the organisation's bound tenants.
func (c *Client) ListTeamsChannels() (*TeamsChannelList, error) {
	var list TeamsChannelList
	if listError := c.sendJSON(http.MethodGet, c.BaseURL+alertDestinationsBasePath+"/teams/channels", nil, &list); listError != nil {
		return nil, listError
	}
	return &list, nil
}

// ListNotificationRoutes lists the organisation's routing rules.
func (c *Client) ListNotificationRoutes() (*NotificationRouteList, error) {
	var list NotificationRouteList
	if listError := c.sendJSON(http.MethodGet, c.BaseURL+notificationRoutesBasePath, nil, &list); listError != nil {
		return nil, listError
	}
	return &list, nil
}

// CreateNotificationRoute adds a routing rule. An unknown destination
// answers 404 with the backend's detail.
func (c *Client) CreateNotificationRoute(request CreateNotificationRouteRequest) (*NotificationRoute, error) {
	var route NotificationRoute
	if createError := c.sendJSON(http.MethodPost, c.BaseURL+notificationRoutesBasePath, request, &route); createError != nil {
		return nil, createError
	}
	return &route, nil
}

// UpdateNotificationRoute patches only the members set on request.
func (c *Client) UpdateNotificationRoute(routeID string, request UpdateNotificationRouteRequest) (*NotificationRoute, error) {
	var route NotificationRoute
	if updateError := c.sendJSON(http.MethodPatch, c.notificationRouteURL(routeID), request, &route); updateError != nil {
		return nil, updateError
	}
	return &route, nil
}

// DeleteNotificationRoute removes a routing rule; the backend answers 204
// with no body.
func (c *Client) DeleteNotificationRoute(routeID string) error {
	return c.sendJSON(http.MethodDelete, c.notificationRouteURL(routeID), nil, nil)
}

// TestNotificationRoute queues a sample delivery through the route's
// destination and returns the delivery id (202).
func (c *Client) TestNotificationRoute(routeID string) (*NotificationRouteTestResult, error) {
	var result NotificationRouteTestResult
	if testError := c.sendJSON(http.MethodPost, c.notificationRouteURL(routeID)+"/test", nil, &result); testError != nil {
		return nil, testError
	}
	return &result, nil
}

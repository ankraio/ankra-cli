package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
)

// AlertmanagerTarget is the in-cluster Alertmanager a silence call went to.
type AlertmanagerTarget struct {
	Namespace string `json:"namespace" yaml:"namespace"`
	Service   string `json:"service" yaml:"service"`
	Port      int    `json:"port" yaml:"port"`
}

// AlertmanagerSilenceMatcher is one label matcher of a silence.
type AlertmanagerSilenceMatcher struct {
	Name    string `json:"name" yaml:"name"`
	Value   string `json:"value" yaml:"value"`
	IsRegex bool   `json:"is_regex" yaml:"is_regex"`
}

// AlertmanagerSilence is one silence as the listing returns it.
type AlertmanagerSilence struct {
	ID        string                       `json:"id" yaml:"id"`
	State     string                       `json:"state" yaml:"state"`
	Matchers  []AlertmanagerSilenceMatcher `json:"matchers" yaml:"matchers"`
	StartsAt  string                       `json:"starts_at" yaml:"starts_at"`
	EndsAt    string                       `json:"ends_at" yaml:"ends_at"`
	CreatedBy string                       `json:"created_by" yaml:"created_by"`
	Comment   string                       `json:"comment" yaml:"comment"`
}

// AlertmanagerSilenceList is the GET .../alertmanager/silences body.
type AlertmanagerSilenceList struct {
	Alertmanager AlertmanagerTarget    `json:"alertmanager" yaml:"alertmanager"`
	State        string                `json:"state" yaml:"state"`
	Silences     []AlertmanagerSilence `json:"silences" yaml:"silences"`
	Count        int                   `json:"count" yaml:"count"`
}

// AlertmanagerSilenceCreated is the POST .../alertmanager/silences answer.
type AlertmanagerSilenceCreated struct {
	SilenceID    string                       `json:"silence_id" yaml:"silence_id"`
	Matchers     []AlertmanagerSilenceMatcher `json:"matchers" yaml:"matchers"`
	StartsAt     string                       `json:"starts_at" yaml:"starts_at"`
	EndsAt       string                       `json:"ends_at" yaml:"ends_at"`
	CreatedBy    string                       `json:"created_by" yaml:"created_by"`
	Comment      string                       `json:"comment" yaml:"comment"`
	Alertmanager AlertmanagerTarget           `json:"alertmanager" yaml:"alertmanager"`
}

// AlertmanagerSilenceExpired is the DELETE .../alertmanager/silences/{id} answer.
type AlertmanagerSilenceExpired struct {
	SilenceID    string             `json:"silence_id" yaml:"silence_id"`
	Expired      bool               `json:"expired" yaml:"expired"`
	Alertmanager AlertmanagerTarget `json:"alertmanager" yaml:"alertmanager"`
}

// AlertmanagerLocation names the Alertmanager when discovery cannot find it
// or finds several. Empty fields are not sent and the platform discovers;
// Port zero means the platform's default (9093).
type AlertmanagerLocation struct {
	Namespace string
	Service   string
	Port      int
}

func (location AlertmanagerLocation) query() neturl.Values {
	query := neturl.Values{}
	if location.Namespace != "" {
		query.Set("namespace", location.Namespace)
	}
	if location.Service != "" {
		query.Set("service", location.Service)
	}
	if location.Port > 0 {
		query.Set("port", strconv.Itoa(location.Port))
	}
	return query
}

// CreateAlertmanagerSilenceRequest is the create body. Duration empty means
// the platform's default (2h).
type CreateAlertmanagerSilenceRequest struct {
	Matchers  []AlertmanagerSilenceMatcher `json:"matchers"`
	Duration  string                       `json:"duration,omitempty"`
	Comment   string                       `json:"comment"`
	Namespace string                       `json:"namespace,omitempty"`
	Service   string                       `json:"service,omitempty"`
	Port      int                          `json:"port,omitempty"`
}

// ListAlertmanagerSilences lists the silences on the cluster's in-cluster
// Alertmanager in state (active, pending, expired or all; empty means
// active). It needs clusters.read on the cluster.
func (c *Client) ListAlertmanagerSilences(clusterID string, state string, location AlertmanagerLocation) (*AlertmanagerSilenceList, error) {
	query := location.query()
	if state != "" {
		query.Set("state", state)
	}
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/alertmanager/silences", c.BaseURL, neturl.PathEscape(clusterID))
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	var list AlertmanagerSilenceList
	if listError := c.sendJSON(http.MethodGet, endpoint, nil, &list); listError != nil {
		return nil, listError
	}
	return &list, nil
}

// CreateAlertmanagerSilence silences the alerts matching every matcher on
// the cluster's Alertmanager. It needs alerts.write on the cluster.
func (c *Client) CreateAlertmanagerSilence(clusterID string, request CreateAlertmanagerSilenceRequest) (*AlertmanagerSilenceCreated, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/alertmanager/silences", c.BaseURL, neturl.PathEscape(clusterID))
	var created AlertmanagerSilenceCreated
	if createError := c.sendJSON(http.MethodPost, endpoint, request, &created); createError != nil {
		return nil, createError
	}
	return &created, nil
}

// ExpireAlertmanagerSilence ends one silence on the cluster's Alertmanager.
// It needs alerts.write on the cluster.
func (c *Client) ExpireAlertmanagerSilence(clusterID string, silenceID string, location AlertmanagerLocation) (*AlertmanagerSilenceExpired, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/alertmanager/silences/%s", c.BaseURL,
		neturl.PathEscape(clusterID), neturl.PathEscape(silenceID))
	if encoded := location.query().Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	var expired AlertmanagerSilenceExpired
	if expireError := c.sendJSON(http.MethodDelete, endpoint, nil, &expired); expireError != nil {
		return nil, expireError
	}
	return &expired, nil
}

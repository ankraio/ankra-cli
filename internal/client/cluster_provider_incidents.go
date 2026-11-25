package client

import (
	"fmt"
	"net/url"
	"time"
)

// Provider status feed states: ok when every status page of the provider was
// read and they list the cluster's place, unknown when one could not be read,
// the provider publishes none the platform reads, they list none of the
// cluster's regions and zones (or the platform has none recorded), or the
// cluster's provider is not known. An unknown state with no incidents means
// "could not check", never "no incident".
const (
	ProviderFeedStateOK      = "ok"
	ProviderFeedStateUnknown = "unknown"
)

// ProviderIncidentsResponse is GET /api/v1/clusters/{cluster_id}/provider-incidents:
// the open incidents the cluster's cloud provider lists on its own public
// status page for the cluster's region and the zones its machines are in.
type ProviderIncidentsResponse struct {
	ClusterID    string `json:"cluster_id"`
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name"`
	FeedState    string `json:"feed_state"`
	// FeedReason says why FeedState is unknown: no_feed,
	// provider_not_known, managed_kubernetes, location_not_listed,
	// unreachable or timeout. Empty when ok.
	FeedReason                  string                  `json:"feed_reason"`
	Regions                     []string                `json:"regions"`
	Zones                       []string                `json:"zones"`
	Machines                    int                     `json:"machines"`
	MachinesWithoutRecordedZone int                     `json:"machines_without_recorded_zone"`
	OpenIncidentsOnPages        int                     `json:"open_incidents_on_pages"`
	Feeds                       []ProviderStatusFeed    `json:"feeds"`
	Incidents                   []ProviderIncidentMatch `json:"incidents"`
	Summary                     string                  `json:"summary"`
}

// ProviderStatusFeed is one status page the platform read for the provider.
type ProviderStatusFeed struct {
	Page      string     `json:"page"`
	State     string     `json:"state"`
	Reason    string     `json:"reason"`
	FetchedAt *time.Time `json:"fetched_at"`
}

// ProviderIncidentMatch is one open incident that matches the cluster. Match
// is zone, region or provider_wide; ZoneUnknown marks a region match on a
// zone of the cluster's region while some machines have no recorded zone.
// Title is the provider's own text.
type ProviderIncidentMatch struct {
	Title           string     `json:"title"`
	Status          string     `json:"status"`
	Impact          string     `json:"impact"`
	StartedAt       *time.Time `json:"started_at"`
	PostedAt        *time.Time `json:"posted_at"`
	UpdatedAt       *time.Time `json:"updated_at"`
	Link            string     `json:"link"`
	Page            string     `json:"page"`
	Match           string     `json:"match"`
	MatchedLocation string     `json:"matched_location"`
	ZoneUnknown     bool       `json:"zone_unknown"`
	Components      []string   `json:"components"`
}

// GetClusterProviderIncidents reads the open incidents the cluster's cloud
// provider reports on its status page for the cluster's region and zones.
// A status page the platform could not read is feed_state "unknown" in the
// answer, never an error.
func (c *Client) GetClusterProviderIncidents(clusterID string) (*ProviderIncidentsResponse, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/provider-incidents", c.BaseURL, url.PathEscape(clusterID))
	var response ProviderIncidentsResponse
	if err := c.getJSON(requestURL, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

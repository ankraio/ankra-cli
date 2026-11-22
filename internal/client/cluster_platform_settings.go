package client

// Per-cluster reads and switches the platform serves under bearer routes and
// the CLI had no command for (ankra-hq41xb.15): the Baseline score, the take
// of a held platform update, worker self-heal, the registered metrics source
// and the rotation of the cluster's DNS write token.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ClusterBaselineCheck is one Baseline check with its verdict and the
// evidence it rests on. Status is pass, fail or unknown; unknown means the
// data was absent or stale and is never a pass.
type ClusterBaselineCheck struct {
	ID         string `json:"id" yaml:"id"`
	Title      string `json:"title" yaml:"title"`
	Why        string `json:"why" yaml:"why"`
	DataSource string `json:"data_source" yaml:"data_source"`
	Weight     int    `json:"weight" yaml:"weight"`
	Status     string `json:"status" yaml:"status"`
	Evidence   string `json:"evidence" yaml:"evidence"`
}

// ClusterBaselineWeights is the weight of the checks by verdict.
type ClusterBaselineWeights struct {
	Passed  int `json:"passed" yaml:"passed"`
	Failed  int `json:"failed" yaml:"failed"`
	Unknown int `json:"unknown" yaml:"unknown"`
	Total   int `json:"total" yaml:"total"`
}

// ClusterBaseline is a cluster's latest stored Baseline score. Computed is
// false when the nightly recorder has not scored the cluster yet: every
// check is then unknown and Score is nil, which is not a score of zero.
type ClusterBaseline struct {
	ClusterID        string                 `json:"cluster_id" yaml:"cluster_id"`
	CatalogueVersion int                    `json:"catalogue_version" yaml:"catalogue_version"`
	Computed         bool                   `json:"computed" yaml:"computed"`
	ComputedAt       *string                `json:"computed_at" yaml:"computed_at"`
	Score            *float64               `json:"score" yaml:"score"`
	Coverage         float64                `json:"coverage" yaml:"coverage"`
	Weights          ClusterBaselineWeights `json:"weights" yaml:"weights"`
	Checks           []ClusterBaselineCheck `json:"checks" yaml:"checks"`
	Gaps             []ClusterBaselineCheck `json:"gaps" yaml:"gaps"`
	Unknowns         []ClusterBaselineCheck `json:"unknowns" yaml:"unknowns"`
}

// GetClusterBaseline reads a cluster's Baseline score
// (GET /api/v1/org/clusters/{cluster_id}/baseline).
func (c *Client) GetClusterBaseline(ctx context.Context, clusterID string) (*ClusterBaseline, error) {
	requestURL := fmt.Sprintf("%s/api/v1/org/clusters/%s/baseline", c.BaseURL, url.PathEscape(clusterID))
	var baseline ClusterBaseline
	if getError := c.sendJSONContext(ctx, http.MethodGet, requestURL, nil, &baseline); getError != nil {
		return nil, getError
	}
	return &baseline, nil
}

// HeldPlatformUpdate is one platform add-on update a held platform update
// card names. Reason is why it was held: production, unconfirmed_purpose,
// customer_version, agent_unavailable, control_plane_not_ready or
// control_plane_unread.
type HeldPlatformUpdate struct {
	Stack      string `json:"stack" yaml:"stack"`
	MemberKind string `json:"member_kind" yaml:"member_kind"`
	Member     string `json:"member" yaml:"member"`
	PinKind    string `json:"pin_kind" yaml:"pin_kind"`
	Image      string `json:"image" yaml:"image"`
	Running    string `json:"running" yaml:"running"`
	Pinned     string `json:"pinned" yaml:"pinned"`
	Reason     string `json:"reason" yaml:"reason"`
}

// HeldPlatformUpdateTake is what a take recorded: the updates the next
// publish applies, and the ones it does not lift.
type HeldPlatformUpdateTake struct {
	ClusterID      string               `json:"cluster_id" yaml:"cluster_id"`
	NotificationID string               `json:"notification_id" yaml:"notification_id"`
	Taken          []HeldPlatformUpdate `json:"taken" yaml:"taken"`
	Kept           []HeldPlatformUpdate `json:"kept" yaml:"kept"`
}

type heldPlatformUpdateTakeRequest struct {
	Fingerprint string `json:"fingerprint"`
}

// TakeHeldPlatformUpdate takes Ankra's update on a held platform update card
// (POST /api/v1/org/clusters/{cluster_id}/held-platform-updates/{notification_id}/take).
// fingerprint is the card's data.fingerprint as it was read: the platform
// refuses a card that changed since with 409.
func (c *Client) TakeHeldPlatformUpdate(ctx context.Context, clusterID string, notificationID string, fingerprint string) (*HeldPlatformUpdateTake, error) {
	requestURL := fmt.Sprintf("%s/api/v1/org/clusters/%s/held-platform-updates/%s/take",
		c.BaseURL, url.PathEscape(clusterID), url.PathEscape(notificationID))
	var take HeldPlatformUpdateTake
	if postError := c.sendJSONContext(ctx, http.MethodPost, requestURL,
		heldPlatformUpdateTakeRequest{Fingerprint: fingerprint}, &take); postError != nil {
		return nil, postError
	}
	return &take, nil
}

// WorkerSelfHealIncident is the latest NotReady episode of a worker node.
// Mode is "shadow" when the platform only recorded what it would have done
// and "active" when it was allowed to restart the server. Outcome is empty
// while the episode is open.
type WorkerSelfHealIncident struct {
	NodeName      string  `json:"node_name" yaml:"node_name"`
	State         string  `json:"state" yaml:"state"`
	Outcome       string  `json:"outcome" yaml:"outcome"`
	Reason        string  `json:"reason" yaml:"reason"`
	NotReadySince string  `json:"not_ready_since" yaml:"not_ready_since"`
	LastRestartAt *string `json:"last_restart_at" yaml:"last_restart_at"`
	ClosedAt      *string `json:"closed_at" yaml:"closed_at"`
	Mode          string  `json:"mode" yaml:"mode"`
}

// WorkerSelfHeal is the wire shape both the GET and the PUT answer with.
// LatestIncident is nil when the cluster never had a NotReady worker.
type WorkerSelfHeal struct {
	ClusterID      string                  `json:"cluster_id" yaml:"cluster_id"`
	Enabled        bool                    `json:"enabled" yaml:"enabled"`
	LatestIncident *WorkerSelfHealIncident `json:"latest_incident" yaml:"latest_incident"`
}

// workerSelfHealUpdate is the PUT body. Enabled is always sent: false is the
// point of turning the switch off, and the route refuses a body without a
// boolean.
type workerSelfHealUpdate struct {
	Enabled bool `json:"enabled"`
}

func workerSelfHealURL(baseURL string, clusterID string) string {
	return fmt.Sprintf("%s/api/v1/org/clusters/%s/worker-self-heal", baseURL, url.PathEscape(clusterID))
}

// GetWorkerSelfHeal reads one cluster's switch and latest worker episode.
func (c *Client) GetWorkerSelfHeal(ctx context.Context, clusterID string) (*WorkerSelfHeal, error) {
	var state WorkerSelfHeal
	if getError := c.sendJSONContext(ctx, http.MethodGet,
		workerSelfHealURL(c.BaseURL, clusterID), nil, &state); getError != nil {
		return nil, getError
	}
	return &state, nil
}

// SetWorkerSelfHeal turns one cluster's automatic restart of a NotReady
// worker on or off and returns the resulting state. The platform gates the
// write on clusters.write.
func (c *Client) SetWorkerSelfHeal(ctx context.Context, clusterID string, enabled bool) (*WorkerSelfHeal, error) {
	var state WorkerSelfHeal
	if putError := c.sendJSONContext(ctx, http.MethodPut,
		workerSelfHealURL(c.BaseURL, clusterID), workerSelfHealUpdate{Enabled: enabled}, &state); putError != nil {
		return nil, putError
	}
	return &state, nil
}

// ClusterMetricsSourceCredential is the non-secret identity of the credential
// a metrics source resolves to. The password and token are never returned.
type ClusterMetricsSourceCredential struct {
	Name     string  `json:"name" yaml:"name"`
	State    string  `json:"state" yaml:"state"`
	Username *string `json:"username" yaml:"username"`
}

// ClusterMetricsSourceHealth is the latest probe of a metrics source.
type ClusterMetricsSourceHealth struct {
	State       string  `json:"state" yaml:"state"`
	CheckedAt   *string `json:"checked_at" yaml:"checked_at"`
	Detail      *string `json:"detail" yaml:"detail"`
	SampleCount *int    `json:"sample_count" yaml:"sample_count"`
}

// ClusterMetricsSource is the cluster's registered metrics source.
type ClusterMetricsSource struct {
	Provider       string                          `json:"provider" yaml:"provider"`
	Endpoint       string                          `json:"endpoint" yaml:"endpoint"`
	State          string                          `json:"state" yaml:"state"`
	Flavor         *string                         `json:"flavor" yaml:"flavor"`
	TenantID       *string                         `json:"tenant_id" yaml:"tenant_id"`
	CredentialName *string                         `json:"credential_name" yaml:"credential_name"`
	Credential     *ClusterMetricsSourceCredential `json:"credential" yaml:"credential"`
	Health         *ClusterMetricsSourceHealth     `json:"health" yaml:"health"`
}

// GetClusterMetricsSource reads the cluster's registered metrics source
// (GET /api/v1/org/clusters/imported/{cluster_id}/metrics-source). A cluster
// without one answers 404.
func (c *Client) GetClusterMetricsSource(ctx context.Context, clusterID string) (*ClusterMetricsSource, error) {
	requestURL := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/metrics-source", c.BaseURL, url.PathEscape(clusterID))
	var source ClusterMetricsSource
	if getError := c.sendJSONContext(ctx, http.MethodGet, requestURL, nil, &source); getError != nil {
		return nil, getError
	}
	return &source, nil
}

// ClusterDNSTokenRotation reports where the rotation of the cluster's DNS
// write token stands. It never carries a token.
type ClusterDNSTokenRotation struct {
	Success                        bool    `json:"success" yaml:"success"`
	FQDN                           string  `json:"fqdn" yaml:"fqdn"`
	State                          string  `json:"state" yaml:"state"`
	RotationRequestedAt            *string `json:"rotation_requested_at" yaml:"rotation_requested_at"`
	PreviousTokenPendingRevocation bool    `json:"previous_token_pending_revocation" yaml:"previous_token_pending_revocation"`
	RevocationRequestedAt          *string `json:"revocation_requested_at" yaml:"revocation_requested_at"`
}

// GetClusterDNSTokenRotation reads the rotation state
// (GET /api/v1/clusters/{cluster_id}/dns-zone/token-rotation).
func (c *Client) GetClusterDNSTokenRotation(ctx context.Context, clusterID string) (*ClusterDNSTokenRotation, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/dns-zone/token-rotation", c.BaseURL, url.PathEscape(clusterID))
	var rotation ClusterDNSTokenRotation
	if getError := c.sendJSONContext(ctx, http.MethodGet, requestURL, nil, &rotation); getError != nil {
		return nil, getError
	}
	return &rotation, nil
}

// RotateClusterDNSToken asks the platform to replace the cluster's DNS write
// token (POST /api/v1/clusters/{cluster_id}/dns-zone/token-rotation). The
// replaced token stays valid until RevokePreviousClusterDNSToken. The
// dns-zone writes sit behind the double-submit CSRF guard, like
// EnableClusterDNSZone.
func (c *Client) RotateClusterDNSToken(clusterID string) (*ClusterDNSTokenRotation, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/dns-zone/token-rotation", c.BaseURL, url.PathEscape(clusterID))
	var rotation ClusterDNSTokenRotation
	if postError := c.postCSRFJSON(requestURL, nil, &rotation, "rotate cluster dns token"); postError != nil {
		return nil, postError
	}
	return &rotation, nil
}

// RevokePreviousClusterDNSToken asks the platform to revoke the token the
// last rotation replaced
// (POST /api/v1/clusters/{cluster_id}/dns-zone/token-rotation/revoke-previous).
func (c *Client) RevokePreviousClusterDNSToken(clusterID string) (*ClusterDNSTokenRotation, error) {
	requestURL := fmt.Sprintf("%s/api/v1/clusters/%s/dns-zone/token-rotation/revoke-previous", c.BaseURL, url.PathEscape(clusterID))
	var rotation ClusterDNSTokenRotation
	if postError := c.postCSRFJSON(requestURL, nil, &rotation, "revoke previous cluster dns token"); postError != nil {
		return nil, postError
	}
	return &rotation, nil
}

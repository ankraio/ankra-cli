package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"time"
)

// ErrAddonNotFound is returned (wrapped) by GetAddonByName when no addon with
// the requested name exists on the cluster. Callers use errors.Is to classify
// it as a not-found condition (exit code 3) rather than a generic failure.
var ErrAddonNotFound = errors.New("addon not found")

// ClusterAddonListItem mirrors the backend's importedread
// ClusterAddonListItem. The listing carries no resource id — uninstall
// resolves it through the stack history (GetStackAddonResourceID) — and the
// chart repository arrives as registry_url.
type ClusterAddonListItem struct {
	Name               string     `json:"name"`
	ChartName          string     `json:"chart_name"`
	ChartVersion       string     `json:"chart_version"`
	RegistryURL        string     `json:"registry_url"`
	RegistryName       *string    `json:"registry_name"`
	Namespace          string     `json:"namespace"`
	StackName          *string    `json:"stack_name"`
	Health             *string    `json:"health,omitempty"`
	CreatedAt          *time.Time `json:"created_at"`
	UpdatedAt          *time.Time `json:"updated_at"`
	State              *string    `json:"state,omitempty"`
	ThroughAnkra       bool       `json:"through_ankra"`
	LatestChartVersion *string    `json:"latest_chart_version"`
	// SecurityAdvisories lists the published upstream security advisories
	// covering this addon's version that have a fix available, most severe
	// first. Read it with SecurityAdvisoryStatus: an empty list is a clean
	// answer only when the status is "checked"; "unknown" means the platform
	// could not judge the addon and "not_tracked" that its chart has no
	// advisory source. Older platforms send none of these fields.
	SecurityAdvisories     []AddonSecurityAdvisory `json:"security_advisories" yaml:"security_advisories"`
	SecurityAdvisoryStatus string                  `json:"security_advisory_status" yaml:"security_advisory_status"`
	// SecurityUpgradeChartVersion is the nearest chart version that fixes
	// every listed advisory.
	SecurityUpgradeChartVersion *string `json:"security_upgrade_chart_version" yaml:"security_upgrade_chart_version"`
}

// Security advisory statuses the platform reports per addon.
const (
	SecurityAdvisoryStatusChecked    = "checked"
	SecurityAdvisoryStatusUnknown    = "unknown"
	SecurityAdvisoryStatusNotTracked = "not_tracked"
)

// AddonSecurityAdvisory is one published advisory covering an installed
// addon's version.
type AddonSecurityAdvisory struct {
	AdvisoryID          string     `json:"advisory_id" yaml:"advisory_id"`
	Aliases             []string   `json:"aliases" yaml:"aliases"`
	Severity            string     `json:"severity" yaml:"severity"`
	Summary             string     `json:"summary" yaml:"summary"`
	URL                 string     `json:"url" yaml:"url"`
	PublishedAt         *time.Time `json:"published_at" yaml:"published_at"`
	AffectedVersion     string     `json:"affected_version" yaml:"affected_version"`
	FixedVersion        *string    `json:"fixed_version" yaml:"fixed_version"`
	UpgradeChartVersion *string    `json:"upgrade_chart_version" yaml:"upgrade_chart_version"`
}

// ListClusterAddonsResponse is one page of the addon listing.
// SecurityAdvisoriesStale says an advisory feed behind the page's advisory
// answers has not been read successfully in the last day, or ever, and
// SecurityAdvisoriesCheckedAt is the oldest successful read (null when one
// never happened). Older platforms send neither, which reads as not stale.
type ListClusterAddonsResponse struct {
	Result                      []ClusterAddonListItem `json:"result"`
	Pagination                  Pagination             `json:"pagination"`
	SecurityAdvisoriesCheckedAt *time.Time             `json:"security_advisories_checked_at"`
	SecurityAdvisoriesStale     bool                   `json:"security_advisories_stale"`
}

// ClusterAddonListing is the whole addon listing of a cluster with the
// advisory feed state the platform reported alongside it: an "ok" judged
// from a stale feed only means no advisory was known when the feed was
// last read.
type ClusterAddonListing struct {
	Addons []ClusterAddonListItem
	// SecurityAdvisoriesStale is true when any page reported a stale feed.
	SecurityAdvisoriesStale bool
	// SecurityAdvisoriesCheckedAt is the oldest successful feed read any
	// page reported; nil when none did, or when a stale page reported none
	// (a feed never read successfully).
	SecurityAdvisoriesCheckedAt *time.Time
}

type AddonSettings struct {
	RetryPolicy          *RetryPolicy         `json:"retry_policy,omitempty"`
	SyncPolicy           *SyncPolicy          `json:"sync_policy,omitempty"`
	RevisionHistoryLimit *int                 `json:"revision_history_limit,omitempty"`
	Backup               *AddonBackupSettings `json:"backup,omitempty"`
}

// AddonBackupSettings is the add-on's override of its stack's backup policy
// (epic ankra-0xsdd, WS4). Omitted means the add-on follows the stack: the
// override exists for the one add-on in a protected stack that should be
// captured differently, not as a second place to configure protection.
type AddonBackupSettings struct {
	// Enabled says whether this add-on's data assets are captured. The
	// Stateful settings profile defaults it on.
	Enabled bool `json:"enabled"`
	// Consistency is how the capture is taken: transactional, application,
	// crash or logical. Empty leaves the engine's own choice in place.
	Consistency string `json:"consistency,omitempty"`
	// Schedule and Retention override the stack's for this add-on only.
	// Empty / nil inherits.
	Schedule  string                `json:"schedule,omitempty"`
	Retention *AddonBackupRetention `json:"retention,omitempty"`
}

// AddonBackupRetention mirrors the stack block's retention, for an add-on
// whose data needs keeping longer (or shorter) than the rest of the stack.
type AddonBackupRetention struct {
	Hourly       int    `json:"hourly,omitempty"`
	Daily        int    `json:"daily,omitempty"`
	Weekly       int    `json:"weekly,omitempty"`
	Monthly      int    `json:"monthly,omitempty"`
	Yearly       int    `json:"yearly,omitempty"`
	MinimumCount int    `json:"minimum_count,omitempty"`
	MinimumAge   string `json:"minimum_age,omitempty"`
}

type RetryPolicy struct {
	Limit   int      `json:"limit"`
	Backoff *Backoff `json:"backoff,omitempty"`
}

type Backoff struct {
	Duration    string `json:"duration"`
	Factor      int    `json:"factor"`
	MaxDuration string `json:"max_duration"`
}

type SyncPolicy struct {
	Automated   bool     `json:"automated"`
	SelfHeal    bool     `json:"self_heal"`
	AutoPrune   bool     `json:"auto_prune"`
	SyncOptions []string `json:"sync_options,omitempty"`
}

type GetAddonSettingsResponse struct {
	AddonName string        `json:"addon_name"`
	Settings  AddonSettings `json:"settings"`
}

type UninstallAddonResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	// GitPushDeferred marks a designed git-push refusal: the uninstall is
	// applied and live, only the commit back to Git waits on the background
	// sync. Message then carries the platform's detail verbatim.
	GitPushDeferred bool `json:"git_push_deferred,omitempty"`
}

type AvailableAddon struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	ChartName   string  `json:"chart_name"`
	Version     string  `json:"version"`
	Category    *string `json:"category,omitempty"`
}

type ListAvailableAddonsResponse struct {
	Result []AvailableAddon `json:"result"`
}

// ListClusterAddons pages through the full addon listing (the backend
// serves 25 per page by default and clamps page_size at 100).
func (c *Client) ListClusterAddons(clusterID string) ([]ClusterAddonListItem, error) {
	listing, err := c.ListClusterAddonListing(clusterID)
	if err != nil {
		return nil, err
	}
	return listing.Addons, nil
}

// ListClusterAddonListing pages through the full addon listing like
// ListClusterAddons and keeps the advisory feed state each page reports.
func (c *Client) ListClusterAddonListing(clusterID string) (ClusterAddonListing, error) {
	var listing ClusterAddonListing
	// A stale page with no read time was judged from a feed never read
	// successfully, which outranks any read time another page reports.
	neverRead := false
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/api/v1/clusters/%s/addons?page=%d&page_size=100",
			c.BaseURL, neturl.PathEscape(clusterID), page)
		var resp ListClusterAddonsResponse
		if err := c.getJSON(url, &resp); err != nil {
			return ClusterAddonListing{}, fmt.Errorf("failed to get cluster addons: %w", err)
		}
		listing.Addons = append(listing.Addons, resp.Result...)
		if resp.SecurityAdvisoriesStale {
			listing.SecurityAdvisoriesStale = true
			if resp.SecurityAdvisoriesCheckedAt == nil {
				neverRead = true
			}
		}
		if checkedAt := resp.SecurityAdvisoriesCheckedAt; checkedAt != nil &&
			(listing.SecurityAdvisoriesCheckedAt == nil || checkedAt.Before(*listing.SecurityAdvisoriesCheckedAt)) {
			listing.SecurityAdvisoriesCheckedAt = checkedAt
		}
		if page >= resp.Pagination.TotalPages || len(resp.Result) == 0 {
			break
		}
	}
	if neverRead {
		listing.SecurityAdvisoriesCheckedAt = nil
	}
	return listing, nil
}

func (c *Client) ListAvailableAddons(clusterID string) ([]AvailableAddon, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/available", c.BaseURL, neturl.PathEscape(clusterID))
	var resp ListAvailableAddonsResponse
	if err := c.getJSON(url, &resp); err != nil {
		return nil, fmt.Errorf("failed to get available addons: %w", err)
	}
	return resp.Result, nil
}

func (c *Client) GetAddonSettings(clusterID, addonName string) (*GetAddonSettingsResponse, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s/settings",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(addonName))
	var resp GetAddonSettingsResponse
	if err := c.getJSON(url, &resp); err != nil {
		return nil, fmt.Errorf("failed to get addon settings: %w", err)
	}
	return &resp, nil
}

// UpdateAddonSettingsResult reports the outcome of an addon-settings update.
// The zero value is a plain success; GitPushDeferred marks a designed
// git-push refusal — the settings are applied and live, only the commit back
// to Git waits on the background sync — with the platform's detail verbatim
// in GitPushMessage.
type UpdateAddonSettingsResult struct {
	GitPushDeferred bool   `json:"git_push_deferred,omitempty"`
	GitPushMessage  string `json:"git_push_message,omitempty"`
}

func (c *Client) UpdateAddonSettings(ctx context.Context, clusterID, addonName string, settings AddonSettings) (*UpdateAddonSettingsResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s/settings",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(addonName))
	payload, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshal settings: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if deferral := gitPushDeferralFromResponse(resp.StatusCode, body); deferral != nil {
			return &UpdateAddonSettingsResult{GitPushDeferred: true, GitPushMessage: deferral.Message}, nil
		}
		return nil, newUnexpectedResponseError("update failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	return &UpdateAddonSettingsResult{}, nil
}

func (c *Client) UninstallAddon(ctx context.Context, clusterID, addonResourceID string, deletePermanently bool) (*UninstallAddonResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s?delete=%t",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(addonResourceID), deletePermanently)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if deferral := gitPushDeferralFromResponse(resp.StatusCode, body); deferral != nil {
			return &UninstallAddonResult{Success: true, Message: deferral.Message, GitPushDeferred: true}, nil
		}
		return nil, newUnexpectedResponseError("uninstall failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	return &UninstallAddonResult{Success: true, Message: "Addon uninstalled"}, nil
}

// addonConfigurationV2Response mirrors the response shape of
// GET /api/v1/org/clusters/imported/{cluster_id}/addons/{addon_name}/configuration
// returning AddonStandaloneConfiguration.values (base64-encoded plaintext YAML).
type addonConfigurationV2Response struct {
	Result *struct {
		ClusterAddonConfiguration struct {
			Values string `json:"values"`
		} `json:"cluster_addon_configuration"`
	} `json:"result"`
}

// GetClusterAddonValues returns the current values YAML for a cluster addon
// (base64-decoded plaintext). The DB always stores plaintext per
// assert_no_silent_plaintext in the SOPS encryption service; SOPS is only
// applied at git-push time, so this is safe to mutate locally and round-trip
// back through PATCH /stacks.
//
// encrypted_paths is NOT returned by this endpoint; callers should source it
// from the cluster IaC YAML when needed (it informs whether SOPS will
// re-encrypt on the next git push).
func (c *Client) GetClusterAddonValues(ctx context.Context, clusterID, addonName string) (string, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/addons/%s/configuration",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(addonName))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	body, err := readResponseBody(resp)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return "", newUnexpectedResponseError("get addon configuration failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var parsed addonConfigurationV2Response
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if parsed.Result == nil {
		return "", nil
	}
	encoded := parsed.Result.ClusterAddonConfiguration.Values
	if encoded == "" {
		return "", nil
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64-decode addon values: %w", err)
	}
	return string(decoded), nil
}

func (c *Client) GetAddonByName(clusterID, addonName string) (*ClusterAddonListItem, error) {
	addons, err := c.ListClusterAddons(clusterID)
	if err != nil {
		return nil, err
	}
	for i := range addons {
		if addons[i].Name == addonName {
			return &addons[i], nil
		}
	}
	return nil, fmt.Errorf("addon %q: %w", addonName, ErrAddonNotFound)
}

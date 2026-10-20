package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

type AgentInfo struct {
	UpgradeAvailable   bool    `json:"upgrade_available"`
	Upgrading          bool    `json:"upgrading"`
	CreatedAt          string  `json:"created_at"`
	CheckedInAt        *string `json:"checked_in_at,omitempty"`
	AgentVersion       *string `json:"agent_version,omitempty"`
	LatestAgentVersion *string `json:"latest_agent_version,omitempty"`
	DisableAutoUpgrade bool    `json:"disable_auto_upgrade"`
	IsOnline           *bool   `json:"is_online,omitempty"`
	// SecurityUpgradeRequired is true when the agent must be upgraded before
	// the platform switches its legacy NATS grant off, false when it already
	// holds the scoped grant, and nil when the platform cannot decide (or is
	// too old to report it). Nil is not "no upgrade needed".
	SecurityUpgradeRequired *bool `json:"security_upgrade_required,omitempty"`
}

type AgentToken struct {
	Token     string `json:"token"`
	ClusterID string `json:"cluster_id"`
	Command   string `json:"command"`
}

// tokenInInstallCommandPattern matches the --set config.token=<token> value
// inside the helm install command the token endpoints return.
var tokenInInstallCommandPattern = regexp.MustCompile(`--set config\.token=(\S+)`)

// fillFromCommand backfills Token and ClusterID for platform versions whose
// token endpoints only return the helm install command.
func (agentToken *AgentToken) fillFromCommand(clusterID string) {
	if agentToken.Token == "" && agentToken.Command != "" {
		if match := tokenInInstallCommandPattern.FindStringSubmatch(agentToken.Command); match != nil {
			agentToken.Token = match[1]
		}
	}
	if agentToken.ClusterID == "" {
		agentToken.ClusterID = clusterID
	}
}

type UpgradeAgentResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (c *Client) GetClusterAgent(clusterID string) (*AgentInfo, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/agent",
		c.BaseURL, clusterID)
	var agent AgentInfo
	if err := c.getJSON(url, &agent); err != nil {
		return nil, fmt.Errorf("failed to get agent info: %w", err)
	}
	return &agent, nil
}

func (c *Client) GetAgentToken(clusterID string) (*AgentToken, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/cluster-agent/token",
		c.BaseURL, clusterID)
	var agentToken AgentToken
	if err := c.getJSON(url, &agentToken); err != nil {
		return nil, fmt.Errorf("failed to get agent token: %w", err)
	}
	agentToken.fillFromCommand(clusterID)
	return &agentToken, nil
}

func (c *Client) GenerateAgentToken(ctx context.Context, clusterID string) (*AgentToken, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/cluster-agent/token",
		c.BaseURL, clusterID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
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
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, newUnexpectedResponseError("generate token failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var agentToken AgentToken
	if err := parseJSON(body, &agentToken); err != nil {
		return nil, err
	}
	agentToken.fillFromCommand(clusterID)
	return &agentToken, nil
}

// AgentSettingsResult is the envelope PATCH .../agent/settings answers.
type AgentSettingsResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// SetClusterAgentAutoUpgrade flips the cluster agent's automatic-upgrade
// opt-out: enabled=false stores disable_auto_upgrade=true, which the
// platform's fleet rollout honours; enabled=true clears it. The same flag
// the portal's cluster settings write (ankra-f5y9z).
func (c *Client) SetClusterAgentAutoUpgrade(ctx context.Context, clusterID string, enabled bool) (*AgentSettingsResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/agent/settings", c.BaseURL, clusterID)
	var result AgentSettingsResult
	if err := c.sendJSONContext(ctx, http.MethodPatch, url,
		map[string]bool{"disable_auto_upgrade": !enabled}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AgentUpgradeRequest is what a hand upgrade asks the platform for. The zero
// value is the plain upgrade: the platform installs the newer of the release
// the agent runs and the latest stable release, so it never moves an agent
// backwards. Version names an exact release; one older than the running
// release is refused unless AllowDowngrade is also set.
type AgentUpgradeRequest struct {
	Version        string
	AllowDowngrade bool
}

// IsPlain reports a request that names nothing, which is sent with no body,
// exactly as every CLI before --version sent it.
func (request AgentUpgradeRequest) IsPlain() bool {
	return request.Version == "" && !request.AllowDowngrade
}

// AgentUpgradeRefusedError is the platform refusing a hand upgrade before any
// command reached the agent: 409 for a version older than the running
// release without allow_downgrade, 422 for a version that is neither
// published nor running (or allow_downgrade without a version). Detail is
// the platform's own sentence.
type AgentUpgradeRefusedError struct {
	StatusCode int
	Detail     string
}

func (e *AgentUpgradeRefusedError) Error() string {
	if e == nil {
		return ""
	}
	return e.Detail
}

// IsDowngradeRefusal reports the 409: the named release is older than the one
// the agent runs, and the request did not allow a downgrade.
func (e *AgentUpgradeRefusedError) IsDowngradeRefusal() bool {
	return e != nil && e.StatusCode == http.StatusConflict
}

// UpgradeClusterAgent asks the platform to upgrade the cluster's agent now.
// A plain request posts no body; a request naming a version posts
// {"version", "allow_downgrade"}.
func (c *Client) UpgradeClusterAgent(ctx context.Context, clusterID string, upgradeRequest AgentUpgradeRequest) (*UpgradeAgentResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/agent/upgrade",
		c.BaseURL, clusterID)
	var requestBody io.Reader
	if !upgradeRequest.IsPlain() {
		payload := map[string]any{"allow_downgrade": upgradeRequest.AllowDowngrade}
		if upgradeRequest.Version != "" {
			payload["version"] = upgradeRequest.Version
		}
		encoded, marshalError := json.Marshal(payload)
		if marshalError != nil {
			return nil, fmt.Errorf("marshal request: %w", marshalError)
		}
		requestBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, requestBody)
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
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusUnprocessableEntity {
		if detail := detailFromBody(body); detail != "" {
			return nil, &AgentUpgradeRefusedError{StatusCode: resp.StatusCode, Detail: detail}
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("upgrade failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	return &UpgradeAgentResult{Success: true, Message: "Agent upgrade initiated"}, nil
}

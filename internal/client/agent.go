package client

import (
	"context"
	"fmt"
	"net/http"
)

type AgentInfo struct {
	UpgradeAvailable    bool    `json:"upgrade_available"`
	Upgrading           bool    `json:"upgrading"`
	CreatedAt           string  `json:"created_at"`
	CheckedInAt         *string `json:"checked_in_at,omitempty"`
	AgentVersion        *string `json:"agent_version,omitempty"`
	LatestAgentVersion  *string `json:"latest_agent_version,omitempty"`
	DisableAutoUpgrade  bool    `json:"disable_auto_upgrade"`
}

type AgentToken struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	ClusterID string `json:"cluster_id"`
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
		return nil, fmt.Errorf("generate token failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	var agentToken AgentToken
	if err := parseJSON(body, &agentToken); err != nil {
		return nil, err
	}
	return &agentToken, nil
}

func (c *Client) UpgradeClusterAgent(ctx context.Context, clusterID string) (*UpgradeAgentResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/agent/upgrade",
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upgrade failed: status %d, body: %s", resp.StatusCode, truncateForError(body, 500))
	}

	return &UpgradeAgentResult{Success: true, Message: "Agent upgrade initiated"}, nil
}

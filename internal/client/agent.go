package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
)

type AgentInfo struct {
	ID               string  `json:"id"`
	ClusterID        string  `json:"cluster_id"`
	Status           string  `json:"status"`
	Version          string  `json:"version"`
	LastSeen         *string `json:"last_seen,omitempty"`
	ConnectedAt      *string `json:"connected_at,omitempty"`
	Healthy          bool    `json:"healthy"`
	LatestVersion    *string `json:"latest_version,omitempty"`
	UpgradeAvailable bool    `json:"upgrade_available"`
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
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("generate token failed: status %d, body: %s", resp.StatusCode, string(body))
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
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upgrade failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return &UpgradeAgentResult{Success: true, Message: "Agent upgrade initiated"}, nil
}

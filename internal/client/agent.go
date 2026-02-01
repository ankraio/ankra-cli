package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// AgentInfo represents cluster agent information
type AgentInfo struct {
	ID              string  `json:"id"`
	ClusterID       string  `json:"cluster_id"`
	Status          string  `json:"status"`
	Version         string  `json:"version"`
	LastSeen        *string `json:"last_seen,omitempty"`
	ConnectedAt     *string `json:"connected_at,omitempty"`
	Healthy         bool    `json:"healthy"`
	LatestVersion   *string `json:"latest_version,omitempty"`
	UpgradeAvailable bool   `json:"upgrade_available"`
}

// AgentToken represents the agent identity token
type AgentToken struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	ClusterID string `json:"cluster_id"`
}

// UpgradeAgentResult is the response from upgrading an agent
type UpgradeAgentResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// GetClusterAgent returns information about a cluster's agent
func GetClusterAgent(token, baseURL, clusterID string) (*AgentInfo, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/agent",
		strings.TrimRight(baseURL, "/"), clusterID)
	var agent AgentInfo
	if err := getJSON(url, token, &agent); err != nil {
		return nil, fmt.Errorf("failed to get agent info: %w", err)
	}
	return &agent, nil
}

// GetAgentToken returns the agent identity token for a cluster
func GetAgentToken(token, baseURL, clusterID string) (*AgentToken, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/cluster-agent/token",
		strings.TrimRight(baseURL, "/"), clusterID)
	var agentToken AgentToken
	if err := getJSON(url, token, &agentToken); err != nil {
		return nil, fmt.Errorf("failed to get agent token: %w", err)
	}
	return &agentToken, nil
}

// GenerateAgentToken generates a new agent token for a cluster
func GenerateAgentToken(ctx context.Context, token, baseURL, clusterID string) (*AgentToken, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/cluster-agent/token",
		strings.TrimRight(baseURL, "/"), clusterID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
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

// UpgradeClusterAgent triggers an agent upgrade for a cluster
func UpgradeClusterAgent(ctx context.Context, token, baseURL, clusterID string) (*UpgradeAgentResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/agent/upgrade",
		strings.TrimRight(baseURL, "/"), clusterID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
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

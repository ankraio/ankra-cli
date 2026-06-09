package client

import (
	"context"
	"fmt"
	"net/http"
)

type KubeToken struct {
	Token     string `json:"token"`
	Server    string `json:"server"`
	ExpiresAt string `json:"expires_at"`
}

func (c *Client) GetClusterKubeToken(ctx context.Context, clusterID string) (*KubeToken, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/k8s-token", c.BaseURL, clusterID)
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
		return nil, fmt.Errorf("kube token request failed: status %d, body: %s", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var kubeToken KubeToken
	if err := parseJSON(body, &kubeToken); err != nil {
		return nil, err
	}
	return &kubeToken, nil
}

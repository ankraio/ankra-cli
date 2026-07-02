package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
)

type ClusterManifestListItem struct {
	Name              string   `json:"name"`
	ManifestBase64    string   `json:"manifest_base64"`
	Namespace         string   `json:"namespace"`
	Parents           []Parent `json:"parents"`
	DeletePermanently bool     `json:"delete_permanently"`
	State             string   `json:"state"`
}

type ListClusterManifestsResponse struct {
	Manifests []ClusterManifestListItem `json:"manifests"`
}

func (c *Client) ListClusterManifests(clusterID string) ([]ClusterManifestListItem, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/manifests", c.BaseURL, clusterID)

	var response ListClusterManifestsResponse
	if err := c.getJSON(url, &response); err != nil {
		return nil, fmt.Errorf("failed to list cluster manifests: %w", err)
	}

	return response.Manifests, nil
}

// getClusterManifestResponse mirrors GetClusterManifestResult from the
// backend; only the manifest_base64 field is required by the upgrade flow.
type getClusterManifestResponse struct {
	Manifest struct {
		ManifestBase64 string `json:"manifest_base64"`
	} `json:"manifest"`
}

// GetClusterManifestConfiguration returns the current manifest_base64 stored
// for a manifest. Used as the fallback content source when
// `ankra cluster manifests upgrade` is invoked without --from-file/--manifest
// (the backend's manifest_resource.update_from_external does not preserve an
// absent manifest_base64).
func (c *Client) GetClusterManifestConfiguration(ctx context.Context, clusterID, manifestName string) (string, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/manifests/%s/configuration",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(manifestName))
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
		return "", newUnexpectedResponseError("get manifest configuration failed", resp.StatusCode, truncateForError(body, 500))
	}

	var parsed getClusterManifestResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	return parsed.Manifest.ManifestBase64, nil
}

// DisconnectManifestResult mirrors DisconnectClusterStackManifestResult from
// the backend.
type DisconnectManifestResult struct {
	DisconnectedAt string `json:"disconnected_at"`
	CommitSHA      string `json:"commit_sha,omitempty"`
	CommitURL      string `json:"commit_url,omitempty"`
}

// DisconnectManifest removes a manifest from a stack, deleting its resources
// from the cluster. Backed by the bearer-token disconnect endpoint.
func (c *Client) DisconnectManifest(ctx context.Context, clusterID, stackName, manifestName string) (*DisconnectManifestResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/stacks/%s/manifests/%s/disconnect",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(stackName), neturl.PathEscape(manifestName))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
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
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newUnexpectedResponseError("disconnect manifest failed", resp.StatusCode, truncateForError(body, 500))
	}

	var parsed DisconnectManifestResult
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &parsed, nil
}

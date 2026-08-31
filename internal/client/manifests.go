package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"time"
)

// ClusterManifestListItem mirrors the backend's cliread ManifestItem: the
// listing carries no namespace or parents — the stack a manifest belongs to
// arrives as stack_name (nil for standalone manifests).
type ClusterManifestListItem struct {
	Name              string     `json:"name"`
	ManifestBase64    string     `json:"manifest_base64"`
	State             string     `json:"state"`
	DeletePermanently bool       `json:"delete_permanently"`
	CreatedAt         *time.Time `json:"created_at"`
	StackName         *string    `json:"stack_name"`
}

// ListClusterManifestsResponse mirrors ListClusterManifestsResult: the
// backend wraps the page in a result/pagination envelope.
type ListClusterManifestsResponse struct {
	Result     []ClusterManifestListItem `json:"result"`
	Pagination Pagination                `json:"pagination"`
}

// ListClusterManifests pages through the full manifest listing (the backend
// serves a default page size and clamps page_size at 100). The previous
// implementation sent no paging parameters and returned only the first page,
// silently truncating clusters with more manifests than one page.
func (c *Client) ListClusterManifests(clusterID string) ([]ClusterManifestListItem, error) {
	var manifests []ClusterManifestListItem
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/api/v1/clusters/%s/manifests?page=%d&page_size=100",
			c.BaseURL, neturl.PathEscape(clusterID), page)
		var response ListClusterManifestsResponse
		if err := c.getJSON(url, &response); err != nil {
			return nil, fmt.Errorf("failed to list cluster manifests: %w", err)
		}
		manifests = append(manifests, response.Result...)
		if page >= response.Pagination.TotalPages || len(response.Result) == 0 {
			break
		}
	}
	return manifests, nil
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
		return "", newUnexpectedResponseError("get manifest configuration failed", resp.StatusCode, redactedBodyForError(body, 500))
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
	// GitPushDeferred marks a designed git-push refusal: the disconnect is
	// applied and live, only the commit back to Git waits on the background
	// sync (no commit fields in that case). GitPushMessage carries the
	// platform's detail verbatim.
	GitPushDeferred bool   `json:"git_push_deferred,omitempty"`
	GitPushMessage  string `json:"git_push_message,omitempty"`
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
		if deferral := gitPushDeferralFromResponse(resp.StatusCode, body); deferral != nil {
			return &DisconnectManifestResult{GitPushDeferred: true, GitPushMessage: deferral.Message}, nil
		}
		return nil, newUnexpectedResponseError("disconnect manifest failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var parsed DisconnectManifestResult
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &parsed, nil
}

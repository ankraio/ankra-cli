package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// ResyncSSHKeysResult is returned by the cluster ssh-keys resync endpoint.
// It lists the SSH key resource ids that were flagged for re-sync.
type ResyncSSHKeysResult struct {
	ResourceIDs []string `json:"resource_ids"`
}

func (c *Client) getClusterSSHKeys(kind, clusterID string) (*ClusterSSHKeysResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/%s/ssh-keys", c.BaseURL, kind, url.PathEscape(clusterID))
	var result ClusterSSHKeysResult
	if err := c.getJSON(endpoint, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) updateClusterSSHKeys(kind, clusterID string, sshKeyCredentialIDs []string) (*UpdateClusterSSHKeysResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/%s/ssh-keys", c.BaseURL, kind, url.PathEscape(clusterID))
	payload, err := json.Marshal(UpdateClusterSSHKeysRequest{SSHKeyCredentialIDs: sshKeyCredentialIDs})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(payload))
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
		return nil, newUnexpectedResponseError("update ssh keys failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result UpdateClusterSSHKeysResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) resyncClusterSSHKeys(kind, clusterID string) (*ResyncSSHKeysResult, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/%s/ssh-keys/resync", c.BaseURL, kind, url.PathEscape(clusterID))

	req, err := http.NewRequest(http.MethodPost, endpoint, nil)
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
		return nil, newUnexpectedResponseError("resync ssh keys failed", resp.StatusCode, redactedBodyForError(body, 500))
	}

	var result ResyncSSHKeysResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func (c *Client) GetHetznerClusterSSHKeys(clusterID string) (*ClusterSSHKeysResult, error) {
	return c.getClusterSSHKeys("hetzner", clusterID)
}

func (c *Client) UpdateHetznerClusterSSHKeys(clusterID string, sshKeyCredentialIDs []string) (*UpdateClusterSSHKeysResult, error) {
	return c.updateClusterSSHKeys("hetzner", clusterID, sshKeyCredentialIDs)
}

func (c *Client) ResyncHetznerClusterSSHKeys(clusterID string) (*ResyncSSHKeysResult, error) {
	return c.resyncClusterSSHKeys("hetzner", clusterID)
}

func (c *Client) ResyncOvhClusterSSHKeys(clusterID string) (*ResyncSSHKeysResult, error) {
	return c.resyncClusterSSHKeys("ovh", clusterID)
}

func (c *Client) GetUpcloudClusterSSHKeys(clusterID string) (*ClusterSSHKeysResult, error) {
	return c.getClusterSSHKeys("upcloud", clusterID)
}

func (c *Client) UpdateUpcloudClusterSSHKeys(clusterID string, sshKeyCredentialIDs []string) (*UpdateClusterSSHKeysResult, error) {
	return c.updateClusterSSHKeys("upcloud", clusterID, sshKeyCredentialIDs)
}

func (c *Client) ResyncUpcloudClusterSSHKeys(clusterID string) (*ResyncSSHKeysResult, error) {
	return c.resyncClusterSSHKeys("upcloud", clusterID)
}

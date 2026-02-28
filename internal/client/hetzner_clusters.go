package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type CreateHetznerClusterRequest struct {
	Name                   string  `json:"name"`
	Description            *string `json:"description,omitempty"`
	CredentialID           string  `json:"credential_id"`
	SSHKeyCredentialID     string  `json:"ssh_key_credential_id"`
	Location               string  `json:"location"`
	NetworkIPRange         string  `json:"network_ip_range"`
	SubnetRange            string  `json:"subnet_range"`
	BastionServerType      string  `json:"bastion_server_type"`
	ControlPlaneCount      int     `json:"control_plane_count"`
	ControlPlaneServerType string  `json:"control_plane_server_type"`
	WorkerCount            int     `json:"worker_count"`
	WorkerServerType       string  `json:"worker_server_type"`
	Distribution           string  `json:"distribution"`
	KubernetesVersion      *string `json:"kubernetes_version,omitempty"`
}

type CreateHetznerClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

type DeprovisionHetznerClusterResponse struct {
	Success         bool   `json:"success"`
	ClusterID       string `json:"cluster_id"`
	DeletedServers  []int  `json:"deleted_servers"`
	DeletedNetworks []int  `json:"deleted_networks"`
	DeletedSSHKeys  []int  `json:"deleted_ssh_keys"`
	Errors          []string `json:"errors"`
}

type WorkerCountResult struct {
	WorkerCount int `json:"worker_count"`
	Min         int `json:"min"`
	Max         int `json:"max"`
}

type ScaleWorkersRequest struct {
	WorkerCount int `json:"worker_count"`
}

type ScaleWorkersResult struct {
	PreviousCount int `json:"previous_count"`
	NewCount      int `json:"new_count"`
}

func CreateHetznerCluster(token, baseURL string, req CreateHetznerClusterRequest) (*CreateHetznerClusterResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/clusters/hetzner"
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(httpReq)
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
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result CreateHetznerClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func DeprovisionHetznerCluster(token, baseURL, clusterID string) (*DeprovisionHetznerClusterResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s", strings.TrimRight(baseURL, "/"), clusterID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
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
		return nil, fmt.Errorf("deprovision failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result DeprovisionHetznerClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func GetHetznerWorkerCount(token, baseURL, clusterID string) (*WorkerCountResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/worker-count", strings.TrimRight(baseURL, "/"), clusterID)
	var result WorkerCountResult
	if err := getJSON(url, token, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func ScaleHetznerWorkers(token, baseURL, clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/scale-workers", strings.TrimRight(baseURL, "/"), clusterID)
	payload, err := json.Marshal(ScaleWorkersRequest{WorkerCount: workerCount})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
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
		return nil, fmt.Errorf("scale failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result ScaleWorkersResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

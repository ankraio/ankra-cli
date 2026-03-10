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

type CreateOvhClusterRequest struct {
	Name                 string  `json:"name"`
	Description          *string `json:"description,omitempty"`
	CredentialID         string  `json:"credential_id"`
	SSHKeyCredentialID   string  `json:"ssh_key_credential_id"`
	Region               string  `json:"region"`
	NetworkVlanID        int     `json:"network_vlan_id"`
	SubnetCIDR           string  `json:"subnet_cidr"`
	DHCPStart            string  `json:"dhcp_start"`
	DHCPEnd              string  `json:"dhcp_end"`
	GatewayFlavorID      string  `json:"gateway_flavor_id"`
	ControlPlaneCount    int     `json:"control_plane_count"`
	ControlPlaneFlavorID string  `json:"control_plane_flavor_id"`
	WorkerCount          int     `json:"worker_count"`
	WorkerFlavorID       string  `json:"worker_flavor_id"`
	Distribution         string  `json:"distribution"`
	KubernetesVersion    *string `json:"kubernetes_version,omitempty"`
}

type CreateOvhClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

type DeprovisionOvhClusterResponse struct {
	Success         bool     `json:"success"`
	ClusterID       string   `json:"cluster_id"`
	DeletedServers  []string `json:"deleted_servers"`
	DeletedNetworks []string `json:"deleted_networks"`
	DeletedSSHKeys  []string `json:"deleted_ssh_keys"`
	Errors          []string `json:"errors"`
}

func CreateOvhCluster(token, baseURL string, req CreateOvhClusterRequest) (*CreateOvhClusterResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/clusters/ovh"
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

	var result CreateOvhClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func DeprovisionOvhCluster(token, baseURL, clusterID string) (*DeprovisionOvhClusterResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s", strings.TrimRight(baseURL, "/"), clusterID)
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

	var result DeprovisionOvhClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func GetOvhWorkerCount(token, baseURL, clusterID string) (*WorkerCountResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/worker-count", strings.TrimRight(baseURL, "/"), clusterID)
	var result WorkerCountResult
	if err := getJSON(url, token, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func GetOvhK8sVersion(token, baseURL, clusterID string) (*K8sVersionInfo, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/k8s-version", strings.TrimRight(baseURL, "/"), clusterID)
	var result K8sVersionInfo
	if err := getJSON(url, token, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func UpgradeOvhK8sVersion(token, baseURL, clusterID, targetVersion string) (*UpgradeK8sVersionResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/upgrade-k8s-version", strings.TrimRight(baseURL, "/"), clusterID)
	payload, err := json.Marshal(UpgradeK8sVersionRequest{TargetVersion: targetVersion})
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
		return nil, fmt.Errorf("upgrade failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result UpgradeK8sVersionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func ListOvhNodeGroups(token, baseURL, clusterID string) (*NodeGroupListResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups", strings.TrimRight(baseURL, "/"), clusterID)
	var result NodeGroupListResult
	if err := getJSON(url, token, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func AddOvhNodeGroup(token, baseURL, clusterID string, req AddNodeGroupRequest) (*AddNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups", strings.TrimRight(baseURL, "/"), clusterID)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return postJSON[AddNodeGroupResult](url, token, payload)
}

func ScaleOvhNodeGroup(token, baseURL, clusterID, groupName string, count int) (*ScaleNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s/scale", strings.TrimRight(baseURL, "/"), clusterID, groupName)
	payload, err := json.Marshal(ScaleNodeGroupRequest{Count: count})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return putJSON[ScaleNodeGroupResult](url, token, payload)
}

func UpdateOvhNodeGroupInstanceType(token, baseURL, clusterID, groupName, instanceType string) (*UpdateNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s/instance-type", strings.TrimRight(baseURL, "/"), clusterID, groupName)
	payload, err := json.Marshal(UpdateInstanceTypeRequest{InstanceType: instanceType})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return putJSON[UpdateNodeGroupResult](url, token, payload)
}

func DeleteOvhNodeGroup(token, baseURL, clusterID, groupName string) (*DeleteNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/node-groups/%s", strings.TrimRight(baseURL, "/"), clusterID, groupName)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("delete failed: status %d, body: %s", resp.StatusCode, string(body))
	}
	var result DeleteNodeGroupResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func ScaleOvhWorkers(token, baseURL, clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/ovh/%s/scale-workers", strings.TrimRight(baseURL, "/"), clusterID)
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

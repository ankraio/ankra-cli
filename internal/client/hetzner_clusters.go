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

type NodeTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

type CreateNodeGroupRequest struct {
	Name         string            `json:"name"`
	InstanceType string            `json:"instance_type"`
	Count        int               `json:"count"`
	Labels       map[string]string `json:"labels,omitempty"`
	Taints       []NodeTaint       `json:"taints,omitempty"`
}

type CreateHetznerClusterRequest struct {
	Name                   string                  `json:"name"`
	Description            *string                 `json:"description,omitempty"`
	CredentialID           string                  `json:"credential_id"`
	SSHKeyCredentialID     string                  `json:"ssh_key_credential_id,omitempty"`
	SSHKeyCredentialIDs    []string                `json:"ssh_key_credential_ids,omitempty"`
	Location               string                  `json:"location"`
	NetworkIPRange         string                  `json:"network_ip_range"`
	SubnetRange            string                  `json:"subnet_range"`
	BastionServerType      string                  `json:"bastion_server_type"`
	ControlPlaneCount      int                     `json:"control_plane_count"`
	ControlPlaneServerType string                  `json:"control_plane_server_type"`
	WorkerCount            int                     `json:"worker_count"`
	WorkerServerType       string                  `json:"worker_server_type"`
	Distribution           string                  `json:"distribution"`
	KubernetesVersion      *string                 `json:"kubernetes_version,omitempty"`
	NodeGroups             []CreateNodeGroupRequest `json:"node_groups,omitempty"`
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

type K8sVersionInfo struct {
	CurrentVersion *string `json:"current_version"`
	Distribution   string  `json:"distribution"`
}

type UpgradeK8sVersionRequest struct {
	TargetVersion string `json:"target_version"`
}

type UpgradeK8sVersionResult struct {
	PreviousVersion *string `json:"previous_version"`
	NewVersion      string  `json:"new_version"`
	NodesAffected   int     `json:"nodes_affected"`
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

func GetHetznerK8sVersion(token, baseURL, clusterID string) (*K8sVersionInfo, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/k8s-version", strings.TrimRight(baseURL, "/"), clusterID)
	var result K8sVersionInfo
	if err := getJSON(url, token, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func UpgradeHetznerK8sVersion(token, baseURL, clusterID, targetVersion string) (*UpgradeK8sVersionResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/upgrade-k8s-version", strings.TrimRight(baseURL, "/"), clusterID)
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

// --- Node Group Operations ---

type NodeGroupInfo struct {
	Name         string            `json:"name"`
	InstanceType string            `json:"instance_type"`
	Count        int               `json:"count"`
	Min          int               `json:"min"`
	Max          int               `json:"max"`
	Labels       map[string]string `json:"labels"`
	Taints       []NodeTaint       `json:"taints"`
}

type NodeGroupListResult struct {
	NodeGroups []NodeGroupInfo `json:"node_groups"`
}

type AddNodeGroupRequest struct {
	Name         string            `json:"name"`
	InstanceType string            `json:"instance_type"`
	Count        int               `json:"count"`
	Labels       map[string]string `json:"labels,omitempty"`
	Taints       []NodeTaint       `json:"taints,omitempty"`
}

type AddNodeGroupResult struct {
	GroupName string `json:"group_name"`
	Count     int    `json:"count"`
}

type ScaleNodeGroupRequest struct {
	Count int `json:"count"`
}

type ScaleNodeGroupResult struct {
	GroupName     string `json:"group_name"`
	PreviousCount int    `json:"previous_count"`
	NewCount      int    `json:"new_count"`
}

type UpdateInstanceTypeRequest struct {
	InstanceType string `json:"instance_type"`
}

type UpdateNodeGroupResult struct {
	GroupName string `json:"group_name"`
	Updated   int    `json:"updated"`
}

type DeleteNodeGroupResult struct {
	GroupName string `json:"group_name"`
	Deleted   int    `json:"deleted"`
}

func ListHetznerNodeGroups(token, baseURL, clusterID string) (*NodeGroupListResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/node-groups", strings.TrimRight(baseURL, "/"), clusterID)
	var result NodeGroupListResult
	if err := getJSON(url, token, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func AddHetznerNodeGroup(token, baseURL, clusterID string, req AddNodeGroupRequest) (*AddNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/node-groups", strings.TrimRight(baseURL, "/"), clusterID)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return postJSON[AddNodeGroupResult](url, token, payload)
}

func ScaleHetznerNodeGroup(token, baseURL, clusterID, groupName string, count int) (*ScaleNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/node-groups/%s/scale", strings.TrimRight(baseURL, "/"), clusterID, groupName)
	payload, err := json.Marshal(ScaleNodeGroupRequest{Count: count})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return putJSON[ScaleNodeGroupResult](url, token, payload)
}

func UpdateHetznerNodeGroupInstanceType(token, baseURL, clusterID, groupName, instanceType string) (*UpdateNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/node-groups/%s/instance-type", strings.TrimRight(baseURL, "/"), clusterID, groupName)
	payload, err := json.Marshal(UpdateInstanceTypeRequest{InstanceType: instanceType})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return putJSON[UpdateNodeGroupResult](url, token, payload)
}

func DeleteHetznerNodeGroup(token, baseURL, clusterID, groupName string) (*DeleteNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/hetzner/%s/node-groups/%s", strings.TrimRight(baseURL, "/"), clusterID, groupName)
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
		return nil, fmt.Errorf("delete failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result DeleteNodeGroupResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func postJSON[T any](url, token string, payload []byte) (*T, error) {
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
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("request failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result T
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func putJSON[T any](url, token string, payload []byte) (*T, error) {
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result T
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

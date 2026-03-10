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

type CreateUpcloudClusterRequest struct {
	Name               string  `json:"name"`
	Description        *string `json:"description,omitempty"`
	CredentialID       string  `json:"credential_id"`
	SSHKeyCredentialID string  `json:"ssh_key_credential_id"`
	Zone               string  `json:"zone"`
	NetworkIPRange     string  `json:"network_ip_range"`
	BastionPlan        string  `json:"bastion_plan"`
	ControlPlaneCount  int     `json:"control_plane_count"`
	ControlPlanePlan   string  `json:"control_plane_plan"`
	WorkerCount        int     `json:"worker_count"`
	WorkerPlan         string  `json:"worker_plan"`
	Distribution       string  `json:"distribution"`
	KubernetesVersion  *string `json:"kubernetes_version,omitempty"`
}

type CreateUpcloudClusterResponse struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
}

type DeprovisionUpcloudClusterResponse struct {
	Success        bool     `json:"success"`
	ClusterID      string   `json:"cluster_id"`
	OperationID    *string  `json:"operation_id,omitempty"`
	ResourcesMarked int    `json:"resources_marked"`
	Errors         []string `json:"errors"`
}

func CreateUpcloudCluster(token, baseURL string, req CreateUpcloudClusterRequest) (*CreateUpcloudClusterResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/clusters/upcloud"
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

	var result CreateUpcloudClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func DeprovisionUpcloudCluster(token, baseURL, clusterID string) (*DeprovisionUpcloudClusterResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s", strings.TrimRight(baseURL, "/"), clusterID)
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

	var result DeprovisionUpcloudClusterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

func GetUpcloudWorkerCount(token, baseURL, clusterID string) (*WorkerCountResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/worker-count", strings.TrimRight(baseURL, "/"), clusterID)
	var result WorkerCountResult
	if err := getJSON(url, token, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func GetUpcloudK8sVersion(token, baseURL, clusterID string) (*K8sVersionInfo, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/k8s-version", strings.TrimRight(baseURL, "/"), clusterID)
	var result K8sVersionInfo
	if err := getJSON(url, token, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func UpgradeUpcloudK8sVersion(token, baseURL, clusterID, targetVersion string) (*UpgradeK8sVersionResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/upgrade-k8s-version", strings.TrimRight(baseURL, "/"), clusterID)
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

func ListUpcloudNodeGroups(token, baseURL, clusterID string) (*NodeGroupListResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/node-groups", strings.TrimRight(baseURL, "/"), clusterID)
	var result NodeGroupListResult
	if err := getJSON(url, token, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func AddUpcloudNodeGroup(token, baseURL, clusterID string, req AddNodeGroupRequest) (*AddNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/node-groups", strings.TrimRight(baseURL, "/"), clusterID)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return postJSON[AddNodeGroupResult](url, token, payload)
}

func ScaleUpcloudNodeGroup(token, baseURL, clusterID, groupName string, count int) (*ScaleNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/node-groups/%s/scale", strings.TrimRight(baseURL, "/"), clusterID, groupName)
	payload, err := json.Marshal(ScaleNodeGroupRequest{Count: count})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return putJSON[ScaleNodeGroupResult](url, token, payload)
}

func UpdateUpcloudNodeGroupInstanceType(token, baseURL, clusterID, groupName, instanceType string) (*UpdateNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/node-groups/%s/instance-type", strings.TrimRight(baseURL, "/"), clusterID, groupName)
	payload, err := json.Marshal(UpdateInstanceTypeRequest{InstanceType: instanceType})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return putJSON[UpdateNodeGroupResult](url, token, payload)
}

func DeleteUpcloudNodeGroup(token, baseURL, clusterID, groupName string) (*DeleteNodeGroupResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/node-groups/%s", strings.TrimRight(baseURL, "/"), clusterID, groupName)
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

func ScaleUpcloudWorkers(token, baseURL, clusterID string, workerCount int) (*ScaleWorkersResult, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/upcloud/%s/scale-workers", strings.TrimRight(baseURL, "/"), clusterID)
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

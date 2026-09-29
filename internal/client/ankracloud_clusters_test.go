package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func decodeAnkraCloudBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
		t.Fatalf("decode request body: %v", decodeError)
	}
	return body
}

func TestCreateAnkraCloudClusterSendsKubeadmBody(t *testing.T) {
	var receivedBody map[string]any
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/clusters/ankracloud" {
			t.Errorf("request = %s %s, want POST /api/v1/clusters/ankracloud", request.Method, request.URL.Path)
		}
		receivedBody = decodeAnkraCloudBody(t, request)
		jsonResponse(t, responseWriter, http.StatusOK, CreateAnkraCloudClusterResponse{ClusterID: "cluster-1", Name: "prod"})
	})

	networkIPRange := "10.20.0.0/20"
	workerCount := 3
	result, createError := testClient.CreateAnkraCloudCluster(CreateAnkraCloudClusterRequest{
		Name:               "prod",
		CredentialID:       "credential-1",
		SSHKeyCredentialID: "ssh-key-1",
		Zone:               "fi-hel1",
		NetworkIPRange:     &networkIPRange,
		BastionPlan:        "s-1",
		ControlPlaneCount:  3,
		ControlPlanePlan:   "s-4",
		WorkerCount:        &workerCount,
		WorkerPlan:         "s-8",
		Distribution:       AnkraCloudDistributionKubeadm,
	})
	if createError != nil {
		t.Fatalf("CreateAnkraCloudCluster: %v", createError)
	}
	if result.ClusterID != "cluster-1" {
		t.Errorf("ClusterID = %q, want cluster-1", result.ClusterID)
	}

	expected := map[string]any{
		"name":                  "prod",
		"credential_id":         "credential-1",
		"ssh_key_credential_id": "ssh-key-1",
		"zone":                  "fi-hel1",
		"network_ip_range":      "10.20.0.0/20",
		"bastion_plan":          "s-1",
		"control_plane_count":   float64(3),
		"control_plane_plan":    "s-4",
		"worker_count":          float64(3),
		"worker_plan":           "s-8",
		"distribution":          "kubeadm",
	}
	for key, value := range expected {
		if receivedBody[key] != value {
			t.Errorf("body[%s] = %v, want %v", key, receivedBody[key], value)
		}
	}
	for _, omitted := range []string{"private_network_id", "template", "etcd_topology", "cni", "retention_policy", "gitops_repository"} {
		if _, present := receivedBody[omitted]; present {
			t.Errorf("body carries %s = %v, want it omitted so the server default applies", omitted, receivedBody[omitted])
		}
	}
}

func TestCreateAnkraCloudClusterSendsK3sWithZeroWorkers(t *testing.T) {
	var receivedBody map[string]any
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		receivedBody = decodeAnkraCloudBody(t, request)
		jsonResponse(t, responseWriter, http.StatusOK, CreateAnkraCloudClusterResponse{ClusterID: "cluster-2", Name: "edge"})
	})

	privateNetworkID := "network-1"
	workerCount := 0
	if _, createError := testClient.CreateAnkraCloudCluster(CreateAnkraCloudClusterRequest{
		Name:               "edge",
		CredentialID:       "credential-1",
		SSHKeyCredentialID: "ssh-key-1",
		Zone:               "fi-hel1",
		PrivateNetworkID:   &privateNetworkID,
		BastionPlan:        "s-1",
		ControlPlanePlan:   "s-2",
		WorkerCount:        &workerCount,
		Distribution:       AnkraCloudDistributionK3s,
	}); createError != nil {
		t.Fatalf("CreateAnkraCloudCluster: %v", createError)
	}
	if receivedBody["distribution"] != "k3s" {
		t.Errorf("distribution = %v, want k3s", receivedBody["distribution"])
	}
	if receivedBody["private_network_id"] != "network-1" {
		t.Errorf("private_network_id = %v, want network-1", receivedBody["private_network_id"])
	}
	if count, present := receivedBody["worker_count"]; !present || count != float64(0) {
		t.Errorf("worker_count = %v (present %v), want an explicit 0", count, present)
	}
}

func TestPreflightAnkraCloudCluster(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/clusters/ankracloud/preflight" {
			t.Errorf("request = %s %s, want POST /api/v1/clusters/ankracloud/preflight", request.Method, request.URL.Path)
		}
		jsonResponse(t, responseWriter, http.StatusOK, map[string]any{
			"can_proceed": false,
			"items":       []map[string]string{{"check": "zone", "status": "fail", "message": "unknown zone"}},
		})
	})

	result, preflightError := testClient.PreflightAnkraCloudCluster(CreateAnkraCloudClusterRequest{Name: "prod"})
	if preflightError != nil {
		t.Fatalf("PreflightAnkraCloudCluster: %v", preflightError)
	}
	if result.CanProceed || len(result.Items) != 1 || result.Items[0].Check != "zone" {
		t.Errorf("result = %+v, want one failing zone check", result)
	}
}

func TestPreflightAnkraCloudClusterSurfacesRefusal(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		jsonResponse(t, responseWriter, http.StatusUnprocessableEntity, map[string]string{"detail": "bastion_plan is required"})
	})

	if _, preflightError := testClient.PreflightAnkraCloudCluster(CreateAnkraCloudClusterRequest{}); preflightError == nil {
		t.Fatal("PreflightAnkraCloudCluster succeeded on a 422, want an error")
	}
}

func TestAnkraCloudCatalogsReadTheirRoutes(t *testing.T) {
	testCases := []struct {
		name          string
		call          func(testClient *Client) (*AnkraCloudCatalogResult, error)
		expectedPath  string
		expectedQuery string
	}{
		{"zones", func(testClient *Client) (*AnkraCloudCatalogResult, error) {
			return testClient.ListAnkraCloudZones("credential-1")
		}, "/api/v1/clusters/ankracloud/zones", "credential_id=credential-1"},
		{"plans", func(testClient *Client) (*AnkraCloudCatalogResult, error) {
			return testClient.ListAnkraCloudPlans("credential-1")
		}, "/api/v1/clusters/ankracloud/plans", "credential_id=credential-1"},
		{"templates", func(testClient *Client) (*AnkraCloudCatalogResult, error) {
			return testClient.ListAnkraCloudTemplates("credential-1")
		}, "/api/v1/clusters/ankracloud/templates", "credential_id=credential-1"},
		{"networks", func(testClient *Client) (*AnkraCloudCatalogResult, error) {
			return testClient.ListAnkraCloudNetworks("credential-1", "fi-hel1")
		}, "/api/v1/clusters/ankracloud/networks", "credential_id=credential-1&zone=fi-hel1"},
		{"pricing", func(testClient *Client) (*AnkraCloudCatalogResult, error) {
			return testClient.ListAnkraCloudPricing("credential-1")
		}, "/api/v1/clusters/ankracloud/pricing", "credential_id=credential-1"},
		{"cluster plans", func(testClient *Client) (*AnkraCloudCatalogResult, error) {
			return testClient.ListAnkraCloudClusterPlans("cluster-1")
		}, "/api/v1/clusters/ankracloud/cluster-1/plans", ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.Path != testCase.expectedPath {
					t.Errorf("request = %s %s, want GET %s", request.Method, request.URL.Path, testCase.expectedPath)
				}
				if request.URL.RawQuery != testCase.expectedQuery {
					t.Errorf("query = %q, want %q", request.URL.RawQuery, testCase.expectedQuery)
				}
				jsonResponse(t, responseWriter, http.StatusOK, map[string]any{
					"plans":                     []map[string]any{{"name": "s-2", "cores": 2, "price_monthly_cents": 900, "available": true}},
					"storage_prices":            []map[string]any{{"tier": "ssd", "gb_month_cents": 10}},
					"public_ipv4_monthly_cents": 300,
					"pricing_complete":          false,
					"incomplete_reasons":        []string{"hourly"},
				})
			})
			result, catalogError := testCase.call(testClient)
			if catalogError != nil {
				t.Fatalf("catalog: %v", catalogError)
			}
			if len(result.Plans) != 1 || result.Plans[0].PriceMonthlyCents != 900 || result.PricingComplete {
				t.Errorf("result = %+v, want the decoded envelope", result)
			}
			if result.PublicIPv4MonthlyCents == nil || *result.PublicIPv4MonthlyCents != 300 {
				t.Errorf("PublicIPv4MonthlyCents = %v, want 300", result.PublicIPv4MonthlyCents)
			}
		})
	}
}

func TestAnkraCloudDayTwoCallsUseTheAnkraCloudPrefix(t *testing.T) {
	requestContext := context.Background()
	testCases := []struct {
		name           string
		call           func(testClient *Client) error
		expectedMethod string
		expectedPath   string
	}{
		{"stop", func(testClient *Client) error {
			_, callError := testClient.StopAnkraCloudCluster("cluster-1", StopClusterOptions{})
			return callError
		}, http.MethodPost, "/api/v1/clusters/ankracloud/cluster-1/stop"},
		{"start", func(testClient *Client) error {
			_, callError := testClient.StartAnkraCloudCluster("cluster-1", StartClusterOptions{Scope: "all"})
			return callError
		}, http.MethodPost, "/api/v1/clusters/ankracloud/cluster-1/start"},
		{"deprovision", func(testClient *Client) error {
			_, callError := testClient.DeprovisionAnkraCloudCluster("cluster-1", DeprovisionOptions{})
			return callError
		}, http.MethodDelete, "/api/v1/clusters/ankracloud/cluster-1"},
		{"worker count", func(testClient *Client) error {
			_, callError := testClient.GetAnkraCloudWorkerCount("cluster-1")
			return callError
		}, http.MethodGet, "/api/v1/clusters/ankracloud/cluster-1/worker-count"},
		{"scale workers", func(testClient *Client) error {
			_, callError := testClient.ScaleAnkraCloudWorkers("cluster-1", 4)
			return callError
		}, http.MethodPost, "/api/v1/clusters/ankracloud/cluster-1/scale-workers"},
		{"k8s version", func(testClient *Client) error {
			_, callError := testClient.GetAnkraCloudK8sVersion("cluster-1")
			return callError
		}, http.MethodGet, "/api/v1/clusters/ankracloud/cluster-1/k8s-version"},
		{"upgrade", func(testClient *Client) error {
			_, callError := testClient.UpgradeAnkraCloudK8sVersion("cluster-1", "1.33.1", false)
			return callError
		}, http.MethodPost, "/api/v1/clusters/ankracloud/cluster-1/upgrade-k8s-version"},
		{"node groups", func(testClient *Client) error {
			_, callError := testClient.ListAnkraCloudNodeGroups("cluster-1")
			return callError
		}, http.MethodGet, "/api/v1/clusters/ankracloud/cluster-1/node-groups"},
		{"add node group", func(testClient *Client) error {
			_, _, callError := testClient.AddAnkraCloudNodeGroup(requestContext, "cluster-1", AddNodeGroupRequest{Name: "gpu", InstanceType: "g-1", Count: 1}, false)
			return callError
		}, http.MethodPost, "/api/v1/clusters/ankracloud/cluster-1/node-groups"},
		{"scale node group", func(testClient *Client) error {
			_, _, callError := testClient.ScaleAnkraCloudNodeGroup(requestContext, "cluster-1", "gpu", 2, false)
			return callError
		}, http.MethodPut, "/api/v1/clusters/ankracloud/cluster-1/node-groups/gpu/scale"},
		{"node group plan", func(testClient *Client) error {
			_, _, callError := testClient.UpdateAnkraCloudNodeGroupInstanceType(requestContext, "cluster-1", "gpu", "g-2", false)
			return callError
		}, http.MethodPut, "/api/v1/clusters/ankracloud/cluster-1/node-groups/gpu/instance-type"},
		{"node group labels", func(testClient *Client) error {
			_, _, callError := testClient.UpdateAnkraCloudNodeGroupLabels(requestContext, "cluster-1", "gpu", map[string]string{"tier": "gpu"}, false)
			return callError
		}, http.MethodPut, "/api/v1/clusters/ankracloud/cluster-1/node-groups/gpu/labels"},
		{"node group taints", func(testClient *Client) error {
			_, _, callError := testClient.UpdateAnkraCloudNodeGroupTaints(requestContext, "cluster-1", "gpu", []NodeTaint{{Key: "gpu", Effect: "NoSchedule"}}, false)
			return callError
		}, http.MethodPut, "/api/v1/clusters/ankracloud/cluster-1/node-groups/gpu/taints"},
		{"delete node group", func(testClient *Client) error {
			_, _, callError := testClient.DeleteAnkraCloudNodeGroup(requestContext, "cluster-1", "gpu", false)
			return callError
		}, http.MethodDelete, "/api/v1/clusters/ankracloud/cluster-1/node-groups/gpu"},
		{"autoscaling", func(testClient *Client) error {
			_, callError := testClient.GetAnkraCloudNodeGroupAutoscaling("cluster-1", "gpu")
			return callError
		}, http.MethodGet, "/api/v1/clusters/ankracloud/cluster-1/node-groups/gpu/autoscaling"},
		{"set autoscaling", func(testClient *Client) error {
			_, _, callError := testClient.UpdateAnkraCloudNodeGroupAutoscaling(requestContext, "cluster-1", "gpu", NodeGroupAutoscalingRequest{Enabled: true, MinCount: 1, MaxCount: 3}, false)
			return callError
		}, http.MethodPut, "/api/v1/clusters/ankracloud/cluster-1/node-groups/gpu/autoscaling"},
		{"control plane", func(testClient *Client) error {
			_, callError := testClient.GetAnkraCloudControlPlane("cluster-1")
			return callError
		}, http.MethodGet, "/api/v1/clusters/ankracloud/cluster-1/control-plane"},
		{"control plane count", func(testClient *Client) error {
			_, callError := testClient.ChangeAnkraCloudControlPlaneCount("cluster-1", 3)
			return callError
		}, http.MethodPut, "/api/v1/clusters/ankracloud/cluster-1/control-plane"},
		{"control plane plan", func(testClient *Client) error {
			_, callError := testClient.ChangeAnkraCloudControlPlaneInstanceType("cluster-1", "s-8")
			return callError
		}, http.MethodPut, "/api/v1/clusters/ankracloud/cluster-1/control-plane/instance-type"},
		{"nodes", func(testClient *Client) error {
			_, callError := testClient.ListAnkraCloudClusterNodes("cluster-1")
			return callError
		}, http.MethodGet, "/api/v1/clusters/ankracloud/cluster-1/nodes"},
		{"node", func(testClient *Client) error {
			_, callError := testClient.GetAnkraCloudClusterNode("cluster-1", "node-1")
			return callError
		}, http.MethodGet, "/api/v1/clusters/ankracloud/cluster-1/nodes/node-1"},
		{"restart node", func(testClient *Client) error {
			_, callError := testClient.RestartAnkraCloudClusterNode("cluster-1", "node-1")
			return callError
		}, http.MethodPost, "/api/v1/clusters/ankracloud/cluster-1/nodes/node-1/restart"},
		{"cloud-init log", func(testClient *Client) error {
			_, callError := testClient.AnkraCloudNodeCloudInitLog("cluster-1", "node-1")
			return callError
		}, http.MethodPost, "/api/v1/clusters/ankracloud/cluster-1/nodes/node-1/cloud-init-log"},
		{"ssh keys", func(testClient *Client) error {
			_, callError := testClient.GetAnkraCloudClusterSSHKeys("cluster-1")
			return callError
		}, http.MethodGet, "/api/v1/clusters/ankracloud/cluster-1/ssh-keys"},
		{"set ssh keys", func(testClient *Client) error {
			_, callError := testClient.UpdateAnkraCloudClusterSSHKeys("cluster-1", []string{"ssh-key-1"})
			return callError
		}, http.MethodPut, "/api/v1/clusters/ankracloud/cluster-1/ssh-keys"},
		{"resync ssh keys", func(testClient *Client) error {
			_, callError := testClient.ResyncAnkraCloudClusterSSHKeys("cluster-1")
			return callError
		}, http.MethodPost, "/api/v1/clusters/ankracloud/cluster-1/ssh-keys/resync"},
		{"bastion plan", func(testClient *Client) error {
			_, _, callError := testClient.UpdateAnkraCloudBastionInstanceType(requestContext, "cluster-1", "s-2", false)
			return callError
		}, http.MethodPut, "/api/v1/clusters/ankracloud/cluster-1/bastion/instance-type"},
		{"bastion health", func(testClient *Client) error {
			_, callError := testClient.GetAnkraCloudBastionHealth("cluster-1")
			return callError
		}, http.MethodGet, "/api/v1/clusters/ankracloud/cluster-1/bastion/health"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var receivedMethod, receivedPath string
			testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
				receivedMethod, receivedPath = request.Method, request.URL.Path
				jsonResponse(t, responseWriter, http.StatusOK, map[string]any{})
			})
			_ = testCase.call(testClient)
			if receivedPath != testCase.expectedPath {
				t.Errorf("path = %q, want %q", receivedPath, testCase.expectedPath)
			}
			if receivedMethod != testCase.expectedMethod {
				t.Errorf("method = %q, want %q", receivedMethod, testCase.expectedMethod)
			}
		})
	}
}

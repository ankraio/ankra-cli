package client

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestCreateAwsCluster_PostsSnakeCaseBody(t *testing.T) {
	var received map[string]any
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/clusters/aws" {
			t.Errorf("path = %s, want /api/v1/clusters/aws", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q, want the bearer token", got)
		}
		body, _ := io.ReadAll(request.Body)
		if decodeError := json.Unmarshal(body, &received); decodeError != nil {
			t.Fatalf("body is not JSON: %v", decodeError)
		}
		jsonResponse(t, responseWriter, http.StatusCreated, CreateAwsClusterResponse{ClusterID: "cluster-123", Name: "prod"})
	})

	workerCount := 0
	includeDNS := false
	result, createError := testClient.CreateAwsCluster(CreateAwsClusterRequest{
		Name:               "prod",
		CredentialID:       "cred-aws",
		SSHKeyCredentialID: "cred-ssh",
		Region:             "eu-north-1",
		VpcID:              "vpc-0abc",
		NodeSubnetIDs:      []string{"subnet-0aaa", "subnet-0bbb"},
		BastionSubnetID:    "subnet-0ccc",
		BastionAllowedIPs:  []string{"203.0.113.0/24"},
		EgressMode:         "bastion_nat",
		WorkerCount:        &workerCount,
		IncludeDNS:         &includeDNS,
		CNIFeatures:        []string{"hubble"},
	})
	if createError != nil {
		t.Fatalf("CreateAwsCluster: %v", createError)
	}
	if result.ClusterID != "cluster-123" || result.Name != "prod" {
		t.Errorf("result = %+v, want cluster-123/prod", result)
	}

	for key, want := range map[string]any{
		"name":                  "prod",
		"credential_id":         "cred-aws",
		"ssh_key_credential_id": "cred-ssh",
		"region":                "eu-north-1",
		"vpc_id":                "vpc-0abc",
		"bastion_subnet_id":     "subnet-0ccc",
		"egress_mode":           "bastion_nat",
		"worker_count":          float64(0),
		"include_dns":           false,
	} {
		if got := received[key]; got != want {
			t.Errorf("body[%q] = %v, want %v", key, got, want)
		}
	}
	if subnets, _ := received["node_subnet_ids"].([]any); len(subnets) != 2 || subnets[0] != "subnet-0aaa" {
		t.Errorf("node_subnet_ids = %v, want the two subnets", received["node_subnet_ids"])
	}
	if features, _ := received["cni_features"].([]any); len(features) != 1 || features[0] != "hubble" {
		t.Errorf("cni_features = %v, want [hubble]", received["cni_features"])
	}
	// Unset optionals are omitted so the server default applies.
	for _, absent := range []string{"description", "kubernetes_version", "control_plane_count", "distribution", "include_networking", "bastion_instance_type", "k3s_disabled_components", "node_groups", "classification"} {
		if _, present := received[absent]; present {
			t.Errorf("body[%q] must be omitted when unset, got %v", absent, received[absent])
		}
	}
	// The required list members are always sent, even when empty.
	for _, always := range []string{"node_subnet_ids", "bastion_allowed_ips"} {
		if _, present := received[always]; !present {
			t.Errorf("body[%q] must always be sent", always)
		}
	}
}

func TestPreflightAwsCluster(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/clusters/aws/preflight" {
			t.Errorf("path = %s, want /api/v1/clusters/aws/preflight", request.URL.Path)
		}
		jsonResponse(t, responseWriter, http.StatusOK, AwsPreflightResult{
			CanProceed:         false,
			ResolvedEgressMode: "existing",
			Items:              []AwsPreflightItem{{Check: "vpc", Status: "error", Message: "vpc-0abc not found"}},
		})
	})

	result, preflightError := testClient.PreflightAwsCluster(CreateAwsClusterRequest{Name: "prod"})
	if preflightError != nil {
		t.Fatalf("PreflightAwsCluster: %v", preflightError)
	}
	if result.CanProceed {
		t.Error("CanProceed = true, want false")
	}
	if result.ResolvedEgressMode != "existing" {
		t.Errorf("ResolvedEgressMode = %q, want existing", result.ResolvedEgressMode)
	}
	if len(result.Items) != 1 || result.Items[0].Check != "vpc" {
		t.Errorf("Items = %+v, want the vpc check", result.Items)
	}
}

func TestPreflightAwsCluster_RejectsNon200(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		jsonResponse(t, responseWriter, http.StatusUnprocessableEntity, map[string]string{"detail": "region is required"})
	})
	if _, preflightError := testClient.PreflightAwsCluster(CreateAwsClusterRequest{}); preflightError == nil {
		t.Fatal("a 422 must surface as an error")
	}
}

func TestDeprovisionAwsCluster(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", request.Method)
		}
		if request.URL.Path != "/api/v1/clusters/aws/cluster-123" {
			t.Errorf("path = %s, want /api/v1/clusters/aws/cluster-123", request.URL.Path)
		}
		if request.URL.RawQuery != "" {
			t.Errorf("query = %q, want none: the AWS deprovision takes no force argument", request.URL.RawQuery)
		}
		jsonResponse(t, responseWriter, http.StatusOK, ProviderDeprovisionClusterResponse{Success: true, ClusterID: "cluster-123"})
	})

	result, deprovisionError := testClient.DeprovisionAwsCluster("cluster-123")
	if deprovisionError != nil {
		t.Fatalf("DeprovisionAwsCluster: %v", deprovisionError)
	}
	if !result.Success || result.ClusterID != "cluster-123" {
		t.Errorf("result = %+v, want success for cluster-123", result)
	}
}

func TestStopAwsCluster(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/clusters/aws/cluster-123/stop" {
			t.Errorf("path = %s, want /api/v1/clusters/aws/cluster-123/stop", request.URL.Path)
		}
		if request.URL.Query().Get("force") != "true" {
			t.Errorf("force = %q, want true", request.URL.Query().Get("force"))
		}
		jsonResponse(t, responseWriter, http.StatusOK, ProviderStopClusterResponse{Success: true, ClusterID: "cluster-123"})
	})

	result, stopError := testClient.StopAwsCluster("cluster-123", true)
	if stopError != nil {
		t.Fatalf("StopAwsCluster: %v", stopError)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
}

func TestStartAwsCluster(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/clusters/aws/cluster-123/start" {
			t.Errorf("path = %s, want /api/v1/clusters/aws/cluster-123/start", request.URL.Path)
		}
		if scope := request.URL.Query().Get("scope"); scope != "control_plane" {
			t.Errorf("scope = %q, want control_plane", scope)
		}
		jsonResponse(t, responseWriter, http.StatusOK, ProviderStartClusterResult{Scope: "control_plane", CreatedOperations: 2})
	})

	result, startError := testClient.StartAwsCluster("cluster-123", "control_plane")
	if startError != nil {
		t.Fatalf("StartAwsCluster: %v", startError)
	}
	if result.Scope != "control_plane" || result.CreatedOperations != 2 {
		t.Errorf("result = %+v, want control_plane/2", result)
	}
}

// Every catalog is a GET on /api/v1/clusters/aws/<catalog> scoped by
// credential_id, then region and vpc_id only where the catalog takes them.
func TestAwsCatalogs_HitTheirPaths(t *testing.T) {
	type call struct {
		invoke      func(c *Client) (*AwsCatalogResult, error)
		wantPath    string
		wantQuery   map[string]string
		absentQuery []string
	}
	for name, testCase := range map[string]call{
		"regions": {
			invoke:      func(c *Client) (*AwsCatalogResult, error) { return c.ListAwsRegions("cred-aws") },
			wantPath:    "/api/v1/clusters/aws/regions",
			wantQuery:   map[string]string{"credential_id": "cred-aws"},
			absentQuery: []string{"region", "vpc_id"},
		},
		"instance-types": {
			invoke:      func(c *Client) (*AwsCatalogResult, error) { return c.ListAwsInstanceTypes("cred-aws", "eu-north-1") },
			wantPath:    "/api/v1/clusters/aws/instance-types",
			wantQuery:   map[string]string{"credential_id": "cred-aws", "region": "eu-north-1"},
			absentQuery: []string{"vpc_id"},
		},
		"vpcs": {
			invoke:    func(c *Client) (*AwsCatalogResult, error) { return c.ListAwsVpcs("cred-aws", "eu-north-1") },
			wantPath:  "/api/v1/clusters/aws/vpcs",
			wantQuery: map[string]string{"credential_id": "cred-aws", "region": "eu-north-1"},
		},
		"subnets": {
			invoke: func(c *Client) (*AwsCatalogResult, error) {
				return c.ListAwsSubnets("cred-aws", "eu-north-1", "vpc-0abc")
			},
			wantPath:  "/api/v1/clusters/aws/subnets",
			wantQuery: map[string]string{"credential_id": "cred-aws", "region": "eu-north-1", "vpc_id": "vpc-0abc"},
		},
		"availability-zones": {
			invoke: func(c *Client) (*AwsCatalogResult, error) {
				return c.ListAwsAvailabilityZones("cred-aws", "eu-north-1")
			},
			wantPath:  "/api/v1/clusters/aws/availability-zones",
			wantQuery: map[string]string{"credential_id": "cred-aws", "region": "eu-north-1"},
		},
		"images": {
			invoke:    func(c *Client) (*AwsCatalogResult, error) { return c.ListAwsImages("cred-aws", "eu-north-1") },
			wantPath:  "/api/v1/clusters/aws/images",
			wantQuery: map[string]string{"credential_id": "cred-aws", "region": "eu-north-1"},
		},
		"pricing": {
			invoke:    func(c *Client) (*AwsCatalogResult, error) { return c.ListAwsPricing("cred-aws", "eu-north-1") },
			wantPath:  "/api/v1/clusters/aws/pricing",
			wantQuery: map[string]string{"credential_id": "cred-aws", "region": "eu-north-1"},
		},
		"cluster instance-types": {
			invoke:      func(c *Client) (*AwsCatalogResult, error) { return c.ListAwsClusterInstanceTypes("cluster-123") },
			wantPath:    "/api/v1/clusters/aws/cluster-123/instance-types",
			absentQuery: []string{"credential_id", "region"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet {
					t.Errorf("method = %s, want GET", request.Method)
				}
				if request.URL.Path != testCase.wantPath {
					t.Errorf("path = %s, want %s", request.URL.Path, testCase.wantPath)
				}
				for key, want := range testCase.wantQuery {
					if got := request.URL.Query().Get(key); got != want {
						t.Errorf("query %s = %q, want %q", key, got, want)
					}
				}
				for _, key := range testCase.absentQuery {
					if request.URL.Query().Has(key) {
						t.Errorf("query %s must not be sent for %s", key, name)
					}
				}
				jsonResponse(t, responseWriter, http.StatusOK, AwsCatalogResult{
					Regions:       []AwsRegion{{Name: "eu-north-1"}},
					InstanceTypes: []AwsInstanceType{{Name: "t3.medium"}},
				})
			})
			result, listError := testCase.invoke(testClient)
			if listError != nil {
				t.Fatalf("%s: %v", name, listError)
			}
			if len(result.Regions) != 1 || len(result.InstanceTypes) != 1 {
				t.Errorf("result = %+v, want the decoded catalog", result)
			}
		})
	}
}

// The node-group label and taint updates are the two literal aws paths
// outside the kind-parameterised helpers, so they get their own pin.
func TestUpdateAwsNodeGroupLabelsAndTaints_HitTheirPaths(t *testing.T) {
	var paths []string
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.Method+" "+request.URL.Path)
		jsonResponse(t, responseWriter, http.StatusOK, UpdateNodeGroupResult{})
	})
	if _, _, labelsError := testClient.UpdateAwsNodeGroupLabels(t.Context(), "cluster-123", "workers", map[string]string{"tier": "web"}, true); labelsError != nil {
		t.Fatalf("UpdateAwsNodeGroupLabels: %v", labelsError)
	}
	if _, _, taintsError := testClient.UpdateAwsNodeGroupTaints(t.Context(), "cluster-123", "workers", []NodeTaint{{Key: "dedicated", Value: "web", Effect: "NoSchedule"}}, true); taintsError != nil {
		t.Fatalf("UpdateAwsNodeGroupTaints: %v", taintsError)
	}
	want := []string{
		"PUT /api/v1/clusters/aws/cluster-123/node-groups/workers/labels",
		"PUT /api/v1/clusters/aws/cluster-123/node-groups/workers/taints",
	}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestGetAwsAccessInfo(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/clusters/aws/cluster-123/access-info" {
			t.Errorf("path = %s, want /api/v1/clusters/aws/cluster-123/access-info", request.URL.Path)
		}
		jsonResponse(t, responseWriter, http.StatusOK, ClusterAccessInfo{BastionIP: strPtr("203.0.113.10"), ControlPlaneIP: strPtr("10.0.1.10")})
	})
	result, accessError := testClient.GetAwsAccessInfo("cluster-123")
	if accessError != nil {
		t.Fatalf("GetAwsAccessInfo: %v", accessError)
	}
	if result.BastionIP == nil || *result.BastionIP != "203.0.113.10" {
		t.Errorf("BastionIP = %v, want 203.0.113.10", result.BastionIP)
	}
}

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
		jsonResponse(t, responseWriter, http.StatusCreated, CreateAwsClusterResponse{
			ClusterID: "cluster-123", Name: "prod", Kind: "aws", State: "creating", OperationID: strPtr("op-1"),
		})
	})

	workerCount := 0
	includeDNS := false
	environment := "production"
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
		CNIFeatures:        &AwsCNIFeatures{Hubble: true},
		Environment:        &environment,
	})
	if createError != nil {
		t.Fatalf("CreateAwsCluster: %v", createError)
	}
	if result.ClusterID != "cluster-123" || result.Name != "prod" || result.Kind != "aws" || result.State != "creating" {
		t.Errorf("result = %+v, want cluster-123/prod/aws/creating", result)
	}
	if result.OperationID == nil || *result.OperationID != "op-1" {
		t.Errorf("OperationID = %v, want op-1", result.OperationID)
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
		"environment":           "production",
	} {
		if got := received[key]; got != want {
			t.Errorf("body[%q] = %v, want %v", key, got, want)
		}
	}
	if subnets, _ := received["node_subnet_ids"].([]any); len(subnets) != 2 || subnets[0] != "subnet-0aaa" {
		t.Errorf("node_subnet_ids = %v, want the two subnets", received["node_subnet_ids"])
	}
	// cni_features is an object of booleans on the wire, and a feature left
	// off is omitted rather than sent as false.
	features, isObject := received["cni_features"].(map[string]any)
	if !isObject || features["hubble"] != true || len(features) != 1 {
		t.Errorf("cni_features = %v, want {hubble: true}", received["cni_features"])
	}
	// Unset optionals are omitted so the server default applies.
	for _, absent := range []string{"description", "kubernetes_version", "control_plane_count", "distribution", "include_networking", "bastion_instance_type", "k3s_disabled_components", "node_groups", "criticality", "classification"} {
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
			ResolvedEgressMode: strPtr("existing"),
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
	if result.ResolvedEgressMode == nil || *result.ResolvedEgressMode != "existing" {
		t.Errorf("ResolvedEgressMode = %v, want existing", result.ResolvedEgressMode)
	}
	if len(result.Items) != 1 || result.Items[0].Check != "vpc" {
		t.Errorf("Items = %+v, want the vpc check", result.Items)
	}
}

// A null resolved_egress_mode is the server declining to settle one, which
// must stay distinguishable from a resolved "existing".
func TestPreflightAwsCluster_KeepsAnUnresolvedEgressModeNil(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"items":[],"can_proceed":false,"resolved_egress_mode":null}`))
	})
	result, preflightError := testClient.PreflightAwsCluster(CreateAwsClusterRequest{})
	if preflightError != nil {
		t.Fatalf("PreflightAwsCluster: %v", preflightError)
	}
	if result.ResolvedEgressMode != nil {
		t.Errorf("ResolvedEgressMode = %q, want nil for a null", *result.ResolvedEgressMode)
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
// credential_id, then region and vpc_id only where the catalog takes them,
// and each decodes into its own envelope - the catalogs are separate
// endpoints, not one shared result.
func TestAwsCatalogs_HitTheirPaths(t *testing.T) {
	type call struct {
		invoke      func(c *Client) (any, error)
		wantPath    string
		wantQuery   map[string]string
		absentQuery []string
		body        string
		check       func(t *testing.T, result any)
	}
	for name, testCase := range map[string]call{
		"regions": {
			invoke:      func(c *Client) (any, error) { return c.ListAwsRegions("cred-aws") },
			wantPath:    "/api/v1/clusters/aws/regions",
			wantQuery:   map[string]string{"credential_id": "cred-aws"},
			absentQuery: []string{"region", "vpc_id"},
			body:        `{"regions":[{"slug":"eu-north-1","name":"Europe (Stockholm)"}],"pricing_complete":true}`,
			check: func(t *testing.T, result any) {
				catalog := result.(*AwsRegionsCatalog)
				if len(catalog.Regions) != 1 || catalog.Regions[0].Slug != "eu-north-1" || catalog.Regions[0].Name != "Europe (Stockholm)" || !catalog.PricingComplete {
					t.Errorf("regions = %+v", catalog)
				}
			},
		},
		"instance-types": {
			invoke:      func(c *Client) (any, error) { return c.ListAwsInstanceTypes("cred-aws", "eu-north-1") },
			wantPath:    "/api/v1/clusters/aws/instance-types",
			wantQuery:   map[string]string{"credential_id": "cred-aws", "region": "eu-north-1"},
			absentQuery: []string{"vpc_id"},
			body: `{"instance_types":[{"name":"t3.medium","vcpus":2,"memory_gib":4,"architecture":"amd64","category":"general","hourly_price_usd":0.0418,"monthly_price_usd":30.5,"current_generation":true},` +
				`{"name":"m7g.large","vcpus":2,"memory_gib":8,"architecture":"arm64","category":"general","hourly_price_usd":null,"monthly_price_usd":null,"current_generation":true}],` +
				`"pricing_complete":false,"incomplete_reasons":["m7g pricing not published"]}`,
			check: func(t *testing.T, result any) {
				catalog := result.(*AwsInstanceTypesCatalog)
				if len(catalog.InstanceTypes) != 2 {
					t.Fatalf("instance types = %+v", catalog.InstanceTypes)
				}
				priced := catalog.InstanceTypes[0]
				if priced.Name != "t3.medium" || priced.VCPUs != 2 || priced.MemoryGiB != 4 || priced.Category != "general" || !priced.CurrentGeneration {
					t.Errorf("priced type = %+v", priced)
				}
				if priced.HourlyPriceUSD == nil || *priced.HourlyPriceUSD != 0.0418 || priced.MonthlyPriceUSD == nil || *priced.MonthlyPriceUSD != 30.5 {
					t.Errorf("prices = %v/%v, want 0.0418/30.5", priced.HourlyPriceUSD, priced.MonthlyPriceUSD)
				}
				// A null price stays nil: it is unknown, not zero.
				if unpriced := catalog.InstanceTypes[1]; unpriced.HourlyPriceUSD != nil || unpriced.MonthlyPriceUSD != nil {
					t.Errorf("null prices must decode to nil, got %v/%v", unpriced.HourlyPriceUSD, unpriced.MonthlyPriceUSD)
				}
				if catalog.PricingComplete || len(catalog.IncompleteReasons) != 1 {
					t.Errorf("pricing flags = %v/%v", catalog.PricingComplete, catalog.IncompleteReasons)
				}
			},
		},
		"vpcs": {
			invoke:    func(c *Client) (any, error) { return c.ListAwsVpcs("cred-aws", "eu-north-1") },
			wantPath:  "/api/v1/clusters/aws/vpcs",
			wantQuery: map[string]string{"credential_id": "cred-aws", "region": "eu-north-1"},
			body: `{"vpcs":[{"id":"vpc-0abc","name":"prod","cidr":"10.0.0.0/16","is_default":false,"dhcp_domain_name":"eu-north-1.compute.internal","dhcp_domain_name_state":"set"},` +
				`{"id":"vpc-0def","name":"","cidr":"172.31.0.0/16","is_default":true,"dhcp_domain_name":null,"dhcp_domain_name_state":"unknown"}]}`,
			check: func(t *testing.T, result any) {
				catalog := result.(*AwsVpcsCatalog)
				if len(catalog.Vpcs) != 2 || catalog.Vpcs[0].ID != "vpc-0abc" || catalog.Vpcs[0].CIDR != "10.0.0.0/16" {
					t.Fatalf("vpcs = %+v", catalog.Vpcs)
				}
				if catalog.Vpcs[0].DhcpDomainNameState != "set" || catalog.Vpcs[0].DhcpDomainName == nil || *catalog.Vpcs[0].DhcpDomainName != "eu-north-1.compute.internal" {
					t.Errorf("dhcp domain = %+v", catalog.Vpcs[0])
				}
				if catalog.Vpcs[1].DhcpDomainNameState != "unknown" || catalog.Vpcs[1].DhcpDomainName != nil || !catalog.Vpcs[1].IsDefault {
					t.Errorf("unknown dhcp domain = %+v", catalog.Vpcs[1])
				}
			},
		},
		"subnets": {
			invoke:    func(c *Client) (any, error) { return c.ListAwsSubnets("cred-aws", "eu-north-1", "vpc-0abc") },
			wantPath:  "/api/v1/clusters/aws/subnets",
			wantQuery: map[string]string{"credential_id": "cred-aws", "region": "eu-north-1", "vpc_id": "vpc-0abc"},
			body: `{"subnets":[{"id":"subnet-0aaa","name":"private-a","cidr":"10.0.1.0/24","availability_zone":"eu-north-1a","map_public_ip_on_launch":false,"egress":{"kind":"nat_gateway"},"foreign_instance_count":0},` +
				`{"id":"subnet-0ccc","name":"public-a","cidr":"10.0.100.0/24","availability_zone":"eu-north-1a","map_public_ip_on_launch":true,"egress":{"kind":"internet_gateway"},"foreign_instance_count":null}]}`,
			check: func(t *testing.T, result any) {
				catalog := result.(*AwsSubnetsCatalog)
				if len(catalog.Subnets) != 2 {
					t.Fatalf("subnets = %+v", catalog.Subnets)
				}
				private := catalog.Subnets[0]
				if private.ID != "subnet-0aaa" || private.AvailabilityZone != "eu-north-1a" || private.Egress.Kind != "nat_gateway" || private.MapPublicIPOnLaunch {
					t.Errorf("private subnet = %+v", private)
				}
				if private.ForeignInstanceCount == nil || *private.ForeignInstanceCount != 0 {
					t.Errorf("a zero foreign count is a counted zero, got %v", private.ForeignInstanceCount)
				}
				public := catalog.Subnets[1]
				if public.Egress.Kind != "internet_gateway" || !public.MapPublicIPOnLaunch || public.ForeignInstanceCount != nil {
					t.Errorf("public subnet = %+v (a null count must stay nil)", public)
				}
			},
		},
		"availability-zones": {
			invoke:    func(c *Client) (any, error) { return c.ListAwsAvailabilityZones("cred-aws", "eu-north-1") },
			wantPath:  "/api/v1/clusters/aws/availability-zones",
			wantQuery: map[string]string{"credential_id": "cred-aws", "region": "eu-north-1"},
			body:      `{"zones":[{"name":"eu-north-1a","id":"eun1-az1","state":"available"}]}`,
			check: func(t *testing.T, result any) {
				catalog := result.(*AwsAvailabilityZonesCatalog)
				if len(catalog.Zones) != 1 || catalog.Zones[0].Name != "eu-north-1a" || catalog.Zones[0].ID != "eun1-az1" || catalog.Zones[0].State != "available" {
					t.Errorf("zones = %+v", catalog.Zones)
				}
			},
		},
		"images": {
			invoke:    func(c *Client) (any, error) { return c.ListAwsImages("cred-aws", "eu-north-1") },
			wantPath:  "/api/v1/clusters/aws/images",
			wantQuery: map[string]string{"credential_id": "cred-aws", "region": "eu-north-1"},
			body:      `{"ubuntu_series":["22.04","24.04"],"architectures":["amd64"],"default_series":"24.04"}`,
			check: func(t *testing.T, result any) {
				catalog := result.(*AwsImagesCatalog)
				if len(catalog.UbuntuSeries) != 2 || catalog.DefaultSeries != "24.04" || len(catalog.Architectures) != 1 || catalog.Architectures[0] != "amd64" {
					t.Errorf("images = %+v", catalog)
				}
			},
		},
		"pricing": {
			invoke:    func(c *Client) (any, error) { return c.ListAwsPricing("cred-aws", "eu-north-1") },
			wantPath:  "/api/v1/clusters/aws/pricing",
			wantQuery: map[string]string{"credential_id": "cred-aws", "region": "eu-north-1"},
			body: `{"instance_types":[{"name":"t3.medium","vcpus":2,"memory_gib":4,"architecture":"amd64","category":"general","hourly_price_usd":0.0418,"monthly_price_usd":30.5,"current_generation":true}],` +
				`"storage":{"gp3_gib_month_usd":null},"pricing_complete":false,"incomplete_reasons":["EBS pricing not published"]}`,
			check: func(t *testing.T, result any) {
				catalog := result.(*AwsPricingCatalog)
				if len(catalog.InstanceTypes) != 1 || catalog.InstanceTypes[0].Name != "t3.medium" {
					t.Errorf("pricing instance types = %+v", catalog.InstanceTypes)
				}
				if catalog.Storage.Gp3GiBMonthUSD != nil {
					t.Errorf("a null storage price must stay nil, got %v", *catalog.Storage.Gp3GiBMonthUSD)
				}
				if catalog.PricingComplete || len(catalog.IncompleteReasons) != 1 || catalog.IncompleteReasons[0] != "EBS pricing not published" {
					t.Errorf("pricing flags = %v/%v", catalog.PricingComplete, catalog.IncompleteReasons)
				}
			},
		},
		"cluster instance-types": {
			invoke:      func(c *Client) (any, error) { return c.ListAwsClusterInstanceTypes("cluster-123") },
			wantPath:    "/api/v1/clusters/aws/cluster-123/instance-types",
			absentQuery: []string{"credential_id", "region"},
			body:        `{"instance_types":[{"name":"t3.medium","vcpus":2,"memory_gib":4,"architecture":"amd64","category":"general","hourly_price_usd":0.0418,"monthly_price_usd":30.5,"current_generation":true}],"pricing_complete":true,"incomplete_reasons":[]}`,
			check: func(t *testing.T, result any) {
				catalog := result.(*AwsInstanceTypesCatalog)
				if len(catalog.InstanceTypes) != 1 || !catalog.PricingComplete {
					t.Errorf("cluster instance types = %+v", catalog)
				}
			},
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
				responseWriter.Header().Set("Content-Type", "application/json")
				_, _ = responseWriter.Write([]byte(testCase.body))
			})
			result, listError := testCase.invoke(testClient)
			if listError != nil {
				t.Fatalf("%s: %v", name, listError)
			}
			testCase.check(t, result)
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
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"bastion_ip":"203.0.113.10","bastion_host":"203.0.113.10","bastion_port":22,"bastion_user":"ubuntu",` +
			`"target_user":"ubuntu","control_plane_ip":"10.0.1.10","control_plane_ips":["10.0.1.10"],"cluster_name":"prod"}`))
	})
	result, accessError := testClient.GetAwsAccessInfo("cluster-123")
	if accessError != nil {
		t.Fatalf("GetAwsAccessInfo: %v", accessError)
	}
	if result.BastionIP == nil || *result.BastionIP != "203.0.113.10" {
		t.Errorf("BastionIP = %v, want 203.0.113.10", result.BastionIP)
	}
	if result.BastionHost != "203.0.113.10" || result.BastionPort != 22 || result.BastionUser != "ubuntu" || result.TargetUser != "ubuntu" {
		t.Errorf("jump details = %+v", result)
	}
	if result.ControlPlaneIP == nil || *result.ControlPlaneIP != "10.0.1.10" || len(result.ControlPlaneIPs) != 1 || result.ClusterName == nil || *result.ClusterName != "prod" {
		t.Errorf("control plane = %+v", result)
	}
}

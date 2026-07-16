package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCreateScalewayCredentialUsesExactPayloadWithoutLoggingSecrets(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/credentials/scaleway" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var request CreateScalewayCredentialRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request != (CreateScalewayCredentialRequest{
			Name: "prod", AccessKey: "AK", SecretKey: "SK", ProjectID: "project",
		}) {
			t.Fatalf("payload = %#v", request)
		}
		jsonResponse(t, w, http.StatusCreated, CreateScalewayCredentialResponse{Success: true})
	})
	result, err := c.CreateScalewayCredential(CreateScalewayCredentialRequest{
		Name: "prod", AccessKey: "AK", SecretKey: "SK", ProjectID: "project",
	})
	if err != nil || !result.Success {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestScalewayCreateAndPreflightPayloadIncludesCNIAndNetworkMode(t *testing.T) {
	requests := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request CreateScalewayClusterRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.CNI != "cilium" || !request.CNIFeatures.Hubble || request.PrivateNetworkID == nil {
			t.Fatalf("request = %#v", request)
		}
		switch r.URL.Path {
		case "/api/v1/clusters/scaleway/preflight":
			jsonResponse(t, w, http.StatusOK, ScalewayPreflightResult{CanProceed: true})
		case "/api/v1/clusters/scaleway":
			jsonResponse(t, w, http.StatusCreated, ScalewayCreateClusterResponse{ClusterID: "id", Name: "prod"})
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	})
	network := "pn-1"
	request := CreateScalewayClusterRequest{
		Name: "prod", CredentialID: "cred", SSHKeyCredentialID: "ssh", Region: "fr-par", Zone: "fr-par-1",
		PrivateNetworkID: &network, GatewayType: "VPC-GW-S", BastionPort: 61000,
		ExternalCloudProvider: true, CNI: "cilium", CNIFeatures: ScalewayCNIFeatures{Hubble: true},
	}
	if _, err := c.PreflightScalewayCluster(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateScalewayCluster(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestScalewayCatalogQueriesAndEscapedDayTwoPaths(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			if r.URL.Path != "/api/v1/clusters/scaleway/instance-types" ||
				r.URL.Query().Get("credential_id") != "cred/id" || r.URL.Query().Get("zone") != "fr-par-1" {
				t.Fatalf("catalog URL = %s", r.URL.String())
			}
			jsonResponse(t, w, http.StatusOK, ScalewayCatalogResult{PricingComplete: true})
		case 2:
			if r.URL.EscapedPath() != "/api/v1/clusters/scaleway/cluster%2Fid/node-groups/pool%2Fblue/scale" {
				t.Fatalf("scale path = %s", r.URL.EscapedPath())
			}
			jsonResponse(t, w, http.StatusOK, ScaleNodeGroupResult{GroupName: "pool/blue", NewCount: 3})
		}
	})
	if _, err := c.ListScalewayInstanceTypes(context.Background(), "cred/id", "fr-par-1"); err != nil {
		t.Fatal(err)
	}
	result, submitted, err := c.ScaleScalewayNodeGroup(context.Background(), "cluster/id", "pool/blue", 3, true)
	if err != nil || submitted || result.NewCount != 3 {
		t.Fatalf("result = %#v, submitted = %t, err = %v", result, submitted, err)
	}
}

func TestScalewayErrorsRedactCredentialMaterial(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]string{
			"detail": "invalid", "access_key": "AK-SECRET", "secret_key": "SK-SECRET",
		})
	})
	_, err := c.CreateScalewayCredential(CreateScalewayCredentialRequest{Name: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "AK-SECRET") || strings.Contains(err.Error(), "SK-SECRET") {
		t.Fatalf("secret leaked: %v", err)
	}
}

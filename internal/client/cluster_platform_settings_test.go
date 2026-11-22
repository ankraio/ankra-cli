package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

type platformSettingsCall struct {
	method string
	path   string
	query  string
	body   map[string]any
}

// recordingPlatformClient answers every request with status and response and
// records what was sent.
func recordingPlatformClient(t *testing.T, status int, response string) (*Client, *platformSettingsCall) {
	t.Helper()
	seen := &platformSettingsCall{}
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		seen.method, seen.path, seen.query = request.Method, request.URL.EscapedPath(), request.URL.RawQuery
		seen.body = nil
		if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &seen.body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(response))
	})
	return testClient, seen
}

func requirePlatformCall(t *testing.T, seen *platformSettingsCall, method string, path string) {
	t.Helper()
	if seen.method != method || seen.path != path {
		t.Fatalf("got %s %s, want %s %s", seen.method, seen.path, method, path)
	}
}

// Each route is pinned on the request itself: method, path and body.
func TestClusterPlatformSettingsLaneParity(t *testing.T) {
	ctx := context.Background()

	testClient, seen := recordingPlatformClient(t, http.StatusOK, `{"cluster_id":"c1","computed":false,"score":null,"checks":[]}`)
	baseline, baselineError := testClient.GetClusterBaseline(ctx, "c1")
	if baselineError != nil || baseline.Computed || baseline.Score != nil {
		t.Fatalf("baseline = %+v err = %v", baseline, baselineError)
	}
	requirePlatformCall(t, seen, http.MethodGet, "/api/v1/org/clusters/c1/baseline")

	testClient, seen = recordingPlatformClient(t, http.StatusOK, `{"cluster_id":"c1","notification_id":"n1","taken":[{"member":"cert-manager"}],"kept":[]}`)
	take, takeError := testClient.TakeHeldPlatformUpdate(ctx, "c1", "n1", "fp")
	if takeError != nil || len(take.Taken) != 1 || take.Taken[0].Member != "cert-manager" {
		t.Fatalf("take = %+v err = %v", take, takeError)
	}
	requirePlatformCall(t, seen, http.MethodPost, "/api/v1/org/clusters/c1/held-platform-updates/n1/take")
	if seen.body["fingerprint"] != "fp" {
		t.Fatalf("the fingerprint must be sent, got %v", seen.body)
	}

	for _, enabled := range []bool{true, false} {
		testClient, seen = recordingPlatformClient(t, http.StatusOK, `{"cluster_id":"c1","enabled":false,"latest_incident":null}`)
		if _, setError := testClient.SetWorkerSelfHeal(ctx, "c1", enabled); setError != nil {
			t.Fatal(setError)
		}
		requirePlatformCall(t, seen, http.MethodPut, "/api/v1/org/clusters/c1/worker-self-heal")
		if seen.body["enabled"] != enabled {
			t.Fatalf("enabled %v must be sent, not dropped as a zero value: %v", enabled, seen.body)
		}
	}
	testClient, seen = recordingPlatformClient(t, http.StatusOK, `{"cluster_id":"c1","enabled":true,"latest_incident":{"node_name":"w-1","mode":"shadow"}}`)
	heal, healError := testClient.GetWorkerSelfHeal(ctx, "c1")
	if healError != nil || !heal.Enabled || heal.LatestIncident == nil || heal.LatestIncident.NodeName != "w-1" {
		t.Fatalf("heal = %+v err = %v", heal, healError)
	}
	requirePlatformCall(t, seen, http.MethodGet, "/api/v1/org/clusters/c1/worker-self-heal")

	testClient, seen = recordingPlatformClient(t, http.StatusOK, `{"provider":"prometheus","endpoint":"https://p","state":"up","credential":{"name":"cred","state":"up"}}`)
	source, sourceError := testClient.GetClusterMetricsSource(ctx, "c1")
	if sourceError != nil || source.Credential == nil || source.Credential.Name != "cred" || source.Health != nil {
		t.Fatalf("source = %+v err = %v", source, sourceError)
	}
	requirePlatformCall(t, seen, http.MethodGet, "/api/v1/org/clusters/imported/c1/metrics-source")

	rotationBody := `{"success":true,"fqdn":"a.ankra.cc","state":"active","rotation_requested_at":null,"previous_token_pending_revocation":true,"revocation_requested_at":null}`
	testClient, seen = recordingPlatformClient(t, http.StatusOK, rotationBody)
	rotation, rotationError := testClient.GetClusterDNSTokenRotation(ctx, "c1")
	if rotationError != nil || !rotation.PreviousTokenPendingRevocation || rotation.RotationRequestedAt != nil {
		t.Fatalf("rotation = %+v err = %v", rotation, rotationError)
	}
	requirePlatformCall(t, seen, http.MethodGet, "/api/v1/clusters/c1/dns-zone/token-rotation")
	if _, rotateError := testClient.RotateClusterDNSToken("c1"); rotateError != nil {
		t.Fatal(rotateError)
	}
	requirePlatformCall(t, seen, http.MethodPost, "/api/v1/clusters/c1/dns-zone/token-rotation")
	if _, revokeError := testClient.RevokePreviousClusterDNSToken("c1"); revokeError != nil {
		t.Fatal(revokeError)
	}
	requirePlatformCall(t, seen, http.MethodPost, "/api/v1/clusters/c1/dns-zone/token-rotation/revoke-previous")
}

func TestProviderClusterSettingsLaneParity(t *testing.T) {
	ctx := context.Background()

	for _, kind := range K3sDisabledComponentsKinds {
		testClient, seen := recordingPlatformClient(t, http.StatusOK, `{"k3s_disabled_components":["traefik"],"resource_ids":[],"control_plane_count":1,"applies":""}`)
		if _, getError := testClient.GetK3sDisabledComponents(ctx, kind, "c1"); getError != nil {
			t.Fatal(getError)
		}
		requirePlatformCall(t, seen, http.MethodGet, "/api/v1/clusters/"+kind+"/c1/k3s-disabled-components")
		if _, setError := testClient.SetK3sDisabledComponents(ctx, kind, "c1", nil); setError != nil {
			t.Fatal(setError)
		}
		requirePlatformCall(t, seen, http.MethodPut, "/api/v1/clusters/"+kind+"/c1/k3s-disabled-components")
		if list, isList := seen.body["k3s_disabled_components"].([]any); !isList || len(list) != 0 {
			t.Fatalf("an empty list must be sent as [], not null: %v", seen.body)
		}
	}
	unknownClient, unknownSeen := recordingPlatformClient(t, http.StatusOK, `{}`)
	if _, kindError := unknownClient.GetK3sDisabledComponents(ctx, "imported", "c1"); kindError == nil || unknownSeen.method != "" {
		t.Fatalf("a kind without the route must be refused before anything is sent, got %v", kindError)
	}

	testClient, seen := recordingPlatformClient(t, http.StatusOK, `{"plans":[{"name":"standard"}],"default_plan":"standard","plan_change_notice":"n"}`)
	gatewayPlans, plansError := testClient.ListUpcloudGatewayPlans(ctx, "cred 1")
	if plansError != nil || len(gatewayPlans.Plans) != 1 || gatewayPlans.DefaultPlan != "standard" {
		t.Fatalf("plans = %+v err = %v", gatewayPlans, plansError)
	}
	requirePlatformCall(t, seen, http.MethodGet, "/api/v1/clusters/upcloud/gateway-plans")
	if seen.query != "credential_id=cred+1" {
		t.Fatalf("the credential must be sent escaped, got %q", seen.query)
	}

	testClient, seen = recordingPlatformClient(t, http.StatusAccepted, `{"gateway_plan":"production","plan_change_request_id":"r1","resource_ids":["g1"],"deferred_resource_ids":[],"notice":"n"}`)
	change, changeError := testClient.SetUpcloudGatewayPlan(ctx, "c1", "production")
	if changeError != nil || change.PlanChangeRequestID != "r1" {
		t.Fatalf("change = %+v err = %v", change, changeError)
	}
	requirePlatformCall(t, seen, http.MethodPut, "/api/v1/clusters/upcloud/c1/gateway-plan")
	if seen.body["gateway_plan"] != "production" {
		t.Fatalf("body = %v", seen.body)
	}

	testClient, seen = recordingPlatformClient(t, http.StatusOK, `[{"name":"development","server_number":1,"per_server_max_sessions":1000,"highly_available":false}]`)
	balancerPlans, balancerPlansError := testClient.ListUpcloudLoadBalancerPlans(ctx, "cred")
	if balancerPlansError != nil || len(balancerPlans) != 1 || balancerPlans[0].Name != "development" {
		t.Fatalf("plans = %+v err = %v", balancerPlans, balancerPlansError)
	}
	requirePlatformCall(t, seen, http.MethodGet, "/api/v1/clusters/upcloud/load-balancer-plans")

	testClient, seen = recordingPlatformClient(t, http.StatusOK, `{"load_balancer_plan":null,"managed":true,"load_balancers_observed":false,"load_balancers":null,"observation_error":"e","available_plans":null}`)
	balancerPlan, balancerPlanError := testClient.GetUpcloudClusterLoadBalancerPlan(ctx, "c1")
	if balancerPlanError != nil || balancerPlan.LoadBalancerPlan != nil || balancerPlan.LoadBalancers != nil {
		t.Fatalf("plan = %+v err = %v", balancerPlan, balancerPlanError)
	}
	requirePlatformCall(t, seen, http.MethodGet, "/api/v1/clusters/upcloud/c1/load-balancer-plan")

	testClient, seen = recordingPlatformClient(t, http.StatusOK, `{"load_balancer_plan":"p","previous_load_balancer_plan":null,"changed":true,"resource_ids":[]}`)
	if _, setError := testClient.SetUpcloudClusterLoadBalancerPlan(ctx, "c1", "p"); setError != nil {
		t.Fatal(setError)
	}
	requirePlatformCall(t, seen, http.MethodPut, "/api/v1/clusters/upcloud/c1/load-balancer-plan")
	if seen.body["load_balancer_plan"] != "p" {
		t.Fatalf("body = %v", seen.body)
	}

	testClient, seen = recordingPlatformClient(t, http.StatusOK, `[{"id":"fi-hel2","description":"Helsinki #2"}]`)
	zones, zonesError := testClient.ListUpcloudZones(ctx, "cred")
	if zonesError != nil || len(zones) != 1 || zones[0].ID != "fi-hel2" {
		t.Fatalf("zones = %+v err = %v", zones, zonesError)
	}
	requirePlatformCall(t, seen, http.MethodGet, "/api/v1/clusters/upcloud/zones")

	testClient, seen = recordingPlatformClient(t, http.StatusOK, `{"gateway_model":"m","resource_ids":["g1"]}`)
	if _, modelError := testClient.SetOvhGatewayModel(ctx, "c1", "m"); modelError != nil {
		t.Fatal(modelError)
	}
	requirePlatformCall(t, seen, http.MethodPut, "/api/v1/clusters/ovh/c1/gateway-model")
	if seen.body["gateway_model"] != "m" {
		t.Fatalf("body = %v", seen.body)
	}

	testClient, seen = recordingPlatformClient(t, http.StatusOK, `{"external_cloud_provider":true,"converged_resource_ids":["n1"],"already_external_resource_ids":[],"verified_node_count":null,"applies":"a"}`)
	convergence, convergeError := testClient.ConvergeHetznerExternalCloudProvider(ctx, "c1")
	if convergeError != nil || !convergence.ExternalCloudProvider || convergence.VerifiedNodeCount != nil {
		t.Fatalf("convergence = %+v err = %v", convergence, convergeError)
	}
	requirePlatformCall(t, seen, http.MethodPost, "/api/v1/clusters/hetzner/c1/external-cloud-provider/converge")
}

// A refusal keeps its status and the platform's own reason, and a 401 is the
// shared unauthorised error.
func TestClusterPlatformSettingsRefusals(t *testing.T) {
	ctx := context.Background()
	conflictClient, _ := recordingPlatformClient(t, http.StatusConflict, `{"detail":"The card changed since it was read"}`)
	_, takeError := conflictClient.TakeHeldPlatformUpdate(ctx, "c1", "n1", "stale")
	var unexpected *UnexpectedResponseError
	if !errors.As(takeError, &unexpected) || unexpected.StatusCode != http.StatusConflict || unexpected.Detail != "The card changed since it was read" {
		t.Fatalf("a 409 must keep its status and detail, got %v", takeError)
	}
	unauthorisedClient, _ := recordingPlatformClient(t, http.StatusUnauthorized, `{"detail":"Unknown token"}`)
	if _, getError := unauthorisedClient.GetClusterBaseline(ctx, "c1"); !errors.Is(getError, ErrUnauthorized) {
		t.Fatalf("a 401 must be ErrUnauthorized, got %v", getError)
	}
	missingClient, _ := recordingPlatformClient(t, http.StatusNotFound, `{"detail":"Cluster not found"}`)
	_, sourceError := missingClient.GetClusterMetricsSource(ctx, "c1")
	if !errors.As(sourceError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("a 404 must keep its status, got %v", sourceError)
	}
}

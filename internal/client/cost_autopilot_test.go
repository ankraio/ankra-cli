package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

// costAutopilotGolden is a GET /api/v1/org/cloud-cost/autopilot body in the
// shape the cluster's openapi.json declares (CostAutopilotPolicy): a set
// policy with quiet hours and a pre-notice route, the tiers, a cluster whose
// environment set its tier and one under an override with no expiry.
const costAutopilotGolden = `{"is_set":true,` +
	`"defaults":{"production":"hands-off","staging":"scheduled","development":"managed","preview":"ephemeral","unknown":"hands-off"},` +
	`"recommended_defaults":{"production":"hands-off","staging":"scheduled","development":"managed","preview":"ephemeral","unknown":"hands-off"},` +
	`"quiet_hours":{"start":"22:00","end":"07:00","timezone":"Europe/Stockholm"},` +
	`"notification_route_id":"5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3","updated_by":"77777777-7777-4777-8777-777777777777",` +
	`"updated_at":"2026-09-20T10:00:00Z","environment_kinds":["production","staging","development","preview","unknown"],` +
	`"tiers":[{"tier":"hands-off","name":"Hands-off","applies_to":"Production","does":"Nothing on its own.",` +
	`"proposes":"Every change, for a person to approve.","rollback_window":"-"}],` +
	`"clusters":[{"cluster_id":"11111111-1111-4111-8111-111111111111","cluster_name":"eu-production","environment":"production",` +
	`"environment_kind":"production","override":null,` +
	`"reason":"eu-production is Hands-off as a production cluster, so every change waits for a person.","source":"environment","tier":"hands-off"},` +
	`{"cluster_id":"33333333-3333-4333-8333-333333333333","cluster_name":"staging-1","environment":"stage","environment_kind":"staging",` +
	`"override":{"tier":"managed","reason":"Load test week","set_by":"77777777-7777-4777-8777-777777777777",` +
	`"set_at":"2026-09-21T09:00:00Z","expires_at":null},` +
	`"reason":"staging-1 is Managed by an override: Load test week.","source":"override","tier":"managed"}]}`

func TestGetCostAutopilot_DecodesPolicyTiersAndClusters(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/org/cloud-cost/autopilot" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(costAutopilotGolden))
	}
	policy, err := newTestClient(t, handler).GetCostAutopilot()
	if err != nil {
		t.Fatalf("GetCostAutopilot: %v", err)
	}
	if !policy.IsSet || policy.Defaults["development"] != "managed" || len(policy.RecommendedDefaults) != 5 ||
		policy.QuietHours == nil || *policy.QuietHours != (CostAutopilotQuietHours{Start: "22:00", End: "07:00", Timezone: "Europe/Stockholm"}) ||
		policy.NotificationRouteID == nil || policy.UpdatedAt == nil || len(policy.EnvironmentKinds) != 5 {
		t.Fatalf("policy did not decode: %+v", policy)
	}
	if len(policy.Tiers) != 1 || policy.Tiers[0].Proposes != "Every change, for a person to approve." {
		t.Fatalf("tiers did not decode: %+v", policy.Tiers)
	}
	if len(policy.Clusters) != 2 || policy.Clusters[0].Override != nil || policy.Clusters[0].Source != "environment" {
		t.Fatalf("clusters did not decode: %+v", policy.Clusters)
	}
	override := policy.Clusters[1].Override
	if override == nil || override.Tier != "managed" || override.ExpiresAt != nil || policy.Clusters[1].EnvironmentKind != "staging" {
		t.Fatalf("an override with no expiry did not decode: %+v", policy.Clusters[1])
	}
}

func TestGetCostAutopilot_NeverSetDecodesNullsAndEmptyLists(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"is_set":false,"defaults":{"production":"hands-off"},"recommended_defaults":{},` +
			`"quiet_hours":null,"notification_route_id":null,"updated_by":null,"updated_at":null}`))
	}
	policy, err := newTestClient(t, handler).GetCostAutopilot()
	if err != nil {
		t.Fatalf("GetCostAutopilot: %v", err)
	}
	if policy.IsSet || policy.QuietHours != nil || policy.NotificationRouteID != nil || policy.UpdatedBy != nil || policy.UpdatedAt != nil {
		t.Fatalf("an unset policy must decode its nulls as nil: %+v", policy)
	}
	if policy.Clusters == nil || policy.Tiers == nil || policy.EnvironmentKinds == nil {
		t.Fatalf("absent lists must decode as empty, not nil: %+v", policy)
	}
}

type capturedAutopilotWrite struct {
	method, path, csrf string
	body               map[string]json.RawMessage
}

func captureAutopilotWrite(t *testing.T, response string) (*Client, *capturedAutopilotWrite) {
	t.Helper()
	captured := &capturedAutopilotWrite{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		captured.method, captured.path, captured.csrf = r.Method, r.URL.Path, r.Header.Get("X-Ankra-CSRF")
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &captured.body); decodeError != nil {
				t.Fatalf("body is not a JSON object: %v: %s", decodeError, raw)
			}
		}
		_, _ = w.Write([]byte(response))
	}
	return newTestClient(t, handler), captured
}

// A partial policy write sends the parts given and nothing else: an omitted
// part keeps its value on the platform, a cleared one is sent as null.
func TestUpdateCostAutopilot_OmittedIsNotCleared(t *testing.T) {
	testClient, captured := captureAutopilotWrite(t, costAutopilotGolden)
	if _, err := testClient.UpdateCostAutopilot(CostAutopilotPolicyUpdate{Defaults: map[string]string{"development": "managed"}}); err != nil {
		t.Fatalf("UpdateCostAutopilot: %v", err)
	}
	if captured.method != http.MethodPut || captured.path != "/api/v1/org/cloud-cost/autopilot" || captured.csrf != "" {
		t.Fatalf("request = %s %s (csrf %q)", captured.method, captured.path, captured.csrf)
	}
	if len(captured.body) != 1 || string(captured.body["defaults"]) != `{"development":"managed"}` {
		t.Fatalf("only the defaults were given, got %v", captured.body)
	}

	testClient, captured = captureAutopilotWrite(t, costAutopilotGolden)
	if _, err := testClient.UpdateCostAutopilot(CostAutopilotPolicyUpdate{ClearQuietHours: true, ClearNotificationRoute: true}); err != nil {
		t.Fatalf("UpdateCostAutopilot: %v", err)
	}
	if len(captured.body) != 2 || string(captured.body["quiet_hours"]) != "null" || string(captured.body["notification_route_id"]) != "null" {
		t.Fatalf("clearing sends null for exactly the cleared parts, got %v", captured.body)
	}

	route := "5a2b9c6d-0e1f-4a3b-8c5d-6e7f8091a2b3"
	testClient, captured = captureAutopilotWrite(t, costAutopilotGolden)
	if _, err := testClient.UpdateCostAutopilot(CostAutopilotPolicyUpdate{
		QuietHours:          &CostAutopilotQuietHours{Start: "22:00", End: "07:00", Timezone: "Europe/Stockholm"},
		NotificationRouteID: &route,
	}); err != nil {
		t.Fatalf("UpdateCostAutopilot: %v", err)
	}
	if len(captured.body) != 2 || string(captured.body["quiet_hours"]) != `{"start":"22:00","end":"07:00","timezone":"Europe/Stockholm"}` ||
		string(captured.body["notification_route_id"]) != `"`+route+`"` {
		t.Fatalf("setting sends the values, got %v", captured.body)
	}
	if _, present := captured.body["defaults"]; present {
		t.Fatalf("defaults were not given, so they must not be sent: %v", captured.body)
	}
}

const costAutopilotClusterBody = `{"cluster_id":"33333333-3333-4333-8333-333333333333","cluster_name":"staging-1","environment":"stage",` +
	`"environment_kind":"staging","override":{"tier":"managed","reason":"Load test week","set_by":"77777777-7777-4777-8777-777777777777",` +
	`"set_at":"2026-09-24T09:00:00Z","expires_at":"2026-09-27T09:00:00Z"},"reason":"staging-1 is Managed by an override: Load test week.",` +
	`"source":"override","tier":"managed"}`

func TestSetCostAutopilotOverride_SendsTheExpiryOnlyWhenGiven(t *testing.T) {
	testClient, captured := captureAutopilotWrite(t, costAutopilotClusterBody)
	cluster, err := testClient.SetCostAutopilotOverride("33333333-3333-4333-8333-333333333333",
		CostAutopilotOverrideRequest{Tier: "managed", Reason: "Load test week"})
	if err != nil {
		t.Fatalf("SetCostAutopilotOverride: %v", err)
	}
	if captured.method != http.MethodPut || captured.path != "/api/v1/org/cloud-cost/autopilot/clusters/33333333-3333-4333-8333-333333333333" {
		t.Fatalf("request = %s %s", captured.method, captured.path)
	}
	if len(captured.body) != 2 || string(captured.body["tier"]) != `"managed"` || string(captured.body["reason"]) != `"Load test week"` {
		t.Fatalf("an override with no expiry sends tier and reason only, got %v", captured.body)
	}
	if cluster.Override == nil || cluster.Override.ExpiresAt == nil || *cluster.Override.ExpiresAt != "2026-09-27T09:00:00Z" {
		t.Fatalf("the cluster did not decode: %+v", cluster)
	}

	expiresAt := "2026-10-01T00:00:00Z"
	testClient, captured = captureAutopilotWrite(t, costAutopilotClusterBody)
	if _, err := testClient.SetCostAutopilotOverride("33333333-3333-4333-8333-333333333333",
		CostAutopilotOverrideRequest{Tier: "managed", Reason: "Load test week", ExpiresAt: &expiresAt}); err != nil {
		t.Fatalf("SetCostAutopilotOverride: %v", err)
	}
	if string(captured.body["expires_at"]) != `"2026-10-01T00:00:00Z"` {
		t.Fatalf("a given expiry is sent, got %v", captured.body)
	}
}

func TestClearCostAutopilotOverride_SendsDeleteAndDecodesTheCluster(t *testing.T) {
	testClient, captured := captureAutopilotWrite(t, `{"cluster_id":"33333333-3333-4333-8333-333333333333","cluster_name":"staging-1",`+
		`"environment":"stage","environment_kind":"staging","override":null,"reason":"staging-1 is Scheduled as a staging cluster.",`+
		`"source":"environment","tier":"scheduled"}`)
	cluster, err := testClient.ClearCostAutopilotOverride("33333333-3333-4333-8333-333333333333")
	if err != nil {
		t.Fatalf("ClearCostAutopilotOverride: %v", err)
	}
	if captured.method != http.MethodDelete || captured.path != "/api/v1/org/cloud-cost/autopilot/clusters/33333333-3333-4333-8333-333333333333" {
		t.Fatalf("request = %s %s", captured.method, captured.path)
	}
	if cluster.Override != nil || cluster.Source != "environment" || cluster.Tier != "scheduled" {
		t.Fatalf("the cleared cluster did not decode: %+v", cluster)
	}
}

// The writes' permission refusal is the RBAC shape and names the permission;
// the cluster routes' 404 carries its sentence.
func TestCostAutopilot_RefusalsKeepTheirShape(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusForbidden, map[string]string{"detail": "permission_denied", "permission": "clusters.write",
			"scope_type": "cluster", "scope_id": "33333333-3333-4333-8333-333333333333"})
	}
	_, writeError := newTestClient(t, handler).SetCostAutopilotOverride("33333333-3333-4333-8333-333333333333",
		CostAutopilotOverrideRequest{Tier: "managed", Reason: "why"})
	var denied *PermissionDeniedError
	if !errors.As(writeError, &denied) || denied.Permission != "clusters.write" {
		t.Fatalf("error = %v", writeError)
	}

	handler = func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusNotFound, map[string]string{"detail": "Cluster not found"})
	}
	_, clearError := newTestClient(t, handler).ClearCostAutopilotOverride("33333333-3333-4333-8333-333333333333")
	var unexpected *UnexpectedResponseError
	if !errors.As(clearError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "Cluster not found" {
		t.Fatalf("error = %v", clearError)
	}

	handler = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	_, readError := newTestClient(t, handler).GetCostAutopilot()
	if !errors.As(readError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "" {
		t.Fatalf("error = %v", readError)
	}
}

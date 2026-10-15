package cmd

// Tests for the route check behind ship's Live line (ankra-4wl92c): the
// bake-off printed "Live:" for an Ingress whose backend named a Service that
// did not exist.

import (
	"encoding/json"
	"strings"
	"testing"
)

// The bake-off shape: the Ingress for the host sends traffic to Service
// bakeoff-conductor-hub, while the release's Service is conductor-hub.
func TestApplicationShipRefusesLiveWhenTheIngressBackendServiceIsMissing(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.deploymentsPayload = []byte(`{"deployments":[{"cluster_id":"` + shipTestClusterID +
		`","namespace":"shop","ingress_host":"bakeoff-conductor-hub.acme.ankra.cc","ingress_host_publication":"publisher_present"}]}`)
	mockClient.ingressItems = []interface{}{
		shipTestIngress("bakeoff-conductor-hub", "bakeoff-conductor-hub.acme.ankra.cc", "bakeoff-conductor-hub", 80),
	}
	mockClient.serviceItems = []interface{}{shipTestService("conductor-hub", 80)}

	output, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil {
		t.Fatalf("a URL whose Ingress names a missing Service must not be reported Live\nstdout: %s\nprogress: %s",
			output, progress)
	}
	if strings.Contains(output, "Live:") {
		t.Errorf("stdout must not claim Live: %q", output)
	}
	message := executeError.Error()
	for _, want := range []string{
		"https://bakeoff-conductor-hub.acme.ankra.cc does not route",
		`Ingress "bakeoff-conductor-hub" sends it to Service "bakeoff-conductor-hub", which does not exist in namespace "shop"`,
		"the namespace has conductor-hub",
		"ankra cluster get ingresses -n shop",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the error must contain %q: %v", want, message)
		}
	}
	// The Services must be read live: one the deploy just created may not
	// be in the platform's cache yet, and "missing" must never be stale.
	for _, request := range mockClient.resourceRequests {
		if request.Kind == "Service" && request.Namespace != "shop" {
			t.Errorf("the Service read must be scoped to the namespace: %+v", request)
		}
	}
}

// -o json keeps stdout one document: the URL is left out, the state says
// unroutable, url_problem says why, and the command still exits non-zero.
func TestApplicationShipReportsAnUnroutableURLInJSON(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.serviceItems = []interface{}{}

	output, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production", "-o", "json")
	if executeError == nil {
		t.Fatalf("an unroutable URL must fail ship\nprogress: %s", progress)
	}
	var result shipResult
	if decodeError := json.Unmarshal([]byte(output), &result); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, output)
	}
	if result.State != "unroutable" || result.URL != "" ||
		!strings.Contains(result.URLProblem, `Service "shop", which does not exist`) ||
		!strings.Contains(result.URLProblem, "there are none") {
		t.Errorf("structured result = %+v", result)
	}
}

// A Service with no ready endpoint routes nowhere: its selector matches no
// ready pod.
func TestApplicationShipRefusesLiveWhenTheBackendHasNoReadyEndpoint(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.endpointSliceItems = []interface{}{shipTestEndpointSlice("shop", false)}

	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil {
		t.Fatalf("a backend with no ready endpoint must not be reported Live\nprogress: %s", progress)
	}
	if !strings.Contains(executeError.Error(), `Service "shop", which has no ready endpoints`) {
		t.Errorf("the error must name the Service with no ready endpoints: %v", executeError)
	}
	found := false
	for _, request := range mockClient.resourceRequests {
		if request.Kind == "EndpointSlice" {
			found = true
			if request.LabelSelector != "kubernetes.io/service-name=shop" {
				t.Errorf("the endpoint read must select the Service's slices: %+v", request)
			}
		}
	}
	if !found {
		t.Error("the Service's EndpointSlices were never read")
	}
}

// The Ingress asking for a port the Service does not expose routes nowhere.
func TestApplicationShipRefusesLiveWhenTheServiceLacksTheIngressPort(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.serviceItems = []interface{}{shipTestService("shop", 8080)}

	_, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError == nil {
		t.Fatalf("a backend port the Service lacks must not be reported Live\nprogress: %s", progress)
	}
	if !strings.Contains(executeError.Error(), `port 80 of Service "shop", which the Service does not expose`) {
		t.Errorf("the error must name the missing port: %v", executeError)
	}
}

// A read that fails is not an empty namespace: ship cannot prove the route is
// broken, so it keeps the Live line and says the backend is unverified.
func TestApplicationShipKeepsLiveWhenTheServiceReadFails(t *testing.T) {
	fastShipPolling(t)
	mockClient := newApplicationShipMock()
	mockClient.failedResourceKinds = map[string]bool{"Service": true}

	output, progress, executeError := runApplicationShipCommand(t, mockClient, "--cluster", "production")
	if executeError != nil {
		t.Fatalf("an unreadable Service list must not fail ship: %v\nprogress: %s", executeError, progress)
	}
	if !strings.Contains(output, "Live: https://shop.acme.ankra.cc") {
		t.Errorf("stdout = %q", output)
	}
	if !strings.Contains(progress, "Could not read the namespace's services") ||
		!strings.Contains(progress, "forbidden: cannot list Service") {
		t.Errorf("the unverified backend must be said, with why: %s", progress)
	}
}

func TestShipIngressBackendsForHostUsesTheDefaultBackendForARuleWithoutPaths(t *testing.T) {
	ingress := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "web"},
		"spec": map[string]interface{}{
			"defaultBackend": map[string]interface{}{
				"service": map[string]interface{}{"name": "web", "port": map[string]interface{}{"name": "http"}},
			},
			"rules": []interface{}{
				map[string]interface{}{"host": "WEB.example.com"},
				map[string]interface{}{"host": "other.example.com"},
			},
		},
	}
	backends, hostFound := shipIngressBackendsForHost([]map[string]interface{}{ingress}, "web.example.com")
	if !hostFound || len(backends) != 1 || backends[0].serviceName != "web" || backends[0].portName != "http" {
		t.Fatalf("backends = %+v, hostFound = %v", backends, hostFound)
	}
	if !shipServiceExposesPort(shipServiceView{ports: []shipServicePort{{name: "http", number: "8080"}}}, backends[0]) {
		t.Error("a backend asking for a port by name is satisfied by a Service port of that name")
	}
}

func TestShipReadyEndpointCountTreatsAMissingConditionAsReady(t *testing.T) {
	slices := []map[string]interface{}{{
		"endpoints": []interface{}{
			map[string]interface{}{},
			map[string]interface{}{"conditions": map[string]interface{}{"ready": false}},
			map[string]interface{}{"conditions": map[string]interface{}{"ready": true}},
		},
	}}
	if ready := shipReadyEndpointCount(slices); ready != 2 {
		t.Errorf("ready endpoints = %d, want 2", ready)
	}
}

package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetUpgradePreflightReadsTheReportAndKeepsTheDocument(t *testing.T) {
	var requestedPath, requestedTarget string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestedPath = request.URL.Path
		requestedTarget = request.URL.Query().Get("target_version")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"cluster_id":"c","target_version":"v1.35.9+k3s1","verdict":"blocked","blocks":1,` +
			`"checks":[{"check":"pod_disruption_budgets","status":"block","message":"m","overridable_with_force":true,"evidence":{"blocking":[]}}]}`))
	}))
	defer server.Close()

	report, requestError := (&Client{BaseURL: server.URL, Token: "t", HTTP: server.Client()}).
		GetUpgradePreflight("hetzner", "c", "v1.35.9+k3s1")
	if requestError != nil {
		t.Fatalf("GetUpgradePreflight: %v", requestError)
	}
	if requestedPath != "/api/v1/clusters/hetzner/c/upgrade-preflight" || requestedTarget != "v1.35.9+k3s1" {
		t.Fatalf("the preflight is the provider's bearer route with the target as a query: %s %s", requestedPath, requestedTarget)
	}
	if report.Verdict != UpgradePreflightVerdictBlocked || len(report.Checks) != 1 || !report.Checks[0].OverridableWithForce ||
		!strings.Contains(string(report.RawJSON()), `"evidence"`) {
		t.Fatalf("the report and its document must be kept: %+v", report)
	}
}

func TestGetUpgradePreflightSurfacesTheRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"detail":"The Kubernetes upgrade preflight is only available for Hetzner clusters"}`))
	}))
	defer server.Close()

	_, requestError := (&Client{BaseURL: server.URL, Token: "t", HTTP: server.Client()}).GetUpgradePreflight("hetzner", "c", "v1")
	if requestError == nil || !strings.Contains(requestError.Error(), "only available for Hetzner clusters") {
		t.Fatalf("the platform's sentence must reach the caller: %v", requestError)
	}
	if errors.Is(requestError, ErrUnauthorized) {
		t.Fatal("a 409 is not an auth failure")
	}
}

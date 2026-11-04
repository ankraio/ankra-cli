package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The option's route is mounted per provider and the PUT takes no body: the
// method, the path and the empty body are pinned on the request itself.
func TestGatewayAPIRequestsAddressTheProviderRoute(t *testing.T) {
	type seen struct {
		method string
		path   string
		body   string
	}
	var requests []seen
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		requests = append(requests, seen{method: request.Method, path: request.URL.EscapedPath(), body: string(raw)})
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			_, _ = writer.Write([]byte(`{"available":true,"enabled":false,"phase":"off","crds":{"source":"","state":"absent","observed":[],"missing":["gatewayclasses.gateway.networking.k8s.io"],"addon_state":null,"addon_applied":"unknown","established":false,"detail":""},"traefik":{"addon":"traefik","managed":true,"values":"add","conflicts":[],"applied":"applied","last_applied_at":null},"cert_manager":{"addon":"cert-manager","managed":true,"values":"add","conflicts":[],"applied":"applied","last_applied_at":null},"values_hold":"","resource_ids":["r1"],"applies":"","note":"off"}`))
		case http.MethodPut:
			_, _ = writer.Write([]byte(`{"available":true,"enabled":true,"phase":"crds_pending","crds":{"source":"addon","state":"absent","observed":[],"missing":[],"addon_state":null,"addon_applied":"unknown","established":false,"detail":""},"traefik":{"addon":"traefik","managed":true,"values":"add","conflicts":[],"applied":"applied","last_applied_at":null},"cert_manager":{"addon":"cert-manager","managed":true,"values":"add","conflicts":[],"applied":"applied","last_applied_at":null},"values_hold":"","resource_ids":["r1"],"applies":"","note":"pending","changed":true,"recorded":true,"values_written":false,"commit_sha":null,"operation_id":null,"git_push_failure":null}`))
		}
	}))
	defer server.Close()

	apiClient := New("test-token", server.URL)
	status, getError := apiClient.GetClusterGatewayAPI(context.Background(), "hetzner", "cluster/one")
	if getError != nil {
		t.Fatalf("GET failed: %v", getError)
	}
	if status.Phase != GatewayAPIPhaseOff || status.Traefik.Values != GatewayAPIValuesAdd || len(status.CRDs.Missing) != 1 {
		t.Fatalf("GET decoded %+v", status)
	}
	result, putError := apiClient.EnableClusterGatewayAPI(context.Background(), "hetzner", "cluster/one")
	if putError != nil {
		t.Fatalf("PUT failed: %v", putError)
	}
	if !result.Recorded || result.ValuesWritten || result.Phase != GatewayAPIPhaseCRDsPending || result.CRDs.Source != "addon" {
		t.Fatalf("PUT decoded %+v", result)
	}

	wantPath := "/api/v1/clusters/hetzner/cluster%2Fone/gateway-api"
	if len(requests) != 2 {
		t.Fatalf("expected a GET and a PUT, got %+v", requests)
	}
	if requests[0].method != http.MethodGet || requests[0].path != wantPath {
		t.Errorf("GET went to %s %s, want GET %s", requests[0].method, requests[0].path, wantPath)
	}
	if requests[1].method != http.MethodPut || requests[1].path != wantPath {
		t.Errorf("PUT went to %s %s, want PUT %s", requests[1].method, requests[1].path, wantPath)
	}
	if requests[1].body != "" {
		t.Errorf("the PUT sent a body %q, want none", requests[1].body)
	}
}

// A 409 is the platform refusing the enable; its sentence rides on Detail so
// the command can show it without parsing the message.
func TestGatewayAPIEnableRefusalKeepsThePlatformsDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"detail":"The cluster's cloud provider stack is still being created or removed; turn the Gateway API on once it is up"}`))
	}))
	defer server.Close()

	_, putError := New("test-token", server.URL).EnableClusterGatewayAPI(context.Background(), "ovh", "cluster-id")
	var unexpected *UnexpectedResponseError
	if !errors.As(putError, &unexpected) {
		t.Fatalf("expected an UnexpectedResponseError, got %T %v", putError, putError)
	}
	if unexpected.StatusCode != http.StatusConflict || unexpected.Detail == "" {
		t.Fatalf("expected the 409 with its detail, got %d %q", unexpected.StatusCode, unexpected.Detail)
	}
}

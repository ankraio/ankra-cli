package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpdateBastionAllowedIPsPutsTheWholeList(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotMethod, gotPath = request.Method, request.URL.Path
		body, _ := io.ReadAll(request.Body)
		gotBody = string(body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"node_id":"n-1","kind":"upcloud_bastion","name":"b","bastion_allowed_ips":["203.0.113.7/32"],"operation_id":"op-1"}`))
	}))
	defer server.Close()
	testClient := &Client{BaseURL: server.URL, Token: "token", HTTP: server.Client()}

	result, updateError := testClient.UpdateUpcloudBastionAllowedIPs(context.Background(), "cluster-1", []string{"203.0.113.7"})
	if updateError != nil {
		t.Fatalf("UpdateUpcloudBastionAllowedIPs: %v", updateError)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v1/clusters/upcloud/cluster-1/bastion/allowed-ips" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody != `{"bastion_allowed_ips":["203.0.113.7"]}` {
		t.Fatalf("body = %s", gotBody)
	}
	if result.OperationID == nil || *result.OperationID != "op-1" || result.BastionAllowedIPs[0] != "203.0.113.7/32" {
		t.Fatalf("result = %+v", result)
	}
}

// Clearing must send a present empty list: the platform reads a missing
// member as a validation error, never as "clear".
func TestUpdateBastionAllowedIPsSendsAnEmptyListToClear(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		gotBody = string(body)
		_, _ = writer.Write([]byte(`{"name":"b","bastion_allowed_ips":[],"operation_id":null}`))
	}))
	defer server.Close()
	testClient := &Client{BaseURL: server.URL, Token: "token", HTTP: server.Client()}

	if _, updateError := testClient.UpdateDigitaloceanBastionAllowedIPs(context.Background(), "cluster-1", nil); updateError != nil {
		t.Fatalf("UpdateDigitaloceanBastionAllowedIPs: %v", updateError)
	}
	if gotBody != `{"bastion_allowed_ips":[]}` {
		t.Fatalf("body = %s, want an explicit empty list", gotBody)
	}
}

func TestUpdateBastionAllowedIPsSurfacesTheValidationDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = writer.Write([]byte(`{"detail":"bastion_allowed_ips entry \"2001:db8::/32\" is IPv6; the bastion is reached over IPv4, so list IPv4 addresses or CIDRs only"}`))
	}))
	defer server.Close()
	testClient := &Client{BaseURL: server.URL, Token: "token", HTTP: server.Client()}

	_, updateError := testClient.UpdateHetznerBastionAllowedIPs(context.Background(), "cluster-1", []string{"2001:db8::/32"})
	if updateError == nil || !strings.Contains(updateError.Error(), "is IPv6") {
		t.Fatalf("expected the platform's refusal to reach the caller, got %v", updateError)
	}
}

func TestCreateRequestsOmitAnUnsetBastionAllowlist(t *testing.T) {
	for name, request := range map[string]any{
		"hetzner":      CreateHetznerClusterRequest{},
		"ovh":          CreateOvhClusterRequest{},
		"upcloud":      CreateUpcloudClusterRequest{},
		"digitalocean": CreateDigitaloceanClusterRequest{},
	} {
		encoded, _ := json.Marshal(request)
		if strings.Contains(string(encoded), "bastion_allowed_ips") {
			t.Errorf("%s: an unset allowlist must stay off the wire, got %s", name, encoded)
		}
	}
}

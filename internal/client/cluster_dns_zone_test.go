package client

import (
	"net/http"
	"testing"
)

func TestEnableClusterDNSZone_PostsAndDecodesZone(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/cluster-1/dns-zone" {
			t.Errorf("path = %s, want /api/v1/clusters/cluster-1/dns-zone", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, ClusterDNSZoneResponse{
			Success: true, FQDN: "abc123.org456.ankra.cc", State: "pending"})
	}
	testClient := newTestClient(t, handler)

	result, err := testClient.EnableClusterDNSZone("cluster-1")
	if err != nil {
		t.Fatalf("EnableClusterDNSZone: %v", err)
	}
	if result.FQDN != "abc123.org456.ankra.cc" {
		t.Errorf("fqdn = %s, want abc123.org456.ankra.cc", result.FQDN)
	}
	if result.State != "pending" {
		t.Errorf("state = %s, want pending", result.State)
	}
}

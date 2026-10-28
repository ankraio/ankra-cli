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

func TestDisableClusterDNSZone_DeletesAndDecodesZone(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/cluster-1/dns-zone" {
			t.Errorf("path = %s, want /api/v1/clusters/cluster-1/dns-zone", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, ClusterDNSZoneResponse{
			Success: true, FQDN: "abc123.org456.ankra.cc", State: "deleting"})
	}
	testClient := newTestClient(t, handler)

	result, err := testClient.DisableClusterDNSZone("cluster-1")
	if err != nil {
		t.Fatalf("DisableClusterDNSZone: %v", err)
	}
	if result.State != "deleting" {
		t.Errorf("state = %s, want deleting", result.State)
	}
}

func TestGetClusterDNSZone_ReadsWithoutMutating(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/cluster-1/dns-zone" {
			t.Errorf("path = %s, want /api/v1/clusters/cluster-1/dns-zone", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, ClusterDNSZoneResponse{
			Success: true, FQDN: "abc123.org456.ankra.cc", State: "active"})
	}
	testClient := newTestClient(t, handler)

	result, err := testClient.GetClusterDNSZone("cluster-1")
	if err != nil {
		t.Fatalf("GetClusterDNSZone: %v", err)
	}
	if result.FQDN != "abc123.org456.ankra.cc" || result.State != "active" {
		t.Errorf("result = %+v", result)
	}
}

func TestGetClusterDNSZone_ReportsNoneForAClusterWithoutAZone(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, ClusterDNSZoneResponse{Success: true, FQDN: "", State: "none"})
	}
	testClient := newTestClient(t, handler)

	result, err := testClient.GetClusterDNSZone("cluster-1")
	if err != nil {
		t.Fatalf("GetClusterDNSZone: %v", err)
	}
	if result.FQDN != "" || result.State != "none" {
		t.Errorf("result = %+v, want the none state", result)
	}
}

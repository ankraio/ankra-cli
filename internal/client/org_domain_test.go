package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGetOrganisationDomain_ReadsTheAiEnvironmentTwin(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/ai-environment" {
			t.Errorf("path = %s, want /api/v1/org/ai-environment", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"dns_root_domain": "smartoptics.dev", "dns_root_domain_default": "ankra.cc"})
	}
	testClient := newTestClient(t, handler)

	domain, err := testClient.GetOrganisationDomain(context.Background())
	if err != nil {
		t.Fatalf("GetOrganisationDomain: %v", err)
	}
	if domain.DNSRootDomain != "smartoptics.dev" || domain.DNSRootDomainDefault != "ankra.cc" {
		t.Errorf("domain = %+v", domain)
	}
}

func TestGetOrganisationDomain_ReportsAnUnsetCustomDomainAsEmpty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"dns_root_domain": nil, "dns_root_domain_default": "ankra.cc"})
	}
	testClient := newTestClient(t, handler)

	domain, err := testClient.GetOrganisationDomain(context.Background())
	if err != nil {
		t.Fatalf("GetOrganisationDomain: %v", err)
	}
	if domain.DNSRootDomain != "" || domain.DNSRootDomainDefault != "ankra.cc" {
		t.Errorf("domain = %+v", domain)
	}
}

func TestSetOrganisationDomain_SendsOnlyTheRootDomainMember(t *testing.T) {
	var receivedBody map[string]any
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		if unmarshalError := json.Unmarshal(raw, &receivedBody); unmarshalError != nil {
			t.Fatalf("request body is not json: %v", unmarshalError)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"dns_root_domain": "smartoptics.dev", "dns_root_domain_default": "ankra.cc"})
	}
	testClient := newTestClient(t, handler)

	if _, err := testClient.SetOrganisationDomain(context.Background(), "smartoptics.dev"); err != nil {
		t.Fatalf("SetOrganisationDomain: %v", err)
	}
	if len(receivedBody) != 1 || receivedBody["dns_root_domain"] != "smartoptics.dev" {
		t.Errorf("body = %v, want only dns_root_domain", receivedBody)
	}
}

func TestSetOrganisationDomain_ClearsWithAnExplicitNull(t *testing.T) {
	var receivedRaw string
	handler := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		receivedRaw = string(raw)
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"dns_root_domain": nil, "dns_root_domain_default": "ankra.cc"})
	}
	testClient := newTestClient(t, handler)

	if _, err := testClient.SetOrganisationDomain(context.Background(), ""); err != nil {
		t.Fatalf("SetOrganisationDomain: %v", err)
	}
	if !strings.Contains(receivedRaw, `"dns_root_domain":null`) {
		t.Errorf("body = %s, want an explicit null so the backend clears the field", receivedRaw)
	}
}

func TestSetOrganisationDomain_SurfacesTheBlockingRows(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]any{
			"detail": "Changing dns_root_domain requires removing the organisation's cluster DNS zones and DNS records first. Ankra then re-creates your zone under the new domain automatically.",
			"blocking_cluster_zones": []map[string]any{{
				"cluster_id": "c-1", "cluster_name": "playground",
				"fqdn": "abc.org1234.ankra.cc", "state": "active"}},
			"blocking_dns_records": []map[string]any{{
				"id": "r-1", "name": "chat.org1234.ankra.cc",
				"record_type": "A", "state": "active"}},
		})
	}
	testClient := newTestClient(t, handler)

	_, err := testClient.SetOrganisationDomain(context.Background(), "smartoptics.dev")
	var blocked *OrganisationDomainBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %v, want an OrganisationDomainBlockedError", err)
	}
	if len(blocked.ClusterZones) != 1 || blocked.ClusterZones[0].ClusterName != "playground" {
		t.Errorf("cluster zones = %+v", blocked.ClusterZones)
	}
	if len(blocked.DnsRecords) != 1 || blocked.DnsRecords[0].Name != "chat.org1234.ankra.cc" {
		t.Errorf("dns records = %+v", blocked.DnsRecords)
	}
}

func TestSetOrganisationDomain_SurfacesAPlainValidationDetail(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]any{
			"detail": "dns_root_domain must be a bare domain like example.com."})
	}
	testClient := newTestClient(t, handler)

	_, err := testClient.SetOrganisationDomain(context.Background(), "not a domain!")
	if err == nil || err.Error() != "dns_root_domain must be a bare domain like example.com." {
		t.Fatalf("error = %v, want the backend detail verbatim", err)
	}
	var blocked *OrganisationDomainBlockedError
	if errors.As(err, &blocked) {
		t.Fatalf("a plain validation refusal must not decode as a blocked switch: %v", err)
	}
}

func TestGetOrganisationDomain_ReportsUnauthorized(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}
	testClient := newTestClient(t, handler)

	if _, err := testClient.GetOrganisationDomain(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
}

func TestListOrganisationClusterDnsZones_ReadsTheInventory(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/dns/cluster-zones" {
			t.Errorf("path = %s, want /api/v1/org/dns/cluster-zones", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, DnsClusterZonesListResult{Items: []DnsClusterZone{
			{ClusterID: "c-1", ClusterName: "playground", FQDN: "abc.org1234.ankra.cc", State: "active"},
		}})
	}
	testClient := newTestClient(t, handler)

	list, err := testClient.ListOrganisationClusterDnsZones(context.Background())
	if err != nil {
		t.Fatalf("ListOrganisationClusterDnsZones: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ClusterName != "playground" {
		t.Errorf("items = %+v", list.Items)
	}
}

func TestListOrganisationClusterDnsZones_ReportsAnEmptyInventory(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, DnsClusterZonesListResult{Items: []DnsClusterZone{}})
	}
	testClient := newTestClient(t, handler)

	list, err := testClient.ListOrganisationClusterDnsZones(context.Background())
	if err != nil {
		t.Fatalf("ListOrganisationClusterDnsZones: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("items = %+v, want empty", list.Items)
	}
}

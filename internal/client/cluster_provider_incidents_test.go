package client

import (
	"net/http"
	"testing"
	"time"
)

func TestGetClusterProviderIncidents_ReadsTheClusterRoute(t *testing.T) {
	startedAt := time.Date(2026, 11, 24, 10, 4, 0, 0, time.UTC)
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/clusters/cluster-1/provider-incidents" {
			t.Errorf("path = %s, want /api/v1/clusters/cluster-1/provider-incidents", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, ProviderIncidentsResponse{
			ClusterID: "cluster-1", Provider: "ovh", ProviderName: "OVH", FeedState: ProviderFeedStateOK,
			Regions: []string{"EU-WEST-PAR"}, Zones: []string{"EU-WEST-PAR-A"}, Machines: 3,
			OpenIncidentsOnPages: 5,
			Incidents: []ProviderIncidentMatch{{
				Title: "[EU-WEST-PAR-A][Public Cloud] - Compute Instance incident notification", Status: "identified",
				StartedAt: &startedAt, Link: "https://stspg.io/hjfl3mwph7x9", Match: "zone",
				MatchedLocation: "EU-WEST-PAR-A",
			}},
		})
	}
	testClient := newTestClient(t, handler)

	result, err := testClient.GetClusterProviderIncidents("cluster-1")
	if err != nil {
		t.Fatalf("GetClusterProviderIncidents: %v", err)
	}
	if result.FeedState != ProviderFeedStateOK || result.OpenIncidentsOnPages != 5 || len(result.Incidents) != 1 {
		t.Fatalf("result = %+v", result)
	}
	incident := result.Incidents[0]
	if incident.Match != "zone" || incident.MatchedLocation != "EU-WEST-PAR-A" ||
		incident.StartedAt == nil || !incident.StartedAt.Equal(startedAt) {
		t.Errorf("incident = %+v", incident)
	}
}

func TestGetClusterProviderIncidents_SurfacesAnErrorStatus(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	testClient := newTestClient(t, handler)

	if _, err := testClient.GetClusterProviderIncidents("cluster-1"); err == nil {
		t.Fatal("a 404 must be an error, not an empty report")
	}
}

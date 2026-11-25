package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

const providerStatusTestClusterID = "e781e5e1-d1d6-40f6-b088-d08acf15f7dc"

// providerStatusMock answers the provider status read it holds and records
// the cluster it was asked about.
type providerStatusMock struct {
	readCalls []string
	response  client.ProviderIncidentsResponse
}

func (m *providerStatusMock) GetClusterProviderIncidents(clusterID string) (*client.ProviderIncidentsResponse, error) {
	m.readCalls = append(m.readCalls, clusterID)
	response := m.response
	return &response, nil
}

func runClusterProviderStatus(t *testing.T, mock *providerStatusMock, arguments ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	clusterProviderStatusCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		_ = flag.Value.Set(flag.DefValue)
		flag.Changed = false
	})
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs(append([]string{"cluster", "provider-status"}, arguments...))
	executeError := rootCmd.Execute()
	return output.String(), executeError
}

// providerStatusIncidentResponse is OVH's answer for prod-multi-az on
// 2026-11-24: the compute incident in EU-WEST-PAR-A matched on the zone.
func providerStatusIncidentResponse() client.ProviderIncidentsResponse {
	startedAt := time.Date(2026, 11, 24, 10, 4, 0, 0, time.UTC)
	return client.ProviderIncidentsResponse{
		ClusterID: providerStatusTestClusterID, Provider: "ovh", ProviderName: "OVH",
		FeedState: client.ProviderFeedStateOK, Regions: []string{"EU-WEST-PAR"},
		Zones:    []string{"EU-WEST-PAR-A", "EU-WEST-PAR-B", "EU-WEST-PAR-C"},
		Machines: 6, OpenIncidentsOnPages: 5,
		Incidents: []client.ProviderIncidentMatch{{
			Title:  "[EU-WEST-PAR-A][Public Cloud] - Compute Instance incident notification",
			Status: "identified", StartedAt: &startedAt, Link: "https://stspg.io/hjfl3mwph7x9",
			Match: "zone", MatchedLocation: "EU-WEST-PAR-A",
		}},
		Summary: "OVH reports an open incident in EU-WEST-PAR-A since 2026-11-24 10:04 UTC.",
	}
}

func TestClusterProviderStatus_ListsTheMatchedIncident(t *testing.T) {
	mock := &providerStatusMock{response: providerStatusIncidentResponse()}

	output, executeError := runClusterProviderStatus(t, mock, providerStatusTestClusterID)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.readCalls) != 1 || mock.readCalls[0] != providerStatusTestClusterID {
		t.Fatalf("read calls = %v", mock.readCalls)
	}
	for _, want := range []string{
		"Provider:  OVH", "Zones:     EU-WEST-PAR-A, EU-WEST-PAR-B, EU-WEST-PAR-C", "Status page: read",
		"zone", "2026-11-24 10:04", "identified", "[EU-WEST-PAR-A][Public Cloud]",
		"https://stspg.io/hjfl3mwph7x9", "OVH reports an open incident in EU-WEST-PAR-A",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}

// An unreadable status page says the status could not be checked: an empty
// list must never read as "the provider reports nothing".
func TestClusterProviderStatus_SaysAnUnreadablePageCouldNotBeChecked(t *testing.T) {
	mock := &providerStatusMock{response: client.ProviderIncidentsResponse{
		ClusterID: providerStatusTestClusterID, Provider: "ovh", ProviderName: "OVH",
		FeedState: client.ProviderFeedStateUnknown, FeedReason: "unreachable",
		Regions: []string{"EU-WEST-PAR"}, Machines: 2, MachinesWithoutRecordedZone: 2,
	}}

	output, executeError := runClusterProviderStatus(t, mock, providerStatusTestClusterID)
	if executeError != nil {
		t.Fatalf("an unreadable page is an answer, not a failure: %v\noutput: %s", executeError, output)
	}
	for _, want := range []string{
		"Status page: could not be read (unreachable)", "2 without a recorded zone",
		"could not be checked. This does not mean the provider reports nothing.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "No open incident") {
		t.Errorf("an unreadable page must not read as no incident:\n%s", output)
	}
}

func TestClusterProviderStatus_SaysAReadPageWithNothingMatching(t *testing.T) {
	mock := &providerStatusMock{response: client.ProviderIncidentsResponse{
		ClusterID: providerStatusTestClusterID, Provider: "scaleway", ProviderName: "Scaleway",
		FeedState: client.ProviderFeedStateOK, Regions: []string{"FR-PAR"}, Zones: []string{"FR-PAR-1"},
		Machines: 3, OpenIncidentsOnPages: 2,
	}}

	output, executeError := runClusterProviderStatus(t, mock, providerStatusTestClusterID)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "No open incident on Scaleway's status page matches this cluster (2 open in total).") {
		t.Errorf("output = %s", output)
	}
}

func TestClusterProviderStatus_MarksARegionMatchWithoutARecordedZone(t *testing.T) {
	response := providerStatusIncidentResponse()
	response.Incidents[0].Match = "region"
	response.Incidents[0].MatchedLocation = "EU-WEST-PAR"
	response.Incidents[0].ZoneUnknown = true
	mock := &providerStatusMock{response: response}

	output, executeError := runClusterProviderStatus(t, mock, providerStatusTestClusterID)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "EU-WEST-PAR (zone not recorded)") {
		t.Errorf("a region match on an unrecorded zone must say so:\n%s", output)
	}
}

func TestClusterProviderStatus_PrintsThePlatformAnswerAsJSON(t *testing.T) {
	mock := &providerStatusMock{response: providerStatusIncidentResponse()}

	output, executeError := runClusterProviderStatus(t, mock, providerStatusTestClusterID, "-o", "json")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	var document map[string]any
	if decodeError := json.Unmarshal([]byte(output), &document); decodeError != nil {
		t.Fatalf("-o json must print only JSON: %v\n%s", decodeError, output)
	}
	if document["feed_state"] != "ok" || document["provider"] != "ovh" {
		t.Errorf("document = %v", document)
	}
	incidents, _ := document["incidents"].([]any)
	if len(incidents) != 1 || incidents[0].(map[string]any)["match"] != "zone" {
		t.Errorf("incidents = %v", document["incidents"])
	}
}

// A page that answered but does not list where the cluster runs, and a
// managed Kubernetes cluster, are said in words, never as "no incident".
func TestClusterProviderStatus_SaysWhyNothingCouldBeChecked(t *testing.T) {
	for reason, want := range map[string]string{
		"location_not_listed": "Status page: read, but it does not list the cluster's region or zones",
		"managed_kubernetes":  "Status page: none read: Ankra does not read where a managed Kubernetes cluster's nodes run yet",
	} {
		mock := &providerStatusMock{response: client.ProviderIncidentsResponse{
			ClusterID: providerStatusTestClusterID, Provider: "ovh", ProviderName: "OVH",
			FeedState: client.ProviderFeedStateUnknown, FeedReason: reason, Regions: []string{"EU-WEST-PAR"},
		}}
		output, executeError := runClusterProviderStatus(t, mock, providerStatusTestClusterID)
		if executeError != nil {
			t.Fatalf("%s: execute failed: %v", reason, executeError)
		}
		if !strings.Contains(output, want) || strings.Contains(output, "No open incident") {
			t.Errorf("%s: output lacks %q or reads as no incident:\n%s", reason, want, output)
		}
	}
}

// A cluster with no recorded region or zone is said as that, not as a page
// that leaves its place out; and a provider-wide incident beside it must not
// claim a page went unread.
func TestClusterProviderStatus_SaysWhenNoPlaceIsRecorded(t *testing.T) {
	mock := &providerStatusMock{response: client.ProviderIncidentsResponse{
		ClusterID: providerStatusTestClusterID, Provider: "ovh", ProviderName: "OVH",
		FeedState: client.ProviderFeedStateUnknown, FeedReason: "location_not_listed", OpenIncidentsOnPages: 1,
		Incidents: []client.ProviderIncidentMatch{{Title: "[Global] - Backbone", Status: "investigating",
			Match: "provider_wide", Link: "https://stspg.io/abc"}},
	}}
	output, executeError := runClusterProviderStatus(t, mock, providerStatusTestClusterID)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	for _, want := range []string{
		"Status page: read, but Ankra has no region or zone recorded for this cluster",
		"provider-wide", "Ankra cannot tell whether more incidents are open where this cluster runs.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "could be read") {
		t.Errorf("every page was read:\n%s", output)
	}
}

// When one of a provider's status pages could not be read, each page is
// listed with its own state, so the output says which one failed and why.
func TestClusterProviderStatus_ListsEachPageWhenOneCouldNotBeRead(t *testing.T) {
	fetchedAt := time.Date(2026, 11, 24, 10, 7, 0, 0, time.UTC)
	mock := &providerStatusMock{response: client.ProviderIncidentsResponse{
		ClusterID: providerStatusTestClusterID, Provider: "ovh", ProviderName: "OVH",
		FeedState: client.ProviderFeedStateUnknown, FeedReason: "timeout", Regions: []string{"GRA11"},
		Feeds: []client.ProviderStatusFeed{
			{Page: "https://public-cloud.status-ovhcloud.com", State: client.ProviderFeedStateOK, FetchedAt: &fetchedAt},
			{Page: "https://network.status-ovhcloud.com", State: client.ProviderFeedStateUnknown, Reason: "timeout"},
		},
	}}
	output, executeError := runClusterProviderStatus(t, mock, providerStatusTestClusterID)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	for _, want := range []string{
		"  https://public-cloud.status-ovhcloud.com: read at 2026-11-24 10:07 UTC",
		"  https://network.status-ovhcloud.com: could not be read (timed out)",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}

// One page, or every page read, needs no per-page list.
func TestClusterProviderStatus_ListsNoPagesWhenEveryPageWasRead(t *testing.T) {
	response := providerStatusIncidentResponse()
	response.Feeds = []client.ProviderStatusFeed{
		{Page: "https://public-cloud.status-ovhcloud.com", State: client.ProviderFeedStateOK},
		{Page: "https://network.status-ovhcloud.com", State: client.ProviderFeedStateOK},
	}
	output, executeError := runClusterProviderStatus(t, &providerStatusMock{response: response}, providerStatusTestClusterID)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if strings.Contains(output, "  https://network.status-ovhcloud.com:") {
		t.Errorf("an ok answer must not list its pages:\n%s", output)
	}
}

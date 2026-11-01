package client

import (
	"net/http"
	"strings"
	"testing"
)

const (
	wasteServerFindingID       = "6f1a2b3c-4d5e-4f60-8a7b-9c0d1e2f3a4b"
	wasteAddressFindingID      = "7a2b3c4d-5e6f-4a70-9b8c-0d1e2f3a4b5c"
	wasteStoppedServerSentence = "I understand this server and the data on its disks are deleted, and the snapshot taken first is deleted on its retention date."
)

// cloudWasteGolden is a GET /api/v1/org/cloud-cost/waste body in the shape
// the cluster's openapi.json declares (CloudWasteResponse) on a platform
// with cluster#5646: two findings of different kinds and the consent policy
// with consent_by_kind.
const cloudWasteGolden = `{"has_data":true,"scanned_at":"2026-10-30T08:00:00","currency":"eur","total_monthly_cost_cents":1800,` +
	`"unpriced_finding_count":0,"shared_not_counted_monthly_cost_cents":0,"shared_not_counted_finding_count":0,"credential_errors":[],` +
	`"findings":[{"id":"` + wasteServerFindingID + `","provider":"hetzner","kind":"stopped_server","external_id":"4711","resource_name":"web-1",` +
	`"region":"fsn1","credential_name":"hetzner-main","cluster_id":null,"cluster_name":null,"details":{},"monthly_cost_cents":1200,` +
	`"first_seen_at":"2026-10-01T08:00:00","last_seen_at":"2026-10-30T08:00:00","origin_namespace":null,"shared_with_org_count":0,"counts_toward_savings":true},` +
	`{"id":"` + wasteAddressFindingID + `","provider":"hetzner","kind":"unassigned_ip","external_id":"88","resource_name":"203.0.113.7",` +
	`"region":"fsn1","credential_name":"hetzner-main","cluster_id":null,"cluster_name":null,"details":{},"monthly_cost_cents":600,` +
	`"first_seen_at":"2026-10-01T08:00:00","last_seen_at":"2026-10-30T08:00:00","origin_namespace":null,"shared_with_org_count":0,"counts_toward_savings":true}],` +
	`"consent":{"kinds":["unattached_volume","released_pv","stopped_server","unassigned_ip","idle_load_balancer"],"min_unattached_days":30,` +
	`"minimum_days_by_kind":{"stopped_server":14,"unassigned_ip":7},` +
	`"sentence_by_kind":{"stopped_server":"` + wasteStoppedServerSentence + `",` +
	`"unassigned_ip":"I understand a released address cannot be recovered, and anything still pointing at it stops working."},` +
	`"consent_by_kind":{"stopped_server":"delete_stopped_server_acknowledged","unassigned_ip":"release_unassigned_ip_acknowledged"},` +
	`"snapshot_retention_days":30},"unattributed_finding_count":0,"unattributed_monthly_cost_cents":0,"scope":{"kind":"organisation"}}`

// The listing is read organisation-wide even under a cost project scope: a
// proposal is not filed under a project, so the finding it names may sit
// outside one project's listing.
func TestGetCloudWaste_ReadsTheOrganisationListing(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, cloudWasteGolden)
	testClient.SetCostProjectScope(CostProjectScopeAll)
	waste, err := testClient.GetCloudWaste()
	if err != nil {
		t.Fatalf("GetCloudWaste: %v", err)
	}
	request := platform.last(t)
	if request.method != http.MethodGet || request.path != "/api/v1/org/cloud-cost/waste" || request.query != "" {
		t.Fatalf("request = %s %s?%s, want GET /api/v1/org/cloud-cost/waste with no project scope", request.method, request.path, request.query)
	}
	if len(waste.Findings) != 2 || waste.Findings[0].Kind != "stopped_server" || waste.Findings[0].ResourceName != "web-1" ||
		waste.Consent.ConsentByKind["unassigned_ip"] != "release_unassigned_ip_acknowledged" ||
		waste.Consent.SentenceByKind["stopped_server"] != wasteStoppedServerSentence {
		t.Fatalf("the listing did not decode: %+v", waste)
	}
}

func TestGetCloudWaste_AListingWithNoFindingsDecodesEmpty(t *testing.T) {
	testClient, _ := newRecordingClient(t, http.StatusOK, `{"findings":null,"consent":{"kinds":[]}}`)
	waste, err := testClient.GetCloudWaste()
	if err != nil {
		t.Fatalf("GetCloudWaste: %v", err)
	}
	if waste.Findings == nil || len(waste.Findings) != 0 || waste.Consent.ConsentByKind != nil {
		t.Fatalf("no findings is an empty list and an absent consent_by_kind stays absent, got %+v", waste)
	}
}

func wasteCleanupProposal(findingID string) *DecisionProposal {
	return &DecisionProposal{ID: decisionWasteID, Kind: "waste_cleanup",
		Plan: map[string]any{"consent_required": true, "parameters": map[string]any{"waste_finding_id": findingID}}}
}

func decodedCloudWaste(t *testing.T, body string) *CloudWaste {
	t.Helper()
	testClient, _ := newRecordingClient(t, http.StatusOK, body)
	waste, err := testClient.GetCloudWaste()
	if err != nil {
		t.Fatalf("GetCloudWaste: %v", err)
	}
	return waste
}

// Each waste cleanup sends the consent naming its own finding's kind
// (ankra-bn86k4.6.16.2): a stopped server's value for a stopped server, an
// address's for an address, never the value of another finding on the page.
func TestKindConsentFor_NamesTheKindOfTheProposalsFinding(t *testing.T) {
	waste := decodedCloudWaste(t, cloudWasteGolden)
	consent, found := waste.KindConsentFor(wasteCleanupProposal(wasteServerFindingID))
	if !found || consent.Value != "delete_stopped_server_acknowledged" || consent.Kind != "stopped_server" ||
		consent.ResourceName != "web-1" || consent.Sentence != wasteStoppedServerSentence {
		t.Fatalf("the stopped server's consent = %+v (found %v)", consent, found)
	}
	consent, found = waste.KindConsentFor(wasteCleanupProposal(strings.ToUpper(wasteAddressFindingID)))
	if !found || consent.Value != "release_unassigned_ip_acknowledged" || consent.Kind != "unassigned_ip" || consent.ResourceName != "203.0.113.7" {
		t.Fatalf("the address's consent, its id in upper case = %+v (found %v)", consent, found)
	}
}

// Wherever the platform does not name the kind's consent, or the proposal's
// finding cannot be matched, nothing is found and the caller keeps the
// general consent the platform takes for one finding.
func TestKindConsentFor_FindsNothingWhereThePlatformNamesNone(t *testing.T) {
	olderPlatform := decodedCloudWaste(t, strings.Replace(cloudWasteGolden,
		`"consent_by_kind":{"stopped_server":"delete_stopped_server_acknowledged","unassigned_ip":"release_unassigned_ip_acknowledged"},`, "", 1))
	current := decodedCloudWaste(t, cloudWasteGolden)
	noParameters := wasteCleanupProposal(wasteServerFindingID)
	noParameters.Plan = map[string]any{"consent_required": true}
	rightSize := wasteCleanupProposal(wasteServerFindingID)
	rightSize.Kind = "right_size"
	for _, testCase := range []struct {
		name     string
		waste    *CloudWaste
		proposal *DecisionProposal
	}{
		{"a platform before consent_by_kind", olderPlatform, wasteCleanupProposal(wasteServerFindingID)},
		{"a finding no longer listed", current, wasteCleanupProposal("9b8c7d6e-5f4a-4b3c-8d2e-1f0a9b8c7d6e")},
		{"a plan naming no finding", current, noParameters},
		{"a proposal of another kind", current, rightSize},
		{"no proposal", current, nil},
		{"no listing", nil, wasteCleanupProposal(wasteServerFindingID)},
	} {
		if consent, found := testCase.waste.KindConsentFor(testCase.proposal); found || consent != (WasteKindConsent{}) {
			t.Fatalf("%s: found %+v, want nothing", testCase.name, consent)
		}
	}
}

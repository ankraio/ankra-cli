package client

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func newSecurityTestServer(t *testing.T, body string, capture *http.Request) (*httptest.Server, *Client) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		*capture = *request
		if request.Header.Get("Authorization") != "Bearer test-token" {
			responseWriter.WriteHeader(http.StatusUnauthorized)
			return
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, New("test-token", server.URL)
}

func TestListSecurityFindingsEncodesEveryFilterAndSort(t *testing.T) {
	var captured http.Request
	_, apiClient := newSecurityTestServer(t, `{
        "result": [{
            "id": "632d93d6-f07b-4618-ad37-ae453409e928",
            "provider": "trivy_operator", "cve_id": "CVE-2025-24813", "package_type": "maven",
            "package_name": "org.apache.tomcat.embed:tomcat-embed-core", "severity": "CRITICAL",
            "title": null, "primary_link": null, "score": 9.8,
            "first_seen_at": "2026-08-01T00:00:00Z", "last_seen_at": "2026-08-28T20:00:00Z",
            "occurrences": 4, "affected_clusters": 1, "affected_workloads": 4, "fixable_occurrences": 4,
            "disposition_counts": {"open": 4, "acknowledged": 0, "accepted_risk": 0, "resolved": 0},
            "known_exploited": true, "kev_date_added": "2025-04-01", "kev_due_date": "2025-04-22",
            "kev_ransomware_use": false, "epss_score": 0.997, "epss_percentile": 0.999,
            "kev_vendor_project": "Apache", "kev_product": "Tomcat",
            "kev_vulnerability_name": "Apache Tomcat Path Equivalence Vulnerability",
            "kev_required_action": "Apply mitigations per vendor instructions."
        }],
        "pagination": {"page": 2, "page_size": 10, "total_pages": 3, "total_count": 22},
        "facets": {"severity": [], "status": [], "addons": [], "clusters": [], "exploited": [{"value": "known_exploited", "count": 22}]},
        "scanner": {"status": "degraded", "last_scan": null, "fresh_clusters": 11, "stale_clusters": 1, "unscanned_clusters": 15, "stale_after_seconds": 7200},
        "intelligence": {"kev_synced_at": "2026-08-28T20:13:11Z", "epss_synced_at": null, "kev_listed": 1685}
    }`, &captured)

	fixable := true
	knownExploited := true
	list, listError := apiClient.ListSecurityFindings(SecurityFindingsOptions{
		Page:           2,
		PageSize:       10,
		Search:         " tomcat ",
		Severities:     []string{"Critical", "high"},
		Statuses:       []string{"open", "acknowledged"},
		Fixable:        &fixable,
		KnownExploited: &knownExploited,
		ClusterID:      "0cfbf8ab-8f3a-4a9f-9a9c-5c1f0b4d1d1c",
		AddonSlug:      "nginx-ingress",
		Namespace:      "payments",
		Sort:           "exploitability",
		Order:          "desc",
	})
	if listError != nil {
		t.Fatalf("ListSecurityFindings returned an error: %v", listError)
	}
	if captured.URL.Path != "/api/v1/org/security/findings" {
		t.Fatalf("path = %s", captured.URL.Path)
	}
	expectedQuery := url.Values{
		"page":            {"2"},
		"page_size":       {"10"},
		"search":          {"tomcat"},
		"severity":        {"critical", "high"},
		"status":          {"open", "acknowledged"},
		"fixable":         {"true"},
		"known_exploited": {"true"},
		"cluster_id":      {"0cfbf8ab-8f3a-4a9f-9a9c-5c1f0b4d1d1c"},
		"addon_slug":      {"nginx-ingress"},
		"namespace":       {"payments"},
		"sort":            {"exploitability"},
		"order":           {"desc"},
	}
	if !reflect.DeepEqual(captured.URL.Query(), expectedQuery) {
		t.Fatalf("query = %v, want %v", captured.URL.Query(), expectedQuery)
	}
	if len(list.Result) != 1 || list.Pagination.TotalCount != 22 {
		t.Fatalf("decoded list = %+v", list)
	}
	finding := list.Result[0]
	if !finding.KnownExploited || finding.KevDueDate == nil || *finding.KevDueDate != "2025-04-22" ||
		finding.KevRequiredAction == nil || *finding.KevRequiredAction != "Apply mitigations per vendor instructions." ||
		finding.EPSSScore == nil || *finding.EPSSScore != 0.997 {
		t.Fatalf("exploit intelligence not decoded: %+v", finding.SecurityExploitIntelligence)
	}
	if list.Facets.Exploited[0].Count != 22 || list.Intelligence.KevListed != 1685 {
		t.Fatalf("facets/intelligence not decoded: %+v %+v", list.Facets, list.Intelligence)
	}
}

func TestListSecurityFindingsOmitsUnsetFiltersAndNormalisesEmptyResult(t *testing.T) {
	var captured http.Request
	_, apiClient := newSecurityTestServer(t, `{"result": null, "pagination": {"page": 1, "page_size": 25, "total_pages": 0, "total_count": 0},
        "facets": {"severity": [], "status": [], "addons": [], "clusters": [], "exploited": []},
        "scanner": {"status": "unscanned", "last_scan": null, "fresh_clusters": 0, "stale_clusters": 0, "unscanned_clusters": 0, "stale_after_seconds": 7200},
        "intelligence": {"kev_synced_at": null, "epss_synced_at": null, "kev_listed": 0}}`, &captured)

	list, listError := apiClient.ListSecurityFindings(SecurityFindingsOptions{})
	if listError != nil {
		t.Fatalf("ListSecurityFindings returned an error: %v", listError)
	}
	if captured.URL.RawQuery != "" {
		t.Fatalf("an unfiltered list must send no query, got %q", captured.URL.RawQuery)
	}
	if list.Result == nil || len(list.Result) != 0 {
		t.Fatalf("a null result must decode to an empty slice, got %#v", list.Result)
	}
	if list.Intelligence.KevSyncedAt != nil {
		t.Fatalf("an unsynced catalog must stay nil, got %v", list.Intelligence.KevSyncedAt)
	}
}

func TestGetSecurityOverviewScopesToClusterAndAddon(t *testing.T) {
	var captured http.Request
	_, apiClient := newSecurityTestServer(t, `{
        "totals": {"observed": 10, "actionable": 8, "acknowledged": 1, "accepted_risk": 1, "resolved": 3},
        "severity": {"critical": 1, "high": 2, "medium": 3, "low": 4, "unknown": 0},
        "fixable_severe": 2, "known_exploited": 5, "known_exploited_findings": 2,
        "known_exploited_overdue": 1, "known_exploited_ransomware": 1,
        "intelligence": {"kev_synced_at": "2026-08-28T20:13:11Z", "epss_synced_at": "2026-08-28T20:13:12Z", "kev_listed": 1685},
        "coverage": {"total_clusters": 1, "scanned_clusters": 1, "unscanned_clusters": 0, "stale_clusters": 0, "fresh_clusters": 1, "latest_report_at": "2026-08-28T20:25:14Z"},
        "scanner": {"status": "fresh", "last_scan": "2026-08-28T20:25:14Z", "fresh_clusters": 1, "stale_clusters": 0, "unscanned_clusters": 0, "stale_after_seconds": 7200},
        "top_remediation_candidates": [], "observed_trend": [], "generated_at": "2026-08-28T21:00:00Z"
    }`, &captured)

	overview, overviewError := apiClient.GetSecurityOverview(SecurityOverviewOptions{ClusterID: "abc", AddonSlug: "cert-manager"})
	if overviewError != nil {
		t.Fatalf("GetSecurityOverview returned an error: %v", overviewError)
	}
	if captured.URL.Path != "/api/v1/org/security/overview" ||
		captured.URL.Query().Get("cluster_id") != "abc" || captured.URL.Query().Get("addon_slug") != "cert-manager" {
		t.Fatalf("request = %s?%s", captured.URL.Path, captured.URL.RawQuery)
	}
	if overview.KnownExploitedFindings != 2 || overview.KnownExploitedOverdue != 1 || overview.KnownExploitedRansomware != 1 {
		t.Fatalf("known-exploited counts not decoded: %+v", overview)
	}
}

func TestGetSecurityFindingEscapesTheIdAndDecodesOccurrences(t *testing.T) {
	var captured http.Request
	_, apiClient := newSecurityTestServer(t, `{
        "finding": {"id": "f1", "provider": "trivy_operator", "cve_id": "CVE-2020-1938", "package_type": "maven",
            "package_name": "tomcat", "severity": "CRITICAL", "title": "Ghostcat", "primary_link": null, "score": null,
            "first_seen_at": "2026-08-01T00:00:00Z", "last_seen_at": "2026-08-28T20:00:00Z",
            "occurrences": 1, "affected_clusters": 1, "affected_workloads": 1, "fixable_occurrences": 1,
            "disposition_counts": {"open": 1, "acknowledged": 0, "accepted_risk": 0, "resolved": 0},
            "known_exploited": true, "kev_date_added": "2022-03-03", "kev_due_date": "2022-03-17",
            "kev_ransomware_use": true, "epss_score": null, "epss_percentile": null,
            "kev_vendor_project": null, "kev_product": null, "kev_vulnerability_name": null,
            "kev_required_action": "Apply updates per vendor instructions."},
        "occurrences": [{"id": "o1", "cluster_id": "c1", "cluster_name": "prod", "report_scope": "namespaced",
            "report_name": "replicaset-x", "report_namespace": "shop", "workload_kind": "Deployment",
            "workload_namespace": "shop", "workload_name": "carts", "container_name": "carts",
            "image_ref": "weaveworksdemos/carts:0.4.8", "installed_version": "8.5.11", "fixed_version": "9.0.99",
            "addon_slug": null, "addon_attribution_confidence": "none", "scan_state": "active",
            "effective_disposition": "none", "first_seen_at": "2026-08-01T00:00:00Z", "last_seen_at": "2026-08-28T20:00:00Z"}],
        "matching_policies": []
    }`, &captured)

	detail, detailError := apiClient.GetSecurityFinding("id with space")
	if detailError != nil {
		t.Fatalf("GetSecurityFinding returned an error: %v", detailError)
	}
	if captured.URL.Path != "/api/v1/org/security/findings/id with space" {
		t.Fatalf("path = %q", captured.URL.Path)
	}
	if len(detail.Occurrences) != 1 || detail.Occurrences[0].ClusterName != "prod" ||
		!detail.Finding.KevRansomwareUse || detail.Finding.KevRequiredAction == nil {
		t.Fatalf("detail not decoded: %+v", detail)
	}
}

func TestListSecurityClustersEncodesControls(t *testing.T) {
	var captured http.Request
	_, apiClient := newSecurityTestServer(t, `{"result": [{"cluster_id": "c1", "cluster_name": "prod", "environment": "production",
        "scanner_status": "fresh", "posture_status": "critical", "latest_report_at": "2026-08-28T20:25:14Z",
        "observed": 10, "actionable": 8, "acknowledged": 0, "accepted_risk": 2, "fixable_severe": 3, "known_exploited": 4,
        "severity": {"critical": 1, "high": 2, "medium": 3, "low": 4, "unknown": 0}}],
        "pagination": {"page": 1, "page_size": 50, "total_pages": 1, "total_count": 1}}`, &captured)

	list, listError := apiClient.ListSecurityClusters(SecurityClustersOptions{
		Page: 1, PageSize: 50, Search: "prod", Status: "critical", Sort: "known_exploited", Order: "desc",
	})
	if listError != nil {
		t.Fatalf("ListSecurityClusters returned an error: %v", listError)
	}
	expectedQuery := url.Values{
		"page": {"1"}, "page_size": {"50"}, "search": {"prod"}, "status": {"critical"},
		"sort": {"known_exploited"}, "order": {"desc"},
	}
	if captured.URL.Path != "/api/v1/org/security/clusters" || !reflect.DeepEqual(captured.URL.Query(), expectedQuery) {
		t.Fatalf("request = %s?%s", captured.URL.Path, captured.URL.RawQuery)
	}
	if len(list.Result) != 1 || list.Result[0].KnownExploited != 4 {
		t.Fatalf("clusters not decoded: %+v", list)
	}
}

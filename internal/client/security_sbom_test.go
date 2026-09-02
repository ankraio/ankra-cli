package client

import (
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestListSecurityNamespacesAndPodsEncodeTheirControls(t *testing.T) {
	var captured http.Request
	_, apiClient := newSecurityTestServer(t, `{"result": [{"cluster_id": "c1", "cluster_name": "prod", "namespace": "backend",
        "report_scope": "namespaced", "workloads": 3, "images": 2, "pods": 5, "observed": 12,
        "actionable": {"critical": 2, "high": 4, "medium": 3, "low": 0, "unknown": 0}, "actionable_total": 9,
        "accepted_risk": 3, "fixable_severe": 4, "known_exploited": 1, "priority": "critical",
        "last_scan": "2026-08-28T20:25:14Z", "scanner_status": "fresh"}],
        "pagination": {"page": 1, "page_size": 50, "total_pages": 1, "total_count": 1},
        "scanner": {"status": "fresh", "last_scan": null, "fresh_clusters": 1, "stale_clusters": 0, "unscanned_clusters": 0, "stale_after_seconds": 7200}}`, &captured)

	list, listError := apiClient.ListSecurityNamespaces(SecurityNamespacesOptions{Page: 2, PageSize: 25, Search: " prod ", ClusterID: "c1", Sort: "pods", Order: "asc"})
	if listError != nil {
		t.Fatalf("ListSecurityNamespaces returned an error: %v", listError)
	}
	expectedQuery := url.Values{"page": {"2"}, "page_size": {"25"}, "search": {"prod"}, "cluster_id": {"c1"}, "sort": {"pods"}, "order": {"asc"}}
	if captured.URL.Path != "/api/v1/org/security/namespaces" || !reflect.DeepEqual(captured.URL.Query(), expectedQuery) {
		t.Fatalf("request = %s?%s", captured.URL.Path, captured.URL.RawQuery)
	}
	if len(list.Result) != 1 || list.Result[0].Pods != 5 || list.Result[0].Actionable.High != 4 {
		t.Fatalf("namespaces not decoded: %+v", list)
	}

	_, apiClient = newSecurityTestServer(t, `{"result": [], "pagination": {"page": 1, "page_size": 50, "total_pages": 0, "total_count": 0}, "capped": false}`, &captured)
	pods, podsError := apiClient.ListSecurityPods(SecurityPodsOptions{ClusterID: "c1", Namespace: "backend", WorkloadUID: "uid-1", WorkloadKind: "Deployment", WorkloadName: "api", PageSize: 100})
	if podsError != nil {
		t.Fatalf("ListSecurityPods returned an error: %v", podsError)
	}
	expectedQuery = url.Values{"page_size": {"100"}, "cluster_id": {"c1"}, "namespace": {"backend"}, "workload_uid": {"uid-1"}, "workload_kind": {"Deployment"}, "workload_name": {"api"}}
	if captured.URL.Path != "/api/v1/org/security/pods" || !reflect.DeepEqual(captured.URL.Query(), expectedQuery) {
		t.Fatalf("request = %s?%s", captured.URL.Path, captured.URL.RawQuery)
	}
	if pods.Result == nil || len(pods.Result) != 0 || pods.Capped {
		t.Fatalf("empty pod list must decode to an empty slice: %+v", pods)
	}
}

func TestSecuritySBOMReadsEncodeFiltersAndDecodeCoverage(t *testing.T) {
	var captured http.Request
	_, apiClient := newSecurityTestServer(t, `{"result": [{"name": "openssl", "version": "3.0.14", "package_type": "deb",
        "component_type": "library", "purl": "pkg:deb/debian/openssl@3.0.14", "licenses": ["Apache-2.0"],
        "images": 4, "workloads": 7, "clusters": 2, "vulnerable_findings": 2, "actionable_findings": 1, "known_exploited": 1}],
        "pagination": {"page": 1, "page_size": 50, "total_pages": 1, "total_count": 1},
        "facets": {"package_type": [{"value": "deb", "count": 1}]},
        "coverage": {"scanned_clusters": 3, "clusters_with_sbom": 1, "images": 12, "components": 1840, "workloads": 20, "latest_generated_at": null}}`, &captured)
	vulnerable := false
	list, listError := apiClient.ListSecuritySBOMComponents(SecuritySBOMComponentsOptions{
		Search: "openssl", PackageTypes: []string{"deb", " ", "apk"}, ClusterID: "c1", Namespace: "backend",
		ImageIdentity: "sha256:abc", Vulnerable: &vulnerable, Sort: "vulnerable", Order: "desc",
	})
	if listError != nil {
		t.Fatalf("ListSecuritySBOMComponents returned an error: %v", listError)
	}
	expectedQuery := url.Values{
		"search": {"openssl"}, "package_type": {"deb", "apk"}, "cluster_id": {"c1"}, "namespace": {"backend"},
		"image": {"sha256:abc"}, "vulnerable": {"false"}, "sort": {"vulnerable"}, "order": {"desc"},
	}
	if captured.URL.Path != "/api/v1/org/security/sbom" || !reflect.DeepEqual(captured.URL.Query(), expectedQuery) {
		t.Fatalf("request = %s?%s", captured.URL.Path, captured.URL.RawQuery)
	}
	if list.Coverage.ClustersWithSBOM != 1 || list.Coverage.ScannedClusters != 3 || list.Result[0].KnownExploited != 1 {
		t.Fatalf("components not decoded: %+v", list)
	}

	_, apiClient = newSecurityTestServer(t, `{"result": [], "pagination": {"page": 1, "page_size": 50, "total_pages": 0, "total_count": 0},
        "coverage": {"scanned_clusters": 3, "clusters_with_sbom": 0, "images": 0, "components": 0, "workloads": 0, "latest_generated_at": null}}`, &captured)
	images, imagesError := apiClient.ListSecuritySBOMImages(SecuritySBOMImagesOptions{Search: "api", ClusterID: "c1", Sort: "components"})
	if imagesError != nil {
		t.Fatalf("ListSecuritySBOMImages returned an error: %v", imagesError)
	}
	if captured.URL.Path != "/api/v1/org/security/sbom/images" || captured.URL.Query().Get("sort") != "components" || len(images.Result) != 0 {
		t.Fatalf("images request = %s?%s %+v", captured.URL.Path, captured.URL.RawQuery, images)
	}

	_, apiClient = newSecurityTestServer(t, `{"image": {"image_identity": "sha256:abc", "image_ref": "registry.test/api:1.0.0",
        "image_repository": null, "image_tag": null, "image_digest": "sha256:abc", "registry": null, "os_family": null, "os_name": null,
        "bom_format": null, "spec_version": null, "component_count": 1, "dependency_count": 0, "workloads": 0, "clusters": 0,
        "namespaces": [], "observed": 0, "actionable": {"critical": 0, "high": 0, "medium": 0, "low": 0, "unknown": 0},
        "known_exploited": 0, "generated_at": null, "first_seen_at": "2026-08-28T20:25:14Z", "last_seen_at": "2026-08-28T20:25:14Z"},
        "components": null, "pagination": {"page": 1, "page_size": 100, "total_pages": 0, "total_count": 0},
        "facets": {"package_type": []}, "workloads": null}`, &captured)
	detail, detailError := apiClient.GetSecuritySBOMImage(SecuritySBOMImageOptions{ImageIdentity: "sha256:abc", PackageTypes: []string{"deb"}, Sort: "name", Order: "asc"})
	if detailError != nil {
		t.Fatalf("GetSecuritySBOMImage returned an error: %v", detailError)
	}
	expectedQuery = url.Values{"image": {"sha256:abc"}, "package_type": {"deb"}, "sort": {"name"}, "order": {"asc"}}
	if captured.URL.Path != "/api/v1/org/security/sbom/image" || !reflect.DeepEqual(captured.URL.Query(), expectedQuery) {
		t.Fatalf("detail request = %s?%s", captured.URL.Path, captured.URL.RawQuery)
	}
	if detail.Components == nil || detail.Workloads == nil {
		t.Fatalf("null lists must decode to empty slices: %+v", detail)
	}
}

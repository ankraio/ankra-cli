package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestGetAnkraCloudQuotasDecodesTheReport(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/clusters/ankracloud/cluster-1/quotas" {
			t.Errorf("request = %s %s, want GET the cluster's quotas", request.Method, request.URL.Path)
		}
		jsonResponse(t, responseWriter, http.StatusOK, map[string]any{
			"provider":       "ankracloud",
			"limits_exposed": true,
			"increase":       map[string]any{"mechanism": "provider_support", "console_url": "https://cloud.ankra.app/settings/quotas", "instructions": "request a higher value"},
			"dimensions": []map[string]any{
				{"dimension": "storage_gb", "scope": "account", "unit": "GB", "used": 275, "limit": 300, "remaining": 25, "utilisation_percent": 91.6, "exhausted": false},
				{"dimension": "floating_ips", "scope": "account", "unit": "count", "used": 0, "limit": nil, "exhausted": false},
			},
			"pressured": []map[string]any{},
		})
	})
	report, readError := testClient.GetAnkraCloudQuotas("cluster-1")
	if readError != nil {
		t.Fatalf("quotas: %v", readError)
	}
	if report.Increase.ConsoleURL != "https://cloud.ankra.app/settings/quotas" || len(report.Dimensions) != 2 {
		t.Fatalf("report = %+v", report)
	}
	if storage := report.Dimensions[0]; storage.Limit == nil || *storage.Limit != 300 || *storage.Remaining != 25 {
		t.Fatalf("storage = %+v", storage)
	}
	if unknown := report.Dimensions[1]; unknown.Limit != nil || unknown.Remaining != nil {
		t.Fatalf("a limit the provider does not report must stay unknown, got %+v", unknown)
	}
}

func TestGetAnkraCloudQuotasSurfacesARefusal(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		jsonResponse(t, responseWriter, http.StatusNotFound, map[string]any{"detail": "Cluster has no associated Ankra Cloud credential"})
	})
	if _, readError := testClient.GetAnkraCloudQuotas("cluster-1"); readError == nil {
		t.Fatal("a 404 must be an error, not an empty report")
	}
}

// TestStackWriteWarningsDecodeThePlatformShape pins the warnings of a stack
// write to the objects the platform sends; a string list could not decode a
// write that carried any warning at all.
func TestStackWriteWarningsDecodeThePlatformShape(t *testing.T) {
	var result StackWriteResult
	body := `{"stack_name":"storage","job_count":2,"warnings":[{"kind":"PersistentVolumeClaim","name":"new claims","key":"storage","message":"This change asks for 50 GiB","category":"ankracloud_quota"}]}`
	if decodeError := json.Unmarshal([]byte(body), &result); decodeError != nil {
		t.Fatalf("decoding a stack write with a warning: %v", decodeError)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Category != "ankracloud_quota" {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
}

func TestApplyClusterCarriesTheWarnings(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		jsonResponse(t, responseWriter, http.StatusOK, map[string]any{
			"name": "production", "cluster_id": "cluster-1", "import_command": "", "errors": []any{},
			"warnings": []map[string]any{{"kind": "PersistentVolumeClaim", "name": "new claims", "key": "storage",
				"message": "This change asks for 50 GiB", "category": "ankracloud_quota"}},
		})
	})
	response, _, applyError := testClient.ApplyCluster(context.Background(), CreateImportClusterRequest{Name: "production"}, true)
	if applyError != nil {
		t.Fatalf("apply: %v", applyError)
	}
	if len(response.Warnings) != 1 || response.Warnings[0].Name != "new claims" {
		t.Fatalf("warnings = %+v", response.Warnings)
	}
}

package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestGetFleetCloudCost_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/cloud-cost/summary" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, FleetCloudCost{
			Currency: "eur", ClusterCount: 2, ProjectedMonthEndCents: 86000,
			ByProvider: []FleetProviderCost{{Provider: "hetzner", ClusterCount: 2, ProjectedMonthEndCents: 86000}},
		})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetFleetCloudCost()
	if err != nil {
		t.Fatalf("GetFleetCloudCost: %v", err)
	}
	if result.Currency != "eur" || result.ClusterCount != 2 || len(result.ByProvider) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.TopClusters == nil {
		t.Fatalf("an absent top_clusters list must decode as empty, not nil")
	}
}

func TestGetCloudSavings_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/cloud-cost/savings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"currency":"eur","generated_at":"2026-09-18T06:00:00Z","total_monthly_savings_cents":43800,` +
			`"recommendations":[{"id":"right_size_idle:c1","kind":"right_size_idle","cluster_id":"c1","cluster_name":"prod-eu",` +
			`"provider":"hetzner","environment":null,"monthly_savings_cents":43800,"monthly_cost_cents":200000,"share_percent":22,` +
			`"evidence":{"idle_monthly_cents":43800,"unallocated_monthly_cents":10000,"unallocated_share_percent":null,` +
			`"stoppable_monthly_cents":null,"off_hours_share_percent":null,"stop_cron":null,"start_cron":null}}],` +
			`"breakdowns":[],"namespaces":[],"analysed_cluster_count":1,"unanalysed_cluster_count":0,"priced_cluster_count":1,` +
			`"unpriced_cluster_count":0,"unpriced_clusters":[],"stale_cluster_count":0,"stale_clusters":[],"unreadable_clusters":[],` +
			`"waste":{"available":true,"has_data":false,"scanned_at":null,"total_monthly_cost_cents":0,"finding_count":0,"unpriced_finding_count":0},` +
			`"thresholds":{"minimum_savings_cents":500,"minimum_off_hours_monthly_cents":1000,"unallocated_share_threshold":0.25,` +
			`"off_hours_share":0.6428571428571429,"analysed_cluster_limit":8}}`))
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetCloudSavings()
	if err != nil {
		t.Fatalf("GetCloudSavings: %v", err)
	}
	if result.Currency != "eur" || result.TotalMonthlySavingsCents != 43800 || len(result.Recommendations) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	recommendation := result.Recommendations[0]
	if recommendation.Kind != "right_size_idle" || recommendation.Evidence.IdleMonthlyCents == nil ||
		*recommendation.Evidence.IdleMonthlyCents != 43800 || recommendation.Evidence.StopCron != nil || recommendation.Environment != nil {
		t.Fatalf("recommendation did not decode: %+v", recommendation)
	}
	if result.Thresholds.AnalysedClusterLimit != 8 || result.Thresholds.UnallocatedShareThreshold != 0.25 {
		t.Fatalf("thresholds did not decode: %+v", result.Thresholds)
	}
	if !result.Waste.Available || result.Waste.HasData || result.Waste.ScannedAt != nil {
		t.Fatalf("waste did not decode: %+v", result.Waste)
	}
}

func TestGetCloudSavings_AbsentListsDecodeAsEmpty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{"currency": "usd", "waste": map[string]any{"available": false}})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetCloudSavings()
	if err != nil {
		t.Fatalf("GetCloudSavings: %v", err)
	}
	if result.Recommendations == nil || result.Breakdowns == nil || result.Namespaces == nil ||
		result.UnpricedClusters == nil || result.StaleClusters == nil || result.UnreadableClusters == nil {
		t.Fatalf("absent lists must decode as empty, not nil: %+v", result)
	}
}

func TestGetClusterCost_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/clusters/cluster-123/cost" {
			t.Errorf("path = %s", r.URL.Path)
		}
		jsonResponse(t, w, http.StatusOK, map[string]any{
			"has_data":   false,
			"summary":    nil,
			"trend":      []any{},
			"namespaces": []any{},
			"readiness":  map[string]any{"state": "no_credential", "provider": "aws"},
		})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetClusterCost("cluster-123")
	if err != nil {
		t.Fatalf("GetClusterCost: %v", err)
	}
	if result.HasData || result.Summary != nil || result.Readiness == nil || result.Readiness.State != "no_credential" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestUpdateCostSettings_SendsPutWithCSRFAndBody(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/api/v1/org/cloud-cost/settings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		header := r.Header.Get("X-Ankra-CSRF")
		cookie, cookieError := r.Cookie("ankra_csrf")
		if header == "" || cookieError != nil || cookie.Value != header {
			t.Errorf("CSRF double-submit missing: header=%q cookieError=%v", header, cookieError)
		}
		var body map[string]any
		if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode body: %v", decodeError)
		}
		if body["currency"] != "eur" || body["effective_discount_pct"] != 12.5 || body["include_network_egress_estimate"] != true {
			t.Errorf("unexpected body: %+v", body)
		}
		jsonResponse(t, w, http.StatusOK, CostSettings{Currency: "eur", EffectiveDiscountPct: 12.5, IncludeNetworkEgressEstimate: true})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.UpdateCostSettings(CostSettings{Currency: "eur", EffectiveDiscountPct: 12.5, IncludeNetworkEgressEstimate: true})
	if err != nil {
		t.Fatalf("UpdateCostSettings: %v", err)
	}
	if result.Currency != "eur" || result.EffectiveDiscountPct != 12.5 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCost_BackendDetailSurfaces(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusForbidden, map[string]string{
			"detail": "Only organisation admins can change cloud cost settings"})
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.UpdateCostSettings(CostSettings{Currency: "usd"})
	if err == nil || !strings.Contains(err.Error(), "Only organisation admins") {
		t.Fatalf("expected the backend detail to surface, got %v", err)
	}
	_, err = testClient.GetCostSettings()
	if err == nil || !strings.Contains(err.Error(), "Only organisation admins") {
		t.Fatalf("expected the backend detail to surface on a read too, got %v", err)
	}
}

func TestGetCloudLedger_DecodesRowsNullsAndNegatives(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/org/cloud-cost/ledger" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"currency":"usd","generated_at":"2026-09-23T21:33:14Z","measured_total_cents":-4200,` +
			`"month":"2026-09","measured_this_month_cents":-4200,"running_total_cents":23100,` +
			`"counts":{"pending":1,"measured":1,"unmeasured":1,"reverted":0},"rows":[` +
			`{"decision_id":"d1","cluster_id":"c1","cluster_name":"staging-1","lever":"off_hours_schedule",` +
			`"summary":"Stop staging-1 weeknights and weekends","status":"succeeded","expected_monthly_cents":23100,` +
			`"baseline_monthly_cents":null,"measured_monthly_cents":null,"measurement_status":"pending",` +
			`"measurement_reason":null,"days":3,"decided_at":"2026-09-19T08:00:00Z","executed_at":"2026-09-20T10:00:00Z",` +
			`"verify_until":"2026-09-27T10:00:00Z","measured_at":null},` +
			`{"decision_id":"d2","cluster_id":null,"cluster_name":null,"lever":"right_size","summary":"Resize workers",` +
			`"status":"succeeded","expected_monthly_cents":null,"baseline_monthly_cents":41000,"measured_monthly_cents":-4200,` +
			`"measurement_status":"measured","measurement_reason":null,"days":7,"decided_at":null,"executed_at":null,` +
			`"verify_until":null,"measured_at":"2026-09-23T10:00:00Z","verification_status":"passed",` +
			`"verification_days":[{"day":1,"from":"2026-09-16T10:00:00Z","to":"2026-09-17T10:00:00Z","state":"clear",` +
			`"cpu_p95_share":0.41,"memory_p95_share":null,"hottest_node":"batch-1","nodes":2,"reporting":2}]},` +
			`{"decision_id":"d3","cluster_id":"c3","cluster_name":"data","lever":"waste_cleanup","summary":"Delete volumes",` +
			`"status":"succeeded","expected_monthly_cents":null,"baseline_monthly_cents":null,"measured_monthly_cents":null,` +
			`"measurement_status":"unmeasured_coverage_moved","measurement_reason":"Coverage moved.","days":7,` +
			`"decided_at":null,"executed_at":null,"verify_until":null,"measured_at":null}],"truncated":true}`))
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetCloudLedger()
	if err != nil {
		t.Fatalf("GetCloudLedger: %v", err)
	}
	if result.Currency != "usd" || result.Month != "2026-09" || result.MeasuredTotalCents != -4200 ||
		result.MeasuredThisMonthCents != -4200 || result.RunningTotalCents != 23100 || !result.Truncated {
		t.Fatalf("totals did not decode: %+v", result)
	}
	if result.Counts != (CloudLedgerCounts{Pending: 1, Measured: 1, Unmeasured: 1, Reverted: 0}) {
		t.Fatalf("counts did not decode: %+v", result.Counts)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("rows did not decode: %+v", result.Rows)
	}
	pending := result.Rows[0]
	if pending.ExpectedMonthlyCents == nil || *pending.ExpectedMonthlyCents != 23100 || pending.MeasuredMonthlyCents != nil ||
		pending.BaselineMonthlyCents != nil || pending.Days == nil || *pending.Days != 3 || pending.MeasuredAt != nil ||
		pending.VerifyUntil == nil || *pending.VerifyUntil != "2026-09-27T10:00:00Z" {
		t.Fatalf("pending row did not decode: %+v", pending)
	}
	if pending.VerificationStatus != nil || pending.VerificationDays != nil {
		t.Fatalf("a row sent without verification must decode without one: %+v", pending)
	}
	measured := result.Rows[1]
	if measured.ClusterID != nil || measured.ClusterName != nil || measured.ExpectedMonthlyCents != nil ||
		measured.MeasuredMonthlyCents == nil || *measured.MeasuredMonthlyCents != -4200 {
		t.Fatalf("an unknown must decode as nil and a negative measurement as negative: %+v", measured)
	}
	if measured.VerificationStatus == nil || *measured.VerificationStatus != "passed" || measured.VerificationDays == nil ||
		len(*measured.VerificationDays) != 1 {
		t.Fatalf("verification did not decode: %+v", measured)
	}
	day := (*measured.VerificationDays)[0]
	if day.State != "clear" || day.CPUP95Share == nil || *day.CPUP95Share != 0.41 || day.MemoryP95Share != nil ||
		day.HottestNode != "batch-1" || day.Nodes != 2 || day.Reporting != 2 {
		t.Fatalf("verification day did not decode: %+v", day)
	}
	unmeasured := result.Rows[2]
	if unmeasured.MeasurementReason == nil || *unmeasured.MeasurementReason != "Coverage moved." {
		t.Fatalf("the reason did not decode: %+v", unmeasured)
	}
}

func TestGetCloudLedger_AbsentRowsDecodeAsEmpty(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]any{"currency": "eur", "month": "2026-09"})
	}
	testClient := newTestClient(t, handler)
	result, err := testClient.GetCloudLedger()
	if err != nil {
		t.Fatalf("GetCloudLedger: %v", err)
	}
	if result.Rows == nil || len(result.Rows) != 0 {
		t.Fatalf("an absent rows list must decode as empty, not nil: %+v", result)
	}
}

// A platform that predates the ledger answers the route with a bare 404; the
// command keys its "this platform predates it" message on the status.
func TestGetCloudLedger_SurfacesTheStatusCode(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	testClient := newTestClient(t, handler)
	_, readError := testClient.GetCloudLedger()
	if readError == nil {
		t.Fatal("a 404 is an error")
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(readError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Fatalf("error = %v", readError)
	}
}

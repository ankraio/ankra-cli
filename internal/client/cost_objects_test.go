package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const objectCostTestClusterID = "0b7c4d11-5f6a-4b8c-9d0e-1f2a3b4c5d6e"

// TestGetObjectCost_ReadsEachKindsRoute pins the route of every kind and
// that the namespace and stack segments are path-escaped, so a name with a
// space or a slash stays one segment instead of reaching another route.
func TestGetObjectCost_ReadsEachKindsRoute(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		segments []string
		wantPath string
	}{
		{"cluster", ObjectCostKindCluster, []string{objectCostTestClusterID},
			"/api/v1/org/cloud-cost/objects/cluster/" + objectCostTestClusterID},
		{"namespace", ObjectCostKindNamespace, []string{objectCostTestClusterID, "kube-system"},
			"/api/v1/org/cloud-cost/objects/namespace/" + objectCostTestClusterID + "/kube-system"},
		{"namespace escaped", ObjectCostKindNamespace, []string{objectCostTestClusterID, "a/b"},
			"/api/v1/org/cloud-cost/objects/namespace/" + objectCostTestClusterID + "/a%2Fb"},
		{"stack escaped", ObjectCostKindStack, []string{objectCostTestClusterID, "web app/v2?x#y"},
			"/api/v1/org/cloud-cost/objects/stack/" + objectCostTestClusterID + "/web%20app%2Fv2%3Fx%23y"},
		{"application", ObjectCostKindApplication, []string{"0b7c4d13-5f6a-4b8c-9d0e-1f2a3b4c5d6e"},
			"/api/v1/org/cloud-cost/objects/application/0b7c4d13-5f6a-4b8c-9d0e-1f2a3b4c5d6e"},
		{"credential", ObjectCostKindCredential, []string{"0b7c4d14-5f6a-4b8c-9d0e-1f2a3b4c5d6e"},
			"/api/v1/org/cloud-cost/objects/credential/0b7c4d14-5f6a-4b8c-9d0e-1f2a3b4c5d6e"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var gotPath, gotMethod, gotQuery string
			handler := func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotMethod, gotQuery = r.URL.EscapedPath(), r.Method, r.URL.RawQuery
				jsonResponse(t, w, http.StatusOK, map[string]any{
					"kind": testCase.kind, "object": map[string]any{"id": "x", "name": "x", "cluster_id": nil, "cluster_name": nil},
					"currency": "usd", "priced": false, "unpriced_reason": "nothing behind it",
					"open_waste": map[string]any{"available": false, "reason": "not attributed", "count": 0,
						"monthly_cents": nil, "unpriced_count": 0},
					"snapshot_stale_after_hours": 26,
				})
			}
			result, err := newTestClient(t, handler).GetObjectCost(testCase.kind, testCase.segments...)
			if err != nil {
				t.Fatalf("GetObjectCost: %v", err)
			}
			if gotMethod != http.MethodGet || gotPath != testCase.wantPath || gotQuery != "" {
				t.Fatalf("request = %s %s?%s, want GET %s", gotMethod, gotPath, gotQuery, testCase.wantPath)
			}
			if result.Trend30d == nil || result.Clusters == nil || result.Namespaces == nil {
				t.Fatalf("absent lists must decode as empty, not nil: %+v", result)
			}
			if result.MonthlyCents != nil || result.UnpricedReason == nil || *result.UnpricedReason != "nothing behind it" {
				t.Fatalf("decoded = %+v", result)
			}
		})
	}
}

// TestGetObjectCost_KeepsThePlatformsRefusal pins that a refusal reaches the
// command with its status and the platform's detail, which is what the
// command's error mapping keys on; a 422 validation list carries no detail
// string.
func TestGetObjectCost_KeepsThePlatformsRefusal(t *testing.T) {
	for _, testCase := range []struct {
		status     int
		body       any
		wantDetail string
	}{
		{http.StatusNotFound, map[string]any{"detail": "Object not found"}, "Object not found"},
		{http.StatusNotFound, map[string]any{"detail": "Cluster not found"}, "Cluster not found"},
		{http.StatusBadRequest, map[string]any{"detail": "namespace must be a Kubernetes namespace name"},
			"namespace must be a Kubernetes namespace name"},
		{http.StatusUnprocessableEntity, map[string]any{"detail": []any{map[string]any{"type": "uuid_parsing",
			"loc": []any{"path", "application_id"}, "msg": "Input should be a valid UUID"}}}, ""},
	} {
		handler := func(w http.ResponseWriter, r *http.Request) { jsonResponse(t, w, testCase.status, testCase.body) }
		_, err := newTestClient(t, handler).GetObjectCost(ObjectCostKindApplication, "not-a-uuid")
		var unexpected *UnexpectedResponseError
		if !errors.As(err, &unexpected) || unexpected.StatusCode != testCase.status || unexpected.Detail != testCase.wantDetail {
			t.Fatalf("status %d: err = %#v, want status %d detail %q", testCase.status, err, testCase.status, testCase.wantDetail)
		}
	}
}

// TestObjectCostCluster_CoverageFlagKeepsAbsentApartFromNull pins the
// per-cluster coverage_incomplete (cluster#3453): true, false and null are
// read as sent, a platform that predates the field leaves it absent, and
// encoding gives back exactly those keys, so an absent flag never returns
// as a null (which the contract reads as an unpriced cluster).
func TestObjectCostCluster_CoverageFlagKeepsAbsentApartFromNull(t *testing.T) {
	document := `[{"cluster_id":"a","cluster_name":"a","priced":true,"monthly_cents":1,"confidence":"high","coverage_incomplete":true},` +
		`{"cluster_id":"b","cluster_name":"b","priced":true,"monthly_cents":1,"confidence":"high","coverage_incomplete":false},` +
		`{"cluster_id":"c","cluster_name":"c","priced":false,"monthly_cents":null,"confidence":null,"coverage_incomplete":null},` +
		`{"cluster_id":"d","cluster_name":"d","priced":true,"monthly_cents":1,"confidence":"high"}]`
	var clusters []ObjectCostCluster
	if err := json.Unmarshal([]byte(document), &clusters); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	flags := clusters[0].CoverageIncomplete
	if !flags.Present || !flags.IsTrue() {
		t.Fatalf("true = %+v", flags)
	}
	if flags = clusters[1].CoverageIncomplete; !flags.Present || flags.Value == nil || *flags.Value || flags.IsTrue() {
		t.Fatalf("false = %+v", flags)
	}
	if flags = clusters[2].CoverageIncomplete; !flags.Present || flags.Value != nil || flags.IsTrue() {
		t.Fatalf("null = %+v", flags)
	}
	if flags = clusters[3].CoverageIncomplete; flags.Present || flags.Value != nil || flags.IsTrue() {
		t.Fatalf("absent = %+v", flags)
	}
	encoded, err := json.Marshal(clusters)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	var want, got []map[string]any
	_ = json.Unmarshal([]byte(document), &want)
	_ = json.Unmarshal(encoded, &got)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round trip changed the rows:\nwant %s\ngot  %s", document, encoded)
	}
	yamlEncoded, err := yaml.Marshal(clusters)
	if err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if strings.Count(string(yamlEncoded), "coverage_incomplete:") != 3 || !strings.Contains(string(yamlEncoded), "coverage_incomplete: null") {
		t.Fatalf("yaml must carry the three sent flags and leave the absent one out:\n%s", yamlEncoded)
	}
}

// TestObjectCostDay_MeteringFieldsKeepAbsentApart pins the trend day's
// partial and hours_metered (cluster#5617): a platform that sends them is
// read as sent, false and 0 included, and one that predates them leaves
// Partial absent and HoursMetered nil, so the command can fall back to the
// date instead of reading "not partial". Encoding gives back exactly the
// keys that were sent, in JSON and in YAML.
func TestObjectCostDay_MeteringFieldsKeepAbsentApart(t *testing.T) {
	document := `[{"date":"2026-10-24","cents":8100,"partial":false,"hours_metered":24,"clusters_metered":1,"clusters_total":1},` +
		`{"date":"2026-10-25","cents":6748,"partial":true,"hours_metered":17,"clusters_metered":1,"clusters_total":1},` +
		`{"date":"2026-10-23","cents":null,"partial":false,"hours_metered":0,"clusters_metered":0,"clusters_total":1},` +
		`{"date":"2026-10-22","cents":8000,"clusters_metered":1,"clusters_total":1}]`
	var days []ObjectCostDay
	if err := json.Unmarshal([]byte(document), &days); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if whole := days[0]; !whole.Partial.Present || whole.Partial.Value == nil || *whole.Partial.Value ||
		whole.HoursMetered == nil || *whole.HoursMetered != 24 {
		t.Fatalf("a whole day = %+v", whole)
	}
	if today := days[1]; !today.Partial.IsTrue() || today.HoursMetered == nil || *today.HoursMetered != 17 {
		t.Fatalf("today = %+v", today)
	}
	if unmetered := days[2]; !unmetered.Partial.Present || unmetered.HoursMetered == nil || *unmetered.HoursMetered != 0 {
		t.Fatalf("an unmetered day must keep its sent 0 hours and false = %+v", unmetered)
	}
	if older := days[3]; older.Partial.Present || older.Partial.Value != nil || older.HoursMetered != nil {
		t.Fatalf("a day from an older platform must read absent, not false or 0: %+v", older)
	}
	encoded, err := json.Marshal(days)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	var want, got []map[string]any
	_ = json.Unmarshal([]byte(document), &want)
	_ = json.Unmarshal(encoded, &got)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round trip changed the days:\nwant %s\ngot  %s", document, encoded)
	}
	yamlEncoded, err := yaml.Marshal(days)
	if err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if strings.Count(string(yamlEncoded), "partial:") != 3 || strings.Count(string(yamlEncoded), "hours_metered:") != 3 ||
		!strings.Contains(string(yamlEncoded), "hours_metered: 0") {
		t.Fatalf("yaml must carry the three sent pairs and leave the older day's out:\n%s", yamlEncoded)
	}
}

// objectCostByEnvironmentDocument is a by_environment block with one
// environment whose parts carry a counted row with no amount, a database
// instance row with every field set, a storage row and a backups row.
const objectCostByEnvironmentDocument = `{"state":"available","reason":null,"environments":[` +
	`{"environment_id":"` + objectCostTestClusterID + `","named":false,"shared":false,"trend_30d":[],` +
	`"parts":{"state":"partial","gaps":["split_rounded"],"monthly_cents":903,"rows":[` +
	`{"kind":"workload","name":"web","detail":null,"cluster_id":"` + objectCostTestClusterID + `","namespace":"shop",` +
	`"monthly_cents":null,"shared":false,"counted_in_rate":true,"role":null,"database":null,"engine":null,"image_version":null,` +
	`"instances":null,"copies":2,"reserved_cpu_millicores":null,"reserved_memory_bytes":null,"copies_alike":true,` +
	`"claim_count":null,"claim_bytes":null,"claim_size_known":null},` +
	`{"kind":"database_instance","name":"orders-db, primary","detail":"1 instance.","cluster_id":"` + objectCostTestClusterID + `",` +
	`"namespace":"shop","monthly_cents":400,"shared":false,"counted_in_rate":true,"role":"primary","database":"orders-db",` +
	`"engine":"postgres","image_version":"16.4","instances":null,"copies":1,"reserved_cpu_millicores":250,` +
	`"reserved_memory_bytes":536870912,"copies_alike":true,"claim_count":null,"claim_bytes":null,"claim_size_known":null},` +
	`{"kind":"storage","name":"Storage","detail":null,"cluster_id":null,"namespace":null,"monthly_cents":null,"shared":false,` +
	`"counted_in_rate":true,"role":null,"database":null,"engine":null,"image_version":null,"instances":null,"copies":null,` +
	`"reserved_cpu_millicores":null,"reserved_memory_bytes":null,"copies_alike":null,"claim_count":0,"claim_bytes":0,` +
	`"claim_size_known":false},` +
	`{"kind":"backups","name":"Backups","detail":null,"cluster_id":null,"namespace":null,"monthly_cents":null,"shared":false,` +
	`"counted_in_rate":false,"role":null,"database":null,"engine":null,"image_version":null,"instances":null,"copies":null,` +
	`"reserved_cpu_millicores":null,"reserved_memory_bytes":null,"copies_alike":null,"claim_count":null,"claim_bytes":null,` +
	`"claim_size_known":null}],` +
	`"rows_truncated":false,"truncated_monthly_cents":0,"unattributed_monthly_cents":503}}],` +
	`"environments_truncated":false,` +
	`"uncounted":[{"reason":"installation_removed","environment_id":null,"named":false,"cluster_id":"` + objectCostTestClusterID + `",` +
	`"namespace":"old","dates":["2026-11-01"]}],` +
	`"uncounted_truncated":false,"uncounted_known_from":"2026-10-01"}`

// TestGetObjectCost_DecodesAnApplicationsCostByEnvironment pins the
// by_environment block read from the route: every level decodes, a null
// amount stays nil (never 0), a zero stays a zero, and the response document
// is kept whole for -o json.
func TestGetObjectCost_DecodesAnApplicationsCostByEnvironment(t *testing.T) {
	body := `{"kind":"application","object":{"id":"x","name":"checkout","cluster_id":null,"cluster_name":null},` +
		`"currency":"eur","priced":true,"unpriced_reason":null,"monthly_cents":903,` +
		`"open_waste":{"available":false,"reason":"not attributed","count":0,"monthly_cents":null,"unpriced_count":0},` +
		`"snapshot_stale_after_hours":26,"by_environment":` + objectCostByEnvironmentDocument + `}`
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
	result, err := newTestClient(t, handler).GetObjectCost(ObjectCostKindApplication, "0b7c4d13-5f6a-4b8c-9d0e-1f2a3b4c5d6e")
	if err != nil {
		t.Fatalf("GetObjectCost: %v", err)
	}
	block := result.ByEnvironment.Value
	if !result.ByEnvironment.Present || block == nil || block.State != ObjectCostStateAvailable || len(block.Environments) != 1 {
		t.Fatalf("by_environment = %+v", result.ByEnvironment)
	}
	if block.UncountedKnownFrom == nil || *block.UncountedKnownFrom != "2026-10-01" || len(block.Uncounted) != 1 ||
		block.Uncounted[0].EnvironmentID != nil || block.Uncounted[0].Reason != "installation_removed" {
		t.Fatalf("uncounted = %+v (known from %v)", block.Uncounted, block.UncountedKnownFrom)
	}
	parts := block.Environments[0].Parts
	if parts == nil || parts.State != ObjectCostStatePartial || !reflect.DeepEqual(parts.Gaps, []string{ObjectCostGapSplitRounded}) ||
		parts.MonthlyCents == nil || *parts.MonthlyCents != 903 || parts.UnattributedMonthlyCents == nil ||
		*parts.UnattributedMonthlyCents != 503 || parts.TruncatedMonthlyCents == nil || *parts.TruncatedMonthlyCents != 0 ||
		len(parts.Rows) != 4 {
		t.Fatalf("parts = %+v", parts)
	}
	web, database, storage, backups := parts.Rows[0], parts.Rows[1], parts.Rows[2], parts.Rows[3]
	if web.MonthlyCents != nil || !web.IsCountedInRate() || web.Copies == nil || *web.Copies != 2 || web.ReservedCPUMillicores != nil {
		t.Fatalf("a counted row with no amount must stay nil, never 0: %+v", web)
	}
	if database.MonthlyCents == nil || *database.MonthlyCents != 400 || database.Role == nil || *database.Role != "primary" ||
		database.Engine == nil || *database.Engine != "postgres" || database.ImageVersion == nil || *database.ImageVersion != "16.4" ||
		database.ReservedMemoryBytes == nil || *database.ReservedMemoryBytes != 536870912 || database.Detail == nil {
		t.Fatalf("database row = %+v", database)
	}
	if storage.ClaimCount == nil || *storage.ClaimCount != 0 || storage.ClaimSizeKnown == nil || *storage.ClaimSizeKnown ||
		storage.Namespace != nil || storage.ClusterID != nil {
		t.Fatalf("storage row = %+v", storage)
	}
	if backups.IsCountedInRate() || backups.MonthlyCents != nil {
		t.Fatalf("backups are beside the rate: %+v", backups)
	}
	var want, got map[string]any
	_ = json.Unmarshal([]byte(body), &want)
	if err := json.Unmarshal(result.RawJSON(), &got); err != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("the response document must be kept whole: %s (%v)", result.RawJSON(), err)
	}
}

// TestObjectCostProjection_ByEnvironmentKeepsAbsentApartFromNull pins the
// block's three forms: absent (an older platform) stays absent, null (every
// kind but an application) stays null, and an object round-trips field for
// field, in JSON and in YAML.
func TestObjectCostProjection_ByEnvironmentKeepsAbsentApartFromNull(t *testing.T) {
	base := `{"kind":"application","object":{"id":"x","name":"x","cluster_id":null,"cluster_name":null},"currency":"eur",` +
		`"priced":false,"unpriced_reason":null,"monthly_cents":null,"idle_pct":null,"share_of_fleet_pct":null,"confidence":null,` +
		`"coverage_incomplete":null,"open_waste":{"available":false,"reason":null,"count":0,"monthly_cents":null,"unpriced_count":0},` +
		`"trend_30d":[],"clusters":[],"namespaces":[],"snapshot_stale_after_hours":26`
	for _, testCase := range []struct {
		name        string
		document    string
		wantPresent bool
		wantValue   bool
		yamlKey     string
	}{
		{"absent", base + `}`, false, false, ""},
		{"null", base + `,"by_environment":null}`, true, false, "by_environment: null"},
		{"object", base + `,"by_environment":` + objectCostByEnvironmentDocument + `}`, true, true, "unattributed_monthly_cents: 503"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var projection ObjectCostProjection
			if err := json.Unmarshal([]byte(testCase.document), &projection); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if projection.ByEnvironment.Present != testCase.wantPresent || (projection.ByEnvironment.Value != nil) != testCase.wantValue {
				t.Fatalf("by_environment = %+v", projection.ByEnvironment)
			}
			encoded, err := json.Marshal(projection)
			if err != nil {
				t.Fatalf("encoding: %v", err)
			}
			var want, got map[string]any
			_ = json.Unmarshal([]byte(testCase.document), &want)
			_ = json.Unmarshal(encoded, &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("round trip changed the projection:\nwant %s\ngot  %s", testCase.document, encoded)
			}
			yamlEncoded, err := yaml.Marshal(projection)
			if err != nil {
				t.Fatalf("yaml: %v", err)
			}
			hasKey := strings.Contains(string(yamlEncoded), "by_environment:")
			if hasKey != testCase.wantPresent || !strings.Contains(string(yamlEncoded), testCase.yamlKey) {
				t.Fatalf("yaml must carry the key exactly when it was sent:\n%s", yamlEncoded)
			}
		})
	}
}

// TestObjectCostRatePart_CountedInRateFromAnOlderShape pins IsCountedInRate
// for a row that does not say: every kind but backups is in the rate.
func TestObjectCostRatePart_CountedInRateFromAnOlderShape(t *testing.T) {
	var rows []ObjectCostRatePart
	if err := json.Unmarshal([]byte(`[{"kind":"workload","name":"web"},{"kind":"backups","name":"Backups"},`+
		`{"kind":"workload","name":"api","counted_in_rate":null},{"kind":"backups","name":"Backups","counted_in_rate":true}]`), &rows); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for index, want := range []bool{true, false, true, true} {
		if rows[index].IsCountedInRate() != want {
			t.Fatalf("row %d (%+v) counted = %v, want %v", index, rows[index], rows[index].IsCountedInRate(), want)
		}
	}
}

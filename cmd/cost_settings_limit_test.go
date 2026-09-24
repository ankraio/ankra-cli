package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"
)

// settingsServer is a platform that answers the settings read with a stored
// limit in effect and records every settings write's raw body.
type settingsServer struct {
	mutex  sync.Mutex
	puts   []map[string]json.RawMessage
	status int
	detail string
}

func newSettingsServer(t *testing.T) (*settingsServer, *client.Client) {
	t.Helper()
	recorder := &settingsServer{status: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/v1/org/cloud-cost/settings":
			_, _ = writer.Write([]byte(`{"effective_discount_pct":5,"currency":"usd","include_network_egress_estimate":true,` +
				`"analysed_cluster_limit":8,"display_currency":"usd","fx_rates":{"usd":1}}`))
		case "PUT /api/v1/org/cloud-cost/settings":
			raw, _ := io.ReadAll(request.Body)
			body := map[string]json.RawMessage{}
			if decodeError := json.Unmarshal(raw, &body); decodeError != nil {
				t.Errorf("the settings write is not a JSON object: %v: %s", decodeError, raw)
			}
			recorder.mutex.Lock()
			recorder.puts = append(recorder.puts, body)
			status, detail := recorder.status, recorder.detail
			recorder.mutex.Unlock()
			if status != http.StatusOK {
				writer.WriteHeader(status)
				_ = json.NewEncoder(writer).Encode(map[string]string{"detail": detail})
				return
			}
			limit := json.RawMessage("8")
			if sent, present := body["analysed_cluster_limit"]; present && string(sent) != "null" {
				limit = sent
			}
			_, _ = writer.Write([]byte(`{"effective_discount_pct":` + string(body["effective_discount_pct"]) +
				`,"currency":` + string(body["currency"]) + `,"include_network_egress_estimate":` +
				string(body["include_network_egress_estimate"]) + `,"analysed_cluster_limit":` + string(limit) + `}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return recorder, client.New("test-token", server.URL)
}

// writes returns a copy of the settings writes recorded so far.
func (recorder *settingsServer) writes() []map[string]json.RawMessage {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]map[string]json.RawMessage{}, recorder.puts...)
}

// refuse makes every later settings write answer status with detail.
func (recorder *settingsServer) refuse(status int, detail string) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.status, recorder.detail = status, detail
}

func runCostSettingsAgainst(t *testing.T, apiClient APIClient, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, apiClient)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs(args)
	// Reset before the run as well as after the test: a test that runs the
	// command more than once must not carry one run's flags into the next.
	resetTreeFlags(t, costCommandTree()...)
	t.Cleanup(func() { resetTreeFlags(t, costCommandTree()...) })
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

// The existing set restates currency, discount and egress from a read, and
// that read now carries the limit in effect. The limit must still go out only
// when --analysed-cluster-limit was given: restating it would turn the
// default into the organisation's own, and a zero would be refused.
func TestCostSettingsSetSendsTheLimitOnlyWhenTheFlagIsGiven(t *testing.T) {
	recorder, realClient := newSettingsServer(t)
	output, executeError := runCostSettingsAgainst(t, realClient, "cost", "settings", "set", "--discount", "12.5")
	if executeError != nil {
		t.Fatalf("cost settings set --discount failed: %v", executeError)
	}
	writes := recorder.writes()
	if len(writes) != 1 {
		t.Fatalf("one write expected, got %d", len(writes))
	}
	if _, present := writes[0]["analysed_cluster_limit"]; present {
		t.Fatalf("--discount alone must not send analysed_cluster_limit: %v", writes[0])
	}
	if string(writes[0]["effective_discount_pct"]) != "12.5" || string(writes[0]["currency"]) != `"usd"` {
		t.Fatalf("the other settings are restated as before: %v", writes[0])
	}
	if !strings.Contains(output, "Analysed-cluster limit: 8 (the savings model analyses the 8 costliest priced clusters)") {
		t.Fatalf("the saved settings show the limit in effect:\n%s", output)
	}

	output, executeError = runCostSettingsAgainst(t, realClient, "cost", "settings", "set", "--analysed-cluster-limit", "20")
	if executeError != nil {
		t.Fatalf("cost settings set --analysed-cluster-limit 20 failed: %v", executeError)
	}
	if writes = recorder.writes(); string(writes[1]["analysed_cluster_limit"]) != "20" || string(writes[1]["effective_discount_pct"]) != "5" {
		t.Fatalf("--analysed-cluster-limit 20 sends 20 with the other settings restated: %v", writes[1])
	}
	if !strings.Contains(output, "Analysed-cluster limit: 20") {
		t.Fatalf("the new limit is shown:\n%s", output)
	}

	if _, executeError = runCostSettingsAgainst(t, realClient, "cost", "settings", "set", "--analysed-cluster-limit", "Default"); executeError != nil {
		t.Fatalf("cost settings set --analysed-cluster-limit default failed: %v", executeError)
	}
	if raw, present := recorder.writes()[2]["analysed_cluster_limit"]; !present || string(raw) != "null" {
		t.Fatalf("--analysed-cluster-limit default sends null: %v", recorder.writes()[2])
	}
}

func TestCostSettingsSetRefusesALimitOutOfRange(t *testing.T) {
	recorder, realClient := newSettingsServer(t)
	for _, value := range []string{"0", "51", "-3", "eight", "8.5", ""} {
		_, executeError := runCostSettingsAgainst(t, realClient, "cost", "settings", "set", "--analysed-cluster-limit", value)
		if executeError == nil || exitCodeFor(executeError) != exitUsage ||
			!strings.Contains(executeError.Error(), "--analysed-cluster-limit must be a whole number from 1 to 50, or default") {
			t.Fatalf("%q: error = %v (exit %d)", value, executeError, exitCodeFor(executeError))
		}
	}
	if writes := recorder.writes(); len(writes) != 0 {
		t.Fatalf("a refused limit must not be sent, got %d writes", len(writes))
	}

	refusal := "analysed_cluster_limit must be a whole number from 1 to 50, or null for the default of 8"
	recorder.refuse(http.StatusBadRequest, refusal)
	_, executeError := runCostSettingsAgainst(t, realClient, "cost", "settings", "set", "--analysed-cluster-limit", "12")
	if executeError == nil || !strings.Contains(executeError.Error(), refusal) {
		t.Fatalf("the platform's refusal is relayed, got %v", executeError)
	}
}

func TestCostSettingsGetShowsTheLimitOrThatItIsUnknown(t *testing.T) {
	limit := 20
	output, executeError := runCostCommand(t, &costMock{settings: &client.CostSettings{Currency: "eur", AnalysedClusterLimit: &limit}},
		"cost", "settings", "get")
	if executeError != nil {
		t.Fatalf("cost settings get failed: %v", executeError)
	}
	if !strings.Contains(output, "Analysed-cluster limit: 20 (the savings model analyses the 20 costliest priced clusters)") {
		t.Fatalf("the limit in effect is shown:\n%s", output)
	}

	output, executeError = runCostCommand(t, &costMock{settings: &client.CostSettings{Currency: "eur"}}, "cost", "settings", "get")
	if executeError != nil {
		t.Fatalf("cost settings get failed: %v", executeError)
	}
	if !strings.Contains(output, "Analysed-cluster limit: unknown (this platform does not report it)") || strings.Contains(output, "limit: 8") {
		t.Fatalf("a platform that predates the limit reads as unknown, not 8:\n%s", output)
	}

	output, executeError = runCostCommand(t, &costMock{settings: &client.CostSettings{Currency: "eur", AnalysedClusterLimit: &limit}},
		"cost", "settings", "get", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost settings get -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil || decoded["analysed_cluster_limit"] != float64(20) {
		t.Fatalf("-o json carries the limit: %v\n%s", unmarshalError, output)
	}
}

func TestCostSavingsSaysWhenTheBudgetCutTheAnalysisShort(t *testing.T) {
	savings := cloudSavingsFixture()
	exhausted := true
	budgetSeconds, staleHours := 20, 26
	savings.AnalysisBudgetExhausted = &exhausted
	savings.Thresholds.AnalysisBudgetSeconds = &budgetSeconds
	savings.Thresholds.SnapshotStaleAfterHours = &staleHours
	output, executeError := runCostCommand(t, &costMock{savings: savings}, "cost", "savings")
	if executeError != nil {
		t.Fatalf("cost savings failed: %v", executeError)
	}
	for _, expected := range []string{
		"3 of 5 clusters analysed (2 not analysed: the 20s analysis time budget ran out before every one of the 8 biggest was read)",
		"Stale clusters (no cost snapshot in the last 26 hours, so metering has stopped):",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "only the 8 biggest are") || strings.Contains(output, "over a day ago") {
		t.Fatalf("a budget cut must not read as the limit, and the platform's window replaces the old wording:\n%s", output)
	}

	output, executeError = runCostCommand(t, &costMock{savings: savings}, "cost", "savings", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost savings -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	thresholds, _ := decoded["thresholds"].(map[string]any)
	if decoded["analysis_budget_exhausted"] != true || thresholds["analysis_budget_seconds"] != float64(20) ||
		thresholds["snapshot_stale_after_hours"] != float64(26) || thresholds["analysed_cluster_limit"] != float64(8) {
		t.Fatalf("-o json carries the budget, the staleness window and the limit: %+v", decoded)
	}
}

func TestCostSavingsShowsTheLimitWhenNothingWasLeftOut(t *testing.T) {
	savings := cloudSavingsFixture()
	savings.UnanalysedClusterCount = 0
	savings.PricedClusterCount = 3
	notExhausted := false
	savings.AnalysisBudgetExhausted = &notExhausted
	output, executeError := runCostCommand(t, &costMock{savings: savings}, "cost", "savings")
	if executeError != nil {
		t.Fatalf("cost savings failed: %v", executeError)
	}
	if !strings.Contains(output, "3 of 3 clusters analysed (limit 8)") {
		t.Fatalf("the limit in effect is shown even when every priced cluster was analysed:\n%s", output)
	}
	// A platform that predates the staleness window keeps the old heading
	// and sends no budget fields, which stay absent in -o json.
	output, _ = runCostCommand(t, &costMock{savings: cloudSavingsFixture()}, "cost", "savings", "-o", "json")
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	if _, present := decoded["analysis_budget_exhausted"]; present {
		t.Fatalf("a platform that did not send the budget flag must not gain one: %+v", decoded)
	}
}

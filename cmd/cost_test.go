package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type costMock struct {
	baseMock
	fleet       *client.FleetCloudCost
	savings     *client.CloudSavings
	clusterCost *client.ClusterCost
	settings    *client.CostSettings
	updates     []client.CostSettings
}

func (m *costMock) GetFleetCloudCost() (*client.FleetCloudCost, error) {
	return m.fleet, nil
}

func (m *costMock) GetCloudSavings() (*client.CloudSavings, error) {
	return m.savings, nil
}

func (m *costMock) GetClusterCost(string) (*client.ClusterCost, error) {
	return m.clusterCost, nil
}

func (m *costMock) GetCostSettings() (*client.CostSettings, error) {
	return m.settings, nil
}

func (m *costMock) UpdateCostSettings(settings client.CostSettings) (*client.CostSettings, error) {
	m.updates = append(m.updates, settings)
	saved := settings
	return &saved, nil
}

func costCommandTree() []*cobra.Command {
	return []*cobra.Command{costSummaryCmd, costSavingsCmd, costClusterCmd, costSettingsGetCmd, costSettingsSetCmd}
}

func runCostCommand(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs(args)
	t.Cleanup(func() { resetTreeFlags(t, costCommandTree()...) })
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

func fleetCostFixture() *client.FleetCloudCost {
	return &client.FleetCloudCost{
		Currency:                 "eur",
		ClusterCount:             3,
		MonthlyCostEstimateCents: 84200,
		MonthToDateCents:         41000,
		ProjectedMonthEndCents:   86000,
		ByProvider: []client.FleetProviderCost{
			{Provider: "hetzner", ClusterCount: 2, MonthlyCostEstimateCents: 60000, MonthToDateCents: 30000, ProjectedMonthEndCents: 64500},
			{Provider: "aws", ClusterCount: 1, MonthlyCostEstimateCents: 24200, MonthToDateCents: 11000, ProjectedMonthEndCents: 21500},
		},
		TopClusters: []client.FleetClusterCost{
			{ClusterID: "11111111-1111-4111-8111-111111111111", ClusterName: "prod-eu", Provider: "hetzner",
				MonthlyCostEstimateCents: 40000, MonthToDateCents: 20000, ProjectedMonthEndCents: 43000, ConfidenceLevel: "high"},
		},
	}
}

const costClusterID = "1834920e-3001-4157-8938-33c447031033"

func TestCostSummaryRendersTotalsProvidersAndTopClusters(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{fleet: fleetCostFixture()}, "cost", "summary")
	if executeError != nil {
		t.Fatalf("cost summary failed: %v", executeError)
	}
	for _, expected := range []string{
		"Cloud cost (EUR): €860.00 projected month end · €410.00 month to date · €842.00/mo run rate",
		"3 clusters priced across 2 provider(s)",
		"Hetzner Cloud", "Amazon Web Services", "€645.00", "prod-eu", "€430.00", "high",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestCostSummaryStructuredOutputIsTheApiDocument(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{fleet: fleetCostFixture()}, "cost", "summary", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost summary -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	if decoded["currency"] != "eur" || decoded["projected_month_end_cents"] != float64(86000) {
		t.Fatalf("structured document lacks the wire fields: %+v", decoded)
	}
	if len(decoded["by_provider"].([]any)) != 2 {
		t.Fatalf("by_provider missing: %+v", decoded)
	}
}

func TestCostSummaryWithNothingPricedSaysWhy(t *testing.T) {
	empty := &client.FleetCloudCost{Currency: "usd", ByProvider: []client.FleetProviderCost{}, TopClusters: []client.FleetClusterCost{}}
	output, executeError := runCostCommand(t, &costMock{fleet: empty}, "cost", "summary")
	if executeError != nil {
		t.Fatalf("cost summary failed: %v", executeError)
	}
	if !strings.Contains(output, "No priced clusters yet.") || strings.Contains(output, "$0.00") {
		t.Fatalf("an empty rollup must read as absent, not as zero cost:\n%s", output)
	}
}

func cloudSavingsFixture() *client.CloudSavings {
	idle := int64(43800)
	unallocated := int64(60000)
	unallocatedShare := 35
	stoppable := int64(20000)
	offHoursShare := 64
	stopCron := "0 19 * * 1-5"
	startCron := "0 7 * * 1-5"
	staging := "staging"
	scannedAt := "2026-09-17T22:00:00"
	bestProd := "right_size_idle:11111111-1111-4111-8111-111111111111"
	return &client.CloudSavings{
		Currency:                 "eur",
		GeneratedAt:              "2026-09-18T06:00:00Z",
		TotalMonthlySavingsCents: 43800 + 60000 + 12857,
		Recommendations: []client.CloudSavingsRecommendation{
			{ID: bestProd, Kind: "right_size_idle", ClusterID: "11111111-1111-4111-8111-111111111111", ClusterName: "prod-eu",
				Provider: "hetzner", MonthlySavingsCents: 43800, MonthlyCostCents: 200000, SharePercent: 22,
				Evidence: client.CloudSavingsEvidence{IdleMonthlyCents: &idle}},
			{ID: "reduce_unallocated:22222222-2222-4222-8222-222222222222", Kind: "reduce_unallocated",
				ClusterID: "22222222-2222-4222-8222-222222222222", ClusterName: "data-platform", Provider: "aws",
				MonthlySavingsCents: 60000, MonthlyCostCents: 171000, SharePercent: 35,
				Evidence: client.CloudSavingsEvidence{UnallocatedMonthlyCents: &unallocated, UnallocatedSharePercent: &unallocatedShare}},
			{ID: "off_hours_schedule:33333333-3333-4333-8333-333333333333", Kind: "off_hours_schedule",
				ClusterID: "33333333-3333-4333-8333-333333333333", ClusterName: "staging-1", Provider: "hetzner", Environment: &staging,
				MonthlySavingsCents: 12857, MonthlyCostCents: 24000, SharePercent: 54,
				Evidence: client.CloudSavingsEvidence{StoppableMonthlyCents: &stoppable, OffHoursSharePercent: &offHoursShare,
					StopCron: &stopCron, StartCron: &startCron}},
		},
		Breakdowns: []client.CloudSavingsBreakdown{
			{ClusterID: "11111111-1111-4111-8111-111111111111", ClusterName: "prod-eu", Provider: "hetzner", MonthlyCents: 200000,
				Components:       []client.CloudSavingsComponent{{Key: "compute_on_demand", Label: "Compute (on-demand)", MonthlyCents: 180000}},
				IdleMonthlyCents: 43800, UnallocatedMonthlyCents: 10000, BestRecommendationID: &bestProd,
				TopNamespaces: []client.CloudSavingsNamespace{}},
		},
		Namespaces:             []client.CloudSavingsNamespace{},
		AnalysedClusterCount:   3,
		UnanalysedClusterCount: 2,
		PricedClusterCount:     5,
		UnpricedClusterCount:   1,
		UnpricedClusters:       []client.CloudSavingsCluster{{ClusterID: "44444444-4444-4444-8444-444444444444", ClusterName: "imported-lab", Kind: "imported"}},
		StaleClusterCount:      1,
		StaleClusters:          []client.CloudSavingsCluster{{ClusterID: "55555555-5555-4555-8555-555555555555", ClusterName: "old-dev", Kind: "k3s"}},
		UnreadableClusters:     []client.CloudSavingsCluster{{ClusterID: "66666666-6666-4666-8666-666666666666", ClusterName: "flaky"}},
		Waste: client.CloudSavingsWaste{Available: true, HasData: true, ScannedAt: &scannedAt, TotalMonthlyCostCents: 12000,
			FindingCount: 7, UnpricedFindingCount: 2},
		Thresholds: client.CloudSavingsThresholds{MinimumSavingsCents: 500, MinimumOffHoursMonthlyCents: 1000,
			UnallocatedShareThreshold: 0.25, OffHoursShare: 0.642857, AnalysedClusterLimit: 8},
	}
}

func TestCostSavingsRendersRecommendationsUnanalysedClustersAndWaste(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{savings: cloudSavingsFixture()}, "cost", "savings")
	if executeError != nil {
		t.Fatalf("cost savings failed: %v", executeError)
	}
	for _, expected := range []string{
		"Cloud savings (EUR): €1166.57/mo across 3 recommendations",
		"3 of 5 clusters analysed (2 not analysed: only the 8 biggest are) · 1 unpriced · 1 stale · 1 unreadable · generated 2026-09-18T06:00:00Z",
		"Recommendations (a cluster can carry several; the total counts each cluster once, at its best lever):",
		"prod-eu", "Right-size idle capacity", "€438.00", "22%", "€2000.00",
		"data-platform", "Reduce unallocated run rate (35% unclaimed)", "€600.00", "35%",
		"staging-1", "staging", "Off-hours schedule (weeknights and weekends)", "€128.57", "54%",
		"Off-hours saving for staging-1 is computed for stop \"0 19 * * 1-5\" / start \"0 7 * * 1-5\"", "ankra cluster power-schedules create",
		"Unpriced clusters (no cost snapshot yet):", "imported-lab", "imported",
		"Stale clusters (metering stopped over a day ago):", "old-dev", "k3s",
		"Unreadable clusters (breakdown could not be read on this pass):", "flaky",
		"Waste: 7 findings open, €120.00/mo (2 unpriced, not in that figure) · scanned 2026-09-17T22:00:00",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestCostSavingsOffHoursHintGroupsClustersByTheirOwnCrons(t *testing.T) {
	savings := cloudSavingsFixture()
	weekendStop := "0 20 * * 5"
	weekendStart := "0 6 * * 1"
	sameStop := "0 19 * * 1-5"
	sameStart := "0 7 * * 1-5"
	savings.Recommendations = append(savings.Recommendations,
		client.CloudSavingsRecommendation{Kind: "off_hours_schedule", ClusterName: "qa-2", ClusterID: "77777777-7777-4777-8777-777777777777",
			Evidence: client.CloudSavingsEvidence{StopCron: &sameStop, StartCron: &sameStart}},
		client.CloudSavingsRecommendation{Kind: "off_hours_schedule", ClusterName: "perf-lab", ClusterID: "88888888-8888-4888-8888-888888888888",
			Evidence: client.CloudSavingsEvidence{StopCron: &weekendStop, StartCron: &weekendStart}})
	output, executeError := runCostCommand(t, &costMock{savings: savings}, "cost", "savings")
	if executeError != nil {
		t.Fatalf("cost savings failed: %v", executeError)
	}
	for _, expected := range []string{
		"Off-hours saving for staging-1, qa-2 is computed for stop \"0 19 * * 1-5\" / start \"0 7 * * 1-5\"",
		"Off-hours saving for perf-lab is computed for stop \"0 20 * * 5\" / start \"0 6 * * 1\"",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestCostSavingsStructuredOutputIsTheApiDocument(t *testing.T) {
	output, executeError := runCostCommand(t, &costMock{savings: cloudSavingsFixture()}, "cost", "savings", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost savings -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	if decoded["currency"] != "eur" || decoded["total_monthly_savings_cents"] != float64(116657) {
		t.Fatalf("structured document lacks the wire fields: %+v", decoded)
	}
	recommendations, _ := decoded["recommendations"].([]any)
	if len(recommendations) != 3 {
		t.Fatalf("recommendations missing: %+v", decoded)
	}
	evidence, _ := recommendations[2].(map[string]any)["evidence"].(map[string]any)
	if evidence["stop_cron"] != "0 19 * * 1-5" || evidence["idle_monthly_cents"] != nil {
		t.Fatalf("evidence must keep the wire members, nulls included: %+v", evidence)
	}
	for _, listMember := range []string{"breakdowns", "namespaces", "unpriced_clusters", "stale_clusters", "unreadable_clusters"} {
		if _, isList := decoded[listMember].([]any); !isList {
			t.Fatalf("%s must be a list in the document: %+v", listMember, decoded[listMember])
		}
	}
	unreadable := decoded["unreadable_clusters"].([]any)[0].(map[string]any)
	if _, hasKind := unreadable["kind"]; hasKind {
		t.Fatalf("an unreadable cluster carries no kind on the wire, got %+v", unreadable)
	}
}

func TestCostSavingsWithNothingPricedSaysWhyAndNeverPrintsZeroSavings(t *testing.T) {
	empty := &client.CloudSavings{
		Currency: "usd", Recommendations: []client.CloudSavingsRecommendation{}, Breakdowns: []client.CloudSavingsBreakdown{},
		Namespaces: []client.CloudSavingsNamespace{}, StaleClusters: []client.CloudSavingsCluster{},
		UnreadableClusters:   []client.CloudSavingsCluster{},
		UnpricedClusters:     []client.CloudSavingsCluster{{ClusterID: costClusterID, ClusterName: "lab", Kind: "imported"}},
		UnpricedClusterCount: 1,
		Waste:                client.CloudSavingsWaste{Available: false},
		Thresholds:           client.CloudSavingsThresholds{MinimumSavingsCents: 500, AnalysedClusterLimit: 8},
	}
	output, executeError := runCostCommand(t, &costMock{savings: empty}, "cost", "savings")
	if executeError != nil {
		t.Fatalf("cost savings failed: %v", executeError)
	}
	for _, expected := range []string{
		"No priced clusters yet, so there is nothing to recommend.",
		"Unpriced clusters (no cost snapshot yet):", "lab",
		"Waste: the scan could not be read (this is not the same as no waste).",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "$0.00") {
		t.Fatalf("an empty model must read as absent, not as zero savings:\n%s", output)
	}
}

func TestCostSavingsWithOnlyStaleClustersDoesNotSayYet(t *testing.T) {
	stale := &client.CloudSavings{
		Currency: "eur", Recommendations: []client.CloudSavingsRecommendation{}, Breakdowns: []client.CloudSavingsBreakdown{},
		Namespaces: []client.CloudSavingsNamespace{}, UnpricedClusters: []client.CloudSavingsCluster{},
		UnreadableClusters: []client.CloudSavingsCluster{},
		StaleClusters:      []client.CloudSavingsCluster{{ClusterID: costClusterID, ClusterName: "old-dev", Kind: "k3s"}},
		StaleClusterCount:  1,
		Waste:              client.CloudSavingsWaste{Available: true, HasData: true},
		Thresholds:         client.CloudSavingsThresholds{MinimumSavingsCents: 500, AnalysedClusterLimit: 8},
	}
	output, executeError := runCostCommand(t, &costMock{savings: stale}, "cost", "savings")
	if executeError != nil {
		t.Fatalf("cost savings failed: %v", executeError)
	}
	if !strings.Contains(output, "No cluster is priced right now (1 cluster with stalled metering)") ||
		!strings.Contains(output, "Stale clusters (metering stopped over a day ago):") || !strings.Contains(output, "old-dev") {
		t.Fatalf("a fleet whose only clusters are stale must say so:\n%s", output)
	}
	if strings.Contains(output, "No priced clusters yet") {
		t.Fatalf("a formerly priced cluster must not be described as never priced:\n%s", output)
	}
}

func TestCostSavingsWithPricedClustersButNoLeverNamesTheMinimum(t *testing.T) {
	quiet := &client.CloudSavings{
		Currency: "gbp", PricedClusterCount: 2, AnalysedClusterCount: 2,
		Recommendations: []client.CloudSavingsRecommendation{}, Breakdowns: []client.CloudSavingsBreakdown{},
		Namespaces: []client.CloudSavingsNamespace{}, UnpricedClusters: []client.CloudSavingsCluster{},
		StaleClusters: []client.CloudSavingsCluster{}, UnreadableClusters: []client.CloudSavingsCluster{},
		Waste:      client.CloudSavingsWaste{Available: true, HasData: true, FindingCount: 0},
		Thresholds: client.CloudSavingsThresholds{MinimumSavingsCents: 500, AnalysedClusterLimit: 8},
	}
	output, executeError := runCostCommand(t, &costMock{savings: quiet}, "cost", "savings")
	if executeError != nil {
		t.Fatalf("cost savings failed: %v", executeError)
	}
	for _, expected := range []string{
		"Cloud savings (GBP): £0.00/mo across 0 recommendations",
		"2 of 2 clusters analysed · 0 unpriced · 0 stale",
		"No recommendation clears the £5.00/mo minimum on the analysed clusters.",
		"Waste: no open findings.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "Recommendations (a cluster can carry several; the total counts each cluster once, at its best lever):") {
		t.Fatalf("an empty recommendation list must not render a table:\n%s", output)
	}
}

func TestCostClusterRendersBreakdownAndNamespaces(t *testing.T) {
	stackID := "22222222-2222-4222-8222-222222222222"
	cost := &client.ClusterCost{
		HasData: true,
		Summary: &client.ClusterCostSummary{
			Provider: "hetzner", Currency: "eur", TotalNodeCount: 4, PricedNodeCount: 4,
			HourlyCostCents: 115, MonthlyCostEstimateCents: 84200, MonthToDateCents: 41000, ProjectedMonthEndCents: 86000,
			ConfidenceLevel: "high", SnapshotAt: "2026-08-30T14:00:00",
			ComputeOnDemandCents: 100, StorageCents: 10, NetworkCents: 2, ControlPlaneCents: 3, InfrastructureCents: 5,
			IdleHourlyCents: 20, UnallocatedHourlyCents: 30, StorageVolumeCount: 3, AppliedDiscountPct: 10,
		},
		Trend: []client.CostTrendPoint{
			{Day: "2026-08-01", MonthlyCostEstimateCents: 80000},
			{Day: "2026-08-30", MonthlyCostEstimateCents: 84200},
		},
		Namespaces: []client.NamespaceCost{
			{Namespace: "payments", StackID: &stackID, AllocatedMonthlyCents: 30000, CPUShare: 0.4, MemoryShare: 0.35, AllocationSource: "requests"},
		},
		Readiness: &client.CostReadiness{State: "ready"},
	}
	output, executeError := runCostCommand(t, &costMock{clusterCost: cost}, "cost", "cluster", costClusterID)
	if executeError != nil {
		t.Fatalf("cost cluster failed: %v", executeError)
	}
	for _, expected := range []string{
		"€860.00 projected month end", "€1.15/hr", "4 of 4 nodes priced", "10.0% discount applied",
		"Compute (on-demand)", "€730.00", "Bastion & VMs", "€36.50",
		"payments", "€300.00", "40.0%", "35.0%", "requests",
		"Trend: 2 daily points",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestCostClusterWithoutEstimateExplainsReadiness(t *testing.T) {
	provider := "aws"
	cost := &client.ClusterCost{
		HasData:    false,
		Trend:      []client.CostTrendPoint{},
		Namespaces: []client.NamespaceCost{},
		Readiness:  &client.CostReadiness{State: "no_credential", Provider: &provider},
	}
	output, executeError := runCostCommand(t, &costMock{clusterCost: cost}, "cost", "cluster", costClusterID)
	if executeError != nil {
		t.Fatalf("cost cluster failed: %v", executeError)
	}
	if !strings.Contains(output, "No cost estimate for "+costClusterID) ||
		!strings.Contains(output, "no cloud credential for Amazon Web Services is connected") {
		t.Fatalf("a missing estimate must say why:\n%s", output)
	}
	if strings.Contains(output, "€0.00") || strings.Contains(output, "$0.00") {
		t.Fatalf("a missing estimate must not print zeros:\n%s", output)
	}
}

func TestCostSettingsGetRendersEverySetting(t *testing.T) {
	settings := &client.CostSettings{Currency: "gbp", EffectiveDiscountPct: 7.5, IncludeNetworkEgressEstimate: true}
	output, executeError := runCostCommand(t, &costMock{settings: settings}, "cost", "settings", "get")
	if executeError != nil {
		t.Fatalf("cost settings get failed: %v", executeError)
	}
	for _, expected := range []string{"Display currency: GBP", "Effective discount: 7.5%", "Network egress estimate: on"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestCostSettingsSetChangesOnlyThePassedFlags(t *testing.T) {
	mock := &costMock{settings: &client.CostSettings{Currency: "usd", EffectiveDiscountPct: 0, IncludeNetworkEgressEstimate: true}}
	output, executeError := runCostCommand(t, mock, "cost", "settings", "set", "--discount", "12.5")
	if executeError != nil {
		t.Fatalf("cost settings set failed: %v", executeError)
	}
	if len(mock.updates) != 1 {
		t.Fatalf("expected one update, got %d", len(mock.updates))
	}
	sent := mock.updates[0]
	if sent.Currency != "usd" || sent.EffectiveDiscountPct != 12.5 || !sent.IncludeNetworkEgressEstimate {
		t.Fatalf("update must keep the unchanged settings: %+v", sent)
	}
	if !strings.Contains(output, "Cost settings updated.") || !strings.Contains(output, "Effective discount: 12.5%") {
		t.Fatalf("output lacks the confirmation:\n%s", output)
	}
}

func TestCostSettingsSetNormalisesCurrencyAndDropsEgress(t *testing.T) {
	mock := &costMock{settings: &client.CostSettings{Currency: "usd", EffectiveDiscountPct: 5, IncludeNetworkEgressEstimate: true}}
	_, executeError := runCostCommand(t, mock, "cost", "settings", "set", "--currency", " EUR ", "--include-egress=false")
	if executeError != nil {
		t.Fatalf("cost settings set failed: %v", executeError)
	}
	sent := mock.updates[0]
	if sent.Currency != "eur" || sent.EffectiveDiscountPct != 5 || sent.IncludeNetworkEgressEstimate {
		t.Fatalf("unexpected update: %+v", sent)
	}
}

func TestCostSettingsSetRefusesNoFlagsAndOutOfRangeDiscount(t *testing.T) {
	mock := &costMock{settings: &client.CostSettings{Currency: "usd"}}
	_, executeError := runCostCommand(t, mock, "cost", "settings", "set")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("expected a usage error without flags, got %v", executeError)
	}
	_, executeError = runCostCommand(t, mock, "cost", "settings", "set", "--discount", "140")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("expected a usage error for an out-of-range discount, got %v", executeError)
	}
	if len(mock.updates) != 0 {
		t.Fatalf("no update must be sent on a refused invocation, got %d", len(mock.updates))
	}
}

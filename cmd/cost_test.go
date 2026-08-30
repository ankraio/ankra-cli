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
	clusterCost *client.ClusterCost
	settings    *client.CostSettings
	updates     []client.CostSettings
}

func (m *costMock) GetFleetCloudCost() (*client.FleetCloudCost, error) {
	return m.fleet, nil
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
	return []*cobra.Command{costSummaryCmd, costClusterCmd, costSettingsGetCmd, costSettingsSetCmd}
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

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"ankra/internal/client"
)

func breakdownFleet() *client.FleetCloudCost {
	projectName := "Edge"
	credentialName := "hetzner-prod"
	return &client.FleetCloudCost{
		Currency:                 "eur",
		ClusterCount:             3,
		MonthlyCostEstimateCents: 90000,
		MonthToDateCents:         60000,
		ProjectedMonthEndCents:   95000,
		ByProvider:               []client.FleetProviderCost{{Provider: "hetzner", ClusterCount: 3, MonthlyCostEstimateCents: 90000}},
		ByProject: []client.FleetProjectCost{
			{ProjectName: &projectName, ClusterCount: 2, MonthlyCostEstimateCents: 60000},
			{ClusterCount: 1, MonthlyCostEstimateCents: 30000},
		},
		ByCredential: []client.FleetCredentialCost{
			{CredentialName: &credentialName, Provider: "hetzner", ClusterCount: 2, MonthlyCostEstimateCents: 60000},
			{Provider: "hetzner", ClusterCount: 1, MonthlyCostEstimateCents: 30000},
		},
		TopClusters: []client.FleetClusterCost{{ClusterName: "prod-eu", Provider: "hetzner"}},
	}
}

func TestCostSummaryBreakdowns(t *testing.T) {
	for _, testCase := range []struct {
		by      string
		present []string
		absent  []string
	}{
		{"", []string{"By provider:", "By project:", "Costliest clusters:"}, []string{"By credential:"}},
		{"provider", []string{"By provider:"}, []string{"By project:", "By credential:", "Costliest clusters:"}},
		{"credential", []string{"By credential:", "hetzner-prod", "(no credential)"}, []string{"By provider:", "By project:", "Costliest clusters:"}},
		{"Project", []string{"By project:", "Edge", "(no project)"}, []string{"By provider:", "By credential:", "Costliest clusters:"}},
	} {
		t.Run("by "+testCase.by, func(t *testing.T) {
			mock := &projectsMock{projects: twoProjects(), fleet: breakdownFleet()}
			args := []string{"cost", "summary"}
			if testCase.by != "" {
				args = append(args, "--by", testCase.by)
			}
			stdout, _, err := runProjectCommand(t, mock, "", args...)
			if err != nil {
				t.Fatalf("cost summary: %v", err)
			}
			if !strings.Contains(stdout, "projected month end") {
				t.Errorf("every layout keeps the totals: %s", stdout)
			}
			for _, fragment := range testCase.present {
				if !strings.Contains(stdout, fragment) {
					t.Errorf("stdout is missing %q:\n%s", fragment, stdout)
				}
			}
			for _, fragment := range testCase.absent {
				if strings.Contains(stdout, fragment) {
					t.Errorf("stdout carries %q:\n%s", fragment, stdout)
				}
			}
		})
	}
}

func TestCostSummaryBreakdownTheRollupDidNotServe(t *testing.T) {
	fleet := breakdownFleet()
	fleet.ByCredential = nil
	mock := &projectsMock{projects: twoProjects(), fleet: fleet}
	stdout, _, err := runProjectCommand(t, mock, "", "cost", "summary", "--by", "credential")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "No by-credential breakdown in this rollup.") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestCostSummaryBreakdownRejectsAnUnknownGrouping(t *testing.T) {
	mock := &projectsMock{projects: twoProjects(), fleet: breakdownFleet()}
	_, _, err := runProjectCommand(t, mock, "", "cost", "summary", "--by", "region")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("err = %v (exit %d)", err, exitCodeFor(err))
	}
}

func TestCostSummaryBreakdownWithNothingPriced(t *testing.T) {
	var out bytes.Buffer
	renderFleetCloudCostBreakdown(&out, &client.FleetCloudCost{}, fleetCostByProject)
	if !strings.Contains(out.String(), "No priced clusters yet.") {
		t.Fatalf("out = %s", out.String())
	}
}

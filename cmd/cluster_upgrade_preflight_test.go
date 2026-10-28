package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

type upgradePreflightCall struct {
	Kind          string
	ClusterID     string
	TargetVersion string
}

// upgradePreflightMock answers the preflight with a fixed report and records
// every preflight and upgrade it was asked for.
type upgradePreflightMock struct {
	clusterUpgradeMock
	report         *client.UpgradePreflightReport
	preflightError error
	preflightCalls []upgradePreflightCall
}

func (m *upgradePreflightMock) GetUpgradePreflight(kind, clusterID, targetVersion string) (*client.UpgradePreflightReport, error) {
	m.preflightCalls = append(m.preflightCalls, upgradePreflightCall{Kind: kind, ClusterID: clusterID, TargetVersion: targetVersion})
	if m.preflightError != nil {
		return nil, m.preflightError
	}
	return m.report, nil
}

const upgradePreflightClusterID = "62f4559a-a44d-46d7-aab3-a57c9dd6b4c6"

func readyPreflightReport() *client.UpgradePreflightReport {
	return &client.UpgradePreflightReport{
		ClusterID: upgradePreflightClusterID, RequestedTargetVersion: "v1.35.9", TargetVersion: "v1.35.9+k3s1",
		Distribution: "k3s", Verdict: client.UpgradePreflightVerdictReady, Warnings: 1,
		Checks: []client.UpgradePreflightFinding{
			{Check: "hop_plan", Status: client.UpgradePreflightStatusPass, Message: "2 of 2 nodes will upgrade to v1.35.9+k3s1."},
			{Check: "cloudnative_pg", Status: client.UpgradePreflightStatusWarn, Message: "data/main stops its instances over 1800 s."},
			{Check: "k3s_address_pin", Status: client.UpgradePreflightStatusSkipped, Message: "Not checked."},
		},
	}
}

func blockedPreflightReport(overridable bool) *client.UpgradePreflightReport {
	report := readyPreflightReport()
	report.Verdict = client.UpgradePreflightVerdictBlocked
	report.Blocks = 1
	report.Checks = append(report.Checks, client.UpgradePreflightFinding{Check: "pod_disruption_budgets",
		Status: client.UpgradePreflightStatusBlock, OverridableWithForce: overridable,
		Message: "PodDisruptionBudget data/postgres allows no disruption; it will park the drain on node worker-0."})
	return report
}

func newUpgradePreflightMock(report *client.UpgradePreflightReport) *upgradePreflightMock {
	return &upgradePreflightMock{
		clusterUpgradeMock: clusterUpgradeMock{
			cluster: client.ClusterListItem{ID: upgradePreflightClusterID, Name: "demo", Kind: "hetzner"}},
		report: report,
	}
}

func resetUpgradePreflightFlags(t *testing.T) {
	t.Cleanup(func() {
		_ = clusterUpgradePreflightCmd.Flags().Set("output", "")
		clusterUpgradeForce = false
		_ = clusterUpgradeCmd.Flags().Set("force", "false")
	})
}

func TestClusterUpgradePreflightPrintsEveryCheckAndExitsZeroWhenReady(t *testing.T) {
	mock := newUpgradePreflightMock(readyPreflightReport())
	setMockClient(t, mock)
	resetUpgradePreflightFlags(t)

	output, runError := executeCommand("cluster", "upgrade", "preflight", upgradePreflightClusterID, "v1.35.9")
	if runError != nil {
		t.Fatalf("a ready preflight exits 0: %v", runError)
	}
	output = stripANSICodes(output)
	for _, want := range []string{"Upgrade preflight to v1.35.9+k3s1 (k3s): READY, 0 blocking, 1 warnings",
		"hop_plan", "cloudnative_pg", "warn", "k3s_address_pin", "skipped"} {
		if !strings.Contains(output, want) {
			t.Fatalf("the preflight output must carry %q:\n%s", want, output)
		}
	}
	if len(mock.preflightCalls) != 1 || mock.preflightCalls[0] != (upgradePreflightCall{
		Kind: "hetzner", ClusterID: upgradePreflightClusterID, TargetVersion: "v1.35.9"}) {
		t.Fatalf("the preflight must be asked of the cluster's provider: %+v", mock.preflightCalls)
	}
	if len(mock.hetznerCalls) != 0 {
		t.Fatal("the preflight must never upgrade")
	}
}

func TestClusterUpgradePreflightExitsEightWhenBlocked(t *testing.T) {
	setMockClient(t, newUpgradePreflightMock(blockedPreflightReport(true)))
	resetUpgradePreflightFlags(t)

	output, runError := executeCommand("cluster", "upgrade", "preflight", upgradePreflightClusterID, "v1.35.9")
	if exitCodeFor(runError) != exitBlocked {
		t.Fatalf("a blocked preflight exits %d, got %d (%v)", exitBlocked, exitCodeFor(runError), runError)
	}
	if output = stripANSICodes(output); !strings.Contains(output, "BLOCKED") || !strings.Contains(output, "block (--force)") {
		t.Fatalf("the blocked check and its force note must print:\n%s", output)
	}
}

func TestClusterUpgradePreflightJSONIsThePlatformDocument(t *testing.T) {
	report := readyPreflightReport()
	report.RawDocument = client.NewRawDocument([]byte(`{"verdict":"ready","checks":[],"added_later":true}`))
	setMockClient(t, newUpgradePreflightMock(report))
	resetUpgradePreflightFlags(t)

	output, runError := executeCommand("cluster", "upgrade", "preflight", upgradePreflightClusterID, "v1.35.9", "-o", "json")
	if runError != nil {
		t.Fatalf("preflight -o json: %v", runError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(strings.TrimSpace(output)), &decoded); decodeError != nil {
		t.Fatalf("-o json must print only the document: %v\n%s", decodeError, output)
	}
	if decoded["added_later"] != true {
		t.Fatalf("-o json must carry fields the CLI does not declare: %v", decoded)
	}
}

func TestClusterUpgradePreflightRefusesAnUnsupportedKind(t *testing.T) {
	mock := newUpgradePreflightMock(readyPreflightReport())
	mock.cluster.Kind = "imported"
	setMockClient(t, mock)
	resetUpgradePreflightFlags(t)

	if _, runError := executeCommand("cluster", "upgrade", "preflight", upgradePreflightClusterID, "v1.35.9"); runError == nil {
		t.Fatal("an imported cluster has no upgrade preflight")
	}
	if len(mock.preflightCalls) != 0 {
		t.Fatal("an unsupported kind must not reach the platform")
	}
}

func TestClusterUpgradeStopsOnABlockingPreflightWithoutUpgrading(t *testing.T) {
	mock := newUpgradePreflightMock(blockedPreflightReport(false))
	setMockClient(t, mock)
	resetUpgradePreflightFlags(t)

	_, runError := executeCommand("cluster", "upgrade", upgradePreflightClusterID, "v1.35.9", "--force")
	if exitCodeFor(runError) != exitBlocked || !errors.Is(runError, errUpgradePreflightBlocked) {
		t.Fatalf("a block force cannot override stops the upgrade with exit %d: %v", exitBlocked, runError)
	}
	if len(mock.hetznerCalls) != 0 {
		t.Fatalf("a blocked preflight must not upgrade: %+v", mock.hetznerCalls)
	}
}

func TestClusterUpgradeForceGoesPastOnlyTheBudgetBlock(t *testing.T) {
	mock := newUpgradePreflightMock(blockedPreflightReport(true))
	setMockClient(t, mock)
	resetUpgradePreflightFlags(t)

	if _, runError := executeCommand("cluster", "upgrade", upgradePreflightClusterID, "v1.35.9"); exitCodeFor(runError) != exitBlocked {
		t.Fatalf("without --force the budget block stops the upgrade: %v", runError)
	}
	if len(mock.hetznerCalls) != 0 {
		t.Fatal("an unforced upgrade with a budget block must not upgrade")
	}
	if _, runError := executeCommand("cluster", "upgrade", upgradePreflightClusterID, "v1.35.9", "--force"); runError != nil {
		t.Fatalf("--force goes past the budget block: %v", runError)
	}
	if len(mock.hetznerCalls) != 1 || !mock.hetznerCalls[0].Force {
		t.Fatalf("the forced upgrade must reach the platform with force: %+v", mock.hetznerCalls)
	}
}

func TestClusterUpgradeHoldsABlockedVerdictNoCheckExplains(t *testing.T) {
	report := readyPreflightReport()
	report.Verdict = client.UpgradePreflightVerdictBlocked
	report.Blocks = 1
	mock := newUpgradePreflightMock(report)
	setMockClient(t, mock)
	resetUpgradePreflightFlags(t)

	_, runError := executeCommand("cluster", "upgrade", upgradePreflightClusterID, "v1.35.9", "--force")
	if exitCodeFor(runError) != exitBlocked || !errors.Is(runError, errUpgradePreflightBlocked) {
		t.Fatalf("a blocked verdict with no blocking check stops even a forced upgrade: %v", runError)
	}
	if len(mock.hetznerCalls) != 0 {
		t.Fatalf("an unexplained block must not upgrade: %+v", mock.hetznerCalls)
	}
}

func TestPrintUpgradePreflightLeavesAnUnknownVerdictUncoloured(t *testing.T) {
	report := readyPreflightReport()
	report.Verdict = "inconclusive"
	var output strings.Builder
	printUpgradePreflight(&output, report)
	if !strings.Contains(output.String(), ": INCONCLUSIVE,") {
		t.Fatalf("an unknown verdict prints as is, without the ready colour:\n%s", output.String())
	}
}

func TestClusterUpgradePrintsTheReadyPreflightAndUpgrades(t *testing.T) {
	mock := newUpgradePreflightMock(readyPreflightReport())
	setMockClient(t, mock)
	resetUpgradePreflightFlags(t)

	var output string
	captured := captureStdout(t, func() {
		var runError error
		output, runError = executeCommand("cluster", "upgrade", upgradePreflightClusterID, "v1.35.9")
		if runError != nil {
			t.Fatalf("a ready preflight lets the upgrade run: %v", runError)
		}
	})
	if !strings.Contains(stripANSICodes(output), "READY") || !strings.Contains(captured, "Kubernetes version upgrade initiated") {
		t.Fatalf("the preflight prints before the upgrade:\n%s\n%s", output, captured)
	}
	if len(mock.hetznerCalls) != 1 {
		t.Fatalf("the upgrade must run once: %+v", mock.hetznerCalls)
	}
}

func TestClusterUpgradeProceedsWhenThePreflightCannotRun(t *testing.T) {
	mock := newUpgradePreflightMock(nil)
	mock.preflightError = errors.New("unexpected status: 404 Not Found")
	setMockClient(t, mock)
	resetUpgradePreflightFlags(t)

	captureStdout(t, func() {
		output, runError := executeCommand("cluster", "upgrade", upgradePreflightClusterID, "v1.35.9")
		if runError != nil {
			t.Fatalf("a platform without the preflight still upgrades (it gates itself): %v", runError)
		}
		if !strings.Contains(output, "could not be run") {
			t.Fatalf("the missing preflight must be said on stderr:\n%s", output)
		}
	})
	if len(mock.hetznerCalls) != 1 {
		t.Fatalf("the upgrade must run once: %+v", mock.hetznerCalls)
	}
}

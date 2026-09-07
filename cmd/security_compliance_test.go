package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

type securityComplianceMock struct {
	baseMock
	overview          *client.SecurityComplianceOverview
	frameworks        *client.SecurityComplianceFrameworkList
	framework         *client.SecurityComplianceFramework
	switchedKey       string
	switchedEnabled   *bool
	report            *client.SecurityComplianceFrameworkReport
	reportMonth       string
	export            *client.SecurityComplianceExport
	exportOptions     *client.SecurityComplianceExportOptions
	benchmarks        *client.SecurityClusterBenchmarks
	benchmarksError   error
	resources         *client.SecurityBenchmarkResources
	resourcesOptions  *client.SecurityBenchmarkResourcesOptions
	violations        *client.SecurityClusterPolicyViolations
	exposure          *client.SecurityClusterNetworkExposure
	policyMode        *client.SecurityPolicyModeResult
	policyModeSet     string
	baseline          *client.SecurityBaselineResult
	baselineCluster   string
	addon             *client.SecurityAddonPosture
	addonName         string
	versions          *client.ApplicationSecurityVersions
	versionsComponent string
}

func (m *securityComplianceMock) GetSecurityComplianceOverview() (*client.SecurityComplianceOverview, error) {
	return m.overview, nil
}

func (m *securityComplianceMock) ListSecurityComplianceFrameworks() (*client.SecurityComplianceFrameworkList, error) {
	return m.frameworks, nil
}

func (m *securityComplianceMock) SetSecurityComplianceFrameworkEnabled(frameworkKey string, enabled bool) (*client.SecurityComplianceFramework, error) {
	m.switchedKey = frameworkKey
	m.switchedEnabled = &enabled
	return m.framework, nil
}

func (m *securityComplianceMock) GetSecurityComplianceFrameworkReport(_ string, month string) (*client.SecurityComplianceFrameworkReport, error) {
	m.reportMonth = month
	return m.report, nil
}

func (m *securityComplianceMock) ExportSecurityComplianceReport(options client.SecurityComplianceExportOptions) (*client.SecurityComplianceExport, error) {
	m.exportOptions = &options
	return m.export, nil
}

func (m *securityComplianceMock) GetSecurityClusterBenchmarks(string) (*client.SecurityClusterBenchmarks, error) {
	return m.benchmarks, m.benchmarksError
}

func (m *securityComplianceMock) GetSecurityBenchmarkResources(options client.SecurityBenchmarkResourcesOptions) (*client.SecurityBenchmarkResources, error) {
	m.resourcesOptions = &options
	return m.resources, nil
}

func (m *securityComplianceMock) GetSecurityClusterPolicyViolations(string) (*client.SecurityClusterPolicyViolations, error) {
	return m.violations, nil
}

func (m *securityComplianceMock) GetSecurityClusterNetworkExposure(string) (*client.SecurityClusterNetworkExposure, error) {
	return m.exposure, nil
}

func (m *securityComplianceMock) SetSecurityPolicyMode(_ string, mode string) (*client.SecurityPolicyModeResult, error) {
	m.policyModeSet = mode
	return m.policyMode, nil
}

func (m *securityComplianceMock) EnableSecurityBaseline(clusterID string) (*client.SecurityBaselineResult, error) {
	m.baselineCluster = clusterID
	return m.baseline, nil
}

func (m *securityComplianceMock) GetSecurityAddonPosture(_ string, addonName string) (*client.SecurityAddonPosture, error) {
	m.addonName = addonName
	return m.addon, nil
}

func (m *securityComplianceMock) GetApplicationSecurityVersions(_ context.Context, _ string, component string) (*client.ApplicationSecurityVersions, error) {
	m.versionsComponent = component
	return m.versions, nil
}

func TestSecurityCompliance_RendersUnavailableClustersWithReason(t *testing.T) {
	mock := &securityComplianceMock{overview: &client.SecurityComplianceOverview{Clusters: []client.SecurityComplianceCluster{
		{ClusterName: "production", Status: "available", Benchmarks: []client.SecurityComplianceBenchmarkTotals{{Title: "CIS Kubernetes", PassCount: 40, FailCount: 3, FailedChecks: 3}}},
		{ClusterName: "lab", Status: "not_available", Detail: stringPointer("Trivy Operator is not installed on this cluster.")},
	}}}
	output, err := runSecurityCommand(t, mock, "security", "compliance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{"CIS Kubernetes", "40", "not_available", "Trivy Operator is not installed", "1 without compliance data"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
}

func TestSecurityComplianceFrameworks_NilScoreRendersDash(t *testing.T) {
	score := 82.5
	satisfied := 12
	mock := &securityComplianceMock{frameworks: &client.SecurityComplianceFrameworkList{Frameworks: []client.SecurityComplianceFramework{
		{Key: "soc2", Title: "SOC 2", Version: "2017", Enabled: true, Score: &score, SatisfiedControls: &satisfied, ControlCount: 20, AutomatedControlCount: 14},
		{Key: "gdpr", Title: "GDPR", Version: "2016/679", Enabled: false, ControlCount: 30, AutomatedControlCount: 9},
	}}}
	output, err := runSecurityCommand(t, mock, "security", "compliance", "frameworks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "82.5%") || !strings.Contains(output, "gdpr") {
		t.Errorf("expected the enabled score and the disabled framework:\n%s", output)
	}
	lines := strings.Split(output, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "gdpr") && strings.Count(line, "-") >= 5 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the disabled framework's scores to read as dashes:\n%s", output)
	}
}

func TestSecurityComplianceFrameworksEnable_DeclineWritesNothing(t *testing.T) {
	mock := &securityComplianceMock{framework: &client.SecurityComplianceFramework{Key: "soc2", Title: "SOC 2", Enabled: true}}
	_, _, err := runSecurityCommandWithInput(t, mock, "n\n", "security", "compliance", "frameworks", "enable", "soc2")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled, got %v", err)
	}
	if mock.switchedEnabled != nil {
		t.Fatalf("expected no write after a decline")
	}
}

func TestSecurityComplianceFrameworksDisable_YesSendsFalse(t *testing.T) {
	mock := &securityComplianceMock{framework: &client.SecurityComplianceFramework{Key: "soc2", Title: "SOC 2", Enabled: false}}
	output, err := runSecurityCommand(t, mock, "security", "compliance", "frameworks", "disable", "soc2", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.switchedKey != "soc2" || mock.switchedEnabled == nil || *mock.switchedEnabled {
		t.Fatalf("expected disable forwarded for soc2, got %q %v", mock.switchedKey, mock.switchedEnabled)
	}
	if !strings.Contains(output, "SOC 2 (soc2) is now disabled") {
		t.Errorf("expected the state line:\n%s", output)
	}
}

func TestSecurityComplianceFrameworksReport_RendersControlsAndMonth(t *testing.T) {
	categoryScore := 50.0
	mock := &securityComplianceMock{report: &client.SecurityComplianceFrameworkReport{
		Framework:   client.SecurityComplianceFrameworkIdentity{Key: "soc2", Title: "SOC 2", Version: "2017"},
		Period:      "2026-08",
		GeneratedAt: "2026-09-01T00:00:00Z",
		Score:       75,
		Categories: []client.SecurityComplianceCategory{{
			Title: "Access control", Score: &categoryScore, Satisfied: 1, Failing: 1,
			Controls: []client.SecurityComplianceControl{
				{ID: "CC6.1", Title: "Logical access", Status: "failing", Checks: []client.SecurityComplianceCheck{{Title: "MFA enforced", Passed: false}}},
				{ID: "CC6.2", Title: "Provisioning", Status: "satisfied"},
			},
		}},
		Trend:           []client.SecurityComplianceTrendPoint{{Month: "2026-07", Score: 60}, {Month: "2026-08", Score: 75}},
		AvailableMonths: []string{"2026-07", "2026-08"},
	}}
	output, err := runSecurityCommand(t, mock, "security", "compliance", "frameworks", "report", "soc2", "--month", "2026-08")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.reportMonth != "2026-08" {
		t.Fatalf("expected the month forwarded, got %q", mock.reportMonth)
	}
	for _, fragment := range []string{"SOC 2 2017 · 2026-08 · score 75%", "Access control", "CC6.1", "FAIL", "MFA enforced", "failing", "Trend: 2026-07 60%", "Recorded months: 2026-07, 2026-08"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
}

func TestSecurityComplianceExport_WritesFileAndRefusesOverwrite(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "evidence.json")
	mock := &securityComplianceMock{export: &client.SecurityComplianceExport{FileName: "../etc/passwd", ContentType: "application/json", Body: []byte(`{"ok":true}`)}}
	output, err := runSecurityCommand(t, mock, "security", "compliance", "export", "--format", "json", "--start-date", "2026-07-01", "--output-file", target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.exportOptions == nil || mock.exportOptions.Format != "json" || mock.exportOptions.StartDate != "2026-07-01" {
		t.Fatalf("expected the format and window forwarded, got %+v", mock.exportOptions)
	}
	if !strings.Contains(output, "Wrote "+target) {
		t.Errorf("expected the wrote line:\n%s", output)
	}
	info, statError := os.Stat(target)
	if statError != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("expected the file written with 0600, got %v %v", info, statError)
	}
	_, err = runSecurityCommand(t, mock, "security", "compliance", "export", "--format", "json", "--output-file", target)
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error on overwrite without --force, got %v", err)
	}
}

func TestSecurityComplianceExport_DashWithWhitespaceStillMeansStdout(t *testing.T) {
	mock := &securityComplianceMock{export: &client.SecurityComplianceExport{ContentType: "text/csv", Body: []byte("a,b\n")}}
	output, err := runSecurityCommand(t, mock, "security", "compliance", "export", "--output-file", " - ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "a,b\n" {
		t.Fatalf("expected the body on stdout, got %q", output)
	}
	if _, statError := os.Stat("-"); statError == nil {
		_ = os.Remove("-")
		t.Fatal("a file named - was written to the working directory")
	}
}

func TestSecurityComplianceExport_ServerFileNameIsNeverAPath(t *testing.T) {
	if got := complianceExportLocalFileName("../../etc/passwd", "csv"); got != "passwd" {
		t.Errorf("expected the last path element only, got %q", got)
	}
	if got := complianceExportLocalFileName("", "json"); got != "compliance-report.json" {
		t.Errorf("expected the format-derived default, got %q", got)
	}
}

func TestSecurityComplianceExport_RefusesUnknownFormat(t *testing.T) {
	mock := &securityComplianceMock{}
	_, err := runSecurityCommand(t, mock, "security", "compliance", "export", "--format", "pdf")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestSecurityBenchmarks_RequiresCluster(t *testing.T) {
	mock := &securityComplianceMock{}
	_, err := runSecurityCommand(t, mock, "security", "benchmarks")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error without --cluster, got %v", err)
	}
}

func TestSecurityBenchmarks_AgentUnavailableReadsAsRetryableMessage(t *testing.T) {
	mock := &securityComplianceMock{benchmarksError: &client.ClusterAgentUnavailableError{Code: "CLUSTER_OFFLINE", Detail: "the cluster agent has not reported in", RetryAfter: 30}}
	_, err := runSecurityCommand(t, mock, "security", "benchmarks", "--cluster", securityTestClusterID)
	if err == nil {
		t.Fatal("expected an error")
	}
	message := err.Error()
	if !strings.Contains(message, "CLUSTER_OFFLINE") || !strings.Contains(message, "retry in 30s") || strings.Contains(message, "{") {
		t.Errorf("expected a plain retryable message, got %q", message)
	}
}

func TestSecurityBenchmarks_RendersFailedChecks(t *testing.T) {
	mock := &securityComplianceMock{benchmarks: &client.SecurityClusterBenchmarks{
		ClusterName: "production", Status: "available",
		Benchmarks: []client.SecurityBenchmark{{
			SpecID: "cis-1.23", Title: "CIS Kubernetes Benchmark", PassCount: 40, FailCount: 1,
			FailedChecks: []client.SecurityBenchmarkFailedCheck{{ID: "5.1.1", Title: "Ensure cluster-admin is used only where required", Severity: "HIGH", Remediation: "Remove the binding", AffectedCount: 2}},
		}},
		ConfigAudit: &client.SecurityConfigAuditAggregate{ReportCount: 12, Severity: client.SecurityConfigAuditSeverityCounts{Critical: 1},
			TopFailedChecks: []client.SecurityConfigAuditCheckCount{{ID: "KSV014", Title: "Root file system is not read-only", Severity: "LOW", Count: 9}}},
		IsTruncated: true,
	}}
	output, err := runSecurityCommand(t, mock, "security", "benchmarks", "--cluster", securityTestClusterID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{"CIS Kubernetes Benchmark (cis-1.23)", "5.1.1", "Remove the binding", "Config audit: 12 reports", "KSV014", "truncated"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
}

func TestSecurityBenchmarksResources_BenchmarkSourceNeedsSpec(t *testing.T) {
	mock := &securityComplianceMock{}
	_, err := runSecurityCommand(t, mock, "security", "benchmarks", "resources", "--cluster", securityTestClusterID, "--check", "5.1.1")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error without --benchmark, got %v", err)
	}
}

func TestSecurityBenchmarksResources_RendersOwners(t *testing.T) {
	mock := &securityComplianceMock{resources: &client.SecurityBenchmarkResources{
		ClusterName: "production", CheckID: "KSV014", Source: "config_audit", ControlTitle: "Root file system is not read-only",
		Resources: []client.SecurityComplianceAffectedResource{{
			Kind: "Deployment", Name: "api", Namespace: stringPointer("payments"), Severity: "LOW", Message: "container api runs with a writable root",
			Owner: client.SecurityComplianceResourceOwner{ResourceKind: stringPointer("addon"), Name: stringPointer("acme-api"), StackName: stringPointer("apps"), MatchedBy: "release"},
		}},
	}}
	output, err := runSecurityCommand(t, mock, "security", "benchmarks", "resources", "--cluster", securityTestClusterID, "--check", "KSV014", "--source", "config_audit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.resourcesOptions == nil || mock.resourcesOptions.Source != "config_audit" || mock.resourcesOptions.CheckID != "KSV014" {
		t.Fatalf("expected the drill-down options forwarded, got %+v", mock.resourcesOptions)
	}
	for _, fragment := range []string{"Root file system is not read-only", "payments", "addon acme-api (stack apps)", "1 failing resources"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
}

func TestSecurityViolations_RendersModeAndPlatformPosture(t *testing.T) {
	mock := &securityComplianceMock{violations: &client.SecurityClusterPolicyViolations{
		ClusterName: "production", Status: "not_available", Detail: stringPointer("Kyverno is not installed."),
		PlatformPosture: client.SecurityPlatformPosture{
			Status: "available", EvaluatedWorkloads: 14, Totals: client.SecurityPolicyTotals{Pass: 30, Fail: 4},
			Policies: []client.SecurityPolicyEntry{{Policy: "disallow-privileged", Severity: "HIGH", Category: "Pod Security", PassCount: 12, FailCount: 2,
				TopNamespaces: []client.SecurityPolicyNamespaceCount{{Namespace: "kube-system", FailCount: 2}}}},
		},
	}}
	output, err := runSecurityCommand(t, mock, "security", "violations", "--cluster", securityTestClusterID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{"policy add-on not installed", "Kyverno is not installed", "14 workloads evaluated", "disallow-privileged", "kube-system (2)"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
}

func TestSecurityNetworkExposure_NoWorkloadsIsNotAPerfectScore(t *testing.T) {
	mock := &securityComplianceMock{exposure: &client.SecurityClusterNetworkExposure{
		ClusterName:       "lab",
		Ingress:           client.SecurityDirectionScore{Score: 100},
		Egress:            client.SecurityDirectionScore{Score: 42, TotalWorkloads: 5, UnprotectedWorkloads: 3, TotalAdmittingRules: 4, BroadRules: 3, OverPrivilegeRate: 0.75},
		Findings:          []client.SecurityNetworkExposureFinding{{Direction: "egress", Severity: "MEDIUM", Title: "Unprotected workload", Namespace: "web", Workload: "frontend", AffectedCount: 1, Remediation: "Add a default-deny egress policy"}},
		FindingsTruncated: 2,
	}}
	output, err := runSecurityCommand(t, mock, "security", "network-exposure", "--cluster", securityTestClusterID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{"ingress: n/a (no workloads to score)", "egress:  42/100", "poor", "3 of 5 workloads unprotected", "Unprotected workload", "2 more not listed"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
}

func TestSecurityPolicyMode_RefusesUnknownMode(t *testing.T) {
	mock := &securityComplianceMock{}
	_, err := runSecurityCommand(t, mock, "security", "policy-mode", "strict", "--cluster", securityTestClusterID, "--yes")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestSecurityPolicyMode_EnforceDeclineWritesNothing(t *testing.T) {
	mock := &securityComplianceMock{policyMode: &client.SecurityPolicyModeResult{Mode: "enforce", Valid: true}}
	_, _, err := runSecurityCommandWithInput(t, mock, "n\n", "security", "policy-mode", "enforce", "--cluster", securityTestClusterID)
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled, got %v", err)
	}
	if mock.policyModeSet != "" {
		t.Fatalf("expected no write after a decline, got %q", mock.policyModeSet)
	}
}

func TestSecurityPolicyMode_YesSetsMode(t *testing.T) {
	mock := &securityComplianceMock{policyMode: &client.SecurityPolicyModeResult{Mode: "audit", Valid: true, CommitURL: stringPointer("https://github.com/acme/gitops/commit/abc")}}
	output, err := runSecurityCommand(t, mock, "security", "policy-mode", "audit", "--cluster", securityTestClusterID, "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.policyModeSet != "audit" || !strings.Contains(output, "Policy mode set to audit · committed https://github.com/acme/gitops/commit/abc") {
		t.Errorf("expected the mode forwarded and rendered, got %q:\n%s", mock.policyModeSet, output)
	}
}

func TestSecurityEnableBaseline_YesInstallsAndNarrates(t *testing.T) {
	mock := &securityComplianceMock{baseline: &client.SecurityBaselineResult{StackName: "ankra-security-baseline", JobCount: 3, Errors: []client.SecurityBaselineResourceError{}}}
	output, err := runSecurityCommand(t, mock, "security", "enable-baseline", "--cluster", securityTestClusterID, "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.baselineCluster != securityTestClusterID {
		t.Fatalf("expected the cluster forwarded, got %q", mock.baselineCluster)
	}
	if !strings.Contains(output, "Security baseline stack ankra-security-baseline: 3 jobs queued") || !strings.Contains(output, "ankra security clusters") {
		t.Errorf("expected the install summary:\n%s", output)
	}
}

func TestSecurityAddon_RendersPostureAndTopFindings(t *testing.T) {
	mock := &securityComplianceMock{addon: &client.SecurityAddonPosture{
		AddonName: "ingress-nginx", ChartName: stringPointer("ingress-nginx"), Namespace: stringPointer("ingress"), Status: "actionable",
		Attribution:       client.SecurityAttributionSummary{Status: "matched", Matched: 3},
		Actionable:        client.SecuritySeverityCounts{High: 2},
		AffectedWorkloads: 2, AffectedImages: 1, KnownExploited: 1,
		TopActionableFindings: []client.SecurityRemediationCandidate{{CVEID: "CVE-2025-1974", Severity: "HIGH", PackageName: "nginx", ActionableCount: 2, FindingID: "f-9"}},
	}}
	output, err := runSecurityCommand(t, mock, "security", "addon", "ingress-nginx", "--cluster", securityTestClusterID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.addonName != "ingress-nginx" {
		t.Fatalf("expected the add-on forwarded, got %q", mock.addonName)
	}
	for _, fragment := range []string{"ingress-nginx (ingress-nginx) in ingress: actionable", "2H", "1 known exploited", "CVE-2025-1974", "f-9"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
}

func TestApplicationSecurityVersions_RendersNotScannedAndLicenceVerdict(t *testing.T) {
	applicationID := "2f4e6a8c-0b1d-4e3f-a5b7-c9d1e3f5a7b9"
	mock := &securityComplianceMock{versions: &client.ApplicationSecurityVersions{
		ApplicationID: applicationID,
		Repository:    client.ApplicationRepository{Provider: "github", Owner: "acme", Name: "api", Visibility: "private"},
		Components:    []client.ApplicationImageVersionComponent{{Name: "api", Registry: "harbor.ankra.cloud", Repository: "acme/api", RegistryStatus: "listed"}},
		Versions: []client.ApplicationImageVersion{
			{Component: "api", Tag: "sha-abc1234", SBOM: client.ApplicationImageVersionSBOM{Status: "present", ComponentCount: intPointer(412), LicenseExposure: &client.SecurityLicenseExposure{NetworkCopyleft: 1}},
				Findings: client.ApplicationImageVersionFindings{Scanned: true, Observed: 9, Actionable: client.SecuritySeverityCounts{High: 1}},
				Running:  client.ApplicationImageVersionRunning{Workloads: 2, Clusters: 1, Namespaces: []string{"prod"}}},
			{Component: "api", Tag: "sha-def5678", SBOM: client.ApplicationImageVersionSBOM{Status: "absent"}, Findings: client.ApplicationImageVersionFindings{Scanned: false}},
		},
		Summary: client.ApplicationImageVersionsSummary{Versions: 2, WithSBOM: 1, WithFindings: 1, Running: 1},
		LicenseExposure: client.ApplicationLicenseExposure{Status: "assessed", Latest: &client.SecurityLicenseExposure{NetworkCopyleft: 1}, SourceDisclosureRequired: true, FlaggedComponents: 1,
			Components: []client.ApplicationLicenseComponent{{Component: "api", Tag: "sha-abc1234", Name: "mongo-driver", Version: "5.0", PackageType: "gomod", Licenses: []string{"SSPL-1.0"}, LicenseRisk: "network_copyleft"}}},
	}}
	output, err := runSecurityCommand(t, mock, "application", "security-versions", applicationID, "--component", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.versionsComponent != "api" {
		t.Fatalf("expected the component forwarded, got %q", mock.versionsComponent)
	}
	for _, fragment := range []string{"acme/api (github, private repository)", "412 components", "not scanned", "absent", "2 workloads on 1 clusters in prod", "obliges publishing its source", "mongo-driver", "SSPL-1.0"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
}

func intPointer(value int) *int { return &value }

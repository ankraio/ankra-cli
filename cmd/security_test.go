package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type securityMock struct {
	baseMock
	overview        *client.SecurityOverview
	findings        *client.SecurityFindingList
	findingsOptions *client.SecurityFindingsOptions
	detail          *client.SecurityFindingDetail
	clusters        *client.SecurityClusterList
}

func (m *securityMock) GetSecurityOverview(client.SecurityOverviewOptions) (*client.SecurityOverview, error) {
	return m.overview, nil
}

func (m *securityMock) ListSecurityFindings(options client.SecurityFindingsOptions) (*client.SecurityFindingList, error) {
	m.findingsOptions = &options
	return m.findings, nil
}

func (m *securityMock) GetSecurityFinding(string) (*client.SecurityFindingDetail, error) {
	return m.detail, nil
}

func (m *securityMock) ListSecurityClusters(client.SecurityClustersOptions) (*client.SecurityClusterList, error) {
	return m.clusters, nil
}

func securityCommandTree() []*cobra.Command {
	return []*cobra.Command{securityOverviewCmd, securityFindingsCmd, securityFindingCmd, securityClustersCmd}
}

func runSecurityCommand(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs(args)
	t.Cleanup(func() { resetTreeFlags(t, securityCommandTree()...) })
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

func securityFloatPointer(value float64) *float64 { return &value }

func exploitedFinding() client.SecurityFinding {
	return client.SecurityFinding{
		ID:                 "632d93d6-f07b-4618-ad37-ae453409e928",
		Provider:           "trivy_operator",
		CVEID:              "CVE-2025-24813",
		PackageType:        "maven",
		PackageName:        "org.apache.tomcat.embed:tomcat-embed-core",
		Severity:           "CRITICAL",
		FirstSeenAt:        "2026-08-01T00:00:00Z",
		LastSeenAt:         "2026-08-28T20:00:00Z",
		Occurrences:        4,
		AffectedClusters:   1,
		AffectedWorkloads:  4,
		FixableOccurrences: 4,
		DispositionCounts:  client.SecurityDispositionCounts{Open: 4},
		SecurityExploitIntelligence: client.SecurityExploitIntelligence{
			KnownExploited:       true,
			KevDateAdded:         stringPointer("2025-04-01"),
			KevDueDate:           stringPointer("2025-04-22"),
			KevRansomwareUse:     true,
			EPSSScore:            securityFloatPointer(0.997),
			EPSSPercentile:       securityFloatPointer(0.999),
			KevVendorProject:     stringPointer("Apache"),
			KevProduct:           stringPointer("Tomcat"),
			KevVulnerabilityName: stringPointer("Apache Tomcat Path Equivalence Vulnerability"),
			KevRequiredAction:    stringPointer("Apply mitigations per vendor instructions."),
		},
	}
}

func TestSecurityFindingsDefaultsToActionableExploitabilityAndMapsFlags(t *testing.T) {
	mock := &securityMock{findings: &client.SecurityFindingList{
		Result:       []client.SecurityFinding{exploitedFinding()},
		Pagination:   client.SecurityPagination{Page: 1, PageSize: 25, TotalPages: 1, TotalCount: 1},
		Facets:       client.SecurityFindingFacets{Exploited: []client.SecurityFacetCount{{Value: "known_exploited", Count: 22}}},
		Intelligence: client.SecurityIntelligenceStatus{KevSyncedAt: stringPointer("2026-08-28T20:13:11Z"), KevListed: 1685},
	}}

	output, executeError := runSecurityCommand(t, mock, "security", "findings")
	if executeError != nil {
		t.Fatalf("security findings failed: %v", executeError)
	}
	options := mock.findingsOptions
	if options == nil || options.Sort != "exploitability" || options.Order != "desc" ||
		strings.Join(options.Statuses, ",") != "open,acknowledged" || options.KnownExploited != nil || options.Fixable != nil {
		t.Fatalf("default options = %+v", options)
	}
	for _, expected := range []string{"CVE-2025-24813", "KEV", "deadline passed 2025-04-22", "ransomware", "EPSS 100% (top 1%)",
		"22 findings are on CISA's Known Exploited Vulnerabilities catalog"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}

	_, executeError = runSecurityCommand(t, mock, "security", "findings",
		"--known-exploited", "--severity", "critical,high", "--status", "any", "--fixable", "false",
		"--sort", "epss", "--order", "asc", "--page", "3", "--page-size", "50", "--search", "tomcat", "--namespace", "shop")
	if executeError != nil {
		t.Fatalf("security findings with flags failed: %v", executeError)
	}
	options = mock.findingsOptions
	if options.KnownExploited == nil || !*options.KnownExploited || options.Fixable == nil || *options.Fixable ||
		options.Statuses != nil || strings.Join(options.Severities, ",") != "critical,high" ||
		options.Sort != "epss" || options.Order != "asc" || options.Page != 3 || options.PageSize != 50 ||
		options.Search != "tomcat" || options.Namespace != "shop" {
		t.Fatalf("mapped options = %+v", options)
	}
}

func TestSecurityFindingsRejectsAnUnknownFixableValue(t *testing.T) {
	mock := &securityMock{findings: &client.SecurityFindingList{}}
	_, executeError := runSecurityCommand(t, mock, "security", "findings", "--fixable", "maybe")
	if executeError == nil || !strings.Contains(executeError.Error(), "--fixable must be true, false or any") {
		t.Fatalf("expected a usage error, got %v", executeError)
	}
	if exitCodeFor(executeError) != exitUsage {
		t.Fatalf("expected exit code %d, got %d", exitUsage, exitCodeFor(executeError))
	}
}

func TestSecurityFindingsStructuredOutputIsTheApiDocument(t *testing.T) {
	mock := &securityMock{findings: &client.SecurityFindingList{
		Result:     []client.SecurityFinding{exploitedFinding()},
		Pagination: client.SecurityPagination{Page: 1, PageSize: 25, TotalPages: 1, TotalCount: 1},
	}}
	output, executeError := runSecurityCommand(t, mock, "security", "findings", "-o", "json")
	if executeError != nil {
		t.Fatalf("security findings -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal([]byte(output), &decoded); unmarshalError != nil {
		t.Fatalf("output is not JSON: %v\n%s", unmarshalError, output)
	}
	first := decoded["result"].([]any)[0].(map[string]any)
	if first["known_exploited"] != true || first["kev_due_date"] != "2025-04-22" ||
		first["kev_required_action"] != "Apply mitigations per vendor instructions." || first["epss_score"] != 0.997 {
		t.Fatalf("structured row lacks the exploit intelligence: %+v", first)
	}
}

func TestSecurityOverviewNeverReadsAnUnsyncedCatalogAsClean(t *testing.T) {
	synced := &client.SecurityOverview{
		Totals:                   client.SecurityTotals{Observed: 10, Actionable: 8},
		KnownExploited:           91,
		KnownExploitedFindings:   22,
		KnownExploitedOverdue:    21,
		KnownExploitedRansomware: 0,
		Intelligence:             client.SecurityIntelligenceStatus{KevSyncedAt: stringPointer("2026-08-28T20:13:11Z"), KevListed: 1685},
		Coverage:                 client.SecurityCoverage{TotalClusters: 27, FreshClusters: 11, StaleClusters: 1, UnscannedClusters: 15},
		Scanner:                  client.SecurityScanner{Status: "degraded"},
	}
	output, executeError := runSecurityCommand(t, &securityMock{overview: synced}, "security", "overview")
	if executeError != nil {
		t.Fatalf("security overview failed: %v", executeError)
	}
	for _, expected := range []string{"22 vulnerabilities in this fleet are being exploited in the wild",
		"21 past CISA's remediation deadline", "11 fresh · 1 stale · 15 unscanned of 27 clusters",
		"ankra security findings --known-exploited"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}

	unsynced := &client.SecurityOverview{Totals: client.SecurityTotals{Actionable: 8}}
	output, executeError = runSecurityCommand(t, &securityMock{overview: unsynced}, "security", "overview")
	if executeError != nil {
		t.Fatalf("security overview (unsynced) failed: %v", executeError)
	}
	if !strings.Contains(output, "the CISA catalog has not been synced yet") || strings.Contains(output, "none of the actionable findings") {
		t.Fatalf("an unsynced catalog must read as unknown, not clean:\n%s", output)
	}
}

func TestSecurityFindingQuotesCisaGuidance(t *testing.T) {
	detail := &client.SecurityFindingDetail{
		Finding: exploitedFinding(),
		Occurrences: []client.SecurityOccurrence{{
			ID: "o1", ClusterID: "c1", ClusterName: "prod", ReportScope: "namespaced", ReportName: "replicaset-carts",
			WorkloadKind: stringPointer("Deployment"), WorkloadNamespace: stringPointer("shop"), WorkloadName: stringPointer("carts"),
			ContainerName: stringPointer("carts"), ImageRef: stringPointer("weaveworksdemos/carts:0.4.8"),
			InstalledVersion: stringPointer("8.5.11"), FixedVersion: stringPointer("9.0.99"),
			ScanState: "active", EffectiveDisposition: "none",
		}},
	}
	output, executeError := runSecurityCommand(t, &securityMock{detail: detail}, "security", "finding", "632d93d6-f07b-4618-ad37-ae453409e928")
	if executeError != nil {
		t.Fatalf("security finding failed: %v", executeError)
	}
	for _, expected := range []string{"Known exploited in the wild (CISA KEV)",
		"Apache Tomcat Path Equivalence Vulnerability (Apache Tomcat)", "listed since 2025-04-01",
		"deadline passed 2025-04-22", "used in ransomware campaigns",
		"CISA required action: Apply mitigations per vendor instructions.", "weaveworksdemos/carts:0.4.8", "9.0.99"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

func TestSecurityClustersFlagsTheKnownExploitedCount(t *testing.T) {
	clusters := &client.SecurityClusterList{
		Result: []client.SecurityClusterPosture{{
			ClusterID: "c1", ClusterName: "prod", Environment: stringPointer("production"),
			ScannerStatus: "fresh", PostureStatus: "critical", Actionable: 8, KnownExploited: 4, FixableSevere: 3,
			Severity: client.SecuritySeverityCounts{Critical: 1, High: 2},
		}},
		Pagination: client.SecurityPagination{Page: 1, PageSize: 50, TotalPages: 1, TotalCount: 1},
	}
	output, executeError := runSecurityCommand(t, &securityMock{clusters: clusters}, "security", "clusters")
	if executeError != nil {
		t.Fatalf("security clusters failed: %v", executeError)
	}
	for _, expected := range []string{"prod", "critical", "KNOWN EXPLOITED", "Page 1 of 1 · 1 clusters"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
}

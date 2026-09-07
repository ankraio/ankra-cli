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
	advisory        *client.SecurityAdvisory
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

func (m *securityMock) GetSecurityAdvisory(string) (*client.SecurityAdvisory, error) {
	return m.advisory, nil
}

func (m *securityMock) ListSecurityClusters(client.SecurityClustersOptions) (*client.SecurityClusterList, error) {
	return m.clusters, nil
}

func securityCommandTree() []*cobra.Command {
	return []*cobra.Command{
		securityOverviewCmd, securityFindingsCmd, securityFindingCmd, securityAdvisoryCmd, securityClustersCmd,
		securityNamespacesCmd, securityPodsCmd, securitySbomCmd, securitySbomImagesCmd, securitySbomImageCmd,
		securityStacksCmd, securityStackCmd, securityPodCmd,
	}
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

func TestSecurityAdvisoryRendersTheParsedRecordAndTheThreeStates(t *testing.T) {
	title := "Glibc: buffer overflow in ld.so leading to privilege escalation"
	description := "A buffer overflow was discovered in the GNU C Library's dynamic loader ld.so."
	score := 7.8
	severity := "HIGH"
	vector := "CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H"
	version := "3.1"
	readAt := "2026-08-29T10:28:21Z"
	finding := exploitedFinding()
	finding.CVEID = "CVE-2023-4911"
	finding.PackageName = "libc6"
	fetched := &client.SecurityAdvisory{
		CVEID:  "CVE-2023-4911",
		Status: "fetched",
		Sources: client.SecurityAdvisorySources{
			NVDFetchedAt: &readAt, OSVFetchedAt: &readAt,
			NVDURL: "https://nvd.nist.gov/vuln/detail/CVE-2023-4911", OSVURL: "https://osv.dev/vulnerability/CVE-2023-4911",
		},
		Advisory: &client.SecurityAdvisoryRecord{
			Title: &title, Description: &description,
			CVSSScore: &score, CVSSSeverity: &severity, CVSSVector: &vector, CVSSVersion: &version,
			CWEIDs: []string{"CWE-122", "CWE-787"}, Aliases: []string{"GHSA-m77w-6vjw-wh2f"},
			References: []client.SecurityAdvisoryReference{{URL: "https://access.redhat.com/errata/RHSA-2023:5453", Source: "nvd", Tags: []string{"Patch"}}},
			Affected: []client.SecurityAdvisoryAffected{
				{Source: "nvd", Package: "glibc", Ranges: []client.SecurityAdvisoryVersionRange{{Introduced: "2.34", Fixed: "2.39", Status: "affected"}}},
				{Source: "nvd", Vendor: "Red Hat", Product: "Red Hat Enterprise Linux 8", Package: "glibc",
					Ranges: []client.SecurityAdvisoryVersionRange{{Introduced: "0:2.28-225.el8_8.6", Status: "unaffected"}}},
				{Source: "nvd", Vendor: "Siemens", Product: "SIMATIC S7-1500", Ranges: []client.SecurityAdvisoryVersionRange{{Introduced: "V3.1.5", Status: "affected"}}},
			},
			SSVC: &client.SecurityAdvisorySSVC{Exploitation: "active", Automatable: "no", TechnicalImpact: "total"},
		},
		Intelligence:       finding.SecurityExploitIntelligence,
		IntelligenceStatus: client.SecurityIntelligenceStatus{KevSyncedAt: &readAt, EPSSSyncedAt: &readAt, KevListed: 1685},
		Fleet: client.SecurityAdvisoryFleet{
			Findings: []client.SecurityFinding{finding}, Occurrences: 48, FixableOccurrences: 9, AffectedClusters: 3, AffectedWorkloads: 7,
		},
	}
	output, executeError := runSecurityCommand(t, &securityMock{advisory: fetched}, "security", "advisory", "cve-2023-4911")
	if executeError != nil {
		t.Fatalf("security advisory failed: %v", executeError)
	}
	for _, expected := range []string{
		"CVE-2023-4911", "CVSS 7.8 (3.1)", title, description,
		"Known exploited in the wild (CISA KEV)", "CISA required action: Apply mitigations per vendor instructions.",
		"CISA SSVC: exploitation active · automatable no · technical impact total",
		"Weaknesses: CWE-122, CWE-787", "Also known as: GHSA-m77w-6vjw-wh2f",
		"glibc: 2.34 before 2.39 (fixed in 2.39)",
		"glibc · Red Hat Enterprise Linux 8: fixed from 0:2.28-225.el8_8.6",
		"Siemens SIMATIC S7-1500: V3.1.5 and later",
		"In your fleet: 1 findings · 48 occurrences (9 with a fix) · 3 clusters · 7 workloads",
		"libc6", "[Patch] https://access.redhat.com/errata/RHSA-2023:5453",
		"https://nvd.nist.gov/vuln/detail/CVE-2023-4911",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}

	pending := &client.SecurityAdvisory{CVEID: "CVE-2077-0001", Status: "pending", RequestedAt: &readAt,
		Sources:            client.SecurityAdvisorySources{NVDURL: "https://nvd.nist.gov/vuln/detail/CVE-2077-0001", OSVURL: "https://osv.dev/vulnerability/CVE-2077-0001"},
		IntelligenceStatus: client.SecurityIntelligenceStatus{KevSyncedAt: &readAt}}
	output, executeError = runSecurityCommand(t, &securityMock{advisory: pending}, "security", "advisory", "CVE-2077-0001")
	if executeError != nil || !strings.Contains(output, "fetching this advisory from NVD and OSV now") || !strings.Contains(output, "no current findings") {
		t.Fatalf("a pending advisory must say it is being fetched, got %v:\n%s", executeError, output)
	}
	missing := &client.SecurityAdvisory{CVEID: "CVE-2077-0002", Status: "missing",
		Sources: client.SecurityAdvisorySources{NVDURL: "https://nvd.nist.gov/vuln/detail/CVE-2077-0002", OSVURL: "https://osv.dev/vulnerability/CVE-2077-0002"}}
	output, executeError = runSecurityCommand(t, &securityMock{advisory: missing}, "security", "advisory", "CVE-2077-0002")
	if executeError != nil || !strings.Contains(output, "No public advisory record for this CVE yet") || !strings.Contains(output, "CISA KEV status unknown") {
		t.Fatalf("a missing advisory must say so and keep the KEV state honest, got %v:\n%s", executeError, output)
	}
}

func TestSecurityAdvisoryStructuredOutputIsTheApiDocument(t *testing.T) {
	advisory := &client.SecurityAdvisory{CVEID: "CVE-2023-4911", Status: "fetched",
		Fleet: client.SecurityAdvisoryFleet{Findings: []client.SecurityFinding{}}}
	output, executeError := runSecurityCommand(t, &securityMock{advisory: advisory}, "security", "advisory", "CVE-2023-4911", "-o", "json")
	if executeError != nil {
		t.Fatalf("security advisory -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	if decoded["cve_id"] != "CVE-2023-4911" || decoded["status"] != "fetched" {
		t.Fatalf("structured output must be the API document, got %v", decoded)
	}
}

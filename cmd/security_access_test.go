package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type securityAccessMock struct {
	baseMock
	clusters      *client.SecurityClusterList
	posture       *client.SecurityClusterAccessPosture
	postureError  error
	postureTarget string
}

func (m *securityAccessMock) ListSecurityClusters(client.SecurityClustersOptions) (*client.SecurityClusterList, error) {
	return m.clusters, nil
}

func (m *securityAccessMock) GetSecurityClusterAccessPosture(clusterID string) (*client.SecurityClusterAccessPosture, error) {
	m.postureTarget = clusterID
	return m.posture, m.postureError
}

func accessPostureClusters() *client.SecurityClusterList {
	return &client.SecurityClusterList{
		Result: []client.SecurityClusterPosture{
			{ClusterID: "c1", ClusterName: "prod", ScannerStatus: "fresh", PostureStatus: "clean",
				AccessPosture: &client.SecurityClusterAccessSummary{GrantsTotal: intPointer(3), StandingElevated: 1,
					OverPolicy: intPointer(2), ImpersonateReachable: intPointer(0), ImpersonateVerifiedGrants: 3}},
			{ClusterID: "c2", ClusterName: "staging", ScannerStatus: "fresh", PostureStatus: "clean",
				AccessPosture: &client.SecurityClusterAccessSummary{GrantsTotal: intPointer(3), StandingElevated: 1,
					OverPolicy: nil, ImpersonateReachable: nil, ImpersonateVerifiedGrants: 1}},
			{ClusterID: "c3", ClusterName: "legacy", ScannerStatus: "fresh", PostureStatus: "clean"},
			{ClusterID: "c4", ClusterName: "empty", ScannerStatus: "fresh", PostureStatus: "clean",
				AccessPosture: &client.SecurityClusterAccessSummary{GrantsTotal: intPointer(0), OverPolicy: intPointer(0), ImpersonateReachable: intPointer(0)}},
			{ClusterID: "c5", ClusterName: "fresh-policy-down", ScannerStatus: "fresh", PostureStatus: "clean",
				AccessPosture: &client.SecurityClusterAccessSummary{GrantsTotal: intPointer(0), OverPolicy: nil, ImpersonateReachable: nil}},
			{ClusterID: "c6", ClusterName: "no-total", ScannerStatus: "fresh", PostureStatus: "clean",
				AccessPosture: &client.SecurityClusterAccessSummary{StandingElevated: 0, OverPolicy: intPointer(0), ImpersonateReachable: intPointer(0)}},
		},
		Pagination: client.SecurityPagination{Page: 1, PageSize: 50, TotalPages: 1, TotalCount: 6},
	}
}

func TestSecurityClustersAccessColumnNeverReadsUnknownOrAbsentAsZero(t *testing.T) {
	output, executeError := runSecurityCommand(t, &securityAccessMock{clusters: accessPostureClusters()}, "security", "clusters")
	if executeError != nil {
		t.Fatalf("security clusters failed: %v", executeError)
	}
	for _, expected := range []string{
		"ACCESS",
		"3 grants, 1 standing elevated, 2 over policy, 0 can impersonate",
		"3 grants, 1 standing elevated, over policy unknown, impersonate unknown (1 of 3 verified)",
		"not reported",
		"no grants",
		"0 grants, 0 standing elevated, over policy unknown, impersonate unknown (0 of 0 verified)",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "legacy") && strings.Contains(line, "0 grants") {
			t.Fatalf("a cluster the platform reported no access posture for must not read as zero grants:\n%s", line)
		}
		if strings.Contains(line, "fresh-policy-down") && strings.Contains(line, "no grants") {
			t.Fatalf("zero grants with unknown counts must show the unknowns, not \"no grants\":\n%s", line)
		}
		if strings.Contains(line, "no-total") && !strings.Contains(line, "not reported") {
			t.Fatalf("an omitted grants_total must read as not reported, never as zero grants:\n%s", line)
		}
		if strings.Contains(line, "staging") && (strings.Contains(line, "0 over policy") || strings.Contains(line, "can impersonate")) {
			t.Fatalf("a null count must render as unknown, never a number:\n%s", line)
		}
	}
}

func TestSecurityClustersStructuredOutputKeepsNullAndAbsentAccessPosture(t *testing.T) {
	output, executeError := runSecurityCommand(t, &securityAccessMock{clusters: accessPostureClusters()}, "security", "clusters", "-o", "json")
	if executeError != nil {
		t.Fatalf("security clusters -o json failed: %v", executeError)
	}
	var decoded struct {
		Result []map[string]any `json:"result"`
	}
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	staging, isObject := decoded.Result[1]["access_posture"].(map[string]any)
	if !isObject {
		t.Fatalf("staging lost its access_posture: %+v", decoded.Result[1])
	}
	for _, key := range []string{"over_policy", "impersonate_reachable"} {
		if value, present := staging[key]; !present || value != nil {
			t.Fatalf("%s = %v (present %v), want null passed through", key, value, present)
		}
	}
	if _, present := decoded.Result[2]["access_posture"]; present {
		t.Fatalf("a platform that omitted access_posture must stay omitted, got %+v", decoded.Result[2])
	}
}

func accessPostureWithFindings() *client.SecurityClusterAccessPosture {
	return &client.SecurityClusterAccessPosture{
		ClusterID:   securityTestClusterID,
		EvaluatedAt: "2026-10-10T08:00:00Z",
		Summary: &client.SecurityClusterAccessSummary{GrantsTotal: intPointer(3), StandingElevated: 1, OverPolicy: intPointer(2),
			ImpersonateReachable: nil, ImpersonateVerifiedGrants: 1},
		Findings: []client.SecurityClusterAccessFinding{
			{Check: "grant_over_policy", Severity: "HIGH", Title: "Grant exceeds the access policy",
				GrantID: "11111111-1111-1111-1111-111111111111", AnkraUserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
				UserEmail: nil, Role: "edit", Scope: "namespace", Namespace: stringPointer("payments"),
				Detail:      "edit is above the policy ceiling view.",
				Remediation: "ankra cluster access revoke 11111111-1111-1111-1111-111111111111 --cluster prod"},
			{Check: "impersonate_reachable", Severity: "CRITICAL", Title: "Grant identity can impersonate",
				GrantID: "22222222-2222-2222-2222-222222222222", AnkraUserID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
				UserEmail: stringPointer("alice@example.com"), Role: "view", Scope: "cluster",
				Detail:      "The apiserver allowed impersonate on users.",
				Remediation: "ankra cluster access revoke 22222222-2222-2222-2222-222222222222 --cluster prod"},
		},
		Unknowns: []client.SecurityClusterAccessUnknown{
			{Check: "impersonate_reachable", GrantID: stringPointer("33333333-3333-3333-3333-333333333333"), Reason: "not probed yet"},
			{Check: "grant_over_policy", GrantID: nil, Reason: "the access policy could not be read"},
		},
	}
}

func TestSecurityAccessPrintsFindingsBySeverityWithGranteeAndFix(t *testing.T) {
	mock := &securityAccessMock{posture: accessPostureWithFindings()}
	output, executeError := runSecurityCommand(t, mock, "security", "access", "--cluster", securityTestClusterID)
	if executeError != nil {
		t.Fatalf("security access failed: %v", executeError)
	}
	if mock.postureTarget != securityTestClusterID {
		t.Fatalf("read cluster %q, want %q", mock.postureTarget, securityTestClusterID)
	}
	for _, expected := range []string{
		"3 grants, 1 standing elevated, 2 over policy, impersonate unknown (1 of 3 verified)",
		"Findings (2):",
		"grant 22222222-2222-2222-2222-222222222222 · alice@example.com · view, cluster-wide",
		"grant 11111111-1111-1111-1111-111111111111 · user aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa · edit, namespace payments",
		"Fix: ankra cluster access revoke 11111111-1111-1111-1111-111111111111 --cluster prod",
		"edit is above the policy ceiling view.",
		"Not evaluated (2), unknown rather than passed:",
		"impersonate_reachable · grant 33333333-3333-3333-3333-333333333333: not probed yet",
		"grant_over_policy · every grant: the access policy could not be read",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output lacks %q:\n%s", expected, output)
		}
	}
	critical := strings.Index(output, "Grant identity can impersonate")
	high := strings.Index(output, "Grant exceeds the access policy")
	if critical < 0 || high < 0 || critical > high {
		t.Fatalf("findings must print most severe first:\n%s", output)
	}
}

func TestSecurityAccessWithOnlyUnknownsIsNotReadAsClean(t *testing.T) {
	posture := accessPostureWithFindings()
	posture.Findings = []client.SecurityClusterAccessFinding{}
	output, executeError := runSecurityCommand(t, &securityAccessMock{posture: posture}, "security", "access", "--cluster", securityTestClusterID)
	if executeError != nil {
		t.Fatalf("security access failed: %v", executeError)
	}
	if strings.Contains(output, "Every access check passed") || !strings.Contains(output, "not every check was evaluated") {
		t.Fatalf("unknowns must not read as a clean cluster:\n%s", output)
	}

	posture.Unknowns = []client.SecurityClusterAccessUnknown{}
	output, executeError = runSecurityCommand(t, &securityAccessMock{posture: posture}, "security", "access", "--cluster", securityTestClusterID)
	if executeError != nil || !strings.Contains(output, "Every access check passed on every grant.") {
		t.Fatalf("a clean cluster must say so, got %v:\n%s", executeError, output)
	}
}

func TestSecurityAccessStructuredOutputIsTheApiDocument(t *testing.T) {
	output, executeError := runSecurityCommand(t, &securityAccessMock{posture: accessPostureWithFindings()},
		"security", "access", "--cluster", securityTestClusterID, "-o", "json")
	if executeError != nil {
		t.Fatalf("security access -o json failed: %v", executeError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	summary := decoded["summary"].(map[string]any)
	if value, present := summary["impersonate_reachable"]; !present || value != nil {
		t.Fatalf("impersonate_reachable = %v (present %v), want null", value, present)
	}
	finding := decoded["findings"].([]any)[0].(map[string]any)
	if value, present := finding["user_email"]; !present || value != nil || finding["ankra_user_id"] == "" {
		t.Fatalf("a redacted finding must keep user_email null and the user id, got %+v", finding)
	}
	if len(decoded["unknowns"].([]any)) != 2 {
		t.Fatalf("unknowns not passed through: %+v", decoded["unknowns"])
	}
}

func TestSecurityAccessNotFoundNamesBothReadings(t *testing.T) {
	mock := &securityAccessMock{postureError: client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found")}
	_, executeError := runSecurityCommand(t, mock, "security", "access", "--cluster", securityTestClusterID)
	if executeError == nil || exitCodeFor(executeError) != exitNotFound || !strings.Contains(executeError.Error(), "predates the access posture view") {
		t.Fatalf("a 404 must exit not-found and name an older platform, got %v", executeError)
	}
}

func TestSecurityAccessRequiresCluster(t *testing.T) {
	_, executeError := runSecurityCommand(t, &securityAccessMock{}, "security", "access")
	if executeError == nil || exitCodeFor(executeError) != exitUsage {
		t.Fatalf("expected a usage error without --cluster, got %v", executeError)
	}
}

func TestSecurityAccessNullSummaryIsNotReportedAndExitsNonZero(t *testing.T) {
	posture := accessPostureWithFindings()
	posture.Summary = nil
	posture.Findings = []client.SecurityClusterAccessFinding{}
	posture.Unknowns = []client.SecurityClusterAccessUnknown{}
	output, executeError := runSecurityCommand(t, &securityAccessMock{posture: posture}, "security", "access", "--cluster", securityTestClusterID)
	if executeError == nil || exitCodeFor(executeError) != exitError || !strings.Contains(executeError.Error(), "summary not reported") {
		t.Fatalf("a null summary must exit 1 naming the summary, got %v", executeError)
	}
	if !strings.Contains(output, "the platform did not report access posture") ||
		strings.Contains(output, "no grants") || strings.Contains(output, "Every access check passed") {
		t.Fatalf("a null summary must never read as no grants or passed:\n%s", output)
	}
}

func TestSecurityAccessNullListsAreNotReportedNeverPassed(t *testing.T) {
	posture := accessPostureWithFindings()
	posture.Findings = []client.SecurityClusterAccessFinding{}
	posture.Unknowns = nil
	output, executeError := runSecurityCommand(t, &securityAccessMock{posture: posture}, "security", "access", "--cluster", securityTestClusterID)
	if executeError == nil || exitCodeFor(executeError) != exitError || !strings.Contains(executeError.Error(), "unknowns not reported") {
		t.Fatalf("a null unknowns list must exit 1 naming it, got %v", executeError)
	}
	if strings.Contains(output, "Every access check passed") || !strings.Contains(output, "Not evaluated: not reported by the platform") {
		t.Fatalf("a null unknowns list must read as not reported, never as passed:\n%s", output)
	}

	posture = accessPostureWithFindings()
	posture.Findings = nil
	output, executeError = runSecurityCommand(t, &securityAccessMock{posture: posture}, "security", "access", "--cluster", securityTestClusterID)
	if executeError == nil || exitCodeFor(executeError) != exitError || !strings.Contains(executeError.Error(), "findings not reported") {
		t.Fatalf("a null findings list must exit 1 naming it, got %v", executeError)
	}
	if !strings.Contains(output, "Findings: not reported by the platform.") || !strings.Contains(output, "not probed yet") {
		t.Fatalf("a null findings list must read as not reported and still print the unknowns:\n%s", output)
	}
}

func TestSecurityAccessStructuredOutputPassesNullsThrough(t *testing.T) {
	posture := &client.SecurityClusterAccessPosture{ClusterID: securityTestClusterID, EvaluatedAt: "2026-10-10T08:00:00Z"}
	output, executeError := runSecurityCommand(t, &securityAccessMock{posture: posture},
		"security", "access", "--cluster", securityTestClusterID, "-o", "json")
	if executeError != nil {
		t.Fatalf("-o json must pass the document through and exit 0, got %v", executeError)
	}
	var decoded map[string]any
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	for _, key := range []string{"summary", "findings", "unknowns"} {
		if value, present := decoded[key]; !present || value != nil {
			t.Fatalf("%s = %v (present %v), want null passed through:\n%s", key, value, present, output)
		}
	}

	posture.Summary = &client.SecurityClusterAccessSummary{}
	output, executeError = runSecurityCommand(t, &securityAccessMock{posture: posture},
		"security", "access", "--cluster", securityTestClusterID, "-o", "json")
	if executeError != nil {
		t.Fatalf("-o json failed: %v", executeError)
	}
	decoded = map[string]any{}
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	summary := decoded["summary"].(map[string]any)
	if value, present := summary["grants_total"]; !present || value != nil {
		t.Fatalf("grants_total = %v (present %v), want null passed through", value, present)
	}
}

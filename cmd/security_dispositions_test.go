package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

const securityTestClusterID = "0b8f2f5e-3c1a-4d2b-9e7f-1a2b3c4d5e6f"

type securityDispositionsMock struct {
	baseMock
	detail             *client.SecurityFindingDetail
	occurrences        *client.SecurityOccurrenceList
	occurrencesOptions *client.SecurityOccurrencesOptions
	workloads          *client.SecurityWorkloadList
	workloadsOptions   *client.SecurityFindingsOptions
	findings           *client.SecurityFindingList
	dispositions       *client.SecurityDispositionList
	listOptions        *client.SecurityDispositionsOptions
	preview            *client.SecurityDispositionPreview
	previewRequest     *client.SecurityDispositionPreviewRequest
	mutation           *client.SecurityDispositionMutation
	createRequest      *client.SecurityDispositionCreateRequest
	updateRequest      *client.SecurityDispositionUpdateRequest
	revokeRequest      *client.SecurityDispositionRevokeRequest
	revokedPolicyID    string
}

func (m *securityDispositionsMock) GetSecurityFinding(string) (*client.SecurityFindingDetail, error) {
	return m.detail, nil
}

func (m *securityDispositionsMock) ListSecurityFindings(client.SecurityFindingsOptions) (*client.SecurityFindingList, error) {
	return m.findings, nil
}

func (m *securityDispositionsMock) ListSecurityFindingOccurrences(options client.SecurityOccurrencesOptions) (*client.SecurityOccurrenceList, error) {
	m.occurrencesOptions = &options
	return m.occurrences, nil
}

func (m *securityDispositionsMock) ListSecurityWorkloads(options client.SecurityFindingsOptions) (*client.SecurityWorkloadList, error) {
	m.workloadsOptions = &options
	return m.workloads, nil
}

func (m *securityDispositionsMock) ListSecurityDispositions(options client.SecurityDispositionsOptions) (*client.SecurityDispositionList, error) {
	m.listOptions = &options
	return m.dispositions, nil
}

func (m *securityDispositionsMock) PreviewSecurityDisposition(request client.SecurityDispositionPreviewRequest) (*client.SecurityDispositionPreview, error) {
	m.previewRequest = &request
	return m.preview, nil
}

func (m *securityDispositionsMock) CreateSecurityDisposition(request client.SecurityDispositionCreateRequest) (*client.SecurityDispositionMutation, error) {
	m.createRequest = &request
	return m.mutation, nil
}

func (m *securityDispositionsMock) UpdateSecurityDisposition(_ string, request client.SecurityDispositionUpdateRequest) (*client.SecurityDispositionMutation, error) {
	m.updateRequest = &request
	return m.mutation, nil
}

func (m *securityDispositionsMock) RevokeSecurityDisposition(policyID string, request client.SecurityDispositionRevokeRequest) (*client.SecurityDispositionMutation, error) {
	m.revokedPolicyID = policyID
	m.revokeRequest = &request
	return m.mutation, nil
}

// runSecurityCommandWithInput is runSecurityCommand with a stdin, for the
// commands that confirm before writing.
func runSecurityCommandWithInput(t *testing.T, mock APIClient, input string, args ...string) (string, string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(args)
	t.Cleanup(func() { resetTreeFlags(t, securityCommandTree()...) })
	executeError := rootCmd.Execute()
	return stdout.String(), stderr.String(), executeError
}

func sampleDisposition() client.SecurityDisposition {
	return client.SecurityDisposition{
		ID:                     "8e2a4b6c-1d3f-4a5b-8c7d-9e0f1a2b3c4d",
		Selector:               client.SecurityDispositionSelector{OrganisationID: "org", AddonSlug: "ingress-nginx", CVEID: "CVE-2025-24813", PackageType: "maven", PackageName: "tomcat-embed-core"},
		Disposition:            "accepted_risk",
		Reason:                 "not reachable behind the gateway",
		ExpiresAt:              stringPointer("2020-01-31T23:59:59Z"),
		Status:                 "expired",
		MatchHealth:            "healthy",
		MatchedOccurrenceCount: 4,
		ActiveMatchCount:       3,
		FixAvailableMatchCount: 1,
		CreatedAt:              "2026-08-01T00:00:00Z",
		UpdatedAt:              "2026-08-20T00:00:00Z",
	}
}

func sampleDispositionPreview() *client.SecurityDispositionPreview {
	return &client.SecurityDispositionPreview{
		Selector:            client.SecurityDispositionSelector{OrganisationID: "org", AddonSlug: "ingress-nginx", CVEID: "CVE-2025-24813", PackageType: "maven", PackageName: "tomcat-embed-core"},
		AffectedOccurrences: 4,
		AffectedFindings:    1,
		AffectedClusters:    2,
		AffectedAddons:      1,
		Exclusions:          client.SecurityDispositionPreviewExclusions{Ambiguous: 1},
		Delta:               client.SecurityActionableDelta{ObservedBefore: 10, ObservedAfter: 10, ActionableBefore: 9, ActionableAfter: 5},
		Overlaps:            []client.SecurityDispositionOverlap{},
	}
}

func TestSecurityWorkloads_RendersRiskOrderedTable(t *testing.T) {
	mock := &securityDispositionsMock{workloads: &client.SecurityWorkloadList{
		Result: []client.SecurityWorkload{{
			ClusterName:       "production",
			WorkloadKind:      stringPointer("Deployment"),
			WorkloadNamespace: stringPointer("payments"),
			WorkloadName:      stringPointer("api"),
			ImageRef:          stringPointer("ghcr.io/acme/api:1.2.3"),
			AddonSlug:         stringPointer("acme-api"),
			AddonAttribution:  client.SecurityAttributionSummary{Status: "matched", Matched: 1},
			Actionable:        client.SecuritySeverityCounts{Critical: 2, High: 3},
			AcceptedRisk:      client.SecuritySeverityCounts{Medium: 1},
			KnownExploited:    1,
			Priority:          "critical",
			RiskScore:         91,
			LastScan:          "2026-09-06T00:00:00Z",
		}},
		Pagination: client.SecurityPagination{Page: 1, TotalPages: 1, TotalCount: 1},
		Scanner:    client.SecurityScanner{Status: "stale", StaleClusters: 1},
	}}
	output, err := runSecurityCommand(t, mock, "security", "workloads", "--known-exploited")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{"payments/Deployment api", "acme-api", "2C", "3H", "91", "1 workloads", "sorted by risk_score desc", "Scanner coverage is stale"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
	if mock.workloadsOptions == nil || mock.workloadsOptions.Sort != "risk_score" || mock.workloadsOptions.KnownExploited == nil {
		t.Fatalf("expected the risk_score default sort and the KEV filter, got %+v", mock.workloadsOptions)
	}
}

func TestSecurityFinding_StatusFlagListsResolvedOccurrences(t *testing.T) {
	mock := &securityDispositionsMock{
		detail: &client.SecurityFindingDetail{Finding: exploitedFinding()},
		occurrences: &client.SecurityOccurrenceList{
			Result: []client.SecurityOccurrence{{
				ID: "occ-1", ClusterName: "staging", WorkloadKind: stringPointer("Deployment"),
				WorkloadNamespace: stringPointer("web"), WorkloadName: stringPointer("frontend"),
				ScanState: "resolved", EffectiveDisposition: "none", LastSeenAt: "2026-09-01T00:00:00Z",
			}},
			Pagination: client.SecurityPagination{Page: 1, TotalPages: 1, TotalCount: 1},
		},
	}
	output, err := runSecurityCommand(t, mock, "security", "finding", "f-1", "--status", "resolved", "--cluster", securityTestClusterID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.occurrencesOptions == nil || mock.occurrencesOptions.Status != "resolved" || mock.occurrencesOptions.ClusterID != securityTestClusterID {
		t.Fatalf("expected the occurrence read scoped to resolved + cluster, got %+v", mock.occurrencesOptions)
	}
	for _, fragment := range []string{"web/Deployment frontend", "resolved", "1 occurrences", "resolved only", "occ-1"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
}

func TestSecurityFinding_RefusesUnknownStatus(t *testing.T) {
	mock := &securityDispositionsMock{detail: &client.SecurityFindingDetail{Finding: exploitedFinding()}}
	_, err := runSecurityCommand(t, mock, "security", "finding", "f-1", "--status", "later")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

func TestSecurityFinding_StructuredOccurrenceDocument(t *testing.T) {
	mock := &securityDispositionsMock{
		detail:      &client.SecurityFindingDetail{Finding: exploitedFinding()},
		occurrences: &client.SecurityOccurrenceList{Result: []client.SecurityOccurrence{}, Pagination: client.SecurityPagination{Page: 2, TotalPages: 2}},
	}
	output, err := runSecurityCommand(t, mock, "security", "finding", "f-1", "--page", "2", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var document map[string]json.RawMessage
	if parseError := json.Unmarshal([]byte(output), &document); parseError != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%s", parseError, output)
	}
	for _, key := range []string{"finding", "occurrences", "pagination"} {
		if _, present := document[key]; !present {
			t.Errorf("expected %q in the document keys %v", key, document)
		}
	}
}

func TestSecurityFindings_PrintsBothIntelligenceCaveats(t *testing.T) {
	mock := &securityDispositionsMock{findings: &client.SecurityFindingList{
		Result:     []client.SecurityFinding{exploitedFinding()},
		Pagination: client.SecurityPagination{Page: 1, TotalPages: 1, TotalCount: 1},
	}}
	output, err := runSecurityCommand(t, mock, "security", "findings")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "CISA KEV catalog has not been synced") || !strings.Contains(output, "EPSS has not been synced") {
		t.Errorf("expected both the KEV and the EPSS caveat:\n%s", output)
	}
}

func TestSecurityDispositions_ListsPoliciesAndReviewCount(t *testing.T) {
	mock := &securityDispositionsMock{dispositions: &client.SecurityDispositionList{
		Result:     []client.SecurityDisposition{sampleDisposition()},
		Pagination: client.SecurityPagination{Page: 1, TotalPages: 1, TotalCount: 1},
	}}
	output, err := runSecurityCommand(t, mock, "security", "dispositions", "--status", "expired", "--disposition", "accepted_risk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fragment := range []string{"accepted_risk", "CVE-2025-24813", "ingress-nginx", "3 of 4", "passed 2020-01-31", "1 on this page need review"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("expected %q in output:\n%s", fragment, output)
		}
	}
	if mock.listOptions == nil || len(mock.listOptions.Statuses) != 1 || mock.listOptions.Dispositions[0] != "accepted_risk" {
		t.Fatalf("expected the filters forwarded, got %+v", mock.listOptions)
	}
}

func TestSecurityDispositionsPreview_RequiresExactlyOneAnchor(t *testing.T) {
	mock := &securityDispositionsMock{preview: sampleDispositionPreview()}
	_, err := runSecurityCommand(t, mock, "security", "dispositions", "preview", "--disposition", "acknowledged")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error without an anchor, got %v", err)
	}
}

func TestSecurityDispositionsPreview_RendersBlastRadius(t *testing.T) {
	mock := &securityDispositionsMock{preview: sampleDispositionPreview()}
	output, err := runSecurityCommand(t, mock, "security", "dispositions", "preview", "--occurrence", "occ-1", "--disposition", "ack", "--expires-at", "2026-12-31")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "Would cover 4 occurrences across 1 findings, 2 clusters") || !strings.Contains(output, "Actionable: 9 -> 5") {
		t.Errorf("expected the blast radius:\n%s", output)
	}
	request := mock.previewRequest
	if request == nil || request.Disposition != "acknowledged" || request.ExpiresAt == nil || request.ExpiresAt.Format("2006-01-02T15:04:05Z") != "2026-12-31T23:59:59Z" {
		t.Fatalf("expected the normalised disposition and an end-of-day deadline, got %+v", request)
	}
}

func TestSecurityDispositionsCreate_DeclineWritesNothing(t *testing.T) {
	mock := &securityDispositionsMock{preview: sampleDispositionPreview()}
	_, _, err := runSecurityCommandWithInput(t, mock, "n\n", "security", "dispositions", "create", "--occurrence", "occ-1", "--disposition", "accepted_risk", "--reason", "x")
	if !errors.Is(err, errCancelled) || exitCodeFor(err) != exitCancelled {
		t.Fatalf("expected errCancelled, got %v", err)
	}
	if mock.createRequest != nil {
		t.Fatalf("expected no write after a decline, got %+v", mock.createRequest)
	}
}

func TestSecurityDispositionsCreate_YesWritesAndRenders(t *testing.T) {
	policy := sampleDisposition()
	policy.Status = "active"
	mock := &securityDispositionsMock{preview: sampleDispositionPreview(), mutation: &client.SecurityDispositionMutation{Policy: policy, Preview: *sampleDispositionPreview()}}
	output, err := runSecurityCommand(t, mock, "security", "dispositions", "create", "--occurrence", "occ-1", "--disposition", "accepted_risk",
		"--reason", "tracked in PLA-812", "--expire-when-fix-available", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	request := mock.createRequest
	if request == nil || request.OccurrenceID != "occ-1" || request.Disposition != "accepted_risk" || !request.ExpireWhenFixAvailable || request.Reason != "tracked in PLA-812" {
		t.Fatalf("expected the create request built from the flags, got %+v", request)
	}
	if !strings.Contains(output, "Would cover 4 occurrences") || !strings.Contains(output, "Recorded accepted_risk policy "+policy.ID) {
		t.Errorf("expected the preview and the recorded line:\n%s", output)
	}
}

func TestSecurityDispositionsCreate_StructuredOutputKeepsStdoutClean(t *testing.T) {
	mock := &securityDispositionsMock{preview: sampleDispositionPreview(), mutation: &client.SecurityDispositionMutation{Policy: sampleDisposition(), Preview: *sampleDispositionPreview()}}
	stdout, stderr, err := runSecurityCommandWithInput(t, mock, "", "security", "dispositions", "create", "--occurrence", "occ-1", "--disposition", "acknowledged", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var document map[string]any
	if parseError := json.Unmarshal([]byte(stdout), &document); parseError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", parseError, stdout)
	}
	if !strings.Contains(stderr, "Would cover") {
		t.Errorf("expected the preview narrated on stderr, got %q", stderr)
	}
}

func TestSecurityDispositionsUpdate_RefusesNoChange(t *testing.T) {
	mock := &securityDispositionsMock{}
	_, err := runSecurityCommand(t, mock, "security", "dispositions", "update", "policy-1", "--yes")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error with nothing to change, got %v", err)
	}
}

func TestSecurityDispositionsUpdate_SendsOnlyChangedMembers(t *testing.T) {
	mock := &securityDispositionsMock{mutation: &client.SecurityDispositionMutation{Policy: sampleDisposition(), Preview: *sampleDispositionPreview()}}
	output, err := runSecurityCommand(t, mock, "security", "dispositions", "update", "policy-1", "--reason", "new reason", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	request := mock.updateRequest
	if request == nil || request.Reason == nil || *request.Reason != "new reason" || request.ExpiresAt != nil || request.ExpireWhenFixAvailable != nil {
		t.Fatalf("expected only the reason in the request, got %+v", request)
	}
	if !strings.Contains(output, "Updated accepted_risk policy") {
		t.Errorf("expected the updated line:\n%s", output)
	}
}

func TestSecurityDispositionsRevoke_RequiresReason(t *testing.T) {
	mock := &securityDispositionsMock{}
	_, err := runSecurityCommand(t, mock, "security", "dispositions", "revoke", "policy-1", "--yes")
	if err == nil || exitCodeFor(err) != exitUsage {
		t.Fatalf("expected a usage error without a reason, got %v", err)
	}
}

func TestSecurityDispositionsRevoke_ConfirmsThenRevokes(t *testing.T) {
	policy := sampleDisposition()
	policy.Status = "revoked"
	mock := &securityDispositionsMock{mutation: &client.SecurityDispositionMutation{Policy: policy, Preview: *sampleDispositionPreview()}}
	stdout, _, err := runSecurityCommandWithInput(t, mock, "y\n", "security", "dispositions", "revoke", policy.ID, "--reason", "fix rolled out")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.revokedPolicyID != policy.ID || mock.revokeRequest == nil || mock.revokeRequest.Reason != "fix rolled out" {
		t.Fatalf("expected the revoke forwarded, got %q %+v", mock.revokedPolicyID, mock.revokeRequest)
	}
	if !strings.Contains(stdout, "Revoked accepted_risk policy") || !strings.Contains(stdout, "(revoked)") {
		t.Errorf("expected the revoked line:\n%s", stdout)
	}
}

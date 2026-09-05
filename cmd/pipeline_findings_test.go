package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// pipelineFindingsMock adds ListPipelineFindings to pipelineLaneMock without
// touching its shared definition in pipeline_test.go: Go resolves the
// method on the outer type before the embedded one, so this is the whole
// override.
type pipelineFindingsMock struct {
	pipelineLaneMock

	findingsRunID  string
	findingsResult *client.PipelineFindingList
	findingsError  error
}

func (mock *pipelineFindingsMock) ListPipelineFindings(ctx context.Context, selector client.PipelineSelector,
	runID string) (*client.PipelineFindingList, error) {
	mock.lastSelector = selector
	mock.findingsRunID = runID
	if mock.findingsError != nil {
		return nil, mock.findingsError
	}
	return mock.findingsResult, nil
}

func cveID(value string) *string { return &value }

func TestPipelineFindingsRegistered(t *testing.T) {
	pipelineCommand := newPipelineCommand()
	if findSubcommandOrNil(pipelineCommand, "findings") == nil {
		t.Fatal("pipeline subcommand \"findings\" is not registered")
	}
}

func TestPipelineFindingsEmpty(t *testing.T) {
	mockClient := &pipelineFindingsMock{findingsResult: &client.PipelineFindingList{Findings: []client.PipelineFinding{}}}
	output, executeError := runPipelineCommand(t, mockClient, "findings", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("findings error = %v", executeError)
	}
	if !strings.Contains(output, "No findings recorded") {
		t.Errorf("output = %q", output)
	}
	if mockClient.findingsRunID != "run-1" {
		t.Errorf("findings run id = %q", mockClient.findingsRunID)
	}
}

func TestPipelineFindingsTableSortsWorstSeverityFirst(t *testing.T) {
	mockClient := &pipelineFindingsMock{findingsResult: &client.PipelineFindingList{Findings: []client.PipelineFinding{
		{Tool: "checkov", Severity: "LOW", RuleID: "CKV_LOW_1", Title: "minor misconfiguration"},
		{Tool: "trivy", Severity: "CRITICAL", CVEID: cveID("CVE-2026-9999"), PackageName: "openssl",
			PackageVersion: "1.0.0", FixedVersion: "1.0.1", Title: "critical CVE"},
		{Tool: "semgrep", Severity: "HIGH", RuleID: "python.lang.security.audit", Title: "hardcoded secret"},
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "findings", "run-1", "--application", testApplicationID)
	if executeError != nil {
		t.Fatalf("findings error = %v", executeError)
	}
	criticalIndex := strings.Index(output, "critical CVE")
	highIndex := strings.Index(output, "hardcoded secret")
	lowIndex := strings.Index(output, "minor misconfiguration")
	if criticalIndex == -1 || highIndex == -1 || lowIndex == -1 {
		t.Fatalf("output missing a row: %q", output)
	}
	if criticalIndex >= highIndex || highIndex >= lowIndex {
		t.Errorf("rows are not worst-severity-first: critical=%d high=%d low=%d", criticalIndex, highIndex, lowIndex)
	}
	if !strings.Contains(output, "CVE-2026-9999") || !strings.Contains(output, "openssl@1.0.0") {
		t.Errorf("output missing the CVE / package columns: %q", output)
	}
}

func TestPipelineFindingsStructuredOutputSkipsTheTable(t *testing.T) {
	mockClient := &pipelineFindingsMock{findingsResult: &client.PipelineFindingList{Findings: []client.PipelineFinding{
		{Tool: "trivy", Severity: "HIGH", Title: "a finding"},
	}}}
	output, executeError := runPipelineCommand(t, mockClient, "findings", "run-1", "--application", testApplicationID, "-o", "json")
	if executeError != nil {
		t.Fatalf("findings -o json error = %v", executeError)
	}
	if !strings.Contains(output, `"tool": "trivy"`) || strings.Contains(output, "SEVERITY") {
		t.Errorf("output = %q, want the raw JSON shape rather than the table", output)
	}
}

func TestPipelineFindingsRunNotFound(t *testing.T) {
	mockClient := &pipelineFindingsMock{findingsError: errors.New("Pipeline run not found")}
	_, executeError := runPipelineCommand(t, mockClient, "findings", "missing-run", "--application", testApplicationID)
	if executeError == nil || executeError.Error() != "Pipeline run not found" {
		t.Fatalf("error = %v, want the sentinel text verbatim", executeError)
	}
}

package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

type aiRemediationMock struct {
	baseMock
	policy    *client.AIRemediationPolicy
	readError error
	reads     int
}

func (mock *aiRemediationMock) GetAIRemediationPolicy() (*client.AIRemediationPolicy, error) {
	mock.reads++
	if mock.readError != nil {
		return nil, mock.readError
	}
	policy := *mock.policy
	return &policy, nil
}

func runAIRemediationCommand(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stdout)
	rootCmd.SetArgs(args)
	resetTreeFlags(t, aiRemediationPolicyCmd)
	t.Cleanup(func() { resetTreeFlags(t, aiRemediationPolicyCmd) })
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

// defaultDocumentPolicy is what the platform answers for an organisation that
// never saved a policy: a full document with HTTP 200 and a null updated_at.
func defaultDocumentPolicy() *client.AIRemediationPolicy {
	return &client.AIRemediationPolicy{
		Enabled:               false,
		AutonomyLevel:         "propose",
		TierOverrides:         map[string]string{},
		ApproverUserIDs:       []string{},
		MaxActionsPerIncident: 5,
		CooldownMinutes:       30,
	}
}

func savedPolicy() *client.AIRemediationPolicy {
	updatedAt := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
	webhookID := "0b4f1b9e-0000-4000-8000-000000000001"
	return &client.AIRemediationPolicy{
		Enabled:       true,
		AutonomyLevel: "auto",
		TierOverrides: map[string]string{
			"apply_stack_changes": "auto",
			"delete_pod":          "approval",
			"delete_cluster":      "never",
		},
		SlackWebhookID:        &webhookID,
		ApproverUserIDs:       []string{"11111111-0000-4000-8000-000000000001"},
		MaxActionsPerIncident: 7,
		CooldownMinutes:       15,
		UpdatedAt:             &updatedAt,
	}
}

// The no-row default is the case that must never read as somebody's decision:
// the platform returns it with a 200, so only the CLI can make the difference
// visible.
func TestAIRemediationPolicySaysWhenNothingIsConfigured(t *testing.T) {
	mock := &aiRemediationMock{policy: defaultDocumentPolicy()}

	stdout, runError := runAIRemediationCommand(t, mock, "ai", "remediation", "policy")
	if runError != nil {
		t.Fatalf("policy failed: %v", runError)
	}
	for _, fragment := range []string{
		"No auto-remediation policy is configured",
		"platform default",
		"never (no policy has been saved for this organisation)",
		"off, so no alert trigger opens an auto-remediation run",
		"propose (every write waits for a human approval)",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("output lacks %q:\n%s", fragment, stdout)
		}
	}
}

func TestAIRemediationPolicyDoesNotCallASavedPolicyADefault(t *testing.T) {
	mock := &aiRemediationMock{policy: savedPolicy()}

	stdout, runError := runAIRemediationCommand(t, mock, "ai", "remediation", "policy")
	if runError != nil {
		t.Fatalf("policy failed: %v", runError)
	}
	if strings.Contains(stdout, "No auto-remediation policy is configured") {
		t.Fatalf("a saved policy is not the platform default:\n%s", stdout)
	}
	if !strings.Contains(stdout, "2026-09-01T10:30:00Z") {
		t.Fatalf("a saved policy reports when it was saved:\n%s", stdout)
	}
}

// The two readings a safety review has to be able to take at a glance.
func TestAIRemediationPolicySpellsOutTheAutoTierAndTheClusterScope(t *testing.T) {
	mock := &aiRemediationMock{policy: savedPolicy()}

	stdout, runError := runAIRemediationCommand(t, mock, "ai", "remediation", "policy")
	if runError != nil {
		t.Fatalf("policy failed: %v", runError)
	}
	for _, fragment := range []string{
		"apply_stack_changes",
		"executes with NO approval card",
		"parks an approval card for a human",
		"refused outright",
		"1 tool(s) run at the auto tier",
		"every cluster (no allow list is set)",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("output lacks %q:\n%s", fragment, stdout)
		}
	}
}

// An allow list that was never set admits every cluster; an allow list that is
// present and empty matches none. Folding the two together would misreport the
// scope in whichever direction the reader guessed.
func TestAIRemediationPolicyKeepsAnEmptyAllowListApartFromNoAllowList(t *testing.T) {
	policy := savedPolicy()
	policy.ClusterAllowList = []string{}
	mock := &aiRemediationMock{policy: policy}

	stdout, runError := runAIRemediationCommand(t, mock, "ai", "remediation", "policy")
	if runError != nil {
		t.Fatalf("policy failed: %v", runError)
	}
	if !strings.Contains(stdout, "no cluster (the allow list is empty, so nothing matches it)") {
		t.Fatalf("an empty allow list is not 'every cluster':\n%s", stdout)
	}

	listed := savedPolicy()
	listed.ClusterAllowList = []string{"22222222-0000-4000-8000-000000000002"}
	stdout, runError = runAIRemediationCommand(t, &aiRemediationMock{policy: listed},
		"ai", "remediation", "policy")
	if runError != nil {
		t.Fatalf("policy failed: %v", runError)
	}
	if !strings.Contains(stdout, "1 cluster(s) on the allow list") ||
		!strings.Contains(stdout, "22222222-0000-4000-8000-000000000002") {
		t.Fatalf("a configured allow list names its clusters:\n%s", stdout)
	}
}

func TestAIRemediationPolicyStructuredOutputCarriesTheDistinction(t *testing.T) {
	stdout, runError := runAIRemediationCommand(t,
		&aiRemediationMock{policy: defaultDocumentPolicy()},
		"ai", "remediation", "policy", "-o", "json")
	if runError != nil {
		t.Fatalf("policy -o json failed: %v", runError)
	}
	var document aiRemediationPolicyView
	if unmarshalError := json.Unmarshal([]byte(stdout), &document); unmarshalError != nil {
		t.Fatalf("output is not json: %v\n%s", unmarshalError, stdout)
	}
	if document.Configured {
		t.Fatalf("the platform default is not a configured policy: %s", stdout)
	}
	if document.Policy.UpdatedAt != nil {
		t.Fatalf("the default document carries no updated_at: %s", stdout)
	}
	if document.ClusterScope != aiRemediationScopeAllClusters {
		t.Fatalf("cluster_scope = %q", document.ClusterScope)
	}
	if len(document.ToolsWithoutApproval) != 0 {
		t.Fatalf("tools_without_approval = %v", document.ToolsWithoutApproval)
	}

	stdout, runError = runAIRemediationCommand(t, &aiRemediationMock{policy: savedPolicy()},
		"ai", "remediation", "policy", "-o", "json")
	if runError != nil {
		t.Fatalf("policy -o json failed: %v", runError)
	}
	document = aiRemediationPolicyView{}
	if unmarshalError := json.Unmarshal([]byte(stdout), &document); unmarshalError != nil {
		t.Fatalf("output is not json: %v\n%s", unmarshalError, stdout)
	}
	if !document.Configured || document.Policy.UpdatedAt == nil {
		t.Fatalf("a saved policy is configured: %s", stdout)
	}
	if len(document.ToolsWithoutApproval) != 1 || document.ToolsWithoutApproval[0] != "apply_stack_changes" {
		t.Fatalf("tools_without_approval = %v", document.ToolsWithoutApproval)
	}

	empty := savedPolicy()
	empty.ClusterAllowList = []string{}
	stdout, runError = runAIRemediationCommand(t, &aiRemediationMock{policy: empty},
		"ai", "remediation", "policy", "-o", "json")
	if runError != nil {
		t.Fatalf("policy -o json failed: %v", runError)
	}
	document = aiRemediationPolicyView{}
	if unmarshalError := json.Unmarshal([]byte(stdout), &document); unmarshalError != nil {
		t.Fatalf("output is not json: %v\n%s", unmarshalError, stdout)
	}
	if document.ClusterScope != aiRemediationScopeNoClusters {
		t.Fatalf("an empty allow list is not every cluster: %s", stdout)
	}
}

// The endpoint answers a default document rather than a 404 for an
// organisation with no policy, so a 404 can only mean the route is absent.
// Reporting that as exit 3 would read as "this organisation has no policy".
func TestAIRemediationPolicyReportsAMissingRouteAsSuch(t *testing.T) {
	mock := &aiRemediationMock{
		readError: client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found")}

	_, runError := runAIRemediationCommand(t, mock, "ai", "remediation", "policy")
	if runError == nil {
		t.Fatal("a 404 on this route is an error")
	}
	if !strings.Contains(runError.Error(), "does not serve the auto-remediation policy to API tokens") {
		t.Fatalf("error = %v", runError)
	}
	if got := exitCodeFor(runError); got != exitError {
		t.Fatalf("exit code = %d, want %d (not the not-found code)", got, exitError)
	}
}

func TestAIRemediationPolicyRelaysOtherFailures(t *testing.T) {
	mock := &aiRemediationMock{
		readError: &client.PermissionDeniedError{Permission: "ai.manage"}}

	_, runError := runAIRemediationCommand(t, mock, "ai", "remediation", "policy")
	if runError == nil {
		t.Fatal("a refusal is an error")
	}
	if got := exitCodeFor(runError); got != exitForbidden {
		t.Fatalf("exit code = %d, want %d", got, exitForbidden)
	}
	if mock.reads != 1 {
		t.Fatalf("reads = %d", mock.reads)
	}
}

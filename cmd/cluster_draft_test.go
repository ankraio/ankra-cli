package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

type draftBootstrapMock struct {
	baseMock
	existingCluster *client.ClusterListItem

	applyCalls      int
	applyStacks     []client.Stack
	applyGitRepo    *client.GitRepository
	applyName       string
	draftCalls      int
	draftReplace    bool
	draftClusterIDs []string
	draftStackNames []string
}

func (m *draftBootstrapMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.existingCluster != nil && m.existingCluster.Name == name {
		return *m.existingCluster, nil
	}
	return client.ClusterListItem{}, fmt.Errorf("no cluster found for name %q", name)
}

func (m *draftBootstrapMock) ApplyCluster(ctx context.Context, clusterReq client.CreateImportClusterRequest, wait bool) (*client.ImportResponse, bool, error) {
	m.applyCalls++
	m.applyName = clusterReq.Name
	m.applyGitRepo = clusterReq.Spec.GitRepository
	m.applyStacks = append([]client.Stack(nil), clusterReq.Spec.Stacks...)
	return &client.ImportResponse{Name: clusterReq.Name, ClusterId: "bootstrapped-cluster-id"}, false, nil
}

func (m *draftBootstrapMock) CreateStackDrafts(ctx context.Context, clusterID string, stacks []client.Stack, replace bool) (*client.StackDraftsResult, error) {
	m.draftCalls++
	m.draftReplace = replace
	result := &client.StackDraftsResult{}
	for _, stack := range stacks {
		m.draftClusterIDs = append(m.draftClusterIDs, clusterID)
		m.draftStackNames = append(m.draftStackNames, stack.Name)
		result.Stacks = append(result.Stacks, client.StagedStackDraft{StackName: stack.Name, DraftID: "draft-" + stack.Name})
	}
	return result, nil
}

func writeDraftImportClusterYAML(t *testing.T) string {
	t.Helper()
	yamlContent := `kind: ImportCluster
metadata:
  name: draft-cluster
spec:
  git_repository:
    provider: github
    credential_name: my-cred
    branch: main
    repository: org/repo
  stacks:
    - name: monitoring
      manifests: []
      addons: []
    - name: apps
      manifests: []
      addons: []
`
	yamlPath := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}
	return yamlPath
}

func TestDraftBootstrapImportsClusterWithoutStacks(t *testing.T) {
	mock := &draftBootstrapMock{}
	setMockClient(t, mock)
	yamlPath := writeDraftImportClusterYAML(t)

	var output string
	var err error
	stdout := captureStdout(t, func() {
		output, err = executeCommand("cluster", "draft", "-f", yamlPath)
	})
	if err != nil {
		t.Fatalf("cluster draft failed: %v\noutput: %s", err, output)
	}

	if mock.applyCalls != 1 {
		t.Fatalf("bootstrap ApplyCluster calls = %d, want 1", mock.applyCalls)
	}
	if len(mock.applyStacks) != 0 {
		names := make([]string, 0, len(mock.applyStacks))
		for _, stack := range mock.applyStacks {
			names = append(names, stack.Name)
		}
		t.Errorf("bootstrap ApplyCluster received stacks %v, want none", names)
	}
	if mock.applyName != "draft-cluster" {
		t.Errorf("bootstrap ApplyCluster name = %q, want %q", mock.applyName, "draft-cluster")
	}
	if mock.applyGitRepo == nil || mock.applyGitRepo.Repository != "org/repo" {
		t.Errorf("bootstrap ApplyCluster git_repository = %+v, want repository %q kept", mock.applyGitRepo, "org/repo")
	}

	if mock.draftCalls != 1 {
		t.Errorf("CreateStackDrafts calls = %d, want one for the whole file", mock.draftCalls)
	}
	wantStacks := []string{"monitoring", "apps"}
	if len(mock.draftStackNames) != len(wantStacks) {
		t.Fatalf("CreateStackDrafts stacks = %v, want %v", mock.draftStackNames, wantStacks)
	}
	for i, want := range wantStacks {
		if mock.draftStackNames[i] != want {
			t.Errorf("CreateStackDrafts stack %d = %q, want %q", i, mock.draftStackNames[i], want)
		}
		if mock.draftClusterIDs[i] != "bootstrapped-cluster-id" {
			t.Errorf("CreateStackDrafts stack %d cluster ID = %q, want %q", i, mock.draftClusterIDs[i], "bootstrapped-cluster-id")
		}
	}
	if mock.draftReplace {
		t.Error("a replace was asked for without --replace")
	}

	for _, want := range []string{`stack "monitoring": draft created (draft-monitoring)`, `stack "apps": draft created (draft-apps)`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected stdout to contain %q, got: %s", want, stdout)
		}
	}
}

func TestDraftExistingClusterSkipsImport(t *testing.T) {
	mock := &draftBootstrapMock{
		existingCluster: &client.ClusterListItem{ID: "existing-cluster-id", Name: "draft-cluster"},
	}
	setMockClient(t, mock)
	yamlPath := writeDraftImportClusterYAML(t)

	var output string
	var err error
	_ = captureStdout(t, func() {
		output, err = executeCommand("cluster", "draft", "-f", yamlPath)
	})
	if err != nil {
		t.Fatalf("cluster draft failed: %v\noutput: %s", err, output)
	}

	if mock.applyCalls != 0 {
		t.Errorf("ApplyCluster calls = %d, want 0 for an existing cluster", mock.applyCalls)
	}
	if len(mock.draftStackNames) != 2 {
		t.Errorf("CreateStackDrafts stacks = %v, want both stacks", mock.draftStackNames)
	}
	for i, clusterID := range mock.draftClusterIDs {
		if clusterID != "existing-cluster-id" {
			t.Errorf("CreateStackDrafts stack %d cluster ID = %q, want %q", i, clusterID, "existing-cluster-id")
		}
	}
}

type draftRefusalMock struct {
	draftBootstrapMock
}

func (m *draftRefusalMock) CreateStackDrafts(ctx context.Context, clusterID string, stacks []client.Stack, replace bool) (*client.StackDraftsResult, error) {
	m.draftCalls++
	return &client.StackDraftsResult{Errors: []client.ImportResponseResourceError{
		{Kind: "addon", Name: "grafana", Errors: []client.ImportResponseErrorItem{
			{Key: "registry_name", Message: "Field required"},
			{Key: "registry_url", Message: "Field required"},
		}},
		{Kind: "stack", Name: "monitoring", Errors: []client.ImportResponseErrorItem{{Message: "stack is locked"}}},
	}}, nil
}

func TestDraftPrintsEveryRefusalWithItsLocation(t *testing.T) {
	mock := &draftRefusalMock{draftBootstrapMock{
		existingCluster: &client.ClusterListItem{ID: "existing-cluster-id", Name: "draft-cluster"},
	}}
	setMockClient(t, mock)
	yamlPath := writeDraftImportClusterYAML(t)

	var output string
	var commandError error
	_ = captureStdout(t, func() {
		output, commandError = executeCommand("cluster", "draft", "-f", yamlPath)
	})
	if commandError == nil {
		t.Fatalf("cluster draft succeeded, want a failure\noutput: %s", output)
	}
	for _, want := range []string{
		`- stack "monitoring":`,
		`addon "grafana": registry_name - Field required`,
		`addon "grafana": registry_url - Field required`,
		`stack "monitoring": stack is locked`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected output to contain %q, got: %s", want, output)
		}
	}
}

// draftAnswerMock answers the staging call with what a test gives it.
type draftAnswerMock struct {
	draftBootstrapMock
	result *client.StackDraftsResult
	err    error
}

func (m *draftAnswerMock) CreateStackDrafts(ctx context.Context, clusterID string, stacks []client.Stack, replace bool) (*client.StackDraftsResult, error) {
	m.draftCalls++
	m.draftReplace = replace
	return m.result, m.err
}

func existingDraftCluster() *client.ClusterListItem {
	return &client.ClusterListItem{ID: "existing-cluster-id", Name: "draft-cluster"}
}

// runClusterDraftCommand runs the command with input on stdin and returns stdout and
// stderr apart, so structured output can be checked for being clean. Plain output is
// printed to the process's stdout, which is captured with the rest. A prompt is only
// answered when a test opts in with isInteractive.
func runClusterDraftCommand(t *testing.T, input string, isInteractive bool, arguments ...string) (string, string, error) {
	t.Helper()
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return isInteractive }
	t.Cleanup(func() { promptIsInteractive = previous })

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(append([]string{"cluster", "draft"}, arguments...))
	resetTreeFlags(t, clusterDraftCmd)
	t.Cleanup(func() {
		rootCmd.SetIn(nil)
		resetTreeFlags(t, clusterDraftCmd)
	})
	var runError error
	printed := captureStdout(t, func() {
		runError = rootCmd.Execute()
	})
	return printed + stdout.String(), stderr.String(), runError
}

func TestDraftReplaceWithYesAsksForTheReplaceAndNamesWhatItDiscarded(t *testing.T) {
	mock := &draftAnswerMock{
		draftBootstrapMock: draftBootstrapMock{existingCluster: existingDraftCluster()},
		result: &client.StackDraftsResult{Stacks: []client.StagedStackDraft{
			{StackName: "monitoring", DraftID: "new-monitoring", ReplacedDraftID: "old-monitoring"},
			{StackName: "apps", NoChange: true, ReplacedDraftID: "old-apps"},
		}},
	}
	setMockClient(t, mock)

	stdout, stderr, runError := runClusterDraftCommand(t, "", false, "-f", writeDraftImportClusterYAML(t), "--replace", "--yes")
	if runError != nil {
		t.Fatalf("cluster draft failed: %v\nstderr: %s", runError, stderr)
	}
	if mock.draftCalls != 1 || !mock.draftReplace {
		t.Errorf("calls = %d, replace = %t, want one call asking for a replace", mock.draftCalls, mock.draftReplace)
	}
	for _, want := range []string{
		`- stack "monitoring": draft created (new-monitoring), replacing draft old-monitoring`,
		`- stack "apps": no changes, discarded draft old-apps`,
		"Staged 1 draft(s), 1 stack(s) already up to date.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	for _, want := range []string{"--replace discards", "monitoring", "apps"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to say what is discarded: %q", stderr, want)
		}
	}
}

// A script that never saw what is discarded must not discard it, and importing a
// cluster that does not exist yet is a change too, so nothing at all happens.
func TestDraftReplaceWithoutATerminalOrYesChangesNothing(t *testing.T) {
	mock := &draftBootstrapMock{}
	setMockClient(t, mock)

	_, stderr, runError := runClusterDraftCommand(t, "", false, "-f", writeDraftImportClusterYAML(t), "--replace")
	if runError == nil {
		t.Fatal("cluster draft --replace succeeded without a terminal or --yes")
	}
	if code := exitCodeFor(runError); code != exitUsage {
		t.Errorf("exit code = %d, want %d (usage)", code, exitUsage)
	}
	if !strings.Contains(runError.Error(), "--yes") {
		t.Errorf("error = %q, want it to name --yes", runError)
	}
	if mock.applyCalls != 0 || mock.draftCalls != 0 {
		t.Errorf("apply calls = %d, staging calls = %d, want nothing changed", mock.applyCalls, mock.draftCalls)
	}
	if !strings.Contains(stderr, "--replace discards") {
		t.Errorf("stderr = %q, want it to say what would be discarded", stderr)
	}
}

func TestDraftReplaceDeclinedAtThePromptChangesNothing(t *testing.T) {
	mock := &draftBootstrapMock{}
	setMockClient(t, mock)

	_, _, runError := runClusterDraftCommand(t, "n\n", true, "-f", writeDraftImportClusterYAML(t), "--replace")
	if !errors.Is(runError, errCancelled) {
		t.Fatalf("error = %v, want the shared cancelled error", runError)
	}
	if code := exitCodeFor(runError); code != exitCancelled {
		t.Errorf("exit code = %d, want %d (cancelled)", code, exitCancelled)
	}
	if mock.applyCalls != 0 || mock.draftCalls != 0 {
		t.Errorf("apply calls = %d, staging calls = %d, want nothing changed", mock.applyCalls, mock.draftCalls)
	}
}

func TestDraftReplaceConfirmedAtThePromptGoesThrough(t *testing.T) {
	mock := &draftBootstrapMock{existingCluster: existingDraftCluster()}
	setMockClient(t, mock)

	_, stderr, runError := runClusterDraftCommand(t, "y\n", true, "-f", writeDraftImportClusterYAML(t), "--replace")
	if runError != nil {
		t.Fatalf("cluster draft failed: %v\nstderr: %s", runError, stderr)
	}
	if mock.draftCalls != 1 || !mock.draftReplace {
		t.Errorf("calls = %d, replace = %t, want one call asking for a replace", mock.draftCalls, mock.draftReplace)
	}
}

// A draft run without --replace has nothing to confirm, on a terminal or off one.
func TestDraftWithoutReplaceNeverAsks(t *testing.T) {
	mock := &draftBootstrapMock{existingCluster: existingDraftCluster()}
	setMockClient(t, mock)

	_, stderr, runError := runClusterDraftCommand(t, "", false, "-f", writeDraftImportClusterYAML(t))
	if runError != nil {
		t.Fatalf("cluster draft failed: %v\nstderr: %s", runError, stderr)
	}
	if strings.Contains(stderr, "--replace discards") {
		t.Errorf("stderr = %q, want no confirmation text", stderr)
	}
}

func TestDraftRefusedFileSaysNothingWasStagedAndKeepsAnEntryPerStack(t *testing.T) {
	mock := &draftAnswerMock{
		draftBootstrapMock: draftBootstrapMock{existingCluster: existingDraftCluster()},
		result: &client.StackDraftsResult{Errors: []client.ImportResponseResourceError{
			{Kind: "stack", Name: "monitoring", Errors: []client.ImportResponseErrorItem{{Message: "stack is locked"}}},
		}},
	}
	setMockClient(t, mock)

	stdout, stderr, runError := runClusterDraftCommand(t, "", false, "-f", writeDraftImportClusterYAML(t), "-o", "json")
	if runError == nil {
		t.Fatal("cluster draft succeeded, want a failure")
	}
	if !strings.Contains(runError.Error(), "nothing was staged") {
		t.Errorf("error = %q, want it to say nothing was staged", runError)
	}
	if !strings.Contains(stderr, "nothing was staged") || !strings.Contains(stderr, `- stack "monitoring":`) {
		t.Errorf("stderr = %q, want the refusal under its stack", stderr)
	}
	var outputs []stackDraftOutput
	if decodeError := json.Unmarshal([]byte(stdout), &outputs); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs = %+v, want one entry per stack of the file", outputs)
	}
	if outputs[0].StackName != "monitoring" || !strings.Contains(outputs[0].Error, "stack is locked") {
		t.Errorf("monitoring = %+v, want its own refusal", outputs[0])
	}
	if outputs[1].StackName != "apps" || !strings.Contains(outputs[1].Error, "not staged") {
		t.Errorf("apps = %+v, want it to say it was not staged", outputs[1])
	}
}

// A refusal the platform pinned on nothing holds up every stack of the file, and
// naming one of them would be a guess.
func TestDraftRefusalNamingNoStackGoesOnEveryEntry(t *testing.T) {
	mock := &draftAnswerMock{
		draftBootstrapMock: draftBootstrapMock{existingCluster: existingDraftCluster()},
		result: &client.StackDraftsResult{Errors: []client.ImportResponseResourceError{
			{Errors: []client.ImportResponseErrorItem{{Message: "The cluster is being deleted."}}},
		}},
	}
	setMockClient(t, mock)

	stdout, stderr, runError := runClusterDraftCommand(t, "", false, "-f", writeDraftImportClusterYAML(t), "-o", "json")
	if runError == nil {
		t.Fatal("cluster draft succeeded, want a failure")
	}
	if !strings.Contains(stderr, "- the file:") || !strings.Contains(stderr, "The cluster is being deleted.") {
		t.Errorf("stderr = %q, want the refusal under the file", stderr)
	}
	if strings.Contains(stderr, `- stack "`) || strings.Contains(stderr, `"": `) {
		t.Errorf("stderr = %q, want no stack named and no empty location", stderr)
	}
	var outputs []stackDraftOutput
	if decodeError := json.Unmarshal([]byte(stdout), &outputs); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	for _, output := range outputs {
		if output.Error != "The cluster is being deleted." {
			t.Errorf("%s error = %q, want the refusal", output.StackName, output.Error)
		}
	}
}

func TestDraftExistingDraftRefusalPointsAtReplaceUnlessItWasAskedFor(t *testing.T) {
	refusal := "A stack with the same name already exists in this cluster draft."
	testCases := []struct {
		name      string
		arguments []string
		wantHint  bool
	}{
		{name: "without --replace", wantHint: true},
		{name: "with --replace", arguments: []string{"--replace", "--yes"}, wantHint: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &draftAnswerMock{
				draftBootstrapMock: draftBootstrapMock{existingCluster: existingDraftCluster()},
				result: &client.StackDraftsResult{Errors: []client.ImportResponseResourceError{
					{Kind: "stack", Name: "monitoring", Errors: []client.ImportResponseErrorItem{{Key: "name", Message: refusal}}},
				}},
			}
			setMockClient(t, mock)

			arguments := append([]string{"-f", writeDraftImportClusterYAML(t)}, testCase.arguments...)
			_, stderr, runError := runClusterDraftCommand(t, "", false, arguments...)
			if runError == nil {
				t.Fatal("cluster draft succeeded, want a failure")
			}
			if got := strings.Contains(stderr, "re-run with --replace"); got != testCase.wantHint {
				t.Errorf("hint shown = %t, want %t\nstderr: %s", got, testCase.wantHint, stderr)
			}
		})
	}
}

// A staging call that failed keeps its class, so a script can tell a missing cluster
// from a refusal, and -o json still carries an entry per stack.
func TestDraftFailureKeepsItsExitClassAndAnEntryPerStack(t *testing.T) {
	mock := &draftAnswerMock{
		draftBootstrapMock: draftBootstrapMock{existingCluster: existingDraftCluster()},
		err:                client.NewUnexpectedResponseError(http.StatusNotFound, "nothing was staged: the platform did not find the cluster"),
	}
	setMockClient(t, mock)

	stdout, _, runError := runClusterDraftCommand(t, "", false, "-f", writeDraftImportClusterYAML(t), "-o", "json")
	if code := exitCodeFor(runError); code != exitNotFound {
		t.Errorf("exit code = %d, want %d (not found), error: %v", code, exitNotFound, runError)
	}
	var outputs []stackDraftOutput
	if decodeError := json.Unmarshal([]byte(stdout), &outputs); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if len(outputs) != 2 || !strings.Contains(outputs[0].Error, "nothing was staged") || !strings.Contains(outputs[1].Error, "nothing was staged") {
		t.Errorf("outputs = %+v, want the failure on both stacks", outputs)
	}
}

func TestDraftJSONCarriesTheReplacedDraftAndLeavesOutWhatIsNotThere(t *testing.T) {
	mock := &draftAnswerMock{
		draftBootstrapMock: draftBootstrapMock{existingCluster: existingDraftCluster()},
		result: &client.StackDraftsResult{Stacks: []client.StagedStackDraft{
			{StackName: "monitoring", DraftID: "new-monitoring", ReplacedDraftID: "old-monitoring"},
			{StackName: "apps", NoChange: true},
		}},
	}
	setMockClient(t, mock)

	stdout, _, runError := runClusterDraftCommand(t, "", false, "-f", writeDraftImportClusterYAML(t), "--replace", "--yes", "-o", "json")
	if runError != nil {
		t.Fatalf("cluster draft failed: %v", runError)
	}
	var outputs []map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &outputs); decodeError != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", decodeError, stdout)
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs = %+v, want one entry per stack", outputs)
	}
	if outputs[0]["replaced_draft_id"] != "old-monitoring" || outputs[0]["draft_id"] != "new-monitoring" {
		t.Errorf("monitoring = %+v", outputs[0])
	}
	if outputs[1]["no_change"] != true {
		t.Errorf("apps = %+v, want no_change", outputs[1])
	}
	for _, absent := range []string{"draft_id", "replaced_draft_id", "error"} {
		if _, isPresent := outputs[1][absent]; isPresent {
			t.Errorf("apps = %+v, want no %s", outputs[1], absent)
		}
	}
}

func TestDraftFileWithoutStacksAsksNothingAndStagesNothing(t *testing.T) {
	mock := &draftBootstrapMock{existingCluster: existingDraftCluster()}
	setMockClient(t, mock)
	yamlPath := filepath.Join(t.TempDir(), "cluster.yaml")
	yamlContent := `kind: ImportCluster
metadata:
  name: draft-cluster
spec:
  git_repository:
    provider: github
    credential_name: my-cred
    branch: main
    repository: org/repo
  stacks: []
`
	if writeError := os.WriteFile(yamlPath, []byte(yamlContent), 0644); writeError != nil {
		t.Fatal(writeError)
	}

	stdout, stderr, runError := runClusterDraftCommand(t, "", false, "-f", yamlPath, "--replace")
	if runError != nil {
		t.Fatalf("cluster draft failed: %v\nstderr: %s", runError, stderr)
	}
	if mock.draftCalls != 0 {
		t.Errorf("staging calls = %d, want none", mock.draftCalls)
	}
	if !strings.Contains(stdout, "Staged 0 draft(s)") {
		t.Errorf("stdout = %q, want the summary", stdout)
	}
}

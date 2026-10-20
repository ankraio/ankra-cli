package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type clusterUpdateCall struct {
	clusterID   string
	environment string
	criticality string
}

type clusterUpdateMock struct {
	baseMock
	cluster     client.ClusterListItem
	calls       []clusterUpdateCall
	updateError error
}

func (mock *clusterUpdateMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if mock.cluster.ID == clusterID {
		return mock.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (mock *clusterUpdateMock) GetCluster(name string) (client.ClusterListItem, error) {
	if mock.cluster.Name == name || mock.cluster.ID == name {
		return mock.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

// SetClusterEnvironment answers the way the platform does: the criticality
// comes back only when the request carried one.
func (mock *clusterUpdateMock) SetClusterEnvironment(_ context.Context, clusterID string, environment string,
	criticality string) (*client.ClusterEnvironmentResult, error) {
	mock.calls = append(mock.calls, clusterUpdateCall{clusterID: clusterID, environment: environment, criticality: criticality})
	if mock.updateError != nil {
		return nil, mock.updateError
	}
	result := &client.ClusterEnvironmentResult{Environment: environment}
	if criticality != "" {
		result.Criticality = &criticality
	}
	return result, nil
}

func newClusterUpdateMock() *clusterUpdateMock {
	return &clusterUpdateMock{cluster: client.ClusterListItem{
		ID: "3d0e2935-963b-4c7b-a494-b7266776ec68", Name: "so-infrastructure"}}
}

func runClusterUpdateCommand(t *testing.T, mock APIClient, arguments ...string) (string, error) {
	t.Helper()
	return runConfirmCommand(t, mock, "", []*cobra.Command{clusterUpdateCmd}, append([]string{"cluster", "update"}, arguments...)...)
}

// The ticket's own command: an imported cluster moved from production to
// development by name, graded as development like the same move in the portal.
func TestClusterUpdateMovesTheClusterToAPresetEnvironment(t *testing.T) {
	mock := newClusterUpdateMock()

	output, runError := runClusterUpdateCommand(t, mock, "so-infrastructure", "--environment", "development")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	want := clusterUpdateCall{clusterID: mock.cluster.ID, environment: "development", criticality: "development"}
	if len(mock.calls) != 1 || mock.calls[0] != want {
		t.Fatalf("calls = %+v, want one %+v", mock.calls, want)
	}
	for _, line := range []string{
		"Cluster 'so-infrastructure' environment set to 'development'.",
		"Reporting level: development.",
	} {
		if !strings.Contains(output, line) {
			t.Errorf("output missing %q:\n%s", line, output)
		}
	}
}

// The cluster may be named by id, which is how the ticket identified it.
func TestClusterUpdateAcceptsAClusterID(t *testing.T) {
	mock := newClusterUpdateMock()

	if _, runError := runClusterUpdateCommand(t, mock, mock.cluster.ID, "--environment", "staging"); runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if len(mock.calls) != 1 || mock.calls[0].clusterID != mock.cluster.ID {
		t.Fatalf("expected one write to the cluster by id, got %+v", mock.calls)
	}
}

func TestResolveClusterUpdateInput(t *testing.T) {
	for name, testCase := range map[string]struct {
		environment, criticality         string
		wantEnvironment, wantCriticality string
	}{
		"preset brings its own level":           {"production", "", "production", "production"},
		"testing is graded as development":      {"testing", "", "testing", "development"},
		"local is graded as disposable":         {"local", "", "local", "disposable"},
		"preset is sent as the preset":          {"  Development ", "", "development", "development"},
		"criticality overrides the pairing":     {"production", "Staging", "production", "staging"},
		"own name leaves the level alone":       {"eu-qa", "", "eu-qa", ""},
		"own name is sent as typed":             {" EU QA ", "", "EU QA", ""},
		"own name with an explicit criticality": {"eu-qa", "disposable", "eu-qa", "disposable"},
	} {
		environment, criticality, inputError := resolveClusterUpdateInput(testCase.environment, testCase.criticality)
		if inputError != nil {
			t.Errorf("%s: unexpected error %v", name, inputError)
			continue
		}
		if environment != testCase.wantEnvironment || criticality != testCase.wantCriticality {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", name, environment, criticality,
				testCase.wantEnvironment, testCase.wantCriticality)
		}
	}
}

// A custom environment without --criticality sends none, and the output says
// the level was left as it was rather than inventing one.
func TestClusterUpdateCustomEnvironmentLeavesTheLevelAlone(t *testing.T) {
	mock := newClusterUpdateMock()

	output, runError := runClusterUpdateCommand(t, mock, "so-infrastructure", "--environment", "eu-qa")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if len(mock.calls) != 1 || mock.calls[0].criticality != "" {
		t.Fatalf("a custom environment sent a criticality: %+v", mock.calls)
	}
	if !strings.Contains(output, "Reporting level unchanged; set one with --criticality.") {
		t.Errorf("output does not say the level is unchanged:\n%s", output)
	}
}

// Bad input is a usage error (exit 2) and nothing is written. Each case is a
// subtest so the flags one case sets are reset before the next runs.
func TestClusterUpdateRefusesBadInputBeforeWriting(t *testing.T) {
	for name, arguments := range map[string][]string{
		"no environment":       {"so-infrastructure"},
		"blank environment":    {"so-infrastructure", "--environment", "   "},
		"environment too long": {"so-infrastructure", "--environment", strings.Repeat("e", clusterEnvironmentNameLimit+1)},
		"unknown criticality":  {"so-infrastructure", "--environment", "development", "--criticality", "loud"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newClusterUpdateMock()
			_, runError := runClusterUpdateCommand(t, mock, arguments...)
			if runError == nil || exitCodeFor(runError) != exitUsage {
				t.Errorf("expected a usage error, got %v", runError)
			}
			if len(mock.calls) != 0 {
				t.Errorf("wrote despite the usage error: %+v", mock.calls)
			}
		})
	}
}

// A platform refusal reaches the user with the cluster named, and nothing
// prints as if it had worked.
func TestClusterUpdateRefusalReachesTheUser(t *testing.T) {
	mock := newClusterUpdateMock()
	mock.updateError = errors.New("Cluster not found")

	output, runError := runClusterUpdateCommand(t, mock, "so-infrastructure", "--environment", "development")
	if runError == nil || !strings.Contains(runError.Error(), `updating cluster "so-infrastructure": Cluster not found`) {
		t.Fatalf("expected the refusal, got %v", runError)
	}
	if strings.Contains(output, "environment set to") {
		t.Fatalf("a refused update printed success:\n%s", output)
	}
}

func TestClusterUpdateStructuredOutputStaysParseable(t *testing.T) {
	mock := newClusterUpdateMock()

	output, runError := runClusterUpdateCommand(t, mock, "so-infrastructure", "--environment", "development", "-o", "json")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	var decoded clusterUpdateOutput
	if decodeError := json.Unmarshal([]byte(strings.TrimSpace(output)), &decoded); decodeError != nil {
		t.Fatalf("structured output is not JSON: %v\n%s", decodeError, output)
	}
	if decoded.ClusterID != mock.cluster.ID || decoded.Environment != "development" ||
		decoded.Criticality == nil || *decoded.Criticality != "development" {
		t.Fatalf("unexpected structured output %+v", decoded)
	}
}

const importClusterWithEnvironment = `apiVersion: v1
kind: ImportCluster
metadata:
  name: so-infrastructure
  environment: development
spec:
  stacks: []
`

func writeImportClusterWithEnvironment(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "so-infrastructure.yaml")
	if writeError := os.WriteFile(path, []byte(contents), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	return path
}

// The question the ticket asked: does apply honour metadata.environment? It
// does not send it at all, so the request a file with one produces is the
// request the same file without it produces (PLA-913).
func TestApplyDoesNotSendMetadataEnvironment(t *testing.T) {
	importRequest, buildError := buildImportRequestFromBytes([]byte(importClusterWithEnvironment), t.TempDir())
	if buildError != nil {
		t.Fatalf("building the request: %v", buildError)
	}
	encoded, marshalError := json.Marshal(importRequest)
	if marshalError != nil {
		t.Fatal(marshalError)
	}
	if strings.Contains(string(encoded), "environment") {
		t.Fatalf("the apply request now carries the environment; the warning below is stale: %s", encoded)
	}
}

func TestUnreadImportMetadataWarnings(t *testing.T) {
	for name, testCase := range map[string]struct {
		document string
		want     []string
	}{
		"environment": {importClusterWithEnvironment, []string{
			"metadata.environment is not read: a cluster file does not set or change the cluster's environment. " +
				"Set it with: ankra cluster update so-infrastructure --environment development"}},
		"criticality that is not text": {"kind: ImportCluster\nmetadata:\n  name: edge\n  criticality: [loud]\nspec:\n  stacks: []\n",
			[]string{"metadata.criticality is not read: a cluster file does not set or change the cluster's reporting level. " +
				"Set it with: ankra cluster update edge --criticality <value>"}},
		"a value that needs quoting": {"kind: ImportCluster\nmetadata:\n  name: edge\n  environment: EU QA\nspec:\n  stacks: []\n",
			[]string{"metadata.environment is not read: a cluster file does not set or change the cluster's environment. " +
				"Set it with: ankra cluster update edge --environment 'EU QA'"}},
		"neither": {"kind: ImportCluster\nmetadata:\n  name: edge\n  description: x\nspec:\n  stacks: []\n", nil},
	} {
		got := unreadImportMetadataWarnings(writeImportClusterWithEnvironment(t, testCase.document))
		if strings.Join(got, "\n") != strings.Join(testCase.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", name, got, testCase.want)
		}
	}
}

// apply says so on stderr, offline, and still validates the file.
func TestClusterApplyDryRunWarnsAboutMetadataEnvironment(t *testing.T) {
	resetClusterApplyFlags(t)
	setMockClient(t, &applyOverrideMock{})
	path := writeImportClusterWithEnvironment(t, importClusterWithEnvironment)

	var commandOutput string
	stdout := captureStdout(t, func() {
		var executeError error
		commandOutput, executeError = executeCommand("cluster", "apply", "-f", path, "--dry-run")
		if executeError != nil {
			t.Errorf("dry-run failed: %v", executeError)
		}
	})
	if !strings.Contains(commandOutput, "warning: metadata.environment is not read") {
		t.Errorf("apply did not warn about metadata.environment:\n%s", commandOutput)
	}
	if strings.Contains(stdout, "metadata.environment") {
		t.Errorf("the warning went to stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Validation succeeded") {
		t.Errorf("dry-run no longer validates:\n%s", stdout)
	}
}

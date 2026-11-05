package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"
)

// applyEnvironmentMock answers the calls an apply that declares an
// environment makes, and records their order: the cluster read, the apply,
// the read again, then the environment change. storedAfterApply and
// lookupErrorAfterApply stand for the cluster as it is once the apply ran,
// for a label somebody chose or a read that failed in between.
type applyEnvironmentMock struct {
	baseMock
	stored                *client.ClusterListItem
	lookupError           error
	storedAfterApply      *client.ClusterListItem
	lookupErrorAfterApply error
	importResponse        *client.ImportResponse
	submitted             bool
	updateError           error
	events                []string
	applied               []client.CreateImportClusterRequest
	updates               []clusterUpdateCall
}

func (mock *applyEnvironmentMock) GetCluster(name string) (client.ClusterListItem, error) {
	afterApply := len(mock.applied) > 0
	mock.events = append(mock.events, "lookup")
	if afterApply && mock.lookupErrorAfterApply != nil {
		return client.ClusterListItem{}, mock.lookupErrorAfterApply
	}
	if mock.lookupError != nil {
		return client.ClusterListItem{}, mock.lookupError
	}
	if afterApply && mock.storedAfterApply != nil {
		return *mock.storedAfterApply, nil
	}
	if mock.stored != nil && mock.stored.Name == name {
		return *mock.stored, nil
	}
	return client.ClusterListItem{}, fmt.Errorf("no cluster found for name %q: %w", name, client.ErrClusterNotFound)
}

func (mock *applyEnvironmentMock) ApplyCluster(_ context.Context, request client.CreateImportClusterRequest,
	_ bool) (*client.ImportResponse, bool, error) {
	mock.events = append(mock.events, "apply")
	mock.applied = append(mock.applied, request)
	if mock.importResponse != nil {
		return mock.importResponse, false, nil
	}
	if mock.submitted {
		return &client.ImportResponse{OperationID: "4f1d2c3b-0000-4000-8000-000000000001"}, true, nil
	}
	return &client.ImportResponse{Name: request.Name, ClusterId: applyEnvironmentClusterID}, false, nil
}

func (mock *applyEnvironmentMock) SetClusterEnvironment(_ context.Context, clusterID string, environment string,
	criticality string) (*client.ClusterEnvironmentResult, error) {
	mock.events = append(mock.events, "environment")
	mock.updates = append(mock.updates, clusterUpdateCall{clusterID: clusterID, environment: environment, criticality: criticality})
	if mock.updateError != nil {
		return nil, mock.updateError
	}
	return &client.ClusterEnvironmentResult{Environment: environment}, nil
}

const applyEnvironmentClusterID = "6f0c1a2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b"

// storedEdgeCluster is eu-edge as the platform lists it, with its label and
// how that label was set.
func storedEdgeCluster(environment string, source string) *client.ClusterListItem {
	return &client.ClusterListItem{ID: applyEnvironmentClusterID, Name: "eu-edge", Environment: environment,
		EnvironmentSource: source}
}

// applyEnvironmentDocument is an ImportCluster for eu-edge carrying the given
// metadata lines (each indented two spaces and ending in a newline).
func applyEnvironmentDocument(metadataLines string) string {
	return "apiVersion: v1\nkind: ImportCluster\nmetadata:\n  name: eu-edge\n" + metadataLines + "spec:\n  stacks: []\n"
}

// runApplyCommand applies document with mock, returning what the command wrote
// to its own writers (out and err together), what it printed on os.Stdout,
// and its error.
func runApplyCommand(t *testing.T, mock *applyEnvironmentMock, document string, extraArgs ...string) (string, string, error) {
	t.Helper()
	resetClusterApplyFlags(t)
	setMockClient(t, mock)
	path := writeImportClusterWithEnvironment(t, document)
	var commandOutput string
	var executeError error
	stdout := captureStdout(t, func() {
		commandOutput, executeError = executeCommand(append([]string{"cluster", "apply", "-f", path}, extraArgs...)...)
	})
	return commandOutput, stdout, executeError
}

// The question PLA-913 asked, answered the other way now: the request a file
// with metadata.environment produces carries it, and the platform records it
// when that import creates the cluster (ankra-bn86k4.6.6.23).
func TestApplySendsMetadataEnvironmentOnTheImportBody(t *testing.T) {
	for name, testCase := range map[string]struct{ line, want string }{
		"a preset":                     {"  environment: staging\n", `"environment":"staging"`},
		"a preset written in capitals": {"  environment: Production\n", `"environment":"production"`},
		"a name of your own, trimmed":  {"  environment: ' EU QA '\n", `"environment":"EU QA"`},
	} {
		importRequest, buildError := buildImportRequestFromBytes([]byte(applyEnvironmentDocument(testCase.line)), t.TempDir())
		if buildError != nil {
			t.Fatalf("%s: building the request: %v", name, buildError)
		}
		encoded, marshalError := json.Marshal(importRequest)
		if marshalError != nil {
			t.Fatal(marshalError)
		}
		if !strings.Contains(string(encoded), testCase.want) {
			t.Errorf("%s: the request does not carry %s: %s", name, testCase.want, encoded)
		}
	}
	for name, line := range map[string]string{"no key": "", "a key with no value": "  environment:\n"} {
		importRequest, buildError := buildImportRequestFromBytes([]byte(applyEnvironmentDocument(line)), t.TempDir())
		if buildError != nil {
			t.Fatalf("%s: building the request: %v", name, buildError)
		}
		encoded, marshalError := json.Marshal(importRequest)
		if marshalError != nil {
			t.Fatal(marshalError)
		}
		if strings.Contains(string(encoded), `"environment"`) {
			t.Errorf("%s: a file that declares no environment must send none, so the platform keeps its default: %s", name, encoded)
		}
	}
}

// A value the platform would refuse is refused before anything is sent, with
// the key named, and so is the same file under 'ankra cluster validate'.
func TestApplyRefusesAMetadataEnvironmentThePlatformWouldRefuse(t *testing.T) {
	for name, line := range map[string]string{
		"empty":    "  environment: ''\n",
		"blank":    "  environment: '   '\n",
		"too long": "  environment: " + strings.Repeat("a", clusterEnvironmentNameLimit+1) + "\n",
		"not text": "  environment: [production]\n",
	} {
		_, buildError := buildImportRequestFromBytes([]byte(applyEnvironmentDocument(line)), t.TempDir())
		if buildError == nil || !strings.Contains(buildError.Error(), "metadata.environment") {
			t.Errorf("%s: want a refusal naming metadata.environment, got %v", name, buildError)
		}
	}
	atTheLimit := "  environment: " + strings.Repeat("a", clusterEnvironmentNameLimit) + "\n"
	if _, buildError := buildImportRequestFromBytes([]byte(applyEnvironmentDocument(atTheLimit)), t.TempDir()); buildError != nil {
		t.Errorf("a %d-byte environment is within the platform's limit: %v", clusterEnvironmentNameLimit, buildError)
	}
}

// A cluster that does not exist yet is created with the declared label by the
// import itself, so nothing else is sent.
func TestApplyCreatesTheClusterWithTheDeclaredEnvironment(t *testing.T) {
	mock := &applyEnvironmentMock{}
	_, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: staging\n"))
	if executeError != nil {
		t.Fatalf("apply failed: %v", executeError)
	}
	if len(mock.applied) != 1 || mock.applied[0].Environment == nil || *mock.applied[0].Environment != "staging" {
		t.Fatalf("the import must carry the declared environment, applied %+v", mock.applied)
	}
	if len(mock.updates) != 0 {
		t.Errorf("a cluster the import creates needs no environment change, sent %+v", mock.updates)
	}
}

// An existing cluster whose label nobody chose (a create lane's default) is
// changed to the file's through the environment route, after the apply is
// taken, with the preset's reporting level (as 'ankra cluster update' sends).
func TestApplyChangesAnUnchosenEnvironmentToTheDeclaredOne(t *testing.T) {
	mock := &applyEnvironmentMock{stored: storedEdgeCluster("development", "default")}
	commandOutput, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: staging\n"))
	if executeError != nil {
		t.Fatalf("apply failed: %v", executeError)
	}
	want := []clusterUpdateCall{{clusterID: applyEnvironmentClusterID, environment: "staging", criticality: "staging"}}
	if fmt.Sprint(mock.updates) != fmt.Sprint(want) {
		t.Fatalf("environment changes = %+v, want %+v", mock.updates, want)
	}
	if strings.Join(mock.events, ",") != "lookup,apply,lookup,environment" {
		t.Errorf("calls ran in the order %v; the cluster is read before the apply, read again after it, then changed",
			mock.events)
	}
	if !strings.Contains(commandOutput, "changed from 'development' to 'staging'") {
		t.Errorf("the change must be reported:\n%s", commandOutput)
	}
}

// An apply under --wait can run for minutes. A label somebody chose while it
// ran is read again right before the write and left, with the same warning
// as a label chosen before.
func TestApplyLeavesALabelChosenWhileTheApplyRan(t *testing.T) {
	mock := &applyEnvironmentMock{
		stored:           storedEdgeCluster("development", "default"),
		storedAfterApply: storedEdgeCluster("production", "person"),
	}
	commandOutput, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: staging\n"),
		"--wait")
	if executeError != nil {
		t.Fatalf("a label left as somebody chose it is not a failure: %v", executeError)
	}
	if len(mock.updates) != 0 {
		t.Fatalf("overwrote a label chosen during the apply: %+v", mock.updates)
	}
	if !strings.Contains(commandOutput, "keeps its environment 'production': a person chose it") {
		t.Errorf("the warning must name the label as it is now:\n%s", commandOutput)
	}
}

// A second read that fails stops the change and says the configuration was
// applied, with exit 1 so a script never reads the cluster as matching.
func TestApplyReportsASecondReadThatFails(t *testing.T) {
	mock := &applyEnvironmentMock{
		stored:                storedEdgeCluster("development", "default"),
		lookupErrorAfterApply: errors.New("platform unavailable"),
	}
	_, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: staging\n"))
	if executeError == nil || !strings.Contains(executeError.Error(), "reading it again before the change failed") ||
		!strings.Contains(executeError.Error(), "the configuration was applied") {
		t.Fatalf("want the failed second read reported, got %v", executeError)
	}
	if exitCodeFor(executeError) != exitError {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitError)
	}
	if len(mock.updates) != 0 {
		t.Errorf("changed the environment without a second read: %+v", mock.updates)
	}
}

// A declared preset is the same label as a stored one in other casing: no
// change is sent and no warning claims a difference nobody can see.
func TestApplyTreatsAPresetInOtherCasingAsTheSameLabel(t *testing.T) {
	for _, source := range []string{"default", "person"} {
		mock := &applyEnvironmentMock{stored: storedEdgeCluster("Production", source)}
		commandOutput, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: production\n"))
		if executeError != nil {
			t.Fatalf("%s: apply failed: %v", source, executeError)
		}
		if len(mock.updates) != 0 || strings.Contains(commandOutput, "warning: metadata.environment") {
			t.Errorf("%s: treated 'Production' as a different label: changes %+v, output:\n%s", source, mock.updates,
				commandOutput)
		}
	}
}

// A label somebody chose is never reverted by a file: apply leaves it, says
// who set it and names the command that changes it, and still exits 0. A
// source that is not 'default', including one an older platform does not
// send, counts as a choice.
func TestApplyLeavesAChosenEnvironmentAndSaysHowToChangeIt(t *testing.T) {
	for source, saying := range map[string]string{
		"person":     "a person chose it",
		"automation": "automation set it",
		"unknown":    "it was set before Ankra recorded who chose it",
		"":           "the platform does not say who chose it",
	} {
		mock := &applyEnvironmentMock{stored: storedEdgeCluster("production", source)}
		commandOutput, stdout, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: staging\n"))
		if executeError != nil {
			t.Fatalf("source %q: a kept label is not a failure: %v", source, executeError)
		}
		if len(mock.updates) != 0 {
			t.Errorf("source %q: a chosen label must not be changed by a file, sent %+v", source, mock.updates)
		}
		if len(mock.applied) != 1 {
			t.Errorf("source %q: the configuration must still be applied, applied %d times", source, len(mock.applied))
		}
		want := "warning: metadata.environment is \"staging\" but cluster 'eu-edge' keeps its environment 'production': " +
			saying + ", and a cluster file sets the environment only where nobody has. " +
			"To change it: ankra cluster update eu-edge --environment staging"
		if !strings.Contains(commandOutput, want) {
			t.Errorf("source %q: want the warning\n%s\ngot:\n%s", source, want, commandOutput)
		}
		if strings.Contains(stdout, "metadata.environment") {
			t.Errorf("source %q: the warning belongs on stderr, not stdout:\n%s", source, stdout)
		}
	}
}

// Re-applying a file nobody edited sends no environment change: the route
// records a person's write of an unchanged label as a new confirmation of what
// the cluster is for, and a re-apply confirms nothing.
func TestApplySendsNoEnvironmentChangeForAnUnchangedLabel(t *testing.T) {
	mock := &applyEnvironmentMock{stored: storedEdgeCluster("staging", "default")}
	_, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: Staging\n"))
	if executeError != nil {
		t.Fatalf("apply failed: %v", executeError)
	}
	if len(mock.updates) != 0 {
		t.Errorf("an unchanged label must not be re-sent, sent %+v", mock.updates)
	}
	if len(mock.applied) != 1 {
		t.Errorf("the configuration must still be applied, applied %d times", len(mock.applied))
	}
}

// A name of your own brings no reporting level, so the stored one is left as
// it is; a cluster imported without a label reads as having none.
func TestApplyChangesToANameOfYourOwnLeaveTheReportingLevel(t *testing.T) {
	mock := &applyEnvironmentMock{stored: storedEdgeCluster("", "default")}
	commandOutput, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: eu-qa\n"))
	if executeError != nil {
		t.Fatalf("apply failed: %v", executeError)
	}
	want := []clusterUpdateCall{{clusterID: applyEnvironmentClusterID, environment: "eu-qa", criticality: ""}}
	if fmt.Sprint(mock.updates) != fmt.Sprint(want) {
		t.Fatalf("environment changes = %+v, want %+v", mock.updates, want)
	}
	if !strings.Contains(commandOutput, "changed from (none) to 'eu-qa'") {
		t.Errorf("an unlabelled cluster must read as having none:\n%s", commandOutput)
	}
}

// A file that declares no environment costs no extra read and sends no change.
func TestApplyWithoutMetadataEnvironmentNeitherReadsNorChangesIt(t *testing.T) {
	mock := &applyEnvironmentMock{stored: storedEdgeCluster("development", "default")}
	_, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument(""))
	if executeError != nil {
		t.Fatalf("apply failed: %v", executeError)
	}
	if strings.Join(mock.events, ",") != "apply" {
		t.Errorf("calls = %v, want the apply alone", mock.events)
	}
}

// The default --wait=false submits the apply; the environment change still
// follows it.
func TestApplyChangesTheEnvironmentAfterASubmittedApply(t *testing.T) {
	mock := &applyEnvironmentMock{stored: storedEdgeCluster("development", "default"), submitted: true}
	_, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: production\n"))
	if executeError != nil {
		t.Fatalf("apply failed: %v", executeError)
	}
	if len(mock.updates) != 1 || mock.updates[0].environment != "production" {
		t.Errorf("a submitted apply must still change the environment, sent %+v", mock.updates)
	}
}

// A configuration the platform refused changes nothing, the label included.
func TestApplyRefusedByThePlatformChangesNoEnvironment(t *testing.T) {
	mock := &applyEnvironmentMock{stored: storedEdgeCluster("development", "default"), importResponse: &client.ImportResponse{
		Errors: []client.ImportResponseResourceError{{Name: "web", Kind: "addon",
			Errors: []client.ImportResponseErrorItem{{Key: "chart", Message: "not found"}}}}}}
	_, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: staging\n"))
	if executeError == nil {
		t.Fatal("a refused import must fail the command")
	}
	if len(mock.updates) != 0 {
		t.Errorf("a refused apply must not change the environment, sent %+v", mock.updates)
	}
}

// A refused environment change (here, a service account moving a cluster off
// production) fails the command and says the configuration itself was applied.
func TestApplyReportsARefusedEnvironmentChange(t *testing.T) {
	refusal := errors.New("only a person can move a cluster off production")
	mock := &applyEnvironmentMock{stored: storedEdgeCluster("production", "default"), updateError: refusal}
	_, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: development\n"))
	if executeError == nil {
		t.Fatal("a refused environment change must fail the command")
	}
	if !errors.Is(executeError, refusal) || !strings.Contains(executeError.Error(), "the configuration was applied") ||
		!strings.Contains(executeError.Error(), "kept its environment 'production'") {
		t.Errorf("the error must say what was applied and what was not: %v", executeError)
	}
	if len(mock.applied) != 1 {
		t.Errorf("the configuration is applied first, applied %d times", len(mock.applied))
	}
}

// A cluster read that fails stops the apply before anything is written, so
// the label is never silently left behind.
func TestApplyStopsWhenTheClusterCannotBeRead(t *testing.T) {
	mock := &applyEnvironmentMock{lookupError: errors.New("platform unavailable")}
	_, _, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: staging\n"))
	if executeError == nil || !strings.Contains(executeError.Error(), "to compare its environment") {
		t.Fatalf("want the lookup failure, got %v", executeError)
	}
	if len(mock.applied) != 0 || len(mock.updates) != 0 {
		t.Errorf("nothing may be written after a failed read: applied %d, changes %+v", len(mock.applied), mock.updates)
	}
}

// Under -o json the note goes to stderr, so stdout stays one JSON document.
func TestApplyEnvironmentNoteKeepsStructuredOutputParseable(t *testing.T) {
	mock := &applyEnvironmentMock{stored: storedEdgeCluster("development", "default")}
	resetClusterApplyFlags(t)
	setMockClient(t, mock)
	path := writeImportClusterWithEnvironment(t, applyEnvironmentDocument("  environment: staging\n"))
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"cluster", "apply", "-f", path, "--wait", "-o", "json"})
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("apply failed: %v", executeError)
	}
	var decoded map[string]interface{}
	if decodeError := json.Unmarshal(stdout.Bytes(), &decoded); decodeError != nil {
		t.Fatalf("stdout is not one JSON document (%v):\n%s", decodeError, stdout.String())
	}
	if !strings.Contains(stderr.String(), "changed from 'development' to 'staging'") {
		t.Errorf("the note belongs on stderr:\n%s", stderr.String())
	}
}

// metadata.environment is read now, so only metadata.criticality is warned
// about, and the suggested command carries the --environment that
// 'ankra cluster update' requires.
func TestUnreadImportMetadataWarnings(t *testing.T) {
	for name, testCase := range map[string]struct {
		document string
		want     []string
	}{
		"environment alone": {applyEnvironmentDocument("  environment: development\n"), nil},
		"criticality beside an environment": {applyEnvironmentDocument("  environment: EU QA\n  criticality: staging\n"),
			[]string{"metadata.criticality is not read: a cluster file does not set the cluster's reporting level, which follows its environment. " +
				"Once the cluster exists, set it with: ankra cluster update eu-edge --environment 'EU QA' --criticality staging"}},
		"criticality that is not text": {applyEnvironmentDocument("  criticality: [loud]\n"),
			[]string{"metadata.criticality is not read: a cluster file does not set the cluster's reporting level, which follows its environment. " +
				"Once the cluster exists, set it with: ankra cluster update eu-edge --environment <environment> --criticality <value>"}},
		"neither": {applyEnvironmentDocument("  description: x\n"), nil},
	} {
		got := unreadImportMetadataWarnings(writeImportClusterWithEnvironment(t, testCase.document))
		if strings.Join(got, "\n") != strings.Join(testCase.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", name, got, testCase.want)
		}
	}
}

// The warning inspects the document apply sends and no other: apply reads
// only the first document of a '---' stream.
func TestUnreadImportMetadataWarningsReadTheDocumentApplySends(t *testing.T) {
	const twoDocuments = "kind: ImportCluster\nmetadata:\n  name: so-infrastructure\nspec:\n  stacks: []\n" +
		"---\nkind: ImportCluster\nmetadata:\n  name: so-upcloud-infrastructure\n  criticality: development\nspec:\n  stacks: []\n"
	importRequest, buildError := buildImportRequestFromBytes([]byte(twoDocuments), t.TempDir())
	if buildError != nil {
		t.Fatalf("building the request: %v", buildError)
	}
	if importRequest.Name != "so-infrastructure" {
		t.Fatalf("apply now sends %q, not the first document; the warning reads only the first", importRequest.Name)
	}
	if got := unreadImportMetadataWarnings(writeImportClusterWithEnvironment(t, twoDocuments)); got != nil {
		t.Errorf("warned about a document apply does not send: %q", got)
	}
}

// A dry run stays offline and no longer calls metadata.environment unread.
func TestClusterApplyDryRunReadsMetadataEnvironmentOffline(t *testing.T) {
	mock := &applyEnvironmentMock{stored: storedEdgeCluster("development", "default")}
	commandOutput, stdout, executeError := runApplyCommand(t, mock, applyEnvironmentDocument("  environment: staging\n"), "--dry-run")
	if executeError != nil {
		t.Fatalf("dry-run failed: %v", executeError)
	}
	if len(mock.events) != 0 {
		t.Errorf("dry-run called the API: %v", mock.events)
	}
	if strings.Contains(commandOutput+stdout, "metadata.environment is not read") {
		t.Errorf("metadata.environment is read now; the old warning is stale:\n%s%s", commandOutput, stdout)
	}
	if !strings.Contains(stdout, "Validation succeeded") {
		t.Errorf("dry-run no longer validates:\n%s", stdout)
	}
}

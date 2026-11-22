package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

const (
	clusterConnectTestClusterID      = "0b7c4d1e-5f6a-4b8c-9d0e-1f2a3b4c5d6e"
	clusterConnectTestInstallCommand = "helm upgrade --install ankra-agent oci://registry.ankra.cloud/ankra/ankra-agent " +
		"--version 2.1.0 --set config.ankra_url=https://platform.ankra.app " +
		"--set config.token=example-agent-token --namespace=ankra --create-namespace"
)

// clusterConnectSuccessLine opens the output of a connect that registered the
// cluster. A test that must not see a success checks for this line, not for
// the words "is registered": an error may rightly say "once it is registered".
const clusterConnectSuccessLine = "Cluster 'my-cluster' is registered"

type clusterConnectMock struct {
	existing    []client.ClusterListItem
	lookupError error
	requests    []client.CreateImportClusterRequest
	waits       []bool
	response    *client.ImportResponse
	submitted   bool
	applyError  error
}

func (mock *clusterConnectMock) GetCluster(name string) (client.ClusterListItem, error) {
	if mock.lookupError != nil {
		return client.ClusterListItem{}, mock.lookupError
	}
	for _, cluster := range mock.existing {
		if cluster.Name == name {
			return cluster, nil
		}
	}
	return client.ClusterListItem{}, client.ErrClusterNotFound
}

// ApplyCluster answers the way the platform does for a new cluster: its name,
// its id and the Helm command that installs the agent.
func (mock *clusterConnectMock) ApplyCluster(_ context.Context, request client.CreateImportClusterRequest, wait bool) (*client.ImportResponse, bool, error) {
	mock.requests = append(mock.requests, request)
	mock.waits = append(mock.waits, wait)
	if mock.applyError != nil {
		return nil, false, mock.applyError
	}
	if mock.response != nil {
		return mock.response, mock.submitted, nil
	}
	return &client.ImportResponse{Name: request.Name, ClusterId: clusterConnectTestClusterID,
		ImportCommand: clusterConnectTestInstallCommand}, false, nil
}

func runClusterConnectCommand(t *testing.T, mock any, arguments ...string) (string, error) {
	t.Helper()
	return runConfirmCommand(t, mock, "", []*cobra.Command{clusterConnectCmd}, append([]string{"cluster", "connect"}, arguments...)...)
}

// One command and no cluster file: a single import of a cluster with nothing
// declared on it, waited for, answered with the Helm command and what to do
// with it.
func TestClusterConnectRegistersTheClusterAndPrintsTheInstallCommand(t *testing.T) {
	mock := &clusterConnectMock{}

	output, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("imports = %d, want one", len(mock.requests))
	}
	request := mock.requests[0]
	if request.Name != "my-cluster" || request.Environment != nil || request.Description != "" {
		t.Fatalf("request = %+v, want only the name", request)
	}
	if request.Spec.Stacks == nil || len(request.Spec.Stacks) != 0 {
		t.Fatalf("stacks = %#v, want an empty list: null is not a cluster with nothing declared", request.Spec.Stacks)
	}
	if !mock.waits[0] {
		t.Fatal("the import was not waited for, and the platform returns the install command only with an applied write")
	}
	if !request.CreateOnly {
		t.Fatal("the import was not marked create only, so the platform would update a cluster that took the name meanwhile")
	}
	for _, want := range []string{
		clusterConnectSuccessLine,
		"kubectl config current-context",
		clusterConnectTestInstallCommand,
		"do not share it or commit it to Git",
		"ankra cluster agent status --cluster my-cluster",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}

// What the cluster is for and its description go on the same write.
func TestClusterConnectSendsTheEnvironmentAndDescription(t *testing.T) {
	mock := &clusterConnectMock{}

	_, runError := runClusterConnectCommand(t, mock, "payments-prod",
		"--environment", " production ", "--description", " Card payments ")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("imports = %d, want one", len(mock.requests))
	}
	request := mock.requests[0]
	if request.Environment == nil || *request.Environment != "production" {
		t.Fatalf("environment = %v, want production", request.Environment)
	}
	if request.Description != "Card payments" {
		t.Fatalf("description = %q, want it trimmed", request.Description)
	}
}

// `ankra cluster import <name>` is the same command.
func TestClusterConnectAnswersToImport(t *testing.T) {
	mock := &clusterConnectMock{}

	output, runError := runConfirmCommand(t, mock, "", []*cobra.Command{clusterConnectCmd}, "cluster", "import", "my-cluster")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if len(mock.requests) != 1 || !strings.Contains(output, clusterConnectTestInstallCommand) {
		t.Fatalf("imports = %d, output:\n%s", len(mock.requests), output)
	}
}

// The platform's import updates a cluster that already has the name. A
// connect never does: the name is refused (exit 8) before anything is sent.
func TestClusterConnectRefusesANameThatIsTaken(t *testing.T) {
	mock := &clusterConnectMock{existing: []client.ClusterListItem{{ID: clusterConnectTestClusterID, Name: "my-cluster"}}}

	_, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if exitCodeFor(runError) != exitBlocked {
		t.Fatalf("exit = %d (%v), want blocked", exitCodeFor(runError), runError)
	}
	if len(mock.requests) != 0 {
		t.Fatalf("a taken name was imported: %+v", mock.requests)
	}
	for _, want := range []string{"already exists", "nothing was changed", "ankra cluster agent token --cluster my-cluster"} {
		if !strings.Contains(runError.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, runError)
		}
	}
	assertClusterConnectSaysToChooseAnotherNameFirst(t, runError)
}

// A taken name is most often met by someone connecting a different cluster.
// The existing cluster's install command would connect that cluster under
// the existing name, so the answer says to choose another name before it
// names the command that prints it.
func assertClusterConnectSaysToChooseAnotherNameFirst(t *testing.T, runError error) {
	t.Helper()
	message := runError.Error()
	another := strings.Index(message, "choose another name")
	token := strings.Index(message, "ankra cluster agent token")
	if another < 0 || token < 0 || another > token {
		t.Errorf("the answer must say to choose another name before it names the token command: %v", runError)
	}
}

// A cluster that takes the name after this command read it is refused by the
// platform, which was asked to create only. The answer is the same refusal as
// a name found taken up front (exit 8): it names the existing cluster and the
// command that prints its install command, and nothing was changed.
func TestClusterConnectRefusesANameTakenWhileItRan(t *testing.T) {
	mock := &clusterConnectMock{applyError: &client.ClusterAlreadyExistsError{
		StatusCode: 409, Message: "Cluster with this name already exists. Please choose a different name.",
		ClusterID: clusterConnectTestClusterID, Name: "my-cluster",
	}}

	output, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if exitCodeFor(runError) != exitBlocked {
		t.Fatalf("exit = %d (%v), want blocked", exitCodeFor(runError), runError)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("imports = %d, want the one the platform refused", len(mock.requests))
	}
	for _, want := range []string{
		"already exists",
		"(id " + clusterConnectTestClusterID + ")",
		"nothing was changed",
		"ankra cluster agent token --cluster my-cluster",
	} {
		if !strings.Contains(runError.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, runError)
		}
	}
	assertClusterConnectSaysToChooseAnotherNameFirst(t, runError)
	if strings.Contains(output, clusterConnectSuccessLine) {
		t.Errorf("a refused create was reported as a registration:\n%s", output)
	}
}

// A refusal that names no cluster still reads as a sentence: no empty id.
func TestClusterConnectRefusalWithoutAnID(t *testing.T) {
	mock := &clusterConnectMock{applyError: &client.ClusterAlreadyExistsError{StatusCode: 409, Message: "a cluster with this name already exists"}}

	_, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if exitCodeFor(runError) != exitBlocked {
		t.Fatalf("exit = %d (%v), want blocked", exitCodeFor(runError), runError)
	}
	if strings.Contains(runError.Error(), "(id") {
		t.Fatalf("the refusal shows an id the platform did not give: %v", runError)
	}
	if !strings.Contains(runError.Error(), `a cluster named "my-cluster" already exists; nothing was changed`) {
		t.Fatalf("refusal = %v", runError)
	}
}

// A lookup that fails for another reason stops the connect: without it the
// name could be one an existing cluster has.
func TestClusterConnectStopsWhenTheNameCannotBeChecked(t *testing.T) {
	mock := &clusterConnectMock{lookupError: errors.New("the platform did not answer")}

	_, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if runError == nil {
		t.Fatal("expected the lookup failure, got none")
	}
	for _, want := range []string{"the platform did not answer", "nothing was sent and nothing was changed", "try again"} {
		if !strings.Contains(runError.Error(), want) {
			t.Errorf("error lacks %q: %v", want, runError)
		}
	}
	if len(mock.requests) != 0 {
		t.Fatalf("the cluster was imported without the name check: %+v", mock.requests)
	}
}

// A name or environment the platform would refuse is a usage error (exit 2)
// before anything is read or sent.
func TestClusterConnectRefusesInputThePlatformWouldRefuse(t *testing.T) {
	cases := map[string][]string{
		"capital letters":      {"My-Cluster"},
		"starts with a digit":  {"1cluster"},
		"ends with a hyphen":   {"cluster-"},
		"over 53 characters":   {strings.Repeat("a", clusterNameLimit+1)},
		"environment too long": {"my-cluster", "--environment", strings.Repeat("e", clusterEnvironmentNameLimit+1)},
		"no time to wait":      {"my-cluster", "--timeout", "0s"},
	}
	for name, arguments := range cases {
		t.Run(name, func(subtest *testing.T) {
			mock := &clusterConnectMock{}

			_, runError := runClusterConnectCommand(subtest, mock, arguments...)
			if exitCodeFor(runError) != exitUsage {
				subtest.Fatalf("exit = %d (%v), want usage", exitCodeFor(runError), runError)
			}
			if len(mock.requests) != 0 {
				subtest.Fatalf("refused input was sent: %+v", mock.requests)
			}
		})
	}
}

// The platform's own refusal (no payment method, no permission) is the error
// the person reads.
func TestClusterConnectReportsThePlatformsRefusal(t *testing.T) {
	mock := &clusterConnectMock{applyError: errors.New("add a payment method before you connect a cluster")}

	_, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if runError == nil || !strings.Contains(runError.Error(), "add a payment method") {
		t.Fatalf("expected the platform's refusal, got %v", runError)
	}
}

// A script reads the cluster id and the command from -o json, and nothing
// else is on stdout.
func TestClusterConnectStructuredOutput(t *testing.T) {
	mock := &clusterConnectMock{}

	output, runError := runClusterConnectCommand(t, mock, "my-cluster", "-o", "json")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	var decoded clusterConnectOutput
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	want := clusterConnectOutput{ClusterID: clusterConnectTestClusterID, Name: "my-cluster",
		InstallCommand: clusterConnectTestInstallCommand}
	if decoded != want {
		t.Fatalf("decoded = %+v, want %+v", decoded, want)
	}
}

// The command is printed on one line, so it pastes as shown whatever line
// breaks the platform sent it with.
func TestClusterConnectPrintsTheInstallCommandOnOneLine(t *testing.T) {
	mock := &clusterConnectMock{response: &client.ImportResponse{Name: "my-cluster", ClusterId: clusterConnectTestClusterID,
		ImportCommand: "helm upgrade --install ankra-agent\n    oci://registry.ankra.cloud/ankra/ankra-agent\n    --namespace=ankra"}}

	output, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	if want := "  helm upgrade --install ankra-agent oci://registry.ankra.cloud/ankra/ankra-agent --namespace=ankra\n"; !strings.Contains(output, want) {
		t.Fatalf("output lacks the command on one line:\n%s", output)
	}
}

// The platform returns the install command only from the call that creates
// the cluster. An applied write without one means a cluster of this name was
// created between the name check and the write: the command says that, does
// not call the cluster registered, and names the command that prints the
// existing cluster's install command.
func TestClusterConnectReportsAClusterCreatedWhileItRan(t *testing.T) {
	mock := &clusterConnectMock{response: &client.ImportResponse{Name: "my-cluster", ClusterId: clusterConnectTestClusterID}}

	output, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if runError == nil {
		t.Fatalf("expected an error, got output:\n%s", output)
	}
	for _, want := range []string{
		"was created while this command ran",
		"instead of registering a new one",
		"ankra cluster agent token --cluster my-cluster",
	} {
		if !strings.Contains(runError.Error(), want) {
			t.Errorf("error lacks %q: %v", want, runError)
		}
	}
	assertClusterConnectSaysToChooseAnotherNameFirst(t, runError)
	if strings.Contains(output, clusterConnectSuccessLine) {
		t.Errorf("an update of another cluster was reported as a registration:\n%s", output)
	}
}

// A write the platform queued instead of applying has no command to print
// yet: the answer names the operation to follow and the command that prints
// the install command once the cluster is registered.
func TestClusterConnectSaysWhatToDoWhenThePlatformQueuedTheWrite(t *testing.T) {
	mock := &clusterConnectMock{submitted: true,
		response: &client.ImportResponse{OperationID: "0b7c4d2a-5f6a-4b8c-9d0e-1f2a3b4c5d6e"}}

	output, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if runError == nil {
		t.Fatalf("expected an error, got output:\n%s", output)
	}
	for _, want := range []string{
		"still registering",
		followOperationCommand("0b7c4d2a-5f6a-4b8c-9d0e-1f2a3b4c5d6e"),
		"ankra cluster agent token --cluster my-cluster",
	} {
		if !strings.Contains(runError.Error(), want) {
			t.Errorf("error lacks %q: %v", want, runError)
		}
	}
	if strings.Contains(output, clusterConnectSuccessLine) {
		t.Errorf("a queued write was reported as registered:\n%s", output)
	}
}

// A connect that outlives its deadline exits 5, the wait exit code, like
// every other waited write. The request was sent, so the answer does not say
// the cluster was not registered: it says how to find out and how to print
// the install command if it was.
func TestClusterConnectDeadlineIsAWaitTimeout(t *testing.T) {
	mock := &clusterConnectMock{applyError: context.DeadlineExceeded}

	output, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if exitCodeFor(runError) != exitWaitTimeout {
		t.Fatalf("exit = %d (%v), want the wait timeout", exitCodeFor(runError), runError)
	}
	for _, want := range []string{
		"may or may not be registered",
		"ankra cluster list",
		"ankra cluster agent token --cluster my-cluster",
	} {
		if !strings.Contains(runError.Error(), want) {
			t.Errorf("error lacks %q: %v", want, runError)
		}
	}
	if strings.Contains(output, clusterConnectSuccessLine) {
		t.Errorf("an unanswered write was reported as a registration:\n%s", output)
	}
}

// With -o json the platform's warnings are still shown, on stderr, and
// stdout stays the one document a script reads.
func TestClusterConnectStructuredOutputKeepsWarningsOnStderr(t *testing.T) {
	mock := &clusterConnectMock{response: &client.ImportResponse{Name: "my-cluster", ClusterId: clusterConnectTestClusterID,
		ImportCommand: clusterConnectTestInstallCommand,
		Warnings:      []client.ValidationWarning{{Message: "an advisory finding of the write"}}}}
	withTempHome(t)
	setMockClient(t, mock)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"cluster", "connect", "my-cluster", "-o", "json"})
	t.Cleanup(func() { resetTreeFlags(t, clusterConnectCmd) })

	if runError := rootCmd.Execute(); runError != nil {
		t.Fatalf("expected success, got %v", runError)
	}
	var decoded clusterConnectOutput
	if decodeError := json.Unmarshal(stdout.Bytes(), &decoded); decodeError != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", decodeError, stdout.String())
	}
	if decoded.ClusterID != clusterConnectTestClusterID {
		t.Fatalf("decoded = %+v", decoded)
	}
	if !strings.Contains(stderr.String(), "an advisory finding of the write") {
		t.Errorf("the warning is not on stderr:\n%s", stderr.String())
	}
}

// A reply that is only the notice of a waiting GitOps commit says nothing
// about whether this call created the cluster, so the command claims neither:
// it repeats the notice and names the command that prints the install command.
func TestClusterConnectRepeatsAWaitingGitOpsCommit(t *testing.T) {
	mock := &clusterConnectMock{response: &client.ImportResponse{GitPushDeferred: true,
		GitPushMessage: "The change is saved; the commit to Git is waiting on the background sync."}}

	output, runError := runClusterConnectCommand(t, mock, "my-cluster")
	if runError == nil {
		t.Fatalf("expected an error, got output:\n%s", output)
	}
	for _, want := range []string{
		"the commit to Git is waiting on the background sync",
		"ankra cluster agent token --cluster my-cluster",
	} {
		if !strings.Contains(runError.Error(), want) {
			t.Errorf("error lacks %q: %v", want, runError)
		}
	}
	for _, unwanted := range []string{"was created while this command ran", clusterConnectSuccessLine} {
		if strings.Contains(runError.Error(), unwanted) || strings.Contains(output, unwanted) {
			t.Errorf("a waiting commit was reported as %q: %v\n%s", unwanted, runError, output)
		}
	}
}

package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The four answers a named read can get (ankra-kb18b.16.4): present, not
// found, deleted from the cluster with the platform's last record still in
// items, and a read that failed. Only the first may print the object.

func deletedPodResponse() client.ResourceResponseItem {
	deletedAt := "2026-10-17T10:00:00Z"
	return client.ResourceResponseItem{
		Status: "success", Kind: "Pod", Version: "v1",
		Items: []interface{}{crashLoopingPod()},
		CacheMetadata: &client.ResourceCacheMetadata{
			ServedFromCache: true, SyncStatus: "agent_offline", IsDeleted: true, DeletedAt: &deletedAt,
		},
	}
}

func getPodsCommand(t *testing.T) *cobra.Command {
	t.Helper()
	for _, command := range clusterGetCmd.Commands() {
		if command.Name() == "pods" {
			return command
		}
	}
	t.Fatal("cluster get pods is not registered")
	return nil
}

func TestNamedReadItemsClassifiesEveryAnswer(t *testing.T) {
	forbidden := "pods \"web\" is forbidden: User cannot get resource"
	notFoundText := "Resource not found"
	empty := "  "
	cases := []struct {
		name           string
		item           client.ResourceResponseItem
		wantItems      bool
		wantExitCode   int
		wantMessageHas string
	}{
		{name: "present", item: client.ResourceResponseItem{Status: "success", Items: []interface{}{crashLoopingPod()}},
			wantItems: true},
		{name: "apiserver 404", item: client.ResourceResponseItem{Status: "error", NotFound: true, ErrorResponse: &notFoundText},
			wantExitCode: exitNotFound, wantMessageHas: `Pod "web" not found in namespace default`},
		{name: "apiserver 404 from a platform without the not_found flag",
			item:         client.ResourceResponseItem{Status: "error", ErrorResponse: &notFoundText},
			wantExitCode: exitNotFound, wantMessageHas: `Pod "web" not found in namespace default`},
		{name: "deleted", item: deletedPodResponse(),
			wantExitCode: exitNotFound, wantMessageHas: "was deleted from the cluster at 2026-10-17T10:00:00Z"},
		{name: "failed read", item: client.ResourceResponseItem{Status: "error", ErrorResponse: &forbidden},
			wantExitCode: exitError, wantMessageHas: "is forbidden"},
		{name: "failed read without a reason", item: client.ResourceResponseItem{Status: "error", ErrorResponse: &empty},
			wantExitCode: exitError, wantMessageHas: "the platform gave no reason"},
		{name: "empty answer", item: client.ResourceResponseItem{Status: "success"},
			wantExitCode: exitNotFound, wantMessageHas: "not found"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := &client.GetResourcesResponse{ResourceResponses: []client.ResourceResponseItem{testCase.item}}
			items, readError := namedReadItems(response, "Pod", "web", "default")
			if testCase.wantItems {
				if readError != nil || len(items) != 1 {
					t.Fatalf("a present object must be returned, got %v items, error %v", len(items), readError)
				}
				return
			}
			if readError == nil {
				t.Fatalf("expected a refusal, got %d items", len(items))
			}
			if got := exitCodeFor(readError); got != testCase.wantExitCode {
				t.Errorf("exit code %d, want %d (%v)", got, testCase.wantExitCode, readError)
			}
			if !strings.Contains(readError.Error(), testCase.wantMessageHas) {
				t.Errorf("message %q does not contain %q", readError.Error(), testCase.wantMessageHas)
			}
		})
	}
}

func TestDescribeDeletedResourceExitsNotFoundWithoutItsOldBody(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = deletedPodResponse()
	setMockClient(t, mock)

	var executeError error
	output := captureStdout(t, func() {
		_, executeError = executeCommand("cluster", "describe", "pod", "web-7d9f-2xkvp", "-n", "default")
	})
	if got := exitCodeFor(executeError); got != exitNotFound {
		t.Fatalf("describing a deleted pod should exit %d, got %d (%v)", exitNotFound, got, executeError)
	}
	if !strings.Contains(executeError.Error(), "was deleted from the cluster") {
		t.Errorf("the error must say the pod was deleted, got %v", executeError)
	}
	if strings.Contains(output, "CrashLoopBackOff") {
		t.Errorf("a deleted pod's last body must not be printed as live:\n%s", output)
	}
}

func TestDescribeFailedReadIsNotReportedAsMissing(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterDescribeCmd)
	mock := newDebugVerbsMock()
	forbidden := "pods \"web-7d9f-2xkvp\" is forbidden"
	mock.responses["Pod"] = client.ResourceResponseItem{Status: "error", ErrorResponse: &forbidden}
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "describe", "pod", "web-7d9f-2xkvp", "-n", "default")
	if got := exitCodeFor(executeError); got == exitNotFound || got == 0 {
		t.Fatalf("a refused read is neither a success nor a missing object, got exit %d (%v)", got, executeError)
	}
	if !strings.Contains(executeError.Error(), "is forbidden") {
		t.Errorf("the platform's reason must reach the user, got %v", executeError)
	}
}

func TestGetNamedDeletedPodExitsNotFound(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, getPodsCommand(t))
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = deletedPodResponse()
	setMockClient(t, mock)

	var executeError error
	output := captureStdout(t, func() {
		_, executeError = executeCommand("cluster", "get", "pods", "web-7d9f-2xkvp", "-n", "default")
	})
	if got := exitCodeFor(executeError); got != exitNotFound {
		t.Fatalf("getting a deleted pod should exit %d, got %d (%v)", exitNotFound, got, executeError)
	}
	if strings.Contains(output, "Running") {
		t.Errorf("a deleted pod must not be rendered as running:\n%s", output)
	}
}

func TestLogsAllContainersOfADeletedPodExitsNotFound(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	mock.responses["Pod"] = deletedPodResponse()
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "logs", "web-7d9f-2xkvp", "-n", "default",
		"--all-containers", "--follow=false")
	if got := exitCodeFor(executeError); got != exitNotFound {
		t.Fatalf("logs of a deleted pod should exit %d, got %d (%v)", exitNotFound, got, executeError)
	}
	if len(mock.logOptions) != 0 {
		t.Errorf("no log stream may be opened for a deleted pod, got %d", len(mock.logOptions))
	}
}

func TestLogsSelectorReadThatFailedIsNotReportedAsNoMatch(t *testing.T) {
	writeSelectedClusterJSON(t)
	resetCommandFlags(t, clusterLogsCmd)
	mock := newDebugVerbsMock()
	forbidden := "pods is forbidden: User cannot list resource"
	mock.responses["Pod"] = client.ResourceResponseItem{Status: "error", ErrorResponse: &forbidden}
	setMockClient(t, mock)

	_, executeError := executeCommand("cluster", "logs", "-l", "app=web", "-n", "default", "--follow=false")
	if got := exitCodeFor(executeError); got != exitError {
		t.Fatalf("a refused selector read should exit %d, got %d (%v)", exitError, got, executeError)
	}
	if !strings.Contains(executeError.Error(), "is forbidden") {
		t.Errorf("the platform's reason must reach the user, got %v", executeError)
	}
}

func TestNamedReadWithNoItemInTheReplyIsAFailedReadNotNotFound(t *testing.T) {
	_, readError := namedReadItems(&client.GetResourcesResponse{}, "Pod", "web", "default")
	if got := exitCodeFor(readError); got != exitError {
		t.Fatalf("a reply carrying no item is not an answer about the object, want exit %d, got %d (%v)",
			exitError, got, readError)
	}
}

func TestGetListThatFailedIsNotReportedAsEmpty(t *testing.T) {
	writeSelectedClusterJSON(t)
	var deploymentsCommand *cobra.Command
	for _, command := range clusterGetCmd.Commands() {
		if command.Name() == "deployments" {
			deploymentsCommand = command
		}
	}
	if deploymentsCommand == nil {
		t.Fatal("cluster get deployments is not registered")
	}
	resetCommandFlags(t, deploymentsCommand)
	mock := newDebugVerbsMock()
	forbidden := "deployments.apps is forbidden: User cannot list resource"
	mock.responses["Deployment"] = client.ResourceResponseItem{Status: "error", ErrorResponse: &forbidden}
	setMockClient(t, mock)

	var executeError error
	output := captureStdout(t, func() {
		_, executeError = executeCommand("cluster", "get", "deployments", "-n", "default")
	})
	if got := exitCodeFor(executeError); got != exitError {
		t.Fatalf("a refused listing should exit %d, got %d (%v)", exitError, got, executeError)
	}
	if strings.Contains(output, "No deployments found") {
		t.Fatalf("a refused listing must not read as an empty one:\n%s", output)
	}
	if !strings.Contains(executeError.Error(), "is forbidden") {
		t.Errorf("the platform's reason must reach the user, got %v", executeError)
	}
}

// noItemMock answers every resources/get with a reply carrying no item at
// all, which the wire contract never sends for a request item.
type noItemMock struct{ debugVerbsMock }

func (mock *noItemMock) GetResources(string, client.GetResourcesRequest) (*client.GetResourcesResponse, error) {
	return &client.GetResourcesResponse{}, nil
}

func TestListingsWithNoItemInTheReplyAreFailedReadsNotEmpty(t *testing.T) {
	writeSelectedClusterJSON(t)
	deploymentsCommand := findClusterGetSubcommand(t, "deployments")
	resetCommandFlags(t, deploymentsCommand)
	resetCommandFlags(t, clusterLogsCmd)
	setMockClient(t, &noItemMock{debugVerbsMock: *newDebugVerbsMock()})

	var getError error
	output := captureStdout(t, func() {
		_, getError = executeCommand("cluster", "get", "deployments", "-n", "default")
	})
	if got := exitCodeFor(getError); got != exitError || strings.Contains(output, "No deployments found") {
		t.Fatalf("a listing the reply never answered must exit %d without 'No deployments found', got %d (%v):\n%s",
			exitError, got, getError, output)
	}
	_, logsError := executeCommand("cluster", "logs", "-l", "app=web", "-n", "default", "--follow=false")
	if got := exitCodeFor(logsError); got != exitError {
		t.Fatalf("a selector the reply never answered must exit %d, not 'no pods match', got %d (%v)",
			exitError, got, logsError)
	}
}

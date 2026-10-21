package cmd

// Tests for commands run against a cluster of the wrong kind: node groups,
// SSH keys, worker scaling and Kubernetes upgrades on a cluster Ankra did not
// build, `ankra delete cluster` on a cloud cluster, and every
// `ankra cluster <provider> ...` command on a cluster of another provider.
// Each is a usage error (exit 2) naming the cluster's kind, the same class as
// `ankra cluster ankracloud quota` on a cluster that is not an Ankra Cloud one.

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type wrongKindMock struct {
	baseMock

	cluster     client.ClusterListItem
	lookupError error
	workerError error
	lookups     int
}

func (m *wrongKindMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	m.lookups++
	if m.lookupError != nil {
		return client.ClusterListItem{}, m.lookupError
	}
	return m.cluster, nil
}

func (m *wrongKindMock) GetCluster(name string) (client.ClusterListItem, error) {
	return m.cluster, nil
}

func (m *wrongKindMock) GetHetznerWorkerCount(clusterID string) (*client.WorkerCountResult, error) {
	if m.workerError != nil {
		return nil, m.workerError
	}
	return &client.WorkerCountResult{WorkerCount: 3, Min: 1, Max: 5}, nil
}

func TestCommandsOnAClusterOfTheWrongKindExitTwoNamingTheKind(t *testing.T) {
	writeSelectedClusterJSON(t)
	imported := client.ClusterListItem{ID: testClusterID, Name: "lab", Kind: "imported"}
	for name, arguments := range map[string][]string{
		"node-group list":            {"cluster", "node-group", "list", testClusterID},
		"node-group scale":           {"cluster", "node-group", "scale", testClusterID, "workers", "3"},
		"node-group autoscaling get": {"cluster", "node-group", "autoscaling", "get", testClusterID, "workers"},
		"ssh-keys get":               {"cluster", "ssh-keys", "get", testClusterID},
		"ssh-keys resync":            {"cluster", "ssh-keys", "resync", testClusterID},
		"scale":                      {"cluster", "scale", testClusterID, "3"},
		"upgrade":                    {"cluster", "upgrade", testClusterID, "v1.33.2"},
		"node-group autoscaling set": {"cluster", "node-group", "autoscaling", "set", testClusterID, "workers", "--enabled=false"},
		"node-group delete (--yes)":  {"cluster", "node-group", "delete", testClusterID, "workers", "--yes"},
	} {
		setMockClient(t, &wrongKindMock{cluster: imported})
		_, runError := executeCommand(arguments...)
		if runError == nil || exitCodeFor(runError) != exitUsage {
			t.Fatalf("%s on an imported cluster must exit 2, got %v (exit %d)", name, runError, exitCodeFor(runError))
		}
		message := runError.Error()
		if !strings.HasPrefix(message, testClusterID+" does not support ") ||
			!strings.Contains(message, "(its kind is imported)") ||
			!strings.HasSuffix(message, "only "+cloudClusterKindsSentence+" clusters do") {
			t.Fatalf("%s: the refusal must name the cluster and its kind in a plain sentence, got %q", name, message)
		}
	}
}

func TestUnsupportedClusterKindErrorSentence(t *testing.T) {
	got := unsupportedClusterKindError("lab", "imported", "node groups")
	want := "lab does not support node groups (its kind is imported); only Hetzner, OVH, UpCloud, DigitalOcean, Scaleway, Ankra Cloud, AWS, Proxmox VE and HPE Morpheus clusters do"
	if got.Error() != want || exitCodeFor(got) != exitUsage {
		t.Fatalf("got %q (exit %d), want %q (exit 2)", got, exitCodeFor(got), want)
	}
}

func TestAnUnreportedKindIsNotCalledTheWrongKind(t *testing.T) {
	got := unsupportedClusterKindError("lab", "", "node groups")
	want := "the platform did not report what kind of cluster lab is, so node groups cannot be used on it"
	if got.Error() != want || exitCodeFor(got) != exitError {
		t.Fatalf("got %q (exit %d), want %q (exit 1)", got, exitCodeFor(got), want)
	}

	writeSelectedClusterJSON(t)
	setMockClient(t, &wrongKindMock{cluster: client.ClusterListItem{ID: testClusterID}})
	_, runError := executeCommand("cluster", "node-group", "list", testClusterID)
	if runError == nil || exitCodeFor(runError) != exitError || strings.Contains(runError.Error(), "its kind is") {
		t.Fatalf("an empty kind must not read as a wrong kind, got %v (exit %d)", runError, exitCodeFor(runError))
	}
}

func TestUnsupportedAutoscalingKindNamesNoSupportedKinds(t *testing.T) {
	got := unsupportedAutoscalingKindError("lab", "proxmox")
	want := "lab does not support node-group autoscaling (its kind is proxmox)"
	if got.Error() != want || exitCodeFor(got) != exitUsage {
		t.Fatalf("got %q (exit %d), want %q (exit 2)", got, exitCodeFor(got), want)
	}
}

func TestDeleteClusterRefusesACloudClusterWithExitTwo(t *testing.T) {
	originalForce := forceDelete
	forceDelete = true
	t.Cleanup(func() { forceDelete = originalForce })
	setMockClient(t, &wrongKindMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "games", Kind: "hetzner"}})

	var runError error
	output := captureStdout(t, func() {
		runError = deleteClusterCmd.RunE(deleteClusterCmd, []string{"games"})
	})
	want := "games is a cloud cluster (its kind is hetzner), which `ankra delete cluster` cannot remove; " +
		"run `ankra cluster deprovision " + testClusterID + "` instead so its cloud resources are released"
	if runError == nil || runError.Error() != want || exitCodeFor(runError) != exitUsage {
		t.Fatalf("a Hetzner cluster must be refused with exit 2:\n got %v (exit %d)\nwant %q", runError, exitCodeFor(runError), want)
	}
	if strings.Contains(output, "deprovision") {
		t.Fatalf("the refusal belongs on stderr, not stdout: %q", output)
	}
}

func TestProviderCommandOnAnotherProvidersClusterExitsTwo(t *testing.T) {
	writeSelectedClusterJSON(t)
	wrapProviderKindRefusals(rootCmd)
	conflict := client.NewUnexpectedResponseError(409, "unexpected status: 409 Conflict")
	mock := &wrongKindMock{workerError: conflict, cluster: client.ClusterListItem{ID: testClusterID, Name: "ci", Kind: "ovh"}}
	setMockClient(t, mock)

	_, runError := executeCommand("cluster", "hetzner", "workers", testClusterID)
	want := testClusterID + " is not a Hetzner cluster (its kind is ovh); `ankra cluster hetzner workers` applies to Hetzner clusters only." +
		" For this cluster use `ankra cluster ovh workers`"
	if runError == nil || runError.Error() != want || exitCodeFor(runError) != exitUsage {
		t.Fatalf("an OVH cluster must be refused with exit 2 and a plain sentence:\n got %v (exit %d)\nwant %q", runError, exitCodeFor(runError), want)
	}
	if strings.Contains(runError.Error(), "409") {
		t.Fatalf("the refusal must not carry the raw 409: %v", runError)
	}

	// A kind with no provider group of its own gets no alternative.
	setMockClient(t, &wrongKindMock{workerError: conflict, cluster: client.ClusterListItem{ID: testClusterID, Kind: "imported"}})
	_, runError = executeCommand("cluster", "hetzner", "workers", testClusterID)
	if runError == nil || exitCodeFor(runError) != exitUsage || strings.Contains(runError.Error(), "For this cluster use") ||
		!strings.Contains(runError.Error(), "(its kind is imported)") {
		t.Fatalf("an imported cluster must be refused with exit 2 and no alternative, got %v (exit %d)", runError, exitCodeFor(runError))
	}
}

func TestProviderCommandKeepsThePlatformAnswerWhenTheKindDoesNotExplainIt(t *testing.T) {
	writeSelectedClusterJSON(t)
	wrapProviderKindRefusals(rootCmd)
	conflict := client.NewUnexpectedResponseError(409, "unexpected status: 409 Conflict")
	for name, mock := range map[string]*wrongKindMock{
		"a Hetzner cluster":  {workerError: conflict, cluster: client.ClusterListItem{ID: testClusterID, Kind: "hetzner"}},
		"an unreadable kind": {workerError: conflict, lookupError: errors.New("listing clusters failed")},
		"an empty kind":      {workerError: conflict, cluster: client.ClusterListItem{ID: testClusterID}},
	} {
		setMockClient(t, mock)
		_, runError := executeCommand("cluster", "hetzner", "workers", testClusterID)
		if runError == nil || !strings.Contains(runError.Error(), "409") || exitCodeFor(runError) != exitError {
			t.Fatalf("%s: the platform's answer must be kept (exit 1), got %v (exit %d)", name, runError, exitCodeFor(runError))
		}
	}

	serverError := &wrongKindMock{workerError: client.NewUnexpectedResponseError(502, "unexpected status: 502 Bad Gateway"),
		cluster: client.ClusterListItem{ID: testClusterID, Kind: "ovh"}}
	setMockClient(t, serverError)
	if _, runError := executeCommand("cluster", "hetzner", "workers", testClusterID); runError == nil ||
		exitCodeFor(runError) != exitError || serverError.lookups != 0 {
		t.Fatalf("a server error must not read the kind: got %v (exit %d) after %d lookups", runError, exitCodeFor(runError), serverError.lookups)
	}

	success := &wrongKindMock{cluster: client.ClusterListItem{ID: testClusterID, Kind: "hetzner"}}
	setMockClient(t, success)
	if _, runError := executeCommand("cluster", "hetzner", "workers", testClusterID); runError != nil || success.lookups != 0 {
		t.Fatalf("a successful run must cost no extra request: %v after %d lookups", runError, success.lookups)
	}
}

// notAClusterArgument lists the first positional arguments of provider
// group commands that are not a cluster. Any other argument list must start
// with a cluster and be wrapped, so a command that names its cluster argument
// differently ("[cluster]", "<id>") fails here instead of silently keeping
// the raw 409 and exit 1.
var notAClusterArgument = map[string]bool{
	"<credential-id>": true,
}

func TestEveryProviderClusterCommandIsWrapped(t *testing.T) {
	wrapProviderKindRefusals(rootCmd)
	wrapProviderKindRefusals(rootCmd) // a second call must not stack wrappers
	wrapped := 0
	var walk func(command *cobra.Command, family string)
	walk = func(command *cobra.Command, family string) {
		fields := strings.Fields(command.Use)
		if command.RunE != nil && len(fields) > 1 && !strings.HasPrefix(fields[1], "-") && !notAClusterArgument[fields[1]] {
			if !takesClusterFirst(command) {
				t.Errorf("%s takes %s first: name it <cluster_id|name> or add it to notAClusterArgument", command.CommandPath(), fields[1])
			}
			if command.Annotations[providerKindRefusalAnnotation] != family {
				t.Errorf("%s takes a cluster but is not wrapped", command.CommandPath())
			}
			wrapped++
		}
		for _, sub := range command.Commands() {
			walk(sub, family)
		}
	}
	clusterCommand, _, findError := rootCmd.Find([]string{"cluster"})
	if findError != nil {
		t.Fatalf("finding the cluster command: %v", findError)
	}
	for _, familyCommand := range clusterCommand.Commands() {
		if _, known := providerFamilies[familyCommand.Name()]; known {
			walk(familyCommand, familyCommand.Name())
		}
	}
	if wrapped < 50 {
		t.Fatalf("expected the provider groups' cluster commands to be wrapped, found only %d", wrapped)
	}
}

package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// resetTreeFlags restores every flag on the given commands (and the shared
// persistent --cluster override) to its default so the package-level rootCmd
// does not leak state between confirm-prompt test cases.
func resetTreeFlags(t *testing.T, cmds ...*cobra.Command) {
	t.Helper()
	cmds = append(cmds, clusterCmd)
	for _, command := range cmds {
		command.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
		command.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	}
}

// runConfirmCommand executes rootCmd end-to-end with the given stdin, capturing
// combined output. It mirrors runSupportWithInput but is scoped to the
// destructive-confirmation commands under test here.
func runConfirmCommand(t *testing.T, mock APIClient, input string, resets []*cobra.Command, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	out := new(bytes.Buffer)
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(args)
	t.Cleanup(func() { resetTreeFlags(t, resets...) })
	err := rootCmd.Execute()
	return out.String(), err
}

// --- cluster stacks delete ------------------------------------------------

type stacksDeleteMock struct {
	baseMock
	cluster     client.ClusterListItem
	deleteCalls int
}

func (m *stacksDeleteMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.cluster.Name == name || m.cluster.ID == name {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *stacksDeleteMock) DeleteStack(ctx context.Context, clusterID, stackName string) (*client.DeleteStackResult, error) {
	m.deleteCalls++
	return &client.DeleteStackResult{Success: true}, nil
}

func TestClusterStacksDelete_DeclineDoesNotDelete(t *testing.T) {
	mock := &stacksDeleteMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo"}}
	_, err := runConfirmCommand(t, mock, "n\n",
		[]*cobra.Command{clusterStacksDeleteCmd},
		"cluster", "stacks", "delete", "platform", "--cluster", "demo")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if got := exitCodeFor(err); got != exitCancelled {
		t.Errorf("expected exit code %d, got %d", exitCancelled, got)
	}
	if mock.deleteCalls != 0 {
		t.Errorf("expected no delete call on decline, got %d", mock.deleteCalls)
	}
}

func TestClusterStacksDelete_YesFlagProceeds(t *testing.T) {
	mock := &stacksDeleteMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo"}}
	_, err := runConfirmCommand(t, mock, "",
		[]*cobra.Command{clusterStacksDeleteCmd},
		"cluster", "stacks", "delete", "platform", "--cluster", "demo", "--yes")
	if err != nil {
		t.Fatalf("expected success with --yes, got %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Errorf("expected one delete call with --yes, got %d", mock.deleteCalls)
	}
}

func TestClusterStacksDelete_PipedYesProceeds(t *testing.T) {
	mock := &stacksDeleteMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo"}}
	_, err := runConfirmCommand(t, mock, "y\n",
		[]*cobra.Command{clusterStacksDeleteCmd},
		"cluster", "stacks", "delete", "platform", "--cluster", "demo")
	if err != nil {
		t.Fatalf("expected success with piped y, got %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Errorf("expected one delete call with piped y, got %d", mock.deleteCalls)
	}
}

// --- cluster stacks list <name> not found ---------------------------------

type stacksListMock struct {
	baseMock
	cluster client.ClusterListItem
	stacks  []client.ClusterStackListItem
}

func (m *stacksListMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.cluster.Name == name || m.cluster.ID == name {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *stacksListMock) ListClusterStacks(clusterID string) ([]client.ClusterStackListItem, error) {
	return m.stacks, nil
}

func TestClusterStacksList_NotFoundUsesExitNotFound(t *testing.T) {
	mock := &stacksListMock{
		cluster: client.ClusterListItem{ID: "c-1", Name: "demo"},
		stacks:  []client.ClusterStackListItem{{Name: "other"}},
	}
	_, err := runConfirmCommand(t, mock, "",
		[]*cobra.Command{clusterStacksListCmd},
		"cluster", "stacks", "list", "missing", "--cluster", "demo")
	if err == nil {
		t.Fatal("expected an error for a missing stack")
	}
	if got := exitCodeFor(err); got != exitNotFound {
		t.Errorf("expected exit code %d for missing stack, got %d", exitNotFound, got)
	}
}

// --- cluster deprovision --------------------------------------------------

type deprovisionMock struct {
	baseMock
	cluster     client.ClusterListItem
	deleteCalls int
}

func (m *deprovisionMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.cluster.ID == clusterID {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *deprovisionMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.cluster.Name == name || m.cluster.ID == name {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *deprovisionMock) DeprovisionCluster(ctx context.Context, clusterID string, autoDelete, force bool) (*client.DeprovisionClusterResult, error) {
	m.deleteCalls++
	return &client.DeprovisionClusterResult{}, nil
}

func TestClusterDeprovision_DeclineDoesNotDeprovision(t *testing.T) {
	// Kind is intentionally empty so the generic deprovision path is used.
	mock := &deprovisionMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo"}}
	_, err := runConfirmCommand(t, mock, "n\n",
		[]*cobra.Command{clusterDeprovisionCmd},
		"cluster", "deprovision", "demo")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if got := exitCodeFor(err); got != exitCancelled {
		t.Errorf("expected exit code %d, got %d", exitCancelled, got)
	}
	if mock.deleteCalls != 0 {
		t.Errorf("expected no deprovision call on decline, got %d", mock.deleteCalls)
	}
}

func TestClusterDeprovision_YesFlagProceeds(t *testing.T) {
	mock := &deprovisionMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo"}}
	_, err := runConfirmCommand(t, mock, "",
		[]*cobra.Command{clusterDeprovisionCmd},
		"cluster", "deprovision", "demo", "--yes")
	if err != nil {
		t.Fatalf("expected success with --yes, got %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Errorf("expected one deprovision call with --yes, got %d", mock.deleteCalls)
	}
}

func TestClusterDeprovision_PipedYesProceeds(t *testing.T) {
	mock := &deprovisionMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo"}}
	_, err := runConfirmCommand(t, mock, "y\n",
		[]*cobra.Command{clusterDeprovisionCmd},
		"cluster", "deprovision", "demo")
	if err != nil {
		t.Fatalf("expected success with piped y, got %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Errorf("expected one deprovision call with piped y, got %d", mock.deleteCalls)
	}
}

// --- cluster node-group delete --------------------------------------------

type nodeGroupDeleteMock struct {
	baseMock
	cluster     client.ClusterListItem
	deleteCalls int
}

func (m *nodeGroupDeleteMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.cluster.ID == clusterID {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, errors.New("not found")
}

func (m *nodeGroupDeleteMock) DeleteHetznerNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*client.DeleteNodeGroupResult, bool, error) {
	m.deleteCalls++
	return &client.DeleteNodeGroupResult{GroupName: groupName, Deleted: 1}, false, nil
}

func TestClusterNodeGroupDelete_DeclineDoesNotDelete(t *testing.T) {
	mock := &nodeGroupDeleteMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo", Kind: "hetzner"}}
	_, err := runConfirmCommand(t, mock, "n\n",
		[]*cobra.Command{clusterNodeGroupDeleteCmd},
		"cluster", "node-group", "delete", "c-1", "workers")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if got := exitCodeFor(err); got != exitCancelled {
		t.Errorf("expected exit code %d, got %d", exitCancelled, got)
	}
	if mock.deleteCalls != 0 {
		t.Errorf("expected no delete call on decline, got %d", mock.deleteCalls)
	}
}

func TestClusterNodeGroupDelete_YesFlagProceeds(t *testing.T) {
	mock := &nodeGroupDeleteMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo", Kind: "hetzner"}}
	_, err := runConfirmCommand(t, mock, "",
		[]*cobra.Command{clusterNodeGroupDeleteCmd},
		"cluster", "node-group", "delete", "c-1", "workers", "--yes")
	if err != nil {
		t.Fatalf("expected success with --yes, got %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Errorf("expected one delete call with --yes, got %d", mock.deleteCalls)
	}
}

func TestClusterNodeGroupDelete_PipedYesProceeds(t *testing.T) {
	mock := &nodeGroupDeleteMock{cluster: client.ClusterListItem{ID: "c-1", Name: "demo", Kind: "hetzner"}}
	_, err := runConfirmCommand(t, mock, "y\n",
		[]*cobra.Command{clusterNodeGroupDeleteCmd},
		"cluster", "node-group", "delete", "c-1", "workers")
	if err != nil {
		t.Fatalf("expected success with piped y, got %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Errorf("expected one delete call with piped y, got %d", mock.deleteCalls)
	}
}

// --- credentials delete ----------------------------------------------------

type credentialsDeleteMock struct {
	baseMock
	deleteCalls int
}

func (m *credentialsDeleteMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	return []client.OrganisationSummary{{OrganisationID: "org-1", UserCurrent: true}}, nil
}

func (m *credentialsDeleteMock) DeleteCredential(ctx context.Context, credentialID, organisationID string) (*client.DeleteCredentialResult, error) {
	m.deleteCalls++
	return &client.DeleteCredentialResult{Success: true}, nil
}

func TestCredentialsDelete_DeclineDoesNotDelete(t *testing.T) {
	mock := &credentialsDeleteMock{}
	_, err := runConfirmCommand(t, mock, "n\n",
		[]*cobra.Command{credentialsDeleteCmd},
		"credentials", "delete", "cred-1")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if got := exitCodeFor(err); got != exitCancelled {
		t.Errorf("expected exit code %d, got %d", exitCancelled, got)
	}
	if mock.deleteCalls != 0 {
		t.Errorf("expected no delete call on decline, got %d", mock.deleteCalls)
	}
}

func TestCredentialsDelete_YesFlagProceeds(t *testing.T) {
	mock := &credentialsDeleteMock{}
	_, err := runConfirmCommand(t, mock, "",
		[]*cobra.Command{credentialsDeleteCmd},
		"credentials", "delete", "cred-1", "--yes")
	if err != nil {
		t.Fatalf("expected success with --yes, got %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Errorf("expected one delete call with --yes, got %d", mock.deleteCalls)
	}
}

func TestCredentialsDelete_PipedYesProceeds(t *testing.T) {
	mock := &credentialsDeleteMock{}
	_, err := runConfirmCommand(t, mock, "y\n",
		[]*cobra.Command{credentialsDeleteCmd},
		"credentials", "delete", "cred-1")
	if err != nil {
		t.Fatalf("expected success with piped y, got %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Errorf("expected one delete call with piped y, got %d", mock.deleteCalls)
	}
}

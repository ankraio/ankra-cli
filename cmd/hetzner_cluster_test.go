package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type hetznerDeprovisionMock struct {
	baseMock
	called       bool
	gotClusterID string
	gotForce     bool
}

func (m *hetznerDeprovisionMock) DeprovisionHetznerCluster(clusterID string, force bool) (*client.DeprovisionHetznerClusterResponse, error) {
	m.called = true
	m.gotClusterID = clusterID
	m.gotForce = force
	return &client.DeprovisionHetznerClusterResponse{Success: true, ClusterID: clusterID}, nil
}

type hetznerNodeGroupDeleteMock struct {
	baseMock
	called       bool
	gotClusterID string
	gotGroupName string
}

func (m *hetznerNodeGroupDeleteMock) DeleteHetznerNodeGroup(ctx context.Context, clusterID, groupName string, wait bool) (*client.DeleteNodeGroupResult, bool, error) {
	m.called = true
	m.gotClusterID = clusterID
	m.gotGroupName = groupName
	return &client.DeleteNodeGroupResult{GroupName: groupName, Deleted: 1}, false, nil
}

// resetConfirmFlag clears the flags on the given commands to their defaults
// before and after the test. The reset runs on cleanup as well so a persistent
// flag set here (e.g. --cluster on clusterCmd) does not leak into later tests.
func resetConfirmFlag(t *testing.T, commands ...*cobra.Command) {
	t.Helper()
	reset := func() {
		for _, command := range commands {
			command.Flags().VisitAll(func(flag *pflag.Flag) {
				_ = flag.Value.Set(flag.DefValue)
				flag.Changed = false
			})
		}
	}
	reset()
	t.Cleanup(reset)
}

func runWithInput(t *testing.T, mock APIClient, input string, args ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	out := &strings.Builder{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

func TestHetznerDeprovision_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &hetznerDeprovisionMock{}
	resetConfirmFlag(t, hetznerDeprovisionCmd)
	_, err := runWithInput(t, mock, "n\n", "cluster", "hetzner", "deprovision", "hz-123")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if mock.called {
		t.Error("expected no deprovision call when declined")
	}
}

func TestHetznerDeprovision_YesProceeds(t *testing.T) {
	mock := &hetznerDeprovisionMock{}
	resetConfirmFlag(t, hetznerDeprovisionCmd)
	out, err := runWithInput(t, mock, "", "cluster", "hetzner", "deprovision", "hz-123", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !mock.called {
		t.Fatal("expected deprovision call with --yes")
	}
	if mock.gotClusterID != "hz-123" {
		t.Errorf("cluster id = %q, want hz-123", mock.gotClusterID)
	}
}

func TestHetznerDeprovision_YesPreservesForce(t *testing.T) {
	mock := &hetznerDeprovisionMock{}
	resetConfirmFlag(t, hetznerDeprovisionCmd)
	_, err := runWithInput(t, mock, "", "cluster", "hetzner", "deprovision", "hz-123", "--yes", "--force")
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !mock.gotForce {
		t.Error("expected --force to still reach the API alongside --yes")
	}
}

func TestHetznerNodeGroupDelete_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &hetznerNodeGroupDeleteMock{}
	resetConfirmFlag(t, nodeGroupDeleteCmd)
	_, err := runWithInput(t, mock, "n\n", "cluster", "hetzner", "node-group", "delete", "hz-123", "workers")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if mock.called {
		t.Error("expected no delete call when declined")
	}
}

func TestHetznerNodeGroupDelete_YesProceeds(t *testing.T) {
	mock := &hetznerNodeGroupDeleteMock{}
	resetConfirmFlag(t, nodeGroupDeleteCmd)
	out, err := runWithInput(t, mock, "", "cluster", "hetzner", "node-group", "delete", "hz-123", "workers", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !mock.called {
		t.Fatal("expected delete call with --yes")
	}
	if mock.gotClusterID != "hz-123" || mock.gotGroupName != "workers" {
		t.Errorf("got cluster=%q group=%q, want hz-123/workers", mock.gotClusterID, mock.gotGroupName)
	}
}

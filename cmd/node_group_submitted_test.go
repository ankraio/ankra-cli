package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// A node-group write answered 202 can still be refused or fail afterwards
// (ankra-adfdf6.1). The platform records that as a failed node_group_write
// operation, but its 202 body names no operation, so the CLI points at the
// failed-operations listing instead of leaving "Re-run with --wait" as the
// only trace.

type nodeGroupSubmittedMock struct {
	baseMock
	resizeError error
}

func (m *nodeGroupSubmittedMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: clusterID, Name: "demo", Kind: "hetzner"}, nil
}

func (m *nodeGroupSubmittedMock) UpdateHetznerNodeGroupInstanceType(ctx context.Context, clusterID, groupName, instanceType string, drainOptions client.DrainOptions, wait bool) (*client.UpdateNodeGroupResult, bool, error) {
	if m.resizeError != nil {
		return nil, false, m.resizeError
	}
	return nil, true, nil
}

func (m *nodeGroupSubmittedMock) ScaleHetznerNodeGroup(ctx context.Context, clusterID, groupName string, count int, drainOptions client.DrainOptions, wait bool) (*client.ScaleNodeGroupResult, bool, error) {
	return nil, true, nil
}

func TestNodeGroupSubmittedPointsAtTheFailedOperationRecord(t *testing.T) {
	wantFollow := "ankra cluster operations list --failed --cluster " + testClusterID
	for name, arguments := range map[string][]string{
		"upgrade": {"cluster", "node-group", "upgrade", testClusterID, "workers", "cpx32"},
		"scale":   {"cluster", "node-group", "scale", testClusterID, "workers", "2"},
	} {
		t.Run(name, func(t *testing.T) {
			var runError error
			stdout := captureStdout(t, func() {
				_, runError = runConfirmCommand(t, &nodeGroupSubmittedMock{}, "",
					[]*cobra.Command{clusterNodeGroupUpgradeCmd, clusterNodeGroupScaleCmd}, arguments...)
			})
			if runError != nil {
				t.Fatalf("execute failed: %v", runError)
			}
			if !strings.Contains(stdout, "Re-run the same command with --wait") {
				t.Errorf("the --wait guidance must stay:\n%s", stdout)
			}
			if !strings.Contains(stdout, wantFollow) {
				t.Errorf("a submitted node-group write must point at %q:\n%s", wantFollow, stdout)
			}
			if !strings.Contains(stdout, "provider's reason") {
				t.Errorf("the output must say the record carries the provider's reason:\n%s", stdout)
			}
		})
	}
}

func TestNodeGroupSubmittedStructuredHintNamesTheFailedOperationListing(t *testing.T) {
	var runError error
	stdout := captureStdout(t, func() {
		_, runError = runConfirmCommand(t, &nodeGroupSubmittedMock{}, "",
			[]*cobra.Command{clusterNodeGroupUpgradeCmd},
			"cluster", "node-group", "upgrade", testClusterID, "workers", "cpx32", "-o", "json")
	})
	if runError != nil {
		t.Fatalf("execute failed: %v", runError)
	}
	var result asyncSubmittedResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout must stay parseable JSON, got %q: %v", stdout, err)
	}
	if !result.Submitted || !strings.Contains(result.Hint, "ankra cluster operations list --failed --cluster "+testClusterID) {
		t.Errorf("structured result = %+v, want the failed-operation listing in the hint", result)
	}
}

// With --wait the platform holds the request and answers a refusal as an
// error carrying the provider's reason; the command must fail with it.
func TestNodeGroupUpgradeWaitSurfacesTheProviderRefusal(t *testing.T) {
	reason := "Server type 'cpx32' is not available in location 'fsn1'"
	mock := &nodeGroupSubmittedMock{resizeError: errors.New("request failed: status 409: " + reason)}
	var runError error
	captureStdout(t, func() {
		_, runError = runConfirmCommand(t, mock, "", []*cobra.Command{clusterNodeGroupUpgradeCmd},
			"cluster", "node-group", "upgrade", testClusterID, "workers", "cpx32", "--wait")
	})
	if runError == nil || !strings.Contains(runError.Error(), reason) {
		t.Fatalf("error = %v, want the provider's reason", runError)
	}
}

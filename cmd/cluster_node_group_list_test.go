package cmd

// Tests for the joined-node column on `ankra cluster node-group list`
// (PLA-853): every count the platform reports comes from its worker
// records, and a record can stay on the books with no Kubernetes node
// behind it. The line now carries joined=N next to count, and a group
// with an unregistered worker is followed by a line naming it.

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type nodeGroupListJoinedMock struct {
	baseMock
	result *client.NodeGroupListResult
}

func (m *nodeGroupListJoinedMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: clusterID, Kind: "upcloud"}, nil
}

func (m *nodeGroupListJoinedMock) ListUpcloudNodeGroups(clusterID string) (*client.NodeGroupListResult, error) {
	return m.result, nil
}

func TestClusterNodeGroupListShowsJoinedCountAndNamesTheWorkerWithoutANode(t *testing.T) {
	writeSelectedClusterJSON(t)
	joinedCount := 2
	mock := &nodeGroupListJoinedMock{result: &client.NodeGroupListResult{NodeGroups: []client.NodeGroupInfo{{
		Name:                "default",
		InstanceType:        "4xCPU-8GB",
		Count:               3,
		JoinedCount:         &joinedCount,
		UnregisteredWorkers: []string{"so-upcloud-infrastructure-cbd6f750-default-2"},
		Labels:              map[string]string{"role": "worker"},
		Zones:               []string{"fi-hel2"},
	}}}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "node-group", "list", testClusterID)
	})

	if !strings.Contains(stdoutOutput, "count=3  joined=2  labels=1  taints=0  zones=fi-hel2") {
		t.Errorf("expected joined next to count on the list line, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "no Kubernetes node is registered for: so-upcloud-infrastructure-cbd6f750-default-2") {
		t.Errorf("expected the unregistered worker to be named, got: %s", stdoutOutput)
	}
}

func TestClusterNodeGroupListLineIsUnchangedWhenHealthyOrWhenThePlatformCannotTell(t *testing.T) {
	healthyCount := 3
	for name, group := range map[string]client.NodeGroupInfo{
		"platform cannot tell": {Name: "default", InstanceType: "4xCPU-8GB", Count: 3},
		"every worker joined":  {Name: "default", InstanceType: "4xCPU-8GB", Count: 3, JoinedCount: &healthyCount},
	} {
		t.Run(name, func(t *testing.T) {
			writeSelectedClusterJSON(t)
			setMockClient(t, &nodeGroupListJoinedMock{result: &client.NodeGroupListResult{
				NodeGroups: []client.NodeGroupInfo{group}}})

			stdoutOutput := captureStdout(t, func() {
				_, _ = executeCommand("cluster", "node-group", "list", testClusterID)
			})

			if !strings.Contains(stdoutOutput, "count=3  labels=0  taints=0") {
				t.Errorf("expected the plain list line, got: %s", stdoutOutput)
			}
			if strings.Contains(stdoutOutput, "joined=") || strings.Contains(stdoutOutput, "no Kubernetes node") {
				t.Errorf("only a mismatch is rendered, got: %s", stdoutOutput)
			}
		})
	}
}

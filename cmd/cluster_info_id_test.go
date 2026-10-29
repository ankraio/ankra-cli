package cmd

// ankra-mz2awi item 1: `cluster info` takes a cluster id as well as a name,
// like --cluster and every other cluster command.

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

type clusterInfoByIDMock struct {
	baseMock
	cluster     client.ClusterListItem
	idLookups   []string
	nameLookups []string
}

func (m *clusterInfoByIDMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	m.idLookups = append(m.idLookups, clusterID)
	if clusterID == m.cluster.ID {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, client.ErrClusterNotFound
}

func (m *clusterInfoByIDMock) GetCluster(name string) (client.ClusterListItem, error) {
	m.nameLookups = append(m.nameLookups, name)
	if name == m.cluster.Name {
		return m.cluster, nil
	}
	return client.ClusterListItem{}, client.ErrClusterNotFound
}

func TestClusterInfoAcceptsAClusterID(t *testing.T) {
	mock := &clusterInfoByIDMock{cluster: client.ClusterListItem{
		ID: testClusterID, Name: "pla906-rehearsal", KubeVersion: "v1.36.5+k3s1", State: "online",
	}}
	setMockClient(t, mock)

	var commandError error
	stdoutOutput := captureStdout(t, func() {
		_, commandError = executeCommand("cluster", "info", testClusterID)
	})

	if commandError != nil {
		t.Fatalf("cluster info <id> must resolve the id, got %v", commandError)
	}
	if len(mock.idLookups) != 1 || mock.idLookups[0] != testClusterID {
		t.Errorf("expected one lookup by id, got %v", mock.idLookups)
	}
	for _, expected := range []string{"pla906-rehearsal", testClusterID, "v1.36.5+k3s1"} {
		if !strings.Contains(stdoutOutput, expected) {
			t.Errorf("expected output to contain %q, got: %s", expected, stdoutOutput)
		}
	}
}

func TestClusterInfoStillAcceptsAName(t *testing.T) {
	mock := &clusterInfoByIDMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "pla906-rehearsal"}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		if _, err := executeCommand("cluster", "info", "pla906-rehearsal"); err != nil {
			t.Errorf("cluster info <name>: %v", err)
		}
	})

	if len(mock.idLookups) != 0 || len(mock.nameLookups) != 1 {
		t.Errorf("a name is looked up by name only, got id=%v name=%v", mock.idLookups, mock.nameLookups)
	}
	if !strings.Contains(stdoutOutput, testClusterID) {
		t.Errorf("expected the cluster's details, got: %s", stdoutOutput)
	}
}

func TestClusterInfoUnknownIDExitsNotFound(t *testing.T) {
	mock := &clusterInfoByIDMock{cluster: client.ClusterListItem{ID: testClusterID, Name: "pla906-rehearsal"}}
	setMockClient(t, mock)

	const unknownID = "00000000-0000-4000-8000-000000000000"
	_, commandError := executeCommand("cluster", "info", unknownID)

	if commandError == nil || !strings.Contains(commandError.Error(), unknownID) {
		t.Fatalf("expected a not-found error naming the id, got %v", commandError)
	}
	if code := exitCodeFor(commandError); code != exitNotFound {
		t.Errorf("an unknown cluster must exit %d, got %d (%v)", exitNotFound, code, commandError)
	}
}

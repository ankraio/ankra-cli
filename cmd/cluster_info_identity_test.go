package cmd

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

type clusterInfoIdentityMock struct {
	baseMock
	byID        client.ClusterListItem
	byName      client.ClusterListItem
	idError     error
	nameLookups int
}

func (m *clusterInfoIdentityMock) GetClusterByID(id string) (client.ClusterListItem, error) {
	if m.idError != nil {
		return client.ClusterListItem{}, m.idError
	}
	if id != m.byID.ID {
		return client.ClusterListItem{}, client.ErrClusterNotFound
	}
	return m.byID, nil
}

func (m *clusterInfoIdentityMock) GetCluster(string) (client.ClusterListItem, error) {
	m.nameLookups++
	return m.byName, nil
}

func TestClusterInfoPreservesIdentity(t *testing.T) {
	const replacementID = "00000000-0000-4000-8000-000000000001"
	readError := errors.New("cluster service unavailable")
	for _, testCase := range []struct {
		name      string
		override  bool
		freshName string
		idError   error
		wantCode  int
	}{
		{name: "explicit ID with reused name", override: true, freshName: "test-cluster"},
		{name: "selected ID with reused name", freshName: "test-cluster"},
		{name: "selected cluster renamed", freshName: "renamed-cluster"},
		{name: "removed selection never substitutes replacement", idError: client.ErrClusterNotFound, wantCode: exitNotFound},
		{name: "failed refresh never substitutes replacement", idError: readError, wantCode: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetFlagsToDefaults(clusterInfoCmd)
			previousConfig := cfgFile
			cfgFile = filepath.Join(t.TempDir(), "config.yaml")
			t.Cleanup(func() {
				resetFlagsToDefaults(clusterInfoCmd)
				cfgFile = previousConfig
			})
			if err := saveSelectedCluster(client.ClusterListItem{ID: testClusterID, Name: "test-cluster", State: "stale"}); err != nil {
				t.Fatal(err)
			}
			mock := &clusterInfoIdentityMock{
				byID:    client.ClusterListItem{ID: testClusterID, Name: testCase.freshName, State: "online"},
				byName:  client.ClusterListItem{ID: replacementID, Name: "test-cluster", State: "online"},
				idError: testCase.idError,
			}
			setMockClient(t, mock)
			arguments := []string{"cluster", "info", "-o", "json"}
			if testCase.override {
				arguments = append(arguments, "--cluster", testClusterID)
			}
			output, err := executeCommand(arguments...)
			if testCase.idError != nil {
				if !errors.Is(err, testCase.idError) || exitCodeFor(err) != testCase.wantCode {
					t.Fatalf("expected original error and exit %d, got %v (exit %d)", testCase.wantCode, err, exitCodeFor(err))
				}
			} else if err != nil || !strings.Contains(output, testClusterID) || !strings.Contains(output, testCase.freshName) || !strings.Contains(output, "online") {
				t.Fatalf("expected fresh details for requested identity, output=%s error=%v", output, err)
			}
			if mock.nameLookups != 0 || strings.Contains(output, replacementID) {
				t.Fatalf("looked up a replacement by name: calls=%d output=%s", mock.nameLookups, output)
			}
		})
	}
}

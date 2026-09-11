package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

const applyOverrideDocument = `apiVersion: v1
kind: ImportCluster
metadata:
  name: hello-fleet
spec:
  stacks:
  - name: hello-fleet
    manifests:
    - name: hello-namespace
      manifest_base64: YXBpVmVyc2lvbjogdjEKa2luZDogTmFtZXNwYWNlCm1ldGFkYXRhOgogIG5hbWU6IGhlbGxvCg==
      parents: []
    addons: []
`

type applyOverrideMock struct {
	baseMock
	applied []client.CreateImportClusterRequest
}

func (mock *applyOverrideMock) GetCluster(name string) (client.ClusterListItem, error) {
	if name == "prod-eu" {
		return client.ClusterListItem{ID: "11111111-1111-1111-1111-111111111111", Name: "prod-eu"}, nil
	}
	return client.ClusterListItem{}, os.ErrNotExist
}

func (mock *applyOverrideMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if clusterID == "11111111-1111-1111-1111-111111111111" {
		return client.ClusterListItem{ID: clusterID, Name: "prod-eu"}, nil
	}
	return client.ClusterListItem{}, os.ErrNotExist
}

func (mock *applyOverrideMock) ApplyCluster(ctx context.Context, request client.CreateImportClusterRequest, wait bool) (*client.ImportResponse, bool, error) {
	mock.applied = append(mock.applied, request)
	return &client.ImportResponse{Name: request.Name, ClusterId: "11111111-1111-1111-1111-111111111111"}, false, nil
}

func writeApplyOverrideDocument(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hello-fleet.yaml")
	if writeError := os.WriteFile(path, []byte(applyOverrideDocument), 0o600); writeError != nil {
		t.Fatal(writeError)
	}
	return path
}

// resetClusterApplyFlags clears the apply flags and the persistent --cluster
// override, now and again when the test ends: the override is a package
// global, and a value left behind would redirect every later apply test.
func resetClusterApplyFlags(t *testing.T) {
	t.Helper()
	clear := func() {
		resetCommandFlags(t, clusterApplyCmd)
		_ = clusterCmd.PersistentFlags().Set(activeClusterFlagName, "")
		clusterCmd.PersistentFlags().Lookup(activeClusterFlagName).Changed = false
	}
	clear()
	t.Cleanup(clear)
}

func TestClusterApplyClusterFlagOverridesMetadataName(t *testing.T) {
	resetClusterApplyFlags(t)
	mock := &applyOverrideMock{}
	setMockClient(t, mock)
	path := writeApplyOverrideDocument(t)

	stdout := captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "apply", "-f", path, "--cluster", "prod-eu"); executeError != nil {
			t.Errorf("apply failed: %v", executeError)
		}
	})

	if len(mock.applied) != 1 || mock.applied[0].Name != "prod-eu" {
		t.Fatalf("applied = %+v, want the --cluster name as the target", mock.applied)
	}
	if !strings.Contains(stdout, "Applying to cluster 'prod-eu' (--cluster), not 'hello-fleet'") {
		t.Errorf("the substitution must be announced:\n%s", stdout)
	}
}

func TestClusterApplyClusterFlagAcceptsAnID(t *testing.T) {
	resetClusterApplyFlags(t)
	mock := &applyOverrideMock{}
	setMockClient(t, mock)
	path := writeApplyOverrideDocument(t)

	captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "apply", "-f", path, "--cluster", "11111111-1111-1111-1111-111111111111"); executeError != nil {
			t.Errorf("apply failed: %v", executeError)
		}
	})
	if len(mock.applied) != 1 || mock.applied[0].Name != "prod-eu" {
		t.Fatalf("applied = %+v, want the cluster's name resolved from its id", mock.applied)
	}
}

func TestClusterApplyWithoutClusterFlagKeepsMetadataName(t *testing.T) {
	resetClusterApplyFlags(t)
	mock := &applyOverrideMock{}
	setMockClient(t, mock)
	path := writeApplyOverrideDocument(t)

	captureStdout(t, func() {
		if _, executeError := executeCommand("cluster", "apply", "-f", path); executeError != nil {
			t.Errorf("apply failed: %v", executeError)
		}
	})
	if len(mock.applied) != 1 || mock.applied[0].Name != "hello-fleet" {
		t.Fatalf("applied = %+v, want metadata.name untouched", mock.applied)
	}
}

func TestClusterApplyUnknownClusterFlagFails(t *testing.T) {
	resetClusterApplyFlags(t)
	mock := &applyOverrideMock{}
	setMockClient(t, mock)
	path := writeApplyOverrideDocument(t)

	var executeError error
	captureStdout(t, func() {
		_, executeError = executeCommand("cluster", "apply", "-f", path, "--cluster", "nowhere")
	})
	if executeError == nil || !strings.Contains(executeError.Error(), `cluster "nowhere" not found`) {
		t.Fatalf("expected a not-found error, got %v", executeError)
	}
	if len(mock.applied) != 0 {
		t.Errorf("nothing should be applied: %+v", mock.applied)
	}
}

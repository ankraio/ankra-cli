package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

type draftBootstrapMock struct {
	baseMock
	existingCluster *client.ClusterListItem

	applyCalls      int
	applyStacks     []client.Stack
	applyGitRepo    *client.GitRepository
	applyName       string
	draftClusterIDs []string
	draftStackNames []string
}

func (m *draftBootstrapMock) GetCluster(name string) (client.ClusterListItem, error) {
	if m.existingCluster != nil && m.existingCluster.Name == name {
		return *m.existingCluster, nil
	}
	return client.ClusterListItem{}, fmt.Errorf("no cluster found for name %q", name)
}

func (m *draftBootstrapMock) ApplyCluster(ctx context.Context, clusterReq client.CreateImportClusterRequest, wait bool) (*client.ImportResponse, bool, error) {
	m.applyCalls++
	m.applyName = clusterReq.Name
	m.applyGitRepo = clusterReq.Spec.GitRepository
	m.applyStacks = append([]client.Stack(nil), clusterReq.Spec.Stacks...)
	return &client.ImportResponse{Name: clusterReq.Name, ClusterId: "bootstrapped-cluster-id"}, false, nil
}

func (m *draftBootstrapMock) CreateStackDraft(ctx context.Context, clusterID string, stack client.Stack) (*client.StackDraftResult, error) {
	m.draftClusterIDs = append(m.draftClusterIDs, clusterID)
	m.draftStackNames = append(m.draftStackNames, stack.Name)
	return &client.StackDraftResult{DraftID: "draft-" + stack.Name}, nil
}

func writeDraftImportClusterYAML(t *testing.T) string {
	t.Helper()
	yamlContent := `kind: ImportCluster
metadata:
  name: draft-cluster
spec:
  git_repository:
    provider: github
    credential_name: my-cred
    branch: main
    repository: org/repo
  stacks:
    - name: monitoring
      manifests: []
      addons: []
    - name: apps
      manifests: []
      addons: []
`
	yamlPath := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}
	return yamlPath
}

func TestDraftBootstrapImportsClusterWithoutStacks(t *testing.T) {
	mock := &draftBootstrapMock{}
	setMockClient(t, mock)
	yamlPath := writeDraftImportClusterYAML(t)

	var output string
	var err error
	stdout := captureStdout(t, func() {
		output, err = executeCommand("cluster", "draft", "-f", yamlPath)
	})
	if err != nil {
		t.Fatalf("cluster draft failed: %v\noutput: %s", err, output)
	}

	if mock.applyCalls != 1 {
		t.Fatalf("bootstrap ApplyCluster calls = %d, want 1", mock.applyCalls)
	}
	if len(mock.applyStacks) != 0 {
		names := make([]string, 0, len(mock.applyStacks))
		for _, stack := range mock.applyStacks {
			names = append(names, stack.Name)
		}
		t.Errorf("bootstrap ApplyCluster received stacks %v, want none", names)
	}
	if mock.applyName != "draft-cluster" {
		t.Errorf("bootstrap ApplyCluster name = %q, want %q", mock.applyName, "draft-cluster")
	}
	if mock.applyGitRepo == nil || mock.applyGitRepo.Repository != "org/repo" {
		t.Errorf("bootstrap ApplyCluster git_repository = %+v, want repository %q kept", mock.applyGitRepo, "org/repo")
	}

	wantStacks := []string{"monitoring", "apps"}
	if len(mock.draftStackNames) != len(wantStacks) {
		t.Fatalf("CreateStackDraft calls = %v, want %v", mock.draftStackNames, wantStacks)
	}
	for i, want := range wantStacks {
		if mock.draftStackNames[i] != want {
			t.Errorf("CreateStackDraft call %d = %q, want %q", i, mock.draftStackNames[i], want)
		}
		if mock.draftClusterIDs[i] != "bootstrapped-cluster-id" {
			t.Errorf("CreateStackDraft call %d cluster ID = %q, want %q", i, mock.draftClusterIDs[i], "bootstrapped-cluster-id")
		}
	}

	for _, want := range []string{`stack "monitoring": draft created (draft-monitoring)`, `stack "apps": draft created (draft-apps)`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected stdout to contain %q, got: %s", want, stdout)
		}
	}
}

func TestDraftExistingClusterSkipsImport(t *testing.T) {
	mock := &draftBootstrapMock{
		existingCluster: &client.ClusterListItem{ID: "existing-cluster-id", Name: "draft-cluster"},
	}
	setMockClient(t, mock)
	yamlPath := writeDraftImportClusterYAML(t)

	var output string
	var err error
	_ = captureStdout(t, func() {
		output, err = executeCommand("cluster", "draft", "-f", yamlPath)
	})
	if err != nil {
		t.Fatalf("cluster draft failed: %v\noutput: %s", err, output)
	}

	if mock.applyCalls != 0 {
		t.Errorf("ApplyCluster calls = %d, want 0 for an existing cluster", mock.applyCalls)
	}
	if len(mock.draftStackNames) != 2 {
		t.Errorf("CreateStackDraft calls = %v, want both stacks", mock.draftStackNames)
	}
	for i, clusterID := range mock.draftClusterIDs {
		if clusterID != "existing-cluster-id" {
			t.Errorf("CreateStackDraft call %d cluster ID = %q, want %q", i, clusterID, "existing-cluster-id")
		}
	}
}

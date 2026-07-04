package cmd

import (
	"errors"
	"testing"

	"ankra/internal/client"
)

type helmUninstallMock struct {
	baseMock
	called         bool
	gotClusterID   string
	gotReleaseName string
	gotNamespace   string
}

func (m *helmUninstallMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "cluster-abc", Name: name}, nil
}

func (m *helmUninstallMock) UninstallHelmRelease(clusterID, releaseName, namespace string) (*client.UninstallHelmReleaseResponse, error) {
	m.called = true
	m.gotClusterID = clusterID
	m.gotReleaseName = releaseName
	m.gotNamespace = namespace
	return &client.UninstallHelmReleaseResponse{Status: "uninstalled"}, nil
}

func TestHelmUninstall_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &helmUninstallMock{}
	resetConfirmFlag(t, clusterHelmUninstallCmd, clusterCmd)
	_, err := runWithInput(t, mock, "n\n",
		"cluster", "helm", "uninstall", "my-release", "--namespace", "prod", "--cluster", "prod-cluster")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if mock.called {
		t.Error("expected no uninstall call when declined")
	}
}

func TestHelmUninstall_YesProceeds(t *testing.T) {
	mock := &helmUninstallMock{}
	resetConfirmFlag(t, clusterHelmUninstallCmd, clusterCmd)
	out, err := runWithInput(t, mock, "",
		"cluster", "helm", "uninstall", "my-release", "--namespace", "prod", "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !mock.called {
		t.Fatal("expected uninstall call with --yes")
	}
	if mock.gotClusterID != "cluster-abc" || mock.gotReleaseName != "my-release" || mock.gotNamespace != "prod" {
		t.Errorf("got cluster=%q release=%q ns=%q, want cluster-abc/my-release/prod",
			mock.gotClusterID, mock.gotReleaseName, mock.gotNamespace)
	}
}

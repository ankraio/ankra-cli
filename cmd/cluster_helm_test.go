package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

type helmReleaseOpsMock struct {
	baseMock
	rollbacks   []client.RollbackHelmReleaseRequest
	upgrades    []client.UpgradeHelmReleaseRequest
	gotRelease  string
	gotNS       string
	historyCall int
	historyMax  int
}

func (m *helmReleaseOpsMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "cluster-abc", Name: name}, nil
}

func (m *helmReleaseOpsMock) GetHelmReleaseDetail(clusterID, namespace, releaseName string) (*client.HelmReleaseDetail, error) {
	m.gotRelease, m.gotNS = releaseName, namespace
	chart := "traefik-30.0.0"
	notes := "Traefik is up.\n"
	return &client.HelmReleaseDetail{
		Metadata:   client.HelmReleaseMetadata{Name: releaseName, Namespace: namespace, Revision: 4, Status: "deployed", Chart: &chart},
		UserValues: map[string]interface{}{"deployment": map[string]interface{}{"replicas": 2}},
		Notes:      &notes,
	}, nil
}

func (m *helmReleaseOpsMock) GetHelmReleaseHistory(clusterID, namespace, releaseName string, limit int) (*client.HelmReleaseHistory, error) {
	m.historyCall++
	m.historyMax = limit
	m.gotRelease, m.gotNS = releaseName, namespace
	chart := "traefik-30.0.0"
	return &client.HelmReleaseHistory{Revisions: []client.HelmReleaseHistoryEntry{
		{Revision: 4, Status: "deployed", Chart: &chart},
		{Revision: 3, Status: "superseded", Chart: &chart},
	}}, nil
}

func (m *helmReleaseOpsMock) RollbackHelmRelease(clusterID, namespace, releaseName string, request client.RollbackHelmReleaseRequest) (*client.HelmReleaseMutationResult, error) {
	m.rollbacks = append(m.rollbacks, request)
	m.gotRelease, m.gotNS = releaseName, namespace
	return &client.HelmReleaseMutationResult{ReleaseName: releaseName, Namespace: namespace, Revision: 5, ElapsedMS: 1500}, nil
}

func (m *helmReleaseOpsMock) UpgradeHelmRelease(clusterID, namespace, releaseName string, request client.UpgradeHelmReleaseRequest) (*client.HelmReleaseMutationResult, error) {
	m.upgrades = append(m.upgrades, request)
	m.gotRelease, m.gotNS = releaseName, namespace
	return &client.HelmReleaseMutationResult{ReleaseName: releaseName, Namespace: namespace, Revision: 5, ElapsedMS: 2500}, nil
}

func TestHelmGet_ValuesOutputPrintsUserValuesYAML(t *testing.T) {
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmGetCmd, clusterCmd)
	out, err := runWithInput(t, mock, "", "cluster", "helm", "get", "traefik", "-n", "traefik", "--cluster", "prod-cluster", "-o", "values")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if strings.TrimSpace(out) != "deployment:\n    replicas: 2" {
		t.Errorf("-o values should print only the user values as YAML, got: %q", out)
	}
	if mock.gotRelease != "traefik" || mock.gotNS != "traefik" {
		t.Errorf("detail should be fetched for traefik/traefik, got %s/%s", mock.gotNS, mock.gotRelease)
	}
}

func TestHelmGet_TableShowsMetadataValuesAndNotes(t *testing.T) {
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmGetCmd, clusterCmd)
	out, err := runWithInput(t, mock, "", "cluster", "helm", "get", "traefik", "-n", "traefik", "--cluster", "prod-cluster")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"Revision:       4", "Chart:          traefik-30.0.0", "replicas: 2", "Traefik is up."} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the rendering, got: %s", want, out)
		}
	}
}

func TestHelmHistory_RendersRevisionsWithLimit(t *testing.T) {
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmHistoryCmd, clusterCmd)
	out, err := runWithInput(t, mock, "", "cluster", "helm", "history", "traefik", "-n", "traefik", "--cluster", "prod-cluster", "--limit", "10")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if mock.historyMax != 10 {
		t.Errorf("--limit should reach the API, got %d", mock.historyMax)
	}
	plain := stripANSICodes(out)
	if !strings.Contains(plain, "deployed") || !strings.Contains(plain, "superseded") {
		t.Errorf("expected both revisions in the table, got: %s", plain)
	}
}

func TestHelmRollback_DeclineDoesNotCallAPI(t *testing.T) {
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmRollbackCmd, clusterCmd)
	_, err := runWithInput(t, mock, "n\n", "cluster", "helm", "rollback", "traefik", "-n", "traefik", "--revision", "3", "--cluster", "prod-cluster")
	if !errors.Is(err, errCancelled) {
		t.Fatalf("expected errCancelled on decline, got %v", err)
	}
	if len(mock.rollbacks) != 0 {
		t.Error("expected no rollback call when declined")
	}
}

func TestHelmRollback_YesSendsRevision(t *testing.T) {
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmRollbackCmd, clusterCmd)
	out, err := runWithInput(t, mock, "", "cluster", "helm", "rollback", "traefik", "-n", "traefik", "--revision", "3", "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.rollbacks) != 1 {
		t.Fatalf("expected one rollback call, got %d", len(mock.rollbacks))
	}
	request := mock.rollbacks[0]
	if request.Revision != 3 || !request.Wait || request.TimeoutSeconds != 600 {
		t.Errorf("request = %+v, want revision=3 wait=true timeout=600", request)
	}
	if !strings.Contains(out, "rolled back to revision 3; it is now revision 5 (1.5s)") {
		t.Errorf("expected the rollback summary, got: %s", out)
	}
}

func TestHelmRollback_MissingRevisionExitsUsage(t *testing.T) {
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmRollbackCmd, clusterCmd)
	_, err := runWithInput(t, mock, "", "cluster", "helm", "rollback", "traefik", "-n", "traefik", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("missing --revision should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.rollbacks) != 0 {
		t.Error("missing revision must be rejected before any API call")
	}
}

func TestHelmUpgrade_RequiresChartAndValues(t *testing.T) {
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmUpgradeCmd, clusterCmd)
	_, err := runWithInput(t, mock, "", "cluster", "helm", "upgrade", "traefik", "-n", "traefik", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("missing --chart should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	resetConfirmFlag(t, clusterHelmUpgradeCmd, clusterCmd)
	_, err = runWithInput(t, mock, "", "cluster", "helm", "upgrade", "traefik", "-n", "traefik", "--chart", "traefik/traefik", "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("missing --values should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.upgrades) != 0 {
		t.Error("missing flags must be rejected before any API call")
	}
}

func TestHelmUpgrade_YesSendsChartAndValuesFile(t *testing.T) {
	valuesPath := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(valuesPath, []byte("deployment:\n  replicas: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmUpgradeCmd, clusterCmd)
	out, err := runWithInput(t, mock, "", "cluster", "helm", "upgrade", "traefik", "-n", "traefik",
		"--chart", "traefik", "--repo", "https://traefik.github.io/charts", "--version", "30.0.0",
		"--values", valuesPath, "--cluster", "prod-cluster", "--yes")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.upgrades) != 1 {
		t.Fatalf("expected one upgrade call, got %d", len(mock.upgrades))
	}
	request := mock.upgrades[0]
	if request.ChartRef != "traefik" || request.RepoURL != "https://traefik.github.io/charts" || request.ChartVersion != "30.0.0" {
		t.Errorf("request = %+v, want chart=traefik repo=https://traefik.github.io/charts version=30.0.0", request)
	}
	if request.ValuesYAML != "deployment:\n  replicas: 3\n" {
		t.Errorf("values_yaml should be the file verbatim, got %q", request.ValuesYAML)
	}
	if !request.Wait || request.TimeoutSeconds != 600 {
		t.Errorf("wait/timeout defaults should be true/600, got %+v", request)
	}
	if !strings.Contains(out, "upgraded; it is now revision 5 (2.5s)") {
		t.Errorf("expected the upgrade summary, got: %s", out)
	}
}

func TestHelmUpgrade_InvalidValuesFileExitsUsage(t *testing.T) {
	valuesPath := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(valuesPath, []byte("deployment: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmUpgradeCmd, clusterCmd)
	_, err := runWithInput(t, mock, "", "cluster", "helm", "upgrade", "traefik", "-n", "traefik",
		"--chart", "traefik/traefik", "--values", valuesPath, "--cluster", "prod-cluster", "--yes")
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("invalid YAML should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if len(mock.upgrades) != 0 {
		t.Error("invalid values must be rejected before any API call")
	}
}

func TestHelmGet_YamlOutputUsesWireKeys(t *testing.T) {
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmGetCmd, clusterCmd)
	out, err := runWithInput(t, mock, "", "cluster", "helm", "get", "traefik", "-n", "traefik", "--cluster", "prod-cluster", "-o", "yaml")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "user_values:") || !strings.Contains(out, "chart_version:") {
		t.Errorf("-o yaml should use the JSON wire keys, got: %s", out)
	}
	if strings.Contains(out, "uservalues") || strings.Contains(out, "chartversion") {
		t.Errorf("-o yaml must not leak Go field names, got: %s", out)
	}
}

func TestHelmHistory_UnsupportedOutputExitsUsage(t *testing.T) {
	mock := &helmReleaseOpsMock{}
	resetConfirmFlag(t, clusterHelmHistoryCmd, clusterCmd)
	_, err := runWithInput(t, mock, "", "cluster", "helm", "history", "traefik", "-n", "traefik", "--cluster", "prod-cluster", "-o", "wide")
	if got := exitCodeFor(err); got != exitUsage {
		t.Errorf("-o wide should exit %d, got %d (err=%v)", exitUsage, got, err)
	}
	if mock.historyCall != 0 {
		t.Error("an unsupported output format must be rejected before any API call")
	}
}

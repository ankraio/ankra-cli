package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

// migrateUpMock scripts the platform for 'ankra migrate up': the cluster
// listing, the stack apply, the pods of the deployed workloads, and the
// import lifecycle the restore drives.
type migrateUpMock struct {
	migrateRestoreMock
	clusterName   string
	stacks        []string
	applied       []client.CreateImportClusterRequest
	podPolls      int
	podReadyAfter int
}

func (mock *migrateUpMock) ListClusters(int, int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{Result: []client.ClusterListItem{{ID: migrateRestoreTestCluster, Name: mock.clusterName}}}, nil
}

func (mock *migrateUpMock) ListClusterStacks(string) ([]client.ClusterStackListItem, error) {
	items := make([]client.ClusterStackListItem, 0, len(mock.stacks))
	for _, name := range mock.stacks {
		items = append(items, client.ClusterStackListItem{Name: name})
	}
	return items, nil
}

func (mock *migrateUpMock) ApplyCluster(_ context.Context, request client.CreateImportClusterRequest, _ bool) (*client.ImportResponse, bool, error) {
	mock.applied = append(mock.applied, request)
	return &client.ImportResponse{Name: request.Name, ClusterId: migrateRestoreTestCluster}, false, nil
}

func (mock *migrateUpMock) ListPods(_ string, options *client.ListPodsOptions) (*client.ListPodsResponse, error) {
	mock.podPolls++
	namespace := options.Namespace
	if mock.podPolls <= mock.podReadyAfter {
		return &client.ListPodsResponse{Pods: []client.PodSummary{{Name: "db-0", Namespace: &namespace, Phase: "Pending", Ready: "0/1"}}}, nil
	}
	return &client.ListPodsResponse{Pods: []client.PodSummary{
		{Name: "dbadmin-7c9-x", Namespace: &namespace, Phase: "Running", Ready: "1/1"},
		{Name: "db-0", Namespace: &namespace, Phase: "Running", Ready: "1/1"},
	}}, nil
}

func installMigrateUpMock(t *testing.T, mock *migrateUpMock) {
	t.Helper()
	installMigrateRestoreMock(t, &mock.migrateRestoreMock)
	apiClient = mock
	original := migrateUpPodPollInterval
	migrateUpPodPollInterval = 0
	t.Cleanup(func() { migrateUpPodPollInterval = original })
}

func newMigrateUpMock() *migrateUpMock {
	return &migrateUpMock{
		migrateRestoreMock: migrateRestoreMock{vaults: []client.BackupVault{readyVault("backups1")}, getStatuses: []string{"completed"}},
		clusterName:        "shop-cluster",
		podReadyAfter:      2,
	}
}

func TestMigrateUpPlanChangesNothing(t *testing.T) {
	fakeDockerOnPath(t)
	mock := newMigrateUpMock()
	installMigrateUpMock(t, mock)
	dir := writeMigrateFixture(t)
	out := filepath.Join(t.TempDir(), "migration")

	stdout, stderr, err := runMigrate(t, "up", dir, "--out", out, "--cluster", migrateRestoreTestCluster, "--plan")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, want := range []string{
		"Migration plan for " + dir + " (docker module)",
		"cluster    shop-cluster",
		"stack      shop (new), namespace shop",
		"db: postgres 17.2 as postgres - office (8.0 MiB)",
		"about 8.0 MiB to dump",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the plan must say %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stdout, "Plan only; nothing was changed") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if _, statError := os.Stat(out); statError == nil {
		t.Error("--plan must not create the output directory")
	}
	if len(mock.applied) != 0 || mock.createdWith != nil || mock.podPolls != 0 {
		t.Errorf("--plan must not touch the platform: applied=%d imports=%v polls=%d", len(mock.applied), mock.createdWith != nil, mock.podPolls)
	}
}

func TestMigrateUpMigratesEndToEnd(t *testing.T) {
	fakeDockerOnPath(t)
	mock := newMigrateUpMock()
	mock.stacks = []string{"other"}
	installMigrateUpMock(t, mock)
	dir := writeMigrateFixture(t)
	out := filepath.Join(t.TempDir(), "migration")

	stdout, stderr, err := runMigrate(t, "up", dir, "--out", out, "--cluster", migrateRestoreTestCluster, "--yes", "--option", "ingress.web=shop.example.com")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if len(mock.applied) != 1 || mock.applied[0].Name != "shop-cluster" {
		t.Fatalf("the stack must be applied under the target cluster's own name, got %+v", mock.applied)
	}
	if stacks := mock.applied[0].Spec.Stacks; len(stacks) != 1 || stacks[0].Name != "shop" || len(stacks[0].Manifests) == 0 {
		t.Errorf("applied stacks = %+v", stacks)
	}
	if mock.podPolls < 3 {
		t.Errorf("the database pod must be waited for until it is ready, got %d polls", mock.podPolls)
	}
	for _, file := range []string{"stack/cluster.yaml", "data/manifest.json", "data/db/office.dump"} {
		if _, statError := os.Stat(filepath.Join(out, filepath.FromSlash(file))); statError != nil {
			t.Errorf("%s missing: %v", file, statError)
		}
	}
	if mock.createdWith == nil || mock.createdWith.StackName != "shop" || len(mock.uploads) != 2 || mock.restored != 1 {
		t.Errorf("the export must be registered as stack shop, uploaded and restored: created=%+v uploads=%d restored=%d", mock.createdWith, len(mock.uploads), mock.restored)
	}
	for _, want := range []string{"==> Deploying stack shop to cluster shop-cluster", "db: Pending 0/1", "db: running", "==> Exporting", "==> Restoring"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("progress must say %q:\n%s", want, stderr)
		}
	}
	for _, want := range []string{
		dir + " is running on cluster shop-cluster as stack shop (namespace shop).",
		"Data: 1 database server(s) restored",
		"Reachable at: http://shop.example.com",
		"This was a rehearsal",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout must say %q:\n%s", want, stdout)
		}
	}
}

func TestMigrateUpStopSourceIsTheCutover(t *testing.T) {
	fakeDockerOnPath(t)
	dockerLog := filepath.Join(t.TempDir(), "docker.log")
	t.Setenv("FAKE_DOCKER_LOG", dockerLog)
	mock := newMigrateUpMock()
	installMigrateUpMock(t, mock)
	dir := writeMigrateFixture(t)

	// Declining the prompt changes nothing, on either side.
	rootCmd.SetIn(strings.NewReader("n\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	_, _, err := runMigrate(t, "up", dir, "--out", filepath.Join(t.TempDir(), "m1"), "--cluster", migrateRestoreTestCluster, "--stop-source")
	if exitCodeFor(err) != exitCancelled || len(mock.applied) != 0 {
		t.Fatalf("a declined prompt must exit %d with nothing applied, got %v (applied %d)", exitCancelled, err, len(mock.applied))
	}
	if _, statError := os.Stat(dockerLog); statError == nil {
		t.Fatal("nothing may be stopped before the confirmation")
	}

	stdout, stderr, err := runMigrate(t, "up", dir, "--out", filepath.Join(t.TempDir(), "m2"), "--cluster", migrateRestoreTestCluster, "--stop-source", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	stopped, _ := os.ReadFile(dockerLog)
	if !strings.Contains(string(stopped), "docker stop web1") || strings.Contains(string(stopped), "abc123") {
		t.Errorf("the web service must be stopped and the database left running, got %q", stopped)
	}
	if !strings.Contains(stderr, "Stopped web. To bring them back: docker start web1") {
		t.Errorf("stderr:\n%s", stderr)
	}
	if !strings.Contains(stdout, "this was the cutover") || !strings.Contains(stdout, "docker start web1") || strings.Contains(stdout, "rehearsal") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestMigrateUpRefusesBeforeTouchingAnythingWhenTheVaultIsMissing(t *testing.T) {
	fakeDockerOnPath(t)
	mock := newMigrateUpMock()
	mock.vaults = nil
	installMigrateUpMock(t, mock)
	dir := writeMigrateFixture(t)
	out := filepath.Join(t.TempDir(), "migration")

	_, _, err := runMigrate(t, "up", dir, "--out", out, "--cluster", migrateRestoreTestCluster, "--yes")
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "no ready backup vault") {
		t.Errorf("a missing vault must fail the plan, got %v", err)
	}
	if _, statError := os.Stat(out); statError == nil {
		t.Error("a failed plan must leave no output directory")
	}
	if len(mock.applied) != 0 {
		t.Error("a failed plan must not deploy")
	}
}

func TestMigrateUpWarnsAboutADatabaseAboveOneUpload(t *testing.T) {
	fakeDockerOnPath(t)
	t.Setenv("FAKE_DB_SIZE", "6442450944")
	installMigrateUpMock(t, newMigrateUpMock())
	dir := writeMigrateFixture(t)

	_, stderr, err := runMigrate(t, "up", dir, "--out", filepath.Join(t.TempDir(), "m"), "--cluster", migrateRestoreTestCluster, "--plan")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "db: database office is 6.0 GiB on the source; a dump above 5.0 GiB cannot be uploaded in one piece") {
		t.Errorf("the plan must warn about the upload limit:\n%s", stderr)
	}
}

func TestMigrateUpNoDataDeploysOnly(t *testing.T) {
	fakeDockerOnPath(t)
	mock := newMigrateUpMock()
	mock.vaults = nil
	installMigrateUpMock(t, mock)
	dir := writeMigrateFixture(t)
	out := filepath.Join(t.TempDir(), "migration")

	stdout, stderr, err := runMigrate(t, "up", dir, "--out", out, "--cluster", migrateRestoreTestCluster, "--no-data", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if len(mock.applied) != 1 || mock.podPolls != 0 || mock.createdWith != nil {
		t.Errorf("--no-data must deploy and nothing else: applied=%d polls=%d import=%v", len(mock.applied), mock.podPolls, mock.createdWith != nil)
	}
	if _, statError := os.Stat(filepath.Join(out, "data")); statError == nil {
		t.Error("--no-data must not dump")
	}
	if !strings.Contains(stdout, "is running on cluster shop-cluster") || strings.Contains(stdout, "rehearsal") {
		t.Errorf("stdout:\n%s", stdout)
	}
	_, _, err = runMigrate(t, "up", dir, "--out", filepath.Join(t.TempDir(), "m2"), "--cluster", migrateRestoreTestCluster, "--no-data", "--stop-source", "--yes")
	if exitCodeFor(err) != exitUsage {
		t.Errorf("--stop-source without data is a usage error, got %v", err)
	}
}

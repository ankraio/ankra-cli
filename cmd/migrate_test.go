package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/migrate"
	"ankra/internal/migrate/docker"
)

const migrateTestCompose = `services:
  web:
    image: nginx:1.27
    ports: ['8080:80']
    depends_on: [db]
  db:
    image: postgres:17
    environment:
      POSTGRES_PASSWORD: secret
    volumes: ['pgdata:/var/lib/postgresql/data']
volumes:
  pgdata:
`

// offlineRegistry avoids scanning the developer's real PATH for modules.
func offlineRegistry(t *testing.T) {
	t.Helper()
	original := newMigrateRegistry
	newMigrateRegistry = func() *migrate.Registry {
		registry := migrate.NewRegistry(docker.New())
		return registry
	}
	t.Cleanup(func() { newMigrateRegistry = original })
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

func writeMigrateFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "shop")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(migrateTestCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runMigrate(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	resetMigrateFlags()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"migrate"}, args...))
	rootCmd.SetContext(context.Background())
	err := rootCmd.Execute()
	rootCmd.SetArgs(nil)
	return stdout.String(), stderr.String(), err
}

func resetMigrateFlags() {
	migrateConvertModule = ""
	migrateConvertOut = "ankra-migration"
	migrateConvertClusterName = ""
	migrateConvertNamespace = ""
	migrateConvertOptions = nil
	migrateConvertForce = false
	migrateConvertDryRun = false
	for _, command := range []string{"convert", "detect", "modules"} {
		sub, _, _ := migrateCmd.Find([]string{command})
		if sub != nil && sub.Flags().Lookup("output") != nil {
			_ = sub.Flags().Set("output", "")
		}
	}
}

func TestMigrateCommandsNeedNoLogin(t *testing.T) {
	for _, command := range []string{"modules", "detect", "convert"} {
		sub, _, err := migrateCmd.Find([]string{command})
		if err != nil {
			t.Fatal(err)
		}
		if commandRequiresAuth(sub) {
			t.Errorf("ankra migrate %s must work offline", command)
		}
	}
}

func TestMigrateModulesListsBuiltIn(t *testing.T) {
	offlineRegistry(t)
	stdout, _, err := runMigrate(t, "modules")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "docker") || !strings.Contains(stdout, "built-in") {
		t.Errorf("modules output:\n%s", stdout)
	}
}

func TestMigrateDetect(t *testing.T) {
	offlineRegistry(t)
	dir := writeMigrateFixture(t)
	stdout, _, err := runMigrate(t, "detect", dir, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"module": "docker"`) || !strings.Contains(stdout, `"confidence": 1`) {
		t.Errorf("detect output:\n%s", stdout)
	}

	_, _, err = runMigrate(t, "detect", filepath.Join(dir, "missing"))
	if exitCodeFor(err) != exitNotFound {
		t.Errorf("a missing directory should exit %d, got %v", exitNotFound, err)
	}
}

func TestMigrateConvertWritesOutput(t *testing.T) {
	offlineRegistry(t)
	dir := writeMigrateFixture(t)
	out := filepath.Join(t.TempDir(), "out")

	stdout, stderr, err := runMigrate(t, "convert", dir, "--out", out, "--option", "ingress.web=shop.example.com")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "docker module") || !strings.Contains(stdout, "ankra cluster apply") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "db: 1 credential(s)") {
		t.Errorf("warnings belong on stderr, got:\n%s", stderr)
	}

	cluster, err := os.ReadFile(filepath.Join(out, "cluster.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"kind: ImportCluster", "name: shop", "from_file: manifests/web.yaml", "- name: db\n", "kind: manifest"} {
		if !strings.Contains(string(cluster), fragment) {
			t.Errorf("cluster.yaml lacks %q:\n%s", fragment, cluster)
		}
	}
	web, err := os.ReadFile(filepath.Join(out, "manifests", "web.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(web), "host: shop.example.com") {
		t.Errorf("ingress option not applied:\n%s", web)
	}
	if _, err := os.Stat(filepath.Join(out, "manifests", "volumes.yaml")); err != nil {
		t.Error("volumes.yaml missing")
	}

	// A second run into the same directory must refuse without --force.
	_, _, err = runMigrate(t, "convert", dir, "--out", out)
	if exitCodeFor(err) != exitUsage {
		t.Errorf("non-empty output dir should exit %d, got %v", exitUsage, err)
	}
	if _, _, err = runMigrate(t, "convert", dir, "--out", out, "--force"); err != nil {
		t.Errorf("--force should overwrite: %v", err)
	}
}

func TestMigrateConvertDryRunWritesNothing(t *testing.T) {
	offlineRegistry(t)
	dir := writeMigrateFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	stdout, stderr, err := runMigrate(t, "convert", dir, "--out", out, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout, "apiVersion: ankra.io/v1alpha1") {
		t.Errorf("dry-run should print cluster.yaml to stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Would write") || !strings.Contains(stderr, "manifests/db.yaml") {
		t.Errorf("dry-run should list files on stderr:\n%s", stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("dry-run must not create the output directory")
	}
}

func TestMigrateConvertStructuredOutput(t *testing.T) {
	offlineRegistry(t)
	dir := writeMigrateFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	stdout, _, err := runMigrate(t, "convert", dir, "--out", out, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "{") || !strings.Contains(stdout, `"module": "docker"`) || !strings.Contains(stdout, `"warnings"`) {
		t.Errorf("json output must be the only thing on stdout:\n%s", stdout)
	}
}

func TestMigrateConvertRejectsBadOption(t *testing.T) {
	offlineRegistry(t)
	dir := writeMigrateFixture(t)
	_, _, err := runMigrate(t, "convert", dir, "--dry-run", "--option", "profiles")
	if exitCodeFor(err) != exitUsage {
		t.Errorf("a bare option key should exit %d, got %v", exitUsage, err)
	}
}

func TestMigrateConvertUnknownModule(t *testing.T) {
	offlineRegistry(t)
	dir := writeMigrateFixture(t)
	_, _, err := runMigrate(t, "convert", dir, "--dry-run", "--module", "heroku")
	if exitCodeFor(err) != exitNotFound {
		t.Errorf("an unknown module should exit %d, got %v", exitNotFound, err)
	}
}

func TestMigrateConvertNothingRecognised(t *testing.T) {
	offlineRegistry(t)
	_, _, err := runMigrate(t, "convert", t.TempDir(), "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "no module recognises") {
		t.Errorf("an empty directory should explain itself, got %v", err)
	}
}

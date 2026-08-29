package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeDockerScript answers the docker calls the export makes for the
// migrateTestCompose fixture: one postgres service holding one database.
const fakeDockerScript = `#!/bin/sh
case "$*" in
  *"ps -q"*) echo abc123 ;;
  *"SHOW server_version"*) echo 17.2 ;;
  *"SELECT datname"*) echo office ;;
  *pg_dumpall*) printf -- '-- globals\n' ;;
  *"-d boom"*) echo 'pg_dump: error: connection to server failed: FATAL: database "boom" does not exist' >&2; exit 1 ;;
  *"pg_dump "*) printf 'PGDMP\001\002fake' ;;
  *) echo "unexpected docker call: $*" >&2; exit 1 ;;
esac
`

// plainModule is an external module without the export capability.
const plainModule = `#!/bin/sh
case "$1" in
  describe) printf '%s' '{"name":"plain","version":"0.1","protocol":1,"summary":"no export"}' ;;
  detect) printf '%s' '{"confidence":0,"reason":"never"}' ;;
  *) exit 2 ;;
esac
`

func resetMigrateExportFlags() {
	migrateExportModule = ""
	migrateExportOut = "ankra-migration-data"
	migrateExportNamespace = ""
	migrateExportOptions = nil
	migrateExportForce = false
	_ = migrateExportCmd.Flags().Set("output", "")
}

// fakeDockerOnPath puts a docker stand-in and the plain module on a PATH of
// their own, so the export exercises the real docker module against a
// scripted daemon without touching this machine's docker.
func fakeDockerOnPath(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-ins")
	}
	offlineRegistry(t)
	resetMigrateExportFlags()
	binDir := t.TempDir()
	for name, body := range map[string]string{"docker": fakeDockerScript, "ankra-module-plain": plainModule} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)
}

func TestMigrateExportNeedsNoLogin(t *testing.T) {
	sub, _, err := migrateCmd.Find([]string{"export"})
	if err != nil {
		t.Fatal(err)
	}
	if commandRequiresAuth(sub) {
		t.Error("ankra migrate export must work offline")
	}
}

func TestMigrateExportWritesManifest(t *testing.T) {
	fakeDockerOnPath(t)
	dir := writeMigrateFixture(t)
	out := filepath.Join(t.TempDir(), "data")

	stdout, stderr, err := runMigrate(t, "export", dir, "--out", out)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "Wrote 2 artifact(s)") || !strings.Contains(stdout, "db restores into db:5432 in namespace shop as postgres, password from Secret db-secrets key POSTGRES_PASSWORD") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "db: dumping database office") {
		t.Errorf("progress belongs on stderr, got:\n%s", stderr)
	}
	for _, file := range []string{"manifest.json", "SHA256SUMS", "db/globals.sql", "db/office.dump"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(file))); err != nil {
			t.Errorf("%s missing: %v", file, err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"module": "docker"`, `"engine": "postgres"`, `"database": "office"`, `"format": "pg_custom"`, `"sha256": "`} {
		if !strings.Contains(string(manifest), fragment) {
			t.Errorf("manifest.json lacks %s:\n%s", fragment, manifest)
		}
	}

	// A second export into the same directory refuses without --force.
	resetMigrateExportFlags()
	_, _, err = runMigrate(t, "export", dir, "--out", out)
	if exitCodeFor(err) != exitUsage {
		t.Errorf("non-empty output dir should exit %d, got %v", exitUsage, err)
	}
	resetMigrateExportFlags()
	if _, _, err = runMigrate(t, "export", dir, "--out", out, "--force"); err != nil {
		t.Errorf("--force should overwrite: %v", err)
	}
}

func TestMigrateExportStructuredOutput(t *testing.T) {
	fakeDockerOnPath(t)
	dir := writeMigrateFixture(t)
	stdout, _, err := runMigrate(t, "export", dir, "--out", filepath.Join(t.TempDir(), "data"), "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "{") || !strings.Contains(stdout, `"module": "docker"`) || !strings.Contains(stdout, `"password_secret": "db-secrets"`) {
		t.Errorf("json output must be the only thing on stdout:\n%s", stdout)
	}
}

func TestMigrateExportRefusesModulesWithoutExport(t *testing.T) {
	fakeDockerOnPath(t)
	dir := writeMigrateFixture(t)
	_, _, err := runMigrate(t, "export", dir, "--out", filepath.Join(t.TempDir(), "data"), "--module", "plain")
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "does not export data") {
		t.Errorf("a module without the capability should exit %d and say so, got %v", exitUsage, err)
	}
}

func TestMigrateExportSurfacesDockerFailures(t *testing.T) {
	fakeDockerOnPath(t)
	dir := writeMigrateFixture(t)
	out := filepath.Join(t.TempDir(), "data")
	_, _, err := runMigrate(t, "export", dir, "--out", out, "--option", "databases.db=boom", "--option", "container.db=db-1")
	if err == nil || !strings.Contains(err.Error(), "db:") || !strings.Contains(err.Error(), `database "boom" does not exist`) {
		t.Errorf("the dump's own error must reach the user with the workload named, got %v", err)
	}
	if _, statError := os.Stat(filepath.Join(out, "manifest.json")); statError == nil {
		t.Error("a failed export must not leave a manifest behind")
	}
	resetMigrateExportFlags()
	_, _, err = runMigrate(t, "export", t.TempDir(), "--out", filepath.Join(t.TempDir(), "data"))
	if exitCodeFor(err) != exitNotFound {
		t.Errorf("an unrecognised directory should exit %d, got %v", exitNotFound, err)
	}
}

func TestFormatByteSize(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB"}
	for size, want := range cases {
		if got := formatByteSize(size); got != want {
			t.Errorf("formatByteSize(%d) = %q, want %q", size, got, want)
		}
	}
}

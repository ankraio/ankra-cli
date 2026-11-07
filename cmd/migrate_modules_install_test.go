package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const installableModule = `#!/bin/sh
case "$1" in
  describe) printf '%s' '{"name":"procz","version":"1.0","protocol":1,"summary":"test module"}' ;;
  detect) printf '%s' '{"confidence":0,"reason":"never"}' ;;
  *) exit 2 ;;
esac
`

func moduleInstallEnvironment(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-ins")
	}
	offlineRegistry(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func installedModulePath(home string) string {
	return filepath.Join(home, ".ankra", "modules", "ankra-module-procz")
}

func TestMigrateModulesInstallFromAFile(t *testing.T) {
	home := moduleInstallEnvironment(t)
	source := filepath.Join(t.TempDir(), "some-download")
	if err := os.WriteFile(source, []byte(installableModule), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := runMigrate(t, "modules", "install", source, "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Installed module procz 1.0") || !strings.Contains(stdout, "sha256 ") {
		t.Errorf("stdout:\n%s", stdout)
	}
	info, statError := os.Stat(installedModulePath(home))
	if statError != nil {
		t.Fatal(statError)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("the installed module must be executable")
	}
	listing, _, listError := runMigrate(t, "modules")
	if listError != nil || !strings.Contains(listing, "procz") {
		t.Errorf("the installed module must be discovered: %v\n%s", listError, listing)
	}

	_, _, err = runMigrate(t, "modules", "install", source, "--yes")
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "--force") {
		t.Errorf("reinstalling without --force is refused with the fix, got %v", err)
	}
	if _, _, err = runMigrate(t, "modules", "install", source, "--yes", "--force"); err != nil {
		t.Errorf("--force replaces: %v", err)
	}
}

func TestMigrateModulesInstallFromAURL(t *testing.T) {
	home := moduleInstallEnvironment(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(installableModule))
	}))
	t.Cleanup(server.Close)
	original := newModuleDownloadClient
	newModuleDownloadClient = func() *http.Client { return server.Client() }
	t.Cleanup(func() { newModuleDownloadClient = original })

	if _, _, err := runMigrate(t, "modules", "install", server.URL+"/ankra-module-procz", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, statError := os.Stat(installedModulePath(home)); statError != nil {
		t.Fatal(statError)
	}
}

func TestMigrateModulesStageBesideDestinationAndPreserveInstalledOnRefusal(t *testing.T) {
	home := moduleInstallEnvironment(t)
	source := filepath.Join(t.TempDir(), "download")
	directory := filepath.Dir(installedModulePath(home))
	t.Setenv("ANKRA_TEST_MODULE_DIRECTORY", directory)
	// The executable verifies where describe runs; this catches cross-filesystem
	// staging without requiring the test host to provide a second mounted volume.
	content := strings.Replace(installableModule, "#!/bin/sh\n",
		"#!/bin/sh\n[ \"${0%/*}\" = \"$ANKRA_TEST_MODULE_DIRECTORY\" ] || exit 7\n", 1)
	if writeError := os.WriteFile(source, []byte(content), 0o644); writeError != nil {
		t.Fatal(writeError)
	}
	if _, _, installError := runMigrate(t, "modules", "install", source, "--yes"); installError != nil {
		t.Fatal(installError)
	}
	if writeError := os.WriteFile(source, []byte("#!/bin/sh\nexit 2\n"), 0o644); writeError != nil {
		t.Fatal(writeError)
	}
	if _, _, installError := runMigrate(t, "modules", "install", source, "--yes", "--force"); installError == nil {
		t.Fatal("a failed describe must refuse replacement")
	}
	installed, readError := os.ReadFile(installedModulePath(home))
	if readError != nil || string(installed) != content {
		t.Fatalf("refusal must preserve installed bytes: %v", readError)
	}
	entries, readError := os.ReadDir(directory)
	if readError != nil || len(entries) != 1 || entries[0].Name() != "ankra-module-procz" {
		t.Fatalf("installation and refusal must leave no staging files: %v, %v", entries, readError)
	}
}

func TestMigrateModuleStagingIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions")
	}
	directory := t.TempDir()
	staging, stagingError := writeModuleStaging(directory, []byte(installableModule))
	if stagingError != nil {
		t.Fatal(stagingError)
	}
	info, statError := os.Stat(staging)
	if statError != nil {
		t.Fatal(statError)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("unverified executable mode = %o, want 700", info.Mode().Perm())
	}
}

func TestMigrateModulesInstallRefusals(t *testing.T) {
	home := moduleInstallEnvironment(t)
	source := filepath.Join(t.TempDir(), "some-download")
	if err := os.WriteFile(source, []byte(installableModule), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runMigrate(t, "modules", "install", "http://example.com/m", "--yes")
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "https") {
		t.Errorf("plain http is refused, got %v", err)
	}

	_, _, err = runMigrate(t, "modules", "install", source, "--yes", "--sha256", strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "refusing it") {
		t.Errorf("a checksum mismatch is refused, got %v", err)
	}

	_, _, err = runMigrate(t, "modules", "install", source, "--yes", "--name", "other")
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), `calls itself "procz"`) {
		t.Errorf("a name mismatch is refused with the module's own name, got %v", err)
	}

	broken := filepath.Join(t.TempDir(), "not-a-module")
	if writeError := os.WriteFile(broken, []byte("#!/bin/sh\nexit 2\n"), 0o644); writeError != nil {
		t.Fatal(writeError)
	}
	_, _, err = runMigrate(t, "modules", "install", broken, "--yes")
	if err == nil || !strings.Contains(err.Error(), "does not answer describe") {
		t.Errorf("a non-module is refused after describe, got %v", err)
	}

	rootCmd.SetIn(strings.NewReader("n\n"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	_, _, err = runMigrate(t, "modules", "install", source)
	if exitCodeFor(err) != exitCancelled {
		t.Errorf("a declined prompt exits %d, got %v", exitCancelled, err)
	}
	if _, statError := os.Stat(installedModulePath(home)); statError == nil {
		t.Error("nothing may be installed after any refusal")
	}
}

func TestMigrateModulesUninstall(t *testing.T) {
	home := moduleInstallEnvironment(t)
	source := filepath.Join(t.TempDir(), "some-download")
	if err := os.WriteFile(source, []byte(installableModule), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runMigrate(t, "modules", "install", source, "--yes"); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := runMigrate(t, "modules", "uninstall", "procz", "--yes")
	if err != nil || !strings.Contains(stdout, "Removed module procz") {
		t.Fatalf("uninstall: %v\n%s", err, stdout)
	}
	if _, statError := os.Stat(installedModulePath(home)); statError == nil {
		t.Error("the executable must be gone")
	}

	_, _, err = runMigrate(t, "modules", "uninstall", "procz", "--yes")
	if exitCodeFor(err) != exitNotFound {
		t.Errorf("uninstalling twice exits %d, got %v", exitNotFound, err)
	}

	pathDir := t.TempDir()
	if writeError := os.WriteFile(filepath.Join(pathDir, "ankra-module-pathy"), []byte(installableModule), 0o755); writeError != nil {
		t.Fatal(writeError)
	}
	t.Setenv("PATH", pathDir)
	_, _, err = runMigrate(t, "modules", "uninstall", "pathy", "--yes")
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), pathDir) {
		t.Errorf("a PATH-managed module is refused with its location, got %v", err)
	}
}

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

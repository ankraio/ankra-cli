package cmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// devInstallTestDirs is one install's shim and link directories under a
// temporary home.
type devInstallTestDirs struct {
	home    string
	shimDir string
	linkDir string
}

func newDevInstallTestDirs(t *testing.T) devInstallTestDirs {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the dev shims are POSIX shell scripts")
	}
	home := withTempHome(t)
	dirs := devInstallTestDirs{
		home:    home,
		shimDir: filepath.Join(home, "shims"),
		linkDir: filepath.Join(home, "bin"),
	}
	if mkdirError := os.MkdirAll(dirs.linkDir, 0o755); mkdirError != nil {
		t.Fatal(mkdirError)
	}
	t.Setenv(envAnkraExecMode, "")
	t.Setenv("SHELL", "/bin/zsh")
	return dirs
}

func (dirs devInstallTestDirs) install(t *testing.T, extra ...string) (string, string, int) {
	t.Helper()
	args := append([]string{"install", "--dir", dirs.shimDir, "--link-dir", dirs.linkDir}, extra...)
	return runDevCommand(t, context.Background(), args...)
}

func TestDevInstallWritesShimsAndLinksAndIsIdempotent(t *testing.T) {
	dirs := newDevInstallTestDirs(t)
	toolchain := devToolchain(t)
	t.Setenv("PATH", strings.Join([]string{dirs.linkDir, toolchain}, string(os.PathListSeparator)))

	stdout, stderr, code := dirs.install(t)
	if code != 0 {
		t.Fatalf("install exit = %d; stderr:\n%s", code, stderr)
	}
	for _, tool := range devShimTools {
		shimPath := filepath.Join(dirs.shimDir, tool)
		info, statError := os.Stat(shimPath)
		if statError != nil || info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("%s: %v, want an executable shim", shimPath, statError)
		}
		if _, isOurs := isRoutingShim(shimPath); !isOurs {
			t.Fatalf("%s carries no marker", shimPath)
		}
		if target := linkTarget(filepath.Join(dirs.linkDir, tool)); target != shimPath {
			t.Fatalf("%s links to %q, want the shim", tool, target)
		}
	}
	if !strings.Contains(stdout, "Every shim is first on PATH") {
		t.Fatalf("stdout = %q, want every shim reported first on PATH", stdout)
	}
	recorded, isRecorded := readDevInstallation()
	if !isRecorded || recorded.ShimDir != dirs.shimDir || recorded.LinkDir != dirs.linkDir {
		t.Fatalf("recorded install = %+v %t", recorded, isRecorded)
	}

	// Again: the same files, no complaints.
	before, _ := os.ReadFile(filepath.Join(dirs.shimDir, "go"))
	if _, stderr, code = dirs.install(t); code != 0 || strings.TrimSpace(stderr) != "" {
		t.Fatalf("re-install exit = %d stderr %q", code, stderr)
	}
	after, _ := os.ReadFile(filepath.Join(dirs.shimDir, "go"))
	if string(before) != string(after) {
		t.Fatal("a re-install changed the shim")
	}
	entries, _ := os.ReadDir(dirs.linkDir)
	if len(entries) != len(devShimTools) {
		t.Fatalf("link dir holds %d entries after two installs, want %d", len(entries), len(devShimTools))
	}
}

func TestDevInstallNeverClobbersARealToolOrAForeignLink(t *testing.T) {
	dirs := newDevInstallTestDirs(t)
	t.Setenv("PATH", dirs.linkDir)
	realGo := writeDevExecutable(t, filepath.Join(dirs.linkDir, "go"), "#!/bin/sh\necho the real go\n")
	elsewhere := writeDevExecutable(t, filepath.Join(dirs.home, "node-bin", "npx"), "#!/bin/sh\n")
	if linkError := os.Symlink(elsewhere, filepath.Join(dirs.linkDir, "npx")); linkError != nil {
		t.Fatal(linkError)
	}
	legacyShim := writeDevExecutable(t, filepath.Join(dirs.home, ".claude", "remote-exec", "shims", "route"),
		"#!/usr/bin/env bash\n# Routing shim for go, gofmt, pnpm, npm, npx and golangci-lint\n")
	if linkError := os.Symlink(legacyShim, filepath.Join(dirs.linkDir, "pnpm")); linkError != nil {
		t.Fatal(linkError)
	}

	stdout, stderr, code := dirs.install(t)
	if code != 0 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
	if contents, _ := os.ReadFile(realGo); string(contents) != "#!/bin/sh\necho the real go\n" {
		t.Fatal("the real go was overwritten")
	}
	if target := linkTarget(filepath.Join(dirs.linkDir, "npx")); target != elsewhere {
		t.Fatalf("the foreign npx link now points at %q", target)
	}
	if target := linkTarget(filepath.Join(dirs.linkDir, "pnpm")); target != legacyShim {
		t.Fatal("the remote-exec link was replaced without --replace-legacy")
	}
	for _, want := range []string{"go: a real file is there", "npx: it is a link to " + elsewhere,
		"pnpm: it points at the claude-tools remote-exec shim", "--replace-legacy"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	// The shims are not first for those tools: the PATH line is printed.
	if !strings.Contains(stdout, `export PATH="$HOME/shims":"$PATH"`) || !strings.Contains(stdout, "~/.zshrc") {
		t.Fatalf("stdout gives no PATH line:\n%s", stdout)
	}

	if _, stderr, code = dirs.install(t, "--replace-legacy"); code != 0 {
		t.Fatalf("--replace-legacy exit = %d; stderr:\n%s", code, stderr)
	}
	if target := linkTarget(filepath.Join(dirs.linkDir, "pnpm")); target != filepath.Join(dirs.shimDir, "pnpm") {
		t.Fatalf("--replace-legacy left pnpm pointing at %q", target)
	}

	// Uninstall takes exactly what install made.
	if _, stderr, code = runDevCommand(t, context.Background(), "install", "--uninstall"); code != 0 {
		t.Fatalf("uninstall exit = %d; stderr:\n%s", code, stderr)
	}
	if _, statError := os.Stat(realGo); statError != nil {
		t.Fatal("uninstall removed the real go")
	}
	if target := linkTarget(filepath.Join(dirs.linkDir, "npx")); target != elsewhere {
		t.Fatal("uninstall touched the foreign npx link")
	}
	for _, tool := range []string{"gofmt", "golangci-lint", "npm", "pnpm"} {
		if _, statError := os.Lstat(filepath.Join(dirs.linkDir, tool)); statError == nil {
			t.Errorf("uninstall left the %s link", tool)
		}
	}
	if _, statError := os.Stat(dirs.shimDir); statError == nil {
		t.Fatal("uninstall left the shim directory")
	}
	if _, isRecorded := readDevInstallation(); isRecorded {
		t.Fatal("uninstall left the install record")
	}
	if _, statError := os.Stat(legacyShim); statError != nil {
		t.Fatal("uninstall removed the remote-exec shim itself")
	}
}

func TestDevUninstallLeavesAFileThatIsNotOurs(t *testing.T) {
	dirs := newDevInstallTestDirs(t)
	t.Setenv("PATH", dirs.linkDir)
	if _, stderr, code := dirs.install(t); code != 0 {
		t.Fatalf("install exit = %d; stderr:\n%s", code, stderr)
	}
	// Somebody replaced one shim with their own script.
	replaced := filepath.Join(dirs.shimDir, "go")
	if removeError := os.Remove(replaced); removeError != nil {
		t.Fatal(removeError)
	}
	writeDevExecutable(t, replaced, "#!/bin/sh\necho mine\n")
	_, stderr, code := runDevCommand(t, context.Background(), "install", "--uninstall")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if _, statError := os.Stat(replaced); statError != nil {
		t.Fatal("uninstall removed a file that is not a shim")
	}
	if !strings.Contains(stderr, "Leaving "+replaced) {
		t.Fatalf("stderr = %q", stderr)
	}
	// A second install refuses to overwrite it too.
	if _, stderr, _ = dirs.install(t); !strings.Contains(stderr, "Not writing "+replaced) {
		t.Fatalf("stderr = %q, want the foreign file reported", stderr)
	}
	if contents, _ := os.ReadFile(replaced); string(contents) != "#!/bin/sh\necho mine\n" {
		t.Fatal("install overwrote a file that is not a shim")
	}
}

func TestDevUninstallWithNothingInstalled(t *testing.T) {
	dirs := newDevInstallTestDirs(t)
	stdout, _, code := runDevCommand(t, context.Background(), "install", "--uninstall", "--dir", dirs.shimDir)
	if code != 0 || !strings.Contains(stdout, "Nothing to remove") {
		t.Fatalf("exit = %d stdout %q", code, stdout)
	}
}

func TestDevInstallMovesWhenTheDirectoriesChange(t *testing.T) {
	dirs := newDevInstallTestDirs(t)
	t.Setenv("PATH", dirs.linkDir)
	if _, _, code := dirs.install(t); code != 0 {
		t.Fatal("first install failed")
	}
	moved := filepath.Join(dirs.home, "moved-shims")
	if _, stderr, code := runDevCommand(t, context.Background(), "install", "--dir", moved, "--link-dir", dirs.linkDir); code != 0 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
	if _, statError := os.Stat(dirs.shimDir); statError == nil {
		t.Fatal("the old shim directory is still there")
	}
	if target := linkTarget(filepath.Join(dirs.linkDir, "go")); target != filepath.Join(moved, "go") {
		t.Fatalf("go links to %q, want the moved shim", target)
	}
}

func TestDevInstallWithoutALinkDirPrintsThePathLine(t *testing.T) {
	dirs := newDevInstallTestDirs(t)
	t.Setenv("PATH", devToolchain(t))
	t.Setenv("SHELL", "/usr/bin/fish")
	stdout, _, code := runDevCommand(t, context.Background(), "install", "--dir", dirs.shimDir, "--link-dir", "")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, `fish_add_path --prepend --move "$HOME/shims"`) {
		t.Fatalf("stdout = %q", stdout)
	}
	entries, _ := os.ReadDir(dirs.linkDir)
	if len(entries) != 0 {
		t.Fatal("something was linked with --link-dir \"\"")
	}
	if _, _, code := runDevCommand(t, context.Background(), "install", "--link-dir", filepath.Join(dirs.home, "nope")); code != exitUsage {
		t.Fatalf("a missing --link-dir exit = %d, want usage", code)
	}
}

func TestDevStatusReportsWhatResolvesFirst(t *testing.T) {
	dirs := newDevInstallTestDirs(t)
	toolchain := devToolchain(t)
	t.Setenv("PATH", strings.Join([]string{dirs.linkDir, toolchain}, string(os.PathListSeparator)))
	if _, _, code := dirs.install(t); code != 0 {
		t.Fatal("install failed")
	}
	// A real npx in front of the link directory bypasses routing.
	t.Setenv("PATH", strings.Join([]string{filepath.Dir(writeDevExecutable(t,
		filepath.Join(dirs.home, "front", "npx"), "#!/bin/sh\n")), dirs.linkDir, toolchain}, string(os.PathListSeparator)))
	stdout, _, code := runDevCommand(t, context.Background(), "status", "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var status devStatus
	if decodeError := json.Unmarshal([]byte(stdout), &status); decodeError != nil {
		t.Fatalf("status is not JSON: %v\n%s", decodeError, stdout)
	}
	if !status.IsInstalled || status.ShimDir != dirs.shimDir || status.Mode != devModeAuto || len(status.Tools) != len(devShimTools) {
		t.Fatalf("status = %+v", status)
	}
	for _, tool := range status.Tools {
		switch tool.Tool {
		case "npx":
			if tool.IsFirstOnPath || !strings.Contains(tool.Note, "routing is bypassed") {
				t.Errorf("npx = %+v, want it reported bypassed", tool)
			}
		default:
			if !tool.IsShimmed || !tool.IsFirstOnPath || tool.Real != filepath.Join(toolchain, tool.Tool) {
				t.Errorf("%s = %+v, want shimmed, first and the toolchain's real one", tool.Tool, tool)
			}
		}
	}
	human, _, _ := runDevCommand(t, context.Background(), "status")
	if !strings.Contains(human, "Mode:      auto (default)") || !strings.Contains(human, "Shim dir:  "+dirs.shimDir) {
		t.Fatalf("human status = %q", human)
	}
}

func TestDevStatusWhenNothingIsInstalled(t *testing.T) {
	newDevInstallTestDirs(t)
	stdout, _, code := runDevCommand(t, context.Background(), "status")
	if code != 0 || !strings.Contains(stdout, "Not installed. Run 'ankra dev install'.") {
		t.Fatalf("exit = %d stdout %q", code, stdout)
	}
}

// The shim script itself, run by /bin/sh: it hands the invocation to the
// CLI, and without one it runs the real tool past every other shim copy.
func TestDevShimScriptHandsOverToTheCLIOrFallsThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	if _, lookError := exec.LookPath("sh"); lookError != nil {
		t.Skip("no sh")
	}
	root := t.TempDir()
	fakeCLI := writeDevExecutable(t, filepath.Join(root, "cli", "ankra"), "#!/bin/sh\necho \"cli:$*\"\n")
	shims := filepath.Join(root, "shims")
	shim := writeDevExecutable(t, filepath.Join(shims, "go"), devShimScript("go", fakeCLI))
	otherShims := filepath.Join(root, "other-shims")
	writeDevExecutable(t, filepath.Join(otherShims, "go"), devShimScript("go", filepath.Join(root, "missing")))
	legacy := filepath.Join(root, "legacy")
	writeDevExecutable(t, filepath.Join(legacy, "go"), "#!/usr/bin/env bash\n# Routing shim for go\nexit 9\n")
	toolchain := filepath.Join(root, "toolchain")
	writeDevExecutable(t, filepath.Join(toolchain, "go"), "#!/bin/sh\necho \"real:$*\"\n")
	systemPath := os.Getenv("PATH")

	run := func(environment []string, args ...string) string {
		command := exec.Command(shim, args...)
		command.Env = environment
		output, runError := command.CombinedOutput()
		if runError != nil {
			t.Fatalf("shim %v: %v\n%s", args, runError, output)
		}
		return strings.TrimSpace(string(output))
	}
	searchPath := strings.Join([]string{shims, otherShims, legacy, toolchain, systemPath}, string(os.PathListSeparator))
	if got := run([]string{"PATH=" + searchPath}, "test", "a b", "--", "-x"); got != "cli:dev shim go -- test a b -- -x" {
		t.Fatalf("hand-over = %q", got)
	}
	override := writeDevExecutable(t, filepath.Join(root, "dev-cli"), "#!/bin/sh\necho \"dev:$*\"\n")
	if got := run([]string{"PATH=" + searchPath, envDevCLI + "=" + override}, "vet"); got != "dev:dev shim go -- vet" {
		t.Fatalf("ANKRA_DEV_CLI = %q", got)
	}

	// No CLI anywhere: the real go, past both shim copies.
	noCLI := writeDevExecutable(t, filepath.Join(root, "nocli-shims", "go"), devShimScript("go", filepath.Join(root, "gone")))
	command := exec.Command(noCLI, "version", "x y")
	command.Env = []string{"PATH=" + strings.Join([]string{filepath.Dir(noCLI), otherShims, legacy, toolchain, "/usr/bin", "/bin"},
		string(os.PathListSeparator))}
	output, runError := command.CombinedOutput()
	if runError != nil || strings.TrimSpace(string(output)) != "real:version x y" {
		t.Fatalf("fallthrough = %q (%v), want the real go", output, runError)
	}

	// Nothing real at all: 127.
	command = exec.Command(noCLI, "version")
	command.Env = []string{"PATH=" + strings.Join([]string{filepath.Dir(noCLI), otherShims, "/usr/bin", "/bin"},
		string(os.PathListSeparator))}
	output, runError = command.CombinedOutput()
	var exitError *exec.ExitError
	if !isExitError(runError, &exitError) || exitError.ExitCode() != 127 || !strings.Contains(string(output), "not found on PATH") {
		t.Fatalf("no real go = %q (%v), want 127", output, runError)
	}
}

package cmd

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// devLocalCall is what a test's devExecLocal saw.
type devLocalCall struct {
	path        string
	argv        []string
	environment []string
}

// stubDevExecLocal records the local runs instead of replacing the test
// process, answering exitCode for each.
func stubDevExecLocal(t *testing.T, exitCode int) *[]devLocalCall {
	t.Helper()
	calls := &[]devLocalCall{}
	original := devExecLocal
	devExecLocal = func(path string, argv []string, environment []string) error {
		*calls = append(*calls, devLocalCall{path: path, argv: argv, environment: environment})
		if exitCode != 0 {
			return &devLocalExit{code: exitCode}
		}
		return nil
	}
	t.Cleanup(func() { devExecLocal = original })
	return calls
}

// writeDevExecutable writes an executable file.
func writeDevExecutable(t *testing.T, path string, contents string) string {
	t.Helper()
	writeTestFile(t, path, contents)
	if chmodError := os.Chmod(path, 0o755); chmodError != nil {
		t.Fatal(chmodError)
	}
	return path
}

// devToolchain is a PATH directory holding a "real" tool of every shimmed
// name.
func devToolchain(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	for _, tool := range devShimTools {
		writeDevExecutable(t, filepath.Join(directory, tool), "#!/bin/sh\necho real-"+tool+" \"$@\"\n")
	}
	return directory
}

func runDevCommand(t *testing.T, ctx context.Context, args ...string) (string, string, int) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(append([]string{"dev"}, args...))
	rootCmd.SetIn(strings.NewReader(""))
	resetTreeFlags(t, devCmd, devInstallCmd, devStatusCmd, devModeCmd, devShimCmd, execCmd)
	t.Cleanup(func() {
		resetTreeFlags(t, devCmd, devInstallCmd, devStatusCmd, devModeCmd, devShimCmd, execCmd)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
		devShimCmd.SilenceErrors = false
		execCmd.SilenceErrors = false
	})
	for _, command := range []*cobra.Command{devCmd, devInstallCmd, devStatusCmd, devModeCmd, devShimCmd} {
		command.SetContext(ctx)
	}
	executeError := rootCmd.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), exitCodeFor(executeError)
}

func TestDevShimExplainsWithoutRunning(t *testing.T) {
	withTempHome(t)
	calls := stubDevExecLocal(t, 0)
	t.Setenv(envAnkraExecMode, "")
	t.Setenv(envAnkraExecExplain, "1")
	cases := map[string]string{
		"go -- test ./...":              "route",
		"go -- run ./cmd/x":             "local",
		"pnpm -- test:e2e":              "route:playwright",
		"golangci-lint -- run --fix":    "route",
		"npx -- eslint .":               "local",
		"gofmt -- -l .":                 "route",
		"go test ./...":                 "route",
		"pnpm -- exec tsc --noEmit":     "route",
		"npm -- install":                "local",
		"go -- -C sub test ./...":       "local",
		"golangci-lint -- version":      "local",
		"npx -- playwright test --ui":   "local",
		"go -- --help":                  "local",
		"pnpm -- vitest run --changed":  "route",
		"gofmt -- -w x.go":              "route",
		"go -- mod tidy":                "route",
		"go -- build -o bin/x ./cmd/x":  "local",
		"pnpm -- run lint":              "route",
		"npm -- run typecheck":          "route",
		"npx -- vitest run src/a.ts":    "route",
		"pnpm -- install --frozen-lock": "local",
	}
	for command, want := range cases {
		stdout, stderr, code := runDevCommand(t, context.Background(), append([]string{"shim"}, strings.Fields(command)...)...)
		if code != 0 || strings.TrimSpace(stdout) != want {
			t.Errorf("explain %q = %q (exit %d, stderr %q), want %q", command, stdout, code, stderr, want)
		}
	}
	t.Setenv(envAnkraExecMode, devModeLocal)
	if stdout, _, _ := runDevCommand(t, context.Background(), "shim", "go", "--", "test", "./..."); strings.TrimSpace(stdout) != "local" {
		t.Errorf("ANKRA_EXEC=local explain = %q, want local", stdout)
	}
	if len(*calls) != 0 {
		t.Fatalf("explain ran %d command(s)", len(*calls))
	}
}

func TestDevShimNeedsATool(t *testing.T) {
	withTempHome(t)
	if _, _, code := runDevCommand(t, context.Background(), "shim"); code != exitUsage {
		t.Fatalf("exit = %d, want usage", code)
	}
	if _, _, code := runDevCommand(t, context.Background(), "shim", "--", "go"); code != exitUsage {
		t.Fatalf("exit = %d, want usage for a missing tool", code)
	}
}

func TestDevExecModePrecedence(t *testing.T) {
	home := withTempHome(t)
	t.Setenv(envAnkraExecMode, "")
	if mode, source := devExecMode(); mode != devModeAuto || source != "default" {
		t.Fatalf("mode = %s (%s), want auto by default", mode, source)
	}
	legacy := filepath.Join(home, ".config", "ankra-exec", "mode")
	writeTestFile(t, legacy, "remote\n")
	if mode, source := devExecMode(); mode != devModeRemote || source != legacy {
		t.Fatalf("mode = %s (%s), want the bash client's file read", mode, source)
	}
	writeTestFile(t, filepath.Join(home, ".ankra", "settings.json"), `{"exec_mode":"local"}`)
	if mode, source := devExecMode(); mode != devModeLocal || source != "ankra dev mode" {
		t.Fatalf("mode = %s (%s), want the CLI setting over the bash file", mode, source)
	}
	t.Setenv(envAnkraExecMode, "auto")
	if mode, source := devExecMode(); mode != devModeAuto || source != envAnkraExecMode {
		t.Fatalf("mode = %s (%s), want ANKRA_EXEC first", mode, source)
	}
	t.Setenv(envAnkraExecMode, "bogus")
	if mode, _ := devExecMode(); mode != devModeLocal {
		t.Fatalf("mode = %s, want an unknown ANKRA_EXEC ignored", mode)
	}
	if _, isValid := normalizeDevMode(" Local\n"); isValid {
		t.Fatal("only lower-case letters count, as in the bash shim: ' Local' reads as 'ocal'")
	}
	if mode, isValid := normalizeDevMode("local\n"); !isValid || mode != devModeLocal {
		t.Fatal("a trailing newline is not part of the mode")
	}
}

func TestDevModeCommandSavesTheDefault(t *testing.T) {
	home := withTempHome(t)
	t.Setenv(envAnkraExecMode, "")
	if stdout, _, code := runDevCommand(t, context.Background(), "mode", "local"); code != 0 ||
		!strings.Contains(stdout, "local") {
		t.Fatalf("mode local = %q exit %d", stdout, code)
	}
	contents, _ := os.ReadFile(filepath.Join(home, ".ankra", "settings.json"))
	if !strings.Contains(string(contents), `"exec_mode": "local"`) {
		t.Fatalf("settings = %s", contents)
	}
	if stdout, _, _ := runDevCommand(t, context.Background(), "mode"); strings.TrimSpace(stdout) != "local (ankra dev mode)" {
		t.Fatalf("mode = %q", stdout)
	}
	if _, _, code := runDevCommand(t, context.Background(), "mode", "sometimes"); code != exitUsage {
		t.Fatalf("an unknown mode exit = %d, want usage", code)
	}
}

func TestFindRealToolSkipsEveryShimCopy(t *testing.T) {
	ours := t.TempDir()
	writeDevExecutable(t, filepath.Join(ours, "go"), devShimScript("go", "/nonexistent/ankra"))
	links := t.TempDir()
	if linkError := os.Symlink(filepath.Join(ours, "go"), filepath.Join(links, "go")); linkError != nil {
		t.Fatal(linkError)
	}
	another := t.TempDir()
	writeDevExecutable(t, filepath.Join(another, "go"), devShimScript("go", "/elsewhere/ankra"))
	legacy := t.TempDir()
	writeDevExecutable(t, filepath.Join(legacy, "go"), "#!/usr/bin/env bash\n# Routing shim for go, gofmt, pnpm (ankra-b5c3as.3).\nexit 9\n")
	notExecutable := t.TempDir()
	writeTestFile(t, filepath.Join(notExecutable, "go"), "#!/bin/sh\n")
	toolchain := devToolchain(t)

	searchPath := strings.Join([]string{links, "", ours, another, legacy, notExecutable, toolchain}, string(os.PathListSeparator))
	realTool, isFound := findRealTool("go", searchPath)
	if !isFound || realTool != filepath.Join(toolchain, "go") {
		t.Fatalf("real go = %q %t, want the toolchain's past every shim copy", realTool, isFound)
	}
	if _, isFound := findRealTool("go", strings.Join([]string{links, ours, another, legacy}, string(os.PathListSeparator))); isFound {
		t.Fatal("a PATH of shims only has a real go")
	}
	if first, _ := firstOnPath("go", searchPath); first != filepath.Join(links, "go") {
		t.Fatalf("first go = %q", first)
	}
}

func TestFindRealToolTakesTheToolAWrappedDirectoryKeptBesideTheShim(t *testing.T) {
	wrapped := t.TempDir()
	writeDevExecutable(t, filepath.Join(wrapped, "go"), "#!/usr/bin/env bash\n# Routing shim for go\n")
	writeDevExecutable(t, filepath.Join(wrapped, "go.slot"), "#!/bin/sh\nexec go.real \"$@\"\n")
	writeDevExecutable(t, filepath.Join(wrapped, "gofmt"), "#!/usr/bin/env bash\n# Routing shim for go\n")
	writeDevExecutable(t, filepath.Join(wrapped, "gofmt.real"), "\x7fELF")
	if realTool, _ := findRealTool("go", wrapped); realTool != filepath.Join(wrapped, "go.slot") {
		t.Fatalf("real go = %q, want go.slot (the go-slot queue)", realTool)
	}
	if realTool, _ := findRealTool("gofmt", wrapped); realTool != filepath.Join(wrapped, "gofmt.real") {
		t.Fatalf("real gofmt = %q, want gofmt.real", realTool)
	}
}

func TestIsRoutingShimReadsOnlyTheTopOfAScript(t *testing.T) {
	directory := t.TempDir()
	deep := writeDevExecutable(t, filepath.Join(directory, "deep"), "#!/bin/sh\n#\n#\n# "+devShimMarker+"\n")
	if isShim, _ := isRoutingShim(deep); isShim {
		t.Fatal("a marker past the third line made a shim")
	}
	binary := writeDevExecutable(t, filepath.Join(directory, "binary"), "\x7fELF "+devShimMarker)
	if isShim, _ := isRoutingShim(binary); isShim {
		t.Fatal("a binary is never a shim")
	}
	ours := writeDevExecutable(t, filepath.Join(directory, "ours"), devShimScript("pnpm", "/x/ankra"))
	if isShim, isOurs := isRoutingShim(ours); !isShim || !isOurs {
		t.Fatal("our own shim is not recognised")
	}
}

func TestDevShimRunsALocalInvocationWithTheRealTool(t *testing.T) {
	withTempHome(t)
	calls := stubDevExecLocal(t, 7)
	toolchain := devToolchain(t)
	shims := t.TempDir()
	writeDevExecutable(t, filepath.Join(shims, "go"), devShimScript("go", "/x/ankra"))
	t.Setenv("PATH", shims+string(os.PathListSeparator)+toolchain)
	t.Setenv(envAnkraExecMode, "")
	t.Setenv(envAnkraExecExplain, "")
	t.Setenv(envDevShimDepth, "2")

	_, stderr, code := runDevCommand(t, context.Background(), "shim", "go", "--", "run", "./cmd/x", "--", "-flag")
	if code != 7 {
		t.Fatalf("exit = %d, want the local tool's 7; stderr %q", code, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("local runs = %d", len(*calls))
	}
	call := (*calls)[0]
	wantArgv := []string{filepath.Join(toolchain, "go"), "run", "./cmd/x", "--", "-flag"}
	if call.path != filepath.Join(toolchain, "go") || strings.Join(call.argv, " ") != strings.Join(wantArgv, " ") {
		t.Fatalf("local run = %s %q, want %q", call.path, call.argv, wantArgv)
	}
	depths := 0
	for _, entry := range call.environment {
		if strings.HasPrefix(entry, envDevShimDepth+"=") {
			depths++
		}
	}
	if !slices.Contains(call.environment, envDevShimDepth+"=3") || depths != 1 {
		t.Fatalf("the local run carries %d depth entries, want exactly %s=3", depths, envDevShimDepth)
	}
}

func TestDevShimStopsALoopAndAMissingTool(t *testing.T) {
	withTempHome(t)
	calls := stubDevExecLocal(t, 0)
	t.Setenv(envAnkraExecMode, devModeLocal)
	t.Setenv(envAnkraExecExplain, "")
	t.Setenv("PATH", devToolchain(t))
	t.Setenv(envDevShimDepth, "24")
	_, stderr, code := runDevCommand(t, context.Background(), "shim", "go", "--", "version")
	if code != devExitNotFound || !strings.Contains(stderr, "keeps coming back") {
		t.Fatalf("exit = %d stderr %q, want the loop stopped", code, stderr)
	}
	t.Setenv(envDevShimDepth, "")
	t.Setenv("PATH", t.TempDir())
	_, stderr, code = runDevCommand(t, context.Background(), "shim", "go", "--", "version")
	if code != devExitNotFound || !strings.Contains(stderr, "go: not found on PATH") {
		t.Fatalf("exit = %d stderr %q, want 127 for a missing tool", code, stderr)
	}
	if len(*calls) != 0 {
		t.Fatal("something ran")
	}
}

// devRoutedEnv is an exec test environment with a real toolchain on PATH
// behind the shims, the mode cleared and local runs recorded.
func devRoutedEnv(t *testing.T) (*execTestEnv, *[]devLocalCall, string) {
	t.Helper()
	environment := newExecTestEnv(t)
	calls := stubDevExecLocal(t, 0)
	toolchain := devToolchain(t)
	t.Setenv("PATH", toolchain+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(envAnkraExecMode, "")
	t.Setenv(envAnkraExecExplain, "")
	t.Setenv(envDevShimDepth, "")
	return environment, calls, toolchain
}

func TestDevShimRoutesAHeavyInvocationToTheFamilysWorkspace(t *testing.T) {
	environment, calls, _ := devRoutedEnv(t)
	environment.api.profilesBody = `{"repository_id":"repo-1","profiles":{"go":{}}}`
	environment.api.addStreams(streamOutput("ok\n", "", exitCode(3), true))

	stdout, stderr, code := runDevCommand(t, context.Background(), "shim", "go", "--", "test", "./...")
	if code != 3 || stdout != "ok\n" {
		t.Fatalf("exit = %d stdout %q, want the remote run's; stderr:\n%s", code, stdout, stderr)
	}
	if len(*calls) != 0 {
		t.Fatal("a command that ran remotely also ran here")
	}
	if len(environment.api.upRequests) != 1 || environment.api.upRequests[0].Kind != "go" {
		t.Fatalf("up requests = %+v, want the go profile's workspace", environment.api.upRequests)
	}
	request := environment.api.runRequests[0]
	if strings.Join(request.Argv, " ") != "go test ./..." || request.Apply {
		t.Fatalf("run request = %+v", request)
	}
	if environment.notRunMarked() {
		t.Fatal("the shim created the not-run file")
	}
}

func TestDevShimRoutesAWriterWithApplyIntoTheDefaultWorkspace(t *testing.T) {
	environment, _, _ := devRoutedEnv(t)
	environment.api.addStreams(streamOutput("", "", exitCode(0), true))
	if _, stderr, code := runDevCommand(t, context.Background(), "shim", "gofmt", "--", "-w", "pkg"); code != 0 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
	if environment.api.upRequests[0].Kind != defaultWorkspaceKind {
		t.Fatalf("kind = %q, want default: the repository has no go profile", environment.api.upRequests[0].Kind)
	}
	if request := environment.api.runRequests[0]; !request.Apply || strings.Join(request.Argv, " ") != "gofmt -w pkg" {
		t.Fatalf("run request = %+v, want gofmt -w with apply", request)
	}
	if len(environment.api.exportRequests) != 1 || environment.api.exportRequests[0].Kind != "patch" {
		t.Fatalf("exports = %+v, want the patch fetched", environment.api.exportRequests)
	}
}

func TestDevShimRoutesAPlaywrightRunAndFetchesItsResults(t *testing.T) {
	environment, _, _ := devRoutedEnv(t)
	environment.api.profilesBody = `{"repository_id":"repo-1","profiles":{"playwright":{}}}`
	environment.api.addStreams(streamOutput("", "", exitCode(1), true))
	if _, stderr, code := runDevCommand(t, context.Background(), "shim", "pnpm", "--", "test:e2e"); code != 1 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
	if environment.api.upRequests[0].Kind != "playwright" {
		t.Fatalf("kind = %q", environment.api.upRequests[0].Kind)
	}
	request := environment.api.runRequests[0]
	if len(request.Argv) < 6 || request.Argv[0] != "sh" || request.Argv[4] != "pnpm" || request.Argv[5] != "test:e2e" {
		t.Fatalf("argv = %q", request.Argv)
	}
	if len(environment.api.exportRequests) != 1 || environment.api.exportRequests[0].Kind != "dirs" ||
		strings.Join(environment.api.exportRequests[0].Paths, " ") != "test-results playwright-report e2e-report blob-report" {
		t.Fatalf("exports = %+v", environment.api.exportRequests)
	}
}

func TestDevShimRunsLocallyQuietlyWhenTheCheckoutHasNoWorkspace(t *testing.T) {
	environment, calls, toolchain := devRoutedEnv(t)
	environment.api.repositories = nil

	_, stderr, code := runDevCommand(t, context.Background(), "shim", "go", "--", "vet", "./...")
	if code != 0 || len(*calls) != 1 || (*calls)[0].path != filepath.Join(toolchain, "go") {
		t.Fatalf("exit = %d calls %+v; stderr %q, want the real go run here", code, *calls, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("auto mode said %q, want nothing", stderr)
	}
	if len(environment.api.upRequests) != 0 {
		t.Fatal("a workspace was brought up for a repository that is not connected")
	}

	t.Setenv(envAnkraExecMode, devModeRemote)
	_, stderr, _ = runDevCommand(t, context.Background(), "shim", "go", "--", "vet", "./...")
	if !strings.Contains(stderr, "no Ankra Workspace for this checkout; running 'go vet ./...' on this machine") ||
		strings.Count(strings.TrimSpace(stderr), "\n") != 0 {
		t.Fatalf("remote mode said %q, want one line", stderr)
	}
}

func TestDevShimRunsLocallyWhenTheWorkspaceDidNotRunIt(t *testing.T) {
	environment, calls, _ := devRoutedEnv(t)
	environment.api.startFailStatus = http.StatusUnprocessableEntity
	environment.api.startFailBody = `{"detail":"PATH cannot be forwarded."}`

	_, stderr, code := runDevCommand(t, context.Background(), "shim", "go", "--", "test", "./...")
	if code != 0 || len(*calls) != 1 {
		t.Fatalf("exit = %d local runs %d; stderr:\n%s", code, len(*calls), stderr)
	}
	if !strings.Contains(stderr, "running 'go test ./...' on this machine instead") {
		t.Fatalf("stderr = %q, want the fallback said", stderr)
	}
	if environment.notRunMarked() {
		t.Fatal("the in-process run created the not-run file")
	}
}

func TestDevShimPassesARemote196Through(t *testing.T) {
	environment, calls, _ := devRoutedEnv(t)
	environment.api.addStreams(streamOutput("", "", exitCode(execExitNotRun), true))
	if _, _, code := runDevCommand(t, context.Background(), "shim", "go", "--", "test", "./..."); code != execExitNotRun {
		t.Fatalf("exit = %d, want the remote command's own 196", code)
	}
	if len(*calls) != 0 {
		t.Fatal("a command that ran remotely and exited 196 was re-run here")
	}
}

func TestDevShimLocalModeNeverAsksThePlatform(t *testing.T) {
	environment, calls, _ := devRoutedEnv(t)
	t.Setenv(envAnkraExecMode, devModeLocal)
	if _, _, code := runDevCommand(t, context.Background(), "shim", "go", "--", "test", "./..."); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if len(*calls) != 1 || environment.api.requests != 0 {
		t.Fatalf("local runs %d, platform requests %d, want one local run and no request", len(*calls),
			environment.api.requests)
	}
}

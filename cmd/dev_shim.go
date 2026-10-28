package cmd

// `ankra dev shim <tool> [--] <args...>`: what the PATH shims that
// `ankra dev install` writes exec. It decides whether the invocation runs in
// the checkout's Ankra Workspace (dev_route.go), runs it there through the
// same code as `ankra exec`, and otherwise - or when the workspace did not
// run it - runs the real tool on this machine: the next executable of that
// name on PATH that is not a routing shim.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const (
	// devShimMarker is in the first lines of every shim `ankra dev install`
	// writes: how a file is known to be ours before it is replaced, removed
	// or skipped as "not the real tool".
	devShimMarker = "ankra-dev-shim"
	// devLegacyShimMarker opens the bash shim of claude-tools remote-exec,
	// which is never the real tool either.
	devLegacyShimMarker = "# Routing shim for go"
	// devShimHeadBytes is how much of a file is read to tell a shim.
	devShimHeadBytes = 512

	// The modes ANKRA_EXEC (or the saved default) selects.
	devModeAuto   = "auto"
	devModeRemote = "remote"
	devModeLocal  = "local"

	envAnkraExecMode    = "ANKRA_EXEC"
	envAnkraExecExplain = "ANKRA_EXEC_EXPLAIN"
	// envDevShimDepth counts the shims that handed a command to a local
	// tool on the way here.
	envDevShimDepth = "ANKRA_DEV_SHIM_DEPTH"
	// devShimMaxDepth is where a chain of shims handing a command to each
	// other is called a loop. Honest nesting (pnpm runs a script that runs
	// go, whose test runs go build) is a handful deep.
	devShimMaxDepth = 24
	// devExitNotFound is the shell's "command not found".
	devExitNotFound = 127
)

// devExecLocal replaces this process with the real tool; tests replace it.
// It returns only when the tool could not be started (or, where a process
// cannot be replaced, with the tool's exit code as an error).
var devExecLocal = execLocalTool

var devShimCmd = &cobra.Command{
	Use:    "shim <tool> [--] [args...]",
	Short:  "Run a shimmed tool invocation in the workspace or on this machine (used by the dev shims)",
	Hidden: true,
	Long: `What the shims written by 'ankra dev install' run. Not meant to be typed:
call the tool itself (go, gofmt, golangci-lint, pnpm, npm, npx).

ANKRA_EXEC=auto|remote|local overrides the saved mode for one command, and
ANKRA_EXEC_EXPLAIN=1 prints where the invocation would run (route,
route:playwright or local) and runs nothing.`,
	// Everything after the tool belongs to the tool, flags included.
	DisableFlagParsing: true,
	Annotations: map[string]string{
		// Credentials are resolved inside: a missing login means the tool
		// runs here, never an auth error in front of `go test`.
		annotationRequiresAuth: "false",
	},
	RunE: runDevShim,
}

func runDevShim(cmd *cobra.Command, args []string) error {
	if len(args) == 0 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return withExitCode(exitUsage, errors.New("name the tool: ankra dev shim <tool> [--] [args...]"))
	}
	cmd.SilenceErrors = true
	tool, arguments := args[0], args[1:]
	if len(arguments) > 0 && arguments[0] == "--" {
		arguments = arguments[1:]
	}
	shim := &devShim{
		cmd:       cmd,
		stderr:    cmd.ErrOrStderr(),
		tool:      tool,
		arguments: arguments,
	}
	shim.mode, _ = devExecMode()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if code := shim.run(ctx); code != 0 {
		return withExitCode(code, errExecSilent)
	}
	return nil
}

// devShim is one shimmed invocation.
type devShim struct {
	cmd       *cobra.Command
	stderr    io.Writer
	tool      string
	arguments []string
	mode      string
}

func (shim *devShim) say(format string, arguments ...any) {
	_, _ = fmt.Fprintf(shim.stderr, "ankra dev: "+format+"\n", arguments...)
}

// commandLine is the invocation as it was typed, for messages.
func (shim *devShim) commandLine() string {
	return strings.Join(append([]string{shim.tool}, shim.arguments...), " ")
}

// run answers the invocation's exit code. When the tool runs on this machine
// the process is replaced and run does not return.
func (shim *devShim) run(ctx context.Context) int {
	decision := classifyDevInvocation(shim.tool, shim.arguments)
	if shim.mode == devModeLocal {
		decision = devDecision{}
	}
	if os.Getenv(envAnkraExecExplain) != "" {
		_, _ = fmt.Fprintln(shim.cmd.OutOrStdout(), decision.explain())
		return 0
	}
	if decision.IsRouted {
		if code, hasRun := shim.route(ctx, decision); hasRun {
			return code
		}
	}
	return shim.runLocal()
}

// route runs the invocation in the checkout's workspace. hasRun is false
// when it did not run there, and the tool runs here instead: quietly when
// the checkout simply has no workspace to use (auto mode), with one line
// when one was expected.
func (shim *devShim) route(ctx context.Context, decision devDecision) (int, bool) {
	runner := &execRunner{
		cmd:              shim.cmd,
		stdout:           shim.cmd.OutOrStdout(),
		stderr:           shim.stderr,
		argv:             decision.Argv,
		isApplying:       decision.IsApplying,
		fetchDirectories: decision.FetchDirectories,
		startedAt:        execNow(),
		isEmbedded:       true,
	}
	// The cached --check answer first: a checkout that is no pipeline
	// repository, a missing login and an unreachable platform all answer "no"
	// without the retries a real run makes.
	if runner.check(ctx) != 0 {
		if shim.mode == devModeRemote {
			shim.say("no Ankra Workspace for this checkout; running '%s' on this machine", shim.commandLine())
		}
		return 0, false
	}
	checkout, checkoutError := inspectWorkspaceCheckout(ctx, ".")
	if checkoutError != nil {
		return 0, false
	}
	runner.kind = devWorkspaceKind(ctx, shim.cmd, checkout.Remote, decision.Family)
	code := runner.run(ctx)
	if runner.didNotRun {
		shim.say("%s; running '%s' on this machine instead", runner.notRunReason, shim.commandLine())
		return 0, false
	}
	return code, true
}

// runLocal hands the invocation to the real tool on this machine.
func (shim *devShim) runLocal() int {
	depth, _ := strconv.Atoi(os.Getenv(envDevShimDepth))
	if depth >= devShimMaxDepth {
		shim.say("%s keeps coming back to the routing shim (%d times): check PATH for a wrapper that calls it",
			shim.tool, depth)
		return devExitNotFound
	}
	realTool, isFound := findRealTool(shim.tool, os.Getenv("PATH"))
	if !isFound {
		_, _ = fmt.Fprintf(shim.stderr, "%s: not found on PATH (outside the ankra dev shims), and not run remotely\n",
			shim.tool)
		return devExitNotFound
	}
	environment := withEnvironmentValue(os.Environ(), envDevShimDepth, strconv.Itoa(depth+1))
	execError := devExecLocal(realTool, append([]string{realTool}, shim.arguments...), environment)
	if execError == nil {
		return 0
	}
	var exited *devLocalExit
	if errors.As(execError, &exited) {
		return exited.code
	}
	_, _ = fmt.Fprintf(shim.stderr, "%s: %v\n", realTool, execError)
	return devExitNotFound
}

// withEnvironmentValue answers environment with name set to value, every
// earlier entry for it dropped: a child reading the first of two entries
// would otherwise see the old depth and never stop a loop.
func withEnvironmentValue(environment []string, name string, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, name+"=") {
			result = append(result, entry)
		}
	}
	return append(result, name+"="+value)
}

// devLocalExit is the exit code of a tool that ran as a child, where the
// process could not be replaced.
type devLocalExit struct{ code int }

func (exited *devLocalExit) Error() string { return "exit status " + strconv.Itoa(exited.code) }

// devExecMode answers the routing mode and where it came from: ANKRA_EXEC,
// then the CLI's own setting (ankra dev mode), then the bash client's
// ~/.config/ankra-exec/mode, then auto.
func devExecMode() (string, string) {
	if mode, isValid := normalizeDevMode(os.Getenv(envAnkraExecMode)); isValid {
		return mode, envAnkraExecMode
	}
	if mode, isValid := normalizeDevMode(savedDevMode()); isValid {
		return mode, "ankra dev mode"
	}
	if legacyPath := devLegacyModeFile(); legacyPath != "" {
		if contents, readError := os.ReadFile(legacyPath); readError == nil {
			if mode, isValid := normalizeDevMode(string(contents)); isValid {
				return mode, legacyPath
			}
		}
	}
	return devModeAuto, "default"
}

// savedDevMode reads the mode `ankra dev mode` saved in ~/.ankra/settings.json,
// without creating anything: every shim invocation asks.
func savedDevMode() string {
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return ""
	}
	contents, readError := os.ReadFile(filepath.Join(home, ".ankra", "settings.json"))
	if readError != nil {
		return ""
	}
	var settings CLISettings
	if unmarshalError := json.Unmarshal(contents, &settings); unmarshalError != nil {
		return ""
	}
	return settings.ExecMode
}

// devLegacyModeFile is where the bash client keeps its default mode.
func devLegacyModeFile() string {
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return ""
	}
	return filepath.Join(home, ".config", "ankra-exec", "mode")
}

// normalizeDevMode reads a mode the way the bash shim does: only its
// lower-case letters count. Anything that is not a mode is no setting.
func normalizeDevMode(raw string) (string, bool) {
	var letters strings.Builder
	for _, character := range raw {
		if character >= 'a' && character <= 'z' {
			letters.WriteRune(character)
		}
	}
	switch mode := letters.String(); mode {
	case devModeAuto, devModeRemote, devModeLocal:
		return mode, true
	}
	return "", false
}

// isRoutingShim reports whether the file at path (through any links) is a
// routing shim - one of ours or the bash client's - and whether it is ours.
func isRoutingShim(path string) (bool, bool) {
	file, openError := os.Open(path)
	if openError != nil {
		return false, false
	}
	defer func() { _ = file.Close() }()
	head := make([]byte, devShimHeadBytes)
	read, _ := io.ReadFull(file, head)
	head = head[:read]
	if !bytes.HasPrefix(head, []byte("#!")) {
		return false, false
	}
	// The marker sits in the first three lines, as the bash shim's does.
	lines := bytes.SplitN(head, []byte("\n"), 4)
	if len(lines) > 3 {
		lines = lines[:3]
	}
	top := bytes.Join(lines, []byte("\n"))
	if bytes.Contains(top, []byte(devShimMarker)) {
		return true, true
	}
	return bytes.Contains(top, []byte(devLegacyShimMarker)), false
}

// firstOnPath answers the first executable named tool on searchPath, shim
// or not.
func firstOnPath(tool string, searchPath string) (string, bool) {
	for _, directory := range filepath.SplitList(searchPath) {
		if directory == "" {
			continue
		}
		candidate := filepath.Join(directory, tool)
		if isExecutableFile(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// findRealTool answers the next executable named tool on searchPath that is
// not a routing shim. Every copy of a shim is skipped by what it contains,
// wherever it sits and however it is linked, so two installs on one PATH
// never hand a command back and forth. A toolchain directory the bash
// installer wrapped keeps its real tool beside the shim as <tool>.slot (the
// go-slot queue) or <tool>.real; those are taken.
func findRealTool(tool string, searchPath string) (string, bool) {
	for _, directory := range filepath.SplitList(searchPath) {
		if directory == "" {
			continue
		}
		candidate := filepath.Join(directory, tool)
		if !isExecutableFile(candidate) {
			continue
		}
		if isShim, _ := isRoutingShim(candidate); !isShim {
			return candidate, true
		}
		for _, suffix := range []string{".slot", ".real"} {
			if wrapped := candidate + suffix; isExecutableFile(wrapped) {
				if isShim, _ := isRoutingShim(wrapped); !isShim {
					return wrapped, true
				}
			}
		}
	}
	return "", false
}

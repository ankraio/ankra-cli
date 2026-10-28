package cmd

// `ankra dev`: the remote dev loop on this machine. `install` writes PATH
// shims for the heavy developer tools, which send their heavy invocations to
// the checkout's Ankra Workspace through `ankra exec`'s own code and run
// everything else locally; `status` shows what is installed and which tool
// wins on PATH; `mode` sets the default routing mode (ankra-b5c3as.27).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Run this machine's heavy dev commands (test, lint, typecheck) in an Ankra Workspace",
	Long: `Send the heavy commands of a development loop - go test, go vet, go build,
golangci-lint, pnpm typecheck / test / lint, vitest, tsc --noEmit, Playwright
suites - to the checkout's Ankra Workspace instead of running them on this
machine, without changing what you or a coding agent type.

'ankra dev install' puts small shims named go, gofmt, golangci-lint, pnpm, npm
and npx in front of the real tools on PATH. Each shim asks 'ankra' where the
invocation belongs:

  routed          go test|vet|build (no -o, -c, coverage or profile files),
                  golangci-lint run, gofmt -l|-d <paths>, pnpm|npm typecheck,
                  test, test:ci, test:unit, test:shard, lint, lint:*,
                  format-check, knip, vitest run, tsc --noEmit
  routed, changes go generate, go mod tidy|vendor|edit, go get, go fix, go fmt,
  applied here    go tool ..., gofmt -w, golangci-lint run --fix, golangci-lint fmt
  routed, results Playwright test runs (pnpm|npm test:e2e*, e2e*, [exec]
  copied back     playwright, npx playwright test): test-results and reports
  local           everything else: go run, go install, go -C, dev servers,
                  builds, pnpm install|add, anything interactive (--ui,
                  --debug, --headed, codegen), gofmt reading stdin

A routed command runs exactly like 'ankra exec': same output, same exit code.
When the checkout has no workspace to use (not a repository connected to Ankra
Pipelines, not logged in, offline) or the workspace could not run it, the real
tool runs on this machine instead, so nothing ever blocks on the platform.

The workspace kind follows the tool: go, gofmt and golangci-lint use the
repository's "go" workspace profile, pnpm, npm and npx its "node" profile and
Playwright runs its "e2e" (or "playwright") profile - when the repository has
that profile. Otherwise they all share its default workspace.

Mode: ANKRA_EXEC=auto|remote|local for one command, 'ankra dev mode' for the
default. ANKRA_EXEC_EXPLAIN=1 <command> prints route or local and runs nothing.`,
	Example: `  ankra dev install
  ankra dev status
  ANKRA_EXEC_EXPLAIN=1 go test ./...
  ANKRA_EXEC=local pnpm typecheck
  ankra dev mode local`,
}

var devModeCmd = &cobra.Command{
	Use:   "mode [auto|remote|local]",
	Short: "Show or set the default routing mode of the dev shims",
	Long: `Show the routing mode the dev shims use, or set the default.

  auto    route when the checkout has a workspace, otherwise run locally (default)
  remote  the same, and say so in one line when a routable command runs locally
  local   never route: every command runs on this machine

The ANKRA_EXEC environment variable overrides the default for one command. A
default that was never set here is read from ~/.config/ankra-exec/mode, where
the earlier remote-exec scripts kept theirs.`,
	Example: `  ankra dev mode
  ankra dev mode local`,
	Args:      cobra.MaximumNArgs(1),
	ValidArgs: []string{devModeAuto, devModeRemote, devModeLocal},
	RunE:      runDevMode,
}

func runDevMode(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		mode, source := devExecMode()
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", mode, source)
		return nil
	}
	mode, isValid := normalizeDevMode(args[0])
	if !isValid || mode != strings.TrimSpace(args[0]) {
		return withExitCode(exitUsage, fmt.Errorf("unknown mode %q: use auto, remote or local", args[0]))
	}
	settings, loadError := loadCLISettings()
	if loadError != nil {
		return fmt.Errorf("read settings: %w", loadError)
	}
	settings.ExecMode = mode
	if saveError := saveCLISettings(settings); saveError != nil {
		return fmt.Errorf("save settings: %w", saveError)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Dev shim mode: %s\n", mode)
	if effective, source := devExecMode(); effective != mode {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Note: %s=%s is set in this shell and overrides it.\n", source, effective)
	}
	return nil
}

// devToolStatus is one shimmed tool as `ankra dev status` reports it.
type devToolStatus struct {
	Tool string `json:"tool" yaml:"tool"`
	// IsShimmed says the shim file exists in the shim directory.
	IsShimmed bool `json:"shimmed" yaml:"shimmed"`
	// IsFirstOnPath says the tool resolves to that shim on PATH.
	IsFirstOnPath bool `json:"first_on_path" yaml:"first_on_path"`
	// ResolvesTo is the first executable of that name on PATH.
	ResolvesTo string `json:"resolves_to,omitempty" yaml:"resolves_to,omitempty"`
	// Real is the tool a local run uses: the next one that is no shim.
	Real string `json:"real,omitempty" yaml:"real,omitempty"`
	// Note explains a tool that does not resolve to the shim.
	Note string `json:"note,omitempty" yaml:"note,omitempty"`
}

// devStatus is what `ankra dev status` reports.
type devStatus struct {
	Mode        string          `json:"mode" yaml:"mode"`
	ModeSource  string          `json:"mode_source" yaml:"mode_source"`
	IsInstalled bool            `json:"installed" yaml:"installed"`
	ShimDir     string          `json:"shim_dir" yaml:"shim_dir"`
	LinkDir     string          `json:"link_dir,omitempty" yaml:"link_dir,omitempty"`
	Ankra       string          `json:"ankra,omitempty" yaml:"ankra,omitempty"`
	Tools       []devToolStatus `json:"tools" yaml:"tools"`
}

var devStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the routing mode, the shim directory and which tools resolve to a shim",
	Long: `Show how the dev shims are set up on this machine: the routing mode and where
it comes from, the shim directory, and for each tool whether its shim is
installed and whether it is what the name resolves to first on PATH (a tool
installed in front of it would bypass routing). Reads nothing from the
platform.`,
	Example: `  ankra dev status
  ankra dev status -o json`,
	Args: cobra.NoArgs,
	RunE: runDevStatus,
}

func runDevStatus(cmd *cobra.Command, _ []string) error {
	if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
		return formatError
	}
	installation, isInstalled := readDevInstallation()
	if !isInstalled {
		defaults, defaultsError := defaultDevInstallation()
		if defaultsError != nil {
			return defaultsError
		}
		installation = defaults
		installation.LinkDir = ""
	}
	status := collectDevStatus(installation, os.Getenv("PATH"))
	if written, renderError := renderStructured(cmd, status); written || renderError != nil {
		return renderError
	}
	printDevStatus(cmd.OutOrStdout(), status)
	if !status.IsInstalled {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nNot installed. Run 'ankra dev install'.")
	}
	return nil
}

// collectDevStatus reads the installation's state off the disk and
// searchPath.
func collectDevStatus(installation devInstallation, searchPath string) devStatus {
	mode, source := devExecMode()
	status := devStatus{
		Mode:       mode,
		ModeSource: source,
		ShimDir:    installation.ShimDir,
		LinkDir:    installation.LinkDir,
		Ankra:      installation.Ankra,
	}
	for _, tool := range devShimTools {
		shimPath := filepath.Join(installation.ShimDir, tool)
		toolStatus := devToolStatus{Tool: tool}
		if _, isOurs := isRoutingShim(shimPath); isOurs {
			toolStatus.IsShimmed = true
			status.IsInstalled = true
		}
		if realTool, isFound := findRealTool(tool, searchPath); isFound {
			toolStatus.Real = realTool
		}
		first, isFound := firstOnPath(tool, searchPath)
		switch {
		case !isFound:
			toolStatus.Note = "not on PATH"
		default:
			toolStatus.ResolvesTo = first
			isShim, isOurs := isRoutingShim(first)
			switch {
			case toolStatus.IsShimmed && samePath(first, shimPath):
				toolStatus.IsFirstOnPath = true
			case isOurs:
				toolStatus.Note = "another ankra dev shim comes first"
			case isShim:
				toolStatus.Note = "the claude-tools remote-exec shim comes first"
			default:
				toolStatus.Note = "the real tool comes first: routing is bypassed"
			}
		}
		if toolStatus.IsFirstOnPath && toolStatus.Real == "" {
			toolStatus.Note = "no real tool on PATH: local runs fail"
		}
		status.Tools = append(status.Tools, toolStatus)
	}
	return status
}

func printDevStatus(out io.Writer, status devStatus) {
	_, _ = fmt.Fprintf(out, "Mode:      %s (%s)\n", status.Mode, status.ModeSource)
	_, _ = fmt.Fprintf(out, "Shim dir:  %s\n", status.ShimDir)
	if status.LinkDir != "" {
		_, _ = fmt.Fprintf(out, "Links in:  %s\n", status.LinkDir)
	}
	if status.Ankra != "" {
		_, _ = fmt.Fprintf(out, "CLI:       %s\n", status.Ankra)
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "%-14s %-8s %-14s %s\n", "TOOL", "SHIM", "FIRST ON PATH", "RESOLVES TO")
	for _, tool := range status.Tools {
		resolves := tool.ResolvesTo
		if tool.Note != "" {
			if resolves != "" {
				resolves += "  (" + tool.Note + ")"
			} else {
				resolves = "(" + tool.Note + ")"
			}
		}
		_, _ = fmt.Fprintf(out, "%-14s %-8s %-14s %s\n", tool.Tool, yesNo(tool.IsShimmed), yesNo(tool.IsFirstOnPath),
			resolves)
	}
}

// samePath reports whether two paths name the same file: as written, or
// once every link is followed.
func samePath(left string, right string) bool {
	if left == "" || right == "" {
		return false
	}
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	resolvedLeft, leftError := filepath.EvalSymlinks(left)
	resolvedRight, rightError := filepath.EvalSymlinks(right)
	return leftError == nil && rightError == nil && resolvedLeft == resolvedRight
}

// errDevUnsupported is what the shim commands answer where no POSIX shell
// runs the shims.
var errDevUnsupported = errors.New("the dev shims are POSIX shell scripts: 'ankra dev install' supports macOS and Linux " +
	"(on Windows, run it inside WSL)")

func init() {
	devInstallCmd.Flags().Bool("uninstall", false, "Remove the shims and links a previous install created")
	devInstallCmd.Flags().String("dir", "", "Directory the shims are written to (default ~/.ankra/dev/shims)")
	devInstallCmd.Flags().String("link-dir", "", "Directory already on PATH to link the shims into "+
		"(default ~/.local/bin when it is on PATH; empty links nothing)")
	devInstallCmd.Flags().Bool("replace-legacy", false, "Replace links that point at the claude-tools remote-exec shim")
	registerStructuredOutputFlags(devStatusCmd)
	devCmd.AddCommand(devInstallCmd, devStatusCmd, devModeCmd, devShimCmd)
	setRequiresAuth(devCmd, false)
	rootCmd.AddCommand(devCmd)
}

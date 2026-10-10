package cmd

// `ankra dev install [--uninstall]`: the shim files, their links into a
// directory already on PATH, and the record of what was created. It only
// ever replaces or removes a file it can prove is one of its own shims (by
// the marker in it) or a link that points at one; a real tool is never
// touched, and no shell profile is edited - the PATH line to add is printed.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// devShimVersion is bumped when the shim script changes shape; a re-run of
// install rewrites every shim either way.
const devShimVersion = 1

// envDevCLI overrides the CLI a shim runs (a development build).
const envDevCLI = "ANKRA_DEV_CLI"

// devInstallation is what an install created, recorded so status and
// --uninstall act on the same directories without being told.
type devInstallation struct {
	ShimDir string `json:"shim_dir" yaml:"shim_dir"`
	// LinkDir is the PATH directory the shims are linked into, "" when
	// none.
	LinkDir string   `json:"link_dir,omitempty" yaml:"link_dir,omitempty"`
	Tools   []string `json:"tools" yaml:"tools"`
	// Ankra is the CLI the shims run first.
	Ankra string `json:"ankra,omitempty" yaml:"ankra,omitempty"`
}

var devInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install (or remove) the PATH shims that route heavy dev commands to the workspace",
	Long: `Write the routing shims for go, gofmt, golangci-lint, pnpm, npm and npx and
put them in front of the real tools.

The shims go to ~/.ankra/dev/shims (--dir) and are linked into a directory
that is already on PATH, ~/.local/bin by default (--link-dir), so running
sessions and git hooks pick them up at once. When no such directory is on
PATH, or a real tool still comes first, the exact PATH line to add to your
shell profile is printed; no profile is ever edited for you.

Safe to run again (it upgrades the shims in place). A name that is taken by
a real file or by a link to something else is left alone and reported. Links
that point at the earlier claude-tools remote-exec shim are replaced only
with --replace-legacy.

--uninstall removes exactly what an install created: the links that point at
its shims and the shim files themselves. Commands then run locally again.

Each shim is a few lines of POSIX sh that hand the invocation to
'ankra dev shim'; without an ankra CLI it runs the real tool, so removing the
CLI never breaks go or pnpm. See 'ankra dev --help' for what routes.`,
	Example: `  ankra dev install
  ankra dev install --link-dir ~/bin
  ankra dev install --dir ~/.local/share/ankra/shims --link-dir ""
  ankra dev install --uninstall`,
	Args: cobra.NoArgs,
	RunE: runDevInstall,
}

func runDevInstall(cmd *cobra.Command, _ []string) error {
	if runtime.GOOS == "windows" {
		return withExitCode(exitUsage, errDevUnsupported)
	}
	isUninstalling, _ := cmd.Flags().GetBool("uninstall")
	shouldReplaceLegacy, _ := cmd.Flags().GetBool("replace-legacy")
	requested, requestError := requestedDevInstallation(cmd)
	if requestError != nil {
		return requestError
	}
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if isUninstalling {
		removed := uninstallDevShims(requested, errOut)
		forgetDevInstallation()
		if removed == 0 {
			_, _ = fmt.Fprintf(out, "Nothing to remove: no ankra dev shims in %s.\n", requested.ShimDir)
			return nil
		}
		_, _ = fmt.Fprintf(out, "Removed %d shim file(s) and link(s). Commands run on this machine again.\n", removed)
		return nil
	}

	// An earlier install into other directories is taken out first, so a
	// changed --dir or --link-dir moves the install instead of doubling it.
	if previous, hadPrevious := readDevInstallation(); hadPrevious &&
		(!samePath(previous.ShimDir, requested.ShimDir) || previous.LinkDir != requested.LinkDir) {
		uninstallDevShims(previous, errOut)
	}
	if installError := installDevShims(requested, shouldReplaceLegacy, errOut); installError != nil {
		return installError
	}
	if recordError := recordDevInstallation(requested); recordError != nil {
		_, _ = fmt.Fprintf(errOut, "Could not record the install (%v): pass the same --dir and --link-dir to "+
			"--uninstall.\n", recordError)
	}
	status := collectDevStatus(requested, os.Getenv("PATH"))
	_, _ = fmt.Fprintf(out, "Installed the dev shims in %s.\n\n", requested.ShimDir)
	printDevStatus(out, status)
	printDevPathAdvice(out, requested, status)
	_, _ = fmt.Fprintln(out, "\nSwitch routing off with 'ankra dev mode local' (ANKRA_EXEC=local for one command); "+
		"remove the shims with 'ankra dev install --uninstall'.")
	return nil
}

// defaultDevInstallation is the install a plain `ankra dev install` makes.
func defaultDevInstallation() (devInstallation, error) {
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return devInstallation{}, fmt.Errorf("finding the home directory: %w", homeError)
	}
	installation := devInstallation{
		ShimDir: filepath.Join(home, ".ankra", "dev", "shims"),
		Tools:   append([]string{}, devShimTools...),
	}
	localBin := filepath.Join(home, ".local", "bin")
	if isDirectory(localBin) && isOnPath(localBin, os.Getenv("PATH")) {
		installation.LinkDir = localBin
	}
	return installation, nil
}

// requestedDevInstallation resolves the directories this invocation acts on:
// the flags, else what the last install recorded, else the defaults.
func requestedDevInstallation(cmd *cobra.Command) (devInstallation, error) {
	installation, defaultsError := defaultDevInstallation()
	if defaultsError != nil {
		return devInstallation{}, defaultsError
	}
	isUninstalling, _ := cmd.Flags().GetBool("uninstall")
	if previous, hadPrevious := readDevInstallation(); hadPrevious && isUninstalling {
		installation.ShimDir, installation.LinkDir = previous.ShimDir, previous.LinkDir
	}
	if cmd.Flags().Changed("dir") {
		directory, _ := cmd.Flags().GetString("dir")
		if strings.TrimSpace(directory) == "" {
			return devInstallation{}, withExitCode(exitUsage, errors.New("--dir must name a directory"))
		}
		absolute, absoluteError := filepath.Abs(expandHome(directory))
		if absoluteError != nil {
			return devInstallation{}, withExitCode(exitUsage, fmt.Errorf("--dir: %w", absoluteError))
		}
		installation.ShimDir = absolute
	}
	if cmd.Flags().Changed("link-dir") {
		directory, _ := cmd.Flags().GetString("link-dir")
		installation.LinkDir = ""
		if strings.TrimSpace(directory) != "" {
			absolute, absoluteError := filepath.Abs(expandHome(directory))
			if absoluteError != nil {
				return devInstallation{}, withExitCode(exitUsage, fmt.Errorf("--link-dir: %w", absoluteError))
			}
			if !isUninstalling && !isDirectory(absolute) {
				return devInstallation{}, withExitCode(exitUsage, fmt.Errorf(
					"--link-dir %s is not a directory: name one that is on PATH", absolute))
			}
			installation.LinkDir = absolute
		}
	}
	if samePath(installation.LinkDir, installation.ShimDir) {
		installation.LinkDir = ""
	}
	if executable, executableError := os.Executable(); executableError == nil {
		installation.Ankra = executable
	}
	return installation, nil
}

// expandHome turns a leading ~/ into the home directory, for a flag value
// the shell did not expand (--dir=~/x).
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

func isDirectory(path string) bool {
	info, statError := os.Stat(path)
	return statError == nil && info.IsDir()
}

// isOnPath reports whether directory is an entry of searchPath.
func isOnPath(directory string, searchPath string) bool {
	for _, entry := range filepath.SplitList(searchPath) {
		if entry != "" && samePath(entry, directory) {
			return true
		}
	}
	return false
}

// devShimScript is the shim for one tool. The marker in its second line is
// what makes the file recognisably ours.
func devShimScript(tool string, ankraPath string) string {
	return fmt.Sprintf(`#!/bin/sh
# %[1]s %[2]d: written by 'ankra dev install'; remove with 'ankra dev install --uninstall'.
# Hands every %[3]s invocation to 'ankra dev shim', which runs the heavy ones in
# this checkout's Ankra Workspace and everything else with the real %[3]s.
tool=%[4]s
for ankra in "${%[5]s:-}" %[6]s "$(command -v ankra 2>/dev/null)"; do
  if [ -n "$ankra" ] && [ -x "$ankra" ] && [ ! -d "$ankra" ]; then
    exec "$ankra" dev shim "$tool" -- "$@"
  fi
done
# No ankra CLI on this machine: run the real tool, the next one on PATH that
# is not a routing shim.
rest=$PATH:
while [ -n "$rest" ]; do
  dir=${rest%%%%:*}
  rest=${rest#*:}
  [ -n "$dir" ] || continue
  candidate=$dir/$tool
  [ -f "$candidate" ] && [ -x "$candidate" ] || continue
  if head -n 3 "$candidate" 2>/dev/null | LC_ALL=C grep -q -e '%[1]s' -e '^%[7]s' 2>/dev/null; then
    continue
  fi
  exec "$candidate" "$@"
done
echo "$tool: not found on PATH (outside the ankra dev shims)" >&2
exit 127
`, devShimMarker, devShimVersion, tool, shellQuote(tool), envDevCLI, shellQuote(ankraPath), devLegacyShimMarker)
}

// shellQuote single-quotes a value for POSIX sh.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// installDevShims writes the shim files and links them. A name it may not
// take is reported on notes and skipped; only a failure to write its own
// files is an error.
func installDevShims(installation devInstallation, shouldReplaceLegacy bool, notes io.Writer) error {
	if mkdirError := os.MkdirAll(installation.ShimDir, 0o755); mkdirError != nil {
		return fmt.Errorf("creating the shim directory: %w", mkdirError)
	}
	for _, tool := range installation.Tools {
		shimPath := filepath.Join(installation.ShimDir, tool)
		if info, statError := os.Lstat(shimPath); statError == nil {
			_, isOurs := isRoutingShim(shimPath)
			if !info.Mode().IsRegular() || !isOurs {
				_, _ = fmt.Fprintf(notes, "Not writing %s: something that is not an ankra dev shim is there.\n", shimPath)
				continue
			}
		}
		if writeError := writeFileAtomically(shimPath, []byte(devShimScript(tool, installation.Ankra)), 0o755); writeError != nil {
			return fmt.Errorf("writing %s: %w", shimPath, writeError)
		}
		if installation.LinkDir == "" {
			continue
		}
		linkPath := filepath.Join(installation.LinkDir, tool)
		if reason := linkDevShim(linkPath, shimPath, shouldReplaceLegacy); reason != "" {
			_, _ = fmt.Fprintf(notes, "Not linking %s: %s.\n", linkPath, reason)
		}
	}
	return nil
}

// linkDevShim makes linkPath a link to shimPath. It answers why not when
// the name is taken by something it may not replace.
func linkDevShim(linkPath string, shimPath string, shouldReplaceLegacy bool) string {
	info, statError := os.Lstat(linkPath)
	switch {
	case errors.Is(statError, os.ErrNotExist):
	case statError != nil:
		return statError.Error()
	case info.Mode()&os.ModeSymlink == 0:
		return "a real file is there"
	default:
		if samePath(linkTarget(linkPath), shimPath) {
			return ""
		}
		isShim, isOurs := isRoutingShim(linkPath)
		switch {
		case isOurs:
			// A link to another copy of our shim (an earlier --dir).
		case isShim && shouldReplaceLegacy:
		case isShim:
			return "it points at the claude-tools remote-exec shim (" + linkTarget(linkPath) +
				"); pass --replace-legacy to take it over"
		default:
			return "it is a link to " + linkTarget(linkPath)
		}
	}
	temporary := fmt.Sprintf("%s.ankra-dev-%d", linkPath, os.Getpid())
	_ = os.Remove(temporary)
	if linkError := os.Symlink(shimPath, temporary); linkError != nil {
		return linkError.Error()
	}
	if renameError := os.Rename(temporary, linkPath); renameError != nil {
		_ = os.Remove(temporary)
		return renameError.Error()
	}
	return ""
}

// linkTarget is where a link points, as an absolute path.
func linkTarget(linkPath string) string {
	target, readError := os.Readlink(linkPath)
	if readError != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	return filepath.Clean(target)
}

// uninstallDevShims removes what an install created: every link in the link
// directory that points at one of the installation's shims, then every shim
// file that carries the marker. Anything else is left and reported. It
// answers how many files and links it removed.
func uninstallDevShims(installation devInstallation, notes io.Writer) int {
	removed := 0
	tools := installation.Tools
	if len(tools) == 0 {
		tools = devShimTools
	}
	for _, tool := range tools {
		shimPath := filepath.Join(installation.ShimDir, tool)
		if installation.LinkDir != "" {
			linkPath := filepath.Join(installation.LinkDir, tool)
			if info, statError := os.Lstat(linkPath); statError == nil && info.Mode()&os.ModeSymlink != 0 &&
				samePath(linkTarget(linkPath), shimPath) {
				if removeError := os.Remove(linkPath); removeError != nil {
					_, _ = fmt.Fprintf(notes, "Could not remove %s: %v\n", linkPath, removeError)
				} else {
					removed++
				}
			}
		}
		info, statError := os.Lstat(shimPath)
		if statError != nil {
			continue
		}
		if _, isOurs := isRoutingShim(shimPath); !info.Mode().IsRegular() || !isOurs {
			_, _ = fmt.Fprintf(notes, "Leaving %s: it is not an ankra dev shim.\n", shimPath)
			continue
		}
		if removeError := os.Remove(shimPath); removeError != nil {
			_, _ = fmt.Fprintf(notes, "Could not remove %s: %v\n", shimPath, removeError)
			continue
		}
		removed++
	}
	// The directory goes only when nothing else is in it.
	_ = os.Remove(installation.ShimDir)
	return removed
}

// writeFileAtomically writes contents beside path and renames it in, so a
// shim that is being executed is never seen half written.
func writeFileAtomically(path string, contents []byte, mode os.FileMode) error {
	temporary, createError := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".")
	if createError != nil {
		return createError
	}
	_, writeError := temporary.Write(contents)
	chmodError := temporary.Chmod(mode)
	closeError := temporary.Close()
	if joined := errors.Join(writeError, chmodError, closeError); joined != nil {
		_ = os.Remove(temporary.Name())
		return joined
	}
	if renameError := os.Rename(temporary.Name(), path); renameError != nil {
		_ = os.Remove(temporary.Name())
		return renameError
	}
	return nil
}

// devInstallationFile is where the last install is recorded.
func devInstallationFile() string {
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return ""
	}
	return filepath.Join(home, ".ankra", "dev", "install.json")
}

func readDevInstallation() (devInstallation, bool) {
	path := devInstallationFile()
	if path == "" {
		return devInstallation{}, false
	}
	contents, readError := os.ReadFile(path)
	if readError != nil {
		return devInstallation{}, false
	}
	var installation devInstallation
	if unmarshalError := json.Unmarshal(contents, &installation); unmarshalError != nil || installation.ShimDir == "" {
		return devInstallation{}, false
	}
	if len(installation.Tools) == 0 {
		installation.Tools = append([]string{}, devShimTools...)
	}
	return installation, true
}

func recordDevInstallation(installation devInstallation) error {
	path := devInstallationFile()
	if path == "" {
		return errors.New("no home directory")
	}
	encoded, marshalError := json.MarshalIndent(installation, "", "  ")
	if marshalError != nil {
		return marshalError
	}
	if mkdirError := os.MkdirAll(filepath.Dir(path), 0o755); mkdirError != nil {
		return mkdirError
	}
	return writeFileAtomically(path, append(encoded, '\n'), 0o644)
}

// forgetDevInstallation drops the record, and its directory when empty.
func forgetDevInstallation() {
	path := devInstallationFile()
	if path == "" {
		return
	}
	_ = os.Remove(path)
	_ = os.Remove(filepath.Dir(path))
}

// printDevPathAdvice says what is left to do by hand: when a tool does not
// resolve to its shim, the PATH line that makes it.
func printDevPathAdvice(out io.Writer, installation devInstallation, status devStatus) {
	var shadowed []string
	for _, tool := range status.Tools {
		if tool.IsShimmed && !tool.IsFirstOnPath {
			shadowed = append(shadowed, tool.Tool)
		}
	}
	if len(shadowed) == 0 {
		_, _ = fmt.Fprintln(out, "\nEvery shim is first on PATH: heavy commands route from the next command on.")
		return
	}
	_, _ = fmt.Fprintf(out, "\nNot first on PATH yet: %s.\n", strings.Join(shadowed, ", "))
	directory := shellHomePath(installation.ShimDir)
	switch filepath.Base(os.Getenv("SHELL")) {
	case "fish":
		_, _ = fmt.Fprintf(out, "Run once (fish):\n\n  fish_add_path --prepend --move %s\n", directory)
	case "zsh":
		_, _ = fmt.Fprintf(out, "Add this as the LAST line of ~/.zshrc, and to ~/.zshenv for non-interactive shells "+
			"and git hooks:\n\n  export PATH=%s:\"$PATH\"\n", directory)
	case "bash":
		_, _ = fmt.Fprintf(out, "Add this as the LAST line of ~/.bashrc and ~/.bash_profile:\n\n  export PATH=%s:\"$PATH\"\n",
			directory)
	default:
		_, _ = fmt.Fprintf(out, "Add this as the LAST line of your shell profile:\n\n  export PATH=%s:\"$PATH\"\n", directory)
	}
	_, _ = fmt.Fprintln(out, "\nThen open a new shell and check with 'ankra dev status'.")
}

// shellHomePath writes a path for a shell profile: "$HOME/..." when it is
// under the home directory, quoted either way.
func shellHomePath(path string) string {
	if home, homeError := os.UserHomeDir(); homeError == nil && home != "" {
		if relative, relativeError := filepath.Rel(home, path); relativeError == nil &&
			relative != "." && !strings.HasPrefix(relative, "..") {
			return `"$HOME/` + strings.NewReplacer(`"`, `\"`, `$`, `\$`, "`", "\\`").Replace(filepath.ToSlash(relative)) + `"`
		}
	}
	return shellQuote(path)
}

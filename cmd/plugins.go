package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// The CLI dispatches unknown commands to external plugins the way kubectl
// does: an executable named ankra-<command> on PATH or in ~/.ankra/plugins
// runs as `ankra <command>`, with the remaining arguments and this
// process's standard streams. Built-in commands always win, so a plugin can
// extend the CLI but never change it.

// externalPluginPrefix is what a plugin executable's file name starts with.
const externalPluginPrefix = "ankra-"

// pluginReservedNames are cobra's own entry points; they are dispatchable
// commands even though they are not in the command list.
var pluginReservedNames = map[string]bool{
	"help": true, "completion": true, "__complete": true, "__completeNoDesc": true,
}

// dispatchExternalPlugin runs the plugin matching the arguments, if one
// exists and the first argument is not a built-in command. It reports
// whether a plugin ran and the exit code to finish with.
func dispatchExternalPlugin(arguments []string) (bool, int) {
	if len(arguments) == 0 || strings.HasPrefix(arguments[0], "-") {
		return false, 0
	}
	if isKnownRootCommand(arguments[0]) {
		return false, 0
	}
	nameParts := leadingCommandWords(arguments)
	path, consumed := lookupExternalPlugin(nameParts)
	if path == "" {
		return false, 0
	}
	command := exec.Command(path, arguments[consumed:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = append(os.Environ(), "ANKRA_CLI_VERSION="+version)
	runError := command.Run()
	if runError == nil {
		return true, 0
	}
	var exitError *exec.ExitError
	if isExit := isExitError(runError, &exitError); isExit {
		return true, exitError.ExitCode()
	}
	_, _ = fmt.Fprintf(os.Stderr, "running %s: %v\n", path, runError)
	return true, 1
}

// isExitError exists so the errors.As call reads as a question at the one
// place it is asked.
func isExitError(runError error, target **exec.ExitError) bool {
	exitError, ok := runError.(*exec.ExitError)
	if ok {
		*target = exitError
	}
	return ok
}

// isKnownRootCommand says whether name is a built-in command or one of
// cobra's reserved entry points; those are never dispatched to a plugin.
func isKnownRootCommand(name string) bool {
	if pluginReservedNames[name] {
		return true
	}
	for _, command := range rootCmd.Commands() {
		if command.Name() == name {
			return true
		}
		for _, alias := range command.Aliases {
			if alias == name {
				return true
			}
		}
	}
	return false
}

// leadingCommandWords is the run of arguments before the first flag: the
// words that can name a plugin.
func leadingCommandWords(arguments []string) []string {
	var words []string
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "-") {
			break
		}
		words = append(words, argument)
	}
	return words
}

// lookupExternalPlugin finds the plugin covering the most leading words,
// kubectl's rule: `ankra foo bar` prefers ankra-foo-bar over ankra-foo.
// Returns the executable's path and how many words its name consumed.
func lookupExternalPlugin(words []string) (string, int) {
	for consumed := len(words); consumed >= 1; consumed-- {
		name := externalPluginPrefix + strings.Join(words[:consumed], "-")
		if path := findPluginExecutable(name); path != "" {
			return path, consumed
		}
	}
	return "", 0
}

// homePluginsDir is ~/.ankra/plugins, so a plugin can be installed without
// touching PATH - the twin of ~/.ankra/modules for migrate modules.
func homePluginsDir() (string, bool) {
	home, homeError := os.UserHomeDir()
	if homeError != nil {
		return "", false
	}
	return filepath.Join(home, ".ankra", "plugins"), true
}

// findPluginExecutable resolves a plugin name to an executable:
// ~/.ankra/plugins first, then PATH.
func findPluginExecutable(name string) string {
	if directory, ok := homePluginsDir(); ok {
		candidate := filepath.Join(directory, name)
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	if path, lookError := exec.LookPath(name); lookError == nil {
		return path
	}
	return ""
}

func isExecutableFile(path string) bool {
	info, statError := os.Stat(path)
	if statError != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// discoveredPlugin is one plugin as `ankra plugins` reports it.
type discoveredPlugin struct {
	Name    string `json:"name" yaml:"name"`
	Command string `json:"command" yaml:"command"`
	Path    string `json:"path" yaml:"path"`
	// Shadowed names a built-in command with the same name, which always
	// wins: the plugin can never run.
	Shadowed bool `json:"shadowed,omitempty" yaml:"shadowed,omitempty"`
}

var pluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "List the external plugins that extend this CLI",
	Long: `List every executable the CLI would dispatch an unknown command to.

A plugin is an executable named ankra-<command> in ~/.ankra/plugins or on
PATH. Running 'ankra <command>' with a command the CLI does not know runs
the plugin with the remaining arguments, this terminal's streams, and
ANKRA_CLI_VERSION in its environment; its exit code is the CLI's. Built-in
commands always win, and multi-word names resolve longest first, so
'ankra foo bar' prefers ankra-foo-bar over ankra-foo.

Plugins run with your permissions; install only ones you trust. Executables
named ankra-module-<name> are migrate modules, a separate mechanism listed
by 'ankra migrate modules'.`,
	Example: `  ankra plugins
  ankra plugins -o json`,
	Args:        cobra.NoArgs,
	Annotations: map[string]string{annotationRequiresAuth: "false"},
	RunE:        runPlugins,
}

func init() {
	registerStructuredOutputFlags(pluginsCmd)
	rootCmd.AddCommand(pluginsCmd)
}

func runPlugins(cmd *cobra.Command, _ []string) error {
	plugins := discoverPlugins()
	if handled, err := renderStructured(cmd, plugins); handled || err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(plugins) == 0 {
		_, _ = fmt.Fprintln(out, "No plugins found. Put an executable named ankra-<command> in ~/.ankra/plugins or on PATH.")
		return nil
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"COMMAND", "PATH"})
	for _, plugin := range plugins {
		writer.AppendRow(table.Row{"ankra " + plugin.Command, plugin.Path})
	}
	writer.Render()
	for _, plugin := range plugins {
		if plugin.Shadowed {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s is shadowed by the built-in command %q and will never run\n", plugin.Path, plugin.Command)
		}
	}
	return nil
}

// discoverPlugins scans ~/.ankra/plugins then PATH, first hit per name
// winning - the same order dispatch resolves in.
func discoverPlugins() []discoveredPlugin {
	var directories []string
	if directory, ok := homePluginsDir(); ok {
		directories = append(directories, directory)
	}
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		if directory != "" {
			directories = append(directories, directory)
		}
	}
	seen := map[string]bool{}
	var plugins []discoveredPlugin
	for _, directory := range directories {
		entries, readError := os.ReadDir(directory)
		if readError != nil {
			continue
		}
		for _, entry := range entries {
			name, ok := externalPluginName(entry.Name())
			if !ok || seen[name] {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			if !isExecutableFile(path) {
				continue
			}
			seen[name] = true
			plugins = append(plugins, discoveredPlugin{
				Name:     entry.Name(),
				Command:  strings.ReplaceAll(name, "-", " "),
				Path:     path,
				Shadowed: isKnownRootCommand(strings.SplitN(name, "-", 2)[0]),
			})
		}
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Command < plugins[j].Command })
	return plugins
}

// externalPluginName is the command a file name stands for, or ok=false for
// files that are not plugins - migrate modules among them.
func externalPluginName(fileName string) (string, bool) {
	if runtime.GOOS == "windows" {
		for _, extension := range []string{".exe", ".bat", ".cmd"} {
			fileName = strings.TrimSuffix(fileName, extension)
		}
	}
	if !strings.HasPrefix(fileName, externalPluginPrefix) {
		return "", false
	}
	name := strings.TrimPrefix(fileName, externalPluginPrefix)
	if name == "" || strings.HasPrefix(name, "module-") {
		return "", false
	}
	return name, true
}

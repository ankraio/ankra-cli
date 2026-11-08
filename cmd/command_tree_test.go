package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// docs/command-tree.json is the CLI's command inventory: every visible,
// runnable command path, sorted. The surface parity check in ankraio/cluster
// (scripts/check_surface_parity.py, run nightly) reads it from master and
// fails when a command is not mapped to a capability there, so a new command
// is mapped in parity/capabilities.yaml once it lands. This test keeps the
// file equal to the built tree; regenerate it with
//
//	COMMAND_TREE_UPDATE=1 go test ./cmd -run TestCommandTreeIsCurrent

const (
	commandTreePath         = "../docs/command-tree.json"
	commandTreeUpdateEnv    = "COMMAND_TREE_UPDATE"
	commandTreeRegeneration = "COMMAND_TREE_UPDATE=1 go test ./cmd -run TestCommandTreeIsCurrent"
)

type commandTreeDocument struct {
	GeneratedBy string   `json:"generated_by"`
	Commands    []string `json:"commands"`
}

// runnableCommandPaths walks the tree under root and returns the full path
// of every command a user can run: visible, not deprecated, not cobra's help
// command, and with a Run or RunE. Group commands that only hold
// subcommands are not capabilities of their own.
func runnableCommandPaths(root *cobra.Command) []string {
	var paths []string
	var walk func(command *cobra.Command)
	walk = func(command *cobra.Command) {
		for _, child := range command.Commands() {
			if child.Hidden || child.Deprecated != "" || child.Name() == "help" {
				continue
			}
			if child.Runnable() {
				paths = append(paths, child.CommandPath())
			}
			walk(child)
		}
	}
	walk(root)
	sort.Strings(paths)
	return paths
}

func renderCommandTree(root *cobra.Command) ([]byte, error) {
	document := commandTreeDocument{
		GeneratedBy: commandTreeRegeneration + " (cmd/command_tree_test.go)",
		Commands:    runnableCommandPaths(root),
	}
	rendered, marshalError := json.MarshalIndent(document, "", "  ")
	if marshalError != nil {
		return nil, marshalError
	}
	return append(rendered, '\n'), nil
}

func TestCommandTreeIsCurrent(t *testing.T) {
	rendered, renderError := renderCommandTree(rootCmd)
	if renderError != nil {
		t.Fatalf("rendering the command tree: %v", renderError)
	}
	if os.Getenv(commandTreeUpdateEnv) == "1" {
		if writeError := os.WriteFile(filepath.FromSlash(commandTreePath), rendered, 0o644); writeError != nil {
			t.Fatalf("writing %s: %v", commandTreePath, writeError)
		}
		return
	}
	committed, readError := os.ReadFile(filepath.FromSlash(commandTreePath))
	if readError != nil {
		t.Fatalf("reading %s: %v (regenerate it: %s)", commandTreePath, readError, commandTreeRegeneration)
	}
	if bytes.Equal(committed, rendered) {
		return
	}
	var committedDocument commandTreeDocument
	if unmarshalError := json.Unmarshal(committed, &committedDocument); unmarshalError != nil {
		t.Fatalf("%s is not a command tree: %v (regenerate it: %s)", commandTreePath, unmarshalError, commandTreeRegeneration)
	}
	committedPaths := map[string]bool{}
	for _, path := range committedDocument.Commands {
		committedPaths[path] = true
	}
	builtPaths := runnableCommandPaths(rootCmd)
	builtPathSet := map[string]bool{}
	var missing []string
	for _, path := range builtPaths {
		builtPathSet[path] = true
		if !committedPaths[path] {
			missing = append(missing, path)
		}
	}
	var extra []string
	for _, path := range committedDocument.Commands {
		if !builtPathSet[path] {
			extra = append(extra, path)
		}
	}
	t.Fatalf("%s differs from the built command tree; regenerate it: %s\nnot in the file (%d):\n  %s\nin the file but not built (%d):\n  %s",
		commandTreePath, commandTreeRegeneration,
		len(missing), strings.Join(missing, "\n  "), len(extra), strings.Join(extra, "\n  "))
}

func TestRunnableCommandPathsSkipsHiddenDeprecatedHelpAndGroups(t *testing.T) {
	root := &cobra.Command{Use: "ankra"}
	group := &cobra.Command{Use: "alerts"}
	run := func(*cobra.Command, []string) {}
	group.AddCommand(
		&cobra.Command{Use: "list", Run: run},
		&cobra.Command{Use: "secret", Run: run, Hidden: true},
		&cobra.Command{Use: "old", Run: run, Deprecated: "use list"},
	)
	root.AddCommand(group, &cobra.Command{Use: "login", Run: run}, &cobra.Command{Use: "help", Run: run})

	paths := runnableCommandPaths(root)

	expected := []string{"ankra alerts list", "ankra login"}
	if strings.Join(paths, ",") != strings.Join(expected, ",") {
		t.Fatalf("runnable command paths = %v, want %v", paths, expected)
	}
}

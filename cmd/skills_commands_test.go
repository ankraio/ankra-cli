package cmd

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"ankra/internal/skills"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// A skill that documents a command the CLI does not have is worse than one
// that documents nothing: the agent runs it and the user gets an error with
// no idea the guidance was wrong. Two shipped skills accumulated thirteen of
// these - provider verbs that moved to the generic group, flags that were
// renamed, a --wait that never existed - and review did not catch any of
// them, because every line looked plausible. This test resolves every `ankra
// ...` invocation in the embedded skills against the real command tree, so
// the next rename fails here instead of in someone's terminal.

// commandNode is one command in the tree, with the flags it accepts.
type commandNode struct {
	children map[string]*commandNode
	flags    map[string]bool
	// runnable is true when the command does work itself, so a trailing word
	// may be a positional argument rather than a bad subcommand.
	runnable bool
}

func buildCommandTree(command *cobra.Command) *commandNode {
	node := &commandNode{
		children: make(map[string]*commandNode),
		flags:    make(map[string]bool),
		runnable: command.Runnable(),
	}
	// Cobra registers the help flag lazily, so a command that has never run
	// reports none. It is valid on every command.
	node.flags["help"] = true
	collect := func(flag *pflag.Flag) { node.flags[flag.Name] = true }
	command.LocalFlags().VisitAll(collect)
	command.InheritedFlags().VisitAll(collect)
	command.PersistentFlags().VisitAll(collect)
	for _, child := range command.Commands() {
		if child.Hidden || child.Name() == "help" {
			continue
		}
		childNode := buildCommandTree(child)
		node.children[child.Name()] = childNode
		for _, alias := range child.Aliases {
			node.children[alias] = childNode
		}
	}
	return node
}

// documentedCommand is one `ankra ...` line found in a skill.
type documentedCommand struct {
	file string
	line int
	text string
}

var (
	inlineCommandPattern = regexp.MustCompile("`(ankra [^`]+)`")
	continuationPattern  = regexp.MustCompile(`\\\n\s*`)
	flagPattern          = regexp.MustCompile(`(?:^|[\s=])--([a-z0-9][a-z0-9-]*)`)
	// placeholderPattern marks an invocation as illustrative rather than
	// literal: "..." elisions, "a|b" alternations, and shell/YAML fragments.
	placeholderPattern = regexp.MustCompile(`\.\.\.|\||\$\{|^\s*ankra\s*=`)
)

// collectDocumentedCommands reads every embedded skill and returns the ankra
// invocations in it, with backslash continuations joined.
func collectDocumentedCommands(t *testing.T) []documentedCommand {
	t.Helper()
	fsys, err := skills.EmbeddedFS()
	if err != nil {
		t.Fatalf("EmbeddedFS: %v", err)
	}
	var found []documentedCommand
	walkError := fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		data, readError := fs.ReadFile(fsys, path)
		if readError != nil {
			return readError
		}
		joined := continuationPattern.ReplaceAllString(string(data), " ")
		for number, line := range strings.Split(joined, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "ankra ") {
				found = append(found, documentedCommand{path, number + 1, stripComment(trimmed)})
				continue
			}
			for _, match := range inlineCommandPattern.FindAllStringSubmatch(trimmed, -1) {
				found = append(found, documentedCommand{path, number + 1, match[1]})
			}
		}
		return nil
	})
	if walkError != nil {
		t.Fatalf("walking the embedded skills: %v", walkError)
	}
	return found
}

func stripComment(line string) string {
	if index := strings.Index(line, " #"); index > 0 {
		return strings.TrimSpace(line[:index])
	}
	return line
}

// argumentPattern matches a token that is clearly a positional argument or a
// value rather than a subcommand name.
var argumentPattern = regexp.MustCompile(`^[<"'{/.$-]|^[A-Z]|[/.=]|^\d`)

// resolveChain walks the tokens as far as they name real subcommands and
// returns the node reached, the chain consumed, and the first token that was
// not a subcommand.
func resolveChain(root *commandNode, tokens []string) (*commandNode, []string, string) {
	node := root
	var chain []string
	for _, token := range tokens {
		if strings.HasPrefix(token, "-") {
			break
		}
		child, known := node.children[token]
		if !known {
			return node, chain, token
		}
		node = child
		chain = append(chain, token)
	}
	return node, chain, ""
}

func TestSkillsDocumentOnlyRealCommands(t *testing.T) {
	root := buildCommandTree(rootCmd)
	documented := collectDocumentedCommands(t)
	if len(documented) < 100 {
		t.Fatalf("only found %d documented commands; the extractor is broken", len(documented))
	}

	checked := 0
	for _, entry := range documented {
		if placeholderPattern.MatchString(entry.text) {
			continue
		}
		tokens := strings.Fields(entry.text)
		if len(tokens) < 2 || tokens[0] != "ankra" {
			continue
		}
		node, chain, unresolved := resolveChain(root, tokens[1:])
		if len(chain) == 0 {
			// A bare `ankra --version` or similar; nothing to verify.
			continue
		}
		checked++

		// An unresolved word under a command group that cannot run on its own
		// has to be a subcommand, and it is not one.
		if unresolved != "" && !node.runnable && !argumentPattern.MatchString(unresolved) {
			t.Errorf("%s:%d: %q is not a subcommand of `ankra %s`\n    %s",
				entry.file, entry.line, unresolved, strings.Join(chain, " "), entry.text)
			continue
		}

		for _, match := range flagPattern.FindAllStringSubmatch(entry.text, -1) {
			flag := match[1]
			if !node.flags[flag] {
				t.Errorf("%s:%d: `ankra %s` has no --%s flag\n    %s",
					entry.file, entry.line, strings.Join(chain, " "), flag, entry.text)
			}
		}
	}
	t.Logf("verified %d of %d documented ankra invocations against the command tree",
		checked, len(documented))
}

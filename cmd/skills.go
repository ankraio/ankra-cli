package cmd

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"ankra/internal/skills"

	"github.com/spf13/cobra"
)

// skillsCmd installs the curated Ankra Agent Skills (SKILL.md files) into a
// Cursor/Claude Code skills directory. The skills are embedded in the binary,
// so installation works offline and is versioned with the CLI release. This is
// distinct from `ankra openclaw skill`, which generates a per-cluster skill.
var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Install Ankra Agent Skills into your editor",
	Long: `Install the curated Ankra Agent Skills into a Cursor/Claude Code skills
directory. The skills teach your agent to follow Ankra's recommended practices
for the CLI, ImportCluster YAML, stacks/addons, GitOps, CI/CD, SOPS secrets,
Helm registries, observability, alerts, Terraform, and cloud clusters.

Because skills are only picked up when they match the conversation, install
also writes a small always-applied rule (Cursor rule / CLAUDE.md block) that
tells the agent Kubernetes here is managed by Ankra and to route changes
through the GitOps repo or the ankra CLI instead of raw kubectl/helm. Skip it
with --no-rules. Add --with-hooks to also install an agent hook that pauses
direct kubectl/helm cluster mutations for confirmation.

Skills are embedded in the CLI, so installation works offline.

  ankra skills list
  ankra skills install
  ankra skills install --editor claude-code
  ankra skills install --project .
  ankra skills install --with-hooks
  ankra skills install ankra-cli ankra-gitops`,
}

type skillsEditor struct {
	Name                   string
	ConfigurationDirectory string
	GuardFormat            string
}

type skillsTarget struct {
	Directory  string
	Scope      string
	EditorName string
	// Root anchors the editor's non-skill artefacts: the user's home
	// directory for personal scope, the project directory for project scope.
	Root   string
	Editor skillsEditor
}

type skillListEntry struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Installed   bool   `json:"installed" yaml:"installed"`
}

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the available Ankra skills",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		fsys, err := skillsSourceFS(cmd)
		if err != nil {
			return err
		}
		target, err := skillsTargetForCommand(cmd)
		if err != nil {
			return err
		}
		list, err := skills.List(fsys)
		if err != nil {
			return err
		}
		entries := make([]skillListEntry, 0, len(list))
		for _, s := range list {
			entries = append(entries, skillListEntry{
				Name:        s.Name,
				Description: s.Description,
				Installed:   dirExists(filepath.Join(target.Directory, s.Name)),
			})
		}
		if rendered, err := renderStructured(cmd, entries); rendered || err != nil {
			return err
		}
		for _, entry := range entries {
			marker := ""
			if entry.Installed {
				marker = " [installed]"
			}
			fmt.Printf("%s%s\n  %s\n", entry.Name, marker, entry.Description)
		}
		fmt.Printf("\n%d skills available. Install with: ankra skills install\n", len(entries))
		return nil
	},
}

var skillsInstallCmd = &cobra.Command{
	Use:   "install [skill ...]",
	Short: "Install Ankra skills into your skills directory",
	Long: `Install all skills (default) or only the named ones.

By default skills install into ~/.cursor/skills/ (personal, available in every
project). Use --editor claude-code to install into ~/.claude/skills/ instead.
Use --project <DIR> to install into the selected editor's project skills
directory (--project . for the current directory).

Install also writes an always-applied rule so the agent treats Ankra as the
default route for Kubernetes work (skip with --no-rules): a local Cursor
plugin rule or project .cursor/rules/ankra.mdc for Cursor, a managed CLAUDE.md
block for Claude Code. With --with-hooks it additionally installs an agent
hook ('ankra skills guard') that intercepts direct kubectl/helm cluster
mutations and asks for confirmation, pointing back at the Ankra workflow.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fsys, err := skillsSourceFS(cmd)
		if err != nil {
			return err
		}
		target, err := skillsTargetForCommand(cmd)
		if err != nil {
			return err
		}
		force, _ := cmd.Flags().GetBool("force")
		noRules, _ := cmd.Flags().GetBool("no-rules")
		withHooks, _ := cmd.Flags().GetBool("with-hooks")

		if err := os.MkdirAll(target.Directory, 0o755); err != nil {
			return fmt.Errorf("could not create %s: %w", target.Directory, err)
		}

		installed, skipped, err := skills.Install(fsys, target.Directory, args, force)
		if err != nil {
			return err
		}

		for _, name := range installed {
			fmt.Printf("installed %s\n", name)
		}
		for _, name := range skipped {
			fmt.Printf("skipped   %s (already exists; use --force to overwrite)\n", name)
		}

		if !noRules {
			rulePath, err := installAgentRule(target)
			if err != nil {
				return fmt.Errorf("could not install the agent rule: %w", err)
			}
			fmt.Printf("rule      %s (always applied; makes Ankra the default for Kubernetes work)\n", rulePath)
		}
		if withHooks {
			hookPath, err := installAgentHook(target)
			if err != nil {
				return fmt.Errorf("could not install the agent hook: %w", err)
			}
			fmt.Printf("hook      %s (kubectl/helm cluster mutations ask for confirmation)\n", hookPath)
		}

		fmt.Printf("\nInstalled %d skill(s) to %s (%s, skipped %d).\n",
			len(installed), target.Directory, target.Scope, len(skipped))
		fmt.Printf("Restart %s to load the skills.\n", target.EditorName)
		return nil
	},
}

var skillsUninstallCmd = &cobra.Command{
	Use:   "uninstall [skill ...]",
	Short: "Remove installed Ankra skills",
	Long: `Remove the named skills, or all Ankra skills when none are given.

A full uninstall (no skill names) also removes the always-applied agent rule
and the 'ankra skills guard' hook that install added for this editor and
scope; uninstalling named skills leaves them in place.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := skillsTargetForCommand(cmd)
		if err != nil {
			return err
		}
		names := args
		if len(names) == 0 {
			fsys, err := skillsSourceFS(cmd)
			if err != nil {
				return err
			}
			names, err = skills.Names(fsys)
			if err != nil {
				return err
			}
		}
		// Validate every name before removing anything, so one bad argument
		// (e.g. "." or "../x") cannot delete the skills directory or escape it.
		for _, name := range names {
			if err := validateSkillName(target.Directory, name); err != nil {
				return err
			}
		}
		removed := 0
		for _, name := range names {
			dest := filepath.Join(target.Directory, name)
			if dirExists(dest) {
				if err := os.RemoveAll(dest); err != nil {
					return fmt.Errorf("could not remove %s: %w", dest, err)
				}
				fmt.Printf("removed %s\n", name)
				removed++
			}
		}
		if len(args) == 0 {
			rulePath, found, err := removeAgentRule(target)
			if err != nil {
				return fmt.Errorf("could not remove the agent rule: %w", err)
			}
			if found {
				fmt.Printf("removed rule %s\n", rulePath)
			}
			hookPath, found, err := removeAgentHook(target)
			if err != nil {
				return fmt.Errorf("could not remove the agent hook: %w", err)
			}
			if found {
				fmt.Printf("removed hook %s\n", hookPath)
			}
		}
		fmt.Printf("\nRemoved %d skill(s) from %s (%s).\n", removed, target.Directory, target.Scope)
		return nil
	},
}

// skillsGuardCmd is the hook entrypoint that 'ankra skills install
// --with-hooks' wires into Cursor (beforeShellExecution) and Claude Code
// (PreToolUse). It reads one hook event from stdin and prints a decision:
// commands that mutate a Kubernetes cluster out-of-band (kubectl
// apply/delete/..., helm install/upgrade/...) come back as "ask" with a
// redirect to the Ankra workflow, everything else passes through. It fails
// open and always exits 0 so a broken event can never block the terminal.
var skillsGuardCmd = &cobra.Command{
	Use:   "guard",
	Short: "Agent-hook entrypoint that gates direct kubectl/helm mutations",
	Long: `Read an agent hook event (JSON) from stdin and print a permission decision.

Wired by 'ankra skills install --with-hooks' into Cursor's
beforeShellExecution hook and Claude Code's PreToolUse hook. Shell commands
that would mutate a Kubernetes cluster out-of-band (kubectl apply, helm
upgrade, ...) return an "ask" decision explaining the Ankra GitOps workflow;
read-only commands and anything unparseable pass through unchanged.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		format, _ := cmd.Flags().GetString("format")
		input, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			input = nil
		}
		response, err := skills.GuardRespond(format, input)
		if err != nil {
			return withExitCode(exitUsage, err)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(response))
		return err
	},
}

// installAgentRule writes the always-applied rule for the target editor and
// scope, returning the path of what was written. Cursor has no supported
// user-level rules directory, so personal scope installs a local plugin
// (~/.cursor/plugins/local/ankra) whose rules apply to every project.
func installAgentRule(target skillsTarget) (string, error) {
	switch target.Editor.GuardFormat {
	case "cursor":
		if target.Scope == "personal" {
			return skills.WriteCursorPlugin(cursorPluginDir(target), version)
		}
		return skills.WriteCursorRule(filepath.Join(target.Root, ".cursor", "rules"))
	default:
		path := claudeMemoryPath(target)
		return path, skills.UpsertClaudeRule(path)
	}
}

// removeAgentRule undoes installAgentRule, reporting whether anything was
// found to remove.
func removeAgentRule(target skillsTarget) (string, bool, error) {
	switch target.Editor.GuardFormat {
	case "cursor":
		if target.Scope == "personal" {
			dir := cursorPluginDir(target)
			found, err := skills.RemoveCursorPlugin(dir)
			return dir, found, err
		}
		rulesDir := filepath.Join(target.Root, ".cursor", "rules")
		found, err := skills.RemoveCursorRule(rulesDir)
		return filepath.Join(rulesDir, skills.CursorRuleFilename), found, err
	default:
		path := claudeMemoryPath(target)
		found, err := skills.RemoveClaudeRule(path)
		return path, found, err
	}
}

// installAgentHook wires the guard into the editor's hook config for the
// target scope, returning the config path it wrote.
func installAgentHook(target skillsTarget) (string, error) {
	path := hookConfigPath(target)
	guardCommand := skills.GuardCommandLine(currentExecutable(), target.Editor.GuardFormat)
	if target.Editor.GuardFormat == "cursor" {
		return path, skills.UpsertCursorHook(path, guardCommand)
	}
	return path, skills.UpsertClaudeHook(path, guardCommand)
}

// removeAgentHook undoes installAgentHook, reporting whether a guard entry
// was found.
func removeAgentHook(target skillsTarget) (string, bool, error) {
	path := hookConfigPath(target)
	if target.Editor.GuardFormat == "cursor" {
		found, err := skills.RemoveCursorHook(path)
		return path, found, err
	}
	found, err := skills.RemoveClaudeHook(path)
	return path, found, err
}

func cursorPluginDir(target skillsTarget) string {
	return filepath.Join(target.Root, ".cursor", "plugins", "local", skills.CursorPluginName)
}

// claudeMemoryPath returns the CLAUDE.md Claude Code actually loads for the
// scope: ~/.claude/CLAUDE.md for personal, <project>/CLAUDE.md for projects.
func claudeMemoryPath(target skillsTarget) string {
	if target.Scope == "personal" {
		return filepath.Join(target.Root, ".claude", "CLAUDE.md")
	}
	return filepath.Join(target.Root, "CLAUDE.md")
}

// hookConfigPath returns the hook config file for the target: Cursor's
// hooks.json or Claude Code's settings.json, at the matching scope.
func hookConfigPath(target skillsTarget) string {
	if target.Editor.GuardFormat == "cursor" {
		return filepath.Join(target.Root, ".cursor", "hooks.json")
	}
	return filepath.Join(target.Root, ".claude", "settings.json")
}

// currentExecutable resolves the running binary so hook configs keep working
// regardless of PATH; the install path is stable across 'ankra upgrade'.
func currentExecutable() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "ankra"
	}
	return exe
}

// validateSkillName rejects skill arguments that would resolve outside dir
// when joined onto it: empty, ".", "..", and anything containing a path
// separator. Belt-and-braces, it also verifies the joined path stays strictly
// inside dir (compare copySkill in internal/skills).
func validateSkillName(dir, name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return withExitCode(exitUsage, fmt.Errorf("invalid skill name %q: must be a bare directory name", name))
	}
	rel, err := filepath.Rel(dir, filepath.Join(dir, name))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return withExitCode(exitUsage, fmt.Errorf("invalid skill name %q: resolves outside %s", name, dir))
	}
	return nil
}

// skillsSourceFS returns the skills filesystem: a local directory when
// --source is given (offline, for testing/power users), otherwise the copy
// embedded in the binary.
func skillsSourceFS(cmd *cobra.Command) (fs.FS, error) {
	source, _ := cmd.Flags().GetString("source")
	if source != "" {
		return skills.SourceFS(source)
	}
	return skills.EmbeddedFS()
}

// skillsTargetDir resolves the install directory and a human-readable scope.
func skillsTargetDir(cmd *cobra.Command) (string, string, error) {
	target, err := skillsTargetForCommand(cmd)
	if err != nil {
		return "", "", err
	}
	return target.Directory, target.Scope, nil
}

func skillsTargetForCommand(cmd *cobra.Command) (skillsTarget, error) {
	editor, err := skillsEditorForCommand(cmd)
	if err != nil {
		return skillsTarget{}, err
	}
	projectFlag := cmd.Flags().Lookup("project")
	if projectFlag != nil && projectFlag.Changed {
		dir := projectFlag.Value.String()
		if dir == "" {
			dir = "."
		}
		return skillsTarget{
			Directory:  filepath.Join(dir, editor.ConfigurationDirectory, "skills"),
			Scope:      "project",
			EditorName: editor.Name,
			Root:       dir,
			Editor:     editor,
		}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return skillsTarget{}, fmt.Errorf("could not determine home directory: %w", err)
	}
	return skillsTarget{
		Directory:  filepath.Join(home, editor.ConfigurationDirectory, "skills"),
		Scope:      "personal",
		EditorName: editor.Name,
		Root:       home,
		Editor:     editor,
	}, nil
}

func skillsEditorForCommand(cmd *cobra.Command) (skillsEditor, error) {
	editor, _ := cmd.Flags().GetString("editor")
	switch strings.ToLower(strings.TrimSpace(editor)) {
	case "", "cursor":
		return skillsEditor{Name: "Cursor", ConfigurationDirectory: ".cursor", GuardFormat: "cursor"}, nil
	case "claude", "claude-code", "claudecode":
		return skillsEditor{Name: "Claude Code", ConfigurationDirectory: ".claude", GuardFormat: "claude"}, nil
	default:
		return skillsEditor{}, fmt.Errorf("unsupported editor %q (expected cursor or claude-code)", editor)
	}
}

func init() {
	for _, c := range []*cobra.Command{skillsListCmd, skillsInstallCmd, skillsUninstallCmd} {
		c.Flags().Bool("personal", false, "install into the selected editor's personal skills directory (default)")
		c.Flags().String("project", "", "install into <DIR>/<editor config>/skills (use \".\" for the current directory)")
		c.Flags().String("editor", "cursor", "skills app to target: cursor or claude-code")
		c.Flags().String("source", "", "read skills from a local directory instead of the embedded copy")
	}
	skillsInstallCmd.Flags().Bool("force", false, "overwrite existing skills without prompting")
	skillsInstallCmd.Flags().Bool("no-rules", false, "skip installing the always-applied agent rule")
	skillsInstallCmd.Flags().Bool("with-hooks", false, "also install the agent hook that gates direct kubectl/helm cluster mutations")
	skillsGuardCmd.Flags().String("format", "cursor", "hook event format: cursor or claude")
	registerStructuredOutputFlags(skillsListCmd)

	skillsCmd.AddCommand(skillsListCmd)
	skillsCmd.AddCommand(skillsInstallCmd)
	skillsCmd.AddCommand(skillsUninstallCmd)
	skillsCmd.AddCommand(skillsGuardCmd)

	setRequiresAuth(skillsCmd, false)
	rootCmd.AddCommand(skillsCmd)
}

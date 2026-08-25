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

// skillsCmd installs the curated Ankra Agent Skills (SKILL.md files) into
// every agent client on the machine. The skills are embedded in the binary,
// so installation works offline and is versioned with the CLI release. This
// is distinct from `ankra openclaw skill`, which generates a per-cluster
// skill.
var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Install Ankra Agent Skills into your AI coding assistants",
	Long: `Install the curated Ankra Agent Skills into the AI assistants you use.

The skills teach an agent to follow Ankra's practices for the CLI, ImportCluster
YAML, stacks and addons, applications and their CI/CD, stack profiles, GitOps,
SOPS secrets, Helm registries, observability, troubleshooting, security, and the
AI agent surface.

Claude Code, the Claude app, Cursor, Codex, GitHub Copilot, Windsurf, Gemini CLI,
OpenCode, Cline, Zed and OpenClaw are supported, plus any assistant that reads
AGENTS.md. With no --client, install picks the assistants configured on this
machine; 'ankra skills clients' shows what was detected.

Three things are installed per client:

  skills     the SKILL.md files themselves
  rule       an always-applied instruction making Ankra the default route for
             Kubernetes work, plus an index of the skills for clients that do
             not discover them on their own (skip with --no-rules)
  workflows  named multi-step entry points (/ankra-ship-service,
             /ankra-triage, ...) for clients with slash commands
             (skip with --no-workflows)

Add --with-hooks to also install an agent hook that pauses direct kubectl/helm
cluster mutations for confirmation.

  ankra skills clients
  ankra skills list
  ankra skills install
  ankra skills install --client all
  ankra skills install --client claude-code --client cursor
  ankra skills install --client copilot --project .
  ankra skills install --client claude-app
  ankra skills install --with-hooks
  ankra skills install ankra-cli ankra-gitops`,
}

type skillListEntry struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Installed   bool   `json:"installed" yaml:"installed"`
}

type clientListEntry struct {
	ID          string `json:"id" yaml:"id"`
	DisplayName string `json:"display_name" yaml:"display_name"`
	Detected    bool   `json:"detected" yaml:"detected"`
	Installed   bool   `json:"installed" yaml:"installed"`
	Scope       string `json:"scope" yaml:"scope"`
	Directory   string `json:"directory" yaml:"directory"`
	Delivery    string `json:"delivery" yaml:"delivery"`
}

var skillsClientsCmd = &cobra.Command{
	Use:   "clients",
	Short: "List the AI assistants skills can be installed into",
	Long: `List every supported assistant, whether it is configured here, and where
skills would be installed for it. Detection is what --client auto (the default)
selects; pass --project <DIR> to see the repository-scoped locations instead.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		scope, root, err := skillsScope(cmd)
		if err != nil {
			return err
		}
		detected := make(map[string]bool)
		for _, client := range skills.DetectClients(root, scope) {
			detected[client.ID] = true
		}
		entries := make([]clientListEntry, 0, len(skills.Clients()))
		for _, client := range skills.Clients() {
			target, targetError := skills.ResolveTarget(client, scope, root)
			if targetError != nil {
				continue
			}
			entries = append(entries, clientListEntry{
				ID:          client.ID,
				DisplayName: client.DisplayName,
				Detected:    detected[client.ID],
				Installed:   skillsDirectoryHasAnkraSkills(target),
				Scope:       scope,
				Directory:   skills.DisplayPath(target.SkillsDirectory),
				Delivery:    clientDelivery(client),
			})
		}
		if rendered, renderError := renderStructured(cmd, entries); rendered || renderError != nil {
			return renderError
		}
		for _, entry := range entries {
			marker := ""
			switch {
			case entry.Installed:
				marker = " [installed]"
			case entry.Detected:
				marker = " [detected]"
			}
			fmt.Printf("%s%s\n  %s — %s, installs to %s\n",
				entry.ID, marker, entry.DisplayName, entry.Delivery, entry.Directory)
		}
		fmt.Printf("\nInstall with: ankra skills install --client <id> (or --client all).\n")
		return nil
	},
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
		targets, err := skillsTargetsForCommand(cmd)
		if err != nil {
			return err
		}
		list, err := skills.List(fsys)
		if err != nil {
			return err
		}
		entries := make([]skillListEntry, 0, len(list))
		for _, skill := range list {
			entries = append(entries, skillListEntry{
				Name:        skill.Name,
				Description: skill.Description,
				Installed:   skillInstalledInAnyTarget(targets, skill.Name),
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
	Short: "Install Ankra skills into your AI assistants",
	Long: `Install all skills (default) or only the named ones.

Without --client, install targets every assistant configured on this machine
(see 'ankra skills clients'), falling back to Claude Code and Cursor when none
is detected. --client all installs everywhere; --client <id> (repeatable, or
comma-separated) picks specific ones.

By default skills install for your user, available in every project. Use
--project <DIR> to install into a repository instead (--project . for the
current directory) - the right scope for GitHub Copilot, whose instructions
file is per repository.

The Claude app cannot read your filesystem, so --client claude-app writes one
uploadable .zip bundle per skill and prints where to upload them.

Install also writes an always-applied rule so the agent treats Ankra as the
default route for Kubernetes work, and - for clients that do not discover
skills on their own - an index naming each skill and its path (skip both with
--no-rules). Clients with slash commands additionally get the Ankra workflow
commands (skip with --no-workflows). With --with-hooks it installs the agent
hook ('ankra skills guard') that intercepts direct kubectl/helm cluster
mutations and asks for confirmation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fsys, err := skillsSourceFS(cmd)
		if err != nil {
			return err
		}
		targets, err := skillsTargetsForCommand(cmd)
		if err != nil {
			return err
		}
		options := skillsInstallOptions{}
		options.force, _ = cmd.Flags().GetBool("force")
		noRules, _ := cmd.Flags().GetBool("no-rules")
		noWorkflows, _ := cmd.Flags().GetBool("no-workflows")
		options.rules = !noRules
		options.workflows = !noWorkflows
		options.hooks, _ = cmd.Flags().GetBool("with-hooks")

		for index, target := range targets {
			if index > 0 {
				fmt.Println()
			}
			if err := installForTarget(fsys, target, args, options); err != nil {
				return err
			}
		}
		fmt.Printf("\nRestart your assistant to load the skills. Ask it to \"use ankra-platform-principles\" to check.\n")
		return nil
	},
}

var skillsUninstallCmd = &cobra.Command{
	Use:   "uninstall [skill ...]",
	Short: "Remove installed Ankra skills",
	Long: `Remove the named skills, or all Ankra skills when none are given.

The client selection works as it does for install, except that --client auto
(the default) resolves to the assistants that actually have Ankra skills
installed, so a plain 'ankra skills uninstall' undoes a plain install.

A full uninstall (no skill names) also removes the always-applied rule, the
workflow commands, and the 'ankra skills guard' hook that install added for
each client and scope; uninstalling named skills leaves them in place.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := skillsUninstallTargets(cmd)
		if err != nil {
			return err
		}
		names := args
		if len(names) == 0 {
			fsys, sourceError := skillsSourceFS(cmd)
			if sourceError != nil {
				return sourceError
			}
			names, sourceError = skills.Names(fsys)
			if sourceError != nil {
				return sourceError
			}
		}
		for index, target := range targets {
			if index > 0 {
				fmt.Println()
			}
			if err := uninstallForTarget(target, names, len(args) == 0); err != nil {
				return err
			}
		}
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

type skillsInstallOptions struct {
	force     bool
	rules     bool
	workflows bool
	hooks     bool
}

// installForTarget installs the selected skills and the client's supporting
// artefacts (rule, skill index, workflow commands, guard hook) for one
// target, reporting each step.
func installForTarget(fsys fs.FS, target skills.Target, names []string, options skillsInstallOptions) error {
	fmt.Printf("%s (%s) → %s\n", target.Client.DisplayName, target.Scope, skills.DisplayPath(target.SkillsDirectory))

	if err := os.MkdirAll(target.SkillsDirectory, 0o755); err != nil {
		return fmt.Errorf("could not create %s: %w", target.SkillsDirectory, err)
	}

	installed, skipped, err := installSkillPayload(fsys, target, names, options.force)
	if err != nil {
		return err
	}
	for _, name := range installed {
		fmt.Printf("  skill     %s\n", name)
	}
	if len(skipped) > 0 {
		fmt.Printf("  skipped   %s (already present; use --force to overwrite)\n", strings.Join(skipped, ", "))
	}

	if options.rules {
		rulePath, ruleError := installAgentRule(target)
		if ruleError != nil {
			return fmt.Errorf("could not install the agent rule for %s: %w", target.Client.DisplayName, ruleError)
		}
		if rulePath != "" {
			fmt.Printf("  rule      %s\n", skills.DisplayPath(rulePath))
		}
	}

	if options.workflows {
		written, workflowError := skills.WriteWorkflows(target)
		if workflowError != nil {
			return fmt.Errorf("could not install the workflow commands for %s: %w", target.Client.DisplayName, workflowError)
		}
		if len(written) > 0 {
			fmt.Printf("  workflows %d in %s\n", len(written), skills.DisplayPath(target.CommandsDirectory))
		}
	}

	if options.hooks {
		if !target.SupportsHooks() {
			fmt.Printf("  hook      not supported by %s; skipped\n", target.Client.DisplayName)
		} else {
			hookPath, hookError := installAgentHook(target)
			if hookError != nil {
				return fmt.Errorf("could not install the agent hook for %s: %w", target.Client.DisplayName, hookError)
			}
			fmt.Printf("  hook      %s (kubectl/helm cluster mutations ask for confirmation)\n", skills.DisplayPath(hookPath))
		}
	}

	if target.Client.Note != "" {
		fmt.Printf("  note      %s\n", target.Client.Note)
	}
	return nil
}

// installSkillPayload copies the skills into the target, as directories for
// clients that read the filesystem and as uploadable bundles for those that
// cannot.
func installSkillPayload(fsys fs.FS, target skills.Target, names []string, force bool) (installed, skipped []string, err error) {
	if target.Client.Packaged {
		return skills.PackageBundles(fsys, target.SkillsDirectory, names, force)
	}
	return skills.Install(fsys, target.SkillsDirectory, names, force)
}

// uninstallForTarget removes the named skills from one target and, on a full
// uninstall, the rule, workflow commands and hook install wrote for it.
func uninstallForTarget(target skills.Target, names []string, full bool) error {
	fmt.Printf("%s (%s) → %s\n", target.Client.DisplayName, target.Scope, skills.DisplayPath(target.SkillsDirectory))

	for _, name := range names {
		if err := validateSkillName(target.SkillsDirectory, name); err != nil {
			return err
		}
	}
	removed := 0
	if target.Client.Packaged {
		count, err := skills.RemoveBundles(target.SkillsDirectory, names)
		if err != nil {
			return err
		}
		removed = count
	} else {
		for _, name := range names {
			destination := filepath.Join(target.SkillsDirectory, name)
			if !dirExists(destination) {
				continue
			}
			if err := os.RemoveAll(destination); err != nil {
				return fmt.Errorf("could not remove %s: %w", destination, err)
			}
			removed++
		}
	}
	fmt.Printf("  removed   %d skill(s)\n", removed)

	if !full {
		return nil
	}
	rulePath, found, err := removeAgentRule(target)
	if err != nil {
		return fmt.Errorf("could not remove the agent rule: %w", err)
	}
	if found {
		fmt.Printf("  rule      removed %s\n", skills.DisplayPath(rulePath))
	}
	workflowCount, err := skills.RemoveWorkflows(target)
	if err != nil {
		return fmt.Errorf("could not remove the workflow commands: %w", err)
	}
	if workflowCount > 0 {
		fmt.Printf("  workflows removed %d from %s\n", workflowCount, skills.DisplayPath(target.CommandsDirectory))
	}
	hookPath, found, err := removeAgentHook(target)
	if err != nil {
		return fmt.Errorf("could not remove the agent hook: %w", err)
	}
	if found {
		fmt.Printf("  hook      removed from %s\n", skills.DisplayPath(hookPath))
	}
	return nil
}

// installAgentRule writes the always-applied rule for a target, returning the
// path written (empty when the client takes no rule). Cursor has no supported
// user-level rules directory, so personal scope installs a local plugin
// (~/.cursor/plugins/local/ankra) whose rules apply to every project. Every
// other client takes a managed block in its instructions file, which also
// carries the index of installed skills.
func installAgentRule(target skills.Target) (string, error) {
	switch target.Client.RuleFormat {
	case "cursor":
		if target.Scope == skills.ScopePersonal {
			return skills.WriteCursorPlugin(cursorPluginDir(target), version)
		}
		return skills.WriteCursorRule(filepath.Join(target.Root, ".cursor", "rules"))
	case "block":
		if target.InstructionsPath == "" {
			return "", nil
		}
		block := skills.InstructionsBlock(target, installedSkillsIn(target))
		return target.InstructionsPath, skills.UpsertManagedBlock(target.InstructionsPath, block)
	default:
		return "", nil
	}
}

// removeAgentRule undoes installAgentRule, reporting whether anything was
// found to remove.
func removeAgentRule(target skills.Target) (string, bool, error) {
	switch target.Client.RuleFormat {
	case "cursor":
		if target.Scope == skills.ScopePersonal {
			dir := cursorPluginDir(target)
			found, err := skills.RemoveCursorPlugin(dir)
			return dir, found, err
		}
		rulesDir := filepath.Join(target.Root, ".cursor", "rules")
		found, err := skills.RemoveCursorRule(rulesDir)
		return filepath.Join(rulesDir, skills.CursorRuleFilename), found, err
	case "block":
		if target.InstructionsPath == "" {
			return "", false, nil
		}
		found, err := skills.RemoveManagedBlock(target.InstructionsPath)
		return target.InstructionsPath, found, err
	default:
		return "", false, nil
	}
}

// installAgentHook wires the guard into the client's hook config for the
// target scope, returning the config path it wrote.
func installAgentHook(target skills.Target) (string, error) {
	guardCommand := skills.GuardCommandLine(currentExecutable(), target.Client.HookFormat)
	if target.Client.HookFormat == "cursor" {
		return target.HooksPath, skills.UpsertCursorHook(target.HooksPath, guardCommand)
	}
	return target.HooksPath, skills.UpsertClaudeHook(target.HooksPath, guardCommand)
}

// removeAgentHook undoes installAgentHook, reporting whether a guard entry
// was found.
func removeAgentHook(target skills.Target) (string, bool, error) {
	if !target.SupportsHooks() {
		return "", false, nil
	}
	if target.Client.HookFormat == "cursor" {
		found, err := skills.RemoveCursorHook(target.HooksPath)
		return target.HooksPath, found, err
	}
	found, err := skills.RemoveClaudeHook(target.HooksPath)
	return target.HooksPath, found, err
}

// currentExecutable resolves the running binary so hook configs keep working
// regardless of PATH; the install path is stable across 'ankra upgrade'.
func currentExecutable() string {
	executable, err := os.Executable()
	if err != nil || executable == "" {
		return "ankra"
	}
	return executable
}

func cursorPluginDir(target skills.Target) string {
	return filepath.Join(target.Root, ".cursor", "plugins", "local", skills.CursorPluginName)
}

// installedSkillsIn reads back what is actually present in a target's skills
// directory, so the managed index names the skills the agent can really open
// rather than everything the binary carries.
func installedSkillsIn(target skills.Target) []skills.Skill {
	if target.Client.Packaged {
		return nil
	}
	list, err := skills.List(os.DirFS(target.SkillsDirectory))
	if err != nil {
		return nil
	}
	return list
}

// skillsDirectoryHasAnkraSkills reports whether a target already carries an
// Ankra install, for the [installed] marker in 'skills clients'.
func skillsDirectoryHasAnkraSkills(target skills.Target) bool {
	if target.Client.Packaged {
		entries, err := os.ReadDir(target.SkillsDirectory)
		if err != nil {
			return false
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "ankra-") && strings.HasSuffix(entry.Name(), ".zip") {
				return true
			}
		}
		return false
	}
	return len(installedSkillsIn(target)) > 0
}

func skillInstalledInAnyTarget(targets []skills.Target, name string) bool {
	for _, target := range targets {
		if target.Client.Packaged {
			if _, err := os.Stat(filepath.Join(target.SkillsDirectory, name+".zip")); err == nil {
				return true
			}
			continue
		}
		if dirExists(filepath.Join(target.SkillsDirectory, name)) {
			return true
		}
	}
	return false
}

func clientDelivery(client skills.Client) string {
	switch {
	case client.Packaged:
		return "uploadable bundles"
	case client.LoadsSkillsNatively:
		return "native skills directory"
	default:
		return "skills directory + indexed instructions"
	}
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

// skillsScope resolves the install scope and its root directory: the project
// directory when --project is given, the home directory otherwise.
func skillsScope(cmd *cobra.Command) (scope, root string, err error) {
	projectFlag := cmd.Flags().Lookup("project")
	if projectFlag != nil && projectFlag.Changed {
		directory := projectFlag.Value.String()
		if directory == "" {
			directory = "."
		}
		return skills.ScopeProject, directory, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("could not determine home directory: %w", err)
	}
	return skills.ScopePersonal, home, nil
}

// skillsTargetsForCommand resolves --client (and the deprecated --editor)
// into the install targets for the current scope.
func skillsTargetsForCommand(cmd *cobra.Command) ([]skills.Target, error) {
	scope, root, err := skillsScope(cmd)
	if err != nil {
		return nil, err
	}
	selected, err := skills.ResolveClients(skillsRequestedClients(cmd), root, scope)
	if err != nil {
		return nil, withExitCode(exitUsage, err)
	}
	targets := make([]skills.Target, 0, len(selected))
	for _, client := range selected {
		target, targetError := skills.ResolveTarget(client, scope, root)
		if targetError != nil {
			return nil, targetError
		}
		targets = append(targets, target)
	}
	return targets, nil
}

// skillsUninstallTargets resolves the uninstall targets. With no explicit
// --client, it narrows the detected set to the clients that actually carry an
// Ankra install, so a plain uninstall undoes a plain install and does not
// report on assistants that never had the skills.
func skillsUninstallTargets(cmd *cobra.Command) ([]skills.Target, error) {
	targets, err := skillsTargetsForCommand(cmd)
	if err != nil {
		return nil, err
	}
	if len(skillsRequestedClients(cmd)) > 0 {
		return targets, nil
	}
	withInstall := make([]skills.Target, 0, len(targets))
	for _, target := range targets {
		if skillsDirectoryHasAnkraSkills(target) {
			withInstall = append(withInstall, target)
		}
	}
	if len(withInstall) == 0 {
		return targets, nil
	}
	return withInstall, nil
}

// skillsRequestedClients merges --client with the deprecated --editor alias.
func skillsRequestedClients(cmd *cobra.Command) []string {
	requested, _ := cmd.Flags().GetStringArray("client")
	editorFlag := cmd.Flags().Lookup("editor")
	if editorFlag != nil && editorFlag.Changed {
		requested = append(requested, editorFlag.Value.String())
	}
	return requested
}

func init() {
	for _, command := range []*cobra.Command{skillsListCmd, skillsInstallCmd, skillsUninstallCmd, skillsClientsCmd} {
		command.Flags().String("project", "", "install into <DIR> instead of your home directory (use \".\" for the current directory)")
		command.Flags().StringArray("client", nil,
			"assistant to target, repeatable or comma-separated: "+strings.Join(skills.ClientIDs(), ", ")+
				", plus \"all\" and \"auto\" (default: the assistants configured here)")
		command.Flags().String("editor", "", "deprecated alias for --client")
		_ = command.Flags().MarkDeprecated("editor", "use --client instead")
		command.Flags().Bool("personal", false, "install for your user (default)")
		_ = command.Flags().MarkDeprecated("personal", "personal scope is the default; use --project for a repository install")
	}
	for _, command := range []*cobra.Command{skillsListCmd, skillsInstallCmd, skillsUninstallCmd} {
		command.Flags().String("source", "", "read skills from a local directory instead of the embedded copy")
	}
	skillsInstallCmd.Flags().Bool("force", false, "overwrite existing skills without prompting")
	skillsInstallCmd.Flags().Bool("no-rules", false, "skip the always-applied agent rule and skill index")
	skillsInstallCmd.Flags().Bool("no-workflows", false, "skip the Ankra workflow commands")
	skillsInstallCmd.Flags().Bool("with-hooks", false, "also install the agent hook that gates direct kubectl/helm cluster mutations")
	skillsGuardCmd.Flags().String("format", "cursor", "hook event format: cursor or claude")
	registerStructuredOutputFlags(skillsListCmd, skillsClientsCmd)

	skillsCmd.AddCommand(skillsClientsCmd)
	skillsCmd.AddCommand(skillsListCmd)
	skillsCmd.AddCommand(skillsInstallCmd)
	skillsCmd.AddCommand(skillsUninstallCmd)
	skillsCmd.AddCommand(skillsGuardCmd)

	setRequiresAuth(skillsCmd, false)
	rootCmd.AddCommand(skillsCmd)
}

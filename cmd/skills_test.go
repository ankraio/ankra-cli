package cmd

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/skills"

	"github.com/spf13/cobra"
)

func newSkillsTestCmd() *cobra.Command {
	c := &cobra.Command{Use: "x", Run: func(*cobra.Command, []string) {}}
	c.Flags().Bool("personal", false, "")
	c.Flags().String("project", "", "")
	c.Flags().StringArray("client", nil, "")
	c.Flags().String("editor", "", "")
	c.Flags().String("source", "", "")
	return c
}

// newSkillsInstallTestCmd mirrors the full skills install flag set.
func newSkillsInstallTestCmd() *cobra.Command {
	c := newSkillsTestCmd()
	c.Flags().Bool("force", false, "")
	c.Flags().Bool("no-rules", false, "")
	c.Flags().Bool("no-workflows", false, "")
	c.Flags().Bool("with-hooks", false, "")
	return c
}

// skillsTestCmdFor builds a command already scoped to a project and client.
func skillsTestCmdFor(t *testing.T, base *cobra.Command, project, client string) *cobra.Command {
	t.Helper()
	if project != "" {
		if err := base.Flags().Set("project", project); err != nil {
			t.Fatal(err)
		}
	}
	if client != "" {
		if err := base.Flags().Set("client", client); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func targetFor(t *testing.T, project, client string) skills.Target {
	t.Helper()
	targets, err := skillsTargetsForCommand(skillsTestCmdFor(t, newSkillsTestCmd(), project, client))
	if err != nil {
		t.Fatalf("skillsTargetsForCommand(%s): %v", client, err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected exactly one target for %s, got %d", client, len(targets))
	}
	return targets[0]
}

func TestSkillsTargetPersonalDefaultsToDetectedClients(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	targets, err := skillsTargetsForCommand(newSkillsTestCmd())
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Client.ID != "claude-code" {
		t.Fatalf("expected the detected claude-code target, got %+v", targets)
	}
	if targets[0].Scope != skills.ScopePersonal {
		t.Fatalf("expected personal scope, got %q", targets[0].Scope)
	}
	want := filepath.Join(home, ".claude", "skills")
	if targets[0].SkillsDirectory != want {
		t.Fatalf("got %s want %s", targets[0].SkillsDirectory, want)
	}
}

func TestSkillsTargetPersonalFallsBackWhenNothingDetected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	targets, err := skillsTargetsForCommand(newSkillsTestCmd())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(targets))
	for _, target := range targets {
		got = append(got, target.Client.ID)
	}
	if strings.Join(got, ",") != "claude-code,cursor" {
		t.Fatalf("expected the claude-code+cursor fallback, got %v", got)
	}
}

func TestSkillsTargetProjectPaths(t *testing.T) {
	project := t.TempDir()
	for _, testCase := range []struct {
		client string
		want   string
	}{
		{"cursor", filepath.Join(project, ".cursor", "skills")},
		{"claude-code", filepath.Join(project, ".claude", "skills")},
		{"claude", filepath.Join(project, ".claude", "skills")},
		{"codex", filepath.Join(project, ".ankra", "skills")},
		{"copilot", filepath.Join(project, ".ankra", "skills")},
		{"gemini", filepath.Join(project, ".ankra", "skills")},
	} {
		t.Run(testCase.client, func(t *testing.T) {
			target := targetFor(t, project, testCase.client)
			if target.Scope != skills.ScopeProject {
				t.Fatalf("expected project scope, got %q", target.Scope)
			}
			if target.SkillsDirectory != testCase.want {
				t.Fatalf("got %s want %s", target.SkillsDirectory, testCase.want)
			}
		})
	}
}

func TestSkillsTargetRejectsUnknownClient(t *testing.T) {
	c := skillsTestCmdFor(t, newSkillsTestCmd(), t.TempDir(), "emacs-doctor")
	_, err := skillsTargetsForCommand(c)
	if err == nil {
		t.Fatal("expected unsupported client error")
	}
	if code := exitCodeFor(err); code != exitUsage {
		t.Fatalf("exit code %d, want %d (err: %v)", code, exitUsage, err)
	}
}

func TestSkillsTargetAllSelectsEveryClient(t *testing.T) {
	targets, err := skillsTargetsForCommand(skillsTestCmdFor(t, newSkillsTestCmd(), t.TempDir(), "all"))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != len(skills.Clients()) {
		t.Fatalf("--client all resolved %d of %d clients", len(targets), len(skills.Clients()))
	}
}

// The deprecated --editor flag keeps working as an alias for --client.
func TestSkillsEditorFlagStillSelectsAClient(t *testing.T) {
	project := t.TempDir()
	c := newSkillsTestCmd()
	if err := c.Flags().Set("project", project); err != nil {
		t.Fatal(err)
	}
	if err := c.Flags().Set("editor", "claude-code"); err != nil {
		t.Fatal(err)
	}
	targets, err := skillsTargetsForCommand(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Client.ID != "claude-code" {
		t.Fatalf("--editor claude-code resolved to %+v", targets)
	}
}

func TestSkillsInstallCommandHasClientFlag(t *testing.T) {
	if skillsInstallCmd.Flags().Lookup("client") == nil {
		t.Fatal("expected client flag on skills install command")
	}
	if skillsInstallCmd.Flags().Lookup("editor") == nil {
		t.Fatal("the deprecated editor alias should still be accepted")
	}
}

func TestSkillsSourceFSDefaultsToEmbedded(t *testing.T) {
	fsys, err := skillsSourceFS(newSkillsTestCmd())
	if err != nil {
		t.Fatalf("skillsSourceFS: %v", err)
	}
	if fsys == nil {
		t.Fatal("expected embedded filesystem, got nil")
	}
}

func TestSkillsCommandIsAuthFree(t *testing.T) {
	if commandRequiresAuth(skillsListCmd) {
		t.Error("ankra skills should not require auth")
	}
	if commandRequiresAuth(skillsClientsCmd) {
		t.Error("ankra skills clients should not require auth")
	}
}

// seedInstalledSkill creates <project>/.cursor/skills/<name>/SKILL.md and
// returns the skill directory.
func seedInstalledSkill(t *testing.T, project, name string) string {
	t.Helper()
	dir := filepath.Join(project, ".cursor", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runSkillsUninstall(t *testing.T, project string, args []string) error {
	t.Helper()
	return runSkillsUninstallForClient(t, project, "cursor", args)
}

func runSkillsUninstallForClient(t *testing.T, project, client string, args []string) error {
	t.Helper()
	c := skillsTestCmdFor(t, newSkillsTestCmd(), project, client)
	var err error
	captureStdout(t, func() {
		err = skillsUninstallCmd.RunE(c, args)
	})
	return err
}

func TestSkillsUninstallRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "../x", "ankra-cli/.."} {
		t.Run(name, func(t *testing.T) {
			project := t.TempDir()
			installed := seedInstalledSkill(t, project, "ankra-cli")
			// Decoy that "../x" would resolve to; it must survive.
			escapeTarget := filepath.Join(project, ".cursor", "x")
			if err := os.MkdirAll(escapeTarget, 0o755); err != nil {
				t.Fatal(err)
			}

			err := runSkillsUninstall(t, project, []string{name})
			if err == nil {
				t.Fatalf("uninstall %q: expected error, got nil", name)
			}
			if code := exitCodeFor(err); code != exitUsage {
				t.Fatalf("uninstall %q: exit code %d, want %d (err: %v)", name, code, exitUsage, err)
			}
			for _, path := range []string{installed, filepath.Join(project, ".cursor", "skills"), escapeTarget} {
				if !dirExists(path) {
					t.Errorf("uninstall %q removed %s", name, path)
				}
			}
		})
	}
}

func TestSkillsUninstallValidatesAllNamesBeforeRemoving(t *testing.T) {
	project := t.TempDir()
	installed := seedInstalledSkill(t, project, "ankra-cli")

	err := runSkillsUninstall(t, project, []string{"ankra-cli", ".."})
	if err == nil {
		t.Fatal("expected error for invalid second argument")
	}
	if code := exitCodeFor(err); code != exitUsage {
		t.Fatalf("exit code %d, want %d (err: %v)", code, exitUsage, err)
	}
	if !dirExists(installed) {
		t.Error("valid skill was removed despite invalid sibling argument")
	}
}

func TestSkillsUninstallRemovesNamedSkill(t *testing.T) {
	project := t.TempDir()
	removedSkill := seedInstalledSkill(t, project, "ankra-cli")
	keptSkill := seedInstalledSkill(t, project, "ankra-gitops")

	// A valid but not-installed name is silently skipped.
	if err := runSkillsUninstall(t, project, []string{"ankra-cli", "not-installed"}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if dirExists(removedSkill) {
		t.Error("ankra-cli was not removed")
	}
	if !dirExists(keptSkill) {
		t.Error("ankra-gitops was removed but not requested")
	}
}

func runSkillsInstall(t *testing.T, project, client string, withHooks bool, args []string) error {
	t.Helper()
	c := skillsTestCmdFor(t, newSkillsInstallTestCmd(), project, client)
	if withHooks {
		if err := c.Flags().Set("with-hooks", "true"); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	captureStdout(t, func() {
		err = skillsInstallCmd.RunE(c, args)
	})
	return err
}

// The full install/uninstall cycle for a Cursor project: skills, the
// always-applied rule, the workflow commands, and the guard hook all appear
// and disappear together.
func TestSkillsInstallCursorProjectWritesRuleWorkflowsAndHook(t *testing.T) {
	project := t.TempDir()
	if err := runSkillsInstall(t, project, "cursor", true, nil); err != nil {
		t.Fatalf("install: %v", err)
	}

	if !dirExists(filepath.Join(project, ".cursor", "skills", "ankra-cli")) {
		t.Error("skills not installed")
	}
	rulePath := filepath.Join(project, ".cursor", "rules", "ankra.mdc")
	rule, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("rule not written: %v", err)
	}
	if !strings.Contains(string(rule), "alwaysApply: true") {
		t.Error("rule is not always-applied")
	}
	workflowPath := filepath.Join(project, ".cursor", "commands", "ankra-ship-service.md")
	if _, err := os.Stat(workflowPath); err != nil {
		t.Errorf("workflow command not written: %v", err)
	}
	hooksPath := filepath.Join(project, ".cursor", "hooks.json")
	hooksData, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("hooks.json not written: %v", err)
	}
	if !strings.Contains(string(hooksData), "skills guard --format cursor") {
		t.Errorf("guard not wired: %s", hooksData)
	}

	if err := runSkillsUninstallForClient(t, project, "cursor", nil); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if dirExists(filepath.Join(project, ".cursor", "skills", "ankra-cli")) {
		t.Error("skills not removed")
	}
	if _, err := os.Stat(rulePath); !os.IsNotExist(err) {
		t.Error("rule not removed")
	}
	if _, err := os.Stat(workflowPath); !os.IsNotExist(err) {
		t.Error("workflow command not removed")
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Error("hooks.json not removed")
	}
}

// Same cycle for a Claude Code project: CLAUDE.md gains and loses the managed
// block without touching user content, settings.json gains and loses the
// PreToolUse guard.
func TestSkillsInstallClaudeProjectWritesRuleAndHook(t *testing.T) {
	project := t.TempDir()
	claudeMd := filepath.Join(project, "CLAUDE.md")
	if err := os.WriteFile(claudeMd, []byte("# Existing notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runSkillsInstall(t, project, "claude-code", true, nil); err != nil {
		t.Fatalf("install: %v", err)
	}

	memory, err := os.ReadFile(claudeMd)
	if err != nil {
		t.Fatalf("CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(memory), "# Existing notes") {
		t.Error("existing CLAUDE.md content lost")
	}
	if !strings.Contains(string(memory), "ANKRA MANAGED BLOCK") {
		t.Error("managed block missing from CLAUDE.md")
	}

	settingsPath := filepath.Join(project, ".claude", "settings.json")
	settingsData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatalf("settings.json invalid: %v", err)
	}
	if !strings.Contains(string(settingsData), "skills guard --format claude") {
		t.Errorf("guard not wired: %s", settingsData)
	}

	if err := runSkillsUninstallForClient(t, project, "claude-code", nil); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	memory, err = os.ReadFile(claudeMd)
	if err != nil {
		t.Fatalf("CLAUDE.md should survive with user content: %v", err)
	}
	if strings.Contains(string(memory), "ANKRA MANAGED BLOCK") {
		t.Error("managed block not removed")
	}
	if !strings.Contains(string(memory), "# Existing notes") {
		t.Error("user content lost on uninstall")
	}
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Error("settings.json holding only the guard should be removed")
	}
}

// Clients that do not discover skills on their own get an index naming each
// installed skill, so the agent knows the files exist and where to open them.
func TestSkillsInstallIndexedClientWritesSkillIndex(t *testing.T) {
	project := t.TempDir()
	if err := runSkillsInstall(t, project, "codex", false, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !dirExists(filepath.Join(project, ".ankra", "skills", "ankra-cli")) {
		t.Fatal("skills not installed into the shared library")
	}
	agents, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md not written: %v", err)
	}
	for _, want := range []string{"ANKRA MANAGED BLOCK", "Ankra skills installed here", ".ankra/skills", "`ankra-cli`"} {
		if !strings.Contains(string(agents), want) {
			t.Errorf("AGENTS.md missing %q:\n%s", want, agents)
		}
	}

	if err := runSkillsUninstallForClient(t, project, "codex", nil); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("an AGENTS.md holding only the managed block should be removed")
	}
}

// Copilot's project instructions file is what GitHub actually reads, and its
// prompt files carry the .prompt.md suffix.
func TestSkillsInstallCopilotWritesInstructionsAndPromptFiles(t *testing.T) {
	project := t.TempDir()
	if err := runSkillsInstall(t, project, "copilot", false, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	instructions, err := os.ReadFile(filepath.Join(project, ".github", "copilot-instructions.md"))
	if err != nil {
		t.Fatalf("copilot-instructions.md not written: %v", err)
	}
	if !strings.Contains(string(instructions), "Ankra skills installed here") {
		t.Error("copilot instructions missing the skill index")
	}
	prompt, err := os.ReadFile(filepath.Join(project, ".github", "prompts", "ankra-triage.prompt.md"))
	if err != nil {
		t.Fatalf("prompt file not written: %v", err)
	}
	if !strings.HasPrefix(string(prompt), "---\nmode: agent\n") {
		t.Errorf("copilot prompt missing its mode header:\n%s", prompt)
	}
}

// The Claude app cannot read the filesystem, so its install is a directory of
// uploadable bundles rather than loose skills.
func TestSkillsInstallClaudeAppWritesBundles(t *testing.T) {
	project := t.TempDir()
	if err := runSkillsInstall(t, project, "claude-app", false, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	bundle := filepath.Join(project, ".ankra", "claude-app", "ankra-cli.zip")
	archive, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatalf("bundle not written: %v", err)
	}
	defer func() { _ = archive.Close() }()
	found := false
	for _, file := range archive.File {
		if file.Name == "ankra-cli/SKILL.md" {
			found = true
		}
	}
	if !found {
		t.Error("bundle does not contain ankra-cli/SKILL.md at its root")
	}

	if err := runSkillsUninstallForClient(t, project, "claude-app", nil); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(bundle); !os.IsNotExist(err) {
		t.Error("bundle not removed")
	}
}

// Named uninstalls must leave the rule, workflows and hook alone: only a full
// uninstall tears down the steering.
func TestSkillsNamedUninstallKeepsRuleAndHook(t *testing.T) {
	project := t.TempDir()
	if err := runSkillsInstall(t, project, "cursor", true, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := runSkillsUninstallForClient(t, project, "cursor", []string{"ankra-cli"}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !dirExists(filepath.Join(project, ".cursor", "skills", "ankra-gitops")) {
		t.Error("unrelated skill removed")
	}
	if _, err := os.Stat(filepath.Join(project, ".cursor", "rules", "ankra.mdc")); err != nil {
		t.Error("rule should survive a named uninstall")
	}
	if _, err := os.Stat(filepath.Join(project, ".cursor", "commands", "ankra-triage.md")); err != nil {
		t.Error("workflows should survive a named uninstall")
	}
	if _, err := os.Stat(filepath.Join(project, ".cursor", "hooks.json")); err != nil {
		t.Error("hook should survive a named uninstall")
	}
}

func TestSkillsInstallNoRulesAndNoWorkflowsSkipThem(t *testing.T) {
	project := t.TempDir()
	c := skillsTestCmdFor(t, newSkillsInstallTestCmd(), project, "cursor")
	if err := c.Flags().Set("no-rules", "true"); err != nil {
		t.Fatal(err)
	}
	if err := c.Flags().Set("no-workflows", "true"); err != nil {
		t.Fatal(err)
	}
	var err error
	captureStdout(t, func() {
		err = skillsInstallCmd.RunE(c, nil)
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".cursor", "rules", "ankra.mdc")); !os.IsNotExist(err) {
		t.Error("--no-rules must not write the rule")
	}
	if _, err := os.Stat(filepath.Join(project, ".cursor", "commands")); !os.IsNotExist(err) {
		t.Error("--no-workflows must not write the workflow commands")
	}
	if _, err := os.Stat(filepath.Join(project, ".cursor", "hooks.json")); !os.IsNotExist(err) {
		t.Error("hooks are opt-in and must not be written by default")
	}
}

// --with-hooks against a client with no hook mechanism is a skip, not an
// error, so '--client all --with-hooks' installs everywhere it can.
func TestSkillsInstallWithHooksSkipsClientsWithoutHooks(t *testing.T) {
	project := t.TempDir()
	if err := runSkillsInstall(t, project, "codex", true, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !dirExists(filepath.Join(project, ".ankra", "skills", "ankra-cli")) {
		t.Error("skills should still install when the client takes no hook")
	}
}

// Personal scope routes the Cursor rule into a local plugin (there is no
// supported user-level rules directory) and the Claude rule into
// ~/.claude/CLAUDE.md.
func TestAgentRuleAndHookPathsPersonalScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cursorTarget := targetFor(t, "", "cursor")
	if got, want := cursorPluginDir(cursorTarget), filepath.Join(home, ".cursor", "plugins", "local", skills.CursorPluginName); got != want {
		t.Errorf("cursor plugin dir = %s, want %s", got, want)
	}
	if got, want := cursorTarget.HooksPath, filepath.Join(home, ".cursor", "hooks.json"); got != want {
		t.Errorf("cursor hook path = %s, want %s", got, want)
	}

	claudeTarget := targetFor(t, "", "claude-code")
	if got, want := claudeTarget.InstructionsPath, filepath.Join(home, ".claude", "CLAUDE.md"); got != want {
		t.Errorf("claude instructions path = %s, want %s", got, want)
	}
	if got, want := claudeTarget.HooksPath, filepath.Join(home, ".claude", "settings.json"); got != want {
		t.Errorf("claude hook path = %s, want %s", got, want)
	}

	rulePath, err := installAgentRule(cursorTarget)
	if err != nil {
		t.Fatalf("installAgentRule: %v", err)
	}
	if rulePath != filepath.Join(cursorPluginDir(cursorTarget), "rules", skills.CursorRuleFilename) {
		t.Errorf("personal cursor rule landed at %s", rulePath)
	}
	if _, err := os.Stat(filepath.Join(cursorPluginDir(cursorTarget), ".cursor-plugin", "plugin.json")); err != nil {
		t.Errorf("plugin manifest missing: %v", err)
	}
	if _, found, err := removeAgentRule(cursorTarget); err != nil || !found {
		t.Errorf("removeAgentRule found=%v err=%v", found, err)
	}
}

func TestSkillsGuardCommand(t *testing.T) {
	run := func(t *testing.T, format, event string) string {
		t.Helper()
		if err := skillsGuardCmd.Flags().Set("format", format); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = skillsGuardCmd.Flags().Set("format", "cursor") })
		var out bytes.Buffer
		skillsGuardCmd.SetIn(strings.NewReader(event))
		skillsGuardCmd.SetOut(&out)
		t.Cleanup(func() { skillsGuardCmd.SetIn(nil); skillsGuardCmd.SetOut(nil) })
		if err := skillsGuardCmd.RunE(skillsGuardCmd, nil); err != nil {
			t.Fatalf("guard: %v", err)
		}
		return out.String()
	}

	if got := run(t, "cursor", `{"command":"kubectl apply -f x.yaml"}`); !strings.Contains(got, `"permission":"ask"`) {
		t.Errorf("expected ask decision, got %s", got)
	}
	if got := run(t, "cursor", `{"command":"kubectl get pods"}`); !strings.Contains(got, `"permission":"allow"`) {
		t.Errorf("expected allow decision, got %s", got)
	}
	if got := run(t, "claude", `{"tool_input":{"command":"helm upgrade x ./c"}}`); !strings.Contains(got, `"permissionDecision":"ask"`) {
		t.Errorf("expected ask decision, got %s", got)
	}
}

func TestSkillsGuardIsAuthFree(t *testing.T) {
	if commandRequiresAuth(skillsGuardCmd) {
		t.Error("ankra skills guard must not require auth; it runs inside editor hooks")
	}
}

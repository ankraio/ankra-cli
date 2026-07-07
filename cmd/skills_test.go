package cmd

import (
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
	c.Flags().String("editor", "cursor", "")
	c.Flags().String("source", "", "")
	return c
}

// newSkillsInstallTestCmd mirrors the full skills install flag set.
func newSkillsInstallTestCmd() *cobra.Command {
	c := newSkillsTestCmd()
	c.Flags().Bool("force", false, "")
	c.Flags().Bool("no-rules", false, "")
	c.Flags().Bool("with-hooks", false, "")
	return c
}

func TestSkillsTargetDirPersonalDefault(t *testing.T) {
	dir, scope, err := skillsTargetDir(newSkillsTestCmd())
	if err != nil {
		t.Fatal(err)
	}
	if scope != "personal" {
		t.Fatalf("expected personal scope, got %q", scope)
	}
	if filepath.Base(dir) != "skills" || filepath.Base(filepath.Dir(dir)) != ".cursor" {
		t.Fatalf("unexpected personal dir: %s", dir)
	}
}

func TestSkillsTargetDirProject(t *testing.T) {
	c := newSkillsTestCmd()
	if err := c.Flags().Set("project", "/tmp/foo"); err != nil {
		t.Fatal(err)
	}
	dir, scope, err := skillsTargetDir(c)
	if err != nil {
		t.Fatal(err)
	}
	if scope != "project" {
		t.Fatalf("expected project scope, got %q", scope)
	}
	want := filepath.Join("/tmp/foo", ".cursor", "skills")
	if dir != want {
		t.Fatalf("got %s want %s", dir, want)
	}
}

func TestSkillsTargetDirClaudeCodePersonal(t *testing.T) {
	c := newSkillsTestCmd()
	if err := c.Flags().Set("editor", "claude-code"); err != nil {
		t.Fatal(err)
	}
	dir, scope, err := skillsTargetDir(c)
	if err != nil {
		t.Fatal(err)
	}
	if scope != "personal" {
		t.Fatalf("expected personal scope, got %q", scope)
	}
	if filepath.Base(dir) != "skills" || filepath.Base(filepath.Dir(dir)) != ".claude" {
		t.Fatalf("unexpected claude code personal dir: %s", dir)
	}
}

func TestSkillsTargetDirClaudeCodeProject(t *testing.T) {
	c := newSkillsTestCmd()
	if err := c.Flags().Set("editor", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := c.Flags().Set("project", "/tmp/foo"); err != nil {
		t.Fatal(err)
	}
	dir, scope, err := skillsTargetDir(c)
	if err != nil {
		t.Fatal(err)
	}
	if scope != "project" {
		t.Fatalf("expected project scope, got %q", scope)
	}
	want := filepath.Join("/tmp/foo", ".claude", "skills")
	if dir != want {
		t.Fatalf("got %s want %s", dir, want)
	}
}

func TestSkillsTargetDirInvalidEditor(t *testing.T) {
	c := newSkillsTestCmd()
	if err := c.Flags().Set("editor", "zed"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := skillsTargetDir(c); err == nil {
		t.Fatal("expected unsupported editor error")
	}
}

func TestSkillsInstallCommandHasEditorFlag(t *testing.T) {
	flag := skillsInstallCmd.Flags().Lookup("editor")
	if flag == nil {
		t.Fatal("expected editor flag on skills install command")
	}
	if flag.DefValue != "cursor" {
		t.Fatalf("expected cursor default, got %q", flag.DefValue)
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
	c := newSkillsTestCmd()
	if err := c.Flags().Set("project", project); err != nil {
		t.Fatal(err)
	}
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

func runSkillsInstall(t *testing.T, project, editor string, withHooks bool, args []string) error {
	t.Helper()
	c := newSkillsInstallTestCmd()
	if err := c.Flags().Set("project", project); err != nil {
		t.Fatal(err)
	}
	if err := c.Flags().Set("editor", editor); err != nil {
		t.Fatal(err)
	}
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

func runSkillsUninstallForEditor(t *testing.T, project, editor string, args []string) error {
	t.Helper()
	c := newSkillsTestCmd()
	if err := c.Flags().Set("project", project); err != nil {
		t.Fatal(err)
	}
	if err := c.Flags().Set("editor", editor); err != nil {
		t.Fatal(err)
	}
	var err error
	captureStdout(t, func() {
		err = skillsUninstallCmd.RunE(c, args)
	})
	return err
}

// The full install/uninstall cycle for a Cursor project: skills, the
// always-applied rule, and the guard hook all appear and disappear together.
func TestSkillsInstallCursorProjectWritesRuleAndHook(t *testing.T) {
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
	hooksPath := filepath.Join(project, ".cursor", "hooks.json")
	hooksData, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("hooks.json not written: %v", err)
	}
	if !strings.Contains(string(hooksData), "skills guard --format cursor") {
		t.Errorf("guard not wired: %s", hooksData)
	}

	if err := runSkillsUninstallForEditor(t, project, "cursor", nil); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if dirExists(filepath.Join(project, ".cursor", "skills", "ankra-cli")) {
		t.Error("skills not removed")
	}
	if _, err := os.Stat(rulePath); !os.IsNotExist(err) {
		t.Error("rule not removed")
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

	if err := runSkillsUninstallForEditor(t, project, "claude-code", nil); err != nil {
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

// Named uninstalls must leave the rule and hook alone: only a full uninstall
// tears down the steering.
func TestSkillsNamedUninstallKeepsRuleAndHook(t *testing.T) {
	project := t.TempDir()
	if err := runSkillsInstall(t, project, "cursor", true, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := runSkillsUninstallForEditor(t, project, "cursor", []string{"ankra-cli"}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !dirExists(filepath.Join(project, ".cursor", "skills", "ankra-gitops")) {
		t.Error("unrelated skill removed")
	}
	if _, err := os.Stat(filepath.Join(project, ".cursor", "rules", "ankra.mdc")); err != nil {
		t.Error("rule should survive a named uninstall")
	}
	if _, err := os.Stat(filepath.Join(project, ".cursor", "hooks.json")); err != nil {
		t.Error("hook should survive a named uninstall")
	}
}

func TestSkillsInstallNoRulesSkipsRule(t *testing.T) {
	project := t.TempDir()
	c := newSkillsInstallTestCmd()
	if err := c.Flags().Set("project", project); err != nil {
		t.Fatal(err)
	}
	if err := c.Flags().Set("no-rules", "true"); err != nil {
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
	if _, err := os.Stat(filepath.Join(project, ".cursor", "hooks.json")); !os.IsNotExist(err) {
		t.Error("hooks are opt-in and must not be written by default")
	}
}

// Personal scope routes the Cursor rule into a local plugin (there is no
// supported user-level rules directory) and the Claude rule into
// ~/.claude/CLAUDE.md.
func TestAgentRuleAndHookPathsPersonalScope(t *testing.T) {
	home := t.TempDir()
	cursorTarget := skillsTarget{
		Scope: "personal", Root: home,
		Editor: skillsEditor{Name: "Cursor", ConfigurationDirectory: ".cursor", GuardFormat: "cursor"},
	}
	if got, want := cursorPluginDir(cursorTarget), filepath.Join(home, ".cursor", "plugins", "local", skills.CursorPluginName); got != want {
		t.Errorf("cursor plugin dir = %s, want %s", got, want)
	}
	if got, want := hookConfigPath(cursorTarget), filepath.Join(home, ".cursor", "hooks.json"); got != want {
		t.Errorf("cursor hook path = %s, want %s", got, want)
	}

	claudeTarget := skillsTarget{
		Scope: "personal", Root: home,
		Editor: skillsEditor{Name: "Claude Code", ConfigurationDirectory: ".claude", GuardFormat: "claude"},
	}
	if got, want := claudeMemoryPath(claudeTarget), filepath.Join(home, ".claude", "CLAUDE.md"); got != want {
		t.Errorf("claude memory path = %s, want %s", got, want)
	}
	if got, want := hookConfigPath(claudeTarget), filepath.Join(home, ".claude", "settings.json"); got != want {
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

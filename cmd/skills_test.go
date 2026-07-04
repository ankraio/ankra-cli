package cmd

import (
	"os"
	"path/filepath"
	"testing"

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

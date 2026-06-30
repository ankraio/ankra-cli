package cmd

import (
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

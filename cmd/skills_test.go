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

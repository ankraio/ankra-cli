package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedListHasSkills(t *testing.T) {
	fsys, err := EmbeddedFS()
	if err != nil {
		t.Fatalf("EmbeddedFS: %v", err)
	}
	list, err := List(fsys)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected embedded skills, got none")
	}
	foundCLI := false
	for _, s := range list {
		if s.Name == "" {
			t.Errorf("skill has empty name: %+v", s)
		}
		if s.Description == "" {
			t.Errorf("skill %q has empty description", s.Name)
		}
		if s.Name == "ankra-cli" {
			foundCLI = true
		}
	}
	if !foundCLI {
		t.Error("expected ankra-cli skill to be embedded")
	}
}

func TestEmbeddedListSorted(t *testing.T) {
	fsys, _ := EmbeddedFS()
	list, _ := List(fsys)
	for i := 1; i < len(list); i++ {
		if list[i-1].Name > list[i].Name {
			t.Fatalf("skills not sorted: %q before %q", list[i-1].Name, list[i].Name)
		}
	}
}

func TestInstallAllThenSkipThenForce(t *testing.T) {
	fsys, _ := EmbeddedFS()
	dest := t.TempDir()

	installed, skipped, err := Install(fsys, dest, nil, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(installed) == 0 {
		t.Fatal("expected skills installed")
	}
	if len(skipped) != 0 {
		t.Fatalf("expected nothing skipped on first install, got %v", skipped)
	}

	if _, err := os.Stat(filepath.Join(dest, "ankra-cli", "SKILL.md")); err != nil {
		t.Fatalf("expected ankra-cli/SKILL.md installed: %v", err)
	}

	installed2, skipped2, err := Install(fsys, dest, nil, false)
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if len(installed2) != 0 {
		t.Fatalf("expected all skipped without force, installed %v", installed2)
	}
	if len(skipped2) != len(installed) {
		t.Fatalf("expected %d skipped, got %d", len(installed), len(skipped2))
	}

	installed3, _, err := Install(fsys, dest, nil, true)
	if err != nil {
		t.Fatalf("force reinstall: %v", err)
	}
	if len(installed3) != len(installed) {
		t.Fatalf("expected %d reinstalled with force, got %d", len(installed), len(installed3))
	}
}

func TestInstallNamedAndUnknown(t *testing.T) {
	fsys, _ := EmbeddedFS()
	dest := t.TempDir()

	installed, _, err := Install(fsys, dest, []string{"ankra-cli"}, false)
	if err != nil {
		t.Fatalf("install named: %v", err)
	}
	if len(installed) != 1 || installed[0] != "ankra-cli" {
		t.Fatalf("expected only ankra-cli installed, got %v", installed)
	}

	if _, _, err := Install(fsys, dest, []string{"does-not-exist"}, false); err == nil {
		t.Fatal("expected error for unknown skill")
	}
}

func TestSourceFSAcceptsRepoRootAndSkillsDir(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills", "demo-skill")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo-skill\ndescription: A demo.\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	fsysRoot, err := SourceFS(root)
	if err != nil {
		t.Fatalf("SourceFS(root): %v", err)
	}
	names, err := Names(fsysRoot)
	if err != nil || len(names) != 1 || names[0] != "demo-skill" {
		t.Fatalf("expected demo-skill via repo root, got %v err=%v", names, err)
	}

	fsysSkills, err := SourceFS(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatalf("SourceFS(skills): %v", err)
	}
	names2, err := Names(fsysSkills)
	if err != nil || len(names2) != 1 || names2[0] != "demo-skill" {
		t.Fatalf("expected demo-skill via skills dir, got %v err=%v", names2, err)
	}
}

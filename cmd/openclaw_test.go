package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
	"ankra/internal/skills"

	"github.com/spf13/cobra"
)

// newOpenclawSkillTestCmd mirrors the flags `ankra openclaw skill` reads.
func newOpenclawSkillTestCmd() *cobra.Command {
	c := &cobra.Command{Use: "skill", Run: func(*cobra.Command, []string) {}}
	c.Flags().String("output", "", "")
	c.Flags().String("cluster", "", "")
	return c
}

// selectClusterForTest points $HOME at a fresh directory holding a selected
// cluster, the state `ankra cluster select` leaves behind.
func selectClusterForTest(t *testing.T, cluster client.ClusterListItem) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	originalConfigFile := cfgFile
	cfgFile = ""
	t.Cleanup(func() { cfgFile = originalConfigFile })
	if err := saveSelectedCluster(cluster); err != nil {
		t.Fatal(err)
	}
	return home
}

func runOpenclawSkill(t *testing.T, c *cobra.Command) (string, error) {
	t.Helper()
	var err error
	out := captureStdout(t, func() {
		err = openclawSkillCmd.RunE(c, nil)
	})
	return out, err
}

// TestOpenclawSkillDefaultIsASkillDirectory pins ankra-0iixk: OpenClaw only
// loads <dir>/SKILL.md, so the default write is
// ~/.openclaw/skills/ankra-<cluster>/SKILL.md, never a flat .md file.
func TestOpenclawSkillDefaultIsASkillDirectory(t *testing.T) {
	home := selectClusterForTest(t, client.ClusterListItem{ID: "c-123", Name: "Prod EU"})

	out, err := runOpenclawSkill(t, newOpenclawSkillTestCmd())
	if err != nil {
		t.Fatalf("openclaw skill: %v", err)
	}
	want := filepath.Join(home, ".openclaw", "skills", "ankra-prod-eu", "SKILL.md")
	body, readErr := os.ReadFile(want)
	if readErr != nil {
		t.Fatalf("expected the skill at %s: %v", want, readErr)
	}
	if !strings.HasPrefix(string(body), "---\nname: ankra-prod-eu\n") {
		t.Fatalf("the SKILL.md must open with the frontmatter name, got %q", string(body)[:40])
	}
	if _, statErr := os.Stat(filepath.Join(home, ".openclaw", "skills", "ankra-prod-eu.md")); !os.IsNotExist(statErr) {
		t.Fatalf("no flat ankra-<cluster>.md may be written: %v", statErr)
	}
	if !strings.Contains(out, want) {
		t.Fatalf("the output must name the written path %s: %q", want, out)
	}
}

// TestOpenclawSkillHonoursOutput pins that --output still writes exactly
// where it is told, the workaround the docs used before this fix.
func TestOpenclawSkillHonoursOutput(t *testing.T) {
	home := selectClusterForTest(t, client.ClusterListItem{ID: "c-123", Name: "prod"})
	target := filepath.Join(t.TempDir(), "custom", "SKILL.md")
	c := newOpenclawSkillTestCmd()
	if err := c.Flags().Set("output", target); err != nil {
		t.Fatal(err)
	}
	if _, err := runOpenclawSkill(t, c); err != nil {
		t.Fatalf("openclaw skill --output: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected the skill at %s: %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".openclaw")); !os.IsNotExist(err) {
		t.Fatalf("--output must not also write the default location: %v", err)
	}
}

// TestOpenclawSkillRefusesToOverwriteABundledSkill pins that a cluster whose
// skill directory would be named like a bundled Ankra skill (ankra-cli for a
// cluster named "cli") does not overwrite what `ankra skills install
// --client openclaw` put in the same directory.
func TestOpenclawSkillRefusesToOverwriteABundledSkill(t *testing.T) {
	home := selectClusterForTest(t, client.ClusterListItem{ID: "c-1", Name: "CLI"})
	installed := filepath.Join(home, ".openclaw", "skills", "ankra-cli", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("---\nname: ankra-cli\ndescription: bundled\n---\n")
	if err := os.WriteFile(installed, original, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runOpenclawSkill(t, newOpenclawSkillTestCmd())
	var coded *codedError
	if err == nil || !errors.As(err, &coded) || coded.code != exitUsage {
		t.Fatalf("want a usage error, got %v", err)
	}
	if !strings.Contains(err.Error(), "--output") {
		t.Fatalf("the error must point at --output: %v", err)
	}
	body, _ := os.ReadFile(installed)
	if string(body) != string(original) {
		t.Fatalf("the bundled skill was overwritten: %q", body)
	}
}

func TestDefaultOpenclawSkillPathRejectsAnUnusableName(t *testing.T) {
	if _, err := defaultOpenclawSkillPath(t.TempDir(), "!!!"); err == nil {
		t.Fatal("a cluster name with no usable characters must be refused")
	}
}

// TestOpenclawSkillPromisesNoHandoff pins that the generated skill no longer
// tells the agent a handoff command opens its conversation in the portal:
// nothing in the portal reads such a link (ankra-0iixk).
func TestOpenclawSkillPromisesNoHandoff(t *testing.T) {
	body := buildSkillMarkdown("prod", "c-1", "https://platform.ankra.app")
	for _, forbidden := range []string{"handoff", "pre-loaded", "?openclaw=", "/organisation/ai-agents"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("the skill must not mention %q:\n%s", forbidden, body)
		}
	}
	if !strings.Contains(body, "https://platform.ankra.app/organisation/ai/agents") {
		t.Fatalf("the skill should link the AI Agents page that exists:\n%s", body)
	}
}

// TestOpenclawHandoffIsDeprecatedAndHonest pins the handoff command: it is
// deprecated, prints only the AI Agents page that exists, and still accepts
// the conversation id so existing scripts keep running.
func TestOpenclawHandoffIsDeprecatedAndHonest(t *testing.T) {
	if openclawHandoffCmd.Deprecated == "" {
		t.Fatal("openclaw handoff must be marked deprecated")
	}
	originalBase := baseURL
	baseURL = "https://platform.example.test/"
	t.Cleanup(func() { baseURL = originalBase })

	c := &cobra.Command{Use: "handoff"}
	c.SetErr(new(strings.Builder))
	var err error
	out := captureStdout(t, func() {
		err = openclawHandoffCmd.RunE(c, []string{"conv-1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "https://platform.example.test/organisation/ai/agents") {
		t.Fatalf("want the AI Agents page URL, got %q", out)
	}
	if strings.Contains(out, "openclaw=") || strings.Contains(out, "conv-1") {
		t.Fatalf("no conversation parameter may be printed: %q", out)
	}
}

// TestSkillsInstallForOpenclawUsesTheSkillDirectoryLayout pins that `ankra
// skills install --client openclaw` writes the same layout as `ankra
// openclaw skill`: ~/.openclaw/skills/<name>/SKILL.md.
func TestSkillsInstallForOpenclawUsesTheSkillDirectoryLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := runSkillsInstall(t, "", "openclaw", false, []string{"ankra-cli"}); err != nil {
		t.Fatalf("skills install --client openclaw: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".openclaw", "skills", "ankra-cli", "SKILL.md")); err != nil {
		t.Fatalf("expected ~/.openclaw/skills/ankra-cli/SKILL.md: %v", err)
	}
}

// TestClusterSkillIsNotAnAnkraSkillsInstall pins that the per-cluster skill
// `ankra openclaw skill` writes into ~/.openclaw/skills is neither counted as
// an `ankra skills` install nor named in the upgrade refresh, which would
// otherwise run `skills install ankra-<cluster>` and fail on an unknown skill
// (or, with nothing else installed, install every skill unasked).
func TestClusterSkillIsNotAnAnkraSkillsInstall(t *testing.T) {
	home := selectClusterForTest(t, client.ClusterListItem{ID: "c-1", Name: "prod"})
	if _, err := runOpenclawSkill(t, newOpenclawSkillTestCmd()); err != nil {
		t.Fatal(err)
	}
	if ids := installedClientIDsOf(t, home); len(ids) != 0 {
		t.Fatalf("a cluster skill alone is not an Ankra skills install, got %v", ids)
	}

	openclaw := clientNamed(t, "openclaw")
	writeInstalledSkill(t, home, openclaw, false)
	groups, err := skillsRefreshGroupsFor(home, []skills.Client{openclaw})
	if err != nil {
		t.Fatal(err)
	}
	want := "skills install ankra-cli --force --client openclaw"
	if len(groups) != 1 {
		t.Fatalf("want one refresh group, got %+v", groups)
	}
	if got := strings.Join(skillsRefreshArguments(groups[0]), " "); got != want {
		t.Fatalf("the refresh must name only bundled skills: want %q, got %q", want, got)
	}
}

package skills

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientRegistryIsWellFormed(t *testing.T) {
	seen := make(map[string]string)
	for _, client := range Clients() {
		if client.ID == "" || client.DisplayName == "" {
			t.Fatalf("client with empty id or display name: %+v", client)
		}
		for _, name := range append([]string{client.ID}, client.Aliases...) {
			if owner, taken := seen[name]; taken {
				t.Errorf("%q is claimed by both %s and %s", name, owner, client.ID)
			}
			seen[name] = client.ID
		}
		if client.Personal.Skills == "" || client.Project.Skills == "" {
			t.Errorf("%s has no skills directory for one of the scopes", client.ID)
		}
		if client.RuleFormat == "block" && client.Personal.Instructions == "" && client.Project.Instructions == "" {
			t.Errorf("%s takes a managed block but names no instructions file", client.ID)
		}
		if client.CommandFormat != "" && client.Personal.Commands == "" && client.Project.Commands == "" {
			t.Errorf("%s declares a command format but no commands directory", client.ID)
		}
		if client.HookFormat != "" && client.Personal.Hooks == "" {
			t.Errorf("%s declares a hook format but no hook config path", client.ID)
		}
	}
	for _, required := range []string{"claude-code", "claude-app", "cursor", "codex", "copilot", "agents"} {
		if _, taken := seen[required]; !taken {
			t.Errorf("the registry is missing %s", required)
		}
	}
}

func TestLookupClientAcceptsAliases(t *testing.T) {
	for alias, want := range map[string]string{
		"claude":         "claude-code",
		"CLAUDE-CODE":    "claude-code",
		" cursor ":       "cursor",
		"github-copilot": "copilot",
		"claude-desktop": "claude-app",
		"generic":        "agents",
	} {
		client, err := LookupClient(alias)
		if err != nil {
			t.Fatalf("LookupClient(%q): %v", alias, err)
		}
		if client.ID != want {
			t.Errorf("LookupClient(%q) = %s, want %s", alias, client.ID, want)
		}
	}
	if _, err := LookupClient("notepad"); err == nil {
		t.Error("expected an error for an unknown client")
	}
}

func TestResolveClientsExpandsAllAndCommaLists(t *testing.T) {
	root := t.TempDir()

	all, err := ResolveClients([]string{"all"}, root, ScopePersonal)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(Clients()) {
		t.Errorf("\"all\" resolved %d of %d clients", len(all), len(Clients()))
	}

	pair, err := ResolveClients([]string{"cursor,claude-code", "cursor"}, root, ScopePersonal)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(pair))
	for _, client := range pair {
		got = append(got, client.ID)
	}
	if strings.Join(got, ",") != "claude-code,cursor" {
		t.Errorf("expected de-duplicated registry order, got %v", got)
	}
}

func TestResolveClientsAutoDetectsAndFallsBack(t *testing.T) {
	root := t.TempDir()
	fallback, err := ResolveClients(nil, root, ScopePersonal)
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback) != 2 {
		t.Fatalf("expected the two-client fallback, got %d", len(fallback))
	}

	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	detected, err := ResolveClients(nil, root, ScopePersonal)
	if err != nil {
		t.Fatal(err)
	}
	if len(detected) != 1 || detected[0].ID != "codex" {
		t.Fatalf("expected codex to be detected alone, got %+v", detected)
	}
}

func TestResolveTargetJoinsLayoutOntoRoot(t *testing.T) {
	root := t.TempDir()
	client, err := LookupClient("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	target, err := ResolveTarget(client, ScopeProject, root)
	if err != nil {
		t.Fatal(err)
	}
	if target.SkillsDirectory != filepath.Join(root, ".claude", "skills") {
		t.Errorf("skills directory = %s", target.SkillsDirectory)
	}
	if target.InstructionsPath != filepath.Join(root, "CLAUDE.md") {
		t.Errorf("instructions path = %s", target.InstructionsPath)
	}
	if !target.SupportsHooks() || !target.SupportsCommands() {
		t.Error("claude-code should support both hooks and commands")
	}

	codex, err := LookupClient("codex")
	if err != nil {
		t.Fatal(err)
	}
	codexTarget, err := ResolveTarget(codex, ScopeProject, root)
	if err != nil {
		t.Fatal(err)
	}
	if codexTarget.SupportsHooks() {
		t.Error("codex has no hook mechanism")
	}
	if codexTarget.SupportsCommands() {
		t.Error("codex has no project-scoped commands directory")
	}
}

func TestInstructionsBlockIndexesSkillsForIndexedClients(t *testing.T) {
	root := t.TempDir()
	client, err := LookupClient("codex")
	if err != nil {
		t.Fatal(err)
	}
	target, err := ResolveTarget(client, ScopeProject, root)
	if err != nil {
		t.Fatal(err)
	}
	block := InstructionsBlock(target, []Skill{
		{Name: "ankra-cli", Description: "Drive the Ankra CLI. Use when the user mentions the ankra CLI."},
	})
	for _, want := range []string{
		"Ankra skills installed here",
		"**`ankra-cli`** — Drive the Ankra CLI",
		".ankra/skills",
		"ankra-platform-principles",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "Use when the user mentions") {
		t.Error("the index should drop the trigger clause from the description")
	}
}

func TestSkillIndexPathIsRelativeForAProjectInstall(t *testing.T) {
	root := t.TempDir()
	client, err := LookupClient("copilot")
	if err != nil {
		t.Fatal(err)
	}
	project, err := ResolveTarget(client, ScopeProject, root)
	if err != nil {
		t.Fatal(err)
	}
	block := InstructionsBlock(project, []Skill{{Name: "ankra-cli", Description: "Drive the Ankra CLI."}})
	if strings.Contains(block, root) {
		t.Errorf("a committed project index must not carry an absolute path:\n%s", block)
	}
	if !strings.Contains(block, "`"+ankraLibrary+"`") {
		t.Errorf("expected the repository-relative directory:\n%s", block)
	}

	personal, err := ResolveTarget(client, ScopePersonal, root)
	if err != nil {
		t.Fatal(err)
	}
	personalBlock := InstructionsBlock(personal, []Skill{{Name: "ankra-cli", Description: "Drive the Ankra CLI."}})
	if !strings.Contains(personalBlock, personal.SkillsDirectory) {
		t.Errorf("a personal index should name the absolute directory:\n%s", personalBlock)
	}
}

func TestHeadlineKeepsOneLinePerSkill(t *testing.T) {
	for description, want := range map[string]string{
		"Take source code to production - registering, building, deploying. Use when the user deploys code.": "Take source code to production",
		"Compose Ankra Stacks from Helm addons. Use whenever editing a stack.":                               "Compose Ankra Stacks from Helm addons",
		"Structure a GitOps repository": "Structure a GitOps repository",
	} {
		if got := headline(description); got != want {
			t.Errorf("headline(%q) = %q, want %q", description, got, want)
		}
	}
}

func TestInstructionsBlockForNativeClientPointsAtTheDirectory(t *testing.T) {
	root := t.TempDir()
	client, err := LookupClient("cursor")
	if err != nil {
		t.Fatal(err)
	}
	target, err := ResolveTarget(client, ScopeProject, root)
	if err != nil {
		t.Fatal(err)
	}
	block := InstructionsBlock(target, []Skill{{Name: "ankra-cli", Description: "Drive the Ankra CLI."}})
	if !strings.Contains(block, "loads these from") {
		t.Errorf("expected the native wording:\n%s", block)
	}
}

func TestUpsertAndRemoveManagedBlockPreserveUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte("# House rules\n\nUse tabs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpsertManagedBlock(path, "<!-- BEGIN ANKRA MANAGED BLOCK (ankra skills install) -->\nfirst\n<!-- END ANKRA MANAGED BLOCK -->"); err != nil {
		t.Fatal(err)
	}
	if err := UpsertManagedBlock(path, "<!-- BEGIN ANKRA MANAGED BLOCK (ankra skills install) -->\nsecond\n<!-- END ANKRA MANAGED BLOCK -->"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), "BEGIN ANKRA MANAGED BLOCK") != 1 {
		t.Errorf("upsert duplicated the block:\n%s", content)
	}
	if !strings.Contains(string(content), "second") || strings.Contains(string(content), "first") {
		t.Errorf("upsert did not replace the block body:\n%s", content)
	}
	if !strings.Contains(string(content), "Use tabs.") {
		t.Errorf("user content lost:\n%s", content)
	}

	found, err := RemoveManagedBlock(path)
	if err != nil || !found {
		t.Fatalf("RemoveManagedBlock found=%v err=%v", found, err)
	}
	content, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("file with user content should survive: %v", err)
	}
	if strings.Contains(string(content), "ANKRA MANAGED BLOCK") {
		t.Errorf("block not removed:\n%s", content)
	}
}

func TestWriteAndRemoveWorkflowsPerFormat(t *testing.T) {
	for _, testCase := range []struct {
		client   string
		file     string
		contains string
	}{
		{"claude-code", "ankra-ship-service.md", "---\ndescription: "},
		{"copilot", "ankra-ship-service.prompt.md", "---\nmode: agent\n"},
		{"gemini", "ship-service.toml", "prompt = \"\"\""},
	} {
		t.Run(testCase.client, func(t *testing.T) {
			root := t.TempDir()
			client, err := LookupClient(testCase.client)
			if err != nil {
				t.Fatal(err)
			}
			target, err := ResolveTarget(client, ScopeProject, root)
			if err != nil {
				t.Fatal(err)
			}
			written, err := WriteWorkflows(target)
			if err != nil {
				t.Fatal(err)
			}
			if len(written) != len(Workflows()) {
				t.Fatalf("wrote %d of %d workflows", len(written), len(Workflows()))
			}
			path := filepath.Join(target.CommandsDirectory, testCase.file)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("expected %s: %v", path, err)
			}
			if !strings.Contains(string(content), testCase.contains) {
				t.Errorf("%s missing %q:\n%s", testCase.file, testCase.contains, content)
			}

			removed, err := RemoveWorkflows(target)
			if err != nil {
				t.Fatal(err)
			}
			if removed != len(Workflows()) {
				t.Errorf("removed %d of %d workflows", removed, len(Workflows()))
			}
		})
	}
}

func TestWriteWorkflowsIsANoOpWithoutACommandsDirectory(t *testing.T) {
	root := t.TempDir()
	client, err := LookupClient("zed")
	if err != nil {
		t.Fatal(err)
	}
	target, err := ResolveTarget(client, ScopeProject, root)
	if err != nil {
		t.Fatal(err)
	}
	written, err := WriteWorkflows(target)
	if err != nil || len(written) != 0 {
		t.Fatalf("expected a no-op, wrote %d (err %v)", len(written), err)
	}
}

func TestPackageBundlesProducesUploadableZips(t *testing.T) {
	fsys, err := EmbeddedFS()
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	written, skipped, err := PackageBundles(fsys, destination, []string{"ankra-cli"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || len(skipped) != 0 {
		t.Fatalf("written=%v skipped=%v", written, skipped)
	}
	data, err := os.ReadFile(filepath.Join(destination, "ankra-cli.zip"))
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range archive.File {
		if file.Name == "ankra-cli/SKILL.md" {
			found = true
		}
		if strings.HasPrefix(file.Name, "/") || strings.Contains(file.Name, "..") {
			t.Errorf("unsafe archive entry %q", file.Name)
		}
	}
	if !found {
		t.Error("bundle does not carry ankra-cli/SKILL.md")
	}

	_, skipped, err = PackageBundles(fsys, destination, []string{"ankra-cli"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 {
		t.Error("an existing bundle should be skipped without --force")
	}

	removed, err := RemoveBundles(destination, []string{"ankra-cli", "never-installed"})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed %d bundles, want 1", removed)
	}
}

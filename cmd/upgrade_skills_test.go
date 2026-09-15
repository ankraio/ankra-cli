package cmd

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/skills"
)

func writeInstalledSkill(t *testing.T, home string, client skills.Client, withManagedBlock bool) skills.Target {
	t.Helper()
	target, err := skills.ResolveTarget(client, skills.ScopePersonal, home)
	if err != nil {
		t.Fatalf("resolve %s: %v", client.ID, err)
	}
	skillDirectory := filepath.Join(target.SkillsDirectory, "ankra-cli")
	if err := os.MkdirAll(skillDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDirectory, "SKILL.md"),
		[]byte("---\nname: ankra-cli\ndescription: Drive the CLI\n---\n\n# Ankra CLI\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if withManagedBlock && target.InstructionsPath != "" {
		if err := skills.UpsertManagedBlock(target.InstructionsPath, skills.InstructionsBlock(target, nil)); err != nil {
			t.Fatal(err)
		}
	}
	return target
}

func clientNamed(t *testing.T, id string) skills.Client {
	t.Helper()
	client, err := skills.LookupClient(id)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func installedClientIDsOf(t *testing.T, home string) []string {
	t.Helper()
	clients, err := skillsInstalledClients(home)
	if err != nil {
		t.Fatalf("skillsInstalledClients: %v", err)
	}
	return installedClientIDs(clients)
}

func installedClientIDs(clients []skills.Client) []string {
	ids := make([]string, 0, len(clients))
	for _, client := range clients {
		ids = append(ids, client.ID)
	}
	return ids
}

func TestSkillsInstalledClientsRecognisesNativeAndIndexedInstalls(t *testing.T) {
	home := t.TempDir()
	if got := installedClientIDsOf(t, home); len(got) != 0 {
		t.Fatalf("empty home should carry no install, got %v", got)
	}

	writeInstalledSkill(t, home, clientNamed(t, "claude-code"), false)
	got := installedClientIDsOf(t, home)
	if strings.Join(got, ",") != "claude-code" {
		t.Fatalf("a native skills directory alone is an install; got %v", got)
	}

	writeInstalledSkill(t, home, clientNamed(t, "windsurf"), false)
	got = installedClientIDsOf(t, home)
	if strings.Join(got, ",") != "claude-code" {
		t.Fatalf("an indexed client without its managed block is not an install; got %v", got)
	}

	writeInstalledSkill(t, home, clientNamed(t, "windsurf"), true)
	got = installedClientIDsOf(t, home)
	if strings.Join(got, ",") != "claude-code,windsurf" {
		t.Fatalf("the managed block makes the indexed install count; got %v", got)
	}
}

func TestDecideSkillsRefresh(t *testing.T) {
	claudeCode := clientNamed(t, "claude-code")
	cursor := clientNamed(t, "cursor")
	cases := []struct {
		name   string
		choice skillsRefreshChoice
		input  string
		want   bool
		asked  bool
	}{
		{name: "nothing installed", choice: skillsRefreshChoice{}, input: "y\n", want: false},
		{name: "explicit --skills=false", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagExplicit: true, FlagValue: false}, input: "y\n", want: false},
		{name: "explicit --skills", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagExplicit: true, FlagValue: true}, want: true},
		{name: "--yes takes the offer", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagValue: true, SkipPrompts: true}, want: true},
		{name: "enter means yes", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode, cursor}, FlagValue: true, TargetVersion: "0.18.0"}, input: "\n", want: true, asked: true},
		{name: "y means yes", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagValue: true}, input: "y\n", want: true, asked: true},
		{name: "n means no", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagValue: true}, input: "n\n", want: false, asked: true},
		{name: "closed stdin means no", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagValue: true}, input: "", want: false, asked: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := decideSkillsRefresh(strings.NewReader(testCase.input), &out, testCase.choice)
			if err != nil {
				t.Fatal(err)
			}
			if got != testCase.want {
				t.Fatalf("want %v, got %v (output %q)", testCase.want, got, out.String())
			}
			asked := strings.Contains(out.String(), "Also refresh the Ankra agent skills")
			if asked != testCase.asked {
				t.Fatalf("asked=%v, want %v (output %q)", asked, testCase.asked, out.String())
			}
			if testCase.asked && len(testCase.choice.Clients) == 2 && !strings.Contains(out.String(), "Claude Code and Cursor to v0.18.0") {
				t.Fatalf("prompt should name the assistants and the version: %q", out.String())
			}
			if testCase.asked && testCase.input == "" && !strings.Contains(out.String(), "ankra skills install --force --client claude-code") {
				t.Fatalf("an unanswered question must print the by-hand command: %q", out.String())
			}
		})
	}
}

// TestSkillsPromptReadsTypedAheadInputAfterTheUpgradePrompt pins the shared
// reader contract runUpgrade relies on: confirmPrompt wraps its reader in
// bufio.NewReader, which hands back the same *bufio.Reader when given one, so
// a second line typed ahead of the first question is still there for the
// second question instead of being swallowed by a discarded buffer.
func TestSkillsPromptReadsTypedAheadInputAfterTheUpgradePrompt(t *testing.T) {
	input := bufio.NewReader(strings.NewReader("y\nn\n"))
	var out bytes.Buffer
	if err := confirmPrompt(input, &out, "Upgrade? [y/N]: ", false); err != nil {
		t.Fatalf("the first prompt should read the y: %v", err)
	}
	refresh, err := decideSkillsRefresh(input, &out, skillsRefreshChoice{
		Clients:   []skills.Client{clientNamed(t, "claude-code")},
		FlagValue: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if refresh {
		t.Fatalf("the second prompt must read the typed-ahead n, not fall to a default; output %q", out.String())
	}
}

func TestRefreshInstalledSkillsRunsTheNewBinary(t *testing.T) {
	original := runSkillsRefresh
	t.Cleanup(func() { runSkillsRefresh = original })

	var gotExecutable string
	var gotArguments []string
	runSkillsRefresh = func(out io.Writer, executable string, arguments []string) error {
		gotExecutable = executable
		gotArguments = arguments
		_, _ = io.WriteString(out, "installed 25 skills\n")
		return nil
	}
	var out bytes.Buffer
	refreshInstalledSkills(&out, "/usr/local/bin/ankra", []skills.Client{clientNamed(t, "claude-code"), clientNamed(t, "codex")})
	if gotExecutable != "/usr/local/bin/ankra" {
		t.Fatalf("the refresh must run the replaced binary, got %q", gotExecutable)
	}
	want := "skills install --force --client claude-code --client codex"
	if strings.Join(gotArguments, " ") != want {
		t.Fatalf("arguments: want %q, got %q", want, strings.Join(gotArguments, " "))
	}
	if !strings.Contains(out.String(), "Refreshing the Ankra agent skills for Claude Code and Codex") || !strings.Contains(out.String(), "installed 25 skills") {
		t.Fatalf("output: %q", out.String())
	}

	runSkillsRefresh = func(io.Writer, string, []string) error { return io.ErrUnexpectedEOF }
	out.Reset()
	refreshInstalledSkills(&out, "/usr/local/bin/ankra", []skills.Client{clientNamed(t, "claude-code")})
	if !strings.Contains(out.String(), "Warning: the agent skills were not refreshed") ||
		!strings.Contains(out.String(), "/usr/local/bin/ankra skills install --force --client claude-code") {
		t.Fatalf("a failed refresh must say so and name the command: %q", out.String())
	}
}

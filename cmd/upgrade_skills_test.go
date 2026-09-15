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

func TestSkillsInstalledClientsReportsAnUnreadableInstructionsFile(t *testing.T) {
	home := t.TempDir()
	target := writeInstalledSkill(t, home, clientNamed(t, "windsurf"), true)
	if err := os.Chmod(target.InstructionsPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target.InstructionsPath, 0o644) })
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	if _, err := skillsInstalledClients(home); err == nil {
		t.Fatal("an unreadable instructions file must be an error, not \"not installed\"")
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
		// hinted is the one stderr line --yes alone prints: the skills were
		// not refreshed, and the command that does it.
		hinted bool
	}{
		{name: "nothing installed", choice: skillsRefreshChoice{}, input: "y\n", want: false},
		{name: "explicit --skills=false", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagExplicit: true, FlagValue: false}, input: "y\n", want: false},
		{name: "explicit --skills", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagExplicit: true, FlagValue: true}, want: true},
		{name: "--yes alone leaves the skills alone", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagValue: true, SkipPrompts: true}, input: "y\n", want: false, hinted: true},
		{name: "--yes with --skills refreshes", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagExplicit: true, FlagValue: true, SkipPrompts: true}, want: true},
		{name: "--yes with --skills=false declines", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagExplicit: true, FlagValue: false, SkipPrompts: true}, want: false},
		{name: "enter means yes", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode, cursor}, FlagValue: true, TargetVersion: "0.18.0"}, input: "\n", want: true, asked: true},
		{name: "y means yes", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagValue: true}, input: "y\n", want: true, asked: true},
		{name: "n means no", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagValue: true}, input: "n\n", want: false, asked: true},
		{name: "closed stdin means no", choice: skillsRefreshChoice{Clients: []skills.Client{claudeCode}, FlagValue: true}, input: "", want: false, asked: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			got, err := decideSkillsRefresh(strings.NewReader(testCase.input), &out, &errOut, testCase.choice)
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
			hinted := strings.Contains(errOut.String(), "were not refreshed")
			if hinted != testCase.hinted {
				t.Fatalf("hinted=%v, want %v (stderr %q)", hinted, testCase.hinted, errOut.String())
			}
			if testCase.hinted {
				if strings.Count(strings.TrimSpace(errOut.String()), "\n") != 0 {
					t.Fatalf("the --yes hint must be one line: %q", errOut.String())
				}
				if !strings.Contains(errOut.String(), "ankra skills install --force --client claude-code") {
					t.Fatalf("the --yes hint must name the by-hand command: %q", errOut.String())
				}
				if out.Len() != 0 {
					t.Fatalf("--yes alone must print nothing on stdout, got %q", out.String())
				}
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
	refresh, err := decideSkillsRefresh(input, &out, io.Discard, skillsRefreshChoice{
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
	defaults := []skillsRefreshGroup{{
		Options: skills.DefaultInstallOptions(),
		Clients: []skills.Client{clientNamed(t, "claude-code"), clientNamed(t, "codex")},
	}}
	refreshInstalledSkills(&out, "/usr/local/bin/ankra", defaults)
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
	refreshInstalledSkills(&out, "/usr/local/bin/ankra", []skillsRefreshGroup{{
		Options: skills.DefaultInstallOptions(),
		Clients: []skills.Client{clientNamed(t, "claude-code")},
	}})
	if !strings.Contains(out.String(), "Warning: the agent skills for Claude Code were not refreshed") ||
		!strings.Contains(out.String(), "/usr/local/bin/ankra skills install --force --client claude-code") {
		t.Fatalf("a failed refresh must say so and name the command: %q", out.String())
	}
}

// TestRefreshInstalledSkillsReplaysRecordedOptions pins the fix for the
// upgrade refresh overwriting a person's choices: an install made with
// --no-rules, --no-workflows or --with-hooks is refreshed with exactly those
// flags, one `skills install` run per distinct set of options, and an
// install with nothing recorded rides the defaults run.
func TestRefreshInstalledSkillsReplaysRecordedOptions(t *testing.T) {
	home := t.TempDir()
	claudeCode := clientNamed(t, "claude-code")
	cursor := clientNamed(t, "cursor")
	codex := clientNamed(t, "codex")
	if err := skills.RecordInstallOptions(home, claudeCode.ID, skills.InstallOptions{Rules: false, Workflows: false, Hooks: true}); err != nil {
		t.Fatal(err)
	}
	if err := skills.RecordInstallOptions(home, cursor.ID, skills.InstallOptions{Rules: false, Workflows: false, Hooks: true}); err != nil {
		t.Fatal(err)
	}
	// codex has no record: it was installed by a release that wrote none.

	groups, err := skillsRefreshGroupsFor(home, []skills.Client{claudeCode, cursor, codex})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("want two groups (custom, defaults), got %+v", groups)
	}

	original := runSkillsRefresh
	t.Cleanup(func() { runSkillsRefresh = original })
	var runs []string
	runSkillsRefresh = func(_ io.Writer, _ string, arguments []string) error {
		runs = append(runs, strings.Join(arguments, " "))
		return nil
	}
	var out bytes.Buffer
	refreshInstalledSkills(&out, "/usr/local/bin/ankra", groups)
	want := []string{
		"skills install --force --no-rules --no-workflows --with-hooks --client claude-code --client cursor",
		"skills install --force --client codex",
	}
	if strings.Join(runs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("replayed commands:\nwant %q\n got %q", want, runs)
	}
	if !strings.Contains(out.String(), "Refreshing the Ankra agent skills for Claude Code, Cursor and Codex CLI") {
		t.Fatalf("the refresh must name every assistant across the groups: %q", out.String())
	}
}

// TestSkillsRefreshGroupsDefaultWithoutARecord pins the backwards
// compatibility: a home with no record at all (every install predates the
// record) refreshes exactly as before, one defaults run naming each client.
func TestSkillsRefreshGroupsDefaultWithoutARecord(t *testing.T) {
	home := t.TempDir()
	clients := []skills.Client{clientNamed(t, "claude-code"), clientNamed(t, "windsurf")}
	groups, err := skillsRefreshGroupsFor(home, clients)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Options != skills.DefaultInstallOptions() || len(groups[0].Clients) != 2 {
		t.Fatalf("want one defaults group with both clients, got %+v", groups)
	}
	want := "skills install --force --client claude-code --client windsurf"
	if got := strings.Join(skillsRefreshArguments(groups[0]), " "); got != want {
		t.Fatalf("arguments: want %q, got %q", want, got)
	}
	if got := skillsRefreshCommandLine("ankra", groups); got != "ankra "+want {
		t.Fatalf("command line: want %q, got %q", "ankra "+want, got)
	}
}

// TestSkillsRefreshGroupsReportAnUnreadableRecord pins that a record which
// cannot be read is an error and not "nothing recorded": replaying the
// defaults over choices we cannot see is the defect the record prevents.
func TestSkillsRefreshGroupsReportAnUnreadableRecord(t *testing.T) {
	home := t.TempDir()
	path := skills.InstallRecordPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := skillsRefreshGroupsFor(home, []skills.Client{clientNamed(t, "claude-code")}); err == nil {
		t.Fatal("a malformed record must be an error")
	}
}

// TestSkillsInstallRecordsTheOptionsItUsed pins the writer side: a personal
// install records the options the upgrade refresh replays, a re-install
// replaces them, and a full uninstall forgets them.
func TestSkillsInstallRecordsTheOptionsItUsed(t *testing.T) {
	home := t.TempDir()
	fsys, err := skills.EmbeddedFS()
	if err != nil {
		t.Fatal(err)
	}
	target, err := skills.ResolveTarget(clientNamed(t, "claude-code"), skills.ScopePersonal, home)
	if err != nil {
		t.Fatal(err)
	}
	install := func(options skills.InstallOptions) {
		t.Helper()
		var installError error
		captureStdout(t, func() {
			installError = installForTarget(fsys, target, []string{"ankra-cli"}, skillsInstallOptions{force: true, InstallOptions: options}, map[string]string{})
		})
		if installError != nil {
			t.Fatalf("install: %v", installError)
		}
	}

	install(skills.InstallOptions{Rules: false, Workflows: false, Hooks: false})
	recorded, found, err := skills.RecordedInstallOptions(home, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if !found || recorded != (skills.InstallOptions{Rules: false, Workflows: false, Hooks: false}) {
		t.Fatalf("install must record --no-rules --no-workflows, got found=%v %+v", found, recorded)
	}

	install(skills.DefaultInstallOptions())
	recorded, found, err = skills.RecordedInstallOptions(home, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if !found || recorded != skills.DefaultInstallOptions() {
		t.Fatalf("a re-install must replace the record, got found=%v %+v", found, recorded)
	}

	// Removing named skills leaves the install in place, so the record
	// stays: the next upgrade must replay these options, not the defaults.
	var partialError error
	captureStdout(t, func() {
		partialError = uninstallForTarget(target, []string{"ankra-cli"}, false)
	})
	if partialError != nil {
		t.Fatalf("partial uninstall: %v", partialError)
	}
	if _, found, _ := skills.RecordedInstallOptions(home, "claude-code"); !found {
		t.Fatal("a partial uninstall must keep the recorded options")
	}

	var uninstallError error
	captureStdout(t, func() {
		uninstallError = uninstallForTarget(target, []string{"ankra-cli"}, true)
	})
	if uninstallError != nil {
		t.Fatalf("uninstall: %v", uninstallError)
	}
	if _, found, _ := skills.RecordedInstallOptions(home, "claude-code"); found {
		t.Fatal("a full uninstall must forget the recorded options")
	}
}

// TestRefreshInstalledSkillsWithNothingDetectedSaysSo pins the forced
// refresh after a detection failure: no banner for nobody, nothing run, the
// by-hand command instead.
func TestRefreshInstalledSkillsWithNothingDetectedSaysSo(t *testing.T) {
	original := runSkillsRefresh
	t.Cleanup(func() { runSkillsRefresh = original })
	runs := 0
	runSkillsRefresh = func(io.Writer, string, []string) error {
		runs++
		return nil
	}
	var out bytes.Buffer
	refreshInstalledSkills(&out, "/usr/local/bin/ankra", nil)
	if runs != 0 {
		t.Fatalf("nothing may run with no clients, got %d runs", runs)
	}
	if strings.Contains(out.String(), "Refreshing the Ankra agent skills for") {
		t.Fatalf("no refresh banner for nobody, got %q", out.String())
	}
	if !strings.Contains(out.String(), "/usr/local/bin/ankra skills install --force") {
		t.Fatalf("the by-hand command must be named, got %q", out.String())
	}
}

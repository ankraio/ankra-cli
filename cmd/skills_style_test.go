package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/skills"
)

func TestSkillsStyleIsPlainWhenNotATerminal(t *testing.T) {
	var buffer bytes.Buffer
	style := newSkillsStyle(&buffer)
	if style.isColor || style.width != 0 {
		t.Fatalf("a non-terminal writer must get plain, untruncated output, got %+v", style)
	}
	if got := style.green("ok"); got != "ok" {
		t.Errorf("plain style painted %q", got)
	}
}

func TestSkillsTableAlignsStyledCellsByTheirPlainWidth(t *testing.T) {
	var buffer bytes.Buffer
	style := skillsStyle{isColor: true}
	style.writeSkillsTable(&buffer, []string{"STATUS", "NAME"}, [][]skillsTableCell{
		{paintedCell("● installed", style.green), plainCell("claude-code")},
		{paintedCell("·", style.dim), plainCell("cursor")},
	})
	lines := strings.Split(strings.TrimRight(buffer.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header and two rows, got:\n%s", buffer.String())
	}
	want := len([]rune("● installed")) + 2
	for _, line := range lines[1:] {
		visible := []rune(stripANSI(line))
		column := 0
		for column < len(visible) && visible[column] != 'c' {
			column++
		}
		if column != want {
			t.Errorf("second column starts at %d in %q, want %d", column, string(visible), want)
		}
	}
}

func TestSkillsTableTruncatesTheLastColumnToTheTerminal(t *testing.T) {
	var buffer bytes.Buffer
	style := skillsStyle{width: 40}
	style.writeSkillsTable(&buffer, []string{"A", "B"}, [][]skillsTableCell{
		{plainCell("x"), plainCell(strings.Repeat("word ", 20))},
	})
	for _, line := range strings.Split(strings.TrimRight(buffer.String(), "\n"), "\n") {
		if length := len([]rune(line)); length > 40 {
			t.Errorf("line is %d columns, wider than the 40-column terminal: %q", length, line)
		}
	}
	if !strings.Contains(buffer.String(), "…") {
		t.Errorf("a truncated cell must end in an ellipsis:\n%s", buffer.String())
	}
}

func TestSkillSummaryTakesTheFirstClause(t *testing.T) {
	cases := map[string]string{
		"Run and govern Ankra's AI agents - choosing the model provider.":  "Run and govern Ankra's AI agents",
		"Encrypt Secrets with SOPS. Use when the user needs a secret.":     "Encrypt Secrets with SOPS",
		"A single sentence with no break.":                                 "A single sentence with no break",
		"Uses `ankra cluster encrypt`/`decrypt` - glob patterns. Use when": "Uses `ankra cluster encrypt`/`decrypt`",
	}
	for description, want := range cases {
		if got := skillSummary(description); got != want {
			t.Errorf("skillSummary(%q) = %q, want %q", description, got, want)
		}
	}
}

func TestDescribeSkillNamesCountsTheWholeBundle(t *testing.T) {
	if got := describeSkillNames([]string{"a", "b", "c"}, nil, "installed"); got != "3 installed" {
		t.Errorf("whole-bundle install = %q", got)
	}
	if got := describeSkillNames([]string{"ankra-cli"}, []string{"ankra-cli"}, "installed"); got != "ankra-cli installed" {
		t.Errorf("named install = %q", got)
	}
}

// Bare 'ankra skills' reports each assistant's install, and a client that
// only shares another client's skills directory must not be reported as
// installed.
func TestSkillsOverviewReportsInstalledAssistantsOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fsys, err := skills.EmbeddedFS()
	if err != nil {
		t.Fatal(err)
	}
	target, err := skills.ResolveTarget(clientNamed(t, "zed"), skills.ScopePersonal, home)
	if err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if installError := installForTarget(fsys, target, []string{"ankra-cli"}, skillsInstallOptions{InstallOptions: skills.DefaultInstallOptions()}, map[string]string{}); installError != nil {
			t.Fatalf("install: %v", installError)
		}
	})

	var buffer bytes.Buffer
	if err := writeSkillsOverview(&buffer); err != nil {
		t.Fatalf("overview: %v", err)
	}
	output := buffer.String()
	if !strings.Contains(output, "Zed") || !strings.Contains(output, "◐ 1/") {
		t.Errorf("expected Zed with a partial install:\n%s", output)
	}
	for _, sharer := range []string{"GitHub Copilot", "Windsurf", "Cline"} {
		if strings.Contains(output, sharer) {
			t.Errorf("%s shares Zed's directory but has no install of its own:\n%s", sharer, output)
		}
	}
	if !dirExists(filepath.Join(home, ".ankra", "skills", "ankra-cli")) {
		t.Fatal("the install under test did not land")
	}
}

func stripANSI(text string) string {
	var builder strings.Builder
	isEscape := false
	for _, character := range text {
		switch {
		case character == '\033':
			isEscape = true
		case isEscape && character == 'm':
			isEscape = false
		case !isEscape:
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func TestSkillsTableTruncatesAStyledLastColumnWithoutCuttingItsColour(t *testing.T) {
	var buffer bytes.Buffer
	style := skillsStyle{isColor: true, width: 40}
	style.writeSkillsTable(&buffer, []string{"A", "B"}, [][]skillsTableCell{
		{plainCell("x"), paintedCell(strings.Repeat("word ", 20), style.dim)},
	})
	lines := strings.Split(strings.TrimRight(buffer.String(), "\n"), "\n")
	row := lines[len(lines)-1]
	if visible := len([]rune(stripANSI(row))); visible > 40 {
		t.Errorf("styled row is %d columns, wider than the 40-column terminal: %q", visible, row)
	}
	if !strings.HasSuffix(row, "…"+ansiReset) {
		t.Errorf("the colour must wrap the truncated text, closing after the ellipsis: %q", row)
	}
}

// One assistant whose skills directory cannot be read must not take the
// whole overview down with it.
func TestSkillsOverviewShowsAnUnreadableAssistantInsteadOfFailing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "skills"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := writeSkillsOverview(&buffer); err != nil {
		t.Fatalf("an unreadable assistant must not fail the overview: %v", err)
	}
	if !strings.Contains(buffer.String(), "? unreadable") {
		t.Errorf("expected the Claude Code row to say it could not be read:\n%s", buffer.String())
	}
	if !strings.Contains(buffer.String(), "ankra skills install") {
		t.Errorf("the overview must still print its commands:\n%s", buffer.String())
	}
}

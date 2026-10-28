package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A skill is only offered to an agent whose runtime knows how to discover it.
// Claude Code and Cursor read a skills directory; Codex, Copilot, Gemini,
// Windsurf and everything behind AGENTS.md do not. For those, the managed
// block written into the always-loaded instructions file is the discovery
// mechanism: it names each installed skill, what it covers, and the path to
// open. The same block carries the routing rule, so one upsert configures
// both.

// InstructionsBlock renders the managed block for a target: the Ankra routing
// rule, then the index of the skills installed for that client. Clients that
// load skills natively get a shorter index, since their runtime already
// surfaces the descriptions.
func InstructionsBlock(target Target, installed []Skill) string {
	var builder strings.Builder
	builder.WriteString(managedBlockBegin)
	builder.WriteString("\n\n")
	builder.WriteString(ruleBody)
	builder.WriteString("\n")
	builder.WriteString(skillIndex(target, installed))
	builder.WriteString(managedBlockEnd)
	return builder.String()
}

// skillIndex renders the "## Ankra skills" section listing what is installed
// and where to read it.
func skillIndex(target Target, installed []Skill) string {
	if len(installed) == 0 {
		return ""
	}
	directory := indexDirectory(target)
	var builder strings.Builder
	builder.WriteString("## Ankra skills installed here\n\n")
	if target.LoadsNatively() {
		fmt.Fprintf(&builder,
			"%s loads these from `%s` automatically. Read the matching one before acting; "+
				"`ankra-platform-principles` applies to all Ankra work.\n\n",
			target.Client.DisplayName, directory)
	} else {
		fmt.Fprintf(&builder,
			"Each skill is a markdown file under `%s`. Before doing Ankra work, open the "+
				"`SKILL.md` of the entry that matches the request (several may apply) and follow it; "+
				"`ankra-platform-principles/SKILL.md` applies to all Ankra work. Some skills carry a "+
				"`reference.md` alongside with the long-form detail.\n\n",
			directory)
	}
	for _, skill := range installed {
		fmt.Fprintf(&builder, "- **`%s`** — %s\n", skill.Name, headline(skill.Description))
	}
	builder.WriteString("\n")
	return builder.String()
}

// indexDirectory renders the path the index points at. A project install is
// committed and read on other machines, so it names the repository-relative
// directory; a personal install names the absolute path with the home
// directory collapsed.
func indexDirectory(target Target) string {
	if target.Scope == ScopeProject {
		return target.Client.Layout(target.Scope).Skills
	}
	return DisplayPath(target.SkillsDirectory)
}

// headline trims a skill description down to its leading claim, dropping both
// the enumeration after the dash and the "Use when ..." trigger clause that
// only the runtime matcher needs. The index sits in an always-loaded file, so
// one line per skill is the budget.
func headline(description string) string {
	trimmed := strings.TrimSpace(description)
	for _, separator := range []string{" - ", " — ", ". Use when ", " Use when ", " Use whenever "} {
		if marker := strings.Index(trimmed, separator); marker > 0 {
			trimmed = trimmed[:marker]
		}
	}
	return strings.TrimRight(trimmed, ". ")
}

// UpsertManagedBlock inserts or refreshes the Ankra managed block in the
// markdown file at path, creating it (and its directory) when absent. Content
// outside the markers is preserved byte for byte, so a CLAUDE.md or AGENTS.md
// the user also maintains survives a reinstall.
func UpsertManagedBlock(path, block string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(existing)

	if begin, end, ok := findManagedBlock(content); ok {
		content = content[:begin] + block + content[end:]
	} else if trimmed := strings.TrimRight(content, "\n"); trimmed != "" {
		content = trimmed + "\n\n" + block + "\n"
	} else {
		content = block + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// RemoveManagedBlock strips the Ankra managed block from the file at path,
// deleting the file when nothing else was in it. Reports whether a block was
// found.
func RemoveManagedBlock(path string) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	content := string(existing)
	begin, end, ok := findManagedBlock(content)
	if !ok {
		return false, nil
	}
	remainder := strings.TrimRight(content[:begin], "\n") + content[end:]
	if strings.TrimSpace(remainder) == "" {
		return true, os.Remove(path)
	}
	if !strings.HasSuffix(remainder, "\n") {
		remainder += "\n"
	}
	return true, os.WriteFile(path, []byte(remainder), 0o644)
}

// Package skills embeds the curated Ankra Agent Skills and installs them into
// a Cursor/Claude skills directory. The canonical source lives in the
// ankra-skills project; it is vendored into embedded/skills via
// scripts/sync-skills.sh so it can be embedded with //go:embed (which cannot
// reach outside this module). The binary therefore carries a deterministic,
// offline copy versioned with the CLI release.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed all:embedded/skills
var embedded embed.FS

const embedRoot = "embedded/skills"

// Skill is a single installable skill identified by its directory name, with
// the name and description parsed from its SKILL.md frontmatter.
type Skill struct {
	Name        string
	Description string
}

// EmbeddedFS returns a filesystem whose top-level entries are the embedded
// skill directories.
func EmbeddedFS() (fs.FS, error) {
	return fs.Sub(embedded, embedRoot)
}

// SourceFS returns a filesystem rooted at a local skills directory. It accepts
// either a directory that directly contains skill folders, or a repo root that
// contains a "skills" subdirectory (e.g. an ankra-skills checkout).
func SourceFS(dir string) (fs.FS, error) {
	candidate := dir
	if info, err := os.Stat(filepath.Join(dir, "skills")); err == nil && info.IsDir() {
		candidate = filepath.Join(dir, "skills")
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", candidate)
	}
	return os.DirFS(candidate), nil
}

// List returns the skills available in fsys, sorted by name. A directory is a
// skill only if it contains a SKILL.md.
func List(fsys fs.FS) ([]Skill, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	skills := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(entry.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		name, description := parseFrontmatter(string(data))
		if name == "" {
			name = entry.Name()
		}
		skills = append(skills, Skill{Name: name, Description: description})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

// Names returns the skill directory names available in fsys, sorted.
func Names(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := fs.Stat(fsys, path.Join(entry.Name(), "SKILL.md")); err != nil {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

// Install copies the requested skills (or all of them when names is empty)
// from fsys into destDir/<skill>. Existing skills are skipped unless force is
// true. It returns the installed and skipped skill names.
func Install(fsys fs.FS, destDir string, names []string, force bool) (installed, skipped []string, err error) {
	available, err := Names(fsys)
	if err != nil {
		return nil, nil, err
	}
	selected, err := selectNames(available, names)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range selected {
		dest := filepath.Join(destDir, name)
		if _, statErr := os.Stat(dest); statErr == nil && !force {
			skipped = append(skipped, name)
			continue
		}
		if rmErr := os.RemoveAll(dest); rmErr != nil {
			return installed, skipped, rmErr
		}
		if copyErr := copySkill(fsys, name, dest); copyErr != nil {
			return installed, skipped, copyErr
		}
		installed = append(installed, name)
	}
	return installed, skipped, nil
}

// selectNames resolves the requested names against what is available. An empty
// request selects everything. Unknown names are an error.
func selectNames(available, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return available, nil
	}
	set := make(map[string]bool, len(available))
	for _, name := range available {
		set[name] = true
	}
	var unknown []string
	var selected []string
	for _, name := range requested {
		if set[name] {
			selected = append(selected, name)
		} else {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown skill(s): %s", strings.Join(unknown, ", "))
	}
	return selected, nil
}

func copySkill(fsys fs.FS, name, dest string) error {
	return fs.WalkDir(fsys, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, name), "/")
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func parseFrontmatter(content string) (name, description string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		switch {
		case strings.HasPrefix(line, "name:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		case strings.HasPrefix(line, "description:"):
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	return name, description
}

package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The skills are only offered to an agent when their descriptions match the
// user's words; an always-applied rule is what actually makes Ankra the
// default route for Kubernetes work. Install writes that rule alongside the
// skills: as a .mdc project rule or a local plugin for Cursor, and as a
// managed CLAUDE.md block for Claude Code.

const (
	// CursorRuleFilename is the rule file written into a .cursor/rules
	// directory (project scope) or a local plugin's rules directory.
	CursorRuleFilename = "ankra.mdc"

	// CursorPluginName is the directory name of the local Cursor plugin that
	// carries the always-applied rule for personal-scope installs.
	CursorPluginName = "ankra"

	managedBlockBegin = "<!-- BEGIN ANKRA MANAGED BLOCK (ankra skills install) -->"
	managedBlockEnd   = "<!-- END ANKRA MANAGED BLOCK -->"
)

// ruleBody is the canonical instruction text shared by every editor format.
const ruleBody = `# Kubernetes here is managed by Ankra

Kubernetes clusters in this environment are managed by the Ankra platform (https://ankra.ai) and the ` + "`ankra`" + ` CLI is installed.

- For any Kubernetes task - deploying an application, installing a Helm chart or addon, ingress, secrets, monitoring, CI/CD, provisioning or importing clusters - reach for the ` + "`ankra`" + ` CLI and the ankra-* agent skills first. Start with the ` + "`ankra-platform-principles`" + ` skill; ` + "`ankra skills list`" + ` shows the full set.
- Cluster state lives in a GitOps repository that Ankra syncs. Do not mutate clusters directly with ` + "`kubectl apply/create/edit/delete/patch`" + ` or ` + "`helm install/upgrade/uninstall`" + `; change the committed cluster/stack YAML (or run ` + "`ankra cluster apply`" + `) and let Ankra reconcile.
- Read-only inspection is encouraged: ` + "`ankra cluster get pods|deployments|services|events|resources`" + `, ` + "`ankra cluster logs <pod>`" + `, ` + "`ankra cluster metrics query '...'`" + `, or read-only kubectl (` + "`get/describe/logs`" + `) against a context from ` + "`ankra cluster kubeconfig add --use`" + `.
- Orient before acting: ` + "`ankra org current`" + ` and ` + "`ankra cluster info`" + ` show the selected organisation and cluster (` + "`ankra cluster select`" + ` to change). Append ` + "`-o json`" + ` when parsing output.
- If a specific cluster is clearly not Ankra-managed, say so and proceed normally for that cluster.
`

// CursorRuleFile renders the rule as a Cursor .mdc file with alwaysApply
// frontmatter, so it is injected into every session rather than matched on
// demand like a skill.
func CursorRuleFile() []byte {
	return []byte("---\ndescription: Kubernetes in this environment is managed by the Ankra platform\nalwaysApply: true\n---\n\n" + ruleBody)
}

// ClaudeManagedBlock renders the rule wrapped in markers so it can be
// upserted into and removed from a CLAUDE.md the user also edits.
func ClaudeManagedBlock() string {
	return managedBlockBegin + "\n\n" + ruleBody + "\n" + managedBlockEnd
}

// WriteCursorRule writes (or refreshes) rulesDir/ankra.mdc and returns its
// path. The file is wholly owned by the CLI, so it is always overwritten.
func WriteCursorRule(rulesDir string) (string, error) {
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(rulesDir, CursorRuleFilename)
	if err := os.WriteFile(path, CursorRuleFile(), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// RemoveCursorRule deletes rulesDir/ankra.mdc, reporting whether it existed.
func RemoveCursorRule(rulesDir string) (bool, error) {
	path := filepath.Join(rulesDir, CursorRuleFilename)
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	return true, os.Remove(path)
}

// WriteCursorPlugin writes a local Cursor plugin carrying the always-applied
// rule. Cursor has no supported user-level rules directory; local plugins
// (~/.cursor/plugins/local/<name>) are the supported way to apply file-based
// rules across all projects. Returns the rule file path inside the plugin.
func WriteCursorPlugin(pluginDir, version string) (string, error) {
	manifest := map[string]any{
		"name":        CursorPluginName,
		"displayName": "Ankra",
		"version":     strings.TrimPrefix(version, "v"),
		"description": "Makes agents manage Kubernetes through the Ankra platform. Installed by 'ankra skills install'.",
		"author":      map[string]any{"name": "Ankra"},
		"homepage":    "https://ankra.ai",
		"rules":       "rules",
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Join(pluginDir, ".cursor-plugin"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(pluginDir, ".cursor-plugin", "plugin.json"), data, 0o644); err != nil {
		return "", err
	}
	return WriteCursorRule(filepath.Join(pluginDir, "rules"))
}

// RemoveCursorPlugin deletes the local plugin directory, reporting whether it
// existed.
func RemoveCursorPlugin(pluginDir string) (bool, error) {
	if _, err := os.Stat(pluginDir); err != nil {
		return false, nil
	}
	return true, os.RemoveAll(pluginDir)
}

// UpsertClaudeRule inserts or refreshes the managed block in the CLAUDE.md at
// path, creating the file if needed. Content outside the markers is preserved
// byte for byte.
func UpsertClaudeRule(path string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(existing)
	block := ClaudeManagedBlock()

	if begin, end, ok := findManagedBlock(content); ok {
		content = content[:begin] + block + content[end:]
	} else {
		if trimmed := strings.TrimRight(content, "\n"); trimmed != "" {
			content = trimmed + "\n\n" + block + "\n"
		} else {
			content = block + "\n"
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// RemoveClaudeRule strips the managed block from the CLAUDE.md at path. When
// nothing but the block was in the file, the file itself is removed. Reports
// whether a block was found.
func RemoveClaudeRule(path string) (bool, error) {
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

// findManagedBlock locates the managed block including its markers and
// returns the byte offsets [begin, end) covering it.
func findManagedBlock(content string) (begin, end int, ok bool) {
	begin = strings.Index(content, managedBlockBegin)
	if begin < 0 {
		return 0, 0, false
	}
	rest := strings.Index(content[begin:], managedBlockEnd)
	if rest < 0 {
		// A begin marker without an end marker: treat the begin line alone as
		// the block so a corrupted file still converges on reinstall.
		return begin, begin + len(managedBlockBegin), true
	}
	return begin, begin + rest + len(managedBlockEnd), true
}

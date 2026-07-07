package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCursorRuleFileIsAlwaysApplied(t *testing.T) {
	content := string(CursorRuleFile())
	if !strings.HasPrefix(content, "---\n") {
		t.Fatal("expected frontmatter")
	}
	if !strings.Contains(content, "alwaysApply: true") {
		t.Error("rule must be always applied; matching-on-demand is what skills already do")
	}
	for _, want := range []string{"ankra cluster apply", "kubectl", "GitOps"} {
		if !strings.Contains(content, want) {
			t.Errorf("rule content missing %q", want)
		}
	}
}

func TestWriteAndRemoveCursorRule(t *testing.T) {
	rulesDir := filepath.Join(t.TempDir(), ".cursor", "rules")
	path, err := WriteCursorRule(rulesDir)
	if err != nil {
		t.Fatalf("WriteCursorRule: %v", err)
	}
	if filepath.Base(path) != CursorRuleFilename {
		t.Fatalf("unexpected rule path %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("rule not written: %v", err)
	}

	// Overwrite is idempotent.
	if _, err := WriteCursorRule(rulesDir); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	found, err := RemoveCursorRule(rulesDir)
	if err != nil || !found {
		t.Fatalf("RemoveCursorRule found=%v err=%v", found, err)
	}
	found, err = RemoveCursorRule(rulesDir)
	if err != nil || found {
		t.Fatalf("second RemoveCursorRule found=%v err=%v", found, err)
	}
}

func TestWriteCursorPluginManifestAndRule(t *testing.T) {
	pluginDir := filepath.Join(t.TempDir(), "plugins", "local", "ankra")
	rulePath, err := WriteCursorPlugin(pluginDir, "v1.2.3")
	if err != nil {
		t.Fatalf("WriteCursorPlugin: %v", err)
	}
	if rulePath != filepath.Join(pluginDir, "rules", CursorRuleFilename) {
		t.Fatalf("unexpected rule path %s", rulePath)
	}

	data, err := os.ReadFile(filepath.Join(pluginDir, ".cursor-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if manifest["name"] != CursorPluginName {
		t.Errorf("manifest name = %v", manifest["name"])
	}
	if manifest["rules"] != "rules" {
		t.Errorf("manifest must point Cursor at the rules directory, got %v", manifest["rules"])
	}
	if manifest["version"] != "1.2.3" {
		t.Errorf("expected v prefix stripped, got %v", manifest["version"])
	}

	found, err := RemoveCursorPlugin(pluginDir)
	if err != nil || !found {
		t.Fatalf("RemoveCursorPlugin found=%v err=%v", found, err)
	}
	if _, err := os.Stat(pluginDir); !os.IsNotExist(err) {
		t.Error("plugin directory still exists")
	}
}

func TestUpsertClaudeRuleCreatesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "CLAUDE.md")
	if err := UpsertClaudeRule(path); err != nil {
		t.Fatalf("UpsertClaudeRule: %v", err)
	}
	first, _ := os.ReadFile(path)
	if err := UpsertClaudeRule(path); err != nil {
		t.Fatalf("second UpsertClaudeRule: %v", err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Error("upsert is not idempotent")
	}
	if got := strings.Count(string(second), managedBlockBegin); got != 1 {
		t.Errorf("expected exactly one managed block, got %d", got)
	}
}

func TestUpsertClaudeRulePreservesUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	userContent := "# My project\n\nAlways use tabs.\n"
	if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpsertClaudeRule(path); err != nil {
		t.Fatalf("UpsertClaudeRule: %v", err)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "Always use tabs.") {
		t.Error("user content lost on upsert")
	}
	if !strings.Contains(string(content), managedBlockBegin) {
		t.Error("managed block not appended")
	}

	// Refresh replaces the block in place, still keeping user content.
	if err := UpsertClaudeRule(path); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(path)
	if !strings.Contains(string(content), "Always use tabs.") {
		t.Error("user content lost on refresh")
	}
	if got := strings.Count(string(content), managedBlockBegin); got != 1 {
		t.Errorf("expected exactly one managed block after refresh, got %d", got)
	}
}

func TestRemoveClaudeRule(t *testing.T) {
	dir := t.TempDir()

	// Block plus user content: block goes, content stays.
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpsertClaudeRule(path); err != nil {
		t.Fatal(err)
	}
	found, err := RemoveClaudeRule(path)
	if err != nil || !found {
		t.Fatalf("RemoveClaudeRule found=%v err=%v", found, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file should survive when user content remains: %v", err)
	}
	if !strings.Contains(string(content), "keep me") || strings.Contains(string(content), managedBlockBegin) {
		t.Errorf("unexpected content after removal: %q", string(content))
	}

	// Block only: the whole file goes.
	solo := filepath.Join(dir, "solo", "CLAUDE.md")
	if err := UpsertClaudeRule(solo); err != nil {
		t.Fatal(err)
	}
	found, err = RemoveClaudeRule(solo)
	if err != nil || !found {
		t.Fatalf("RemoveClaudeRule(solo) found=%v err=%v", found, err)
	}
	if _, err := os.Stat(solo); !os.IsNotExist(err) {
		t.Error("file with only the managed block should be removed")
	}

	// Nothing installed: not found, no error.
	found, err = RemoveClaudeRule(filepath.Join(dir, "absent", "CLAUDE.md"))
	if err != nil || found {
		t.Fatalf("RemoveClaudeRule(absent) found=%v err=%v", found, err)
	}
}

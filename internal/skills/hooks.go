package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent hooks are the enforcement layer: rules and skills steer the agent,
// but a hook deterministically intercepts kubectl/helm mutations at the
// moment the agent is about to bypass Ankra, and asks for confirmation with
// a redirect message. The hook command is `ankra skills guard`, so the logic
// stays in this binary and the installed config is a one-liner.

// guardMarker identifies hook entries owned by the CLI inside shared config
// files, so install/uninstall converge without touching user entries.
const guardMarker = "skills guard"

// GuardReason explains an intercepted mutation to the user and the agent.
const GuardReason = "This cluster is managed by Ankra: cluster changes should go through the GitOps repository or 'ankra cluster apply' (see the ankra-* skills) so they stay reconciled and traceable. Read-only inspection (get/describe/logs) needs no approval. Approve only if this cluster is not Ankra-managed or a direct mutation is genuinely intended."

// GuardCommandLine renders the shell command hooks invoke. exePath is the
// installed ankra binary; quoted in case the path contains spaces.
func GuardCommandLine(exePath, format string) string {
	exe := exePath
	if exe == "" {
		exe = "ankra"
	}
	if strings.ContainsAny(exe, " \t") {
		exe = `"` + exe + `"`
	}
	return fmt.Sprintf("%s skills guard --format %s", exe, format)
}

// ClassifyShellCommand reports whether a shell command mutates a Kubernetes
// cluster out-of-band (kubectl apply/delete/..., helm install/upgrade/...).
// It scans every kubectl/helm invocation in compound commands, treats
// --dry-run as read-only, and fails open: anything it cannot parse is not
// flagged.
func ClassifyShellCommand(command string) bool {
	tokens := strings.FieldsFunc(command, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ';', '|', '&', '(', ')', '`':
			return true
		}
		return false
	})
	for _, t := range tokens {
		if strings.HasPrefix(t, "--dry-run") {
			return false
		}
	}
	for i, t := range tokens {
		switch commandName(t) {
		case "kubectl":
			if kubectlVerbMutates(tokens[i+1:]) {
				return true
			}
		case "helm":
			if helmVerbMutates(tokens[i+1:]) {
				return true
			}
		}
	}
	return false
}

// commandName reduces a token to its binary name ("/usr/local/bin/kubectl"
// -> "kubectl").
func commandName(token string) string {
	if idx := strings.LastIndex(token, "/"); idx >= 0 {
		return token[idx+1:]
	}
	return token
}

// valueFlags are flags whose value follows as a separate token and must be
// skipped when looking for the subcommand verb.
var valueFlags = map[string]bool{
	"-n": true, "--namespace": true, "--context": true, "--kube-context": true,
	"--kubeconfig": true, "--cluster": true, "--user": true, "-s": true, "--server": true,
}

// firstVerb returns the first non-flag token, skipping flags and the values
// of flags known to take one.
func firstVerb(tokens []string) (verb string, rest []string) {
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if strings.HasPrefix(t, "-") {
			if valueFlags[t] && i+1 < len(tokens) {
				i++
			}
			continue
		}
		return t, tokens[i+1:]
	}
	return "", nil
}

var kubectlMutatingVerbs = map[string]bool{
	"apply": true, "create": true, "delete": true, "edit": true, "patch": true,
	"replace": true, "scale": true, "autoscale": true, "annotate": true,
	"label": true, "taint": true, "cordon": true, "uncordon": true,
	"drain": true, "expose": true, "run": true, "set": true, "cp": true,
	"certificate": true,
}

func kubectlVerbMutates(tokens []string) bool {
	verb, rest := firstVerb(tokens)
	if verb == "rollout" {
		sub, _ := firstVerb(rest)
		return sub != "status" && sub != "history" && sub != ""
	}
	return kubectlMutatingVerbs[verb]
}

var helmMutatingVerbs = map[string]bool{
	"install": true, "upgrade": true, "uninstall": true, "delete": true, "rollback": true,
}

func helmVerbMutates(tokens []string) bool {
	verb, _ := firstVerb(tokens)
	return helmMutatingVerbs[verb]
}

// GuardRespond consumes a hook event (JSON on stdin) and returns the response
// JSON for the given format ("cursor" or "claude"). It never returns an error
// for malformed input: the guard fails open with a no-objection response.
func GuardRespond(format string, input []byte) ([]byte, error) {
	var event struct {
		Command   string `json:"command"` // Cursor beforeShellExecution
		ToolInput struct {
			Command string `json:"command"` // Claude Code PreToolUse (Bash)
		} `json:"tool_input"`
	}
	_ = json.Unmarshal(input, &event)
	command := event.Command
	if command == "" {
		command = event.ToolInput.Command
	}
	mutating := command != "" && ClassifyShellCommand(command)

	switch format {
	case "cursor":
		if !mutating {
			return json.Marshal(map[string]any{"permission": "allow"})
		}
		return json.Marshal(map[string]any{
			"permission":    "ask",
			"user_message":  "Ankra manages this cluster: prefer the GitOps repo or 'ankra cluster apply' over direct kubectl/helm mutations.",
			"agent_message": GuardReason,
		})
	case "claude":
		if !mutating {
			// No decision: Claude Code's normal permission flow applies.
			return []byte("{}"), nil
		}
		return json.Marshal(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":            "PreToolUse",
				"permissionDecision":       "ask",
				"permissionDecisionReason": GuardReason,
			},
		})
	default:
		return nil, fmt.Errorf("unsupported guard format %q (expected cursor or claude)", format)
	}
}

// UpsertCursorHook merges the guard into a Cursor hooks.json, preserving
// unrelated hooks. Existing guard entries are replaced.
func UpsertCursorHook(path, guardCommand string) error {
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	if root == nil {
		root = map[string]any{}
	}
	root["version"] = 1
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	entries, _ := hooks["beforeShellExecution"].([]any)
	entries = removeEntriesWithCommand(entries, guardMarker)
	entries = append(entries, map[string]any{
		"command": guardCommand,
		"matcher": `\bkubectl\b|\bhelm\b`,
	})
	hooks["beforeShellExecution"] = entries
	root["hooks"] = hooks
	return writeJSONObject(path, root)
}

// RemoveCursorHook strips guard entries from a Cursor hooks.json, reporting
// whether any were present. A file left with no hooks at all is deleted.
func RemoveCursorHook(path string) (bool, error) {
	root, err := readJSONObject(path)
	if err != nil || root == nil {
		return false, err
	}
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks["beforeShellExecution"].([]any)
	filtered := removeEntriesWithCommand(entries, guardMarker)
	if len(filtered) == len(entries) {
		return false, nil
	}
	if len(filtered) == 0 {
		delete(hooks, "beforeShellExecution")
	} else {
		hooks["beforeShellExecution"] = filtered
	}
	if len(hooks) == 0 {
		if onlyVersionAndHooks(root) {
			return true, os.Remove(path)
		}
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
	return true, writeJSONObject(path, root)
}

// UpsertClaudeHook merges the guard into a Claude Code settings.json as a
// PreToolUse hook on the Bash tool, preserving every other setting.
func UpsertClaudeHook(path, guardCommand string) error {
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	if root == nil {
		root = map[string]any{}
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	groups, _ := hooks["PreToolUse"].([]any)
	groups = removeClaudeGuardGroups(groups)
	groups = append(groups, map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			map[string]any{"type": "command", "command": guardCommand},
		},
	})
	hooks["PreToolUse"] = groups
	root["hooks"] = hooks
	return writeJSONObject(path, root)
}

// RemoveClaudeHook strips guard handlers from a Claude Code settings.json,
// reporting whether any were present.
func RemoveClaudeHook(path string) (bool, error) {
	root, err := readJSONObject(path)
	if err != nil || root == nil {
		return false, err
	}
	hooks, _ := root["hooks"].(map[string]any)
	groups, _ := hooks["PreToolUse"].([]any)
	filtered := removeClaudeGuardGroups(groups)
	if len(filtered) == len(groups) {
		return false, nil
	}
	if len(filtered) == 0 {
		delete(hooks, "PreToolUse")
	} else {
		hooks["PreToolUse"] = filtered
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
	if len(root) == 0 {
		return true, os.Remove(path)
	}
	return true, writeJSONObject(path, root)
}

// removeEntriesWithCommand filters flat hook entries whose command contains
// the marker.
func removeEntriesWithCommand(entries []any, marker string) []any {
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if ok {
			if cmd, _ := entry["command"].(string); strings.Contains(cmd, marker) {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// removeClaudeGuardGroups drops guard handlers from Claude matcher groups,
// and whole groups that end up with no handlers.
func removeClaudeGuardGroups(groups []any) []any {
	out := make([]any, 0, len(groups))
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			out = append(out, g)
			continue
		}
		handlers, _ := group["hooks"].([]any)
		kept := removeEntriesWithCommand(handlers, guardMarker)
		if len(kept) == 0 && len(handlers) > 0 && len(kept) != len(handlers) {
			continue
		}
		if len(kept) != len(handlers) {
			group["hooks"] = kept
		}
		out = append(out, group)
	}
	return out
}

// onlyVersionAndHooks reports whether the config object holds nothing beyond
// the keys this CLI writes, i.e. deleting the file loses nothing.
func onlyVersionAndHooks(root map[string]any) bool {
	for key := range root {
		if key != "version" && key != "hooks" {
			return false
		}
	}
	hooks, _ := root["hooks"].(map[string]any)
	return len(hooks) == 0
}

// readJSONObject loads a JSON object file, returning nil without error when
// the file does not exist.
func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%w); fix or remove it and retry", path, err)
	}
	return root, nil
}

func writeJSONObject(path string, root map[string]any) error {
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyShellCommand(t *testing.T) {
	mutating := []string{
		"kubectl apply -f deploy.yaml",
		"kubectl -n staging apply -f deploy.yaml",
		"kubectl --namespace staging delete pod web-0",
		"kubectl delete -f -",
		"kubectl edit deployment web",
		"kubectl patch svc web -p '{}'",
		"kubectl scale --replicas=3 deployment/web",
		"kubectl rollout restart deployment/web",
		"kubectl rollout undo deployment/web",
		"kubectl label nodes n1 disk=ssd",
		"kubectl drain node-1",
		"kubectl set image deployment/web web=nginx:1.27",
		"/usr/local/bin/kubectl apply -k overlays/prod",
		"echo done && kubectl delete ns scratch",
		"kubectl get pods | kubectl delete -f -",
		"helm install web ./chart",
		"helm upgrade web ./chart -f values.yaml",
		"helm -n infra upgrade --install ingress ingress-nginx/ingress-nginx",
		"helm uninstall web",
		"helm rollback web 3",
		"helm delete web",
	}
	for _, cmd := range mutating {
		if !ClassifyShellCommand(cmd) {
			t.Errorf("expected mutating: %q", cmd)
		}
	}

	readOnly := []string{
		"kubectl get pods",
		"kubectl get pods -n kube-system -o json",
		"kubectl describe pod web-0",
		"kubectl logs web-0 --tail 100",
		"kubectl top nodes",
		"kubectl rollout status deployment/web",
		"kubectl rollout history deployment/web",
		"kubectl diff -f deploy.yaml",
		"kubectl apply -f deploy.yaml --dry-run=server",
		"kubectl apply -f deploy.yaml --dry-run=client -o yaml",
		"helm list -A",
		"helm template web ./chart",
		"helm status web",
		"helm upgrade web ./chart --dry-run",
		"helm repo update",
		"ankra cluster apply -f cluster.yaml",
		"git push origin main",
		"kubectl describe pod x | grep -i image",
		"kubectl explain deployment.spec",
		"kubectl auth can-i create pods",
		"",
	}
	for _, cmd := range readOnly {
		if ClassifyShellCommand(cmd) {
			t.Errorf("expected read-only/unflagged: %q", cmd)
		}
	}
}

func TestGuardRespondCursor(t *testing.T) {
	out, err := GuardRespond("cursor", []byte(`{"command":"kubectl get pods"}`))
	if err != nil {
		t.Fatal(err)
	}
	var allow map[string]any
	if err := json.Unmarshal(out, &allow); err != nil {
		t.Fatal(err)
	}
	if allow["permission"] != "allow" {
		t.Errorf("expected allow, got %v", allow)
	}

	out, err = GuardRespond("cursor", []byte(`{"command":"kubectl apply -f x.yaml"}`))
	if err != nil {
		t.Fatal(err)
	}
	var ask map[string]any
	if err := json.Unmarshal(out, &ask); err != nil {
		t.Fatal(err)
	}
	if ask["permission"] != "ask" {
		t.Errorf("expected ask, got %v", ask)
	}
	if msg, _ := ask["agent_message"].(string); !strings.Contains(msg, "ankra cluster apply") {
		t.Errorf("agent_message should redirect to the Ankra workflow, got %q", msg)
	}
}

func TestGuardRespondClaude(t *testing.T) {
	// Claude events carry the command in tool_input; no opinion must be an
	// empty object so the normal permission flow is untouched.
	out, err := GuardRespond("claude", []byte(`{"tool_name":"Bash","tool_input":{"command":"kubectl get pods"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "{}" {
		t.Errorf("expected no-decision {}, got %s", out)
	}

	out, err = GuardRespond("claude", []byte(`{"tool_name":"Bash","tool_input":{"command":"helm upgrade web ./chart"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var decision struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &decision); err != nil {
		t.Fatal(err)
	}
	if decision.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q", decision.HookSpecificOutput.HookEventName)
	}
	if decision.HookSpecificOutput.PermissionDecision != "ask" {
		t.Errorf("permissionDecision = %q", decision.HookSpecificOutput.PermissionDecision)
	}
	if decision.HookSpecificOutput.PermissionDecisionReason == "" {
		t.Error("expected a reason")
	}
}

func TestGuardRespondFailsOpenAndRejectsUnknownFormat(t *testing.T) {
	out, err := GuardRespond("cursor", []byte("not json"))
	if err != nil {
		t.Fatalf("malformed input must fail open: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil || v["permission"] != "allow" {
		t.Errorf("expected allow on malformed input, got %s", out)
	}

	if _, err := GuardRespond("zed", []byte("{}")); err == nil {
		t.Error("expected error for unknown format")
	}
}

func TestGuardCommandLine(t *testing.T) {
	if got := GuardCommandLine("/usr/local/bin/ankra", "cursor"); got != "/usr/local/bin/ankra skills guard --format cursor" {
		t.Errorf("unexpected command line %q", got)
	}
	if got := GuardCommandLine("/Users/x y/ankra", "claude"); got != `"/Users/x y/ankra" skills guard --format claude` {
		t.Errorf("expected quoted path, got %q", got)
	}
	if got := GuardCommandLine("", "cursor"); got != "ankra skills guard --format cursor" {
		t.Errorf("expected PATH fallback, got %q", got)
	}
}

func TestUpsertAndRemoveCursorHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cursor", "hooks.json")
	guard := "/usr/local/bin/ankra skills guard --format cursor"

	if err := UpsertCursorHook(path, guard); err != nil {
		t.Fatalf("UpsertCursorHook: %v", err)
	}
	// Idempotent: a second upsert must not duplicate the entry.
	if err := UpsertCursorHook(path, guard); err != nil {
		t.Fatal(err)
	}
	root := readJSONFile(t, path)
	if root["version"] != float64(1) {
		t.Errorf("version = %v", root["version"])
	}
	entries := root["hooks"].(map[string]any)["beforeShellExecution"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected 1 guard entry, got %d", len(entries))
	}
	entry := entries[0].(map[string]any)
	if entry["command"] != guard {
		t.Errorf("command = %v", entry["command"])
	}
	if matcher, _ := entry["matcher"].(string); !strings.Contains(matcher, "kubectl") {
		t.Errorf("matcher should narrow to kubectl/helm, got %q", matcher)
	}

	found, err := RemoveCursorHook(path)
	if err != nil || !found {
		t.Fatalf("RemoveCursorHook found=%v err=%v", found, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("hooks.json holding only the guard should be deleted")
	}
}

func TestCursorHookPreservesForeignEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	existing := `{
  "version": 1,
  "hooks": {
    "beforeShellExecution": [{"command": "./hooks/audit.sh"}],
    "afterFileEdit": [{"command": "./hooks/format.sh"}]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpsertCursorHook(path, "ankra skills guard --format cursor"); err != nil {
		t.Fatal(err)
	}
	root := readJSONFile(t, path)
	hooks := root["hooks"].(map[string]any)
	if entries := hooks["beforeShellExecution"].([]any); len(entries) != 2 {
		t.Fatalf("expected audit + guard, got %d entries", len(entries))
	}
	if _, ok := hooks["afterFileEdit"]; !ok {
		t.Error("unrelated hook event was dropped")
	}

	found, err := RemoveCursorHook(path)
	if err != nil || !found {
		t.Fatalf("RemoveCursorHook found=%v err=%v", found, err)
	}
	root = readJSONFile(t, path)
	hooks = root["hooks"].(map[string]any)
	if entries := hooks["beforeShellExecution"].([]any); len(entries) != 1 {
		t.Errorf("foreign beforeShellExecution entry should survive, got %v", entries)
	}
	if _, ok := hooks["afterFileEdit"]; !ok {
		t.Error("unrelated hook event was dropped on removal")
	}
}

func TestUpsertAndRemoveClaudeHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	guard := "/usr/local/bin/ankra skills guard --format claude"

	if err := UpsertClaudeHook(path, guard); err != nil {
		t.Fatalf("UpsertClaudeHook: %v", err)
	}
	if err := UpsertClaudeHook(path, guard); err != nil {
		t.Fatal(err)
	}
	root := readJSONFile(t, path)
	groups := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(groups) != 1 {
		t.Fatalf("expected 1 matcher group, got %d", len(groups))
	}
	group := groups[0].(map[string]any)
	if group["matcher"] != "Bash" {
		t.Errorf("matcher = %v", group["matcher"])
	}
	handlers := group["hooks"].([]any)
	if len(handlers) != 1 || handlers[0].(map[string]any)["command"] != guard {
		t.Errorf("unexpected handlers %v", handlers)
	}

	found, err := RemoveClaudeHook(path)
	if err != nil || !found {
		t.Fatalf("RemoveClaudeHook found=%v err=%v", found, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("settings.json holding only the guard should be deleted")
	}
}

func TestClaudeHookPreservesForeignSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	existing := `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "./mine.sh"}]}
    ]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpsertClaudeHook(path, "ankra skills guard --format claude"); err != nil {
		t.Fatal(err)
	}
	root := readJSONFile(t, path)
	if root["model"] != "opus" {
		t.Error("unrelated setting was dropped")
	}
	if groups := root["hooks"].(map[string]any)["PreToolUse"].([]any); len(groups) != 2 {
		t.Fatalf("expected user group + guard group, got %d", len(groups))
	}

	found, err := RemoveClaudeHook(path)
	if err != nil || !found {
		t.Fatalf("RemoveClaudeHook found=%v err=%v", found, err)
	}
	root = readJSONFile(t, path)
	if root["model"] != "opus" {
		t.Error("unrelated setting was dropped on removal")
	}
	groups := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"] != "./mine.sh" {
		t.Errorf("foreign PreToolUse group should survive, got %v", groups)
	}
}

func TestRemoveHooksWhenNothingInstalled(t *testing.T) {
	dir := t.TempDir()
	if found, err := RemoveCursorHook(filepath.Join(dir, "hooks.json")); err != nil || found {
		t.Fatalf("RemoveCursorHook on absent file found=%v err=%v", found, err)
	}
	if found, err := RemoveClaudeHook(filepath.Join(dir, "settings.json")); err != nil || found {
		t.Fatalf("RemoveClaudeHook on absent file found=%v err=%v", found, err)
	}
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	return root
}

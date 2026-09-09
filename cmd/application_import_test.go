package cmd

// `application import claude-design`: the export on disk becomes the
// platform's import request, and the platform's answer becomes the command's
// report.

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func resetApplicationImportFlags(t *testing.T) {
	t.Helper()
	for _, applicationCommand := range rootCmd.Commands() {
		if applicationCommand.Name() != "application" {
			continue
		}
		for _, command := range applicationCommand.Commands() {
			if command.Name() != "import" {
				continue
			}
			for _, subcommand := range command.Commands() {
				resetTreeFlags(t, subcommand)
			}
			return
		}
	}
}

func writeClaudeDesignExport(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "Neighborly export")
	if mkdirError := os.MkdirAll(directory, 0o755); mkdirError != nil {
		t.Fatal(mkdirError)
	}
	for name, content := range map[string]string{
		"Main.dc.html": "<!doctype html><html><head><script src=\"./support.js\"></script></head><body><x-dc><h1>Hi</h1></x-dc></body></html>",
		"canvas.json":  `{"artboards":[{"file":"Main.dc.html","w":1440,"h":900}]}`,
		"logo.png":     "png-bytes",
		".DS_Store":    "junk",
	} {
		if writeError := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); writeError != nil {
			t.Fatal(writeError)
		}
	}
	return directory
}

// serveClaudeDesignImport answers the credential listing and the import
// route, keeping the import body for the test to inspect.
func serveClaudeDesignImport(t *testing.T, importResponse string) func() map[string]any {
	t.Helper()
	var mutex sync.Mutex
	var importBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		if strings.Contains(request.URL.Path, "/credentials") {
			_, _ = responseWriter.Write([]byte(`[{"id":"credential-acme","name":"github-acme","provider":"github",` +
				`"available":true,"account_login":"acme","created_at":"2026-09-01T00:00:00Z"}]`))
			return
		}
		if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/applications/imports/claude-design") {
			bodyBytes, _ := io.ReadAll(request.Body)
			mutex.Lock()
			_ = json.Unmarshal(bodyBytes, &importBody)
			mutex.Unlock()
			_, _ = responseWriter.Write([]byte(importResponse))
			return
		}
		responseWriter.WriteHeader(http.StatusNotFound)
	}))
	useTestClient(t, server.URL)
	t.Cleanup(server.Close)
	return func() map[string]any {
		mutex.Lock()
		defer mutex.Unlock()
		return importBody
	}
}

const importedNeighborly = `{"id":"application-id","errors":[],` +
	`"repository":{"owner":"acme","name":"neighborly-export","web_url":"https://github.com/acme/neighborly-export",` +
	`"default_branch":"main","commit_sha":"abc123","reused":false},` +
	`"pages":[{"artboard":"Main.dc.html","path":"index.html","title":"Home","dynamic":false},` +
	`{"artboard":"Prices.dc.html","path":"prices.html","title":"Prices","dynamic":true}],` +
	`"warnings":[{"artboard":"Prices.dc.html","kind":"dynamic_artboard","message":"Prices.dc.html uses template logic"}],` +
	`"notes":[]}`

func TestApplicationImportClaudeDesignSendsTheExportAndReportsTheResult(t *testing.T) {
	directory := writeClaudeDesignExport(t)
	resetApplicationImportFlags(t)
	t.Cleanup(func() { resetApplicationImportFlags(t) })
	importBody := serveClaudeDesignImport(t, importedNeighborly)

	output, executeError := executeCommand("application", "import", "claude-design", directory,
		"--source-url", "https://claude.ai/design/p/abc")
	if executeError != nil {
		t.Fatalf("application import claude-design = %v\n%s", executeError, output)
	}

	body := importBody()
	if body["name"] != "neighborly-export" {
		t.Errorf("name should derive from the directory: %v", body["name"])
	}
	if body["app_repo_credential_name"] != "github-acme" || body["app_repo_owner"] != "acme" {
		t.Errorf("credential and owner should come from the single GitHub credential: %v", body)
	}
	if body["visibility"] != "private" || body["source_url"] != "https://claude.ai/design/p/abc" {
		t.Errorf("visibility/source_url = %v / %v", body["visibility"], body["source_url"])
	}
	files, _ := body["files"].([]any)
	if len(files) != 3 {
		t.Fatalf("expected the three export files (dotfiles skipped), got %v", files)
	}
	for _, rawFile := range files {
		file, _ := rawFile.(map[string]any)
		if file["path"] == "logo.png" {
			decoded, _ := base64.StdEncoding.DecodeString(file["content_base64"].(string))
			if string(decoded) != "png-bytes" {
				t.Errorf("logo.png content = %q", decoded)
			}
		}
		if strings.Contains(file["path"].(string), "/") {
			t.Errorf("files are sent by base name: %v", file["path"])
		}
	}

	for _, expected := range []string{
		"Application imported from Claude Design.",
		"ID:         application-id",
		"Repository: acme/neighborly-export (https://github.com/acme/neighborly-export)",
		"Commit:     abc123",
		"Pages:      2",
		"site/prices.html  (uses template logic; served as authored)",
		"Warnings:",
		"Prices.dc.html uses template logic",
		"Ankra is now analyzing the repository.",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output missing %q:\n%s", expected, output)
		}
	}
}

func TestApplicationImportClaudeDesignHonoursNameOwnerAndRepositoryFlags(t *testing.T) {
	directory := writeClaudeDesignExport(t)
	resetApplicationImportFlags(t)
	t.Cleanup(func() { resetApplicationImportFlags(t) })
	importBody := serveClaudeDesignImport(t, importedNeighborly)

	output, executeError := executeCommand("application", "import", "claude-design", filepath.Join(directory, "Main.dc.html"),
		"--name", "neighborly", "--owner", "acme", "--repository", "neighborly-site", "--visibility", "public", "-o", "json")
	if executeError != nil {
		t.Fatalf("application import claude-design = %v\n%s", executeError, output)
	}
	body := importBody()
	if body["name"] != "neighborly" || body["app_repo_name"] != "neighborly-site" || body["visibility"] != "public" {
		t.Errorf("flags not carried: %v", body)
	}
	if files, _ := body["files"].([]any); len(files) != 1 {
		t.Errorf("a single file path sends one file: %v", files)
	}
	var structured map[string]any
	if unmarshalError := json.Unmarshal([]byte(stripANSICodes(output)), &structured); unmarshalError != nil {
		t.Fatalf("-o json output does not parse: %v\n%s", unmarshalError, output)
	}
	if structured["id"] != "application-id" || structured["repository_url"] != "https://github.com/acme/neighborly-export" {
		t.Errorf("structured output = %v", structured)
	}
}

func TestApplicationImportClaudeDesignReportsThePlatformRefusal(t *testing.T) {
	directory := writeClaudeDesignExport(t)
	resetApplicationImportFlags(t)
	t.Cleanup(func() { resetApplicationImportFlags(t) })
	serveClaudeDesignImport(t, `{"id":null,"errors":[{"name":"neighborly-export","kind":"ApplicationResource",`+
		`"errors":[{"key":"app_repo_name","message":"A repository named 'neighborly-export' already exists under acme and holds other content; choose another repository name."}]}],`+
		`"repository":null,"pages":[],"warnings":[],"notes":[]}`)

	_, executeError := executeCommand("application", "import", "claude-design", directory)
	if executeError == nil {
		t.Fatal("a platform refusal must fail the command")
	}
	if !strings.Contains(executeError.Error(), "already exists under acme") {
		t.Errorf("error does not carry the platform's reason: %v", executeError)
	}
}

func TestApplicationImportClaudeDesignRefusesAnEmptyDirectory(t *testing.T) {
	resetApplicationImportFlags(t)
	t.Cleanup(func() { resetApplicationImportFlags(t) })
	serveClaudeDesignImport(t, importedNeighborly)

	_, executeError := executeCommand("application", "import", "claude-design", t.TempDir())
	if executeError == nil || !strings.Contains(executeError.Error(), "holds no files") {
		t.Fatalf("an empty directory must be refused before any request: %v", executeError)
	}
}

func TestApplicationNameSlug(t *testing.T) {
	for input, expected := range map[string]string{
		"Neighborly export": "neighborly-export",
		"Neighborly":        "neighborly",
		"my__site v2":       "my-site-v2",
	} {
		if slug := applicationNameSlug(input); slug != expected {
			t.Errorf("applicationNameSlug(%q) = %q, want %q", input, slug, expected)
		}
	}
}

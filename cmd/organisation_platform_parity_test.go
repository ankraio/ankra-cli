package cmd

// The organisation-level parity commands (ankra-hq41xb.15) against a
// scripted platform: the real client talks to an httptest server, so the
// method, path, query and body each command sends are pinned on the request
// itself. The three credential-bearing groups are also held to the secret
// rules: a secret reaches the request body and nothing else.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type organisationParityRequest struct {
	method string
	path   string
	query  string
	raw    string
	body   map[string]any
}

// organisationParityServer answers "METHOD /path" with the scripted status
// and JSON body and records every request. An unscripted route answers the
// router's own 404.
type organisationParityServer struct {
	mutex     sync.Mutex
	responses map[string]string
	statuses  map[string]int
	requests  []organisationParityRequest
}

func newOrganisationParityServer(t *testing.T, responses map[string]string) *organisationParityServer {
	t.Helper()
	recorder := &organisationParityServer{responses: responses, statuses: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(request.Body)
		recorded := organisationParityRequest{method: request.Method, path: request.URL.Path, query: request.URL.RawQuery, raw: string(raw)}
		if len(bytes.TrimSpace(raw)) > 0 {
			if decodeError := json.Unmarshal(raw, &recorded.body); decodeError != nil {
				t.Errorf("the request body is not a JSON object: %v: %s", decodeError, raw)
			}
		}
		key := request.Method + " " + request.URL.Path
		recorder.mutex.Lock()
		recorder.requests = append(recorder.requests, recorded)
		body, scripted := recorder.responses[key]
		status := recorder.statuses[key]
		recorder.mutex.Unlock()
		if !scripted {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"Not Found"}`))
			return
		}
		if status != 0 {
			writer.WriteHeader(status)
		}
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	withTempHome(t)
	setMockClient(t, client.New("test-token", server.URL))
	return recorder
}

func (recorder *organisationParityServer) seen() []organisationParityRequest {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]organisationParityRequest{}, recorder.requests...)
}

func (recorder *organisationParityServer) last(t *testing.T) organisationParityRequest {
	t.Helper()
	requests := recorder.seen()
	if len(requests) == 0 {
		t.Fatal("no request was sent")
	}
	return requests[len(requests)-1]
}

// runOrganisationParityCommand runs one command line through the root
// command with stdin holding input, and the flags of the top-level group it
// names put back to their defaults before and after.
func runOrganisationParityCommand(t *testing.T, input string, arguments ...string) (string, string, error) {
	t.Helper()
	group, _, findError := rootCmd.Find(arguments[:1])
	if findError != nil || group.Name() != arguments[0] {
		t.Fatalf("%s is not registered on the root command: %v", arguments[0], findError)
	}
	var commands []*cobra.Command
	var walk func(command *cobra.Command)
	walk = func(command *cobra.Command) {
		commands = append(commands, command)
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(group)
	resetTreeFlags(t, commands...)
	t.Cleanup(func() { resetTreeFlags(t, commands...) })
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(arguments)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
		rootCmd.SetArgs(nil)
	})
	runError := rootCmd.Execute()
	return stdout.String(), stderr.String(), runError
}

func organisationPromptsNotInteractive(t *testing.T) {
	t.Helper()
	previous := promptIsInteractive
	promptIsInteractive = func(io.Reader) bool { return false }
	t.Cleanup(func() { promptIsInteractive = previous })
}

// requireSecretOnlyInRequest fails when the secret shows up anywhere a
// person or a log could read it.
func requireSecretOnlyInRequest(t *testing.T, secret string, stdout string, stderr string, runError error) {
	t.Helper()
	if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Fatalf("the secret was printed: stdout %q, stderr %q", stdout, stderr)
	}
	if runError != nil && strings.Contains(runError.Error(), secret) {
		t.Fatalf("the secret is in the error: %v", runError)
	}
}

const organisationParitySecret = "s3cr3t-value-that-must-not-leak"

const addonDefinitionJSON = `{"id":"d1","name":"internal-gateway","display_name":"Internal gateway","description":"Edge","website_url":null,"documentation_url":null,"icon_url":null,"category":"networking","match_chart_names":["internal-gateway"],"match_keywords":["gateway"],"match_home_url_prefixes":[],"source":"organisation","created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-01T10:00:00Z"`

func TestHelmAddonDefinitionsListAndGet(t *testing.T) {
	recorder := newOrganisationParityServer(t, map[string]string{
		"GET /api/v1/org/helm/addon-definitions":                  `{"result":[` + addonDefinitionJSON + `},null]}`,
		"GET /api/v1/org/helm/addon-definitions/internal-gateway": addonDefinitionJSON + `,"chart_links":[{"id":"l1","chart_name":"edge","registry_url":"https://charts.example.com","link_type":"include","source":"organisation","created_at":"2026-11-01T10:00:00Z"},null]}`,
	})
	stdout, _, runError := runOrganisationParityCommand(t, "", "helm", "addon-definitions", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"internal-gateway", "Internal gateway", "networking", "organisation"})

	stdout, _, runError = runOrganisationParityCommand(t, "", "helm", "addon-definitions", "list", "-o", "json")
	if runError != nil {
		t.Fatalf("list -o json failed: %v", runError)
	}
	var listed []map[string]any
	if decodeError := json.Unmarshal([]byte(stdout), &listed); decodeError != nil || len(listed) != 1 || listed[0]["name"] != "internal-gateway" {
		t.Fatalf("stdout is not the definitions as JSON without the null entry: %v: %q", decodeError, stdout)
	}

	stdout, _, runError = runOrganisationParityCommand(t, "", "helm", "addon-definitions", "get", "internal-gateway")
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	if last := recorder.last(t); last.method != http.MethodGet || last.path != "/api/v1/org/helm/addon-definitions/internal-gateway" {
		t.Fatalf("unexpected request %+v", last)
	}
	assertContainsAll(t, stdout, []string{"Internal gateway", "include edge from https://charts.example.com"})
}

func TestHelmAddonDefinitionsCreateSendsTheWholeDefinition(t *testing.T) {
	recorder := newOrganisationParityServer(t, map[string]string{
		"POST /api/v1/org/helm/addon-definitions": addonDefinitionJSON + `}`,
	})
	_, _, runError := runOrganisationParityCommand(t, "", "helm", "addon-definitions", "create", "internal-gateway")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
		t.Fatalf("a create without --display-name must be a usage error that sends nothing, got %v", runError)
	}
	_, _, runError = runOrganisationParityCommand(t, "", "helm", "addon-definitions", "create", "internal-gateway",
		"--display-name", "Internal gateway", "--category", "networking", "--match-chart-name", "internal-gateway,edge", "--match-keyword", "gateway")
	if runError != nil {
		t.Fatalf("create failed: %v", runError)
	}
	body := recorder.last(t).body
	charts, _ := body["match_chart_names"].([]any)
	prefixes, isList := body["match_home_url_prefixes"].([]any)
	if body["name"] != "internal-gateway" || body["display_name"] != "Internal gateway" || body["category"] != "networking" ||
		len(charts) != 2 || charts[1] != "edge" || !isList || len(prefixes) != 0 || body["description"] != nil {
		t.Fatalf("unexpected create body %v", body)
	}
}

func TestHelmAddonDefinitionsUpdateChangesOnlyWhatWasNamed(t *testing.T) {
	path := "/api/v1/org/helm/addon-definitions/internal-gateway"
	recorder := newOrganisationParityServer(t, map[string]string{
		"GET " + path: addonDefinitionJSON + `,"chart_links":[]}`,
		"PUT " + path: addonDefinitionJSON + `}`,
	})
	_, _, runError := runOrganisationParityCommand(t, "", "helm", "addon-definitions", "update", "internal-gateway")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
		t.Fatalf("an update without a flag must be a usage error that sends nothing, got %v", runError)
	}
	_, _, runError = runOrganisationParityCommand(t, "", "helm", "addon-definitions", "update", "internal-gateway",
		"--category", "ingress", "--description", "")
	if runError != nil {
		t.Fatalf("update failed: %v", runError)
	}
	last := recorder.last(t)
	keywords, _ := last.body["match_keywords"].([]any)
	_, sentName := last.body["name"]
	if last.method != http.MethodPut || last.path != path || last.body["category"] != "ingress" || last.body["description"] != nil ||
		last.body["display_name"] != "Internal gateway" || len(keywords) != 1 || keywords[0] != "gateway" || sentName {
		t.Fatalf("the update must carry the unchanged members and the changed ones, without the name: %+v", last)
	}
}

func TestHelmAddonDefinitionsDeleteConfirmsAndGlobalIsRefused(t *testing.T) {
	path := "/api/v1/org/helm/addon-definitions/cert-manager"
	recorder := newOrganisationParityServer(t, map[string]string{"DELETE " + path: `{"detail":"Global add-on definitions are read-only"}`})
	recorder.statuses["DELETE "+path] = http.StatusForbidden
	organisationPromptsNotInteractive(t)
	_, _, runError := runOrganisationParityCommand(t, "", "helm", "addon-definitions", "delete", "cert-manager")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
		t.Fatalf("an unconfirmed delete must be a usage error that sends nothing, got %v", runError)
	}
	_, _, runError = runOrganisationParityCommand(t, "", "helm", "addon-definitions", "delete", "cert-manager", "--yes")
	if runError == nil || exitCodeFor(runError) != exitForbidden || !strings.Contains(runError.Error(), "read-only") {
		t.Fatalf("deleting a global definition must exit %d with the platform's reason, got %v", exitForbidden, runError)
	}
}

func TestHelmAddonDefinitionChartLinks(t *testing.T) {
	path := "/api/v1/org/helm/addon-definitions/cert-manager/chart-links"
	recorder := newOrganisationParityServer(t, map[string]string{
		"POST " + path:   `{"id":"l1","chart_name":"fork","registry_url":"https://charts.example.com","link_type":"exclude","source":"organisation","created_at":"2026-11-01T10:00:00Z"}`,
		"DELETE " + path: `{"deleted":true}`,
	})
	_, _, runError := runOrganisationParityCommand(t, "", "helm", "addon-definitions", "link-chart", "cert-manager", "--chart", "fork")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
		t.Fatalf("a link without --registry-url must be a usage error that sends nothing, got %v", runError)
	}
	stdout, _, runError := runOrganisationParityCommand(t, "", "helm", "addon-definitions", "link-chart", "cert-manager",
		"--chart", "fork", "--registry-url", "https://charts.example.com", "--type", "exclude")
	if runError != nil {
		t.Fatalf("link-chart failed: %v", runError)
	}
	if body := recorder.last(t).body; body["chart_name"] != "fork" || body["registry_url"] != "https://charts.example.com" || body["link_type"] != "exclude" {
		t.Fatalf("unexpected link body %v", body)
	}
	assertContainsAll(t, stdout, []string{"fork", "exclude"})

	_, _, runError = runOrganisationParityCommand(t, "", "helm", "addon-definitions", "unlink-chart", "cert-manager",
		"--chart", "fork", "--registry-url", "https://charts.example.com")
	if runError != nil {
		t.Fatalf("unlink-chart failed: %v", runError)
	}
	last := recorder.last(t)
	if last.method != http.MethodDelete || last.path != path ||
		last.query != "chart_name=fork&link_type=include&registry_url=https%3A%2F%2Fcharts.example.com" {
		t.Fatalf("the link is named by query parameters, got %+v", last)
	}
}

func TestHelmAddonCatalogListAndGet(t *testing.T) {
	recorder := newOrganisationParityServer(t, map[string]string{
		"GET /api/v1/org/helm/addon-catalog":              `{"result":[{"type":"addon","name":"cert-manager","display_name":"cert-manager","description":null,"website_url":null,"documentation_url":null,"icon_url":null,"category":"security","source":"global","chart_count":2,"installed_count":3,"charts":[]},null],"pagination":{"page":1,"page_size":20,"total_pages":4,"total_count":70},"categories":[{"name":"security","count":5}],"category_options":["security"]}`,
		"GET /api/v1/org/helm/addon-catalog/cert-manager": `{"addon":` + addonDefinitionJSON + `,"chart_links":[]},"charts":[{"name":"cert-manager","version":"1.16.1","registry_name":"jetstack","registry_url":"https://charts.jetstack.io","description":null,"icon":null,"is_deprecated":false,"is_private":false,"is_global":true,"installed_count":3}],"clusters":[{"cluster_id":"c1","cluster_name":"prod","cluster_kind":"hetzner","addon_name":"cert-manager","stack_name":"platform","installed_version":"1.15.0","chart_name":"cert-manager","registry_name":"jetstack"}],"installed_count":3,"category_options":[]}`,
	})
	stdout, _, runError := runOrganisationParityCommand(t, "", "helm", "addon-catalog", "list", "--search", "cert", "--page", "2", "--page-size", "50")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	if last := recorder.last(t); last.query != "page=2&page_size=50&search=cert" {
		t.Fatalf("unexpected query %q", last.query)
	}
	assertContainsAll(t, stdout, []string{"cert-manager", "security", "Page 1 of 4 (70 entries)."})

	_, _, runError = runOrganisationParityCommand(t, "", "helm", "addon-catalog", "list", "--page-size", "500")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("a page size past 100 must be a usage error, got %v", runError)
	}

	stdout, _, runError = runOrganisationParityCommand(t, "", "helm", "addon-catalog", "get", "cert-manager")
	if runError != nil {
		t.Fatalf("get failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"cert-manager 1.16.1 from jetstack", "prod: cert-manager 1.15.0 (stack platform)"})
}

func TestDatasourceCredentialsList(t *testing.T) {
	recorder := newOrganisationParityServer(t, map[string]string{
		"GET /api/v1/credentials/datasource": `[{"id":"cred-1","name":"grafana-cloud","provider":"datasource","organisation_id":"o1","system":false,"available":true,"state":"up","created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-01T10:00:00Z"}]`,
	})
	stdout, _, runError := runOrganisationParityCommand(t, "", "credentials", "datasource", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	if last := recorder.last(t); last.method != http.MethodGet || last.path != "/api/v1/credentials/datasource" {
		t.Fatalf("unexpected request %+v", last)
	}
	assertContainsAll(t, stdout, []string{"cred-1", "grafana-cloud", "up"})
}

func TestDatasourceCredentialCreateTakesTheSecretFromStdinOnly(t *testing.T) {
	recorder := newOrganisationParityServer(t, map[string]string{
		"POST /api/v1/credentials/datasource": `{"success":true,"id":"cred-1","name":"grafana-cloud","errors":[]}`,
	})
	recorder.statuses["POST /api/v1/credentials/datasource"] = http.StatusCreated
	organisationPromptsNotInteractive(t)

	for _, secretFlag := range []string{"--token", "--password", "--secret"} {
		_, _, runError := runOrganisationParityCommand(t, "", "credentials", "datasource", "create", "--name", "grafana-cloud", secretFlag, organisationParitySecret)
		if runError == nil || len(recorder.seen()) != 0 {
			t.Fatalf("%s must not exist: a secret is never an argument, got %v", secretFlag, runError)
		}
	}
	_, _, runError := runOrganisationParityCommand(t, "", "credentials", "datasource", "create", "--name", "grafana-cloud")
	if runError == nil || exitCodeFor(runError) != exitUsage || !strings.Contains(runError.Error(), "--secret-stdin") || len(recorder.seen()) != 0 {
		t.Fatalf("without a terminal the stdin flag is required and nothing is sent, got %v", runError)
	}
	for _, unusable := range []string{"\n", "  \t\n", strings.Repeat("x", secretStdinLimit+1)} {
		_, _, runError = runOrganisationParityCommand(t, unusable, "credentials", "datasource", "create", "--name", "grafana-cloud", "--secret-stdin")
		if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 0 {
			t.Fatalf("an empty, blank or over-long stdin must be a usage error that sends nothing, got %v", runError)
		}
	}

	stdout, stderr, runError := runOrganisationParityCommand(t, organisationParitySecret+"\n", "credentials", "datasource", "create",
		"--name", "grafana-cloud", "--secret-stdin")
	if runError != nil {
		t.Fatalf("create failed: %v", runError)
	}
	body := recorder.last(t).body
	if body["name"] != "grafana-cloud" || body["token"] != organisationParitySecret || body["password"] != nil || body["username"] != nil {
		t.Fatalf("a token credential sends the secret as token only, got %v", body)
	}
	requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)
	assertContainsAll(t, stdout, []string{"grafana-cloud", "cred-1"})

	stdout, stderr, runError = runOrganisationParityCommand(t, organisationParitySecret+"\n", "credentials", "datasource", "create",
		"--name", "mimir", "--username", "reader", "--secret-stdin", "-o", "json")
	if runError != nil {
		t.Fatalf("basic create failed: %v", runError)
	}
	body = recorder.last(t).body
	if body["username"] != "reader" || body["password"] != organisationParitySecret || body["token"] != nil {
		t.Fatalf("a basic credential sends the secret as password only, got %v", body)
	}
	requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)
}

func TestDatasourceCredentialCreatePromptsMaskedInATerminal(t *testing.T) {
	recorder := newOrganisationParityServer(t, map[string]string{
		"POST /api/v1/credentials/datasource": `{"success":true,"id":"cred-1","name":"grafana-cloud","errors":[]}`,
	})
	previousInteractive, previousPrompt := promptIsInteractive, maskedSecretPrompt
	promptIsInteractive = func(io.Reader) bool { return true }
	askedFor := ""
	maskedSecretPrompt = func(label string) (string, error) {
		askedFor = label
		return organisationParitySecret, nil
	}
	t.Cleanup(func() { promptIsInteractive, maskedSecretPrompt = previousInteractive, previousPrompt })
	stdout, stderr, runError := runOrganisationParityCommand(t, "", "credentials", "datasource", "create", "--name", "grafana-cloud", "--auth-scheme", "api_key")
	if runError != nil {
		t.Fatalf("create failed: %v", runError)
	}
	body := recorder.last(t).body
	if askedFor != "token" || body["token"] != organisationParitySecret || body["auth_scheme"] != "api_key" {
		t.Fatalf("the prompted token must be sent with its scheme: asked for %q, body %v", askedFor, body)
	}
	requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)
}

func TestDatasourceCredentialCreateRedactsAnEchoedSecret(t *testing.T) {
	recorder := newOrganisationParityServer(t, map[string]string{
		"POST /api/v1/credentials/datasource": `{"detail":"token ` + organisationParitySecret + ` is not valid"}`,
	})
	recorder.statuses["POST /api/v1/credentials/datasource"] = http.StatusBadRequest
	stdout, stderr, runError := runOrganisationParityCommand(t, organisationParitySecret, "credentials", "datasource", "create",
		"--name", "grafana-cloud", "--secret-stdin")
	if runError == nil || !strings.Contains(runError.Error(), "[redacted]") {
		t.Fatalf("a refusal must be returned with the secret redacted, got %v", runError)
	}
	requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)

	_, _, runError = runOrganisationParityCommand(t, "x", "credentials", "datasource", "create", "--name", "n", "--auth-scheme", "basic", "--secret-stdin")
	if runError == nil || exitCodeFor(runError) != exitUsage {
		t.Fatalf("--auth-scheme basic without --username must be a usage error, got %v", runError)
	}
}

const evidenceDestinationJSON = `{"id":"dest-1","name":"siem","kind":"webhook","config":{"url":"https://siem.example.com/ankra"},"credential_configured":true,"enabled":true,"organisation_id":"o1","created_by":null,"created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-01T10:00:00Z","revoked_at":null}`

func TestSecurityEvidenceDestinationsListAndRevoke(t *testing.T) {
	revokePath := "/api/v1/org/security/evidence/destinations/dest-1/revoke"
	recorder := newOrganisationParityServer(t, map[string]string{
		"GET /api/v1/org/security/evidence/destinations": `{"result":[` + evidenceDestinationJSON + `]}`,
		"POST " + revokePath:                             strings.Replace(evidenceDestinationJSON, `"revoked_at":null`, `"revoked_at":"2026-11-10T09:00:00Z"`, 1),
	})
	stdout, _, runError := runOrganisationParityCommand(t, "", "security", "evidence", "destinations", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"dest-1", "siem", "webhook", "https://siem.example.com/ankra", "enabled"})

	organisationPromptsNotInteractive(t)
	_, _, runError = runOrganisationParityCommand(t, "", "security", "evidence", "destinations", "revoke", "dest-1")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 1 {
		t.Fatalf("an unconfirmed revoke must be a usage error that sends nothing, got %v", runError)
	}
	stdout, _, runError = runOrganisationParityCommand(t, "", "security", "evidence", "destinations", "revoke", "dest-1", "--yes")
	if runError != nil {
		t.Fatalf("revoke failed: %v", runError)
	}
	if last := recorder.last(t); last.method != http.MethodPost || last.path != revokePath {
		t.Fatalf("unexpected request %+v", last)
	}
	assertContainsAll(t, stdout, []string{"State:      revoked"})
}

func TestSecurityEvidenceDestinationCreateSendsTheCredentialForItsKind(t *testing.T) {
	recorder := newOrganisationParityServer(t, map[string]string{
		"POST /api/v1/org/security/evidence/destinations": evidenceDestinationJSON,
	})
	recorder.statuses["POST /api/v1/org/security/evidence/destinations"] = http.StatusCreated
	organisationPromptsNotInteractive(t)

	for _, refused := range [][]string{
		{"--name", "siem", "--kind", "webhook", "--secret-stdin"},
		{"--name", "siem", "--kind", "pagerduty", "--url", "https://x.example.com", "--secret-stdin"},
		{"--name", "vanta", "--kind", "vanta", "--document-id", "doc-1", "--secret-stdin"},
		{"--name", "siem", "--kind", "webhook", "--url", "https://siem.example.com/ankra"},
		{"--name", "siem", "--kind", "webhook", "--url", "https://siem.example.com/ankra", "--signing-secret", organisationParitySecret},
	} {
		arguments := append([]string{"security", "evidence", "destinations", "create"}, refused...)
		_, _, runError := runOrganisationParityCommand(t, organisationParitySecret, arguments...)
		if runError == nil || len(recorder.seen()) != 0 {
			t.Fatalf("%v must be refused before anything is sent, got %v", refused, runError)
		}
	}

	for _, testCase := range []struct {
		kind      string
		arguments []string
		secretKey string
	}{
		{"webhook", []string{"--url", "https://siem.example.com/ankra"}, "signing_secret"},
		{"wazuh", []string{"--url", "https://wazuh.example.com/relay"}, "bearer_token"},
		{"vanta", []string{"--document-id", "doc-1", "--client-id", "client-1", "--api-base-url", "https://api.eu.vanta.com"}, "client_secret"},
	} {
		t.Run(testCase.kind, func(t *testing.T) {
			arguments := append([]string{"security", "evidence", "destinations", "create", "--name", "siem", "--kind", testCase.kind, "--secret-stdin"}, testCase.arguments...)
			stdout, stderr, runError := runOrganisationParityCommand(t, organisationParitySecret+"\n", arguments...)
			if runError != nil {
				t.Fatalf("create failed: %v", runError)
			}
			body := recorder.last(t).body
			credential, _ := body["credential"].(map[string]any)
			configuration, _ := body["config"].(map[string]any)
			if body["kind"] != testCase.kind || credential[testCase.secretKey] != organisationParitySecret {
				t.Fatalf("the secret must be sent as %s, got %v", testCase.secretKey, body)
			}
			if testCase.kind == "vanta" {
				if credential["client_id"] != "client-1" || configuration["document_id"] != "doc-1" || configuration["api_base_url"] != "https://api.eu.vanta.com" || len(credential) != 2 {
					t.Fatalf("unexpected vanta body %v", body)
				}
			} else if configuration["url"] != testCase.arguments[1] || len(credential) != 1 {
				t.Fatalf("unexpected %s body %v", testCase.kind, body)
			}
			requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)
			assertContainsAll(t, stdout, []string{"held by the platform (never shown)"})
		})
	}
}

const evidenceDeliveryJSON = `{"id":"del-1","destination_id":"dest-1","status":"uploaded","framework":"iso27001","period":"2026-10","complete":true,"attempts":1,"payload_sha256":"abc","receipt":{},"last_error":null,"last_error_class":null,"next_attempt_at":null,"organisation_id":"o1","requested_by":null,"created_at":"2026-11-01T10:00:00Z","updated_at":"2026-11-01T10:00:00Z","delivered_at":null,"submitted_at":null}`

func TestSecurityEvidenceDeliveries(t *testing.T) {
	submitPath := "/api/v1/org/security/evidence/deliveries/del-1/submit"
	recorder := newOrganisationParityServer(t, map[string]string{
		"GET /api/v1/org/security/evidence/deliveries":  `{"result":[` + evidenceDeliveryJSON + `]}`,
		"POST /api/v1/org/security/evidence/deliveries": evidenceDeliveryJSON,
		"POST " + submitPath:                            strings.Replace(evidenceDeliveryJSON, `"status":"uploaded"`, `"status":"submitted"`, 1),
	})
	recorder.statuses["POST /api/v1/org/security/evidence/deliveries"] = http.StatusAccepted

	stdout, _, runError := runOrganisationParityCommand(t, "", "security", "evidence", "deliveries", "list", "--destination", "dest-1", "--limit", "20")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	if last := recorder.last(t); last.query != "destination_id=dest-1&limit=20" {
		t.Fatalf("unexpected query %q", last.query)
	}
	assertContainsAll(t, stdout, []string{"del-1", "uploaded", "iso27001", "2026-10"})

	_, _, runError = runOrganisationParityCommand(t, "", "security", "evidence", "deliveries", "send")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != 1 {
		t.Fatalf("a send without --destination must be a usage error that sends nothing, got %v", runError)
	}
	stdout, _, runError = runOrganisationParityCommand(t, "", "security", "evidence", "deliveries", "send",
		"--destination", "dest-1", "--framework", "iso27001", "--month", "2026-10")
	if runError != nil {
		t.Fatalf("send failed: %v", runError)
	}
	if body := recorder.last(t).body; body["destination_id"] != "dest-1" || body["framework"] != "iso27001" || body["month"] != "2026-10" {
		t.Fatalf("unexpected send body %v", body)
	}
	assertContainsAll(t, stdout, []string{"draft in Vanta", "deliveries submit del-1"})

	_, _, runError = runOrganisationParityCommand(t, "", "security", "evidence", "deliveries", "send", "--destination", "dest-1")
	if runError != nil {
		t.Fatalf("send failed: %v", runError)
	}
	if body := recorder.last(t).body; len(body) != 1 {
		t.Fatalf("an omitted framework and month must not be sent, got %v", body)
	}

	stdout, _, runError = runOrganisationParityCommand(t, "", "security", "evidence", "deliveries", "submit", "del-1")
	if runError != nil {
		t.Fatalf("submit failed: %v", runError)
	}
	if last := recorder.last(t); last.method != http.MethodPost || last.path != submitPath {
		t.Fatalf("unexpected request %+v", last)
	}
	assertContainsAll(t, stdout, []string{"Status:      submitted"})
}

func TestSecurityEvidenceSubmitThatStaysUploadedExitsNonZero(t *testing.T) {
	submitPath := "/api/v1/org/security/evidence/deliveries/del-1/submit"
	newOrganisationParityServer(t, map[string]string{
		"POST " + submitPath: strings.Replace(evidenceDeliveryJSON, `"last_error":null`, `"last_error":"Vanta refused the document"`, 1),
	})
	stdout, _, runError := runOrganisationParityCommand(t, "", "security", "evidence", "deliveries", "submit", "del-1")
	if runError == nil || exitCodeFor(runError) != exitError || !strings.Contains(stdout, "Vanta refused the document") {
		t.Fatalf("a submission that leaves the delivery uploaded must exit non-zero and show why, got %v and %q", runError, stdout)
	}
}

const serviceCredentialFamilyID = "5b1f0c7e-3a55-4d0b-9a57-0d5f3c1a2b3c"

const serviceCredentialFamilyJSON = `{"id":"` + serviceCredentialFamilyID + `","name":"orders-db-password","created_at":"2026-11-01T10:00:00Z","active_value_id":null}`

func TestServiceCredentialFamilies(t *testing.T) {
	recorder := newOrganisationParityServer(t, map[string]string{
		"GET /api/v1/org/service-admission/credentials":                              `{"items":[` + serviceCredentialFamilyJSON + `],"next_cursor":null}`,
		"POST /api/v1/org/service-admission/credentials":                             serviceCredentialFamilyJSON,
		"GET /api/v1/org/service-admission/credentials/" + serviceCredentialFamilyID: serviceCredentialFamilyJSON,
	})
	recorder.statuses["POST /api/v1/org/service-admission/credentials"] = http.StatusCreated

	stdout, _, runError := runOrganisationParityCommand(t, "", "services", "credentials", "list")
	if runError != nil {
		t.Fatalf("list failed: %v", runError)
	}
	if last := recorder.last(t); last.query != "limit=50" {
		t.Fatalf("unexpected query %q", last.query)
	}
	assertContainsAll(t, stdout, []string{serviceCredentialFamilyID, "orders-db-password", "none yet"})

	stdout, _, runError = runOrganisationParityCommand(t, "", "services", "credentials", "create", "orders-db-password")
	if runError != nil {
		t.Fatalf("create failed: %v", runError)
	}
	if last := recorder.last(t); last.method != http.MethodPost || last.body["name"] != "orders-db-password" {
		t.Fatalf("unexpected request %+v", last)
	}
	assertContainsAll(t, stdout, []string{"orders-db-password", "Active value: none yet"})

	_, _, runError = runOrganisationParityCommand(t, "", "services", "credentials", "get", serviceCredentialFamilyID)
	if runError != nil {
		t.Fatalf("get by id failed: %v", runError)
	}
	if last := recorder.last(t); last.path != "/api/v1/org/service-admission/credentials/"+serviceCredentialFamilyID {
		t.Fatalf("an id is read directly, got %+v", last)
	}
	_, _, runError = runOrganisationParityCommand(t, "", "services", "credentials", "get", "orders-db-password")
	if runError != nil {
		t.Fatalf("get by name failed: %v", runError)
	}
	_, _, runError = runOrganisationParityCommand(t, "", "services", "credentials", "get", "no-such-family")
	if runError == nil || exitCodeFor(runError) != exitNotFound {
		t.Fatalf("an unknown family name must exit %d, got %v", exitNotFound, runError)
	}
}

func TestServiceCredentialValueAddTakesTheValueFromStdinOnly(t *testing.T) {
	familyPath := "/api/v1/org/service-admission/credentials/" + serviceCredentialFamilyID
	recorder := newOrganisationParityServer(t, map[string]string{
		"GET " + familyPath:              serviceCredentialFamilyJSON,
		"GET " + familyPath + "/values":  `{"items":[{"id":"val-1","state":"active","created_at":"2026-11-01T10:00:00Z","activated_at":"2026-11-01T10:00:01Z","retired_at":null,"retained":true}],"next_cursor":null}`,
		"POST " + familyPath + "/values": `{"value":{"id":"val-2","state":"active","created_at":"2026-11-10T10:00:00Z","activated_at":"2026-11-10T10:00:01Z","retired_at":null,"retained":true},"replaced":[{"id":"grant-1","value_id":"val-1","package_digest":"sha256:aa","input_name":"DATABASE_PASSWORD","mode":"existing","created_at":"2026-11-01T10:00:00Z","revoked_at":"2026-11-10T10:00:01Z"}],"granted":[{"id":"grant-2","value_id":"val-2","package_digest":"sha256:aa","input_name":"DATABASE_PASSWORD","mode":"existing","created_at":"2026-11-10T10:00:01Z","revoked_at":null}]}`,
	})
	recorder.statuses["POST "+familyPath+"/values"] = http.StatusCreated
	organisationPromptsNotInteractive(t)
	posts := func() int {
		count := 0
		for _, request := range recorder.seen() {
			if request.method == http.MethodPost {
				count++
			}
		}
		return count
	}

	stdout, _, runError := runOrganisationParityCommand(t, "", "services", "credentials", "values", "list", serviceCredentialFamilyID)
	if runError != nil {
		t.Fatalf("values list failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"val-1", "active"})

	for _, refused := range [][]string{
		{"--value-stdin"},
		{"--first", "--expected-value-id", "val-1", "--value-stdin"},
		{"--first"},
		{"--first", "--value", organisationParitySecret},
	} {
		arguments := append([]string{"services", "credentials", "values", "add", serviceCredentialFamilyID}, refused...)
		_, _, runError := runOrganisationParityCommand(t, organisationParitySecret, arguments...)
		if runError == nil || posts() != 0 {
			t.Fatalf("%v must be refused before a value is sent, got %v", refused, runError)
		}
	}

	stdout, stderr, runError := runOrganisationParityCommand(t, organisationParitySecret+"\n", "services", "credentials", "values", "add",
		serviceCredentialFamilyID, "--first", "--value-stdin")
	if runError != nil {
		t.Fatalf("values add failed: %v", runError)
	}
	last := recorder.last(t)
	expected, sent := last.body["expected_value_id"]
	if last.body["value"] != organisationParitySecret || !sent || expected != nil {
		t.Fatalf("--first must send the value with expected_value_id null, got %s", strings.ReplaceAll(last.raw, organisationParitySecret, "<secret>"))
	}
	requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)
	assertContainsAll(t, stdout, []string{"val-2", "Grants revoked with the previous value", "grant-1", "Replacement grants issued", "grant-2"})

	stdout, stderr, runError = runOrganisationParityCommand(t, organisationParitySecret+"\n", "services", "credentials", "values", "add",
		serviceCredentialFamilyID, "--expected-value-id", "val-1", "--value-stdin", "-o", "json")
	if runError != nil {
		t.Fatalf("rotation failed: %v", runError)
	}
	if body := recorder.last(t).body; body["expected_value_id"] != "val-1" {
		t.Fatalf("the expected value must be sent, got %v", body["expected_value_id"])
	}
	requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)
}

func TestServiceCredentialValueAddOnAStaleFamilyKeepsTheReasonAndTheSecretOut(t *testing.T) {
	familyPath := "/api/v1/org/service-admission/credentials/" + serviceCredentialFamilyID
	recorder := newOrganisationParityServer(t, map[string]string{
		"GET " + familyPath:              serviceCredentialFamilyJSON,
		"POST " + familyPath + "/values": `{"detail":"Credential state changed"}`,
	})
	recorder.statuses["POST "+familyPath+"/values"] = http.StatusConflict
	stdout, stderr, runError := runOrganisationParityCommand(t, organisationParitySecret, "services", "credentials", "values", "add",
		serviceCredentialFamilyID, "--first", "--value-stdin")
	if runError == nil || !strings.Contains(runError.Error(), "Credential state changed") {
		t.Fatalf("a 409 must keep the platform's reason, got %v", runError)
	}
	requireSecretOnlyInRequest(t, organisationParitySecret, stdout, stderr, runError)
}

func TestServiceCredentialGrants(t *testing.T) {
	familyPath := "/api/v1/org/service-admission/credentials/" + serviceCredentialFamilyID
	grantJSON := `{"id":"grant-1","value_id":"val-1","package_digest":"sha256:aa","input_name":"DATABASE_PASSWORD","mode":"existing","created_at":"2026-11-01T10:00:00Z","revoked_at":null}`
	recorder := newOrganisationParityServer(t, map[string]string{
		"GET " + familyPath:                                              serviceCredentialFamilyJSON,
		"GET " + familyPath + "/grants":                                  `{"items":[` + grantJSON + `],"next_cursor":null}`,
		"POST /api/v1/org/service-admission/credential-grants":           grantJSON,
		"DELETE /api/v1/org/service-admission/credential-grants/grant-1": ``,
	})
	recorder.statuses["POST /api/v1/org/service-admission/credential-grants"] = http.StatusCreated
	recorder.statuses["DELETE /api/v1/org/service-admission/credential-grants/grant-1"] = http.StatusNoContent
	organisationPromptsNotInteractive(t)

	stdout, _, runError := runOrganisationParityCommand(t, "", "services", "credentials", "grants", "list", serviceCredentialFamilyID)
	if runError != nil {
		t.Fatalf("grants list failed: %v", runError)
	}
	assertContainsAll(t, stdout, []string{"grant-1", "DATABASE_PASSWORD", "existing", "sha256:aa"})

	before := len(recorder.seen())
	_, _, runError = runOrganisationParityCommand(t, "", "services", "credentials", "grants", "issue", serviceCredentialFamilyID,
		"--package-digest", "sha256:aa", "--input-name", "DATABASE_PASSWORD", "--mode", "shared")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != before {
		t.Fatalf("an unknown mode must be a usage error that sends nothing, got %v", runError)
	}
	stdout, _, runError = runOrganisationParityCommand(t, "", "services", "credentials", "grants", "issue", serviceCredentialFamilyID,
		"--package-digest", "sha256:aa", "--input-name", "DATABASE_PASSWORD", "--mode", "existing")
	if runError != nil {
		t.Fatalf("grants issue failed: %v", runError)
	}
	body := recorder.last(t).body
	if body["family_id"] != serviceCredentialFamilyID || body["value_id"] != client.ServiceCredentialActiveValue ||
		body["package_digest"] != "sha256:aa" || body["input_name"] != "DATABASE_PASSWORD" || body["mode"] != "existing" {
		t.Fatalf("unexpected grant body %v", body)
	}
	assertContainsAll(t, stdout, []string{"Grant grant-1 issued"})

	before = len(recorder.seen())
	_, _, runError = runOrganisationParityCommand(t, "", "services", "credentials", "grants", "revoke", "grant-1")
	if runError == nil || exitCodeFor(runError) != exitUsage || len(recorder.seen()) != before {
		t.Fatalf("an unconfirmed revoke must be a usage error that sends nothing, got %v", runError)
	}
	stdout, _, runError = runOrganisationParityCommand(t, "", "services", "credentials", "grants", "revoke", "grant-1", "--yes")
	if runError != nil {
		t.Fatalf("grants revoke failed: %v", runError)
	}
	if last := recorder.last(t); last.method != http.MethodDelete || last.path != "/api/v1/org/service-admission/credential-grants/grant-1" {
		t.Fatalf("unexpected request %+v", last)
	}
	assertContainsAll(t, stdout, []string{"Grant grant-1 revoked."})
}

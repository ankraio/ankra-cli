package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"ankra/internal/client"

	"gopkg.in/yaml.v3"
)

// costDocumentWithFutureFields is value's wire document with fields the CLI
// struct does not declare added, as a newer platform answers.
func costDocumentWithFutureFields(t *testing.T, value any, futureFields map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding the fixture: %v", err)
	}
	document := map[string]any{}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	for key, futureValue := range futureFields {
		document[key] = futureValue
	}
	encoded, err = json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the document: %v", err)
	}
	return encoded
}

// newCostDocumentServer answers each GET path with its document through a
// real client, so the raw body travels the same way it does in production.
func newCostDocumentServer(t *testing.T, documents map[string][]byte) *client.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		document, known := documents[request.Method+" "+request.URL.Path]
		if !known {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(document)
	}))
	t.Cleanup(server.Close)
	return client.New("test-token", server.URL)
}

func decodeJSONDocument(t *testing.T, document []byte) map[string]any {
	t.Helper()
	decoded := map[string]any{}
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, document)
	}
	return decoded
}

var summaryFutureFields = map[string]any{
	"generated_at":  "2026-10-25T19:00:00Z",
	"scope":         map[string]any{"kind": "organisation"},
	"by_credential": []any{map[string]any{"credential_id": "c-1", "monthly_cost_estimate_cents": 1200}},
	"by_project":    []any{map[string]any{"project": "web", "monthly_cost_estimate_cents": 800}},
	"clusters":      []any{map[string]any{"cluster_id": "11111111-1111-4111-8111-111111111111", "fully_priced": true}},
}

var savingsFutureFields = map[string]any{
	"ladder":                     []any{map[string]any{"step": "right_size", "monthly_savings_cents": 900}},
	"total_ladder_savings_cents": 1234,
	"ladder_notes":               []any{"123", "true", ""},
	"unanalysed_clusters":        []any{map[string]any{"cluster_id": "99999999-9999-4999-8999-999999999999"}},
	"withheld":                   []any{map[string]any{"reason": "purpose_unconfirmed"}},
	"scope":                      map[string]any{"kind": "organisation"},
}

// -o json is the platform's document, every field it sent included, not the
// subset the CLI struct declares (ankra-bn86k4.8.5).
func TestCostStructuredOutputKeepsFieldsTheCliDoesNotDeclare(t *testing.T) {
	summary := costDocumentWithFutureFields(t, fleetCostFixture(), summaryFutureFields)
	savings := costDocumentWithFutureFields(t, cloudSavingsFixture(), savingsFutureFields)
	realClient := newCostDocumentServer(t, map[string][]byte{
		"GET /api/v1/org/cloud-cost/summary": summary,
		"GET /api/v1/org/cloud-cost/savings": savings,
	})
	for _, testCase := range []struct {
		command  string
		document []byte
		future   map[string]any
	}{
		{"summary", summary, summaryFutureFields},
		{"savings", savings, savingsFutureFields},
	} {
		output, executeError := runCostSettingsAgainst(t, realClient, "cost", testCase.command, "-o", "json")
		if executeError != nil {
			t.Fatalf("cost %s -o json: %v", testCase.command, executeError)
		}
		got := decodeJSONDocument(t, []byte(output))
		if want := decodeJSONDocument(t, testCase.document); !reflect.DeepEqual(want, got) {
			t.Fatalf("cost %s -o json is not the platform's document:\nwant %s\ngot  %s", testCase.command, testCase.document, output)
		}
		for key := range testCase.future {
			if _, present := got[key]; !present {
				t.Fatalf("cost %s -o json dropped %q:\n%s", testCase.command, key, output)
			}
		}
	}
}

func TestCostYAMLOutputKeepsFieldsTheCliDoesNotDeclare(t *testing.T) {
	realClient := newCostDocumentServer(t, map[string][]byte{
		"GET /api/v1/org/cloud-cost/summary": costDocumentWithFutureFields(t, fleetCostFixture(), summaryFutureFields),
		"GET /api/v1/org/cloud-cost/savings": costDocumentWithFutureFields(t, cloudSavingsFixture(), savingsFutureFields),
	})
	output, executeError := runCostSettingsAgainst(t, realClient, "cost", "summary", "-o", "yaml")
	if executeError != nil {
		t.Fatalf("cost summary -o yaml: %v", executeError)
	}
	summary := map[string]any{}
	if err := yaml.Unmarshal([]byte(output), &summary); err != nil {
		t.Fatalf("not YAML: %v\n%s", err, output)
	}
	if summary["generated_at"] != "2026-10-25T19:00:00Z" || summary["projected_month_end_cents"] != 86000 {
		t.Fatalf("summary -o yaml lacks the platform's fields:\n%s", output)
	}
	if scope, _ := summary["scope"].(map[string]any); scope["kind"] != "organisation" {
		t.Fatalf("summary -o yaml dropped scope:\n%s", output)
	}

	output, executeError = runCostSettingsAgainst(t, realClient, "cost", "savings", "-o", "yaml")
	if executeError != nil {
		t.Fatalf("cost savings -o yaml: %v", executeError)
	}
	savings := map[string]any{}
	if err := yaml.Unmarshal([]byte(output), &savings); err != nil {
		t.Fatalf("not YAML: %v\n%s", err, output)
	}
	if savings["total_ladder_savings_cents"] != 1234 {
		t.Fatalf("savings -o yaml dropped total_ladder_savings_cents:\n%s", output)
	}
	// Strings that read as another type when bare stay strings.
	if notes, _ := savings["ladder_notes"].([]any); !reflect.DeepEqual(notes, []any{"123", "true", ""}) {
		t.Fatalf("ladder_notes = %#v, want the three strings:\n%s", savings["ladder_notes"], output)
	}
	if ladder, _ := savings["ladder"].([]any); len(ladder) != 1 {
		t.Fatalf("savings -o yaml dropped ladder:\n%s", output)
	}
}

// The typed decode still drives the human rendering of the same document.
func TestCostHumanRenderingReadsTheSameDocument(t *testing.T) {
	realClient := newCostDocumentServer(t, map[string][]byte{
		"GET /api/v1/org/cloud-cost/summary": costDocumentWithFutureFields(t, fleetCostFixture(), summaryFutureFields),
		"GET /api/v1/org/cloud-cost/savings": costDocumentWithFutureFields(t, cloudSavingsFixture(), savingsFutureFields),
	})
	output, executeError := runCostSettingsAgainst(t, realClient, "cost", "summary")
	if executeError != nil {
		t.Fatalf("cost summary: %v", executeError)
	}
	if !strings.Contains(output, "Cloud cost (EUR): €860.00 projected month end · €410.00 month to date · €842.00/mo run rate") ||
		!strings.Contains(output, "prod-eu") || strings.HasPrefix(strings.TrimSpace(output), "{") {
		t.Fatalf("the summary renders for people:\n%s", output)
	}
	output, executeError = runCostSettingsAgainst(t, realClient, "cost", "savings")
	if executeError != nil {
		t.Fatalf("cost savings: %v", executeError)
	}
	if !strings.Contains(output, "Cloud savings (EUR): €1166.57/mo across 3 recommendations") {
		t.Fatalf("the savings render for people:\n%s", output)
	}
}

// settings get and set print the platform's document too: the settings
// server answers fields (display_currency, fx_rates) the struct lacks.
func TestCostSettingsStructuredOutputIsThePlatformsDocument(t *testing.T) {
	_, realClient := newSettingsServer(t)
	output, executeError := runCostSettingsAgainst(t, realClient, "cost", "settings", "get", "-o", "json")
	if executeError != nil {
		t.Fatalf("cost settings get -o json: %v", executeError)
	}
	got := decodeJSONDocument(t, []byte(output))
	if got["display_currency"] != "usd" || got["fx_rates"] == nil {
		t.Fatalf("settings -o json dropped the platform's fields:\n%s", output)
	}
}

// A refused settings write is a permission refusal (7), not an expired login
// (6): the admin-only route answers a bare-detail 403. A 401 stays a login
// problem.
func TestCostSettingsSetRefusalExitCodes(t *testing.T) {
	recorder, realClient := newSettingsServer(t)
	refusal := "Only organisation admins can change the cost settings"
	recorder.refuse(http.StatusForbidden, refusal)
	_, executeError := runCostSettingsAgainst(t, realClient, "cost", "settings", "set", "--discount", "10")
	if executeError == nil || !strings.Contains(executeError.Error(), refusal) || exitCodeFor(executeError) != exitForbidden {
		t.Fatalf("a refused write = %v (exit %d), want the refusal (exit %d)", executeError, exitCodeFor(executeError), exitForbidden)
	}

	recorder.refuse(http.StatusUnauthorized, "Not authenticated")
	_, executeError = runCostSettingsAgainst(t, realClient, "cost", "settings", "set", "--discount", "10")
	if executeError == nil || exitCodeFor(executeError) != exitAuth {
		t.Fatalf("a rejected credential = %v (exit %d), want exit %d", executeError, exitCodeFor(executeError), exitAuth)
	}
}

// rawDocumentFixture carries a document the way a decoded response does.
type rawDocumentFixture struct {
	Declared string `json:"declared" yaml:"declared"`
	document json.RawMessage
}

func (fixture rawDocumentFixture) RawJSON() json.RawMessage { return fixture.document }

func TestStructuredDocumentKeepsKeyOrderAndNumberLiterals(t *testing.T) {
	document := `{"zeta":1,"alpha":{"b":"x","a":[1.50,"2",true,null]},"big":12345678901234567890123,` +
		`"empty":"","multi":"a\nb","emoji":"\ud83d\ude00","nothing":{},"none":[]}`
	value := &rawDocumentFixture{Declared: "only this", document: json.RawMessage(document)}

	var jsonOut bytes.Buffer
	if _, err := encodeStructuredCounting(&jsonOut, outputJSON, value, new(bytes.Buffer)); err != nil {
		t.Fatalf("json: %v", err)
	}
	text := jsonOut.String()
	if !strings.Contains(text, "12345678901234567890123") || !strings.Contains(text, "1.50") ||
		strings.Index(text, `"zeta"`) > strings.Index(text, `"alpha"`) || strings.Contains(text, "only this") {
		t.Fatalf("-o json must be the document, literals and order kept:\n%s", text)
	}

	var yamlOut bytes.Buffer
	if _, err := encodeStructuredCounting(&yamlOut, outputYAML, value, new(bytes.Buffer)); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	text = yamlOut.String()
	for _, want := range []string{"big: 12345678901234567890123", "- 1.50", `- "2"`, `empty: ""`, "nothing: {}", "none: []"} {
		if !strings.Contains(text, want) {
			t.Fatalf("-o yaml lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "zeta:") > strings.Index(text, "alpha:") || strings.Contains(text, "only this") {
		t.Fatalf("-o yaml must be the document in its own order:\n%s", text)
	}
	decoded := map[string]any{}
	if err := yaml.Unmarshal(yamlOut.Bytes(), &decoded); err != nil {
		t.Fatalf("not YAML: %v\n%s", err, text)
	}
	if decoded["multi"] != "a\nb" || decoded["emoji"] != "😀" || decoded["empty"] != "" {
		t.Fatalf("strings must round-trip: %#v", decoded)
	}

	// A carrier without a document prints as its struct, as before.
	var fallback bytes.Buffer
	if _, err := encodeStructuredCounting(&fallback, outputJSON, &rawDocumentFixture{Declared: "only this"}, new(bytes.Buffer)); err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if !strings.Contains(fallback.String(), `"declared": "only this"`) {
		t.Fatalf("an empty carrier prints its struct:\n%s", fallback.String())
	}
}

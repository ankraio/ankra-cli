package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// documentRoute is what a stand-in platform answers one route with.
type documentRoute struct {
	status int
	body   string
}

// documentCall is one request the stand-in platform received.
type documentCall struct {
	method string
	path   string
	query  string
	body   string
}

// serveDocuments stands in for the platform: it answers each "METHOD /path"
// in routes, a 404 for anything else, and records every request it received.
func serveDocuments(t *testing.T, routes map[string]documentRoute) (*httptest.Server, *[]documentCall) {
	t.Helper()
	calls := &[]documentCall{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		*calls = append(*calls, documentCall{method: request.Method, path: request.URL.Path,
			query: request.URL.RawQuery, body: string(body)})
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("%s %s authorization = %q", request.Method, request.URL.Path, got)
		}
		writer.Header().Set("Content-Type", "application/json")
		route, isServed := routes[request.Method+" "+request.URL.Path]
		if !isServed {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(writer, `{"detail":"Not Found"}`)
			return
		}
		writer.WriteHeader(route.status)
		_, _ = fmt.Fprint(writer, route.body)
	}))
	t.Cleanup(server.Close)
	return server, calls
}

// runDocumentCommand runs a freshly built command against the stand-in
// platform and returns stdout and stderr apart.
func runDocumentCommand(t *testing.T, server *httptest.Server, command *cobra.Command, stdin string, arguments ...string) (string, string, error) {
	t.Helper()
	useTestClient(t, server.URL)
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetIn(strings.NewReader(stdin))
	command.SetArgs(arguments)
	executeError := command.Execute()
	return stdout.String(), stderr.String(), executeError
}

// requireDocumentCalls fails unless the platform received exactly the wanted
// "METHOD /path" requests, in order.
func requireDocumentCalls(t *testing.T, calls *[]documentCall, wanted ...string) {
	t.Helper()
	received := make([]string, 0, len(*calls))
	for _, call := range *calls {
		received = append(received, call.method+" "+call.path)
	}
	if strings.Join(received, "\n") != strings.Join(wanted, "\n") {
		t.Fatalf("requests = %q, want %q", received, wanted)
	}
}

// decodeDocumentCallBody reads the JSON object a recorded request carried.
func decodeDocumentCallBody(t *testing.T, call documentCall) map[string]any {
	t.Helper()
	body := map[string]any{}
	if decodeError := json.Unmarshal([]byte(call.body), &body); decodeError != nil {
		t.Fatalf("%s %s body %q is not a JSON object: %v", call.method, call.path, call.body, decodeError)
	}
	return body
}

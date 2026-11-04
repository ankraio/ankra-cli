package client

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const warningProjectID = "6f1c1f0e-0000-4000-8000-000000000001"

// projectWarningClient builds a client whose transports are the real ones New
// builds, with the project selected and the warning's output captured apart
// from anything a command would print on stdout.
func projectWarningClient(t *testing.T, handler http.HandlerFunc) (*Client, *bytes.Buffer) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	testClient := New(testToken, server.URL)
	testClient.SetProjectOverride(warningProjectID)
	output := &bytes.Buffer{}
	testClient.projectScopeWarning.output = output
	return testClient, output
}

func answering(t *testing.T, statusCode int, headerValue string) http.HandlerFunc {
	t.Helper()
	return func(writer http.ResponseWriter, _ *http.Request) {
		if headerValue != "" {
			writer.Header().Set(AppliedProjectHeader, headerValue)
		}
		jsonResponse(t, writer, statusCode, map[string]string{})
	}
}

func getThrough(t *testing.T, testClient *Client, path string) {
	t.Helper()
	response, requestError := testClient.HTTP.Get(testClient.BaseURL + path)
	if requestError != nil {
		t.Fatalf("GET %s error = %v", path, requestError)
	}
	_ = response.Body.Close()
}

// The warning names the project and the route on one line, however many
// requests the invocation makes, and only ever reaches its own writer.
func TestProjectScopeWarningFiresOnceWhenAReadSaysNone(t *testing.T) {
	testClient, output := projectWarningClient(t, answering(t, http.StatusOK, appliedProjectNone))
	for range 3 {
		getThrough(t, testClient, "/api/v1/org/security/findings")
	}
	lines := strings.Split(strings.TrimRight(output.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly one warning line, got %q", output.String())
	}
	for _, want := range []string{warningProjectID, "GET /api/v1/org/security/findings", envProjectScopeWarning} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("warning lacks %q: %q", want, lines[0])
		}
	}
	if strings.Contains(lines[0], testToken) {
		t.Fatal("the warning must never carry the token")
	}
}

// The default writer is what keeps `-o json` stdout parseable.
func TestProjectScopeWarningWritesToStderrByDefault(t *testing.T) {
	testClient := New(testToken, "https://platform.example.com")
	if testClient.projectScopeWarning.output != newProjectScopeWarning().output {
		t.Fatal("the warning must write to stderr")
	}
}

func TestProjectScopeWarningStaysQuiet(t *testing.T) {
	for name, testCase := range map[string]struct {
		statusCode  int
		headerValue string
		path        string
	}{
		"scoped":                    {http.StatusOK, warningProjectID, "/api/v1/org/security/findings"},
		"platform without a header": {http.StatusOK, "", "/api/v1/org/security/findings"},
		"refused":                   {http.StatusNotFound, appliedProjectNone, "/api/v1/org/security/findings"},
		"organisation-level route":  {http.StatusOK, appliedProjectNone, "/api/v1/org/billing/analytics"},
		"exact organisation route":  {http.StatusOK, appliedProjectNone, "/api/v1/org/organisation"},
		"explicit all projects":     {http.StatusOK, appliedProjectNone, "/api/v1/org/cloud-cost/summary?project_id=all"},
	} {
		t.Run(name, func(t *testing.T) {
			testClient, output := projectWarningClient(t, answering(t, testCase.statusCode, testCase.headerValue))
			getThrough(t, testClient, testCase.path)
			if output.Len() != 0 {
				t.Fatalf("want no warning, got %q", output.String())
			}
		})
	}
}

// A route only sharing a prefix's first letters is not organisation-level.
func TestProjectScopeWarningMatchesWholePathSegments(t *testing.T) {
	testClient, output := projectWarningClient(t, answering(t, http.StatusOK, appliedProjectNone))
	getThrough(t, testClient, "/api/v1/org/organisation-things")
	if output.Len() == 0 {
		t.Fatal("want a warning for a route outside the organisation-level list")
	}
}

func TestProjectScopeWarningStaysQuietWithNoProjectSelected(t *testing.T) {
	testClient, output := projectWarningClient(t, answering(t, http.StatusOK, appliedProjectNone))
	testClient.SetProjectOverride("")
	getThrough(t, testClient, "/api/v1/org/security/findings")
	if output.Len() != 0 {
		t.Fatalf("want no warning, got %q", output.String())
	}
}

func TestProjectScopeWarningCanBeSilencedForScripts(t *testing.T) {
	t.Setenv(envProjectScopeWarning, "off")
	testClient, output := projectWarningClient(t, answering(t, http.StatusOK, appliedProjectNone))
	getThrough(t, testClient, "/api/v1/org/security/findings")
	if output.Len() != 0 {
		t.Fatalf("want no warning, got %q", output.String())
	}
}

func TestProjectScopeWarningStaysQuietForAnOrganisationLevelCommand(t *testing.T) {
	SetInvocationOrganisationLevel(true)
	t.Cleanup(func() { SetInvocationOrganisationLevel(false) })
	testClient, output := projectWarningClient(t, answering(t, http.StatusOK, appliedProjectNone))
	getThrough(t, testClient, "/api/v1/org/security/findings")
	if output.Len() != 0 {
		t.Fatalf("want no warning, got %q", output.String())
	}
}

// A long synchronous write takes its own transport; a read through it must
// warn all the same.
func TestProjectScopeWarningCoversTheSlowWriteTransport(t *testing.T) {
	testClient, output := projectWarningClient(t, answering(t, http.StatusOK, appliedProjectNone))
	response, requestError := testClient.httpClientForSlowWrite().Get(testClient.BaseURL + "/api/v1/org/alerts")
	if requestError != nil {
		t.Fatalf("GET error = %v", requestError)
	}
	_ = response.Body.Close()
	if !strings.Contains(output.String(), "/api/v1/org/alerts") {
		t.Fatalf("want a warning, got %q", output.String())
	}
}

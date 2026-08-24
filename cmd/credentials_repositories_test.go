package cmd

// The credential repository-coverage command.
//
// The customer in PLA-786 had a credential reporting one repository while
// every call against the repository it was bound to answered 404, and no
// surface anywhere broke that count down. These pin the two distinctions the
// command exists to keep: reachable-versus-required, and an unread listing
// versus an installation that reaches nothing.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ankra/internal/client"
)

// serveCoverage stands up a server answering the repositories route with the
// supplied body and points the command's client at it.
func serveCoverage(t *testing.T, body string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if !strings.HasSuffix(request.URL.Path, "/repositories") {
			responseWriter.WriteHeader(http.StatusNotFound)
			return
		}
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(body))
	}))
	previousClient := apiClient
	previousBaseURL := baseURL
	apiClient = client.New("test-token", server.URL)
	baseURL = server.URL
	t.Cleanup(func() {
		apiClient = previousClient
		baseURL = previousBaseURL
		server.Close()
	})
}

// runRepositories drives the command and returns everything it printed. The
// command prints with fmt.Printf like the rest of this package, so stdout is
// what has to be captured.
func runRepositories(t *testing.T, credentialID string) string {
	t.Helper()
	var runError error
	output := captureStdout(t, func() {
		runError = credentialsRepositoriesCmd.RunE(credentialsRepositoriesCmd, []string{credentialID})
	})
	if runError != nil {
		t.Fatalf("command returned an error: %v", runError)
	}
	return output
}

const coveredBody = `{
  "credential": {"id":"11111111-1111-4111-8111-111111111111","name":"github-app-example",
    "provider":"github","state":"up","available":true,"account_login":"example-org",
    "app_backed":true,"syncing":false},
  "coverage": "complete",
  "coverage_message": "The GitHub App installation behind this credential can reach every repository Ankra needs from it.",
  "accessible_repositories": ["example-org/one","example-org/two"],
  "accessible_repositories_complete": true,
  "accessible_repositories_error": null,
  "required_repositories": ["example-org/one"],
  "required_repositories_complete": true,
  "unreachable_repositories": [],
  "unverified_repositories": []
}`

// The reported failure: required repositories the installation cannot see.
const incompleteBody = `{
  "credential": {"id":"11111111-1111-4111-8111-111111111111","name":"github-app-example",
    "provider":"github","state":"up","available":true,"app_backed":true,"syncing":false},
  "coverage": "incomplete",
  "coverage_message": "This credential cannot reach every repository Ankra needs from it.",
  "accessible_repositories": ["example-org/two"],
  "accessible_repositories_complete": true,
  "accessible_repositories_error": null,
  "required_repositories": ["example-org/one"],
  "required_repositories_complete": true,
  "unreachable_repositories": ["example-org/one"],
  "unverified_repositories": []
}`

// The listing itself failed. Null, not an empty array.
const unknownBody = `{
  "credential": {"id":"11111111-1111-4111-8111-111111111111","name":"github-app-example",
    "provider":"github","state":"up","available":true,"app_backed":true,"syncing":false},
  "coverage": "unknown",
  "coverage_message": "Whether this credential covers what Ankra needs could not be established.",
  "accessible_repositories": null,
  "accessible_repositories_complete": false,
  "accessible_repositories_error": "GitHub API returned status 502 while listing installation repositories.",
  "required_repositories": ["example-org/one"],
  "required_repositories_complete": true,
  "unreachable_repositories": [],
  "unverified_repositories": ["example-org/one"]
}`

// An installation that genuinely reaches nothing. Empty array, not null - the
// opposite fact from unknownBody, and it must not render the same way.
const reachesNothingBody = `{
  "credential": {"id":"11111111-1111-4111-8111-111111111111","name":"github-app-example",
    "provider":"github","state":"up","available":true,"app_backed":true,"syncing":false},
  "coverage": "incomplete",
  "coverage_message": "This credential cannot reach every repository Ankra needs from it.",
  "accessible_repositories": [],
  "accessible_repositories_complete": true,
  "accessible_repositories_error": null,
  "required_repositories": ["example-org/one"],
  "required_repositories_complete": true,
  "unreachable_repositories": ["example-org/one"],
  "unverified_repositories": []
}`

func TestCredentialRepositoriesReportsCoverageAndBothSets(t *testing.T) {
	serveCoverage(t, coveredBody)
	output := runRepositories(t, "11111111-1111-4111-8111-111111111111")

	for _, want := range []string{"complete", "example-org/one", "example-org/two", "Required by Ankra"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want it to mention %q", output, want)
		}
	}
}

func TestCredentialRepositoriesNamesWhatIsRequiredButUnreachable(t *testing.T) {
	serveCoverage(t, incompleteBody)
	output := runRepositories(t, "11111111-1111-4111-8111-111111111111")

	if !strings.Contains(output, "Required but NOT reachable") {
		t.Fatalf("output = %q, want the unreachable section", output)
	}
	if !strings.Contains(output, "incomplete") {
		t.Fatalf("output = %q, want the incomplete verdict", output)
	}
}

// The distinction the whole endpoint exists for: "we could not check" must
// never render as "it covers nothing", which is what the frozen REPOS count
// effectively did.
func TestCredentialRepositoriesSeparatesAnUnreadListingFromAnEmptyOne(t *testing.T) {
	serveCoverage(t, unknownBody)
	unread := runRepositories(t, "11111111-1111-4111-8111-111111111111")

	if !strings.Contains(unread, "could not be read") {
		t.Fatalf("output = %q, want the unread listing named as such", unread)
	}
	if !strings.Contains(unread, "502") {
		t.Fatalf("output = %q, want the underlying error surfaced", unread)
	}
	if strings.Contains(unread, "reaches no repository at all") {
		t.Fatalf("output = %q, must not claim the installation reaches nothing", unread)
	}

	serveCoverage(t, reachesNothingBody)
	empty := runRepositories(t, "11111111-1111-4111-8111-111111111111")

	if !strings.Contains(empty, "reaches no repository at all") {
		t.Fatalf("output = %q, want an empty set stated plainly", empty)
	}
	if strings.Contains(empty, "could not be read") {
		t.Fatalf("output = %q, an empty set is an answer and must not read as unknown", empty)
	}
}

// The client model must keep null distinguishable from [] after decoding, or
// the rendering above cannot tell them apart no matter what it does.
func TestCredentialRepositoryCoverageDecodesNullAndEmptyDistinctly(t *testing.T) {
	var unread client.CredentialRepositoryCoverage
	if decodeError := json.Unmarshal([]byte(unknownBody), &unread); decodeError != nil {
		t.Fatalf("decoding the unread body: %v", decodeError)
	}
	if unread.AccessibleRepositories != nil {
		t.Fatalf("a null accessible list must decode to nil, got %v", *unread.AccessibleRepositories)
	}

	var empty client.CredentialRepositoryCoverage
	if decodeError := json.Unmarshal([]byte(reachesNothingBody), &empty); decodeError != nil {
		t.Fatalf("decoding the empty body: %v", decodeError)
	}
	if empty.AccessibleRepositories == nil {
		t.Fatal("an empty accessible list must decode to a non-nil empty slice, not nil")
	}
	if len(*empty.AccessibleRepositories) != 0 {
		t.Fatalf("accessible = %v, want empty", *empty.AccessibleRepositories)
	}
}

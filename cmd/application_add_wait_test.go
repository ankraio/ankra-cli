package cmd

// The --wait flag on `application add`.
//
// Without it the command ends on "Ankra is now analyzing the repository" and
// nothing says when that finished or where the setup pull request went. These
// pin the three answers the wait can reach.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// resetApplicationAddFlags puts the add command's flags back to their
// defaults. The root command is shared across invocations in this package, so
// a --wait set by one case is still Changed in the next one without it.
func resetApplicationAddFlags(t *testing.T) {
	t.Helper()
	for _, applicationCommand := range rootCmd.Commands() {
		if applicationCommand.Name() != "application" {
			continue
		}
		for _, command := range applicationCommand.Commands() {
			if command.Name() == "add" {
				resetTreeFlags(t, command)
				return
			}
		}
	}
}

// serveApplicationAnalysis answers the create route once and then the
// application read route with each supplied body in turn, repeating the last.
func serveApplicationAnalysis(t *testing.T, readBodies ...string) {
	t.Helper()
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			_, _ = responseWriter.Write([]byte(`{"id":"application-id"}`))
			return
		}
		if strings.Contains(request.URL.Path, "/credentials") {
			_, _ = responseWriter.Write([]byte(`[{"id":"credential-acme","name":"github-acme","provider":"github",` +
				`"available":true,"account_login":"acme","created_at":"2026-09-01T00:00:00Z"}]`))
			return
		}
		index := int(reads.Add(1)) - 1
		if index >= len(readBodies) {
			index = len(readBodies) - 1
		}
		_, _ = responseWriter.Write([]byte(readBodies[index]))
	}))
	// useTestClient satisfies the root command's auth gate as well as pointing
	// the client at the server: the gate reads the token, so without it these
	// fail with "not logged in" wherever no real credentials exist, which is
	// every CI runner.
	useTestClient(t, server.URL)
	t.Cleanup(server.Close)
}

func TestApplicationAddWaitReportsTheSetupPullRequest(t *testing.T) {
	repositoryPath := createTestGitRepository(t, "main", "https://github.com/acme/payments.git")
	resetApplicationAddFlags(t)
	t.Cleanup(func() { resetApplicationAddFlags(t) })
	serveApplicationAnalysis(t,
		`{"analysis_status":"pending","creation_progress":"Reading the repository"}`,
		`{"analysis_status":"complete","pull_request_url":"https://github.com/acme/payments/pull/7"}`)

	output, executeError := executeCommand("application", "add", repositoryPath, "--wait", "--timeout", "30s")
	if executeError != nil {
		t.Fatalf("application add --wait = %v", executeError)
	}
	if !strings.Contains(output, "Analysis complete.") {
		t.Errorf("output does not report completion:\n%s", output)
	}
	if !strings.Contains(output, "https://github.com/acme/payments/pull/7") {
		t.Errorf("output does not carry the setup pull request:\n%s", output)
	}
}

func TestApplicationAddWaitReportsAFailedAnalysis(t *testing.T) {
	repositoryPath := createTestGitRepository(t, "main", "https://github.com/acme/payments.git")
	resetApplicationAddFlags(t)
	t.Cleanup(func() { resetApplicationAddFlags(t) })
	serveApplicationAnalysis(t,
		`{"analysis_status":"failed","error_message":"no Dockerfile and no recipe could be generated"}`)

	_, executeError := executeCommand("application", "add", repositoryPath, "--wait", "--timeout", "30s")
	if executeError == nil {
		t.Fatal("a failed analysis must fail the command")
	}
	if !strings.Contains(executeError.Error(), "no Dockerfile") {
		t.Errorf("error does not carry the platform's reason: %v", executeError)
	}
}

func TestApplicationAddWithoutWaitReturnsImmediately(t *testing.T) {
	repositoryPath := createTestGitRepository(t, "main", "https://github.com/acme/payments.git")
	resetApplicationAddFlags(t)
	t.Cleanup(func() { resetApplicationAddFlags(t) })
	serveApplicationAnalysis(t, `{"analysis_status":"pending"}`)

	output, executeError := executeCommand("application", "add", repositoryPath)
	if executeError != nil {
		t.Fatalf("application add = %v", executeError)
	}
	if strings.Contains(output, "Waiting for the analysis") {
		t.Errorf("the default must not wait:\n%s", output)
	}
}

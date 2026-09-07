package cmd

// The credential detail view's health lines.
//
// `credentials list` prints a state column, so a credential can read "down"
// there while the detail view says nothing about it and nothing hints that a
// third command holds the reason. These pin that the detail view answers the
// question it raises.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ankra/internal/client"
)

// serveCredentialAndCoverage answers the credential detail route with
// detailBody and the repository-coverage route with coverageBody, and points
// the command's client at both.
func serveCredentialAndCoverage(t *testing.T, detailBody string, coverageBody string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/repositories") {
			_, _ = responseWriter.Write([]byte(coverageBody))
			return
		}
		_, _ = responseWriter.Write([]byte(detailBody))
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

const credentialDetailIdentifier = "11111111-1111-4111-8111-111111111111"

// runCredentialsGet drives the get command and captures what it printed; the
// command prints with fmt.Printf like the rest of this package, so stdout is
// what has to be captured.
func runCredentialsGet(t *testing.T, credentialID string) string {
	t.Helper()
	var runError error
	output := captureStdout(t, func() {
		runError = credentialsGetCmd.RunE(credentialsGetCmd, []string{credentialID})
	})
	if runError != nil {
		t.Fatalf("credentials get returned an error: %v", runError)
	}
	return output
}

func TestCredentialsGetExplainsAnUnusableCredential(t *testing.T) {
	serveCredentialAndCoverage(t,
		`{"id":"`+credentialDetailIdentifier+`","name":"github-acme","provider":"github",`+
			`"created_at":"2026-09-01T00:00:00Z","organisation_id":"org","available":false}`,
		`{"coverage":"incomplete","coverage_message":"The GitHub App installation behind this credential cannot reach acme/other.",`+
			`"accessible_repositories":["acme/payments"],"accessible_repositories_complete":true}`)

	output := runCredentialsGet(t, credentialDetailIdentifier)
	if !strings.Contains(output, "Usable:   no") {
		t.Errorf("output does not say the credential is unusable:\n%s", output)
	}
	if !strings.Contains(output, "cannot reach acme/other") {
		t.Errorf("output does not carry the reason:\n%s", output)
	}
	if !strings.Contains(output, "ankra credentials repositories github-acme") {
		t.Errorf("output does not point at the repository listing:\n%s", output)
	}
}

func TestCredentialsGetSaysAUsableCredentialIsUsable(t *testing.T) {
	serveCredentialAndCoverage(t,
		`{"id":"`+credentialDetailIdentifier+`","name":"github-acme","provider":"github",`+
			`"created_at":"2026-09-01T00:00:00Z","organisation_id":"org","available":true}`,
		`{"coverage":"complete","coverage_message":""}`)

	output := runCredentialsGet(t, credentialDetailIdentifier)
	if !strings.Contains(output, "Usable:   yes") {
		t.Errorf("output does not say the credential is usable:\n%s", output)
	}
	if strings.Contains(output, "Reason:") {
		t.Errorf("a usable credential needs no reason line:\n%s", output)
	}
}

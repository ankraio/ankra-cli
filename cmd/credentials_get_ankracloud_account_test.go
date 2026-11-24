package cmd

// The credential detail view's Ankra Cloud account lines: which Ankra Cloud
// account an Ankra Cloud credential is connected to, or why the platform
// could not say.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ankra/internal/client"
)

const ankraCloudCredentialDetailBody = `{"id":"` + credentialDetailIdentifier + `","name":"ankra-cloud","provider":"ankracloud",` +
	`"created_at":"2026-10-04T22:06:16Z","organisation_id":"7b2fedb6-4d79-45fd-b71a-044a2cfddce7","available":true`

func TestCredentialsGetNamesTheAnkraCloudAccount(t *testing.T) {
	serveCredentialAndCoverage(t, ankraCloudCredentialDetailBody+
		`,"ankra_cloud_account":{"id":"01a108f4-16f0-7ada-84bb-9ef22be40664","name":"demo",`+
		`"organisation":{"id":"7b2fedb6-4d79-45fd-b71a-044a2cfddce7","name":"demo"}}}`, `{}`)

	output := runCredentialsGet(t, credentialDetailIdentifier)
	for _, expected := range []string{
		"  Ankra Cloud account: demo (01a108f4-16f0-7ada-84bb-9ef22be40664)\n",
		"  Ankra Cloud account organisation: demo (7b2fedb6-4d79-45fd-b71a-044a2cfddce7)\n",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output does not carry %q:\n%s", expected, output)
		}
	}
}

func TestCredentialsGetSaysAnAnkraCloudAccountIsNotLinked(t *testing.T) {
	serveCredentialAndCoverage(t, ankraCloudCredentialDetailBody+
		`,"ankra_cloud_account":{"id":"01a108f4-16f0-7ada-84bb-9ef22be40664","name":"Own","organisation":null}}`, `{}`)

	output := runCredentialsGet(t, credentialDetailIdentifier)
	if !strings.Contains(output, "Ankra Cloud account: Own (01a108f4-16f0-7ada-84bb-9ef22be40664)") ||
		!strings.Contains(output, "Ankra Cloud account organisation: none") {
		t.Errorf("an unlinked account is not described:\n%s", output)
	}
}

// TestCredentialsGetSaysWhyTheAnkraCloudAccountIsUnknown pins that an account
// the platform could not read is reported as unknown with the reason, never
// left out as if the credential had none.
func TestCredentialsGetSaysWhyTheAnkraCloudAccountIsUnknown(t *testing.T) {
	serveCredentialAndCoverage(t, ankraCloudCredentialDetailBody+
		`,"ankra_cloud_account_error":"Ankra Cloud did not say which account this credential belongs to: Ankra Cloud rejected the API token."}`, `{}`)

	output := runCredentialsGet(t, credentialDetailIdentifier)
	if !strings.Contains(output, "Ankra Cloud account: unknown (Ankra Cloud did not say which account this credential belongs to: Ankra Cloud rejected the API token.)") {
		t.Errorf("an unread account does not say why:\n%s", output)
	}
}

func TestCredentialsGetPrintsNoAnkraCloudAccountForOtherCredentials(t *testing.T) {
	serveCredentialAndCoverage(t,
		`{"id":"`+credentialDetailIdentifier+`","name":"hetzner-prod","provider":"hetzner",`+
			`"created_at":"2026-09-01T00:00:00Z","organisation_id":"org","available":true}`, `{}`)

	output := runCredentialsGet(t, credentialDetailIdentifier)
	if strings.Contains(output, "Ankra Cloud account") {
		t.Errorf("a hetzner credential prints an Ankra Cloud account:\n%s", output)
	}
}

// TestCredentialDetailJSONCarriesTheAnkraCloudAccount pins what `-o json`
// prints: the detail as decoded, so the account survives the round trip.
func TestCredentialDetailJSONCarriesTheAnkraCloudAccount(t *testing.T) {
	var detail client.CredentialDetail
	if decodeError := json.Unmarshal([]byte(ankraCloudCredentialDetailBody+
		`,"ankra_cloud_account":{"id":"01a108f4-16f0-7ada-84bb-9ef22be40664","name":"demo","organisation":null}}`), &detail); decodeError != nil {
		t.Fatal(decodeError)
	}
	encoded, encodeError := json.Marshal(detail)
	if encodeError != nil {
		t.Fatal(encodeError)
	}
	if !strings.Contains(string(encoded), `"ankra_cloud_account":{"id":"01a108f4-16f0-7ada-84bb-9ef22be40664","name":"demo","organisation":null}`) {
		t.Fatalf("-o json loses the account: %s", encoded)
	}
	if strings.Contains(string(encoded), "ankra_cloud_account_error") {
		t.Fatalf("a read account carries an error: %s", encoded)
	}
}

// serveRevealRefusingPlatform answers like the platform does a role holding
// credentials.read but not credentials.reveal: the listing and the account
// read succeed, the credential detail is refused with the RBAC 403. It
// returns the paths the command asked for.
func serveRevealRefusingPlatform(t *testing.T, listBody string, accountStatus int, accountBody string) *[]string {
	t.Helper()
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requested = append(requested, request.URL.Path)
		responseWriter.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/ankra-cloud-account"):
			responseWriter.WriteHeader(accountStatus)
			_, _ = responseWriter.Write([]byte(accountBody))
		case request.URL.Path == "/api/v1/org/credentials":
			_, _ = responseWriter.Write([]byte(listBody))
		default:
			responseWriter.WriteHeader(http.StatusForbidden)
			_, _ = responseWriter.Write([]byte(`{"detail":"permission_denied","permission":"credentials.reveal","scope_type":"organisation"}`))
		}
	}))
	previousClient := apiClient
	previousBaseURL := baseURL
	apiClient = adaptMockClient(client.New("test-token", server.URL))
	baseURL = server.URL
	t.Cleanup(func() {
		apiClient = adaptMockClient(previousClient)
		baseURL = previousBaseURL
		server.Close()
	})
	return &requested
}

const revealRefusedAnkraCloudListBody = `[{"id":"` + credentialDetailIdentifier + `","name":"ankra-cloud","provider":"ankracloud",` +
	`"organisation_id":"7b2fedb6-4d79-45fd-b71a-044a2cfddce7","available":true,"created_at":"2026-10-04T22:06:16Z","syncing":false}]`

// TestCredentialsGetShowsTheAnkraCloudAccountToARoleThatCannotReveal pins
// what a member who may read credentials but not reveal them sees: the
// listing's facts and the Ankra Cloud account, instead of the reveal refusal.
func TestCredentialsGetShowsTheAnkraCloudAccountToARoleThatCannotReveal(t *testing.T) {
	requested := serveRevealRefusingPlatform(t, revealRefusedAnkraCloudListBody, http.StatusOK,
		`{"credential_id":"`+credentialDetailIdentifier+`","account":{"id":"01a108f4-16f0-7ada-84bb-9ef22be40664","name":"demo",`+
			`"organisation":{"id":"7b2fedb6-4d79-45fd-b71a-044a2cfddce7","name":"demo"}},"account_error":null}`)

	output := runCredentialsGet(t, credentialDetailIdentifier)
	for _, expected := range []string{
		"  Name:     ankra-cloud\n",
		"  Provider: ankracloud\n",
		"  Ankra Cloud account: demo (01a108f4-16f0-7ada-84bb-9ef22be40664)\n",
		"  Ankra Cloud account organisation: demo (7b2fedb6-4d79-45fd-b71a-044a2cfddce7)\n",
		"  Usable:   yes\n",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output does not carry %q:\n%s", expected, output)
		}
	}
	if !strings.Contains(strings.Join(*requested, " "), "/api/v1/org/credentials/"+credentialDetailIdentifier+"/ankra-cloud-account") {
		t.Errorf("the account read was not asked: %v", *requested)
	}
}

func TestCredentialsGetSaysWhyTheAccountIsUnknownToARoleThatCannotReveal(t *testing.T) {
	serveRevealRefusingPlatform(t, revealRefusedAnkraCloudListBody, http.StatusOK,
		`{"credential_id":"`+credentialDetailIdentifier+`","account":null,`+
			`"account_error":"Ankra Cloud did not say which account this credential belongs to: Ankra Cloud rejected the API token."}`)

	output := runCredentialsGet(t, credentialDetailIdentifier)
	if !strings.Contains(output, "Ankra Cloud account: unknown (Ankra Cloud did not say which account this credential belongs to: Ankra Cloud rejected the API token.)") {
		t.Errorf("an unread account does not say why:\n%s", output)
	}

	serveRevealRefusingPlatform(t, revealRefusedAnkraCloudListBody, http.StatusInternalServerError, `{"detail":"boom"}`)
	output = runCredentialsGet(t, credentialDetailIdentifier)
	if !strings.Contains(output, "Ankra Cloud account: unknown (could not be read:") {
		t.Errorf("a failed account read does not say so:\n%s", output)
	}
}

// TestCredentialsGetKeepsTheRevealRefusalForACredentialTheListingLacks pins
// that the fallback never invents a credential: an id the listing does not
// carry still fails with the platform's refusal.
func TestCredentialsGetKeepsTheRevealRefusalForACredentialTheListingLacks(t *testing.T) {
	serveRevealRefusingPlatform(t, `[]`, http.StatusOK, `{}`)
	var runError error
	captureStdout(t, func() {
		runError = credentialsGetCmd.RunE(credentialsGetCmd, []string{credentialDetailIdentifier})
	})
	if runError == nil || !strings.Contains(runError.Error(), "credentials.reveal") {
		t.Fatalf("error = %v, want the reveal refusal", runError)
	}
}

// serveCredentialDetailAndAccount answers the credential detail with
// detailStatus and detailBody, the account read with accountBody, and the
// listing with listStatus and listBody. It returns the paths asked for.
func serveCredentialDetailAndAccount(t *testing.T, detailStatus int, detailBody string, accountBody string, listStatus int, listBody string) *[]string {
	t.Helper()
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requested = append(requested, request.URL.Path)
		responseWriter.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/ankra-cloud-account"):
			_, _ = responseWriter.Write([]byte(accountBody))
		case request.URL.Path == "/api/v1/org/credentials":
			responseWriter.WriteHeader(listStatus)
			_, _ = responseWriter.Write([]byte(listBody))
		default:
			responseWriter.WriteHeader(detailStatus)
			_, _ = responseWriter.Write([]byte(detailBody))
		}
	}))
	previousClient := apiClient
	previousBaseURL := baseURL
	apiClient = adaptMockClient(client.New("test-token", server.URL))
	baseURL = server.URL
	t.Cleanup(func() {
		apiClient = adaptMockClient(previousClient)
		baseURL = previousBaseURL
		server.Close()
	})
	return &requested
}

// TestCredentialsGetAsksTheAccountReadBesideTheDetail pins that a role that
// may reveal the credential gets the account too: the detail never asks
// Ankra Cloud, so the command reads the account beside it.
func TestCredentialsGetAsksTheAccountReadBesideTheDetail(t *testing.T) {
	requested := serveCredentialDetailAndAccount(t, http.StatusOK, ankraCloudCredentialDetailBody+`}`,
		`{"credential_id":"`+credentialDetailIdentifier+`","account":{"id":"01a108f4-16f0-7ada-84bb-9ef22be40664","name":"demo","organisation":null},"account_error":null}`,
		http.StatusOK, `[]`)

	output := runCredentialsGet(t, credentialDetailIdentifier)
	if !strings.Contains(output, "  Ankra Cloud account: demo (01a108f4-16f0-7ada-84bb-9ef22be40664)\n") ||
		!strings.Contains(output, "Ankra Cloud account organisation: none") {
		t.Errorf("the account is not shown beside the detail:\n%s", output)
	}
	if !strings.Contains(strings.Join(*requested, " "), "/ankra-cloud-account") {
		t.Errorf("the account read was not asked: %v", *requested)
	}
}

// TestCredentialsGetReportsAnEmptyAccountAnswerAsUnknown pins that an answer
// naming neither an account nor a reason is an unknown account, never a
// credential without one.
func TestCredentialsGetReportsAnEmptyAccountAnswerAsUnknown(t *testing.T) {
	serveCredentialDetailAndAccount(t, http.StatusOK, ankraCloudCredentialDetailBody+`}`,
		`{"credential_id":"`+credentialDetailIdentifier+`","account":null,"account_error":null}`, http.StatusOK, `[]`)

	output := runCredentialsGet(t, credentialDetailIdentifier)
	if !strings.Contains(output, "Ankra Cloud account: unknown (the platform returned no account and no reason)") {
		t.Errorf("an empty account answer is not reported as unknown:\n%s", output)
	}
}

// TestCredentialsGetSaysWhenTheListingFallbackFails pins that a reveal
// refusal whose listing fallback fails names both: the refusal, and that
// the listing could not be read.
func TestCredentialsGetSaysWhenTheListingFallbackFails(t *testing.T) {
	serveCredentialDetailAndAccount(t, http.StatusForbidden,
		`{"detail":"permission_denied","permission":"credentials.reveal","scope_type":"organisation"}`, `{}`,
		http.StatusInternalServerError, `{"detail":"boom"}`)

	var runError error
	captureStdout(t, func() {
		runError = credentialsGetCmd.RunE(credentialsGetCmd, []string{credentialDetailIdentifier})
	})
	if runError == nil || !strings.Contains(runError.Error(), "credentials.reveal") ||
		!strings.Contains(runError.Error(), "reading it from the credential listing instead failed too") {
		t.Fatalf("error = %v, want the refusal and the listing failure", runError)
	}
	var refusal *client.PermissionDeniedError
	if !errors.As(runError, &refusal) {
		t.Fatalf("the refusal no longer classifies the error: %v", runError)
	}
}

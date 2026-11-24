package cmd

// The credential detail view's Ankra Cloud account lines: which Ankra Cloud
// account an Ankra Cloud credential is connected to, or why the platform
// could not say.

import (
	"encoding/json"
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

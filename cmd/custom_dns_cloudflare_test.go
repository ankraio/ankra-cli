package cmd

// The Cloudflare arm of the custom DNS zone verbs (ankra-zesx5.10): the
// --cloudflare-credential flag sends provider "cloudflare" so the platform
// refuses anything that is not an organisation Cloudflare credential, the
// publishing line reports the upsert-only policy, and the listing shows the
// provider and policy of every zone.

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// resetCustomDNSFlags clears the package-level flag variables before and
// after a test: the older tests in this package set them and never clear
// them, so a value left by one would make --credential and
// --cloudflare-credential collide in the next.
func resetCustomDNSFlags(t *testing.T) {
	t.Helper()
	clearFlags := func() {
		clusterCustomDNSZone, clusterCustomDNSCredential, clusterCustomDNSCloudflareCredential = "", "", ""
		orgCustomDNSZone, orgCustomDNSCredential, orgCustomDNSCloudflareCredential = "", "", ""
	}
	clearFlags()
	t.Cleanup(clearFlags)
}

func TestClusterCustomDNSAddWithACloudflareCredentialAsksForTheCloudflareProvider(t *testing.T) {
	resetCustomDNSFlags(t)
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["POST /api/v1/clusters/"+customDNSClusterID+"/custom-dns-zones"] =
		`{"success":true,"zone":"ankra.cloud","credential_name":"ankra-cloudflare","provider":"cloudflare",
		  "policy":"upsert-only","cloudflare_zone":"ankra.cloud","dns_edit_access":"granted"}`

	clusterCustomDNSZone = "ankra.cloud"
	clusterCustomDNSCloudflareCredential = "ankra-cloudflare"
	output := captureStdout(t, func() {
		if runError := clusterCustomDNSAddCmd.RunE(clusterCustomDNSAddCmd,
			[]string{customDNSClusterID}); runError != nil {
			t.Fatalf("add returned an error: %v", runError)
		}
	})
	if recorder.lastBody["provider"] != "cloudflare" || recorder.lastBody["credential_name"] != "ankra-cloudflare" {
		t.Fatalf("request body = %v, want the cloudflare provider and credential", recorder.lastBody)
	}
	for _, want := range []string{"cloudflare provider", "upsert-only", "never changes or deletes", "Cloudflare zone ankra.cloud"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want it to mention %q", output, want)
		}
	}
}

// --credential keeps its old body exactly: no provider field, so an older
// platform sees the request it always knew.
func TestClusterCustomDNSAddWithAPlainCredentialSendsNoProvider(t *testing.T) {
	resetCustomDNSFlags(t)
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["POST /api/v1/clusters/"+customDNSClusterID+"/custom-dns-zones"] =
		`{"success":true,"zone":"launch.example.com","credential_name":"example-dns"}`

	clusterCustomDNSZone = "launch.example.com"
	clusterCustomDNSCredential = "example-dns"
	_ = captureStdout(t, func() {
		if runError := clusterCustomDNSAddCmd.RunE(clusterCustomDNSAddCmd,
			[]string{customDNSClusterID}); runError != nil {
			t.Fatalf("add returned an error: %v", runError)
		}
	})
	if _, hasProvider := recorder.lastBody["provider"]; hasProvider {
		t.Fatalf("request body = %v, a plain --credential must not send a provider", recorder.lastBody)
	}
}

func TestClusterCustomDNSAddRefusesBothOrNeitherCredentialFlag(t *testing.T) {
	resetCustomDNSFlags(t)
	clusterCustomDNSZone = "ankra.cloud"
	clusterCustomDNSCredential = "a"
	clusterCustomDNSCloudflareCredential = "b"
	runError := clusterCustomDNSAddCmd.RunE(clusterCustomDNSAddCmd, []string{customDNSClusterID})
	var coded *codedError
	if !errors.As(runError, &coded) || coded.code != exitUsage {
		t.Fatalf("both flags must be a usage error, got %v", runError)
	}
	clusterCustomDNSCredential, clusterCustomDNSCloudflareCredential = "", ""
	runError = clusterCustomDNSAddCmd.RunE(clusterCustomDNSAddCmd, []string{customDNSClusterID})
	if !errors.As(runError, &coded) || coded.code != exitUsage || !strings.Contains(runError.Error(), "--cloudflare-credential") {
		t.Fatalf("no credential must be a usage error naming both flags, got %v", runError)
	}
}

// The platform's Cloudflare refusals are explanations; the CLI surfaces them
// verbatim.
func TestClusterCustomDNSAddSurfacesACloudflareRefusal(t *testing.T) {
	resetCustomDNSFlags(t)
	recorder := newCustomDNSServer(t)
	recorder.refusalStatus = 400
	recorder.refusalDetail = `The Cloudflare credential "ankra-cloudflare" cannot reach ankra.cloud: no Cloudflare zone its API token can see holds it.`
	clusterCustomDNSZone = "ankra.cloud"
	clusterCustomDNSCloudflareCredential = "ankra-cloudflare"
	runError := clusterCustomDNSAddCmd.RunE(clusterCustomDNSAddCmd, []string{customDNSClusterID})
	if runError == nil || !strings.Contains(runError.Error(), "cannot reach ankra.cloud") {
		t.Fatalf("error = %v, want the platform's refusal surfaced", runError)
	}
}

func TestClusterCustomDNSListShowsProviderAndPolicy(t *testing.T) {
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["GET /api/v1/clusters/"+customDNSClusterID+"/custom-dns-zones"] = `{"success":true,"zones":[
		{"zone":"ankra.cloud","credential_name":"ankra-cloudflare","source":"cluster","provider":"cloudflare","policy":"upsert-only"},
		{"zone":"gone.example","credential_name":"deleted","source":"cluster","provider":null,"policy":null}]}`
	output := captureStdout(t, func() {
		if runError := clusterCustomDNSListCmd.RunE(clusterCustomDNSListCmd,
			[]string{customDNSClusterID}); runError != nil {
			t.Fatalf("list returned an error: %v", runError)
		}
	})
	for _, want := range []string{"PROVIDER", "POLICY", "cloudflare", "upsert-only"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want it to mention %q", output, want)
		}
	}
}

func TestOrgCustomDNSAddWithACloudflareCredentialAsksForTheCloudflareProvider(t *testing.T) {
	resetCustomDNSFlags(t)
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["POST /api/v1/org/custom-dns-zones"] =
		`{"success":true,"zone":"ankra.cloud","credential_name":"ankra-cloudflare","provider":"cloudflare","policy":"upsert-only"}`
	orgCustomDNSZone = "ankra.cloud"
	orgCustomDNSCloudflareCredential = "ankra-cloudflare"
	output := captureStdout(t, func() {
		if runError := orgCustomDNSAddCmd.RunE(orgCustomDNSAddCmd, nil); runError != nil {
			t.Fatalf("add returned an error: %v", runError)
		}
	})
	if recorder.lastBody["provider"] != "cloudflare" {
		t.Fatalf("request body = %v, want the cloudflare provider", recorder.lastBody)
	}
	if !strings.Contains(output, "upsert-only") {
		t.Fatalf("output = %q, want the policy reported", output)
	}
}

// ankra-meh8u: a credential whose token the platform no longer holds lists
// as needs-reconnect with the command that repairs it.
func TestCloudflareCredentialsListShowsNeedsReconnectAndTheCommand(t *testing.T) {
	var out bytes.Buffer
	renderError := renderCloudflareCredentials(&out, []client.CloudflareCredential{
		{Name: "avura-cutover", State: "needs-reconnect", NeedsReconnect: true,
			Detail: "The Cloudflare credential \"avura-cutover\" needs reconnecting: Ankra no longer holds its API token. Reconnect it with `ankra org cloudflare connect avura-cutover` (or from the Credentials page)."},
		{Name: "healthy", State: "up"},
	}, "")
	if renderError != nil {
		t.Fatalf("render: %v", renderError)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "needs-reconnect") || !strings.Contains(rendered, "ankra org cloudflare connect avura-cutover") {
		t.Fatalf("output = %q", rendered)
	}
}

package cmd

// The custom DNS zone verbs. The platform half (cluster#1804) renders one
// isolated external-dns per declared zone; these pin the CLI's contract with
// it: the request shapes, the refusal details surfaced verbatim, and the one
// secrecy property the credential lane has - the webhook URL goes in and
// never comes back out.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ankra/internal/client"
)

const customDNSClusterID = "22222222-2222-4222-8222-222222222222"

// serveCustomDNS answers the custom-dns-zones and dns-credential routes with
// scripted bodies and records what the CLI sent.
type customDNSServer struct {
	server        *httptest.Server
	lastMethod    string
	lastPath      string
	lastBody      map[string]any
	zonesByPath   map[string]string
	refusalDetail string
	refusalStatus int
}

func newCustomDNSServer(t *testing.T) *customDNSServer {
	t.Helper()
	recorder := &customDNSServer{zonesByPath: map[string]string{}}
	recorder.server = httptest.NewServer(http.HandlerFunc(
		func(responseWriter http.ResponseWriter, request *http.Request) {
			recorder.lastMethod = request.Method
			recorder.lastPath = request.URL.Path
			recorder.lastBody = nil
			if request.Body != nil {
				raw, _ := io.ReadAll(request.Body)
				if len(raw) > 0 {
					decoded := map[string]any{}
					if json.Unmarshal(raw, &decoded) == nil {
						recorder.lastBody = decoded
					}
				}
			}
			if recorder.refusalStatus != 0 {
				responseWriter.Header().Set("Content-Type", "application/json")
				responseWriter.WriteHeader(recorder.refusalStatus)
				_ = json.NewEncoder(responseWriter).Encode(map[string]string{"detail": recorder.refusalDetail})
				return
			}
			responseWriter.Header().Set("Content-Type", "application/json")
			body, isScripted := recorder.zonesByPath[request.Method+" "+request.URL.Path]
			if !isScripted {
				responseWriter.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(responseWriter).Encode(map[string]string{"detail": "unscripted route in test"})
				return
			}
			_, _ = responseWriter.Write([]byte(body))
		}))
	previousClient := apiClient
	previousBaseURL := baseURL
	apiClient = client.New("test-token", recorder.server.URL)
	baseURL = recorder.server.URL
	t.Cleanup(func() {
		apiClient = previousClient
		baseURL = previousBaseURL
		recorder.server.Close()
	})
	return recorder
}

func TestClusterCustomDNSListRendersZonesAndTheEmptyAnswer(t *testing.T) {
	recorder := newCustomDNSServer(t)
	listPath := "GET /api/v1/clusters/" + customDNSClusterID + "/custom-dns-zones"
	recorder.zonesByPath[listPath] = `{"success":true,"zones":[
		{"zone":"launch.example.com","credential_name":"example-dns"},
		{"zone":"shop.example.net","credential_name":"example-dns"}]}`

	output := captureStdout(t, func() {
		if runError := clusterCustomDNSListCmd.RunE(clusterCustomDNSListCmd,
			[]string{customDNSClusterID}); runError != nil {
			t.Fatalf("list returned an error: %v", runError)
		}
	})
	for _, want := range []string{"launch.example.com", "shop.example.net", "example-dns"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want it to mention %q", output, want)
		}
	}

	recorder.zonesByPath[listPath] = `{"success":true,"zones":[]}`
	output = captureStdout(t, func() {
		if runError := clusterCustomDNSListCmd.RunE(clusterCustomDNSListCmd,
			[]string{customDNSClusterID}); runError != nil {
			t.Fatalf("empty list returned an error: %v", runError)
		}
	})
	if !strings.Contains(output, "No custom DNS zones declared") {
		t.Fatalf("output = %q, want the empty answer stated, not a bare table", output)
	}
}

func TestClusterCustomDNSAddSendsTheBindingAndReportsIt(t *testing.T) {
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["POST /api/v1/clusters/"+customDNSClusterID+"/custom-dns-zones"] =
		`{"success":true,"zone":"launch.example.com","credential_name":"example-dns"}`

	clusterCustomDNSZone = "Launch.Example.com"
	clusterCustomDNSCredential = "example-dns"
	output := captureStdout(t, func() {
		if runError := clusterCustomDNSAddCmd.RunE(clusterCustomDNSAddCmd,
			[]string{customDNSClusterID}); runError != nil {
			t.Fatalf("add returned an error: %v", runError)
		}
	})

	if recorder.lastBody["zone"] != "Launch.Example.com" || recorder.lastBody["credential_name"] != "example-dns" {
		t.Fatalf("request body = %v, want the zone and credential as given (the platform normalises)", recorder.lastBody)
	}
	// The reported zone is the platform's normalised answer, not the input.
	if !strings.Contains(output, "launch.example.com") {
		t.Fatalf("output = %q, want the normalised zone reported back", output)
	}
}

// The refusals are the platform's, and the CLI's job is to surface them
// verbatim - an overlap with the delegated zone is an explanation, not a
// generic failure.
func TestClusterCustomDNSAddSurfacesTheRefusalDetail(t *testing.T) {
	recorder := newCustomDNSServer(t)
	recorder.refusalStatus = http.StatusBadRequest
	recorder.refusalDetail = "the zone overlaps the delegated zone Ankra already serves for this cluster"

	clusterCustomDNSZone = "sub.of.delegated.zone"
	clusterCustomDNSCredential = "example-dns"
	runError := clusterCustomDNSAddCmd.RunE(clusterCustomDNSAddCmd, []string{customDNSClusterID})
	if runError == nil {
		t.Fatal("a refused declaration must return an error")
	}
	if !strings.Contains(runError.Error(), "overlaps the delegated zone") {
		t.Fatalf("error = %q, want the platform's refusal detail surfaced", runError)
	}
}

func TestClusterCustomDNSRemoveEscapesTheZoneAndReportsTheWithdrawal(t *testing.T) {
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["DELETE /api/v1/clusters/"+customDNSClusterID+"/custom-dns-zones/launch.example.com"] =
		`{"success":true,"zone":"launch.example.com"}`

	clusterCustomDNSZone = "launch.example.com"
	output := captureStdout(t, func() {
		if runError := clusterCustomDNSRemoveCmd.RunE(clusterCustomDNSRemoveCmd,
			[]string{customDNSClusterID}); runError != nil {
			t.Fatalf("remove returned an error: %v", runError)
		}
	})
	if !strings.Contains(output, "withdrawn") || !strings.Contains(output, "records are untouched") {
		t.Fatalf("output = %q, want the withdrawal and the records-untouched promise stated", output)
	}
}

func TestOrgDnsCredentialCreateNeverEchoesTheWebhookURL(t *testing.T) {
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["POST /api/v1/credentials/dns"] =
		`{"success":true,"id":"cred-1","name":"example-dns"}`

	orgDnsCredentialName = "example-dns"
	orgDnsCredentialWebhookURL = "https://dns.example.com/api/external-dns/secret-token-value"
	output := captureStdout(t, func() {
		if runError := orgDnsCredentialsCreateCmd.RunE(orgDnsCredentialsCreateCmd, nil); runError != nil {
			t.Fatalf("create returned an error: %v", runError)
		}
	})

	if recorder.lastBody["webhook_provider_url"] != orgDnsCredentialWebhookURL {
		t.Fatalf("request body = %v, want the webhook url sent", recorder.lastBody)
	}
	if strings.Contains(output, "secret-token-value") {
		t.Fatalf("output = %q, the webhook url embeds the token and must never be printed", output)
	}
	if !strings.Contains(output, "will not be shown again") {
		t.Fatalf("output = %q, want the secrecy stated so the operator saves it", output)
	}
}

func TestOrgDnsCredentialListRendersNamesOnly(t *testing.T) {
	recorder := newCustomDNSServer(t)
	recorder.zonesByPath["GET /api/v1/credentials/dns"] =
		`[{"id":"cred-1","name":"example-dns","provider":"dns","created_at":"2026-08-24T10:00:00Z"}]`

	output := captureStdout(t, func() {
		if runError := orgDnsCredentialsListCmd.RunE(orgDnsCredentialsListCmd, nil); runError != nil {
			t.Fatalf("list returned an error: %v", runError)
		}
	})
	if !strings.Contains(output, "example-dns") {
		t.Fatalf("output = %q, want the credential named", output)
	}
}

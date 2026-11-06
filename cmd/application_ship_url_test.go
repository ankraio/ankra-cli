package cmd

// Tests for the public check behind ship's Live line (ankra-oznf7k.26): a route
// that holds inside the cluster is not a URL that answers.

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func shipPendingMock() *applicationShipMock {
	mockClient := newApplicationShipMock()
	mockClient.deploymentsPayload = []byte(`{"deployments":[{"cluster_id":"` + shipTestClusterID +
		`","namespace":"shop","ingress_host":"shop.acme.ankra.cc","ingress_host_publication":"publisher_present"}]}`)
	mockClient.ingressItems = []interface{}{shipTestIngress("shop", "shop.acme.ankra.cc", "shop", 80)}
	mockClient.serviceItems = []interface{}{shipTestService("shop", 80)}
	return mockClient
}

func TestApplicationShipDoesNotClaimLiveBeforeTheURLAnswers(t *testing.T) {
	fastShipPolling(t)
	previousWindow := shipURLAnswerWindow
	shipURLAnswerWindow = 0
	t.Cleanup(func() { shipURLAnswerWindow = previousWindow })
	shipURLProbe = func(context.Context, string) string { return "its certificate is not issued yet" }

	output, progress, executeError := runApplicationShipCommand(t, shipPendingMock(), "--cluster", "production")
	if executeError != nil {
		t.Fatalf("a healthy deploy whose URL is pending is not a failure: %v", executeError)
	}
	if strings.Contains(output, "Live:") {
		t.Errorf("stdout must not claim Live: %q", output)
	}
	for _, want := range []string{"https://shop.acme.ankra.cc", "its certificate is not issued yet"} {
		if !strings.Contains(output, want) {
			t.Errorf("stdout must contain %q: %q", want, output)
		}
	}
	if !strings.Contains(progress, "Waiting for https://shop.acme.ankra.cc to answer") {
		t.Errorf("progress must say what ship waits for: %q", progress)
	}
}

func TestApplicationShipClaimsLiveOnceTheURLAnswers(t *testing.T) {
	fastShipPolling(t)
	attempts := 0
	shipURLProbe = func(context.Context, string) string {
		attempts++
		if attempts < 3 {
			return "nothing answers it from this machine yet"
		}
		return ""
	}

	output, _, executeError := runApplicationShipCommand(t, shipPendingMock(), "--cluster", "production")
	if executeError != nil {
		t.Fatalf("ship failed: %v", executeError)
	}
	if !strings.Contains(output, "Live: https://shop.acme.ankra.cc") {
		t.Errorf("stdout must claim Live once it answered: %q", output)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestApplicationShipReportsAPendingURLInJSON(t *testing.T) {
	fastShipPolling(t)
	previousWindow := shipURLAnswerWindow
	shipURLAnswerWindow = 0
	t.Cleanup(func() { shipURLAnswerWindow = previousWindow })
	shipURLProbe = func(context.Context, string) string { return "its hostname does not resolve yet" }

	output, _, executeError := runApplicationShipCommand(t, shipPendingMock(), "--cluster", "production", "-o", "json")
	if executeError != nil {
		t.Fatalf("ship failed: %v", executeError)
	}
	var result shipResult
	if decodeError := json.Unmarshal([]byte(output), &result); decodeError != nil {
		t.Fatalf("decoding %q: %v", output, decodeError)
	}
	if result.State != shipStateURLPending || result.URL != "" ||
		result.PendingURL != "https://shop.acme.ankra.cc" || result.URLPending != "its hostname does not resolve yet" {
		t.Errorf("result = %+v", result)
	}
}

func TestProbeShipURLNamesWhatIsPending(t *testing.T) {
	serverWithStatus := func(status int) *httptest.Server {
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(status)
		}))
		t.Cleanup(server.Close)
		return server
	}
	if reason := probeShipURL(context.Background(), serverWithStatus(http.StatusOK).URL); !strings.Contains(reason, "certificate") {
		t.Errorf("an unverified certificate must read as not issued, got %q", reason)
	}
	if reason := shipURLPendingReason(&net.DNSError{Err: "no such host", Name: "shop.acme.ankra.cc"}); !strings.Contains(reason, "resolve") {
		t.Errorf("a DNS failure must name DNS, got %q", reason)
	}
	if reason := shipURLPendingReason(&tls.CertificateVerificationError{Err: errors.New("expired")}); !strings.Contains(reason, "certificate") {
		t.Errorf("a verification failure must name the certificate, got %q", reason)
	}
	if reason := shipURLPendingReason(errors.New("connection refused")); !strings.Contains(reason, "this machine") {
		t.Errorf("anything else must say it is seen from this machine, got %q", reason)
	}
}

func TestWaitForShipURLStopsWhenTheContextExpires(t *testing.T) {
	previousProbe, previousSleep := shipURLProbe, shipEndpointSettleSleep
	t.Cleanup(func() { shipURLProbe, shipEndpointSettleSleep = previousProbe, previousSleep })
	shipURLProbe = func(context.Context, string) string { return "pending" }
	shipEndpointSettleSleep = func(context.Context, time.Duration) bool { return false }
	var progress bytes.Buffer
	if reason := waitForShipURLToAnswer(context.Background(), &progress, "https://shop.acme.ankra.cc"); reason != "pending" {
		t.Errorf("reason = %q", reason)
	}
}

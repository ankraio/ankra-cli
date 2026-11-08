package cmd

// The public check behind ship's Live line (ankra-oznf7k.26). A route that
// holds inside the cluster is not a URL that answers: a certificate still being
// issued left a direct Ingress returning nothing for three minutes after ship
// had printed Live. So before it claims Live, ship asks the URL itself over
// HTTPS, for a bounded window.
//
// An unanswered URL is pending, never a failed deploy. The deploy is healthy,
// the answer depends on this machine's network as well (an IPv6-only address
// cannot answer a machine with no IPv6), and the usual cause clears by itself.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// shipStateURLPending is the State of a healthy deploy whose URL did not answer
// inside the window.
const shipStateURLPending = "url_pending"

// shipURLProbeTimeout bounds one request; a load balancer that answers nothing
// costs this much per attempt.
const shipURLProbeTimeout = 10 * time.Second

// Variables so tests can inject the probe and finish instantly.
var (
	shipURLAnswerWindow   = 5 * time.Minute
	shipURLAnswerInterval = 5 * time.Second
	shipURLProbe          = probeShipURL
)

// shipURLHTTPClient is shared by every probe so the attempts reuse one
// transport and its connections. Redirects are not followed, because the first
// response already proves the URL answers.
var shipURLHTTPClient = &http.Client{
	Timeout: shipURLProbeTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// probeShipURL answers "" when the URL serves a response over verified HTTPS,
// and otherwise what is still pending in the reader's terms.
func probeShipURL(probeContext context.Context, url string) string {
	request, requestError := http.NewRequestWithContext(probeContext, http.MethodGet, url, nil)
	if requestError != nil {
		return "the URL could not be requested: " + requestError.Error()
	}
	response, responseError := shipURLHTTPClient.Do(request)
	if responseError != nil {
		return shipURLPendingReason(responseError)
	}
	defer func() { _ = response.Body.Close() }()
	return shipURLStatusPending(response.StatusCode)
}

// shipURLStatusPending answers "" for a status that shows the application
// answered. A 5xx is not an answer, and neither is a 404: an ingress
// controller answers 404 from its default backend for a host it does not route
// yet, which is the false Live this check exists to catch. Any other 4xx means
// the application is reached and decides for itself what its root does.
func shipURLStatusPending(status int) string {
	if status == http.StatusNotFound {
		return "it answers HTTP 404, which the ingress also answers for a host it does not route yet"
	}
	if status >= http.StatusInternalServerError {
		return fmt.Sprintf("it answers HTTP %d", status)
	}
	return ""
}

// shipURLPendingReason names which stage of reaching the URL failed, since each
// one sends the reader somewhere else.
func shipURLPendingReason(requestError error) string {
	if errors.Is(requestError, context.DeadlineExceeded) || errors.Is(requestError, context.Canceled) {
		return "the wait ended before it answered"
	}
	var dnsError *net.DNSError
	if errors.As(requestError, &dnsError) {
		return "its hostname does not resolve yet"
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var certificateInvalid x509.CertificateInvalidError
	var certificateVerification *tls.CertificateVerificationError
	if errors.As(requestError, &unknownAuthority) || errors.As(requestError, &hostnameError) ||
		errors.As(requestError, &certificateInvalid) || errors.As(requestError, &certificateVerification) {
		return "its certificate is not issued yet"
	}
	return "nothing answers it from this machine yet"
}

// waitForShipURLToAnswer probes until the URL answers or the window closes, and
// returns what was still pending at the last attempt, or "" once it answered.
func waitForShipURLToAnswer(waitContext context.Context, progress io.Writer, url string) string {
	deadline := time.Now().Add(shipURLAnswerWindow)
	lastReason := ""
	for {
		reason := shipURLProbe(waitContext, url)
		if reason == "" {
			return ""
		}
		if reason != lastReason {
			_, _ = fmt.Fprintf(progress, "Waiting for %s to answer: %s.\n", url, reason)
			lastReason = reason
		}
		if !time.Now().Before(deadline) || !shipEndpointSettleSleep(waitContext, shipURLAnswerInterval) {
			return lastReason
		}
	}
}

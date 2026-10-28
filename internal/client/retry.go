package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Transient platform hiccups (a stalled database primary, a rolling
// restart, a dropped connection) should not fail read-only CLI calls
// outright: scripts and deploy pipelines otherwise turn a seconds-long
// blip into a hard failure. On 2026-07-14 a ~5-minute platform write
// stall failed a production rollout because every cluster-listing
// attempt died with "http2: timeout awaiting response headers".
// retryTransport retries idempotent requests (bodyless GET/HEAD) a
// couple of times with a short backoff; writes are never retried here —
// callers own their own write retry semantics.
type retryTransport struct {
	base http.RoundTripper
}

// retryAttempts is the total number of tries for an idempotent request.
const retryAttempts = 3

// retryBackoffSchedule is the wait before the second and third attempts.
// A package variable so tests can shrink it.
var retryBackoffSchedule = []time.Duration{time.Second, 2 * time.Second}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !requestIsRetryable(req) {
		return t.base.RoundTrip(req)
	}

	var lastError error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		if attempt > 1 {
			fmt.Fprintf(os.Stderr, "Warning: %s %s failed with a transient error (%v); retrying (attempt %d/%d)\n",
				req.Method, req.URL.Path, lastError, attempt, retryAttempts)
			backoff := retryBackoffSchedule[min(attempt-2, len(retryBackoffSchedule)-1)]
			if !waitForRetry(req.Context(), backoff) {
				break
			}
		}

		response, requestError := t.base.RoundTrip(req)
		if requestError != nil {
			if req.Context().Err() != nil || !isTransientRequestError(requestError) {
				return nil, requestError
			}
			lastError = requestError
			continue
		}
		// Gateway-style statuses mean the platform edge answered but the
		// backend did not; retry those, but hand the caller the real
		// response when the budget is spent so the status is surfaced.
		if responseIsRetryable(response) && attempt < retryAttempts {
			lastError = fmt.Errorf("unexpected status %s", response.Status)
			drainAndClose(response)
			continue
		}
		return response, nil
	}
	return nil, lastError
}

// requestIsRetryable limits retries to requests that are safe to replay:
// idempotent methods that carry no body.
func requestIsRetryable(req *http.Request) bool {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return false
	}
	return req.Body == nil || req.Body == http.NoBody
}

// isTransientRequestError classifies transport-level failures worth a
// retry: timeouts (including http2's "timeout awaiting response
// headers"), connection setup/reset failures, a connection the server
// closed mid-exchange, and HTTP/2 GOAWAY shutdowns. Context
// cancellation is the caller's deadline, never retried.
func isTransientRequestError(requestError error) bool {
	if errors.Is(requestError, context.Canceled) || errors.Is(requestError, context.DeadlineExceeded) {
		return false
	}
	var netError net.Error
	if errors.As(requestError, &netError) && netError.Timeout() {
		return true
	}
	var opError *net.OpError
	if errors.As(requestError, &opError) {
		return true
	}
	if errors.Is(requestError, io.EOF) || errors.Is(requestError, io.ErrUnexpectedEOF) {
		return true
	}
	// The bundled net/http HTTP/2 GOAWAY error type is not exported;
	// match its stable message instead.
	return strings.Contains(requestError.Error(), "GOAWAY")
}

func responseIsRetryable(response *http.Response) bool {
	switch response.StatusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// waitForRetry sleeps for the backoff or returns false early when the
// request's context is done.
func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// drainAndClose discards a bounded amount of a retryable response's body
// and closes it so the underlying connection can be reused.
func drainAndClose(response *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
}

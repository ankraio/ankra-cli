package client

// Long synchronous writes, and what to say when one times out.
//
// Some platform writes do their whole job on the request path inside one
// transaction. Publishing a stack profile draft redacts the draft, derives
// its parameters, inserts the version and moves latest/current before it
// answers, so it can take longer to send response headers than the shared
// client's 30s ResponseHeaderTimeout allows. When it does, the transport
// gives up while the server is still working and the CLI reports
//
//	Error: publishing stack profile draft: request failed: Post
//	"...": http2: timeout awaiting response headers
//
// on work that then completes. That happened publishing
// claude-code-session v2 (ankra-rs107): the profile reached v2 and the
// draft was consumed, and the CLI called it a hard failure.
//
// Two things follow, and this file is both of them.
//
// A client-side timeout on a NON-IDEMPOTENT write is not a failure - it is
// an unknown outcome, and the two must not be reported the same way. The
// natural response to "Error:" is to run the command again, and a second
// publish of a still-open draft mints a SECOND version. So a timeout here
// is wrapped in a WriteOutcomeUnknownError that says the work may have
// landed and names the command that settles it.
//
// Removing the deadline is not enough on its own and not safe on its own:
// a request still ends at the client's overall 5-minute Timeout, a laptop
// still loses its network mid-flight, and either leaves the same unknown
// outcome. The deadline change makes the timeout rare; the error type
// makes it survivable.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// WriteOutcomeUnknownError reports a write whose outcome the CLI could not
// determine: the request was sent, and the answer never arrived. The
// server may or may not have applied it.
//
// It deliberately does not read as a failure. Callers print it, and the
// text has to stop someone from doing the one thing that turns an
// already-succeeded write into a duplicate.
type WriteOutcomeUnknownError struct {
	// Operation is the human phrase for the write, e.g. "publish stack
	// profile draft".
	Operation string
	// VerifyCommand settles the question, e.g.
	// "ankra stack-profiles get <profile>".
	VerifyCommand string
	// RetryConsequence names what a blind retry would do when the write
	// did land, e.g. "publish a second version".
	RetryConsequence string
	Cause            error
}

func (unknownError *WriteOutcomeUnknownError) Error() string {
	message := fmt.Sprintf(
		"%s: the request timed out waiting for a response, but the server may have completed it",
		unknownError.Operation)
	if unknownError.VerifyCommand != "" {
		message += fmt.Sprintf("\n\nCheck before retrying:\n  %s", unknownError.VerifyCommand)
	}
	if unknownError.RetryConsequence != "" {
		message += fmt.Sprintf("\n\nRetrying without checking would %s if the first call did land.",
			unknownError.RetryConsequence)
	}
	if unknownError.Cause != nil {
		message += fmt.Sprintf("\n\nUnderlying error: %v", unknownError.Cause)
	}
	return message
}

func (unknownError *WriteOutcomeUnknownError) Unwrap() error { return unknownError.Cause }

// isTimeoutError reports whether the transport gave up waiting rather than
// receiving an answer.
//
// It matches on behaviour, not on message text. The header deadline
// surfaces as x/net/http2's errTimeout, the overall Client.Timeout as a
// context deadline; both arrive wrapped in *url.Error, which forwards
// Timeout(), so net.Error covers them and a request cancelled by its own
// context is caught alongside.
func isTimeoutError(requestError error) bool {
	if requestError == nil {
		return false
	}
	if errors.Is(requestError, context.DeadlineExceeded) {
		return true
	}
	var netError net.Error
	if errors.As(requestError, &netError) {
		return netError.Timeout()
	}
	return false
}

// unknownOutcome wraps a timeout on a non-idempotent write, and returns
// every other error untouched: a 4xx, a refused connection or a DNS
// failure all mean the write did not happen, and saying "it may have
// completed" about those would be worse than saying nothing.
func unknownOutcome(requestError error, operation string, verifyCommand string, retryConsequence string) error {
	if !isTimeoutError(requestError) {
		return requestError
	}
	return &WriteOutcomeUnknownError{
		Operation:        operation,
		VerifyCommand:    verifyCommand,
		RetryConsequence: retryConsequence,
		Cause:            requestError,
	}
}

// postCSRFJSONSlowWrite is postCSRFJSON for a write that legitimately
// takes longer than the shared client's response-header deadline. The call
// is bounded by the 5-minute overall client timeout instead, and a timeout
// is reported as an unknown outcome rather than a failure.
func (c *Client) postCSRFJSONSlowWrite(
	requestURL string,
	requestBody interface{},
	target interface{},
	operation string,
	verifyCommand string,
	retryConsequence string,
) error {
	payload, marshalError := marshalOptionalJSON(requestBody)
	if marshalError != nil {
		return marshalError
	}
	request, requestError := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(payload))
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Content-Type", "application/json")
	c.applyAuthAndCSRFHeaders(request)

	if doError := c.doJSONWithClient(c.httpClientForSlowWrite(), request, target, operation); doError != nil {
		return unknownOutcome(doError, operation, verifyCommand, retryConsequence)
	}
	return nil
}

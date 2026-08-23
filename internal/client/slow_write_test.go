package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// slowHeaderHandler withholds the response headers for the given delay, the
// way a publish that is still inside its transaction does.
func slowHeaderServer(t *testing.T, headerDelay time.Duration, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(headerDelay)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// TestSlowWriteOutlivesTheSharedResponseHeaderDeadline pins the first half
// of ankra-rs107. The shared client gives up after 30s of silence; a
// publish that takes longer to answer must not be cut off by the transport
// while the server is still working.
func TestSlowWriteOutlivesTheSharedResponseHeaderDeadline(t *testing.T) {
	sharedTransport, isTransport := New("token", "http://example.invalid").
		HTTP.Transport.(*orgOverrideTransport)
	if !isTransport {
		t.Fatalf("shared transport = %T, want *orgOverrideTransport", sharedTransport)
	}
	retrying, isRetrying := sharedTransport.base.(*retryTransport)
	if !isRetrying {
		t.Fatalf("shared base = %T, want *retryTransport", sharedTransport.base)
	}
	sharedBase, isHTTPTransport := retrying.base.(*http.Transport)
	if !isHTTPTransport {
		t.Fatalf("shared inner = %T, want *http.Transport", retrying.base)
	}
	if sharedBase.ResponseHeaderTimeout == 0 {
		t.Fatal("the shared transport is expected to keep a response-header deadline; " +
			"this test exists because publish must NOT use it")
	}

	slowClient := New("token", "http://example.invalid").httpClientForSlowWrite()
	slowTransport, isSlowTransport := slowClient.Transport.(*orgOverrideTransport)
	if !isSlowTransport {
		t.Fatalf("slow-write transport = %T", slowClient.Transport)
	}
	slowBase, isSlowHTTP := slowTransport.base.(*http.Transport)
	if !isSlowHTTP {
		t.Fatalf("slow-write base = %T, want *http.Transport", slowTransport.base)
	}
	if slowBase.ResponseHeaderTimeout != 0 {
		t.Fatalf("slow-write ResponseHeaderTimeout = %v, want 0 (bounded by the client timeout instead)",
			slowBase.ResponseHeaderTimeout)
	}
	if slowClient.Timeout == 0 {
		t.Fatal("the slow-write client must still be bounded overall, or a hung server hangs the CLI forever")
	}
}

// TestPublishOverTheSlowLaneStillParsesItsResult guards the refactor
// rather than the deadline: moving publish onto a different http.Client
// must not change how the response is read. It does not discriminate on
// the deadline itself - 150ms is inside the shared 30s window too - which
// is what the structural test above and the timeout test below are for.
func TestPublishOverTheSlowLaneStillParsesItsResult(t *testing.T) {
	server := slowHeaderServer(t, 150*time.Millisecond,
		`{"profile":{"id":"p1","name":"claude-code-session"},"version":{"version":2}}`)
	apiClient := New("token", server.URL)

	result, publishError := apiClient.PublishStackProfileDraft("48e1f0b8", PublishStackProfileDraftRequest{})
	if publishError != nil {
		t.Fatalf("publish returned %v", publishError)
	}
	if result == nil {
		t.Fatal("publish returned no result")
	}
	if result.Profile["name"] != "claude-code-session" {
		t.Fatalf("profile = %v", result.Profile)
	}
}

// TestPublishTimeoutReportsAnUnknownOutcome pins the second half, and the
// one that actually cost a duplicate version: a client-side timeout on a
// non-idempotent write must not read as a plain failure, because the
// natural next move after "Error:" is to run the command again.
func TestPublishTimeoutReportsAnUnknownOutcome(t *testing.T) {
	server := slowHeaderServer(t, 2*time.Second, `{}`)
	apiClient := New("token", server.URL)
	// Bound this call far below the server's delay so the timeout is the
	// deterministic outcome, without the test sleeping for the real one.
	apiClient.slowWriteTimeout = 50 * time.Millisecond

	_, publishError := apiClient.PublishStackProfileDraft("48e1f0b8", PublishStackProfileDraftRequest{})
	if publishError == nil {
		t.Fatal("a timed-out publish must report something")
	}

	var unknownError *WriteOutcomeUnknownError
	if !errors.As(publishError, &unknownError) {
		t.Fatalf("publish timeout = %T (%v), want *WriteOutcomeUnknownError", publishError, publishError)
	}

	message := unknownError.Error()
	for _, required := range []string{
		"may have completed",
		"Check before retrying",
		"ankra stack-profiles list",
		"publish a second version",
	} {
		if !strings.Contains(message, required) {
			t.Fatalf("timeout message missing %q:\n%s", required, message)
		}
	}
	// It must not read as a completed failure.
	if strings.Contains(strings.ToLower(message), "failed to publish") {
		t.Fatalf("the message states a failure it cannot know about:\n%s", message)
	}
}

// TestNonTimeoutErrorsAreNotDressedUpAsUnknown: a 4xx, a refused
// connection or a DNS failure all mean the write did NOT happen. Telling
// someone to go and check would be worse than saying nothing.
func TestNonTimeoutErrorsAreNotDressedUpAsUnknown(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"detail":"draft already published"}`))
	}))
	defer refusing.Close()

	apiClient := New("token", refusing.URL)
	_, publishError := apiClient.PublishStackProfileDraft("48e1f0b8", PublishStackProfileDraftRequest{})
	if publishError == nil {
		t.Fatal("a 400 must surface")
	}
	var unknownError *WriteOutcomeUnknownError
	if errors.As(publishError, &unknownError) {
		t.Fatalf("a 400 was reported as an unknown outcome: %v", publishError)
	}
	if !strings.Contains(publishError.Error(), "draft already published") {
		t.Fatalf("the server's detail was lost: %v", publishError)
	}
}

// TestIsTimeoutErrorMatchesOnBehaviourNotText: the header deadline arrives
// as x/net/http2's errTimeout wrapped in *url.Error, the overall client
// timeout as a context deadline. Matching the message string would break
// the first time Go rewords either.
func TestIsTimeoutErrorMatchesOnBehaviourNotText(t *testing.T) {
	timeoutCases := []struct {
		name  string
		given error
	}{
		{"context deadline", context.DeadlineExceeded},
		{"wrapped context deadline", fmt.Errorf("request failed: %w", context.DeadlineExceeded)},
		{"net.Error reporting a timeout", &net.OpError{Op: "read", Err: timeoutStub{}}},
		{"wrapped net timeout", fmt.Errorf("request failed: %w", &net.OpError{Op: "read", Err: timeoutStub{}})},
	}
	for _, testCase := range timeoutCases {
		if !isTimeoutError(testCase.given) {
			t.Fatalf("%s must be a timeout", testCase.name)
		}
	}

	notTimeouts := []struct {
		name  string
		given error
	}{
		{"nil", nil},
		{"connection refused", errors.New("connect: connection refused")},
		{"a 400 response", newUnexpectedResponseErrorWithMessage(400, "draft already published")},
		{"context cancelled by the user", context.Canceled},
	}
	for _, testCase := range notTimeouts {
		if isTimeoutError(testCase.given) {
			t.Fatalf("%s must not be a timeout", testCase.name)
		}
	}
}

// timeoutStub is an error that reports itself as a timeout, standing in for
// the transport's own timeout errors without depending on their text.
type timeoutStub struct{}

func (timeoutStub) Error() string { return "i/o timeout" }
func (timeoutStub) Timeout() bool { return true }
func (timeoutStub) Temporary() bool {
	return true
}

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeRoundTripper serves scripted outcomes in order; extra calls repeat
// the last outcome.
type fakeRoundTripper struct {
	outcomes []fakeOutcome
	calls    int
}

type fakeOutcome struct {
	status int
	err    error
}

func (f *fakeRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	outcome := f.outcomes[min(f.calls, len(f.outcomes)-1)]
	f.calls++
	if outcome.err != nil {
		return nil, outcome.err
	}
	return &http.Response{
		StatusCode: outcome.status,
		Status:     fmt.Sprintf("%d %s", outcome.status, http.StatusText(outcome.status)),
		Body:       io.NopCloser(strings.NewReader("{}")),
	}, nil
}

// timeoutError mimics the http2 "timeout awaiting response headers"
// error shape: a net.Error whose Timeout() is true.
type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout awaiting response headers" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func fastRetrySchedule(t *testing.T) {
	t.Helper()
	previous := retryBackoffSchedule
	retryBackoffSchedule = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryBackoffSchedule = previous })
}

func newRetryRequest(t *testing.T, method string) *http.Request {
	t.Helper()
	request, requestError := http.NewRequest(method, "https://platform.example/api/v1/clusters", nil)
	if requestError != nil {
		t.Fatal(requestError)
	}
	return request
}

func TestRetryTransportRetriesTimeoutsOnGet(t *testing.T) {
	fastRetrySchedule(t)
	fake := &fakeRoundTripper{outcomes: []fakeOutcome{
		{err: timeoutError{}},
		{err: timeoutError{}},
		{status: http.StatusOK},
	}}
	transport := &retryTransport{base: fake}

	response, requestError := transport.RoundTrip(newRetryRequest(t, http.MethodGet))
	if requestError != nil {
		t.Fatalf("expected the third attempt to succeed, got %v", requestError)
	}
	defer closeBody(response)
	if response.StatusCode != http.StatusOK || fake.calls != 3 {
		t.Fatalf("status = %d, calls = %d; want 200 after 3 calls", response.StatusCode, fake.calls)
	}
}

func TestRetryTransportGivesUpAfterBudget(t *testing.T) {
	fastRetrySchedule(t)
	fake := &fakeRoundTripper{outcomes: []fakeOutcome{{err: timeoutError{}}}}
	transport := &retryTransport{base: fake}

	_, requestError := transport.RoundTrip(newRetryRequest(t, http.MethodGet))
	if requestError == nil {
		t.Fatal("expected the exhausted budget to surface the error")
	}
	var netError net.Error
	if !errors.As(requestError, &netError) || !netError.Timeout() {
		t.Fatalf("expected the last transport error, got %v", requestError)
	}
	if fake.calls != retryAttempts {
		t.Fatalf("calls = %d, want %d", fake.calls, retryAttempts)
	}
}

func TestRetryTransportNeverRetriesWrites(t *testing.T) {
	fastRetrySchedule(t)
	fake := &fakeRoundTripper{outcomes: []fakeOutcome{{err: timeoutError{}}}}
	transport := &retryTransport{base: fake}

	_, requestError := transport.RoundTrip(newRetryRequest(t, http.MethodPost))
	if requestError == nil || fake.calls != 1 {
		t.Fatalf("POST must fail on the first error without retrying; calls = %d, err = %v", fake.calls, requestError)
	}
}

func TestRetryTransportDoesNotRetryNonTransientErrors(t *testing.T) {
	fastRetrySchedule(t)
	fake := &fakeRoundTripper{outcomes: []fakeOutcome{{err: errors.New("x509: certificate signed by unknown authority")}}}
	transport := &retryTransport{base: fake}

	_, requestError := transport.RoundTrip(newRetryRequest(t, http.MethodGet))
	if requestError == nil || fake.calls != 1 {
		t.Fatalf("non-transient errors must not be retried; calls = %d, err = %v", fake.calls, requestError)
	}
}

func TestRetryTransportRetriesGatewayStatuses(t *testing.T) {
	fastRetrySchedule(t)
	fake := &fakeRoundTripper{outcomes: []fakeOutcome{
		{status: http.StatusServiceUnavailable},
		{status: http.StatusOK},
	}}
	transport := &retryTransport{base: fake}

	response, requestError := transport.RoundTrip(newRetryRequest(t, http.MethodGet))
	if requestError != nil {
		t.Fatalf("expected the retry to succeed, got %v", requestError)
	}
	defer closeBody(response)
	if response.StatusCode != http.StatusOK || fake.calls != 2 {
		t.Fatalf("status = %d, calls = %d; want 200 after 2 calls", response.StatusCode, fake.calls)
	}
}

func TestRetryTransportSurfacesFinalGatewayStatus(t *testing.T) {
	fastRetrySchedule(t)
	fake := &fakeRoundTripper{outcomes: []fakeOutcome{{status: http.StatusBadGateway}}}
	transport := &retryTransport{base: fake}

	response, requestError := transport.RoundTrip(newRetryRequest(t, http.MethodGet))
	if requestError != nil {
		t.Fatalf("the final gateway response must be returned, not an error: %v", requestError)
	}
	defer closeBody(response)
	if response.StatusCode != http.StatusBadGateway || fake.calls != retryAttempts {
		t.Fatalf("status = %d, calls = %d; want 502 after %d calls", response.StatusCode, fake.calls, retryAttempts)
	}
}

func TestRetryTransportStopsWhenContextIsCancelled(t *testing.T) {
	previous := retryBackoffSchedule
	retryBackoffSchedule = []time.Duration{time.Hour, time.Hour}
	t.Cleanup(func() { retryBackoffSchedule = previous })

	fake := &fakeRoundTripper{outcomes: []fakeOutcome{{err: timeoutError{}}}}
	transport := &retryTransport{base: fake}

	ctx, cancel := context.WithCancel(context.Background())
	request := newRetryRequest(t, http.MethodGet).WithContext(ctx)
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, requestError := transport.RoundTrip(request)
	if requestError == nil {
		t.Fatal("expected an error after cancellation")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation must interrupt the backoff wait, took %s", elapsed)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d, want 1 (no attempt after cancellation)", fake.calls)
	}
}

// TestClientRetriesThroughRealTransport pins the New() wiring: a real
// Client call rides through a single 503 without surfacing an error.
func TestClientRetriesThroughRealTransport(t *testing.T) {
	fastRetrySchedule(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		jsonResponse(t, w, http.StatusOK, []OrganisationSummary{})
	}))
	t.Cleanup(server.Close)

	c := New(testToken, server.URL)
	if _, listError := c.ListOrganisations(); listError != nil {
		t.Fatalf("ListOrganisations() after one 503 = %v", listError)
	}
	if calls != 2 {
		t.Fatalf("server calls = %d, want 2", calls)
	}
}

func TestIsTransientRequestErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"response header timeout", timeoutError{}, true},
		{"op error", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		{"eof", io.EOF, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"goaway", errors.New("http2: server sent GOAWAY and closed the connection"), true},
		{"context canceled", context.Canceled, false},
		{"context deadline", context.DeadlineExceeded, false},
		{"tls failure", errors.New("x509: certificate signed by unknown authority"), false},
	}
	for _, testCase := range cases {
		if got := isTransientRequestError(testCase.err); got != testCase.want {
			t.Errorf("%s: isTransientRequestError = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

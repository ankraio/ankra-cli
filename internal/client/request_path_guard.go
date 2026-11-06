package client

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The client builds API paths from arguments, and a path segment the
// platform's edge rewrites sends the request to another route. The edge in
// front of the platform (probed 2026-11-06, ankra-6pi023):
//
//   - resolves "." and ".." segments before routing, percent-encoded ones
//     too ("%2e%2e", "%2E%2E", ".%2e"): DELETE
//     /api/v1/clusters/<kind>/<id>/node-groups/.. reaches the cluster's own
//     DELETE, which deprovisions it;
//   - merges an empty segment ("//") into its neighbour, so a blank argument
//     in the middle of a path shifts every segment after it.
//
// It decodes a segment once and no more: "%252e" is not a dot to it (that
// answers 404). It does not decode "%2F" or "%5C", it leaves "..." alone, and
// it does not strip a trailing "/" (that answers 404 too). No route the
// platform registers ends in "/" or has an empty, "." or ".." segment (the
// route census, cluster_routes_test.go), so such a path is never a request
// the CLI means to send. The platform cannot refuse it for us: the path is
// rewritten before it arrives.
//
// Every request this package sends to the API host is checked here before
// it leaves: by scopeOverrideTransport, by requestPathGuardTransport on the
// lanes that do not ride it, and by ExchangeGitHubOIDCToken itself, which
// sends with its caller's client (request_path_guard_test.go keeps that
// list complete). A request to another host (a presigned storage URL) is not
// the edge's to rewrite. Arguments are url.PathEscape'd into their segments
// first, so a name that holds '/', '?' or '#' stays one segment.

// UnsafeRequestPathError is the client's refusal to send a request whose
// path has an empty, "." or ".." segment, or one that decodes to "." or
// "..". Nothing was sent. It is a usage error: the argument that built the
// segment has to change.
type UnsafeRequestPathError struct {
	// Method and Path are the request's method and its path as it would
	// have been sent (escaped, without the query).
	Method string
	Path   string
	// Segment is the offending segment as it would have been sent; it is
	// empty for an empty segment.
	Segment string
	// Decoded is what Segment percent-decodes to, when that differs from it.
	Decoded string
	// Trailing is set when the empty segment is the last one: the path ends
	// in "/".
	Trailing bool
}

func (e *UnsafeRequestPathError) Error() string {
	return fmt.Sprintf("refusing to send %s %s: %s", e.Method, e.Path, e.reason())
}

func (e *UnsafeRequestPathError) reason() string {
	switch {
	case e.Segment == "" && e.Trailing:
		return "the path ends in '/', so its last segment is empty (an argument is probably blank), " +
			"and no platform route ends in '/'"
	case e.Segment == "":
		return "a path segment is empty ('//'), which the platform's edge would merge away, sending the request " +
			"to a different route (an argument is probably blank)"
	case e.Decoded != "":
		return fmt.Sprintf("a path segment is '%s', which decodes to '%s', and the platform's edge would resolve "+
			"it to a different route", e.Segment, e.Decoded)
	default:
		return fmt.Sprintf("a path segment is '%s', which the platform's edge would resolve to a different route",
			e.Segment)
	}
}

// UnsafePathSegment reports whether segment, as it is sent on the wire, is
// one the platform's edge rewrites: empty, or "." or ".." once decoded the
// way the edge decodes it (once). decoded is the segment decoded once; a
// segment that does not decode cleanly is judged as it is.
func UnsafePathSegment(segment string) (decoded string, unsafe bool) {
	decoded = segment
	if strings.Contains(segment, "%") {
		if unescaped, unescapeError := url.PathUnescape(segment); unescapeError == nil {
			decoded = unescaped
		}
	}
	return decoded, segment == "" || decoded == "." || decoded == ".."
}

// requestWirePath is the path a request puts on the wire, escaped, without
// the query. An opaque URL carries it in Opaque: "//host/path" is sent in
// absolute form, so its path starts after the authority, and any other
// opaque value is the path itself.
func requestWirePath(requestURL *url.URL) string {
	if requestURL.Opaque == "" {
		return requestURL.EscapedPath()
	}
	if !strings.HasPrefix(requestURL.Opaque, "//") {
		return requestURL.Opaque
	}
	authorityAndPath := strings.TrimPrefix(requestURL.Opaque, "//")
	if slash := strings.IndexByte(authorityAndPath, '/'); slash >= 0 {
		return authorityAndPath[slash:]
	}
	return ""
}

// checkRequestPath returns an *UnsafeRequestPathError when the path the
// request would put on the wire has a segment the edge rewrites, and nil
// otherwise. The root path ("" or "/") has no segments to check.
func checkRequestPath(method string, requestURL *url.URL) error {
	if requestURL == nil {
		return nil
	}
	wirePath := requestWirePath(requestURL)
	if wirePath == "" || wirePath == "/" {
		return nil
	}
	segments := strings.Split(strings.TrimPrefix(wirePath, "/"), "/")
	for index, segment := range segments {
		decoded, unsafe := UnsafePathSegment(segment)
		if !unsafe {
			continue
		}
		refusal := &UnsafeRequestPathError{Method: method, Path: wirePath, Segment: segment,
			Trailing: segment == "" && index == len(segments)-1}
		if decoded != segment {
			refusal.Decoded = decoded
		}
		return refusal
	}
	return nil
}

// refuseUnsafeRequestPath checks a request a transport is about to send. A
// refused request's body is closed, as a RoundTripper must on every error.
func refuseUnsafeRequestPath(request *http.Request) error {
	refusal := checkRequestPath(request.Method, request.URL)
	if refusal != nil && request.Body != nil {
		_ = request.Body.Close()
	}
	return refusal
}

// requestPathGuardTransport is the path check alone, for a client that
// talks only to the API but does not ride scopeOverrideTransport (the pod
// terminal's websocket handshake, which sets its own headers; the staff
// session client; the login client).
type requestPathGuardTransport struct {
	base http.RoundTripper
}

func (t *requestPathGuardTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if refusal := refuseUnsafeRequestPath(request); refusal != nil {
		return nil, refusal
	}
	return t.base.RoundTrip(request)
}

// CloseIdleConnections forwards to the base transport, so
// http.Client.CloseIdleConnections still reaches the connections it pools.
func (t *requestPathGuardTransport) CloseIdleConnections() {
	if closer, canClose := t.base.(interface{ CloseIdleConnections() }); canClose {
		closer.CloseIdleConnections()
	}
}

// guardedDefaultTransport is net/http's default transport (proxy from the
// environment, dial, keep-alive and TLS handshake timeouts, idle connection
// reaping, HTTP/2), cloned so nothing else shares its pool, behind the path
// check. It is the one place the package reads http.DefaultTransport.
func guardedDefaultTransport() *requestPathGuardTransport {
	return &requestPathGuardTransport{base: http.DefaultTransport.(*http.Transport).Clone()}
}

// NewGuardedHTTPClient is an http.Client for the API built outside this
// package (the login flow's): net/http's default transport behind the path
// check, with timeout as the whole-request limit (0 for none).
func NewGuardedHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: guardedDefaultTransport()}
}

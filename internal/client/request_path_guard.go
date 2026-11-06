package client

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The client builds API paths by pasting arguments into them, and a path
// segment the platform's edge rewrites sends the request to another route.
// The edge in front of the platform (probed 2026-11-06, ankra-6pi023):
//
//   - resolves "." and ".." segments before routing, percent-encoded ones
//     too ("%2e%2e", "%2E%2E", ".%2e"): DELETE
//     /api/v1/clusters/<kind>/<id>/node-groups/.. reaches the cluster's own
//     DELETE, which deprovisions it;
//   - merges an empty segment ("//") into its neighbour, so a blank argument
//     in the middle of a path shifts every segment after it.
//
// It does not decode "%2F" or "%5C", and it does not strip a trailing "/"
// (that answers 404). No route the platform registers ends in "/" or has an
// empty, "." or ".." segment (the route census, cluster_routes_test.go), so
// such a path is never a request the CLI means to send. The platform cannot
// refuse it for us: the path is rewritten before it arrives.
//
// Every request the client sends to its API host is checked here before it
// leaves (scopeOverrideTransport, requestPathGuardTransport). A request to
// another host (a presigned storage URL) is not the edge's to rewrite.

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

// checkRequestPath returns an *UnsafeRequestPathError when the path the
// request would put on the wire has a segment the edge rewrites, and nil
// otherwise. The root path ("" or "/") has no segments to check.
func checkRequestPath(method string, requestURL *url.URL) error {
	if requestURL == nil {
		return nil
	}
	wirePath := requestURL.EscapedPath()
	if requestURL.Opaque != "" {
		wirePath = requestURL.Opaque
	}
	if wirePath == "" || wirePath == "/" {
		return nil
	}
	segments := strings.Split(strings.TrimPrefix(wirePath, "/"), "/")
	for index, segment := range segments {
		if segment == "" {
			return &UnsafeRequestPathError{Method: method, Path: wirePath, Trailing: index == len(segments)-1}
		}
		decoded := PercentDecoded(segment)
		if decoded != "." && decoded != ".." {
			continue
		}
		refusal := &UnsafeRequestPathError{Method: method, Path: wirePath, Segment: segment}
		if decoded != segment {
			refusal.Decoded = decoded
		}
		return refusal
	}
	return nil
}

// PercentDecoded decodes a path segment, or an argument headed for one,
// until it stops changing, so a dot escaped more than once ("%252e") is
// still seen as a dot. A value that stops decoding cleanly is judged as far
// as it decoded.
func PercentDecoded(segment string) string {
	decoded := segment
	for round := 0; round < 4 && strings.Contains(decoded, "%"); round++ {
		next, decodeError := url.PathUnescape(decoded)
		if decodeError != nil || next == decoded {
			break
		}
		decoded = next
	}
	return decoded
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
// terminal's websocket handshake, which sets its own headers).
type requestPathGuardTransport struct {
	base http.RoundTripper
}

func (t *requestPathGuardTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if refusal := refuseUnsafeRequestPath(request); refusal != nil {
		return nil, refusal
	}
	return t.base.RoundTrip(request)
}

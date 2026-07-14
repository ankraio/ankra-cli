package client

import (
	"net/http"
	"time"
)

// orgOverrideHeader scopes a single request to a specific organisation the
// authenticated user belongs to, without changing the token's bound
// organisation server-side. Set via the CLI's global `--org` flag.
const orgOverrideHeader = "X-Ankra-Organisation-Id"

type Client struct {
	Token         string
	BaseURL       string
	HTTP          *http.Client
	StreamingHTTP *http.Client

	// orgOverride, when non-empty, is sent as the orgOverrideHeader on every
	// request so commands run against a non-selected organisation.
	orgOverride string
}

// orgOverrideTransport injects the organisation override header on every
// request routed through the client, so no individual call site needs to be
// aware of the override.
type orgOverrideTransport struct {
	base  http.RoundTripper
	orgID *string
}

func (t *orgOverrideTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.orgID != nil && *t.orgID != "" {
		clone := req.Clone(req.Context())
		clone.Header.Set(orgOverrideHeader, *t.orgID)
		return t.base.RoundTrip(clone)
	}
	return t.base.RoundTrip(req)
}

// New constructs a Client with the supplied token and a base URL that is
// expected to already be validated and normalized via NormalizeBaseURL.
func New(token, baseURL string) *Client {
	c := &Client{
		Token:   token,
		BaseURL: baseURL,
	}
	sharedBase := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
	}
	// Idempotent requests ride through transient platform errors (see
	// retryTransport); the org override header is applied outside the
	// retry loop so every attempt carries it.
	sharedTransport := &orgOverrideTransport{
		base:  &retryTransport{base: sharedBase},
		orgID: &c.orgOverride,
	}
	c.HTTP = &http.Client{
		Timeout:   5 * time.Minute,
		Transport: sharedTransport,
	}
	c.StreamingHTTP = &http.Client{
		Transport: sharedTransport,
	}
	return c
}

// SetOrganisationOverride scopes all subsequent requests to the given
// organisation ID. Pass an empty string to clear the override.
func (c *Client) SetOrganisationOverride(orgID string) {
	c.orgOverride = orgID
}

// OrganisationOverride returns the currently configured organisation override.
func (c *Client) OrganisationOverride() string {
	return c.orgOverride
}

// httpClientForSlowWrite returns a client for synchronous writes that can
// legitimately take longer than the shared client's 30s response-header
// timeout to start responding - notably PATCH /stacks/{stack_name}, which
// performs a synchronous git commit+push on the request path. It drops
// ResponseHeaderTimeout so the call is bounded by the caller's context
// instead of failing with "http2: timeout awaiting response headers" while
// the server is still making progress. The org override header is preserved.
func (c *Client) httpClientForSlowWrite() *http.Client {
	slowWriteBase := &http.Transport{
		ResponseHeaderTimeout: 0,
	}
	slowWriteTransport := &orgOverrideTransport{base: slowWriteBase, orgID: &c.orgOverride}
	return &http.Client{
		Timeout:   5 * time.Minute,
		Transport: slowWriteTransport,
	}
}

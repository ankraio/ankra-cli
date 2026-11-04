package client

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

// orgOverrideHeader scopes a single request to a specific organisation the
// authenticated user belongs to, without changing the token's bound
// organisation server-side. Set via the CLI's global `--org` flag.
const orgOverrideHeader = "X-Ankra-Organisation-Id"

// projectOverrideHeader scopes a single request to one project of the
// organisation the request acts in (ADR 0022), without moving the member's
// server-side current project. The value is a project id; the platform
// refuses it for a token pinned to a different project and ignores it while
// the organisation has projects off. Set via the CLI's global `--project`
// flag, ANKRA_PROJECT, or the selection `ankra project switch` saves.
const projectOverrideHeader = "X-Ankra-Project-Id"

// CostProjectScopeAll is the ?project_id= value that widens a fleet cost read
// to the whole organisation when the caller's current project would
// otherwise narrow it.
const CostProjectScopeAll = "all"

type Client struct {
	Token         string
	BaseURL       string
	HTTP          *http.Client
	StreamingHTTP *http.Client

	// orgOverride, when non-empty, is sent as the orgOverrideHeader on every
	// request so commands run against a non-selected organisation.
	orgOverride string

	// projectOverride, when non-empty, is sent as the projectOverrideHeader
	// on every request so commands act in a project other than the one the
	// platform resolves for the token: its pin, else the default project.
	projectOverride string

	// costProjectScope, when non-empty, is sent as ?project_id= on the fleet
	// cost reads that take it (summary, savings, ledger, trend, events,
	// reconciliation, budgets): a project id, or CostProjectScopeAll.
	costProjectScope string

	// slowWriteTimeout bounds the writes that do their whole job on the
	// request path (see slow_write.go). They drop the response-header
	// deadline, so this is the only thing standing between a wedged server
	// and a CLI that never returns.
	slowWriteTimeout time.Duration

	// tokenExpiryWarning warns, once per invocation, when the platform says
	// the token in use expires within TokenExpiryWarningWindow. Every
	// transport the client builds shares it, so the slow-write lane cannot
	// warn a second time.
	tokenExpiryWarning *tokenExpiryWarning

	// projectScopeWarning warns, once per invocation, when a read says it did
	// not apply the selected project. Shared across transports like the expiry
	// warning.
	projectScopeWarning *projectScopeWarning
}

// defaultSlowWriteTimeout is the overall bound for a long synchronous
// write. It matches the shared client's Timeout: the point of the slow
// lane is to stop the transport giving up while the server is still
// making progress, not to wait indefinitely.
const defaultSlowWriteTimeout = 5 * time.Minute

// scopeOverrideTransport injects the organisation and project override
// headers, and the client attribution headers (attribution.go), on every
// request routed through the client, so no individual call site needs to be
// aware of either. Every transport that talks to the Ankra API is one of
// these.
type scopeOverrideTransport struct {
	base      http.RoundTripper
	orgID     *string
	projectID *string
	// baseURL is the client's API base URL. The attribution headers go only
	// to its host, so a redirect to another host, or a caller handing this
	// client a presigned storage or GitHub URL, sends none of them.
	baseURL *string
	// lookupEnvironment reads the environment the agent is detected from;
	// nil is os.Getenv.
	lookupEnvironment func(string) string
	// projectScopeWarning hears the response to every request sent with a
	// project override.
	projectScopeWarning *projectScopeWarning
}

func (t *scopeOverrideTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	organisationID := ""
	if t.orgID != nil {
		organisationID = *t.orgID
	}
	projectID := ""
	if t.projectID != nil {
		projectID = *t.projectID
	}
	clone := req.Clone(req.Context())
	if t.isAPIRequest(clone.URL) {
		applyClientAttribution(clone.Header, t.environment())
	}
	if organisationID != "" {
		clone.Header.Set(orgOverrideHeader, organisationID)
	}
	if projectID != "" {
		clone.Header.Set(projectOverrideHeader, projectID)
	}
	response, requestError := t.base.RoundTrip(clone)
	if requestError == nil && t.isAPIRequest(clone.URL) {
		t.projectScopeWarning.observe(clone, response, projectID)
	}
	return response, requestError
}

// New constructs a Client with the supplied token and a base URL that is
// expected to already be validated and normalized via NormalizeBaseURL.
func New(token, baseURL string) *Client {
	c := &Client{
		Token:               token,
		BaseURL:             baseURL,
		slowWriteTimeout:    defaultSlowWriteTimeout,
		tokenExpiryWarning:  newTokenExpiryWarning(),
		projectScopeWarning: newProjectScopeWarning(),
	}
	sharedBase := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
	}
	// Idempotent requests ride through transient platform errors (see
	// retryTransport); the scope override headers are applied outside the
	// retry loop so every attempt carries them.
	sharedTransport := &scopeOverrideTransport{
		base: &tokenExpiryTransport{
			base:    &retryTransport{base: sharedBase},
			warning: c.tokenExpiryWarning,
		},
		orgID:               &c.orgOverride,
		projectID:           &c.projectOverride,
		baseURL:             &c.BaseURL,
		projectScopeWarning: c.projectScopeWarning,
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

// SetProjectOverride scopes all subsequent requests to the given project ID.
// Pass an empty string to clear the override.
func (c *Client) SetProjectOverride(projectID string) {
	c.projectOverride = projectID
}

// ProjectOverride returns the currently configured project override.
func (c *Client) ProjectOverride() string {
	return c.projectOverride
}

// SetCostProjectScope sets the ?project_id= every fleet cost read sends: a
// project id, CostProjectScopeAll for the whole organisation, or an empty
// string to leave the read in the caller's current project.
func (c *Client) SetCostProjectScope(scope string) {
	c.costProjectScope = scope
}

// costScopedURL appends the cost project scope to a fleet cost read's URL,
// after any query the path already carries.
func (c *Client) costScopedURL(target string) string {
	if c.costProjectScope == "" {
		return target
	}
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	return target + separator + "project_id=" + url.QueryEscape(c.costProjectScope)
}

// httpClientForSlowWrite returns a client for synchronous writes that can
// legitimately take longer than the shared client's 30s response-header
// timeout to start responding - notably PATCH /stacks/{stack_name}, which
// performs a synchronous git commit+push on the request path. It drops
// ResponseHeaderTimeout so the call is bounded by the caller's context
// instead of failing with "http2: timeout awaiting response headers" while
// the server is still making progress. The scope override headers are preserved.
func (c *Client) httpClientForSlowWrite() *http.Client {
	slowWriteBase := &http.Transport{
		ResponseHeaderTimeout: 0,
	}
	slowWriteTransport := &scopeOverrideTransport{
		base:                &tokenExpiryTransport{base: slowWriteBase, warning: c.tokenExpiryWarning},
		orgID:               &c.orgOverride,
		projectID:           &c.projectOverride,
		baseURL:             &c.BaseURL,
		projectScopeWarning: c.projectScopeWarning,
	}
	timeout := c.slowWriteTimeout
	if timeout <= 0 {
		timeout = defaultSlowWriteTimeout
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: slowWriteTransport,
	}
}

package client

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// TokenExpiresAtHeader is the response header the platform sets on every
// request authenticated with an API token: when that token expires, in
// RFC 3339. Platforms older than the header simply leave it out.
const TokenExpiresAtHeader = "X-Ankra-Token-Expires-At"

// TokenExpiryWarningWindow is how close to its expiry a token has to be for
// the CLI to warn about it, and for `ankra tokens list` to flag it.
const TokenExpiryWarningWindow = 7 * 24 * time.Hour

// tokenExpiryWarning prints, at most once per invocation, a one-line stderr
// warning that the token the CLI authenticates with is about to expire. A CI
// job that runs the CLI with a token close to its end then says so in its
// log days before the token stops working, instead of failing on a bare 401
// the day it does.
type tokenExpiryWarning struct {
	once   sync.Once
	output io.Writer
	now    func() time.Time
}

func newTokenExpiryWarning() *tokenExpiryWarning {
	return &tokenExpiryWarning{output: os.Stderr, now: time.Now}
}

// observe reads the expiry header off a response and warns when the token
// expires within TokenExpiryWarningWindow. A missing or unreadable header
// says nothing about the token, so it warns about nothing.
func (warning *tokenExpiryWarning) observe(response *http.Response) {
	if warning == nil || response == nil {
		return
	}
	rawExpiresAt := strings.TrimSpace(response.Header.Get(TokenExpiresAtHeader))
	if rawExpiresAt == "" {
		return
	}
	expiresAt, parseError := time.Parse(time.RFC3339, rawExpiresAt)
	if parseError != nil {
		return
	}
	remaining := expiresAt.Sub(warning.now())
	if remaining > TokenExpiryWarningWindow {
		return
	}
	warning.once.Do(func() {
		_, _ = fmt.Fprintln(warning.output, tokenExpiryWarningLine(expiresAt, remaining))
	})
}

// tokenExpiryWarningLine is the warning itself: one line, naming when the
// token expires and what to do. It never carries the token.
func tokenExpiryWarningLine(expiresAt time.Time, remaining time.Duration) string {
	return fmt.Sprintf("Warning: the Ankra API token in use %s (%s). "+
		"Run `ankra tokens rotate <id>` to renew it (find the id with `ankra tokens list`; for a service token, "+
		"`ankra tokens list --service` and `ankra tokens rotate <id> --service`), "+
		"or run `ankra login`.",
		DescribeTokenExpiry(remaining), expiresAt.UTC().Format("2006-01-02 15:04 UTC"))
}

// DescribeTokenExpiry says how far off a token's expiry is: "has expired",
// "expires within a day", "expires in 1 day", "expires in N days".
func DescribeTokenExpiry(remaining time.Duration) string {
	switch days := TokenDaysLeft(remaining); {
	case remaining <= 0:
		return "has expired"
	case days == 0:
		return "expires within a day"
	case days == 1:
		return "expires in 1 day"
	default:
		return fmt.Sprintf("expires in %d days", days)
	}
}

// TokenDaysLeft is the whole days before a token expires, rounded down: 0
// inside its last day, negative once it has expired. It is the reading the
// platform's token listings answer as days_left.
func TokenDaysLeft(remaining time.Duration) int {
	return int(math.Floor(remaining.Hours() / 24))
}

// tokenExpiryTransport hands every response to the expiry warning.
type tokenExpiryTransport struct {
	base    http.RoundTripper
	warning *tokenExpiryWarning
}

func (transport *tokenExpiryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, requestError := transport.base.RoundTrip(request)
	if requestError == nil {
		transport.warning.observe(response)
	}
	return response, requestError
}

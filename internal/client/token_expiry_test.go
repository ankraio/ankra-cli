package client

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// expiryWarningClient builds a client whose transports are the real ones New
// builds, pointed at handler, with the warning's output and clock captured.
func expiryWarningClient(t *testing.T, handler http.HandlerFunc, now time.Time) (*Client, *bytes.Buffer) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	testClient := New(testToken, server.URL)
	output := &bytes.Buffer{}
	testClient.tokenExpiryWarning.output = output
	testClient.tokenExpiryWarning.now = func() time.Time { return now }
	return testClient, output
}

// TestTokenExpiryWarningIsOneLineOncePerInvocation pins the CI-log promise:
// a token that expires within seven days prints one stderr line naming the
// expiry, however many requests the invocation makes, and never the token.
func TestTokenExpiryWarningIsOneLineOncePerInvocation(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(3*24*time.Hour + time.Hour)
	testClient, output := expiryWarningClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set(TokenExpiresAtHeader, expiresAt.Format(time.RFC3339))
		jsonResponse(t, writer, http.StatusOK, []APIToken{})
	}, now)
	for range 3 {
		if _, listError := testClient.ListAPITokens(); listError != nil {
			t.Fatalf("ListAPITokens() error = %v", listError)
		}
	}
	lines := strings.Split(strings.TrimRight(output.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly one warning line, got %q", output.String())
	}
	if !strings.Contains(lines[0], "expires in 3 days (2026-10-18 13:00 UTC)") {
		t.Fatalf("warning = %q", lines[0])
	}
	if strings.Contains(lines[0], testToken) {
		t.Fatal("the warning must never carry the token")
	}
}

// TestTokenExpiryWarningStaysQuietOutsideTheWindow pins the quiet cases: a
// token with more than seven days left, a platform that sends no header, and
// a header that does not parse.
func TestTokenExpiryWarningStaysQuietOutsideTheWindow(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for name, headerValue := range map[string]string{
		"eight days left": now.Add(8 * 24 * time.Hour).Format(time.RFC3339),
		"no header":       "",
		"unparseable":     "next tuesday",
	} {
		t.Run(name, func(t *testing.T) {
			testClient, output := expiryWarningClient(t, func(writer http.ResponseWriter, _ *http.Request) {
				if headerValue != "" {
					writer.Header().Set(TokenExpiresAtHeader, headerValue)
				}
				jsonResponse(t, writer, http.StatusOK, []APIToken{})
			}, now)
			if _, listError := testClient.ListAPITokens(); listError != nil {
				t.Fatalf("ListAPITokens() error = %v", listError)
			}
			if output.Len() != 0 {
				t.Fatalf("want no warning, got %q", output.String())
			}
		})
	}
}

// TestDescribeTokenExpiryRoundsDown pins the wording the warning and the
// listing share.
func TestDescribeTokenExpiryRoundsDown(t *testing.T) {
	cases := map[time.Duration]string{
		7*24*time.Hour - time.Minute: "expires in 6 days",
		25 * time.Hour:               "expires in 1 day",
		23 * time.Hour:               "expires within a day",
		-time.Minute:                 "has expired",
	}
	for remaining, expected := range cases {
		if got := DescribeTokenExpiry(remaining); got != expected {
			t.Errorf("remaining %s = %q, want %q", remaining, got, expected)
		}
	}
}

// TestTokenExpiryWarningNamesCommandsThatExist pins the service-token half of
// the warning to commands the CLI has: v0.37.0 said "add --service" next to
// `ankra tokens list`, which had no such flag.
func TestTokenExpiryWarningNamesCommandsThatExist(t *testing.T) {
	line := tokenExpiryWarningLine(time.Date(2026, 10, 18, 13, 0, 0, 0, time.UTC), 3*24*time.Hour)
	for _, want := range []string{"`ankra tokens list --service`", "`ankra tokens rotate <id> --service`"} {
		if !strings.Contains(line, want) {
			t.Errorf("warning lacks %s: %q", want, line)
		}
	}
}

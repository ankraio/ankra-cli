package cmd

import (
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

// TestDescribeTokenExpiryFlagsTokensExpiringWithinSevenDays pins what
// `ankra tokens list` shows: the date and how far off it is, Expiring for a
// live token inside seven days, Expired past it, and nothing flagged for a
// revoked token or a date it cannot read. The listing's zone-less
// expires_at is read as UTC.
func TestDescribeTokenExpiryFlagsTokensExpiringWithinSevenDays(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	five := 5
	cases := []struct {
		name             string
		token            client.APIToken
		wantText         string
		wantExpiringSoon bool
		wantExpired      bool
	}{
		{"platform days_left inside the window",
			client.APIToken{ExpiresAt: "2026-10-20T13:00:00", DaysLeft: &five},
			"2026-10-20 (in 5 days)", true, false},
		{"computed from a zone-less expiry",
			client.APIToken{ExpiresAt: "2026-10-18T12:30:00"},
			"2026-10-18 (in 3 days)", true, false},
		{"far off",
			client.APIToken{ExpiresAt: "2027-01-13T12:00:00Z"},
			"2027-01-13 (in 90 days)", false, false},
		{"expired",
			client.APIToken{ExpiresAt: "2026-10-14T12:00:00"},
			"2026-10-14 (expired)", false, true},
		{"revoked is not expiring",
			client.APIToken{ExpiresAt: "2026-10-16T12:00:00", Revoked: true},
			"2026-10-16 (in 1 day)", false, false},
		{"platform days_left wins over a skewed expires_at",
			client.APIToken{ExpiresAt: "2026-12-01T12:00:00", DaysLeft: &five},
			"2026-12-01 (in 5 days)", true, false},
		{"unreadable expires_at with platform days_left",
			client.APIToken{ExpiresAt: "soon", DaysLeft: &five},
			"soon (in 5 days)", true, false},
		{"unreadable",
			client.APIToken{ExpiresAt: "soon"},
			"soon", false, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := describeTokenExpiry(testCase.token, now)
			if got.text != testCase.wantText || got.isExpiringSoon != testCase.wantExpiringSoon ||
				got.isExpired != testCase.wantExpired {
				t.Fatalf("got %+v, want text %q expiring %t expired %t", got, testCase.wantText,
					testCase.wantExpiringSoon, testCase.wantExpired)
			}
		})
	}
}

// TestTokensListMarksAnExpiringToken pins the table: a token expiring within
// seven days reads Expiring in the table on stdout.
func TestTokensListMarksAnExpiringToken(t *testing.T) {
	soon := time.Now().UTC().Add(3 * 24 * time.Hour).Format(time.RFC3339)
	mock := &tokensListMock{
		tokens: []client.APIToken{
			{ID: "token-id-1", Name: "github_cluster_ci", CreatedAt: "2026-07-15T12:00:00Z", ExpiresAt: soon},
		},
	}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("tokens", "list")
	})
	if !strings.Contains(stdoutOutput, "Expiring") || !strings.Contains(stdoutOutput, "github_cluster_ci") {
		t.Fatalf("expected the token marked Expiring, got: %s", stdoutOutput)
	}
}

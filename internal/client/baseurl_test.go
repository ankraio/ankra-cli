package client

import (
	"strings"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		allowInsecureHTTP bool
		expected          string
		expectErr         string
	}{
		{
			name:     "valid https without trailing slash",
			input:    "https://platform.ankra.app",
			expected: "https://platform.ankra.app",
		},
		{
			name:     "valid https trims trailing slash",
			input:    "https://platform.ankra.app/",
			expected: "https://platform.ankra.app",
		},
		{
			name:     "https with path is preserved",
			input:    "https://platform.ankra.app/api",
			expected: "https://platform.ankra.app/api",
		},
		{
			name:     "https with path trims trailing slash",
			input:    "https://platform.ankra.app/api/",
			expected: "https://platform.ankra.app/api",
		},
		{
			name:     "https with port",
			input:    "https://platform.ankra.app:8443",
			expected: "https://platform.ankra.app:8443",
		},
		{
			name:     "http to localhost is allowed",
			input:    "http://localhost:8080",
			expected: "http://localhost:8080",
		},
		{
			name:     "http to 127.0.0.1 is allowed",
			input:    "http://127.0.0.1:9000",
			expected: "http://127.0.0.1:9000",
		},
		{
			name:     "http to ipv6 loopback is allowed",
			input:    "http://[::1]:9000",
			expected: "http://[::1]:9000",
		},
		{
			name:              "http with insecure override is allowed",
			input:             "http://internal.example.lan",
			allowInsecureHTTP: true,
			expected:          "http://internal.example.lan",
		},
		{
			name:      "http to public host is rejected",
			input:     "http://platform.ankra.app",
			expectErr: "plaintext http",
		},
		{
			name:      "empty input is rejected",
			input:     "",
			expectErr: "empty",
		},
		{
			name:      "whitespace input is rejected",
			input:     "   ",
			expectErr: "empty",
		},
		{
			name:      "missing scheme is rejected",
			input:     "platform.ankra.app",
			expectErr: "scheme",
		},
		{
			name:      "ftp scheme is rejected",
			input:     "ftp://platform.ankra.app",
			expectErr: "scheme must be http or https",
		},
		{
			name:      "javascript scheme is rejected",
			input:     "javascript:alert(1)",
			expectErr: "scheme",
		},
		{
			name:      "userinfo is rejected",
			input:     "https://user:pass@platform.ankra.app",
			expectErr: "userinfo",
		},
		{
			name:      "query string is rejected",
			input:     "https://platform.ankra.app?token=oops",
			expectErr: "query",
		},
		{
			name:      "fragment is rejected",
			input:     "https://platform.ankra.app#foo",
			expectErr: "fragment",
		},
		{
			name:      "scheme only is rejected",
			input:     "https://",
			expectErr: "host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBaseURL(tt.input, tt.allowInsecureHTTP)
			if tt.expectErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (result=%q)", tt.expectErr, got)
				}
				if !strings.Contains(err.Error(), tt.expectErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.expectErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

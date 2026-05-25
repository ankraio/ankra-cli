package client

import (
	"strings"
	"testing"
)

func TestRedactedBodyForErrorRedactsCommonSecretFields(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		mustNotLeak  []string
		mustContain  []string
		shouldRedact bool
	}{
		{
			name:         "redacts token in detail",
			body:         `{"detail":"failed","token":"ankra_pat_secret","org":"acme"}`,
			mustNotLeak:  []string{"ankra_pat_secret"},
			mustContain:  []string{"<redacted>", "acme", "failed"},
			shouldRedact: true,
		},
		{
			name:         "redacts password fields",
			body:         `{"username":"alice","password":"hunter2"}`,
			mustNotLeak:  []string{"hunter2"},
			mustContain:  []string{"<redacted>", "alice"},
			shouldRedact: true,
		},
		{
			name:         "redacts nested structures",
			body:         `{"errors":[{"name":"cred","detail":{"private_key":"-----BEGIN..."}}]}`,
			mustNotLeak:  []string{"-----BEGIN"},
			mustContain:  []string{"<redacted>", "cred"},
			shouldRedact: true,
		},
		{
			name:         "redacts arrays of secrets",
			body:         `{"refresh_tokens":["a","b","c"]}`,
			mustNotLeak:  []string{`"a"`, `"b"`, `"c"`},
			mustContain:  []string{"<redacted>"},
			shouldRedact: true,
		},
		{
			name:         "non-JSON body passes through",
			body:         "Bad Request: token=xyz",
			mustNotLeak:  nil,
			mustContain:  []string{"Bad Request"},
			shouldRedact: false,
		},
		{
			name:         "empty body",
			body:         "",
			mustNotLeak:  nil,
			mustContain:  nil,
			shouldRedact: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactedBodyForError([]byte(tc.body), 500)
			for _, leaked := range tc.mustNotLeak {
				if strings.Contains(got, leaked) {
					t.Errorf("expected %q to be removed, got: %s", leaked, got)
				}
			}
			for _, required := range tc.mustContain {
				if !strings.Contains(got, required) {
					t.Errorf("expected %q in output: %s", required, got)
				}
			}
		})
	}
}

func TestRedactedBodyForErrorHandlesUnicode(t *testing.T) {
	body := `{"client_secret":"shh","msg":"héllo"}`
	got := redactedBodyForError([]byte(body), 500)
	if strings.Contains(got, "shh") {
		t.Errorf("client_secret leaked: %s", got)
	}
	if !strings.Contains(got, "héllo") {
		t.Errorf("unicode survived: %s", got)
	}
}

func TestRedactedBodyForErrorTruncates(t *testing.T) {
	body := `"` + strings.Repeat("a", 1000) + `"`
	got := redactedBodyForError([]byte(body), 50)
	if !strings.Contains(got, "(truncated)") {
		t.Errorf("expected truncation marker, got: %s", got)
	}
}

func TestKeyLooksSensitive(t *testing.T) {
	cases := map[string]bool{
		"token":            true,
		"ANKRA_API_TOKEN":  true,
		"refresh_token":    true,
		"name":             false,
		"id":               false,
		"PrivateKey":       true,
		"client_secret":    true,
		"Cookie":           true,
		"consumer_key":     true,
		"acme_credentials": true,
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			if got := keyLooksSensitive(input); got != want {
				t.Errorf("keyLooksSensitive(%q) = %v, want %v", input, got, want)
			}
		})
	}
}

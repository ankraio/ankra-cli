package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrganisationOverrideHeader(t *testing.T) {
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(orgOverrideHeader)
		jsonResponse(t, w, http.StatusOK, []OrganisationSummary{})
	}))
	t.Cleanup(server.Close)

	c := New(testToken, server.URL)

	if _, err := c.ListOrganisations(); err != nil {
		t.Fatalf("ListOrganisations() error = %v", err)
	}
	if gotHeader != "" {
		t.Errorf("expected no override header without an override set, got %q", gotHeader)
	}

	c.SetOrganisationOverride("org-123")
	if c.OrganisationOverride() != "org-123" {
		t.Errorf("OrganisationOverride() = %q, want %q", c.OrganisationOverride(), "org-123")
	}
	if _, err := c.ListOrganisations(); err != nil {
		t.Fatalf("ListOrganisations() error = %v", err)
	}
	if gotHeader != "org-123" {
		t.Errorf("expected override header %q, got %q", "org-123", gotHeader)
	}

	c.SetOrganisationOverride("")
	if _, err := c.ListOrganisations(); err != nil {
		t.Fatalf("ListOrganisations() error = %v", err)
	}
	if gotHeader != "" {
		t.Errorf("expected override header cleared, got %q", gotHeader)
	}
}

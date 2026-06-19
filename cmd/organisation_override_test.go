package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestResolveOrgFlagToID(t *testing.T) {
	orgs := []client.OrganisationSummary{
		{OrganisationID: "11111111-1111-1111-1111-111111111111", Name: strPtrCmd("Acme Corp"), UserCurrent: true},
		{OrganisationID: "22222222-2222-2222-2222-222222222222", Name: strPtrCmd("Beta Inc")},
	}

	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "exact id", value: "22222222-2222-2222-2222-222222222222", want: "22222222-2222-2222-2222-222222222222"},
		{name: "name match", value: "Beta Inc", want: "22222222-2222-2222-2222-222222222222"},
		{name: "name case insensitive", value: "acme corp", want: "11111111-1111-1111-1111-111111111111"},
		{name: "unknown", value: "Nope", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setMockClient(t, &orgListMock{organisations: orgs})
			got, err := resolveOrgFlagToID(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveOrgFlagToID(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("resolveOrgFlagToID(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestResolveOrgFlagToIDAmbiguous(t *testing.T) {
	orgs := []client.OrganisationSummary{
		{OrganisationID: "aaaa", Name: strPtrCmd("Shared")},
		{OrganisationID: "bbbb", Name: strPtrCmd("Shared")},
	}
	setMockClient(t, &orgListMock{organisations: orgs})
	_, err := resolveOrgFlagToID("Shared")
	if err == nil {
		t.Fatal("expected error for ambiguous organisation name")
	}
	if !strings.Contains(err.Error(), "multiple organisations") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestResolveOrganisationReference(t *testing.T) {
	orgs := []client.OrganisationSummary{
		{OrganisationID: "11111111-1111-1111-1111-111111111111", Name: strPtrCmd("Acme Corp"), Slug: strPtrCmd("acme-corp")},
		{OrganisationID: "22222222-2222-2222-2222-222222222222", Name: strPtrCmd("Beta Inc"), Slug: strPtrCmd("beta-inc")},
	}

	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "exact id", value: "22222222-2222-2222-2222-222222222222", want: "22222222-2222-2222-2222-222222222222"},
		{name: "exact slug", value: "acme-corp", want: "11111111-1111-1111-1111-111111111111"},
		{name: "slug case insensitive", value: "ACME-CORP", want: "11111111-1111-1111-1111-111111111111"},
		{name: "name match", value: "Beta Inc", want: "22222222-2222-2222-2222-222222222222"},
		{name: "unknown", value: "nope", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveOrganisationReference(orgs, tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveOrganisationReference(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if !tt.wantErr && got.OrganisationID != tt.want {
				t.Errorf("resolveOrganisationReference(%q) = %q, want %q", tt.value, got.OrganisationID, tt.want)
			}
		})
	}
}

func TestResolveOrganisationReferenceAmbiguousSlug(t *testing.T) {
	orgs := []client.OrganisationSummary{
		{OrganisationID: "aaaa", Name: strPtrCmd("Acme One"), Slug: strPtrCmd("shared")},
		{OrganisationID: "bbbb", Name: strPtrCmd("Acme Two"), Slug: strPtrCmd("shared")},
	}
	_, err := resolveOrganisationReference(orgs, "shared")
	if err == nil {
		t.Fatal("expected error for ambiguous organisation slug")
	}
	if !strings.Contains(err.Error(), "slug") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestResolveOrgFlagToIDBySlug(t *testing.T) {
	orgs := []client.OrganisationSummary{
		{OrganisationID: "11111111-1111-1111-1111-111111111111", Name: strPtrCmd("Acme Corp"), Slug: strPtrCmd("acme-corp")},
	}
	setMockClient(t, &orgListMock{organisations: orgs})
	got, err := resolveOrgFlagToID("acme-corp")
	if err != nil {
		t.Fatalf("resolveOrgFlagToID(slug) error = %v", err)
	}
	if got != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("resolveOrgFlagToID(slug) = %q, want the matching id", got)
	}
}

func strPtrCmd(s string) *string {
	return &s
}

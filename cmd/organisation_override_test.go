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

func strPtrCmd(s string) *string {
	return &s
}

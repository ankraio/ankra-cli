package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

// orgPreviewSettingsMock captures the preview-settings reads and writes,
// keeping the exact change map so the present-vs-absent contract can be
// asserted: the endpoint distinguishes "not in the body" from "null".
type orgPreviewSettingsMock struct {
	baseMock

	settings   client.OrganisationPreviewSettings
	updateSeen []map[string]*string
}

func (m *orgPreviewSettingsMock) GetOrganisationPreviewSettings(
	ctx context.Context) (*client.OrganisationPreviewSettings, error) {
	settings := m.settings
	return &settings, nil
}

func (m *orgPreviewSettingsMock) UpdateOrganisationPreviewSettings(ctx context.Context,
	changes map[string]*string) (*client.OrganisationPreviewSettings, error) {
	m.updateSeen = append(m.updateSeen, changes)
	settings := m.settings
	return &settings, nil
}

func resetOrgAIEnvironmentFlags(t *testing.T) {
	t.Helper()
	for _, flagged := range []interface{ Flags() *pflag.FlagSet }{
		orgAIEnvironmentGetCmd, orgAIEnvironmentSetCmd,
	} {
		flagged.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	}
}

func runOrgAIEnvironment(t *testing.T, mock *orgPreviewSettingsMock, args ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	resetOrgAIEnvironmentFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs(args)
	executeError := rootCmd.Execute()
	return output.String(), executeError
}

func TestRunOrgAIEnvironmentGet_NamesTheFallbackWhenNoPreviewDomainIsSet(t *testing.T) {
	mock := &orgPreviewSettingsMock{}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "staging cluster's Ankra subzone") {
		t.Errorf("an unset preview domain must say what it falls back to, got %s", output)
	}
}

// The silent state this lane exists to end: a preview domain with neither a
// wildcard secret nor an issuer serves plain http (PLA-773).
func TestRunOrgAIEnvironmentGet_WarnsWhenPreviewsWouldBePlainHTTP(t *testing.T) {
	mock := &orgPreviewSettingsMock{settings: client.OrganisationPreviewSettings{
		DemoBaseDomain: "previews.smartoptics.dev"}}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "plain http") {
		t.Errorf("a preview domain with no TLS story must say so, got %s", output)
	}
}

func TestRunOrgAIEnvironmentGet_StaysQuietOnceAnIssuerIsNamed(t *testing.T) {
	mock := &orgPreviewSettingsMock{settings: client.OrganisationPreviewSettings{
		DemoBaseDomain: "previews.smartoptics.dev", DemoCertIssuerName: "letsencrypt-prod"}}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if strings.Contains(output, "plain http") {
		t.Errorf("a named issuer answers the TLS question, got %s", output)
	}
	if !strings.Contains(output, "letsencrypt-prod") {
		t.Errorf("the stored issuer must be shown, got %s", output)
	}
}

func TestRunOrgAIEnvironmentSet_SendsOnlyTheFlagsPassed(t *testing.T) {
	mock := &orgPreviewSettingsMock{}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "set",
		"--demo-cert-issuer", "letsencrypt-prod")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.updateSeen) != 1 {
		t.Fatalf("update calls = %d", len(mock.updateSeen))
	}
	changes := mock.updateSeen[0]
	if len(changes) != 1 {
		t.Fatalf("an untouched field must stay out of the body entirely, got %v", changes)
	}
	if changes["demo_cert_issuer_name"] == nil || *changes["demo_cert_issuer_name"] != "letsencrypt-prod" {
		t.Fatalf("changes = %v", changes)
	}
}

func TestRunOrgAIEnvironmentSet_AnEmptyValueClearsTheField(t *testing.T) {
	mock := &orgPreviewSettingsMock{}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "set",
		"--demo-base-domain", "")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	changes := mock.updateSeen[0]
	value, isPresent := changes["demo_base_domain"]
	if !isPresent || value != nil {
		t.Fatalf("an empty flag must send an explicit null, got %v", changes)
	}
}

func TestRunOrgAIEnvironmentSet_RefusesACallThatChangesNothing(t *testing.T) {
	mock := &orgPreviewSettingsMock{}
	_, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "set")
	if executeError == nil {
		t.Fatalf("a set with no flags must be a usage error, not an empty PUT")
	}
	if len(mock.updateSeen) != 0 {
		t.Fatalf("nothing should have been sent, got %v", mock.updateSeen)
	}
}

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

// The silent state this lane exists to end. Whether previews really land on
// plain http depends on what the staging cluster carries, so the verdict is
// the backend's and this command only has to surface it (PLA-773).
func TestRunOrgAIEnvironmentGet_SurfacesTheBackendsPlainHTTPWarning(t *testing.T) {
	mock := &orgPreviewSettingsMock{settings: client.OrganisationPreviewSettings{
		DemoBaseDomain: "previews.smartoptics.dev",
		PreviewTLSWarning: "Demos on previews.smartoptics.dev will be served over plain http: " +
			"the staging cluster carries no ACME HTTP-01 ClusterIssuer to request a certificate from."}}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "plain http") {
		t.Errorf("the warning must reach the operator, got %s", output)
	}
}

func TestRunOrgAIEnvironmentGet_StaysQuietWhenTheBackendReportsNoProblem(t *testing.T) {
	mock := &orgPreviewSettingsMock{settings: client.OrganisationPreviewSettings{
		DemoBaseDomain: "previews.smartoptics.dev", DemoCertIssuerName: "letsencrypt-prod"}}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if strings.Contains(output, "plain http") {
		t.Errorf("no warning from the backend means nothing to say, got %s", output)
	}
	if !strings.Contains(output, "letsencrypt-prod") {
		t.Errorf("the stored issuer must be shown, got %s", output)
	}
}

// A set that leaves previews on plain http must say so in its own output
// rather than reporting success and nothing else.
func TestRunOrgAIEnvironmentSet_ReportsThePlainHTTPWarningOnTheWrite(t *testing.T) {
	mock := &orgPreviewSettingsMock{settings: client.OrganisationPreviewSettings{
		DemoBaseDomain:    "previews.smartoptics.dev",
		PreviewTLSWarning: "Demos on previews.smartoptics.dev will be served over plain http.",
	}}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "set",
		"--demo-base-domain", "previews.smartoptics.dev")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "plain http") {
		t.Errorf("the write must not succeed silently, got %s", output)
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

// The renderer must not decide for itself when a warning is relevant. Today
// the backend only warns about an organisation's own preview domain, but a
// display tied to that field would silently drop any future warning.
func TestRunOrgAIEnvironmentGet_PrintsAWarningEvenWithNoPreviewDomain(t *testing.T) {
	mock := &orgPreviewSettingsMock{settings: client.OrganisationPreviewSettings{
		PreviewTLSWarning: "The staging cluster carries no ACME HTTP-01 ClusterIssuer."}}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "no ACME HTTP-01 ClusterIssuer") {
		t.Errorf("the backend's warning must survive the no-preview-domain branch, got %s", output)
	}
	if !strings.Contains(output, "staging cluster's Ankra subzone") {
		t.Errorf("the fallback line must still be printed, got %s", output)
	}
}

// -o yaml went through gopkg.in/yaml.v3, which ignores json tags and
// lowercases the Go field names, so it emitted demobasedomain while -o json
// emitted demo_base_domain. Anything scripted against the yaml form read the
// wrong keys.
func TestRunOrgAIEnvironmentGet_YAMLKeysMatchTheJSONKeys(t *testing.T) {
	mock := &orgPreviewSettingsMock{settings: client.OrganisationPreviewSettings{
		DemoBaseDomain: "previews.smartoptics.dev", DemoCertIssuerName: "letsencrypt-prod"}}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "get", "-o", "yaml")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	for _, key := range []string{
		"demo_base_domain:", "demo_ingress_class_name:", "demo_tls_secret_name:",
		"demo_cert_issuer_name:", "demo_preview_tls_warning:",
	} {
		if !strings.Contains(output, key) {
			t.Errorf("yaml output is missing %q, got %s", key, output)
		}
	}
	if strings.Contains(output, "demobasedomain") {
		t.Errorf("yaml fell back to the lowercased Go field names, got %s", output)
	}
}

// Clearing only the preview domain used to hide the three overrides, leaving
// stored values nothing could show and that take effect again the moment a
// domain is set.
func TestRunOrgAIEnvironmentGet_ShowsStoredOverridesWithNoPreviewDomain(t *testing.T) {
	mock := &orgPreviewSettingsMock{settings: client.OrganisationPreviewSettings{
		DemoTLSSecretName: "previews-wildcard-tls"}}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "previews-wildcard-tls") {
		t.Errorf("a stored override must stay visible, got %s", output)
	}
	if !strings.Contains(output, "inert until one is set") {
		t.Errorf("and must say it is not in force, got %s", output)
	}
}

func TestRunOrgAIEnvironmentGet_StaysTerseWhenNothingIsStored(t *testing.T) {
	mock := &orgPreviewSettingsMock{}
	output, executeError := runOrgAIEnvironment(t, mock, "org", "ai-environment", "get")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if strings.Contains(output, "Ingress class") || strings.Contains(output, "inert") {
		t.Errorf("nothing stored means nothing to list, got %s", output)
	}
}

// Registration and parsing read one map, so a renamed flag cannot become a
// flag cobra accepts and the update loop never matches.
func TestPreviewSettingFlagsAreRegisteredForEveryWireField(t *testing.T) {
	for flagName := range previewSettingFlagFields {
		if orgAIEnvironmentSetCmd.Flags().Lookup(flagName) == nil {
			t.Errorf("flag %q maps to a wire field but is not registered", flagName)
		}
	}
	orgAIEnvironmentSetCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if !strings.HasPrefix(flag.Name, "demo-") {
			return
		}
		if _, isKnown := previewSettingFlagFields[flag.Name]; !isKnown {
			t.Errorf("flag %q is registered but writes no wire field", flag.Name)
		}
	})
}

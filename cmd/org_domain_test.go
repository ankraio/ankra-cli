package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

// orgDomainMock captures the root-domain reads and writes plus the cluster
// zone inventory the migration lane needs.
type orgDomainMock struct {
	baseMock

	domain     client.OrganisationDomain
	setCalls   []string
	setError   error
	zones      []client.DnsClusterZone
	zonesError error
}

func (m *orgDomainMock) GetOrganisationDomain(ctx context.Context) (*client.OrganisationDomain, error) {
	domain := m.domain
	return &domain, nil
}

func (m *orgDomainMock) SetOrganisationDomain(ctx context.Context, rootDomain string) (*client.OrganisationDomain, error) {
	m.setCalls = append(m.setCalls, rootDomain)
	if m.setError != nil {
		return nil, m.setError
	}
	m.domain.DNSRootDomain = rootDomain
	domain := m.domain
	return &domain, nil
}

func (m *orgDomainMock) ListOrganisationClusterDnsZones(ctx context.Context) (*client.DnsClusterZonesListResult, error) {
	if m.zonesError != nil {
		return nil, m.zonesError
	}
	return &client.DnsClusterZonesListResult{Items: m.zones}, nil
}

func resetOrgDomainFlags(t *testing.T) {
	t.Helper()
	for _, flagged := range []interface{ Flags() *pflag.FlagSet }{
		orgDomainGetCmd, orgDomainSetCmd, orgDnsZonesCmd,
	} {
		flagged.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	}
	orgDomainUseDefault = false
}

func TestRunOrgDomainGet_ShowsTheRegisteredDomain(t *testing.T) {
	mock := &orgDomainMock{domain: client.OrganisationDomain{
		DNSRootDomain: "smartoptics.dev", DNSRootDomainDefault: "ankra.cc"}}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "domain", "get"})
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output.String())
	}
	if !strings.Contains(output.String(), "smartoptics.dev") {
		t.Errorf("output = %s", output.String())
	}
}

func TestRunOrgDomainGet_NamesThePlatformDefaultWhenUnset(t *testing.T) {
	mock := &orgDomainMock{domain: client.OrganisationDomain{DNSRootDomainDefault: "ankra.cc"}}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "domain", "get"})
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output.String())
	}
	if !strings.Contains(output.String(), "ankra.cc (platform default)") {
		t.Errorf("output = %s", output.String())
	}
}

func TestRunOrgDomainSet_SendsTheDomain(t *testing.T) {
	mock := &orgDomainMock{domain: client.OrganisationDomain{DNSRootDomainDefault: "ankra.cc"}}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "domain", "set", "smartoptics.dev"})
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output.String())
	}
	if len(mock.setCalls) != 1 || mock.setCalls[0] != "smartoptics.dev" {
		t.Fatalf("set calls = %v", mock.setCalls)
	}
}

func TestRunOrgDomainSet_DefaultClearsTheCustomDomain(t *testing.T) {
	mock := &orgDomainMock{domain: client.OrganisationDomain{
		DNSRootDomain: "smartoptics.dev", DNSRootDomainDefault: "ankra.cc"}}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "domain", "set", "--default"})
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output.String())
	}
	if len(mock.setCalls) != 1 || mock.setCalls[0] != "" {
		t.Fatalf("set calls = %v, want one empty value", mock.setCalls)
	}
}

func TestRunOrgDomainSet_RefusesWithNoDomainAndNoDefault(t *testing.T) {
	mock := &orgDomainMock{}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "domain", "set"})
	executeError := rootCmd.Execute()
	var coded *codedError
	if !errors.As(executeError, &coded) || coded.code != exitUsage {
		t.Fatalf("error = %v, want exitUsage", executeError)
	}
	if len(mock.setCalls) != 0 {
		t.Fatalf("the refusal must not reach the API: %v", mock.setCalls)
	}
}

func TestRunOrgDomainSet_ListsTheBlockingZonesAndRecords(t *testing.T) {
	mock := &orgDomainMock{
		domain: client.OrganisationDomain{DNSRootDomainDefault: "ankra.cc"},
		setError: &client.OrganisationDomainBlockedError{
			Detail: "Changing dns_root_domain requires removing the organisation's cluster DNS zones and DNS records first. Ankra then re-creates your zone under the new domain automatically.",
			ClusterZones: []client.OrganisationDomainBlockingClusterZone{
				{ClusterID: "c-1", ClusterName: "playground", FQDN: "abc.org1234.ankra.cc", State: "active"},
			},
			DnsRecords: []client.OrganisationDomainBlockingDnsRecord{
				{ID: "r-1", Name: "chat.org1234.ankra.cc", RecordType: "A", State: "active"},
			},
		},
	}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "domain", "set", "smartoptics.dev"})
	executeError := rootCmd.Execute()
	if executeError == nil {
		t.Fatalf("expected a refusal, output: %s", output.String())
	}
	message := executeError.Error()
	for _, expected := range []string{
		"playground", "abc.org1234.ankra.cc", "chat.org1234.ankra.cc",
		"ankra cluster domain <cluster> --remove", "ankra org dns delete <record>",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("refusal message missing %q:\n%s", expected, message)
		}
	}
}

func TestRunOrgDnsZones_ListsTheClusterDomains(t *testing.T) {
	mock := &orgDomainMock{zones: []client.DnsClusterZone{
		{ClusterID: "c-1", ClusterName: "playground", FQDN: "abc.org1234.ankra.cc", State: "active"},
	}}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "dns", "zones"})
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output.String())
	}
	if !strings.Contains(output.String(), "playground") ||
		!strings.Contains(output.String(), "abc.org1234.ankra.cc") {
		t.Errorf("output = %s", output.String())
	}
}

func TestRunOrgDnsZones_ReportsAnEmptyInventory(t *testing.T) {
	mock := &orgDomainMock{zones: []client.DnsClusterZone{}}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "dns", "zones"})
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output.String())
	}
	if !strings.Contains(output.String(), "No cluster domains") {
		t.Errorf("output = %s", output.String())
	}
}

func TestRunOrgDnsZones_StructuredOutputStaysParseable(t *testing.T) {
	mock := &orgDomainMock{zones: []client.DnsClusterZone{
		{ClusterID: "c-1", ClusterName: "playground", FQDN: "abc.org1234.ankra.cc", State: "pending"},
	}}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "dns", "zones", "-o", "json"})
	if executeError := rootCmd.Execute(); executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output.String())
	}
	if !strings.Contains(output.String(), `"cluster_name": "playground"`) {
		t.Errorf("json output = %s", output.String())
	}
}

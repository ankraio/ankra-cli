package cmd

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestRunOrgDomainSet_BlockedSwitchHonoursStructuredOutput(t *testing.T) {
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
			DnsRecordsTruncated: true,
		},
	}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"org", "domain", "set", "smartoptics.dev", "-o", "json"})
	executeError := rootCmd.Execute()
	if executeError == nil {
		t.Fatalf("a blocked switch must still fail, stdout: %s", stdout.String())
	}

	var document struct {
		Detail               string `json:"detail"`
		BlockingClusterZones []struct {
			ClusterName string `json:"cluster_name"`
		} `json:"blocking_cluster_zones"`
		BlockingDnsRecords []struct {
			Name string `json:"name"`
		} `json:"blocking_dns_records"`
		DnsRecordsTruncated bool `json:"blocking_dns_records_truncated"`
	}
	if unmarshalError := json.Unmarshal(stdout.Bytes(), &document); unmarshalError != nil {
		t.Fatalf("stdout must stay parseable json: %v\nstdout: %s", unmarshalError, stdout.String())
	}
	if len(document.BlockingClusterZones) != 1 || document.BlockingClusterZones[0].ClusterName != "playground" {
		t.Errorf("cluster zones = %+v", document.BlockingClusterZones)
	}
	if len(document.BlockingDnsRecords) != 1 || document.BlockingDnsRecords[0].Name != "chat.org1234.ankra.cc" {
		t.Errorf("dns records = %+v", document.BlockingDnsRecords)
	}
	if !document.DnsRecordsTruncated {
		t.Errorf("the truncation flag must survive into the structured output: %s", stdout.String())
	}
}

func TestRunOrgDomainSet_HumanRefusalNamesTheTruncation(t *testing.T) {
	mock := &orgDomainMock{
		domain: client.OrganisationDomain{DNSRootDomainDefault: "ankra.cc"},
		setError: &client.OrganisationDomainBlockedError{
			Detail: "blocked",
			DnsRecords: []client.OrganisationDomainBlockingDnsRecord{
				{ID: "r-1", Name: "chat.org1234.ankra.cc", RecordType: "A", State: "active"},
			},
			DnsRecordsTruncated: true,
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
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(executeError.Error(), "run 'ankra org dns list' for the full list") {
		t.Errorf("a truncated list must say so:\n%s", executeError.Error())
	}
}

func TestRunOrgDomainSet_SeparatesPlatformOwnedRecordsAndNamesThePlayground(t *testing.T) {
	// PLA-771: an admin was told to delete a record that the playground
	// provisioner wrote back two minutes later. The refusal has to say which
	// records deleting actually clears, and name the environment that has to
	// go for the rest.
	mock := &orgDomainMock{
		domain: client.OrganisationDomain{DNSRootDomainDefault: "ankra.cc"},
		setError: &client.OrganisationDomainBlockedError{
			Detail: "Changing dns_root_domain requires removing the organisation's cluster DNS zones and DNS records first. Ankra then re-creates your zone under the new domain automatically.",
			DnsRecords: []client.OrganisationDomainBlockingDnsRecord{
				{ID: "r-1", Name: "chat.org1234.ankra.cc", RecordType: "A", State: "active", CreatedBy: "mark@example.com"},
				{ID: "r-2", Name: "*.smdjc5s3hx", RecordType: "A", State: "active", CreatedBy: "playground_provisioner"},
			},
			Playgrounds: []client.OrganisationDomainBlockingPlayground{
				{ClusterID: "pg-1", ClusterName: "playground-smartoptics", Phase: "ready"},
			},
		},
	}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "domain", "set", "smartoptics.dev"})
	if executeError := rootCmd.Execute(); executeError == nil {
		t.Fatalf("expected a refusal, output: %s", output.String())
	} else {
		message := executeError.Error()
		for _, expected := range []string{
			"DNS records still under the current root:",
			"chat.org1234.ankra.cc",
			"ankra org dns delete <record>",
			"DNS records Ankra owns and re-creates:",
			"*.smdjc5s3hx",
			"created by playground_provisioner",
			"Playground environments publishing those records:",
			"playground-smartoptics",
			"ankra cluster playground destroy <cluster_id>",
		} {
			if !strings.Contains(message, expected) {
				t.Errorf("refusal message missing %q:\n%s", expected, message)
			}
		}
		// The admin's own record must not be listed under the section that
		// says deleting is futile, and the platform's must not be listed
		// under the one that says to delete it.
		adminSection := message[strings.Index(message, "DNS records still under the current root:"):strings.Index(message, "DNS records Ankra owns and re-creates:")]
		if strings.Contains(adminSection, "*.smdjc5s3hx") {
			t.Errorf("a platform-owned record was listed as deletable:\n%s", message)
		}
		platformSection := message[strings.Index(message, "DNS records Ankra owns and re-creates:"):]
		if strings.Contains(platformSection, "chat.org1234.ankra.cc") {
			t.Errorf("an admin's own record was listed as platform-owned:\n%s", message)
		}
	}
}

func TestRunOrgDomainSet_NamesTheExternalDNSInteractionOnBlockingZones(t *testing.T) {
	// Ask 1 of PLA-771: the refusal must explain what the clusters it lists
	// are going to do about it, and that their labels survive the switch.
	mock := &orgDomainMock{
		domain: client.OrganisationDomain{DNSRootDomainDefault: "ankra.cc"},
		setError: &client.OrganisationDomainBlockedError{
			Detail: "Changing dns_root_domain requires removing the organisation's cluster DNS zones and DNS records first. Ankra then re-creates your zone under the new domain automatically.",
			ClusterZones: []client.OrganisationDomainBlockingClusterZone{
				{ClusterID: "c-1", ClusterName: "so-development", FQDN: "axmndle4sl.qh4thi04cs.ankra.cc", State: "active"},
			},
		},
	}
	setMockClient(t, mock)
	resetOrgDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs([]string{"org", "domain", "set", "smartoptics.dev"})
	if executeError := rootCmd.Execute(); executeError == nil {
		t.Fatalf("expected a refusal, output: %s", output.String())
	} else {
		message := executeError.Error()
		for _, expected := range []string{
			"so-development", "axmndle4sl.qh4thi04cs.ankra.cc",
			"external-dns", "held", "--txt-owner-id", "never re-labels",
		} {
			if !strings.Contains(message, expected) {
				t.Errorf("refusal message missing %q:\n%s", expected, message)
			}
		}
	}
}

func TestOrganisationDomainBlockedMessage_TreatsAnUnknownWriterAsTheAdmins(t *testing.T) {
	// An older backend does not send created_by. Reading the empty string as
	// "platform-owned" would tell admins their own records cannot be deleted.
	blocked := &client.OrganisationDomainBlockedError{
		Detail: "refused",
		DnsRecords: []client.OrganisationDomainBlockingDnsRecord{
			{ID: "r-1", Name: "legacy.org1234.ankra.cc", RecordType: "CNAME", State: "active"},
		},
	}
	message := organisationDomainBlockedMessage(blocked)
	if !strings.Contains(message, "ankra org dns delete <record>") {
		t.Errorf("a record with no writer must stay deletable:\n%s", message)
	}
	if strings.Contains(message, "DNS records Ankra owns and re-creates:") {
		t.Errorf("a record with no writer must not be called platform-owned:\n%s", message)
	}
}

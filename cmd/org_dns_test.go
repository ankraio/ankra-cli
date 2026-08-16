package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

// resetOrgDnsFlags clears flag state the previous Execute left behind - flag
// values and their Changed markers persist on the shared command objects
// across tests in this package.
func resetOrgDnsFlags(t *testing.T) {
	t.Helper()
	for _, flagged := range []interface{ Flags() *pflag.FlagSet }{
		orgDnsAddCmd, orgDnsUpdateCmd, orgDnsDeleteCmd,
	} {
		flagged.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	}
}

// dnsMock captures DNS record calls so the command tests can assert the wire
// shapes and the id/name resolution. It embeds baseMock for the rest of the
// APIClient surface.
type dnsMock struct {
	baseMock

	zone        client.DnsZone
	records     []client.DnsRecord
	createCalls []createDnsCall
	updateCalls []updateDnsCall
	deleteCalls []string
	createErr   error
}

type createDnsCall struct {
	Name, RecordType, Content string
	TTL                       *int
}

type updateDnsCall struct {
	RecordID, Content string
	TTL               *int
}

func (m *dnsMock) GetOrganisationDnsZone(ctx context.Context) (*client.DnsZone, error) {
	return &m.zone, nil
}

func (m *dnsMock) ListOrganisationDnsRecords(ctx context.Context) (*client.DnsRecordsListResult, error) {
	return &client.DnsRecordsListResult{Items: m.records}, nil
}

func (m *dnsMock) CreateOrganisationDnsRecord(ctx context.Context, name, recordType, content string, ttl *int) (*client.DnsRecord, error) {
	m.createCalls = append(m.createCalls, createDnsCall{Name: name, RecordType: recordType, Content: content, TTL: ttl})
	if m.createErr != nil {
		return nil, m.createErr
	}
	return &client.DnsRecord{ID: "3f0a2f7e-0000-4000-8000-000000000001",
		Name: name + ".org1234.ankra.cc", RecordType: recordType, Content: content, TTL: ttl, State: "pending"}, nil
}

func (m *dnsMock) UpdateOrganisationDnsRecord(ctx context.Context, recordID, content string, ttl *int) (*client.DnsRecord, error) {
	m.updateCalls = append(m.updateCalls, updateDnsCall{RecordID: recordID, Content: content, TTL: ttl})
	return &client.DnsRecord{ID: recordID, Name: "chat.org1234.ankra.cc", RecordType: "CNAME",
		Content: content, TTL: ttl, State: "pending"}, nil
}

func (m *dnsMock) DeleteOrganisationDnsRecord(ctx context.Context, recordID string) error {
	m.deleteCalls = append(m.deleteCalls, recordID)
	return nil
}

func TestRunOrgDnsZone(t *testing.T) {
	mock := &dnsMock{zone: client.DnsZone{FQDN: "org1234.ankra.cc", State: "active"}}
	setMockClient(t, mock)
	resetOrgDnsFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "dns", "zone"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "org1234.ankra.cc") || !strings.Contains(out.String(), "active") {
		t.Errorf("expected zone fqdn and state, got: %s", out.String())
	}
}

func TestRunOrgDnsAdd_SendsLabelTypeContentAndTTL(t *testing.T) {
	mock := &dnsMock{}
	setMockClient(t, mock)
	resetOrgDnsFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "dns", "add", "chat", "cname", "lb-1.upcloudlb.com", "--ttl", "300"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.createCalls) != 1 {
		t.Fatalf("expected one create call, got %d", len(mock.createCalls))
	}
	got := mock.createCalls[0]
	if got.Name != "chat" || got.RecordType != "CNAME" || got.Content != "lb-1.upcloudlb.com" {
		t.Errorf("create call = %+v", got)
	}
	if got.TTL == nil || *got.TTL != 300 {
		t.Errorf("ttl = %v, want 300", got.TTL)
	}
	if !strings.Contains(out.String(), "chat.org1234.ankra.cc") || !strings.Contains(out.String(), "pending") {
		t.Errorf("expected created fqdn and state, got: %s", out.String())
	}
}

func TestRunOrgDnsAdd_OmittedTTLStaysAuto(t *testing.T) {
	mock := &dnsMock{}
	setMockClient(t, mock)
	resetOrgDnsFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "dns", "add", "app", "A", "203.0.113.10"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.createCalls) != 1 || mock.createCalls[0].TTL != nil {
		t.Fatalf("expected one create call with nil ttl, got %+v", mock.createCalls)
	}
}

func TestRunOrgDnsUpdate_ResolvesNameToID(t *testing.T) {
	mock := &dnsMock{records: []client.DnsRecord{
		{ID: "3f0a2f7e-0000-4000-8000-0000000000aa", Name: "chat.org1234.ankra.cc", RecordType: "CNAME", Content: "old.example.com", State: "active"},
		{ID: "3f0a2f7e-0000-4000-8000-0000000000bb", Name: "app.org1234.ankra.cc", RecordType: "A", Content: "203.0.113.10", State: "active"},
	}}
	setMockClient(t, mock)
	resetOrgDnsFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "dns", "update", "chat", "new.example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.updateCalls) != 1 {
		t.Fatalf("expected one update call, got %d", len(mock.updateCalls))
	}
	got := mock.updateCalls[0]
	if got.RecordID != "3f0a2f7e-0000-4000-8000-0000000000aa" || got.Content != "new.example.com" || got.TTL != nil {
		t.Errorf("update call = %+v", got)
	}
}

func TestRunOrgDnsDelete_AmbiguousNameNeedsType(t *testing.T) {
	mock := &dnsMock{records: []client.DnsRecord{
		{ID: "3f0a2f7e-0000-4000-8000-0000000000aa", Name: "eu.org1234.ankra.cc", RecordType: "A", Content: "203.0.113.10", State: "active"},
		{ID: "3f0a2f7e-0000-4000-8000-0000000000bb", Name: "eu.org1234.ankra.cc", RecordType: "TXT", Content: "v=spf1 ~all", State: "active"},
	}}
	setMockClient(t, mock)
	resetOrgDnsFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "dns", "delete", "eu", "--yes"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected an ambiguity error, got err=%v output=%s", err, out.String())
	}
	if len(mock.deleteCalls) != 0 {
		t.Fatalf("no delete must be issued on ambiguity, got %v", mock.deleteCalls)
	}

	cmd.SetArgs([]string{"org", "dns", "delete", "eu", "--type", "TXT", "--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out.String())
	}
	if len(mock.deleteCalls) != 1 || mock.deleteCalls[0] != "3f0a2f7e-0000-4000-8000-0000000000bb" {
		t.Fatalf("delete calls = %v", mock.deleteCalls)
	}
}

func TestRunOrgDnsDelete_UnknownNameFails(t *testing.T) {
	mock := &dnsMock{}
	setMockClient(t, mock)
	resetOrgDnsFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"org", "dns", "delete", "missing", "--yes"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "no DNS record matches") {
		t.Fatalf("expected a not-found resolution error, got %v", err)
	}
}

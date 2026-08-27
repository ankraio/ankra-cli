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

// resetOrgCloudflareFlags clears flag state a previous Execute left behind -
// values and their Changed markers persist on the shared command objects
// across tests in this package, and the Changed marker is exactly what the
// "omitted keeps the current value" behaviour reads.
func resetOrgCloudflareFlags(t *testing.T) {
	t.Helper()
	for _, flagged := range []interface{ Flags() *pflag.FlagSet }{
		orgCloudflareConnectCmd, orgCloudflareDomainsCmd, orgCloudflareRecordsCmd,
		orgCloudflareAddCmd, orgCloudflareUpdateCmd, orgCloudflareDeleteCmd,
	} {
		flagged.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	}
}

// cloudflareMock captures the calls the commands make so the tests can assert
// the wire shapes and the domain/record resolution.
type cloudflareMock struct {
	baseMock

	credentials  []client.CloudflareCredential
	domains      []client.CloudflareDomain
	records      []client.CloudflareRecord
	verification *client.CloudflareVerification

	domainNameFilters []string
	createCalls       []client.CreateCloudflareRecordInput
	updateCalls       []client.UpdateCloudflareRecordInput
	deleteCalls       []string
	connectCalls      []connectCloudflareCall
	verifyCalls       []string

	listDomainsErr error
}

type connectCloudflareCall struct {
	Name, APIToken, AccountID string
}

func (m *cloudflareMock) ListCloudflareCredentials(ctx context.Context) (*client.CloudflareCredentialsListResult, error) {
	return &client.CloudflareCredentialsListResult{Items: m.credentials}, nil
}

func (m *cloudflareMock) VerifyCloudflareToken(ctx context.Context, apiToken, accountID string) (*client.CloudflareVerification, error) {
	m.verifyCalls = append(m.verifyCalls, apiToken)
	return m.verification, nil
}

func (m *cloudflareMock) ConnectCloudflareCredential(ctx context.Context, name, apiToken, accountID string) (*client.CloudflareVerification, error) {
	m.connectCalls = append(m.connectCalls, connectCloudflareCall{Name: name, APIToken: apiToken, AccountID: accountID})
	return m.verification, nil
}

func (m *cloudflareMock) ListCloudflareDomains(ctx context.Context, credentialName, domainName string) (*client.CloudflareDomainsListResult, error) {
	if m.listDomainsErr != nil {
		return nil, m.listDomainsErr
	}
	m.domainNameFilters = append(m.domainNameFilters, domainName)
	if domainName == "" {
		return &client.CloudflareDomainsListResult{Items: m.domains}, nil
	}
	matched := make([]client.CloudflareDomain, 0, 1)
	for _, domain := range m.domains {
		if domain.Name == domainName {
			matched = append(matched, domain)
		}
	}
	return &client.CloudflareDomainsListResult{Items: matched}, nil
}

func (m *cloudflareMock) ListCloudflareRecords(ctx context.Context, credentialName, domainID, nameFilter, typeFilter string) (*client.CloudflareRecordsListResult, error) {
	return &client.CloudflareRecordsListResult{Items: m.records}, nil
}

func (m *cloudflareMock) CreateCloudflareRecord(ctx context.Context, credentialName, domainID string, input client.CreateCloudflareRecordInput) (*client.CloudflareRecord, error) {
	m.createCalls = append(m.createCalls, input)
	return &client.CloudflareRecord{
		Name: input.Name, RecordType: input.RecordType, Content: input.Content,
		Proxied: input.Proxied != nil && *input.Proxied,
	}, nil
}

func (m *cloudflareMock) UpdateCloudflareRecord(ctx context.Context, credentialName, domainID, recordID string, input client.UpdateCloudflareRecordInput) (*client.CloudflareRecord, error) {
	m.updateCalls = append(m.updateCalls, input)
	return &client.CloudflareRecord{Name: "app.example.com", RecordType: "A", Content: input.Content}, nil
}

func (m *cloudflareMock) DeleteCloudflareRecord(ctx context.Context, credentialName, domainID, recordID string) error {
	m.deleteCalls = append(m.deleteCalls, recordID)
	return nil
}

func cloudflareFixture() *cloudflareMock {
	return &cloudflareMock{
		credentials: []client.CloudflareCredential{{
			Name: "production", AccountName: "Acme Ltd", TokenID: "token-1", State: "active",
		}},
		domains: []client.CloudflareDomain{{
			ID: "zone-1", Name: "example.com", Status: "active", IsActive: true, Type: "full",
			NameServers: []string{"ns1.cloudflare.com", "ns2.cloudflare.com"},
		}},
		records: []client.CloudflareRecord{
			{
				ID: "record-1", ZoneID: "zone-1", ZoneName: "example.com", Name: "app.example.com",
				RecordType: "A", Content: "203.0.113.10", TTL: 300, ManagedBy: "ankra",
			},
			{
				ID: "record-2", ZoneID: "zone-1", ZoneName: "example.com", Name: "legacy.example.com",
				RecordType: "A", Content: "198.51.100.7", TTL: 1, IsTTLAutomatic: true, ManagedBy: "external",
			},
		},
		verification: &client.CloudflareVerification{
			Status: "active", IsActive: true, AccountName: "Acme Ltd",
			ZoneCount: 1, ZoneNames: []string{"example.com"},
		},
	}
}

func runCloudflare(t *testing.T, mock *cloudflareMock, stdin string, args ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	resetOrgCloudflareFlags(t)

	cmd := rootCmd
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"org", "cloudflare"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func TestOrgCloudflareDomainsListsReachableDomains(t *testing.T) {
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "", "domains")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "example.com") || !strings.Contains(out, "zone-1") {
		t.Errorf("expected the domain and its zone id, got: %s", out)
	}
}

// A named domain is resolved through the server-side filter, not by walking
// the whole listing.
func TestOrgCloudflareDomainsResolvesANamedDomainServerSide(t *testing.T) {
	mock := cloudflareFixture()
	if _, err := runCloudflare(t, mock, "", "domains", "example.com"); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(mock.domainNameFilters) != 1 || mock.domainNameFilters[0] != "example.com" {
		t.Errorf("name filters = %v; the lookup was not server-side", mock.domainNameFilters)
	}
}

func TestOrgCloudflareRecordsShowsWhichArrivedThroughAnkra(t *testing.T) {
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "", "records", "example.com")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "ankra") || !strings.Contains(out, "external") {
		t.Errorf("expected the managed-by column to distinguish the records, got: %s", out)
	}
	// The automatic TTL renders as Auto rather than the raw sentinel 1.
	if !strings.Contains(out, "Auto") {
		t.Errorf("expected an Auto ttl, got: %s", out)
	}
}

func TestOrgCloudflareAddSendsTheRecordAndTTL(t *testing.T) {
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "", "add", "example.com", "app", "a", "203.0.113.10", "--ttl", "300")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.createCalls) != 1 {
		t.Fatalf("expected one create call, got %d", len(mock.createCalls))
	}
	got := mock.createCalls[0]
	if got.Name != "app" || got.RecordType != "A" || got.Content != "203.0.113.10" {
		t.Errorf("create call = %+v", got)
	}
	if got.TTL == nil || *got.TTL != 300 {
		t.Errorf("ttl = %v, want 300", got.TTL)
	}
	if got.Proxied != nil {
		t.Errorf("proxied = %v; an unset --proxied must be omitted", got.Proxied)
	}
}

func TestOrgCloudflareAddSendsTheProxyFlagWhenSet(t *testing.T) {
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "", "add", "example.com", "www", "cname", "app.example.com", "--proxied")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	got := mock.createCalls[0]
	if got.Proxied == nil || !*got.Proxied {
		t.Errorf("proxied = %v, want true", got.Proxied)
	}
	if !strings.Contains(out, "proxied through Cloudflare") {
		t.Errorf("expected the output to name the proxy, got: %s", out)
	}
}

// The backend's PATCH leaves an absent field as it stands, so an omitted flag
// must be omitted from the body - not sent as a zero that resets it.
func TestOrgCloudflareUpdateOmitsTheFlagsTheCallerDidNotPass(t *testing.T) {
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "", "update", "example.com", "app", "203.0.113.99")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.updateCalls) != 1 {
		t.Fatalf("expected one update call, got %d", len(mock.updateCalls))
	}
	got := mock.updateCalls[0]
	if got.Content != "203.0.113.99" {
		t.Errorf("content = %q", got.Content)
	}
	if got.TTL != nil || got.Proxied != nil || got.Priority != nil {
		t.Errorf("unset flags leaked into the body: %+v", got)
	}
}

func TestOrgCloudflareUpdateSendsTheFlagsTheCallerDidPass(t *testing.T) {
	mock := cloudflareFixture()
	if _, err := runCloudflare(t, mock, "", "update", "example.com", "app", "203.0.113.99",
		"--ttl", "3600", "--proxied"); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	got := mock.updateCalls[0]
	if got.TTL == nil || *got.TTL != 3600 {
		t.Errorf("ttl = %v, want 3600", got.TTL)
	}
	if got.Proxied == nil || !*got.Proxied {
		t.Errorf("proxied = %v, want true", got.Proxied)
	}
}

// The delete prompt has to name the record: "delete app" is not enough to
// approve removing live DNS.
func TestOrgCloudflareDeletePromptNamesTheRecord(t *testing.T) {
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "y\n", "delete", "example.com", "app")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "A app.example.com -> 203.0.113.10") {
		t.Errorf("the prompt did not name the record: %s", out)
	}
	if len(mock.deleteCalls) != 1 || mock.deleteCalls[0] != "record-1" {
		t.Errorf("delete calls = %v", mock.deleteCalls)
	}
}

// A record Ankra did not create is called out: something outside the platform
// put it there.
func TestOrgCloudflareDeleteCallsOutAnExternalRecord(t *testing.T) {
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "y\n", "delete", "example.com", "legacy")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "not created through Ankra") {
		t.Errorf("the prompt did not call out the external record: %s", out)
	}
}

func TestOrgCloudflareDeleteRefusesAnUnknownRecord(t *testing.T) {
	mock := cloudflareFixture()
	_, err := runCloudflare(t, mock, "y\n", "delete", "example.com", "nothing-like-this")
	if err == nil {
		t.Fatal("an unknown record was accepted")
	}
	if !strings.Contains(err.Error(), "no DNS record matches") {
		t.Errorf("error = %v", err)
	}
	if len(mock.deleteCalls) != 0 {
		t.Errorf("a delete was issued for an unknown record: %v", mock.deleteCalls)
	}
}

// An organisation with no credential gets the next step, not a bare 404.
func TestOrgCloudflareDomainsGuidesAnUnconnectedOrganisation(t *testing.T) {
	mock := cloudflareFixture()
	mock.listDomainsErr = client.ErrCloudflareNotConnected
	_, err := runCloudflare(t, mock, "", "domains")
	if err == nil {
		t.Fatal("an unconnected organisation reported success")
	}
	if !strings.Contains(err.Error(), "ankra org cloudflare connect") {
		t.Errorf("error = %v; it must name the next step", err)
	}
}

// The token never comes from a flag - it would land in shell history and the
// process list.
func TestOrgCloudflareConnectRefusesWithNoTokenSupplied(t *testing.T) {
	t.Setenv("ANKRA_CLOUDFLARE_API_TOKEN", "")
	mock := cloudflareFixture()
	_, err := runCloudflare(t, mock, "", "connect", "production")
	if err == nil {
		t.Fatal("connect succeeded with no token")
	}
	if !strings.Contains(err.Error(), "ANKRA_CLOUDFLARE_API_TOKEN") || !strings.Contains(err.Error(), "--token-stdin") {
		t.Errorf("error = %v; it must name both input paths", err)
	}
	if len(mock.connectCalls) != 0 {
		t.Errorf("a connect was issued with no token: %v", mock.connectCalls)
	}
}

func TestOrgCloudflareConnectReadsTheTokenFromTheEnvironment(t *testing.T) {
	t.Setenv("ANKRA_CLOUDFLARE_API_TOKEN", "a-scoped-token")
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "", "connect", "production")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.connectCalls) != 1 || mock.connectCalls[0].APIToken != "a-scoped-token" {
		t.Fatalf("connect calls = %+v", mock.connectCalls)
	}
	if !strings.Contains(out, "Reaches 1 domain") || !strings.Contains(out, "example.com") {
		t.Errorf("expected the verification summary, got: %s", out)
	}
}

func TestOrgCloudflareConnectReadsTheTokenFromStdin(t *testing.T) {
	t.Setenv("ANKRA_CLOUDFLARE_API_TOKEN", "")
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "piped-token\n", "connect", "production", "--token-stdin")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.connectCalls) != 1 || mock.connectCalls[0].APIToken != "piped-token" {
		t.Fatalf("connect calls = %+v", mock.connectCalls)
	}
}

// --verify-only checks a token and stores nothing.
func TestOrgCloudflareConnectVerifyOnlyStoresNothing(t *testing.T) {
	t.Setenv("ANKRA_CLOUDFLARE_API_TOKEN", "a-scoped-token")
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "", "connect", "--verify-only")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if len(mock.verifyCalls) != 1 {
		t.Fatalf("verify calls = %v", mock.verifyCalls)
	}
	if len(mock.connectCalls) != 0 {
		t.Errorf("--verify-only stored a credential: %+v", mock.connectCalls)
	}
}

// A disabled or expired token reports what to do rather than claiming success.
func TestOrgCloudflareConnectReportsANonActiveToken(t *testing.T) {
	t.Setenv("ANKRA_CLOUDFLARE_API_TOKEN", "a-scoped-token")
	mock := cloudflareFixture()
	mock.verification = &client.CloudflareVerification{Status: "expired", IsActive: false}
	out, err := runCloudflare(t, mock, "", "connect", "--verify-only")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "expired") || !strings.Contains(out, "Renew or re-enable") {
		t.Errorf("expected the renewal guidance, got: %s", out)
	}
}

func TestOrgCloudflareCredentialsListsWithoutTheToken(t *testing.T) {
	mock := cloudflareFixture()
	out, err := runCloudflare(t, mock, "", "credentials")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "production") || !strings.Contains(out, "Acme Ltd") {
		t.Errorf("expected the credential row, got: %s", out)
	}
	if !strings.Contains(out, "never") {
		t.Errorf("a credential with no expiry should render 'never', got: %s", out)
	}
}

func TestOrgCloudflareCredentialsGuidesAnEmptyOrganisation(t *testing.T) {
	mock := cloudflareFixture()
	mock.credentials = nil
	out, err := runCloudflare(t, mock, "", "credentials")
	if err != nil {
		t.Fatalf("execute failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "ankra org cloudflare connect") {
		t.Errorf("expected the connect guidance, got: %s", out)
	}
}

// The two sentinels carry different next steps, so they must not collapse.
func TestCloudflareErrorKeepsTheSentinelsApart(t *testing.T) {
	notConnected := cloudflareError(client.ErrCloudflareNotConnected, "list")
	if !strings.Contains(notConnected.Error(), "ankra org cloudflare connect") {
		t.Errorf("not-connected error = %v", notConnected)
	}
	notFound := cloudflareError(client.ErrCloudflareNotFound, "list")
	if !errors.Is(notFound, client.ErrCloudflareNotFound) {
		t.Errorf("not-found error lost its sentinel: %v", notFound)
	}
	other := cloudflareError(errors.New("connection reset"), "list domains")
	if !strings.Contains(other.Error(), "list domains") {
		t.Errorf("a generic error lost its action: %v", other)
	}
}

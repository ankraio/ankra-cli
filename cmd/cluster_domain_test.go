package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/pflag"
)

const domainTestClusterID = "3f0a2f7e-0000-4000-8000-0000000000aa"

// clusterDomainMock records which DNS-zone verb the command reached for, so
// the tests can prove the plain lookup never mutates.
type clusterDomainMock struct {
	baseMock

	readCalls    []string
	enableCalls  []string
	disableCalls []string
	zone         client.ClusterDNSZoneResponse
	disableState string
}

func (m *clusterDomainMock) GetClusterDNSZone(clusterID string) (*client.ClusterDNSZoneResponse, error) {
	m.readCalls = append(m.readCalls, clusterID)
	zone := m.zone
	return &zone, nil
}

func (m *clusterDomainMock) EnableClusterDNSZone(clusterID string) (*client.ClusterDNSZoneResponse, error) {
	m.enableCalls = append(m.enableCalls, clusterID)
	return &client.ClusterDNSZoneResponse{Success: true, FQDN: "abc.org1234.ankra.cc", State: "pending"}, nil
}

func (m *clusterDomainMock) DisableClusterDNSZone(clusterID string) (*client.ClusterDNSZoneResponse, error) {
	m.disableCalls = append(m.disableCalls, clusterID)
	state := m.disableState
	if state == "" {
		state = "deleting"
	}
	return &client.ClusterDNSZoneResponse{Success: true, FQDN: "abc.org1234.ankra.cc", State: state}, nil
}

func resetClusterDomainFlags(t *testing.T) {
	t.Helper()
	clusterDomainCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		_ = flag.Value.Set(flag.DefValue)
		flag.Changed = false
	})
	clusterDomainEnable = false
	clusterDomainRemove = false
}

func runClusterDomain(t *testing.T, mock *clusterDomainMock, arguments ...string) (string, error) {
	t.Helper()
	setMockClient(t, mock)
	resetClusterDomainFlags(t)
	output := new(bytes.Buffer)
	rootCmd.SetOut(output)
	rootCmd.SetErr(output)
	rootCmd.SetArgs(append([]string{"cluster", "domain"}, arguments...))
	executeError := rootCmd.Execute()
	return output.String(), executeError
}

func TestRunClusterDomain_PlainLookupNeverEnablesTheZone(t *testing.T) {
	mock := &clusterDomainMock{zone: client.ClusterDNSZoneResponse{
		Success: true, FQDN: "abc.org1234.ankra.cc", State: "active"}}

	output, executeError := runClusterDomain(t, mock, domainTestClusterID)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.enableCalls) != 0 || len(mock.disableCalls) != 0 {
		t.Fatalf("the plain lookup mutated: enable=%v disable=%v", mock.enableCalls, mock.disableCalls)
	}
	if len(mock.readCalls) != 1 || mock.readCalls[0] != domainTestClusterID {
		t.Fatalf("read calls = %v", mock.readCalls)
	}
	if !strings.Contains(output, "abc.org1234.ankra.cc") || !strings.Contains(output, "active") {
		t.Errorf("output = %s", output)
	}
}

func TestRunClusterDomain_ReportsNoneAndPointsAtEnable(t *testing.T) {
	mock := &clusterDomainMock{zone: client.ClusterDNSZoneResponse{Success: true, State: "none"}}

	output, executeError := runClusterDomain(t, mock, domainTestClusterID)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.enableCalls) != 0 {
		t.Fatalf("a cluster without a zone must not be enabled by a lookup: %v", mock.enableCalls)
	}
	if !strings.Contains(output, "none") || !strings.Contains(output, "--enable") {
		t.Errorf("output = %s, want the none state and the --enable hint", output)
	}
}

func TestRunClusterDomain_EnableCreatesTheZone(t *testing.T) {
	mock := &clusterDomainMock{}

	output, executeError := runClusterDomain(t, mock, domainTestClusterID, "--enable")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.enableCalls) != 1 || len(mock.readCalls) != 0 {
		t.Fatalf("enable=%v read=%v", mock.enableCalls, mock.readCalls)
	}
	if !strings.Contains(output, "pending") {
		t.Errorf("output = %s", output)
	}
}

func TestRunClusterDomain_RemoveTearsTheZoneDown(t *testing.T) {
	mock := &clusterDomainMock{}

	output, executeError := runClusterDomain(t, mock, domainTestClusterID, "--remove")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if len(mock.disableCalls) != 1 || len(mock.readCalls) != 0 {
		t.Fatalf("disable=%v read=%v", mock.disableCalls, mock.readCalls)
	}
	if !strings.Contains(output, "deleting") {
		t.Errorf("output = %s", output)
	}
}

func TestRunClusterDomain_RefusesEnableWithRemove(t *testing.T) {
	mock := &clusterDomainMock{}

	output, executeError := runClusterDomain(t, mock, domainTestClusterID, "--enable", "--remove")
	if executeError == nil {
		t.Fatalf("expected a usage error, output: %s", output)
	}
	var coded *codedError
	if !errors.As(executeError, &coded) || coded.code != exitUsage {
		t.Fatalf("error = %v, want exitUsage", executeError)
	}
	if len(mock.readCalls)+len(mock.enableCalls)+len(mock.disableCalls) != 0 {
		t.Fatalf("the refusal must not reach the API")
	}
}

func TestRunClusterDomain_StructuredOutputStaysParseable(t *testing.T) {
	mock := &clusterDomainMock{zone: client.ClusterDNSZoneResponse{Success: true, State: "none"}}

	output, executeError := runClusterDomain(t, mock, domainTestClusterID, "-o", "json")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, `"state": "none"`) {
		t.Errorf("json output = %s", output)
	}
	if strings.Contains(output, "--enable") {
		t.Errorf("the human hint leaked into stdout: %s", output)
	}
}

func TestRunClusterDomain_RemoveDoesNotEmitTheEnableHint(t *testing.T) {
	// A backend that answered a removal with state "none" must not answer it
	// with the empty-cluster hint - "this cluster has no public domain,
	// create one" reads as though the removal did nothing. Naming --enable as
	// the way back is a different statement and belongs there: the removal is
	// held, and that is how the hold is withdrawn.
	mock := &clusterDomainMock{}
	mock.disableState = clusterDomainStateNone

	output, executeError := runClusterDomain(t, mock, domainTestClusterID, "--remove")
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if strings.Contains(output, "has no public domain") {
		t.Errorf("the removal path emitted the empty-cluster hint:\n%s", output)
	}
	if !strings.Contains(output, "held") {
		t.Errorf("the removal path must say the removal is held:\n%s", output)
	}
}

func TestRunClusterDomain_ReportsAHeldRemoval(t *testing.T) {
	// The window PLA-771 was watched from: the teardown has finished, the
	// zone row is gone, and the operator is looking for the difference
	// between "removed and staying removed" and "removed a moment ago".
	mock := &clusterDomainMock{zone: client.ClusterDNSZoneResponse{
		Success: true, State: clusterDomainStateNone, OptedOut: true}}

	output, executeError := runClusterDomain(t, mock, domainTestClusterID)
	if executeError != nil {
		t.Fatalf("execute failed: %v\noutput: %s", executeError, output)
	}
	if !strings.Contains(output, "Opted out: yes") {
		t.Errorf("a held removal must report the hold:\n%s", output)
	}
	if strings.Contains(output, "has no public domain") {
		t.Errorf("a held removal is not an empty cluster:\n%s", output)
	}
}

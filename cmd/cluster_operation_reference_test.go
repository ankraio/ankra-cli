package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

const (
	referenceFixtureID      = "40aa7c1d-0000-4000-8000-000000000001"
	referenceFixtureTwin    = "40aa7c1d-0000-4000-8000-000000000002"
	referenceFixtureOtherID = "9e52bc17-0000-4000-8000-000000000003"
)

func referenceExecutions(ids ...string) []client.ExecutionSummary {
	executions := make([]client.ExecutionSummary, 0, len(ids))
	for _, id := range ids {
		executions = append(executions, client.ExecutionSummary{ID: id, DisplayName: "Restart node"})
	}
	return executions
}

// withoutSelectedCluster points HOME somewhere with no selected cluster, so a
// short id is looked up across the organisation.
func withoutSelectedCluster(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestFullExecutionIDIsPassedThroughWithoutACall(t *testing.T) {
	withoutSelectedCluster(t)
	mock := &executionsPageMock{}
	setMockClient(t, mock)

	resolved, resolveError := resolveExecutionReference(&cobra.Command{}, referenceFixtureID)

	if resolveError != nil || resolved != referenceFixtureID {
		t.Fatalf("resolved = %q, %v", resolved, resolveError)
	}
	if len(mock.requestedOptions) != 0 {
		t.Errorf("a full id spent %d listing calls", len(mock.requestedOptions))
	}
}

func TestShortExecutionIDResolvesToTheOneItStarts(t *testing.T) {
	withoutSelectedCluster(t)
	mock := &executionsPageMock{pages: [][]client.ExecutionSummary{
		referenceExecutions(referenceFixtureOtherID), referenceExecutions(referenceFixtureID),
	}}
	setMockClient(t, mock)

	resolved, resolveError := resolveExecutionReference(&cobra.Command{}, "40AA7C1D")

	if resolveError != nil {
		t.Fatalf("resolve error = %v", resolveError)
	}
	if resolved != referenceFixtureID {
		t.Errorf("resolved = %q, want %q", resolved, referenceFixtureID)
	}
	for _, options := range mock.requestedOptions {
		if !options.IncludeInternalExecutions {
			t.Errorf("listing call hid internal executions: %+v", options)
		}
		if options.ClusterID != "" {
			t.Errorf("no cluster is selected, but the lookup was scoped to %q", options.ClusterID)
		}
	}
}

func TestShortExecutionIDLooksInTheSelectedCluster(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeSelectedCluster(t, "cluster-uuid", "staging")
	mock := &executionsPageMock{pages: [][]client.ExecutionSummary{referenceExecutions(referenceFixtureID)}}
	setMockClient(t, mock)

	if _, resolveError := resolveExecutionReference(&cobra.Command{}, "40aa7c1d"); resolveError != nil {
		t.Fatalf("resolve error = %v", resolveError)
	}
	if mock.requestedOptions[0].ClusterID != "cluster-uuid" {
		t.Errorf("lookup was not scoped to the selected cluster: %+v", mock.requestedOptions[0])
	}
}

func TestAmbiguousShortExecutionIDIsRefusedAndNamesBoth(t *testing.T) {
	withoutSelectedCluster(t)
	setMockClient(t, &executionsPageMock{pages: [][]client.ExecutionSummary{
		referenceExecutions(referenceFixtureID, referenceFixtureTwin),
	}})

	_, resolveError := resolveExecutionReference(&cobra.Command{}, "40aa7c1d")

	if resolveError == nil {
		t.Fatal("two matches resolved to one execution")
	}
	if got := exitCodeFor(resolveError); got != exitUsage {
		t.Errorf("exit code = %d, want %d", got, exitUsage)
	}
	for _, want := range []string{referenceFixtureID, referenceFixtureTwin} {
		if !strings.Contains(resolveError.Error(), want) {
			t.Errorf("error %q does not name %s", resolveError, want)
		}
	}
}

func TestUnknownShortExecutionIDIsNotFound(t *testing.T) {
	withoutSelectedCluster(t)
	setMockClient(t, &executionsPageMock{pages: [][]client.ExecutionSummary{referenceExecutions(referenceFixtureOtherID)}})

	_, resolveError := resolveExecutionReference(&cobra.Command{}, "40aa7c1d")

	if got := exitCodeFor(resolveError); got != exitNotFound {
		t.Errorf("exit code = %d (%v), want %d", got, resolveError, exitNotFound)
	}
}

// A walk that stopped at its cap read only part of the history, and the id may
// be in the rest: that is not a verified absence, so it must not exit 3.
func TestShortExecutionIDNotFoundInACappedWalkIsNotReportedAsAbsent(t *testing.T) {
	withoutSelectedCluster(t)
	pages := make([][]client.ExecutionSummary, 0, maxExecutionsPagesWalked+1)
	for range maxExecutionsPagesWalked + 1 {
		pages = append(pages, referenceExecutions(referenceFixtureOtherID))
	}
	setMockClient(t, &executionsPageMock{pages: pages})

	_, resolveError := resolveExecutionReference(&cobra.Command{}, "40aa7c1d")

	if resolveError == nil {
		t.Fatal("expected an error")
	}
	if got := exitCodeFor(resolveError); got == exitNotFound {
		t.Errorf("a capped walk claimed the execution does not exist: %v", resolveError)
	}
	if !strings.Contains(resolveError.Error(), "most recent") {
		t.Errorf("error %q does not say how far it looked", resolveError)
	}
}

func TestExecutionReferenceThatCannotBeAnIDIsAUsageError(t *testing.T) {
	withoutSelectedCluster(t)
	mock := &executionsPageMock{}
	setMockClient(t, mock)

	for _, reference := range []string{"abc12", "not-an-id", "zzzzzzzz", ""} {
		_, resolveError := resolveExecutionReference(&cobra.Command{}, reference)
		if got := exitCodeFor(resolveError); got != exitUsage {
			t.Errorf("%q: exit code = %d (%v), want %d", reference, got, resolveError, exitUsage)
		}
	}
	if len(mock.requestedOptions) != 0 {
		t.Errorf("a malformed id spent %d listing calls", len(mock.requestedOptions))
	}
}

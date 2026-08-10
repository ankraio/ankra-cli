package cmd

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// resetCommandFlags restores a command's flags to their defaults so tests do
// not inherit flag values from earlier executions of the shared cobra tree.
func resetCommandFlags(t *testing.T, command *cobra.Command) {
	t.Helper()
	command.Flags().VisitAll(func(flag *pflag.Flag) {
		_ = flag.Value.Set(flag.DefValue)
		flag.Changed = false
	})
}

type chartsValuesMock struct {
	baseMock
	searchResults       []client.ChartItem
	valuesResult        *client.GetChartDefaultValuesResult
	valuesError         error
	requestedRepository string
	requestedChart      string
	requestedVersion    string
}

func (m *chartsValuesMock) SearchCharts(query string) ([]client.ChartItem, error) {
	return m.searchResults, nil
}

func (m *chartsValuesMock) GetChartDefaultValues(repositoryName, chartName, chartVersion string) (*client.GetChartDefaultValuesResult, error) {
	m.requestedRepository = repositoryName
	m.requestedChart = chartName
	m.requestedVersion = chartVersion
	if m.valuesError != nil {
		return nil, m.valuesError
	}
	return m.valuesResult, nil
}

func TestChartsValuesPrintsDecodedDefaultValues(t *testing.T) {
	resetCommandFlags(t, chartsValuesCmd)
	defaultValuesYAML := "replicaCount: 1\nimage: nginx\n"
	mock := &chartsValuesMock{
		valuesResult: &client.GetChartDefaultValuesResult{
			ValuesBase64: base64.StdEncoding.EncodeToString([]byte(defaultValuesYAML)),
		},
	}
	setMockClient(t, mock)

	output, err := executeCommand("charts", "values", "nginx", "--version", "1.2.3", "--repository", "repo-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, defaultValuesYAML) {
		t.Errorf("expected decoded values in output, got: %s", output)
	}
	if mock.requestedRepository != "repo-a" || mock.requestedChart != "nginx" || mock.requestedVersion != "1.2.3" {
		t.Errorf("unexpected request: %s/%s@%s",
			mock.requestedRepository, mock.requestedChart, mock.requestedVersion)
	}
}

func TestChartsValuesRawOutputPrintsBase64(t *testing.T) {
	resetCommandFlags(t, chartsValuesCmd)
	encoded := base64.StdEncoding.EncodeToString([]byte("replicaCount: 1\n"))
	mock := &chartsValuesMock{
		valuesResult: &client.GetChartDefaultValuesResult{ValuesBase64: encoded},
	}
	setMockClient(t, mock)

	output, err := executeCommand("charts", "values", "nginx", "--version", "1.2.3", "--repository", "repo-a", "-o", "raw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, encoded) {
		t.Errorf("expected base64 form in output, got: %s", output)
	}
}

func TestChartsValuesResolvesRepositoryViaSearch(t *testing.T) {
	resetCommandFlags(t, chartsValuesCmd)
	mock := &chartsValuesMock{
		searchResults: []client.ChartItem{
			{Name: "nginx", Version: "1.2.3", RepositoryName: "repo-a"},
			{Name: "nginx-extras", Version: "0.1.0", RepositoryName: "repo-b"},
		},
		valuesResult: &client.GetChartDefaultValuesResult{
			ValuesBase64: base64.StdEncoding.EncodeToString([]byte("a: 1\n")),
		},
	}
	setMockClient(t, mock)

	if _, err := executeCommand("charts", "values", "nginx", "--version", "1.2.3"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.requestedRepository != "repo-a" {
		t.Errorf("repository = %q, want repo-a", mock.requestedRepository)
	}
}

func TestChartsValuesAmbiguousRepositoryExitsUsage(t *testing.T) {
	resetCommandFlags(t, chartsValuesCmd)
	mock := &chartsValuesMock{
		searchResults: []client.ChartItem{
			{Name: "nginx", Version: "1.2.3", RepositoryName: "repo-a"},
			{Name: "nginx", Version: "1.2.3", RepositoryName: "repo-b"},
		},
	}
	setMockClient(t, mock)

	_, err := executeCommand("charts", "values", "nginx", "--version", "1.2.3")
	if err == nil || !strings.Contains(err.Error(), "several repositories") {
		t.Fatalf("expected ambiguity error, got: %v", err)
	}
	if exitCodeFor(err) != exitUsage {
		t.Errorf("exit code = %d, want %d", exitCodeFor(err), exitUsage)
	}
}

func TestChartsValuesUnknownChartExitsNotFound(t *testing.T) {
	resetCommandFlags(t, chartsValuesCmd)
	mock := &chartsValuesMock{
		valuesError: fmt.Errorf("chart %q version %q in repository %q: %w",
			"nginx", "9.9.9", "repo-a", client.ErrChartNotFound),
	}
	setMockClient(t, mock)

	_, err := executeCommand("charts", "values", "nginx", "--version", "9.9.9", "--repository", "repo-a")
	if err == nil || !errors.Is(err, client.ErrChartNotFound) {
		t.Fatalf("expected a not-found error, got: %v", err)
	}
	if exitCodeFor(err) != exitNotFound {
		t.Errorf("exit code = %d, want %d", exitCodeFor(err), exitNotFound)
	}
}

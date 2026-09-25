package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
)

const costNamespacesClusterID = "1834920e-3001-4157-8938-33c447031033"

type costNamespacesMock struct {
	baseMock
	history     *client.NamespaceCostHistory
	readError   error
	clusterIDs  []string
	days        []int
	granularity []string
}

func (m *costNamespacesMock) GetNamespaceCostHistory(clusterID string, days int, granularity string) (*client.NamespaceCostHistory, error) {
	m.clusterIDs = append(m.clusterIDs, clusterID)
	m.days = append(m.days, days)
	m.granularity = append(m.granularity, granularity)
	if m.readError != nil {
		return nil, m.readError
	}
	return m.history, nil
}

func runCostNamespacesCommand(t *testing.T, mock APIClient, args ...string) (string, error) {
	t.Helper()
	withTempHome(t)
	setMockClient(t, mock)
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs(args)
	t.Cleanup(func() { resetTreeFlags(t, costNamespacesCmd) })
	executeError := rootCmd.Execute()
	return stdout.String(), executeError
}

func namespaceCents(cents int64) *int64 { return &cents }

// threeDayHistory is three day buckets: an unmetered day, a metered day
// shop and batch shared, and today's partial day only shop was in.
func threeDayHistory() *client.NamespaceCostHistory {
	start := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	return &client.NamespaceCostHistory{
		ClusterID: costNamespacesClusterID, Currency: "usd", Granularity: "day", Days: 3,
		From: start, To: start.Add(60 * time.Hour),
		Buckets: []client.NamespaceCostBucket{
			{Start: start, Hours: 24, AttributedHours: 0},
			{Start: start.Add(24 * time.Hour), Hours: 24, AttributedHours: 24},
			{Start: start.Add(48 * time.Hour), Hours: 12, AttributedHours: 12},
		},
		Namespaces: []client.NamespaceCostSeries{
			{Namespace: "shop", CostCents: []*int64{nil, namespaceCents(21000), namespaceCents(20000)}, TotalCents: 41000},
			{Namespace: "batch", CostCents: []*int64{nil, namespaceCents(5000), namespaceCents(0)}, TotalCents: 5000},
		},
	}
}

// TestCostNamespacesTellsUnknownFromZero pins ankra-cozgu.3.3.4's reading:
// the header counts the metered days, an unmetered day is drawn '·' and
// never zero, a metered day a namespace had nothing in is '_', and TOTAL,
// LATEST and PEAK come from the metered buckets only.
func TestCostNamespacesTellsUnknownFromZero(t *testing.T) {
	output, runError := runCostNamespacesCommand(t, &costNamespacesMock{history: threeDayHistory()},
		"cost", "namespaces", costNamespacesClusterID)
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	for _, want := range []string{
		"over the last 3 days by the day, in USD",
		"2 of 3 days were metered; the other 1 are unknown (·), not zero.",
		"NAMESPACE", "TREND",
		"shop", "$410.00", "$200.00", "$210.00", "·██",
		"batch", "$50.00", "$0.00", "·█_",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
}

// TestCostNamespacesPassesTheWindowThrough pins that the flags reach the
// route as given and omitted flags leave the platform's defaults.
func TestCostNamespacesPassesTheWindowThrough(t *testing.T) {
	mock := &costNamespacesMock{history: threeDayHistory()}
	if _, runError := runCostNamespacesCommand(t, mock, "cost", "namespaces", costNamespacesClusterID,
		"--days", "2", "--granularity", "hour"); runError != nil {
		t.Fatalf("run: %v", runError)
	}
	resetTreeFlags(t, costNamespacesCmd)
	if _, runError := runCostNamespacesCommand(t, mock, "cost", "namespaces", costNamespacesClusterID); runError != nil {
		t.Fatalf("run: %v", runError)
	}
	if mock.clusterIDs[0] != costNamespacesClusterID || mock.days[0] != 2 || mock.granularity[0] != "hour" ||
		mock.days[1] != 0 || mock.granularity[1] != "" {
		t.Fatalf("asked for %v %v %v", mock.clusterIDs, mock.days, mock.granularity)
	}
	resetTreeFlags(t, costNamespacesCmd)
	if _, runError := runCostNamespacesCommand(t, mock, "cost", "namespaces", costNamespacesClusterID, "--days", "0"); runError == nil ||
		exitCodeFor(runError) != exitUsage {
		t.Fatalf("--days 0 = %v, want a usage error", runError)
	}
}

// TestCostNamespacesWithNothingMeteredSaysUnknown pins that a window the
// metering never reached reads as unknown, with no table of zeros.
func TestCostNamespacesWithNothingMeteredSaysUnknown(t *testing.T) {
	history := threeDayHistory()
	for index := range history.Buckets {
		history.Buckets[index].AttributedHours = 0
	}
	history.Namespaces = []client.NamespaceCostSeries{}
	output, runError := runCostNamespacesCommand(t, &costNamespacesMock{history: history}, "cost", "namespaces", costNamespacesClusterID)
	if runError != nil || !strings.Contains(output, "No day of this window was metered, so every namespace's cost is unknown, not zero.") ||
		strings.Contains(output, "NAMESPACE") {
		t.Fatalf("output = %q (%v)", output, runError)
	}
}

// TestCostNamespacesDropsTheTrendPastFortyEightBuckets pins the wide hourly
// window: no trend column, and a note that -o json carries every bucket;
// a namespace past the 25 listed is counted, never dropped silently.
func TestCostNamespacesDropsTheTrendPastFortyEightBuckets(t *testing.T) {
	start := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	history := &client.NamespaceCostHistory{ClusterID: costNamespacesClusterID, Currency: "eur", Granularity: "hour", Days: 3}
	for hour := 0; hour < 72; hour++ {
		history.Buckets = append(history.Buckets, client.NamespaceCostBucket{Start: start.Add(time.Duration(hour) * time.Hour),
			Hours: 1, AttributedHours: 1})
	}
	for index := 0; index < 27; index++ {
		values := make([]*int64, 72)
		for hour := range values {
			values[hour] = namespaceCents(int64(100 + index))
		}
		history.Namespaces = append(history.Namespaces, client.NamespaceCostSeries{
			Namespace: fmt.Sprintf("ns-%02d", index), CostCents: values, TotalCents: int64(72 * (100 + index))})
	}
	output, runError := runCostNamespacesCommand(t, &costNamespacesMock{history: history}, "cost", "namespaces", costNamespacesClusterID)
	if runError != nil || strings.Contains(output, "TREND") || !strings.Contains(output, "at most 48 buckets; -o json has all 72") ||
		!strings.Contains(output, "2 namespaces more not listed") || !strings.Contains(output, "All 72 hours were metered.") {
		t.Fatalf("output = %s (%v)", output, runError)
	}
}

// TestCostNamespacesKeepsThePlatformsWords pins the errors: a refused window
// is a usage error in the platform's sentence, the route's own not-found is
// a missing cluster, and a platform without the route (a bare 404, or one
// whose detail is not the route's) is named as predating it.
func TestCostNamespacesKeepsThePlatformsWords(t *testing.T) {
	refused := &client.UnexpectedResponseError{StatusCode: 400,
		Detail: "days must be between 1 and 35, and at most 7 when granularity is hour"}
	_, refusedError := runCostNamespacesCommand(t, &costNamespacesMock{readError: refused}, "cost", "namespaces", costNamespacesClusterID)
	if refusedError == nil || refusedError.Error() != refused.Detail || exitCodeFor(refusedError) != exitUsage {
		t.Fatalf("a refused window = %v (exit %d)", refusedError, exitCodeFor(refusedError))
	}
	missing := &client.UnexpectedResponseError{StatusCode: 404, Detail: "Cluster not found"}
	_, missingError := runCostNamespacesCommand(t, &costNamespacesMock{readError: missing}, "cost", "namespaces", costNamespacesClusterID)
	if missingError == nil || !strings.Contains(missingError.Error(), "the cluster was not found") || exitCodeFor(missingError) != exitNotFound {
		t.Fatalf("a missing cluster = %v (exit %d)", missingError, exitCodeFor(missingError))
	}
	for _, oldPlatform := range []error{
		client.NewUnexpectedResponseError(404, "unexpected status: 404 Not Found"),
		&client.UnexpectedResponseError{StatusCode: 404, Detail: "Not Found."},
	} {
		_, routeError := runCostNamespacesCommand(t, &costNamespacesMock{readError: oldPlatform}, "cost", "namespaces", costNamespacesClusterID)
		if routeError == nil || !strings.Contains(routeError.Error(), "this platform does not serve the namespace cost history") {
			t.Fatalf("a platform without the route = %v", routeError)
		}
	}
}

// TestCostNamespacesStructuredOutputIsTheApiDocument pins -o json: every
// namespace and bucket, with an unmetered value as null, not 0.
func TestCostNamespacesStructuredOutputIsTheApiDocument(t *testing.T) {
	output, runError := runCostNamespacesCommand(t, &costNamespacesMock{history: threeDayHistory()},
		"cost", "namespaces", costNamespacesClusterID, "-o", "json")
	if runError != nil {
		t.Fatalf("run: %v", runError)
	}
	var decoded struct {
		Buckets    []map[string]any `json:"buckets"`
		Namespaces []struct {
			Namespace string `json:"namespace"`
			CostCents []any  `json:"cost_cents"`
		} `json:"namespaces"`
	}
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("decoding %s: %v", output, decodeError)
	}
	if len(decoded.Buckets) != 3 || len(decoded.Namespaces) != 2 || decoded.Namespaces[1].CostCents[0] != nil ||
		decoded.Namespaces[1].CostCents[2] != float64(0) {
		t.Fatalf("structured = %s", output)
	}
}

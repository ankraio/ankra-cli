package cmd

import (
	"cmp"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

func newSortTestCommand() (*cobra.Command, []sortField[int]) {
	fields := []sortField[int]{
		{"value", func(a, b int) int { return cmp.Compare(a, b) }},
		{"other", func(a, b int) int { return 0 }},
	}
	command := &cobra.Command{Use: "sorttest"}
	registerSortFlags(command, fields)
	return command, fields
}

func TestResolveSort(t *testing.T) {
	tests := []struct {
		name     string
		sort     string
		order    string
		want     []int
		wantCode int
	}{
		{name: "no flags keeps server order", want: []int{3, 1, 2}},
		{name: "ascending", sort: "value", want: []int{1, 2, 3}},
		{name: "descending", sort: "value", order: "desc", want: []int{3, 2, 1}},
		{name: "key is case-insensitive", sort: "VALUE", want: []int{1, 2, 3}},
		{name: "unknown key is a usage error", sort: "bogus", wantCode: exitUsage},
		{name: "unknown order is a usage error", sort: "value", order: "sideways", wantCode: exitUsage},
		{name: "order without sort is a usage error", order: "desc", wantCode: exitUsage},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			command, fields := newSortTestCommand()
			if testCase.sort != "" {
				if err := command.Flags().Set("sort", testCase.sort); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.order != "" {
				if err := command.Flags().Set("order", testCase.order); err != nil {
					t.Fatal(err)
				}
			}
			items := []int{3, 1, 2}
			sortItems, err := resolveSort(command, fields)
			if testCase.wantCode != 0 {
				if err == nil {
					t.Fatal("expected an error")
				}
				if code := exitCodeFor(err); code != testCase.wantCode {
					t.Fatalf("expected exit code %d, got %d (%v)", testCase.wantCode, code, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			sortItems(items)
			for i := range testCase.want {
				if items[i] != testCase.want[i] {
					t.Fatalf("expected %v, got %v", testCase.want, items)
				}
			}
		})
	}
}

func TestSortComparators(t *testing.T) {
	if compareTimeStrings("2024-01-01T00:00:00Z", "2024-02-01T00:00:00Z") >= 0 {
		t.Error("expected January to sort before February")
	}
	// Chronological, not lexicographic: the offset timestamp is the later
	// instant even though it compares smaller as a string.
	if compareTimeStrings("2024-01-02T00:00:00Z", "2024-01-01T23:30:00-02:00") >= 0 {
		t.Error("expected instants to be compared in UTC, not as strings")
	}
	if compareTimeStrings("", "2024-01-01T00:00:00Z") >= 0 {
		t.Error("expected unparseable values to sort before timestamps")
	}
	if compareTimeStrings("abc", "abd") >= 0 {
		t.Error("expected unparseable values to compare lexicographically")
	}
	if compareBools(false, true) >= 0 {
		t.Error("expected false to sort before true")
	}
	earlier := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Hour)
	if compareTimePtrs(nil, &earlier) >= 0 || compareTimePtrs(&earlier, &later) >= 0 {
		t.Error("expected nil first, then chronological order")
	}
	three, four := 3, 4
	if compareIntPtrs(nil, &three) >= 0 || compareIntPtrs(&three, &four) >= 0 {
		t.Error("expected nil first, then numeric order")
	}
	name := "Zed"
	if compareFoldPtr(nil, &name) >= 0 {
		t.Error("expected nil to sort like the empty string")
	}
	if compareFold("apple", "Banana") >= 0 {
		t.Error("expected case-insensitive ordering")
	}
}

// resetSortFlags restores a shared command's sort flags after a test.
// Resetting Changed matters: applySort treats an explicit --order without
// --sort as a usage error, so a leaked Changed bit would fail later tests.
func resetSortFlags(t *testing.T, command *cobra.Command, extra ...string) {
	t.Helper()
	t.Cleanup(func() {
		flags := command.Flags()
		for _, flagName := range append([]string{"sort", "order"}, extra...) {
			flag := flags.Lookup(flagName)
			if flag == nil {
				continue
			}
			_ = flags.Set(flagName, flag.DefValue)
			flag.Changed = false
		}
	})
}

func sortTestClusters() []client.ClusterListItem {
	// Server order (bravo, alpha, charlie) differs from both the name order
	// and the created order so a passing test proves a re-sort happened.
	return []client.ClusterListItem{
		{ID: "id-2", Name: "bravo-cluster", State: "online", Kind: "imported", CreatedAt: "2024-03-01T00:00:00Z"},
		{ID: "id-1", Name: "alpha-cluster", State: "online", Kind: "imported", CreatedAt: "2024-01-01T00:00:00Z"},
		{ID: "id-3", Name: "charlie-cluster", State: "offline", Kind: "imported", CreatedAt: "2024-02-01T00:00:00Z"},
	}
}

func assertOrder(t *testing.T, output string, names ...string) {
	t.Helper()
	previous := -1
	for _, name := range names {
		index := strings.Index(output, name)
		if index < 0 {
			t.Fatalf("expected output to contain %q, got: %s", name, output)
		}
		if index < previous {
			t.Fatalf("expected order %v, got: %s", names, output)
		}
		previous = index
	}
}

func TestClusterListSortByName(t *testing.T) {
	setMockClient(t, &clusterListMock{clusters: sortTestClusters()})
	resetSortFlags(t, clusterListCmd)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "list", "--sort", "name")
	})
	assertOrder(t, stdoutOutput, "alpha-cluster", "bravo-cluster", "charlie-cluster")
}

func TestClusterListSortByCreatedDesc(t *testing.T) {
	setMockClient(t, &clusterListMock{clusters: sortTestClusters()})
	resetSortFlags(t, clusterListCmd)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "list", "--sort", "created", "--order", "desc")
	})
	assertOrder(t, stdoutOutput, "bravo-cluster", "charlie-cluster", "alpha-cluster")
}

func TestClusterListSortAppliesToStructuredOutput(t *testing.T) {
	setMockClient(t, &clusterListMock{clusters: sortTestClusters()})
	resetSortFlags(t, clusterListCmd, "output")

	output, err := executeCommand("cluster", "list", "--sort", "name", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var clusters []client.ClusterListItem
	if err := json.Unmarshal([]byte(output), &clusters); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", output, err)
	}
	if len(clusters) != 3 || clusters[0].Name != "alpha-cluster" ||
		clusters[1].Name != "bravo-cluster" || clusters[2].Name != "charlie-cluster" {
		t.Errorf("expected JSON sorted by name, got: %+v", clusters)
	}
}

func TestClusterListInvalidSortIsUsageError(t *testing.T) {
	setMockClient(t, &clusterListMock{clusters: sortTestClusters()})
	resetSortFlags(t, clusterListCmd)

	var err error
	captureStdout(t, func() {
		_, err = executeCommand("cluster", "list", "--sort", "bogus")
	})
	if err == nil {
		t.Fatal("expected an error for an unknown --sort value")
	}
	if code := exitCodeFor(err); code != exitUsage {
		t.Errorf("expected exit code %d, got %d (%v)", exitUsage, code, err)
	}
	if !strings.Contains(err.Error(), "created") {
		t.Errorf("expected the error to list valid keys, got: %v", err)
	}
}

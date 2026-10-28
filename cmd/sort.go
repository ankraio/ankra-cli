package cmd

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// sortField describes one sortable column of a list command: the key the
// user passes to --sort, and a comparator over the raw, unformatted items —
// so "created" orders by timestamp, not by the rendered "2 days ago" text.
type sortField[T any] struct {
	key     string
	compare func(a, b T) int
}

func sortFieldKeys[T any](fields []sortField[T]) []string {
	keys := make([]string, len(fields))
	for i, field := range fields {
		keys[i] = field.key
	}
	return keys
}

// registerSortFlags adds the shared --sort/--order flags to a list command.
func registerSortFlags[T any](cmd *cobra.Command, fields []sortField[T]) {
	cmd.Flags().String("sort", "", "Sort by column: "+strings.Join(sortFieldKeys(fields), ", ")+" (default: server order)")
	cmd.Flags().String("order", "asc", "Sort direction with --sort: asc or desc")
}

// resolveSort validates --sort/--order and returns the sort to run on the
// fetched items. Call it before any API work so a bad flag fails fast with
// the usage code instead of after a wasted request. The returned function
// stably sorts in place — before any rendering, so tables and -o json|yaml
// agree on the order — and is a no-op without --sort, preserving the server
// order.
func resolveSort[T any](cmd *cobra.Command, fields []sortField[T]) (func(items []T), error) {
	noop := func([]T) {}
	key, _ := cmd.Flags().GetString("sort")
	key = strings.ToLower(strings.TrimSpace(key))
	order, _ := cmd.Flags().GetString("order")
	order = strings.ToLower(strings.TrimSpace(order))
	if order != "asc" && order != "desc" {
		return noop, withExitCode(exitUsage, fmt.Errorf("invalid --order %q (valid: asc, desc)", order))
	}
	if key == "" {
		if cmd.Flags().Changed("order") {
			return noop, withExitCode(exitUsage, fmt.Errorf("--order requires --sort"))
		}
		return noop, nil
	}
	for _, field := range fields {
		if field.key != key {
			continue
		}
		compare := field.compare
		if order == "desc" {
			asc := compare
			compare = func(a, b T) int { return -asc(a, b) }
		}
		return func(items []T) { slices.SortStableFunc(items, compare) }, nil
	}
	return noop, withExitCode(exitUsage,
		fmt.Errorf("invalid --sort %q (valid: %s)", key, strings.Join(sortFieldKeys(fields), ", ")))
}

// compareFold orders strings case-insensitively, breaking ties with the
// case-sensitive comparison so the order stays deterministic.
func compareFold(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// compareFoldPtr is compareFold over nullable strings; nil sorts like "".
func compareFoldPtr(a, b *string) int {
	return compareFold(derefString(a), derefString(b))
}

// compareTimeStrings orders the API's RFC3339 timestamp strings
// chronologically. Values that do not parse (including "") sort before every
// parseable timestamp and lexicographically among themselves, mirroring
// formatTimeAgo's tolerance of malformed values.
func compareTimeStrings(a, b string) int {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	switch {
	case errA == nil && errB == nil:
		return ta.Compare(tb)
	case errA == nil:
		return 1
	case errB == nil:
		return -1
	default:
		return strings.Compare(a, b)
	}
}

// compareTimeStringPtrs is compareTimeStrings over nullable timestamps
// (e.g. last-used "Never"); nil sorts like "", before every real timestamp.
func compareTimeStringPtrs(a, b *string) int {
	return compareTimeStrings(derefString(a), derefString(b))
}

// compareTimePtrs orders nullable time.Time values; nil sorts first.
func compareTimePtrs(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	default:
		return a.Compare(*b)
	}
}

// compareBools orders false before true.
func compareBools(a, b bool) int {
	switch {
	case a == b:
		return 0
	case b:
		return -1
	default:
		return 1
	}
}

// compareIntPtrs orders nullable ints; nil sorts first.
func compareIntPtrs(a, b *int) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	default:
		return cmp.Compare(*a, *b)
	}
}

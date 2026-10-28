package cmd

import (
	"fmt"
	"strings"
)

// unifiedDiff renders a unified diff (three lines of context) between two
// texts, the way `diff -u` prints it, so the CLI can show what changed inside
// a profile's manifests without shelling out. Line-based, longest common
// subsequence; the inputs are manifests and values files, never large.
func unifiedDiff(fromLabel string, toLabel string, fromText string, toText string) string {
	fromLines := splitDiffLines(fromText)
	toLines := splitDiffLines(toText)
	operations := diffOperations(fromLines, toLines)
	fromEndsWithNewline := fromText == "" || strings.HasSuffix(fromText, "\n")
	toEndsWithNewline := toText == "" || strings.HasSuffix(toText, "\n")
	if fromEndsWithNewline != toEndsWithNewline && fromText != "" && toText != "" {
		operations = markTrailingNewlineChange(operations)
	}
	hunks := groupDiffHunks(operations, 3)
	if len(hunks) == 0 {
		return ""
	}
	var builder strings.Builder
	_, _ = fmt.Fprintf(&builder, "--- %s\n+++ %s\n", fromLabel, toLabel)
	lastFrom, lastTo := len(fromLines)-1, len(toLines)-1
	fromIndex, toIndex := 0, 0
	for _, hunk := range hunks {
		fromStart, fromCount, toStart, toCount := hunkRanges(operations, hunk)
		_, _ = fmt.Fprintf(&builder, "@@ -%s +%s @@\n", formatHunkRange(fromStart, fromCount), formatHunkRange(toStart, toCount))
		fromIndex, toIndex = fromStart-1, toStart-1
		for _, operation := range operations[hunk.start:hunk.end] {
			builder.WriteString(operation.kind)
			builder.WriteString(operation.text)
			builder.WriteString("\n")
			switch operation.kind {
			case " ":
				fromIndex++
				toIndex++
			case "-":
				fromIndex++
			case "+":
				toIndex++
			}
			// `diff -u` flags a last line that has no newline, so a change that
			// is only the trailing newline is visible instead of vanishing.
			atFromEnd := operation.kind != "+" && !fromEndsWithNewline && fromIndex-1 == lastFrom
			atToEnd := operation.kind != "-" && !toEndsWithNewline && toIndex-1 == lastTo
			if atFromEnd || atToEnd {
				builder.WriteString(noNewlineMarker)
			}
		}
	}
	return builder.String()
}

const noNewlineMarker = "\\ No newline at end of file\n"

// markTrailingNewlineChange turns the final unchanged line into a removal
// plus an addition, which is how a newline-only difference is shown.
func markTrailingNewlineChange(operations []diffOperation) []diffOperation {
	last := len(operations) - 1
	if last < 0 || operations[last].kind != " " {
		return operations
	}
	text := operations[last].text
	return append(append(operations[:last:last], diffOperation{"-", text}), diffOperation{"+", text})
}

type diffOperation struct {
	kind string // " ", "-" or "+"
	text string
}

type diffHunk struct {
	start int
	end   int
}

func splitDiffLines(text string) []string {
	if text == "" {
		return []string{}
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// diffOperations walks the longest-common-subsequence table backwards and
// emits the edit script in forward order.
func diffOperations(fromLines []string, toLines []string) []diffOperation {
	fromCount, toCount := len(fromLines), len(toLines)
	table := make([][]int, fromCount+1)
	for index := range table {
		table[index] = make([]int, toCount+1)
	}
	for fromIndex := fromCount - 1; fromIndex >= 0; fromIndex-- {
		for toIndex := toCount - 1; toIndex >= 0; toIndex-- {
			if fromLines[fromIndex] == toLines[toIndex] {
				table[fromIndex][toIndex] = table[fromIndex+1][toIndex+1] + 1
			} else if table[fromIndex+1][toIndex] >= table[fromIndex][toIndex+1] {
				table[fromIndex][toIndex] = table[fromIndex+1][toIndex]
			} else {
				table[fromIndex][toIndex] = table[fromIndex][toIndex+1]
			}
		}
	}
	operations := []diffOperation{}
	fromIndex, toIndex := 0, 0
	for fromIndex < fromCount && toIndex < toCount {
		switch {
		case fromLines[fromIndex] == toLines[toIndex]:
			operations = append(operations, diffOperation{" ", fromLines[fromIndex]})
			fromIndex++
			toIndex++
		case table[fromIndex+1][toIndex] >= table[fromIndex][toIndex+1]:
			operations = append(operations, diffOperation{"-", fromLines[fromIndex]})
			fromIndex++
		default:
			operations = append(operations, diffOperation{"+", toLines[toIndex]})
			toIndex++
		}
	}
	for ; fromIndex < fromCount; fromIndex++ {
		operations = append(operations, diffOperation{"-", fromLines[fromIndex]})
	}
	for ; toIndex < toCount; toIndex++ {
		operations = append(operations, diffOperation{"+", toLines[toIndex]})
	}
	return operations
}

// groupDiffHunks clusters the changed operations into hunks that carry
// `context` unchanged lines on either side, merging hunks whose context
// would overlap.
func groupDiffHunks(operations []diffOperation, context int) []diffHunk {
	hunks := []diffHunk{}
	for index := 0; index < len(operations); index++ {
		if operations[index].kind == " " {
			continue
		}
		start := index - context
		if start < 0 {
			start = 0
		}
		end := index + 1
		for end < len(operations) {
			next := end
			for next < len(operations) && operations[next].kind == " " {
				next++
			}
			if next >= len(operations) || next-end > 2*context {
				break
			}
			end = next + 1
		}
		end += context
		if end > len(operations) {
			end = len(operations)
		}
		hunks = append(hunks, diffHunk{start: start, end: end})
		index = end - 1
	}
	return hunks
}

func hunkRanges(operations []diffOperation, hunk diffHunk) (int, int, int, int) {
	fromLine, toLine := 1, 1
	for _, operation := range operations[:hunk.start] {
		switch operation.kind {
		case " ":
			fromLine++
			toLine++
		case "-":
			fromLine++
		case "+":
			toLine++
		}
	}
	fromCount, toCount := 0, 0
	for _, operation := range operations[hunk.start:hunk.end] {
		switch operation.kind {
		case " ":
			fromCount++
			toCount++
		case "-":
			fromCount++
		case "+":
			toCount++
		}
	}
	return fromLine, fromCount, toLine, toCount
}

func formatHunkRange(start int, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start-1)
	}
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

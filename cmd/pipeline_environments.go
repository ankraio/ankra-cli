package cmd

// Test environments (ankra-aqb78l.2): the legs of a pipeline stage of
// `kind: environment`. For each cell of the stage's matrix (for example
// Kubernetes 1.35/1.36/1.37 x PostgreSQL 16/17) the platform builds an
// ephemeral cluster, deploys the build under test, runs the tests, records the
// result and tears the cluster down. `ankra pipeline environments <run>` shows
// them as a grid, and `pipeline get` and `pipeline tests` print the same grid
// under a "Test environments" heading when a run has any.

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"ankra/internal/client"
	"ankra/internal/hiddenunicode"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

const (
	// pipelineTestEnvironmentLogLines is how many of a failed leg's last log
	// lines are printed unless --logs asks for the whole tail.
	pipelineTestEnvironmentLogLines = 10
	// pipelineTestEnvironmentNamedLegs bounds how many legs the summary line
	// names per outcome before it says how many more there are.
	pipelineTestEnvironmentNamedLegs = 3
)

// pipelineTestEnvironmentsOptions is how much of a failed leg's log to show,
// and the command that shows the rest.
type pipelineTestEnvironmentsOptions struct {
	isFullLog   bool
	fullLogHint string
}

func newPipelineEnvironmentsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "environments <run>",
		Aliases: []string{"envs"},
		Short:   "Show the ephemeral test environments of a pipeline run",
		Long: `Show the test environments of a pipeline run: the legs of its stages of
kind: environment.

For each cell of such a stage's matrix (for example Kubernetes 1.35, 1.36 and
1.37 against PostgreSQL 16 and 17) Ankra builds an ephemeral cluster, deploys
the build under test, runs the tests, records the result and tears the
cluster down. A two-axis matrix prints as a grid, rows the first axis and
columns the second, followed by a one-line summary and, for each failed leg,
its message and the last lines of its test log (--logs prints the whole
tail). Any other matrix prints as a table.

A leg the platform has not started yet, held back by the organisation's
concurrency cap, shows as queued.

-o json/yaml prints the platform's answer as it was sent.`,
		Example: `  # The grid of a run's test environments
  ankra pipeline environments <run-id> --application my-app

  # With every failed leg's whole test log tail
  ankra pipeline envs <run-id> --repository <repository-id> --logs`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineEnvironments(command, selector, arguments[0])
		},
	}
	command.Flags().Bool("logs", false, "Print each failed leg's whole test log tail, not only its last lines")
	registerPipelineSelectorFlags(command)
	registerStructuredOutputFlags(command)
	return command
}

func runPipelineEnvironments(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	runID = strings.TrimSpace(runID)
	isFullLog, _ := command.Flags().GetBool("logs")
	environments, readError := apiClient.GetPipelineRunTestEnvironments(command.Context(), selector, runID)
	if readError != nil {
		if errors.Is(readError, client.ErrPipelineTestEnvironmentsUnavailable) {
			return withExitCode(exitNotFound, fmt.Errorf(
				"no test environments found for run %s: the run does not exist, or the platform does not report test environments yet",
				runID))
		}
		return readError
	}
	output := command.OutOrStdout()
	if format != outputDefault {
		return encodeStructured(output, format, environments)
	}
	if len(environments.Environments) == 0 {
		_, _ = fmt.Fprintln(output, "This run has no test environments.")
		return nil
	}
	printPipelineTestEnvironments(output, *environments, pipelineTestEnvironmentsOptions{isFullLog: isFullLog})
	return nil
}

// readPipelineRunTestEnvironments reads a run's test environments for a
// command that already read the run. It never fails that command: a 404 is
// "nothing to show", and any other error is one line on stderr.
func readPipelineRunTestEnvironments(command *cobra.Command, selector client.PipelineSelector,
	runID string) *client.PipelineRunTestEnvironments {
	environments, readError := apiClient.GetPipelineRunTestEnvironments(command.Context(), selector, runID)
	if readError != nil {
		if !errors.Is(readError, client.ErrPipelineTestEnvironmentsUnavailable) {
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "Note: could not read the run's test environments: %v\n", readError)
		}
		return nil
	}
	return environments
}

// printPipelineRunTestEnvironmentsSection prints the run's test environments
// under their own heading, and nothing for a run that has none.
func printPipelineRunTestEnvironmentsSection(command *cobra.Command, selector client.PipelineSelector, runID string) {
	environments := readPipelineRunTestEnvironments(command, selector, runID)
	if environments == nil || len(environments.Environments) == 0 {
		return
	}
	output := command.OutOrStdout()
	_, _ = fmt.Fprintln(output, "\nTest environments")
	printPipelineTestEnvironments(output, *environments, pipelineTestEnvironmentsOptions{
		fullLogHint: "ankra pipeline environments " + runID + pipelineSelectorArguments(selector) + " --logs",
	})
}

// printPipelineTestEnvironments writes the grid (or, for a matrix that is not
// two-dimensional, the table), the summary line and the failed legs.
func printPipelineTestEnvironments(output io.Writer, environments client.PipelineRunTestEnvironments,
	options pipelineTestEnvironmentsOptions) {
	axes := pipelineTestEnvironmentAxes(environments)
	legs := latestPipelineTestEnvironmentLegs(environments.Environments, axes)
	if !writePipelineTestEnvironmentGrid(output, legs, axes) {
		writePipelineTestEnvironmentTable(output, legs, axes)
	}
	_, _ = fmt.Fprintln(output, pipelineTestEnvironmentsSummaryLine(legs, axes))
	writePipelineTestEnvironmentFailures(output, legs, axes, options)
}

// pipelineTestEnvironmentAxes is the axes the platform named, or, from one
// that named none, every axis the legs carry, sorted.
func pipelineTestEnvironmentAxes(environments client.PipelineRunTestEnvironments) []string {
	if len(environments.Axes) > 0 {
		return environments.Axes
	}
	seen := map[string]bool{}
	var axes []string
	for _, leg := range environments.Environments {
		for axis := range leg.Matrix {
			if !seen[axis] {
				seen[axis] = true
				axes = append(axes, axis)
			}
		}
	}
	sort.Strings(axes)
	return axes
}

// latestPipelineTestEnvironmentLegs keeps each matrix cell's newest attempt,
// in the order the platform listed the cells.
func latestPipelineTestEnvironmentLegs(legs []client.PipelineTestEnvironment,
	axes []string) []client.PipelineTestEnvironment {
	positions := map[string]int{}
	var latest []client.PipelineTestEnvironment
	for _, leg := range legs {
		key := leg.Stage + "\x00" + pipelineTestEnvironmentMatrix(leg, axes)
		position, isSeen := positions[key]
		switch {
		case !isSeen:
			positions[key] = len(latest)
			latest = append(latest, leg)
		case leg.Attempt > latest[position].Attempt:
			latest[position] = leg
		}
	}
	return latest
}

// writePipelineTestEnvironmentGrid writes a two-axis matrix as a grid and
// reports whether it could: a matrix of any other shape, or a leg missing
// one of the two axes, is left to the table.
func writePipelineTestEnvironmentGrid(output io.Writer, legs []client.PipelineTestEnvironment, axes []string) bool {
	if len(axes) != 2 {
		return false
	}
	rowAxis, columnAxis := axes[0], axes[1]
	stages := map[string]bool{}
	cells := map[[2]string]client.PipelineTestEnvironment{}
	var rowValues, columnValues []string
	for _, leg := range legs {
		rowValue, hasRow := leg.Matrix[rowAxis]
		columnValue, hasColumn := leg.Matrix[columnAxis]
		if !hasRow || !hasColumn {
			return false
		}
		stages[leg.Stage] = true
		if _, isTaken := cells[[2]string{rowValue, columnValue}]; isTaken {
			return false
		}
		cells[[2]string{rowValue, columnValue}] = leg
		rowValues = appendDistinct(rowValues, rowValue)
		columnValues = appendDistinct(columnValues, columnValue)
	}
	if len(stages) > 1 {
		return false
	}
	sort.SliceStable(rowValues, func(left, right int) bool {
		return compareMatrixValues(rowValues[left], rowValues[right]) < 0
	})
	sort.SliceStable(columnValues, func(left, right int) bool {
		return compareMatrixValues(columnValues[left], columnValues[right]) < 0
	})

	grid := table.NewWriter()
	grid.SetOutputMirror(output)
	grid.SetStyle(table.StyleRounded)
	grid.Style().Format.Header = text.FormatDefault
	header := table.Row{""}
	for _, columnValue := range columnValues {
		header = append(header, pipelineTestText(columnAxis+" "+columnValue))
	}
	grid.AppendHeader(header)
	for _, rowValue := range rowValues {
		row := table.Row{pipelineTestText(rowAxis + " " + rowValue)}
		for _, columnValue := range columnValues {
			leg, isPresent := cells[[2]string{rowValue, columnValue}]
			if !isPresent {
				row = append(row, "-")
				continue
			}
			row = append(row, pipelineTestEnvironmentCell(leg))
		}
		grid.AppendRow(row)
	}
	grid.Render()
	return true
}

func writePipelineTestEnvironmentTable(output io.Writer, legs []client.PipelineTestEnvironment, axes []string) {
	legTable := newPipelineTestsTable(output, table.Row{"STEP KEY", "MATRIX", "STATUS", "RESULT", "DURATION", "CLUSTER"})
	for _, leg := range legs {
		legTable.AppendRow(table.Row{
			pipelineTestText(leg.StepKey),
			pipelineTestText(pipelineTestEnvironmentMatrix(leg, axes)),
			pipelineTestText(pipelineTestEnvironmentStatus(leg.Status)),
			pipelineTestText(pipelineTestEnvironmentOutcome(leg)),
			pipelineTestText(pipelineTestEnvironmentDuration(leg.DurationSeconds)),
			pipelineTestText(pipelineStringValue(leg.ClusterID)),
		})
	}
	legTable.Render()
}

// pipelineTestEnvironmentNoResult stands for a leg whose cluster is gone but
// whose outcome was never recorded: it is over, not still in flight.
const pipelineTestEnvironmentNoResult = "no result"

// pipelineTestEnvironmentOutcome is the leg's result, "no result" for a
// leg torn down without one, and empty while it is still queued or in
// flight.
func pipelineTestEnvironmentOutcome(leg client.PipelineTestEnvironment) string {
	if result := pipelineStringValue(leg.Result); result != "" {
		return result
	}
	if isPipelineTestEnvironmentTornDown(leg) {
		return pipelineTestEnvironmentNoResult
	}
	return ""
}

// isPipelineTestEnvironmentTornDown is the one test of whether a leg's
// cluster is gone, shared by the cells, the counts and the teardown line so
// they cannot disagree: its status says destroyed, or a teardown time was
// recorded while the status still lags.
func isPipelineTestEnvironmentTornDown(leg client.PipelineTestEnvironment) bool {
	return leg.Status == "destroyed" || leg.DestroyedAt != nil
}

// pipelineTestEnvironmentCell is one grid cell: "passed 5m12s", "FAILED",
// "no result" for a leg torn down without one, or the phase of a leg that
// has no result yet ("queued", "testing").
func pipelineTestEnvironmentCell(leg client.PipelineTestEnvironment) string {
	switch pipelineTestEnvironmentOutcome(leg) {
	case "passed":
		if duration := pipelineTestEnvironmentDuration(leg.DurationSeconds); duration != "" {
			return "passed " + duration
		}
		return "passed"
	case "failed":
		return "FAILED"
	case "error":
		return "ERROR"
	case "timed_out":
		return "TIMED OUT"
	case "":
		return pipelineTestEnvironmentStatus(leg.Status)
	default:
		return strings.ReplaceAll(pipelineTestEnvironmentOutcome(leg), "_", " ")
	}
}

func pipelineTestEnvironmentStatus(status string) string {
	if strings.TrimSpace(status) == "" {
		return "-"
	}
	return strings.ReplaceAll(status, "_", " ")
}

// pipelineTestEnvironmentDuration is "45s", "5m12s" or "1h05m", and empty
// for a leg with no duration yet.
func pipelineTestEnvironmentDuration(seconds *int64) string {
	if seconds == nil || *seconds < 0 {
		return ""
	}
	duration := time.Duration(*seconds) * time.Second
	switch {
	case duration < time.Minute:
		return fmt.Sprintf("%ds", *seconds)
	case duration < time.Hour:
		return fmt.Sprintf("%dm%02ds", *seconds/60, *seconds%60)
	default:
		return fmt.Sprintf("%dh%02dm", *seconds/3600, (*seconds%3600)/60)
	}
}

// pipelineTestEnvironmentCounts is the shown legs counted by outcome. It is
// counted from the legs the grid shows (each cell's newest attempt), never
// taken from the platform's summary, so the line under the grid cannot
// contradict it when a leg was retried.
type pipelineTestEnvironmentCounts struct {
	total, passed, failed, errored, timedOut, cancelled, noResult, running, queued, tornDown int
}

func countPipelineTestEnvironmentLegs(legs []client.PipelineTestEnvironment) pipelineTestEnvironmentCounts {
	counts := pipelineTestEnvironmentCounts{total: len(legs)}
	for _, leg := range legs {
		isTornDown := isPipelineTestEnvironmentTornDown(leg)
		if isTornDown {
			counts.tornDown++
		}
		switch pipelineStringValue(leg.Result) {
		case "passed":
			counts.passed++
		case "failed":
			counts.failed++
		case "error":
			counts.errored++
		case "timed_out":
			counts.timedOut++
		case "cancelled":
			counts.cancelled++
		case "":
			switch {
			case isTornDown:
				counts.noResult++
			case leg.Status == "queued":
				counts.queued++
			default:
				counts.running++
			}
		}
	}
	return counts
}

// pipelineTestEnvironmentsSummaryLine is "5/6 legs passed - 1 failed
// (kubernetes 1.37 x postgres 16) - all 6 clusters torn down".
func pipelineTestEnvironmentsSummaryLine(legs []client.PipelineTestEnvironment, axes []string) string {
	counts := countPipelineTestEnvironmentLegs(legs)
	legNoun := "legs"
	if counts.total == 1 {
		legNoun = "leg"
	}
	parts := []string{fmt.Sprintf("%d/%d %s passed", counts.passed, counts.total, legNoun)}
	for _, outcome := range []struct {
		count  int
		result string
		label  string
	}{
		{counts.failed, "failed", "failed"},
		{counts.errored, "error", "errored"},
		{counts.timedOut, "timed_out", "timed out"},
		{counts.cancelled, "cancelled", "cancelled"},
		{counts.noResult, pipelineTestEnvironmentNoResult, "ended without a result"},
	} {
		if outcome.count == 0 {
			continue
		}
		part := fmt.Sprintf("%d %s", outcome.count, outcome.label)
		if names := pipelineTestEnvironmentNames(legs, axes, outcome.result); names != "" {
			part += " (" + names + ")"
		}
		parts = append(parts, part)
	}
	if counts.running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", counts.running))
	}
	if counts.queued > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", counts.queued))
	}
	if teardown := pipelineTestEnvironmentTeardown(counts, legs); teardown != "" {
		parts = append(parts, teardown)
	}
	return strings.Join(parts, " - ")
}

func pipelineTestEnvironmentNames(legs []client.PipelineTestEnvironment, axes []string, result string) string {
	var names []string
	for _, leg := range legs {
		if pipelineTestEnvironmentOutcome(leg) == result {
			names = append(names, pipelineTestEnvironmentName(leg, axes))
		}
	}
	if len(names) > pipelineTestEnvironmentNamedLegs {
		hidden := len(names) - pipelineTestEnvironmentNamedLegs
		names = append(names[:pipelineTestEnvironmentNamedLegs], fmt.Sprintf("%d more", hidden))
	}
	return strings.Join(names, ", ")
}

// pipelineTestEnvironmentTeardown says what became of the legs' clusters:
// "all 6 clusters torn down", "2 clusters still up, 4 torn down", or nothing
// while no leg has had a cluster.
func pipelineTestEnvironmentTeardown(counts pipelineTestEnvironmentCounts,
	legs []client.PipelineTestEnvironment) string {
	total := counts.total
	if total > 0 && counts.tornDown >= total {
		if total == 1 {
			return "its cluster torn down"
		}
		return fmt.Sprintf("all %d clusters torn down", total)
	}
	stillUp := 0
	for _, leg := range legs {
		if leg.ClusterID != nil && !isPipelineTestEnvironmentTornDown(leg) {
			stillUp++
		}
	}
	switch {
	case stillUp > 0 && counts.tornDown > 0:
		return fmt.Sprintf("%s still up, %d torn down", pluralClusters(stillUp), counts.tornDown)
	case stillUp > 0:
		return pluralClusters(stillUp) + " still up"
	case counts.tornDown > 0:
		return fmt.Sprintf("%d of %d clusters torn down", counts.tornDown, total)
	default:
		return ""
	}
}

// writePipelineTestEnvironmentFailures prints each failed, errored or timed
// out leg with its message and the last lines of its test log.
func writePipelineTestEnvironmentFailures(output io.Writer, legs []client.PipelineTestEnvironment, axes []string,
	options pipelineTestEnvironmentsOptions) {
	for _, leg := range legs {
		if leg.Result == nil || (*leg.Result != "failed" && *leg.Result != "error" && *leg.Result != "timed_out") {
			continue
		}
		heading := fmt.Sprintf("\n%s  %s", pipelineTestEnvironmentCell(leg), pipelineTestEnvironmentName(leg, axes))
		if leg.Attempt > 1 {
			heading += fmt.Sprintf(" (attempt %d)", leg.Attempt)
		}
		_, _ = fmt.Fprintln(output, heading)
		if strings.TrimSpace(leg.Message) != "" {
			_, _ = fmt.Fprintf(output, "  %s\n", pipelineTestText(leg.Message))
		}
		logLines := pipelineTestEnvironmentLogLinesOf(leg.TestLogTail)
		shown := logLines
		if !options.isFullLog && len(logLines) > pipelineTestEnvironmentLogLines {
			shown = logLines[len(logLines)-pipelineTestEnvironmentLogLines:]
		}
		for _, line := range shown {
			_, _ = fmt.Fprintf(output, "    %s\n", line)
		}
		if len(shown) < len(logLines) {
			hint := "--logs prints the whole tail"
			if options.fullLogHint != "" {
				hint = "'" + options.fullLogHint + "' prints the whole tail"
			}
			_, _ = fmt.Fprintf(output, "  (last %d of %d log lines; %s)\n", len(shown), len(logLines), hint)
		}
	}
}

func pipelineTestEnvironmentLogLinesOf(tail string) []string {
	stripped, _ := hiddenunicode.Strip(strings.ReplaceAll(tail, "\r\n", "\n"))
	cleaned := strings.TrimRight(stripped, "\n ")
	if strings.TrimSpace(cleaned) == "" {
		return nil
	}
	lines := strings.Split(cleaned, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(strings.ReplaceAll(line, "\r", ""), " \t")
	}
	return lines
}

// pipelineTestEnvironmentName is "kubernetes 1.37 x postgres 16", or the
// step key for a leg with no matrix.
func pipelineTestEnvironmentName(leg client.PipelineTestEnvironment, axes []string) string {
	var parts []string
	for _, axis := range axes {
		if value, isPresent := leg.Matrix[axis]; isPresent {
			parts = append(parts, axis+" "+value)
		}
	}
	if len(parts) == 0 {
		return pipelineTestText(leg.StepKey)
	}
	for index, part := range parts {
		parts[index] = pipelineTestText(part)
	}
	return strings.Join(parts, " x ")
}

// pipelineTestEnvironmentMatrix is "kubernetes=1.36,postgres=17" in axis
// order.
func pipelineTestEnvironmentMatrix(leg client.PipelineTestEnvironment, axes []string) string {
	var parts []string
	for _, axis := range axes {
		if value, isPresent := leg.Matrix[axis]; isPresent {
			parts = append(parts, axis+"="+value)
		}
	}
	return strings.Join(parts, ",")
}

func appendDistinct(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// compareMatrixValues orders matrix values as versions where they are ones
// ("1.9" before "1.10", "16" before "17") and as text otherwise.
func compareMatrixValues(left string, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		leftNumber, leftError := strconv.Atoi(leftParts[index])
		rightNumber, rightError := strconv.Atoi(rightParts[index])
		switch {
		case leftError == nil && rightError == nil:
			if leftNumber != rightNumber {
				return leftNumber - rightNumber
			}
		case leftParts[index] != rightParts[index]:
			return strings.Compare(leftParts[index], rightParts[index])
		}
	}
	return len(leftParts) - len(rightParts)
}

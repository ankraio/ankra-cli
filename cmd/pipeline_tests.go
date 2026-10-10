package cmd

// The tests verb: the per-test outcomes Ankra records from the reports a
// stage declares under test_results (ankra-q573dh.9.9 over the platform's
// ankra-q573dh.9.1 routes). 'tests <run>' is one run's summary, 'tests
// history <test key>' one test across a branch's newest runs, and 'tests
// timings --stage <stage>' the mean durations a sharding script splits by.
// Read-only; a step's own outcome is its exit code, never these results.

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// pipelineTestMessageWidth bounds a failure message in the table to one
// readable line; -o json carries the message in full.
const pipelineTestMessageWidth = 80

// The platform's bounds on the tests reads (enginekit/pipelinetests
// MaxSlowestCases and MaxHistoryRuns). The server refuses a value outside
// them with a 422; checking here turns that into a usage error before any
// request.
const (
	pipelineTestsMaxSlowest = 50
	pipelineTestsMaxRuns    = 100
)

func newPipelineTestsCommand() *cobra.Command {
	testsCommand := &cobra.Command{
		Use:   "tests <run>",
		Short: "Show a pipeline run's test results, a test's history, or test timings",
		Long: `Show the test results Ankra recorded for a pipeline run.

A stage declares the reports its test command writes under test_results
(JUnit, go test -json, pytest or Playwright), and Ankra reads every test's
outcome from them. 'ankra pipeline tests <run>' lists the run's reports and
what became of each, then its failed, flaky and slowest tests. A run that
declared no reports says so: that is not a passing run.

  ankra pipeline tests <run>                          one run's summary
  ankra pipeline tests history <test key>             one test across the branch's runs
  ankra pipeline tests timings --stage <stage>        mean durations, for sharding

The test key is the one 'tests <run> -o json' prints for each test. -o
json/yaml prints every field the server carries.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineRunTests(command, selector, arguments[0])
		},
	}
	testsCommand.Flags().Int("slowest", 10, "How many of the slowest tests to list (0 to 50)")
	registerPipelineSelectorFlags(testsCommand)
	registerStructuredOutputFlags(testsCommand)
	testsCommand.AddCommand(newPipelineTestsHistoryCommand(), newPipelineTestsTimingsCommand())
	return testsCommand
}

func newPipelineTestsHistoryCommand() *cobra.Command {
	historyCommand := &cobra.Command{
		Use:   "history <test key>",
		Short: "Show one test's outcomes across a branch's newest runs",
		Long: `Show one test's outcomes in the newest runs of a branch (the repository's
default branch unless --branch names another) and its flaky and failure rates
over them. Only the branch's own runs count (push, schedule, manual): a pull
request ran code the branch does not have.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineTestHistory(command, selector, arguments[0])
		},
	}
	historyCommand.Flags().String("branch", "", "Branch to read (default: the repository's default branch)")
	historyCommand.Flags().Int("runs", 0, "How many of the newest runs to look back over (1 to 100; default 30)")
	registerPipelineSelectorFlags(historyCommand)
	registerStructuredOutputFlags(historyCommand)
	return historyCommand
}

func newPipelineTestsTimingsCommand() *cobra.Command {
	timingsCommand := &cobra.Command{
		Use:   "timings --stage <stage>",
		Short: "Show each file's or test's mean recorded duration for a stage",
		Long: `Show each file's (or, with --group-by test, each test's) mean recorded
duration for one stage across the newest runs of a branch, slowest first: the
input a sharding script balances its shards by. When nothing has been
recorded for the stage on the branch yet the command says so; fall back to an
even split rather than reading that as every test taking no time.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineTestTimings(command, selector)
		},
	}
	timingsCommand.Flags().String("stage", "", "Stage whose tests to time (required)")
	timingsCommand.Flags().String("branch", "", "Branch to read (default: the repository's default branch)")
	timingsCommand.Flags().String("group-by", client.PipelineTestTimingsByFile, "Group by \"file\" or \"test\"")
	timingsCommand.Flags().Int("runs", 0, "How many of the newest runs to sample (1 to 100; default 30)")
	registerPipelineSelectorFlags(timingsCommand)
	registerStructuredOutputFlags(timingsCommand)
	return timingsCommand
}

func runPipelineRunTests(command *cobra.Command, selector client.PipelineSelector, runID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	slowest, _ := command.Flags().GetInt("slowest")
	if slowest < 0 || slowest > pipelineTestsMaxSlowest {
		return withExitCode(exitUsage, fmt.Errorf("--slowest must be 0 to %d", pipelineTestsMaxSlowest))
	}
	summary, readError := apiClient.GetPipelineRunTests(command.Context(), selector, strings.TrimSpace(runID), slowest)
	if readError != nil {
		return readError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, summary)
	}

	output := command.OutOrStdout()
	if !summary.HasReports {
		_, _ = fmt.Fprintln(output, "This run declared no test reports, so Ankra has no test results for it.")
		return nil
	}
	_, _ = fmt.Fprintf(output, "%s in %s\n", pipelineTestCountsSentence(summary.Counts),
		pipelineTestDuration(summary.DurationMS))
	if summary.IsTruncated {
		_, _ = fmt.Fprintln(output, "Not every test was stored: the counts cover every test read, the lists below do not.")
	}

	_, _ = fmt.Fprintln(output, "\nReports")
	reports := newPipelineTestsTable(output, table.Row{"STAGE", "FORMAT", "STATUS", "TESTS", "NOTE"})
	for _, report := range summary.Reports {
		reports.AppendRow(table.Row{
			pipelineStringOrDash(report.Stage),
			pipelineStringOrDash(report.Format),
			pipelineStringOrDash(report.Status),
			strconv.Itoa(report.Counts.Total),
			pipelineTestReportNote(report),
		})
	}
	reports.Render()

	writePipelineTestCases(output, "Failed", summary.Failed, true)
	writePipelineTestCases(output, "Flaky", summary.Flaky, true)
	writePipelineTestCases(output, "Slowest", summary.Slowest, false)
	return nil
}

func runPipelineTestHistory(command *cobra.Command, selector client.PipelineSelector, testKey string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	testKey = strings.TrimSpace(testKey)
	if testKey == "" {
		return withExitCode(exitUsage, errors.New("a test key is required"))
	}
	runs, _ := command.Flags().GetInt("runs")
	if runsError := validatePipelineTestRuns(runs); runsError != nil {
		return runsError
	}
	branch, _ := command.Flags().GetString("branch")
	history, readError := apiClient.GetPipelineTestHistory(command.Context(), selector, testKey,
		client.PipelineTestHistoryOptions{Branch: strings.TrimSpace(branch), Runs: runs})
	if readError != nil {
		return readError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, history)
	}

	output := command.OutOrStdout()
	_, _ = fmt.Fprintf(output, "%s on %s\n", pipelineTestName(history.Suite, history.Name, history.TestKey),
		pipelineStringOrDash(history.Branch))
	if history.Observed == 0 {
		_, _ = fmt.Fprintln(output, "This test has not run on the branch in the runs looked at.")
		return nil
	}
	_, _ = fmt.Fprintf(output, "Ran in %d runs: %d failed, %d flaky. Flaky rate %s, failure rate %s.\n",
		history.Observed, history.Failed, history.Flaky,
		pipelineTestRate(history.FlakyRate), pipelineTestRate(history.FailureRate))
	entries := newPipelineTestsTable(output, table.Row{"RUN", "COMMIT", "OUTCOME", "DURATION", "ATTEMPTS", "RECORDED"})
	for _, entry := range history.Entries {
		entries.AppendRow(table.Row{
			"#" + strconv.FormatInt(entry.RunNumber, 10),
			shortSHA(entry.HeadSHA),
			entry.Outcome,
			pipelineTestDuration(entry.DurationMS),
			strconv.Itoa(entry.Attempts),
			pipelineStringOrDash(entry.RecordedAt),
		})
	}
	entries.Render()
	return nil
}

func runPipelineTestTimings(command *cobra.Command, selector client.PipelineSelector) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	stage, _ := command.Flags().GetString("stage")
	stage = strings.TrimSpace(stage)
	if stage == "" {
		return withExitCode(exitUsage, errors.New("--stage is required"))
	}
	groupBy, _ := command.Flags().GetString("group-by")
	groupBy = strings.TrimSpace(groupBy)
	if groupBy != client.PipelineTestTimingsByFile && groupBy != client.PipelineTestTimingsByTest {
		return withExitCode(exitUsage, fmt.Errorf("--group-by must be %q or %q",
			client.PipelineTestTimingsByFile, client.PipelineTestTimingsByTest))
	}
	runs, _ := command.Flags().GetInt("runs")
	if runsError := validatePipelineTestRuns(runs); runsError != nil {
		return runsError
	}
	branch, _ := command.Flags().GetString("branch")
	timings, readError := apiClient.GetPipelineTestTimings(command.Context(), selector, stage,
		client.PipelineTestTimingsOptions{Branch: strings.TrimSpace(branch), GroupBy: groupBy, Runs: runs})
	if readError != nil {
		return readError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, timings)
	}

	output := command.OutOrStdout()
	if timings.RunsSampled == 0 {
		_, _ = fmt.Fprintf(output, "No timings recorded yet for stage %q on %s: split shards evenly until there are.\n",
			timings.Stage, pipelineStringOrDash(timings.Branch))
		return nil
	}
	partial := ""
	if timings.IsPartial {
		partial = " (partial: not every sampled run recorded every test)"
	}
	_, _ = fmt.Fprintf(output, "Stage %q on %s, by %s, over %d runs%s\n",
		timings.Stage, pipelineStringOrDash(timings.Branch), timings.GroupBy, timings.RunsSampled, partial)
	header := table.Row{"FILE", "MEAN", "SAMPLES"}
	byTest := timings.GroupBy == client.PipelineTestTimingsByTest
	if byTest {
		header = table.Row{"FILE", "TEST", "MEAN", "SAMPLES"}
	}
	entries := newPipelineTestsTable(output, header)
	for _, entry := range timings.Entries {
		row := table.Row{pipelineStringOrDash(entry.File)}
		if byTest {
			row = append(row, pipelineTestName(pipelineStringValue(entry.Suite), pipelineStringValue(entry.Name),
				pipelineStringValue(entry.TestKey)))
		}
		row = append(row, pipelineTestDuration(entry.DurationMS), strconv.Itoa(entry.Samples))
		entries.AppendRow(row)
	}
	entries.Render()
	return nil
}

// validatePipelineTestRuns accepts zero (the server's default window) or a
// window within the platform's bound.
func validatePipelineTestRuns(runs int) error {
	if runs < 0 || runs > pipelineTestsMaxRuns {
		return withExitCode(exitUsage, fmt.Errorf("--runs must be 1 to %d", pipelineTestsMaxRuns))
	}
	return nil
}

func newPipelineTestsTable(output io.Writer, header table.Row) table.Writer {
	writer := table.NewWriter()
	writer.SetOutputMirror(output)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(header)
	return writer
}

// writePipelineTestCases renders one list of a run summary under its own
// heading, and nothing at all for an empty list.
func writePipelineTestCases(output io.Writer, heading string, cases []client.PipelineTestCase, withMessage bool) {
	if len(cases) == 0 {
		return
	}
	_, _ = fmt.Fprintf(output, "\n%s\n", heading)
	header := table.Row{"TEST", "FILE", "STAGE", "DURATION", "ATTEMPTS"}
	if withMessage {
		header = append(header, "MESSAGE")
	}
	writer := newPipelineTestsTable(output, header)
	for _, testCase := range cases {
		row := table.Row{
			truncateCell(pipelineTestName(testCase.Suite, testCase.Name, testCase.TestKey), pipelineTestMessageWidth),
			pipelineStringOrDash(testCase.File),
			pipelineStringOrDash(testCase.Stage),
			pipelineTestDuration(testCase.DurationMS),
			strconv.Itoa(testCase.Attempts),
		}
		if withMessage {
			row = append(row, pipelineTestMessage(testCase.FailureMessage))
		}
		writer.AppendRow(row)
	}
	writer.Render()
}

// pipelineTestCountsSentence is the one-line summary of a run's tests.
func pipelineTestCountsSentence(counts client.PipelineTestCounts) string {
	return fmt.Sprintf("%d tests: %d passed, %d failed, %d flaky, %d skipped",
		counts.Total, counts.Passed, counts.Failed, counts.Flaky, counts.Skipped)
}

// pipelineTestReportNote says what a reader needs about a report beyond its
// status: the platform's reason when it has one, then whether its stored
// tests are partial or another attempt replaced it.
func pipelineTestReportNote(report client.PipelineTestReport) string {
	var notes []string
	if message := strings.TrimSpace(report.ErrorMessage); message != "" {
		notes = append(notes, pipelineTestMessage(message))
	}
	if report.IsTruncated {
		notes = append(notes, fmt.Sprintf("%d of %d tests stored", report.StoredCount, report.Counts.Total))
	}
	if report.IsSuperseded {
		notes = append(notes, "superseded by a later attempt")
	}
	if len(notes) == 0 {
		return "-"
	}
	return strings.Join(notes, "; ")
}

// pipelineTestName names a test as suite > name, falling back to its key.
func pipelineTestName(suite string, name string, testKey string) string {
	switch {
	case suite != "" && name != "":
		return suite + " > " + name
	case name != "":
		return name
	default:
		return pipelineStringOrDash(testKey)
	}
}

// pipelineTestMessage is a failure message's first line, bounded for a table
// cell and stripped of hidden characters (it is test output, not ours).
func pipelineTestMessage(message string) string {
	firstLine, _, _ := strings.Cut(strings.TrimSpace(message), "\n")
	if firstLine == "" {
		return "-"
	}
	return truncateCell(firstLine, pipelineTestMessageWidth)
}

// pipelineTestRate renders a rate the server could compute as a percentage
// and one it could not (nil: the test never ran) as "n/a", never as 0%.
func pipelineTestRate(rate *float64) string {
	if rate == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*rate*100, 'f', 1, 64) + "%"
}

func pipelineTestDuration(milliseconds int64) string {
	return (time.Duration(milliseconds) * time.Millisecond).String()
}

func pipelineStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

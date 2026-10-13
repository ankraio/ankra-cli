package cmd

// The flaky test quarantine verbs (ankra-q573dh.9.10 over the platform's
// …/pipeline-tests/quarantine routes): 'ankra pipeline tests quarantine'
// shows a repository's quarantine, 'candidates' the default branch's tests
// that qualify, 'add' and 'release' quarantine or release one test, and
// 'settings' turns it on or off and sets its thresholds.
//
// A quarantined test still runs and is still reported. The only thing the
// quarantine changes is that a failing run step whose only failed tests are
// quarantined settles as a success; the run's test summary lists those
// failures either way.

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// The platform's bounds on the quarantine (enginekit/pipelinetests). The
// server refuses a value outside them with a 422; checking here turns that
// into a usage error before any request.
const (
	pipelineTestQuarantineMaxReasonRunes   = 500
	pipelineTestQuarantineMaxCandidates    = 100
	pipelineTestQuarantineMinSamples       = 3
	pipelineTestQuarantineMaxSamples       = 1000
	pipelineTestQuarantineMinReleasePasses = 1
	pipelineTestQuarantineMaxReleasePasses = 100
)

// pipelineTestQuarantineLong is the help every quarantine verb shares: what
// the quarantine forgives, which tests may be quarantined, how one is
// released, and where its failures stay visible.
const pipelineTestQuarantineLong = `The flaky test quarantine lets a repository keep a known-flaky test from
failing its pipeline runs while someone fixes it.

It is off until you turn it on ('ankra pipeline tests quarantine settings
--enable', pipelines.manage). A test may be quarantined when, over the default
branch's own runs (never a pull request check) in the last 30 days, it ran in
at least --min-samples runs (default 20) and was flaky - failed, then passed
on a retry - in at least --min-flaky-rate of them (default 0.1, 10%).
'candidates' lists exactly the tests that qualify; 'add' quarantines one.

A quarantined test still runs and is still reported. What changes: a failing
'run' step whose ONLY failed tests are quarantined, read from test reports
the step uploaded whole, succeeds instead, and the steps after it run. A step
that also failed another test, failed on a timeout or infrastructure error,
or whose reports could not be read fails exactly as before.

A quarantine releases itself after --release-after passes in a row on the
default branch (default 10); a failed or flaky run starts the count again.
'release' ends one by hand.

Nothing is hidden: 'ankra pipeline tests <run>' still lists every failed
test, and lists the quarantined failures in a section of their own whether or
not they rescued their step. 'ankra pipeline get <run>' says which steps the
quarantine forgave.`

func newPipelineTestQuarantineCommand() *cobra.Command {
	quarantineCommand := &cobra.Command{
		Use:   "quarantine",
		Short: "Show and manage the repository's flaky test quarantine",
		Long: pipelineTestQuarantineLong + `

With no subcommand, shows the settings, the quarantined tests and the ones
released in the last 30 days (the same as 'quarantine list').`,
		Example: `  ankra pipeline tests quarantine
  ankra pipeline tests quarantine candidates
  ankra pipeline tests quarantine add <test key> --reason "times out on the shared runner"
  ankra pipeline tests quarantine release <quarantine id or test key> --reason "fixed in #123"
  ankra pipeline tests quarantine settings --enable --min-flaky-rate 0.2`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineTestQuarantineList(command, selector)
		},
	}
	registerPipelineSelectorFlags(quarantineCommand)
	registerStructuredOutputFlags(quarantineCommand)
	quarantineCommand.AddCommand(
		newPipelineTestQuarantineListCommand(),
		newPipelineTestQuarantineCandidatesCommand(),
		newPipelineTestQuarantineAddCommand(),
		newPipelineTestQuarantineReleaseCommand(),
		newPipelineTestQuarantineSettingsCommand(),
	)
	return quarantineCommand
}

func newPipelineTestQuarantineListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Show the quarantine settings, the quarantined tests and the recently released ones",
		Long: `Show the repository's quarantine settings, every quarantined test (how
flaky it was when quarantined, its default-branch passes in a row towards its
release, and who quarantined it), and the tests released in the last 30 days
with why. -o json/yaml prints every field the server carries.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineTestQuarantineList(command, selector)
		},
	}
	registerPipelineSelectorFlags(listCommand)
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newPipelineTestQuarantineCandidatesCommand() *cobra.Command {
	candidatesCommand := &cobra.Command{
		Use:   "candidates",
		Short: "List the default branch's flaky tests that may be quarantined",
		Long: `List the tests that qualify for quarantine, most flaky first: tests that, over
the default branch's own runs in the last 30 days, ran in at least the
repository's minimum number of runs and were flaky in at least its minimum
share of them, and are not quarantined already. Quarantine one with
'ankra pipeline tests quarantine add <test key> --reason "..."'.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineTestQuarantineCandidates(command, selector)
		},
	}
	candidatesCommand.Flags().Int("limit", 0,
		fmt.Sprintf("How many candidates to list (1 to %d; default 50)", pipelineTestQuarantineMaxCandidates))
	registerPipelineSelectorFlags(candidatesCommand)
	registerStructuredOutputFlags(candidatesCommand)
	return candidatesCommand
}

func newPipelineTestQuarantineAddCommand() *cobra.Command {
	addCommand := &cobra.Command{
		Use:   "add <test key>",
		Short: "Quarantine one flaky test",
		Long: `Quarantine one test, by the key 'quarantine candidates', 'tests <run> -o json'
and 'tests history' print for it. --reason is required (1 to 500 characters)
and is shown beside the quarantine.

The platform refuses while the quarantine is off for the repository, for a
test that is not flaky enough on the default branch (it says the test's rate
and the thresholds), for a test already quarantined, and for a test with no
default-branch result in the last 30 days. Needs pipelines.manage.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			testKey, keyError := pipelineTestQuarantineTestKey(arguments[0])
			if keyError != nil {
				return keyError
			}
			reason, reasonError := pipelineTestQuarantineReason(command, 1)
			if reasonError != nil {
				return reasonError
			}
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			quarantine, quarantineError := apiClient.QuarantinePipelineTest(command.Context(), selector,
				client.QuarantinePipelineTestRequest{TestKey: testKey, Reason: reason})
			if quarantineError != nil {
				return quarantineError
			}
			if isStructured, renderError := renderStructured(command, quarantine); isStructured || renderError != nil {
				return renderError
			}
			output := command.OutOrStdout()
			_, _ = fmt.Fprintf(output, "Quarantined %s (%s): flaky in %s of %d default-branch runs.\n",
				pipelineTestQuarantineName(*quarantine), pipelineTestText(pipelineStringValue(quarantine.File)),
				pipelineTestQuarantinePercent(quarantine.FlakyRate), quarantine.Samples)
			_, _ = fmt.Fprintf(output, "A step whose only failed tests are quarantined now succeeds. "+
				"Release it with 'ankra pipeline tests quarantine release %s'.\n", quarantine.ID)
			return nil
		},
	}
	addCommand.Flags().String("reason", "", "Why the test is quarantined (required, 1 to 500 characters)")
	registerPipelineSelectorFlags(addCommand)
	registerStructuredOutputFlags(addCommand)
	return addCommand
}

func newPipelineTestQuarantineReleaseCommand() *cobra.Command {
	releaseCommand := &cobra.Command{
		Use:   "release <quarantine id or test key>",
		Short: "Release a quarantined test, so its failures fail their step again",
		Long: `Release a quarantined test: from the next run on, a step that fails it fails.

Name the quarantine by its id ('quarantine list' prints it) or by the test's
key, which is looked up among the repository's live quarantines. --reason is
optional (at most 500 characters). A quarantine also releases itself after
enough default-branch passes in a row. Needs pipelines.manage.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			reason, reasonError := pipelineTestQuarantineReason(command, 0)
			if reasonError != nil {
				return reasonError
			}
			reference := strings.TrimSpace(arguments[0])
			if !looksLikeUUID(strings.ToLower(reference)) && !isPipelineTestKey(strings.ToLower(reference)) {
				return withExitCode(exitUsage, fmt.Errorf(
					"%q is neither a quarantine id nor a test key (64 hexadecimal characters)", arguments[0]))
			}
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			quarantineID, resolveError := resolvePipelineTestQuarantineID(command, selector, reference)
			if resolveError != nil {
				return resolveError
			}
			released, releaseError := apiClient.ReleasePipelineTestQuarantine(command.Context(), selector,
				quarantineID, reason)
			if releaseError != nil {
				return releaseError
			}
			if isStructured, renderError := renderStructured(command, released); isStructured || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(),
				"Released the quarantine of %s: a step that fails it fails again from the next run.\n",
				pipelineTestQuarantineName(*released))
			return nil
		},
	}
	releaseCommand.Flags().String("reason", "", "Why the quarantine is released (optional, at most 500 characters)")
	registerPipelineSelectorFlags(releaseCommand)
	registerStructuredOutputFlags(releaseCommand)
	return releaseCommand
}

func newPipelineTestQuarantineSettingsCommand() *cobra.Command {
	settingsCommand := &cobra.Command{
		Use:   "settings",
		Short: "Turn the quarantine on or off and set its thresholds",
		Long: `Turn the repository's test quarantine on or off and set when a test may be
quarantined and when one releases itself. Only the flags you pass are sent;
the others keep their stored values. With no flag, prints the settings.

  --enable / --disable     whether a step whose only failed tests are
                           quarantined succeeds (off by default)
  --min-flaky-rate 0.1     the share of default-branch runs a test must have
                           been flaky in (greater than 0, at most 1)
  --min-samples 20         over at least this many observed runs (3 to 1000)
  --release-after 10       release after this many passes in a row (1 to 100)

Turning it off keeps the quarantined tests: their failures fail their steps
again until it is turned back on. Needs pipelines.manage to change.`,
		Example: `  ankra pipeline tests quarantine settings
  ankra pipeline tests quarantine settings --enable
  ankra pipeline tests quarantine settings --min-flaky-rate 0.2 --min-samples 30 --release-after 15
  ankra pipeline tests quarantine settings --disable`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			update, updateError := pipelineTestQuarantineSettingsUpdate(command)
			if updateError != nil {
				return updateError
			}
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineTestQuarantineSettings(command, selector, update)
		},
	}
	settingsCommand.Flags().Bool("enable", false, "Turn the quarantine on")
	settingsCommand.Flags().Bool("disable", false, "Turn the quarantine off (the quarantined tests are kept)")
	settingsCommand.Flags().Float64("min-flaky-rate", 0,
		"Share of default-branch runs a test must have been flaky in to be quarantined (greater than 0, at most 1)")
	settingsCommand.Flags().Int("min-samples", 0, "Observed default-branch runs that rate must rest on (3 to 1000)")
	settingsCommand.Flags().Int("release-after", 0,
		"Default-branch passes in a row that release a quarantine (1 to 100)")
	registerPipelineSelectorFlags(settingsCommand)
	registerStructuredOutputFlags(settingsCommand)
	return settingsCommand
}

func runPipelineTestQuarantineList(command *cobra.Command, selector client.PipelineSelector) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	view, readError := apiClient.GetPipelineTestQuarantine(command.Context(), selector)
	if readError != nil {
		return readError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, view)
	}

	output := command.OutOrStdout()
	_, _ = fmt.Fprintln(output, pipelineTestQuarantineSettingsLine(view.Settings))
	if len(view.Quarantines) == 0 {
		_, _ = fmt.Fprintln(output, "\nNo tests are quarantined. "+
			"'ankra pipeline tests quarantine candidates' lists the tests that qualify.")
	} else {
		_, _ = fmt.Fprintln(output, "\nQuarantined")
		writer := newPipelineTestsTable(output,
			table.Row{"NAME", "FILE", "FLAKY RATE", "SAMPLES", "PASSES SO FAR", "SINCE", "BY", "ID"})
		for _, quarantine := range view.Quarantines {
			writer.AppendRow(table.Row{
				truncateCell(pipelineTestQuarantineName(quarantine), pipelineTestMessageWidth),
				pipelineTestText(pipelineStringValue(quarantine.File)),
				pipelineTestQuarantinePercent(quarantine.FlakyRate),
				strconv.Itoa(quarantine.Samples),
				fmt.Sprintf("%d of %d", quarantine.ConsecutivePasses, view.Settings.ReleaseAfterPasses),
				formatTimeAgo(quarantine.CreatedAt),
				pipelineTestText(pipelineStringValue(quarantine.CreatedByEmail)),
				quarantine.ID,
			})
		}
		writer.Render()
	}
	if len(view.RecentlyReleased) > 0 {
		_, _ = fmt.Fprintln(output, "\nReleased in the last 30 days")
		writer := newPipelineTestsTable(output, table.Row{"NAME", "RELEASED", "WHY", "BY"})
		for _, quarantine := range view.RecentlyReleased {
			writer.AppendRow(table.Row{
				truncateCell(pipelineTestQuarantineName(quarantine), pipelineTestMessageWidth),
				formatTimeAgo(pipelineStringValue(quarantine.ReleasedAt)),
				pipelineTestQuarantineReleaseWhy(quarantine),
				pipelineTestText(pipelineStringValue(quarantine.ReleasedByEmail)),
			})
		}
		writer.Render()
	}
	return nil
}

func runPipelineTestQuarantineCandidates(command *cobra.Command, selector client.PipelineSelector) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	limit, _ := command.Flags().GetInt("limit")
	if command.Flags().Changed("limit") && (limit < 1 || limit > pipelineTestQuarantineMaxCandidates) {
		return withExitCode(exitUsage, fmt.Errorf("--limit must be 1 to %d", pipelineTestQuarantineMaxCandidates))
	}
	candidates, readError := apiClient.ListPipelineTestQuarantineCandidates(command.Context(), selector, limit)
	if readError != nil {
		return readError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, candidates)
	}

	output := command.OutOrStdout()
	settings := candidates.Settings
	_, _ = fmt.Fprintf(output, "Tests flaky in at least %s of at least %d runs on %s in the last 30 days, "+
		"most flaky first.\n", pipelineTestQuarantinePercent(settings.MinFlakyRate), settings.MinSamples,
		pipelineTestText(candidates.DefaultBranch))
	if len(candidates.Candidates) == 0 {
		_, _ = fmt.Fprintln(output, "No test qualifies for quarantine.")
		return nil
	}
	writer := newPipelineTestsTable(output, table.Row{"NAME", "FILE", "FLAKY RATE", "FLAKY/RAN", "TEST KEY"})
	for _, candidate := range candidates.Candidates {
		writer.AppendRow(table.Row{
			truncateCell(pipelineTestName(candidate.Suite, candidate.Name, candidate.TestKey), pipelineTestMessageWidth),
			pipelineTestText(candidate.File),
			pipelineTestQuarantinePercent(candidate.FlakyRate),
			fmt.Sprintf("%d/%d", candidate.Flaky, candidate.Observed),
			pipelineTestText(candidate.TestKey),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintln(output, "\nQuarantine one with: ankra pipeline tests quarantine add <test key> --reason \"...\"")
	if !settings.Enabled {
		_, _ = fmt.Fprintln(output, "The quarantine is off for this repository: turn it on first with "+
			"'ankra pipeline tests quarantine settings --enable'.")
	}
	return nil
}

func runPipelineTestQuarantineSettings(command *cobra.Command, selector client.PipelineSelector,
	update client.PipelineTestQuarantineSettingsUpdate) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	var settings client.PipelineTestQuarantineSettings
	if update.IsEmpty() {
		view, readError := apiClient.GetPipelineTestQuarantine(command.Context(), selector)
		if readError != nil {
			return readError
		}
		settings = view.Settings
	} else {
		stored, setError := apiClient.SetPipelineTestQuarantineSettings(command.Context(), selector, update)
		if setError != nil {
			return setError
		}
		settings = *stored
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, settings)
	}
	_, _ = fmt.Fprintln(command.OutOrStdout(), pipelineTestQuarantineSettingsLine(settings))
	return nil
}

// pipelineTestQuarantineSettingsUpdate reads the settings flags into an
// update naming only the flags that were passed. Values are checked against
// the platform's ranges here so a typo is a usage error before any request;
// the platform checks them again.
func pipelineTestQuarantineSettingsUpdate(command *cobra.Command) (client.PipelineTestQuarantineSettingsUpdate, error) {
	var update client.PipelineTestQuarantineSettingsUpdate
	flags := command.Flags()
	if flags.Changed("enable") && flags.Changed("disable") {
		return update, withExitCode(exitUsage, errors.New("--enable and --disable cannot be used together"))
	}
	if flags.Changed("enable") {
		enabled, _ := flags.GetBool("enable")
		update.Enabled = &enabled
	}
	if flags.Changed("disable") {
		disabled, _ := flags.GetBool("disable")
		enabled := !disabled
		update.Enabled = &enabled
	}
	if flags.Changed("min-flaky-rate") {
		rate, _ := flags.GetFloat64("min-flaky-rate")
		if math.IsNaN(rate) || rate <= 0 || rate > 1 {
			return update, withExitCode(exitUsage, fmt.Errorf(
				"--min-flaky-rate must be greater than 0 and at most 1 (0.1 is 10%%), got %v", rate))
		}
		update.MinFlakyRate = &rate
	}
	if flags.Changed("min-samples") {
		samples, _ := flags.GetInt("min-samples")
		if samples < pipelineTestQuarantineMinSamples || samples > pipelineTestQuarantineMaxSamples {
			return update, withExitCode(exitUsage, fmt.Errorf("--min-samples must be %d to %d, got %d",
				pipelineTestQuarantineMinSamples, pipelineTestQuarantineMaxSamples, samples))
		}
		update.MinSamples = &samples
	}
	if flags.Changed("release-after") {
		passes, _ := flags.GetInt("release-after")
		if passes < pipelineTestQuarantineMinReleasePasses || passes > pipelineTestQuarantineMaxReleasePasses {
			return update, withExitCode(exitUsage, fmt.Errorf("--release-after must be %d to %d, got %d",
				pipelineTestQuarantineMinReleasePasses, pipelineTestQuarantineMaxReleasePasses, passes))
		}
		update.ReleaseAfterPasses = &passes
	}
	return update, nil
}

// resolvePipelineTestQuarantineID answers the quarantine a release names. An
// id is used as given; a test key is looked up among the repository's live
// quarantines, and no match or more than one is an error naming what was
// searched rather than a guess.
func resolvePipelineTestQuarantineID(command *cobra.Command, selector client.PipelineSelector,
	reference string) (string, error) {
	if looksLikeUUID(strings.ToLower(reference)) {
		return reference, nil
	}
	testKey := strings.ToLower(reference)
	view, readError := apiClient.GetPipelineTestQuarantine(command.Context(), selector)
	if readError != nil {
		return "", fmt.Errorf("looking test key %s up among the repository's quarantines: %w", testKey, readError)
	}
	var matches []client.PipelineTestQuarantine
	for _, quarantine := range view.Quarantines {
		if strings.EqualFold(quarantine.TestKey, testKey) {
			matches = append(matches, quarantine)
		}
	}
	switch len(matches) {
	case 0:
		return "", withExitCode(exitNotFound, fmt.Errorf(
			"no live quarantine of test key %s among the repository's %d quarantined tests; "+
				"'ankra pipeline tests quarantine list' shows them", testKey, len(view.Quarantines)))
	case 1:
		return matches[0].ID, nil
	default:
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.ID)
		}
		return "", withExitCode(exitUsage, fmt.Errorf(
			"test key %s matches %d live quarantines (%s); release one by its id",
			testKey, len(matches), strings.Join(ids, ", ")))
	}
}

// pipelineTestQuarantineTestKey checks a test key's shape: 64 hexadecimal
// characters, as the platform stores it (lower case).
func pipelineTestQuarantineTestKey(argument string) (string, error) {
	testKey := strings.ToLower(strings.TrimSpace(argument))
	if !isPipelineTestKey(testKey) {
		return "", withExitCode(exitUsage, fmt.Errorf(
			"%q is not a test key: a test key is 64 hexadecimal characters, as "+
				"'ankra pipeline tests quarantine candidates' prints it", argument))
	}
	return testKey, nil
}

// isPipelineTestKey reports whether value is a lower-case test key: the hex
// SHA-256 of a test's suite, name and file.
func isPipelineTestKey(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// pipelineTestQuarantineReason reads --reason and checks its length the way
// the platform counts it: trimmed, in characters.
func pipelineTestQuarantineReason(command *cobra.Command, minimum int) (string, error) {
	reason, _ := command.Flags().GetString("reason")
	length := len([]rune(strings.TrimSpace(reason)))
	if length < minimum {
		return "", withExitCode(exitUsage, errors.New(`--reason is required: say why the test is quarantined`))
	}
	if length > pipelineTestQuarantineMaxReasonRunes {
		return "", withExitCode(exitUsage, fmt.Errorf("--reason is at most %d characters, got %d",
			pipelineTestQuarantineMaxReasonRunes, length))
	}
	return strings.TrimSpace(reason), nil
}

// pipelineTestQuarantineSettingsLine is the one-line statement of a
// repository's quarantine: on or off, and the thresholds it applies.
func pipelineTestQuarantineSettingsLine(settings client.PipelineTestQuarantineSettings) string {
	rule := fmt.Sprintf("flaky rate >= %s over >= %d default-branch runs, released after %d passes in a row",
		pipelineTestQuarantinePercent(settings.MinFlakyRate), settings.MinSamples, settings.ReleaseAfterPasses)
	if settings.Enabled {
		return "Quarantine: on, " + rule
	}
	return "Quarantine: off (when on: " + rule + "). Quarantined tests fail their steps; " +
		"turn it on with 'ankra pipeline tests quarantine settings --enable'."
}

// pipelineTestQuarantinePercent renders a rate as a percentage with at most
// one decimal: 0.1 reads 10%, 0.125 reads 12.5%.
func pipelineTestQuarantinePercent(rate float64) string {
	return strconv.FormatFloat(math.Round(rate*1000)/10, 'f', -1, 64) + "%"
}

// pipelineTestQuarantineName names a quarantined test as suite > name.
func pipelineTestQuarantineName(quarantine client.PipelineTestQuarantine) string {
	return pipelineTestName(pipelineStringValue(quarantine.Suite), quarantine.Name, quarantine.TestKey)
}

// pipelineTestQuarantineReleaseWhy says why a quarantine ended: the passes
// that released it, or the note it was released by hand with.
func pipelineTestQuarantineReleaseWhy(quarantine client.PipelineTestQuarantine) string {
	switch pipelineStringValue(quarantine.ReleaseReason) {
	case client.PipelineTestQuarantineReleasedByPasses:
		return fmt.Sprintf("passed %d times in a row", quarantine.ConsecutivePasses)
	case client.PipelineTestQuarantineReleasedManually:
		if note := strings.TrimSpace(pipelineStringValue(quarantine.ReleaseNote)); note != "" {
			return pipelineTestMessage(note)
		}
		return "released by hand"
	default:
		return pipelineTestText(pipelineStringValue(quarantine.ReleaseReason))
	}
}

// writePipelineTestQuarantinedFailures renders the run summary's quarantined
// failures in two groups: the ones the quarantine forgave (their step
// succeeded) and the ones whose step failed anyway because another test
// failed too. Nothing is printed when the run had none, which is every run on
// a platform older than the quarantine.
func writePipelineTestQuarantinedFailures(output io.Writer, failures []client.PipelineTestQuarantinedFailure) {
	var rescued, notRescued []client.PipelineTestQuarantinedFailure
	for _, failure := range failures {
		if failure.DidRescueStep {
			rescued = append(rescued, failure)
		} else {
			notRescued = append(notRescued, failure)
		}
	}
	writeGroup := func(heading string, group []client.PipelineTestQuarantinedFailure) {
		if len(group) == 0 {
			return
		}
		_, _ = fmt.Fprintf(output, "\n%s\n", heading)
		writer := newPipelineTestsTable(output, table.Row{"TEST", "FILE", "STAGE / STEP", "MESSAGE"})
		for _, failure := range group {
			writer.AppendRow(table.Row{
				truncateCell(pipelineTestName(failure.Suite, failure.Name, failure.TestKey), pipelineTestMessageWidth),
				pipelineTestText(failure.File),
				pipelineTestText(failure.Stage) + " / " + pipelineTestText(failure.StepKey),
				pipelineTestMessage(failure.FailureMessage),
			})
		}
		writer.Render()
	}
	writeGroup("Quarantined failures (did not fail their step)", rescued)
	writeGroup("Quarantined failures (still failed the step: another test failed too)", notRescued)
}

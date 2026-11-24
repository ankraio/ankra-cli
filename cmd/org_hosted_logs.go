package cmd

// `ankra org hosted-logs show|set` (ankra-t5jf5.34.11.20.5): the CLI surface
// over GET/PUT /api/v1/org/billing/hosted-logs, the organisation's paid
// hosted logs setting and the month so far.
//
// Hosted logs are included until the organisation's priced-from month
// (1 January 2027 unless Ankra moved it). From then the first 5 GB of log
// text a month are free, and at the free 5 GB sending pauses for the rest of
// the month unless paid logs are on with a monthly limit. Turning paid logs
// on, or changing the limit while they are on, is agreeing to a charge, so
// `set` says what the limit means and asks first; turning them off charges
// nothing, so it does not ask.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// hostedLogsLimitPattern is a --limit value: whole euros, or euros and up to
// two decimals of cents.
var hostedLogsLimitPattern = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]{1,2}))?$`)

// hostedLogsMaxLimitCents is the platform's sanity bound on the monthly
// limit, EUR 1,000,000. It is checked here so a typo is refused before the
// confirmation names it.
const hostedLogsMaxLimitCents int64 = 100_000_000

func newOrgHostedLogsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "hosted-logs",
		Short: "Show this month's hosted logs against the free 5 GB, and turn paid logs on or off",
		Long: `Ankra hosted logs are included until 31 December 2026. From 1 January 2027
each organisation gets 5 GB of log text a month free, across all its
clusters, then EUR 0.25 per GB, kept 7 days. The month is the calendar
month in UTC.

At the free 5 GB, sending pauses for the rest of the month unless an owner
turns on paid logs with a monthly limit. With paid logs on, sending goes on
past the free 5 GB until the month's charge reaches the limit, and pauses
there. Nothing is charged for hosted logs unless paid logs are on. Owners
are told at 80% and 100% of the free 5 GB, and of the limit while paid logs
are on. Built-in metrics are included in every plan.

  ankra org hosted-logs show   this month's log text, whether sending is paused, and the setting
  ankra org hosted-logs set    turn paid logs on with a monthly limit, or off

Reading needs billing.read; changing the setting needs billing.manage.
Which clusters send their logs at all is 'ankra cluster logs-ship'.`,
		Example: `  ankra org hosted-logs show
  ankra org hosted-logs set --paid --limit 20
  ankra org hosted-logs set --paid=false`,
	}
	command.AddCommand(newOrgHostedLogsShowCommand(), newOrgHostedLogsSetCommand())
	return command
}

func newOrgHostedLogsShowCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "show",
		Short: "Show this month's hosted logs against the free 5 GB, and the paid logs setting",
		Long: `Show how much log text the organisation's clusters sent this month against
the free 5 GB, whether sending is paused and why, what the month comes to so
far, whether paid logs are on and their monthly limit, and the date hosted
logs are priced from. Before that date nothing is charged.

-o json and -o yaml print the platform's answer as it is: bytes and euro
cents, not rounded.`,
		Example: `  ankra org hosted-logs show
  ankra org hosted-logs show -o json`,
		Args: cobra.NoArgs,
		RunE: runOrgHostedLogsShow,
	}
	registerStructuredOutputFlags(command)
	return command
}

func newOrgHostedLogsSetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "set",
		Short: "Turn paid hosted logs on with a monthly limit, or off",
		Long: `Turn paid hosted logs on or off, or change the monthly limit.

--paid --limit <euros> turns paid logs on: logs above the free 5 GB a month
then cost EUR 0.25 per GB, up to the limit you set, and sending pauses for
the rest of the month once the month's charge reaches it. --limit is the
most hosted logs may cost in one month, in euros (20 or 20.50). --paid on
its own keeps the limit already stored. --limit on its own changes the limit
and leaves paid logs as they are.

--paid=false turns paid logs off: at the free 5 GB, sending pauses for the
rest of the month. What the month already charged stays charged.

Turning paid logs on or changing the limit while they are on agrees to a
charge, so set says what the limit means and asks first. --yes skips the
question; without a terminal to ask on (a script, CI) it is required.
Turning paid logs off charges nothing, so it does not ask.

Turning paid logs on needs a paid plan with a payment method, or prepaid
credit, in good standing; the platform says what to do when it refuses.
Needs billing.manage.`,
		Example: `  ankra org hosted-logs set --paid --limit 20
  ankra org hosted-logs set --paid --limit 50 --yes
  ankra org hosted-logs set --limit 30
  ankra org hosted-logs set --paid=false`,
		Args: cobra.NoArgs,
		RunE: runOrgHostedLogsSet,
	}
	command.Flags().Bool("paid", false, "Turn paid hosted logs on (--paid) or off (--paid=false)")
	command.Flags().String("limit", "", "The most hosted logs may cost in one month, in euros, e.g. 20 or 20.50")
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	registerStructuredOutputFlags(command)
	return command
}

func runOrgHostedLogsShow(command *cobra.Command, _ []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	ctx, cancel := hostedLogsContext(command)
	defer cancel()
	view, getError := apiClient.GetHostedLogsBilling(ctx)
	if getError != nil {
		return hostedLogsBillingError(getError, false)
	}
	if view == nil {
		return errors.New("reading paid hosted logs: the platform answered nothing")
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, view)
	}
	printHostedLogsBilling(command.OutOrStdout(), view)
	return nil
}

func runOrgHostedLogsSet(command *cobra.Command, _ []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	paidChanged, limitChanged := command.Flags().Changed("paid"), command.Flags().Changed("limit")
	if !paidChanged && !limitChanged {
		return withExitCode(exitUsage, errors.New("nothing to set: pass --paid --limit <euros> to turn paid hosted logs on, "+
			"--paid=false to turn them off, or --limit <euros> to change the monthly limit"))
	}
	var requestedLimit *int64
	if limitChanged {
		raw, _ := command.Flags().GetString("limit")
		cents, parseError := parseHostedLogsLimit(raw)
		if parseError != nil {
			return withExitCode(exitUsage, fmt.Errorf("--limit: %w", parseError))
		}
		requestedLimit = &cents
	}

	ctx, cancel := hostedLogsContext(command)
	defer cancel()
	current, getError := apiClient.GetHostedLogsBilling(ctx)
	if getError != nil {
		return hostedLogsBillingError(getError, false)
	}
	if current == nil {
		return errors.New("reading paid hosted logs: the platform answered nothing")
	}

	paidEnabled := current.PaidEnabled
	if paidChanged {
		paidEnabled, _ = command.Flags().GetBool("paid")
	}
	limitAfter := current.MonthlyLimitCents
	if requestedLimit != nil {
		limitAfter = requestedLimit
	}
	if paidEnabled && limitAfter == nil {
		return withExitCode(exitUsage, errors.New("turning on paid hosted logs needs a monthly limit: "+
			"add --limit <euros>, the most hosted logs may cost in one month"))
	}
	if paidEnabled == current.PaidEnabled && (requestedLimit == nil || sameCents(requestedLimit, current.MonthlyLimitCents)) {
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, current)
		}
		out := command.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Nothing changed. %s\n\n", hostedLogsPaidSentence(current.PaidEnabled, current.MonthlyLimitCents))
		printHostedLogsBilling(out, current)
		return nil
	}

	errOut := command.ErrOrStderr()
	for _, line := range hostedLogsChangeLines(current, paidEnabled, limitAfter) {
		_, _ = fmt.Fprintln(errOut, line)
	}
	if paidEnabled {
		question := fmt.Sprintf("\nTurn on paid hosted logs, up to %s a month? [y/N]: ", hostedLogsEuros(*limitAfter))
		if current.PaidEnabled {
			question = fmt.Sprintf("\nChange the monthly limit to %s? [y/N]: ", hostedLogsEuros(*limitAfter))
		}
		if confirmError := confirmHostedLogsCharge(command, question); confirmError != nil {
			return confirmError
		}
	}

	view, setError := apiClient.UpdateHostedLogsBilling(ctx, client.HostedLogsBillingUpdate{
		PaidEnabled: paidEnabled, MonthlyLimitCents: requestedLimit,
	})
	if setError != nil {
		return hostedLogsBillingError(setError, true)
	}
	if view == nil {
		return errors.New("changing paid hosted logs: the platform answered nothing")
	}
	// The route answers the setting it stored. One that disagrees with the
	// request did not take effect, so it is reported and exits non-zero.
	var disagreement error
	if view.PaidEnabled != paidEnabled {
		disagreement = withExitCode(exitError, fmt.Errorf("the platform stored paid hosted logs as %s, not %s as requested; "+
			"run 'ankra org hosted-logs show' to check it", hostedLogsOnOff(view.PaidEnabled), hostedLogsOnOff(paidEnabled)))
	}
	if format != outputDefault {
		if encodeError := encodeStructured(command.OutOrStdout(), format, view); encodeError != nil {
			return encodeError
		}
		return disagreement
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "%s\n\n", hostedLogsPaidSentence(view.PaidEnabled, view.MonthlyLimitCents))
	printHostedLogsBilling(out, view)
	return disagreement
}

// confirmHostedLogsCharge asks for the charge the change agrees to. --yes
// skips the question; without a terminal to ask on, --yes is required,
// because a script that never saw what the limit means must not agree to a
// charge by accident. The question goes to stderr so -o json|yaml stays
// parseable.
func confirmHostedLogsCharge(command *cobra.Command, question string) error {
	yes, _ := command.Flags().GetBool("yes")
	if yes {
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, errors.New("paid hosted logs are charged up to the monthly limit, so this needs "+
			"confirmation, but there is no terminal to ask on; re-run with --yes to confirm"))
	}
	if promptError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), question, false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// hostedLogsChangeLines says what a change does before it is made: with
// paid logs on, the price, the limit and how much log text it pays for; with
// them off, where sending pauses.
func hostedLogsChangeLines(current *client.HostedLogsBilling, paidEnabled bool, limitAfter *int64) []string {
	free := hostedLogsGB(current.FreeBytesPerMonth, current.BytesPerGB)
	rate := hostedLogsEuros(current.CentsPerGB)
	var lines []string
	switch {
	case paidEnabled:
		limit := hostedLogsEuros(*limitAfter)
		lines = append(lines, fmt.Sprintf("Paid hosted logs: logs above the free %s a month cost %s per GB, up to %s a month.", free, rate, limit))
		if current.CentsPerGB > 0 {
			lines = append(lines, fmt.Sprintf("%s pays for up to %s of log text above the free %s. At %s, sending pauses for the rest of the month.",
				limit, hostedLogsGB(*limitAfter/current.CentsPerGB*current.BytesPerGB, current.BytesPerGB), free, limit))
		}
		if current.PaidEnabled && !sameCents(current.MonthlyLimitCents, limitAfter) {
			lines = append(lines, fmt.Sprintf("Monthly limit: %s -> %s.", hostedLogsLimitLabel(current.MonthlyLimitCents), limit))
		}
		lines = append(lines, "Owners are told at 80% and 100% of the limit.")
		if !current.Month.Priced {
			lines = append(lines, fmt.Sprintf("Nothing is charged until %s: hosted logs are included until %s.",
				hostedLogsDateLabel(current.PricedFrom), hostedLogsIncludedUntilLabel(current.PricedFrom)))
		}
		if !current.PaidEnabled && current.PaidReadiness != client.HostedLogsPaidReady && current.PaidBlocker != nil &&
			strings.TrimSpace(*current.PaidBlocker) != "" {
			lines = append(lines, "Billing: "+strings.TrimSpace(*current.PaidBlocker))
		}
	case current.PaidEnabled:
		lines = append(lines, fmt.Sprintf("Paid hosted logs off: at the free %s a month, sending pauses for the rest of the month. "+
			"What this month already charged stays charged.", free))
		if current.Month.Priced && current.Month.Bytes >= current.FreeBytesPerMonth {
			lines = append(lines, fmt.Sprintf("This month already used the free %s, so sending pauses at once.", free))
		}
	default:
		lines = append(lines, fmt.Sprintf("Monthly limit: %s -> %s. Paid hosted logs stay off; the limit applies once they are turned on.",
			hostedLogsLimitLabel(current.MonthlyLimitCents), hostedLogsLimitLabel(limitAfter)))
	}
	return lines
}

// printHostedLogsBilling renders the setting and the month for a person.
func printHostedLogsBilling(out io.Writer, view *client.HostedLogsBilling) {
	month := view.Month
	free := hostedLogsGB(view.FreeBytesPerMonth, view.BytesPerGB)
	_, _ = fmt.Fprintf(out, "Hosted logs for %s (UTC):\n\n", hostedLogsMonthLabel(month.MonthStart))
	row := func(label string, value string) {
		_, _ = fmt.Fprintf(out, "  %-15s %s\n", label+":", value)
	}
	row("Log text", hostedLogsUsageLabel(month.Bytes, view.FreeBytesPerMonth, view.BytesPerGB))
	row("Sending", hostedLogsSendingLabel(view))
	row("Amount so far", hostedLogsAmountLabel(view))
	switch {
	case view.PaidEnabled && view.MonthlyLimitCents != nil:
		row("Paid logs", fmt.Sprintf("on, up to %s a month", hostedLogsEuros(*view.MonthlyLimitCents)))
	case view.PaidEnabled:
		row("Paid logs", "on")
	default:
		if month.Priced {
			row("Paid logs", fmt.Sprintf("off: at the free %s a month, sending pauses until the month ends", free))
		} else {
			row("Paid logs", "off")
		}
		row("Monthly limit", hostedLogsLimitLabel(view.MonthlyLimitCents))
		row("Turning on", hostedLogsReadinessLabel(view))
	}
	row("Last changed", hostedLogsChangedLabel(view.UpdatedAt))

	_, _ = fmt.Fprintln(out)
	price := fmt.Sprintf("%s of log text a month free per organisation, then %s per GB, kept 7 days.", free, hostedLogsEuros(view.CentsPerGB))
	if month.Priced {
		_, _ = fmt.Fprintln(out, "Price: "+price)
	} else {
		_, _ = fmt.Fprintf(out, "Hosted logs are included until %s, so nothing is charged until %s.\n",
			hostedLogsIncludedUntilLabel(view.PricedFrom), hostedLogsDateLabel(view.PricedFrom))
		_, _ = fmt.Fprintf(out, "From %s: %s\n", hostedLogsDateLabel(view.PricedFrom), price)
	}
	if !view.PaidEnabled {
		_, _ = fmt.Fprintf(out, "At the free %s, sending pauses for the rest of the month unless an owner turns on paid logs with a monthly limit.\n", free)
	}
	_, _ = fmt.Fprintf(out, "Owners are told at 80%% and 100%% of the free %s, and of the monthly limit while paid logs are on.\n", free)
	if remedy := hostedLogsPauseRemedy(month.PausedReason); remedy != "" {
		_, _ = fmt.Fprintln(out, remedy)
	} else if !view.PaidEnabled {
		_, _ = fmt.Fprintln(out, "Turn on paid logs with 'ankra org hosted-logs set --paid --limit <euros>'.")
	}
}

// hostedLogsUsageLabel is the month's log text against the free amount.
func hostedLogsUsageLabel(bytes int64, free int64, bytesPerGB int64) string {
	freeLabel := hostedLogsGB(free, bytesPerGB)
	if free <= 0 {
		return hostedLogsGB(bytes, bytesPerGB)
	}
	if bytes > free {
		return fmt.Sprintf("%s: the free %s and %s above it", hostedLogsGB(bytes, bytesPerGB), freeLabel, hostedLogsGB(bytes-free, bytesPerGB))
	}
	return fmt.Sprintf("%s of the free %s (%d%%)", hostedLogsGB(bytes, bytesPerGB), freeLabel, bytes*100/free)
}

// hostedLogsSendingLabel says whether sending is paused for the month and
// why, in plain words.
func hostedLogsSendingLabel(view *client.HostedLogsBilling) string {
	month := view.Month
	if month.PausedReason == nil && (month.State == "" || month.State == client.HostedLogsStateShipping) {
		return "not paused"
	}
	until := hostedLogsDateLabel(hostedLogsNextMonth(month.MonthStart))
	switch {
	case month.State == client.HostedLogsStatePausedFree || hostedLogsReasonIs(month.PausedReason, client.HostedLogsPausedFreeAllowanceUsed):
		return fmt.Sprintf("paused until %s: the free %s of this month is used and paid logs are off",
			until, hostedLogsGB(view.FreeBytesPerMonth, view.BytesPerGB))
	case month.State == client.HostedLogsStatePausedLimit || hostedLogsReasonIs(month.PausedReason, client.HostedLogsPausedMonthlyLimitReached):
		return fmt.Sprintf("paused until %s: this month reached the monthly limit of %s", until, hostedLogsLimitLabel(view.MonthlyLimitCents))
	case month.State == client.HostedLogsStatePausedCredit || hostedLogsReasonIs(month.PausedReason, client.HostedLogsPausedNoCredit):
		return fmt.Sprintf("paused: the organisation has no prepaid credit to pay for logs above the free %s",
			hostedLogsGB(view.FreeBytesPerMonth, view.BytesPerGB))
	}
	if month.PausedReason != nil {
		return "paused (" + *month.PausedReason + ")"
	}
	return "paused (" + month.State + ")"
}

// hostedLogsPauseReasonSentence is a pause reason in plain words, shared by
// `org hosted-logs` and `cluster logs-ship`, so the two never describe the
// same pause two ways. An unknown reason is shown as the platform sent it.
func hostedLogsPauseReasonSentence(reason string) string {
	switch reason {
	case client.HostedLogsPausedFreeAllowanceUsed:
		return "your organisation used its free 5 GB of hosted logs this month and paid logs are off"
	case client.HostedLogsPausedMonthlyLimitReached:
		return "your organisation's hosted logs reached the monthly limit set for paid logs"
	case client.HostedLogsPausedNoCredit:
		return "your organisation has no prepaid credit to pay for hosted logs above the free 5 GB"
	default:
		return "the platform paused them (" + reason + ")"
	}
}

// hostedLogsPauseRemedy says what ends a pause before the month does; empty
// while nothing pauses sending.
func hostedLogsPauseRemedy(reason *string) string {
	if reason == nil {
		return ""
	}
	switch *reason {
	case client.HostedLogsPausedFreeAllowanceUsed:
		return "To keep sending this month, an owner can turn on paid logs: 'ankra org hosted-logs set --paid --limit <euros>'."
	case client.HostedLogsPausedMonthlyLimitReached:
		return "To keep sending this month, an owner can raise the limit: 'ankra org hosted-logs set --limit <euros>'."
	case client.HostedLogsPausedNoCredit:
		return "Sending resumes once the prepaid credit is topped up on the Billing page; 'ankra org billing wallet' shows the balance."
	default:
		return "Sending resumes when the month ends; 'ankra org hosted-logs show' says more."
	}
}

func hostedLogsReasonIs(reason *string, want string) bool {
	return reason != nil && *reason == want
}

// hostedLogsAmountLabel is what the month comes to so far.
func hostedLogsAmountLabel(view *client.HostedLogsBilling) string {
	switch {
	case !view.Month.Priced:
		return fmt.Sprintf("nothing: hosted logs are included until %s", hostedLogsIncludedUntilLabel(view.PricedFrom))
	case view.Month.AmountCents == 0 && !view.PaidEnabled:
		return "nothing: paid logs are off, so nothing is charged"
	}
	return hostedLogsEuros(view.Month.AmountCents)
}

// hostedLogsReadinessLabel says whether paid logs can be turned on now.
func hostedLogsReadinessLabel(view *client.HostedLogsBilling) string {
	blocker := ""
	if view.PaidBlocker != nil {
		blocker = strings.TrimSpace(*view.PaidBlocker)
	}
	switch view.PaidReadiness {
	case client.HostedLogsPaidReady:
		return "possible now"
	case client.HostedLogsPaidBlocked:
		if blocker == "" {
			return "not possible now"
		}
		return "not possible now: " + blocker
	case client.HostedLogsPaidUnknown:
		if blocker == "" {
			return "not known: Ankra could not check the billing account just now"
		}
		return "not known: " + blocker
	case "":
		return "not reported"
	default:
		return view.PaidReadiness
	}
}

// hostedLogsPaidSentence is the setting in one sentence.
func hostedLogsPaidSentence(paidEnabled bool, limitCents *int64) string {
	if paidEnabled {
		return fmt.Sprintf("Paid hosted logs are on, up to %s a month.", hostedLogsLimitLabel(limitCents))
	}
	if limitCents != nil {
		return fmt.Sprintf("Paid hosted logs are off (monthly limit %s stored).", hostedLogsEuros(*limitCents))
	}
	return "Paid hosted logs are off."
}

func hostedLogsOnOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func hostedLogsLimitLabel(limitCents *int64) string {
	if limitCents == nil {
		return "none set"
	}
	return hostedLogsEuros(*limitCents)
}

// hostedLogsEuros writes euro cents as "EUR 1,234.56".
func hostedLogsEuros(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("EUR %s%s.%02d", sign, usageThousands(cents/100), cents%100)
}

// hostedLogsGB writes bytes in the decimal GB the price is quoted in: a whole
// number of GB as "5 GB", anything else scaled with one decimal.
func hostedLogsGB(bytes int64, bytesPerGB int64) string {
	if bytesPerGB > 0 && bytes > 0 && bytes%bytesPerGB == 0 {
		return usageThousands(bytes/bytesPerGB) + " GB"
	}
	return usageScaledBytes(bytes, "B")
}

// hostedLogsDateLabel writes a YYYY-MM-DD date as "1 January 2027"; anything
// else is shown as the platform sent it.
func hostedLogsDateLabel(date string) string {
	parsed, parseError := time.Parse(time.DateOnly, date)
	if parseError != nil {
		return date
	}
	return parsed.Format("2 January 2006")
}

// hostedLogsIncludedUntilLabel is the last day hosted logs are included: the
// day before the priced-from date.
func hostedLogsIncludedUntilLabel(pricedFrom string) string {
	parsed, parseError := time.Parse(time.DateOnly, pricedFrom)
	if parseError != nil {
		return "the day before " + pricedFrom
	}
	return parsed.AddDate(0, 0, -1).Format("2 January 2006")
}

func hostedLogsMonthLabel(monthStart string) string {
	parsed, parseError := time.Parse(time.DateOnly, monthStart)
	if parseError != nil {
		return monthStart
	}
	return parsed.Format("January 2006")
}

// hostedLogsNextMonth is the first day of the month after monthStart, as
// YYYY-MM-DD; an unreadable month start is passed through.
func hostedLogsNextMonth(monthStart string) string {
	parsed, parseError := time.Parse(time.DateOnly, monthStart)
	if parseError != nil {
		return "the end of the month"
	}
	return parsed.AddDate(0, 1, 0).Format(time.DateOnly)
}

// parseHostedLogsLimit reads --limit: euros, with up to two decimals, more
// than zero and at most EUR 1,000,000.
func parseHostedLogsLimit(raw string) (int64, error) {
	text := strings.TrimSpace(raw)
	match := hostedLogsLimitPattern.FindStringSubmatch(text)
	if match == nil {
		return 0, fmt.Errorf("expected an amount in euros such as 20 or 20.50, got %q", raw)
	}
	if len(match[1]) > 7 {
		return 0, fmt.Errorf("the monthly limit can be at most %s, got %s", hostedLogsEuros(hostedLogsMaxLimitCents), text)
	}
	euros, _ := strconv.ParseInt(match[1], 10, 64)
	cents := euros * 100
	if match[2] != "" {
		fraction := match[2]
		if len(fraction) == 1 {
			fraction += "0"
		}
		fractionCents, _ := strconv.ParseInt(fraction, 10, 64)
		cents += fractionCents
	}
	if cents <= 0 {
		return 0, errors.New("the monthly limit must be more than EUR 0.00")
	}
	if cents > hostedLogsMaxLimitCents {
		return 0, fmt.Errorf("the monthly limit can be at most %s, got %s", hostedLogsEuros(hostedLogsMaxLimitCents), hostedLogsEuros(cents))
	}
	return cents, nil
}

func sameCents(left *int64, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// hostedLogsBillingError maps a refusal from the paid hosted logs routes onto
// what the user can act on. A 403 is a missing billing.read (reading) or
// billing.manage (writing) and exits 7. The platform's refusals of a write
// are printed as it wrote them, because each names its remedy: 402 (billing
// cannot pay for paid logs yet), 503 (billing could not be checked; nothing
// changed) and 400 (a limit it will not store, exit 2). A 404 without the
// platform's own detail is a platform that predates paid hosted logs.
func hostedLogsBillingError(apiError error, writing bool) error {
	operation := "reading paid hosted logs"
	if writing {
		operation = "changing paid hosted logs"
	}
	var denied *client.PermissionDeniedError
	var unexpected *client.UnexpectedResponseError
	hasStatus := errors.As(apiError, &unexpected)
	if errors.As(apiError, &denied) || (hasStatus && unexpected.StatusCode == http.StatusForbidden) {
		permission := "billing.read"
		if writing {
			permission = "billing.manage"
		}
		return withExitCode(exitForbidden, fmt.Errorf("%s needs the %s permission; ask an organisation owner for a role that has it",
			operation, permission))
	}
	if !hasStatus {
		return fmt.Errorf("%s: %w", operation, apiError)
	}
	switch unexpected.StatusCode {
	case http.StatusNotFound:
		if unexpected.Detail == "" || unexpected.Detail == routeAbsentDetail {
			return withExitCode(exitError, fmt.Errorf("%s: this Ankra platform does not offer paid hosted logs yet "+
				"(the route answered 404); hosted logs are included until it does", operation))
		}
	case http.StatusPaymentRequired, http.StatusServiceUnavailable:
		if unexpected.Detail != "" {
			return withExitCode(exitError, errors.New(unexpected.Detail))
		}
	case http.StatusBadRequest:
		if unexpected.Detail != "" {
			return withExitCode(exitUsage, errors.New(unexpected.Detail))
		}
	}
	return fmt.Errorf("%s: %w", operation, apiError)
}

func init() {
	orgCmd.AddCommand(newOrgHostedLogsCommand())
}

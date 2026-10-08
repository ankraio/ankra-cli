package cmd

// `ankra org usage` (epic ankra-t5jf5.34.12, decision D4): everything the
// organisation used of Ankra over a window, per meter and period, from GET
// /api/v1/org/billing/usage.
//
// Usage is tracked now and charged later. A line carries its quantity, how
// completely it was measured, and its price state: most meters are included
// during the pilot (no published rate, nothing charged), the plan's
// vCPU-hours and ordered playgrounds are priced as the invoice prices them,
// and a line no rate applies to says why. A period nobody measured is shown
// as not measured, never as 0, and stays null in -o json.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// usageNow is the clock --period reads; tests pin it to a known day.
var usageNow = time.Now

const (
	usagePeriodThisMonth = "this-month"
	usagePeriodLastMonth = "last-month"
	usageDateLayout      = "2006-01-02"
)

var (
	usageGranularities = []string{"month", "day"}
	usageGroupings     = []string{"meter", "cluster", "application", "instance"}
)

func newOrgUsageCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "usage",
		Short: "Show what the organisation used of Ankra: vCPU-hours, playgrounds, managed services, hosted logs and metrics, AI",
		Long: `Show everything the organisation used of Ankra over a window of days, per
meter and period: worker vCPU-hours, ordered playground time, managed service
instance time and storage, hosted logs and metrics accepted, and AI requests
and tokens.

The window defaults to the current month to date. --period picks this month
or last month; --from and --to (exclusive) pick any stretch of UTC days up to
93 days long, within the 400 days usage is kept. --granularity splits it
into calendar months or days, and --group-by splits each meter by cluster,
by the application a managed service serves, or by managed service instance.

Each line says how completely it was measured. A quantity is exact when
every hour of the period was measured, a lower bound (shown as >=) when
some were not or are still being counted, and 'not measured' when none was,
for instance a service whose cluster was offline throughout. Not measured is
never 0; in -o json the quantity is null.

Each line also says what it costs. 'included (pilot)' means Ankra publishes
no rate and charges nothing for it: managed services and hosted logs and
metrics during the pilot. A priced line shows the amount in EUR the
published rate makes of it, as the invoice will. A line no rate applies to
says why, for instance that AI usage has no per-unit rate.

Grouped by application, a service that serves several applications is
counted under each of them (marked shared), so application lines do not add
up to the organisation's total. Grouped by meter, every meter answers every
period; grouped otherwise, a group that used none of a meter in a fully
measured period has no line.

-o table (the default) shows quantities in units a person reads (GB, hours,
thousands separators). -o json and -o yaml carry the platform's own report:
raw quantities with their unit, and null where nothing was measured.

Needs the billing.read permission.`,
		Example: `  ankra org usage
  ankra org usage --period last-month
  ankra org usage --from 2026-09-01 --to 2026-10-01 --granularity day
  ankra org usage --group-by cluster
  ankra org usage --group-by application -o json`,
		Args: cobra.NoArgs,
		RunE: runOrgUsage,
	}
	command.Flags().String("from", "", "First UTC day to read, YYYY-MM-DD (default: the first day of this month)")
	command.Flags().String("to", "", "The day after the last UTC day to read, YYYY-MM-DD (default: tomorrow, so today is included)")
	command.Flags().String("period", "", "A ready-made window: this-month or last-month (instead of --from/--to)")
	command.Flags().String("granularity", "month", "Period length of each line: month or day")
	command.Flags().String("group-by", "meter", "Split each meter by: meter, cluster, application or instance")
	command.Flags().StringP("output", "o", usageOutputTable, "Output format: table, json or yaml")
	return command
}

// usageOutputTable is the human rendering's name for -o. The command takes
// table, json or yaml, as the decided command design names it; table is the
// default every other structured command leaves unnamed.
const usageOutputTable = "table"

func runOrgUsage(command *cobra.Command, _ []string) error {
	format, formatError := usageOutputFormat(command)
	if formatError != nil {
		return formatError
	}
	options, optionsError := usageOptionsFromFlags(command)
	if optionsError != nil {
		return optionsError
	}
	report, readError := apiClient.GetOrganisationUsage(command.Context(), options)
	if readError != nil {
		return usageReadError(readError)
	}
	if format != outputDefault {
		_, encodeError := encodeStructuredCounting(command.OutOrStdout(), format, report, command.ErrOrStderr())
		return encodeError
	}
	var applicationNames map[string]string
	if report.GroupBy == "application" {
		applicationNames = usageApplicationNames(command.Context(), report, command.ErrOrStderr())
	}
	printUsageReport(command.OutOrStdout(), report, applicationNames)
	return nil
}

// usageOutputFormat reads -o: table (the default) is the human rendering,
// json and yaml the platform's document.
func usageOutputFormat(command *cobra.Command) (outputFormat, error) {
	raw, _ := command.Flags().GetString("output")
	if strings.EqualFold(strings.TrimSpace(raw), usageOutputTable) {
		return outputDefault, nil
	}
	format, parseError := parseOutputFormat(raw)
	if parseError != nil {
		return outputDefault, withExitCode(exitUsage, fmt.Errorf("invalid -o format %q (expected table, json or yaml)", raw))
	}
	return format, nil
}

// usageReadError classifies a failed read. A refused query parameter names
// the flag that set it (exit 2). A 404 can only mean the route is missing:
// the usage read has no resource to miss, so exiting 3 ("not found") would
// tell a script the organisation has no usage, when it is the platform that
// predates the read.
func usageReadError(readError error) error {
	var refused *client.UsageQueryRefusedError
	if errors.As(readError, &refused) {
		return withExitCode(exitUsage, usageRefusalError(refused))
	}
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) && unexpected.StatusCode == http.StatusNotFound {
		return withExitCode(exitError, errors.New(
			"this platform does not serve the usage read: GET /api/v1/org/billing/usage is not registered, "+
				"so this platform predates it"))
	}
	return fmt.Errorf("reading usage: %w", readError)
}

// usageOptionsFromFlags validates the window and shape flags. Every
// invocation mistake is exitUsage with the flag named, so a script fixes the
// call rather than retrying it.
func usageOptionsFromFlags(command *cobra.Command) (client.UsageOptions, error) {
	flags := command.Flags()
	from, _ := flags.GetString("from")
	to, _ := flags.GetString("to")
	period, _ := flags.GetString("period")
	granularity, _ := flags.GetString("granularity")
	groupBy, _ := flags.GetString("group-by")
	from, to, period = strings.TrimSpace(from), strings.TrimSpace(to), strings.TrimSpace(period)

	options := client.UsageOptions{Granularity: strings.ToLower(strings.TrimSpace(granularity)),
		GroupBy: strings.ToLower(strings.TrimSpace(groupBy))}
	if !containsString(usageGranularities, options.Granularity) {
		return options, withExitCode(exitUsage, fmt.Errorf("--granularity must be one of %s, not %q",
			strings.Join(usageGranularities, ", "), granularity))
	}
	if !containsString(usageGroupings, options.GroupBy) {
		return options, withExitCode(exitUsage, fmt.Errorf("--group-by must be one of %s, not %q",
			strings.Join(usageGroupings, ", "), groupBy))
	}

	if period != "" {
		if from != "" || to != "" {
			return options, withExitCode(exitUsage, errors.New("--period cannot be combined with --from or --to"))
		}
		periodFrom, periodTo, periodError := usagePeriodWindow(strings.ToLower(period), usageNow().UTC())
		if periodError != nil {
			return options, periodError
		}
		options.From, options.To = periodFrom, periodTo
		return options, nil
	}

	var fromDay, toDay time.Time
	if from != "" {
		parsed, parseError := time.Parse(usageDateLayout, from)
		if parseError != nil {
			return options, withExitCode(exitUsage, fmt.Errorf("--from must be a UTC day as YYYY-MM-DD, not %q", from))
		}
		fromDay, options.From = parsed, from
	}
	if to != "" {
		parsed, parseError := time.Parse(usageDateLayout, to)
		if parseError != nil {
			return options, withExitCode(exitUsage, fmt.Errorf("--to must be a UTC day as YYYY-MM-DD, not %q", to))
		}
		toDay, options.To = parsed, to
	}
	if from != "" && to != "" && !toDay.After(fromDay) {
		return options, withExitCode(exitUsage, fmt.Errorf("--to (%s) must be after --from (%s): it is the day after the last day read", to, from))
	}
	return options, nil
}

// usagePeriodWindow turns a named period into a from/to pair of UTC days.
// This month is month to date: from the first of the month, to left to the
// platform's default (today). Last month is the whole previous calendar
// month.
func usagePeriodWindow(period string, now time.Time) (string, string, error) {
	firstOfThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	switch period {
	case usagePeriodThisMonth:
		return firstOfThisMonth.Format(usageDateLayout), "", nil
	case usagePeriodLastMonth:
		firstOfLastMonth := firstOfThisMonth.AddDate(0, -1, 0)
		return firstOfLastMonth.Format(usageDateLayout), firstOfThisMonth.Format(usageDateLayout), nil
	default:
		return "", "", withExitCode(exitUsage, fmt.Errorf("--period must be %s or %s, not %q",
			usagePeriodThisMonth, usagePeriodLastMonth, period))
	}
}

// usageRefusalError maps the platform's refused query parameters back to
// the flags that set them.
func usageRefusalError(refused *client.UsageQueryRefusedError) error {
	flagForParameter := map[string]string{"from": "--from", "to": "--to", "granularity": "--granularity", "group_by": "--group-by"}
	parts := make([]string, 0, len(refused.Refusals))
	for _, refusal := range refused.Refusals {
		if flag, known := flagForParameter[refusal.Parameter]; known {
			parts = append(parts, flag+": "+refusal.Message)
		} else if refusal.Parameter != "" {
			parts = append(parts, refusal.Parameter+": "+refusal.Message)
		} else {
			parts = append(parts, refusal.Message)
		}
	}
	return errors.New("the platform refused the usage query: " + strings.Join(parts, "; "))
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// printUsageReport renders the human table: the window and shape on top,
// one row per line, and below it what the markers in the table mean.
// applicationNames maps application ids to names for the application
// grouping; an id it does not hold is shown as the id.
func printUsageReport(out io.Writer, report *client.UsageReport, applicationNames map[string]string) {
	_, _ = fmt.Fprintf(out, "Usage %s (%s periods, by %s)\n", usageWindowLabel(report.From, report.To), report.Granularity, report.GroupBy)
	if len(report.Lines) == 0 {
		_, _ = fmt.Fprintln(out, "No usage lines for this window.")
		return
	}

	usageTable := table.NewWriter()
	usageTable.SetOutputMirror(out)
	usageTable.SetStyle(table.StyleRounded)
	// The platform attributes an instance line to its instance only, not to
	// a cluster, so the instance grouping has no cluster column.
	header := table.Row{"Meter", "Period"}
	switch report.GroupBy {
	case "cluster":
		header = append(header, "Cluster")
	case "application":
		header = append(header, "Application", "Shared")
	case "instance":
		header = append(header, "Instance")
	}
	header = append(header, "Quantity", "Price")
	usageTable.AppendHeader(header)

	groupLabels := usageGroupLabels(report, applicationNames)
	seenUnknown, seenPartial, seenIncluded := false, false, false
	for _, line := range report.Lines {
		row := table.Row{usageMeterLabel(line.Meter), usagePeriodLabel(line, report.Granularity)}
		switch report.GroupBy {
		case "cluster":
			row = append(row, usageGroupLabel(line.ClusterID, groupLabels, "(organisation)"))
		case "application":
			shared := ""
			if line.IsShared {
				shared = "yes"
			}
			row = append(row, usageApplicationLabel(line, groupLabels), shared)
		case "instance":
			row = append(row, usageGroupLabel(line.InstanceID, groupLabels, "(organisation)"))
		}
		row = append(row, usageQuantityLabel(line), usagePriceLabel(line))
		usageTable.AppendRow(row)
		switch {
		case line.Quantity == nil || line.Coverage == client.UsageCoverageUnknown:
			seenUnknown = true
		case line.Coverage == client.UsageCoveragePartial:
			seenPartial = true
		}
		if line.PriceState == client.UsagePriceIncluded {
			seenIncluded = true
		}
	}
	usageTable.Render()

	if seenUnknown {
		_, _ = fmt.Fprintln(out, "not measured: no hour of that period was measured, for instance while the cluster was offline. It is not zero.")
	}
	if seenPartial {
		_, _ = fmt.Fprintln(out, ">=: only part of that period was measured, or it is still being counted; the quantity is a lower bound.")
	}
	if seenIncluded {
		_, _ = fmt.Fprintln(out, "included (pilot): Ankra publishes no rate and charges nothing for it during the pilot.")
	}
	if report.GroupBy == "application" {
		_, _ = fmt.Fprintln(out, "A service that serves several applications is counted under each of them (Shared: yes), so application lines do not add up to the organisation's total.")
	}
}

// usageWindowLabel names the window by the days it covers. The platform's
// end is exclusive: midnight after the last day read, or the time of the
// request when that is earlier (usage that has not happened is not
// reported). A midnight end is shown as the last day read, through it; any
// other as the instant the window stops at. Anything that is not an RFC 3339
// instant is shown as the platform sent it.
func usageWindowLabel(from string, to string) string {
	fromLabel := from
	if parsed, parseError := time.Parse(time.RFC3339, from); parseError == nil {
		fromLabel = parsed.UTC().Format(usageDateLayout)
	}
	parsedTo, parseError := time.Parse(time.RFC3339, to)
	if parseError != nil {
		return fmt.Sprintf("from %s to %s", fromLabel, to)
	}
	parsedTo = parsedTo.UTC()
	if parsedTo.Equal(parsedTo.Truncate(24 * time.Hour)) {
		return fmt.Sprintf("from %s through %s", fromLabel, parsedTo.AddDate(0, 0, -1).Format(usageDateLayout))
	}
	return fmt.Sprintf("from %s to %s UTC", fromLabel, parsedTo.Format("2006-01-02 15:04"))
}

// usageMeterLabels names each meter the way the billing page does. A meter
// this build does not know is shown by its platform name, never dropped.
var usageMeterLabels = map[string]string{
	"worker_vcpu_hours":            "Worker vCPUs",
	"playground_seconds":           "Playgrounds",
	"service_instance_seconds":     "Managed service time",
	"service_storage_byte_hours":   "Managed service storage",
	"hosted_logs_ingested_bytes":   "Hosted logs (bytes)",
	"hosted_logs_ingested_lines":   "Hosted logs (lines)",
	"hosted_metrics_samples":       "Hosted metrics (samples)",
	"hosted_metrics_active_series": "Hosted metrics (active series)",
	"ai_requests":                  "AI requests",
	"ai_input_tokens":              "AI input tokens",
	"ai_output_tokens":             "AI output tokens",
}

func usageMeterLabel(meter string) string {
	if label, known := usageMeterLabels[meter]; known {
		return label
	}
	return meter
}

// usagePeriodLabel names a line's period: the calendar month, or the UTC
// day, its period starts in.
func usagePeriodLabel(line client.UsageLine, granularity string) string {
	parsed, parseError := time.Parse(time.RFC3339, line.PeriodStart)
	if parseError != nil {
		return line.PeriodStart
	}
	if granularity == "day" {
		return parsed.UTC().Format(usageDateLayout)
	}
	return parsed.UTC().Format("2006-01")
}

// usageGroupLabels names each group of the report by id: a cluster or
// instance by the name its lines carry, an application by the name the
// lookup found, and any of them by its id when it has no name. A name two
// groups share is followed by the start of each one's id, so their lines
// stay apart: released playground clusters all read "playground", and a
// service's name is free for a new service once the old one is retired.
func usageGroupLabels(report *client.UsageReport, applicationNames map[string]string) map[string]string {
	names := map[string]string{}
	remember := func(id *string, name string) {
		if id == nil {
			return
		}
		if name == "" {
			name = *id
		}
		names[*id] = name
	}
	for _, line := range report.Lines {
		switch report.GroupBy {
		case "cluster":
			remember(line.ClusterID, valueOr(line.ClusterName, ""))
		case "instance":
			remember(line.InstanceID, valueOr(line.InstanceName, ""))
		case "application":
			if line.ApplicationID != nil {
				remember(line.ApplicationID, applicationNames[*line.ApplicationID])
			}
		}
	}
	idsByName := map[string]int{}
	for _, name := range names {
		idsByName[name]++
	}
	labels := make(map[string]string, len(names))
	for id, name := range names {
		if idsByName[name] > 1 && name != id {
			name = fmt.Sprintf("%s (%s)", name, shortUsageID(id))
		}
		labels[id] = name
	}
	return labels
}

// shortUsageID is enough of an id to tell two groups of one name apart.
func shortUsageID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// usageGroupLabel names a line's group. A line the platform does not
// attribute to the grouping (AI usage has no cluster; a vCPU-hour has no
// instance) keeps one organisation line, and says so rather than showing a
// blank.
func usageGroupLabel(id *string, labels map[string]string, none string) string {
	if id == nil {
		return none
	}
	if label := labels[*id]; label != "" {
		return label
	}
	return *id
}

// usageApplicationLabel names a line's application. A managed service line
// with none is usage of a service that serves no application; any other
// meter is not attributed to applications at all.
func usageApplicationLabel(line client.UsageLine, labels map[string]string) string {
	none := "(organisation)"
	if strings.HasPrefix(line.Meter, "service_") {
		none = "(no application)"
	}
	return usageGroupLabel(line.ApplicationID, labels, none)
}

// usageApplicationNamePages bounds the application name lookup.
const usageApplicationNamePages = 20

// usageApplicationNames reads the organisation's application names for the
// application grouping, whose lines carry ids only. The lookup is best
// effort: a name it cannot read is shown as its id, and a lookup that failed
// or stopped short says so once on notes, so an id shown for want of a name
// is not read as an application that is gone.
func usageApplicationNames(ctx context.Context, report *client.UsageReport, notes io.Writer) map[string]string {
	wanted := map[string]bool{}
	for _, line := range report.Lines {
		if line.ApplicationID != nil {
			wanted[*line.ApplicationID] = true
		}
	}
	names := map[string]string{}
	if len(wanted) == 0 {
		return names
	}
	for page := 1; page <= usageApplicationNamePages; page++ {
		payload, listError := apiClient.ListApplicationsRaw(ctx, page, maxApplicationLookupPageSize, "")
		var listing applicationListingPage
		if listError == nil {
			listError = json.Unmarshal(payload, &listing)
		}
		if listError != nil {
			_, _ = fmt.Fprintf(notes, "Note: application names could not be read (%v); applications are shown by id.\n", listError)
			return names
		}
		for _, application := range listing.Result {
			if wanted[application.ID] {
				names[application.ID] = application.Name
			}
		}
		if len(names) == len(wanted) || listing.Pagination.TotalPages <= page || len(listing.Result) == 0 {
			return names
		}
	}
	_, _ = fmt.Fprintf(notes, "Note: only the first %d applications were read for names; others are shown by id.\n",
		usageApplicationNamePages*maxApplicationLookupPageSize)
	return names
}

// usageQuantityLabel renders the quantity by its coverage: 'not measured'
// when nothing was (never 0), a lower bound when part was, and the exact
// quantity in human units otherwise.
func usageQuantityLabel(line client.UsageLine) string {
	if line.Quantity == nil || line.Coverage == client.UsageCoverageUnknown {
		return "not measured"
	}
	quantity := usageHumanQuantity(*line.Quantity, line.Unit)
	if line.Coverage == client.UsageCoveragePartial {
		return ">= " + quantity
	}
	return quantity
}

// usageHumanQuantity shows a quantity in units a person reads: bytes as
// KB/MB/GB, seconds as hours, byte-hours as GB-hours, and counts with
// thousands separators. -o json keeps the raw value and unit.
func usageHumanQuantity(quantity int64, unit string) string {
	switch unit {
	case "bytes":
		return usageScaledBytes(quantity, "B")
	case "byte_hours":
		return usageScaledBytes(quantity, "B-hours")
	case "seconds":
		return usageDuration(quantity)
	case "vcpu_hours":
		return usageThousands(quantity) + " vCPU-hours"
	case "series_hours":
		return usageThousands(quantity) + " series-hours"
	case "":
		return usageThousands(quantity)
	default:
		return usageThousands(quantity) + " " + unit
	}
}

// usageDuration shows seconds as hours with one decimal. Under an hour it
// shows whole minutes, and under a minute seconds, so a short stretch is
// never rounded to a 0.0 that reads as nothing used.
func usageDuration(seconds int64) string {
	switch {
	case seconds == 0:
		return "0 hours"
	case seconds < 60:
		return usagePlural(seconds, "second")
	case seconds < 3600:
		return usagePlural(seconds/60, "minute")
	default:
		return usageOneDecimal(float64(seconds)/3600) + " hours"
	}
}

func usagePlural(count int64, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return usageThousands(count) + " " + noun + "s"
}

// usageOneDecimal writes a value with one decimal and thousands separators
// in its whole part: 8760.04 reads 8,760.0.
func usageOneDecimal(value float64) string {
	tenths := int64(value*10 + 0.5)
	return fmt.Sprintf("%s.%d", usageThousands(tenths/10), tenths%10)
}

// usageScaledBytes scales a byte count to the largest decimal unit under
// which it reads as at least one, with one decimal: 1,500,000 bytes reads
// 1.5 MB. The suffix is the unit's tail ("B" or "B-hours").
func usageScaledBytes(quantity int64, suffix string) string {
	const step = 1000
	if quantity < step {
		return usageThousands(quantity) + " " + suffix
	}
	value := float64(quantity)
	prefixes := []string{"K", "M", "G", "T", "P"}
	index := -1
	// Scale on the value as it will print, so 999,960 bytes reads 1.0 MB
	// rather than 1000.0 KB.
	for float64(int64(value*10+0.5))/10 >= step && index < len(prefixes)-1 {
		value /= step
		index++
	}
	return usageOneDecimal(value) + " " + prefixes[index] + suffix
}

// usageThousands writes an integer with thousands separators.
func usageThousands(quantity int64) string {
	digits := strconv.FormatInt(quantity, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var grouped strings.Builder
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(digit)
	}
	return sign + grouped.String()
}

// usagePriceLabel renders the price state: included during the pilot, the
// amount a published rate makes of the quantity, or why no rate applies.
func usagePriceLabel(line client.UsageLine) string {
	switch line.PriceState {
	case client.UsagePriceIncluded:
		return "included (pilot)"
	case client.UsagePricePriced:
		if line.AmountMinor == nil {
			return "priced, amount not reported"
		}
		return usageAmount(*line.AmountMinor, line.Currency)
	case client.UsagePriceUnpriced:
		return usagePriceBasisReason(line.PriceBasis)
	default:
		if line.PriceState == "" {
			return "price state not reported"
		}
		return line.PriceState
	}
}

// usageAmount shows minor units as a currency amount: euro cents as €x.xx,
// any other currency with its code after the amount.
// A negative amount (a credit) keeps its sign in front, whatever its size:
// -5 cents reads -€0.05, not €0.05.
func usageAmount(amountMinor int64, currency *string) string {
	sign := ""
	if amountMinor < 0 {
		sign, amountMinor = "-", -amountMinor
	}
	amount := fmt.Sprintf("%s.%02d", usageThousands(amountMinor/100), amountMinor%100)
	if currency == nil || *currency == "" || *currency == "EUR" {
		return sign + "€" + amount
	}
	return sign + amount + " " + *currency
}

// usagePriceBasisReason says in a few words why a line is unpriced.
func usagePriceBasisReason(basis string) string {
	switch basis {
	case "no_rate":
		return "no rate published"
	case "flat_fee":
		return "covered by the plan's flat fee"
	case "not_invoiced":
		return "plan not invoiced"
	case "organisation_total":
		return "priced on the organisation total"
	case "other_currency":
		return "price not in EUR"
	case "":
		return "unpriced"
	default:
		return "unpriced (" + basis + ")"
	}
}

func init() {
	orgCmd.AddCommand(newOrgUsageCommand())
}

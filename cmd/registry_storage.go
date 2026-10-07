package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The registry storage commands. Every registry project has a storage
// limit; once a project is full the registry refuses every push to it, so
// builds stop publishing. These commands show how full it is and what fills
// it, control the image retention policy that frees it, and ask Ankra for
// more.

const (
	bytesPerGiB = int64(1) << 30
	// registryStorageWarnPercent is the fill at which the commands start
	// pointing at the ways to free or add space.
	registryStorageWarnPercent = 80
	// registryStorageMaxRequestGiB is the largest request the platform
	// accepts (10 TiB).
	registryStorageMaxRequestGiB = 10240
	registryStorageBarWidth      = 30
	// The retention policy bounds the platform enforces.
	registryRetentionMaxKeepDays   = 3650
	registryRetentionMaxKeepLatest = 1000
	registryRetentionMaxRules      = 50
	registryCIRepositoryPrefix     = "ankra-ci/"
)

func newRegistryStorageCommand() *cobra.Command {
	storageCommand := &cobra.Command{
		Use:   "storage",
		Short: "See how full the organisation's registry is, free space, or ask for more",
		Long: `See how full the organisation's registry is, free space, or ask for more.

Every registry project has a storage limit (50 GiB by default). Once a project
is full the registry refuses every image push to it, so builds stop
publishing. Run on its own, this shows the usage against the limit for each
project.

  ankra registry storage                      usage against the limit
  ankra registry storage repositories         what takes the space, largest first
  ankra registry storage retention            the image retention policy
  ankra registry storage retention set ...    keep fewer or more images
  ankra registry storage retention run        clean up now instead of at the daily run
  ankra registry storage request ...          ask Ankra for a bigger limit`,
		Example: "  ankra registry storage\n  ankra registry storage -o json",
		Args:    cobra.NoArgs,
		RunE:    runRegistryStorageShow,
	}
	registerStructuredOutputFlags(storageCommand)

	showCommand := &cobra.Command{
		Use:     "show",
		Short:   "Show the registry storage usage against the limit",
		Example: "  ankra registry storage show\n  ankra registry storage show -o json",
		Args:    cobra.NoArgs,
		RunE:    runRegistryStorageShow,
	}
	registerStructuredOutputFlags(showCommand)

	storageCommand.AddCommand(showCommand)
	storageCommand.AddCommand(newRegistryStorageRepositoriesCommand())
	storageCommand.AddCommand(newRegistryRetentionCommand())
	storageCommand.AddCommand(newRegistryStorageRequestCommand())
	return storageCommand
}

// registryStorageRouteError turns the answer of a platform that does not
// serve registry storage yet - a 404 that names nothing - into a sentence
// saying so. Every other error passes through unchanged.
func registryStorageRouteError(routeError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(routeError, &unexpected) && unexpected.StatusCode == http.StatusNotFound && unexpected.Detail == "" {
		return withExitCode(exitError, errors.New("this platform does not serve registry storage yet: "+
			"its registry storage routes are not registered"))
	}
	return routeError
}

func runRegistryStorageShow(command *cobra.Command, _ []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	storage, getError := apiClient.GetRegistryStorage(command.Context())
	if getError != nil {
		return registryStorageRouteError(getError)
	}
	if storage == nil {
		return errors.New("reading registry storage: the platform answered nothing")
	}
	if rendered, renderError := renderStructured(command, storage); rendered || renderError != nil {
		return renderError
	}
	request, requestError := latestRegistryStorageRequest()
	renderRegistryStorage(command.OutOrStdout(), storage, request, requestError)
	return nil
}

// latestRegistryStorageRequest reads the organisation's latest storage
// request, so the usage view can say one is waiting. A failed read is
// answered as an error, so the view says the state is unknown rather than
// showing no request.
func latestRegistryStorageRequest() (*client.LimitRequest, error) {
	list, listError := apiClient.ListLimitRequests()
	if listError != nil {
		return nil, listError
	}
	if list == nil {
		return nil, errors.New("the platform answered nothing")
	}
	for index := range list.Requests {
		if list.Requests[index].LimitKind == client.RegistryStorageLimitKind {
			return &list.Requests[index], nil
		}
	}
	return nil, nil
}

// registryStorageProjectUsage is one project's fill, measured against the
// limit that applies to it: the registry enforces the limit per project, so
// fullness is only ever a project's own usage over its own limit.
type registryStorageProjectUsage struct {
	label   string
	used    *int64
	limit   int64
	percent int
	// measured: used is known and there is a limit to measure it against.
	measured bool
}

func registryStorageProjectUsages(storage *client.RegistryStorage) []registryStorageProjectUsage {
	usages := make([]registryStorageProjectUsage, 0, len(storage.Projects))
	for _, project := range storage.Projects {
		usage := registryStorageProjectUsage{label: project.Name, limit: storage.LimitBytes}
		if project.Project != "" && project.Project != project.Name {
			usage.label += " (" + project.Project + ")"
		}
		if project.LimitBytes != nil {
			usage.limit = *project.LimitBytes
		}
		if project.Status != "unknown" && project.UsedBytes != nil {
			usage.used = project.UsedBytes
			usage.percent, usage.measured = registryStoragePercent(*project.UsedBytes, usage.limit)
		}
		usages = append(usages, usage)
	}
	return usages
}

func renderRegistryStorage(out io.Writer, storage *client.RegistryStorage, request *client.LimitRequest, requestError error) {
	_, _ = fmt.Fprintln(out, "Registry storage")
	_, _ = fmt.Fprintf(out, "  Limit:  %s\n", registryStorageLimitLine(storage))
	partial := storage.UsageStatus == client.RegistryStorageUsagePartial
	switch storage.UsageStatus {
	case client.RegistryStorageUsageNotProvisioned:
		_, _ = fmt.Fprintln(out, "  Used:   nothing yet - the organisation has no registry project. It is created with the first build that publishes an image.")
	case client.RegistryStorageUsageUnknown:
		_, _ = fmt.Fprintln(out, "  Used:   unknown - the registry could not be read just now (this does not mean empty). Try again in a minute.")
	default:
		usages := registryStorageProjectUsages(storage)
		if len(usages) == 0 {
			// A platform that lists no projects: the total is the one
			// project's usage, measured against the per-project limit.
			_, _ = fmt.Fprintf(out, "  Used:   %s\n", registryStorageUsageLine(storage.UsedBytes, storage.LimitBytes, partial))
		} else {
			width := 0
			for _, usage := range usages {
				width = max(width, len(usage.label))
			}
			_, _ = fmt.Fprintln(out, "  Used, per registry project:")
			for _, usage := range usages {
				line := "unknown - could not be read just now (this does not mean empty)"
				if usage.used != nil {
					line = registryStorageUsageLine(usage.used, usage.limit, false)
				}
				_, _ = fmt.Fprintf(out, "    %-*s  %s\n", width, usage.label, line)
			}
			if storage.UsedBytes != nil {
				total := formatBytesGiB(*storage.UsedBytes)
				if partial {
					total = "at least " + total
				}
				_, _ = fmt.Fprintf(out, "  Total:  %s across all projects (the limit applies to each project, not to the total)\n", total)
			}
		}
		if partial {
			_, _ = fmt.Fprintln(out, "  Some of the registry could not be read, so the total is a lower bound: at least this much is used.")
		}
	}

	switch {
	case requestError != nil:
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Storage request: unknown - the limit requests could not be read (%v).\n", requestError)
	case request != nil:
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Storage request: %s, %d GiB per project%s.\n", request.Status, request.RequestedValue,
			optionalSuffix(" (asked "+registryShortTime(request.RequestedAt)+")", request.RequestedAt))
	}

	if fullest, ok := registryStorageFullest(storage); ok && fullest.percent >= registryStorageWarnPercent {
		_, _ = fmt.Fprintln(out)
		if fullest.percent >= 100 {
			_, _ = fmt.Fprintf(out, "Registry project %s is full: every image push to it is refused until space is freed or the limit is raised.\n",
				fullest.label)
		} else {
			_, _ = fmt.Fprintf(out, "Registry project %s is %d%% full. At 100%% every image push to it is refused.\n",
				fullest.label, fullest.percent)
		}
		_, _ = fmt.Fprintln(out, "  Free space:   ankra registry storage repositories   (what takes the space)")
		_, _ = fmt.Fprintln(out, "                ankra registry storage retention set --keep-days <n> --keep-latest <n>")
		_, _ = fmt.Fprintln(out, "  Ask for more: ankra registry storage request --size <GiB> --reason \"...\"")
	}
}

func optionalSuffix(text string, value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return ""
	}
	return text
}

// registryStorageUsageLine is "41.3 GiB of 50 GiB (83%) [#####-----]", or
// the used size alone when there is no limit to measure against.
func registryStorageUsageLine(used *int64, limit int64, lowerBound bool) string {
	if used == nil {
		return "unknown (this does not mean empty)"
	}
	prefix := ""
	if lowerBound {
		prefix = "at least "
	}
	if percent, ok := registryStoragePercent(*used, limit); ok {
		return fmt.Sprintf("%s%s of %s (%d%%)  %s", prefix, formatBytesGiB(*used), formatRegistryLimit(limit), percent,
			registryStorageBar(percent, registryStorageBarWidth))
	}
	return prefix + formatBytesGiB(*used)
}

func registryStorageLimitLine(storage *client.RegistryStorage) string {
	limit := formatRegistryLimit(storage.LimitBytes) + " per registry project"
	if storage.LimitBytes == client.RegistryStorageNoLimit {
		limit = "no limit"
	}
	if storage.Source != "organisation" {
		return limit + " (the platform default)"
	}
	var details []string
	if storage.Reason != nil && strings.TrimSpace(*storage.Reason) != "" {
		details = append(details, strings.TrimSpace(*storage.Reason))
	}
	if storage.UpdatedAt != nil && *storage.UpdatedAt != "" {
		details = append(details, "since "+registryShortTime(storage.UpdatedAt))
	}
	text := limit + ", set for this organisation by Ankra"
	if len(details) > 0 {
		text += " (" + strings.Join(details, ", ") + ")"
	}
	return text
}

// registryStoragePercent is used as a whole percentage of the limit; false
// when there is no limit to measure against.
func registryStoragePercent(used int64, limit int64) (int, bool) {
	if limit <= 0 {
		return 0, false
	}
	return int(used * 100 / limit), true
}

// registryStorageFullest is the fullest project, each measured against its
// own limit. The organisation total is never measured against the
// per-project limit when projects are listed: two projects at 60% each
// would read as 120% and claim pushes are refused when none is. Only a
// platform that lists no projects - where the total is the one project's -
// falls back to the total.
func registryStorageFullest(storage *client.RegistryStorage) (registryStorageProjectUsage, bool) {
	if storage.UsageStatus == client.RegistryStorageUsageUnknown ||
		storage.UsageStatus == client.RegistryStorageUsageNotProvisioned {
		return registryStorageProjectUsage{}, false
	}
	usages := registryStorageProjectUsages(storage)
	if len(usages) == 0 {
		if storage.UsedBytes == nil {
			return registryStorageProjectUsage{}, false
		}
		percent, ok := registryStoragePercent(*storage.UsedBytes, storage.LimitBytes)
		return registryStorageProjectUsage{label: client.RegistryDefaultProjectName, used: storage.UsedBytes,
			limit: storage.LimitBytes, percent: percent, measured: ok}, ok
	}
	var fullest registryStorageProjectUsage
	found := false
	for _, usage := range usages {
		if usage.measured && (!found || usage.percent > fullest.percent) {
			fullest, found = usage, true
		}
	}
	return fullest, found
}

func registryStorageBar(percent int, width int) string {
	filled := min(max(percent, 0), 100) * width / 100
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
}

func formatRegistryLimit(limit int64) string {
	if limit == client.RegistryStorageNoLimit || limit < 0 {
		return "no limit"
	}
	return formatBytesGiB(limit)
}

// formatBytesGiB writes a size in binary units: whole GiB stay whole
// ("50 GiB"), the rest get one decimal ("52.4 GiB", "310.5 MiB").
func formatBytesGiB(size int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 || value == float64(int64(value)) {
		return fmt.Sprintf("%d %s", int64(value), units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

// registryShortTime writes an RFC3339 time as "2006-01-02 15:04 UTC", and
// anything it cannot parse as given.
func registryShortTime(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "-"
	}
	parsed, parseError := time.Parse(time.RFC3339, strings.TrimSpace(*value))
	if parseError != nil {
		return *value
	}
	return parsed.UTC().Format("2006-01-02 15:04 UTC")
}

func newRegistryStorageRepositoriesCommand() *cobra.Command {
	repositoriesCommand := &cobra.Command{
		Use:     "repositories",
		Aliases: []string{"repos"},
		Short:   "Show which repositories take the space, largest first",
		Long: `Show which repositories take the registry space, largest first.

Repositories under ankra-ci/ hold the images Ankra builds and scans in CI, one
per pipeline run and pull request; they are usually what fills the registry.
The others hold the images your applications publish. The Retention column
names the rule that governs each repository, or the organisation policy.

Sizes are approximate: a layer shared by several images is counted for each of
them, so use the sizes to rank repositories, not to add up to the usage. This
reads every image in the registry and can take a while.`,
		Example: "  ankra registry storage repositories\n  ankra registry storage repositories --limit 0 -o json",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			limit, _ := command.Flags().GetInt("limit")
			if limit < 0 {
				return withExitCode(exitUsage, fmt.Errorf("--limit must be 0 (all) or more, got %d", limit))
			}
			repositories, listError := apiClient.ListRegistryStorageRepositories(command.Context())
			if listError != nil {
				return registryStorageRouteError(listError)
			}
			if repositories == nil {
				return errors.New("reading the registry repositories: the platform answered nothing")
			}
			// The view is a copy, so --limit trims what is shown without
			// touching the answer it was read from.
			view := *repositories
			if view.Repositories == nil {
				view.Repositories = []client.RegistryStorageRepository{}
			}
			total := len(view.Repositories)
			if limit > 0 && total > limit {
				view.Repositories = view.Repositories[:limit]
			}
			if rendered, renderError := renderStructured(command, view); rendered || renderError != nil {
				return renderError
			}
			renderRegistryStorageRepositories(command.OutOrStdout(), &view, total, registryRetentionRulePatterns(command))
			return nil
		},
	}
	repositoriesCommand.Flags().Int("limit", 20, "How many repositories to show, largest first (0 shows all)")
	registerStructuredOutputFlags(repositoriesCommand)
	return repositoriesCommand
}

// registryRetentionRulePatterns maps rule ids to their patterns, so the
// repositories table can name the rule. A failed read leaves the ids as
// they are.
func registryRetentionRulePatterns(command *cobra.Command) map[string]string {
	retention, getError := apiClient.GetRegistryRetention(command.Context())
	if getError != nil || retention == nil {
		return nil
	}
	patterns := make(map[string]string, len(retention.Rules))
	for _, rule := range retention.Rules {
		patterns[rule.ID] = rule.RepositoryPattern
	}
	return patterns
}

func renderRegistryStorageRepositories(out io.Writer, repositories *client.RegistryStorageRepositories, total int,
	rulePatterns map[string]string) {
	if repositories.Status == client.RegistryStorageUsageUnknown {
		_, _ = fmt.Fprintln(out, "The registry could not be read just now, so the repositories are unknown (this does not mean empty). Try again in a minute.")
		return
	}
	if total == 0 {
		_, _ = fmt.Fprintln(out, "The registry holds no repositories yet.")
		return
	}
	repositoryTable := table.NewWriter()
	repositoryTable.SetOutputMirror(out)
	repositoryTable.SetStyle(table.StyleRounded)
	repositoryTable.AppendHeader(table.Row{"Repository", "Project", "Size", "Images", "Last push", "Retention"})
	hasCI, hasTruncated, hasUnreadable := false, false, false
	for _, repository := range repositories.Repositories {
		name := repository.Name
		if strings.HasPrefix(repository.Name, registryCIRepositoryPrefix) {
			name += "  (CI)"
			hasCI = true
		}
		size := formatBytesGiB(repository.SizeBytes)
		switch {
		case repository.Unreadable:
			size = "unknown"
			hasUnreadable = true
		case repository.Truncated:
			size = ">= " + size
			hasTruncated = true
		}
		lastPush := repository.LastPushedAt
		lastPush = registryShortTime(&lastPush)
		retention := "org policy"
		if repository.RetentionRuleID != nil && *repository.RetentionRuleID != "" {
			retention = "rule " + *repository.RetentionRuleID
			if pattern, ok := rulePatterns[*repository.RetentionRuleID]; ok {
				retention = "rule " + pattern
			}
		}
		repositoryTable.AppendRow(table.Row{name, repository.Project, size, repository.ArtifactCount, lastPush, retention})
	}
	repositoryTable.Render()
	if shown := len(repositories.Repositories); shown < total {
		_, _ = fmt.Fprintf(out, "Showing the %d largest of %d repositories (--limit 0 shows all).\n", shown, total)
	}
	if repositories.Status == client.RegistryStorageUsagePartial {
		_, _ = fmt.Fprintln(out, "Some of the registry could not be read, so this list may be incomplete.")
	}
	if hasUnreadable {
		_, _ = fmt.Fprintln(out, "unknown marks a repository whose images could not be read just now: its size is not known (this does not mean empty).")
	}
	if hasTruncated {
		_, _ = fmt.Fprintln(out, ">= marks a repository with too many images to read in full: its size is a lower bound.")
	}
	if repositories.SizesAreApproximate {
		_, _ = fmt.Fprintln(out, "Sizes are approximate: layers shared between images are counted for each image, so rank by them rather than adding them up.")
	}
	if hasCI {
		_, _ = fmt.Fprintln(out, "(CI) repositories hold the images CI builds and scans, one per pipeline run. A shorter rule for them frees the most:")
		_, _ = fmt.Fprintln(out, "  ankra registry storage retention rule add 'ankra-ci/**' --keep-days 7 --keep-latest 2")
	}
}

func newRegistryRetentionCommand() *cobra.Command {
	retentionCommand := &cobra.Command{
		Use:   "retention",
		Short: "Show or change which images the registry keeps",
		Long: `Show or change the image retention policy: which images the registry keeps.

An image is kept when it was pushed within the policy's days, or is among the
newest images of its repository; everything else is deleted when retention
runs (daily). The newest image of every repository is never deleted. Deleted
images free their space at the registry's next garbage collection (weekly).

The organisation policy governs every repository. A rule replaces it for the
repositories its pattern matches - for example a shorter one for CI images:

  ankra registry storage retention rule add 'ankra-ci/**' --keep-days 7 --keep-latest 2

Patterns are lower-case letters, digits and . _ / - with * matching within
one path segment and ** across segments; braces and commas are not accepted,
so write one rule per pattern.`,
		Example: "  ankra registry storage retention\n  ankra registry storage retention -o json",
		Args:    cobra.NoArgs,
		RunE:    runRegistryRetentionShow,
	}
	registerStructuredOutputFlags(retentionCommand)
	showCommand := &cobra.Command{
		Use:   "show",
		Short: "Show the image retention policy",
		Args:  cobra.NoArgs,
		RunE:  runRegistryRetentionShow,
	}
	registerStructuredOutputFlags(showCommand)
	retentionCommand.AddCommand(showCommand)
	retentionCommand.AddCommand(newRegistryRetentionSetCommand())
	retentionCommand.AddCommand(newRegistryRetentionRuleCommand())
	retentionCommand.AddCommand(newRegistryRetentionRunCommand())
	return retentionCommand
}

func runRegistryRetentionShow(command *cobra.Command, _ []string) error {
	if _, formatError := structuredFormatFromFlags(command); formatError != nil {
		return formatError
	}
	retention, getError := apiClient.GetRegistryRetention(command.Context())
	if getError != nil {
		return registryStorageRouteError(getError)
	}
	if rendered, renderError := renderStructured(command, retention); rendered || renderError != nil {
		return renderError
	}
	renderRegistryRetention(command.OutOrStdout(), retention)
	return nil
}

// describeRetentionPolicy says a policy in plain words.
func describeRetentionPolicy(policy client.RegistryRetentionPolicy) string {
	latest := "the newest image"
	if policy.KeepLatest != 1 {
		latest = fmt.Sprintf("the %d newest images", policy.KeepLatest)
	}
	if policy.KeepDays <= 0 {
		return fmt.Sprintf("keep %s of each repository, delete the rest", latest)
	}
	return fmt.Sprintf("keep images pushed in the last %s, and always %s of each repository",
		pluralDays(policy.KeepDays), latest)
}

func pluralDays(days int) string {
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

func renderRegistryRetention(out io.Writer, retention *client.RegistryRetention) {
	if retention.Source == "organisation" {
		_, _ = fmt.Fprintln(out, "Image retention (set for this organisation)")
	} else {
		_, _ = fmt.Fprintln(out, "Image retention (the platform default)")
	}
	_, _ = fmt.Fprintf(out, "  Policy:  %s.\n", describeRetentionPolicy(retention.Default))
	if retention.Source == "organisation" {
		updated := ""
		if retention.UpdatedBy != nil && *retention.UpdatedBy != "" {
			updated = " by " + *retention.UpdatedBy
		}
		if retention.UpdatedAt != nil && *retention.UpdatedAt != "" {
			updated += " at " + registryShortTime(retention.UpdatedAt)
		}
		if updated != "" {
			_, _ = fmt.Fprintf(out, "  Changed:%s. Platform default: %s.\n", updated, describeRetentionPolicy(retention.PlatformDefault))
		}
	}
	_, _ = fmt.Fprintln(out)
	if len(retention.Rules) == 0 {
		_, _ = fmt.Fprintln(out, "No rules: every repository follows the policy above.")
	} else {
		_, _ = fmt.Fprintln(out, "Rules (each replaces the policy for the repositories it matches):")
		renderRegistryRetentionRules(out, retention.Rules)
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Always kept:")
	_, _ = fmt.Fprintln(out, "  - the newest image of every repository")
	if retention.RecentlyPulledDays > 0 {
		_, _ = fmt.Fprintf(out, "  - any image pulled in the last %s\n", pluralDays(retention.RecentlyPulledDays))
	}
	_, _ = fmt.Fprintln(out)
	if retention.RunsDailyAt != "" {
		_, _ = fmt.Fprintf(out, "Retention runs daily at %s (start it now: ankra registry storage retention run).\n", retention.RunsDailyAt)
	}
	if retention.SpaceFreedAt != "" {
		_, _ = fmt.Fprintf(out, "Deleted images free their space at the registry's garbage collection: %s.\n", retention.SpaceFreedAt)
	}
}

func renderRegistryRetentionRules(out io.Writer, rules []client.RegistryRetentionRule) {
	ruleTable := table.NewWriter()
	ruleTable.SetOutputMirror(out)
	ruleTable.SetStyle(table.StyleRounded)
	ruleTable.AppendHeader(table.Row{"Pattern", "Keep days", "Keep latest", "Application", "Keeps"})
	for _, rule := range rules {
		application := "-"
		if rule.ApplicationID != nil && *rule.ApplicationID != "" {
			application = *rule.ApplicationID
		}
		keepDays := strconv.Itoa(rule.KeepDays)
		if rule.KeepDays <= 0 {
			keepDays = "off"
		}
		ruleTable.AppendRow(table.Row{rule.RepositoryPattern, keepDays, rule.KeepLatest, application,
			describeRetentionPolicy(client.RegistryRetentionPolicy{KeepDays: rule.KeepDays, KeepLatest: rule.KeepLatest})})
	}
	ruleTable.Render()
}

// retentionTighter reports whether next keeps fewer images than current
// in some repository: fewer days (or the days turned off), or fewer newest.
func retentionTighter(current client.RegistryRetentionPolicy, next client.RegistryRetentionPolicy) bool {
	if next.KeepLatest < current.KeepLatest {
		return true
	}
	if current.KeepDays <= 0 {
		return false
	}
	return next.KeepDays <= 0 || next.KeepDays < current.KeepDays
}

func validateRetentionPolicy(policy client.RegistryRetentionPolicy) error {
	if policy.KeepDays < 0 || policy.KeepDays > registryRetentionMaxKeepDays {
		return withExitCode(exitUsage, fmt.Errorf("--keep-days must be 0 (off) to %d, got %d",
			registryRetentionMaxKeepDays, policy.KeepDays))
	}
	if policy.KeepLatest < 1 || policy.KeepLatest > registryRetentionMaxKeepLatest {
		return withExitCode(exitUsage, fmt.Errorf("--keep-latest must be 1 to %d (the newest image of a repository is always kept), got %d",
			registryRetentionMaxKeepLatest, policy.KeepLatest))
	}
	return nil
}

// confirmRetentionTightening asks before a change that deletes images the
// current policy keeps; --yes skips it.
func confirmRetentionTightening(command *cobra.Command, what string, current, next client.RegistryRetentionPolicy,
	runsDailyAt string) error {
	if !retentionTighter(current, next) {
		return nil
	}
	yes, _ := command.Flags().GetBool("yes")
	when := "at the next retention run"
	if runsDailyAt != "" {
		when += " (daily at " + runsDailyAt + ")"
	}
	return confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), fmt.Sprintf(
		"This keeps fewer images %s.\n  Now: %s.\n  New: %s.\nImages outside it are deleted %s and cannot be restored. Continue? [y/N]: ",
		what, describeRetentionPolicy(current), describeRetentionPolicy(next), when), yes)
}

// renderRetentionUpdate prints a policy write's answer: the policy, and
// whether the registry has it yet.
func renderRetentionUpdate(command *cobra.Command, result *client.RegistryRetentionUpdateResult, headline string) error {
	if rendered, renderError := renderStructured(command, result); rendered || renderError != nil {
		return renderError
	}
	out := command.OutOrStdout()
	_, _ = fmt.Fprintln(out, headline)
	if result.HarborApplied {
		_, _ = fmt.Fprintln(out, "The registry has it now.")
	} else {
		_, _ = fmt.Fprintln(out, "Saved. The registry picks it up automatically within about 5 minutes.")
	}
	_, _ = fmt.Fprintln(out)
	renderRegistryRetention(out, &result.Retention)
	return nil
}

func newRegistryRetentionSetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set",
		Short: "Change the organisation retention policy",
		Long: `Change the organisation retention policy: the one every repository without a
rule follows.

--keep-days keeps every image pushed in that many days (0 turns it off, so
only the newest are kept); --keep-latest always keeps that many newest images
of each repository (at least 1). A flag not passed keeps its current value.
--reset goes back to the platform default. Rules are not touched.

A change that keeps fewer images asks first, because what it no longer keeps
is deleted at the next retention run; --yes skips the prompt.`,
		Example: `  ankra registry storage retention set --keep-days 14
  ankra registry storage retention set --keep-days 7 --keep-latest 2 --yes
  ankra registry storage retention set --reset`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			reset, _ := command.Flags().GetBool("reset")
			daysChanged, latestChanged := command.Flags().Changed("keep-days"), command.Flags().Changed("keep-latest")
			if reset && (daysChanged || latestChanged) {
				return withExitCode(exitUsage, errors.New("--reset goes back to the platform default: pass it alone, without --keep-days or --keep-latest"))
			}
			if !reset && !daysChanged && !latestChanged {
				return withExitCode(exitUsage, errors.New("nothing to set: pass --keep-days, --keep-latest, or --reset"))
			}
			retention, getError := apiClient.GetRegistryRetention(command.Context())
			if getError != nil {
				return registryStorageRouteError(getError)
			}
			update := client.RegistryRetentionUpdate{DefaultSet: true}
			next := retention.PlatformDefault
			if !reset {
				next = retention.Default
				if daysChanged {
					next.KeepDays, _ = command.Flags().GetInt("keep-days")
				}
				if latestChanged {
					next.KeepLatest, _ = command.Flags().GetInt("keep-latest")
				}
				if validationError := validateRetentionPolicy(next); validationError != nil {
					return validationError
				}
				update.Default = &next
			}
			if confirmError := confirmRetentionTightening(command, "in every repository without a rule",
				retention.Default, next, retention.RunsDailyAt); confirmError != nil {
				return confirmError
			}
			result, updateError := apiClient.UpdateRegistryRetention(command.Context(), update)
			if updateError != nil {
				return registryStorageRouteError(updateError)
			}
			headline := "Retention policy updated."
			if reset {
				headline = "Retention policy reset to the platform default."
			}
			return renderRetentionUpdate(command, result, headline)
		},
	}
	setCommand.Flags().Int("keep-days", 0, "Keep every image pushed in this many days (0 turns it off)")
	setCommand.Flags().Int("keep-latest", 0, "Always keep this many newest images of each repository (at least 1)")
	setCommand.Flags().Bool("reset", false, "Go back to the platform default policy")
	setCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt when the change keeps fewer images")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

func newRegistryRetentionRuleCommand() *cobra.Command {
	ruleCommand := &cobra.Command{
		Use:     "rule",
		Aliases: []string{"rules"},
		Short:   "Add, remove and list retention rules for matching repositories",
		Long: `Add, remove and list retention rules. A rule replaces the organisation policy
for the repositories its pattern matches. Patterns are lower-case letters,
digits and . _ / - with * matching within one path segment and ** across
segments; braces and commas are not accepted, so write one rule per pattern.
'**' alone is the organisation policy itself ('retention set').

A rule needs an organisation policy that keeps something: when the policy in
force keeps nothing, the platform refuses rules until one is set with
'ankra registry storage retention set'.`,
	}
	ruleCommand.AddCommand(newRegistryRetentionRuleListCommand())
	ruleCommand.AddCommand(newRegistryRetentionRuleAddCommand())
	ruleCommand.AddCommand(newRegistryRetentionRuleRemoveCommand())
	return ruleCommand
}

func newRegistryRetentionRuleListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the retention rules",
		Example: "  ankra registry storage retention rule list\n  ankra registry storage retention rule list -o json",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			retention, getError := apiClient.GetRegistryRetention(command.Context())
			if getError != nil {
				return registryStorageRouteError(getError)
			}
			rules := retention.Rules
			if rules == nil {
				rules = []client.RegistryRetentionRule{}
			}
			if rendered, renderError := renderStructured(command, map[string]any{"rules": rules}); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(rules) == 0 {
				_, _ = fmt.Fprintf(out, "No rules: every repository follows the organisation policy (%s).\n",
					describeRetentionPolicy(retention.Default))
				return nil
			}
			renderRegistryRetentionRules(out, rules)
			_, _ = fmt.Fprintf(out, "Every other repository follows the organisation policy: %s.\n", describeRetentionPolicy(retention.Default))
			return nil
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

// retentionRuleInputs is the stored rules as they are written back: a rule
// write replaces the whole list, so every rule not being changed is sent
// as it is.
func retentionRuleInputs(rules []client.RegistryRetentionRule) []client.RegistryRetentionRuleInput {
	inputs := make([]client.RegistryRetentionRuleInput, 0, len(rules))
	for _, rule := range rules {
		inputs = append(inputs, client.RegistryRetentionRuleInput{
			RepositoryPattern: rule.RepositoryPattern,
			ApplicationID:     rule.ApplicationID,
			KeepDays:          rule.KeepDays,
			KeepLatest:        rule.KeepLatest,
		})
	}
	return inputs
}

// retentionPatternCharacters is everything a rule pattern may hold: the
// platform refuses braces and commas, so one rule covers one pattern.
var retentionPatternCharacters = regexp.MustCompile(`^[a-z0-9._/*-]+$`)

func normaliseRetentionPattern(raw string) (string, error) {
	pattern := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case pattern == "":
		return "", withExitCode(exitUsage, errors.New("the repository pattern is empty"))
	case pattern == "**":
		return "", withExitCode(exitUsage, errors.New("'**' matches every repository, which is the organisation policy: "+
			"change it with 'ankra registry storage retention set'"))
	case strings.ContainsAny(pattern, "{},"):
		return "", withExitCode(exitUsage, fmt.Errorf("pattern %q uses braces or commas, which rules do not accept: "+
			"add one rule per pattern instead", pattern))
	case !retentionPatternCharacters.MatchString(pattern):
		return "", withExitCode(exitUsage, fmt.Errorf("pattern %q may hold only lower-case letters, digits, . _ / - and * "+
			"(* within a path segment, ** across segments)", pattern))
	}
	return pattern, nil
}

func newRegistryRetentionRuleAddCommand() *cobra.Command {
	addCommand := &cobra.Command{
		Use:   "add <pattern>",
		Short: "Add a rule, or change the one for this pattern",
		Long: `Add a retention rule for the repositories a pattern matches, or change the rule
already stored for that pattern.

--keep-days and --keep-latest work as in 'retention set'; a flag not passed
takes the existing rule's value, or the organisation policy's for a new rule.
--application ties the rule to an application, so it shows with it.

A rule that keeps fewer images than what governs those repositories now asks
first; --yes skips the prompt. Quote the pattern so the shell does not expand
it.`,
		Example: `  ankra registry storage retention rule add 'ankra-ci/**' --keep-days 7 --keep-latest 2
  ankra registry storage retention rule add 'investor-update/*' --keep-latest 10 --application <id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			pattern, patternError := normaliseRetentionPattern(arguments[0])
			if patternError != nil {
				return patternError
			}
			retention, getError := apiClient.GetRegistryRetention(command.Context())
			if getError != nil {
				return registryStorageRouteError(getError)
			}
			inputs := retentionRuleInputs(retention.Rules)
			existing := slices.IndexFunc(inputs, func(rule client.RegistryRetentionRuleInput) bool {
				return rule.RepositoryPattern == pattern
			})
			current := retention.Default
			if existing >= 0 {
				current = client.RegistryRetentionPolicy{KeepDays: inputs[existing].KeepDays, KeepLatest: inputs[existing].KeepLatest}
			} else if len(inputs) >= registryRetentionMaxRules {
				return withExitCode(exitUsage, fmt.Errorf("the organisation already has %d rules, the most it may hold: remove one first",
					registryRetentionMaxRules))
			}
			next := current
			if command.Flags().Changed("keep-days") {
				next.KeepDays, _ = command.Flags().GetInt("keep-days")
			}
			if command.Flags().Changed("keep-latest") {
				next.KeepLatest, _ = command.Flags().GetInt("keep-latest")
			}
			if validationError := validateRetentionPolicy(next); validationError != nil {
				return validationError
			}
			rule := client.RegistryRetentionRuleInput{RepositoryPattern: pattern, KeepDays: next.KeepDays, KeepLatest: next.KeepLatest}
			if existing >= 0 {
				rule.ApplicationID = inputs[existing].ApplicationID
			}
			if command.Flags().Changed("application") {
				applicationID, _ := command.Flags().GetString("application")
				applicationID = strings.TrimSpace(applicationID)
				rule.ApplicationID = nil
				if applicationID != "" {
					rule.ApplicationID = &applicationID
				}
			}
			if existing >= 0 {
				inputs[existing] = rule
			} else {
				inputs = append(inputs, rule)
			}
			if confirmError := confirmRetentionTightening(command, "in the repositories matching "+pattern,
				current, next, retention.RunsDailyAt); confirmError != nil {
				return confirmError
			}
			result, updateError := apiClient.UpdateRegistryRetention(command.Context(),
				client.RegistryRetentionUpdate{RulesSet: true, Rules: inputs})
			if updateError != nil {
				return registryStorageRouteError(updateError)
			}
			headline := fmt.Sprintf("Rule for %s added.", pattern)
			if existing >= 0 {
				headline = fmt.Sprintf("Rule for %s updated.", pattern)
			}
			return renderRetentionUpdate(command, result, headline)
		},
	}
	addCommand.Flags().Int("keep-days", 0, "Keep every image pushed in this many days (0 turns it off)")
	addCommand.Flags().Int("keep-latest", 0, "Always keep this many newest images of each matching repository (at least 1)")
	addCommand.Flags().String("application", "", "The application the rule belongs to (its id)")
	addCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt when the rule keeps fewer images")
	registerStructuredOutputFlags(addCommand)
	return addCommand
}

func newRegistryRetentionRuleRemoveCommand() *cobra.Command {
	removeCommand := &cobra.Command{
		Use:     "remove <pattern>",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove a rule; its repositories follow the organisation policy again",
		Long: `Remove the retention rule for a pattern; the repositories it matched follow the
organisation policy again. When that policy keeps fewer images than the rule
did, it asks first; --yes skips the prompt.`,
		Example: "  ankra registry storage retention rule remove 'ankra-ci/**'",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			pattern := strings.ToLower(strings.TrimSpace(arguments[0]))
			retention, getError := apiClient.GetRegistryRetention(command.Context())
			if getError != nil {
				return registryStorageRouteError(getError)
			}
			inputs := retentionRuleInputs(retention.Rules)
			existing := slices.IndexFunc(inputs, func(rule client.RegistryRetentionRuleInput) bool {
				return rule.RepositoryPattern == pattern
			})
			if existing < 0 {
				var patterns []string
				for _, rule := range inputs {
					patterns = append(patterns, rule.RepositoryPattern)
				}
				known := "there are no rules"
				if len(patterns) > 0 {
					known = "the rules are: " + strings.Join(patterns, ", ")
				}
				return withExitCode(exitNotFound, fmt.Errorf("no retention rule for %q; %s", pattern, known))
			}
			removed := client.RegistryRetentionPolicy{KeepDays: inputs[existing].KeepDays, KeepLatest: inputs[existing].KeepLatest}
			if confirmError := confirmRetentionTightening(command, "in the repositories matching "+pattern,
				removed, retention.Default, retention.RunsDailyAt); confirmError != nil {
				return confirmError
			}
			inputs = slices.Delete(inputs, existing, existing+1)
			result, updateError := apiClient.UpdateRegistryRetention(command.Context(),
				client.RegistryRetentionUpdate{RulesSet: true, Rules: inputs})
			if updateError != nil {
				return registryStorageRouteError(updateError)
			}
			return renderRetentionUpdate(command, result, fmt.Sprintf("Rule for %s removed.", pattern))
		},
	}
	removeCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt when the organisation policy keeps fewer images")
	registerStructuredOutputFlags(removeCommand)
	return removeCommand
}

func newRegistryRetentionRunCommand() *cobra.Command {
	runCommand := &cobra.Command{
		Use:   "run",
		Short: "Clean up now instead of at the daily run",
		Long: `Start the retention cleanup now instead of at its daily time: images the policy
does not keep are deleted. The space they held is freed at the registry's next
garbage collection (weekly), not at once.`,
		Example: "  ankra registry storage retention run",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			run, runError := apiClient.RunRegistryRetention(command.Context())
			if runError != nil {
				return registryStorageRouteError(runError)
			}
			if run.Projects == nil {
				run.Projects = []client.RegistryRetentionRunProject{}
			}
			if rendered, renderError := renderStructured(command, run); rendered || renderError != nil {
				return renderError
			}
			out := command.OutOrStdout()
			if len(run.Projects) == 0 {
				_, _ = fmt.Fprintln(out, "No registry project to clean up.")
				return nil
			}
			for _, project := range run.Projects {
				if project.Started {
					_, _ = fmt.Fprintf(out, "  %s: cleanup started\n", project.Project)
					continue
				}
				reason := "not started"
				if project.Reason != nil && strings.TrimSpace(*project.Reason) != "" {
					reason += ": " + strings.TrimSpace(*project.Reason)
				}
				_, _ = fmt.Fprintf(out, "  %s: %s\n", project.Project, reason)
			}
			freed := "at the registry's next garbage collection"
			if run.SpaceFreedAt != "" {
				freed += " (" + run.SpaceFreedAt + ")"
			}
			_, _ = fmt.Fprintf(out, "Deleted images free their space %s.\n", freed)
			return nil
		},
	}
	registerStructuredOutputFlags(runCommand)
	return runCommand
}

func newRegistryStorageRequestCommand() *cobra.Command {
	requestCommand := &cobra.Command{
		Use:   "request",
		Short: "Ask Ankra for a bigger storage limit",
		Long: `Ask Ankra for a bigger registry storage limit. --size is the limit you need
for each registry project, in GiB (more than today's, at most 10240); --reason
says why. The Ankra team reviews the request and you are notified when it is
decided; once approved the registry applies the new limit within about 5
minutes. 'ankra org limits list' shows the request's state.`,
		Example: `  ankra registry storage request --size 100 --reason "Three services now publish nightly images"`,
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			size, _ := command.Flags().GetInt64("size")
			reason, _ := command.Flags().GetString("reason")
			request, submitError := submitRegistryStorageRequest(command, size, reason)
			if submitError != nil {
				return submitError
			}
			if rendered, renderError := renderStructured(command, request); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(),
				"Storage request submitted (%s): %d GiB per registry project. The Ankra team reviews it and you are notified "+
					"when it is decided; once approved, the registry applies the new limit within about 5 minutes.\n",
				request.Status, size)
			return nil
		},
	}
	requestCommand.Flags().Int64("size", 0, "The storage limit you need for each registry project, in GiB")
	requestCommand.Flags().String("reason", "", "Why the organisation needs it (required)")
	registerStructuredOutputFlags(requestCommand)
	return requestCommand
}

// submitRegistryStorageRequest checks a storage request against the limit
// in force and submits it; 'registry storage request' and 'org limits
// request --kind registry-storage' both come through here.
func submitRegistryStorageRequest(command *cobra.Command, sizeGiB int64, reason string) (*client.LimitRequest, error) {
	if sizeGiB <= 0 || sizeGiB > registryStorageMaxRequestGiB {
		return nil, withExitCode(exitUsage, fmt.Errorf("the requested size must be 1 to %d GiB, got %d",
			registryStorageMaxRequestGiB, sizeGiB))
	}
	if strings.TrimSpace(reason) == "" {
		return nil, withExitCode(exitUsage, errors.New("say why the organisation needs more storage (--reason for "+
			"'registry storage request', --justification for 'org limits request')"))
	}
	// Today's limit is read first so a request at or below it is refused
	// here with the limit named. The platform enforces the same rule, so
	// when the read fails the check is skipped - said on stderr, never
	// silently - and the platform decides.
	storage, getError := apiClient.GetRegistryStorage(command.Context())
	switch {
	case getError != nil:
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "Could not read today's storage limit (%v); submitting anyway - "+
			"the platform refuses a size that is not above it.\n", registryStorageRouteError(getError))
	case storage == nil:
		_, _ = fmt.Fprintln(command.ErrOrStderr(), "Could not read today's storage limit (the platform answered nothing); "+
			"submitting anyway - the platform refuses a size that is not above it.")
	case storage.LimitBytes == client.RegistryStorageNoLimit:
		return nil, withExitCode(exitUsage, errors.New("the organisation's registry storage has no limit: there is nothing to raise"))
	case storage.LimitBytes > 0 && sizeGiB*bytesPerGiB <= storage.LimitBytes:
		return nil, withExitCode(exitUsage, fmt.Errorf("the requested size must be more than today's limit of %s per project",
			formatBytesGiB(storage.LimitBytes)))
	}
	request, submitError := apiClient.SubmitLimitRequest(client.RegistryStorageLimitKind, sizeGiB, strings.TrimSpace(reason))
	if submitError != nil {
		var unexpected *client.UnexpectedResponseError
		if errors.As(submitError, &unexpected) && unexpected.StatusCode == http.StatusConflict {
			return nil, fmt.Errorf("%w ('ankra org limits list' shows the request that is already waiting)", submitError)
		}
		return nil, fmt.Errorf("submitting the storage request: %w", submitError)
	}
	return request, nil
}

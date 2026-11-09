package cmd

// `ankra org audit-logs list|export` and `ankra cluster audit-logs`: the
// organisation's audit trail over GET
// /api/v1/org/organisation/audit-logs/export, the one audit route an API
// token reaches (needs audit.read). The listing reads the JSON export and
// shows the newest rows; the per-cluster view is the same read narrowed to
// one cluster.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// auditLogNow is the clock --since and --until read; tests pin it.
var auditLogNow = time.Now

const (
	auditLogDefaultListWindow = "7d"
	auditLogDefaultListLimit  = 50
	auditLogUnbounded         = "all"
)

// auditLogList is what `list` prints with -o json|yaml.
type auditLogList struct {
	Entries   []client.AuditLogEntry `json:"entries" yaml:"entries"`
	Shown     int                    `json:"shown" yaml:"shown"`
	Matched   int                    `json:"matched" yaml:"matched"`
	Truncated bool                   `json:"truncated" yaml:"truncated"`
}

var orgAuditLogsCmd = &cobra.Command{
	Use:     "audit-logs",
	Aliases: []string{"audit-log", "audit"},
	Short:   "Read and export the organisation's audit trail",
	Long: `The organisation's audit trail: who did what, to which resource, on which
cluster, and whether it worked. Agents, people, service accounts and the
platform itself each appear as their own actor type.

Reading it needs the audit.read permission. One read returns at most
100,000 rows, newest first; narrow the window or the filters when the
platform says the trail was cut short.`,
}

var orgAuditLogsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the newest audit log entries that match the filters",
	Long: `Show the newest audit log entries that match the filters. Without --since or
--until it reads the last 7 days; --since all reads the whole trail.

  ankra org audit-logs list
  ankra org audit-logs list --since 24h --actor-type agent --outcome failed
  ankra org audit-logs list --cluster prod --action delete --limit 0 -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAuditLogList(cmd, "")
	},
}

var orgAuditLogsExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Download the audit trail as CSV or JSON",
	Long: `Download the audit trail that matches the filters, as CSV (the default) or
JSON, to a file or to stdout. Without --since or --until it exports the whole
trail, up to the platform's 100,000-row cap; a capped export is reported on
stderr.

  ankra org audit-logs export --file audit.csv
  ankra org audit-logs export --format json --since 2026-11-01 --until 2026-11-08 > audit.json
  ankra org audit-logs export --cluster prod --actor-type agent --file prod-agents.csv`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("format")
		format = strings.ToLower(strings.TrimSpace(format))
		if format != client.AuditLogFormatCSV && format != client.AuditLogFormatJSON {
			return withExitCode(exitUsage, fmt.Errorf("--format must be csv or json, got %q", format))
		}
		filters, filtersError := auditLogFiltersFromFlags(cmd, "")
		if filtersError != nil {
			return filtersError
		}
		if clusterError := applyAuditLogClusterFlag(cmd, &filters); clusterError != nil {
			return clusterError
		}
		filePath, _ := cmd.Flags().GetString("file")
		return exportAuditLogs(cmd, filters, format, filePath)
	},
}

var clusterAuditLogsCmd = withClusterArgument(clusterSelectionAllowed, &cobra.Command{
	Use:   "audit-logs [cluster]",
	Short: "Show the newest audit log entries for one cluster",
	Long: `Show the newest audit log entries for one cluster: the organisation's trail
narrowed to the cluster, the same rows as the cluster's Audit tab. Without a
cluster argument it uses the selected cluster. Without --since or --until it
reads the last 7 days. To download a cluster's trail, use
ankra org audit-logs export --cluster <cluster>.

  ankra cluster audit-logs prod --since 24h --actor-type agent`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		if _, limitError := auditLogLimit(cmd); limitError != nil {
			return limitError
		}
		if _, filtersError := auditLogFiltersFromFlags(cmd, auditLogDefaultListWindow); filtersError != nil {
			return filtersError
		}
		clusterID, _, resolveError := resolveClusterFromArgs(cmd, args)
		if resolveError != nil {
			return resolveError
		}
		return runAuditLogList(cmd, clusterID)
	},
})

// runAuditLogList reads the trail, keeps the newest --limit rows and prints
// them. clusterID, when set, overrides any --cluster flag.
func runAuditLogList(cmd *cobra.Command, clusterID string) error {
	if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
		return formatError
	}
	limit, limitError := auditLogLimit(cmd)
	if limitError != nil {
		return limitError
	}
	filters, filtersError := auditLogFiltersFromFlags(cmd, auditLogDefaultListWindow)
	if filtersError != nil {
		return filtersError
	}
	if clusterID != "" {
		filters.ClusterID = clusterID
	} else if clusterError := applyAuditLogClusterFlag(cmd, &filters); clusterError != nil {
		return clusterError
	}
	trail, listError := apiClient.ListAuditLogs(context.Background(), filters)
	if listError != nil {
		return fmt.Errorf("reading the audit log: %w", listError)
	}
	listing := auditLogList{Entries: trail.Entries, Matched: len(trail.Entries), Truncated: trail.Truncated}
	if limit > 0 && len(listing.Entries) > limit {
		listing.Entries = listing.Entries[:limit]
	}
	listing.Shown = len(listing.Entries)
	if rendered, renderError := renderStructured(cmd, listing); rendered || renderError != nil {
		return renderError
	}
	if listing.Shown == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No audit log entries match.")
		return nil
	}
	renderAuditLogTable(cmd.OutOrStdout(), listing.Entries)
	if listing.Shown < listing.Matched {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Showing the newest %d of %d matching entries; --limit 0 shows them all.\n",
			listing.Shown, listing.Matched)
	}
	if listing.Truncated {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "The platform cut the trail short at its row cap; narrow --since/--until or the filters to see older entries.")
	}
	return nil
}

func auditLogLimit(cmd *cobra.Command) (int, error) {
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 0 {
		return 0, withExitCode(exitUsage, fmt.Errorf("--limit must be 0 (no limit) or more, got %d", limit))
	}
	return limit, nil
}

func renderAuditLogTable(destination io.Writer, entries []client.AuditLogEntry) {
	writer := table.NewWriter()
	writer.SetOutputMirror(destination)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Time", "Actor", "Action", "Target", "Cluster", "Outcome"})
	for _, entry := range entries {
		writer.AppendRow(table.Row{entry.CreatedAt, auditLogActor(entry), entry.ActionType,
			auditLogTarget(entry), auditLogCluster(entry), entry.Outcome})
	}
	writer.Render()
}

func auditLogActor(entry client.AuditLogEntry) string {
	actor := entry.UserEmail
	if actor == "" {
		actor = "-"
	}
	if entry.ActorType != "" && entry.ActorType != "human" {
		actor += " (" + entry.ActorType + ")"
	}
	return actor
}

func auditLogTarget(entry client.AuditLogEntry) string {
	switch {
	case entry.TargetName != "" && entry.TargetKind != "":
		return entry.TargetKind + "/" + entry.TargetName
	case entry.TargetName != "":
		return entry.TargetName
	case entry.ResourceID != nil && *entry.ResourceID != "":
		return entry.ResourceType + "/" + *entry.ResourceID
	default:
		return entry.ResourceType
	}
}

func auditLogCluster(entry client.AuditLogEntry) string {
	if entry.ClusterName != nil && *entry.ClusterName != "" {
		return *entry.ClusterName
	}
	if entry.ClusterID != nil {
		return *entry.ClusterID
	}
	return "-"
}

// exportAuditLogs streams the export to filePath, or to stdout when it is
// empty or "-". A failed download removes the partial file.
func exportAuditLogs(cmd *cobra.Command, filters client.AuditLogFilters, format string, filePath string) error {
	writingToStdout := filePath == "" || filePath == "-"
	destination := cmd.OutOrStdout()
	var file *os.File
	if !writingToStdout {
		createdFile, createError := os.Create(filepath.Clean(filePath))
		if createError != nil {
			return fmt.Errorf("creating %s: %w", filePath, createError)
		}
		file = createdFile
		destination = file
	}
	result, exportError := apiClient.ExportAuditLogs(context.Background(), filters, format, destination)
	if file != nil {
		closeError := file.Close()
		if exportError == nil && closeError != nil {
			exportError = closeError
		}
		if exportError != nil {
			_ = os.Remove(filePath)
		}
	}
	if exportError != nil {
		return fmt.Errorf("exporting the audit log: %w", exportError)
	}
	if !writingToStdout {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Wrote the audit log (%s, %d bytes) to %s.\n", result.Format, result.BytesWritten, filePath)
	}
	if result.Truncated {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "The platform cut the export short at its 100,000-row cap; export narrower --since/--until windows to get the rest.")
	}
	return nil
}

// auditLogFiltersFromFlags builds the filters from the shared flag set and
// rejects a malformed value before anything is sent. The cluster is not
// part of it: resolving a cluster name reads the platform.
// defaultSince applies when neither --since nor --until was given.
func auditLogFiltersFromFlags(cmd *cobra.Command, defaultSince string) (client.AuditLogFilters, error) {
	flagValue := func(name string) string {
		if cmd.Flags().Lookup(name) == nil {
			return ""
		}
		value, _ := cmd.Flags().GetString(name)
		return strings.TrimSpace(value)
	}
	filters := client.AuditLogFilters{
		Search:       flagValue("search"),
		ActionType:   flagValue("action"),
		ResourceType: flagValue("resource-type"),
		ActorType:    strings.ToLower(flagValue("actor-type")),
		Outcome:      strings.ToLower(flagValue("outcome")),
	}
	if filters.ActorType != "" && !slices.Contains(client.AuditLogActorTypes, filters.ActorType) {
		return filters, withExitCode(exitUsage, fmt.Errorf("--actor-type must be one of %s, got %q",
			strings.Join(client.AuditLogActorTypes, ", "), filters.ActorType))
	}
	if filters.Outcome != "" && !slices.Contains(client.AuditLogOutcomes, filters.Outcome) {
		return filters, withExitCode(exitUsage, fmt.Errorf("--outcome must be one of %s, got %q",
			strings.Join(client.AuditLogOutcomes, ", "), filters.Outcome))
	}
	for _, identifier := range []struct {
		flag   string
		target *string
	}{{"user", &filters.UserID}, {"agent-task", &filters.AgentTaskID}} {
		raw := flagValue(identifier.flag)
		if raw == "" {
			continue
		}
		if _, parseError := uuid.Parse(raw); parseError != nil {
			return filters, withExitCode(exitUsage, fmt.Errorf("--%s must be an id (a UUID), got %q", identifier.flag, raw))
		}
		*identifier.target = raw
	}

	now := auditLogNow()
	rawSince, rawUntil := flagValue("since"), flagValue("until")
	if rawSince == "" && rawUntil == "" {
		rawSince = defaultSince
	}
	startDate, sinceError := parseAuditTimeBound(rawSince, now)
	if sinceError != nil {
		return filters, withExitCode(exitUsage, fmt.Errorf("--since: %w", sinceError))
	}
	endDate, untilError := parseAuditTimeBound(rawUntil, now)
	if untilError != nil {
		return filters, withExitCode(exitUsage, fmt.Errorf("--until: %w", untilError))
	}
	if startDate != nil && endDate != nil && endDate.Before(*startDate) {
		return filters, withExitCode(exitUsage, errors.New("--until is before --since"))
	}
	filters.StartDate, filters.EndDate = startDate, endDate
	return filters, nil
}

// applyAuditLogClusterFlag narrows filters to the cluster --cluster names,
// resolving a name to its id.
func applyAuditLogClusterFlag(cmd *cobra.Command, filters *client.AuditLogFilters) error {
	clusterReference, _ := cmd.Flags().GetString("cluster")
	clusterReference = strings.TrimSpace(clusterReference)
	if clusterReference == "" {
		return nil
	}
	resolvedClusterID, resolveError := resolveClusterArg(clusterReference)
	if resolveError != nil {
		return resolveError
	}
	filters.ClusterID = resolvedClusterID
	return nil
}

// parseAuditTimeBound reads a --since/--until value: empty or "all" for no
// bound, a span back from now (90m, 24h, 7d, 2w), an RFC 3339 time or a
// plain date (midnight UTC).
func parseAuditTimeBound(raw string, now time.Time) (*time.Time, error) {
	text := strings.TrimSpace(raw)
	if text == "" || strings.EqualFold(text, auditLogUnbounded) {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if parsed, parseError := time.Parse(layout, text); parseError == nil {
			parsedUTC := parsed.UTC()
			return &parsedUTC, nil
		}
	}
	span, spanError := parseAuditSpan(text)
	if spanError != nil {
		return nil, fmt.Errorf("expected a span such as 24h or 7d, a date such as 2026-11-01, an RFC 3339 time or all, got %q", raw)
	}
	bound := now.Add(-span).UTC()
	return &bound, nil
}

func parseAuditSpan(text string) (time.Duration, error) {
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if strings.HasSuffix(text, suffix) {
			count, parseError := strconv.Atoi(strings.TrimSuffix(text, suffix))
			if parseError != nil || count <= 0 {
				return 0, errors.New("not a span")
			}
			return time.Duration(count) * unit, nil
		}
	}
	span, parseError := time.ParseDuration(text)
	if parseError != nil || span <= 0 {
		return 0, errors.New("not a span")
	}
	return span, nil
}

func registerAuditLogFilterFlags(command *cobra.Command, includeCluster bool) {
	command.Flags().String("since", "", "start of the window: a span back from now (24h, 7d, 2w), a date, an RFC 3339 time, or all")
	command.Flags().String("until", "", "end of the window, in the same forms as --since")
	command.Flags().String("actor-type", "", "only this kind of actor: agent, human, service_account or system")
	command.Flags().String("user", "", "only actions by this user id")
	command.Flags().String("action", "", "only this action type")
	command.Flags().String("resource-type", "", "only this resource type")
	command.Flags().String("outcome", "", "only succeeded or failed actions")
	command.Flags().String("agent-task", "", "only the runs of this agent id")
	command.Flags().String("search", "", "free-text search over the entries")
	if includeCluster {
		command.Flags().String("cluster", "", "only entries for this cluster (name or id)")
	}
}

func init() {
	registerAuditLogFilterFlags(orgAuditLogsListCmd, true)
	registerAuditLogFilterFlags(orgAuditLogsExportCmd, true)
	registerAuditLogFilterFlags(clusterAuditLogsCmd, false)
	for _, command := range []*cobra.Command{orgAuditLogsListCmd, clusterAuditLogsCmd} {
		command.Flags().Int("limit", auditLogDefaultListLimit, "show at most this many of the newest entries (0 shows all)")
	}
	orgAuditLogsExportCmd.Flags().String("format", client.AuditLogFormatCSV, "export format: csv or json")
	orgAuditLogsExportCmd.Flags().String("file", "", "write the export to this file (default stdout)")
	registerStructuredOutputFlags(orgAuditLogsListCmd, clusterAuditLogsCmd)

	orgAuditLogsCmd.AddCommand(orgAuditLogsListCmd)
	orgAuditLogsCmd.AddCommand(orgAuditLogsExportCmd)
	orgCmd.AddCommand(orgAuditLogsCmd)
	clusterCmd.AddCommand(clusterAuditLogsCmd)
}

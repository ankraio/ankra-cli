package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var securityWorkloadsCmd = &cobra.Command{
	Use:   "workloads",
	Short: "Scanned workloads ranked by risk, with their posture split by disposition",
	Long: `List the fleet's scanned workload containers, riskiest first. Each row
carries the observed and actionable severity counts, what is acknowledged
or accepted, the fixable severe count and how many of its CVEs CISA lists
as exploited.

The filters are the findings list's. --sort defaults to risk_score.

Examples:
  ankra security workloads --known-exploited
  ankra security workloads --cluster production --namespace payments
  ankra security workloads --addon ingress-nginx --sort last_scan --order asc`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		options, err := securityFindingsOptionsFromFlags(cmd)
		if err != nil {
			return err
		}
		list, err := apiClient.ListSecurityWorkloads(options)
		if err != nil {
			return fmt.Errorf("listing security workloads: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecurityWorkloads(cmd, list, options)
		return nil
	},
}

func renderSecurityWorkloads(cmd *cobra.Command, list *client.SecurityWorkloadList, options client.SecurityFindingsOptions) {
	out := cmd.OutOrStdout()
	if len(list.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No workloads match these filters.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Cluster", "Workload", "Image", "Add-on", "Priority", "Risk", "Actionable", "Known exploited", "Fixable severe", "Accepted", "Last scan"})
	for _, workload := range list.Result {
		knownExploited := fmt.Sprintf("%d", workload.KnownExploited)
		if workload.KnownExploited > 0 {
			knownExploited = text.FgRed.Sprint(knownExploited)
		}
		writer.AppendRow(table.Row{
			workload.ClusterName,
			securityWorkloadLabel(workload),
			stringOrEmpty(workload.ImageRef),
			securityAddonLabel(workload.AddonSlug, workload.AddonAttribution),
			workload.Priority,
			workload.RiskScore,
			severityCountsCell(workload.Actionable),
			knownExploited,
			workload.FixableSevere,
			severityCountsTotal(workload.AcceptedRisk),
			formatTimeAgo(workload.LastScan),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d workloads", list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
	if options.Sort != "" {
		_, _ = fmt.Fprintf(out, " · sorted by %s %s", options.Sort, options.Order)
	}
	_, _ = fmt.Fprintln(out)
	if list.Scanner.Status != "" && list.Scanner.Status != "fresh" {
		_, _ = fmt.Fprintf(out, "Scanner coverage is %s: %d stale · %d unscanned clusters in scope.\n",
			list.Scanner.Status, list.Scanner.StaleClusters, list.Scanner.UnscannedClusters)
	}
}

// securityWorkloadLabel names a workload as namespace/kind name; a report
// with no workload behind it (a cluster-scoped scan) names the image.
func securityWorkloadLabel(workload client.SecurityWorkload) string {
	if workload.WorkloadName == nil || *workload.WorkloadName == "" {
		return "(cluster-scoped)"
	}
	return strings.TrimSpace(stringOrEmpty(workload.WorkloadNamespace) + "/" + stringOrEmpty(workload.WorkloadKind) + " " + *workload.WorkloadName)
}

// securityAddonLabel is the attributed add-on slug, marked when the
// attribution is partial or ambiguous rather than certain.
func securityAddonLabel(slug *string, attribution client.SecurityAttributionSummary) string {
	if slug == nil || *slug == "" {
		if attribution.Status != "" && attribution.Status != "matched" {
			return "(" + attribution.Status + ")"
		}
		return "-"
	}
	if attribution.Partial || attribution.Ambiguous > 0 {
		return *slug + " (" + attribution.Status + ")"
	}
	return *slug
}

func severityCountsTotal(counts client.SecuritySeverityCounts) int {
	return counts.Critical + counts.High + counts.Medium + counts.Low + counts.Unknown
}

// severityCountsCell renders a severity split compactly: "3C 5H 12M".
func severityCountsCell(counts client.SecuritySeverityCounts) string {
	parts := []string{}
	if counts.Critical > 0 {
		parts = append(parts, text.FgHiRed.Sprintf("%dC", counts.Critical))
	}
	if counts.High > 0 {
		parts = append(parts, text.FgRed.Sprintf("%dH", counts.High))
	}
	if counts.Medium > 0 {
		parts = append(parts, text.FgYellow.Sprintf("%dM", counts.Medium))
	}
	if counts.Low > 0 {
		parts = append(parts, fmt.Sprintf("%dL", counts.Low))
	}
	if counts.Unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d?", counts.Unknown))
	}
	if len(parts) == 0 {
		return "0"
	}
	return strings.Join(parts, " ")
}

// securityFindingWithOccurrences is the structured document of
// `security finding` when the occurrence flags are set: the finding plus
// the requested occurrence page rather than the detail's active-only list.
type securityFindingWithOccurrences struct {
	Finding     client.SecurityFinding      `json:"finding" yaml:"finding"`
	Occurrences []client.SecurityOccurrence `json:"occurrences" yaml:"occurrences"`
	Pagination  client.SecurityPagination   `json:"pagination" yaml:"pagination"`
}

// securityFindingOccurrenceFlagsSet says whether the reader asked for the
// occurrence listing rather than the detail's own active occurrences.
func securityFindingOccurrenceFlagsSet(cmd *cobra.Command) bool {
	for _, name := range []string{"status", "cluster", "page", "page-size"} {
		if flag := cmd.Flags().Lookup(name); flag != nil && flag.Changed {
			return true
		}
	}
	return false
}

func runSecurityFindingWithOccurrences(cmd *cobra.Command, findingID string) error {
	status, _ := cmd.Flags().GetString("status")
	clusterFlag, _ := cmd.Flags().GetString("cluster")
	page, _ := cmd.Flags().GetInt("page")
	pageSize, _ := cmd.Flags().GetInt("page-size")
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "", "active", "resolved":
	default:
		return withExitCode(exitUsage, fmt.Errorf("--status must be active or resolved, got %q", status))
	}
	options := client.SecurityOccurrencesOptions{FindingID: findingID, Status: status, Page: page, PageSize: pageSize}
	if strings.TrimSpace(clusterFlag) != "" {
		clusterID, err := resolveClusterID(clusterFlag)
		if err != nil {
			return err
		}
		options.ClusterID = clusterID
	}
	detail, err := apiClient.GetSecurityFinding(findingID)
	if err != nil {
		return fmt.Errorf("reading security finding: %w", err)
	}
	occurrences, err := apiClient.ListSecurityFindingOccurrences(options)
	if err != nil {
		return fmt.Errorf("listing finding occurrences: %w", err)
	}
	document := securityFindingWithOccurrences{Finding: detail.Finding, Occurrences: occurrences.Result, Pagination: occurrences.Pagination}
	if rendered, err := renderStructured(cmd, document); rendered || err != nil {
		return err
	}
	detail.Occurrences = nil
	renderSecurityFindingDetail(cmd, detail)
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out)
	if len(occurrences.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No occurrences match these filters.")
		return nil
	}
	renderSecurityOccurrenceTable(out, occurrences.Result)
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d occurrences", occurrences.Pagination.Page, occurrences.Pagination.TotalPages, occurrences.Pagination.TotalCount)
	if status != "" {
		_, _ = fmt.Fprintf(out, " · %s only", status)
	}
	_, _ = fmt.Fprintln(out)
	return nil
}

func renderSecurityOccurrenceTable(out io.Writer, occurrences []client.SecurityOccurrence) {
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Cluster", "Workload", "Container", "Image", "Installed", "Fixed", "State", "Disposition", "Last seen", "Occurrence ID"})
	for _, occurrence := range occurrences {
		workload := strings.TrimSpace(strings.Join([]string{
			stringOrEmpty(occurrence.WorkloadNamespace) + "/" + stringOrEmpty(occurrence.WorkloadKind),
			stringOrEmpty(occurrence.WorkloadName),
		}, " "))
		if occurrence.WorkloadName == nil {
			workload = occurrence.ReportName + " (" + occurrence.ReportScope + ")"
		}
		writer.AppendRow(table.Row{
			occurrence.ClusterName,
			workload,
			stringOrEmpty(occurrence.ContainerName),
			stringOrEmpty(occurrence.ImageRef),
			stringOrEmpty(occurrence.InstalledVersion),
			stringOrEmpty(occurrence.FixedVersion),
			occurrence.ScanState,
			occurrence.EffectiveDisposition,
			formatTimeAgo(occurrence.LastSeenAt),
			occurrence.ID,
		})
	}
	writer.Render()
}

var securityDispositionsCmd = &cobra.Command{
	Use:   "dispositions",
	Short: "Vulnerability dispositions: the acknowledgements and accepted risks that shape the actionable set",
	Long: `List the organisation's vulnerability-disposition policies. A disposition
acknowledges a finding (it stays visible but leaves the actionable set) or
accepts its risk, for every occurrence its selector matches - one CVE and
package inside one add-on across the organisation.

Each policy has a lifecycle status: active, expiring (the review deadline
is near), fix_available (a fixed version exists, so the acceptance should be
revisited), mitigated (nothing matches any more), unmatched (nothing ever
matched), expired or revoked.

Examples:
  ankra security dispositions --status expiring --status fix_available
  ankra security dispositions --disposition accepted_risk --sort expires_at --order asc`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		search, _ := cmd.Flags().GetString("search")
		statuses, _ := cmd.Flags().GetStringSlice("status")
		dispositions, _ := cmd.Flags().GetStringSlice("disposition")
		sort, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		list, err := apiClient.ListSecurityDispositions(client.SecurityDispositionsOptions{
			Page:         page,
			PageSize:     pageSize,
			Search:       search,
			Statuses:     statuses,
			Dispositions: dispositions,
			Sort:         sort,
			Order:        order,
		})
		if err != nil {
			return fmt.Errorf("listing security dispositions: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecurityDispositions(cmd, list)
		return nil
	},
}

func renderSecurityDispositions(cmd *cobra.Command, list *client.SecurityDispositionList) {
	out := cmd.OutOrStdout()
	if len(list.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No dispositions match these filters.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Disposition", "Status", "CVE", "Package", "Add-on", "Active matches", "Fix available", "Expires", "Reason", "Policy ID"})
	needsAttention := 0
	for _, policy := range list.Result {
		if securityDispositionNeedsAttention(policy.Status) {
			needsAttention++
		}
		writer.AppendRow(table.Row{
			policy.Disposition,
			securityDispositionStatusCell(policy.Status),
			policy.Selector.CVEID,
			policy.Selector.PackageName,
			policy.Selector.AddonSlug,
			fmt.Sprintf("%d of %d", policy.ActiveMatchCount, policy.MatchedOccurrenceCount),
			policy.FixAvailableMatchCount,
			securityDispositionExpiryText(policy),
			truncateReason(policy.Reason, 48),
			policy.ID,
		})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d dispositions", list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
	if needsAttention > 0 {
		_, _ = fmt.Fprintf(out, " · %d on this page need review (expiring, fix available, unmatched or expired)", needsAttention)
	}
	_, _ = fmt.Fprintln(out)
}

func securityDispositionNeedsAttention(status string) bool {
	switch status {
	case "expiring", "fix_available", "unmatched", "expired":
		return true
	}
	return false
}

func securityDispositionStatusCell(status string) string {
	switch status {
	case "expiring", "fix_available":
		return text.FgYellow.Sprint(status)
	case "expired", "unmatched":
		return text.FgRed.Sprint(status)
	case "revoked":
		return text.Faint.Sprint(status)
	default:
		return status
	}
}

// securityDispositionExpiryText renders the review deadline in explicit
// tense; "when fixed" names the fix-availability trigger, "never" a policy
// with neither.
func securityDispositionExpiryText(policy client.SecurityDisposition) string {
	parts := []string{}
	if policy.ExpiresAt != nil && *policy.ExpiresAt != "" {
		if deadline, parseError := time.Parse(time.RFC3339, *policy.ExpiresAt); parseError == nil {
			if time.Now().UTC().After(deadline) {
				parts = append(parts, text.FgRed.Sprint("passed "+deadline.UTC().Format("2006-01-02")))
			} else {
				parts = append(parts, deadline.UTC().Format("2006-01-02"))
			}
		} else {
			parts = append(parts, *policy.ExpiresAt)
		}
	}
	if policy.ExpireWhenFixAvailable {
		parts = append(parts, "when fixed")
	}
	if len(parts) == 0 {
		return "never"
	}
	return strings.Join(parts, ", ")
}

// truncateReason folds whitespace and cuts at limit runes, never inside a
// multibyte character.
func truncateReason(reason string, limit int) string {
	reason = strings.Join(strings.Fields(reason), " ")
	runes := []rune(reason)
	if len(runes) <= limit {
		return reason
	}
	return string(runes[:limit-1]) + "…"
}

var securityDispositionsPreviewCmd = &cobra.Command{
	Use:   "preview",
	Short: "Show what a disposition would cover before writing it",
	Long: `Compute a disposition's blast radius without writing anything: how many
occurrences, findings, clusters and add-ons the selector would cover, what
it deliberately excludes (ambiguous or unattributed occurrences, ones with a
fix available when the policy expires on fix), the policies it overlaps, and
the actionable total before and after.

Anchor a new policy on one occurrence with --occurrence (an id from
'ankra security finding <id>'), or re-preview an existing policy with
--policy.

Example:
  ankra security dispositions preview --occurrence 4f1c... --disposition accepted_risk --expires-at 2026-12-31`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		request, err := securityDispositionPreviewRequestFromFlags(cmd)
		if err != nil {
			return err
		}
		preview, err := apiClient.PreviewSecurityDisposition(request)
		if err != nil {
			return fmt.Errorf("previewing the disposition: %w", err)
		}
		if rendered, err := renderStructured(cmd, preview); rendered || err != nil {
			return err
		}
		renderSecurityDispositionPreview(cmd.OutOrStdout(), preview)
		return nil
	},
}

func securityDispositionPreviewRequestFromFlags(cmd *cobra.Command) (client.SecurityDispositionPreviewRequest, error) {
	occurrenceID, _ := cmd.Flags().GetString("occurrence")
	policyID, _ := cmd.Flags().GetString("policy")
	scope, _ := cmd.Flags().GetString("scope")
	disposition, _ := cmd.Flags().GetString("disposition")
	expiresAtRaw, _ := cmd.Flags().GetString("expires-at")
	request := client.SecurityDispositionPreviewRequest{
		OccurrenceID: strings.TrimSpace(occurrenceID),
		PolicyID:     strings.TrimSpace(policyID),
		Scope:        strings.TrimSpace(scope),
	}
	if (request.OccurrenceID == "") == (request.PolicyID == "") {
		return request, withExitCode(exitUsage, fmt.Errorf("pass exactly one of --occurrence or --policy"))
	}
	normalizedDisposition, dispositionError := normalizeSecurityDisposition(disposition, request.PolicyID != "")
	if dispositionError != nil {
		return request, dispositionError
	}
	request.Disposition = normalizedDisposition
	if flag := cmd.Flags().Lookup("expire-when-fix-available"); flag != nil && flag.Changed {
		expireWhenFixAvailable, _ := cmd.Flags().GetBool("expire-when-fix-available")
		request.ExpireWhenFixAvailable = &expireWhenFixAvailable
	}
	expiresAt, expiresError := parseSecurityDeadline(expiresAtRaw)
	if expiresError != nil {
		return request, expiresError
	}
	request.ExpiresAt = expiresAt
	return request, nil
}

// normalizeSecurityDisposition accepts the two dispositions the platform
// stores. An edit of an existing policy may leave it blank (the stored one
// stands); a new policy must name one.
func normalizeSecurityDisposition(raw string, optional bool) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "acknowledged", "acknowledge", "ack":
		return "acknowledged", nil
	case "accepted_risk", "accepted-risk", "accept-risk", "accept_risk":
		return "accepted_risk", nil
	case "":
		if optional {
			return "", nil
		}
		return "", withExitCode(exitUsage, fmt.Errorf("--disposition is required: acknowledged or accepted_risk"))
	}
	return "", withExitCode(exitUsage, fmt.Errorf("--disposition must be acknowledged or accepted_risk, got %q", raw))
}

// parseSecurityDeadline reads a review deadline as RFC3339 or as a date,
// which means the end of that day in UTC so a policy set to expire "on the
// 31st" still covers the 31st.
func parseSecurityDeadline(raw string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if parsed, parseError := time.Parse(time.RFC3339, trimmed); parseError == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	if parsed, parseError := time.Parse("2006-01-02", trimmed); parseError == nil {
		endOfDay := parsed.UTC().Add(24*time.Hour - time.Second)
		return &endOfDay, nil
	}
	return nil, withExitCode(exitUsage, fmt.Errorf("--expires-at must be a date (2026-12-31) or an RFC3339 timestamp, got %q", raw))
}

func renderSecurityDispositionPreview(out io.Writer, preview *client.SecurityDispositionPreview) {
	selector := preview.Selector
	_, _ = fmt.Fprintf(out, "Selector: %s in %s (%s) · add-on %s\n",
		selector.CVEID, selector.PackageName, selector.PackageType, selector.AddonSlug)
	_, _ = fmt.Fprintf(out, "Would cover %d occurrences across %d findings, %d clusters and %d add-ons\n",
		preview.AffectedOccurrences, preview.AffectedFindings, preview.AffectedClusters, preview.AffectedAddons)
	_, _ = fmt.Fprintf(out, "Excluded: %d ambiguous · %d unattributed · %d with a fix available\n",
		preview.Exclusions.Ambiguous, preview.Exclusions.Unattributed, preview.Exclusions.FixAvailable)
	_, _ = fmt.Fprintf(out, "Actionable: %d -> %d (observed %d -> %d)\n",
		preview.Delta.ActionableBefore, preview.Delta.ActionableAfter,
		preview.Delta.ObservedBefore, preview.Delta.ObservedAfter)
	if len(preview.Overlaps) > 0 {
		_, _ = fmt.Fprintf(out, "Overlaps %d existing policies:\n", len(preview.Overlaps))
		for _, overlap := range preview.Overlaps {
			_, _ = fmt.Fprintf(out, "  %s %s %s (%s)\n", overlap.PolicyID, overlap.Disposition, overlap.Selector.CVEID, overlap.Kind)
		}
	}
}

var securityDispositionsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Acknowledge a finding or accept its risk across the organisation",
	Long: `Write a disposition anchored on one occurrence: acknowledged keeps the
finding visible but out of the actionable set; accepted_risk records a
decision to live with it, which needs the security.manage permission.

The preview prints first and the write asks for confirmation; --yes skips
the prompt. Give a --reason: it is what an auditor reads. --expires-at sets
a review deadline; --expire-when-fix-available ends the disposition the
moment a fixed version appears. --allow-unmatched stores a policy whose
anchor no longer matches anything.

Example:
  ankra security dispositions create --occurrence 4f1c... --disposition acknowledged --reason "tracked in PLA-812" --expires-at 2026-10-31`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		preview, err := securityDispositionPreviewRequestFromFlags(cmd)
		if err != nil {
			return err
		}
		if preview.OccurrenceID == "" {
			return withExitCode(exitUsage, fmt.Errorf("--occurrence is required; use preview --policy to inspect an existing policy"))
		}
		reason, _ := cmd.Flags().GetString("reason")
		allowUnmatched, _ := cmd.Flags().GetBool("allow-unmatched")
		yes, _ := cmd.Flags().GetBool("yes")
		structured, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		blastRadius, err := apiClient.PreviewSecurityDisposition(preview)
		if err != nil {
			return fmt.Errorf("previewing the disposition: %w", err)
		}
		narration := cmd.OutOrStdout()
		if structured != outputDefault {
			narration = cmd.ErrOrStderr()
		}
		renderSecurityDispositionPreview(narration, blastRadius)
		prompt := fmt.Sprintf("Record %s for %d occurrences? [y/N]: ", preview.Disposition, blastRadius.AffectedOccurrences)
		if confirmError := confirmPrompt(cmd.InOrStdin(), narration, prompt, yes); confirmError != nil {
			return confirmError
		}
		request := client.SecurityDispositionCreateRequest{
			OccurrenceID:   preview.OccurrenceID,
			Scope:          preview.Scope,
			Disposition:    preview.Disposition,
			Reason:         strings.TrimSpace(reason),
			ExpiresAt:      preview.ExpiresAt,
			AllowUnmatched: allowUnmatched,
		}
		if preview.ExpireWhenFixAvailable != nil {
			request.ExpireWhenFixAvailable = *preview.ExpireWhenFixAvailable
		}
		mutation, err := apiClient.CreateSecurityDisposition(request)
		if err != nil {
			return fmt.Errorf("recording the disposition: %w", err)
		}
		if rendered, err := renderStructured(cmd, mutation); rendered || err != nil {
			return err
		}
		renderSecurityDispositionMutation(cmd.OutOrStdout(), "Recorded", mutation)
		return nil
	},
}

func renderSecurityDispositionMutation(out io.Writer, verb string, mutation *client.SecurityDispositionMutation) {
	policy := mutation.Policy
	_, _ = fmt.Fprintf(out, "%s %s policy %s (%s): %s in %s · add-on %s · %d active matches · expires %s\n",
		verb, policy.Disposition, policy.ID, policy.Status,
		policy.Selector.CVEID, policy.Selector.PackageName, policy.Selector.AddonSlug,
		policy.ActiveMatchCount, securityDispositionExpiryText(policy))
}

var securityDispositionsUpdateCmd = &cobra.Command{
	Use:   "update <policy-id>",
	Short: "Edit a disposition's reason or review deadline",
	Long: `Edit one policy's reason, review deadline (--expires-at) or fix trigger
(--expire-when-fix-available). Members left unset stay as they are. Needs
the security.manage permission; the write asks for confirmation unless
--yes is passed.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		policyID := strings.TrimSpace(args[0])
		yes, _ := cmd.Flags().GetBool("yes")
		request := client.SecurityDispositionUpdateRequest{}
		if flag := cmd.Flags().Lookup("reason"); flag != nil && flag.Changed {
			reason, _ := cmd.Flags().GetString("reason")
			request.Reason = &reason
		}
		if flag := cmd.Flags().Lookup("expires-at"); flag != nil && flag.Changed {
			expiresAtRaw, _ := cmd.Flags().GetString("expires-at")
			if strings.TrimSpace(expiresAtRaw) == "" {
				return withExitCode(exitUsage, fmt.Errorf("--expires-at cannot be empty: the platform keeps a review deadline once set, so pass a new date, or revoke the policy and record it again without one"))
			}
			expiresAt, parseError := parseSecurityDeadline(expiresAtRaw)
			if parseError != nil {
				return parseError
			}
			request.ExpiresAt = expiresAt
		}
		if flag := cmd.Flags().Lookup("expire-when-fix-available"); flag != nil && flag.Changed {
			expireWhenFixAvailable, _ := cmd.Flags().GetBool("expire-when-fix-available")
			request.ExpireWhenFixAvailable = &expireWhenFixAvailable
		}
		if request.Reason == nil && request.ExpiresAt == nil && request.ExpireWhenFixAvailable == nil {
			return withExitCode(exitUsage, fmt.Errorf("nothing to change: pass --reason, --expires-at or --expire-when-fix-available"))
		}
		structured, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		narration := cmd.OutOrStdout()
		if structured != outputDefault {
			narration = cmd.ErrOrStderr()
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), narration, fmt.Sprintf("Update disposition policy %s? [y/N]: ", policyID), yes); confirmError != nil {
			return confirmError
		}
		mutation, err := apiClient.UpdateSecurityDisposition(policyID, request)
		if err != nil {
			return fmt.Errorf("updating the disposition: %w", err)
		}
		if rendered, err := renderStructured(cmd, mutation); rendered || err != nil {
			return err
		}
		renderSecurityDispositionMutation(cmd.OutOrStdout(), "Updated", mutation)
		return nil
	},
}

var securityDispositionsRevokeCmd = &cobra.Command{
	Use:   "revoke <policy-id>",
	Short: "Withdraw a disposition so its occurrences become actionable again",
	Long: `Revoke one policy. Every occurrence it covered returns to the actionable
set immediately. --reason is required and is what the audit trail keeps.
Needs the security.manage permission; asks for confirmation unless --yes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		policyID := strings.TrimSpace(args[0])
		reason, _ := cmd.Flags().GetString("reason")
		yes, _ := cmd.Flags().GetBool("yes")
		if strings.TrimSpace(reason) == "" {
			return withExitCode(exitUsage, fmt.Errorf("--reason is required"))
		}
		structured, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		narration := cmd.OutOrStdout()
		if structured != outputDefault {
			narration = cmd.ErrOrStderr()
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), narration, fmt.Sprintf("Revoke disposition policy %s? Its occurrences become actionable again. [y/N]: ", policyID), yes); confirmError != nil {
			return confirmError
		}
		mutation, err := apiClient.RevokeSecurityDisposition(policyID, client.SecurityDispositionRevokeRequest{Reason: strings.TrimSpace(reason)})
		if err != nil {
			return fmt.Errorf("revoking the disposition: %w", err)
		}
		if rendered, err := renderStructured(cmd, mutation); rendered || err != nil {
			return err
		}
		renderSecurityDispositionMutation(cmd.OutOrStdout(), "Revoked", mutation)
		return nil
	},
}

func registerSecurityFindingsFilterFlags(cmd *cobra.Command, defaultSort string, pageSize int, noun string) {
	cmd.Flags().String("search", "", "Match CVE id, package name or title")
	cmd.Flags().StringSlice("severity", nil, "Severity filter, repeatable: critical, high, medium, low, unknown")
	cmd.Flags().StringSlice("status", []string{"open", "acknowledged"}, "Status filter, repeatable: open, acknowledged, accepted_risk, resolved, or any")
	cmd.Flags().String("fixable", "any", "Fix availability: true, false or any")
	cmd.Flags().Bool("known-exploited", false, "Only CVEs CISA lists as exploited in the wild (KEV)")
	cmd.Flags().String("cluster", "", "Only "+noun+" observed on one cluster (name or id)")
	cmd.Flags().String("addon", "", "Only "+noun+" attributed to one add-on (slug)")
	cmd.Flags().String("namespace", "", "Only "+noun+" observed in one namespace")
	cmd.Flags().String("sort", defaultSort, "Sort key")
	cmd.Flags().String("order", "desc", "Sort order: asc or desc")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("page-size", pageSize, noun+" per page (max 100)")
}

func init() {
	securityCmd.AddCommand(securityWorkloadsCmd, securityDispositionsCmd)
	securityDispositionsCmd.AddCommand(securityDispositionsPreviewCmd, securityDispositionsCreateCmd,
		securityDispositionsUpdateCmd, securityDispositionsRevokeCmd)

	registerSecurityFindingsFilterFlags(securityWorkloadsCmd, "risk_score", 25, "workloads")
	securityWorkloadsCmd.Flags().Lookup("sort").Usage = "Sort key: risk_score, priority, severity, known_exploited, observed, actionable, fixable_severe, last_scan, workload_name, image_ref, cluster_name, addon_attribution"

	securityFindingCmd.Flags().String("status", "", "List occurrences in one scan state: active or resolved (the detail alone shows active ones)")
	securityFindingCmd.Flags().String("cluster", "", "List only the occurrences on one cluster (name or id)")
	securityFindingCmd.Flags().Int("page", 1, "Occurrence page number")
	securityFindingCmd.Flags().Int("page-size", 50, "Occurrences per page (max 100)")

	securityDispositionsCmd.Flags().String("search", "", "Match CVE id, package name, add-on or reason")
	securityDispositionsCmd.Flags().StringSlice("status", nil, "Lifecycle filter, repeatable: active, expiring, fix_available, mitigated, unmatched, expired, revoked")
	securityDispositionsCmd.Flags().StringSlice("disposition", nil, "Disposition filter, repeatable: acknowledged, accepted_risk")
	securityDispositionsCmd.Flags().String("sort", "updated_at", "Sort key: updated_at, created_at, expires_at, matched_occurrence_count, status, disposition")
	securityDispositionsCmd.Flags().String("order", "desc", "Sort order: asc or desc")
	securityDispositionsCmd.Flags().Int("page", 1, "Page number")
	securityDispositionsCmd.Flags().Int("page-size", 50, "Dispositions per page (max 100)")

	for _, command := range []*cobra.Command{securityDispositionsPreviewCmd, securityDispositionsCreateCmd} {
		command.Flags().String("occurrence", "", "Occurrence id to anchor the disposition on (from 'ankra security finding <id>')")
		command.Flags().String("scope", "organisation_addon", "Selector scope; organisation_addon is the only scope the platform accepts today")
		command.Flags().String("disposition", "", "acknowledged or accepted_risk")
		command.Flags().String("expires-at", "", "Review deadline as a date (2026-12-31) or RFC3339 timestamp")
		command.Flags().Bool("expire-when-fix-available", false, "End the disposition the moment a fixed version appears")
	}
	securityDispositionsPreviewCmd.Flags().String("policy", "", "Existing policy id to re-preview instead of an occurrence")
	securityDispositionsCreateCmd.Flags().String("reason", "", "Why the finding is acknowledged or its risk accepted (kept for audit)")
	securityDispositionsCreateCmd.Flags().Bool("allow-unmatched", false, "Store the policy even when its anchor no longer matches any occurrence")
	securityDispositionsCreateCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	securityDispositionsUpdateCmd.Flags().String("reason", "", "New reason")
	securityDispositionsUpdateCmd.Flags().String("expires-at", "", "New review deadline as a date (2026-12-31) or RFC3339 timestamp; a deadline cannot be cleared once set")
	securityDispositionsUpdateCmd.Flags().Bool("expire-when-fix-available", false, "Whether a fixed version ends the disposition")
	securityDispositionsUpdateCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	securityDispositionsRevokeCmd.Flags().String("reason", "", "Why the disposition is withdrawn (required, kept for audit)")
	securityDispositionsRevokeCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(securityWorkloadsCmd, securityDispositionsCmd, securityDispositionsPreviewCmd,
		securityDispositionsCreateCmd, securityDispositionsUpdateCmd, securityDispositionsRevokeCmd)
}

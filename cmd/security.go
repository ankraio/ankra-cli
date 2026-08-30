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

var securityCmd = &cobra.Command{
	Use:   "security",
	Short: "Read the Security Center: exploited-in-the-wild findings, fleet posture and scanner coverage",
	Long: `Read the organisation's Security Center - the same data the portal shows,
for your own reporting and automation.

Every finding carries its exploitation intelligence: whether CISA lists the
CVE in its Known Exploited Vulnerabilities (KEV) catalog, with CISA's
remediation deadline and required action, and the FIRST EPSS probability
that it is exploited in the next 30 days. Findings sort by exploitability by
default - CISA listing first, then EPSS, then severity - so the top of the
list is what is actually being exploited, not what merely has the highest
CVSS score.

Pass -o json (or yaml) for the full API document.`,
}

var securityOverviewCmd = &cobra.Command{
	Use:   "overview",
	Short: "Fleet security summary: totals, CISA KEV exposure, scanner coverage and remediation candidates",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		addonSlug, _ := cmd.Flags().GetString("addon")
		options := client.SecurityOverviewOptions{AddonSlug: addonSlug}
		if clusterFlag != "" {
			clusterID, err := resolveClusterID(clusterFlag)
			if err != nil {
				return err
			}
			options.ClusterID = clusterID
		}
		overview, err := apiClient.GetSecurityOverview(options)
		if err != nil {
			return fmt.Errorf("reading security overview: %w", err)
		}
		if rendered, err := renderStructured(cmd, overview); rendered || err != nil {
			return err
		}
		renderSecurityOverview(cmd, overview)
		return nil
	},
}

var securityFindingsCmd = &cobra.Command{
	Use:   "findings",
	Short: "List findings, exploited-in-the-wild first",
	Long: `List the organisation's logical findings (one row per CVE and package,
deduplicated across clusters and workloads).

By default the list is the actionable set (open and acknowledged findings)
sorted by exploitability. Narrow it with --known-exploited to the CVEs CISA
lists as exploited in the wild, or with --severity, --status, --fixable,
--cluster, --addon and --namespace.

Examples:
  ankra security findings --known-exploited
  ankra security findings --severity critical --fixable true --sort epss
  ankra security findings --cluster production --status any -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		options, err := securityFindingsOptionsFromFlags(cmd)
		if err != nil {
			return err
		}
		list, err := apiClient.ListSecurityFindings(options)
		if err != nil {
			return fmt.Errorf("listing security findings: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecurityFindings(cmd, list, options)
		return nil
	},
}

var securityFindingCmd = &cobra.Command{
	Use:   "finding <finding-id>",
	Short: "Show one finding with CISA's guidance and every current occurrence",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, err := apiClient.GetSecurityFinding(args[0])
		if err != nil {
			return fmt.Errorf("reading security finding: %w", err)
		}
		if rendered, err := renderStructured(cmd, detail); rendered || err != nil {
			return err
		}
		renderSecurityFindingDetail(cmd, detail)
		return nil
	},
}

var securityClustersCmd = &cobra.Command{
	Use:   "clusters",
	Short: "Per-cluster security posture and scanner freshness",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page-size")
		search, _ := cmd.Flags().GetString("search")
		status, _ := cmd.Flags().GetString("status")
		sort, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		list, err := apiClient.ListSecurityClusters(client.SecurityClustersOptions{
			Page:     page,
			PageSize: pageSize,
			Search:   search,
			Status:   status,
			Sort:     sort,
			Order:    order,
		})
		if err != nil {
			return fmt.Errorf("listing cluster security posture: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecurityClusters(cmd, list)
		return nil
	},
}

// securityFindingsOptionsFromFlags turns the findings flags into API options.
// --status defaults to the actionable set; "any" lifts the status filter,
// and --fixable accepts true, false or any.
func securityFindingsOptionsFromFlags(cmd *cobra.Command) (client.SecurityFindingsOptions, error) {
	page, _ := cmd.Flags().GetInt("page")
	pageSize, _ := cmd.Flags().GetInt("page-size")
	search, _ := cmd.Flags().GetString("search")
	severities, _ := cmd.Flags().GetStringSlice("severity")
	statuses, _ := cmd.Flags().GetStringSlice("status")
	fixableRaw, _ := cmd.Flags().GetString("fixable")
	knownExploited, _ := cmd.Flags().GetBool("known-exploited")
	clusterFlag, _ := cmd.Flags().GetString("cluster")
	addonSlug, _ := cmd.Flags().GetString("addon")
	namespace, _ := cmd.Flags().GetString("namespace")
	sort, _ := cmd.Flags().GetString("sort")
	order, _ := cmd.Flags().GetString("order")

	options := client.SecurityFindingsOptions{
		Page:       page,
		PageSize:   pageSize,
		Search:     search,
		Severities: severities,
		AddonSlug:  addonSlug,
		Namespace:  namespace,
		Sort:       sort,
		Order:      order,
	}
	for _, status := range statuses {
		if strings.EqualFold(strings.TrimSpace(status), "any") {
			options.Statuses = nil
			break
		}
		options.Statuses = append(options.Statuses, status)
	}
	switch strings.ToLower(strings.TrimSpace(fixableRaw)) {
	case "", "any":
	case "true", "yes":
		fixable := true
		options.Fixable = &fixable
	case "false", "no":
		fixable := false
		options.Fixable = &fixable
	default:
		return options, withExitCode(exitUsage, fmt.Errorf("--fixable must be true, false or any, got %q", fixableRaw))
	}
	if knownExploited {
		listed := true
		options.KnownExploited = &listed
	}
	if clusterFlag != "" {
		clusterID, err := resolveClusterID(clusterFlag)
		if err != nil {
			return options, err
		}
		options.ClusterID = clusterID
	}
	return options, nil
}

// exploitationCell renders a finding's exploitation intelligence for a table:
// the KEV marker with CISA's deadline in explicit tense, a ransomware marker,
// and the EPSS probability. A finding with neither renders "-", which means
// "no exploit data", not "not exploited".
func exploitationCell(intelligence client.SecurityExploitIntelligence) string {
	parts := []string{}
	if intelligence.KnownExploited {
		parts = append(parts, text.FgRed.Sprint("KEV"))
		if deadline := kevDeadlineText(intelligence.KevDueDate); deadline != "" {
			parts = append(parts, deadline)
		}
		if intelligence.KevRansomwareUse {
			parts = append(parts, text.FgRed.Sprint("ransomware"))
		}
	}
	if epss := epssText(intelligence.EPSSScore, intelligence.EPSSPercentile); epss != "" {
		parts = append(parts, epss)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " · ")
}

// kevDeadlineText renders CISA's remediation deadline with explicit tense so
// a passed deadline never reads as a future one.
func kevDeadlineText(dueDate *string) string {
	if dueDate == nil || *dueDate == "" {
		return ""
	}
	due, parseError := time.Parse("2006-01-02", *dueDate)
	if parseError != nil {
		return "due " + *dueDate
	}
	if time.Now().UTC().After(due.Add(24 * time.Hour)) {
		return text.FgRed.Sprintf("deadline passed %s", *dueDate)
	}
	return "due " + *dueDate
}

// epssText renders the EPSS probability as a percentage with its rank in
// the upper half ("EPSS 94% · top 1%"); nothing when the platform holds no
// score.
func epssText(score *float64, percentile *float64) string {
	if score == nil {
		return ""
	}
	probability := fmt.Sprintf("%.0f%%", *score*100)
	if *score < 0.1 && *score > 0 {
		probability = fmt.Sprintf("%.1f%%", *score*100)
	}
	if percentile != nil && *percentile >= 0.5 {
		top := int((1-*percentile)*100 + 0.5)
		if top < 1 {
			top = 1
		}
		return fmt.Sprintf("EPSS %s (top %d%%)", probability, top)
	}
	return "EPSS " + probability
}

func severityCell(severity string) string {
	switch strings.ToUpper(severity) {
	case "CRITICAL":
		return text.FgHiRed.Sprint("CRITICAL")
	case "HIGH":
		return text.FgRed.Sprint("HIGH")
	case "MEDIUM":
		return text.FgYellow.Sprint("MEDIUM")
	case "LOW":
		return text.FgBlue.Sprint("LOW")
	default:
		return strings.ToUpper(severity)
	}
}

func optionalTimeAgo(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return formatTimeAgo(*value)
}

func newSecurityTable(out io.Writer) table.Writer {
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	return writer
}

func renderSecurityOverview(cmd *cobra.Command, overview *client.SecurityOverview) {
	out := cmd.OutOrStdout()
	verdict := "Fleet is clear"
	if overview.Totals.Actionable > 0 {
		verdict = "Action required"
	}
	_, _ = fmt.Fprintf(out, "Fleet security: %s\n", verdict)
	_, _ = fmt.Fprintf(out, "  %d actionable · %d with a fix available · %d acknowledged · %d accepted risk · %d resolved\n",
		overview.Totals.Actionable, overview.FixableSevere, overview.Totals.Acknowledged,
		overview.Totals.AcceptedRisk, overview.Totals.Resolved)
	_, _ = fmt.Fprintf(out, "  observed by severity: %d critical · %d high · %d medium · %d low · %d unknown\n",
		overview.Severity.Critical, overview.Severity.High, overview.Severity.Medium,
		overview.Severity.Low, overview.Severity.Unknown)
	_, _ = fmt.Fprintln(out)
	renderKnownExploitedSummary(cmd, overview)
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "Scanner coverage: %s - %d fresh · %d stale · %d unscanned of %d clusters",
		overview.Scanner.Status, overview.Coverage.FreshClusters, overview.Coverage.StaleClusters,
		overview.Coverage.UnscannedClusters, overview.Coverage.TotalClusters)
	if overview.Coverage.LatestReportAt != nil {
		_, _ = fmt.Fprintf(out, " (latest report %s)", formatTimeAgo(*overview.Coverage.LatestReportAt))
	}
	_, _ = fmt.Fprintln(out)
	if len(overview.TopRemediation) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Top remediation candidates (known exploited first, then severity, fix availability and reach):")
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"#", "CVE", "Severity", "Exploitation", "Package", "Actionable", "Fixable", "Clusters", "Finding ID"})
	for index, candidate := range overview.TopRemediation {
		writer.AppendRow(table.Row{
			index + 1,
			candidate.CVEID,
			severityCell(candidate.Severity),
			exploitationCell(candidate.SecurityExploitIntelligence),
			candidate.PackageName,
			candidate.ActionableCount,
			candidate.FixableOccurrences,
			candidate.AffectedClusters,
			candidate.FindingID,
		})
	}
	writer.Render()
}

// renderKnownExploitedSummary is the CLI's copy of the portal's
// exploited-in-the-wild banner. It never renders "nothing exploited" from a
// catalog that has not been synced.
func renderKnownExploitedSummary(cmd *cobra.Command, overview *client.SecurityOverview) {
	out := cmd.OutOrStdout()
	if overview.Intelligence.KevSyncedAt == nil {
		_, _ = fmt.Fprintln(out, "Known exploited (CISA KEV): unknown - the CISA catalog has not been synced yet")
		return
	}
	if overview.KnownExploitedFindings == 0 {
		_, _ = fmt.Fprintf(out, "Known exploited (CISA KEV): none of the actionable findings are on CISA's catalog (%d CVEs listed, synced %s)\n",
			overview.Intelligence.KevListed, formatTimeAgo(*overview.Intelligence.KevSyncedAt))
		return
	}
	_, _ = fmt.Fprintln(out, text.FgRed.Sprintf("%d vulnerabilities in this fleet are being exploited in the wild (CISA KEV)",
		overview.KnownExploitedFindings))
	_, _ = fmt.Fprintf(out, "  %d actionable findings · %d occurrences · %d past CISA's remediation deadline · %d used in ransomware campaigns\n",
		overview.KnownExploitedFindings, overview.KnownExploited, overview.KnownExploitedOverdue, overview.KnownExploitedRansomware)
	_, _ = fmt.Fprintf(out, "  catalog: %d CVEs listed, synced %s. Fix these before anything ranked by CVSS alone:\n",
		overview.Intelligence.KevListed, formatTimeAgo(*overview.Intelligence.KevSyncedAt))
	_, _ = fmt.Fprintln(out, "  ankra security findings --known-exploited")
}

func renderSecurityFindings(cmd *cobra.Command, list *client.SecurityFindingList, options client.SecurityFindingsOptions) {
	out := cmd.OutOrStdout()
	if len(list.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No findings match these filters.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"CVE", "Severity", "Exploitation", "Package", "Clusters", "Workloads", "Open", "Fixable", "Last seen", "Finding ID"})
	for _, finding := range list.Result {
		writer.AppendRow(table.Row{
			finding.CVEID,
			severityCell(finding.Severity),
			exploitationCell(finding.SecurityExploitIntelligence),
			finding.PackageName + " (" + finding.PackageType + ")",
			finding.AffectedClusters,
			finding.AffectedWorkloads,
			finding.DispositionCounts.Open,
			finding.FixableOccurrences,
			formatTimeAgo(finding.LastSeenAt),
			finding.ID,
		})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d findings", list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
	if options.Sort != "" {
		_, _ = fmt.Fprintf(out, " · sorted by %s %s", options.Sort, options.Order)
	}
	_, _ = fmt.Fprintln(out)
	if list.Intelligence.KevSyncedAt == nil {
		_, _ = fmt.Fprintln(out, "The CISA KEV catalog has not been synced yet, so known-exploited status is unknown for these findings.")
		return
	}
	if options.KnownExploited != nil && *options.KnownExploited {
		return
	}
	for _, facet := range list.Facets.Exploited {
		if facet.Value == "known_exploited" && facet.Count > 0 {
			_, _ = fmt.Fprintf(out, "%d findings are on CISA's Known Exploited Vulnerabilities catalog: ankra security findings --known-exploited\n", facet.Count)
		}
	}
}

func renderSecurityFindingDetail(cmd *cobra.Command, detail *client.SecurityFindingDetail) {
	out := cmd.OutOrStdout()
	finding := detail.Finding
	_, _ = fmt.Fprintf(out, "%s  %s  %s (%s)\n", finding.CVEID, severityCell(finding.Severity), finding.PackageName, finding.PackageType)
	if finding.Title != nil && *finding.Title != "" {
		_, _ = fmt.Fprintln(out, *finding.Title)
	}
	_, _ = fmt.Fprintln(out)
	if finding.KnownExploited {
		_, _ = fmt.Fprintln(out, text.FgRed.Sprint("Known exploited in the wild (CISA KEV)"))
		if finding.KevVulnerabilityName != nil {
			vendor := strings.TrimSpace(strings.Join([]string{stringOrEmpty(finding.KevVendorProject), stringOrEmpty(finding.KevProduct)}, " "))
			if vendor != "" {
				_, _ = fmt.Fprintf(out, "  %s (%s)\n", *finding.KevVulnerabilityName, vendor)
			} else {
				_, _ = fmt.Fprintf(out, "  %s\n", *finding.KevVulnerabilityName)
			}
		}
		if finding.KevDateAdded != nil {
			_, _ = fmt.Fprintf(out, "  listed since %s\n", *finding.KevDateAdded)
		}
		if deadline := kevDeadlineText(finding.KevDueDate); deadline != "" {
			_, _ = fmt.Fprintf(out, "  CISA %s\n", deadline)
		}
		if finding.KevRansomwareUse {
			_, _ = fmt.Fprintln(out, "  "+text.FgRed.Sprint("used in ransomware campaigns"))
		}
		if finding.KevRequiredAction != nil {
			_, _ = fmt.Fprintf(out, "  CISA required action: %s\n", *finding.KevRequiredAction)
		}
	} else {
		_, _ = fmt.Fprintln(out, "Not on CISA's Known Exploited Vulnerabilities catalog")
	}
	if epss := epssText(finding.EPSSScore, finding.EPSSPercentile); epss != "" {
		_, _ = fmt.Fprintf(out, "  %s exploitation probability in the next 30 days\n", epss)
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "%d clusters · %d workloads · %d occurrences (%d open, %d acknowledged, %d accepted risk, %d resolved) · %d with a fix\n",
		finding.AffectedClusters, finding.AffectedWorkloads, finding.Occurrences,
		finding.DispositionCounts.Open, finding.DispositionCounts.Acknowledged,
		finding.DispositionCounts.AcceptedRisk, finding.DispositionCounts.Resolved,
		finding.FixableOccurrences)
	if finding.PrimaryLink != nil && *finding.PrimaryLink != "" {
		_, _ = fmt.Fprintf(out, "Advisory: %s\n", *finding.PrimaryLink)
	}
	if len(detail.Occurrences) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out)
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Cluster", "Workload", "Container", "Image", "Installed", "Fixed", "State", "Disposition"})
	for _, occurrence := range detail.Occurrences {
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
		})
	}
	writer.Render()
}

func renderSecurityClusters(cmd *cobra.Command, list *client.SecurityClusterList) {
	out := cmd.OutOrStdout()
	if len(list.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No clusters match these filters.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Cluster", "Environment", "Scanner", "Posture", "Actionable", "Known exploited", "Critical", "High", "Fixable severe", "Latest report"})
	for _, cluster := range list.Result {
		knownExploited := fmt.Sprintf("%d", cluster.KnownExploited)
		if cluster.KnownExploited > 0 {
			knownExploited = text.FgRed.Sprint(knownExploited)
		}
		writer.AppendRow(table.Row{
			cluster.ClusterName,
			stringOrEmpty(cluster.Environment),
			cluster.ScannerStatus,
			cluster.PostureStatus,
			cluster.Actionable,
			knownExploited,
			cluster.Severity.Critical,
			cluster.Severity.High,
			cluster.FixableSevere,
			optionalTimeAgo(cluster.LatestReportAt),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Page %d of %d · %d clusters\n", list.Pagination.Page, list.Pagination.TotalPages, list.Pagination.TotalCount)
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

var securityAdvisoryCmd = &cobra.Command{
	Use:   "advisory <cve-id>",
	Short: "Show the platform's advisory for one CVE: the parsed NVD/OSV record, CISA's guidance and your exposure",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		advisory, err := apiClient.GetSecurityAdvisory(strings.ToUpper(strings.TrimSpace(args[0])))
		if err != nil {
			return fmt.Errorf("reading security advisory: %w", err)
		}
		if rendered, err := renderStructured(cmd, advisory); rendered || err != nil {
			return err
		}
		renderSecurityAdvisory(cmd, advisory)
		return nil
	},
}

// advisoryRangeText says one affected range in words: a CNA range with
// status unaffected names the build a fix ships in, so it reads "fixed
// from"; an open-ended affected range reads "and later".
func advisoryRangeText(versionRange client.SecurityAdvisoryVersionRange) string {
	introduced := versionRange.Introduced
	if introduced == "0" {
		introduced = ""
	}
	switch {
	case versionRange.Status == "unaffected" && introduced != "":
		return "fixed from " + introduced
	case versionRange.Status == "unaffected":
		return "not affected"
	case versionRange.Fixed != "" && introduced != "":
		return fmt.Sprintf("%s before %s (fixed in %s)", introduced, versionRange.Fixed, versionRange.Fixed)
	case versionRange.Fixed != "":
		return fmt.Sprintf("all versions before %s (fixed in %s)", versionRange.Fixed, versionRange.Fixed)
	case versionRange.LastAffected != "" && introduced != "":
		return fmt.Sprintf("%s through %s", introduced, versionRange.LastAffected)
	case versionRange.LastAffected != "":
		return "up to " + versionRange.LastAffected
	case introduced != "":
		return introduced + " and later"
	default:
		return "all versions"
	}
}

func advisoryAffectedSubject(entry client.SecurityAdvisoryAffected) string {
	product := entry.Product
	if entry.Vendor != "" && !strings.HasPrefix(strings.ToLower(entry.Product), strings.ToLower(entry.Vendor)) {
		product = strings.TrimSpace(entry.Vendor + " " + entry.Product)
	}
	if product == "" {
		product = entry.Vendor
	}
	if entry.Package != "" {
		name := entry.Package
		if entry.Ecosystem != "" {
			name = entry.Ecosystem + ": " + entry.Package
		}
		if product != "" {
			return name + " · " + product
		}
		return name
	}
	if product != "" {
		return product
	}
	if entry.Repository != "" {
		return entry.Repository
	}
	return "Unnamed product"
}

func renderSecurityAdvisory(cmd *cobra.Command, advisory *client.SecurityAdvisory) {
	out := cmd.OutOrStdout()
	record := advisory.Advisory
	headline := advisory.CVEID
	if record != nil && record.CVSSScore != nil && record.CVSSSeverity != nil {
		headline += fmt.Sprintf("  %s  CVSS %.1f", severityCell(*record.CVSSSeverity), *record.CVSSScore)
		if record.CVSSVersion != nil {
			headline += " (" + *record.CVSSVersion + ")"
		}
	}
	_, _ = fmt.Fprintln(out, headline)
	if record != nil && record.Title != nil && *record.Title != "" {
		_, _ = fmt.Fprintln(out, *record.Title)
	} else if advisory.Intelligence.KevVulnerabilityName != nil {
		_, _ = fmt.Fprintln(out, *advisory.Intelligence.KevVulnerabilityName)
	}
	_, _ = fmt.Fprintln(out)

	switch advisory.Status {
	case "pending":
		_, _ = fmt.Fprintln(out, "Ankra is fetching this advisory from NVD and OSV now - it was queued at the front of the platform's read queue; ask again in a minute.")
	case "missing":
		_, _ = fmt.Fprintln(out, "No public advisory record for this CVE yet: neither NVD nor OSV holds one. Reserved or very new ids look like this until the CNA publishes; Ankra re-checks every two weeks.")
	case "error":
		_, _ = fmt.Fprintln(out, "The advisory sources could not be read; Ankra retries within the hour.")
		if advisory.FetchError != nil {
			_, _ = fmt.Fprintf(out, "  %s\n", *advisory.FetchError)
		}
	}
	if record != nil {
		if record.Description != nil && *record.Description != "" {
			_, _ = fmt.Fprintln(out, *record.Description)
			_, _ = fmt.Fprintln(out)
		}
	}

	intelligence := advisory.Intelligence
	if intelligence.KnownExploited {
		_, _ = fmt.Fprintln(out, text.FgRed.Sprint("Known exploited in the wild (CISA KEV)"))
		if intelligence.KevVulnerabilityName != nil {
			vendor := strings.TrimSpace(strings.Join([]string{stringOrEmpty(intelligence.KevVendorProject), stringOrEmpty(intelligence.KevProduct)}, " "))
			if vendor != "" {
				_, _ = fmt.Fprintf(out, "  %s (%s)\n", *intelligence.KevVulnerabilityName, vendor)
			} else {
				_, _ = fmt.Fprintf(out, "  %s\n", *intelligence.KevVulnerabilityName)
			}
		}
		if intelligence.KevDateAdded != nil {
			_, _ = fmt.Fprintf(out, "  listed since %s\n", *intelligence.KevDateAdded)
		}
		if deadline := kevDeadlineText(intelligence.KevDueDate); deadline != "" {
			_, _ = fmt.Fprintf(out, "  CISA %s\n", deadline)
		}
		if intelligence.KevRansomwareUse {
			_, _ = fmt.Fprintln(out, "  "+text.FgRed.Sprint("used in ransomware campaigns"))
		}
		if intelligence.KevRequiredAction != nil {
			_, _ = fmt.Fprintf(out, "  CISA required action: %s\n", *intelligence.KevRequiredAction)
		}
	} else if advisory.IntelligenceStatus.KevSyncedAt != nil {
		_, _ = fmt.Fprintln(out, "Not on CISA's Known Exploited Vulnerabilities catalog")
	} else {
		_, _ = fmt.Fprintln(out, "CISA KEV status unknown: the platform has not synced the catalog yet")
	}
	if epss := epssText(intelligence.EPSSScore, intelligence.EPSSPercentile); epss != "" {
		_, _ = fmt.Fprintf(out, "  %s exploitation probability in the next 30 days\n", epss)
	}
	if record != nil && record.SSVC != nil {
		_, _ = fmt.Fprintf(out, "  CISA SSVC: exploitation %s · automatable %s · technical impact %s\n",
			record.SSVC.Exploitation, record.SSVC.Automatable, record.SSVC.TechnicalImpact)
	}
	_, _ = fmt.Fprintln(out)

	if record != nil {
		if len(record.CWEIDs) > 0 || len(record.Aliases) > 0 {
			if len(record.CWEIDs) > 0 {
				_, _ = fmt.Fprintf(out, "Weaknesses: %s\n", strings.Join(record.CWEIDs, ", "))
			}
			if len(record.Aliases) > 0 {
				_, _ = fmt.Fprintf(out, "Also known as: %s\n", strings.Join(record.Aliases, ", "))
			}
		}
		if record.CVSSVector != nil {
			_, _ = fmt.Fprintf(out, "Vector: %s\n", *record.CVSSVector)
		}
		if len(record.Affected) > 0 {
			_, _ = fmt.Fprintln(out)
			_, _ = fmt.Fprintln(out, "Affected versions")
			for _, entry := range record.Affected {
				subject := advisoryAffectedSubject(entry)
				switch {
				case len(entry.Ranges) > 0:
					parts := make([]string, 0, len(entry.Ranges))
					for _, versionRange := range entry.Ranges {
						parts = append(parts, advisoryRangeText(versionRange))
					}
					_, _ = fmt.Fprintf(out, "  %s: %s\n", subject, strings.Join(parts, "; "))
				case len(entry.Versions) > 0:
					_, _ = fmt.Fprintf(out, "  %s: versions %s\n", subject, strings.Join(entry.Versions, ", "))
				case entry.DefaultStatus == "affected":
					_, _ = fmt.Fprintf(out, "  %s: all versions affected\n", subject)
				case entry.DefaultStatus == "unaffected":
					_, _ = fmt.Fprintf(out, "  %s: not affected\n", subject)
				default:
					_, _ = fmt.Fprintf(out, "  %s: no version ranges published\n", subject)
				}
			}
		}
	}

	fleet := advisory.Fleet
	_, _ = fmt.Fprintln(out)
	if len(fleet.Findings) == 0 {
		_, _ = fmt.Fprintln(out, "In your fleet: no current findings for this CVE")
	} else {
		_, _ = fmt.Fprintf(out, "In your fleet: %d findings · %d occurrences (%d with a fix) · %d clusters · %d workloads\n",
			len(fleet.Findings), fleet.Occurrences, fleet.FixableOccurrences, fleet.AffectedClusters, fleet.AffectedWorkloads)
		writer := newSecurityTable(out)
		writer.AppendHeader(table.Row{"Package", "Type", "Severity", "Occurrences", "Fixable", "Clusters", "Last seen", "Finding"})
		for _, finding := range fleet.Findings {
			writer.AppendRow(table.Row{
				finding.PackageName, finding.PackageType, severityCell(finding.Severity),
				finding.Occurrences, finding.FixableOccurrences, finding.AffectedClusters,
				optionalTimeAgo(&finding.LastSeenAt), finding.ID,
			})
		}
		writer.Render()
	}

	if record != nil && len(record.References) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "References (%d)\n", len(record.References))
		for _, reference := range record.References {
			tag := "Other"
			if len(reference.Tags) > 0 {
				tag = reference.Tags[0]
			}
			_, _ = fmt.Fprintf(out, "  [%s] %s\n", tag, reference.URL)
		}
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "Parsed by Ankra from NVD (%s) and OSV (%s); records are re-read every 14 days.\n",
		advisorySourceText(advisory.Sources.NVDFetchedAt, advisory.Sources.NVDURL),
		advisorySourceText(advisory.Sources.OSVFetchedAt, advisory.Sources.OSVURL))
}

func advisorySourceText(fetchedAt *string, url string) string {
	if fetchedAt == nil {
		return "no record, " + url
	}
	return "read " + optionalTimeAgo(fetchedAt) + ", " + url
}

func init() {
	rootCmd.AddCommand(securityCmd)
	securityCmd.AddCommand(securityOverviewCmd, securityFindingsCmd, securityFindingCmd, securityAdvisoryCmd, securityClustersCmd)

	securityOverviewCmd.Flags().String("cluster", "", "Scope the overview to one cluster (name or id)")
	securityOverviewCmd.Flags().String("addon", "", "Scope the overview to one add-on (slug)")

	securityFindingsCmd.Flags().String("search", "", "Match CVE id, package name or title")
	securityFindingsCmd.Flags().StringSlice("severity", nil, "Severity filter, repeatable: critical, high, medium, low, unknown")
	securityFindingsCmd.Flags().StringSlice("status", []string{"open", "acknowledged"}, "Status filter, repeatable: open, acknowledged, accepted_risk, resolved, or any")
	securityFindingsCmd.Flags().String("fixable", "any", "Fix availability: true, false or any")
	securityFindingsCmd.Flags().Bool("known-exploited", false, "Only CVEs CISA lists as exploited in the wild (KEV)")
	securityFindingsCmd.Flags().String("cluster", "", "Only findings observed on one cluster (name or id)")
	securityFindingsCmd.Flags().String("addon", "", "Only findings attributed to one add-on (slug)")
	securityFindingsCmd.Flags().String("namespace", "", "Only findings observed in one namespace")
	securityFindingsCmd.Flags().String("sort", "exploitability", "Sort key: exploitability, epss, known_exploited, severity, first_seen_at, last_seen_at, affected_clusters, occurrences, package_name, cve_id")
	securityFindingsCmd.Flags().String("order", "desc", "Sort order: asc or desc")
	securityFindingsCmd.Flags().Int("page", 1, "Page number")
	securityFindingsCmd.Flags().Int("page-size", 25, "Findings per page (max 100)")

	securityClustersCmd.Flags().String("search", "", "Match cluster name")
	securityClustersCmd.Flags().String("status", "", "Scanner or posture status filter: fresh, stale, unscanned, clean, critical, high, degraded")
	securityClustersCmd.Flags().String("sort", "actionable", "Sort key: actionable, known_exploited, severity, observed, accepted_risk, latest_report_at, status, name")
	securityClustersCmd.Flags().String("order", "desc", "Sort order: asc or desc")
	securityClustersCmd.Flags().Int("page", 1, "Page number")
	securityClustersCmd.Flags().Int("page-size", 50, "Clusters per page (max 100)")

	registerStructuredOutputFlags(securityOverviewCmd, securityFindingsCmd, securityFindingCmd, securityAdvisoryCmd, securityClustersCmd)
}

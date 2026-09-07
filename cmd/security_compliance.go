package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var securityComplianceCmd = &cobra.Command{
	Use:   "compliance",
	Short: "Fleet benchmark compliance per cluster, and the framework reports behind it",
	Long: `Show every cluster's benchmark compliance (CIS, NSA/CISA and the other
specs the scanner evaluates) with pass and fail totals and the config-audit
posture. A cluster the scanner does not run on, or whose agent could not be
reached, reads not_available with the reason rather than as compliant.

Subcommands cover the compliance frameworks (GDPR, ISO 27001, SOC 2, NIST
CSF): the catalogue, switching one on or off, its control-by-control report,
and the evidence export.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		overview, err := apiClient.GetSecurityComplianceOverview()
		if err != nil {
			return fmt.Errorf("reading compliance overview: %w", err)
		}
		if rendered, err := renderStructured(cmd, overview); rendered || err != nil {
			return err
		}
		renderSecurityComplianceOverview(cmd.OutOrStdout(), overview)
		return nil
	},
}

func renderSecurityComplianceOverview(out io.Writer, overview *client.SecurityComplianceOverview) {
	if len(overview.Clusters) == 0 {
		_, _ = fmt.Fprintln(out, "No clusters in the organisation.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Cluster", "Status", "Benchmark", "Pass", "Fail", "Failed checks", "Config audit failures", "Updated"})
	unavailable := 0
	for _, cluster := range overview.Clusters {
		if cluster.Status != "available" {
			unavailable++
			writer.AppendRow(table.Row{cluster.ClusterName, text.FgYellow.Sprint(cluster.Status), stringOrEmpty(cluster.Detail), "-", "-", "-", "-", "-"})
			continue
		}
		configAudit := "-"
		if cluster.ConfigAudit != nil {
			configAudit = fmt.Sprintf("%d (%dC %dH)", cluster.ConfigAudit.FailedCount, cluster.ConfigAudit.Severity.Critical, cluster.ConfigAudit.Severity.High)
		}
		if len(cluster.Benchmarks) == 0 {
			writer.AppendRow(table.Row{cluster.ClusterName, cluster.Status, "no benchmark reports yet", "-", "-", "-", configAudit, "-"})
			continue
		}
		for index, benchmark := range cluster.Benchmarks {
			name := cluster.ClusterName
			status := cluster.Status
			audit := configAudit
			if index > 0 {
				name, status, audit = "", "", ""
			}
			writer.AppendRow(table.Row{
				name, status, benchmark.Title, benchmark.PassCount, failCountCell(benchmark.FailCount),
				benchmark.FailedChecks, audit, optionalTimeAgo(benchmark.UpdatedAt),
			})
		}
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "%d clusters", len(overview.Clusters))
	if unavailable > 0 {
		_, _ = fmt.Fprintf(out, " · %d without compliance data (not scanned, or the agent could not be reached)", unavailable)
	}
	_, _ = fmt.Fprintln(out)
}

func failCountCell(count int) string {
	if count > 0 {
		return text.FgRed.Sprintf("%d", count)
	}
	return fmt.Sprintf("%d", count)
}

var securityComplianceFrameworksCmd = &cobra.Command{
	Use:   "frameworks",
	Short: "The compliance framework catalogue with each framework's switch and live score",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		list, err := apiClient.ListSecurityComplianceFrameworks()
		if err != nil {
			return fmt.Errorf("listing compliance frameworks: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecurityComplianceFrameworks(cmd.OutOrStdout(), list)
		return nil
	},
}

func renderSecurityComplianceFrameworks(out io.Writer, list *client.SecurityComplianceFrameworkList) {
	if len(list.Frameworks) == 0 {
		_, _ = fmt.Fprintln(out, "The framework catalogue is empty.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Key", "Framework", "Version", "Enabled", "Score", "Satisfied", "At risk", "Failing", "Manual", "Controls (automated)"})
	for _, framework := range list.Frameworks {
		enabled := "no"
		if framework.Enabled {
			enabled = "yes"
			if framework.EnabledAt != nil {
				enabled += " (" + formatTimeAgo(*framework.EnabledAt) + ")"
			}
		}
		writer.AppendRow(table.Row{
			framework.Key, framework.Title, framework.Version, enabled,
			scoreCell(framework.Score),
			optionalIntCell(framework.SatisfiedControls), optionalIntCell(framework.AtRiskControls),
			optionalIntCell(framework.FailingControls), optionalIntCell(framework.ManualControls),
			fmt.Sprintf("%d (%d)", framework.ControlCount, framework.AutomatedControlCount),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintln(out, "A framework's score is nil until it is enabled: ankra security compliance frameworks enable <key>")
}

// scoreCell renders a 0-100 score as the platform computed it (one
// decimal at most); nil is unscored, never 0.
func scoreCell(score *float64) string {
	if score == nil {
		return "-"
	}
	return scoreText(*score)
}

func scoreText(score float64) string {
	return strconv.FormatFloat(score, 'f', -1, 64) + "%"
}

func optionalIntCell(value *int) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *value)
}

func newSecurityFrameworkSwitchCommand(use string, enabled bool) *cobra.Command {
	verb, gerund := "Enable", "enabling"
	if !enabled {
		verb, gerund = "Disable", "disabling"
	}
	command := &cobra.Command{
		Use:   use + " <framework-key>",
		Short: verb + " a compliance framework for the organisation",
		Long: verb + ` one framework by its key (see 'ankra security compliance frameworks').
Needs the security.manage permission; asks for confirmation unless --yes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			frameworkKey := strings.TrimSpace(args[0])
			yes, _ := cmd.Flags().GetBool("yes")
			structured, err := structuredFormatFromFlags(cmd)
			if err != nil {
				return err
			}
			narration := cmd.OutOrStdout()
			if structured != outputDefault {
				narration = cmd.ErrOrStderr()
			}
			prompt := fmt.Sprintf("%s the %s framework for this organisation? [y/N]: ", verb, frameworkKey)
			if confirmError := confirmPrompt(cmd.InOrStdin(), narration, prompt, yes); confirmError != nil {
				return confirmError
			}
			framework, err := apiClient.SetSecurityComplianceFrameworkEnabled(frameworkKey, enabled)
			if err != nil {
				return fmt.Errorf("%s the framework: %w", gerund, err)
			}
			if rendered, err := renderStructured(cmd, framework); rendered || err != nil {
				return err
			}
			state := "disabled"
			if framework.Enabled {
				state = "enabled"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s (%s) is now %s", framework.Title, framework.Key, state)
			if framework.Enabled && framework.Score != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), " · score %s", scoreCell(framework.Score))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout())
			return nil
		},
	}
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	return command
}

var (
	securityComplianceFrameworksEnableCmd  = newSecurityFrameworkSwitchCommand("enable", true)
	securityComplianceFrameworksDisableCmd = newSecurityFrameworkSwitchCommand("disable", false)
)

var securityComplianceFrameworkReportCmd = &cobra.Command{
	Use:   "report <framework-key>",
	Short: "One framework's control-by-control report, live or for a recorded month",
	Long: `Print a framework's report: the score, every category with its controls
and the automated checks behind each, the recorded monthly trend and the
months a snapshot exists for. --month YYYY-MM reads a recorded month; the
default is the live evaluation.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		month, _ := cmd.Flags().GetString("month")
		report, err := apiClient.GetSecurityComplianceFrameworkReport(strings.TrimSpace(args[0]), month)
		if err != nil {
			return fmt.Errorf("reading the framework report: %w", err)
		}
		if rendered, err := renderStructured(cmd, report); rendered || err != nil {
			return err
		}
		renderSecurityComplianceFrameworkReport(cmd.OutOrStdout(), report)
		return nil
	},
}

func renderSecurityComplianceFrameworkReport(out io.Writer, report *client.SecurityComplianceFrameworkReport) {
	_, _ = fmt.Fprintf(out, "%s %s · %s · score %s\n", report.Framework.Title, report.Framework.Version, report.Period, scoreText(report.Score))
	_, _ = fmt.Fprintf(out, "  %d satisfied · %d at risk · %d failing · %d manual (generated %s)\n",
		report.SatisfiedControls, report.AtRiskControls, report.FailingControls, report.ManualControls, formatTimeAgo(report.GeneratedAt))
	for _, category := range report.Categories {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "%s · score %s · %d satisfied · %d at risk · %d failing · %d manual\n",
			category.Title, scoreCell(category.Score), category.Satisfied, category.AtRisk, category.Failing, category.Manual)
		writer := newSecurityTable(out)
		writer.AppendHeader(table.Row{"Control", "Title", "Status", "Checks"})
		for _, control := range category.Controls {
			checks := make([]string, 0, len(control.Checks))
			for _, check := range control.Checks {
				marker := text.FgGreen.Sprint("pass")
				if !check.Passed {
					marker = text.FgRed.Sprint("FAIL")
				}
				checks = append(checks, marker+" "+check.Title)
			}
			writer.AppendRow(table.Row{control.ID, control.Title, complianceControlStatusCell(control.Status), strings.Join(checks, "\n")})
		}
		writer.Render()
	}
	if len(report.Trend) > 0 {
		_, _ = fmt.Fprintln(out)
		points := make([]string, 0, len(report.Trend))
		for _, point := range report.Trend {
			points = append(points, point.Month+" "+scoreText(point.Score))
		}
		_, _ = fmt.Fprintf(out, "Trend: %s\n", strings.Join(points, " · "))
	}
	if len(report.AvailableMonths) > 0 {
		_, _ = fmt.Fprintf(out, "Recorded months: %s (pass --month YYYY-MM)\n", strings.Join(report.AvailableMonths, ", "))
	}
}

func complianceControlStatusCell(status string) string {
	switch status {
	case "satisfied":
		return text.FgGreen.Sprint(status)
	case "failing":
		return text.FgRed.Sprint(status)
	case "at_risk":
		return text.FgYellow.Sprint(status)
	default:
		return status
	}
}

var securityComplianceExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Download the compliance evidence report as CSV or JSON",
	Long: `Download the organisation's compliance evidence report: cluster posture,
findings, dispositions, the audit summary and the attestation, for a window
that defaults to the last 90 days. CSV by default; --format json for the
full document. Needs the security.read and audit.read permissions.

The file goes to the name the platform suggests in the current directory,
to --output-file, or to standard output with --output-file -.

Example:
  ankra security compliance export --format json --start-date 2026-07-01 --end-date 2026-09-30 --output-file q3.json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("format")
		startDate, _ := cmd.Flags().GetString("start-date")
		endDate, _ := cmd.Flags().GetString("end-date")
		outputFile, _ := cmd.Flags().GetString("output-file")
		force, _ := cmd.Flags().GetBool("force")
		normalizedFormat, formatError := normalizeComplianceExportFormat(format)
		if formatError != nil {
			return formatError
		}
		export, err := apiClient.ExportSecurityComplianceReport(client.SecurityComplianceExportOptions{
			Format:    normalizedFormat,
			StartDate: startDate,
			EndDate:   endDate,
		})
		if err != nil {
			return fmt.Errorf("downloading the compliance report: %w", err)
		}
		target := strings.TrimSpace(outputFile)
		if target == "-" {
			_, writeError := cmd.OutOrStdout().Write(export.Body)
			return writeError
		}
		if target == "" {
			target = complianceExportLocalFileName(export.FileName, normalizedFormat)
		}
		if !force {
			if _, statError := os.Stat(target); statError == nil {
				return withExitCode(exitUsage, fmt.Errorf("%s already exists; pass --force to overwrite it or --output-file to write elsewhere", target))
			}
		}
		if writeError := os.WriteFile(target, export.Body, 0o600); writeError != nil {
			return fmt.Errorf("writing %s: %w", target, writeError)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%d bytes, %s)\n", target, len(export.Body), export.ContentType)
		return nil
	},
}

func normalizeComplianceExportFormat(requested string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "", "csv":
		return "csv", nil
	case "json":
		return "json", nil
	}
	return "", withExitCode(exitUsage, fmt.Errorf("--format must be csv or json, got %q", requested))
}

// complianceExportLocalFileName is the platform's suggested name reduced to
// its last path element (a Content-Disposition is server input, never a
// path), or a name derived from the format.
func complianceExportLocalFileName(suggested string, format string) string {
	base := serverSuggestedFileName(suggested)
	if base == "" {
		return "compliance-report." + format
	}
	return base
}

// serverSuggestedFileName reduces a Content-Disposition filename to a bare
// name on every platform: both separators are stripped, so "..\..\x.csv"
// from a server is "x.csv" on a Unix build too, and a dot-file or an empty
// name is refused (empty result) so the caller falls back to its default.
func serverSuggestedFileName(suggested string) string {
	base := path.Base(strings.ReplaceAll(strings.TrimSpace(suggested), "\\", "/"))
	if base == "" || base == "." || base == "/" || strings.HasPrefix(base, ".") {
		return ""
	}
	return base
}

// requiredClusterIDFromFlags resolves --cluster, which the per-cluster
// posture reads cannot do without.
func requiredClusterIDFromFlags(cmd *cobra.Command) (string, error) {
	clusterFlag, _ := cmd.Flags().GetString("cluster")
	if strings.TrimSpace(clusterFlag) == "" {
		return "", withExitCode(exitUsage, errors.New("--cluster is required"))
	}
	return resolveClusterID(clusterFlag)
}

// clusterReadError turns the agent's 503 envelope into one line the reader
// can act on; every other error passes through with the operation named.
func clusterReadError(operation string, err error) error {
	if unavailable, ok := client.AsClusterAgentUnavailable(err); ok {
		return fmt.Errorf("%s: the cluster agent could not serve this read (%s)", operation, unavailable.Error())
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var securityBenchmarksCmd = &cobra.Command{
	Use:   "benchmarks",
	Short: "One cluster's live benchmark compliance: every failing control with its remediation",
	Long: `Read one cluster's benchmark compliance through its agent: each
ClusterComplianceReport (CIS, NSA/CISA, ...) with pass and fail totals and
the failing controls, plus the config-audit rollup by namespace and check.

A cluster whose agent is offline answers with the reason and a retry hint
rather than an empty report. Drill into one failing control with
'ankra security benchmarks resources'.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		benchmarks, err := apiClient.GetSecurityClusterBenchmarks(clusterID)
		if err != nil {
			return clusterReadError("reading cluster benchmarks", err)
		}
		if rendered, err := renderStructured(cmd, benchmarks); rendered || err != nil {
			return err
		}
		renderSecurityClusterBenchmarks(cmd.OutOrStdout(), benchmarks)
		return nil
	},
}

func renderSecurityClusterBenchmarks(out io.Writer, benchmarks *client.SecurityClusterBenchmarks) {
	_, _ = fmt.Fprintf(out, "%s: compliance %s", benchmarks.ClusterName, benchmarks.Status)
	if benchmarks.Detail != nil && *benchmarks.Detail != "" {
		_, _ = fmt.Fprintf(out, " - %s", *benchmarks.Detail)
	}
	_, _ = fmt.Fprintln(out)
	if benchmarks.Status != "available" {
		return
	}
	for _, benchmark := range benchmarks.Benchmarks {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "%s (%s): %d pass · %s fail · updated %s\n",
			benchmark.Title, benchmark.SpecID, benchmark.PassCount, failCountCell(benchmark.FailCount), optionalTimeAgo(benchmark.UpdatedAt))
		if len(benchmark.FailedChecks) == 0 {
			continue
		}
		writer := newSecurityTable(out)
		writer.AppendHeader(table.Row{"Check", "Severity", "Title", "Affected", "Remediation"})
		for _, check := range benchmark.FailedChecks {
			writer.AppendRow(table.Row{check.ID, severityCell(check.Severity), check.Title, check.AffectedCount, truncateReason(check.Remediation, 80)})
		}
		writer.Render()
	}
	if benchmarks.ConfigAudit != nil {
		audit := benchmarks.ConfigAudit
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Config audit: %d reports · failures %d critical · %d high · %d medium · %d low\n",
			audit.ReportCount, audit.Severity.Critical, audit.Severity.High, audit.Severity.Medium, audit.Severity.Low)
		if len(audit.TopFailedChecks) > 0 {
			writer := newSecurityTable(out)
			writer.AppendHeader(table.Row{"Check", "Severity", "Title", "Reports", "Remediation"})
			for _, check := range audit.TopFailedChecks {
				writer.AppendRow(table.Row{check.ID, severityCell(check.Severity), check.Title, check.Count, truncateReason(check.Remediation, 80)})
			}
			writer.Render()
		}
	}
	if benchmarks.IsTruncated {
		_, _ = fmt.Fprintln(out, text.FgYellow.Sprint("The report was truncated to stay within the relay size limit; totals stay exact."))
	}
}

var securityBenchmarksResourcesCmd = &cobra.Command{
	Use:   "resources",
	Short: "The objects failing one benchmark control, with the Ankra resource each belongs to",
	Long: `List the Kubernetes objects failing one control on a cluster and, when the
platform could match them, the stack and resource that owns each. --check
is the control id from 'ankra security benchmarks'; --source is benchmark
(pass --benchmark with the spec id) or config_audit.

Example:
  ankra security benchmarks resources --cluster production --check 5.1.1 --benchmark cis-1.23`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		checkID, _ := cmd.Flags().GetString("check")
		source, _ := cmd.Flags().GetString("source")
		benchmarkID, _ := cmd.Flags().GetString("benchmark")
		if strings.TrimSpace(checkID) == "" {
			return withExitCode(exitUsage, errors.New("--check is required"))
		}
		switch strings.ToLower(strings.TrimSpace(source)) {
		case "", "benchmark":
			source = "benchmark"
			if strings.TrimSpace(benchmarkID) == "" {
				return withExitCode(exitUsage, errors.New("--benchmark is required when --source is benchmark"))
			}
		case "config_audit":
			source = "config_audit"
		default:
			return withExitCode(exitUsage, fmt.Errorf("--source must be benchmark or config_audit, got %q", source))
		}
		resources, err := apiClient.GetSecurityBenchmarkResources(client.SecurityBenchmarkResourcesOptions{
			ClusterID:   clusterID,
			CheckID:     strings.TrimSpace(checkID),
			Source:      source,
			BenchmarkID: strings.TrimSpace(benchmarkID),
		})
		if err != nil {
			return clusterReadError("listing the control's failing resources", err)
		}
		if rendered, err := renderStructured(cmd, resources); rendered || err != nil {
			return err
		}
		renderSecurityBenchmarkResources(cmd.OutOrStdout(), resources)
		return nil
	},
}

func renderSecurityBenchmarkResources(out io.Writer, resources *client.SecurityBenchmarkResources) {
	_, _ = fmt.Fprintf(out, "%s · %s %s", resources.ClusterName, resources.CheckID, resources.ControlTitle)
	if len(resources.ResolvedCheckIDs) > 0 {
		_, _ = fmt.Fprintf(out, " (checks %s)", strings.Join(resources.ResolvedCheckIDs, ", "))
	}
	_, _ = fmt.Fprintln(out)
	if resources.UnresolvedReason != nil && *resources.UnresolvedReason != "" {
		_, _ = fmt.Fprintln(out, *resources.UnresolvedReason)
	}
	if len(resources.Resources) == 0 {
		_, _ = fmt.Fprintln(out, "No failing resources for this control.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Kind", "Namespace", "Name", "Severity", "Owner", "Message"})
	for _, resource := range resources.Resources {
		owner := "-"
		if resource.Owner.Name != nil {
			owner = stringOrEmpty(resource.Owner.ResourceKind) + " " + *resource.Owner.Name
			if resource.Owner.StackName != nil {
				owner += " (stack " + *resource.Owner.StackName + ")"
			}
		}
		writer.AppendRow(table.Row{resource.Kind, stringOrEmpty(resource.Namespace), resource.Name, severityCell(resource.Severity), owner, truncateReason(resource.Message, 80)})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "%d failing resources", len(resources.Resources))
	if resources.IsTruncated {
		_, _ = fmt.Fprint(out, " (truncated)")
	}
	_, _ = fmt.Fprintln(out)
}

var securityViolationsCmd = &cobra.Command{
	Use:   "violations",
	Short: "One cluster's pod-security policy posture: policy mode and results per policy",
	Long: `Read one cluster's pod-security policy results: the stored policy mode
(audit or enforce; none when the policy add-on is not installed), the
pass/fail/warn totals and each policy with its worst namespaces, plus the
platform's own evaluation of the cluster's workloads from the resource
cache, which is present whether or not the policy engine runs.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		violations, err := apiClient.GetSecurityClusterPolicyViolations(clusterID)
		if err != nil {
			return clusterReadError("reading policy violations", err)
		}
		if rendered, err := renderStructured(cmd, violations); rendered || err != nil {
			return err
		}
		renderSecurityPolicyViolations(cmd.OutOrStdout(), violations)
		return nil
	},
}

func renderSecurityPolicyViolations(out io.Writer, violations *client.SecurityClusterPolicyViolations) {
	mode := "policy add-on not installed"
	if violations.PolicyMode != nil {
		mode = "policy mode " + *violations.PolicyMode
	}
	_, _ = fmt.Fprintf(out, "%s: %s · %s", violations.ClusterName, violations.Status, mode)
	if violations.Detail != nil && *violations.Detail != "" {
		_, _ = fmt.Fprintf(out, " - %s", *violations.Detail)
	}
	_, _ = fmt.Fprintln(out)
	if violations.Status == "available" {
		_, _ = fmt.Fprintf(out, "  %d reports · %d pass · %s fail · %d warn · %d error · %d skip\n",
			violations.ReportCount, violations.Totals.Pass, failCountCell(violations.Totals.Fail),
			violations.Totals.Warn, violations.Totals.Error, violations.Totals.Skip)
		renderSecurityPolicyTable(out, violations.Policies)
	}
	posture := violations.PlatformPosture
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "Platform posture (evaluated from the resource cache): %s", posture.Status)
	if posture.Detail != nil && *posture.Detail != "" {
		_, _ = fmt.Fprintf(out, " - %s", *posture.Detail)
	}
	_, _ = fmt.Fprintln(out)
	if posture.EvaluatedWorkloads > 0 || len(posture.Policies) > 0 {
		_, _ = fmt.Fprintf(out, "  %d workloads evaluated · %d pass · %s fail · %d warn",
			posture.EvaluatedWorkloads, posture.Totals.Pass, failCountCell(posture.Totals.Fail), posture.Totals.Warn)
		if posture.CacheAsOf != nil {
			_, _ = fmt.Fprintf(out, " · cache as of %s", formatTimeAgo(*posture.CacheAsOf))
		}
		if len(posture.ExemptNamespaces) > 0 {
			_, _ = fmt.Fprintf(out, " · exempt: %s", strings.Join(posture.ExemptNamespaces, ", "))
		}
		_, _ = fmt.Fprintln(out)
		renderSecurityPolicyTable(out, posture.Policies)
	}
}

func renderSecurityPolicyTable(out io.Writer, policies []client.SecurityPolicyEntry) {
	if len(policies) == 0 {
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Policy", "Severity", "Category", "Pass", "Fail", "Warn", "Worst namespaces"})
	for _, policy := range policies {
		namespaces := make([]string, 0, len(policy.TopNamespaces))
		for _, namespace := range policy.TopNamespaces {
			namespaces = append(namespaces, fmt.Sprintf("%s (%d)", namespace.Namespace, namespace.FailCount))
		}
		writer.AppendRow(table.Row{policy.Policy, severityCell(policy.Severity), policy.Category, policy.PassCount, failCountCell(policy.FailCount), policy.WarnCount, strings.Join(namespaces, ", ")})
	}
	writer.Render()
}

var securityNetworkExposureCmd = &cobra.Command{
	Use:   "network-exposure",
	Short: "One cluster's NetworkPolicy over-privilege: how tightly ingress and egress are scoped",
	Long: `Score one cluster's NetworkPolicies per direction, 0-100 with higher being
tighter: the share of admitting rules that admit a broader peer set than a
named identity, and the workloads no policy protects at all. A cluster
with no workloads has nothing to score and reads n/a, not 100.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		exposure, err := apiClient.GetSecurityClusterNetworkExposure(clusterID)
		if err != nil {
			return clusterReadError("reading network exposure", err)
		}
		if rendered, err := renderStructured(cmd, exposure); rendered || err != nil {
			return err
		}
		renderSecurityNetworkExposure(cmd.OutOrStdout(), exposure)
		return nil
	},
}

func directionScoreText(score client.SecurityDirectionScore) string {
	if score.TotalWorkloads == 0 {
		return "n/a (no workloads to score)"
	}
	tier := text.FgGreen.Sprint("compliant")
	switch {
	case score.Score < 50:
		tier = text.FgRed.Sprint("poor")
	case score.Score < 80:
		tier = text.FgYellow.Sprint("attention")
	}
	return fmt.Sprintf("%d/100 %s · %d of %d workloads unprotected · %d of %d admitting rules broad (%.0f%%)",
		score.Score, tier, score.UnprotectedWorkloads, score.TotalWorkloads, score.BroadRules, score.TotalAdmittingRules, score.OverPrivilegeRate*100)
}

func renderSecurityNetworkExposure(out io.Writer, exposure *client.SecurityClusterNetworkExposure) {
	_, _ = fmt.Fprintf(out, "%s network exposure\n", exposure.ClusterName)
	_, _ = fmt.Fprintf(out, "  ingress: %s\n", directionScoreText(exposure.Ingress))
	_, _ = fmt.Fprintf(out, "  egress:  %s\n", directionScoreText(exposure.Egress))
	if len(exposure.Findings) == 0 {
		_, _ = fmt.Fprintln(out, "No over-privilege findings.")
		return
	}
	_, _ = fmt.Fprintln(out)
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Direction", "Severity", "Finding", "Namespace", "Workload / policy", "Affected", "Remediation"})
	for _, finding := range exposure.Findings {
		subject := finding.Workload
		if subject == "" {
			subject = finding.PolicyName
			if finding.Reason != "" {
				subject += " (" + finding.Reason + ")"
			}
		}
		writer.AppendRow(table.Row{finding.Direction, severityCell(finding.Severity), finding.Title, finding.Namespace, subject, finding.AffectedCount, truncateReason(finding.Remediation, 80)})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "%d findings", len(exposure.Findings))
	if exposure.FindingsTruncated > 0 {
		_, _ = fmt.Fprintf(out, " (%d more not listed; the scores above count them)", exposure.FindingsTruncated)
	}
	_, _ = fmt.Fprintln(out)
}

var securityPolicyModeCmd = &cobra.Command{
	Use:   "policy-mode <audit|enforce>",
	Short: "Switch a cluster's pod-security policies between audit and enforce",
	Long: `Set the cluster's pod-security policy mode. audit records violations
without blocking; enforce rejects non-compliant workloads at admission, so
it always asks for confirmation - --yes skips the prompt for audit and
enforce alike. The change is committed to the cluster's GitOps repository
and rolled out by the platform.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mode := strings.ToLower(strings.TrimSpace(args[0]))
		if mode != "audit" && mode != "enforce" {
			return withExitCode(exitUsage, fmt.Errorf("mode must be audit or enforce, got %q", args[0]))
		}
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		structured, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		narration := cmd.OutOrStdout()
		if structured != outputDefault {
			narration = cmd.ErrOrStderr()
		}
		prompt := fmt.Sprintf("Set the pod-security policy mode to %s? [y/N]: ", mode)
		if mode == "enforce" {
			prompt = "Set the pod-security policy mode to enforce? Non-compliant workloads will be rejected at admission. [y/N]: "
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), narration, prompt, yes); confirmError != nil {
			return confirmError
		}
		result, err := apiClient.SetSecurityPolicyMode(clusterID, mode)
		if err != nil {
			return fmt.Errorf("setting the policy mode: %w", err)
		}
		if rendered, err := renderStructured(cmd, result); rendered || err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Policy mode set to %s", result.Mode)
		if result.CommitURL != nil && *result.CommitURL != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), " · committed %s", *result.CommitURL)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		return nil
	},
}

var securityEnableBaselineCmd = &cobra.Command{
	Use:   "enable-baseline",
	Short: "Install the security baseline stack on a cluster: the scanner and the policy add-ons",
	Long: `Install the platform's security baseline on one cluster: Trivy Operator for
vulnerability, SBOM and compliance reports, plus the pod-security policy
add-ons in audit mode. The stack is committed to the cluster's GitOps
repository and rolled out by the platform; findings appear once the first
scan completes. Asks for confirmation unless --yes.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		yes, _ := cmd.Flags().GetBool("yes")
		structured, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}
		narration := cmd.OutOrStdout()
		if structured != outputDefault {
			narration = cmd.ErrOrStderr()
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), narration, "Install the security baseline stack on this cluster? [y/N]: ", yes); confirmError != nil {
			return confirmError
		}
		result, err := apiClient.EnableSecurityBaseline(clusterID)
		if err != nil {
			return fmt.Errorf("enabling the security baseline: %w", err)
		}
		if rendered, err := renderStructured(cmd, result); rendered || err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Security baseline stack %s: %d jobs queued · %d settings-only · %d unchanged", result.StackName, result.JobCount, result.SettingsOnlyCount, result.NoopCount)
		if result.CommitURL != nil && *result.CommitURL != "" {
			_, _ = fmt.Fprintf(out, " · committed %s", *result.CommitURL)
		}
		_, _ = fmt.Fprintln(out)
		for _, resourceError := range result.Errors {
			for _, item := range resourceError.Errors {
				_, _ = fmt.Fprintf(out, "  %s %s: %s %s\n", resourceError.Kind, resourceError.Name, item["key"], item["message"])
			}
		}
		if len(result.Errors) == 0 {
			_, _ = fmt.Fprintln(out, "Findings appear once the first scan completes: ankra security clusters")
		}
		return nil
	},
}

var securityAddonCmd = &cobra.Command{
	Use:   "addon <addon-name>",
	Short: "One add-on's security posture on a cluster: the findings attributed to its images",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		posture, err := apiClient.GetSecurityAddonPosture(clusterID, strings.TrimSpace(args[0]))
		if err != nil {
			return fmt.Errorf("reading add-on security posture: %w", err)
		}
		if rendered, err := renderStructured(cmd, posture); rendered || err != nil {
			return err
		}
		renderSecurityAddonPosture(cmd.OutOrStdout(), posture)
		return nil
	},
}

func renderSecurityAddonPosture(out io.Writer, posture *client.SecurityAddonPosture) {
	_, _ = fmt.Fprintf(out, "%s", posture.AddonName)
	if posture.ChartName != nil && *posture.ChartName != "" {
		_, _ = fmt.Fprintf(out, " (%s)", *posture.ChartName)
	}
	if posture.Namespace != nil && *posture.Namespace != "" {
		_, _ = fmt.Fprintf(out, " in %s", *posture.Namespace)
	}
	_, _ = fmt.Fprintf(out, ": %s\n", posture.Status)
	_, _ = fmt.Fprintf(out, "  actionable %s · acknowledged %d · accepted risk %d · %d fixable severe · %d known exploited\n",
		severityCountsCell(posture.Actionable), severityCountsTotal(posture.Acknowledged), severityCountsTotal(posture.AcceptedRisk),
		posture.FixableSevere, posture.KnownExploited)
	_, _ = fmt.Fprintf(out, "  %d workloads · %d images · attribution %s (%d matched, %d unmatched, %d ambiguous) · last scan %s\n",
		posture.AffectedWorkloads, posture.AffectedImages, posture.Attribution.Status,
		posture.Attribution.Matched, posture.Attribution.Unmatched, posture.Attribution.Ambiguous, optionalTimeAgo(posture.LastScan))
	if len(posture.TopActionableFindings) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out)
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"CVE", "Severity", "Exploitation", "Package", "Actionable", "Fixable", "Finding ID"})
	for _, candidate := range posture.TopActionableFindings {
		writer.AppendRow(table.Row{
			candidate.CVEID, severityCell(candidate.Severity), exploitationCell(candidate.SecurityExploitIntelligence),
			candidate.PackageName, candidate.ActionableCount, candidate.FixableOccurrences, candidate.FindingID,
		})
	}
	writer.Render()
}

func init() {
	securityCmd.AddCommand(securityComplianceCmd, securityBenchmarksCmd, securityViolationsCmd,
		securityNetworkExposureCmd, securityPolicyModeCmd, securityEnableBaselineCmd, securityAddonCmd)
	securityComplianceCmd.AddCommand(securityComplianceFrameworksCmd, securityComplianceExportCmd)
	securityComplianceFrameworksCmd.AddCommand(securityComplianceFrameworksEnableCmd, securityComplianceFrameworksDisableCmd, securityComplianceFrameworkReportCmd)
	securityBenchmarksCmd.AddCommand(securityBenchmarksResourcesCmd)

	securityComplianceFrameworkReportCmd.Flags().String("month", "", "Recorded month to read (YYYY-MM); the default is the live evaluation")

	securityComplianceExportCmd.Flags().String("format", "csv", "Report format: csv or json")
	securityComplianceExportCmd.Flags().String("start-date", "", "Window start (YYYY-MM-DD or RFC3339); the platform defaults to 90 days ago")
	securityComplianceExportCmd.Flags().String("end-date", "", "Window end (YYYY-MM-DD or RFC3339); the platform defaults to now")
	securityComplianceExportCmd.Flags().String("output-file", "", "Where to write the report; - for standard output (default: the name the platform suggests)")
	securityComplianceExportCmd.Flags().Bool("force", false, "Overwrite an existing file")

	for _, command := range []*cobra.Command{securityBenchmarksCmd, securityBenchmarksResourcesCmd, securityViolationsCmd,
		securityNetworkExposureCmd, securityPolicyModeCmd, securityEnableBaselineCmd, securityAddonCmd} {
		command.Flags().String("cluster", "", "Cluster (name or id), required")
	}
	securityBenchmarksResourcesCmd.Flags().String("check", "", "Control id, required")
	securityBenchmarksResourcesCmd.Flags().String("source", "benchmark", "Where the control comes from: benchmark or config_audit")
	securityBenchmarksResourcesCmd.Flags().String("benchmark", "", "Benchmark spec id, required with --source benchmark")
	securityPolicyModeCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	securityEnableBaselineCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(securityComplianceCmd, securityComplianceFrameworksCmd, securityComplianceFrameworksEnableCmd,
		securityComplianceFrameworksDisableCmd, securityComplianceFrameworkReportCmd, securityBenchmarksCmd,
		securityBenchmarksResourcesCmd, securityViolationsCmd, securityNetworkExposureCmd, securityPolicyModeCmd,
		securityEnableBaselineCmd, securityAddonCmd)
}

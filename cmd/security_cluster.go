package cmd

import (
	"fmt"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var securityStacksCmd = &cobra.Command{
	Use:   "stacks",
	Short: "One cluster's security posture broken down by the stacks Ankra deployed on it",
	Long: `List every stack on a cluster with the posture its own Security tab shows:
attribution status, scope, actionable and known-exploited findings and the
bill-of-materials coverage of its containers, riskiest first, plus a closing
"outside any stack" row for everything no stack owns, so the rows add up to
the cluster.

A stack whose members matched no workload is reported unmatched, which is not
the same as clean. Open one stack with 'ankra security stack <name>'.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := securityClusterIDFromFlag(cmd)
		if err != nil {
			return err
		}
		list, err := apiClient.ListClusterSecurityStacks(clusterID)
		if err != nil {
			return fmt.Errorf("listing cluster security stacks: %w", err)
		}
		if rendered, err := renderStructured(cmd, list); rendered || err != nil {
			return err
		}
		renderSecurityStacks(cmd, list)
		return nil
	},
}

var securityStackCmd = &cobra.Command{
	Use:   "stack <stack-name>",
	Short: "One stack's security posture: its members, the workloads they deploy and the pods those run",
	Long: `Show one stack the way its Security tab does: attribution scope, the findings
attributed to it, the CVEs CISA lists as exploited, the bill of materials of
its containers, and every member (add-on or manifest) with the posture of the
workloads it resolved to.

Below the members come the Kubernetes workloads each member deploys, with
their pods, scan state, severe actionable findings and SBOM coverage, so the
path from an Ankra resource to a pod is one read. Open a pod with
'ankra security pod <namespace> <pod-name>'.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := securityClusterIDFromFlag(cmd)
		if err != nil {
			return err
		}
		stackName := strings.TrimSpace(args[0])
		posture, err := apiClient.GetStackSecurity(clusterID, stackName)
		if err != nil {
			return fmt.Errorf("reading stack security: %w", err)
		}
		workloads, err := apiClient.ListStackSecurityWorkloads(clusterID, stackName)
		if err != nil {
			return fmt.Errorf("listing stack workloads: %w", err)
		}
		if rendered, err := renderStructured(cmd, struct {
			Posture   *client.SecurityStackPosture      `json:"posture" yaml:"posture"`
			Workloads *client.SecurityStackWorkloadList `json:"workloads" yaml:"workloads"`
		}{posture, workloads}); rendered || err != nil {
			return err
		}
		renderSecurityStack(cmd, posture, workloads)
		return nil
	},
}

var securityPodCmd = &cobra.Command{
	Use:   "pod <namespace> <pod-name>",
	Short: "One pod's security posture, container by container",
	Long: `Show one running pod with the findings of the scanned workload container
behind each of its containers, the CISA KEV exposure, and whether each
container's image has a bill of materials.

A container the scanner has no report for is marked "not scanned", which is
not the same as clean. Open the image behind a container with
'ankra security sbom image <digest>' and its CVEs with
'ankra security sbom findings <digest>'.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := securityClusterIDFromFlag(cmd)
		if err != nil {
			return err
		}
		posture, err := apiClient.GetPodSecurity(clusterID, strings.TrimSpace(args[0]), strings.TrimSpace(args[1]))
		if err != nil {
			return fmt.Errorf("reading pod security: %w", err)
		}
		if rendered, err := renderStructured(cmd, posture); rendered || err != nil {
			return err
		}
		renderSecurityPod(cmd, posture)
		return nil
	},
}

func securityClusterIDFromFlag(cmd *cobra.Command) (string, error) {
	clusterFlag, _ := cmd.Flags().GetString("cluster")
	if strings.TrimSpace(clusterFlag) == "" {
		return "", withExitCode(exitUsage, fmt.Errorf("--cluster is required"))
	}
	return resolveClusterID(clusterFlag)
}

var securityStackStatusText = map[string]string{
	"connected":  "scanned",
	"stale":      "stale",
	"no_reports": "no reports yet",
	"unmatched":  "unmatched",
	"unscanned":  "unscanned",
	"empty":      "no members",
}

func securityStackStatusCell(status string) string {
	label, ok := securityStackStatusText[status]
	if !ok {
		label = status
	}
	switch status {
	case "connected":
		return text.FgGreen.Sprint(label)
	case "stale", "no_reports", "unmatched":
		return text.FgYellow.Sprint(label)
	}
	return label
}

func securitySeverityTotal(counts client.SecuritySeverityCounts) int {
	return counts.Critical + counts.High + counts.Medium + counts.Low + counts.Unknown
}

func severeActionableCell(counts client.SecuritySeverityCounts, fixableSevere int) string {
	if counts.Critical == 0 && counts.High == 0 {
		return "-"
	}
	rendered := fmt.Sprintf("%d critical, %d high", counts.Critical, counts.High)
	if fixableSevere > 0 {
		rendered += fmt.Sprintf(" (%d fixable)", fixableSevere)
	}
	return rendered
}

func sbomCoverageCell(containers int, withSBOM int) string {
	if containers == 0 {
		return "-"
	}
	rendered := fmt.Sprintf("%d / %d", withSBOM, containers)
	if withSBOM < containers {
		return text.FgYellow.Sprint(rendered)
	}
	return rendered
}

func knownExploitedCell(count int, intelligence client.SecurityIntelligenceStatus) string {
	if intelligence.KevSyncedAt == nil {
		return text.FgYellow.Sprint("unknown")
	}
	return redIfPositive(count)
}

func renderSecurityStacks(cmd *cobra.Command, list *client.SecurityClusterStackList) {
	out := cmd.OutOrStdout()
	if list.Scanner.Status == "unscanned" {
		_, _ = fmt.Fprintln(out, text.FgYellow.Sprint("The scanner has not reported on this cluster yet: nothing below is a clean result."))
	}
	if len(list.Stacks) == 0 {
		_, _ = fmt.Fprintln(out, "No stack is deployed on this cluster; everything it runs is counted as outside any stack.")
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Stack", "Status", "Scope", "Actionable", "Severe", "Known exploited", "Pods", "Containers with SBOM"})
	for _, stack := range list.Stacks {
		scope := fmt.Sprintf("%d add-ons, %d manifests, %d workloads", stack.Scope.Addons, stack.Scope.Manifests, stack.Scope.MatchedWorkloads)
		if stack.Scope.UnmatchedMembers > 0 {
			scope += text.FgYellow.Sprintf(", %d unmatched", stack.Scope.UnmatchedMembers)
		}
		writer.AppendRow(table.Row{
			stack.StackName,
			securityStackStatusCell(stack.Status),
			scope,
			securitySeverityTotal(stack.Findings.Actionable),
			severeActionableCell(stack.Findings.Actionable, stack.Findings.FixableSevere),
			knownExploitedCell(stack.Findings.KnownExploited, list.Intelligence),
			stack.Pods,
			sbomCoverageCell(stack.Containers, stack.ContainersWithSBOM),
		})
	}
	outside := list.Outside
	writer.AppendRow(table.Row{
		"(outside any stack)",
		"-",
		fmt.Sprintf("%d workloads", outside.Workloads),
		securitySeverityTotal(outside.Actionable),
		severeActionableCell(outside.Actionable, outside.FixableSevere),
		knownExploitedCell(outside.KnownExploited, list.Intelligence),
		outside.Pods,
		sbomCoverageCell(outside.Containers, outside.ContainersWithSBOM),
	})
	writer.Render()
	renderSbomCoverage(cmd, list.Coverage)
}

func renderSecurityStack(cmd *cobra.Command, posture *client.SecurityStackPosture, workloads *client.SecurityStackWorkloadList) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Stack %s · %s\n", posture.StackName, securityStackStatusCell(posture.Status))
	_, _ = fmt.Fprintf(out, "Scope: %d add-ons, %d manifests, %d declared objects, %d matched workloads, %d unmatched members\n",
		posture.Scope.Addons, posture.Scope.Manifests, posture.Scope.DeclaredObjects, posture.Scope.MatchedWorkloads, posture.Scope.UnmatchedMembers)
	if posture.Status == "empty" {
		_, _ = fmt.Fprintln(out, "The stack has no members yet, so there is nothing to scan.")
		return
	}
	if posture.Scope.UnmatchedMembers > 0 {
		_, _ = fmt.Fprintln(out, text.FgYellow.Sprintf("%d member(s) matched no workload in the resource cache: unmatched, not clean.", posture.Scope.UnmatchedMembers))
	}
	findings := posture.Findings
	_, _ = fmt.Fprintf(out, "Findings: %d observed, %d actionable (%s), %d acknowledged, %d accepted risk · known exploited: %s · %d images, %d workloads affected\n",
		securitySeverityTotal(findings.Observed), securitySeverityTotal(findings.Actionable),
		severeActionableCell(findings.Actionable, findings.FixableSevere),
		securitySeverityTotal(findings.Acknowledged), securitySeverityTotal(findings.AcceptedRisk),
		knownExploitedCell(findings.KnownExploited, posture.Intelligence),
		findings.AffectedImages, findings.AffectedWorkloads)
	if findings.LastScan != nil && *findings.LastScan != "" {
		_, _ = fmt.Fprintf(out, "Last scan: %s\n", formatTimeAgo(*findings.LastScan))
	}
	sbom := posture.SBOM
	_, _ = fmt.Fprintf(out, "Bill of materials: %d of %d containers across %d pods · %d images, %d components\n",
		sbom.ContainersWithSBOM, sbom.Containers, sbom.Pods, sbom.Images, sbom.Components)
	if len(posture.KnownExploited) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Known exploited (CISA KEV):")
		writer := newSecurityTable(out)
		writer.AppendHeader(table.Row{"CVE", "Severity", "Package", "Actionable", "Workloads", "Fixable"})
		for _, candidate := range posture.KnownExploited {
			writer.AppendRow(table.Row{candidate.CVEID, severityCell(candidate.Severity), candidate.PackageName,
				candidate.ActionableCount, candidate.AffectedWorkloads, candidate.FixableOccurrences})
		}
		writer.Render()
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Members:")
	members := newSecurityTable(out)
	members.AppendHeader(table.Row{"Member", "Kind", "Namespace", "Workloads", "Actionable", "Severe", "Known exploited", "Containers with SBOM"})
	for _, member := range posture.Members {
		workloadsCell := fmt.Sprint(member.Workloads)
		if member.Workloads == 0 {
			workloadsCell = text.FgYellow.Sprint("unmatched")
		}
		members.AppendRow(table.Row{
			member.Name,
			member.Kind,
			stringOrEmpty(member.Namespace),
			workloadsCell,
			securitySeverityTotal(member.Actionable),
			severeActionableCell(member.Actionable, member.FixableSevere),
			knownExploitedCell(member.KnownExploited, posture.Intelligence),
			sbomCoverageCell(member.Containers, member.ContainersWithSBOM),
		})
	}
	members.Render()
	_, _ = fmt.Fprintln(out)
	if len(workloads.Result) == 0 {
		_, _ = fmt.Fprintln(out, "No member resolved to a workload the resource cache holds, so no pods can be listed under this stack.")
		return
	}
	_, _ = fmt.Fprintln(out, "Workloads by member:")
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Member", "Workload", "Namespace", "Pods", "Scan", "Actionable", "Severe", "Known exploited", "Containers with SBOM"})
	for _, workload := range workloads.Result {
		scan := "scanned"
		if !workload.Scanned {
			scan = text.FgYellow.Sprint("not scanned")
		}
		writer.AppendRow(table.Row{
			workload.MemberName,
			workload.Kind + " " + workload.Name,
			workload.Namespace,
			workload.Pods,
			scan,
			securitySeverityTotal(workload.Actionable),
			severeActionableCell(workload.Actionable, workload.FixableSevere),
			knownExploitedCell(workload.KnownExploited, posture.Intelligence),
			sbomCoverageCell(workload.Containers, workload.ContainersWithSBOM),
		})
	}
	writer.Render()
	_, _ = fmt.Fprintln(out, "Open a workload's pods with 'ankra security pods --cluster <cluster> --namespace <ns> --workload-kind <kind> --workload-name <name>'.")
}

func renderSecurityPod(cmd *cobra.Command, posture *client.SecurityPodPosture) {
	out := cmd.OutOrStdout()
	owner := "-"
	if posture.WorkloadName != nil {
		owner = strings.TrimSpace(stringOrEmpty(posture.WorkloadKind) + " " + *posture.WorkloadName)
	}
	_, _ = fmt.Fprintf(out, "Pod %s/%s · %s · workload %s · node %s · phase %s\n",
		posture.Namespace, posture.PodName, securityStackStatusCell(posture.Status), owner,
		stringOrEmpty(posture.Node), stringOrEmpty(posture.Phase))
	switch posture.Status {
	case "unscanned":
		_, _ = fmt.Fprintln(out, text.FgYellow.Sprint("The scanner has not reported on this cluster yet, so nothing is known about this pod."))
	case "no_reports":
		_, _ = fmt.Fprintln(out, text.FgYellow.Sprint("No report and no bill of materials names any container of this pod yet: not a clean result."))
	}
	findings := posture.Findings
	_, _ = fmt.Fprintf(out, "Findings: %d observed, %d actionable (%s) · known exploited: %s · %d of %d containers scanned\n",
		findings.Observed, securitySeverityTotal(findings.Actionable),
		severeActionableCell(findings.Actionable, findings.FixableSevere),
		knownExploitedCell(findings.KnownExploited, posture.Intelligence),
		findings.ScannedContainers, findings.Containers)
	_, _ = fmt.Fprintf(out, "Bill of materials: %d of %d containers · %d images, %d components\n",
		posture.SBOM.WithSBOM, posture.SBOM.Containers, posture.SBOM.Images, posture.SBOM.Components)
	if len(posture.Containers) == 0 {
		_, _ = fmt.Fprintln(out, "The resource cache holds no container for this pod.")
		return
	}
	writer := newSecurityTable(out)
	writer.AppendHeader(table.Row{"Container", "Kind", "Image", "Ready", "Scan", "Observed", "Severe", "Known exploited", "SBOM", "Components"})
	for _, container := range posture.Containers {
		scan := "scanned"
		if !container.Scanned {
			scan = text.FgYellow.Sprint("not scanned")
		}
		ready := "yes"
		if !container.Ready {
			ready = text.FgYellow.Sprint("no")
		}
		sbom := container.SBOMStatus
		if sbom == "absent" {
			sbom = text.FgYellow.Sprint("absent")
		}
		components := "-"
		if container.ComponentCount != nil {
			components = fmt.Sprint(*container.ComponentCount)
		}
		writer.AppendRow(table.Row{
			container.Name,
			container.Kind,
			container.Image,
			ready,
			scan,
			container.Observed,
			severeActionableCell(container.Actionable, container.FixableSevere),
			knownExploitedCell(container.KnownExploited, posture.Intelligence),
			sbom,
			components,
		})
	}
	writer.Render()
	_, _ = fmt.Fprintln(out, "Open a container's image with 'ankra security sbom image <digest>' and its CVEs with 'ankra security sbom findings <digest>'.")
}

func init() {
	securityCmd.AddCommand(securityStacksCmd, securityStackCmd, securityPodCmd)
	registerStructuredOutputFlags(securityStacksCmd, securityStackCmd, securityPodCmd)
	securityStacksCmd.Flags().String("cluster", "", "The cluster (name or id); required")
	securityStackCmd.Flags().String("cluster", "", "The cluster (name or id); required")
	securityPodCmd.Flags().String("cluster", "", "The cluster (name or id); required")
}

package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var securityAccessCmd = &cobra.Command{
	Use:   "access",
	Short: "One cluster's Kubernetes access posture: risky grants with their fix, and the checks that could not run",
	Long: `Evaluate one cluster's kube gateway grants and print every failed check,
most severe first: a grant whose identity the cluster verified can
impersonate, a standing cluster-wide admin or cluster-admin grant, a grant
the organisation's access policy would refuse today, and the creator's grant
left unchanged for 90 days on a GitOps cluster. Each finding names the grant,
the grantee, its role and scope, and the commands that fix it.

Checks that could not be evaluated are listed after the findings, so a short
list is never read as a clean one. A grant absent from both lists passed
every check. Grantee emails appear only when you hold kube_access.manage;
otherwise the grantee is shown by its Ankra user id. A service account,
which has no email, is shown as "service account <name>".

If the platform leaves the summary (or one of its always-sent counts), the
findings or the unknowns out of its answer, that part reads "not reported", whatever was reported is still
printed, and the command exits 1: an incomplete answer is never a pass.
-o json prints the document as sent, nulls included, and exits 0.

The same summary line is the Access column of 'ankra security clusters'.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterFlag, _ := cmd.Flags().GetString("cluster")
		clusterID, err := requiredClusterIDFromFlags(cmd)
		if err != nil {
			return err
		}
		posture, err := apiClient.GetSecurityClusterAccessPosture(clusterID)
		if err != nil {
			return securityAccessReadError(err)
		}
		if rendered, err := renderStructured(cmd, posture); rendered || err != nil {
			return err
		}
		return renderSecurityAccessPosture(cmd.OutOrStdout(), strings.TrimSpace(clusterFlag), posture,
			granteeLabelsForFindings(posture.Findings))
	},
}

// securityAccessReadError names both readings of a 404: the route answers
// it for a cluster outside the organisation, and a platform that predates
// the access posture view does not serve the route at all.
func securityAccessReadError(readError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) && unexpected.StatusCode == http.StatusNotFound {
		return withExitCode(exitNotFound, errors.New(
			"reading cluster access posture: not found. Either the cluster is not in this organisation, "+
				"or this platform predates the access posture view ('ankra security clusters' then shows Access as not reported)"))
	}
	return fmt.Errorf("reading cluster access posture: %w", readError)
}

// accessCountIsZero reports a count the platform sent as a real 0. A nil
// count is unknown and is never zero.
func accessCountIsZero(count *int) bool {
	return count != nil && *count == 0
}

// accessPostureCell renders a cluster's access summary line. A nil summary
// or grant total is a platform that did not report one, and any nil count
// is one the platform could not answer or left out: each says so instead
// of reading as zero. "no grants" is printed only when every count is a
// known 0, so neither an unknown nor a contradictory answer hides behind
// it, and "(N of M verified)" only when the verified count is known.
func accessPostureCell(summary *client.SecurityClusterAccessSummary) string {
	if summary == nil || summary.GrantsTotal == nil {
		return "not reported"
	}
	total := *summary.GrantsTotal
	if total == 0 && accessCountIsZero(summary.StandingElevated) && accessCountIsZero(summary.OverPolicy) &&
		accessCountIsZero(summary.ImpersonateReachable) && accessCountIsZero(summary.ImpersonateVerifiedGrants) {
		return "no grants"
	}
	grants := fmt.Sprintf("%d grants", total)
	if total == 1 {
		grants = "1 grant"
	}
	parts := []string{grants}
	if summary.StandingElevated == nil {
		parts = append(parts, "standing elevated unknown")
	} else {
		parts = append(parts, fmt.Sprintf("%d standing elevated", *summary.StandingElevated))
	}
	if summary.OverPolicy == nil {
		parts = append(parts, "over policy unknown")
	} else {
		parts = append(parts, fmt.Sprintf("%d over policy", *summary.OverPolicy))
	}
	switch {
	case summary.ImpersonateReachable != nil:
		parts = append(parts, fmt.Sprintf("%d can impersonate", *summary.ImpersonateReachable))
	case summary.ImpersonateVerifiedGrants != nil:
		parts = append(parts, fmt.Sprintf("impersonate unknown (%d of %d verified)", *summary.ImpersonateVerifiedGrants, total))
	default:
		parts = append(parts, "impersonate unknown")
	}
	return strings.Join(parts, ", ")
}

var accessSeverityRank = map[string]int{"CRITICAL": 0, "HIGH": 1, "MEDIUM": 2, "LOW": 3}

func accessSeverityOrder(severity string) int {
	if rank, known := accessSeverityRank[strings.ToUpper(severity)]; known {
		return rank
	}
	return len(accessSeverityRank)
}

// granteeLabelsForFindings names the service principals among the findings'
// grantees; the roster is read only when a finding has no grantee email.
func granteeLabelsForFindings(findings []client.SecurityClusterAccessFinding) map[string]string {
	for _, finding := range findings {
		if finding.UserEmail == nil || strings.TrimSpace(*finding.UserEmail) == "" {
			return servicePrincipalLabels(effectiveOrganisationID())
		}
	}
	return nil
}

func accessGrantee(finding client.SecurityClusterAccessFinding, labels map[string]string) string {
	if label, named := granteeLabel(finding.UserEmail, finding.AnkraUserID, labels); named {
		return label
	}
	return "user " + finding.AnkraUserID
}

func accessScopeText(scope string, namespace *string) string {
	switch {
	case scope == "cluster":
		return "cluster-wide"
	case namespace != nil && *namespace != "":
		return "namespace " + *namespace
	default:
		return scope
	}
}

// renderSecurityAccessPosture prints the human view and returns an error
// when the platform left part of the document unreported (a null summary,
// findings or unknowns list): whatever was reported is still printed, but
// the read cannot be taken as passed, so the command exits non-zero.
func renderSecurityAccessPosture(out io.Writer, clusterLabel string, posture *client.SecurityClusterAccessPosture, labels map[string]string) error {
	if clusterLabel == "" {
		clusterLabel = posture.ClusterID
	}
	var unreported []string
	summaryLine := "the platform did not report access posture"
	switch {
	case posture.Summary == nil:
		unreported = append(unreported, "summary")
	case posture.Summary.GrantsTotal == nil || posture.Summary.StandingElevated == nil || posture.Summary.ImpersonateVerifiedGrants == nil:
		// over_policy and impersonate_reachable are null by contract when
		// unknown, and the unknowns list explains them; the other three
		// are always sent, so one missing is an incomplete answer.
		summaryLine = accessPostureCell(posture.Summary)
		unreported = append(unreported, "summary counts")
	default:
		summaryLine = accessPostureCell(posture.Summary)
	}
	summaryComplete := len(unreported) == 0
	_, _ = fmt.Fprintf(out, "Kubernetes access on %s: %s\n", clusterLabel, summaryLine)
	if posture.EvaluatedAt != "" {
		_, _ = fmt.Fprintf(out, "Evaluated %s\n", formatTimeAgo(posture.EvaluatedAt))
	}
	_, _ = fmt.Fprintln(out)

	findingsReported := posture.Findings != nil
	unknownsReported := posture.Unknowns != nil
	findings := append([]client.SecurityClusterAccessFinding(nil), posture.Findings...)
	sort.SliceStable(findings, func(left, right int) bool {
		return accessSeverityOrder(findings[left].Severity) < accessSeverityOrder(findings[right].Severity)
	})
	switch {
	case !findingsReported:
		unreported = append(unreported, "findings")
		_, _ = fmt.Fprintln(out, "Findings: not reported by the platform.")
	case len(findings) == 0 && unknownsReported && len(posture.Unknowns) == 0 && summaryComplete:
		_, _ = fmt.Fprintln(out, text.FgGreen.Sprint("Every access check passed on every grant."))
	case len(findings) == 0:
		_, _ = fmt.Fprintln(out, "No failed access checks, but not every check was evaluated (below).")
	default:
		_, _ = fmt.Fprintf(out, "Findings (%d):\n", len(findings))
		for _, finding := range findings {
			title := finding.Title
			if title == "" {
				title = finding.Check
			}
			_, _ = fmt.Fprintf(out, "\n%s  %s (%s)\n", severityCell(finding.Severity), title, finding.Check)
			_, _ = fmt.Fprintf(out, "  grant %s · %s · %s, %s\n", finding.GrantID, accessGrantee(finding, labels), finding.Role, accessScopeText(finding.Scope, finding.Namespace))
			if finding.Detail != "" {
				_, _ = fmt.Fprintf(out, "  %s\n", finding.Detail)
			}
			if finding.Remediation != "" {
				_, _ = fmt.Fprintf(out, "  Fix: %s\n", finding.Remediation)
			}
		}
	}

	switch {
	case !unknownsReported:
		unreported = append(unreported, "unknowns")
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Not evaluated: not reported by the platform, so no check can be read as passed.")
	case len(posture.Unknowns) > 0:
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Not evaluated (%d), unknown rather than passed:\n", len(posture.Unknowns))
		for _, unknown := range posture.Unknowns {
			subject := "every grant"
			if unknown.GrantID != nil && *unknown.GrantID != "" {
				subject = "grant " + *unknown.GrantID
			}
			_, _ = fmt.Fprintf(out, "  %s · %s: %s\n", unknown.Check, subject, unknown.Reason)
		}
	}

	if len(unreported) > 0 {
		return withExitCode(exitError, fmt.Errorf(
			"the platform's access posture for %s is incomplete (%s not reported); nothing above can be read as passed",
			clusterLabel, strings.Join(unreported, ", ")))
	}
	return nil
}

func init() {
	securityCmd.AddCommand(securityAccessCmd)
	securityAccessCmd.Flags().String("cluster", "", "Cluster (name or id), required")
	registerStructuredOutputFlags(securityAccessCmd)
}

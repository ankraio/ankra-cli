package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var aiRemediationCmd = &cobra.Command{
	Use:   "remediation",
	Short: "Read the organisation's AI auto-remediation settings",
	Long: `Read the organisation's AI auto-remediation settings.

Auto-remediation is the lane where an alert trigger opens an AI run that may
change the platform on its own. Its envelope lives in one policy document:
how much the run may do unattended, which tools are exempt from the approval
card, and which clusters it is allowed to touch.

The policy is read-only from the CLI. Changing it is an organisation-admin
action in the portal.`,
}

var aiRemediationPolicyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Show the auto-remediation policy: autonomy level, per-tool overrides and cluster scope",
	Long: `Show the organisation's auto-remediation policy.

An organisation that never saved a policy is not an error: the platform
answers its own defaults, and this command says so rather than presenting
them as a decision somebody made.

Two lines carry most of the risk. A per-tool override of 'auto' means that
tool executes with no approval card at all, and a cluster allow list that was
never set means every cluster is in scope, including Ankra's own.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		policy, readError := apiClient.GetAIRemediationPolicy()
		if readError != nil {
			return aiRemediationPolicyReadError(readError)
		}
		view := newAIRemediationPolicyView(policy)
		if rendered, renderError := renderStructured(cmd, view); rendered || renderError != nil {
			return renderError
		}
		renderAIRemediationPolicy(cmd.OutOrStdout(), policy, view)
		return nil
	},
}

// aiRemediationPolicyReadError renames the one status this read can never
// legitimately answer. The endpoint returns a default document for an
// organisation with no policy row, so it never reports the policy itself as
// missing: a 404 means the platform does not serve this route to API tokens
// at all. Letting it through would exit 3 ("the targeted resource does not
// exist"), which scripts and readers would take as "this organisation has no
// policy" - the opposite of what a 404 here proves.
//
// The status alone decides, deliberately. UnexpectedResponseError.Detail
// exists to tell a backend-authored not-found from a bare router one, and it
// is the right key on a route that has a resource to miss. This one does not:
// whether a 404 arrives bare, from a proxy, or with a detail body, the policy
// was still not answered by a route that would have answered a document. A
// Detail check here would only add a way for the misreading to come back.
func aiRemediationPolicyReadError(readError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) && unexpected.StatusCode == http.StatusNotFound {
		return withExitCode(exitError, errors.New(
			"this platform does not serve the auto-remediation policy to API tokens: "+
				"GET /api/v1/org/ai-remediation/policy is not registered. The policy is readable "+
				"in the portal until the platform ships the token route"))
	}
	return readError
}

// Cluster scope vocabulary for the structured output. The platform's
// dispatcher admits every cluster when the allow list was never set, and
// exactly the listed clusters otherwise, so an empty list admits none.
const (
	aiRemediationScopeAllClusters    = "all_clusters"
	aiRemediationScopeListedClusters = "listed_clusters"
	aiRemediationScopeNoClusters     = "no_clusters"
)

// aiRemediationPolicyView is the -o json|yaml document. It carries the policy
// the platform answered plus the two readings the human rendering spells out,
// so a script never has to re-derive them - and so "no policy is configured"
// survives into the machine-readable output as a field of its own rather than
// as a null timestamp a caller has to know to look at.
//
// ClusterScope is not decoration either: the YAML encoder renders a nil slice
// and an empty one identically as [], so cluster_allow_list alone cannot carry
// "never set" apart from "set to nothing" outside JSON. The named scope can,
// in both encodings.
type aiRemediationPolicyView struct {
	Configured           bool                       `json:"configured" yaml:"configured"`
	ClusterScope         string                     `json:"cluster_scope" yaml:"cluster_scope"`
	ToolsWithoutApproval []string                   `json:"tools_without_approval" yaml:"tools_without_approval"`
	Policy               client.AIRemediationPolicy `json:"policy" yaml:"policy"`
}

func newAIRemediationPolicyView(policy *client.AIRemediationPolicy) aiRemediationPolicyView {
	view := aiRemediationPolicyView{
		Configured:           policy.IsConfigured(),
		ClusterScope:         aiRemediationClusterScope(policy),
		ToolsWithoutApproval: aiRemediationAutoTools(policy),
		Policy:               *policy,
	}
	return view
}

func aiRemediationClusterScope(policy *client.AIRemediationPolicy) string {
	switch {
	case policy.ClusterAllowList == nil:
		return aiRemediationScopeAllClusters
	case len(policy.ClusterAllowList) == 0:
		return aiRemediationScopeNoClusters
	default:
		return aiRemediationScopeListedClusters
	}
}

// aiRemediationAutoTools lists the tools the policy exempts from the approval
// card, sorted so the answer is stable between runs.
func aiRemediationAutoTools(policy *client.AIRemediationPolicy) []string {
	tools := []string{}
	for tool, tier := range policy.TierOverrides {
		if tier == "auto" {
			tools = append(tools, tool)
		}
	}
	sort.Strings(tools)
	return tools
}

func renderAIRemediationPolicy(out io.Writer, policy *client.AIRemediationPolicy,
	view aiRemediationPolicyView) {
	if !view.Configured {
		_, _ = fmt.Fprintln(out,
			"No auto-remediation policy is configured for this organisation.")
		_, _ = fmt.Fprintln(out,
			"What follows is the platform default, not a decision anybody made here.")
		_, _ = fmt.Fprintln(out)
	}

	_, _ = fmt.Fprintf(out, "Auto-remediation:   %s\n", aiRemediationEnabledPhrase(policy.Enabled))
	_, _ = fmt.Fprintf(out, "Autonomy level:     %s\n", aiRemediationAutonomyPhrase(policy.AutonomyLevel))
	_, _ = fmt.Fprintf(out, "Cluster scope:      %s\n", aiRemediationScopePhrase(view.ClusterScope,
		len(policy.ClusterAllowList)))
	for _, clusterID := range policy.ClusterAllowList {
		_, _ = fmt.Fprintf(out, "  - %s\n", clusterID)
	}
	_, _ = fmt.Fprintf(out, "Approvers:          %s\n", aiRemediationApproversPhrase(policy.ApproverUserIDs))
	for _, userID := range policy.ApproverUserIDs {
		_, _ = fmt.Fprintf(out, "  - %s\n", userID)
	}
	_, _ = fmt.Fprintf(out, "Slack webhook:      %s\n", aiRemediationWebhookPhrase(policy.SlackWebhookID))
	_, _ = fmt.Fprintf(out, "Max actions:        %d per incident\n", policy.MaxActionsPerIncident)
	_, _ = fmt.Fprintf(out, "Cooldown:           %d minute(s)\n", policy.CooldownMinutes)
	_, _ = fmt.Fprintf(out, "Last saved:         %s\n", aiRemediationUpdatedPhrase(policy.UpdatedAt))

	renderAIRemediationOverrides(out, policy, view)

	_, _ = fmt.Fprintln(out,
		"\nThe organisation-wide stop is a different switch: ankra ai autonomy status.")
}

func renderAIRemediationOverrides(out io.Writer, policy *client.AIRemediationPolicy,
	view aiRemediationPolicyView) {
	if len(policy.TierOverrides) == 0 {
		_, _ = fmt.Fprintln(out, "\nPer-tool overrides: none, so every tool follows the autonomy level.")
		return
	}
	_, _ = fmt.Fprintln(out, "\nPer-tool overrides:")
	tools := make([]string, 0, len(policy.TierOverrides))
	for tool := range policy.TierOverrides {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Tool", "Tier", "What it means"})
	for _, tool := range tools {
		writer.AppendRow(table.Row{tool, policy.TierOverrides[tool],
			aiRemediationTierMeaning(policy.TierOverrides[tool])})
	}
	writer.Render()
	if len(view.ToolsWithoutApproval) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out,
		"%d tool(s) run at the auto tier, so an auto-remediation run executes them with NO approval card.\n",
		len(view.ToolsWithoutApproval))
}

func aiRemediationEnabledPhrase(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off, so no alert trigger opens an auto-remediation run"
}

// aiRemediationAutonomyPhrase says what each level costs in approvals. An
// unrecognised level is printed bare rather than guessed at.
func aiRemediationAutonomyPhrase(level string) string {
	switch level {
	case "read_only":
		return "read_only (the run investigates and reports; every write is refused)"
	case "propose":
		return "propose (every write waits for a human approval)"
	case "auto":
		return "auto (low-risk reversible writes execute unattended; everything else waits for approval)"
	case "":
		return "(not reported)"
	default:
		return level
	}
}

func aiRemediationScopePhrase(scope string, listed int) string {
	switch scope {
	case aiRemediationScopeAllClusters:
		return "every cluster (no allow list is set)"
	case aiRemediationScopeNoClusters:
		return "no cluster (the allow list is empty, so nothing matches it)"
	default:
		return fmt.Sprintf("%d cluster(s) on the allow list", listed)
	}
}

func aiRemediationApproversPhrase(approverUserIDs []string) string {
	if len(approverUserIDs) == 0 {
		return "none named (organisation admins decide)"
	}
	return fmt.Sprintf("%d user(s)", len(approverUserIDs))
}

func aiRemediationWebhookPhrase(slackWebhookID *string) string {
	if slackWebhookID == nil || *slackWebhookID == "" {
		return "none (approval cards have nowhere to go)"
	}
	return *slackWebhookID
}

func aiRemediationUpdatedPhrase(updatedAt *time.Time) string {
	if updatedAt == nil {
		return "never (no policy has been saved for this organisation)"
	}
	return updatedAt.UTC().Format(time.RFC3339)
}

// aiRemediationTierMeaning translates the closed override vocabulary. An
// unknown value is reported as the platform treats it, which is an approval:
// a typo must never read as widened autonomy.
func aiRemediationTierMeaning(tier string) string {
	switch tier {
	case "auto":
		return "executes with NO approval card"
	case "approval":
		return "parks an approval card for a human"
	case "never":
		return "refused outright"
	default:
		return "unrecognised value; the platform falls back to an approval"
	}
}

func init() {
	registerStructuredOutputFlags(aiRemediationPolicyCmd)
	aiRemediationCmd.AddCommand(aiRemediationPolicyCmd)
	aiCmd.AddCommand(aiRemediationCmd)
}

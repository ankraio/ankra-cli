package cmd

// The organisation cluster access policy, over /api/v1/org/cluster-access-policy
// (PLA-878): the limits every kubectl access grant is held to.
//
// The policy is human-only: no API token can change it. The CLI reads it
// ('get') and PROPOSES a change ('set', ankra-gsjnfb): the platform records
// the proposal and answers a link, and an organisation owner or admin opens
// that link signed in and applies it. There is deliberately no apply or
// dismiss command, and the client has no call for either.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var orgAccessPolicyCmd = &cobra.Command{
	Use:   "access-policy",
	Short: "Show the organisation's Kubernetes access policy, or propose a change to it",
	Long: `Show the limits every kubectl access grant in this organisation is held
to, an admin's included, or propose a change to them:

  creator role       what whoever creates or imports a cluster gets on it
  ceiling            the most access anyone can be given
  elevated lifetime  how long a grant above view may last (also the window
                     'ankra cluster access elevate' may ask for; 4 hours
                     when the policy sets none)
  reason from        the lowest role whose grants need a reason

The policy is changed by a person, never by a token. 'ankra org
access-policy set' proposes a change and prints a link; an organisation
owner or admin opens the link, signed in, and applies it. The CLI cannot
apply it.

Commands:
  get              the policy in force, and a pending proposal if there is one
  set              propose a change (needs kube_access.manage)
  proposals        list proposals
  proposal <id>    show one proposal and its apply link

Every command names the organisation it read or proposed for (the
Organisation line; organisation_id and organisation_name in -o json and
-o yaml). They act on the selected organisation, or on the one --org or
ANKRA_ORG names. When you check another organisation's policy, pass --org
and confirm the name it prints before you report what you read: without
it you read your own organisation's policy, which looks just as real. A
proposal belongs to the organisation it was created in, so 'proposal <id>'
answers not found (exit 3) from any other one, and the message names the
organisation it searched.

Reading requires managing kubectl access, the policy, or holding
break-glass (kube_access.elevate).`,
}

var orgAccessPolicyGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the organisation's Kubernetes access policy",
	Long: `Show the organisation's Kubernetes access policy.

The first line names the organisation that was read (organisation_id and
organisation_name in -o json and -o yaml). The next says whether it has
set a policy at all (Configured: yes or no); with none, the platform
defaults apply. -o json and -o yaml carry that as is_configured. When a proposed change is waiting
for an owner or admin, the last line names it and its apply link
(pending_proposal in -o json and -o yaml).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		read, err := apiClient.GetClusterAccessPolicy(ctx)
		if err != nil {
			return fmt.Errorf("get the cluster access policy: %w", err)
		}
		policy := &read.Policy
		pending, pendingError := pendingPolicyProposal(ctx)
		if pendingError != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"Warning: could not check for a pending proposal (%v); 'ankra org access-policy proposals' lists them.\n", pendingError)
		}
		view := clusterAccessPolicyView{
			ClusterAccessPolicyOrganisation: read.ClusterAccessPolicyOrganisation,
			ClusterAccessPolicy:             *policy,
			PendingProposal:                 pending,
		}
		if rendered, renderError := renderStructured(cmd, view); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		renderPolicyOrganisation(out, "Organisation:       ", read.ClusterAccessPolicyOrganisation)
		renderClusterAccessPolicy(out, policy)
		if pending != nil {
			_, _ = fmt.Fprintf(out, "Pending proposal:   %s by %s, expires %s. Apply: %s\n",
				pending.ID, describeProposer(pending.ProposedBy), formatProposalTime(pending.ExpiresAt), pending.ApplyURL)
		}
		return nil
	},
}

// clusterAccessPolicyView is what 'get' renders as -o json|yaml: the
// organisation that was read, the policy as the API answers it, and the
// pending proposal when there is one.
type clusterAccessPolicyView struct {
	client.ClusterAccessPolicyOrganisation `yaml:",inline"`
	client.ClusterAccessPolicy             `yaml:",inline"`
	PendingProposal                        *client.ClusterAccessPolicyProposal `json:"pending_proposal,omitempty" yaml:"pending_proposal,omitempty"`
}

// renderPolicyOrganisation prints the organisation a policy or proposal
// response describes, under label. It prints nothing for a platform that
// did not say: an empty line would read as "no organisation".
func renderPolicyOrganisation(out io.Writer, label string, organisation client.ClusterAccessPolicyOrganisation) {
	if described := organisation.Describe(); described != "" {
		_, _ = fmt.Fprintf(out, "%s%s\n", label, described)
	}
}

// pendingPolicyProposal returns the organisation's pending proposal, nil
// when there is none. A platform that predates proposals answers 404, which
// is the same as none: 'get' is about the policy and must not fail on it.
// Any other failure is returned so 'get' can say it could not check, rather
// than presenting an unread answer as "no proposal".
func pendingPolicyProposal(ctx context.Context) (*client.ClusterAccessPolicyProposal, error) {
	list, err := apiClient.ListClusterAccessPolicyProposals(ctx, client.ProposalStatusPending)
	if err != nil {
		var unexpected *client.UnexpectedResponseError
		if errors.As(err, &unexpected) && unexpected.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	if list == nil {
		return nil, nil
	}
	for index := range list.Proposals {
		if list.Proposals[index].Status == client.ProposalStatusPending {
			return &list.Proposals[index], nil
		}
	}
	return nil, nil
}

// renderClusterAccessPolicy prints the policy in plain words.
func renderClusterAccessPolicy(out io.Writer, policy *client.ClusterAccessPolicy) {
	suffix := ""
	if policy.IsConfigured {
		_, _ = fmt.Fprintln(out, "Configured:         yes")
	} else {
		_, _ = fmt.Fprintln(out, "Configured:         no (the platform defaults below apply)")
		_, _ = fmt.Fprintln(out, "No policy is set: whoever creates or imports a cluster gets cluster-admin on it, and anyone can be given any access.")
		suffix = " (default)"
	}
	_, _ = fmt.Fprintf(out, "Creator role:       %s%s\n", policy.CreatorGrantRole, suffix)
	_, _ = fmt.Fprintf(out, "Ceiling:            %s%s\n", policy.MaxGrantRole, suffix)
	_, _ = fmt.Fprintf(out, "Elevated lifetime:  %s\n", describeElevatedLifetime(policy.ElevatedMaxTTLSeconds))
	_, _ = fmt.Fprintf(out, "Reason required:    %s\n", describeReasonRequired(policy.RequireReasonFromRole))
}

func describeElevatedLifetime(seconds *int64) string {
	if seconds == nil {
		return "no limit (break-glass: 4h)"
	}
	return (time.Duration(*seconds) * time.Second).String()
}

func describeReasonRequired(role *string) string {
	if role == nil {
		return "only for break-glass"
	}
	return *role + " and above"
}

// Role names the policy accepts. The creator role may also be none (the
// creator gets no access); the ceiling and the reason threshold name a
// grantable role. The CLI checks them only to fail fast; the platform
// decides.
var (
	accessPolicyCreatorRoles = []string{"none", "view", "edit", "admin", "cluster-admin"}
	accessPolicyGrantRoles   = []string{"view", "edit", "admin", "cluster-admin"}
)

// accessPolicyProposalPollInterval paces 'set --wait'. A variable so tests
// can shorten it.
var accessPolicyProposalPollInterval = 5 * time.Second

// accessPolicyProposalWaitTolerance is how many reads in a row --wait
// tolerates failing (a network blip) before it gives up.
const accessPolicyProposalWaitTolerance = 3

const accessPolicyProposalNoApplySentence = "An organisation owner or admin must open the link and apply it; the CLI cannot apply it."

var orgAccessPolicySetCmd = &cobra.Command{
	Use:   "set",
	Short: "Propose a change to the organisation's Kubernetes access policy",
	Long: `Propose a change to the organisation's Kubernetes access policy.

Nothing changes when this runs. The platform records the proposal and
answers a link; an organisation owner or admin must open the link, signed
in, and apply it. The CLI cannot apply it: the policy is changed by a
person, never by a token.

Pass only what should change; every other field keeps its live value. The
command prints the policy before and after (changed rows marked *), the
proposal id, when it expires (7 days) and the apply link. A new proposal
replaces one that is still pending.

  --creator-role         none, view, edit, admin or cluster-admin
  --max-role             view, edit, admin or cluster-admin (the ceiling)
  --elevated-max-ttl     how long a grant above view may last: 30m, 4h,
                         7d; none removes the limit
  --require-reason-from  view, edit, admin or cluster-admin; none means
                         only break-glass needs a reason

--wait blocks until the proposal is decided. Exit codes: 0 applied, 4
dismissed by an owner or admin, 1 expired or replaced by a newer
proposal, 5 still pending when --timeout ran out (it stays pending and
can still be applied).

Proposing needs kube_access.manage.

Examples:
  ankra org access-policy set --max-role edit --creator-role view --reason "PLA-878: no standing admin"
  ankra org access-policy set --elevated-max-ttl 4h --require-reason-from edit --reason "audit finding 12"
  ankra org access-policy set --max-role edit --reason "tighten" --wait --timeout 1h`,
	Args: cobra.NoArgs,
	RunE: runOrgAccessPolicySet,
}

func runOrgAccessPolicySet(cmd *cobra.Command, args []string) error {
	request, err := accessPolicyProposalRequestFromFlags(cmd)
	if err != nil {
		return withExitCode(exitUsage, err)
	}
	if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
		return formatError
	}
	wait, _ := cmd.Flags().GetBool("wait")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	if wait && timeout <= 0 {
		return withExitCode(exitUsage, errors.New("--timeout must be positive"))
	}

	proposeContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	proposal, err := apiClient.ProposeClusterAccessPolicy(proposeContext, request)
	cancel()
	if err != nil {
		return fmt.Errorf("propose the cluster access policy: %w", err)
	}
	format, _ := structuredFormatFromFlags(cmd)
	if !wait {
		if rendered, renderError := renderStructured(cmd, proposal); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, "Proposed a change to the organisation's Kubernetes access policy. Nothing has changed yet.")
		_, _ = fmt.Fprintln(out)
		renderPolicyProposal(out, proposal)
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, accessPolicyProposalNoApplySentence)
		return nil
	}

	// With --wait the human text goes to stderr while waiting, so -o json
	// still prints exactly one document: the proposal as it ended.
	progress := cmd.ErrOrStderr()
	if format == outputDefault {
		progress = cmd.OutOrStdout()
	}
	_, _ = fmt.Fprintln(progress, "Proposed a change to the organisation's Kubernetes access policy. Nothing has changed yet.")
	_, _ = fmt.Fprintln(progress)
	renderPolicyProposal(progress, proposal)
	_, _ = fmt.Fprintln(progress)
	_, _ = fmt.Fprintln(progress, accessPolicyProposalNoApplySentence)
	_, _ = fmt.Fprintf(progress, "Waiting up to %s for it to be applied...\n", timeout)

	waitContext, cancelWait := context.WithTimeout(cmd.Context(), timeout)
	defer cancelWait()
	final, waitError := waitForPolicyProposal(waitContext, proposal.ID)
	if final == nil {
		final = proposal
	}
	if waitError != nil {
		if errors.Is(waitError, context.DeadlineExceeded) && cmd.Context().Err() == nil {
			return withExitCode(exitWaitTimeout, fmt.Errorf(
				"proposal %s was not applied within %s; it stays pending until %s and can still be applied at %s",
				final.ID, timeout, formatProposalTime(final.ExpiresAt), final.ApplyURL))
		}
		return fmt.Errorf("wait for proposal %s: %w", final.ID, waitError)
	}
	if rendered, renderError := renderStructured(cmd, final); renderError != nil {
		return renderError
	} else if !rendered {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), proposalOutcomeSentence(final))
		if final.Status == client.ProposalStatusApplied {
			_, _ = fmt.Fprintln(cmd.OutOrStdout())
			renderClusterAccessPolicy(cmd.OutOrStdout(), &final.Current)
		}
	}
	return proposalOutcomeError(final)
}

// proposalOutcomeError is the exit status of a decided proposal: nil when
// it was applied, exit 4 when a person dismissed it, exit 1 when it expired
// or a newer proposal replaced it.
func proposalOutcomeError(proposal *client.ClusterAccessPolicyProposal) error {
	switch proposal.Status {
	case client.ProposalStatusApplied:
		return nil
	case client.ProposalStatusDismissed:
		return withExitCode(exitCancelled, fmt.Errorf("proposal %s was dismissed by an owner or admin; the policy did not change", proposal.ID))
	case client.ProposalStatusSuperseded:
		return fmt.Errorf("proposal %s was replaced by a newer proposal; the policy did not change", proposal.ID)
	case client.ProposalStatusExpired:
		return fmt.Errorf("proposal %s expired before anyone applied it; the policy did not change", proposal.ID)
	}
	return fmt.Errorf("proposal %s ended as %q", proposal.ID, proposal.Status)
}

// proposalOutcomeSentence says how a decided proposal ended.
func proposalOutcomeSentence(proposal *client.ClusterAccessPolicyProposal) string {
	switch proposal.Status {
	case client.ProposalStatusApplied:
		when := ""
		if proposal.DecidedAt != nil {
			when = " at " + formatProposalTime(*proposal.DecidedAt)
		}
		return fmt.Sprintf("Proposal %s was applied%s. The policy now in force:", proposal.ID, when)
	case client.ProposalStatusDismissed:
		return fmt.Sprintf("Proposal %s was dismissed. The policy did not change.", proposal.ID)
	case client.ProposalStatusSuperseded:
		return fmt.Sprintf("Proposal %s was replaced by a newer proposal. The policy did not change.", proposal.ID)
	case client.ProposalStatusExpired:
		return fmt.Sprintf("Proposal %s expired. The policy did not change.", proposal.ID)
	}
	return fmt.Sprintf("Proposal %s is %s.", proposal.ID, proposal.Status)
}

// waitForPolicyProposal polls the proposal until it is no longer pending or
// the context ends. It tolerates a few failed reads in a row.
func waitForPolicyProposal(ctx context.Context, proposalID string) (*client.ClusterAccessPolicyProposal, error) {
	var last *client.ClusterAccessPolicyProposal
	failures := 0
	for {
		timer := time.NewTimer(accessPolicyProposalPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
		proposal, err := apiClient.GetClusterAccessPolicyProposal(ctx, proposalID)
		if err != nil {
			if ctx.Err() != nil {
				return last, ctx.Err()
			}
			failures++
			if failures >= accessPolicyProposalWaitTolerance {
				return last, err
			}
			continue
		}
		failures = 0
		last = proposal
		if proposal.IsFinal() {
			return proposal, nil
		}
	}
}

// accessPolicyProposalRequestFromFlags builds the proposal from the flags,
// refusing what can never be valid before anything is sent.
func accessPolicyProposalRequestFromFlags(cmd *cobra.Command) (client.ProposeClusterAccessPolicyRequest, error) {
	var request client.ProposeClusterAccessPolicyRequest
	flags := cmd.Flags()
	reason, _ := flags.GetString("reason")
	if strings.TrimSpace(reason) == "" {
		return request, errors.New("--reason is required: say why the policy should change; the owner or admin who applies it reads it")
	}
	request.Reason = reason

	changed := false
	if flags.Changed("creator-role") {
		role, _ := flags.GetString("creator-role")
		if !slices.Contains(accessPolicyCreatorRoles, role) {
			return request, fmt.Errorf("--creator-role %q: must be one of %s", role, strings.Join(accessPolicyCreatorRoles, ", "))
		}
		request.CreatorGrantRole = &role
		changed = true
	}
	if flags.Changed("max-role") {
		role, _ := flags.GetString("max-role")
		if !slices.Contains(accessPolicyGrantRoles, role) {
			return request, fmt.Errorf("--max-role %q: must be one of %s", role, strings.Join(accessPolicyGrantRoles, ", "))
		}
		request.MaxGrantRole = &role
		changed = true
	}
	if flags.Changed("elevated-max-ttl") {
		value, _ := flags.GetString("elevated-max-ttl")
		if strings.EqualFold(strings.TrimSpace(value), "none") {
			request.ClearElevatedMaxTTL = true
		} else {
			seconds, parseError := parsePolicyLifetime(value)
			if parseError != nil {
				return request, fmt.Errorf("--elevated-max-ttl %q: %w", value, parseError)
			}
			request.ElevatedMaxTTLSeconds = &seconds
		}
		changed = true
	}
	if flags.Changed("require-reason-from") {
		role, _ := flags.GetString("require-reason-from")
		if strings.EqualFold(strings.TrimSpace(role), "none") {
			request.ClearRequireReasonFromRole = true
		} else {
			if !slices.Contains(accessPolicyGrantRoles, role) {
				return request, fmt.Errorf("--require-reason-from %q: must be none or one of %s", role, strings.Join(accessPolicyGrantRoles, ", "))
			}
			request.RequireReasonFromRole = &role
		}
		changed = true
	}
	if !changed {
		return request, errors.New("nothing to propose: pass at least one of --creator-role, --max-role, --elevated-max-ttl or --require-reason-from")
	}
	return request, nil
}

// parsePolicyLifetime reads a lifetime as whole seconds: a Go duration
// (90m, 4h) or whole days (7d).
func parsePolicyLifetime(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	var lifetime time.Duration
	if days, isDays := strings.CutSuffix(trimmed, "d"); isDays {
		count, err := strconv.Atoi(days)
		if err != nil || count <= 0 {
			return 0, errors.New("must be a duration such as 30m, 4h or 7d, or none")
		}
		lifetime = time.Duration(count) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(trimmed)
		if err != nil {
			return 0, errors.New("must be a duration such as 30m, 4h or 7d, or none")
		}
		lifetime = parsed
	}
	if lifetime < time.Second {
		return 0, errors.New("must be positive")
	}
	return int64(lifetime / time.Second), nil
}

// renderPolicyProposal prints one proposal: who, why, when, the policy
// before and after with the changed rows marked, and the apply link.
func renderPolicyProposal(out io.Writer, proposal *client.ClusterAccessPolicyProposal) {
	_, _ = fmt.Fprintf(out, "Proposal:     %s\n", proposal.ID)
	renderPolicyOrganisation(out, "Organisation: ", proposal.ClusterAccessPolicyOrganisation)
	_, _ = fmt.Fprintf(out, "Status:       %s\n", describeProposalStatus(proposal.Status))
	_, _ = fmt.Fprintf(out, "Proposed by:  %s\n", describeProposer(proposal.ProposedBy))
	_, _ = fmt.Fprintf(out, "Reason:       %s\n", proposal.Reason)
	if proposal.CreatedAt != "" {
		_, _ = fmt.Fprintf(out, "Created:      %s\n", formatProposalTime(proposal.CreatedAt))
	}
	if proposal.Status == client.ProposalStatusPending {
		_, _ = fmt.Fprintf(out, "Expires:      %s\n", formatProposalTime(proposal.ExpiresAt))
	} else if proposal.DecidedAt != nil {
		_, _ = fmt.Fprintf(out, "Decided:      %s\n", formatProposalTime(*proposal.DecidedAt))
	}
	_, _ = fmt.Fprintln(out)

	// While pending, the comparison is with the policy in force; once
	// decided, with the policy it was proposed against.
	before, beforeLabel := proposal.Current, "CURRENT"
	if proposal.Status != client.ProposalStatusPending {
		before, beforeLabel = proposal.Base, "BEFORE"
	}
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(writer, "  \t\t%s\tPROPOSED\n", beforeLabel)
	for _, row := range policyComparisonRows(before, proposal.Proposed) {
		marker := " "
		if row.changed {
			marker = "*"
		}
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", marker, row.label, row.before, row.after)
	}
	_ = writer.Flush()
	_, _ = fmt.Fprintln(out, "(* changed)")

	if proposal.Status == client.ProposalStatusPending && !proposal.BaseMatchesCurrent {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "The policy has changed since this was proposed, so applying it will be refused. Propose again.")
	}
	if proposal.Status == client.ProposalStatusPending && proposal.ApplyURL != "" {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Apply:        %s\n", proposal.ApplyURL)
	}
}

type policyComparisonRow struct {
	label   string
	before  string
	after   string
	changed bool
}

func policyComparisonRows(before client.ClusterAccessPolicy, after client.ProposedClusterAccessPolicy) []policyComparisonRow {
	rows := []policyComparisonRow{
		{label: "Creator role", before: before.CreatorGrantRole, after: after.CreatorGrantRole},
		{label: "Ceiling", before: before.MaxGrantRole, after: after.MaxGrantRole},
		{label: "Elevated lifetime", before: describeElevatedLifetime(before.ElevatedMaxTTLSeconds),
			after: describeElevatedLifetime(after.ElevatedMaxTTLSeconds)},
		{label: "Reason required", before: describeReasonRequired(before.RequireReasonFromRole),
			after: describeReasonRequired(after.RequireReasonFromRole)},
	}
	for index := range rows {
		rows[index].changed = rows[index].before != rows[index].after
	}
	return rows
}

func describeProposalStatus(status string) string {
	switch status {
	case client.ProposalStatusPending:
		return "pending (waiting for an owner or admin to apply it)"
	case client.ProposalStatusApplied:
		return "applied"
	case client.ProposalStatusDismissed:
		return "dismissed (the policy did not change)"
	case client.ProposalStatusExpired:
		return "expired (the policy did not change)"
	case client.ProposalStatusSuperseded:
		return "superseded by a newer proposal (the policy did not change)"
	}
	return status
}

func describeProposer(proposer client.ClusterAccessPolicyProposer) string {
	who := proposer.Principal
	if who == "" {
		who = "unknown"
	}
	if proposer.TokenName != nil && *proposer.TokenName != "" {
		who += fmt.Sprintf(" (token %q)", *proposer.TokenName)
	}
	return who
}

// formatProposalTime prints a platform timestamp in UTC to the minute, or
// the raw value when it does not parse.
func formatProposalTime(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format("2006-01-02 15:04 UTC")
}

var orgAccessPolicyProposalsCmd = &cobra.Command{
	Use:   "proposals",
	Short: "List proposed changes to the organisation's Kubernetes access policy",
	Long: `List proposed changes to the organisation's Kubernetes access policy,
newest first. --status pending (the default) shows the one waiting for an
owner or admin; --status all shows every proposal, and applied, dismissed,
expired or superseded show those alone.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		status, _ := cmd.Flags().GetString("status")
		filter := status
		switch status {
		case "all":
			filter = ""
		case client.ProposalStatusPending, client.ProposalStatusApplied, client.ProposalStatusDismissed,
			client.ProposalStatusExpired, client.ProposalStatusSuperseded:
		default:
			return withExitCode(exitUsage, fmt.Errorf(
				"--status %q: must be pending, applied, dismissed, expired, superseded or all", status))
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		list, err := apiClient.ListClusterAccessPolicyProposals(ctx, filter)
		if err != nil {
			return fmt.Errorf("list cluster access policy proposals: %w", err)
		}
		if rendered, renderError := renderStructured(cmd, list); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		renderPolicyOrganisation(out, "Organisation: ", list.ClusterAccessPolicyOrganisation)
		if len(list.Proposals) == 0 {
			if filter == "" {
				_, _ = fmt.Fprintln(out, "No proposals.")
			} else {
				_, _ = fmt.Fprintf(out, "No %s proposals.\n", filter)
			}
			return nil
		}
		writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "ID\tSTATUS\tPROPOSED BY\tCHANGES\tCREATED\tEXPIRES")
		for _, proposal := range list.Proposals {
			_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", proposal.ID, proposal.Status,
				describeProposer(proposal.ProposedBy), describeProposalChanges(proposal),
				formatProposalTime(proposal.CreatedAt), formatProposalTime(proposal.ExpiresAt))
		}
		_ = writer.Flush()
		if list.IsCapped {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Only the newest proposals are shown.")
		}
		for _, proposal := range list.Proposals {
			if proposal.Status == client.ProposalStatusPending && proposal.ApplyURL != "" {
				_, _ = fmt.Fprintf(out, "\nApply %s: %s\n%s\n", proposal.ID, proposal.ApplyURL, accessPolicyProposalNoApplySentence)
			}
		}
		return nil
	},
}

// describeProposalChanges names what a proposal changes against the policy
// it was proposed against, e.g. "ceiling edit, creator role view".
func describeProposalChanges(proposal client.ClusterAccessPolicyProposal) string {
	var changes []string
	for _, row := range policyComparisonRows(proposal.Base, proposal.Proposed) {
		if row.changed {
			changes = append(changes, strings.ToLower(row.label[:1])+row.label[1:]+" "+row.after)
		}
	}
	if len(changes) == 0 {
		return "-"
	}
	return strings.Join(changes, ", ")
}

var orgAccessPolicyProposalCmd = &cobra.Command{
	Use:   "proposal <id>",
	Short: "Show one proposed change to the organisation's Kubernetes access policy",
	Long: `Show one proposed change to the organisation's Kubernetes access policy:
who proposed it and why, the policy before and after (changed rows marked
*), its status, and while it is pending the link an owner or admin applies
it from. The CLI cannot apply it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		proposal, err := apiClient.GetClusterAccessPolicyProposal(ctx, args[0])
		if err != nil {
			return fmt.Errorf("get cluster access policy proposal %s: %w", args[0], err)
		}
		if rendered, renderError := renderStructured(cmd, proposal); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		renderPolicyProposal(out, proposal)
		if proposal.Status == client.ProposalStatusPending {
			_, _ = fmt.Fprintln(out)
			_, _ = fmt.Fprintln(out, accessPolicyProposalNoApplySentence)
		}
		return nil
	},
}

func init() {
	setFlags := orgAccessPolicySetCmd.Flags()
	setFlags.String("creator-role", "", "Role whoever creates or imports a cluster gets on it: none, view, edit, admin or cluster-admin")
	setFlags.String("max-role", "", "The most access anyone can be given (the ceiling): view, edit, admin or cluster-admin")
	setFlags.String("elevated-max-ttl", "", "How long a grant above view may last (30m, 4h, 7d), or none for no limit")
	setFlags.String("require-reason-from", "", "Lowest role whose grants need a reason: view, edit, admin or cluster-admin, or none")
	setFlags.String("reason", "", "Why the policy should change; the owner or admin who applies it reads it (required)")
	setFlags.Bool("wait", false, "Wait until an owner or admin applies or dismisses the proposal")
	setFlags.Duration("timeout", 30*time.Minute, "How long --wait waits")
	_ = orgAccessPolicySetCmd.MarkFlagRequired("reason")

	orgAccessPolicyProposalsCmd.Flags().String("status", client.ProposalStatusPending,
		"pending, applied, dismissed, expired, superseded or all")

	registerStructuredOutputFlags(orgAccessPolicyGetCmd, orgAccessPolicySetCmd, orgAccessPolicyProposalsCmd, orgAccessPolicyProposalCmd)
	orgAccessPolicyCmd.AddCommand(orgAccessPolicyGetCmd, orgAccessPolicySetCmd, orgAccessPolicyProposalsCmd, orgAccessPolicyProposalCmd)
	orgCmd.AddCommand(orgAccessPolicyCmd)
}

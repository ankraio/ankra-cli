package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var (
	accessClusterFlag   string
	accessRoleFlag      string
	accessNamespaceFlag string
	accessExpiresFlag   string
	accessReasonFlag    string

	// elevate has variables of its own: cobra writes a flag's default into
	// its variable when the flag is registered, so sharing grant's would
	// let elevate's "edit" default become grant's (whose default is view).
	elevateClusterFlag   string
	elevateRoleFlag      string
	elevateNamespaceFlag string
	elevateExpiresFlag   string
	elevateReasonFlag    string
)

var accessRoles = []string{"view", "edit", "admin", "cluster-admin"}

var clusterAccessCmd = &cobra.Command{
	Use:   "access",
	Short: "Manage who can reach a cluster through the Ankra kube gateway",
	Long: `List, grant, and revoke per-user access to a cluster's Kubernetes API
through the Ankra gateway (the access used by 'ankra cluster kubeconfig' and
'ankra cluster kube-token').

Managing access requires organisation admin rights. Grants apply to one
cluster and one organisation member, identified by email. A grant can be
time-boxed with --expires and carry a --reason; the organisation access
policy ('ankra org access-policy') may require both.

A service account has no email: 'list' names it "service account <name>",
and it gets access with 'elevate', run with its own token.

Break-glass: 'elevate' gives the caller itself time-boxed access, within the
policy. It needs only the kube_access.elevate permission, so automation
running as a service account can use it without being able to grant anyone
else.

'list' shows where each grant came from (Source: manual, creator, role,
elevated) and whether the current policy would refuse it today (Over policy).
A grant whose source is "role" comes from the member's organisation role and
cannot be revoked here: the platform refuses it, because the role sync would
re-create it. Change the member's role, or the role's Kubernetes access, to
remove it.

Exit codes:
  2  a flag is missing or invalid, or the organisation access policy refused
     the request (a role above the ceiling, a missing or too long --expires,
     a missing --reason): change the flags and run it again. Also revoke of a
     grant that comes from an organisation role: change the member's role
  3  the grant or member was not found on the cluster (revoke)
  6  the credentials are missing, expired or rejected: run 'ankra login'
  7  your role does not allow this (for example elevate without
     kube_access.elevate, or grant without managing kubectl access): ask an
     organisation admin

Examples:
  ankra cluster access list --cluster my-cluster
  ankra cluster access grant user@example.com --cluster my-cluster --role view
  ankra cluster access grant user@example.com --cluster my-cluster --role edit --namespace staging
  ankra cluster access grant user@example.com --cluster my-cluster --role admin --expires 4h --reason "maintenance window"
  ankra cluster access elevate --cluster my-cluster --role edit --expires 4h --reason "incident 4711"
  ankra cluster access revoke user@example.com --cluster my-cluster
  ankra cluster access revoke 6f1f9aca-2c3d-4e5f-8a9b-0c1d2e3f4a5b --cluster my-cluster`,
	Annotations: map[string]string{"group": "kubernetes"},
}

var clusterAccessListCmd = &cobra.Command{
	Use:   "list",
	Short: "List access grants for a cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, _, err := resolveGatewayClusterID(accessClusterFlag, os.Stderr)
		if err != nil {
			return err
		}

		grants, err := apiClient.ListClusterAccessGrants(context.Background(), clusterID)
		if err != nil {
			return err
		}

		if handled, err := renderStructured(cmd, grants.Result); err != nil {
			return err
		} else if handled {
			return nil
		}
		if len(grants.Result) == 0 {
			fmt.Println("No access grants found. Add one with: ankra cluster access grant <email> --role view")
			return nil
		}
		renderGrantsTable(grants.Result, granteeLabelsForGrants(grants.Result))
		return nil
	},
}

var clusterAccessGrantCmd = &cobra.Command{
	Use:   "grant <email>",
	Short: "Grant a member access to a cluster through the kube gateway",
	Long: `Grant an organisation member access to a cluster's Kubernetes API through
the Ankra gateway.

The grant is cluster-wide by default; pass --namespace to limit it to one
namespace. Roles map to the standard Kubernetes ClusterRoles: view, edit,
admin, cluster-admin.

Exits 2 when a flag is invalid or the organisation access policy refuses the
grant (change --role, --expires or --reason), and 7 when your role does not
allow managing kubectl access. See 'ankra cluster access --help' for every
code.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		email := args[0]
		if err := validateAccessRole(accessRoleFlag); err != nil {
			return err
		}
		clusterID, _, err := resolveGatewayClusterID(accessClusterFlag, os.Stderr)
		if err != nil {
			return err
		}

		expiresIn, expiresAt, err := parseAccessExpiry(accessExpiresFlag)
		if err != nil {
			return err
		}
		request := client.CreateClusterAccessGrantRequest{
			UserEmail: email,
			Scope:     "cluster",
			Role:      accessRoleFlag,
			ExpiresIn: expiresIn,
			ExpiresAt: expiresAt,
		}
		if accessNamespaceFlag != "" {
			namespace := accessNamespaceFlag
			request.Scope = "namespace"
			request.Namespace = &namespace
		}
		if accessReasonFlag != "" {
			reason := accessReasonFlag
			request.Reason = &reason
		}

		created, err := apiClient.CreateClusterAccessGrant(context.Background(), clusterID, request)
		if err != nil {
			return err
		}

		if handled, err := renderStructured(cmd, created.Grant); err != nil {
			return err
		} else if handled {
			return nil
		}
		grant := created.Grant
		fmt.Printf("Granted %s role %q (%s scope) on the cluster.\n", email, grant.Role, grant.Scope)
		fmt.Printf("  Grant ID: %s\n", grant.ID)
		if grant.ExpiresAt != nil {
			fmt.Printf("  Expires:  %s\n", *grant.ExpiresAt)
		}
		fmt.Println("The grant is applied to the cluster by the RBAC reconciler within about 30 to 60 seconds; check status with: ankra cluster access list")
		fmt.Printf("The member can now run: ankra cluster kubeconfig add --cluster %s --use\n", displayClusterReference())
		return nil
	},
}

var clusterAccessElevateCmd = &cobra.Command{
	Use:   "elevate",
	Short: "Give yourself time-boxed access to a cluster (break-glass)",
	Long: `Give the identity this CLI runs as - you, or a service account's token -
time-boxed access to a cluster through the kube gateway.

This is break-glass within the organisation access policy: the access must
end (--expires, at most the policy's limit or 4 hours) and needs a --reason,
and it can never go above the policy's ceiling. It needs the
kube_access.elevate permission, which grants nothing else: no access for
anyone else, no standing access, and no way to change the policy. Running it
again with a new --expires extends your own elevation; end it early with
'ankra cluster access revoke <grant-id>'.

The binding reaches the cluster within about 30 to 60 seconds; until then
kubectl may still answer 403 Forbidden, so retry it.

Every elevation is recorded in the audit log and notifies the organisation's
notification routes.

Exits 2 when --expires or --reason is missing or a flag is invalid, and when
the organisation access policy refuses the request; 7 when your role lacks
kube_access.elevate. See 'ankra cluster access --help' for every code.`,
	Example: `  ankra cluster access elevate --cluster prod --role edit --expires 4h --reason "incident 4711: restart the stuck rollout"
  ankra cluster access elevate --cluster prod --role edit --namespace payments --expires 30m --reason "hotfix"`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateAccessRole(elevateRoleFlag); err != nil {
			return err
		}
		if strings.TrimSpace(elevateExpiresFlag) == "" {
			return withExitCode(exitUsage, errors.New("--expires is required: break-glass access must end (for example --expires 4h)"))
		}
		if strings.TrimSpace(elevateReasonFlag) == "" {
			return withExitCode(exitUsage, errors.New("--reason is required: say why the access is needed"))
		}
		expiresIn, expiresAt, err := parseAccessExpiry(elevateExpiresFlag)
		if err != nil {
			return err
		}
		clusterID, _, err := resolveGatewayClusterID(elevateClusterFlag, os.Stderr)
		if err != nil {
			return err
		}
		request := client.ElevateClusterAccessRequest{
			Scope:     "cluster",
			Role:      elevateRoleFlag,
			ExpiresIn: expiresIn,
			ExpiresAt: expiresAt,
			Reason:    elevateReasonFlag,
		}
		if elevateNamespaceFlag != "" {
			namespace := elevateNamespaceFlag
			request.Scope = "namespace"
			request.Namespace = &namespace
		}
		created, err := apiClient.ElevateClusterAccess(context.Background(), clusterID, request)
		if err != nil {
			return err
		}
		if handled, err := renderStructured(cmd, created.Grant); err != nil {
			return err
		} else if handled {
			return nil
		}
		grant := created.Grant
		until := "-"
		if grant.ExpiresAt != nil {
			until = *grant.ExpiresAt
		}
		fmt.Printf("Elevated to %q (%s scope) until %s.\n", grant.Role, grant.Scope, until)
		fmt.Printf("  Grant ID: %s\n", grant.ID)
		if grant.ReconcileStatus != "" {
			fmt.Printf("  Status:   %s\n", formatReconcileStatus(grant))
		}
		switch grant.ReconcileStatus {
		case "applied":
		case "failed":
			fmt.Println("The binding could not be applied to the cluster (see Status); retrying kubectl will not help until it is. Check with: ankra cluster access list")
		case "cluster_offline":
			fmt.Println("The cluster is offline: the binding is applied when it reconnects. Check with: ankra cluster access list")
		default:
			fmt.Println("The binding reaches the cluster within about 30 to 60 seconds. Until then kubectl may answer 403 Forbidden: retry it, or check with: ankra cluster access list")
		}
		fmt.Printf("End it early with: ankra cluster access revoke %s --cluster %s\n", grant.ID, elevateClusterReference())
		return nil
	},
}

var clusterAccessRevokeCmd = &cobra.Command{
	Use:   "revoke <grant-id|email>",
	Short: "Revoke access grants from a cluster",
	Long: `Revoke gateway access from a cluster.

Pass a grant ID (from 'ankra cluster access list') to revoke a single grant,
or an email address to revoke every grant that member has on the cluster.

A grant whose Source is "role" in 'ankra cluster access list' comes from the
member's organisation role and cannot be revoked here: the platform refuses
it, because the role sync would re-create it. Change the member's role, or
the role's Kubernetes access, to remove it. Revoking by email still revokes
the member's other grants, then names the ones it kept and exits 2, so a
script never reads the member's access as gone while a role grant remains.

Exits 2 when a grant comes from an organisation role, 3 when no grant
matches, and 7 when your role does not allow managing kubectl access.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]
		clusterID, _, err := resolveGatewayClusterID(accessClusterFlag, os.Stderr)
		if err != nil {
			return err
		}

		grants, err := resolveGrantTargets(clusterID, target)
		if err != nil {
			return err
		}

		var kept []string
		var keptRefusal error
		for _, grant := range grants {
			_, err := apiClient.DeleteClusterAccessGrant(context.Background(), clusterID, grant.ID)
			var roleGrant *client.ClusterAccessRoleGrantError
			if errors.As(err, &roleGrant) {
				kept = append(kept, grant.ID)
				if keptRefusal == nil {
					keptRefusal = err
				}
				continue
			}
			if err != nil {
				return err
			}
			fmt.Printf("Revoked grant %s\n", grant.ID)
		}
		return roleGrantsKeptError(kept, keptRefusal)
	},
}

// roleGrantsKeptError reports the grants a revoke left because they come from
// the member's organisation role. The platform's refusal stays in the chain,
// so the command exits 2 (see exitCodeFor); nil when every grant was revoked.
func roleGrantsKeptError(kept []string, refusal error) error {
	switch len(kept) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("grant %s was not revoked: %w", kept[0], refusal)
	default:
		return fmt.Errorf("grants %s were not revoked: %w", strings.Join(kept, ", "), refusal)
	}
}

// resolveGrantTargets turns revoke's argument into the grants to revoke. A
// grant ID is taken as it is; an email lists the cluster's grants and keeps
// that member's, sources included. Whether a grant can be revoked is the
// platform's answer (409 for a role grant), not decided from the listing.
func resolveGrantTargets(clusterID string, target string) ([]client.ClusterAccessGrant, error) {
	if isLikelyClusterID(target) {
		return []client.ClusterAccessGrant{{ID: target}}, nil
	}
	grants, err := apiClient.ListClusterAccessGrants(context.Background(), clusterID)
	if err != nil {
		return nil, err
	}
	var matched []client.ClusterAccessGrant
	for _, grant := range grants.Result {
		if grant.UserEmail != nil && strings.EqualFold(*grant.UserEmail, target) {
			matched = append(matched, grant)
		}
	}
	if len(matched) == 0 {
		return nil, withExitCode(exitNotFound, fmt.Errorf("no access grants found for %q on this cluster", target))
	}
	return matched, nil
}

func isRoleSourcedGrant(grant client.ClusterAccessGrant) bool {
	return grant.Source != nil && *grant.Source == client.ClusterAccessGrantSourceRole
}

var wholeDaysPattern = regexp.MustCompile(`^[1-9][0-9]*d$`)

// parseAccessExpiry turns --expires into the platform's two shapes: an RFC
// 3339 time becomes expires_at, a duration ("30m", "4h", "1h30m") or whole
// days ("7d") becomes expires_in. Empty is a standing grant.
func parseAccessExpiry(value string) (*string, *string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil, nil
	}
	if _, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return nil, &trimmed, nil
	}
	if wholeDaysPattern.MatchString(trimmed) {
		return &trimmed, nil, nil
	}
	if duration, err := time.ParseDuration(trimmed); err == nil && duration > 0 {
		return &trimmed, nil, nil
	}
	return nil, nil, withExitCode(exitUsage,
		fmt.Errorf("invalid --expires %q: use a duration such as 30m, 4h or 7d, or an RFC 3339 time", value))
}

func validateAccessRole(role string) error {
	for _, allowed := range accessRoles {
		if role == allowed {
			return nil
		}
	}
	return withExitCode(exitUsage, fmt.Errorf("invalid role %q; valid roles: %s", role, strings.Join(accessRoles, ", ")))
}

// elevateClusterReference is displayClusterReference for elevate's own
// --cluster flag.
func elevateClusterReference() string {
	if elevateClusterFlag != "" {
		return elevateClusterFlag
	}
	selected, err := loadSelectedCluster()
	if err == nil && selected.Name != "" {
		return selected.Name
	}
	return "<cluster>"
}

func displayClusterReference() string {
	if accessClusterFlag != "" {
		return accessClusterFlag
	}
	selected, err := loadSelectedCluster()
	if err == nil && selected.Name != "" {
		return selected.Name
	}
	return "<cluster>"
}

// granteeLabelsForGrants names the service principals among the grantees.
// The roster is read only when a grantee has no email, which is what a
// service account looks like on a grant. One roster serves the whole list
// because a listing is one cluster's grants, and the platform scopes it to
// that cluster's organisation, so every row carries the same organisation
// id. A grantee the roster does not name keeps its id.
func granteeLabelsForGrants(grants []client.ClusterAccessGrant) map[string]string {
	organisationID := ""
	needed := false
	for _, grant := range grants {
		if grant.UserEmail == nil || strings.TrimSpace(*grant.UserEmail) == "" {
			needed = true
		}
		if organisationID == "" {
			organisationID = grant.OrganisationID
		}
	}
	if !needed {
		return nil
	}
	if organisationID == "" {
		organisationID = effectiveOrganisationID()
	}
	return servicePrincipalLabels(organisationID)
}

// servicePrincipalLabels maps the Ankra user id of each service principal in
// the organisation (lower-cased) to how a person reads it: "service account
// ci-deployer". The grant and access posture payloads carry no name for a
// grantee without an email, so the organisation's member list supplies it.
// Best effort: a roster that cannot be read leaves every grantee shown by
// its id, as before.
func servicePrincipalLabels(organisationID string) map[string]string {
	if organisationID == "" {
		return nil
	}
	members, err := apiClient.ListOrganisationUsers(organisationID)
	if err != nil {
		return nil
	}
	labels := map[string]string{}
	for _, member := range members {
		if member.ID == nil || member.ServicePrincipalName == nil || strings.TrimSpace(*member.ServicePrincipalName) == "" {
			continue
		}
		labels[strings.ToLower(*member.ID)] = servicePrincipalLabel(member.ServicePrincipalKind, *member.ServicePrincipalName)
	}
	return labels
}

// servicePrincipalLabel is "service account <name>", or "<kind> <name>" for
// another kind of service principal.
func servicePrincipalLabel(kind *string, name string) string {
	kindText := "service account"
	if kind != nil && strings.TrimSpace(*kind) != "" {
		kindText = strings.ReplaceAll(strings.TrimSpace(*kind), "_", " ")
	}
	return kindText + " " + strings.TrimSpace(name)
}

// granteeLabel names a grantee: its email, else its service principal
// name, else its Ankra user id.
func granteeLabel(email *string, ankraUserID string, labels map[string]string) (string, bool) {
	if email != nil && strings.TrimSpace(*email) != "" {
		return *email, true
	}
	if label, found := labels[strings.ToLower(ankraUserID)]; found {
		return label, true
	}
	return ankraUserID, false
}

func renderGrantsTable(grants []client.ClusterAccessGrant, labels map[string]string) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"ID", "User", "Scope", "Namespace", "Role", "Source", "Expires", "Reason", "Over policy", "Status", "Created"})
	roleSourced := false
	for _, grant := range grants {
		user, _ := granteeLabel(grant.UserEmail, grant.AnkraUserID, labels)
		namespace := "-"
		if grant.Namespace != nil {
			namespace = *grant.Namespace
		}
		expires := "standing"
		if grant.ExpiresAt != nil {
			expires = *grant.ExpiresAt
		}
		reason := "-"
		if grant.Reason != nil {
			reason = *grant.Reason
		}
		roleSourced = roleSourced || isRoleSourcedGrant(grant)
		t.AppendRow(table.Row{
			grant.ID,
			user,
			grant.Scope,
			namespace,
			grant.Role,
			formatGrantSource(grant.Source),
			expires,
			reason,
			formatOverPolicy(grant.OverPolicy),
			formatReconcileStatus(grant),
			formatTimeAgo(grant.CreatedAt),
		})
	}
	t.Render()
	if roleSourced {
		fmt.Println(`Grants with source "role" come from the member's organisation role and cannot be revoked here: change the member's role, or the role's Kubernetes access, to remove that access.`)
	}
}

// formatGrantSource names where a grant came from. The platform sends null
// when its stored facts cannot tell, which is "unknown", never "manual".
func formatGrantSource(source *string) string {
	if source == nil || *source == "" {
		return "unknown"
	}
	return *source
}

// formatOverPolicy says whether the current policy would refuse the grant
// today. null means the platform did not evaluate it (or could not read the
// policy), which is "unknown", never "no".
func formatOverPolicy(overPolicy *bool) string {
	switch {
	case overPolicy == nil:
		return "unknown"
	case *overPolicy:
		return text.FgRed.Sprint("yes")
	default:
		return "no"
	}
}

func formatReconcileStatus(grant client.ClusterAccessGrant) string {
	switch grant.ReconcileStatus {
	case "applied":
		return text.FgGreen.Sprint("Applied")
	case "failed":
		status := text.FgRed.Sprint("Failed")
		if grant.ReconcileError != nil && *grant.ReconcileError != "" {
			status += " (" + *grant.ReconcileError + ")"
		}
		return status
	case "cluster_offline":
		return text.FgYellow.Sprint("Cluster offline")
	default:
		return text.FgYellow.Sprint("Pending")
	}
}

func init() {
	clusterAccessListCmd.Flags().StringVar(&accessClusterFlag, "cluster", "", "Cluster name or ID (defaults to the selected cluster)")

	clusterAccessGrantCmd.Flags().StringVar(&accessClusterFlag, "cluster", "", "Cluster name or ID (defaults to the selected cluster)")
	clusterAccessGrantCmd.Flags().StringVar(&accessRoleFlag, "role", "view", "Kubernetes role for the grant: view, edit, admin, or cluster-admin")
	clusterAccessGrantCmd.Flags().StringVar(&accessNamespaceFlag, "namespace", "", "Limit the grant to one namespace (default: cluster-wide)")
	clusterAccessGrantCmd.Flags().StringVar(&accessExpiresFlag, "expires", "", "End the grant after a duration (30m, 4h, 7d) or at an RFC 3339 time (default: standing)")
	clusterAccessGrantCmd.Flags().StringVar(&accessReasonFlag, "reason", "", "Why the access is granted; recorded with the grant and in the audit log")

	clusterAccessElevateCmd.Flags().StringVar(&elevateClusterFlag, "cluster", "", "Cluster name or ID (defaults to the selected cluster)")
	clusterAccessElevateCmd.Flags().StringVar(&elevateRoleFlag, "role", "edit", "Kubernetes role to elevate to: view, edit, admin, or cluster-admin")
	clusterAccessElevateCmd.Flags().StringVar(&elevateNamespaceFlag, "namespace", "", "Limit the elevation to one namespace (default: cluster-wide)")
	clusterAccessElevateCmd.Flags().StringVar(&elevateExpiresFlag, "expires", "", "When the access ends: a duration (30m, 4h) or an RFC 3339 time (required)")
	clusterAccessElevateCmd.Flags().StringVar(&elevateReasonFlag, "reason", "", "Why the access is needed (required)")

	clusterAccessRevokeCmd.Flags().StringVar(&accessClusterFlag, "cluster", "", "Cluster name or ID (defaults to the selected cluster)")

	registerStructuredOutputFlags(clusterAccessListCmd, clusterAccessGrantCmd, clusterAccessElevateCmd)

	clusterAccessCmd.AddCommand(clusterAccessListCmd)
	clusterAccessCmd.AddCommand(clusterAccessGrantCmd)
	clusterAccessCmd.AddCommand(clusterAccessElevateCmd)
	clusterAccessCmd.AddCommand(clusterAccessRevokeCmd)
	clusterCmd.AddCommand(clusterAccessCmd)
}

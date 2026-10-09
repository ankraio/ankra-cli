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

Break-glass: 'elevate' gives the caller itself time-boxed access, within the
policy. It needs only the kube_access.elevate permission, so automation
running as a service account can use it without being able to grant anyone
else.

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
		renderGrantsTable(grants.Result)
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
admin, cluster-admin.`,
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
		fmt.Println("The grant is applied to the cluster by the RBAC reconciler; check status with: ankra cluster access list")
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

Every elevation is recorded in the audit log and notifies the organisation's
notification routes.`,
	Example: `  ankra cluster access elevate --cluster prod --role edit --expires 4h --reason "incident 4711: restart the stuck rollout"
  ankra cluster access elevate --cluster prod --role edit --namespace payments --expires 30m --reason "hotfix"`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateAccessRole(accessRoleFlag); err != nil {
			return err
		}
		if strings.TrimSpace(accessExpiresFlag) == "" {
			return errors.New("--expires is required: break-glass access must end (for example --expires 4h)")
		}
		if strings.TrimSpace(accessReasonFlag) == "" {
			return errors.New("--reason is required: say why the access is needed")
		}
		expiresIn, expiresAt, err := parseAccessExpiry(accessExpiresFlag)
		if err != nil {
			return err
		}
		clusterID, _, err := resolveGatewayClusterID(accessClusterFlag, os.Stderr)
		if err != nil {
			return err
		}
		request := client.ElevateClusterAccessRequest{
			Scope:     "cluster",
			Role:      accessRoleFlag,
			ExpiresIn: expiresIn,
			ExpiresAt: expiresAt,
			Reason:    accessReasonFlag,
		}
		if accessNamespaceFlag != "" {
			namespace := accessNamespaceFlag
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
		fmt.Printf("End it early with: ankra cluster access revoke %s --cluster %s\n", grant.ID, displayClusterReference())
		return nil
	},
}

var clusterAccessRevokeCmd = &cobra.Command{
	Use:   "revoke <grant-id|email>",
	Short: "Revoke access grants from a cluster",
	Long: `Revoke gateway access from a cluster.

Pass a grant ID (from 'ankra cluster access list') to revoke a single grant,
or an email address to revoke every grant that member has on the cluster.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]
		clusterID, _, err := resolveGatewayClusterID(accessClusterFlag, os.Stderr)
		if err != nil {
			return err
		}

		grantIDs, err := resolveGrantIDs(clusterID, target)
		if err != nil {
			return err
		}

		for _, grantID := range grantIDs {
			if _, err := apiClient.DeleteClusterAccessGrant(context.Background(), clusterID, grantID); err != nil {
				return err
			}
			fmt.Printf("Revoked grant %s\n", grantID)
		}
		return nil
	},
}

func resolveGrantIDs(clusterID string, target string) ([]string, error) {
	if isLikelyClusterID(target) {
		return []string{target}, nil
	}
	grants, err := apiClient.ListClusterAccessGrants(context.Background(), clusterID)
	if err != nil {
		return nil, err
	}
	var grantIDs []string
	for _, grant := range grants.Result {
		if grant.UserEmail != nil && strings.EqualFold(*grant.UserEmail, target) {
			grantIDs = append(grantIDs, grant.ID)
		}
	}
	if len(grantIDs) == 0 {
		return nil, withExitCode(exitNotFound, fmt.Errorf("no access grants found for %q on this cluster", target))
	}
	return grantIDs, nil
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
	return nil, nil, fmt.Errorf("invalid --expires %q: use a duration such as 30m, 4h or 7d, or an RFC 3339 time", value)
}

func validateAccessRole(role string) error {
	for _, allowed := range accessRoles {
		if role == allowed {
			return nil
		}
	}
	return fmt.Errorf("invalid role %q; valid roles: %s", role, strings.Join(accessRoles, ", "))
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

func renderGrantsTable(grants []client.ClusterAccessGrant) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"ID", "User", "Scope", "Namespace", "Role", "Expires", "Reason", "Status", "Created"})
	for _, grant := range grants {
		user := grant.AnkraUserID
		if grant.UserEmail != nil {
			user = *grant.UserEmail
		}
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
		t.AppendRow(table.Row{
			grant.ID,
			user,
			grant.Scope,
			namespace,
			grant.Role,
			expires,
			reason,
			formatReconcileStatus(grant),
			formatTimeAgo(grant.CreatedAt),
		})
	}
	t.Render()
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

	clusterAccessElevateCmd.Flags().StringVar(&accessClusterFlag, "cluster", "", "Cluster name or ID (defaults to the selected cluster)")
	clusterAccessElevateCmd.Flags().StringVar(&accessRoleFlag, "role", "edit", "Kubernetes role to elevate to: view, edit, admin, or cluster-admin")
	clusterAccessElevateCmd.Flags().StringVar(&accessNamespaceFlag, "namespace", "", "Limit the elevation to one namespace (default: cluster-wide)")
	clusterAccessElevateCmd.Flags().StringVar(&accessExpiresFlag, "expires", "", "When the access ends: a duration (30m, 4h) or an RFC 3339 time (required)")
	clusterAccessElevateCmd.Flags().StringVar(&accessReasonFlag, "reason", "", "Why the access is needed (required)")

	clusterAccessRevokeCmd.Flags().StringVar(&accessClusterFlag, "cluster", "", "Cluster name or ID (defaults to the selected cluster)")

	registerStructuredOutputFlags(clusterAccessListCmd, clusterAccessGrantCmd, clusterAccessElevateCmd)

	clusterAccessCmd.AddCommand(clusterAccessListCmd)
	clusterAccessCmd.AddCommand(clusterAccessGrantCmd)
	clusterAccessCmd.AddCommand(clusterAccessElevateCmd)
	clusterAccessCmd.AddCommand(clusterAccessRevokeCmd)
	clusterCmd.AddCommand(clusterAccessCmd)
}

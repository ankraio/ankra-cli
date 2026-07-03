package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var (
	accessClusterFlag   string
	accessRoleFlag      string
	accessNamespaceFlag string
)

var accessRoles = []string{"view", "edit", "admin", "cluster-admin"}

var clusterAccessCmd = &cobra.Command{
	Use:   "access",
	Short: "Manage who can reach a cluster through the Ankra kube gateway",
	Long: `List, grant, and revoke per-user access to a cluster's Kubernetes API
through the Ankra gateway (the access used by 'ankra cluster kubeconfig' and
'ankra cluster kube-token').

Managing access requires organisation admin rights. Grants apply to one
cluster and one organisation member, identified by email.

Examples:
  ankra cluster access list --cluster my-cluster
  ankra cluster access grant user@example.com --cluster my-cluster --role view
  ankra cluster access grant user@example.com --cluster my-cluster --role edit --namespace staging
  ankra cluster access revoke user@example.com --cluster my-cluster
  ankra cluster access revoke 6f1f9aca-2c3d-4e5f-8a9b-0c1d2e3f4a5b --cluster my-cluster`,
	Annotations: map[string]string{"group": "kubernetes"},
}

var clusterAccessListCmd = &cobra.Command{
	Use:   "list",
	Short: "List access grants for a cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, err := resolveKubeTokenClusterID(accessClusterFlag)
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
		clusterID, err := resolveKubeTokenClusterID(accessClusterFlag)
		if err != nil {
			return err
		}

		request := client.CreateClusterAccessGrantRequest{
			UserEmail: email,
			Scope:     "cluster",
			Role:      accessRoleFlag,
		}
		if accessNamespaceFlag != "" {
			namespace := accessNamespaceFlag
			request.Scope = "namespace"
			request.Namespace = &namespace
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
		fmt.Println("The grant is applied to the cluster by the RBAC reconciler; check status with: ankra cluster access list")
		fmt.Printf("The member can now run: ankra cluster kubeconfig add --cluster %s --use\n", displayClusterReference())
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
		clusterID, err := resolveKubeTokenClusterID(accessClusterFlag)
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
	t.AppendHeader(table.Row{"ID", "User", "Scope", "Namespace", "Role", "Status", "Created"})
	for _, grant := range grants {
		user := grant.AnkraUserID
		if grant.UserEmail != nil {
			user = *grant.UserEmail
		}
		namespace := "-"
		if grant.Namespace != nil {
			namespace = *grant.Namespace
		}
		t.AppendRow(table.Row{
			grant.ID,
			user,
			grant.Scope,
			namespace,
			grant.Role,
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

	clusterAccessRevokeCmd.Flags().StringVar(&accessClusterFlag, "cluster", "", "Cluster name or ID (defaults to the selected cluster)")

	registerStructuredOutputFlags(clusterAccessListCmd, clusterAccessGrantCmd)

	clusterAccessCmd.AddCommand(clusterAccessListCmd)
	clusterAccessCmd.AddCommand(clusterAccessGrantCmd)
	clusterAccessCmd.AddCommand(clusterAccessRevokeCmd)
	clusterCmd.AddCommand(clusterAccessCmd)
}

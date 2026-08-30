package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var clusterMoveCmd = &cobra.Command{
	Use:   "move [cluster_name]",
	Short: "Move a cluster to another organisation you administer",
	Long: `Move a cluster into another existing organisation.

Only an administrator of BOTH the current organisation and the destination can
move a cluster. The cluster keeps its id, agent, executions, resources, security
findings, insights, DNS records and variables. Bindings that belong to the
current organisation are detached and reported: access grants, kube tokens,
notification routes and mutes, security report schedules, trusted AI actions
and cluster-group memberships. Billing, audit and chat history stay with the
current organisation.

The platform refuses the move while operations are running on the cluster,
while the cluster is a member of a cluster mesh, for playground clusters, and
when the destination already has a cluster with the same name.

The destination is resolved among your organisations by id, slug or name.
If no cluster name is provided, the currently selected cluster is moved.`,
	Example: `  ankra cluster move edge-01 --organisation acme-prod
  ankra cluster move --organisation 6f1c... --yes
  ankra cluster move edge-01 --organisation "Acme Prod" -o json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		destinationReference, _ := cmd.Flags().GetString("organisation")
		yes, _ := cmd.Flags().GetBool("yes")
		if destinationReference == "" {
			return withExitCode(exitUsage, errors.New("--organisation is required: the id, slug or name of the destination organisation"))
		}
		format, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}

		clusterID, clusterName, _, resolveError := resolveClusterFromArgsWithKind(cmd, args)
		if resolveError != nil {
			return resolveError
		}
		organisations, listError := apiClient.ListOrganisations()
		if listError != nil {
			return fmt.Errorf("listing organisations: %w", listError)
		}
		destination, destinationError := resolveOrganisationReference(organisations, destinationReference)
		if destinationError != nil {
			if errors.Is(destinationError, errOrganisationNotFound) {
				return withExitCode(exitNotFound, destinationError)
			}
			return withExitCode(exitUsage, destinationError)
		}
		destinationLabel := organisationDisplayName(*destination)

		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Move cluster %q to organisation %q? Access grants, kube tokens, notification routes "+
				"and group memberships from the current organisation are detached. [y/N]: ", clusterName, destinationLabel),
			yes); confirmError != nil {
			return confirmError
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		result, moveError := apiClient.MoveCluster(ctx, clusterID, destination.OrganisationID)
		if moveError != nil {
			var refused *client.MoveClusterRefusedError
			if errors.As(moveError, &refused) {
				return fmt.Errorf("move refused (%s): %s", refused.Code, refused.Detail)
			}
			return fmt.Errorf("moving cluster: %w", moveError)
		}
		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, result)
		}
		printClusterMoveResult(cmd, result)
		return nil
	},
}

func organisationDisplayName(organisation client.OrganisationSummary) string {
	if organisation.Name != nil && *organisation.Name != "" {
		return *organisation.Name
	}
	if organisation.Slug != nil && *organisation.Slug != "" {
		return *organisation.Slug
	}
	return organisation.OrganisationID
}

func printClusterMoveResult(cmd *cobra.Command, result *client.MoveClusterResult) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Cluster %q moved to organisation %q.\n", result.ClusterName, result.DestinationOrganisationName)
	_, _ = fmt.Fprintf(out, "  Cluster ID: %s\n", result.ClusterID)
	_, _ = fmt.Fprintf(out, "  Destination organisation ID: %s\n", result.DestinationOrganisationID)
	detached := result.Detached
	_, _ = fmt.Fprintf(out, "  Detached from the previous organisation: %d access grant(s), %d kube token(s), %d notification route(s), "+
		"%d mute(s), %d subscription(s), %d report schedule(s), %d trusted action(s), %d group membership(s)\n",
		detached.AccessGrants, detached.KubeTokens, detached.NotificationRoutes, detached.NotificationMutes,
		detached.Subscriptions, detached.ReportSchedules, detached.TrustedActions, detached.GroupMemberships)
	if detached.GitopsRepository != nil && *detached.GitopsRepository != "" {
		_, _ = fmt.Fprintf(out, "  GitOps repository %q was detached; reconnect it in the destination organisation.\n", *detached.GitopsRepository)
	}
	if result.SecretsRelocated > 0 {
		_, _ = fmt.Fprintf(out, "  Secrets relocated: %d\n", result.SecretsRelocated)
	}
	for _, warning := range result.Warnings {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning)
	}
	_, _ = fmt.Fprintf(out, "Switch to the destination with: ankra org switch %s\n", result.DestinationOrganisationID)
}

func init() {
	clusterMoveCmd.Flags().String("organisation", "", "Destination organisation (id, slug or name); you must be an admin there (required)")
	clusterMoveCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(clusterMoveCmd)
	clusterCmd.AddCommand(clusterMoveCmd)
}

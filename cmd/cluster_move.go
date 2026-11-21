package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var clusterMoveCmd = withClusterArgument(clusterMustBeNamed, &cobra.Command{
	Use:   "move <cluster_name>",
	Short: "Move a cluster to another organisation, or to another Ankra Cloud zone",
	Long: `Move a cluster to another organisation (--organisation) or to another Ankra
Cloud zone (--zone). The two destinations cannot be combined.

Name the cluster as the argument or with --cluster. A move never falls back
to the cluster selected with 'ankra cluster select': that selection is shared
by every shell and agent session on the machine.

Organisation move (--organisation)

Only an administrator of BOTH the current organisation and the destination can
move a cluster. The cluster keeps its id, agent, executions, resources, security
findings, insights, DNS records and variables. Bindings that belong to the
current organisation are detached and reported: access grants, kube tokens,
notification routes and mutes, security report schedules, trusted AI actions
and cluster-group memberships. Billing, audit and chat history stay with the
current organisation.

Only imported clusters can be moved for now: Ankra-provisioned and managed
Kubernetes clusters use a provider credential that belongs to the current
organisation, so the platform refuses to move them. The platform also refuses
the move while operations are running on the cluster, while the cluster is a
member of a cluster mesh, while it has DNS zones bound to the current
organisation (a published cluster domain or custom DNS zones), for playground
and sandbox clusters, and when the destination already has a cluster with the
same name. The destination is resolved among your organisations by id, slug
or name.

Zone move (--zone)

A zone move carries an Ankra Cloud cluster to another zone in steps, and the
step that moves traffic always waits for you:

  1. --zone <zone> --plan shows what the move would build and carry, and
     writes nothing: the target cluster's shape, the stacks carried, the data
     volumes copied with their sizes, what is not carried, and every public
     hostname with how it is cut over.
  2. --zone <zone> starts the move. The platform builds a twin of the cluster
     in the zone (or uses --target-cluster, an empty cluster that already
     exists), copies the stacks and their data into it and leaves it in
     standby: its writers are scaled to zero and its CronJobs suspended, so
     nothing runs twice while the source still serves. The move then rests
     at ready_for_cutover.
  3. --cutover turns the maintenance page on, freezes the source, takes the
     final copy and switches traffic to the target. The move then soaks.
  4. While it soaks, --rollback sends traffic back to the source (writes made
     on the target since the cutover are not copied back), and --retire
     deprovisions the source, which cannot be undone.

--status shows the latest move and its steps. --wait follows the move until it
rests (ready_for_cutover, soaking, rolled_back, retired or failed), up to
--timeout. Starting a move, --cutover, --rollback and --retire ask for
confirmation unless --yes is given.`,
	Example: `  # Organisation move
  ankra cluster move edge-01 --organisation acme-prod
  ankra cluster move --cluster edge-01 --organisation 6f1c... --yes

  # Zone move
  ankra cluster move avura-france --zone se-sto1 --plan
  ankra cluster move avura-france --zone se-sto1 --control-plane-count 3 --storage-tier standard --wait
  ankra cluster move avura-france --zone se-sto1 --target-cluster avura-stockholm
  ankra cluster move avura-france --status --wait
  ankra cluster move avura-france --cutover --wait
  ankra cluster move avura-france --rollback
  ankra cluster move avura-france --retire -o json`,
	// The cluster argument may be left out for --cluster, which the run
	// reads itself, by name or id, as it always has; named nowhere, the
	// resolver refuses before the run (clusterMustBeNamed).
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		destinationReference, _ := cmd.Flags().GetString("organisation")
		yes, _ := cmd.Flags().GetBool("yes")
		if destinationReference != "" {
			for _, flagName := range zoneMoveFlags {
				if cmd.Flags().Changed(flagName) {
					return withExitCode(exitUsage, fmt.Errorf(
						"--organisation and --%s cannot be combined: --organisation moves the cluster to another "+
							"organisation, the zone move flags move it to another Ankra Cloud zone", flagName))
				}
			}
		} else {
			action, actionError := zoneMoveActionFromFlags(cmd)
			if actionError != nil {
				return actionError
			}
			if action == zoneMoveNoAction {
				return withExitCode(exitUsage, errors.New(
					"a destination is required: --organisation <org> moves the cluster to another organisation, "+
						"--zone <zone> to another Ankra Cloud zone (--status, --cutover, --rollback and --retire "+
						"act on a zone move already started)"))
			}
			return runZoneMove(cmd, args, action)
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

		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
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
				return fmt.Errorf("move refused (%s): %w", refused.Code, refused)
			}
			return fmt.Errorf("moving cluster: %w", moveError)
		}
		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, result)
		}
		printClusterMoveResult(cmd, result)
		return nil
	},
})

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
	_, _ = fmt.Fprintf(out, "  Detached from the previous organisation: %d access grant(s), %d role assignment(s), %d kube token(s), "+
		"%d notification route(s), %d mute(s), %d subscription(s), %d report schedule(s), %d trusted action(s), "+
		"%d group membership(s), %d platform resource link(s), %d context repositor(y/ies)\n",
		detached.AccessGrants, detached.RoleAssignments, detached.KubeTokens, detached.NotificationRoutes, detached.NotificationMutes,
		detached.Subscriptions, detached.ReportSchedules, detached.TrustedActions, detached.GroupMemberships, detached.PlatformResourceLinks,
		detached.ContextRepositories)
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
	clusterMoveCmd.Flags().String("organisation", "", "Destination organisation (id, slug or name); you must be an admin there")
	clusterMoveCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerZoneMoveFlags(clusterMoveCmd)
	registerStructuredOutputFlags(clusterMoveCmd)
	clusterCmd.AddCommand(clusterMoveCmd)
}

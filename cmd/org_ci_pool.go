package cmd

// `ankra org ci-settings pool list|add|remove` (ankra-q573dh.4): the
// organisation's CI cluster pool, over GET /api/v1/org/ci-settings/pool and
// PUT/DELETE /api/v1/org/ci-settings/pool/{cluster_id}. The pool spreads the
// organisation's pipeline runs over more than one cluster, so one cloud
// provider's quota on one cluster stops being the ceiling on every pipeline.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var orgCIPoolCmd = &cobra.Command{
	Use:   "pool",
	Short: "Show or change the clusters the organisation's pipeline runs are spread across",
	Long: `Show or change the organisation's CI cluster pool: the clusters its pipeline
runs are spread across.

  ankra org ci-settings pool list
  ankra org ci-settings pool add ci-hel1-b
  ankra org ci-settings pool add ci-upcloud --weight 50
  ankra org ci-settings pool remove ci-hel1-b

The organisation's pipeline cluster ('ankra org ci-settings set --cluster') is
always a member - the primary. Each run is pinned, when its first step is
dispatched, to the least-loaded member whose agent can take a step: steps in
flight over the member's CI workers, scaled by its weight, so a member weighted
200 takes runs until it carries twice the load of a member weighted 100. When
two members are close, the one where the repository ran last wins, because it
holds the repository's warm caches. A run never moves between clusters.

An organisation that lists no members runs every pipeline on its pipeline
cluster exactly as before.

A member must be a cluster of this organisation whose agent runs pipeline
steps (give it workers first with 'ankra cluster agent ci set --workers N'),
and a cluster may be in one organisation's pool only.

Reading requires organisation membership; changing requires organisation admin.`,
}

var orgCIPoolListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "get"},
	Short:   "List the organisation's CI pool members",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		pool, poolError := apiClient.GetOrganisationCIPool(ctx)
		if poolError != nil {
			return ciPoolCommandError("list the organisation's CI pool", poolError)
		}
		return renderOrganisationCIPool(cmd, pool)
	},
}

var orgCIPoolAddCmd = &cobra.Command{
	Use:   "add <cluster>",
	Short: "Add a cluster to the organisation's CI pool, or change its weight",
	Long: `Add a cluster (name or id) to the organisation's CI pool, or change the
weight of one already in it.

--weight is the member's relative share of runs, 1 to 1000. Without it a new
member gets 100 and a member already listed keeps its weight.

Runs queued after this land on the member as soon as it is the least loaded.
Requires organisation admin.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var weight *int
		if cmd.Flags().Changed("weight") {
			value, _ := cmd.Flags().GetInt("weight")
			weight = &value
		}
		clusterID, resolveError := resolveClusterID(strings.TrimSpace(args[0]))
		if resolveError != nil {
			return resolveError
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		pool, setError := apiClient.SetOrganisationCIPoolMember(ctx, clusterID, weight)
		if setError != nil {
			return ciPoolCommandError("add the cluster to the organisation's CI pool", setError)
		}
		return renderOrganisationCIPool(cmd, pool)
	},
}

var orgCIPoolRemoveCmd = &cobra.Command{
	Use:     "remove <cluster>",
	Aliases: []string{"rm"},
	Short:   "Remove a cluster from the organisation's CI pool",
	Long: `Remove a cluster (name or id) from the organisation's CI pool.

New runs stop going to it; runs already pinned to it finish there. Removing the
organisation's pipeline cluster only drops its weight - it stays the primary
member until the pipeline cluster itself is changed. Requires organisation
admin.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterID(strings.TrimSpace(args[0]))
		if resolveError != nil {
			return resolveError
		}
		skipConfirmation, _ := cmd.Flags().GetBool("yes")
		confirmMessage := fmt.Sprintf("Remove cluster %s from the organisation's CI pool? [y/N] ", args[0])
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), confirmMessage,
			skipConfirmation); confirmError != nil {
			return confirmError
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		pool, removeError := apiClient.RemoveOrganisationCIPoolMember(ctx, clusterID)
		if removeError != nil {
			return ciPoolCommandError("remove the cluster from the organisation's CI pool", removeError)
		}
		return renderOrganisationCIPool(cmd, pool)
	},
}

// ciPoolCommandError wraps a pool request's error. A platform that predates
// the pool routes answers client.ErrCIPoolUnavailable, which says so in its
// own sentence, and is a not-found for the exit code rather than a runtime
// failure: there is no pool there to act on.
func ciPoolCommandError(action string, requestError error) error {
	wrapped := fmt.Errorf("%s: %w", action, requestError)
	if errors.Is(requestError, client.ErrCIPoolUnavailable) {
		return withExitCode(exitNotFound, wrapped)
	}
	return wrapped
}

// renderOrganisationCIPool prints the pool, or encodes it under -o json|yaml.
func renderOrganisationCIPool(cmd *cobra.Command, pool *client.OrganisationCIPool) error {
	if rendered, renderError := renderStructured(cmd, pool); rendered || renderError != nil {
		return renderError
	}
	out := cmd.OutOrStdout()
	if len(pool.Members) == 0 {
		_, _ = fmt.Fprintln(out, "No CI pool: the organisation has no pipeline cluster and lists no members.")
		return nil
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleLight)
	writer.AppendHeader(table.Row{"Cluster", "ID", "Weight", "Role"})
	for _, member := range pool.Members {
		role := "member"
		switch {
		case member.IsPrimary && member.IsListed:
			role = "primary (listed)"
		case member.IsPrimary:
			role = "primary"
		}
		writer.AppendRow(table.Row{member.ClusterName, member.ClusterID, member.Weight, role})
	}
	writer.Render()
	if !pool.IsPooled {
		_, _ = fmt.Fprintln(out, "\nNo members are listed, so every run goes to the pipeline cluster. "+
			"Add one with: ankra org ci-settings pool add <cluster>")
	}
	return nil
}

func init() {
	registerStructuredOutputFlags(orgCIPoolListCmd, orgCIPoolAddCmd, orgCIPoolRemoveCmd)
	orgCIPoolAddCmd.Flags().Int("weight", 0, "The member's relative share of runs, 1-1000 (100 when new)")
	orgCIPoolRemoveCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	orgCIPoolCmd.AddCommand(orgCIPoolListCmd, orgCIPoolAddCmd, orgCIPoolRemoveCmd)
	orgCISettingsCmd.AddCommand(orgCIPoolCmd)
}

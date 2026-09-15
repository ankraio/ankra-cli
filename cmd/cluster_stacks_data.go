package cmd

import (
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

var clusterStacksDataCmd = &cobra.Command{
	Use:   "data",
	Short: "Inspect what a stack holds that a backup would carry",
	Long: "The stack's data inventory: standalone persistent volume claims and " +
		"database custom resources with their claims folded in, each with the engine " +
		"that would capture it and the consistency that engine can promise.",
}

// dataAssetCarrier names the engine that would capture an asset, in the words
// a person choosing a backup would use rather than the engine's own id.
func dataAssetCarrier(engine string) string {
	switch engine {
	case "velero":
		return "velero (generic volume mover)"
	case "cnpg":
		return "cnpg (CloudNativePG barman)"
	case "percona":
		return "percona (operator backup)"
	case "logical":
		return "logical (database dump)"
	case "":
		return "-"
	default:
		return engine
	}
}

var clusterStacksDataListCmd = &cobra.Command{
	Use:   "list <stack>",
	Short: "List a stack's data assets",
	Long: `List what a stack holds that a backup would have to carry.

Each asset says which engine would capture it and what consistency that engine
can promise: an engine either produces a consistent artifact or it declares
itself crash-consistent, and there is no third answer.

The live read of the database operators can come back unavailable - no relay,
or an offline agent - in which case the command says so rather than presenting
a partial inventory as the whole truth.

Example:
  ankra cluster stacks data list shop`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		inventory, inventoryError := apiClient.GetStackDataAssets(cluster.ID, stackName)
		if inventoryError != nil {
			return backupLaneError("listing the stack's data assets", inventoryError)
		}
		if rendered, renderError := renderStructured(cmd, inventory); rendered || renderError != nil {
			return renderError
		}
		if inventory.CustomResourceScan == client.CustomResourceScanUnavailable {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"The live database-operator read did not run, so any database this stack runs may be missing "+
					"from this list. Check the cluster's agent with 'ankra cluster info'.")
		}
		out := cmd.OutOrStdout()
		if len(inventory.Assets) == 0 {
			_, _ = fmt.Fprintf(out, "Stack '%s' holds no data a backup would carry.\n", stackName)
			return nil
		}
		_, _ = fmt.Fprintf(out, "Stack '%s' (%s), %s requested in total:\n",
			inventory.StackName, strings.Join(inventory.Namespaces, ", "),
			formatByteSize(inventory.TotalRequestedBytes))

		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Kind", "Namespace", "Name", "Engine", "Consistency", "Requested", "Carried by"})
		for _, asset := range inventory.Assets {
			writer.AppendRow(table.Row{
				asset.Kind, asset.Namespace, asset.Name, asset.Engine, asset.Consistency,
				formatByteSize(asset.RequestedBytes), dataAssetCarrier(asset.Engine),
			})
		}
		writer.Render()
		return nil
	},
}

func init() {
	registerStructuredOutputFlags(clusterStacksDataListCmd)
	clusterStacksDataCmd.AddCommand(clusterStacksDataListCmd)
	clusterStacksCmd.AddCommand(clusterStacksDataCmd)
}

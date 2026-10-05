package cmd

import (
	"fmt"
	"io"
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

// dataAssetAttribution says why an asset is the stack's: the member that owns
// it, or the namespace only this stack uses. A platform that predates
// attribution sends none, and that is shown as unknown rather than guessed.
func dataAssetAttribution(asset client.StackDataAsset) string {
	switch asset.Attribution {
	case client.StackDataAttributionMember:
		if asset.Member != nil && asset.Member.Name != "" {
			return fmt.Sprintf("member %s/%s", asset.Member.Kind, asset.Member.Name)
		}
		return "member"
	case client.StackDataAttributionExclusiveNamespace:
		return "namespace only this stack uses"
	case "":
		return "-"
	default:
		return asset.Attribution
	}
}

// renderUnattributedDataAssets prints what the namespace sweep found that no
// member of the stack owns. It is listed so a shared namespace's other data is
// visible, and said plainly to be outside every capture and restore.
func renderUnattributedDataAssets(out io.Writer, assets []client.StackDataAsset) {
	if len(assets) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out,
		"\nNot owned by this stack (unattributed): %d found in its namespaces that no member owns. "+
			"They are never captured or restored as this stack's.\n", len(assets))
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Kind", "Namespace", "Name", "Requested", "Helm release"})
	for _, asset := range assets {
		helmRelease := asset.HelmRelease
		if helmRelease == "" {
			helmRelease = "-"
		}
		writer.AppendRow(table.Row{
			asset.Kind, asset.Namespace, asset.Name, formatByteSize(asset.RequestedBytes), helmRelease,
		})
	}
	writer.Render()
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

Only data a member of the stack owns is listed as the stack's, and each asset
says why it is: the member that owns it, or a namespace only this stack uses.
Data the sweep of the stack's namespaces finds that no member owns - another
stack's database in a shared namespace, an orphan claim - is listed separately
as not owned by this stack (unattributed). Unattributed data is never captured
or restored as this stack's, and the requested total does not count it.

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
					"from this list, or listed as not owned by this stack because its ownership could not be read. "+
					"Check the cluster's agent with 'ankra cluster info'.")
		}
		out := cmd.OutOrStdout()
		if len(inventory.Assets) == 0 {
			_, _ = fmt.Fprintf(out, "Stack '%s' holds no data a backup would carry.\n", stackName)
			renderUnattributedDataAssets(out, inventory.UnattributedAssets)
			return nil
		}
		_, _ = fmt.Fprintf(out, "Stack '%s' (%s), %s requested in total:\n",
			inventory.StackName, strings.Join(inventory.Namespaces, ", "),
			formatByteSize(inventory.TotalRequestedBytes))

		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{
			"Kind", "Namespace", "Name", "Engine", "Consistency", "Requested", "Carried by", "Owned via",
		})
		for _, asset := range inventory.Assets {
			writer.AppendRow(table.Row{
				asset.Kind, asset.Namespace, asset.Name, asset.Engine, asset.Consistency,
				formatByteSize(asset.RequestedBytes), dataAssetCarrier(asset.Engine),
				dataAssetAttribution(asset),
			})
		}
		writer.Render()
		renderUnattributedDataAssets(out, inventory.UnattributedAssets)
		return nil
	},
}

func init() {
	registerStructuredOutputFlags(clusterStacksDataListCmd)
	clusterStacksDataCmd.AddCommand(clusterStacksDataListCmd)
	clusterStacksCmd.AddCommand(clusterStacksDataCmd)
}

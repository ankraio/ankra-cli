package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// errNoClusterSelected is returned when a command needs a selected
// cluster but none has been chosen yet.
type errNoClusterSelected struct{}

func (errNoClusterSelected) Error() string {
	return "no cluster specified and none selected; pass --cluster <name|id>, or run `ankra cluster select <name>` first"
}

// clusterCmd is the parent command for cluster operations
var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Cluster operations",
	Long:  `Commands for managing and operating on clusters.`,
}

var clusterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all clusters",
	RunE: func(cmd *cobra.Command, args []string) error {
		clusters, err := listAllClusters()
		if err != nil {
			return fmt.Errorf("listing clusters: %w", err)
		}
		if clusters == nil {
			clusters = []client.ClusterListItem{}
		}
		if rendered, err := renderStructured(cmd, clusters); rendered || err != nil {
			return err
		}
		if len(clusters) == 0 {
			fmt.Println("No clusters found.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Environment", "Kube Version", "Nodes", "Control Planes", "State", "Kind", "Created"})
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, WidthMin: 20},
			{Number: 2, WidthMin: 10},
			{Number: 3, WidthMin: 10},
			{Number: 4, WidthMin: 5},
			{Number: 5, WidthMin: 10},
			{Number: 6, WidthMin: 10},
			{Number: 7, WidthMin: 10},
			{Number: 8, WidthMin: 15},
		})
		for _, cluster := range clusters {
			state := cluster.State
			if strings.ToLower(state) == "online" {
				state = text.FgGreen.Sprint(state)
			}
			t.AppendRow(table.Row{
				cluster.Name,
				cluster.Environment,
				cluster.KubeVersion,
				cluster.Nodes,
				cluster.ControlPlanes,
				state,
				cluster.Kind,
				formatTimeAgo(cluster.CreatedAt),
			})
		}
		t.Render()
		return nil
	},
}

// listAllClusters paginates through the cluster list until the backend
// reports no more pages. The previous implementation called
// ListClusters(0, 0) which defaults server-side to page=1, page_size=25
// and silently truncates organisations that own more than 25 clusters.
func listAllClusters() ([]client.ClusterListItem, error) {
	const pageSize = 100
	const maxPages = 100
	var clusters []client.ClusterListItem
	for page := 1; page <= maxPages; page++ {
		response, err := apiClient.ListClusters(page, pageSize)
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, response.Result...)
		if response.Pagination.TotalPages <= page || len(response.Result) == 0 {
			break
		}
	}
	return clusters, nil
}

var clusterInfoCmd = &cobra.Command{
	Use:     "info [name]",
	Aliases: []string{"get-cluster"},
	Short:   "Show details of a specific cluster",
	Long: `Show details of a specific cluster.

If no name is provided, shows details for the currently selected cluster.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		} else {
			selected, err := resolveActiveCluster(cmd)
			if err != nil {
				return err
			}
			name = selected.Name
		}
		cluster, err := apiClient.GetCluster(name)
		if err != nil {
			return fmt.Errorf("fetching cluster details for %s: %w", name, err)
		}
		if rendered, err := renderStructured(cmd, cluster); rendered || err != nil {
			return err
		}
		fmt.Printf("Cluster Details:\n")
		fmt.Printf("  ID: %s\n", cluster.ID)
		fmt.Printf("  Name: %s\n", cluster.Name)
		fmt.Printf("  Environment: %s\n", cluster.Environment)
		fmt.Printf("  Kube Version: %s\n", cluster.KubeVersion)
		fmt.Printf("  State: %s\n", cluster.State)
		fmt.Printf("  Control Planes: %d\n", cluster.ControlPlanes)
		fmt.Printf("  Nodes: %d\n", cluster.Nodes)
		fmt.Printf("  Kind: %s\n", cluster.Kind)
		return nil
	},
}

var clusterReconcileCmd = &cobra.Command{
	Use:   "reconcile [cluster_name]",
	Short: "Trigger cluster reconciliation",
	Long: `Trigger a reconciliation for a cluster to sync desired state with actual state.

If no cluster name is provided, uses the currently selected cluster.
If a cluster name is provided, reconciles that specific cluster.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		clusterID, clusterName, err := resolveClusterFromArgs(cmd, args)
		if err != nil {
			return err
		}

		if format == outputDefault {
			fmt.Printf("Triggering reconciliation for cluster: %s\n", clusterName)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.TriggerReconcile(ctx, clusterID)
		if err != nil {
			return fmt.Errorf("triggering reconcile: %w", err)
		}

		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, result)
		}

		if result.Success {
			fmt.Println("Reconciliation triggered successfully!")
		} else {
			fmt.Printf("Reconciliation request completed: %s\n", result.Message)
		}
		if result.Message != "" {
			fmt.Printf("Message: %s\n", result.Message)
		}
		return nil
	},
}

func resolveClusterFromArgs(cmd *cobra.Command, args []string) (string, string, error) {
	if len(args) > 0 {
		clusterName := args[0]
		cluster, err := apiClient.GetCluster(clusterName)
		if err != nil {
			return "", "", fmt.Errorf("finding cluster %s: %w", clusterName, err)
		}
		return cluster.ID, cluster.Name, nil
	}
	selected, err := resolveActiveCluster(cmd)
	if err != nil {
		return "", "", err
	}
	return selected.ID, selected.Name, nil
}

var clusterProvisionCmd = &cobra.Command{
	Use:   "provision [cluster_name]",
	Short: "Provision (start) a managed cluster",
	Long: `Start a managed cluster that was previously created but is not yet running.

If no cluster name is provided, uses the currently selected cluster.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		clusterID, clusterName, err := resolveClusterFromArgs(cmd, args)
		if err != nil {
			return err
		}

		if format == outputDefault {
			fmt.Printf("Provisioning cluster: %s\n", clusterName)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.ProvisionCluster(ctx, clusterID)
		if err != nil {
			return fmt.Errorf("provisioning cluster: %w", err)
		}

		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, result)
		}

		fmt.Printf("Cluster provisioning initiated.\n")
		if result.MarkedToStartAt != "" {
			fmt.Printf("  Scheduled at: %s\n", result.MarkedToStartAt)
		}
		return nil
	},
}

// cloudClusterKind enumerates the cluster kinds that must be deprovisioned
// through provider-specific endpoints. The backend's generic deprovision
// route explicitly rejects these kinds with HTTP 409 (see
// usecase/cluster/imported/deprovision_cluster.py).
type cloudClusterKind string

const (
	cloudClusterKindHetzner cloudClusterKind = "hetzner"
	cloudClusterKindOvh     cloudClusterKind = "ovh"
	cloudClusterKindUpcloud cloudClusterKind = "upcloud"
)

var clusterDeprovisionCmd = &cobra.Command{
	Use:   "deprovision [cluster_name]",
	Short: "Deprovision (stop) a managed cluster",
	Long: `Stop a running managed cluster. This will shut down the cluster but not delete it.

If no cluster name is provided, uses the currently selected cluster.

For cloud clusters (hetzner, ovh, upcloud) this command routes to the provider-specific
deprovision endpoint so cloud resources are released.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		autoDelete, _ := cmd.Flags().GetBool("auto-delete")
		force, _ := cmd.Flags().GetBool("force")
		yes, _ := cmd.Flags().GetBool("yes")

		format, err := structuredFormatFromFlags(cmd)
		if err != nil {
			return err
		}

		clusterID, clusterName, clusterKind, err := resolveClusterFromArgsWithKind(cmd, args)
		if err != nil {
			return err
		}

		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Deprovision cluster %q? This deletes all its cloud resources (servers, networks, SSH keys)! [y/N]: ", clusterName),
			yes,
		); err != nil {
			return err
		}

		if format == outputDefault {
			fmt.Printf("Deprovisioning cluster: %s\n", clusterName)
		}

		switch cloudClusterKind(clusterKind) {
		case cloudClusterKindHetzner:
			result, err := apiClient.DeprovisionHetznerCluster(clusterID, force)
			if err != nil {
				return fmt.Errorf("deprovisioning Hetzner cluster: %w", err)
			}
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, result)
			}
			fmt.Printf("Hetzner cluster deprovision initiated.\n")
			fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
			if result.OperationID != nil && *result.OperationID != "" {
				fmt.Printf("  Operation ID: %s\n", *result.OperationID)
			}
			return nil
		case cloudClusterKindOvh:
			result, err := apiClient.DeprovisionOvhCluster(clusterID)
			if err != nil {
				return fmt.Errorf("deprovisioning OVH cluster: %w", err)
			}
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, result)
			}
			fmt.Printf("OVH cluster deprovision initiated.\n")
			fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
			return nil
		case cloudClusterKindUpcloud:
			result, err := apiClient.DeprovisionUpcloudCluster(clusterID)
			if err != nil {
				return fmt.Errorf("deprovisioning UpCloud cluster: %w", err)
			}
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, result)
			}
			fmt.Printf("UpCloud cluster deprovision initiated.\n")
			fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
			if result.OperationID != nil && *result.OperationID != "" {
				fmt.Printf("  Operation ID: %s\n", *result.OperationID)
			}
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.DeprovisionCluster(ctx, clusterID, autoDelete, force)
		if err != nil {
			return fmt.Errorf("deprovisioning cluster: %w", err)
		}

		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, result)
		}

		fmt.Printf("Cluster deprovision initiated.\n")
		if result.MarkedForDeprovisionAt != "" {
			fmt.Printf("  Scheduled at: %s\n", result.MarkedForDeprovisionAt)
		}
		return nil
	},
}

func resolveClusterFromArgsWithKind(cmd *cobra.Command, args []string) (string, string, string, error) {
	if len(args) > 0 {
		identifier := args[0]
		// Accept either a cluster ID (consistent with `cluster scale`,
		// `cluster node-group`, `cluster upgrade`) or a cluster name.
		if cluster, err := apiClient.GetClusterByID(identifier); err == nil {
			return cluster.ID, cluster.Name, cluster.Kind, nil
		}
		cluster, err := apiClient.GetCluster(identifier)
		if err != nil {
			return "", "", "", fmt.Errorf("finding cluster %s: %w", identifier, err)
		}
		return cluster.ID, cluster.Name, cluster.Kind, nil
	}
	selected, err := resolveActiveCluster(cmd)
	if err != nil {
		return "", "", "", err
	}
	cluster, lookupErr := apiClient.GetCluster(selected.Name)
	if lookupErr != nil {
		// We have a resolved selection but the backend lookup failed. We
		// intentionally return the id/name with no kind so the generic
		// deprovision path is used (and the API will return a precise error
		// if the selection is stale).
		return selected.ID, selected.Name, "", nil
	}
	return cluster.ID, cluster.Name, cluster.Kind, nil
}

var clusterRollToCmd = &cobra.Command{
	Use:   "roll-to",
	Short: "Roll a cluster resource to a specific version",
	Long: `Roll a cluster to a specific resource version.

Uses the currently selected cluster unless --cluster is provided.

Example:
  ankra cluster roll-to --version abc123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		versionID, _ := cmd.Flags().GetString("version")

		selected, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		clusterID := selected.ID

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.RollToClusterResourceVersion(ctx, clusterID, versionID)
		if err != nil {
			return fmt.Errorf("rolling to version: %w", err)
		}

		if result.Ok {
			fmt.Printf("Roll-to version %s initiated successfully.\n", versionID)
			return nil
		}
		return fmt.Errorf("roll-to request completed but reported not ok")
	},
}

func init() {
	clusterDeprovisionCmd.Flags().Bool("auto-delete", false, "Automatically delete the cluster after deprovisioning")
	clusterDeprovisionCmd.Flags().Bool("force", false, "Force deprovision even if cluster is in an unexpected state")
	clusterDeprovisionCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	clusterRollToCmd.Flags().String("version", "", "Resource version ID to roll to (required)")
	_ = clusterRollToCmd.MarkFlagRequired("version")

	registerStructuredOutputFlags(
		clusterListCmd,
		clusterInfoCmd,
		clusterReconcileCmd,
		clusterProvisionCmd,
		clusterDeprovisionCmd,
	)

	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterInfoCmd)
	clusterCmd.AddCommand(clusterReconcileCmd)
	clusterCmd.AddCommand(clusterProvisionCmd)
	clusterCmd.AddCommand(clusterDeprovisionCmd)
	clusterCmd.AddCommand(clusterRollToCmd)
	rootCmd.AddCommand(clusterCmd)
}

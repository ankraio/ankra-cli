package cmd

import (
	"cmp"
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

var clusterSortFields = []sortField[client.ClusterListItem]{
	{"name", func(a, b client.ClusterListItem) int { return compareFold(a.Name, b.Name) }},
	{"environment", func(a, b client.ClusterListItem) int { return compareFold(a.Environment, b.Environment) }},
	{"kube-version", func(a, b client.ClusterListItem) int { return compareFold(a.KubeVersion, b.KubeVersion) }},
	{"nodes", func(a, b client.ClusterListItem) int { return cmp.Compare(a.Nodes, b.Nodes) }},
	{"control-planes", func(a, b client.ClusterListItem) int { return cmp.Compare(a.ControlPlanes, b.ControlPlanes) }},
	{"state", func(a, b client.ClusterListItem) int { return compareFold(a.State, b.State) }},
	{"kind", func(a, b client.ClusterListItem) int { return compareFold(a.Kind, b.Kind) }},
	{"created", func(a, b client.ClusterListItem) int { return compareTimeStrings(a.CreatedAt, b.CreatedAt) }},
}

var clusterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all clusters",
	Example: `  # newest clusters first
  ankra cluster list --sort created --order desc

  # alphabetical by name
  ankra cluster list --sort name`,
	RunE: func(cmd *cobra.Command, args []string) error {
		sortClusters, err := resolveSort(cmd, clusterSortFields)
		if err != nil {
			return err
		}
		clusters, err := listAllClusters()
		if err != nil {
			return fmt.Errorf("listing clusters: %w", err)
		}
		if clusters == nil {
			clusters = []client.ClusterListItem{}
		}
		sortClusters(clusters)
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
		if network := cluster.Network; network != nil {
			fmt.Printf("  Network (%s):\n", network.Provider)
			if network.VPCID != "" {
				fmt.Printf("    VPC ID: %s\n", network.VPCID)
			}
			if network.IPRange != "" {
				fmt.Printf("    IP Range: %s\n", network.IPRange)
			}
			if network.NATGatewayID != "" {
				fmt.Printf("    NAT Gateway ID: %s\n", network.NATGatewayID)
			}
			if network.EgressIP != "" {
				fmt.Printf("    Egress IP: %s\n", network.EgressIP)
			}
			if bastion := network.Bastion; bastion != nil {
				fmt.Printf("    Bastion: %s (public %s, private %s)\n",
					bastion.ID, bastion.PublicIP, bastion.PrivateIP)
			}
		}
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

		if result.CreatedOperations > 0 {
			fmt.Printf("Reconciliation triggered: %d operation(s) created\n", result.CreatedOperations)
		} else {
			fmt.Println("Reconciliation triggered: no operations created - stored state is already in sync")
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
	Short: "Provision a managed cluster (build its infrastructure and redeploy its stacks)",
	Long: `Provision a managed cluster that is not running.

This works for a cluster that was created but never built, and for an imported
cluster that was deprovisioned. It cannot rebuild a deprovisioned cloud cluster
(hetzner, ovh, upcloud, digitalocean, proxmox, morpheus): that deprovision
deleted the record, so there is nothing left to provision - create a new
cluster instead.

Provisioning rebuilds the cluster's infrastructure and then redeploys its stack
resources from the cluster's stored stack definition. Verify anything you patched
in place afterwards - see "ankra cluster addons list <addon>" and
"ankra cluster manifests list".

This is not a power-on. To resume a cluster you powered off, use the provider's
start command (for example "ankra hetzner cluster start") or
"ankra cluster power-schedules".

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
		// Stack resources are redeployed from the stored stack definition, so an
		// in-place patch is worth re-checking once the cluster is back online.
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
			"note: stacks are redeployed from the cluster's stored stack definition; "+
				"verify in-place patches with 'ankra cluster addons list <addon>' once the cluster is online")
		return nil
	},
}

// cloudClusterKind enumerates the cluster kinds that must be deprovisioned
// through provider-specific endpoints. The backend's generic deprovision
// route explicitly rejects these kinds with HTTP 409 (see
// usecase/cluster/imported/deprovision_cluster.py).
type cloudClusterKind string

const (
	cloudClusterKindHetzner      cloudClusterKind = "hetzner"
	cloudClusterKindOvh          cloudClusterKind = "ovh"
	cloudClusterKindUpcloud      cloudClusterKind = "upcloud"
	cloudClusterKindDigitalocean cloudClusterKind = "digitalocean"
	cloudClusterKindProxmox      cloudClusterKind = "proxmox"
	cloudClusterKindMorpheus     cloudClusterKind = "morpheus"
)

// isCloudClusterKind reports whether deprovisioning this kind goes to the
// provider endpoint, which deletes the cluster record along with its
// resources. The generic imported lane keeps the record, so the two are not
// interchangeable and the difference has to reach the operator.
func isCloudClusterKind(kind cloudClusterKind) bool {
	switch kind {
	case cloudClusterKindHetzner, cloudClusterKindOvh, cloudClusterKindUpcloud,
		cloudClusterKindDigitalocean, cloudClusterKindProxmox, cloudClusterKindMorpheus:
		return true
	default:
		return false
	}
}

var clusterDeprovisionCmd = &cobra.Command{
	Use:   "deprovision [cluster_name]",
	Short: "Deprovision a managed cluster (tear it down and release everything it runs on)",
	Long: `Tear down a managed cluster: everything it runs on is released.

Whether the cluster survives as a record depends on its kind, and the two
outcomes are very different:

  - cloud clusters (hetzner, ovh, upcloud, digitalocean, proxmox, morpheus)
    go to the provider-specific endpoint, which DELETES the cluster. The
    record does not survive, "ankra cluster provision" cannot bring it back,
    and the cluster id, its stacks and anything referencing them are gone.
    Rebuilding means creating a new cluster;
  - imported clusters keep their record, so "ankra cluster provision" can
    rebuild them from the stored stack definition later.

This is a teardown, not a power-off:

  - all cloud resources are released (servers, networks, SSH keys);
  - every stack resource on the cluster is uninstalled.

To power a cluster off and back on while keeping its state, use the provider's
stop/start commands (for example "ankra hetzner cluster stop" and
"ankra hetzner cluster start") or "ankra cluster power-schedules" instead.

If no cluster name is provided, uses the currently selected cluster.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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

		// The Hetzner, UpCloud, OVH and DigitalOcean deprovision endpoints
		// honor force; the Proxmox/Morpheus lanes and the generic imported
		// deprovision drop it, so say so instead of implying a forced
		// teardown that never happens.
		if force {
			switch cloudClusterKind(clusterKind) {
			case cloudClusterKindHetzner, cloudClusterKindUpcloud,
				cloudClusterKindOvh, cloudClusterKindDigitalocean:
			default:
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"warning: --force has no effect for this cluster type")
			}
		}

		// A cloud deprovision deletes the cluster itself, not just what it
		// runs on, and no later "cluster provision" can undo that. Saying so
		// here is the only warning an operator sees before typing y.
		recordWarning := ""
		if isCloudClusterKind(cloudClusterKind(clusterKind)) {
			recordWarning = " The cluster record itself is DELETED - this cannot be provisioned again, only recreated."
		}
		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Deprovision cluster %q? This deletes all its cloud resources (servers, networks, SSH keys) "+
				"and uninstalls every stack resource on it - this is a teardown, not a power-off.%s [y/N]: ",
				clusterName, recordWarning),
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
			result, err := apiClient.DeprovisionOvhCluster(clusterID, force)
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
			result, err := apiClient.DeprovisionUpcloudCluster(clusterID, force)
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
		case cloudClusterKindDigitalocean:
			result, err := apiClient.DeprovisionDigitaloceanCluster(clusterID, force)
			if err != nil {
				return fmt.Errorf("deprovisioning DigitalOcean cluster: %w", err)
			}
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, result)
			}
			fmt.Printf("DigitalOcean cluster deprovision initiated.\n")
			fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
			if result.OperationID != nil && *result.OperationID != "" {
				fmt.Printf("  Operation ID: %s\n", *result.OperationID)
			}
			return nil
		case cloudClusterKindProxmox:
			result, deprovisionError := apiClient.DeprovisionProxmoxCluster(clusterID)
			if deprovisionError != nil {
				return fmt.Errorf("deprovisioning Proxmox VE cluster: %w", deprovisionError)
			}
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, result)
			}
			fmt.Printf("Proxmox VE cluster deprovision initiated.\n")
			fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
			if result.OperationID != nil && *result.OperationID != "" {
				fmt.Printf("  Operation ID: %s\n", *result.OperationID)
			}
			return nil
		case cloudClusterKindMorpheus:
			result, deprovisionError := apiClient.DeprovisionMorpheusCluster(clusterID)
			if deprovisionError != nil {
				return fmt.Errorf("deprovisioning HPE Morpheus cluster: %w", deprovisionError)
			}
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, result)
			}
			fmt.Printf("HPE Morpheus cluster deprovision initiated.\n")
			fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
			if result.OperationID != nil && *result.OperationID != "" {
				fmt.Printf("  Operation ID: %s\n", *result.OperationID)
			}
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := apiClient.DeprovisionCluster(ctx, clusterID)
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
	// The backend never implemented auto_delete: it parsed and discarded the
	// parameter, so the flag has always been a silent no-op. Kept (hidden)
	// so existing scripts don't break on an unknown flag; see DEPRECATIONS.md.
	_ = clusterDeprovisionCmd.Flags().MarkDeprecated("auto-delete",
		"the backend does not support it; deprovision never deletes the cluster record (use 'ankra delete cluster' afterwards)")
	clusterDeprovisionCmd.Flags().Bool("force", false, "Force deprovision even if cluster is in an unexpected state; on UpCloud, Hetzner, OVH and DigitalOcean also deletes leftover CSI storage volumes and load balancers")
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
	registerSortFlags(clusterListCmd, clusterSortFields)

	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterInfoCmd)
	clusterCmd.AddCommand(clusterReconcileCmd)
	clusterCmd.AddCommand(clusterProvisionCmd)
	clusterCmd.AddCommand(clusterDeprovisionCmd)
	clusterCmd.AddCommand(clusterRollToCmd)
	rootCmd.AddCommand(clusterCmd)
}

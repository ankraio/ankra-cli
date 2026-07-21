package cmd

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var managedCmd = &cobra.Command{
	Use:   "managed",
	Short: "Manage cloud-managed Kubernetes clusters",
	Long:  "Create, delete, stop and start, scale node pools, and upgrade cloud-managed Kubernetes clusters on DOKS, UpCloud UKS, GKE, OVH MKS, AKS, EKS, and Scaleway Kapsule.",
}

var managedCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a managed Kubernetes cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseManagedProviderFlag(cmd)
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		credentialID, _ := cmd.Flags().GetString("credential-id")
		location, _ := cmd.Flags().GetString("location")
		kubeVersion, _ := cmd.Flags().GetString("kubernetes-version")
		nodePoolName, _ := cmd.Flags().GetString("node-pool-name")
		nodePoolSize, _ := cmd.Flags().GetString("node-pool-size")
		nodePoolCount, _ := cmd.Flags().GetInt("node-pool-count")
		gitopsCredentialName, _ := cmd.Flags().GetString("gitops-credential-name")
		gitopsRepository, _ := cmd.Flags().GetString("gitops-repository")
		gitopsBranch, _ := cmd.Flags().GetString("gitops-branch")
		privateNetworkID, _ := cmd.Flags().GetString("private-network-id")

		autoscaling, autoscalingError := parseManagedAutoscalingFlags(cmd)
		if autoscalingError != nil {
			return autoscalingError
		}

		request := client.CreateManagedClusterRequest{
			Name:         name,
			CredentialID: credentialID,
			Location:     location,
			NodePools: []client.ManagedClusterNodePoolRequest{
				{
					Name:        nodePoolName,
					Size:        nodePoolSize,
					Count:       nodePoolCount,
					Autoscaling: autoscaling,
				},
			},
		}
		if provider == client.ManagedK8sProviderKapsule {
			if privateNetworkID == "" {
				return withExitCode(exitUsage, errors.New("--private-network-id is required with --provider kapsule"))
			}
			request.Kapsule = &client.KapsuleClusterOptions{PrivateNetworkID: privateNetworkID}
		} else if privateNetworkID != "" {
			return withExitCode(exitUsage, fmt.Errorf("--private-network-id is only supported with --provider kapsule, not %q", provider))
		}
		if kubeVersion != "" {
			request.KubernetesVersion = &kubeVersion
		}
		if gitopsCredentialName != "" {
			request.GitopsCredentialName = &gitopsCredentialName
		}
		if gitopsRepository != "" {
			request.GitopsRepository = &gitopsRepository
			if gitopsBranch != "" {
				request.GitopsBranch = &gitopsBranch
			}
		}

		result, err := apiClient.CreateManagedCluster(provider, request)
		if err != nil {
			return fmt.Errorf("creating managed cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Managed %s cluster '%s' created successfully!\n", provider, result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
		return nil
	},
}

var managedDeleteCmd = &cobra.Command{
	Use:   "delete <cluster_id>",
	Short: "Delete a managed Kubernetes cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseManagedProviderFlag(cmd)
		if err != nil {
			return err
		}

		clusterID := args[0]
		force, _ := cmd.Flags().GetBool("force")
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete managed %s cluster %q? This destroys all cloud resources! [y/N]: ", provider, clusterID),
			yes); err != nil {
			return err
		}

		result, err := apiClient.DeprovisionManagedCluster(provider, clusterID, force)
		if err != nil {
			return fmt.Errorf("deleting managed cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("Managed cluster deletion initiated!"))
		} else {
			fmt.Println("Managed cluster deletion requested with issues.")
		}
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if result.OperationID != nil {
			fmt.Printf("  Operation ID: %s\n", *result.OperationID)
		}
		if result.ResourcesMarked > 0 {
			fmt.Printf("  Resources queued for deletion: %d\n", result.ResourcesMarked)
		}
		if len(result.Errors) > 0 {
			fmt.Println(text.FgYellow.Sprint("  Warnings:"))
			for _, warning := range result.Errors {
				fmt.Printf("    - %s\n", warning)
			}
		}
		return nil
	},
}

var managedNodePoolCmd = &cobra.Command{
	Use:   "node-pool",
	Short: "Manage managed cluster node pools",
}

var managedNodePoolAddCmd = &cobra.Command{
	Use:   "add <cluster_id>",
	Short: "Add a node pool to a managed cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseManagedProviderFlag(cmd)
		if err != nil {
			return err
		}

		clusterID := args[0]
		name, _ := cmd.Flags().GetString("name")
		size, _ := cmd.Flags().GetString("size")
		count, _ := cmd.Flags().GetInt("count")

		autoscaling, autoscalingError := parseManagedAutoscalingFlags(cmd)
		if autoscalingError != nil {
			return autoscalingError
		}

		result, err := apiClient.AddManagedNodePool(provider, clusterID, client.AddManagedNodePoolRequest{
			Name:        name,
			Size:        size,
			Count:       count,
			Autoscaling: autoscaling,
		})
		if err != nil {
			return fmt.Errorf("adding node pool: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Node pool %q added to cluster %s (count: %d)\n", result.NodePoolName, result.ClusterID, result.Count)
		return nil
	},
}

var managedNodePoolScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <node_pool_name>",
	Short: "Scale a managed cluster node pool",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseManagedProviderFlag(cmd)
		if err != nil {
			return err
		}

		clusterID := args[0]
		nodePoolName := args[1]
		count, _ := cmd.Flags().GetInt("count")

		result, err := apiClient.ScaleManagedNodePool(provider, clusterID, nodePoolName, count)
		if err != nil {
			return fmt.Errorf("scaling node pool: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Node pool %q scaled to %d nodes on cluster %s\n", result.NodePoolName, result.Count, result.ClusterID)
		return nil
	},
}

var managedNodePoolDeleteCmd = &cobra.Command{
	Use:   "delete <cluster_id> <node_pool_name>",
	Short: "Delete a managed cluster node pool",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseManagedProviderFlag(cmd)
		if err != nil {
			return err
		}

		clusterID := args[0]
		nodePoolName := args[1]
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete node pool %q from cluster %q? [y/N]: ", nodePoolName, clusterID),
			yes); err != nil {
			return err
		}

		result, err := apiClient.DeleteManagedNodePool(provider, clusterID, nodePoolName)
		if err != nil {
			return fmt.Errorf("deleting node pool: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Node pool %q deleted from cluster %s\n", result.NodePoolName, result.ClusterID)
		return nil
	},
}

var managedNodePoolUpdateCmd = &cobra.Command{
	Use:   "update <cluster_id> <node_pool_name>",
	Short: "Update a managed cluster node pool",
	Long: `Update the node count or autoscaling settings of a managed cluster node
pool. Pass at least one of --count, --autoscaling, --autoscaling-min, or
--autoscaling-max; unspecified settings are left unchanged.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseManagedProviderFlag(cmd)
		if err != nil {
			return err
		}

		clusterID := args[0]
		nodePoolName := args[1]

		var request client.UpdateManagedNodePoolRequest
		if cmd.Flags().Changed("count") {
			count, _ := cmd.Flags().GetInt("count")
			request.Count = &count
		}
		if cmd.Flags().Changed("autoscaling") {
			enabled, _ := cmd.Flags().GetBool("autoscaling")
			request.AutoscalingEnabled = &enabled
		}
		if cmd.Flags().Changed("autoscaling-min") {
			minCount, _ := cmd.Flags().GetInt("autoscaling-min")
			request.AutoscalingMin = &minCount
		}
		if cmd.Flags().Changed("autoscaling-max") {
			maxCount, _ := cmd.Flags().GetInt("autoscaling-max")
			request.AutoscalingMax = &maxCount
		}
		if request == (client.UpdateManagedNodePoolRequest{}) {
			return withExitCode(exitUsage, errors.New("pass at least one of --count, --autoscaling, --autoscaling-min, --autoscaling-max"))
		}

		result, err := apiClient.UpdateManagedNodePool(provider, clusterID, nodePoolName, request)
		if err != nil {
			return fmt.Errorf("updating node pool: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Node pool %q updated on cluster %s\n", result.NodePoolName, result.ClusterID)
		if result.Count != nil {
			fmt.Printf("  Count: %d\n", *result.Count)
		}
		if result.AutoscalingEnabled != nil {
			if *result.AutoscalingEnabled {
				if result.AutoscalingMin != nil && result.AutoscalingMax != nil {
					fmt.Printf("  Autoscaling: enabled (min %d, max %d)\n", *result.AutoscalingMin, *result.AutoscalingMax)
				} else {
					fmt.Println("  Autoscaling: enabled")
				}
			} else {
				fmt.Println("  Autoscaling: disabled")
			}
		}
		return nil
	},
}

var managedStopCmd = &cobra.Command{
	Use:   "stop <cluster_id>",
	Short: "Stop a managed cluster's compute",
	Long: `Stop a managed Kubernetes cluster's compute while keeping its configuration
so it can be started again later. Currently only AKS supports stopping and
starting managed clusters.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseManagedProviderFlag(cmd)
		if err != nil {
			return err
		}

		result, stopError := apiClient.StopManagedCluster(provider, args[0])
		if stopError != nil {
			return managedLifecycleError("stopping", stopError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Println(text.FgGreen.Sprint("Managed cluster stop initiated."))
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if result.Status != "" {
			fmt.Printf("  Status: %s\n", result.Status)
		}
		return nil
	},
}

var managedStartCmd = &cobra.Command{
	Use:   "start <cluster_id>",
	Short: "Start a stopped managed cluster",
	Long: `Start a stopped managed Kubernetes cluster. Currently only AKS supports
stopping and starting managed clusters.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseManagedProviderFlag(cmd)
		if err != nil {
			return err
		}

		result, startError := apiClient.StartManagedCluster(provider, args[0])
		if startError != nil {
			return managedLifecycleError("starting", startError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Println(text.FgGreen.Sprint("Managed cluster start initiated."))
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if result.Status != "" {
			fmt.Printf("  Status: %s\n", result.Status)
		}
		return nil
	},
}

var managedUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id>",
	Short: "Upgrade a managed cluster Kubernetes version",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, err := parseManagedProviderFlag(cmd)
		if err != nil {
			return err
		}

		clusterID := args[0]
		version, _ := cmd.Flags().GetString("version")
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Upgrade managed cluster %q to Kubernetes %s? [y/N]: ", clusterID, version),
			yes); err != nil {
			return err
		}

		result, err := apiClient.UpgradeManagedK8sVersion(provider, clusterID, version)
		if err != nil {
			return fmt.Errorf("upgrading managed cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Managed cluster %s upgraded to Kubernetes %s\n", result.ClusterID, result.Version)
		return nil
	},
}

const managedProviderFlagHelp = "Managed Kubernetes provider (doks, uks, gke, ovh_mks, aks, eks, kapsule)"

func parseManagedProviderFlag(cmd *cobra.Command) (client.ManagedK8sProvider, error) {
	providerValue, _ := cmd.Flags().GetString("provider")
	switch strings.ToLower(strings.TrimSpace(providerValue)) {
	case "doks":
		return client.ManagedK8sProviderDoks, nil
	case "uks":
		return client.ManagedK8sProviderUks, nil
	case "gke":
		return client.ManagedK8sProviderGke, nil
	case "ovh_mks", "ovh-mks", "mks":
		return client.ManagedK8sProviderOvhMks, nil
	case "aks":
		return client.ManagedK8sProviderAks, nil
	case "eks":
		return client.ManagedK8sProviderEks, nil
	case "kapsule":
		return client.ManagedK8sProviderKapsule, nil
	default:
		return "", fmt.Errorf("invalid provider %q: must be one of doks, uks, gke, ovh_mks, aks, eks, kapsule", providerValue)
	}
}

// parseManagedAutoscalingFlags reads the shared --autoscaling trio on
// node-pool-shaped commands. Bounds without --autoscaling, or --autoscaling
// without both bounds, are usage errors.
func parseManagedAutoscalingFlags(cmd *cobra.Command) (*client.ManagedNodePoolAutoscaling, error) {
	enabled, _ := cmd.Flags().GetBool("autoscaling")
	if !enabled {
		if cmd.Flags().Changed("autoscaling-min") || cmd.Flags().Changed("autoscaling-max") {
			return nil, withExitCode(exitUsage, errors.New("--autoscaling-min and --autoscaling-max require --autoscaling"))
		}
		return nil, nil
	}
	if !cmd.Flags().Changed("autoscaling-min") || !cmd.Flags().Changed("autoscaling-max") {
		return nil, withExitCode(exitUsage, errors.New("--autoscaling requires both --autoscaling-min and --autoscaling-max"))
	}
	minCount, _ := cmd.Flags().GetInt("autoscaling-min")
	maxCount, _ := cmd.Flags().GetInt("autoscaling-max")
	return &client.ManagedNodePoolAutoscaling{Enabled: true, MinCount: minCount, MaxCount: maxCount}, nil
}

// managedLifecycleError decorates stop/start API refusals: when the backend
// answers that the provider cannot stop or start clusters, remind the user
// that only AKS currently supports it.
func managedLifecycleError(action string, apiError error) error {
	var unexpected *client.UnexpectedResponseError
	if errors.As(apiError, &unexpected) &&
		unexpected.StatusCode >= 400 && unexpected.StatusCode < 500 &&
		strings.Contains(strings.ToLower(unexpected.Error()), "support") {
		return fmt.Errorf("%s managed cluster: %w (only AKS currently supports managed cluster stop/start)", action, apiError)
	}
	return fmt.Errorf("%s managed cluster: %w", action, apiError)
}

func init() {
	managedCreateCmd.Flags().String("provider", "", managedProviderFlagHelp)
	managedCreateCmd.Flags().String("name", "", "Cluster name")
	managedCreateCmd.Flags().String("credential-id", "", "Cloud credential ID")
	managedCreateCmd.Flags().String("location", "", "Region or zone for the cluster")
	managedCreateCmd.Flags().String("kubernetes-version", "", "Kubernetes version (optional)")
	managedCreateCmd.Flags().String("node-pool-name", "workers", "Initial node pool name")
	managedCreateCmd.Flags().String("node-pool-size", "", "Initial node pool size/plan")
	managedCreateCmd.Flags().Int("node-pool-count", 2, "Initial node pool node count")
	managedCreateCmd.Flags().String("gitops-credential-name", "", "GitOps credential name (optional)")
	managedCreateCmd.Flags().String("gitops-repository", "", "GitOps repository URL (optional)")
	managedCreateCmd.Flags().String("gitops-branch", "master", "GitOps branch (optional)")
	managedCreateCmd.Flags().String("private-network-id", "", "Scaleway private network ID (required with --provider kapsule)")
	managedCreateCmd.Flags().Bool("autoscaling", false, "Enable autoscaling for the initial node pool")
	managedCreateCmd.Flags().Int("autoscaling-min", 0, "Minimum node count while autoscaling (requires --autoscaling)")
	managedCreateCmd.Flags().Int("autoscaling-max", 0, "Maximum node count while autoscaling (requires --autoscaling)")
	_ = managedCreateCmd.MarkFlagRequired("provider")
	_ = managedCreateCmd.MarkFlagRequired("name")
	_ = managedCreateCmd.MarkFlagRequired("credential-id")
	_ = managedCreateCmd.MarkFlagRequired("location")
	_ = managedCreateCmd.MarkFlagRequired("node-pool-size")

	managedDeleteCmd.Flags().String("provider", "", managedProviderFlagHelp)
	managedDeleteCmd.Flags().Bool("force", false, "Force delete even when cluster is in a non-idle state")
	managedDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	_ = managedDeleteCmd.MarkFlagRequired("provider")

	managedNodePoolAddCmd.Flags().String("provider", "", managedProviderFlagHelp)
	managedNodePoolAddCmd.Flags().String("name", "", "Node pool name")
	managedNodePoolAddCmd.Flags().String("size", "", "Node pool size/plan")
	managedNodePoolAddCmd.Flags().Int("count", 1, "Node count")
	managedNodePoolAddCmd.Flags().Bool("autoscaling", false, "Enable autoscaling for the node pool")
	managedNodePoolAddCmd.Flags().Int("autoscaling-min", 0, "Minimum node count while autoscaling (requires --autoscaling)")
	managedNodePoolAddCmd.Flags().Int("autoscaling-max", 0, "Maximum node count while autoscaling (requires --autoscaling)")
	_ = managedNodePoolAddCmd.MarkFlagRequired("provider")
	_ = managedNodePoolAddCmd.MarkFlagRequired("name")
	_ = managedNodePoolAddCmd.MarkFlagRequired("size")

	managedNodePoolScaleCmd.Flags().String("provider", "", managedProviderFlagHelp)
	managedNodePoolScaleCmd.Flags().Int("count", 0, "Target node count")
	_ = managedNodePoolScaleCmd.MarkFlagRequired("provider")
	_ = managedNodePoolScaleCmd.MarkFlagRequired("count")

	managedNodePoolDeleteCmd.Flags().String("provider", "", managedProviderFlagHelp)
	managedNodePoolDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	_ = managedNodePoolDeleteCmd.MarkFlagRequired("provider")

	managedNodePoolUpdateCmd.Flags().String("provider", "", managedProviderFlagHelp)
	managedNodePoolUpdateCmd.Flags().Int("count", 0, "Target node count")
	managedNodePoolUpdateCmd.Flags().Bool("autoscaling", false, "Enable (true) or disable (false) autoscaling")
	managedNodePoolUpdateCmd.Flags().Int("autoscaling-min", 0, "Minimum node count while autoscaling")
	managedNodePoolUpdateCmd.Flags().Int("autoscaling-max", 0, "Maximum node count while autoscaling")
	_ = managedNodePoolUpdateCmd.MarkFlagRequired("provider")

	managedStopCmd.Flags().String("provider", "", managedProviderFlagHelp)
	_ = managedStopCmd.MarkFlagRequired("provider")

	managedStartCmd.Flags().String("provider", "", managedProviderFlagHelp)
	_ = managedStartCmd.MarkFlagRequired("provider")

	managedUpgradeCmd.Flags().String("provider", "", managedProviderFlagHelp)
	managedUpgradeCmd.Flags().String("version", "", "Target Kubernetes version")
	managedUpgradeCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	_ = managedUpgradeCmd.MarkFlagRequired("provider")
	_ = managedUpgradeCmd.MarkFlagRequired("version")

	managedNodePoolCmd.AddCommand(managedNodePoolAddCmd, managedNodePoolScaleCmd, managedNodePoolDeleteCmd, managedNodePoolUpdateCmd)
	managedCmd.AddCommand(managedCreateCmd, managedDeleteCmd, managedNodePoolCmd, managedUpgradeCmd, managedStopCmd, managedStartCmd)
	clusterCmd.AddCommand(managedCmd)

	registerStructuredOutputFlags(managedCreateCmd, managedDeleteCmd, managedNodePoolAddCmd, managedNodePoolScaleCmd, managedNodePoolDeleteCmd, managedNodePoolUpdateCmd, managedUpgradeCmd, managedStopCmd, managedStartCmd)
}

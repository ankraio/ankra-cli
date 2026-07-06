package cmd

import (
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var managedCmd = &cobra.Command{
	Use:   "managed",
	Short: "Manage DigitalOcean and UpCloud managed Kubernetes clusters",
	Long:  "Create, delete, scale node pools, and upgrade managed Kubernetes clusters on DOKS and UpCloud UKS.",
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

		request := client.CreateManagedClusterRequest{
			Name:         name,
			CredentialID: credentialID,
			Location:     location,
			NodePools: []client.ManagedClusterNodePoolRequest{
				{
					Name:  nodePoolName,
					Size:  nodePoolSize,
					Count: nodePoolCount,
				},
			},
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

		result, err := apiClient.AddManagedNodePool(provider, clusterID, client.AddManagedNodePoolRequest{
			Name:  name,
			Size:  size,
			Count: count,
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

func parseManagedProviderFlag(cmd *cobra.Command) (client.ManagedK8sProvider, error) {
	providerValue, _ := cmd.Flags().GetString("provider")
	switch providerValue {
	case "doks":
		return client.ManagedK8sProviderDoks, nil
	case "uks":
		return client.ManagedK8sProviderUks, nil
	default:
		return "", fmt.Errorf("invalid provider %q: must be doks or uks", providerValue)
	}
}

func init() {
	managedCreateCmd.Flags().String("provider", "", "Managed Kubernetes provider (doks or uks)")
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
	_ = managedCreateCmd.MarkFlagRequired("provider")
	_ = managedCreateCmd.MarkFlagRequired("name")
	_ = managedCreateCmd.MarkFlagRequired("credential-id")
	_ = managedCreateCmd.MarkFlagRequired("location")
	_ = managedCreateCmd.MarkFlagRequired("node-pool-size")

	managedDeleteCmd.Flags().String("provider", "", "Managed Kubernetes provider (doks or uks)")
	managedDeleteCmd.Flags().Bool("force", false, "Force delete even when cluster is in a non-idle state")
	managedDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	_ = managedDeleteCmd.MarkFlagRequired("provider")

	managedNodePoolAddCmd.Flags().String("provider", "", "Managed Kubernetes provider (doks or uks)")
	managedNodePoolAddCmd.Flags().String("name", "", "Node pool name")
	managedNodePoolAddCmd.Flags().String("size", "", "Node pool size/plan")
	managedNodePoolAddCmd.Flags().Int("count", 1, "Node count")
	_ = managedNodePoolAddCmd.MarkFlagRequired("provider")
	_ = managedNodePoolAddCmd.MarkFlagRequired("name")
	_ = managedNodePoolAddCmd.MarkFlagRequired("size")

	managedNodePoolScaleCmd.Flags().String("provider", "", "Managed Kubernetes provider (doks or uks)")
	managedNodePoolScaleCmd.Flags().Int("count", 0, "Target node count")
	_ = managedNodePoolScaleCmd.MarkFlagRequired("provider")
	_ = managedNodePoolScaleCmd.MarkFlagRequired("count")

	managedNodePoolDeleteCmd.Flags().String("provider", "", "Managed Kubernetes provider (doks or uks)")
	managedNodePoolDeleteCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	_ = managedNodePoolDeleteCmd.MarkFlagRequired("provider")

	managedUpgradeCmd.Flags().String("provider", "", "Managed Kubernetes provider (doks or uks)")
	managedUpgradeCmd.Flags().String("version", "", "Target Kubernetes version")
	managedUpgradeCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	_ = managedUpgradeCmd.MarkFlagRequired("provider")
	_ = managedUpgradeCmd.MarkFlagRequired("version")

	managedNodePoolCmd.AddCommand(managedNodePoolAddCmd, managedNodePoolScaleCmd, managedNodePoolDeleteCmd)
	managedCmd.AddCommand(managedCreateCmd, managedDeleteCmd, managedNodePoolCmd, managedUpgradeCmd)
	clusterCmd.AddCommand(managedCmd)

	registerStructuredOutputFlags(managedCreateCmd, managedDeleteCmd, managedNodePoolAddCmd, managedNodePoolScaleCmd, managedNodePoolDeleteCmd, managedUpgradeCmd)
}

package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var hetznerCmd = &cobra.Command{
	Use:   "hetzner",
	Short: "Manage Hetzner clusters",
	Long:  "Commands to create, deprovision, and scale Hetzner clusters.",
}

var hetznerCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new Hetzner cluster",
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		credentialID, _ := cmd.Flags().GetString("credential-id")
		sshKeyCredentialID, _ := cmd.Flags().GetString("ssh-key-credential-id")
		sshKeyCredentialIDs, _ := cmd.Flags().GetStringSlice("ssh-key-credential-ids")
		location, _ := cmd.Flags().GetString("location")
		networkIPRange, _ := cmd.Flags().GetString("network-ip-range")
		subnetRange, _ := cmd.Flags().GetString("subnet-range")
		bastionServerType, _ := cmd.Flags().GetString("bastion-server-type")
		cpCount, _ := cmd.Flags().GetInt("control-plane-count")
		cpServerType, _ := cmd.Flags().GetString("control-plane-server-type")
		workerCount, _ := cmd.Flags().GetInt("worker-count")
		workerServerType, _ := cmd.Flags().GetString("worker-server-type")
		distribution, _ := cmd.Flags().GetString("distribution")
		kubeVersion, _ := cmd.Flags().GetString("kubernetes-version")

		if sshKeyCredentialID != "" && len(sshKeyCredentialIDs) == 0 {
			sshKeyCredentialIDs = []string{sshKeyCredentialID}
		}

		req := client.CreateHetznerClusterRequest{
			Name:                   name,
			CredentialID:           credentialID,
			SSHKeyCredentialIDs:    sshKeyCredentialIDs,
			Location:               location,
			NetworkIPRange:         networkIPRange,
			SubnetRange:            subnetRange,
			BastionServerType:      bastionServerType,
			ControlPlaneCount:      cpCount,
			ControlPlaneServerType: cpServerType,
			WorkerCount:            workerCount,
			WorkerServerType:       workerServerType,
			Distribution:           distribution,
		}
		if kubeVersion != "" {
			req.KubernetesVersion = &kubeVersion
		}

		result, err := client.CreateHetznerCluster(apiToken, baseURL, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating Hetzner cluster: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Hetzner cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
	},
}

var hetznerDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id>",
	Short: "Deprovision a Hetzner cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.DeprovisionHetznerCluster(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deprovisioning cluster: %v\n", err)
			os.Exit(1)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("Hetzner cluster deprovisioned successfully!"))
		} else {
			fmt.Println("Cluster deprovisioned with issues.")
		}

		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if len(result.DeletedServers) > 0 {
			fmt.Printf("  Deleted servers: %d\n", len(result.DeletedServers))
		}
		if len(result.DeletedNetworks) > 0 {
			fmt.Printf("  Deleted networks: %d\n", len(result.DeletedNetworks))
		}
		if len(result.DeletedSSHKeys) > 0 {
			fmt.Printf("  Deleted SSH keys: %d\n", len(result.DeletedSSHKeys))
		}
		if len(result.Errors) > 0 {
			fmt.Println(text.FgYellow.Sprint("  Warnings:"))
			for _, e := range result.Errors {
				fmt.Printf("    - %s\n", e)
			}
		}
	},
}

var hetznerWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for a Hetzner cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.GetHetznerWorkerCount(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching worker count: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Worker Count: %d\n", result.WorkerCount)
		fmt.Printf("  Min: %d\n", result.Min)
		fmt.Printf("  Max: %d\n", result.Max)
	},
}

var hetznerScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <worker_count>",
	Short: "Scale workers for a Hetzner cluster",
	Long:  "Scale the number of worker nodes up or down for a Hetzner cluster.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		workerCount, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid worker count: %v\n", err)
			os.Exit(1)
		}

		result, err := client.ScaleHetznerWorkers(apiToken, baseURL, clusterID, workerCount)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scaling workers: %v\n", err)
			os.Exit(1)
		}

		if result.PreviousCount == result.NewCount {
			fmt.Printf("Worker count is already %d, no changes needed.\n", result.NewCount)
		} else if result.NewCount > result.PreviousCount {
			fmt.Printf("Scaling %s from %d to %d workers.\n",
				text.FgGreen.Sprint("up"), result.PreviousCount, result.NewCount)
		} else {
			fmt.Printf("Scaling %s from %d to %d workers.\n",
				text.FgYellow.Sprint("down"), result.PreviousCount, result.NewCount)
		}
	},
}

var hetznerK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id>",
	Short: "Get current Kubernetes version for a Hetzner cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.GetHetznerK8sVersion(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching Kubernetes version: %v\n", err)
			os.Exit(1)
		}

		version := "not set (using latest stable)"
		if result.CurrentVersion != nil {
			version = *result.CurrentVersion
		}
		fmt.Printf("Kubernetes Version: %s\n", version)
		fmt.Printf("  Distribution: %s\n", result.Distribution)
	},
}

var hetznerUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <target_version>",
	Short: "Upgrade Kubernetes version for a Hetzner cluster",
	Long:  "Upgrade the Kubernetes (k3s) version on all nodes in a Hetzner cluster.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		targetVersion := args[1]

		result, err := client.UpgradeHetznerK8sVersion(apiToken, baseURL, clusterID, targetVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error upgrading Kubernetes version: %v\n", err)
			os.Exit(1)
		}

		prev := "none"
		if result.PreviousVersion != nil {
			prev = *result.PreviousVersion
		}
		fmt.Printf("Kubernetes version upgrade initiated.\n")
		fmt.Printf("  Previous version: %s\n", prev)
		fmt.Printf("  New version:      %s\n", text.FgGreen.Sprint(result.NewVersion))
		fmt.Printf("  Nodes affected:   %d\n", result.NodesAffected)
	},
}

var nodeGroupCmd = &cobra.Command{
	Use:   "node-group",
	Short: "Manage node groups for a Hetzner cluster",
	Long:  "List, add, scale, upgrade, and delete node groups.",
}

var nodeGroupListCmd = &cobra.Command{
	Use:   "list <cluster_id>",
	Short: "List node groups",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		result, err := client.ListHetznerNodeGroups(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing node groups: %v\n", err)
			os.Exit(1)
		}
		if len(result.NodeGroups) == 0 {
			fmt.Println("No node groups found.")
			return
		}
		for _, ng := range result.NodeGroups {
			fmt.Printf("%-20s  type=%-8s  count=%d  labels=%d  taints=%d\n",
				ng.Name, ng.InstanceType, ng.Count, len(ng.Labels), len(ng.Taints))
		}
	},
}

var nodeGroupAddCmd = &cobra.Command{
	Use:   "add <cluster_id>",
	Short: "Add a node group",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		name, _ := cmd.Flags().GetString("name")
		instanceType, _ := cmd.Flags().GetString("instance-type")
		count, _ := cmd.Flags().GetInt("count")

		req := client.AddNodeGroupRequest{
			Name:         name,
			InstanceType: instanceType,
			Count:        count,
		}

		result, err := client.AddHetznerNodeGroup(apiToken, baseURL, clusterID, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error adding node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' created with %d node(s).\n", result.GroupName, result.Count)
	},
}

var nodeGroupScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <group_name> <count>",
	Short: "Scale a node group",
	Args:  cobra.ExactArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		groupName := args[1]
		count, err := strconv.Atoi(args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid count: %v\n", err)
			os.Exit(1)
		}

		result, err := client.ScaleHetznerNodeGroup(apiToken, baseURL, clusterID, groupName, count)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scaling node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' scaled from %d to %d.\n", result.GroupName, result.PreviousCount, result.NewCount)
	},
}

var nodeGroupUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <group_name> <instance_type>",
	Short: "Upgrade instance type for a node group (cannot be reversed)",
	Args:  cobra.ExactArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		groupName := args[1]
		instanceType := args[2]

		result, err := client.UpdateHetznerNodeGroupInstanceType(apiToken, baseURL, clusterID, groupName, instanceType)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error upgrading node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' instance type upgraded. %d node(s) affected.\n", result.GroupName, result.Updated)
	},
}

var nodeGroupDeleteCmd = &cobra.Command{
	Use:   "delete <cluster_id> <group_name>",
	Short: "Delete a node group and all its nodes",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		groupName := args[1]

		result, err := client.DeleteHetznerNodeGroup(apiToken, baseURL, clusterID, groupName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' deleted. %d node(s) removed.\n", result.GroupName, result.Deleted)
	},
}

func init() {
	hetznerCreateCmd.Flags().String("name", "", "Cluster name (required)")
	hetznerCreateCmd.Flags().String("credential-id", "", "Hetzner API credential ID (required)")
	hetznerCreateCmd.Flags().String("ssh-key-credential-id", "", "SSH key credential ID (single key, use --ssh-key-credential-ids for multiple)")
	hetznerCreateCmd.Flags().StringSlice("ssh-key-credential-ids", nil, "SSH key credential IDs (comma-separated or repeated)")
	hetznerCreateCmd.Flags().String("location", "", "Hetzner location (required)")
	hetznerCreateCmd.Flags().String("network-ip-range", "10.0.0.0/16", "Network IP range")
	hetznerCreateCmd.Flags().String("subnet-range", "10.0.1.0/24", "Subnet range")
	hetznerCreateCmd.Flags().String("bastion-server-type", "cx23", "Bastion server type")
	hetznerCreateCmd.Flags().Int("control-plane-count", 1, "Number of control plane nodes")
	hetznerCreateCmd.Flags().String("control-plane-server-type", "cx33", "Control plane server type")
	hetznerCreateCmd.Flags().Int("worker-count", 1, "Number of worker nodes")
	hetznerCreateCmd.Flags().String("worker-server-type", "cx33", "Worker server type")
	hetznerCreateCmd.Flags().String("distribution", "k3s", "Kubernetes distribution")
	hetznerCreateCmd.Flags().String("kubernetes-version", "", "Kubernetes version (optional)")

	_ = hetznerCreateCmd.MarkFlagRequired("name")
	_ = hetznerCreateCmd.MarkFlagRequired("credential-id")
	_ = hetznerCreateCmd.MarkFlagRequired("location")

	nodeGroupAddCmd.Flags().String("name", "", "Node group name (required)")
	nodeGroupAddCmd.Flags().String("instance-type", "cx33", "Server type for nodes")
	nodeGroupAddCmd.Flags().Int("count", 1, "Number of nodes (0-100)")
	_ = nodeGroupAddCmd.MarkFlagRequired("name")

	nodeGroupCmd.AddCommand(nodeGroupListCmd)
	nodeGroupCmd.AddCommand(nodeGroupAddCmd)
	nodeGroupCmd.AddCommand(nodeGroupScaleCmd)
	nodeGroupCmd.AddCommand(nodeGroupUpgradeCmd)
	nodeGroupCmd.AddCommand(nodeGroupDeleteCmd)

	hetznerCmd.AddCommand(hetznerCreateCmd)
	hetznerCmd.AddCommand(hetznerDeprovisionCmd)
	hetznerCmd.AddCommand(hetznerWorkersCmd)
	hetznerCmd.AddCommand(hetznerScaleCmd)
	hetznerCmd.AddCommand(hetznerK8sVersionCmd)
	hetznerCmd.AddCommand(hetznerUpgradeCmd)
	hetznerCmd.AddCommand(nodeGroupCmd)

	clusterCmd.AddCommand(hetznerCmd)
}

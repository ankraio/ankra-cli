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

var ovhCmd = &cobra.Command{
	Use:   "ovh",
	Short: "Manage OVH clusters",
	Long:  "Commands to create, deprovision, and scale OVH Cloud clusters.",
}

var ovhCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new OVH cluster",
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		credentialID, _ := cmd.Flags().GetString("credential-id")
		sshKeyCredentialID, _ := cmd.Flags().GetString("ssh-key-credential-id")
		region, _ := cmd.Flags().GetString("region")
		networkVlanID, _ := cmd.Flags().GetInt("network-vlan-id")
		subnetCIDR, _ := cmd.Flags().GetString("subnet-cidr")
		dhcpStart, _ := cmd.Flags().GetString("dhcp-start")
		dhcpEnd, _ := cmd.Flags().GetString("dhcp-end")
		gatewayFlavorID, _ := cmd.Flags().GetString("gateway-flavor-id")
		cpCount, _ := cmd.Flags().GetInt("control-plane-count")
		cpFlavorID, _ := cmd.Flags().GetString("control-plane-flavor-id")
		workerCount, _ := cmd.Flags().GetInt("worker-count")
		workerFlavorID, _ := cmd.Flags().GetString("worker-flavor-id")
		distribution, _ := cmd.Flags().GetString("distribution")
		kubeVersion, _ := cmd.Flags().GetString("kubernetes-version")

		req := client.CreateOvhClusterRequest{
			Name:                 name,
			CredentialID:         credentialID,
			SSHKeyCredentialID:   sshKeyCredentialID,
			Region:               region,
			NetworkVlanID:        networkVlanID,
			SubnetCIDR:           subnetCIDR,
			DHCPStart:            dhcpStart,
			DHCPEnd:              dhcpEnd,
			GatewayFlavorID:      gatewayFlavorID,
			ControlPlaneCount:    cpCount,
			ControlPlaneFlavorID: cpFlavorID,
			WorkerCount:          workerCount,
			WorkerFlavorID:       workerFlavorID,
			Distribution:         distribution,
		}
		if kubeVersion != "" {
			req.KubernetesVersion = &kubeVersion
		}

		result, err := client.CreateOvhCluster(apiToken, baseURL, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating OVH cluster: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("OVH cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
	},
}

var ovhDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id>",
	Short: "Deprovision an OVH cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.DeprovisionOvhCluster(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deprovisioning cluster: %v\n", err)
			os.Exit(1)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("OVH cluster deprovisioned successfully!"))
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

var ovhWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for an OVH cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.GetOvhWorkerCount(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching worker count: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Worker Count: %d\n", result.WorkerCount)
		fmt.Printf("  Min: %d\n", result.Min)
		fmt.Printf("  Max: %d\n", result.Max)
	},
}

var ovhScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <worker_count>",
	Short: "Scale workers for an OVH cluster",
	Long:  "Scale the number of worker nodes up or down for an OVH cluster.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		workerCount, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid worker count: %v\n", err)
			os.Exit(1)
		}

		result, err := client.ScaleOvhWorkers(apiToken, baseURL, clusterID, workerCount)
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

var ovhK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id>",
	Short: "Get current Kubernetes version for an OVH cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.GetOvhK8sVersion(apiToken, baseURL, clusterID)
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

var ovhUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <target_version>",
	Short: "Upgrade Kubernetes version for an OVH cluster",
	Long:  "Upgrade the Kubernetes (k3s) version on all nodes in an OVH cluster.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		targetVersion := args[1]

		result, err := client.UpgradeOvhK8sVersion(apiToken, baseURL, clusterID, targetVersion)
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

var ovhNodeGroupCmd = &cobra.Command{
	Use:   "node-group",
	Short: "Manage node groups for an OVH cluster",
	Long:  "List, add, scale, upgrade, and delete node groups.",
}

var ovhNodeGroupListCmd = &cobra.Command{
	Use:   "list <cluster_id>",
	Short: "List node groups",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		result, err := client.ListOvhNodeGroups(apiToken, baseURL, clusterID)
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

var ovhNodeGroupAddCmd = &cobra.Command{
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

		result, err := client.AddOvhNodeGroup(apiToken, baseURL, clusterID, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error adding node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' created with %d node(s).\n", result.GroupName, result.Count)
	},
}

var ovhNodeGroupScaleCmd = &cobra.Command{
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

		result, err := client.ScaleOvhNodeGroup(apiToken, baseURL, clusterID, groupName, count)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scaling node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' scaled from %d to %d.\n", result.GroupName, result.PreviousCount, result.NewCount)
	},
}

var ovhNodeGroupUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <group_name> <instance_type>",
	Short: "Upgrade instance type for a node group (cannot be reversed)",
	Args:  cobra.ExactArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		groupName := args[1]
		instanceType := args[2]

		result, err := client.UpdateOvhNodeGroupInstanceType(apiToken, baseURL, clusterID, groupName, instanceType)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error upgrading node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' instance type upgraded. %d node(s) affected.\n", result.GroupName, result.Updated)
	},
}

var ovhNodeGroupDeleteCmd = &cobra.Command{
	Use:   "delete <cluster_id> <group_name>",
	Short: "Delete a node group and all its nodes",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		groupName := args[1]

		result, err := client.DeleteOvhNodeGroup(apiToken, baseURL, clusterID, groupName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' deleted. %d node(s) removed.\n", result.GroupName, result.Deleted)
	},
}

func init() {
	ovhCreateCmd.Flags().String("name", "", "Cluster name (required)")
	ovhCreateCmd.Flags().String("credential-id", "", "OVH API credential ID (required)")
	ovhCreateCmd.Flags().String("ssh-key-credential-id", "", "SSH key credential ID (required)")
	ovhCreateCmd.Flags().String("region", "", "OVH Cloud region (required)")
	ovhCreateCmd.Flags().Int("network-vlan-id", 0, "Network VLAN ID")
	ovhCreateCmd.Flags().String("subnet-cidr", "10.0.1.0/24", "Subnet CIDR")
	ovhCreateCmd.Flags().String("dhcp-start", "10.0.1.100", "DHCP range start")
	ovhCreateCmd.Flags().String("dhcp-end", "10.0.1.200", "DHCP range end")
	ovhCreateCmd.Flags().String("gateway-flavor-id", "b2-7", "Gateway instance flavor")
	ovhCreateCmd.Flags().Int("control-plane-count", 1, "Number of control plane nodes")
	ovhCreateCmd.Flags().String("control-plane-flavor-id", "b2-15", "Control plane instance flavor")
	ovhCreateCmd.Flags().Int("worker-count", 1, "Number of worker nodes")
	ovhCreateCmd.Flags().String("worker-flavor-id", "b2-15", "Worker instance flavor")
	ovhCreateCmd.Flags().String("distribution", "k3s", "Kubernetes distribution")
	ovhCreateCmd.Flags().String("kubernetes-version", "", "Kubernetes version (optional)")

	_ = ovhCreateCmd.MarkFlagRequired("name")
	_ = ovhCreateCmd.MarkFlagRequired("credential-id")
	_ = ovhCreateCmd.MarkFlagRequired("ssh-key-credential-id")
	_ = ovhCreateCmd.MarkFlagRequired("region")

	ovhNodeGroupAddCmd.Flags().String("name", "", "Node group name (required)")
	ovhNodeGroupAddCmd.Flags().String("instance-type", "b2-15", "Instance flavor for nodes")
	ovhNodeGroupAddCmd.Flags().Int("count", 1, "Number of nodes (0-100)")
	_ = ovhNodeGroupAddCmd.MarkFlagRequired("name")

	ovhNodeGroupCmd.AddCommand(ovhNodeGroupListCmd)
	ovhNodeGroupCmd.AddCommand(ovhNodeGroupAddCmd)
	ovhNodeGroupCmd.AddCommand(ovhNodeGroupScaleCmd)
	ovhNodeGroupCmd.AddCommand(ovhNodeGroupUpgradeCmd)
	ovhNodeGroupCmd.AddCommand(ovhNodeGroupDeleteCmd)

	ovhCmd.AddCommand(ovhCreateCmd)
	ovhCmd.AddCommand(ovhDeprovisionCmd)
	ovhCmd.AddCommand(ovhWorkersCmd)
	ovhCmd.AddCommand(ovhScaleCmd)
	ovhCmd.AddCommand(ovhK8sVersionCmd)
	ovhCmd.AddCommand(ovhUpgradeCmd)
	ovhCmd.AddCommand(ovhNodeGroupCmd)

	clusterCmd.AddCommand(ovhCmd)
}

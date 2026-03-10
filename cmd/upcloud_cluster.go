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

var upcloudCmd = &cobra.Command{
	Use:   "upcloud",
	Short: "Manage UpCloud clusters",
	Long:  "Commands to create, deprovision, and scale UpCloud clusters.",
}

var upcloudCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new UpCloud cluster",
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		credentialID, _ := cmd.Flags().GetString("credential-id")
		sshKeyCredentialID, _ := cmd.Flags().GetString("ssh-key-credential-id")
		zone, _ := cmd.Flags().GetString("zone")
		networkIPRange, _ := cmd.Flags().GetString("network-ip-range")
		bastionPlan, _ := cmd.Flags().GetString("bastion-plan")
		cpCount, _ := cmd.Flags().GetInt("control-plane-count")
		cpPlan, _ := cmd.Flags().GetString("control-plane-plan")
		workerCount, _ := cmd.Flags().GetInt("worker-count")
		workerPlan, _ := cmd.Flags().GetString("worker-plan")
		distribution, _ := cmd.Flags().GetString("distribution")
		kubeVersion, _ := cmd.Flags().GetString("kubernetes-version")

		req := client.CreateUpcloudClusterRequest{
			Name:               name,
			CredentialID:       credentialID,
			SSHKeyCredentialID: sshKeyCredentialID,
			Zone:               zone,
			NetworkIPRange:     networkIPRange,
			BastionPlan:        bastionPlan,
			ControlPlaneCount:  cpCount,
			ControlPlanePlan:   cpPlan,
			WorkerCount:        workerCount,
			WorkerPlan:         workerPlan,
			Distribution:       distribution,
		}
		if kubeVersion != "" {
			req.KubernetesVersion = &kubeVersion
		}

		result, err := client.CreateUpcloudCluster(apiToken, baseURL, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating UpCloud cluster: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("UpCloud cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
	},
}

var upcloudDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id>",
	Short: "Deprovision an UpCloud cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.DeprovisionUpcloudCluster(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deprovisioning cluster: %v\n", err)
			os.Exit(1)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("UpCloud cluster deprovision initiated!"))
		} else {
			fmt.Println("Cluster deprovision requested with issues.")
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
			for _, e := range result.Errors {
				fmt.Printf("    - %s\n", e)
			}
		}
	},
}

var upcloudWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for an UpCloud cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.GetUpcloudWorkerCount(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching worker count: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Worker Count: %d\n", result.WorkerCount)
		fmt.Printf("  Min: %d\n", result.Min)
		fmt.Printf("  Max: %d\n", result.Max)
	},
}

var upcloudScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <worker_count>",
	Short: "Scale workers for an UpCloud cluster",
	Long:  "Scale the number of worker nodes up or down for an UpCloud cluster.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		workerCount, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid worker count: %v\n", err)
			os.Exit(1)
		}

		result, err := client.ScaleUpcloudWorkers(apiToken, baseURL, clusterID, workerCount)
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

var upcloudK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id>",
	Short: "Get current Kubernetes version for an UpCloud cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := client.GetUpcloudK8sVersion(apiToken, baseURL, clusterID)
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

var upcloudUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <target_version>",
	Short: "Upgrade Kubernetes version for an UpCloud cluster",
	Long:  "Upgrade the Kubernetes (k3s) version on all nodes in an UpCloud cluster.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		targetVersion := args[1]

		result, err := client.UpgradeUpcloudK8sVersion(apiToken, baseURL, clusterID, targetVersion)
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

var upcloudNodeGroupCmd = &cobra.Command{
	Use:   "node-group",
	Short: "Manage node groups for an UpCloud cluster",
	Long:  "List, add, scale, upgrade, and delete node groups.",
}

var upcloudNodeGroupListCmd = &cobra.Command{
	Use:   "list <cluster_id>",
	Short: "List node groups",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		result, err := client.ListUpcloudNodeGroups(apiToken, baseURL, clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing node groups: %v\n", err)
			os.Exit(1)
		}
		if len(result.NodeGroups) == 0 {
			fmt.Println("No node groups found.")
			return
		}
		for _, ng := range result.NodeGroups {
			fmt.Printf("%-20s  type=%-12s  count=%d  labels=%d  taints=%d\n",
				ng.Name, ng.InstanceType, ng.Count, len(ng.Labels), len(ng.Taints))
		}
	},
}

var upcloudNodeGroupAddCmd = &cobra.Command{
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

		result, err := client.AddUpcloudNodeGroup(apiToken, baseURL, clusterID, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error adding node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' created with %d node(s).\n", result.GroupName, result.Count)
	},
}

var upcloudNodeGroupScaleCmd = &cobra.Command{
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

		result, err := client.ScaleUpcloudNodeGroup(apiToken, baseURL, clusterID, groupName, count)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scaling node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' scaled from %d to %d.\n", result.GroupName, result.PreviousCount, result.NewCount)
	},
}

var upcloudNodeGroupUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <group_name> <plan>",
	Short: "Upgrade server plan for a node group",
	Args:  cobra.ExactArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		groupName := args[1]
		plan := args[2]

		result, err := client.UpdateUpcloudNodeGroupInstanceType(apiToken, baseURL, clusterID, groupName, plan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error upgrading node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' plan upgraded. %d node(s) affected.\n", result.GroupName, result.Updated)
	},
}

var upcloudNodeGroupDeleteCmd = &cobra.Command{
	Use:   "delete <cluster_id> <group_name>",
	Short: "Delete a node group and all its nodes",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		groupName := args[1]

		result, err := client.DeleteUpcloudNodeGroup(apiToken, baseURL, clusterID, groupName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting node group: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Node group '%s' deleted. %d node(s) removed.\n", result.GroupName, result.Deleted)
	},
}

func init() {
	upcloudCreateCmd.Flags().String("name", "", "Cluster name (required)")
	upcloudCreateCmd.Flags().String("credential-id", "", "UpCloud API credential ID (required)")
	upcloudCreateCmd.Flags().String("ssh-key-credential-id", "", "SSH key credential ID (required)")
	upcloudCreateCmd.Flags().String("zone", "", "UpCloud zone (required)")
	upcloudCreateCmd.Flags().String("network-ip-range", "10.0.0.0/16", "Network IP range")
	upcloudCreateCmd.Flags().String("bastion-plan", "1xCPU-2GB", "Bastion plan")
	upcloudCreateCmd.Flags().Int("control-plane-count", 1, "Number of control plane nodes")
	upcloudCreateCmd.Flags().String("control-plane-plan", "2xCPU-4GB", "Control plane plan")
	upcloudCreateCmd.Flags().Int("worker-count", 1, "Number of worker nodes")
	upcloudCreateCmd.Flags().String("worker-plan", "2xCPU-4GB", "Worker plan")
	upcloudCreateCmd.Flags().String("distribution", "k3s", "Kubernetes distribution")
	upcloudCreateCmd.Flags().String("kubernetes-version", "", "Kubernetes version (optional)")

	_ = upcloudCreateCmd.MarkFlagRequired("name")
	_ = upcloudCreateCmd.MarkFlagRequired("credential-id")
	_ = upcloudCreateCmd.MarkFlagRequired("ssh-key-credential-id")
	_ = upcloudCreateCmd.MarkFlagRequired("zone")

	upcloudNodeGroupAddCmd.Flags().String("name", "", "Node group name (required)")
	upcloudNodeGroupAddCmd.Flags().String("instance-type", "2xCPU-4GB", "Server plan for nodes")
	upcloudNodeGroupAddCmd.Flags().Int("count", 1, "Number of nodes (0-100)")
	_ = upcloudNodeGroupAddCmd.MarkFlagRequired("name")

	upcloudNodeGroupCmd.AddCommand(upcloudNodeGroupListCmd)
	upcloudNodeGroupCmd.AddCommand(upcloudNodeGroupAddCmd)
	upcloudNodeGroupCmd.AddCommand(upcloudNodeGroupScaleCmd)
	upcloudNodeGroupCmd.AddCommand(upcloudNodeGroupUpgradeCmd)
	upcloudNodeGroupCmd.AddCommand(upcloudNodeGroupDeleteCmd)

	upcloudCmd.AddCommand(upcloudCreateCmd)
	upcloudCmd.AddCommand(upcloudDeprovisionCmd)
	upcloudCmd.AddCommand(upcloudWorkersCmd)
	upcloudCmd.AddCommand(upcloudScaleCmd)
	upcloudCmd.AddCommand(upcloudK8sVersionCmd)
	upcloudCmd.AddCommand(upcloudUpgradeCmd)
	upcloudCmd.AddCommand(upcloudNodeGroupCmd)

	clusterCmd.AddCommand(upcloudCmd)
}

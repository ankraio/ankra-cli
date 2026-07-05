package cmd

import (
	"fmt"
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
	RunE: func(cmd *cobra.Command, args []string) error {
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
		etcdTopology, _ := cmd.Flags().GetString("etcd-topology")
		etcdNodeCount, _ := cmd.Flags().GetInt("etcd-node-count")
		etcdPlan, _ := cmd.Flags().GetString("etcd-plan")
		externalCloudProvider, includeNetworking, err := resolveCloudProviderNetworking(cmd)
		if err != nil {
			return err
		}
		gitopsCredentialName, _ := cmd.Flags().GetString("gitops-credential-name")
		gitopsRepository, _ := cmd.Flags().GetString("gitops-repository")
		gitopsBranch, _ := cmd.Flags().GetString("gitops-branch")

		req := client.CreateUpcloudClusterRequest{
			Name:                  name,
			CredentialID:          credentialID,
			SSHKeyCredentialID:    sshKeyCredentialID,
			Zone:                  zone,
			NetworkIPRange:        networkIPRange,
			BastionPlan:           bastionPlan,
			ControlPlaneCount:     cpCount,
			ControlPlanePlan:      cpPlan,
			WorkerCount:           workerCount,
			WorkerPlan:            workerPlan,
			Distribution:          distribution,
			EtcdTopology:          etcdTopology,
			EtcdNodeCount:         etcdNodeCount,
			EtcdPlan:              etcdPlan,
			ExternalCloudProvider: externalCloudProvider,
			IncludeNetworking:     includeNetworking,
		}
		if kubeVersion != "" {
			req.KubernetesVersion = &kubeVersion
		}
		if gitopsCredentialName != "" {
			req.GitopsCredentialName = &gitopsCredentialName
		}
		if gitopsRepository != "" {
			req.GitopsRepository = &gitopsRepository
			if gitopsBranch != "" {
				req.GitopsBranch = &gitopsBranch
			}
		}

		result, err := apiClient.CreateUpcloudCluster(req)
		if err != nil {
			return fmt.Errorf("creating UpCloud cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("UpCloud cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
		return nil
	},
}

var upcloudDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id>",
	Short: "Deprovision an UpCloud cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Deprovision UpCloud cluster %q? This deletes all its servers, networks and SSH keys! [y/N]: ", clusterID),
			yes); err != nil {
			return err
		}

		result, err := apiClient.DeprovisionUpcloudCluster(clusterID)
		if err != nil {
			return fmt.Errorf("deprovisioning cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
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
		return nil
	},
}

var upcloudStopCmd = &cobra.Command{
	Use:   "stop <cluster_id>",
	Short: "Stop an UpCloud cluster",
	Long:  "Stop an UpCloud cluster's compute while keeping its configuration so it can be started again later.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.StopUpcloudCluster(clusterID)
		if err != nil {
			return fmt.Errorf("stopping cluster: %w", err)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("UpCloud cluster stop initiated."))
		} else {
			fmt.Println("Cluster stop request submitted.")
		}
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if result.OperationID != nil {
			fmt.Printf("  Operation ID: %s\n", *result.OperationID)
		}
		return nil
	},
}

var upcloudStartCmd = &cobra.Command{
	Use:   "start <cluster_id>",
	Short: "Start a stopped UpCloud cluster",
	Long:  "Start (re-provision) a stopped UpCloud cluster. Use --scope control_plane to bring up only the control plane.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		scope, _ := cmd.Flags().GetString("scope")
		if scope != "all" && scope != "control_plane" {
			return fmt.Errorf("invalid --scope %q: must be 'all' or 'control_plane'", scope)
		}

		result, err := apiClient.StartUpcloudCluster(clusterID, scope)
		if err != nil {
			return fmt.Errorf("starting cluster: %w", err)
		}

		fmt.Println(text.FgGreen.Sprint("UpCloud cluster start initiated."))
		fmt.Printf("  Scope: %s\n", result.Scope)
		if result.MarkedToStartAt != "" {
			fmt.Printf("  Marked to start at: %s\n", result.MarkedToStartAt)
		}
		fmt.Printf("  Created operations: %d\n", result.CreatedOperations)
		return nil
	},
}

var upcloudWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for an UpCloud cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.GetUpcloudWorkerCount(clusterID)
		if err != nil {
			return fmt.Errorf("fetching worker count: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("Worker Count: %d\n", result.WorkerCount)
		fmt.Printf("  Min: %d\n", result.Min)
		fmt.Printf("  Max: %d\n", result.Max)
		return nil
	},
}

var upcloudScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <worker_count>",
	Short: "Scale workers for an UpCloud cluster",
	Long:  "Scale the number of worker nodes up or down for an UpCloud cluster.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		workerCount, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid worker count: %w", err)
		}

		result, err := apiClient.ScaleUpcloudWorkers(clusterID, workerCount)
		if err != nil {
			return fmt.Errorf("scaling workers: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
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
		return nil
	},
}

var upcloudK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id>",
	Short: "Get current Kubernetes version for an UpCloud cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.GetUpcloudK8sVersion(clusterID)
		if err != nil {
			return fmt.Errorf("fetching Kubernetes version: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		version := "not set (using latest stable)"
		if result.CurrentVersion != nil {
			version = *result.CurrentVersion
		}
		fmt.Printf("Kubernetes Version: %s\n", version)
		fmt.Printf("  Distribution: %s\n", result.Distribution)
		return nil
	},
}

var upcloudUpgradeCmd = &cobra.Command{
	Use:        "upgrade <cluster_id> <target_version>",
	Short:      "Upgrade Kubernetes version for an UpCloud cluster",
	Long:       "Upgrade the Kubernetes version on all nodes in an UpCloud cluster (k3s and kubeadm).",
	Deprecated: "use `ankra cluster upgrade <cluster_id> <target_version>` instead; the cloud provider is detected automatically.",
	Args:       cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		targetVersion := args[1]

		result, err := apiClient.UpgradeUpcloudK8sVersion(clusterID, targetVersion, false)
		if err != nil {
			return fmt.Errorf("upgrading Kubernetes version: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		prev := "none"
		if result.PreviousVersion != nil {
			prev = *result.PreviousVersion
		}
		fmt.Printf("Kubernetes version upgrade initiated.\n")
		fmt.Printf("  Previous version: %s\n", prev)
		fmt.Printf("  New version:      %s\n", text.FgGreen.Sprint(result.NewVersion))
		fmt.Printf("  Nodes affected:   %d\n", result.NodesAffected)
		return nil
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
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		result, err := apiClient.ListUpcloudNodeGroups(clusterID)
		if err != nil {
			return fmt.Errorf("listing node groups: %w", err)
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		if len(result.NodeGroups) == 0 {
			fmt.Println("No node groups found.")
			return nil
		}
		for _, ng := range result.NodeGroups {
			fmt.Printf("%-20s  type=%-12s  count=%d  labels=%d  taints=%d\n",
				ng.Name, ng.InstanceType, ng.Count, len(ng.Labels), len(ng.Taints))
		}
		return nil
	},
}

var upcloudNodeGroupAddCmd = &cobra.Command{
	Use:   "add <cluster_id>",
	Short: "Add a node group",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		name, _ := cmd.Flags().GetString("name")
		instanceType, _ := cmd.Flags().GetString("instance-type")
		count, _ := cmd.Flags().GetInt("count")

		req := client.AddNodeGroupRequest{
			Name:         name,
			InstanceType: instanceType,
			Count:        count,
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, err := apiClient.AddUpcloudNodeGroup(requestContext, clusterID, req, wait)
		if err != nil {
			return asyncWriteError("adding node group", wait, err)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newAsyncSubmittedResult("Node group add")); err != nil {
				return err
			} else if handled {
				return nil
			}
			printAsyncWriteSubmitted("Node group add")
			return nil
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' created with %d node(s).\n", result.GroupName, result.Count)
		return nil
	},
}

var upcloudNodeGroupScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <group_name> <count>",
	Short: "Scale a node group",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		count, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("invalid count: %w", err)
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, err := apiClient.ScaleUpcloudNodeGroup(requestContext, clusterID, groupName, count, wait)
		if err != nil {
			return asyncWriteError("scaling node group", wait, err)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newAsyncSubmittedResult("Node group scale")); err != nil {
				return err
			} else if handled {
				return nil
			}
			printAsyncWriteSubmitted("Node group scale")
			return nil
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' scaled from %d to %d.\n", result.GroupName, result.PreviousCount, result.NewCount)
		return nil
	},
}

var upcloudNodeGroupUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <group_name> <plan>",
	Short: "Upgrade server plan for a node group",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		plan := args[2]

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, err := apiClient.UpdateUpcloudNodeGroupInstanceType(requestContext, clusterID, groupName, plan, wait)
		if err != nil {
			return asyncWriteError("upgrading node group", wait, err)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newAsyncSubmittedResult("Node group plan update")); err != nil {
				return err
			} else if handled {
				return nil
			}
			printAsyncWriteSubmitted("Node group plan update")
			return nil
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' plan upgraded. %d node(s) affected.\n", result.GroupName, result.Updated)
		return nil
	},
}

var upcloudNodeGroupDeleteCmd = &cobra.Command{
	Use:   "delete <cluster_id> <group_name>",
	Short: "Delete a node group and all its nodes",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete node group %q from cluster %q? This deletes all its nodes! [y/N]: ", groupName, clusterID),
			yes); err != nil {
			return err
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, err := apiClient.DeleteUpcloudNodeGroup(requestContext, clusterID, groupName, wait)
		if err != nil {
			return asyncWriteError("deleting node group", wait, err)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newAsyncSubmittedResult("Node group delete")); err != nil {
				return err
			} else if handled {
				return nil
			}
			printAsyncWriteSubmitted("Node group delete")
			return nil
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' deleted. %d node(s) removed.\n", result.GroupName, result.Deleted)
		return nil
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
	upcloudCreateCmd.Flags().String("distribution", "k3s", "Kubernetes distribution: k3s or kubeadm")
	upcloudCreateCmd.Flags().String("kubernetes-version", "", "Kubernetes version (optional; see `ankra cluster k3s-versions` or `ankra cluster kubeadm-versions`)")
	upcloudCreateCmd.Flags().String("etcd-topology", "stacked", "etcd topology for kubeadm clusters: stacked (on control planes) or external (dedicated VMs)")
	upcloudCreateCmd.Flags().Int("etcd-node-count", 3, "Number of dedicated etcd nodes when --etcd-topology=external (3 or 5)")
	upcloudCreateCmd.Flags().String("etcd-plan", "2xCPU-4GB", "Plan for dedicated etcd nodes when --etcd-topology=external")
	upcloudCreateCmd.Flags().Bool("external-cloud-provider", true, "Install the UpCloud CCM and CSI (cloud-provider=external) for LoadBalancers and persistent volumes (default on; pass --external-cloud-provider=false to skip, which also disables --include-networking)")
	upcloudCreateCmd.Flags().Bool("include-networking", true, "Install Traefik + cert-manager for ingress (default on; pass --include-networking=false to skip). Requires --external-cloud-provider (the ingress LoadBalancer is provisioned by the cloud controller manager)")
	upcloudCreateCmd.Flags().String("gitops-credential-name", "", "GitOps GitHub credential name; when set with --gitops-repository, the generated upcloud-cloud-provider stack is committed to Git (optional)")
	upcloudCreateCmd.Flags().String("gitops-repository", "", "GitOps repository (e.g. org/repo) to commit the generated stack to (optional)")
	upcloudCreateCmd.Flags().String("gitops-branch", "master", "GitOps branch to commit to")

	_ = upcloudCreateCmd.MarkFlagRequired("name")
	_ = upcloudCreateCmd.MarkFlagRequired("credential-id")
	_ = upcloudCreateCmd.MarkFlagRequired("ssh-key-credential-id")
	_ = upcloudCreateCmd.MarkFlagRequired("zone")

	upcloudStartCmd.Flags().String("scope", "all", "Provisioning scope: 'all' or 'control_plane'")

	upcloudDeprovisionCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	upcloudNodeGroupDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	upcloudNodeGroupAddCmd.Flags().String("name", "", "Node group name (required)")
	upcloudNodeGroupAddCmd.Flags().String("instance-type", "2xCPU-4GB", "Server plan for nodes")
	upcloudNodeGroupAddCmd.Flags().Int("count", 1, "Number of nodes (0-100)")
	_ = upcloudNodeGroupAddCmd.MarkFlagRequired("name")
	registerAsyncWriteFlags(upcloudNodeGroupAddCmd)
	registerAsyncWriteFlags(upcloudNodeGroupScaleCmd)
	registerAsyncWriteFlags(upcloudNodeGroupUpgradeCmd)
	registerAsyncWriteFlags(upcloudNodeGroupDeleteCmd)

	registerStructuredOutputFlags(
		upcloudCreateCmd,
		upcloudDeprovisionCmd,
		upcloudWorkersCmd,
		upcloudScaleCmd,
		upcloudK8sVersionCmd,
		upcloudUpgradeCmd,
		upcloudNodeGroupListCmd,
		upcloudNodeGroupAddCmd,
		upcloudNodeGroupScaleCmd,
		upcloudNodeGroupUpgradeCmd,
		upcloudNodeGroupDeleteCmd,
	)

	markDeprecatedForGenericVerb("ankra cluster scale <cluster_id> <worker_count>", upcloudScaleCmd)
	markDeprecatedForGenericVerb("ankra cluster deprovision <cluster_id> [--force]", upcloudDeprovisionCmd)
	markDeprecatedForGenericVerb(
		"ankra cluster node-group <list|add|scale|upgrade|delete>",
		upcloudNodeGroupListCmd, upcloudNodeGroupAddCmd, upcloudNodeGroupScaleCmd, upcloudNodeGroupUpgradeCmd, upcloudNodeGroupDeleteCmd,
	)

	upcloudNodeGroupCmd.AddCommand(upcloudNodeGroupListCmd)
	upcloudNodeGroupCmd.AddCommand(upcloudNodeGroupAddCmd)
	upcloudNodeGroupCmd.AddCommand(upcloudNodeGroupScaleCmd)
	upcloudNodeGroupCmd.AddCommand(upcloudNodeGroupUpgradeCmd)
	upcloudNodeGroupCmd.AddCommand(upcloudNodeGroupDeleteCmd)

	upcloudCmd.AddCommand(upcloudCreateCmd)
	upcloudCmd.AddCommand(upcloudDeprovisionCmd)
	upcloudCmd.AddCommand(upcloudStopCmd)
	upcloudCmd.AddCommand(upcloudStartCmd)
	upcloudCmd.AddCommand(upcloudWorkersCmd)
	upcloudCmd.AddCommand(upcloudScaleCmd)
	upcloudCmd.AddCommand(upcloudK8sVersionCmd)
	upcloudCmd.AddCommand(upcloudUpgradeCmd)
	upcloudCmd.AddCommand(upcloudNodeGroupCmd)

	clusterCmd.AddCommand(upcloudCmd)
}

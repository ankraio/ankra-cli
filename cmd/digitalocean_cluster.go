package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var digitaloceanCmd = &cobra.Command{
	Use:     "digitalocean",
	Aliases: []string{"do"},
	Short:   "Manage DigitalOcean clusters",
	Long:    "Commands to create, deprovision, and scale DigitalOcean clusters.",
}

var digitaloceanCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new DigitalOcean cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		credentialID, _ := cmd.Flags().GetString("credential-id")
		sshKeyCredentialID, _ := cmd.Flags().GetString("ssh-key-credential-id")
		region, _ := cmd.Flags().GetString("region")
		networkIPRange, _ := cmd.Flags().GetString("network-ip-range")
		bastionSize, _ := cmd.Flags().GetString("bastion-size")
		cpCount, _ := cmd.Flags().GetInt("control-plane-count")
		cpSize, _ := cmd.Flags().GetString("control-plane-size")
		workerCount, _ := cmd.Flags().GetInt("worker-count")
		workerSize, _ := cmd.Flags().GetString("worker-size")
		distribution, _ := cmd.Flags().GetString("distribution")
		kubeVersion, _ := cmd.Flags().GetString("kubernetes-version")
		etcdTopology, _ := cmd.Flags().GetString("etcd-topology")
		etcdNodeCount, _ := cmd.Flags().GetInt("etcd-node-count")
		etcdSize, _ := cmd.Flags().GetString("etcd-size")
		externalCloudProvider, includeNetworking, err := resolveCloudProviderNetworking(cmd)
		if err != nil {
			return err
		}
		gitopsCredentialName, _ := cmd.Flags().GetString("gitops-credential-name")
		gitopsRepository, _ := cmd.Flags().GetString("gitops-repository")
		gitopsBranch, _ := cmd.Flags().GetString("gitops-branch")

		req := client.CreateDigitaloceanClusterRequest{
			Name:                  name,
			CredentialID:          credentialID,
			SSHKeyCredentialID:    sshKeyCredentialID,
			Region:                  region,
			NetworkIPRange:        networkIPRange,
			BastionSize:           bastionSize,
			ControlPlaneCount:     cpCount,
			ControlPlaneSize:      cpSize,
			WorkerCount:           workerCount,
			WorkerSize:            workerSize,
			Distribution:          distribution,
			EtcdTopology:          etcdTopology,
			EtcdNodeCount:         etcdNodeCount,
			EtcdSize:              etcdSize,
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

		result, err := apiClient.CreateDigitaloceanCluster(req)
		if err != nil {
			return fmt.Errorf("creating DigitalOcean cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("DigitalOcean cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
		return nil
	},
}

var digitaloceanDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id>",
	Short: "Deprovision an DigitalOcean cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Deprovision DigitalOcean cluster %q? This deletes all its servers, networks and SSH keys! [y/N]: ", clusterID),
			yes); err != nil {
			return err
		}

		result, err := apiClient.DeprovisionDigitaloceanCluster(clusterID)
		if err != nil {
			return fmt.Errorf("deprovisioning cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("DigitalOcean cluster deprovision initiated!"))
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

var digitaloceanStopCmd = &cobra.Command{
	Use:   "stop <cluster_id>",
	Short: "Stop an DigitalOcean cluster",
	Long:  "Stop an DigitalOcean cluster's compute while keeping its configuration so it can be started again later.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.StopDigitaloceanCluster(clusterID)
		if err != nil {
			return fmt.Errorf("stopping cluster: %w", err)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("DigitalOcean cluster stop initiated."))
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

var digitaloceanStartCmd = &cobra.Command{
	Use:   "start <cluster_id>",
	Short: "Start a stopped DigitalOcean cluster",
	Long:  "Start (re-provision) a stopped DigitalOcean cluster. Use --scope control_plane to bring up only the control plane.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		scope, _ := cmd.Flags().GetString("scope")
		if scope != "all" && scope != "control_plane" {
			return fmt.Errorf("invalid --scope %q: must be 'all' or 'control_plane'", scope)
		}

		result, err := apiClient.StartDigitaloceanCluster(clusterID, scope)
		if err != nil {
			return fmt.Errorf("starting cluster: %w", err)
		}

		fmt.Println(text.FgGreen.Sprint("DigitalOcean cluster start initiated."))
		fmt.Printf("  Scope: %s\n", result.Scope)
		if result.MarkedToStartAt != "" {
			fmt.Printf("  Marked to start at: %s\n", result.MarkedToStartAt)
		}
		fmt.Printf("  Created operations: %d\n", result.CreatedOperations)
		return nil
	},
}

var digitaloceanWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for an DigitalOcean cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.GetDigitaloceanWorkerCount(clusterID)
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

var digitaloceanScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <worker_count>",
	Short: "Scale workers for an DigitalOcean cluster",
	Long:  "Scale the number of worker nodes up or down for an DigitalOcean cluster.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		workerCount, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid worker count: %w", err)
		}

		result, err := apiClient.ScaleDigitaloceanWorkers(clusterID, workerCount)
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

var digitaloceanK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id>",
	Short: "Get current Kubernetes version for an DigitalOcean cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.GetDigitaloceanK8sVersion(clusterID)
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

var digitaloceanUpgradeCmd = &cobra.Command{
	Use:        "upgrade <cluster_id> <target_version>",
	Short:      "Upgrade Kubernetes version for an DigitalOcean cluster",
	Long:       "Upgrade the Kubernetes version on all nodes in an DigitalOcean cluster. This deprecated form always runs the safe non-forced rollout; use `ankra cluster upgrade` for --force (PodDisruptionBudget override) and operation progress tracking.",
	Deprecated: "use `ankra cluster upgrade <cluster_id> <target_version>` instead; the cloud provider is detected automatically.",
	Args:       cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		targetVersion := args[1]

		result, err := apiClient.UpgradeDigitaloceanK8sVersion(clusterID, targetVersion, false)
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

var digitaloceanNodeGroupCmd = &cobra.Command{
	Use:   "node-group",
	Short: "Manage node groups for an DigitalOcean cluster",
	Long:  "List, add, scale, upgrade, and delete node groups.",
}

var digitaloceanNodeGroupListCmd = &cobra.Command{
	Use:   "list <cluster_id>",
	Short: "List node groups",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		result, err := apiClient.ListDigitaloceanNodeGroups(clusterID)
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

var digitaloceanNodeGroupAddCmd = &cobra.Command{
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

		result, submitted, err := apiClient.AddDigitaloceanNodeGroup(requestContext, clusterID, req, wait)
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

var digitaloceanNodeGroupScaleCmd = &cobra.Command{
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

		result, submitted, err := apiClient.ScaleDigitaloceanNodeGroup(requestContext, clusterID, groupName, count, wait)
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

var digitaloceanNodeGroupUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <group_name> <size>",
	Short: "Upgrade server size for a node group",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		size := args[2]

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, err := apiClient.UpdateDigitaloceanNodeGroupInstanceType(requestContext, clusterID, groupName, size, wait)
		if err != nil {
			return asyncWriteError("upgrading node group", wait, err)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newAsyncSubmittedResult("Node group size update")); err != nil {
				return err
			} else if handled {
				return nil
			}
			printAsyncWriteSubmitted("Node group size update")
			return nil
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' size upgraded. %d node(s) affected.\n", result.GroupName, result.Updated)
		return nil
	},
}

var digitaloceanNodeGroupDeleteCmd = &cobra.Command{
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

		result, submitted, err := apiClient.DeleteDigitaloceanNodeGroup(requestContext, clusterID, groupName, wait)
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

var digitaloceanRegionsCmd = &cobra.Command{
	Use:   "regions",
	Short: "List DigitalOcean regions available to a credential",
	Long:  "List the DigitalOcean regions the supplied credential can deploy in.",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")

		regions, err := apiClient.ListDigitaloceanRegions(credentialID)
		if err != nil {
			return fmt.Errorf("listing regions: %w", err)
		}
		if len(regions) == 0 {
			fmt.Println("No regions available for this credential.")
			return nil
		}
		fmt.Printf("Regions available to credential %s:\n", credentialID)
		for _, region := range regions {
			available := "yes"
			if !region.Available {
				available = "no"
			}
			fmt.Printf("  %-8s %s (available: %s)\n", region.Slug, region.Name, available)
		}
		return nil
	},
}

var digitaloceanSizesCmd = &cobra.Command{
	Use:   "sizes",
	Short: "List DigitalOcean droplet sizes",
	Long:  "List DigitalOcean droplet sizes. Pass --region to filter by region availability.",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		region, _ := cmd.Flags().GetString("region")
		availableOnly, _ := cmd.Flags().GetBool("available-only")

		sizes, err := apiClient.ListDigitaloceanSizes(credentialID, region)
		if err != nil {
			return fmt.Errorf("listing sizes: %w", err)
		}
		if len(sizes) == 0 {
			fmt.Println("No sizes found.")
			return nil
		}
		fmt.Printf("Droplet sizes for credential %s", credentialID)
		if region != "" {
			fmt.Printf(" in region %s", region)
		}
		fmt.Println(":")
		for _, size := range sizes {
			if availableOnly && !size.Available {
				continue
			}
			available := "yes"
			if !size.Available {
				available = "no"
			}
			fmt.Printf("  %-20s %d vCPU, %d GB RAM, %d GB disk ($%.2f/mo, available: %s)\n",
				size.Slug, size.Vcpus, size.Memory/1024, size.Disk, size.PriceMonthly, available)
		}
		return nil
	},
}

func init() {
	digitaloceanCreateCmd.Flags().String("name", "", "Cluster name (required)")
	digitaloceanCreateCmd.Flags().String("credential-id", "", "DigitalOcean API credential ID (required)")
	digitaloceanCreateCmd.Flags().String("ssh-key-credential-id", "", "SSH key credential ID (required)")
	digitaloceanCreateCmd.Flags().String("region", "", "DigitalOcean region (required)")
	digitaloceanCreateCmd.Flags().String("network-ip-range", "10.0.0.0/16", "Network IP range")
	digitaloceanCreateCmd.Flags().String("bastion-size", "s-1vcpu-1gb", "Bastion droplet size")
	digitaloceanCreateCmd.Flags().Int("control-plane-count", 1, "Number of control plane nodes")
	digitaloceanCreateCmd.Flags().String("control-plane-size", "s-2vcpu-4gb", "Control plane droplet size")
	digitaloceanCreateCmd.Flags().Int("worker-count", 1, "Number of worker nodes")
	digitaloceanCreateCmd.Flags().String("worker-size", "s-2vcpu-4gb", "Worker droplet size")
	digitaloceanCreateCmd.Flags().String("distribution", "k3s", "Kubernetes distribution: k3s or kubeadm")
	digitaloceanCreateCmd.Flags().String("kubernetes-version", "", "Kubernetes version (optional; see `ankra cluster k3s-versions` or `ankra cluster kubeadm-versions`)")
	digitaloceanCreateCmd.Flags().String("etcd-topology", "stacked", "etcd topology for kubeadm clusters: stacked (on control plane) or external (dedicated VMs)")
	digitaloceanCreateCmd.Flags().Int("etcd-node-count", 3, "Number of dedicated etcd nodes when --etcd-topology=external (3 or 5)")
	digitaloceanCreateCmd.Flags().String("etcd-size", "s-2vcpu-4gb", "Droplet size for dedicated etcd nodes when --etcd-topology=external")
	digitaloceanCreateCmd.Flags().Bool("external-cloud-provider", true, "Install the DigitalOcean CCM and CSI (cloud-provider=external) for LoadBalancers and persistent volumes (default on; pass --external-cloud-provider=false to skip, which also disables --include-networking)")
	digitaloceanCreateCmd.Flags().Bool("include-networking", true, "Install Traefik + cert-manager for ingress (default on; pass --include-networking=false to skip). Requires --external-cloud-provider (the ingress LoadBalancer is provisioned by the cloud controller manager)")
	digitaloceanCreateCmd.Flags().String("gitops-credential-name", "", "GitOps GitHub credential name; when set with --gitops-repository, the generated digitalocean-cloud-provider stack is committed to Git (optional)")
	digitaloceanCreateCmd.Flags().String("gitops-repository", "", "GitOps repository (e.g. org/repo) to commit the generated stack to (optional)")
	digitaloceanCreateCmd.Flags().String("gitops-branch", "master", "GitOps branch to commit to")

	_ = digitaloceanCreateCmd.MarkFlagRequired("name")
	_ = digitaloceanCreateCmd.MarkFlagRequired("credential-id")
	_ = digitaloceanCreateCmd.MarkFlagRequired("ssh-key-credential-id")
	_ = digitaloceanCreateCmd.MarkFlagRequired("region")

	digitaloceanStartCmd.Flags().String("scope", "all", "Provisioning scope: 'all' or 'control_plane'")

	digitaloceanDeprovisionCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	digitaloceanNodeGroupDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	digitaloceanNodeGroupAddCmd.Flags().String("name", "", "Node group name (required)")
	digitaloceanNodeGroupAddCmd.Flags().String("instance-type", "s-2vcpu-4gb", "Droplet size for nodes")
	digitaloceanNodeGroupAddCmd.Flags().Int("count", 1, "Number of nodes (0-100)")
	_ = digitaloceanNodeGroupAddCmd.MarkFlagRequired("name")
	registerAsyncWriteFlags(digitaloceanNodeGroupAddCmd)
	registerAsyncWriteFlags(digitaloceanNodeGroupScaleCmd)
	registerAsyncWriteFlags(digitaloceanNodeGroupUpgradeCmd)
	registerAsyncWriteFlags(digitaloceanNodeGroupDeleteCmd)

	registerStructuredOutputFlags(
		digitaloceanCreateCmd,
		digitaloceanDeprovisionCmd,
		digitaloceanWorkersCmd,
		digitaloceanScaleCmd,
		digitaloceanK8sVersionCmd,
		digitaloceanUpgradeCmd,
		digitaloceanNodeGroupListCmd,
		digitaloceanNodeGroupAddCmd,
		digitaloceanNodeGroupScaleCmd,
		digitaloceanNodeGroupUpgradeCmd,
		digitaloceanNodeGroupDeleteCmd,
	)

	markDeprecatedForGenericVerb("ankra cluster scale <cluster_id> <worker_count>", digitaloceanScaleCmd)
	markDeprecatedForGenericVerb("ankra cluster deprovision <cluster_id> [--force]", digitaloceanDeprovisionCmd)
	markDeprecatedForGenericVerb(
		"ankra cluster node-group <list|add|scale|upgrade|delete>",
		digitaloceanNodeGroupListCmd, digitaloceanNodeGroupAddCmd, digitaloceanNodeGroupScaleCmd, digitaloceanNodeGroupUpgradeCmd, digitaloceanNodeGroupDeleteCmd,
	)

	digitaloceanNodeGroupCmd.AddCommand(digitaloceanNodeGroupListCmd)
	digitaloceanNodeGroupCmd.AddCommand(digitaloceanNodeGroupAddCmd)
	digitaloceanNodeGroupCmd.AddCommand(digitaloceanNodeGroupScaleCmd)
	digitaloceanNodeGroupCmd.AddCommand(digitaloceanNodeGroupUpgradeCmd)
	digitaloceanNodeGroupCmd.AddCommand(digitaloceanNodeGroupDeleteCmd)

	digitaloceanRegionsCmd.Flags().String("credential-id", "", "DigitalOcean API credential ID (required)")
	_ = digitaloceanRegionsCmd.MarkFlagRequired("credential-id")
	digitaloceanSizesCmd.Flags().String("credential-id", "", "DigitalOcean API credential ID (required)")
	digitaloceanSizesCmd.Flags().String("region", "", "Filter by region slug")
	digitaloceanSizesCmd.Flags().Bool("available-only", false, "Show only sizes available in the selected region")
	_ = digitaloceanSizesCmd.MarkFlagRequired("credential-id")

	digitaloceanCmd.AddCommand(digitaloceanCreateCmd)
	digitaloceanCmd.AddCommand(digitaloceanRegionsCmd)
	digitaloceanCmd.AddCommand(digitaloceanSizesCmd)
	digitaloceanCmd.AddCommand(digitaloceanDeprovisionCmd)
	digitaloceanCmd.AddCommand(digitaloceanStopCmd)
	digitaloceanCmd.AddCommand(digitaloceanStartCmd)
	digitaloceanCmd.AddCommand(digitaloceanWorkersCmd)
	digitaloceanCmd.AddCommand(digitaloceanScaleCmd)
	digitaloceanCmd.AddCommand(digitaloceanK8sVersionCmd)
	digitaloceanCmd.AddCommand(digitaloceanUpgradeCmd)
	digitaloceanCmd.AddCommand(digitaloceanNodeGroupCmd)

	clusterCmd.AddCommand(digitaloceanCmd)
}

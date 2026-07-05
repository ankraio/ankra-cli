package cmd

import (
	"errors"
	"fmt"
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
	RunE: func(cmd *cobra.Command, args []string) error {
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
		etcdTopology, _ := cmd.Flags().GetString("etcd-topology")
		etcdNodeCount, _ := cmd.Flags().GetInt("etcd-node-count")
		etcdFlavorID, _ := cmd.Flags().GetString("etcd-flavor-id")
		externalCloudProvider, includeNetworking, err := resolveCloudProviderNetworking(cmd)
		if err != nil {
			return err
		}
		gitopsCredentialName, _ := cmd.Flags().GetString("gitops-credential-name")
		gitopsRepository, _ := cmd.Flags().GetString("gitops-repository")
		gitopsBranch, _ := cmd.Flags().GetString("gitops-branch")

		req := client.CreateOvhClusterRequest{
			Name:                  name,
			CredentialID:          credentialID,
			SSHKeyCredentialID:    sshKeyCredentialID,
			Region:                region,
			NetworkVlanID:         networkVlanID,
			SubnetCIDR:            subnetCIDR,
			DHCPStart:             dhcpStart,
			DHCPEnd:               dhcpEnd,
			GatewayFlavorID:       gatewayFlavorID,
			ControlPlaneCount:     cpCount,
			ControlPlaneFlavorID:  cpFlavorID,
			WorkerCount:           workerCount,
			WorkerFlavorID:        workerFlavorID,
			Distribution:          distribution,
			EtcdTopology:          etcdTopology,
			EtcdNodeCount:         etcdNodeCount,
			EtcdFlavorID:          etcdFlavorID,
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

		result, err := apiClient.CreateOvhCluster(req)
		if err != nil {
			return fmt.Errorf("creating OVH cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Printf("OVH cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
		return nil
	},
}

var ovhDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id>",
	Short: "Deprovision an OVH cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Deprovision OVH cluster %q? This deletes all its servers, networks and SSH keys! [y/N]: ", clusterID),
			yes); err != nil {
			return err
		}

		result, err := apiClient.DeprovisionOvhCluster(clusterID)
		if err != nil {
			return fmt.Errorf("deprovisioning cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
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
		return nil
	},
}

var ovhWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for an OVH cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.GetOvhWorkerCount(clusterID)
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

var ovhScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <worker_count>",
	Short: "Scale workers for an OVH cluster",
	Long:  "Scale the number of worker nodes up or down for an OVH cluster.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		workerCount, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid worker count: %w", err)
		}

		result, err := apiClient.ScaleOvhWorkers(clusterID, workerCount)
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

var ovhK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id>",
	Short: "Get current Kubernetes version for an OVH cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.GetOvhK8sVersion(clusterID)
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

var ovhUpgradeCmd = &cobra.Command{
	Use:        "upgrade <cluster_id> <target_version>",
	Short:      "Upgrade Kubernetes version for an OVH cluster",
	Long:       "Upgrade the Kubernetes version on all nodes in an OVH cluster. This deprecated form always runs the safe non-forced rollout; use `ankra cluster upgrade` for --force (PodDisruptionBudget override) and operation progress tracking.",
	Deprecated: "use `ankra cluster upgrade <cluster_id> <target_version>` instead; the cloud provider is detected automatically.",
	Args:       cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		targetVersion := args[1]

		result, err := apiClient.UpgradeOvhK8sVersion(clusterID, targetVersion, false)
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

var ovhRegionsCmd = &cobra.Command{
	Use:   "regions",
	Short: "List OVH regions available to a credential",
	Long:  "List the OVH Cloud regions the supplied credential's project can deploy in. Only these regions are valid for cluster creation.",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")

		result, err := apiClient.ListOvhRegions(credentialID)
		if err != nil {
			return fmt.Errorf("listing regions: %w", err)
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		if len(result.Regions) == 0 {
			fmt.Println("No regions available for this credential.")
			return nil
		}
		fmt.Printf("Regions available to credential %s:\n", credentialID)
		for _, region := range result.Regions {
			fmt.Printf("  %s\n", region)
		}
		return nil
	},
}

var ovhStopCmd = &cobra.Command{
	Use:   "stop <cluster_id>",
	Short: "Stop an OVH cluster",
	Long:  "Stop an OVH cluster's compute while keeping its configuration so it can be started again later.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.StopOvhCluster(clusterID)
		if err != nil {
			return fmt.Errorf("stopping cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("OVH cluster stop initiated."))
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

var ovhStartCmd = &cobra.Command{
	Use:   "start <cluster_id>",
	Short: "Start a stopped OVH cluster",
	Long:  "Start (re-provision) a stopped OVH cluster. Use --scope control_plane to bring up only the control plane.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		scope, _ := cmd.Flags().GetString("scope")
		if scope != "all" && scope != "control_plane" {
			return fmt.Errorf("invalid --scope %q: must be 'all' or 'control_plane'", scope)
		}

		result, err := apiClient.StartOvhCluster(clusterID, scope)
		if err != nil {
			return fmt.Errorf("starting cluster: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Println(text.FgGreen.Sprint("OVH cluster start initiated."))
		fmt.Printf("  Scope: %s\n", result.Scope)
		if result.MarkedToStartAt != "" {
			fmt.Printf("  Marked to start at: %s\n", result.MarkedToStartAt)
		}
		fmt.Printf("  Created operations: %d\n", result.CreatedOperations)
		return nil
	},
}

var ovhAccessInfoCmd = &cobra.Command{
	Use:   "access-info <cluster_id>",
	Short: "Show SSH access details for an OVH cluster",
	Long:  "Show the gateway (bastion) and control plane IPs plus ready-to-use SSH jump and Kubernetes API port-forward commands.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.GetOvhAccessInfo(clusterID)
		if err != nil {
			return fmt.Errorf("fetching access info: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		gatewayIP := ""
		if result.BastionIP != nil {
			gatewayIP = *result.BastionIP
		}
		controlPlaneIP := ""
		if result.ControlPlaneIP != nil {
			controlPlaneIP = *result.ControlPlaneIP
		}

		if result.ClusterName != nil && *result.ClusterName != "" {
			fmt.Printf("Cluster: %s\n", *result.ClusterName)
		}
		if gatewayIP == "" {
			fmt.Printf("Gateway IP: -\n")
		} else {
			fmt.Printf("Gateway IP: %s\n", gatewayIP)
		}
		if len(result.ControlPlaneIPs) > 0 {
			fmt.Printf("Control plane IPs: %s\n", strings.Join(result.ControlPlaneIPs, ", "))
		} else {
			fmt.Printf("Control plane IPs: -\n")
		}

		if gatewayIP != "" && controlPlaneIP != "" {
			fmt.Println("\nSSH to a control plane node via the gateway:")
			fmt.Printf("  ssh -J ubuntu@%s ubuntu@%s\n", gatewayIP, controlPlaneIP)
			fmt.Println("\nPort-forward the Kubernetes API:")
			fmt.Printf("  ssh -L 6443:%s:6443 -N ubuntu@%s\n", controlPlaneIP, gatewayIP)
		}
		return nil
	},
}

var ovhSSHKeysCmd = &cobra.Command{
	Use:     "ssh-keys",
	Aliases: []string{"ssh-key"},
	Short:   "Manage SSH keys attached to an OVH cluster",
	Long:    "Get and set the SSH key credentials authorised to access an OVH cluster's nodes.",
}

var ovhSSHKeysGetCmd = &cobra.Command{
	Use:   "get <cluster_id>",
	Short: "Show SSH keys attached to an OVH cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, err := apiClient.GetOvhClusterSSHKeys(clusterID)
		if err != nil {
			return fmt.Errorf("fetching SSH keys: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		if len(result.SSHKeyCredentialIDs) == 0 {
			fmt.Println("Attached SSH keys: none")
		} else {
			fmt.Println("Attached SSH key credential IDs:")
			for _, id := range result.SSHKeyCredentialIDs {
				fmt.Printf("  %s\n", id)
			}
		}

		if len(result.AvailableSSHKeys) > 0 {
			fmt.Println("\nAvailable SSH key credentials:")
			for _, key := range result.AvailableSSHKeys {
				fmt.Printf("  %-38s  %s\n", key.CredentialID, key.Name)
			}
		}
		return nil
	},
}

var ovhSSHKeysSetCmd = &cobra.Command{
	Use:   "set <cluster_id>",
	Short: "Set the SSH keys attached to an OVH cluster",
	Long:  "Replace the SSH key credentials attached to an OVH cluster. Changes take effect on the next reconciliation.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		sshKeyCredentialIDs, _ := cmd.Flags().GetStringSlice("ssh-key-credential-ids")
		if len(sshKeyCredentialIDs) == 0 {
			return errors.New("at least one --ssh-key-credential-ids value is required")
		}

		result, err := apiClient.UpdateOvhClusterSSHKeys(clusterID, sshKeyCredentialIDs)
		if err != nil {
			return fmt.Errorf("updating SSH keys: %w", err)
		}

		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}

		fmt.Println(text.FgGreen.Sprint("SSH keys updated. Changes apply on next reconciliation."))
		fmt.Println("Attached SSH key credential IDs:")
		for _, id := range result.SSHKeyCredentialIDs {
			fmt.Printf("  %s\n", id)
		}
		return nil
	},
}

var ovhNodeGroupCmd = &cobra.Command{
	Use:   "node-group",
	Short: "Manage node groups for an OVH cluster",
	Long:  "List, add, scale, upgrade, label, taint, and delete node groups.",
}

var ovhNodeGroupLabelsCmd = &cobra.Command{
	Use:   "labels <cluster_id> <group_name>",
	Short: "Set labels on all nodes in a node group",
	Long:  "Replace the labels on every node in the group. Pass --labels as a comma-separated list of key=value pairs, or --clear to remove all labels.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		clear, _ := cmd.Flags().GetBool("clear")
		labelsChanged := cmd.Flags().Changed("labels")
		labelsFlag, _ := cmd.Flags().GetString("labels")

		if clear && labelsChanged {
			return withExitCode(exitUsage, errors.New("pass either --labels k=v or --clear, not both"))
		}
		if !clear && !labelsChanged {
			return withExitCode(exitUsage, errors.New("provide --labels k=v to set labels, or pass --clear to remove all labels"))
		}

		labels, err := parseLabelsFlag(labelsFlag)
		if err != nil {
			return fmt.Errorf("invalid --labels: %w", err)
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, err := apiClient.UpdateOvhNodeGroupLabels(requestContext, clusterID, groupName, labels, wait)
		if err != nil {
			return asyncWriteError("updating node group labels", wait, err)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newAsyncSubmittedResult("Node group labels update")); err != nil {
				return err
			} else if handled {
				return nil
			}
			printAsyncWriteSubmitted("Node group labels update")
			return nil
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' labels updated. %d node(s) affected.\n", result.GroupName, result.Updated)
		return nil
	},
}

var ovhNodeGroupTaintsCmd = &cobra.Command{
	Use:   "taints <cluster_id> <group_name>",
	Short: "Set taints on all nodes in a node group",
	Long:  "Replace the taints on every node in the group. Pass --taints as a comma-separated list of key=value:Effect (value optional, effect defaults to NoSchedule), or --clear to remove all taints.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		clear, _ := cmd.Flags().GetBool("clear")
		taintsChanged := cmd.Flags().Changed("taints")
		taintsFlag, _ := cmd.Flags().GetString("taints")

		if clear && taintsChanged {
			return withExitCode(exitUsage, errors.New("pass either --taints k=v:Effect or --clear, not both"))
		}
		if !clear && !taintsChanged {
			return withExitCode(exitUsage, errors.New("provide --taints k=v:Effect to set taints, or pass --clear to remove all taints"))
		}

		taints, err := parseTaintsFlag(taintsFlag)
		if err != nil {
			return fmt.Errorf("invalid --taints: %w", err)
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, err := apiClient.UpdateOvhNodeGroupTaints(requestContext, clusterID, groupName, taints, wait)
		if err != nil {
			return asyncWriteError("updating node group taints", wait, err)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newAsyncSubmittedResult("Node group taints update")); err != nil {
				return err
			} else if handled {
				return nil
			}
			printAsyncWriteSubmitted("Node group taints update")
			return nil
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' taints updated. %d node(s) affected.\n", result.GroupName, result.Updated)
		return nil
	},
}

var ovhNodeGroupListCmd = &cobra.Command{
	Use:   "list <cluster_id>",
	Short: "List node groups",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		result, err := apiClient.ListOvhNodeGroups(clusterID)
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
			fmt.Printf("%-20s  type=%-8s  count=%d  labels=%d  taints=%d\n",
				ng.Name, ng.InstanceType, ng.Count, len(ng.Labels), len(ng.Taints))
		}
		return nil
	},
}

var ovhNodeGroupAddCmd = &cobra.Command{
	Use:   "add <cluster_id>",
	Short: "Add a node group",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		name, _ := cmd.Flags().GetString("name")
		instanceType, _ := cmd.Flags().GetString("instance-type")
		count, _ := cmd.Flags().GetInt("count")
		labelsFlag, _ := cmd.Flags().GetString("labels")
		taintsFlag, _ := cmd.Flags().GetString("taints")

		labels, err := parseLabelsFlag(labelsFlag)
		if err != nil {
			return fmt.Errorf("invalid --labels: %w", err)
		}
		taints, err := parseTaintsFlag(taintsFlag)
		if err != nil {
			return fmt.Errorf("invalid --taints: %w", err)
		}

		req := client.AddNodeGroupRequest{
			Name:         name,
			InstanceType: instanceType,
			Count:        count,
			Labels:       labels,
			Taints:       taints,
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, err := apiClient.AddOvhNodeGroup(requestContext, clusterID, req, wait)
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

var ovhNodeGroupScaleCmd = &cobra.Command{
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

		result, submitted, err := apiClient.ScaleOvhNodeGroup(requestContext, clusterID, groupName, count, wait)
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

var ovhNodeGroupUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <group_name> <instance_type>",
	Short: "Upgrade instance type for a node group (cannot be reversed)",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		instanceType := args[2]

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, err := apiClient.UpdateOvhNodeGroupInstanceType(requestContext, clusterID, groupName, instanceType, wait)
		if err != nil {
			return asyncWriteError("upgrading node group", wait, err)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newAsyncSubmittedResult("Node group instance-type update")); err != nil {
				return err
			} else if handled {
				return nil
			}
			printAsyncWriteSubmitted("Node group instance-type update")
			return nil
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' instance type upgraded. %d node(s) affected.\n", result.GroupName, result.Updated)
		return nil
	},
}

var ovhNodeGroupDeleteCmd = &cobra.Command{
	Use:   "delete <cluster_id> <group_name>",
	Short: "Delete a node group and all its nodes",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		yes, _ := cmd.Flags().GetBool("yes")

		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete node group %q and all its nodes? This action is irreversible! [y/N]: ", groupName),
			yes); err != nil {
			return err
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, err := apiClient.DeleteOvhNodeGroup(requestContext, clusterID, groupName, wait)
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

func parseLabelsFlag(raw string) (map[string]string, error) {
	labels := map[string]string{}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return labels, nil
	}
	for _, pair := range strings.Split(trimmed, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, found := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, fmt.Errorf("label %q must be in key=value form", pair)
		}
		labels[key] = strings.TrimSpace(value)
	}
	return labels, nil
}

func parseTaintsFlag(raw string) ([]client.NodeTaint, error) {
	taints := []client.NodeTaint{}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return taints, nil
	}
	for _, item := range strings.Split(trimmed, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		keyValue, effect, hasEffect := strings.Cut(item, ":")
		if !hasEffect || strings.TrimSpace(effect) == "" {
			effect = "NoSchedule"
		}
		key, value, _ := strings.Cut(keyValue, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("taint %q must specify a key", item)
		}
		taints = append(taints, client.NodeTaint{
			Key:    key,
			Value:  strings.TrimSpace(value),
			Effect: strings.TrimSpace(effect),
		})
	}
	return taints, nil
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
	ovhCreateCmd.Flags().String("distribution", "k3s", "Kubernetes distribution: k3s or kubeadm")
	ovhCreateCmd.Flags().String("kubernetes-version", "", "Kubernetes version (optional; see `ankra cluster k3s-versions` or `ankra cluster kubeadm-versions`)")
	ovhCreateCmd.Flags().String("etcd-topology", "stacked", "etcd topology for kubeadm clusters: stacked (on control planes) or external (dedicated VMs)")
	ovhCreateCmd.Flags().Int("etcd-node-count", 3, "Number of dedicated etcd nodes when --etcd-topology=external (3 or 5)")
	ovhCreateCmd.Flags().String("etcd-flavor-id", "b2-15", "Instance flavor for dedicated etcd nodes when --etcd-topology=external")
	ovhCreateCmd.Flags().Bool("external-cloud-provider", true, "Install the OpenStack CCM and Cinder CSI (cloud-provider=external) for LoadBalancers and persistent volumes (default on; pass --external-cloud-provider=false to skip, which also disables --include-networking)")
	ovhCreateCmd.Flags().Bool("include-networking", true, "Install Traefik + cert-manager for ingress (default on; pass --include-networking=false to skip). Requires --external-cloud-provider (the ingress LoadBalancer is provisioned by the cloud controller manager)")
	ovhCreateCmd.Flags().String("gitops-credential-name", "", "GitOps GitHub credential name; when set with --gitops-repository, the generated ovh-cloud stack is committed to Git (optional)")
	ovhCreateCmd.Flags().String("gitops-repository", "", "GitOps repository (e.g. org/repo) to commit the generated stack to (optional)")
	ovhCreateCmd.Flags().String("gitops-branch", "master", "GitOps branch to commit to")

	_ = ovhCreateCmd.MarkFlagRequired("name")
	_ = ovhCreateCmd.MarkFlagRequired("credential-id")
	_ = ovhCreateCmd.MarkFlagRequired("ssh-key-credential-id")
	_ = ovhCreateCmd.MarkFlagRequired("region")

	ovhStartCmd.Flags().String("scope", "all", "Provisioning scope: 'all' or 'control_plane'")

	ovhSSHKeysSetCmd.Flags().StringSlice("ssh-key-credential-ids", nil, "SSH key credential IDs to attach (comma-separated or repeated)")
	_ = ovhSSHKeysSetCmd.MarkFlagRequired("ssh-key-credential-ids")
	ovhSSHKeysCmd.AddCommand(ovhSSHKeysGetCmd)
	ovhSSHKeysCmd.AddCommand(ovhSSHKeysSetCmd)

	ovhNodeGroupAddCmd.Flags().String("name", "", "Node group name (required)")
	ovhNodeGroupAddCmd.Flags().String("instance-type", "b2-15", "Instance flavor for nodes")
	ovhNodeGroupAddCmd.Flags().Int("count", 1, "Number of nodes (0-100)")
	ovhNodeGroupAddCmd.Flags().String("labels", "", "Comma-separated key=value labels to apply to the node group")
	ovhNodeGroupAddCmd.Flags().String("taints", "", "Comma-separated key=value:Effect taints to apply to the node group")
	_ = ovhNodeGroupAddCmd.MarkFlagRequired("name")
	registerAsyncWriteFlags(ovhNodeGroupAddCmd)
	registerAsyncWriteFlags(ovhNodeGroupScaleCmd)
	registerAsyncWriteFlags(ovhNodeGroupUpgradeCmd)
	registerAsyncWriteFlags(ovhNodeGroupLabelsCmd)
	registerAsyncWriteFlags(ovhNodeGroupTaintsCmd)
	registerAsyncWriteFlags(ovhNodeGroupDeleteCmd)

	ovhNodeGroupLabelsCmd.Flags().String("labels", "", "Comma-separated key=value pairs to set on the group")
	ovhNodeGroupLabelsCmd.Flags().Bool("clear", false, "Remove all labels from the node group")
	ovhNodeGroupTaintsCmd.Flags().String("taints", "", "Comma-separated key=value:Effect taints to set on the group")
	ovhNodeGroupTaintsCmd.Flags().Bool("clear", false, "Remove all taints from the node group")

	ovhDeprovisionCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	ovhNodeGroupDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	ovhRegionsCmd.Flags().String("credential-id", "", "OVH API credential ID (required)")
	_ = ovhRegionsCmd.MarkFlagRequired("credential-id")

	registerStructuredOutputFlags(
		ovhCreateCmd,
		ovhDeprovisionCmd,
		ovhStopCmd,
		ovhStartCmd,
		ovhWorkersCmd,
		ovhScaleCmd,
		ovhK8sVersionCmd,
		ovhUpgradeCmd,
		ovhRegionsCmd,
		ovhAccessInfoCmd,
		ovhSSHKeysGetCmd,
		ovhSSHKeysSetCmd,
		ovhNodeGroupListCmd,
		ovhNodeGroupAddCmd,
		ovhNodeGroupScaleCmd,
		ovhNodeGroupUpgradeCmd,
		ovhNodeGroupLabelsCmd,
		ovhNodeGroupTaintsCmd,
		ovhNodeGroupDeleteCmd,
	)

	markDeprecatedForGenericVerb("ankra cluster scale <cluster_id> <worker_count>", ovhScaleCmd)
	markDeprecatedForGenericVerb("ankra cluster deprovision <cluster_id> [--force]", ovhDeprovisionCmd)
	markDeprecatedForGenericVerb(
		"ankra cluster node-group <list|add|scale|upgrade|delete>",
		ovhNodeGroupListCmd, ovhNodeGroupAddCmd, ovhNodeGroupScaleCmd, ovhNodeGroupUpgradeCmd, ovhNodeGroupDeleteCmd,
	)
	markDeprecatedForGenericVerb("ankra cluster ssh-keys get <cluster_id>", ovhSSHKeysGetCmd)
	markDeprecatedForGenericVerb("ankra cluster ssh-keys set <cluster_id> --ssh-key-credential-ids ...", ovhSSHKeysSetCmd)

	ovhNodeGroupCmd.AddCommand(ovhNodeGroupListCmd)
	ovhNodeGroupCmd.AddCommand(ovhNodeGroupAddCmd)
	ovhNodeGroupCmd.AddCommand(ovhNodeGroupScaleCmd)
	ovhNodeGroupCmd.AddCommand(ovhNodeGroupUpgradeCmd)
	ovhNodeGroupCmd.AddCommand(ovhNodeGroupLabelsCmd)
	ovhNodeGroupCmd.AddCommand(ovhNodeGroupTaintsCmd)
	ovhNodeGroupCmd.AddCommand(ovhNodeGroupDeleteCmd)

	ovhCmd.AddCommand(ovhCreateCmd)
	ovhCmd.AddCommand(ovhDeprovisionCmd)
	ovhCmd.AddCommand(ovhStopCmd)
	ovhCmd.AddCommand(ovhStartCmd)
	ovhCmd.AddCommand(ovhWorkersCmd)
	ovhCmd.AddCommand(ovhScaleCmd)
	ovhCmd.AddCommand(ovhK8sVersionCmd)
	ovhCmd.AddCommand(ovhUpgradeCmd)
	ovhCmd.AddCommand(ovhRegionsCmd)
	ovhCmd.AddCommand(ovhAccessInfoCmd)
	ovhCmd.AddCommand(ovhSSHKeysCmd)
	ovhCmd.AddCommand(ovhNodeGroupCmd)

	clusterCmd.AddCommand(ovhCmd)
}

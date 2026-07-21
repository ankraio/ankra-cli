package cmd

import (
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var morpheusCmd = &cobra.Command{
	Use:   "morpheus",
	Short: "Manage HPE Morpheus clusters",
	Long:  "Commands to create, stop, start, and inspect HPE Morpheus clusters.",
}

var morpheusCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new HPE Morpheus cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		description, _ := cmd.Flags().GetString("description")
		credentialID, _ := cmd.Flags().GetString("credential-id")
		sshKeyCredentialID, _ := cmd.Flags().GetString("ssh-key-credential-id")
		groupID, _ := cmd.Flags().GetInt64("group-id")
		cloudID, _ := cmd.Flags().GetInt64("cloud-id")
		networkID, _ := cmd.Flags().GetInt64("network-id")
		layoutID, _ := cmd.Flags().GetInt64("layout-id")
		virtualImageID, _ := cmd.Flags().GetInt64("virtual-image-id")
		bastionPlanID, _ := cmd.Flags().GetInt64("bastion-plan-id")
		controlPlaneCount, _ := cmd.Flags().GetInt("control-plane-count")
		controlPlanePlanID, _ := cmd.Flags().GetInt64("control-plane-plan-id")
		workerCount, _ := cmd.Flags().GetInt("worker-count")
		workerPlanID, _ := cmd.Flags().GetInt64("worker-plan-id")
		distribution, _ := cmd.Flags().GetString("distribution")
		kubernetesVersion, _ := cmd.Flags().GetString("kubernetes-version")
		etcdTopology, _ := cmd.Flags().GetString("etcd-topology")
		etcdNodeCount, _ := cmd.Flags().GetInt("etcd-node-count")
		etcdPlanID, _ := cmd.Flags().GetInt64("etcd-plan-id")
		cni, _ := cmd.Flags().GetString("cni")
		includeNetworking, _ := cmd.Flags().GetBool("include-networking")

		request := client.CreateMorpheusClusterRequest{
			Name:               name,
			CredentialID:       credentialID,
			SSHKeyCredentialID: sshKeyCredentialID,
			GroupID:            groupID,
			CloudID:            cloudID,
			NetworkID:          networkID,
			LayoutID:           layoutID,
			BastionPlanID:      bastionPlanID,
			ControlPlaneCount:  controlPlaneCount,
			ControlPlanePlanID: controlPlanePlanID,
			WorkerCount:        workerCount,
			WorkerPlanID:       workerPlanID,
			Distribution:       distribution,
			EtcdTopology:       etcdTopology,
			EtcdNodeCount:      etcdNodeCount,
			CNI:                cni,
			IncludeNetworking:  includeNetworking,
		}
		if description != "" {
			request.Description = &description
		}
		if kubernetesVersion != "" {
			request.KubernetesVersion = &kubernetesVersion
		}
		if virtualImageID > 0 {
			request.VirtualImageID = &virtualImageID
		}
		if etcdPlanID > 0 {
			request.EtcdPlanID = &etcdPlanID
		}

		result, createError := apiClient.CreateMorpheusCluster(request)
		if createError != nil {
			return fmt.Errorf("creating HPE Morpheus cluster: %w", createError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Printf("HPE Morpheus cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
		return nil
	},
}

var morpheusStopCmd = &cobra.Command{
	Use:   "stop <cluster_id>",
	Short: "Stop an HPE Morpheus cluster",
	Long:  "Stop an HPE Morpheus cluster's instances while keeping its configuration so it can be started again later.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, stopError := apiClient.StopMorpheusCluster(clusterID)
		if stopError != nil {
			return fmt.Errorf("stopping cluster: %w", stopError)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("HPE Morpheus cluster stop initiated."))
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

var morpheusStartCmd = &cobra.Command{
	Use:   "start <cluster_id>",
	Short: "Start a stopped HPE Morpheus cluster",
	Long:  "Start (re-provision) a stopped HPE Morpheus cluster. Use --scope control_plane to bring up only the control plane.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		scope, _ := cmd.Flags().GetString("scope")
		if scope != "all" && scope != "control_plane" {
			return fmt.Errorf("invalid --scope %q: must be 'all' or 'control_plane'", scope)
		}

		result, startError := apiClient.StartMorpheusCluster(clusterID, scope)
		if startError != nil {
			return fmt.Errorf("starting cluster: %w", startError)
		}

		fmt.Println(text.FgGreen.Sprint("HPE Morpheus cluster start initiated."))
		fmt.Printf("  Scope: %s\n", result.Scope)
		if result.MarkedToStartAt != "" {
			fmt.Printf("  Marked to start at: %s\n", result.MarkedToStartAt)
		}
		fmt.Printf("  Created operations: %d\n", result.CreatedOperations)
		return nil
	},
}

var morpheusWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for an HPE Morpheus cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, fetchError := apiClient.GetMorpheusWorkerCount(clusterID)
		if fetchError != nil {
			return fmt.Errorf("fetching worker count: %w", fetchError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Printf("Worker Count: %d\n", result.WorkerCount)
		fmt.Printf("  Min: %d\n", result.Min)
		fmt.Printf("  Max: %d\n", result.Max)
		return nil
	},
}

var morpheusK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id>",
	Short: "Get current Kubernetes version for an HPE Morpheus cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, fetchError := apiClient.GetMorpheusK8sVersion(clusterID)
		if fetchError != nil {
			return fmt.Errorf("fetching Kubernetes version: %w", fetchError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
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

var morpheusGroupsCmd = &cobra.Command{
	Use:   "groups",
	Short: "List HPE Morpheus groups available to a credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")

		groups, listError := apiClient.ListMorpheusGroups(credentialID)
		if listError != nil {
			return fmt.Errorf("listing groups: %w", listError)
		}
		if len(groups) == 0 {
			fmt.Println("No groups available for this credential.")
			return nil
		}
		fmt.Printf("Groups available to credential %s:\n", credentialID)
		for _, group := range groups {
			location := ""
			if group.Location != nil && *group.Location != "" {
				location = " (" + *group.Location + ")"
			}
			fmt.Printf("  %-8d %s%s\n", group.ID, group.Name, location)
		}
		return nil
	},
}

var morpheusCloudsCmd = &cobra.Command{
	Use:   "clouds",
	Short: "List HPE Morpheus clouds available to a credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")

		clouds, listError := apiClient.ListMorpheusClouds(credentialID)
		if listError != nil {
			return fmt.Errorf("listing clouds: %w", listError)
		}
		if len(clouds) == 0 {
			fmt.Println("No clouds available for this credential.")
			return nil
		}
		fmt.Printf("Clouds available to credential %s:\n", credentialID)
		for _, cloud := range clouds {
			cloudType := ""
			if cloud.CloudType != nil && *cloud.CloudType != "" {
				cloudType = " (" + *cloud.CloudType + ")"
			}
			fmt.Printf("  %-8d %s%s\n", cloud.ID, cloud.Name, cloudType)
		}
		return nil
	},
}

var morpheusPlansCmd = &cobra.Command{
	Use:   "plans",
	Short: "List HPE Morpheus service plans available to a credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")

		plans, listError := apiClient.ListMorpheusPlans(credentialID)
		if listError != nil {
			return fmt.Errorf("listing plans: %w", listError)
		}
		if len(plans) == 0 {
			fmt.Println("No plans available for this credential.")
			return nil
		}
		fmt.Printf("Service plans available to credential %s:\n", credentialID)
		for _, plan := range plans {
			details := []string{}
			if plan.MaxCores != nil {
				details = append(details, fmt.Sprintf("%d cores", *plan.MaxCores))
			}
			if plan.MaxMemoryBytes != nil {
				details = append(details, fmt.Sprintf("%.1f GB RAM", float64(*plan.MaxMemoryBytes)/(1024*1024*1024)))
			}
			detail := ""
			if len(details) > 0 {
				detail = " (" + strings.Join(details, ", ") + ")"
			}
			fmt.Printf("  %-8d %s%s\n", plan.ID, plan.Name, detail)
		}
		return nil
	},
}

var morpheusLayoutsCmd = &cobra.Command{
	Use:   "layouts",
	Short: "List HPE Morpheus instance-type layouts available to a credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")

		layouts, listError := apiClient.ListMorpheusLayouts(credentialID)
		if listError != nil {
			return fmt.Errorf("listing layouts: %w", listError)
		}
		if len(layouts) == 0 {
			fmt.Println("No layouts available for this credential.")
			return nil
		}
		fmt.Printf("Instance-type layouts available to credential %s:\n", credentialID)
		for _, layout := range layouts {
			provisionType := ""
			if layout.ProvisionType != nil && *layout.ProvisionType != "" {
				provisionType = " (" + *layout.ProvisionType + ")"
			}
			fmt.Printf("  %-8d %-40s instance-type=%s%s\n",
				layout.ID, layout.Name, layout.InstanceTypeName, provisionType)
		}
		return nil
	},
}

var morpheusNetworksCmd = &cobra.Command{
	Use:   "networks",
	Short: "List HPE Morpheus networks available to a credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")

		networks, listError := apiClient.ListMorpheusNetworks(credentialID)
		if listError != nil {
			return fmt.Errorf("listing networks: %w", listError)
		}
		if len(networks) == 0 {
			fmt.Println("No networks available for this credential.")
			return nil
		}
		fmt.Printf("Networks available to credential %s:\n", credentialID)
		for _, network := range networks {
			cidr := ""
			if network.CIDR != nil && *network.CIDR != "" {
				cidr = " " + *network.CIDR
			}
			cloud := ""
			if network.CloudID != nil {
				cloud = fmt.Sprintf(" cloud=%d", *network.CloudID)
			}
			fmt.Printf("  %-8d %s%s%s\n", network.ID, network.Name, cidr, cloud)
		}
		return nil
	},
}

func init() {
	morpheusCreateCmd.Flags().String("name", "", "Cluster name (required)")
	morpheusCreateCmd.Flags().String("description", "", "Cluster description")
	morpheusCreateCmd.Flags().String("credential-id", "", "HPE Morpheus API credential ID (required)")
	morpheusCreateCmd.Flags().String("ssh-key-credential-id", "", "SSH key credential ID (required)")
	morpheusCreateCmd.Flags().Int64("group-id", 0, "Morpheus group ID (required; see `ankra cluster morpheus groups`)")
	morpheusCreateCmd.Flags().Int64("cloud-id", 0, "Morpheus cloud ID (required; see `ankra cluster morpheus clouds`)")
	morpheusCreateCmd.Flags().Int64("network-id", 0, "Morpheus network ID (required; see `ankra cluster morpheus networks`)")
	morpheusCreateCmd.Flags().Int64("layout-id", 0, "Morpheus instance-type layout ID (required; see `ankra cluster morpheus layouts`)")
	morpheusCreateCmd.Flags().Int64("virtual-image-id", 0, "Morpheus virtual image ID (optional)")
	morpheusCreateCmd.Flags().Int64("bastion-plan-id", 0, "Service plan ID for the bastion (required; see `ankra cluster morpheus plans`)")
	morpheusCreateCmd.Flags().Int("control-plane-count", 1, "Number of control plane nodes")
	morpheusCreateCmd.Flags().Int64("control-plane-plan-id", 0, "Service plan ID for control plane nodes (required)")
	morpheusCreateCmd.Flags().Int("worker-count", 1, "Number of worker nodes")
	morpheusCreateCmd.Flags().Int64("worker-plan-id", 0, "Service plan ID for worker nodes (required)")
	morpheusCreateCmd.Flags().String("distribution", "k3s", "Kubernetes distribution: k3s or kubeadm")
	morpheusCreateCmd.Flags().String("kubernetes-version", "", "Kubernetes version (optional; see `ankra cluster k3s-versions` or `ankra cluster kubeadm-versions`)")
	morpheusCreateCmd.Flags().String("etcd-topology", "stacked", "etcd topology for kubeadm clusters: stacked (on control plane) or external (dedicated instances)")
	morpheusCreateCmd.Flags().Int("etcd-node-count", 3, "Number of dedicated etcd nodes when --etcd-topology=external (3 or 5)")
	morpheusCreateCmd.Flags().Int64("etcd-plan-id", 0, "Service plan ID for dedicated etcd nodes when --etcd-topology=external (optional)")
	morpheusCreateCmd.Flags().String("cni", "", "CNI plugin (optional; the platform default is used when omitted)")
	morpheusCreateCmd.Flags().Bool("include-networking", true, "Install Traefik + cert-manager as Ankra-managed stacks for ingress (exposed via the built-in k3s service load balancer; default on)")

	_ = morpheusCreateCmd.MarkFlagRequired("name")
	_ = morpheusCreateCmd.MarkFlagRequired("credential-id")
	_ = morpheusCreateCmd.MarkFlagRequired("ssh-key-credential-id")
	_ = morpheusCreateCmd.MarkFlagRequired("group-id")
	_ = morpheusCreateCmd.MarkFlagRequired("cloud-id")
	_ = morpheusCreateCmd.MarkFlagRequired("network-id")
	_ = morpheusCreateCmd.MarkFlagRequired("layout-id")
	_ = morpheusCreateCmd.MarkFlagRequired("bastion-plan-id")
	_ = morpheusCreateCmd.MarkFlagRequired("control-plane-plan-id")
	_ = morpheusCreateCmd.MarkFlagRequired("worker-plan-id")

	morpheusStartCmd.Flags().String("scope", "all", "Provisioning scope: 'all' or 'control_plane'")

	for _, catalogCmd := range []*cobra.Command{morpheusGroupsCmd, morpheusCloudsCmd, morpheusPlansCmd, morpheusLayoutsCmd, morpheusNetworksCmd} {
		catalogCmd.Flags().String("credential-id", "", "HPE Morpheus API credential ID (required)")
		_ = catalogCmd.MarkFlagRequired("credential-id")
	}

	registerStructuredOutputFlags(
		morpheusCreateCmd,
		morpheusWorkersCmd,
		morpheusK8sVersionCmd,
	)

	morpheusCmd.AddCommand(morpheusCreateCmd)
	morpheusCmd.AddCommand(morpheusGroupsCmd)
	morpheusCmd.AddCommand(morpheusCloudsCmd)
	morpheusCmd.AddCommand(morpheusPlansCmd)
	morpheusCmd.AddCommand(morpheusLayoutsCmd)
	morpheusCmd.AddCommand(morpheusNetworksCmd)
	morpheusCmd.AddCommand(morpheusStopCmd)
	morpheusCmd.AddCommand(morpheusStartCmd)
	morpheusCmd.AddCommand(morpheusWorkersCmd)
	morpheusCmd.AddCommand(morpheusK8sVersionCmd)

	clusterCmd.AddCommand(morpheusCmd)
}

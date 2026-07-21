package cmd

import (
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var proxmoxCmd = &cobra.Command{
	Use:     "proxmox",
	Aliases: []string{"pve"},
	Short:   "Manage Proxmox VE clusters",
	Long:    "Commands to create, stop, start, and inspect Proxmox VE clusters.",
}

var proxmoxCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new Proxmox VE cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		description, _ := cmd.Flags().GetString("description")
		credentialID, _ := cmd.Flags().GetString("credential-id")
		sshKeyCredentialID, _ := cmd.Flags().GetString("ssh-key-credential-id")
		node, _ := cmd.Flags().GetString("node")
		placementNodes, _ := cmd.Flags().GetStringSlice("placement-nodes")
		bridge, _ := cmd.Flags().GetString("bridge")
		storage, _ := cmd.Flags().GetString("storage")
		template, _ := cmd.Flags().GetString("template")
		bastionInstanceType, _ := cmd.Flags().GetString("bastion-instance-type")
		controlPlaneCount, _ := cmd.Flags().GetInt("control-plane-count")
		controlPlaneInstanceType, _ := cmd.Flags().GetString("control-plane-instance-type")
		workerCount, _ := cmd.Flags().GetInt("worker-count")
		workerInstanceType, _ := cmd.Flags().GetString("worker-instance-type")
		distribution, _ := cmd.Flags().GetString("distribution")
		kubernetesVersion, _ := cmd.Flags().GetString("kubernetes-version")
		etcdTopology, _ := cmd.Flags().GetString("etcd-topology")
		etcdNodeCount, _ := cmd.Flags().GetInt("etcd-node-count")
		etcdInstanceType, _ := cmd.Flags().GetString("etcd-instance-type")
		cni, _ := cmd.Flags().GetString("cni")
		includeNetworking, _ := cmd.Flags().GetBool("include-networking")

		request := client.CreateProxmoxClusterRequest{
			Name:                     name,
			CredentialID:             credentialID,
			SSHKeyCredentialID:       sshKeyCredentialID,
			Node:                     node,
			PlacementNodes:           placementNodes,
			Bridge:                   bridge,
			Storage:                  storage,
			Template:                 template,
			BastionInstanceType:      bastionInstanceType,
			ControlPlaneCount:        controlPlaneCount,
			ControlPlaneInstanceType: controlPlaneInstanceType,
			WorkerCount:              workerCount,
			WorkerInstanceType:       workerInstanceType,
			Distribution:             distribution,
			EtcdTopology:             etcdTopology,
			EtcdNodeCount:            etcdNodeCount,
			EtcdInstanceType:         etcdInstanceType,
			CNI:                      cni,
			IncludeNetworking:        includeNetworking,
		}
		if description != "" {
			request.Description = &description
		}
		if kubernetesVersion != "" {
			request.KubernetesVersion = &kubernetesVersion
		}

		result, createError := apiClient.CreateProxmoxCluster(request)
		if createError != nil {
			return fmt.Errorf("creating Proxmox VE cluster: %w", createError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Printf("Proxmox VE cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
		return nil
	},
}

var proxmoxStopCmd = &cobra.Command{
	Use:   "stop <cluster_id>",
	Short: "Stop a Proxmox VE cluster",
	Long:  "Stop a Proxmox VE cluster's virtual machines while keeping its configuration so it can be started again later.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, stopError := apiClient.StopProxmoxCluster(clusterID)
		if stopError != nil {
			return fmt.Errorf("stopping cluster: %w", stopError)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("Proxmox VE cluster stop initiated."))
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

var proxmoxStartCmd = &cobra.Command{
	Use:   "start <cluster_id>",
	Short: "Start a stopped Proxmox VE cluster",
	Long:  "Start (re-provision) a stopped Proxmox VE cluster. Use --scope control_plane to bring up only the control plane.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		scope, _ := cmd.Flags().GetString("scope")
		if scope != "all" && scope != "control_plane" {
			return fmt.Errorf("invalid --scope %q: must be 'all' or 'control_plane'", scope)
		}

		result, startError := apiClient.StartProxmoxCluster(clusterID, scope)
		if startError != nil {
			return fmt.Errorf("starting cluster: %w", startError)
		}

		fmt.Println(text.FgGreen.Sprint("Proxmox VE cluster start initiated."))
		fmt.Printf("  Scope: %s\n", result.Scope)
		if result.MarkedToStartAt != "" {
			fmt.Printf("  Marked to start at: %s\n", result.MarkedToStartAt)
		}
		fmt.Printf("  Created operations: %d\n", result.CreatedOperations)
		return nil
	},
}

var proxmoxWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for a Proxmox VE cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, fetchError := apiClient.GetProxmoxWorkerCount(clusterID)
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

var proxmoxK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id>",
	Short: "Get current Kubernetes version for a Proxmox VE cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]

		result, fetchError := apiClient.GetProxmoxK8sVersion(clusterID)
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

var proxmoxHostsCmd = &cobra.Command{
	Use:   "hosts",
	Short: "List Proxmox VE host nodes available to a credential",
	Long:  "List the Proxmox VE host nodes the supplied credential can deploy virtual machines on.",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")

		hosts, listError := apiClient.ListProxmoxNodes(credentialID)
		if listError != nil {
			return fmt.Errorf("listing host nodes: %w", listError)
		}
		if len(hosts) == 0 {
			fmt.Println("No host nodes available for this credential.")
			return nil
		}
		fmt.Printf("Host nodes available to credential %s:\n", credentialID)
		for _, host := range hosts {
			fmt.Printf("  %-20s %s (%d CPUs, %.1f GB RAM)\n",
				host.Node, host.Status, host.CPUCount, float64(host.MemoryBytes)/(1024*1024*1024))
		}
		return nil
	},
}

var proxmoxStoragesCmd = &cobra.Command{
	Use:   "storages",
	Short: "List Proxmox VE storages on a host node",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		node, _ := cmd.Flags().GetString("node")

		storages, listError := apiClient.ListProxmoxStorages(credentialID, node)
		if listError != nil {
			return fmt.Errorf("listing storages: %w", listError)
		}
		if len(storages) == 0 {
			fmt.Println("No storages found.")
			return nil
		}
		fmt.Printf("Storages on node %s:\n", node)
		for _, storage := range storages {
			active := "active"
			if !storage.Active {
				active = "inactive"
			}
			fmt.Printf("  %-20s %-10s %s, %.1f/%.1f GB free (content: %s)\n",
				storage.Storage, storage.Type, active,
				float64(storage.AvailableBytes)/(1024*1024*1024),
				float64(storage.TotalBytes)/(1024*1024*1024),
				strings.Join(storage.Content, ","))
		}
		return nil
	},
}

var proxmoxBridgesCmd = &cobra.Command{
	Use:   "bridges",
	Short: "List Proxmox VE network bridges on a host node",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		node, _ := cmd.Flags().GetString("node")

		bridges, listError := apiClient.ListProxmoxBridges(credentialID, node)
		if listError != nil {
			return fmt.Errorf("listing bridges: %w", listError)
		}
		if len(bridges) == 0 {
			fmt.Println("No bridges found.")
			return nil
		}
		fmt.Printf("Bridges on node %s:\n", node)
		for _, bridge := range bridges {
			active := "active"
			if !bridge.Active {
				active = "inactive"
			}
			cidr := ""
			if bridge.CIDR != nil {
				cidr = " " + *bridge.CIDR
			}
			fmt.Printf("  %-12s %s%s\n", bridge.Name, active, cidr)
		}
		return nil
	},
}

var proxmoxTemplatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "List Proxmox VE VM templates on a host node",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		node, _ := cmd.Flags().GetString("node")

		templates, listError := apiClient.ListProxmoxTemplates(credentialID, node)
		if listError != nil {
			return fmt.Errorf("listing templates: %w", listError)
		}
		if len(templates) == 0 {
			fmt.Println("No templates found.")
			return nil
		}
		fmt.Printf("Templates on node %s:\n", node)
		for _, template := range templates {
			fmt.Printf("  %-8d %-40s node=%s\n", template.VMID, template.Name, template.Node)
		}
		return nil
	},
}

var proxmoxSizesCmd = &cobra.Command{
	Use:   "sizes",
	Short: "List Proxmox VE instance-size presets",
	Long:  "List the instance-size presets (px-small, px-medium, ...) accepted by the Proxmox VE instance-type flags.",
	RunE: func(cmd *cobra.Command, args []string) error {
		sizes, listError := apiClient.ListProxmoxSizes()
		if listError != nil {
			return fmt.Errorf("listing sizes: %w", listError)
		}
		if len(sizes) == 0 {
			fmt.Println("No sizes found.")
			return nil
		}
		for _, size := range sizes {
			available := "yes"
			if !size.Available {
				available = "no"
			}
			fmt.Printf("  %-12s %d vCPU, %d MB RAM, %d GB disk (available: %s)\n",
				size.Slug, size.VCPUs, size.MemoryMB, size.DiskGB, available)
		}
		return nil
	},
}

func init() {
	proxmoxCreateCmd.Flags().String("name", "", "Cluster name (required)")
	proxmoxCreateCmd.Flags().String("description", "", "Cluster description")
	proxmoxCreateCmd.Flags().String("credential-id", "", "Proxmox VE API credential ID (required)")
	proxmoxCreateCmd.Flags().String("ssh-key-credential-id", "", "SSH key credential ID (required)")
	proxmoxCreateCmd.Flags().String("node", "", "Proxmox VE host node to deploy on (required; see `ankra cluster proxmox hosts`)")
	proxmoxCreateCmd.Flags().StringSlice("placement-nodes", nil, "Two or more host nodes for host-spread placement (comma-separated or repeated; optional)")
	proxmoxCreateCmd.Flags().String("bridge", "", "Network bridge for cluster VMs (required; see `ankra cluster proxmox bridges`)")
	proxmoxCreateCmd.Flags().String("storage", "", "Storage for VM disks (default local-lvm; see `ankra cluster proxmox storages`)")
	proxmoxCreateCmd.Flags().String("template", "", "VM template name (optional; the first available template on the node is used when omitted)")
	proxmoxCreateCmd.Flags().String("bastion-instance-type", "px-small", "Bastion instance-size preset")
	proxmoxCreateCmd.Flags().Int("control-plane-count", 1, "Number of control plane nodes")
	proxmoxCreateCmd.Flags().String("control-plane-instance-type", "px-medium", "Control plane instance-size preset")
	proxmoxCreateCmd.Flags().Int("worker-count", 1, "Number of worker nodes")
	proxmoxCreateCmd.Flags().String("worker-instance-type", "px-medium", "Worker instance-size preset")
	proxmoxCreateCmd.Flags().String("distribution", "k3s", "Kubernetes distribution: k3s or kubeadm")
	proxmoxCreateCmd.Flags().String("kubernetes-version", "", "Kubernetes version (optional; see `ankra cluster k3s-versions` or `ankra cluster kubeadm-versions`)")
	proxmoxCreateCmd.Flags().String("etcd-topology", "stacked", "etcd topology for kubeadm clusters: stacked (on control plane) or external (dedicated VMs)")
	proxmoxCreateCmd.Flags().Int("etcd-node-count", 3, "Number of dedicated etcd nodes when --etcd-topology=external (3 or 5)")
	proxmoxCreateCmd.Flags().String("etcd-instance-type", "px-medium", "Instance-size preset for dedicated etcd nodes when --etcd-topology=external")
	proxmoxCreateCmd.Flags().String("cni", "", "CNI plugin (optional; the platform default is used when omitted)")
	proxmoxCreateCmd.Flags().Bool("include-networking", true, "Install Traefik + cert-manager as Ankra-managed stacks for ingress (exposed via the built-in k3s service load balancer; default on)")

	_ = proxmoxCreateCmd.MarkFlagRequired("name")
	_ = proxmoxCreateCmd.MarkFlagRequired("credential-id")
	_ = proxmoxCreateCmd.MarkFlagRequired("ssh-key-credential-id")
	_ = proxmoxCreateCmd.MarkFlagRequired("node")
	_ = proxmoxCreateCmd.MarkFlagRequired("bridge")

	proxmoxStartCmd.Flags().String("scope", "all", "Provisioning scope: 'all' or 'control_plane'")

	proxmoxHostsCmd.Flags().String("credential-id", "", "Proxmox VE API credential ID (required)")
	_ = proxmoxHostsCmd.MarkFlagRequired("credential-id")
	for _, nodeScopedCatalogCmd := range []*cobra.Command{proxmoxStoragesCmd, proxmoxBridgesCmd, proxmoxTemplatesCmd} {
		nodeScopedCatalogCmd.Flags().String("credential-id", "", "Proxmox VE API credential ID (required)")
		nodeScopedCatalogCmd.Flags().String("node", "", "Proxmox VE host node (required; see `ankra cluster proxmox hosts`)")
		_ = nodeScopedCatalogCmd.MarkFlagRequired("credential-id")
		_ = nodeScopedCatalogCmd.MarkFlagRequired("node")
	}

	registerStructuredOutputFlags(
		proxmoxCreateCmd,
		proxmoxWorkersCmd,
		proxmoxK8sVersionCmd,
	)

	proxmoxCmd.AddCommand(proxmoxCreateCmd)
	proxmoxCmd.AddCommand(proxmoxHostsCmd)
	proxmoxCmd.AddCommand(proxmoxStoragesCmd)
	proxmoxCmd.AddCommand(proxmoxBridgesCmd)
	proxmoxCmd.AddCommand(proxmoxTemplatesCmd)
	proxmoxCmd.AddCommand(proxmoxSizesCmd)
	proxmoxCmd.AddCommand(proxmoxStopCmd)
	proxmoxCmd.AddCommand(proxmoxStartCmd)
	proxmoxCmd.AddCommand(proxmoxWorkersCmd)
	proxmoxCmd.AddCommand(proxmoxK8sVersionCmd)

	clusterCmd.AddCommand(proxmoxCmd)
}

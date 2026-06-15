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
		externalCloudProvider, includeNetworking, err := resolveCloudProviderNetworking(cmd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		gitopsCredentialName, _ := cmd.Flags().GetString("gitops-credential-name")
		gitopsRepository, _ := cmd.Flags().GetString("gitops-repository")
		gitopsBranch, _ := cmd.Flags().GetString("gitops-branch")

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
			ExternalCloudProvider:  externalCloudProvider,
			IncludeNetworking:      includeNetworking,
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

		result, err := apiClient.CreateHetznerCluster(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating Hetzner cluster: %v\n", err)
			os.Exit(1)
		}

		if renderStructuredOrExit(cmd, result) {
			return
		}

		fmt.Printf("Hetzner cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
	},
}

var hetznerDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id>",
	Short: "Initiate deprovision of a Hetzner cluster",
	Long: "Initiate asynchronous deprovision of a Hetzner cluster. The platform schedules teardown " +
		"jobs that delete cloud resources (servers, networks, SSH keys). Cloud resources are not " +
		"deleted by the time this command returns; track progress via operations or the UI.",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		force, _ := cmd.Flags().GetBool("force")

		result, err := apiClient.DeprovisionHetznerCluster(clusterID, force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deprovisioning cluster: %v\n", err)
			os.Exit(1)
		}

		if renderStructuredOrExit(cmd, result) {
			return
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("Hetzner cluster deprovision initiated."))
			fmt.Println("Cloud resources are being torn down asynchronously. Track progress in the UI.")
		} else {
			fmt.Println(text.FgYellow.Sprint("Hetzner cluster deprovision request accepted with warnings."))
		}

		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if result.OperationID != nil && *result.OperationID != "" {
			fmt.Printf("  Operation ID: %s\n", *result.OperationID)
		}
	},
}

var hetznerWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for a Hetzner cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]

		result, err := apiClient.GetHetznerWorkerCount(clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching worker count: %v\n", err)
			os.Exit(1)
		}

		if renderStructuredOrExit(cmd, result) {
			return
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

		result, err := apiClient.ScaleHetznerWorkers(clusterID, workerCount)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scaling workers: %v\n", err)
			os.Exit(1)
		}

		if renderStructuredOrExit(cmd, result) {
			return
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

		result, err := apiClient.GetHetznerK8sVersion(clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching Kubernetes version: %v\n", err)
			os.Exit(1)
		}

		if renderStructuredOrExit(cmd, result) {
			return
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
	Use:        "upgrade <cluster_id> <target_version>",
	Short:      "Upgrade Kubernetes version for a Hetzner cluster",
	Long:       "Upgrade the Kubernetes (k3s) version on all nodes in a Hetzner cluster.",
	Deprecated: "use `ankra cluster upgrade <cluster_id> <target_version>` instead; the cloud provider is detected automatically.",
	Args:       cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		clusterID := args[0]
		targetVersion := args[1]

		result, err := apiClient.UpgradeHetznerK8sVersion(clusterID, targetVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error upgrading Kubernetes version: %v\n", err)
			os.Exit(1)
		}

		if renderStructuredOrExit(cmd, result) {
			return
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
		result, err := apiClient.ListHetznerNodeGroups(clusterID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing node groups: %v\n", err)
			os.Exit(1)
		}
		if renderStructuredOrExit(cmd, result) {
			return
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

		requestContext, cancelRequestContext, wait := nodeGroupAsyncContext(cmd)
		defer cancelRequestContext()

		result, submitted, err := apiClient.AddHetznerNodeGroup(requestContext, clusterID, req, wait)
		if err != nil {
			handleNodeGroupSubmitError("adding node group", err)
		}
		if submitted {
			if renderStructuredOrExit(cmd, newAsyncSubmittedResult("Node group add")) {
				return
			}
			printAsyncWriteSubmitted("Node group add")
			return
		}
		if renderStructuredOrExit(cmd, result) {
			return
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

		requestContext, cancelRequestContext, wait := nodeGroupAsyncContext(cmd)
		defer cancelRequestContext()

		result, submitted, err := apiClient.ScaleHetznerNodeGroup(requestContext, clusterID, groupName, count, wait)
		if err != nil {
			handleNodeGroupSubmitError("scaling node group", err)
		}
		if submitted {
			if renderStructuredOrExit(cmd, newAsyncSubmittedResult("Node group scale")) {
				return
			}
			printAsyncWriteSubmitted("Node group scale")
			return
		}
		if renderStructuredOrExit(cmd, result) {
			return
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

		requestContext, cancelRequestContext, wait := nodeGroupAsyncContext(cmd)
		defer cancelRequestContext()

		result, submitted, err := apiClient.UpdateHetznerNodeGroupInstanceType(requestContext, clusterID, groupName, instanceType, wait)
		if err != nil {
			handleNodeGroupSubmitError("upgrading node group", err)
		}
		if submitted {
			if renderStructuredOrExit(cmd, newAsyncSubmittedResult("Node group instance-type update")) {
				return
			}
			printAsyncWriteSubmitted("Node group instance-type update")
			return
		}
		if renderStructuredOrExit(cmd, result) {
			return
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

		requestContext, cancelRequestContext, wait := nodeGroupAsyncContext(cmd)
		defer cancelRequestContext()

		result, submitted, err := apiClient.DeleteHetznerNodeGroup(requestContext, clusterID, groupName, wait)
		if err != nil {
			handleNodeGroupSubmitError("deleting node group", err)
		}
		if submitted {
			if renderStructuredOrExit(cmd, newAsyncSubmittedResult("Node group delete")) {
				return
			}
			printAsyncWriteSubmitted("Node group delete")
			return
		}
		if renderStructuredOrExit(cmd, result) {
			return
		}
		fmt.Printf("Node group '%s' deleted. %d node(s) removed.\n", result.GroupName, result.Deleted)
	},
}

var hetznerLocationsCmd = &cobra.Command{
	Use:   "locations",
	Short: "List Hetzner locations available to a credential",
	Long:  "List the Hetzner Cloud locations the supplied credential can deploy in. Only these locations are valid for cluster creation.",
	Run: func(cmd *cobra.Command, args []string) {
		credentialID, _ := cmd.Flags().GetString("credential-id")

		locations, err := apiClient.ListHetznerLocations(credentialID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing locations: %v\n", err)
			os.Exit(1)
		}
		if len(locations) == 0 {
			fmt.Println("No locations available for this credential.")
			return
		}
		fmt.Printf("Locations available to credential %s:\n", credentialID)
		for _, loc := range locations {
			fmt.Printf("  %-6s %s, %s (zone: %s)\n", loc.Name, loc.City, loc.Country, loc.NetworkZone)
		}
	},
}

var hetznerServerTypesCmd = &cobra.Command{
	Use:   "server-types",
	Short: "List Hetzner server types and availability",
	Long:  "List Hetzner Cloud server types. Pass --location to see which types are currently available for provisioning there, and --available-only to hide the rest.",
	Run: func(cmd *cobra.Command, args []string) {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		location, _ := cmd.Flags().GetString("location")
		availableOnly, _ := cmd.Flags().GetBool("available-only")

		serverTypes, err := apiClient.ListHetznerServerTypes(credentialID, location)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing server types: %v\n", err)
			os.Exit(1)
		}
		if len(serverTypes) == 0 {
			fmt.Println("No server types available for this credential.")
			return
		}
		header := "Server types"
		if location != "" {
			header = fmt.Sprintf("Server types for location %s", location)
		}
		fmt.Printf("%s:\n", header)
		fmt.Printf("  %-12s %-6s %-8s %-7s %-6s %-10s %s\n", "NAME", "CORES", "MEMORY", "DISK", "ARCH", "PRICE/MO", "AVAILABLE")
		for _, st := range serverTypes {
			if availableOnly && !st.Available {
				continue
			}
			available := "yes"
			if !st.Available {
				available = "no"
			}
			fmt.Printf("  %-12s %-6d %-6.0fGB %-5dGB %-6s %-10.2f %s\n",
				st.Name, st.Cores, st.Memory, st.Disk, st.Architecture, st.PriceMonthly, available)
		}
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
	hetznerCreateCmd.Flags().Bool("external-cloud-provider", true, "Install the Hetzner CCM and CSI (cloud-provider=external) for LoadBalancers and persistent volumes (default on; pass --external-cloud-provider=false to skip, which also disables --include-networking)")
	hetznerCreateCmd.Flags().Bool("include-networking", true, "Install Traefik + cert-manager for ingress (default on; pass --include-networking=false to skip). Requires --external-cloud-provider (the ingress LoadBalancer is provisioned by the cloud controller manager)")
	hetznerCreateCmd.Flags().String("gitops-credential-name", "", "GitOps GitHub credential name; when set with --gitops-repository, the generated hcloud stack is committed to Git (optional)")
	hetznerCreateCmd.Flags().String("gitops-repository", "", "GitOps repository (e.g. org/repo) to commit the generated stack to (optional)")
	hetznerCreateCmd.Flags().String("gitops-branch", "master", "GitOps branch to commit to")

	_ = hetznerCreateCmd.MarkFlagRequired("name")
	_ = hetznerCreateCmd.MarkFlagRequired("credential-id")
	_ = hetznerCreateCmd.MarkFlagRequired("location")

	hetznerDeprovisionCmd.Flags().Bool("force", false, "Force deprovision without waiting for the cluster agent (use only when the cluster agent is permanently offline; cloud resources may leak)")

	hetznerLocationsCmd.Flags().String("credential-id", "", "Hetzner API credential ID (required)")
	_ = hetznerLocationsCmd.MarkFlagRequired("credential-id")
	hetznerServerTypesCmd.Flags().String("credential-id", "", "Hetzner API credential ID (required)")
	hetznerServerTypesCmd.Flags().String("location", "", "Filter availability by location")
	hetznerServerTypesCmd.Flags().Bool("available-only", false, "Only show server types available for provisioning (use with --location)")
	_ = hetznerServerTypesCmd.MarkFlagRequired("credential-id")

	nodeGroupAddCmd.Flags().String("name", "", "Node group name (required)")
	nodeGroupAddCmd.Flags().String("instance-type", "cx33", "Server type for nodes")
	nodeGroupAddCmd.Flags().Int("count", 1, "Number of nodes (0-100)")
	_ = nodeGroupAddCmd.MarkFlagRequired("name")
	registerAsyncWriteFlags(nodeGroupAddCmd)
	registerAsyncWriteFlags(nodeGroupScaleCmd)
	registerAsyncWriteFlags(nodeGroupUpgradeCmd)
	registerAsyncWriteFlags(nodeGroupDeleteCmd)

	registerStructuredOutputFlags(
		hetznerCreateCmd,
		hetznerDeprovisionCmd,
		hetznerWorkersCmd,
		hetznerScaleCmd,
		hetznerK8sVersionCmd,
		hetznerUpgradeCmd,
		nodeGroupListCmd,
		nodeGroupAddCmd,
		nodeGroupScaleCmd,
		nodeGroupUpgradeCmd,
		nodeGroupDeleteCmd,
	)

	nodeGroupCmd.AddCommand(nodeGroupListCmd)
	nodeGroupCmd.AddCommand(nodeGroupAddCmd)
	nodeGroupCmd.AddCommand(nodeGroupScaleCmd)
	nodeGroupCmd.AddCommand(nodeGroupUpgradeCmd)
	nodeGroupCmd.AddCommand(nodeGroupDeleteCmd)

	hetznerCmd.AddCommand(hetznerCreateCmd)
	hetznerCmd.AddCommand(hetznerLocationsCmd)
	hetznerCmd.AddCommand(hetznerServerTypesCmd)
	hetznerCmd.AddCommand(hetznerDeprovisionCmd)
	hetznerCmd.AddCommand(hetznerWorkersCmd)
	hetznerCmd.AddCommand(hetznerScaleCmd)
	hetznerCmd.AddCommand(hetznerK8sVersionCmd)
	hetznerCmd.AddCommand(hetznerUpgradeCmd)
	hetznerCmd.AddCommand(nodeGroupCmd)

	clusterCmd.AddCommand(hetznerCmd)
}

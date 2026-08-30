package cmd

import (
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var scalewayCmd = &cobra.Command{
	Use:   "scaleway",
	Short: "Manage Scaleway clusters",
	Long:  "Manage the lifecycle of Scaleway clusters.",
}

var scalewayStopCmd = &cobra.Command{
	Use:   "stop <cluster_id>",
	Short: "Stop a Scaleway cluster",
	Long:  "Stop a Scaleway cluster by terminating its compute while preserving its configuration so it can be re-provisioned later.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, arguments []string) error {
		clusterID := arguments[0]
		force, _ := cmd.Flags().GetBool("force")
		result, stopError := apiClient.StopScalewayCluster(clusterID, force)
		if stopError != nil {
			return fmt.Errorf("stopping Scaleway cluster: %w", stopError)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("Scaleway cluster stop initiated."))
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

var scalewayStartCmd = &cobra.Command{
	Use:   "start <cluster_id>",
	Short: "Start a stopped Scaleway cluster",
	Long:  "Re-provision a stopped Scaleway cluster. Use --scope control_plane to bring up only the control plane.",
	Args:  cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		clusterID := arguments[0]
		scope, scopeError := command.Flags().GetString("scope")
		if scopeError != nil {
			return fmt.Errorf("reading scope: %w", scopeError)
		}
		if scope != "all" && scope != "control_plane" {
			return fmt.Errorf("invalid --scope %q: must be 'all' or 'control_plane'", scope)
		}

		result, startError := apiClient.StartScalewayCluster(clusterID, scope)
		if startError != nil {
			return fmt.Errorf("starting Scaleway cluster: %w", startError)
		}

		fmt.Println(text.FgGreen.Sprint("Scaleway cluster start initiated."))
		fmt.Printf("  Scope: %s\n", result.Scope)
		if result.MarkedToStartAt != "" {
			fmt.Printf("  Marked to start at: %s\n", result.MarkedToStartAt)
		}
		fmt.Printf("  Created operations: %d\n", result.CreatedOperations)
		return nil
	},
}

func init() {
	scalewayStartCmd.Flags().String("scope", "all", "Provisioning scope: 'all' or 'control_plane'")
	scalewayStopCmd.Flags().Bool("force", false, "Force stop: cancel every in-flight operation and block new operations for 60 seconds while the stop lands, and also delete the cluster's tagged volumes and load balancers even when retention_policy is retain (destroys persisted data)")
	registerScalewayCreateFlags(scalewayCreateCmd, scalewayPreflightCmd)
	registerScalewayCatalogFlags(false, scalewayLocationsCmd)
	registerScalewayCatalogFlags(true, scalewayInstanceTypesCmd, scalewayGatewayTypesCmd, scalewayNetworksCmd)
	registerStructuredOutputFlags(
		scalewayCreateCmd, scalewayPreflightCmd, scalewayDeprovisionCmd,
		scalewayWorkersCmd, scalewayK8sVersionCmd, scalewayLocationsCmd,
		scalewayInstanceTypesCmd, scalewayGatewayTypesCmd, scalewayNetworksCmd,
	)

	scalewayCmd.AddCommand(scalewayCreateCmd)
	scalewayCmd.AddCommand(scalewayPreflightCmd)
	scalewayCmd.AddCommand(scalewayDeprovisionCmd)
	scalewayCmd.AddCommand(scalewayStopCmd)
	scalewayCmd.AddCommand(scalewayStartCmd)
	scalewayCmd.AddCommand(scalewayWorkersCmd)
	scalewayCmd.AddCommand(scalewayK8sVersionCmd)
	scalewayCmd.AddCommand(scalewayLocationsCmd)
	scalewayCmd.AddCommand(scalewayInstanceTypesCmd)
	scalewayCmd.AddCommand(scalewayGatewayTypesCmd)
	scalewayCmd.AddCommand(scalewayNetworksCmd)
	scalewayCmd.AddCommand(newControlPlaneCmd(scalewayControlPlaneOps, "Scaleway", "scaleway instance-types"))
	clusterCmd.AddCommand(scalewayCmd)
}

// scalewayCreateRequestFromFlags builds the create body shared by `create` and
// `preflight`. Flags left at their zero value are omitted so the server's
// documented defaults apply (gateway type VPC-GW-S, bastion port 61000,
// DEV1-M instance types, k3s, stacked etcd, retain).
func scalewayCreateRequestFromFlags(cmd *cobra.Command) client.CreateScalewayClusterRequest {
	name, _ := cmd.Flags().GetString("name")
	description, _ := cmd.Flags().GetString("description")
	credentialID, _ := cmd.Flags().GetString("credential-id")
	runtimeCredentialID, _ := cmd.Flags().GetString("runtime-credential-id")
	sshKeyCredentialID, _ := cmd.Flags().GetString("ssh-key-credential-id")
	region, _ := cmd.Flags().GetString("region")
	zone, _ := cmd.Flags().GetString("zone")
	privateNetworkID, _ := cmd.Flags().GetString("private-network-id")
	networkIPRange, _ := cmd.Flags().GetString("network-ip-range")
	gatewayType, _ := cmd.Flags().GetString("gateway-type")
	bastionPort, _ := cmd.Flags().GetInt("bastion-port")
	gatewayAllowedIPs, _ := cmd.Flags().GetStringSlice("gateway-allowed-ips")
	controlPlaneCount, _ := cmd.Flags().GetInt("control-plane-count")
	controlPlaneType, _ := cmd.Flags().GetString("control-plane-type")
	workerType, _ := cmd.Flags().GetString("worker-type")
	distribution, _ := cmd.Flags().GetString("distribution")
	kubernetesVersion, _ := cmd.Flags().GetString("kubernetes-version")
	etcdTopology, _ := cmd.Flags().GetString("etcd-topology")
	etcdNodeCount, _ := cmd.Flags().GetInt("etcd-node-count")
	etcdType, _ := cmd.Flags().GetString("etcd-type")
	cni, _ := cmd.Flags().GetString("cni")
	retentionPolicy, _ := cmd.Flags().GetString("retention-policy")

	request := client.CreateScalewayClusterRequest{
		Name:               name,
		CredentialID:       credentialID,
		SSHKeyCredentialID: sshKeyCredentialID,
		Region:             region,
		Zone:               zone,
		GatewayType:        gatewayType,
		BastionPort:        bastionPort,
		GatewayAllowedIPs:  gatewayAllowedIPs,
		ControlPlaneCount:  controlPlaneCount,
		ControlPlaneType:   controlPlaneType,
		WorkerType:         workerType,
		Distribution:       distribution,
		EtcdTopology:       etcdTopology,
		EtcdNodeCount:      etcdNodeCount,
		EtcdType:           etcdType,
		CNI:                cni,
		RetentionPolicy:    retentionPolicy,
	}
	// worker-count is tri-state: 0 is a legitimate value (node-group-only
	// clusters), so only send it when the user actually set the flag.
	if cmd.Flags().Changed("worker-count") {
		workerCount, _ := cmd.Flags().GetInt("worker-count")
		request.WorkerCount = &workerCount
	}
	if cmd.Flags().Changed("include-networking") {
		includeNetworking, _ := cmd.Flags().GetBool("include-networking")
		request.IncludeNetworking = &includeNetworking
	}
	if cmd.Flags().Changed("include-dns") {
		includeDNS, _ := cmd.Flags().GetBool("include-dns")
		request.IncludeDNS = &includeDNS
	}
	if description != "" {
		request.Description = &description
	}
	if runtimeCredentialID != "" {
		request.RuntimeCredentialID = &runtimeCredentialID
	}
	if privateNetworkID != "" {
		request.PrivateNetworkID = &privateNetworkID
	}
	if networkIPRange != "" {
		request.NetworkIPRange = &networkIPRange
	}
	if kubernetesVersion != "" {
		request.KubernetesVersion = &kubernetesVersion
	}
	return request
}

var scalewayCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new Scaleway Instances cluster",
	Long: `Create an Ankra-managed K3s or kubeadm cluster on Scaleway Instances.

Ankra owns the server graph, security group, Public Gateway v2, generated SSH
key and (unless --private-network-id adopts an existing one) the Private
Network. Run 'preflight' first to check region, zone, CIDR and SKU
availability - it proves availability, not project hard quota.

Examples:
  ankra cluster scaleway create --name prod --credential-id <id> \
    --ssh-key-credential-id <id> --region fr-par --zone fr-par-1`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, createError := apiClient.CreateScalewayCluster(scalewayCreateRequestFromFlags(cmd))
		if createError != nil {
			return fmt.Errorf("creating Scaleway cluster: %w", createError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Printf("Scaleway cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
		return nil
	},
}

var scalewayPreflightCmd = &cobra.Command{
	Use:   "preflight",
	Short: "Validate a Scaleway cluster create request without provisioning",
	Long: `Run the Scaleway create preflight: region/zone reachability, CIDR overlap,
gateway and instance-type availability. A quota warning means SKU stock was
visible, not that project hard-quota headroom was proven.

Takes the same flags as 'create'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, preflightError := apiClient.PreflightScalewayCluster(scalewayCreateRequestFromFlags(cmd))
		if preflightError != nil {
			return fmt.Errorf("running Scaleway preflight: %w", preflightError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Check", "Status", "Message"})
		for _, item := range result.Items {
			t.AppendRow(table.Row{item.Check, item.Status, item.Message})
		}
		t.Render()

		if result.CanProceed {
			fmt.Println(text.FgGreen.Sprint("\nPreflight passed: the cluster can be created."))
			return nil
		}
		return fmt.Errorf("preflight failed: resolve the checks above before creating the cluster")
	},
}

var scalewayDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id>",
	Short: "Deprovision a Scaleway cluster and release its cloud resources",
	Long: `Permanently delete a Scaleway cluster and the provider resources Ankra
created for it. Volumes and load balancers follow the cluster's
retention_policy: 'retain' keeps them, 'delete' sweeps the tagged orphans.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, deprovisionError := apiClient.DeprovisionScalewayCluster(args[0])
		if deprovisionError != nil {
			return fmt.Errorf("deprovisioning Scaleway cluster: %w", deprovisionError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Println(text.FgGreen.Sprint("Scaleway cluster deprovision initiated."))
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if result.OperationID != nil {
			fmt.Printf("  Operation ID: %s\n", *result.OperationID)
		}
		return nil
	},
}

var scalewayWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id>",
	Short: "Get current worker count for a Scaleway cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, fetchError := apiClient.GetScalewayWorkerCount(args[0])
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

var scalewayK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id>",
	Short: "Get current Kubernetes version for a Scaleway cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, fetchError := apiClient.GetScalewayK8sVersion(args[0])
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

var scalewayLocationsCmd = &cobra.Command{
	Use:   "locations",
	Short: "List Scaleway regions and zones available to a credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		result, listError := apiClient.ListScalewayLocations(credentialID)
		if listError != nil {
			return fmt.Errorf("listing Scaleway locations: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Locations) == 0 {
			fmt.Println("No locations available for this credential.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Region", "Zones"})
		for _, location := range result.Locations {
			t.AppendRow(table.Row{location.Region, strings.Join(location.Zones, ", ")})
		}
		t.Render()
		return nil
	},
}

var scalewayInstanceTypesCmd = &cobra.Command{
	Use:   "instance-types",
	Short: "List Scaleway instance types (commercial types) for a zone",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		region, _ := cmd.Flags().GetString("region")
		zone, _ := cmd.Flags().GetString("zone")
		result, listError := apiClient.ListScalewayInstanceTypes(credentialID, region, zone)
		if listError != nil {
			return fmt.Errorf("listing Scaleway instance types: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.InstanceTypes) == 0 {
			fmt.Println("No instance types available for this credential and zone.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "vCPU", "Memory (GB)", "Arch", "Monthly"})
		for _, instanceType := range result.InstanceTypes {
			t.AppendRow(table.Row{
				instanceType.Name, instanceType.CPUs, instanceType.MemoryGB,
				instanceType.Architecture,
				fmt.Sprintf("%.2f %s", instanceType.MonthlyPrice, strings.ToUpper(instanceType.Currency)),
			})
		}
		t.Render()
		if !result.PricingComplete {
			fmt.Println(text.FgYellow.Sprint("\nPricing is incomplete: gateway, flexible IP and dependent networking prices are not published by Scaleway."))
		}
		return nil
	},
}

var scalewayGatewayTypesCmd = &cobra.Command{
	Use:   "gateway-types",
	Short: "List Scaleway Public Gateway types for a zone",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		region, _ := cmd.Flags().GetString("region")
		zone, _ := cmd.Flags().GetString("zone")
		result, listError := apiClient.ListScalewayGatewayTypes(credentialID, region, zone)
		if listError != nil {
			return fmt.Errorf("listing Scaleway gateway types: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.GatewayTypes) == 0 {
			fmt.Println("No gateway types available for this credential and zone.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Name", "Monthly"})
		for _, gatewayType := range result.GatewayTypes {
			t.AppendRow(table.Row{
				gatewayType.Name,
				fmt.Sprintf("%.2f %s", gatewayType.MonthlyPrice, strings.ToUpper(gatewayType.Currency)),
			})
		}
		t.Render()
		return nil
	},
}

var scalewayNetworksCmd = &cobra.Command{
	Use:   "networks",
	Short: "List Scaleway Private Networks a credential can adopt",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		region, _ := cmd.Flags().GetString("region")
		zone, _ := cmd.Flags().GetString("zone")
		result, listError := apiClient.ListScalewayNetworks(credentialID, region, zone)
		if listError != nil {
			return fmt.Errorf("listing Scaleway networks: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Networks) == 0 {
			fmt.Println("No private networks available for this credential and region.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "Subnets"})
		for _, network := range result.Networks {
			t.AppendRow(table.Row{network.ID, network.Name, strings.Join(network.Subnets, ", ")})
		}
		t.Render()
		return nil
	},
}

// registerScalewayCreateFlags declares the create/preflight flag set once so
// the two commands can never drift.
func registerScalewayCreateFlags(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().String("name", "", "Cluster name (required)")
		command.Flags().String("description", "", "Cluster description")
		command.Flags().String("credential-id", "", "Scaleway provisioning credential ID (required)")
		command.Flags().String("runtime-credential-id", "", "Scaleway credential for the in-cluster CCM/CSI (defaults to the provisioning credential)")
		command.Flags().String("ssh-key-credential-id", "", "SSH key credential ID (required)")
		command.Flags().String("region", "", "Scaleway region, e.g. fr-par (required)")
		command.Flags().String("zone", "", "Scaleway zone in that region, e.g. fr-par-1 (required)")
		command.Flags().String("private-network-id", "", "Adopt an existing Private Network instead of creating one")
		command.Flags().String("network-ip-range", "", "CIDR for the created Private Network")
		command.Flags().String("gateway-type", "", "Public Gateway type (server default: VPC-GW-S)")
		command.Flags().Int("bastion-port", 0, "Public Gateway bastion SSH port (server default: 61000)")
		command.Flags().StringSlice("gateway-allowed-ips", nil, "CIDRs allowed to reach the gateway bastion")
		command.Flags().Int("control-plane-count", 0, "Control plane node count (server default: 1)")
		command.Flags().String("control-plane-type", "", "Control plane commercial type (server default: DEV1-M)")
		command.Flags().Int("worker-count", 0, "Default-pool worker count (server default: 1)")
		command.Flags().String("worker-type", "", "Worker commercial type (server default: DEV1-M)")
		command.Flags().String("distribution", "", "Kubernetes distribution: k3s or kubeadm (server default: k3s). kubeadm on Scaleway is Cilium-only")
		command.Flags().String("kubernetes-version", "", "Pin a Kubernetes version (default: latest supported)")
		command.Flags().String("etcd-topology", "", "etcd topology: stacked or external (server default: stacked)")
		command.Flags().Int("etcd-node-count", 0, "External etcd node count, 3 or 5 (server default: 3)")
		command.Flags().String("etcd-type", "", "External etcd commercial type (server default: DEV1-M)")
		command.Flags().String("cni", "", "CNI plugin (default: the platform default; immutable after create)")
		command.Flags().String("retention-policy", "", "Teardown policy for volumes and load balancers: retain or delete (server default: retain)")
		command.Flags().Bool("include-networking", true, "Provision the networking stack")
		command.Flags().Bool("include-dns", true, "Provision the DNS zone")
		_ = command.MarkFlagRequired("name")
		_ = command.MarkFlagRequired("credential-id")
		_ = command.MarkFlagRequired("ssh-key-credential-id")
		_ = command.MarkFlagRequired("region")
		_ = command.MarkFlagRequired("zone")
	}
}

// registerScalewayCatalogFlags declares the credential-scoped catalog flags.
func registerScalewayCatalogFlags(zoned bool, commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().String("credential-id", "", "Scaleway credential ID (required)")
		_ = command.MarkFlagRequired("credential-id")
		if zoned {
			command.Flags().String("region", "", "Scaleway region, e.g. fr-par")
			command.Flags().String("zone", "", "Scaleway zone, e.g. fr-par-1")
		}
	}
}

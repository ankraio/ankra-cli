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

var scalewayCmd = &cobra.Command{
	Use:   "scaleway",
	Short: "Manage self-hosted Kubernetes on Scaleway Instances",
	Long:  "Create and operate private Scaleway Instances clusters behind a Public Gateway v2 managed bastion.",
}

func validateScalewayCNI(distribution, cni string, features client.ScalewayCNIFeatures) error {
	switch cni {
	case "flannel":
		if features != (client.ScalewayCNIFeatures{}) {
			return errors.New("flannel does not support the Scaleway CNI feature flags")
		}
	case "calico":
		if features.Hubble || features.KubeProxyReplacement || features.WireguardEncryption {
			return errors.New("calico supports only --cni-ebpf-dataplane; Hubble, kube-proxy replacement, and WireGuard are Cilium features")
		}
	case "cilium":
		if features.EBPFDataplane {
			return errors.New("--cni-ebpf-dataplane is a Calico feature; use --cni-kube-proxy-replacement for Cilium")
		}
	default:
		return fmt.Errorf("invalid --cni %q: use flannel, calico, or cilium", cni)
	}
	if distribution == "kubeadm" && cni != "cilium" {
		return fmt.Errorf("kubeadm requires --cni cilium; %s is supported only with k3s", cni)
	}
	if distribution != "k3s" && distribution != "kubeadm" {
		return fmt.Errorf("invalid --distribution %q: use k3s or kubeadm", distribution)
	}
	return nil
}

func parseScalewayNodeGroups(values []string) ([]client.ScalewayCreateNodeGroupRequest, error) {
	result := make([]client.ScalewayCreateNodeGroupRequest, 0, len(values))
	for index, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("--node-group entry %d %q must be name:instance-type:count", index+1, value)
		}
		count, err := strconv.Atoi(parts[2])
		if err != nil || count < 0 {
			return nil, fmt.Errorf("--node-group entry %d has invalid count %q", index+1, parts[2])
		}
		if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("--node-group entry %d requires non-empty name and instance type", index+1)
		}
		result = append(result, client.ScalewayCreateNodeGroupRequest{
			Name: strings.TrimSpace(parts[0]), InstanceType: strings.TrimSpace(parts[1]), Count: count,
		})
	}
	return result, nil
}

func scalewayCreateRequestFromFlags(cmd *cobra.Command) (client.CreateScalewayClusterRequest, error) {
	stringFlag := func(name string) string {
		value, _ := cmd.Flags().GetString(name)
		return strings.TrimSpace(value)
	}
	intFlag := func(name string) int {
		value, _ := cmd.Flags().GetInt(name)
		return value
	}
	boolFlag := func(name string) bool {
		value, _ := cmd.Flags().GetBool(name)
		return value
	}

	distribution := stringFlag("distribution")
	cni := stringFlag("cni")
	features := client.ScalewayCNIFeatures{
		EBPFDataplane:        boolFlag("cni-ebpf-dataplane"),
		Hubble:               boolFlag("cni-hubble"),
		KubeProxyReplacement: boolFlag("cni-kube-proxy-replacement"),
		WireguardEncryption:  boolFlag("cni-wireguard"),
	}
	if err := validateScalewayCNI(distribution, cni, features); err != nil {
		return client.CreateScalewayClusterRequest{}, withExitCode(exitUsage, err)
	}

	privateNetworkID := stringFlag("private-network-id")
	networkIPRange := stringFlag("network-ip-range")
	if privateNetworkID != "" && networkIPRange != "" {
		return client.CreateScalewayClusterRequest{}, withExitCode(exitUsage,
			errors.New("--private-network-id and --network-ip-range are mutually exclusive"))
	}
	if privateNetworkID == "" && networkIPRange == "" {
		return client.CreateScalewayClusterRequest{}, withExitCode(exitUsage,
			errors.New("choose existing network mode with --private-network-id or new network/IPAM mode with --network-ip-range"))
	}
	gatewayAllowedIPs, _ := cmd.Flags().GetStringSlice("gateway-allowed-ips")
	if len(gatewayAllowedIPs) == 0 {
		return client.CreateScalewayClusterRequest{}, withExitCode(exitUsage,
			errors.New("at least one --gateway-allowed-ips CIDR is required; avoid 0.0.0.0/0"))
	}
	nodeGroupValues, _ := cmd.Flags().GetStringArray("node-group")
	nodeGroups, err := parseScalewayNodeGroups(nodeGroupValues)
	if err != nil {
		return client.CreateScalewayClusterRequest{}, withExitCode(exitUsage, err)
	}

	request := client.CreateScalewayClusterRequest{
		Name:                  stringFlag("name"),
		CredentialID:          stringFlag("credential-id"),
		SSHKeyCredentialID:    stringFlag("ssh-key-credential-id"),
		Region:                stringFlag("region"),
		Zone:                  stringFlag("zone"),
		GatewayType:           stringFlag("gateway-type"),
		GatewayAllowedIPs:     gatewayAllowedIPs,
		BastionPort:           intFlag("bastion-port"),
		ControlPlaneCount:     intFlag("control-plane-count"),
		ControlPlaneType:      stringFlag("control-plane-type"),
		WorkerCount:           intFlag("worker-count"),
		WorkerType:            stringFlag("worker-type"),
		NodeGroups:            nodeGroups,
		Distribution:          distribution,
		EtcdTopology:          stringFlag("etcd-topology"),
		EtcdNodeCount:         intFlag("etcd-node-count"),
		EtcdType:              stringFlag("etcd-type"),
		ExternalCloudProvider: true,
		CNI:                   cni,
		CNIFeatures:           features,
		IncludeNetworking:     boolFlag("include-networking"),
		IncludeDNS:            boolFlag("include-dns"),
		GitopsCredentialName:  stringFlag("gitops-credential-name"),
		GitopsRepository:      stringFlag("gitops-repository"),
		GitopsBranch:          stringFlag("gitops-branch"),
		RetentionPolicy:       stringFlag("retention-policy"),
	}
	if privateNetworkID != "" {
		request.PrivateNetworkID = &privateNetworkID
	} else {
		request.NetworkIPRange = &networkIPRange
	}
	if value := stringFlag("runtime-credential-id"); value != "" {
		request.RuntimeCredentialID = &value
	}
	if value := stringFlag("kubernetes-version"); value != "" {
		request.KubernetesVersion = &value
	}
	if request.RuntimeCredentialID == nil {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Warning: no --runtime-credential-id supplied; the provisioning credential will be reused by CCM/CSI at runtime. Prefer a dedicated least-privilege runtime credential."); err != nil {
			return client.CreateScalewayClusterRequest{}, fmt.Errorf("writing runtime credential warning: %w", err)
		}
	}
	return request, nil
}

func addScalewayCreateFlags(command *cobra.Command) {
	command.Flags().String("name", "", "Cluster name (required)")
	command.Flags().String("credential-id", "", "Scaleway provisioning credential ID (required)")
	command.Flags().String("runtime-credential-id", "", "Dedicated least-privilege CCM/CSI runtime credential ID")
	command.Flags().String("ssh-key-credential-id", "", "SSH-key credential ID (required)")
	command.Flags().String("region", "", "Scaleway region (required)")
	command.Flags().String("zone", "", "Scaleway zone (required)")
	command.Flags().String("private-network-id", "", "Existing regional Private Network ID")
	command.Flags().String("network-ip-range", "", "CIDR for a new Private Network/IPAM allocation")
	command.Flags().String("gateway-type", "VPC-GW-S", "Public Gateway v2 type")
	command.Flags().StringSlice("gateway-allowed-ips", nil, "CIDRs allowed to reach the managed bastion (required)")
	command.Flags().Int("bastion-port", 61000, "Managed bastion SSH port (61000 or 1024-59999)")
	command.Flags().Int("control-plane-count", 1, "Control-plane node count")
	command.Flags().String("control-plane-type", "DEV1-M", "Control-plane instance type")
	command.Flags().Int("worker-count", 1, "Default worker count")
	command.Flags().String("worker-type", "DEV1-M", "Default worker instance type")
	command.Flags().StringArray("node-group", nil, "Additional node group as name:instance-type:count (repeatable)")
	command.Flags().String("distribution", "k3s", "Kubernetes distribution: k3s or kubeadm")
	command.Flags().String("kubernetes-version", "", "Pinned Kubernetes version")
	command.Flags().String("etcd-topology", "stacked", "kubeadm etcd topology: stacked or external")
	command.Flags().Int("etcd-node-count", 3, "External etcd node count")
	command.Flags().String("etcd-type", "DEV1-M", "External etcd instance type")
	command.Flags().String("cni", "flannel", "CNI: flannel, calico, or cilium (kubeadm requires cilium)")
	command.Flags().Bool("cni-ebpf-dataplane", false, "Enable Calico eBPF dataplane")
	command.Flags().Bool("cni-hubble", false, "Enable Cilium Hubble")
	command.Flags().Bool("cni-kube-proxy-replacement", false, "Enable Cilium kube-proxy replacement")
	command.Flags().Bool("cni-wireguard", false, "Enable Cilium WireGuard encryption")
	command.Flags().Bool("include-networking", true, "Install ingress networking")
	command.Flags().Bool("include-dns", true, "Include DNS integration")
	command.Flags().String("gitops-credential-name", "", "GitOps credential name")
	command.Flags().String("gitops-repository", "", "GitOps repository")
	command.Flags().String("gitops-branch", "master", "GitOps branch")
	command.Flags().String("retention-policy", "retain", "Storage retention on teardown: retain or delete")
	for _, flag := range []string{"name", "credential-id", "ssh-key-credential-id", "region", "zone", "gateway-allowed-ips"} {
		_ = command.MarkFlagRequired(flag)
	}
}

func renderScalewayPreflight(cmd *cobra.Command, result *client.ScalewayPreflightResult) error {
	if handled, err := renderStructured(cmd, result); handled || err != nil {
		if err != nil {
			return err
		}
		if !result.CanProceed {
			return withExitCode(exitError, errors.New("scaleway preflight failed; inspect structured items"))
		}
		return nil
	}
	for _, item := range result.Items {
		fmt.Printf("%-5s  %-24s  %s\n", strings.ToUpper(item.Status), item.Check, item.Message)
	}
	if !result.CanProceed {
		return withExitCode(exitError, errors.New("scaleway preflight failed; fix failed checks before creating the cluster"))
	}
	fmt.Println(text.FgGreen.Sprint("Scaleway preflight passed."))
	return nil
}

func newScalewayCreateCommand(preflightOnly bool) *cobra.Command {
	use := "create"
	short := "Create a Scaleway Instances cluster"
	if preflightOnly {
		use = "preflight"
		short = "Validate quotas, networking, credentials, and topology"
	}
	command := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request, err := scalewayCreateRequestFromFlags(cmd)
			if err != nil {
				return err
			}
			preflight, err := activeScalewayAPI().PreflightScalewayCluster(cmd.Context(), request)
			if err != nil {
				return fmt.Errorf("preflighting Scaleway cluster: %w", err)
			}
			if preflightOnly {
				return renderScalewayPreflight(cmd, preflight)
			}
			if !preflight.CanProceed {
				return renderScalewayPreflight(cmd, preflight)
			}
			result, err := activeScalewayAPI().CreateScalewayCluster(cmd.Context(), request)
			if err != nil {
				return fmt.Errorf("creating Scaleway cluster: %w", err)
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Scaleway cluster %q created (ID: %s).\n", result.Name, result.ClusterID)
			return nil
		},
	}
	addScalewayCreateFlags(command)
	registerStructuredOutputFlags(command)
	return command
}

func newScalewayCatalogCommand(use, short string, run func(*cobra.Command, string, string) (*client.ScalewayCatalogResult, error), secondFlag string) *cobra.Command {
	command := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			credentialID, _ := cmd.Flags().GetString("credential-id")
			second := ""
			if secondFlag != "" {
				second, _ = cmd.Flags().GetString(secondFlag)
			}
			result, err := run(cmd, credentialID, second)
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			switch use {
			case "locations", "regions", "zones":
				for _, location := range result.Locations {
					fmt.Printf("%s  %s\n", location.Region, strings.Join(location.Zones, ","))
				}
			case "networks":
				for _, network := range result.Networks {
					fmt.Printf("%s  %-24s  %s  %s\n", network.ID, network.Name, network.Region, strings.Join(network.Subnets, ","))
				}
			case "gateway-types":
				for _, item := range result.GatewayTypes {
					fmt.Printf("%-16s  zone=%s  bandwidth=%d\n", item.Name, item.Zone, item.Bandwidth)
				}
			default:
				for _, item := range result.InstanceTypes {
					fmt.Printf("%-16s  cpu=%d  memory=%d  monthly_eur=%.2f  available=%t\n", item.Name, item.VCPUs, item.MemoryBytes, item.MonthlyEUR, item.Available)
				}
				for _, item := range result.StoragePrices {
					fmt.Printf("storage %-12s  gb_month_eur=%.4f\n", item.Type, item.GBMonthEUR)
				}
			}
			if !result.PricingComplete {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Warning: pricing is incomplete: %s\n", strings.Join(result.IncompleteReasons, "; ")); err != nil {
					return fmt.Errorf("writing pricing warning: %w", err)
				}
			}
			return nil
		},
	}
	command.Flags().String("credential-id", "", "Scaleway credential ID (required)")
	_ = command.MarkFlagRequired("credential-id")
	if secondFlag != "" {
		command.Flags().String(secondFlag, "", "Scaleway "+secondFlag+" (required)")
		_ = command.MarkFlagRequired(secondFlag)
	}
	registerStructuredOutputFlags(command)
	return command
}

func scalewaySimpleCommands() []*cobra.Command {
	deprovision := &cobra.Command{
		Use: "deprovision <cluster_id>", Short: "Deprovision a Scaleway Instances cluster", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			force, _ := cmd.Flags().GetBool("force")
			if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
				fmt.Sprintf("Deprovision Scaleway cluster %q? Compute, gateway, IPAM and network resources will be released subject to retention policy. [y/N]: ", args[0]), yes); err != nil {
				return err
			}
			result, err := activeScalewayAPI().DeprovisionScalewayCluster(args[0], force)
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Scaleway deprovision initiated for %s.\n", result.ClusterID)
			return nil
		},
	}
	deprovision.Flags().BoolP("yes", "y", false, "Skip confirmation")
	deprovision.Flags().Bool("force", false, "Force teardown through conflicting state")

	stop := &cobra.Command{
		Use: "stop <cluster_id>", Short: "Stop compute while retaining cluster configuration", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().StopScalewayCluster(args[0])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Scaleway cluster stop initiated (operation %s).\n", optionalString(result.OperationID))
			return nil
		},
	}
	start := &cobra.Command{
		Use: "start <cluster_id>", Short: "Start a stopped Scaleway cluster", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().StartScalewayCluster(args[0])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Scaleway start created %d operation(s).\n", result.CreatedOperations)
			return nil
		},
	}
	workers := &cobra.Command{
		Use: "workers <cluster_id>", Short: "Read the default worker count", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().GetScalewayWorkerCount(args[0])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Worker count: %d (min %d, max %d)\n", result.WorkerCount, result.Min, result.Max)
			return nil
		},
	}
	scale := &cobra.Command{
		Use: "scale <cluster_id> <count>", Short: "Scale the default worker group", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			count, err := strconv.Atoi(args[1])
			if err != nil {
				return withExitCode(exitUsage, fmt.Errorf("invalid count: %w", err))
			}
			result, err := activeScalewayAPI().ScaleScalewayWorkers(args[0], count)
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Worker count changed from %d to %d.\n", result.PreviousCount, result.NewCount)
			return nil
		},
	}
	version := &cobra.Command{
		Use: "k8s-version <cluster_id>", Short: "Read Kubernetes version", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().GetScalewayK8sVersion(args[0])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Kubernetes version: %s (%s)\n", optionalString(result.CurrentVersion), result.Distribution)
			return nil
		},
	}
	upgrade := &cobra.Command{
		Use: "upgrade <cluster_id> <target_version>", Short: "Upgrade Kubernetes", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			result, err := activeScalewayAPI().UpgradeScalewayK8sVersion(args[0], args[1], force)
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Kubernetes upgrade to %s scheduled; %d node(s) affected.\n", result.NewVersion, result.NodesAffected)
			return nil
		},
	}
	upgrade.Flags().Bool("force", false, "Bypass PodDisruptionBudget drain failures")
	access := &cobra.Command{
		Use: "access-info <cluster_id>", Short: "Show structured private access and ready-to-use SSH commands", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().GetScalewayAccessInfo(args[0])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Bastion: %s@%s:%d\n", result.BastionUser, result.BastionHost, result.BastionPort)
			fmt.Printf("Control planes: %s\n", strings.Join(result.ControlPlaneIPs, ", "))
			if result.ControlPlaneIP != nil {
				fmt.Printf("SSH: ssh -J %s@%s:%d %s@%s\n", result.BastionUser, result.BastionHost, result.BastionPort, result.TargetUser, *result.ControlPlaneIP)
				fmt.Printf("Port forward: ssh -p %d -L 6443:%s:6443 -N %s@%s\n", result.BastionPort, *result.ControlPlaneIP, result.BastionUser, result.BastionHost)
			}
			return nil
		},
	}
	registerStructuredOutputFlags(deprovision, stop, start, workers, scale, version, upgrade, access)
	return []*cobra.Command{deprovision, stop, start, workers, scale, version, upgrade, access}
}

func optionalString(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

func newScalewaySSHKeysCommand() *cobra.Command {
	root := &cobra.Command{Use: "ssh-keys", Aliases: []string{"ssh-key"}, Short: "Manage Scaleway cluster SSH keys"}
	get := &cobra.Command{
		Use: "get <cluster_id>", Short: "Read attached and available SSH-key credentials", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().GetScalewayClusterSSHKeys(args[0])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Attached SSH-key credential IDs: %s\n", strings.Join(result.SSHKeyCredentialIDs, ", "))
			return nil
		},
	}
	set := &cobra.Command{
		Use: "set <cluster_id>", Short: "Replace attached SSH-key credentials", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, _ := cmd.Flags().GetStringSlice("ssh-key-credential-ids")
			clear, _ := cmd.Flags().GetBool("clear")
			if clear {
				ids = []string{}
			} else if len(ids) == 0 {
				return withExitCode(exitUsage, errors.New("provide --ssh-key-credential-ids or --clear"))
			}
			result, err := activeScalewayAPI().UpdateScalewayClusterSSHKeys(args[0], ids)
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Println("SSH keys updated; running nodes are updated on reconciliation.")
			return nil
		},
	}
	set.Flags().StringSlice("ssh-key-credential-ids", nil, "SSH-key credential IDs")
	set.Flags().Bool("clear", false, "Remove user SSH keys")
	resync := &cobra.Command{
		Use: "resync <cluster_id>", Short: "Repair provider SSH-key state and reapply keys", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().ResyncScalewayClusterSSHKeys(args[0])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("SSH-key resync scheduled for %d resource(s).\n", len(result.ResourceIDs))
			return nil
		},
	}
	registerStructuredOutputFlags(get, set, resync)
	root.AddCommand(get, set, resync)
	return root
}

func newScalewayDayTwoCatalogCommand() *cobra.Command {
	root := &cobra.Command{Use: "catalog", Short: "Read cluster-scoped live resize catalogs"}
	instanceTypes := &cobra.Command{
		Use: "instance-types <cluster_id>", Short: "List instance types using stored provider context", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().GetScalewayClusterInstanceTypes(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			for _, item := range result.InstanceTypes {
				fmt.Printf("%-16s cpu=%d memory=%d available=%t\n", item.Name, item.VCPUs, item.MemoryBytes, item.Available)
			}
			return nil
		},
	}
	gatewayTypes := &cobra.Command{
		Use: "gateway-types <cluster_id>", Short: "List Public Gateway types using stored provider context", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().GetScalewayClusterGatewayTypes(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			for _, item := range result.GatewayTypes {
				fmt.Printf("%-16s zone=%s bandwidth=%d\n", item.Name, item.Zone, item.Bandwidth)
			}
			return nil
		},
	}
	registerStructuredOutputFlags(instanceTypes, gatewayTypes)
	root.AddCommand(instanceTypes, gatewayTypes)
	return root
}

func newScalewayNodeGroupCommand() *cobra.Command {
	root := &cobra.Command{Use: "node-group", Short: "Manage Scaleway node groups"}
	list := &cobra.Command{
		Use: "list <cluster_id>", Short: "List node groups", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().ListScalewayNodeGroups(args[0])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			for _, item := range result.NodeGroups {
				fmt.Printf("%-20s type=%-12s count=%d labels=%d taints=%d\n", item.Name, item.InstanceType, item.Count, len(item.Labels), len(item.Taints))
			}
			return nil
		},
	}
	add := &cobra.Command{
		Use: "add <cluster_id>", Short: "Add a node group", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			instanceType, _ := cmd.Flags().GetString("instance-type")
			count, _ := cmd.Flags().GetInt("count")
			labelsRaw, _ := cmd.Flags().GetString("labels")
			taintsRaw, _ := cmd.Flags().GetString("taints")
			labels, err := parseLabelsFlag(labelsRaw)
			if err != nil {
				return err
			}
			taints, err := parseTaintsFlag(taintsRaw)
			if err != nil {
				return err
			}
			ctx, cancel, wait, err := nodeGroupAsyncContext(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			result, submitted, err := activeScalewayAPI().AddScalewayNodeGroupFull(ctx, args[0], client.ScalewayCreateNodeGroupRequest{
				Name: name, InstanceType: instanceType, Count: count, Labels: labels, Taints: taints,
			}, wait)
			if err != nil {
				return asyncWriteError("adding Scaleway node group", wait, err)
			}
			if submitted {
				return renderAsyncWriteSubmitted(cmd, "Node group add", nil)
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Node group %q created with %d node(s).\n", result.GroupName, result.Count)
			return nil
		},
	}
	add.Flags().String("name", "", "Node group name (required)")
	add.Flags().String("instance-type", "", "Instance type (required)")
	add.Flags().Int("count", 1, "Node count")
	add.Flags().String("labels", "", "Comma-separated key=value labels")
	add.Flags().String("taints", "", "Comma-separated key=value:Effect taints")
	_ = add.MarkFlagRequired("name")
	_ = add.MarkFlagRequired("instance-type")
	registerAsyncWriteFlags(add)

	scale := &cobra.Command{
		Use: "scale <cluster_id> <group_name> <count>", Short: "Scale a node group", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			count, err := strconv.Atoi(args[2])
			if err != nil {
				return withExitCode(exitUsage, err)
			}
			ctx, cancel, wait, err := nodeGroupAsyncContext(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			result, submitted, err := activeScalewayAPI().ScaleScalewayNodeGroup(ctx, args[0], args[1], count, wait)
			if err != nil {
				return asyncWriteError("scaling Scaleway node group", wait, err)
			}
			if submitted {
				return renderAsyncWriteSubmitted(cmd, "Node group scale", nil)
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Node group %q scaled to %d.\n", result.GroupName, result.NewCount)
			return nil
		},
	}
	resize := &cobra.Command{
		Use: "upgrade <cluster_id> <group_name> <instance_type>", Short: "Replace a node group with a new instance type", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel, wait, err := nodeGroupAsyncContext(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			result, submitted, err := activeScalewayAPI().UpdateScalewayNodeGroupInstanceType(ctx, args[0], args[1], args[2], wait)
			if err != nil {
				return asyncWriteError("resizing Scaleway node group", wait, err)
			}
			if submitted {
				return renderAsyncWriteSubmitted(cmd, "Node group instance-type update", nil)
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Node group %q resized; %d node(s) affected.\n", result.GroupName, result.Updated)
			return nil
		},
	}
	remove := &cobra.Command{
		Use: "delete <cluster_id> <group_name>", Short: "Delete a node group", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), fmt.Sprintf("Delete node group %q and all its nodes? [y/N]: ", args[1]), yes); err != nil {
				return err
			}
			ctx, cancel, wait, err := nodeGroupAsyncContext(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			result, submitted, err := activeScalewayAPI().DeleteScalewayNodeGroup(ctx, args[0], args[1], wait)
			if err != nil {
				return asyncWriteError("deleting Scaleway node group", wait, err)
			}
			if submitted {
				return renderAsyncWriteSubmitted(cmd, "Node group delete", nil)
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Node group %q deleted.\n", result.GroupName)
			return nil
		},
	}
	remove.Flags().BoolP("yes", "y", false, "Skip confirmation")
	for _, command := range []*cobra.Command{scale, resize, remove} {
		registerAsyncWriteFlags(command)
	}

	labels := &cobra.Command{
		Use: "labels <cluster_id> <group_name>", Short: "Replace node-group labels", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clear, _ := cmd.Flags().GetBool("clear")
			raw, _ := cmd.Flags().GetString("labels")
			if clear && cmd.Flags().Changed("labels") {
				return withExitCode(exitUsage, errors.New("use either --labels or --clear"))
			}
			if !clear && !cmd.Flags().Changed("labels") {
				return withExitCode(exitUsage, errors.New("provide --labels or --clear"))
			}
			values, err := parseLabelsFlag(raw)
			if err != nil {
				return err
			}
			ctx, cancel, wait, err := nodeGroupAsyncContext(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			result, submitted, err := activeScalewayAPI().UpdateScalewayNodeGroupLabels(ctx, args[0], args[1], values, wait)
			if err != nil {
				return asyncWriteError("updating Scaleway node-group labels", wait, err)
			}
			if submitted {
				return renderAsyncWriteSubmitted(cmd, "Node group labels update", nil)
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Node group %q labels updated on %d node(s).\n", result.GroupName, result.Updated)
			return nil
		},
	}
	labels.Flags().String("labels", "", "Comma-separated key=value labels")
	labels.Flags().Bool("clear", false, "Remove all labels")
	registerAsyncWriteFlags(labels)

	taints := &cobra.Command{
		Use: "taints <cluster_id> <group_name>", Short: "Replace node-group taints", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clear, _ := cmd.Flags().GetBool("clear")
			raw, _ := cmd.Flags().GetString("taints")
			if clear && cmd.Flags().Changed("taints") {
				return withExitCode(exitUsage, errors.New("use either --taints or --clear"))
			}
			if !clear && !cmd.Flags().Changed("taints") {
				return withExitCode(exitUsage, errors.New("provide --taints or --clear"))
			}
			values, err := parseTaintsFlag(raw)
			if err != nil {
				return err
			}
			ctx, cancel, wait, err := nodeGroupAsyncContext(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			result, submitted, err := activeScalewayAPI().UpdateScalewayNodeGroupTaints(ctx, args[0], args[1], values, wait)
			if err != nil {
				return asyncWriteError("updating Scaleway node-group taints", wait, err)
			}
			if submitted {
				return renderAsyncWriteSubmitted(cmd, "Node group taints update", nil)
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Node group %q taints updated on %d node(s).\n", result.GroupName, result.Updated)
			return nil
		},
	}
	taints.Flags().String("taints", "", "Comma-separated key=value:Effect taints")
	taints.Flags().Bool("clear", false, "Remove all taints")
	registerAsyncWriteFlags(taints)

	autoscaling := &cobra.Command{Use: "autoscaling", Short: "Read or update node-group autoscaling"}
	autoscalingGet := &cobra.Command{
		Use: "get <cluster_id> <group_name>", Short: "Read autoscaling settings", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := activeScalewayAPI().GetScalewayNodeGroupAutoscaling(args[0], args[1])
			if err != nil {
				return err
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Node group %q autoscaling=%t min=%d max=%d\n", result.GroupName, result.Enabled, result.MinCount, result.MaxCount)
			return nil
		},
	}
	autoscalingSet := &cobra.Command{
		Use: "set <cluster_id> <group_name>", Short: "Update autoscaling settings", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			enabled, _ := cmd.Flags().GetBool("enabled")
			minimum, _ := cmd.Flags().GetInt("min")
			maximum, _ := cmd.Flags().GetInt("max")
			ctx, cancel, wait, err := nodeGroupAsyncContext(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			result, submitted, err := activeScalewayAPI().UpdateScalewayNodeGroupAutoscaling(ctx, args[0], args[1],
				client.NodeGroupAutoscalingRequest{Enabled: enabled, MinCount: minimum, MaxCount: maximum}, wait)
			if err != nil {
				return asyncWriteError("updating Scaleway node-group autoscaling", wait, err)
			}
			if submitted {
				return renderAsyncWriteSubmitted(cmd, "Node group autoscaling update", nil)
			}
			if handled, err := renderStructured(cmd, result); handled || err != nil {
				return err
			}
			fmt.Printf("Node group %q autoscaling=%t min=%d max=%d\n", result.GroupName, result.Enabled, result.MinCount, result.MaxCount)
			return nil
		},
	}
	autoscalingSet.Flags().Bool("enabled", true, "Enable autoscaling")
	autoscalingSet.Flags().Int("min", 1, "Minimum node count")
	autoscalingSet.Flags().Int("max", 5, "Maximum node count")
	registerAsyncWriteFlags(autoscalingSet)
	registerStructuredOutputFlags(autoscalingGet, autoscalingSet)
	autoscaling.AddCommand(autoscalingGet, autoscalingSet)

	registerStructuredOutputFlags(list, add, scale, resize, remove, labels, taints)
	root.AddCommand(list, add, scale, resize, remove, labels, taints, autoscaling)
	return root
}

func init() {
	create := newScalewayCreateCommand(false)
	preflight := newScalewayCreateCommand(true)
	scalewayCmd.AddCommand(create, preflight)
	scalewayCmd.AddCommand(scalewaySimpleCommands()...)
	scalewayCmd.AddCommand(newScalewayNodeGroupCommand())
	scalewayCmd.AddCommand(newScalewaySSHKeysCommand())
	scalewayCmd.AddCommand(newScalewayDayTwoCatalogCommand())

	scalewayCmd.AddCommand(newScalewayCatalogCommand("locations", "List Scaleway regions and zones",
		func(cmd *cobra.Command, credentialID, _ string) (*client.ScalewayCatalogResult, error) {
			return activeScalewayAPI().ListScalewayLocations(cmd.Context(), credentialID)
		}, ""))
	scalewayCmd.AddCommand(newScalewayCatalogCommand("regions", "List Scaleway regions",
		func(cmd *cobra.Command, credentialID, _ string) (*client.ScalewayCatalogResult, error) {
			return activeScalewayAPI().ListScalewayRegions(cmd.Context(), credentialID)
		}, ""))
	scalewayCmd.AddCommand(newScalewayCatalogCommand("zones", "List Scaleway zones",
		func(cmd *cobra.Command, credentialID, _ string) (*client.ScalewayCatalogResult, error) {
			return activeScalewayAPI().ListScalewayZones(cmd.Context(), credentialID)
		}, ""))
	scalewayCmd.AddCommand(newScalewayCatalogCommand("networks", "List regional Private Networks",
		func(cmd *cobra.Command, credentialID, region string) (*client.ScalewayCatalogResult, error) {
			return activeScalewayAPI().ListScalewayNetworks(cmd.Context(), credentialID, region)
		}, "region"))
	scalewayCmd.AddCommand(newScalewayCatalogCommand("instance-types", "List zonal Instance types and prices",
		func(cmd *cobra.Command, credentialID, zone string) (*client.ScalewayCatalogResult, error) {
			return activeScalewayAPI().ListScalewayInstanceTypes(cmd.Context(), credentialID, zone)
		}, "zone"))
	scalewayCmd.AddCommand(newScalewayCatalogCommand("gateway-types", "List zonal Public Gateway v2 types",
		func(cmd *cobra.Command, credentialID, zone string) (*client.ScalewayCatalogResult, error) {
			return activeScalewayAPI().ListScalewayGatewayTypes(cmd.Context(), credentialID, zone)
		}, "zone"))
	scalewayCmd.AddCommand(newScalewayCatalogCommand("pricing", "List live Scaleway compute and storage pricing",
		func(cmd *cobra.Command, credentialID, zone string) (*client.ScalewayCatalogResult, error) {
			return activeScalewayAPI().ListScalewayPricing(cmd.Context(), credentialID, zone)
		}, "zone"))

	clusterCmd.AddCommand(scalewayCmd)
}

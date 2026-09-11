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

var awsCmd = &cobra.Command{
	Use:   "aws",
	Short: "Manage self-managed AWS clusters",
	Long: `Manage the lifecycle of self-managed Kubernetes clusters Ankra builds on EC2.

These are k3s (or kubeadm) clusters on plain EC2 instances inside a VPC you
already own, not EKS. For an EKS control plane use 'ankra cluster managed'.`,
}

var awsStopCmd = &cobra.Command{
	Use:   "stop <cluster_id|name>",
	Short: "Stop an AWS cluster",
	Long:  "Stop an AWS cluster by terminating its EC2 instances while preserving its configuration so it can be re-provisioned later.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, arguments []string) error {
		clusterID, resolveError := resolveClusterArg(arguments[0])
		if resolveError != nil {
			return resolveError
		}
		force, _ := cmd.Flags().GetBool("force")
		result, stopError := apiClient.StopAwsCluster(clusterID, force)
		if stopError != nil {
			return fmt.Errorf("stopping AWS cluster: %w", stopError)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("AWS cluster stop initiated."))
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

var awsStartCmd = &cobra.Command{
	Use:   "start <cluster_id|name>",
	Short: "Start a stopped AWS cluster",
	Long:  "Re-provision a stopped AWS cluster. Use --scope control_plane to bring up only the control plane.",
	Args:  cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		clusterID, resolveError := resolveClusterArg(arguments[0])
		if resolveError != nil {
			return resolveError
		}
		scope, scopeError := command.Flags().GetString("scope")
		if scopeError != nil {
			return fmt.Errorf("reading scope: %w", scopeError)
		}
		if scope != "all" && scope != "control_plane" {
			return fmt.Errorf("invalid --scope %q: must be 'all' or 'control_plane'", scope)
		}

		result, startError := apiClient.StartAwsCluster(clusterID, scope)
		if startError != nil {
			return fmt.Errorf("starting AWS cluster: %w", startError)
		}

		fmt.Println(text.FgGreen.Sprint("AWS cluster start initiated."))
		fmt.Printf("  Scope: %s\n", result.Scope)
		if result.MarkedToStartAt != "" {
			fmt.Printf("  Marked to start at: %s\n", result.MarkedToStartAt)
		}
		fmt.Printf("  Created operations: %d\n", result.CreatedOperations)
		return nil
	},
}

func init() {
	awsStartCmd.Flags().String("scope", "all", "Provisioning scope: 'all' or 'control_plane'")
	awsStopCmd.Flags().Bool("force", false, "Force stop: cancel every in-flight operation and block new operations for 60 seconds while the stop lands, and also delete the cluster's tagged EBS volumes and load balancers even when retention_policy is retain (destroys persisted data)")
	registerAwsCreateFlags(awsCreateCmd, awsPreflightCmd)
	registerAwsCatalogFlags(false, false, awsRegionsCmd)
	registerAwsCatalogFlags(true, false, awsInstanceTypesCmd, awsVpcsCmd, awsAvailabilityZonesCmd, awsImagesCmd, awsPricingCmd)
	registerAwsCatalogFlags(true, true, awsSubnetsCmd)
	awsDeprovisionCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(
		awsCreateCmd, awsPreflightCmd, awsDeprovisionCmd,
		awsWorkersCmd, awsK8sVersionCmd, awsAccessInfoCmd,
		awsRegionsCmd, awsInstanceTypesCmd, awsVpcsCmd, awsSubnetsCmd,
		awsAvailabilityZonesCmd, awsImagesCmd, awsPricingCmd,
	)

	awsCmd.AddCommand(awsCreateCmd)
	awsCmd.AddCommand(awsPreflightCmd)
	awsCmd.AddCommand(awsDeprovisionCmd)
	awsCmd.AddCommand(awsStopCmd)
	awsCmd.AddCommand(awsStartCmd)
	awsCmd.AddCommand(awsWorkersCmd)
	awsCmd.AddCommand(awsK8sVersionCmd)
	awsCmd.AddCommand(awsAccessInfoCmd)
	awsCmd.AddCommand(awsRegionsCmd)
	awsCmd.AddCommand(awsInstanceTypesCmd)
	awsCmd.AddCommand(awsVpcsCmd)
	awsCmd.AddCommand(awsSubnetsCmd)
	awsCmd.AddCommand(awsAvailabilityZonesCmd)
	awsCmd.AddCommand(awsImagesCmd)
	awsCmd.AddCommand(awsPricingCmd)
	awsCmd.AddCommand(newControlPlaneCmd(awsControlPlaneOps, "AWS", "aws instance-types"))
	clusterCmd.AddCommand(awsCmd)
}

// awsCreateRequestFromFlags builds the create body shared by `create` and
// `preflight`. Flags left at their zero value are omitted so the server's
// documented defaults apply (k3s, stacked etcd, retain, the platform's
// default instance types and Ubuntu series, egress mode auto-detected from
// the node subnets).
func awsCreateRequestFromFlags(cmd *cobra.Command) client.CreateAwsClusterRequest {
	name, _ := cmd.Flags().GetString("name")
	description, _ := cmd.Flags().GetString("description")
	credentialID, _ := cmd.Flags().GetString("credential-id")
	sshKeyCredentialID, _ := cmd.Flags().GetString("ssh-key-credential-id")
	region, _ := cmd.Flags().GetString("region")
	vpcID, _ := cmd.Flags().GetString("vpc-id")
	nodeSubnetIDs, _ := cmd.Flags().GetStringSlice("node-subnet-ids")
	bastionSubnetID, _ := cmd.Flags().GetString("bastion-subnet-id")
	egressMode, _ := cmd.Flags().GetString("egress-mode")
	bastionInstanceType, _ := cmd.Flags().GetString("bastion-instance-type")
	bastionAllowedIPs, _ := cmd.Flags().GetStringSlice("bastion-allowed-ips")
	controlPlaneCount, _ := cmd.Flags().GetInt("control-plane-count")
	controlPlaneType, _ := cmd.Flags().GetString("control-plane-type")
	workerType, _ := cmd.Flags().GetString("worker-type")
	distribution, _ := cmd.Flags().GetString("distribution")
	kubernetesVersion, _ := cmd.Flags().GetString("kubernetes-version")
	etcdTopology, _ := cmd.Flags().GetString("etcd-topology")
	etcdNodeCount, _ := cmd.Flags().GetInt("etcd-node-count")
	etcdType, _ := cmd.Flags().GetString("etcd-type")
	cni, _ := cmd.Flags().GetString("cni")
	cniFeatures, _ := cmd.Flags().GetStringSlice("cni-features")
	k3sDisabledComponents, _ := cmd.Flags().GetStringSlice("k3s-disabled-components")
	ubuntuSeries, _ := cmd.Flags().GetString("ubuntu-series")
	architecture, _ := cmd.Flags().GetString("architecture")
	rootVolumeGiB, _ := cmd.Flags().GetInt("root-volume-gib")
	gitopsCredentialName, _ := cmd.Flags().GetString("gitops-credential-name")
	gitopsRepository, _ := cmd.Flags().GetString("gitops-repository")
	gitopsBranch, _ := cmd.Flags().GetString("gitops-branch")
	retentionPolicy, _ := cmd.Flags().GetString("retention-policy")
	classification, _ := cmd.Flags().GetString("classification")

	request := client.CreateAwsClusterRequest{
		Name:                  name,
		CredentialID:          credentialID,
		SSHKeyCredentialID:    sshKeyCredentialID,
		Region:                region,
		VpcID:                 vpcID,
		NodeSubnetIDs:         nodeSubnetIDs,
		BastionSubnetID:       bastionSubnetID,
		EgressMode:            egressMode,
		BastionInstanceType:   bastionInstanceType,
		BastionAllowedIPs:     bastionAllowedIPs,
		ControlPlaneCount:     controlPlaneCount,
		ControlPlaneType:      controlPlaneType,
		WorkerType:            workerType,
		Distribution:          distribution,
		EtcdTopology:          etcdTopology,
		EtcdNodeCount:         etcdNodeCount,
		EtcdType:              etcdType,
		CNI:                   cni,
		CNIFeatures:           cniFeatures,
		K3sDisabledComponents: k3sDisabledComponents,
		UbuntuSeries:          ubuntuSeries,
		Architecture:          architecture,
		RootVolumeGiB:         rootVolumeGiB,
		RetentionPolicy:       retentionPolicy,
		Classification:        classification,
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
	if kubernetesVersion != "" {
		request.KubernetesVersion = &kubernetesVersion
	}
	if gitopsCredentialName != "" {
		request.GitopsCredentialName = &gitopsCredentialName
	}
	if gitopsRepository != "" {
		request.GitopsRepository = &gitopsRepository
	}
	if gitopsBranch != "" {
		request.GitopsBranch = &gitopsBranch
	}
	return request
}

var awsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new self-managed AWS (EC2) cluster",
	Long: `Create an Ankra-managed k3s or kubeadm cluster on EC2 instances inside a VPC
you already own.

Ankra adopts the VPC, the node subnets and the bastion subnet - it never
creates AWS networking - and owns the instances, security groups, the
generated SSH key and the bastion. Egress for the nodes is detected from the
node subnets' route tables unless --egress-mode pins it: 'existing' uses the
NAT gateway or internet gateway the subnets already route through,
'bastion_nat' makes the bastion the nodes' NAT. Run 'preflight' first to
check the region, VPC, subnets, instance-type availability and the resolved
egress mode.

Examples:
  ankra cluster aws create --name prod --credential-id <id> \
    --ssh-key-credential-id <id> --region eu-north-1 --vpc-id vpc-0abc \
    --node-subnet-ids subnet-0aaa,subnet-0bbb --bastion-subnet-id subnet-0ccc \
    --bastion-allowed-ips 203.0.113.0/24`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, createError := apiClient.CreateAwsCluster(awsCreateRequestFromFlags(cmd))
		if createError != nil {
			return fmt.Errorf("creating AWS cluster: %w", createError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Printf("AWS cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
		return nil
	},
}

var awsPreflightCmd = &cobra.Command{
	Use:   "preflight",
	Short: "Validate an AWS cluster create request without provisioning",
	Long: `Run the AWS create preflight: credential and region reachability, VPC and
subnet membership, bastion subnet routing, instance-type and AMI
availability, and the egress mode the server resolves when --egress-mode is
left unset.

Takes the same flags as 'create'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, preflightError := apiClient.PreflightAwsCluster(awsCreateRequestFromFlags(cmd))
		if preflightError != nil {
			return fmt.Errorf("running AWS preflight: %w", preflightError)
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

		if result.ResolvedEgressMode != "" {
			fmt.Printf("\nResolved egress mode: %s\n", result.ResolvedEgressMode)
		}
		if result.CanProceed {
			fmt.Println(text.FgGreen.Sprint("\nPreflight passed: the cluster can be created."))
			return nil
		}
		return fmt.Errorf("preflight failed: resolve the checks above before creating the cluster")
	},
}

var awsDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id|name>",
	Short: "Deprovision an AWS cluster and release its EC2 resources",
	Long: `Permanently delete an AWS cluster and the provider resources Ankra created
for it: the instances, security groups, bastion and generated SSH key. The
adopted VPC and subnets are never touched. EBS volumes and load balancers
follow the cluster's retention_policy: 'retain' keeps them, 'delete' sweeps
the tagged orphans.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		awsDeprovisionYes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Deprovision AWS cluster %s? This permanently deletes its EC2 resources and the cluster record! [y/N]: ",
				clusterTarget(args[0], clusterID)),
			awsDeprovisionYes); confirmError != nil {
			return confirmError
		}
		result, deprovisionError := apiClient.DeprovisionAwsCluster(clusterID)
		if deprovisionError != nil {
			return fmt.Errorf("deprovisioning AWS cluster: %w", deprovisionError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Println(text.FgGreen.Sprint("AWS cluster deprovision initiated."))
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if result.OperationID != nil {
			fmt.Printf("  Operation ID: %s\n", *result.OperationID)
		}
		return nil
	},
}

var awsWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id|name>",
	Short: "Get current worker count for an AWS cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		result, fetchError := apiClient.GetAwsWorkerCount(clusterID)
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

var awsK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id|name>",
	Short: "Get current Kubernetes version for an AWS cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		result, fetchError := apiClient.GetAwsK8sVersion(clusterID)
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

var awsAccessInfoCmd = &cobra.Command{
	Use:   "access-info <cluster_id|name>",
	Short: "Show SSH access details for an AWS cluster",
	Long:  "Show the bastion and control plane IPs plus ready-to-use SSH jump and Kubernetes API port-forward commands.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		result, fetchError := apiClient.GetAwsAccessInfo(clusterID)
		if fetchError != nil {
			return fmt.Errorf("fetching access info: %w", fetchError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		bastionIP := "-"
		if result.BastionIP != nil && *result.BastionIP != "" {
			bastionIP = *result.BastionIP
		}
		controlPlaneIP := "-"
		if result.ControlPlaneIP != nil && *result.ControlPlaneIP != "" {
			controlPlaneIP = *result.ControlPlaneIP
		}
		if result.ClusterName != nil && *result.ClusterName != "" {
			fmt.Printf("Cluster: %s\n", *result.ClusterName)
		}
		fmt.Printf("Bastion IP: %s\n", bastionIP)
		fmt.Printf("Control Plane IP: %s\n", controlPlaneIP)
		if len(result.ControlPlaneIPs) > 0 {
			fmt.Printf("Control Plane IPs: %s\n", strings.Join(result.ControlPlaneIPs, ", "))
		}
		if bastionIP != "-" && controlPlaneIP != "-" {
			fmt.Printf("\nSSH jump:\n  ssh -J ubuntu@%s ubuntu@%s\n", bastionIP, controlPlaneIP)
			fmt.Printf("Kubernetes API port-forward:\n  ssh -L 6443:%s:6443 ubuntu@%s\n", controlPlaneIP, bastionIP)
		}
		return nil
	},
}

func awsCatalogArgs(cmd *cobra.Command) (credentialID, region, vpcID string) {
	credentialID, _ = cmd.Flags().GetString("credential-id")
	region, _ = cmd.Flags().GetString("region")
	vpcID, _ = cmd.Flags().GetString("vpc-id")
	return credentialID, region, vpcID
}

var awsRegionsCmd = &cobra.Command{
	Use:   "regions",
	Short: "List AWS regions available to a credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _, _ := awsCatalogArgs(cmd)
		result, listError := apiClient.ListAwsRegions(credentialID)
		if listError != nil {
			return fmt.Errorf("listing AWS regions: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Regions) == 0 {
			fmt.Println("No regions available for this credential.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Region", "Name"})
		for _, region := range result.Regions {
			t.AppendRow(table.Row{region.Name, region.DisplayName})
		}
		t.Render()
		return nil
	},
}

func renderAwsInstanceTypes(result *client.AwsCatalogResult, emptyMessage string) {
	if len(result.InstanceTypes) == 0 {
		fmt.Println(emptyMessage)
		return
	}
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"Name", "vCPU", "Memory (GiB)", "Arch", "Hourly", "Monthly"})
	for _, instanceType := range result.InstanceTypes {
		currency := strings.ToUpper(instanceType.Currency)
		t.AppendRow(table.Row{
			instanceType.Name, instanceType.VCPUs, instanceType.MemoryGiB,
			instanceType.Architecture,
			fmt.Sprintf("%.4f %s", instanceType.HourlyPrice, currency),
			fmt.Sprintf("%.2f %s", instanceType.MonthlyPrice, currency),
		})
	}
	t.Render()
	if !result.PricingComplete && len(result.IncompleteReasons) > 0 {
		fmt.Println(text.FgYellow.Sprintf("\nPricing is incomplete: %s", strings.Join(result.IncompleteReasons, "; ")))
	}
}

var awsInstanceTypesCmd = &cobra.Command{
	Use:   "instance-types",
	Short: "List EC2 instance types available in a region",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, region, _ := awsCatalogArgs(cmd)
		result, listError := apiClient.ListAwsInstanceTypes(credentialID, region)
		if listError != nil {
			return fmt.Errorf("listing AWS instance types: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		renderAwsInstanceTypes(result, "No instance types available for this credential and region.")
		return nil
	},
}

var awsVpcsCmd = &cobra.Command{
	Use:   "vpcs",
	Short: "List VPCs a credential can adopt in a region",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, region, _ := awsCatalogArgs(cmd)
		result, listError := apiClient.ListAwsVpcs(credentialID, region)
		if listError != nil {
			return fmt.Errorf("listing AWS VPCs: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Vpcs) == 0 {
			fmt.Println("No VPCs available for this credential and region.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "CIDR", "Default"})
		for _, vpc := range result.Vpcs {
			isDefault := ""
			if vpc.IsDefault {
				isDefault = "yes"
			}
			t.AppendRow(table.Row{vpc.ID, vpc.Name, vpc.CIDR, isDefault})
		}
		t.Render()
		return nil
	},
}

var awsSubnetsCmd = &cobra.Command{
	Use:   "subnets",
	Short: "List the subnets of a VPC",
	Long: `List the subnets of a VPC with their availability zone and whether they
route to an internet gateway. Nodes go in private subnets (egress via a NAT
gateway, or via the bastion with --egress-mode bastion_nat); the bastion
needs a public one.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, region, vpcID := awsCatalogArgs(cmd)
		result, listError := apiClient.ListAwsSubnets(credentialID, region, vpcID)
		if listError != nil {
			return fmt.Errorf("listing AWS subnets: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Subnets) == 0 {
			fmt.Println("No subnets found for this VPC.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"ID", "Name", "CIDR", "Zone", "Public"})
		for _, subnet := range result.Subnets {
			public := "no"
			if subnet.Public {
				public = "yes"
			}
			t.AppendRow(table.Row{subnet.ID, subnet.Name, subnet.CIDR, subnet.AvailabilityZone, public})
		}
		t.Render()
		return nil
	},
}

var awsAvailabilityZonesCmd = &cobra.Command{
	Use:   "availability-zones",
	Short: "List the availability zones of a region",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, region, _ := awsCatalogArgs(cmd)
		result, listError := apiClient.ListAwsAvailabilityZones(credentialID, region)
		if listError != nil {
			return fmt.Errorf("listing AWS availability zones: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.AvailabilityZones) == 0 {
			fmt.Println("No availability zones found for this region.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"Zone", "Zone ID", "State"})
		for _, zone := range result.AvailabilityZones {
			t.AppendRow(table.Row{zone.Name, zone.ZoneID, zone.State})
		}
		t.Render()
		return nil
	},
}

var awsImagesCmd = &cobra.Command{
	Use:   "images",
	Short: "List the Ubuntu AMIs nodes can boot from in a region",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, region, _ := awsCatalogArgs(cmd)
		result, listError := apiClient.ListAwsImages(credentialID, region)
		if listError != nil {
			return fmt.Errorf("listing AWS images: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Images) == 0 {
			fmt.Println("No images found for this region.")
			return nil
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{"AMI", "Name", "Ubuntu", "Arch", "Created"})
		for _, image := range result.Images {
			t.AppendRow(table.Row{image.ID, image.Name, image.UbuntuSeries, image.Architecture, image.CreatedAt})
		}
		t.Render()
		return nil
	},
}

var awsPricingCmd = &cobra.Command{
	Use:   "pricing",
	Short: "Show the on-demand prices a cluster in a region is estimated from",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, region, _ := awsCatalogArgs(cmd)
		result, listError := apiClient.ListAwsPricing(credentialID, region)
		if listError != nil {
			return fmt.Errorf("listing AWS pricing: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Pricing) == 0 && len(result.InstanceTypes) == 0 {
			fmt.Println("No pricing available for this credential and region.")
			return nil
		}
		if len(result.Pricing) > 0 {
			t := table.NewWriter()
			t.SetOutputMirror(os.Stdout)
			t.SetStyle(table.StyleRounded)
			t.AppendHeader(table.Row{"Item", "Hourly", "Monthly"})
			for _, item := range result.Pricing {
				currency := strings.ToUpper(item.Currency)
				t.AppendRow(table.Row{
					item.Item,
					fmt.Sprintf("%.4f %s", item.HourlyPrice, currency),
					fmt.Sprintf("%.2f %s", item.MonthlyPrice, currency),
				})
			}
			t.Render()
		}
		if len(result.InstanceTypes) > 0 {
			renderAwsInstanceTypes(result, "")
		} else if !result.PricingComplete && len(result.IncompleteReasons) > 0 {
			fmt.Println(text.FgYellow.Sprintf("\nPricing is incomplete: %s", strings.Join(result.IncompleteReasons, "; ")))
		}
		return nil
	},
}

// registerAwsCreateFlags declares the create/preflight flag set once so the
// two commands can never drift.
func registerAwsCreateFlags(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().String("name", "", "Cluster name (required)")
		command.Flags().String("description", "", "Cluster description")
		command.Flags().String("credential-id", "", "AWS credential ID - an access key pair or an assumable role (required)")
		command.Flags().String("ssh-key-credential-id", "", "SSH key credential ID (required)")
		command.Flags().String("region", "", "AWS region, e.g. eu-north-1 (required)")
		command.Flags().String("vpc-id", "", "Existing VPC to build the cluster in; Ankra never creates one (required)")
		command.Flags().StringSlice("node-subnet-ids", nil, "Subnets the control plane and workers are spread across, comma-separated (required)")
		command.Flags().String("bastion-subnet-id", "", "Public subnet the bastion is placed in (required)")
		command.Flags().StringSlice("bastion-allowed-ips", nil, "CIDRs allowed to reach the bastion over SSH, comma-separated (required)")
		command.Flags().String("egress-mode", "", "How the nodes reach the internet: 'existing' (the subnets' own NAT or internet gateway) or 'bastion_nat' (the bastion is the NAT); default: detected from the node subnets' route tables")
		command.Flags().String("bastion-instance-type", "", "Bastion instance type (server default)")
		command.Flags().Int("control-plane-count", 0, "Control plane node count (server default: 1)")
		command.Flags().String("control-plane-type", "", "Control plane instance type (server default)")
		command.Flags().Int("worker-count", 0, "Default-pool worker count (server default: 1)")
		command.Flags().String("worker-type", "", "Worker instance type (server default)")
		command.Flags().String("distribution", "", "Kubernetes distribution: k3s or kubeadm (server default: k3s)")
		command.Flags().String("kubernetes-version", "", "Pin a Kubernetes version (default: latest supported)")
		command.Flags().String("etcd-topology", "", "etcd topology: stacked or external (server default: stacked)")
		command.Flags().Int("etcd-node-count", 0, "External etcd node count, 3 or 5 (server default: 3)")
		command.Flags().String("etcd-type", "", "External etcd instance type (server default)")
		command.Flags().String("cni", "", "CNI plugin (default: the platform default; immutable after create)")
		command.Flags().StringSlice("cni-features", nil, "CNI features to enable, comma-separated (default: the platform default)")
		command.Flags().StringSlice("k3s-disabled-components", nil, "k3s packaged components to disable, comma-separated (e.g. traefik,servicelb)")
		command.Flags().String("ubuntu-series", "", "Ubuntu series for the node AMI (server default)")
		command.Flags().String("architecture", "", "Node CPU architecture: x86_64 or arm64 (server default: x86_64)")
		command.Flags().Int("root-volume-gib", 0, "Root EBS volume size in GiB per node (server default)")
		command.Flags().String("gitops-credential-name", "", "GitOps GitHub credential name; when set with --gitops-repository, the generated stack is committed to Git")
		command.Flags().String("gitops-repository", "", "GitOps repository (owner/repo) the generated stack is committed to")
		command.Flags().String("gitops-branch", "", "GitOps branch (server default: master)")
		command.Flags().String("retention-policy", "", "Teardown policy for EBS volumes and load balancers: retain or delete (server default: retain)")
		command.Flags().String("classification", "", "Cluster classification label, e.g. production or development")
		command.Flags().Bool("include-networking", true, "Provision the networking stack (default on; pass --include-networking=false to skip)")
		registerIncludeDNSFlag(command)
		_ = command.MarkFlagRequired("name")
		_ = command.MarkFlagRequired("credential-id")
		_ = command.MarkFlagRequired("ssh-key-credential-id")
		_ = command.MarkFlagRequired("region")
		_ = command.MarkFlagRequired("vpc-id")
		_ = command.MarkFlagRequired("node-subnet-ids")
		_ = command.MarkFlagRequired("bastion-subnet-id")
		_ = command.MarkFlagRequired("bastion-allowed-ips")
	}
}

// registerAwsCatalogFlags declares the credential-scoped catalog flags. The
// regions catalog needs only the credential; every other one is regional,
// and subnets are additionally scoped to one VPC.
func registerAwsCatalogFlags(regional, vpcScoped bool, commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().String("credential-id", "", "AWS credential ID (required)")
		_ = command.MarkFlagRequired("credential-id")
		if regional {
			command.Flags().String("region", "", "AWS region, e.g. eu-north-1 (required)")
			_ = command.MarkFlagRequired("region")
		}
		if vpcScoped {
			command.Flags().String("vpc-id", "", "VPC to list the subnets of (required)")
			_ = command.MarkFlagRequired("vpc-id")
		}
	}
}

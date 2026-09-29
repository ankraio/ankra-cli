package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var ankraCloudCmd = &cobra.Command{
	Use:     "ankracloud",
	Aliases: []string{"ankra-cloud"},
	Short:   "Manage self-managed Ankra Cloud clusters",
	Long: `Manage kubeadm or k3s clusters Ankra builds on Ankra Cloud servers.

For Ankra Cloud Kubernetes, where the Ankra team runs the control plane, use
'ankra cluster managed ... --provider ankracloud_k8s' instead. Both take the
same Ankra Cloud credential ('ankra credentials ankracloud create').`,
}

var ankraCloudStopCmd = &cobra.Command{
	Use:   "stop <cluster_id|name>",
	Short: "Stop an Ankra Cloud cluster",
	Long:  "Stop an Ankra Cloud cluster. A plain stop powers the servers off and keeps them with their disks; only a forced stop or a deprovision deletes servers.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, arguments []string) error {
		clusterID, resolveError := resolveClusterArg(arguments[0])
		if resolveError != nil {
			return resolveError
		}
		force, _ := cmd.Flags().GetBool("force")
		stopMode, modeError := stopModeFlag(cmd)
		if modeError != nil {
			return modeError
		}
		stopOptions := client.StopClusterOptions{Force: force, PreserveState: preserveStateFlag(cmd), Mode: stopMode}

		result, stopError := apiClient.StopAnkraCloudCluster(clusterID, stopOptions)
		if stopError != nil {
			return fmt.Errorf("stopping Ankra Cloud cluster: %w", stopError)
		}

		if result.Success {
			fmt.Println(text.FgGreen.Sprint("Ankra Cloud cluster stop initiated."))
		} else {
			fmt.Println("Cluster stop request submitted.")
		}
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if result.OperationID != nil {
			fmt.Printf("  Operation ID: %s\n", *result.OperationID)
		}
		printStopStateOutcome(result.StatePreserved, result.StateSnapshot, result.Message, result.StopMode)
		return nil
	},
}

var ankraCloudStartCmd = &cobra.Command{
	Use:   "start <cluster_id|name>",
	Short: "Start a stopped Ankra Cloud cluster",
	Long:  "Start a stopped Ankra Cloud cluster. Use --scope control_plane to bring up only the control plane.",
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
			return withExitCode(exitUsage, fmt.Errorf("invalid --scope %q: must be 'all' or 'control_plane'", scope))
		}

		result, startError := apiClient.StartAnkraCloudCluster(clusterID, client.StartClusterOptions{Scope: scope, RestoreState: restoreStateFlag(command)})
		if startError != nil {
			return fmt.Errorf("starting Ankra Cloud cluster: %w", startError)
		}

		fmt.Println(text.FgGreen.Sprint("Ankra Cloud cluster start initiated."))
		fmt.Printf("  Scope: %s\n", result.Scope)
		if result.MarkedToStartAt != "" {
			fmt.Printf("  Marked to start at: %s\n", result.MarkedToStartAt)
		}
		fmt.Printf("  Created operations: %d\n", result.CreatedOperations)
		printStartStateOutcome(result.StateRestore, result.StateSnapshotID)
		return nil
	},
}

func init() {
	ankraCloudStartCmd.Flags().String("scope", "all", "Provisioning scope: 'all' or 'control_plane'")
	registerThreeStateFlag(ankraCloudStartCmd, "restore-state", restoreStateFlagUsage)
	ankraCloudStopCmd.Flags().Bool("force", false, "Force stop: cancel every in-flight operation and block new operations for 60 seconds while the stop lands, and delete the servers instead of powering them off. The cluster's volumes are kept: only a deprovision deletes them")
	registerThreeStateFlag(ankraCloudStopCmd, "preserve-state", preserveStateFlagUsage)
	ankraCloudStopCmd.Flags().String("mode", "", stopModeFlagUsage)
	registerAnkraCloudCreateFlags(ankraCloudCreateCmd, ankraCloudPreflightCmd)
	registerAnkraCloudCatalogFlags(ankraCloudZonesCmd, ankraCloudTemplatesCmd, ankraCloudNetworksCmd, ankraCloudPricingCmd)
	ankraCloudNetworksCmd.Flags().String("zone", "", "Only list the networks in this zone")
	ankraCloudPlansCmd.Flags().String("credential-id", "", "Ankra Cloud credential ID (required unless --cluster is given)")
	ankraCloudPlansCmd.Flags().String("cluster", "", "List the plans under an existing cluster's own credential instead")
	registerStructuredOutputFlags(
		ankraCloudCreateCmd, ankraCloudPreflightCmd, ankraCloudDeprovisionCmd,
		ankraCloudWorkersCmd, ankraCloudK8sVersionCmd, ankraCloudZonesCmd,
		ankraCloudPlansCmd, ankraCloudTemplatesCmd, ankraCloudNetworksCmd, ankraCloudPricingCmd,
	)

	ankraCloudDeprovisionCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerAcceptVolumeDataLossFlag(ankraCloudDeprovisionCmd)
	ankraCloudCmd.AddCommand(
		ankraCloudCreateCmd, ankraCloudPreflightCmd, ankraCloudDeprovisionCmd,
		ankraCloudStopCmd, ankraCloudStartCmd, ankraCloudWorkersCmd, ankraCloudK8sVersionCmd,
		ankraCloudZonesCmd, ankraCloudPlansCmd, ankraCloudTemplatesCmd, ankraCloudNetworksCmd, ankraCloudPricingCmd,
	)
	ankraCloudCmd.AddCommand(newControlPlaneCmd(ankraCloudControlPlaneOps, "Ankra Cloud", "ankracloud plans"))
	clusterCmd.AddCommand(ankraCloudCmd)
}

// ankraCloudDistributions are the values --distribution accepts.
var ankraCloudDistributions = []string{client.AnkraCloudDistributionKubeadm, client.AnkraCloudDistributionK3s}

// ankraCloudCreateRequestFromFlags builds the create body shared by `create`
// and `preflight`. Flags left at their zero value are omitted so the
// server's documented defaults apply (template debian-13, one control plane,
// one worker, stacked etcd, cilium, retain).
func ankraCloudCreateRequestFromFlags(cmd *cobra.Command) (client.CreateAnkraCloudClusterRequest, error) {
	name, _ := cmd.Flags().GetString("name")
	description, _ := cmd.Flags().GetString("description")
	credentialID, _ := cmd.Flags().GetString("credential-id")
	runtimeCredentialID, _ := cmd.Flags().GetString("runtime-credential-id")
	sshKeyCredentialID, _ := cmd.Flags().GetString("ssh-key-credential-id")
	zone, _ := cmd.Flags().GetString("zone")
	template, _ := cmd.Flags().GetString("template")
	privateNetworkID, _ := cmd.Flags().GetString("private-network-id")
	networkIPRange, _ := cmd.Flags().GetString("network-ip-range")
	bastionPlan, _ := cmd.Flags().GetString("bastion-plan")
	bastionAllowedIPs, _ := cmd.Flags().GetStringSlice("bastion-allowed-ips")
	controlPlaneCount, _ := cmd.Flags().GetInt("control-plane-count")
	controlPlanePlan, _ := cmd.Flags().GetString("control-plane-plan")
	workerPlan, _ := cmd.Flags().GetString("worker-plan")
	distribution, _ := cmd.Flags().GetString("distribution")
	kubernetesVersion, _ := cmd.Flags().GetString("kubernetes-version")
	etcdTopology, _ := cmd.Flags().GetString("etcd-topology")
	etcdNodeCount, _ := cmd.Flags().GetInt("etcd-node-count")
	etcdPlan, _ := cmd.Flags().GetString("etcd-plan")
	cni, _ := cmd.Flags().GetString("cni")
	retentionPolicy, _ := cmd.Flags().GetString("retention-policy")
	gitopsCredentialName, _ := cmd.Flags().GetString("gitops-credential-name")
	gitopsRepository, _ := cmd.Flags().GetString("gitops-repository")
	gitopsBranch, _ := cmd.Flags().GetString("gitops-branch")

	distribution = strings.ToLower(strings.TrimSpace(distribution))
	if distribution != client.AnkraCloudDistributionKubeadm && distribution != client.AnkraCloudDistributionK3s {
		return client.CreateAnkraCloudClusterRequest{}, withExitCode(exitUsage,
			fmt.Errorf("invalid --distribution %q: must be one of %s", distribution, strings.Join(ankraCloudDistributions, ", ")))
	}
	if privateNetworkID != "" && networkIPRange != "" {
		return client.CreateAnkraCloudClusterRequest{}, withExitCode(exitUsage,
			errors.New("--network-ip-range sizes the network Ankra creates; omit it with --private-network-id"))
	}

	request := client.CreateAnkraCloudClusterRequest{
		Name:               name,
		CredentialID:       credentialID,
		SSHKeyCredentialID: sshKeyCredentialID,
		Zone:               zone,
		Template:           template,
		BastionPlan:        bastionPlan,
		BastionAllowedIPs:  bastionAllowedIPs,
		ControlPlaneCount:  controlPlaneCount,
		ControlPlanePlan:   controlPlanePlan,
		WorkerPlan:         workerPlan,
		Distribution:       distribution,
		EtcdTopology:       etcdTopology,
		EtcdNodeCount:      etcdNodeCount,
		EtcdPlan:           etcdPlan,
		CNI:                cni,
		RetentionPolicy:    retentionPolicy,
	}
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
	if gitopsCredentialName != "" {
		request.GitopsCredentialName = &gitopsCredentialName
	}
	if gitopsRepository != "" {
		request.GitopsRepository = &gitopsRepository
		if gitopsBranch != "" {
			request.GitopsBranch = &gitopsBranch
		}
	}
	return request, nil
}

var ankraCloudCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a kubeadm or k3s cluster on Ankra Cloud servers",
	Long: `Create an Ankra-managed kubeadm (default) or k3s cluster on Ankra Cloud
servers. Ankra builds the private network (unless --private-network-id adopts
one), a NAT router for the nodes' IPv4 egress, the per-server firewalls, a
bastion with a public IPv4 address, and the control plane and worker servers.
The kube-apiserver stays private and is reached through the bastion.

Servers are sized by plan: list them with 'ankra cluster ankracloud plans'.
Run 'preflight' first to check the zone, plans and network.

Examples:
  ankra cluster ankracloud create --name prod --credential-id <id> \
    --ssh-key-credential-id <id> --zone <zone> \
    --bastion-plan <plan> --control-plane-plan <plan> --worker-plan <plan>
  ankra cluster ankracloud create --name edge --distribution k3s ...`,
	RunE: func(cmd *cobra.Command, args []string) error {
		request, requestError := ankraCloudCreateRequestFromFlags(cmd)
		if requestError != nil {
			return requestError
		}
		result, createError := apiClient.CreateAnkraCloudCluster(request)
		if createError != nil {
			return fmt.Errorf("creating Ankra Cloud cluster: %w", createError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Printf("Ankra Cloud cluster '%s' created successfully!\n", result.Name)
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		fmt.Printf("\nView it in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), result.ClusterID)
		return nil
	},
}

var ankraCloudPreflightCmd = &cobra.Command{
	Use:   "preflight",
	Short: "Validate an Ankra Cloud cluster create request without provisioning",
	Long: `Run the Ankra Cloud create preflight: credential, zone, plan and template
availability and the network, without building anything.

Takes the same flags as 'create'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		request, requestError := ankraCloudCreateRequestFromFlags(cmd)
		if requestError != nil {
			return requestError
		}
		result, preflightError := apiClient.PreflightAnkraCloudCluster(request)
		if preflightError != nil {
			return fmt.Errorf("running Ankra Cloud preflight: %w", preflightError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		checksTable := table.NewWriter()
		checksTable.SetOutputMirror(os.Stdout)
		checksTable.SetStyle(table.StyleRounded)
		checksTable.AppendHeader(table.Row{"Check", "Status", "Message"})
		for _, item := range result.Items {
			checksTable.AppendRow(table.Row{item.Check, item.Status, item.Message})
		}
		checksTable.Render()

		if result.CanProceed {
			fmt.Println(text.FgGreen.Sprint("\nPreflight passed: the cluster can be created."))
			return nil
		}
		return errors.New("preflight failed: resolve the checks above before creating the cluster")
	},
}

var ankraCloudDeprovisionCmd = &cobra.Command{
	Use:   "deprovision <cluster_id|name>",
	Short: "Deprovision an Ankra Cloud cluster and release its servers",
	Long: `Permanently delete an Ankra Cloud cluster and the servers, network, router
and firewall rules Ankra created for it. An adopted private network is left in
place. Volumes follow the cluster's retention_policy: 'retain' keeps them,
'delete' sweeps them. A 'delete' cluster's persistent volumes are named first
and deleted only when you accept that: answer the prompt on a terminal, or
pass --accept-volume-data-loss (--yes does not imply it).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		skipConfirmation, _ := cmd.Flags().GetBool("yes")
		acceptsVolumeDataLoss, volumeError := acknowledgeVolumeDataLoss(cmd, cloudClusterKindAnkraCloud, clusterID,
			clusterTarget(args[0], clusterID))
		if volumeError != nil {
			return volumeError
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Deprovision Ankra Cloud cluster %s? This permanently deletes its servers and the cluster record! [y/N]: ",
				clusterTarget(args[0], clusterID)),
			skipConfirmation); confirmError != nil {
			return confirmError
		}
		result, deprovisionError := apiClient.DeprovisionAnkraCloudCluster(clusterID,
			client.DeprovisionOptions{AcceptVolumeDataLoss: acceptsVolumeDataLoss})
		if deprovisionError != nil {
			return fmt.Errorf("deprovisioning Ankra Cloud cluster: %w", deprovisionError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		fmt.Println(text.FgGreen.Sprint("Ankra Cloud cluster deprovision initiated."))
		fmt.Printf("  Cluster ID: %s\n", result.ClusterID)
		if result.OperationID != nil {
			fmt.Printf("  Operation ID: %s\n", *result.OperationID)
		}
		return nil
	},
}

var ankraCloudWorkersCmd = &cobra.Command{
	Use:   "workers <cluster_id|name>",
	Short: "Get current worker count for an Ankra Cloud cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		result, fetchError := apiClient.GetAnkraCloudWorkerCount(clusterID)
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

var ankraCloudK8sVersionCmd = &cobra.Command{
	Use:   "k8s-version <cluster_id|name>",
	Short: "Get current Kubernetes version for an Ankra Cloud cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		result, fetchError := apiClient.GetAnkraCloudK8sVersion(clusterID)
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

func newAnkraCloudTable() table.Writer {
	catalogTable := table.NewWriter()
	catalogTable.SetOutputMirror(os.Stdout)
	catalogTable.SetStyle(table.StyleRounded)
	return catalogTable
}

// formatEuroCents renders an Ankra Cloud EUR-cent price; zero is unpriced.
func formatEuroCents(cents int) string {
	if cents == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f EUR", float64(cents)/100)
}

func printAnkraCloudPricingNote(result *client.AnkraCloudCatalogResult) {
	if result.PricingComplete {
		return
	}
	note := "\nPricing is incomplete"
	if len(result.IncompleteReasons) > 0 {
		note += ": " + strings.Join(result.IncompleteReasons, "; ")
	}
	fmt.Fprintln(os.Stderr, text.FgYellow.Sprint(note))
}

var ankraCloudZonesCmd = &cobra.Command{
	Use:   "zones",
	Short: "List the Ankra Cloud zones a credential can use",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		result, listError := apiClient.ListAnkraCloudZones(credentialID)
		if listError != nil {
			return fmt.Errorf("listing Ankra Cloud zones: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Zones) == 0 {
			fmt.Println("No zones available for this credential.")
			return nil
		}
		zonesTable := newAnkraCloudTable()
		zonesTable.AppendHeader(table.Row{"Zone", "Region", "Name", "Country"})
		for _, zone := range result.Zones {
			zonesTable.AppendRow(table.Row{zone.Name, zone.Region, zone.DisplayName, zone.Country})
		}
		zonesTable.Render()
		return nil
	},
}

var ankraCloudPlansCmd = &cobra.Command{
	Use:   "plans",
	Short: "List the Ankra Cloud server plans for a credential or an existing cluster",
	Long: `List the server plans (sizes) the create and node-group flags take.

Pass --credential-id to browse before creating a cluster, or --cluster to list
the plans under an existing cluster's own credential (for node groups and
control-plane or bastion resizes).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		clusterArgument, _ := cmd.Flags().GetString("cluster")
		if (credentialID == "") == (clusterArgument == "") {
			return withExitCode(exitUsage, errors.New("pass exactly one of --credential-id or --cluster"))
		}
		var result *client.AnkraCloudCatalogResult
		var listError error
		if clusterArgument != "" {
			clusterID, resolveError := resolveClusterArg(clusterArgument)
			if resolveError != nil {
				return resolveError
			}
			result, listError = apiClient.ListAnkraCloudClusterPlans(clusterID)
		} else {
			result, listError = apiClient.ListAnkraCloudPlans(credentialID)
		}
		if listError != nil {
			return fmt.Errorf("listing Ankra Cloud plans: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Plans) == 0 {
			fmt.Println("No plans available.")
			return nil
		}
		plansTable := newAnkraCloudTable()
		plansTable.AppendHeader(table.Row{"Plan", "Family", "vCPU", "Memory (MiB)", "Disk (GiB)", "Monthly", "Available"})
		for _, plan := range result.Plans {
			available := "yes"
			if !plan.Available {
				available = "no"
			}
			plansTable.AppendRow(table.Row{
				plan.Name, plan.Family, plan.Cores, plan.MemoryMebibytes, plan.StorageGibibytes,
				formatEuroCents(plan.PriceMonthlyCents), available,
			})
		}
		plansTable.Render()
		printAnkraCloudPricingNote(result)
		return nil
	},
}

var ankraCloudTemplatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "List the operating-system templates Ankra Cloud servers boot",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		result, listError := apiClient.ListAnkraCloudTemplates(credentialID)
		if listError != nil {
			return fmt.Errorf("listing Ankra Cloud templates: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Templates) == 0 {
			fmt.Println("No templates available for this credential.")
			return nil
		}
		templatesTable := newAnkraCloudTable()
		templatesTable.AppendHeader(table.Row{"ID", "Name", "OS", "Version"})
		for _, template := range result.Templates {
			templatesTable.AppendRow(table.Row{template.ID, template.DisplayName, template.OperatingSystem, template.Version})
		}
		templatesTable.Render()
		return nil
	},
}

var ankraCloudNetworksCmd = &cobra.Command{
	Use:   "networks",
	Short: "List the Ankra Cloud private networks a cluster can adopt",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		zone, _ := cmd.Flags().GetString("zone")
		result, listError := apiClient.ListAnkraCloudNetworks(credentialID, zone)
		if listError != nil {
			return fmt.Errorf("listing Ankra Cloud networks: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		if len(result.Networks) == 0 {
			fmt.Println("No private networks available for this credential.")
			return nil
		}
		networksTable := newAnkraCloudTable()
		networksTable.AppendHeader(table.Row{"ID", "Name", "Zone", "CIDR"})
		for _, network := range result.Networks {
			networksTable.AppendRow(table.Row{network.ID, network.Name, network.Zone, network.CIDR})
		}
		networksTable.Render()
		return nil
	},
}

var ankraCloudPricingCmd = &cobra.Command{
	Use:   "pricing",
	Short: "Show Ankra Cloud storage and public IPv4 prices",
	RunE: func(cmd *cobra.Command, args []string) error {
		credentialID, _ := cmd.Flags().GetString("credential-id")
		result, listError := apiClient.ListAnkraCloudPricing(credentialID)
		if listError != nil {
			return fmt.Errorf("reading Ankra Cloud pricing: %w", listError)
		}

		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}

		pricingTable := newAnkraCloudTable()
		pricingTable.AppendHeader(table.Row{"Item", "Price"})
		for _, storagePrice := range result.StoragePrices {
			pricingTable.AppendRow(table.Row{"Storage " + storagePrice.Tier + " (per GiB-month)", formatEuroCents(storagePrice.GBMonthCents)})
		}
		if result.PublicIPv4MonthlyCents != nil {
			pricingTable.AppendRow(table.Row{"Public IPv4 (per month)", formatEuroCents(*result.PublicIPv4MonthlyCents)})
		}
		pricingTable.Render()
		printAnkraCloudPricingNote(result)
		return nil
	},
}

// registerAnkraCloudCreateFlags declares the create/preflight flag set once
// so the two commands can never drift.
func registerAnkraCloudCreateFlags(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().String("name", "", "Cluster name (required)")
		command.Flags().String("description", "", "Cluster description")
		command.Flags().String("credential-id", "", "Ankra Cloud credential ID (required)")
		command.Flags().String("runtime-credential-id", "", "Ankra Cloud credential for the in-cluster CCM/CSI (defaults to the provisioning credential)")
		command.Flags().String("ssh-key-credential-id", "", "SSH key credential ID (required)")
		command.Flags().String("zone", "", "Ankra Cloud zone (required; see 'ankracloud zones')")
		command.Flags().String("template", "", "Server template (server default: debian-13; see 'ankracloud templates')")
		command.Flags().String("private-network-id", "", "Adopt an existing private network instead of creating one")
		command.Flags().String("network-ip-range", "", "RFC 1918 CIDR (/16 to /29) for the created private network (server default: a derived /20)")
		command.Flags().String("bastion-plan", "", "Bastion server plan (required)")
		command.Flags().StringSlice("bastion-allowed-ips", nil, "CIDRs allowed to reach the bastion over SSH (default: anywhere)")
		command.Flags().Int("control-plane-count", 0, "Control plane node count (server default: 1)")
		command.Flags().String("control-plane-plan", "", "Control plane server plan (required)")
		command.Flags().Int("worker-count", 0, "Default-pool worker count (server default: 1)")
		command.Flags().String("worker-plan", "", "Worker server plan (required while workers are above zero)")
		command.Flags().String("distribution", client.AnkraCloudDistributionKubeadm, "Kubernetes distribution: kubeadm or k3s")
		command.Flags().String("kubernetes-version", "", "Pin a Kubernetes version (default: latest supported)")
		command.Flags().String("etcd-topology", "", "etcd topology: stacked or external (server default: stacked)")
		command.Flags().Int("etcd-node-count", 0, "External etcd node count, 3 or 5 (server default: 3)")
		command.Flags().String("etcd-plan", "", "External etcd server plan (required with --etcd-topology external)")
		command.Flags().String("cni", "", "CNI plugin (server default: cilium; immutable after create)")
		command.Flags().String("retention-policy", "", "Teardown policy for volumes: retain or delete (server default: retain)")
		command.Flags().String("gitops-credential-name", "", "GitOps credential name (optional)")
		command.Flags().String("gitops-repository", "", "GitOps repository URL (optional)")
		command.Flags().String("gitops-branch", "", "GitOps branch (server default: master)")
		command.Flags().Bool("include-networking", true, "Provision the networking stack")
		command.Flags().Bool("include-dns", true, "Provision the DNS zone")
		for _, required := range []string{"name", "credential-id", "ssh-key-credential-id", "zone", "bastion-plan", "control-plane-plan"} {
			_ = command.MarkFlagRequired(required)
		}
	}
}

// registerAnkraCloudCatalogFlags declares the credential-scoped catalog flag.
func registerAnkraCloudCatalogFlags(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().String("credential-id", "", "Ankra Cloud credential ID (required)")
		_ = command.MarkFlagRequired("credential-id")
	}
}

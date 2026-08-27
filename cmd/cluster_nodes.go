package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type clusterNodesOps struct {
	provider string
	list     func(clusterID string) (*client.NodeListResult, error)
	get      func(clusterID, nodeID string) (*client.NodeDetail, error)
	restart  func(clusterID, nodeID string) (*client.RestartNodeResult, error)
	// cloudInitLog is nil for providers whose node-remediation SSH lane is
	// not wired (Proxmox, Morpheus); the subcommand is only registered when
	// it is set.
	cloudInitLog func(clusterID, nodeID string) (*client.NodeCloudInitLogResult, error)
}

func hetznerNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "hetzner",
		list:         apiClient.ListHetznerClusterNodes,
		get:          apiClient.GetHetznerClusterNode,
		restart:      apiClient.RestartHetznerClusterNode,
		cloudInitLog: apiClient.HetznerNodeCloudInitLog,
	}
}

func ovhNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "ovh",
		list:         apiClient.ListOvhClusterNodes,
		get:          apiClient.GetOvhClusterNode,
		restart:      apiClient.RestartOvhClusterNode,
		cloudInitLog: apiClient.OvhNodeCloudInitLog,
	}
}

func upcloudNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "upcloud",
		list:         apiClient.ListUpcloudClusterNodes,
		get:          apiClient.GetUpcloudClusterNode,
		restart:      apiClient.RestartUpcloudClusterNode,
		cloudInitLog: apiClient.UpcloudNodeCloudInitLog,
	}
}

func digitaloceanNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "digitalocean",
		list:         apiClient.ListDigitaloceanClusterNodes,
		get:          apiClient.GetDigitaloceanClusterNode,
		restart:      apiClient.RestartDigitaloceanClusterNode,
		cloudInitLog: apiClient.DigitaloceanNodeCloudInitLog,
	}
}

func scalewayNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider:     "scaleway",
		list:         apiClient.ListScalewayClusterNodes,
		get:          apiClient.GetScalewayClusterNode,
		restart:      apiClient.RestartScalewayClusterNode,
		cloudInitLog: apiClient.ScalewayNodeCloudInitLog,
	}
}

func proxmoxNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider: "proxmox",
		list:     apiClient.ListProxmoxClusterNodes,
		get:      apiClient.GetProxmoxClusterNode,
		restart:  apiClient.RestartProxmoxClusterNode,
	}
}

func morpheusNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider: "morpheus",
		list:     apiClient.ListMorpheusClusterNodes,
		get:      apiClient.GetMorpheusClusterNode,
	}
}

func runNodesList(cmd *cobra.Command, opsFn func() clusterNodesOps, clusterID string) error {
	ops := opsFn()
	result, err := ops.list(clusterID)
	if err != nil {
		return err
	}

	if handled, err := renderStructured(cmd, result); err != nil {
		return err
	} else if handled {
		return nil
	}

	if len(result.Nodes) == 0 {
		fmt.Println("No nodes found.")
		return nil
	}

	fmt.Printf("%-36s  %-22s  %-13s  %-14s  %-16s  %-12s  %-15s  %-15s\n",
		"ID", "NAME", "ROLE", "NODE_GROUP", "INSTANCE_TYPE", "STATE", "PRIVATE_IP", "PROVIDER_STATUS")
	for _, n := range result.Nodes {
		state := n.State
		if n.IsDeleted {
			state = state + " (soft-deleted)"
		}
		fmt.Printf("%-36s  %-22s  %-13s  %-14s  %-16s  %-12s  %-15s  %-15s\n",
			n.ID,
			truncate(n.Name, 22),
			truncate(stringValue(n.Role), 13),
			truncate(stringValue(n.NodeGroup), 14),
			truncate(n.InstanceType, 16),
			truncate(state, 12),
			truncate(stringValue(n.PrivateIP), 15),
			truncate(providerStatusDisplay(n.ProviderStatus, n.ProviderPowerState), 15),
		)
	}
	return nil
}

// providerStatusDisplay combines the cloud provider's live status and power
// state (e.g. OVH's ACTIVE/SHUTOFF plus its power state) into one column;
// "-" when the provider has no read job or none has run yet.
func providerStatusDisplay(status, powerState *string) string {
	switch {
	case status != nil && powerState != nil:
		return *status + "/" + *powerState
	case status != nil:
		return *status
	case powerState != nil:
		return *powerState
	default:
		return "-"
	}
}

func runNodesRestart(cmd *cobra.Command, opsFn func() clusterNodesOps, clusterID, nodeID string) error {
	ops := opsFn()
	result, err := ops.restart(clusterID, nodeID)
	if err != nil {
		return err
	}

	if handled, err := renderStructured(cmd, result); err != nil {
		return err
	} else if handled {
		return nil
	}

	fmt.Printf("Restart scheduled for node %s (operation %s, job %s).\n", result.NodeID, result.OperationID, result.JobName)
	fmt.Println("The node will reboot (or power-cycle); workloads on it are briefly unavailable until it comes back up.")
	fmt.Printf("Track progress with: ankra cluster operations list %s\n", result.OperationID)
	return nil
}

func runNodesCloudInitLog(cmd *cobra.Command, opsFn func() clusterNodesOps, clusterID, nodeID string) error {
	ops := opsFn()
	result, err := ops.cloudInitLog(clusterID, nodeID)
	if err != nil {
		return err
	}

	if handled, err := renderStructured(cmd, result); err != nil {
		return err
	} else if handled {
		return nil
	}

	if !result.Completed {
		fmt.Printf("Cloud-init log fetch dispatched (operation %s, status %s) - the SSH round trip is still running.\n",
			result.OperationID, result.Status)
		fmt.Printf("Re-run this command to attach to the in-flight fetch, or track it with: ankra cluster operations list %s\n",
			result.OperationID)
		return nil
	}
	if result.ErrorExcerpt != "" {
		fmt.Printf("Cloud-init log fetch failed: %s\n", result.ErrorExcerpt)
		return nil
	}
	if status, ok := result.Report["cloud_init_status"].(string); ok && status != "" {
		fmt.Println("cloud-init status:")
		fmt.Printf("  %s\n\n", strings.ReplaceAll(status, "\n", "\n  "))
	}
	if logTail, ok := result.Report["log_tail"].(string); ok {
		if truncated, _ := result.Report["truncated"].(bool); truncated {
			fmt.Println("cloud-init output log (tail, truncated to the last 64 KiB):")
		} else {
			fmt.Println("cloud-init output log:")
		}
		fmt.Println(logTail)
	}
	return nil
}

func runNodesGet(cmd *cobra.Command, opsFn func() clusterNodesOps, clusterID, nodeID string) error {
	ops := opsFn()
	detail, err := ops.get(clusterID, nodeID)
	if err != nil {
		return err
	}

	if handled, err := renderStructured(cmd, detail); err != nil {
		return err
	} else if handled {
		return nil
	}

	fmt.Printf("Node: %s\n", detail.Name)
	fmt.Printf("  ID:           %s\n", detail.ID)
	fmt.Printf("  Kind:         %s\n", detail.Kind)
	fmt.Printf("  Role:         %s\n", stringValue(detail.Role))
	fmt.Printf("  Node group:   %s\n", stringValue(detail.NodeGroup))
	state := detail.State
	if detail.IsDeleted {
		state += " (soft-deleted)"
	}
	fmt.Printf("  State:        %s\n", state)
	fmt.Printf("  Created at:   %s\n", detail.CreatedAt)
	fmt.Printf("  Updated at:   %s\n", detail.UpdatedAt)

	fmt.Println()
	fmt.Println("Definition:")
	if err := printJSONBlock(detail.Definition); err != nil {
		return err
	}

	if len(detail.Info) > 0 {
		fmt.Println()
		fmt.Println("Provider info (latest):")
		if err := printJSONBlock(detail.Info); err != nil {
			return err
		}
	}
	if len(detail.Data) > 0 {
		fmt.Println()
		fmt.Println("Reconciler data (latest):")
		if err := printJSONBlock(detail.Data); err != nil {
			return err
		}
	}

	printEdges("Dependencies", detail.Dependencies)
	printEdges("Relationships", detail.Relationships)
	printEdges("Groups", detail.Groups)
	return nil
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func printJSONBlock(v interface{}) error {
	encoded, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	fmt.Printf("  %s\n", string(encoded))
	return nil
}

func printEdges(title string, edges map[string][]string) {
	fmt.Println()
	fmt.Printf("%s:\n", title)
	if len(edges) == 0 {
		fmt.Println("  (none)")
		return
	}
	kinds := make([]string, 0, len(edges))
	for k := range edges {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		ids := edges[kind]
		fmt.Printf("  %s ×%d\n", kind, len(ids))
		for _, id := range ids {
			fmt.Printf("    - %s\n", id)
		}
	}
}

func newNodesCmd(opsFn func() clusterNodesOps, provider string, supportsRestart bool, supportsCloudInitLog bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nodes",
		Short: fmt.Sprintf("List and inspect %s cluster nodes", provider),
		Long: `Inspect every server Ankra manages for the cluster (control plane, workers,
and bastion or gateway). Soft-deleted entries from a stopped cluster are
included so the saved topology is visible before re-provisioning.`,
	}

	listCmd := &cobra.Command{
		Use:   "list <cluster_id>",
		Short: "List all nodes for the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNodesList(cmd, opsFn, args[0])
		},
	}

	getCmd := &cobra.Command{
		Use:   "get <cluster_id> <node_id>",
		Short: "Show full spec and metadata for a single node",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNodesGet(cmd, opsFn, args[0], args[1])
		},
	}

	registerStructuredOutputFlags(listCmd, getCmd)
	cmd.AddCommand(listCmd, getCmd)

	if supportsRestart {
		restartCmd := &cobra.Command{
			Use:   "restart <cluster_id> <node_id>",
			Short: "Restart a node (control plane, worker, or bastion/gateway)",
			Long: `Schedule a native reboot (falling back to a power cycle) of the node as a
tracked operation. The node must be in the 'up' state and have no restart
already in flight. Workloads on the node are briefly unavailable while it
reboots. Works for any node returned by 'nodes list', including the
bastion/gateway.`,
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runNodesRestart(cmd, opsFn, args[0], args[1])
			},
		}
		registerStructuredOutputFlags(restartCmd)
		cmd.AddCommand(restartCmd)
	}

	if supportsCloudInitLog {
		cloudInitLogCmd := &cobra.Command{
			Use:   "cloud-init-log <cluster_id> <node_id>",
			Short: "Read the node's cloud-init status and output-log tail",
			Long: `Fetch 'cloud-init status --long' and the tail of
/var/log/cloud-init-output.log from the node over the platform's bastion SSH
lane, as a tracked read-only operation. This is the debug surface for
node-group user-data: it shows whether the first-boot document ran and what
it printed, without needing the cluster's SSH key. Repeated calls attach to
an in-flight fetch instead of dispatching duplicates. Find node ids with
'nodes list'.`,
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runNodesCloudInitLog(cmd, opsFn, args[0], args[1])
			},
		}
		registerStructuredOutputFlags(cloudInitLogCmd)
		cmd.AddCommand(cloudInitLogCmd)
	}
	return cmd
}

func init() {
	hetznerCmd.AddCommand(newNodesCmd(hetznerNodesOps, "Hetzner", true, true))
	ovhCmd.AddCommand(newNodesCmd(ovhNodesOps, "OVH", true, true))
	upcloudCmd.AddCommand(newNodesCmd(upcloudNodesOps, "UpCloud", true, true))
	digitaloceanCmd.AddCommand(newNodesCmd(digitaloceanNodesOps, "DigitalOcean", true, true))
	scalewayCmd.AddCommand(newNodesCmd(scalewayNodesOps, "Scaleway", true, true))
	proxmoxCmd.AddCommand(newNodesCmd(proxmoxNodesOps, "Proxmox VE", true, false))
	morpheusCmd.AddCommand(newNodesCmd(morpheusNodesOps, "HPE Morpheus", false, false))
}

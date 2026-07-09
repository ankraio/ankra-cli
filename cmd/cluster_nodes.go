package cmd

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

type clusterNodesOps struct {
	provider string
	list     func(clusterID string) (*client.NodeListResult, error)
	get      func(clusterID, nodeID string) (*client.NodeDetail, error)
}

func hetznerNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider: "hetzner",
		list:     apiClient.ListHetznerClusterNodes,
		get:      apiClient.GetHetznerClusterNode,
	}
}

func ovhNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider: "ovh",
		list:     apiClient.ListOvhClusterNodes,
		get:      apiClient.GetOvhClusterNode,
	}
}

func upcloudNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider: "upcloud",
		list:     apiClient.ListUpcloudClusterNodes,
		get:      apiClient.GetUpcloudClusterNode,
	}
}

func digitaloceanNodesOps() clusterNodesOps {
	return clusterNodesOps{
		provider: "digitalocean",
		list:     apiClient.ListDigitaloceanClusterNodes,
		get:      apiClient.GetDigitaloceanClusterNode,
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

	fmt.Printf("%-36s  %-22s  %-13s  %-14s  %-16s  %-12s  %-15s\n",
		"ID", "NAME", "ROLE", "NODE_GROUP", "INSTANCE_TYPE", "STATE", "PRIVATE_IP")
	for _, n := range result.Nodes {
		state := n.State
		if n.IsDeleted {
			state = state + " (soft-deleted)"
		}
		fmt.Printf("%-36s  %-22s  %-13s  %-14s  %-16s  %-12s  %-15s\n",
			n.ID,
			truncate(n.Name, 22),
			truncate(stringValue(n.Role), 13),
			truncate(stringValue(n.NodeGroup), 14),
			truncate(n.InstanceType, 16),
			truncate(state, 12),
			truncate(stringValue(n.PrivateIP), 15),
		)
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

func newNodesCmd(opsFn func() clusterNodesOps, provider string) *cobra.Command {
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
	return cmd
}

func init() {
	hetznerCmd.AddCommand(newNodesCmd(hetznerNodesOps, "Hetzner"))
	ovhCmd.AddCommand(newNodesCmd(ovhNodesOps, "OVH"))
	upcloudCmd.AddCommand(newNodesCmd(upcloudNodesOps, "UpCloud"))
	digitaloceanCmd.AddCommand(newNodesCmd(digitaloceanNodesOps, "DigitalOcean"))
}

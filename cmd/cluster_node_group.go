package cmd

import (
	"context"
	"fmt"
	"strconv"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type (
	nodeGroupListFunc           func(clusterID string) (*client.NodeGroupListResult, error)
	nodeGroupAddFunc            func(ctx context.Context, clusterID string, req client.AddNodeGroupRequest, wait bool) (*client.AddNodeGroupResult, bool, error)
	nodeGroupScaleFunc          func(ctx context.Context, clusterID, groupName string, count int, wait bool) (*client.ScaleNodeGroupResult, bool, error)
	nodeGroupUpgradeFunc        func(ctx context.Context, clusterID, groupName, instanceType string, wait bool) (*client.UpdateNodeGroupResult, bool, error)
	nodeGroupDeleteFunc         func(ctx context.Context, clusterID, groupName string, wait bool) (*client.DeleteNodeGroupResult, bool, error)
	nodeGroupAutoscalingGetFunc func(clusterID, groupName string) (*client.NodeGroupAutoscalingResult, error)
	nodeGroupAutoscalingSetFunc func(ctx context.Context, clusterID, groupName string, req client.NodeGroupAutoscalingRequest, wait bool) (*client.NodeGroupAutoscalingResult, bool, error)
)

// resolveNodeGroupClusterKind looks up the cluster and confirms it is a
// cloud-managed kind that supports node groups, returning a clear error
// otherwise.
func resolveNodeGroupClusterKind(clusterID string) (string, error) {
	cluster, lookupError := apiClient.GetClusterByID(clusterID)
	if lookupError != nil {
		return "", fmt.Errorf("looking up cluster %q: %w", clusterID, lookupError)
	}
	switch cluster.Kind {
	case "hetzner", "ovh", "upcloud":
		return cluster.Kind, nil
	default:
		return "", fmt.Errorf(
			"cluster %q (kind %q) does not support node groups. Only Hetzner, OVH, and UpCloud clusters can use this command",
			clusterID, cluster.Kind)
	}
}

func nodeGroupListForKind(kind string) nodeGroupListFunc {
	switch kind {
	case "hetzner":
		return apiClient.ListHetznerNodeGroups
	case "ovh":
		return apiClient.ListOvhNodeGroups
	case "upcloud":
		return apiClient.ListUpcloudNodeGroups
	}
	return nil
}

func nodeGroupAddForKind(kind string) nodeGroupAddFunc {
	switch kind {
	case "hetzner":
		return apiClient.AddHetznerNodeGroup
	case "ovh":
		return apiClient.AddOvhNodeGroup
	case "upcloud":
		return apiClient.AddUpcloudNodeGroup
	}
	return nil
}

func nodeGroupScaleForKind(kind string) nodeGroupScaleFunc {
	switch kind {
	case "hetzner":
		return apiClient.ScaleHetznerNodeGroup
	case "ovh":
		return apiClient.ScaleOvhNodeGroup
	case "upcloud":
		return apiClient.ScaleUpcloudNodeGroup
	}
	return nil
}

func nodeGroupUpgradeForKind(kind string) nodeGroupUpgradeFunc {
	switch kind {
	case "hetzner":
		return apiClient.UpdateHetznerNodeGroupInstanceType
	case "ovh":
		return apiClient.UpdateOvhNodeGroupInstanceType
	case "upcloud":
		return apiClient.UpdateUpcloudNodeGroupInstanceType
	}
	return nil
}

func nodeGroupDeleteForKind(kind string) nodeGroupDeleteFunc {
	switch kind {
	case "hetzner":
		return apiClient.DeleteHetznerNodeGroup
	case "ovh":
		return apiClient.DeleteOvhNodeGroup
	case "upcloud":
		return apiClient.DeleteUpcloudNodeGroup
	}
	return nil
}

func nodeGroupAutoscalingGetForKind(kind string) nodeGroupAutoscalingGetFunc {
	switch kind {
	case "hetzner":
		return apiClient.GetHetznerNodeGroupAutoscaling
	case "ovh":
		return apiClient.GetOvhNodeGroupAutoscaling
	case "upcloud":
		return apiClient.GetUpcloudNodeGroupAutoscaling
	}
	return nil
}

func nodeGroupAutoscalingSetForKind(kind string) nodeGroupAutoscalingSetFunc {
	switch kind {
	case "hetzner":
		return apiClient.UpdateHetznerNodeGroupAutoscaling
	case "ovh":
		return apiClient.UpdateOvhNodeGroupAutoscaling
	case "upcloud":
		return apiClient.UpdateUpcloudNodeGroupAutoscaling
	}
	return nil
}

var clusterNodeGroupCmd = &cobra.Command{
	Use:   "node-group",
	Short: "Manage node groups for a cloud cluster",
	Long: `List, add, scale, upgrade, and delete node groups on a cloud cluster.

The cloud provider (Hetzner, OVH, or UpCloud) is detected automatically from
the cluster.`,
}

var clusterNodeGroupListCmd = &cobra.Command{
	Use:   "list <cluster_id>",
	Short: "List node groups",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		kind, kindError := resolveNodeGroupClusterKind(clusterID)
		if kindError != nil {
			return kindError
		}

		result, listError := nodeGroupListForKind(kind)(clusterID)
		if listError != nil {
			return fmt.Errorf("listing node groups: %w", listError)
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
		for _, nodeGroup := range result.NodeGroups {
			fmt.Printf("%-20s  type=%-8s  count=%d  labels=%d  taints=%d\n",
				nodeGroup.Name, nodeGroup.InstanceType, nodeGroup.Count, len(nodeGroup.Labels), len(nodeGroup.Taints))
		}
		return nil
	},
}

var clusterNodeGroupAddCmd = &cobra.Command{
	Use:   "add <cluster_id>",
	Short: "Add a node group",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		kind, kindError := resolveNodeGroupClusterKind(clusterID)
		if kindError != nil {
			return kindError
		}
		name, _ := cmd.Flags().GetString("name")
		instanceType, _ := cmd.Flags().GetString("instance-type")
		count, _ := cmd.Flags().GetInt("count")

		req := client.AddNodeGroupRequest{
			Name:         name,
			InstanceType: instanceType,
			Count:        count,
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, addError := nodeGroupAddForKind(kind)(requestContext, clusterID, req, wait)
		if addError != nil {
			return asyncWriteError("adding node group", wait, addError)
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

var clusterNodeGroupScaleCmd = &cobra.Command{
	Use:   "scale <cluster_id> <group_name> <count>",
	Short: "Scale a node group",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		count, convertError := strconv.Atoi(args[2])
		if convertError != nil {
			return fmt.Errorf("invalid count: %w", convertError)
		}
		kind, kindError := resolveNodeGroupClusterKind(clusterID)
		if kindError != nil {
			return kindError
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, scaleError := nodeGroupScaleForKind(kind)(requestContext, clusterID, groupName, count, wait)
		if scaleError != nil {
			return asyncWriteError("scaling node group", wait, scaleError)
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

var clusterNodeGroupUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster_id> <group_name> <instance_type>",
	Short: "Upgrade instance type for a node group (cannot be reversed)",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		instanceType := args[2]
		kind, kindError := resolveNodeGroupClusterKind(clusterID)
		if kindError != nil {
			return kindError
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, upgradeError := nodeGroupUpgradeForKind(kind)(requestContext, clusterID, groupName, instanceType, wait)
		if upgradeError != nil {
			return asyncWriteError("upgrading node group", wait, upgradeError)
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

var clusterNodeGroupDeleteCmd = &cobra.Command{
	Use:   "delete <cluster_id> <group_name>",
	Short: "Delete a node group and all its nodes",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		yes, _ := cmd.Flags().GetBool("yes")
		kind, kindError := resolveNodeGroupClusterKind(clusterID)
		if kindError != nil {
			return kindError
		}

		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete node group %q from cluster %q? This deletes all its nodes! [y/N]: ", groupName, clusterID),
			yes,
		); err != nil {
			return err
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, deleteError := nodeGroupDeleteForKind(kind)(requestContext, clusterID, groupName, wait)
		if deleteError != nil {
			return asyncWriteError("deleting node group", wait, deleteError)
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

var clusterNodeGroupAutoscalingCmd = &cobra.Command{
	Use:   "autoscaling",
	Short: "Manage node-group autoscaling",
	Long: `Read or write the Cluster Autoscaler settings of a node group.

When autoscaling is enabled, the Ankra-managed Cluster Autoscaler keeps the
group's node count within [min, max] based on pod demand. Manual scaling
stays allowed but is clamped into the same bounds.`,
}

var clusterNodeGroupAutoscalingGetCmd = &cobra.Command{
	Use:   "get <cluster_id> <group_name>",
	Short: "Show autoscaling settings for a node group",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		kind, kindError := resolveNodeGroupClusterKind(clusterID)
		if kindError != nil {
			return kindError
		}

		result, getError := nodeGroupAutoscalingGetForKind(kind)(clusterID, groupName)
		if getError != nil {
			return fmt.Errorf("fetching node group autoscaling: %w", getError)
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		state := "disabled"
		if result.Enabled {
			state = "enabled"
		}
		fmt.Printf("Node group '%s' autoscaling: %s\n", result.GroupName, state)
		fmt.Printf("  Min: %d\n", result.MinCount)
		fmt.Printf("  Max: %d\n", result.MaxCount)
		return nil
	},
}

var clusterNodeGroupAutoscalingSetCmd = &cobra.Command{
	Use:   "set <cluster_id> <group_name>",
	Short: "Enable or disable autoscaling for a node group",
	Long: `Enable autoscaling with --enabled --min <n> --max <n>, or disable it with
--enabled=false. min must be at least 1 (scale-to-zero is not supported);
enabling requires the cluster's ankra-agent to be recent enough to serve
the autoscaler, and installs the Cluster Autoscaler on first enable.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := args[0]
		groupName := args[1]
		enabled, _ := cmd.Flags().GetBool("enabled")
		minCount, _ := cmd.Flags().GetInt("min")
		maxCount, _ := cmd.Flags().GetInt("max")
		kind, kindError := resolveNodeGroupClusterKind(clusterID)
		if kindError != nil {
			return kindError
		}

		req := client.NodeGroupAutoscalingRequest{Enabled: enabled, MinCount: minCount, MaxCount: maxCount}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, setError := nodeGroupAutoscalingSetForKind(kind)(requestContext, clusterID, groupName, req, wait)
		if setError != nil {
			return asyncWriteError("updating node group autoscaling", wait, setError)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newAsyncSubmittedResult("Node group autoscaling update")); err != nil {
				return err
			} else if handled {
				return nil
			}
			printAsyncWriteSubmitted("Node group autoscaling update")
			return nil
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		if result.Enabled {
			fmt.Printf("Node group '%s' autoscaling enabled (min %d, max %d).\n",
				result.GroupName, result.MinCount, result.MaxCount)
		} else {
			fmt.Printf("Node group '%s' autoscaling disabled.\n", result.GroupName)
		}
		return nil
	},
}

func init() {
	clusterNodeGroupAddCmd.Flags().String("name", "", "Node group name (required)")
	clusterNodeGroupAddCmd.Flags().String("instance-type", "", "Server type / flavor / plan for nodes (required)")
	clusterNodeGroupAddCmd.Flags().Int("count", 1, "Number of nodes")
	_ = clusterNodeGroupAddCmd.MarkFlagRequired("name")
	_ = clusterNodeGroupAddCmd.MarkFlagRequired("instance-type")

	clusterNodeGroupAutoscalingSetCmd.Flags().Bool("enabled", true, "Enable (true) or disable (false) autoscaling")
	clusterNodeGroupAutoscalingSetCmd.Flags().Int("min", 1, "Minimum node count while autoscaling (>= 1)")
	clusterNodeGroupAutoscalingSetCmd.Flags().Int("max", 5, "Maximum node count while autoscaling")

	registerAsyncWriteFlags(clusterNodeGroupAddCmd)
	registerAsyncWriteFlags(clusterNodeGroupScaleCmd)
	registerAsyncWriteFlags(clusterNodeGroupUpgradeCmd)
	registerAsyncWriteFlags(clusterNodeGroupDeleteCmd)
	registerAsyncWriteFlags(clusterNodeGroupAutoscalingSetCmd)

	clusterNodeGroupDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(
		clusterNodeGroupListCmd,
		clusterNodeGroupAddCmd,
		clusterNodeGroupScaleCmd,
		clusterNodeGroupUpgradeCmd,
		clusterNodeGroupDeleteCmd,
		clusterNodeGroupAutoscalingGetCmd,
		clusterNodeGroupAutoscalingSetCmd,
	)

	clusterNodeGroupAutoscalingCmd.AddCommand(clusterNodeGroupAutoscalingGetCmd)
	clusterNodeGroupAutoscalingCmd.AddCommand(clusterNodeGroupAutoscalingSetCmd)

	clusterNodeGroupCmd.AddCommand(clusterNodeGroupListCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupAddCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupScaleCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupUpgradeCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupDeleteCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupAutoscalingCmd)

	clusterCmd.AddCommand(clusterNodeGroupCmd)
}

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

type (
	nodeGroupListFunc           func(clusterID string) (*client.NodeGroupListResult, error)
	nodeGroupAddFunc            func(ctx context.Context, clusterID string, req client.AddNodeGroupRequest, wait bool) (*client.AddNodeGroupResult, bool, error)
	nodeGroupScaleFunc          func(ctx context.Context, clusterID, groupName string, count int, drainOptions client.DrainOptions, wait bool) (*client.ScaleNodeGroupResult, bool, error)
	nodeGroupUpgradeFunc        func(ctx context.Context, clusterID, groupName, instanceType string, drainOptions client.DrainOptions, wait bool) (*client.UpdateNodeGroupResult, bool, error)
	nodeGroupDeleteFunc         func(ctx context.Context, clusterID, groupName string, drainOptions client.DrainOptions, wait bool) (*client.DeleteNodeGroupResult, bool, error)
	nodeGroupAutoscalingGetFunc func(clusterID, groupName string) (*client.NodeGroupAutoscalingResult, error)
	nodeGroupAutoscalingSetFunc func(ctx context.Context, clusterID, groupName string, req client.NodeGroupAutoscalingRequest, wait bool) (*client.NodeGroupAutoscalingResult, bool, error)
	nodeGroupLabelsFunc         func(ctx context.Context, clusterID, groupName string, labels map[string]string, wait bool) (*client.UpdateNodeGroupResult, bool, error)
	nodeGroupTaintsFunc         func(ctx context.Context, clusterID, groupName string, taints []client.NodeTaint, wait bool) (*client.UpdateNodeGroupResult, bool, error)
)

// resolveNodeGroupClusterKind looks up the cluster and confirms it is a
// cloud-managed kind that supports node groups. A cluster of another kind is
// a usage error (exit 2) naming its kind; typed is the cluster as the user
// named it.
func resolveNodeGroupClusterKind(typed, clusterID string) (string, error) {
	cluster, lookupError := apiClient.GetClusterByID(clusterID)
	if lookupError != nil {
		return "", fmt.Errorf("looking up cluster %q: %w", clusterID, lookupError)
	}
	if _, hasNodeGroups := nodeGroupKinds[cluster.Kind]; !hasNodeGroups {
		return "", unsupportedClusterKindError(typed, cluster.Kind, "node groups")
	}
	return cluster.Kind, nil
}

// nodeGroupArgs is the Args check of a node-group command that takes count
// arguments and names a group at groupIndex: the count, the name, and the
// -o value. cobra runs it before the credentials are read, so a name no
// group can have, or an output format the command cannot write, is refused
// before the cluster is looked up, a prompt is shown or a request is sent:
// a write must not be sent when its answer cannot be printed. Every
// node-group command that names a group uses it, the provider ones too.
func nodeGroupArgs(count, groupIndex int) cobra.PositionalArgs {
	return cobra.MatchAll(cobra.ExactArgs(count), func(cmd *cobra.Command, args []string) error {
		if err := validatePathArgument("node group name", args[groupIndex]); err != nil {
			return err
		}
		_, formatError := structuredFormatFromFlags(cmd)
		return formatError
	})
}

// nodeGroupAddArgs is the Args check of a node-group add: the cluster, the
// --name flag, which every later command puts in its request path, so a
// group that could never be addressed there is not created, and the -o
// value. It runs where nodeGroupArgs does, before the credentials are read.
// A --name left out is cobra's required-flag error, reported after this.
func nodeGroupAddArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	if cmd.Flags().Changed("name") {
		name, _ := cmd.Flags().GetString("name")
		if err := validatePathArgument("node group name", name); err != nil {
			return err
		}
	}
	_, formatError := structuredFormatFromFlags(cmd)
	return formatError
}

// validatePathArgument refuses a name the CLI puts into a request path when
// no such resource can have it, before anything is looked up, asked or sent:
//
//   - blank, or with spaces around it, which no name has;
//   - "." or "..", or one that decodes to them ("%2e%2e"): a path step, not a
//     name. Sent as it is, ".." is resolved by the platform's edge before
//     routing, so `node-group delete <cluster> ..` reached the cluster's own
//     DELETE, which deprovisions it (ankra-6pi023). The rule is the client's
//     (client.UnsafePathSegment), so the argument check and the request
//     check agree; "..." is neither and passes both;
//   - holding a '/' (or "%2F"): no such name has one.
//
// label names the argument in the message ("node group name").
func validatePathArgument(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return withExitCode(exitUsage, fmt.Errorf("the %s is blank", label))
	}
	if strings.TrimSpace(value) != value {
		return withExitCode(exitUsage, fmt.Errorf("the %s %q has spaces around it, which no %s has",
			label, value, label))
	}
	decoded, isPathStep := client.UnsafePathSegment(value)
	switch {
	case isPathStep && decoded != value:
		return withExitCode(exitUsage, fmt.Errorf("the %s %q decodes to %q, a path step, not a name",
			label, value, decoded))
	case isPathStep:
		return withExitCode(exitUsage, fmt.Errorf(
			"the %s %q is a path step, not a name: the platform's edge would resolve it to a different route",
			label, value))
	case strings.Contains(decoded, "/"):
		return withExitCode(exitUsage, fmt.Errorf("the %s %q contains '/', which no %s has", label, value, label))
	}
	return nil
}

// unsupportedAutoscalingKindError refuses node-group autoscaling on a kind
// that has node groups but no autoscaling. It names no supported kinds: the
// cluster's own kind is one of the node-group kinds, so the shared sentence
// would list it as supporting what it does not.
func unsupportedAutoscalingKindError(typed, kind string) error {
	return withExitCode(exitUsage, fmt.Errorf(
		"%s does not support node-group autoscaling (its kind is %s)", typed, kind))
}

// nodeGroupCalls is what the node-group commands call for one cluster kind,
// bound to the API client in use.
type nodeGroupCalls struct {
	list           nodeGroupListFunc
	add            nodeGroupAddFunc
	scale          nodeGroupScaleFunc
	upgrade        nodeGroupUpgradeFunc
	deleteGroup    nodeGroupDeleteFunc
	autoscalingGet nodeGroupAutoscalingGetFunc
	autoscalingSet nodeGroupAutoscalingSetFunc
	labels         nodeGroupLabelsFunc
	taints         nodeGroupTaintsFunc
	// nodes is the kind's node listing, which remove-node looks a node up in.
	nodes func() clusterNodesOps
}

// nodeGroupKinds is every cluster kind with node groups, and its calls. It is
// the one list of those kinds: a node-group command accepts a cluster whose
// kind is here and refuses any other (resolveNodeGroupClusterKind), and the
// tests that cover every kind read it.
var nodeGroupKinds = map[string]func(api APIClient) nodeGroupCalls{
	"hetzner": func(api APIClient) nodeGroupCalls {
		return nodeGroupCalls{
			list:           api.ListHetznerNodeGroups,
			add:            api.AddHetznerNodeGroup,
			scale:          api.ScaleHetznerNodeGroup,
			upgrade:        api.UpdateHetznerNodeGroupInstanceType,
			deleteGroup:    api.DeleteHetznerNodeGroup,
			autoscalingGet: api.GetHetznerNodeGroupAutoscaling,
			autoscalingSet: api.UpdateHetznerNodeGroupAutoscaling,
			labels:         api.UpdateHetznerNodeGroupLabels,
			taints:         api.UpdateHetznerNodeGroupTaints,
			nodes:          hetznerNodesOps,
		}
	},
	"ovh": func(api APIClient) nodeGroupCalls {
		return nodeGroupCalls{
			list:           api.ListOvhNodeGroups,
			add:            api.AddOvhNodeGroup,
			scale:          api.ScaleOvhNodeGroup,
			upgrade:        api.UpdateOvhNodeGroupInstanceType,
			deleteGroup:    api.DeleteOvhNodeGroup,
			autoscalingGet: api.GetOvhNodeGroupAutoscaling,
			autoscalingSet: api.UpdateOvhNodeGroupAutoscaling,
			labels:         api.UpdateOvhNodeGroupLabels,
			taints:         api.UpdateOvhNodeGroupTaints,
			nodes:          ovhNodesOps,
		}
	},
	"upcloud": func(api APIClient) nodeGroupCalls {
		return nodeGroupCalls{
			list:           api.ListUpcloudNodeGroups,
			add:            api.AddUpcloudNodeGroup,
			scale:          api.ScaleUpcloudNodeGroup,
			upgrade:        api.UpdateUpcloudNodeGroupInstanceType,
			deleteGroup:    api.DeleteUpcloudNodeGroup,
			autoscalingGet: api.GetUpcloudNodeGroupAutoscaling,
			autoscalingSet: api.UpdateUpcloudNodeGroupAutoscaling,
			labels:         api.UpdateUpcloudNodeGroupLabels,
			taints:         api.UpdateUpcloudNodeGroupTaints,
			nodes:          upcloudNodesOps,
		}
	},
	"digitalocean": func(api APIClient) nodeGroupCalls {
		return nodeGroupCalls{
			list:           api.ListDigitaloceanNodeGroups,
			add:            api.AddDigitaloceanNodeGroup,
			scale:          api.ScaleDigitaloceanNodeGroup,
			upgrade:        api.UpdateDigitaloceanNodeGroupInstanceType,
			deleteGroup:    api.DeleteDigitaloceanNodeGroup,
			autoscalingGet: api.GetDigitaloceanNodeGroupAutoscaling,
			autoscalingSet: api.UpdateDigitaloceanNodeGroupAutoscaling,
			labels:         api.UpdateDigitaloceanNodeGroupLabels,
			taints:         api.UpdateDigitaloceanNodeGroupTaints,
			nodes:          digitaloceanNodesOps,
		}
	},
	"scaleway": func(api APIClient) nodeGroupCalls {
		return nodeGroupCalls{
			list:           api.ListScalewayNodeGroups,
			add:            api.AddScalewayNodeGroup,
			scale:          api.ScaleScalewayNodeGroup,
			upgrade:        api.UpdateScalewayNodeGroupInstanceType,
			deleteGroup:    api.DeleteScalewayNodeGroup,
			autoscalingGet: api.GetScalewayNodeGroupAutoscaling,
			autoscalingSet: api.UpdateScalewayNodeGroupAutoscaling,
			labels:         api.UpdateScalewayNodeGroupLabels,
			taints:         api.UpdateScalewayNodeGroupTaints,
			nodes:          scalewayNodesOps,
		}
	},
	"ankracloud": func(api APIClient) nodeGroupCalls {
		return nodeGroupCalls{
			list:           api.ListAnkraCloudNodeGroups,
			add:            api.AddAnkraCloudNodeGroup,
			scale:          api.ScaleAnkraCloudNodeGroup,
			upgrade:        api.UpdateAnkraCloudNodeGroupInstanceType,
			deleteGroup:    api.DeleteAnkraCloudNodeGroup,
			autoscalingGet: api.GetAnkraCloudNodeGroupAutoscaling,
			autoscalingSet: api.UpdateAnkraCloudNodeGroupAutoscaling,
			labels:         api.UpdateAnkraCloudNodeGroupLabels,
			taints:         api.UpdateAnkraCloudNodeGroupTaints,
			nodes:          ankraCloudNodesOps,
		}
	},
	"aws": func(api APIClient) nodeGroupCalls {
		return nodeGroupCalls{
			list:           api.ListAwsNodeGroups,
			add:            api.AddAwsNodeGroup,
			scale:          api.ScaleAwsNodeGroup,
			upgrade:        api.UpdateAwsNodeGroupInstanceType,
			deleteGroup:    api.DeleteAwsNodeGroup,
			autoscalingGet: api.GetAwsNodeGroupAutoscaling,
			autoscalingSet: api.UpdateAwsNodeGroupAutoscaling,
			labels:         api.UpdateAwsNodeGroupLabels,
			taints:         api.UpdateAwsNodeGroupTaints,
			nodes:          awsNodesOps,
		}
	},
	"proxmox": func(api APIClient) nodeGroupCalls {
		return nodeGroupCalls{
			list:           api.ListProxmoxNodeGroups,
			add:            api.AddProxmoxNodeGroup,
			scale:          api.ScaleProxmoxNodeGroup,
			upgrade:        api.UpdateProxmoxNodeGroupInstanceType,
			deleteGroup:    api.DeleteProxmoxNodeGroup,
			autoscalingGet: api.GetProxmoxNodeGroupAutoscaling,
			autoscalingSet: api.UpdateProxmoxNodeGroupAutoscaling,
			labels:         api.UpdateProxmoxNodeGroupLabels,
			taints:         api.UpdateProxmoxNodeGroupTaints,
			nodes:          proxmoxNodesOps,
		}
	},
	"morpheus": func(api APIClient) nodeGroupCalls {
		return nodeGroupCalls{
			list:           api.ListMorpheusNodeGroups,
			add:            api.AddMorpheusNodeGroup,
			scale:          api.ScaleMorpheusNodeGroup,
			upgrade:        api.UpdateMorpheusNodeGroupInstanceType,
			deleteGroup:    api.DeleteMorpheusNodeGroup,
			autoscalingGet: api.GetMorpheusNodeGroupAutoscaling,
			autoscalingSet: api.UpdateMorpheusNodeGroupAutoscaling,
			labels:         api.UpdateMorpheusNodeGroupLabels,
			taints:         api.UpdateMorpheusNodeGroupTaints,
			nodes:          morpheusNodesOps,
		}
	},
}

// nodeGroupCallsFor is kind's node-group calls on the current API client.
// Every call is nil for a kind without node groups.
func nodeGroupCallsFor(kind string) nodeGroupCalls {
	callsFor, hasNodeGroups := nodeGroupKinds[kind]
	if !hasNodeGroups {
		return nodeGroupCalls{}
	}
	return callsFor(apiClient)
}

var clusterNodeGroupCmd = &cobra.Command{
	Use:   "node-group",
	Short: "Manage node groups for a cloud cluster",
	Long: `List, add, scale, upgrade, and delete node groups on a cloud cluster.

The cloud provider (Hetzner, OVH, UpCloud, DigitalOcean, Scaleway, Ankra Cloud, AWS, Proxmox VE, or
HPE Morpheus) is detected automatically from the cluster.`,
}

var clusterNodeGroupListCmd = &cobra.Command{
	Use:   "list <cluster_id|name>",
	Short: "List node groups",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		kind, kindError := resolveNodeGroupClusterKind(args[0], clusterID)
		if kindError != nil {
			return kindError
		}

		result, listError := nodeGroupCallsFor(kind).list(clusterID)
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
			fmt.Printf("%-20s  type=%-8s  count=%d%s  labels=%d  taints=%d%s%s%s\n",
				nodeGroup.Name, nodeGroup.InstanceType, nodeGroup.Count, nodeGroupJoinedSuffix(nodeGroup),
				len(nodeGroup.Labels), len(nodeGroup.Taints), nodeGroupZoneSuffix(nodeGroup), nodeGroupDiskSuffix(nodeGroup),
				nodeGroupCPUPoolSuffix(nodeGroup))
			printNodeGroupUnregistered(nodeGroup)
		}
		return nil
	},
}

// nodeGroupJoinedSuffix renders how many of a group's recorded workers are
// Kubernetes nodes when that differs from the count, or "" when they agree,
// against a platform that could not tell, and against one that does not
// serve the field yet. So a healthy line is unchanged, and the suffix is
// printed next to count on purpose: "count=3  joined=2" is the whole reason
// it exists. Every count the platform reports comes from its worker
// records, and a record can stay on the books with no node behind it, so an
// operator looking at a two-node cluster was told count=3 by every surface
// (PLA-853).
func nodeGroupJoinedSuffix(nodeGroup client.NodeGroupInfo) string {
	if nodeGroup.JoinedCount == nil || *nodeGroup.JoinedCount == nodeGroup.Count {
		return ""
	}
	return fmt.Sprintf("  joined=%d", *nodeGroup.JoinedCount)
}

// printNodeGroupUnregistered follows a group's list line with the workers
// that have no Kubernetes node behind them, so the mismatch is visible
// without reading execution steps. Silent when there are none.
func printNodeGroupUnregistered(nodeGroup client.NodeGroupInfo) {
	if len(nodeGroup.UnregisteredWorkers) == 0 {
		return
	}
	fmt.Printf("  no Kubernetes node is registered for: %s\n", strings.Join(nodeGroup.UnregisteredWorkers, ", "))
}

// nodeGroupZoneSuffix renders a node group's availability zones for a list
// line, or "" when the group requested none - so the line is unchanged for
// every provider without zone placement, and against a platform that does
// not serve the field yet.
//
// The zones are named rather than counted. "az=eu-west-par-c" on a group
// called general-par-a is the entire reason this is on the list line: a
// count would report "1" for a group correctly pinned to one zone and "1"
// for a group that silently collapsed into the wrong one.
func nodeGroupZoneSuffix(nodeGroup client.NodeGroupInfo) string {
	if len(nodeGroup.AvailabilityZones) > 0 {
		return "  az=" + strings.Join(nodeGroup.AvailabilityZones, ",")
	}
	if len(nodeGroup.Zones) > 0 {
		return "  zones=" + strings.Join(nodeGroup.Zones, ",")
	}
	return ""
}

// nodeGroupCPUPoolSuffix renders the Ankra Cloud CPU pool a node group pins
// its servers to, or "" when it pins none, so the line is unchanged for
// every other group.
func nodeGroupCPUPoolSuffix(nodeGroup client.NodeGroupInfo) string {
	if nodeGroup.CPUPool == "" {
		return ""
	}
	return "  cpu-pool=" + nodeGroup.CPUPool
}

// nodeGroupDiskSuffix renders a node group's root disk size and tier for a
// list line, or "" when the group chose neither, so the line is unchanged for every
// provider without the field and every group on its plan's included disk.
func nodeGroupDiskSuffix(nodeGroup client.NodeGroupInfo) string {
	switch {
	case nodeGroup.DiskGibibytes > 0 && nodeGroup.DiskTier != "":
		return fmt.Sprintf("  disk=%dGiB/%s", nodeGroup.DiskGibibytes, nodeGroup.DiskTier)
	case nodeGroup.DiskGibibytes > 0:
		return fmt.Sprintf("  disk=%dGiB", nodeGroup.DiskGibibytes)
	case nodeGroup.DiskTier != "":
		return "  disk=" + nodeGroup.DiskTier
	}
	return ""
}

// maxNodeGroupUserDataBytes mirrors the platform's refusal cap (the
// OpenStack instance user-data limit); checking client-side turns a
// round-trip rejection into an immediate, clearer error.
const maxNodeGroupUserDataBytes = 65535

// readNodeGroupUserDataFile loads the cloud-init document named by
// --user-data-file; an empty path means the flag was not set.
func readNodeGroupUserDataFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	content, readError := os.ReadFile(path)
	if readError != nil {
		return "", fmt.Errorf("reading --user-data-file: %w", readError)
	}
	if len(content) > maxNodeGroupUserDataBytes {
		return "", fmt.Errorf("--user-data-file %s is %d bytes; the platform caps node-group user data at %d bytes",
			path, len(content), maxNodeGroupUserDataBytes)
	}
	return string(content), nil
}

var clusterNodeGroupAddCmd = &cobra.Command{
	Use:   "add <cluster_id|name>",
	Short: "Add a node group",
	Args:  nodeGroupAddArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		kind, kindError := resolveNodeGroupClusterKind(args[0], clusterID)
		if kindError != nil {
			return kindError
		}
		instanceType, _ := cmd.Flags().GetString("instance-type")
		count, _ := cmd.Flags().GetInt("count")
		availabilityZone, _ := cmd.Flags().GetString("availability-zone")
		zone, _ := cmd.Flags().GetString("zone")
		userDataFile, _ := cmd.Flags().GetString("user-data-file")
		userData, userDataError := readNodeGroupUserDataFile(userDataFile)
		if userDataError != nil {
			return userDataError
		}
		diskGibibytes, diskError := ankraCloudDiskFlag(cmd, "disk-gib")
		if diskError != nil {
			return diskError
		}
		diskTier, diskTierError := ankraCloudDiskTierFlag(cmd, "disk-tier")
		if diskTierError != nil {
			return diskTierError
		}
		if (diskGibibytes > 0 || diskTier != "") && kind != "ankracloud" {
			return withExitCode(exitUsage, fmt.Errorf(
				"--disk-gib and --disk-tier are supported only on Ankra Cloud clusters; cluster %q is %s", clusterID, kind))
		}
		cpuPool, _ := cmd.Flags().GetString("cpu-pool")
		cpuPool = strings.TrimSpace(cpuPool)
		if cpuPool != "" && kind != "ankracloud" {
			return withExitCode(exitUsage, fmt.Errorf(
				"--cpu-pool is supported only on Ankra Cloud clusters; cluster %q is %s", clusterID, kind))
		}
		labelsFlag, _ := cmd.Flags().GetString("labels")
		labels, labelsError := parseLabelsFlag(labelsFlag)
		if labelsError != nil {
			return withExitCode(exitUsage, fmt.Errorf("invalid --labels: %w", labelsError))
		}
		taintsFlag, _ := cmd.Flags().GetString("taints")
		taints, taintsError := parseTaintsFlag(taintsFlag)
		if taintsError != nil {
			return withExitCode(exitUsage, fmt.Errorf("invalid --taints: %w", taintsError))
		}

		req := client.AddNodeGroupRequest{
			Name:             name,
			InstanceType:     instanceType,
			Count:            count,
			Labels:           labels,
			Taints:           taints,
			AvailabilityZone: availabilityZone,
			Zone:             zone,
			UserData:         userData,
			DiskGibibytes:    diskGibibytes,
			DiskTier:         diskTier,
			CPUPool:          cpuPool,
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, addError := nodeGroupCallsFor(kind).add(requestContext, clusterID, req, wait)
		if addError != nil {
			return asyncWriteError("adding node group", wait, addError)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newNodeGroupSubmittedResult("Node group add", clusterID)); err != nil {
				return err
			} else if handled {
				return nil
			}
			printNodeGroupWriteSubmitted("Node group add", clusterID)
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
	Use:   "scale <cluster_id|name> <group_name> <count>",
	Short: "Scale a node group",
	Long: `Scale a node group up or down to <count> nodes.

A scale-down picks the nodes to remove. ` + guardedDrainHelp + `

Examples:
  ankra cluster node-group scale <cluster_id> workers 2
  ankra cluster node-group scale <cluster_id> workers 2 --force-drain`,
	Args: nodeGroupArgs(3, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		groupName := args[1]
		count, convertError := strconv.Atoi(args[2])
		if convertError != nil {
			return fmt.Errorf("invalid count: %w", convertError)
		}
		kind, kindError := resolveNodeGroupClusterKind(args[0], clusterID)
		if kindError != nil {
			return kindError
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, scaleError := nodeGroupCallsFor(kind).scale(requestContext, clusterID, groupName, count, drainOptionsFromFlags(cmd), wait)
		if scaleError != nil {
			return asyncWriteError("scaling node group", wait, scaleError)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newNodeGroupSubmittedResult("Node group scale", clusterID)); err != nil {
				return err
			} else if handled {
				return nil
			}
			printNodeGroupWriteSubmitted("Node group scale", clusterID)
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
	Use:   "upgrade <cluster_id|name> <group_name> <instance_type>",
	Short: "Upgrade instance type for a node group (cannot be reversed)",
	Long: `Change the instance type of every node in a node group. This cannot be reversed.

The resize power-cycles each node, and each is drained first, honouring its
pods' PodDisruptionBudgets. --force-drain bypasses those budgets for that
drain, so a node is resized even if its pods' disruption budget refuses the
eviction. It applies to this request only.

On UpCloud a plan is changed in place only within its plan family (STARTER,
DEV, PREMIUM, GPU, or the unprefixed general-purpose plans such as 4xCPU-8GB).
A new plan from another family replaces each node instead - drained, deleted
and re-provisioned with the new plan - and the command says so. A control
plane cannot be replaced that way, so a cross-family change there is refused.

Examples:
  ankra cluster node-group upgrade <cluster_id> workers <instance_type>
  ankra cluster node-group upgrade <cluster_id> workers <instance_type> --force-drain`,
	Args: nodeGroupArgs(3, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		groupName := args[1]
		instanceType := args[2]
		kind, kindError := resolveNodeGroupClusterKind(args[0], clusterID)
		if kindError != nil {
			return kindError
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, upgradeError := nodeGroupCallsFor(kind).upgrade(requestContext, clusterID, groupName, instanceType, drainOptionsFromFlags(cmd), wait)
		if upgradeError != nil {
			return asyncWriteError("upgrading node group", wait, upgradeError)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newNodeGroupSubmittedResult("Node group instance-type update", clusterID)); err != nil {
				return err
			} else if handled {
				return nil
			}
			printNodeGroupWriteSubmitted("Node group instance-type update", clusterID)
			return nil
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' instance type upgraded. %d node(s) affected.\n", result.GroupName, result.Updated)
		if result.Notice != "" {
			fmt.Printf("Note: %s\n", result.Notice)
		}
		return nil
	},
}

var clusterNodeGroupDeleteCmd = &cobra.Command{
	Use:   "delete <cluster_id|name> <group_name>",
	Short: "Delete a node group and all its nodes",
	Long: `Delete a node group and every node in it.

` + guardedDrainHelp + `

Examples:
  ankra cluster node-group delete <cluster_id> gpu-workers
  ankra cluster node-group delete <cluster_id> gpu-workers --force-drain --yes`,
	Args: nodeGroupArgs(2, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		groupName := args[1]
		yes, _ := cmd.Flags().GetBool("yes")
		drainOptions := drainOptionsFromFlags(cmd)
		kind, kindError := resolveNodeGroupClusterKind(args[0], clusterID)
		if kindError != nil {
			return kindError
		}

		consequence := "This deletes all its nodes!"
		if drainOptions.ForceDrain {
			consequence = "This deletes all its nodes, without honouring their PodDisruptionBudgets!"
		}
		if err := confirmPrompt(
			cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Delete node group %q from cluster %s? %s [y/N]: ", groupName, clusterTarget(args[0], clusterID), consequence),
			yes,
		); err != nil {
			return err
		}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		result, submitted, deleteError := nodeGroupCallsFor(kind).deleteGroup(requestContext, clusterID, groupName, drainOptions, wait)
		if deleteError != nil {
			return asyncWriteError("deleting node group", wait, deleteError)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newNodeGroupSubmittedResult("Node group delete", clusterID)); err != nil {
				return err
			} else if handled {
				return nil
			}
			printNodeGroupWriteSubmitted("Node group delete", clusterID)
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
	Use:   "get <cluster_id|name> <group_name>",
	Short: "Show autoscaling settings for a node group",
	Args:  nodeGroupArgs(2, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		groupName := args[1]
		kind, kindError := resolveNodeGroupClusterKind(args[0], clusterID)
		if kindError != nil {
			return kindError
		}

		autoscalingGet := nodeGroupCallsFor(kind).autoscalingGet
		if autoscalingGet == nil {
			return unsupportedAutoscalingKindError(args[0], kind)
		}

		result, getError := autoscalingGet(clusterID, groupName)
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
	Use:   "set <cluster_id|name> <group_name>",
	Short: "Enable or disable autoscaling for a node group",
	Long: `Enable autoscaling with --enabled --min <n> --max <n>, or disable it with
--enabled=false. min must be at least 1 (scale-to-zero is not supported);
enabling requires the cluster's ankra-agent to be recent enough to serve
the autoscaler, and installs the Cluster Autoscaler on first enable.`,
	Args: nodeGroupArgs(2, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		groupName := args[1]
		enabled, _ := cmd.Flags().GetBool("enabled")
		minCount, _ := cmd.Flags().GetInt("min")
		maxCount, _ := cmd.Flags().GetInt("max")
		kind, kindError := resolveNodeGroupClusterKind(args[0], clusterID)
		if kindError != nil {
			return kindError
		}

		req := client.NodeGroupAutoscalingRequest{Enabled: enabled, MinCount: minCount, MaxCount: maxCount}

		requestContext, cancelRequestContext, wait, err := nodeGroupAsyncContext(cmd)
		if err != nil {
			return err
		}
		defer cancelRequestContext()

		autoscalingSet := nodeGroupCallsFor(kind).autoscalingSet
		if autoscalingSet == nil {
			return unsupportedAutoscalingKindError(args[0], kind)
		}

		result, submitted, setError := autoscalingSet(requestContext, clusterID, groupName, req, wait)
		if setError != nil {
			return asyncWriteError("updating node group autoscaling", wait, setError)
		}
		if submitted {
			if handled, err := renderStructured(cmd, newNodeGroupSubmittedResult("Node group autoscaling update", clusterID)); err != nil {
				return err
			} else if handled {
				return nil
			}
			printNodeGroupWriteSubmitted("Node group autoscaling update", clusterID)
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

var clusterNodeGroupLabelsCmd = &cobra.Command{
	Use:   "labels <cluster_id|name> <group_name>",
	Short: "Set labels on all nodes in a node group",
	Long:  "Replace the labels on every node in the group. Pass --labels as a comma-separated list of key=value pairs, or --clear to remove all labels. The cloud provider is detected automatically from the cluster.",
	Args:  nodeGroupArgs(2, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		groupName := args[1]
		clear, _ := cmd.Flags().GetBool("clear")
		labelsChanged := cmd.Flags().Changed("labels")
		labelsFlag, _ := cmd.Flags().GetString("labels")

		if clear && labelsChanged {
			return withExitCode(exitUsage, errors.New("pass either --labels k=v or --clear, not both"))
		}
		if !clear && !labelsChanged {
			return withExitCode(exitUsage, errors.New("provide --labels k=v to set labels, or pass --clear to remove all labels"))
		}

		labels, parseError := parseLabelsFlag(labelsFlag)
		if parseError != nil {
			return fmt.Errorf("invalid --labels: %w", parseError)
		}
		kind, kindError := resolveNodeGroupClusterKind(args[0], clusterID)
		if kindError != nil {
			return kindError
		}

		requestContext, cancelRequestContext, wait, contextError := nodeGroupAsyncContext(cmd)
		if contextError != nil {
			return contextError
		}
		defer cancelRequestContext()

		result, submitted, updateError := nodeGroupCallsFor(kind).labels(requestContext, clusterID, groupName, labels, wait)
		if updateError != nil {
			return asyncWriteError("updating node group labels", wait, updateError)
		}
		if submitted {
			if handled, renderError := renderStructured(cmd, newNodeGroupSubmittedResult("Node group labels update", clusterID)); renderError != nil {
				return renderError
			} else if handled {
				return nil
			}
			printNodeGroupWriteSubmitted("Node group labels update", clusterID)
			return nil
		}
		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' labels updated. %d node(s) affected.\n", result.GroupName, result.Updated)
		return nil
	},
}

var clusterNodeGroupTaintsCmd = &cobra.Command{
	Use:   "taints <cluster_id|name> <group_name>",
	Short: "Set taints on all nodes in a node group",
	Long:  "Replace the taints on every node in the group. Pass --taints as a comma-separated list of key=value:Effect (value optional, effect defaults to NoSchedule), or --clear to remove all taints. The cloud provider is detected automatically from the cluster.",
	Args:  nodeGroupArgs(2, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		groupName := args[1]
		clear, _ := cmd.Flags().GetBool("clear")
		taintsChanged := cmd.Flags().Changed("taints")
		taintsFlag, _ := cmd.Flags().GetString("taints")

		if clear && taintsChanged {
			return withExitCode(exitUsage, errors.New("pass either --taints k=v:Effect or --clear, not both"))
		}
		if !clear && !taintsChanged {
			return withExitCode(exitUsage, errors.New("provide --taints k=v:Effect to set taints, or pass --clear to remove all taints"))
		}

		taints, parseError := parseTaintsFlag(taintsFlag)
		if parseError != nil {
			return fmt.Errorf("invalid --taints: %w", parseError)
		}
		kind, kindError := resolveNodeGroupClusterKind(args[0], clusterID)
		if kindError != nil {
			return kindError
		}

		requestContext, cancelRequestContext, wait, contextError := nodeGroupAsyncContext(cmd)
		if contextError != nil {
			return contextError
		}
		defer cancelRequestContext()

		result, submitted, updateError := nodeGroupCallsFor(kind).taints(requestContext, clusterID, groupName, taints, wait)
		if updateError != nil {
			return asyncWriteError("updating node group taints", wait, updateError)
		}
		if submitted {
			if handled, renderError := renderStructured(cmd, newNodeGroupSubmittedResult("Node group taints update", clusterID)); renderError != nil {
				return renderError
			} else if handled {
				return nil
			}
			printNodeGroupWriteSubmitted("Node group taints update", clusterID)
			return nil
		}
		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		fmt.Printf("Node group '%s' taints updated. %d node(s) affected.\n", result.GroupName, result.Updated)
		return nil
	},
}

func init() {
	clusterNodeGroupAddCmd.Flags().String("name", "", "Node group name (required)")
	clusterNodeGroupAddCmd.Flags().String("instance-type", "", "Server type / flavor / plan for nodes (required)")
	clusterNodeGroupAddCmd.Flags().Int("count", 1, "Number of nodes")
	clusterNodeGroupAddCmd.Flags().String("availability-zone", "", "Pin every node of the group to one availability zone, on OVH 3-AZ regions (e.g. eu-west-par-b). See 'ankra cluster ovh regions --with-zones'. Pin the group when it runs zonal storage: an OVH volume cannot attach from another zone. Omitted on a zone-spread cluster, each node takes the zone with the fewest instances cluster-wide, so a one-node group lands wherever the cluster is thinnest, not where its name suggests; on a cluster with no zone pool OVH chooses")
	clusterNodeGroupAddCmd.Flags().String("zone", "", "Pin every node of the group to one zone of an UpCloud multi-zone cluster's pool (e.g. se-sto1). Pin the group when it runs zonal storage: an UpCloud volume cannot attach from another zone. Omitted spreads the group across the pool; refused on a single-zone cluster")
	clusterNodeGroupAddCmd.Flags().String("user-data-file", "", "Path to a cloud-init user-data file for the group (OVH clusters only). Applied verbatim at first boot by every instance the group ever creates, replacements included; Ankra sends no cloud-init of its own, so the document is not merged with anything. Max 65535 bytes. Use for provision-time disk layouts, e.g. carving a partition for LUKS before growpart runs")
	clusterNodeGroupAddCmd.Flags().String("labels", "",
		"Comma-separated key=value labels every node of the group carries, from its first boot")
	clusterNodeGroupAddCmd.Flags().String("taints", "",
		"Comma-separated key=value:Effect taints every node of the group registers with (value optional, "+
			"effect defaults to NoSchedule); a tainted group runs only pods that tolerate them, such as "+
			"pipeline pods placed with 'ankra cluster agent ci set --node-group'")
	clusterNodeGroupAddCmd.Flags().Int("disk-gib", 0, "Root disk size in GiB of every server the group creates, scale-ups included (Ankra Cloud clusters only; default: the plan's included disk). Ankra Cloud refuses a size below the plan's and the template's minimum")
	clusterNodeGroupAddCmd.Flags().String("disk-tier", "", "Storage tier of the root disk of every server the group creates, scale-ups included (Ankra Cloud clusters only): local-nvme (the node's own disks, the choice for CI nodes), standard, maxiops or hdd (the zone's Ankra Storage). Default: the zone's default tier")
	clusterNodeGroupAddCmd.Flags().String("cpu-pool", "", "CPU pool of the cluster's zone every server the group creates is pinned to, scale-ups included (Ankra Cloud clusters only). Pick one with 'ankra cluster ankracloud cpu-pools --cluster <cluster>': a pool of x86-64 level 3 has AVX2, which images built with current Python, Rust or Go wheels may need (they crash with an illegal instruction, exit 132, on a lower level). Default: the Cloud places each server")
	_ = clusterNodeGroupAddCmd.MarkFlagRequired("name")
	_ = clusterNodeGroupAddCmd.MarkFlagRequired("instance-type")

	clusterNodeGroupAutoscalingSetCmd.Flags().Bool("enabled", true, "Enable (true) or disable (false) autoscaling")
	clusterNodeGroupAutoscalingSetCmd.Flags().Int("min", 1, "Minimum node count while autoscaling (>= 1)")
	clusterNodeGroupAutoscalingSetCmd.Flags().Int("max", 5, "Maximum node count while autoscaling")

	clusterNodeGroupLabelsCmd.Flags().String("labels", "", "Comma-separated key=value pairs to set on the group")
	clusterNodeGroupLabelsCmd.Flags().Bool("clear", false, "Remove all labels from the node group")
	clusterNodeGroupTaintsCmd.Flags().String("taints", "", "Comma-separated key=value:Effect taints to set on the group")
	clusterNodeGroupTaintsCmd.Flags().Bool("clear", false, "Remove all taints from the node group")

	registerAsyncWriteFlags(clusterNodeGroupAddCmd)
	registerAsyncWriteFlags(clusterNodeGroupScaleCmd)
	registerAsyncWriteFlags(clusterNodeGroupUpgradeCmd)
	registerAsyncWriteFlags(clusterNodeGroupDeleteCmd)
	registerAsyncWriteFlags(clusterNodeGroupRemoveNodeCmd)
	clusterNodeGroupRemoveNodeCmd.Flags().Lookup("wait").Usage = removeNodeWaitUsage
	registerAsyncWriteFlags(clusterNodeGroupAutoscalingSetCmd)
	registerAsyncWriteFlags(clusterNodeGroupLabelsCmd)
	registerAsyncWriteFlags(clusterNodeGroupTaintsCmd)

	clusterNodeGroupDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	clusterNodeGroupRemoveNodeCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	registerForceDrainFlag(clusterNodeGroupScaleCmd, forceDrainScaleUsage)
	registerForceDrainFlag(clusterNodeGroupUpgradeCmd, forceDrainResizeUsage)
	registerForceDrainFlag(clusterNodeGroupDeleteCmd, forceDrainDeleteUsage)
	registerForceDrainFlag(clusterNodeGroupRemoveNodeCmd, forceDrainRemoveNodeUsage)

	registerStructuredOutputFlags(
		clusterNodeGroupListCmd,
		clusterNodeGroupAddCmd,
		clusterNodeGroupScaleCmd,
		clusterNodeGroupUpgradeCmd,
		clusterNodeGroupDeleteCmd,
		clusterNodeGroupRemoveNodeCmd,
		clusterNodeGroupAutoscalingGetCmd,
		clusterNodeGroupAutoscalingSetCmd,
		clusterNodeGroupLabelsCmd,
		clusterNodeGroupTaintsCmd,
	)

	clusterNodeGroupAutoscalingCmd.AddCommand(clusterNodeGroupAutoscalingGetCmd)
	clusterNodeGroupAutoscalingCmd.AddCommand(clusterNodeGroupAutoscalingSetCmd)

	clusterNodeGroupCmd.AddCommand(clusterNodeGroupListCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupAddCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupScaleCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupUpgradeCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupDeleteCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupRemoveNodeCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupAutoscalingCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupLabelsCmd)
	clusterNodeGroupCmd.AddCommand(clusterNodeGroupTaintsCmd)

	clusterCmd.AddCommand(clusterNodeGroupCmd)
}

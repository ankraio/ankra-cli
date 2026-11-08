package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The node maintenance commands of the Ankra-provisioned providers: etcd
// membership and the rolling OS patch lane, the CLI side of the platform's
// get_etcd_members / remove_etcd_member, *_os_patch* and apply_node_os_policy
// tools. Each provider
// command carries them only where the platform runs the lane, so an
// unsupported provider has no command rather than one that always fails.

// defaultNodeMaintenanceTimeout bounds the client's wait for a dispatched
// job. The platform waits up to two and a half minutes for an etcd removal
// and two minutes for the node OS policy before handing back the operation to
// poll, so the client budget sits past both.
const defaultNodeMaintenanceTimeout = 4 * time.Minute

// etcdLaneProviders and osPatchLaneProviders are the provider segments the
// platform serves the etcd membership and rolling OS patch routes for;
// nodeOSPolicyProviders those with a node OS policy job.
var (
	etcdLaneProviders     = []string{"hetzner", "ovh", "upcloud", "digitalocean", "scaleway", "aws", "ankracloud"}
	osPatchLaneProviders  = etcdLaneProviders
	nodeOSPolicyProviders = []string{"hetzner", "ovh", "upcloud", "digitalocean", "aws", "ankracloud", "proxmox", "morpheus"}
)

// newEtcdCommand builds `ankra cluster <provider> etcd`.
func newEtcdCommand(provider string, label string) *cobra.Command {
	command := &cobra.Command{
		Use:   "etcd",
		Short: fmt.Sprintf("Inspect and repair the etcd membership of a %s cluster", label),
		Long: `Read the cluster's etcd membership from one control-plane node, and remove
the stale member a dead control-plane node left behind so its replacement can
join. Both run over the platform's bastion SSH lane as tracked operations and
need clusters.operate on the cluster. Find node ids with 'nodes list'.`,
	}

	members := &cobra.Command{
		Use:   "members <cluster_id|name> <node_id>",
		Short: "List etcd members, their health and the quorum margin",
		Long: `Run 'etcdctl member list' and the endpoint health check on the control-plane
node named and report every member (hex and decimal id), its health, any
alarms and the quorum summary with its failure tolerance. Nothing changes.
Run it on a healthy control-plane node when another one is dead.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			requestContext, cancel, timeoutError := nodeMaintenanceContext(cmd)
			if timeoutError != nil {
				return timeoutError
			}
			defer cancel()
			result, membersError := apiClient.GetEtcdMembers(requestContext, provider, clusterID, args[1])
			if membersError != nil {
				return nodeMaintenanceError("reading etcd members", membersError)
			}
			return renderNodeMaintenanceResult(cmd, "etcd member inspection", result)
		},
	}

	remove := &cobra.Command{
		Use:   "remove <cluster_id|name> <node_id> <member_id>",
		Short: "Remove one stale etcd member through a healthy control-plane node",
		Long: `Remove one etcd member, named by the hex or decimal id 'etcd members'
reported, executing on the healthy control-plane node named. The platform
re-reads live membership first and refuses a healthy member, a member whose
health it cannot read, the node's own member, an ambiguous id, and any
removal that would break quorum.

This is destructive and cannot be undone: the command asks first (--yes
skips, declining exits 4).

  ankra cluster hetzner etcd members prod <healthy-node-id>
  ankra cluster hetzner etcd remove prod <healthy-node-id> 8e9e05c52164694d`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			yes, _ := cmd.Flags().GetBool("yes")
			prompt := fmt.Sprintf("Remove etcd member %s from cluster %s (run on node %s)? [y/N]: ",
				args[2], clusterTarget(args[0], clusterID), args[1])
			if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), prompt, yes); confirmError != nil {
				return confirmError
			}
			requestContext, cancel, timeoutError := nodeMaintenanceContext(cmd)
			if timeoutError != nil {
				return timeoutError
			}
			defer cancel()
			result, removeError := apiClient.RemoveEtcdMember(requestContext, provider, clusterID, args[1], args[2])
			if removeError != nil {
				return nodeMaintenanceError("removing the etcd member", removeError)
			}
			return renderNodeMaintenanceResult(cmd, "etcd member removal", result)
		},
	}
	remove.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	for _, subcommand := range []*cobra.Command{members, remove} {
		subcommand.Flags().Duration("timeout", defaultNodeMaintenanceTimeout, "Maximum time to wait for the job's report")
	}
	registerStructuredOutputFlags(members, remove)
	command.AddCommand(members, remove)
	return command
}

// newOSPatchesCommand builds `ankra cluster <provider> os-patches`. hasLane
// adds the rolling patch lane's commands, hasNodePolicy the node OS update
// hold.
func newOSPatchesCommand(provider string, label string, hasLane bool, hasNodePolicy bool) *cobra.Command {
	command := &cobra.Command{
		Use:   "os-patches",
		Short: fmt.Sprintf("Roll OS security patches across a %s cluster, one host at a time", label),
		Long: `Unattended upgrades hold back the packages whose maintainer scripts restart
networking in place; the rolling OS patch lane installs them instead, one
host at a time, drained and returned to service before the next.

  status       the policy, the newest run host by host, the last dry run
  plan         the dry run: what each host would install
  run          start the rolling patch (asks first)
  policy set   change the maintenance window and the opt-in
  node-policy  write or remove the node OS update hold by hand`,
	}

	if hasLane {
		command.AddCommand(newOSPatchesStatusCommand(provider), newOSPatchesPlanCommand(provider),
			newOSPatchesRunCommand(provider), newOSPatchesPolicyCommand(provider))
	}
	if hasNodePolicy {
		command.AddCommand(newOSPatchesNodePolicyCommand(provider))
	}
	return command
}

func newOSPatchesStatusCommand(provider string) *cobra.Command {
	command := &cobra.Command{
		Use:   "status <cluster_id|name>",
		Short: "Show the OS patch policy, the newest run and the security-lag verdict",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			status, statusError := apiClient.GetOSPatchStatus(provider, clusterID)
			if statusError != nil {
				return fmt.Errorf("reading the OS patch status: %w", statusError)
			}
			if rendered, renderError := renderStructured(cmd, status); rendered || renderError != nil {
				return renderError
			}
			return writeDocument(cmd.OutOrStdout(), "OS patch status", status)
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func newOSPatchesPlanCommand(provider string) *cobra.Command {
	command := &cobra.Command{
		Use:   "plan <cluster_id|name>",
		Short: "Dry-run the OS patches on every host; nothing is installed",
		Long: `Run the dry run on every host and report what each would install, which
held-back security updates are pending, whether a reboot would follow, and
the order 'run' would take. The platform waits about 40 seconds; a slower
plan hands back its operation id, and 'status' shows it once it finishes.
Needs clusters.operate.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			requestContext, cancel, timeoutError := nodeMaintenanceContext(cmd)
			if timeoutError != nil {
				return timeoutError
			}
			defer cancel()
			plan, planError := apiClient.PlanOSPatches(requestContext, provider, clusterID)
			if planError != nil {
				return nodeMaintenanceError("planning the OS patches", planError)
			}
			if rendered, renderError := renderStructured(cmd, plan); rendered || renderError != nil {
				return renderError
			}
			return writeDocument(cmd.OutOrStdout(), "OS patch plan", plan)
		},
	}
	command.Flags().Duration("timeout", defaultNodeMaintenanceTimeout, "Maximum time to wait for the plan")
	registerStructuredOutputFlags(command)
	return command
}

func newOSPatchesRunCommand(provider string) *cobra.Command {
	command := &cobra.Command{
		Use:   "run <cluster_id|name>",
		Short: "Start the rolling OS patch run, one host at a time",
		Long: `Start the rolling patch: each host is drained, patched, rebooted when the
packages ask for it, and returned to service before the next. Workloads move
between nodes while it runs, so the command asks first (--yes skips,
declining exits 4).

Outside the policy's maintenance window the platform refuses unless
--outside-window is passed. Stateful nodes follow the policy unless
--include-stateful-nodes or --exclude-stateful-nodes says otherwise; the
bastion is patched last unless --exclude-bastion. Follow the run with
'status' or 'ankra cluster operations list'. Needs clusters.operate.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			request := client.OSPatchRunRequest{}
			request.OutsideWindow, _ = cmd.Flags().GetBool("outside-window")
			if cmd.Flags().Changed("include-stateful-nodes") || cmd.Flags().Changed("exclude-stateful-nodes") {
				includeStateful := cmd.Flags().Changed("include-stateful-nodes")
				request.IncludeStatefulNodes = &includeStateful
			}
			if excludeBastion, _ := cmd.Flags().GetBool("exclude-bastion"); excludeBastion {
				includeBastion := false
				request.IncludeBastion = &includeBastion
			}
			yes, _ := cmd.Flags().GetBool("yes")
			prompt := fmt.Sprintf("Start the rolling OS patch run on cluster %s? Every host is drained and may reboot. [y/N]: ",
				clusterTarget(args[0], clusterID))
			if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), prompt, yes); confirmError != nil {
				return confirmError
			}
			requestContext, cancel, timeoutError := nodeMaintenanceContext(cmd)
			if timeoutError != nil {
				return timeoutError
			}
			defer cancel()
			run, runError := apiClient.RunOSPatches(requestContext, provider, clusterID, request)
			if runError != nil {
				return nodeMaintenanceError("starting the OS patch run", runError)
			}
			if rendered, renderError := renderStructured(cmd, run); rendered || renderError != nil {
				return renderError
			}
			return writeDocument(cmd.OutOrStdout(), "OS patch run started", run)
		},
	}
	command.Flags().Bool("outside-window", false, "Start now even though the maintenance window is closed")
	command.Flags().Bool("include-stateful-nodes", false, "Patch nodes running stateful workloads, whatever the policy says")
	command.Flags().Bool("exclude-stateful-nodes", false, "Leave nodes running stateful workloads out, whatever the policy says")
	command.Flags().Bool("exclude-bastion", false, "Leave the bastion out of this run")
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	command.Flags().Duration("timeout", defaultNodeMaintenanceTimeout, "Maximum time to wait for the run to start")
	command.MarkFlagsMutuallyExclusive("include-stateful-nodes", "exclude-stateful-nodes")
	registerStructuredOutputFlags(command)
	return command
}

func newOSPatchesPolicyCommand(provider string) *cobra.Command {
	command := &cobra.Command{
		Use:   "policy",
		Short: "Show or change the cluster's OS patch policy",
	}

	get := &cobra.Command{
		Use:   "get <cluster_id|name>",
		Short: "Show the OS patch policy and its maintenance window",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			status, statusError := apiClient.GetOSPatchStatus(provider, clusterID)
			if statusError != nil {
				return fmt.Errorf("reading the OS patch policy: %w", statusError)
			}
			policy, _ := status["policy"].(map[string]any)
			if rendered, renderError := renderStructured(cmd, policy); rendered || renderError != nil {
				return renderError
			}
			return writeDocument(cmd.OutOrStdout(), "OS patch policy", policy)
		},
	}

	set := &cobra.Command{
		Use:   "set <cluster_id|name>",
		Short: "Change the OS patch policy; a setting not named is kept",
		Long: `Change the cluster's OS patch policy. Only the flags passed are changed.
Turning the policy off also removes the node OS update hold from every host,
and is refused while a run or a node OS policy operation is in flight.
Needs clusters.write.

  ankra cluster hetzner os-patches policy set prod --enable --window-days sat,sun --window-start-hour 2 --window-duration-hours 4
  ankra cluster hetzner os-patches policy set prod --disable`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			update, parseError := osPatchPolicyUpdateFromFlags(cmd)
			if parseError != nil {
				return parseError
			}
			requestContext, cancel, timeoutError := nodeMaintenanceContext(cmd)
			if timeoutError != nil {
				return timeoutError
			}
			defer cancel()
			policy, setError := apiClient.SetOSPatchPolicy(requestContext, provider, clusterID, update)
			if setError != nil {
				return nodeMaintenanceError("updating the OS patch policy", setError)
			}
			if rendered, renderError := renderStructured(cmd, policy); rendered || renderError != nil {
				return renderError
			}
			return writeDocument(cmd.OutOrStdout(), "OS patch policy updated", policy)
		},
	}
	set.Flags().Bool("enable", false, "Turn the rolling OS patch policy on")
	set.Flags().Bool("disable", false, "Turn the rolling OS patch policy off")
	set.Flags().StringSlice("window-days", nil, "Days the maintenance window opens (mon,tue,...)")
	set.Flags().Int("window-start-hour", 0, "Hour of the day (UTC, 0-23) the window opens")
	set.Flags().Int("window-duration-hours", 0, "How many hours the window stays open")
	set.Flags().Int("cadence-days", 0, "Days between scheduled runs")
	set.Flags().Int("security-lag-days", 0, "Days a held security update may wait before the cluster is reported lagging")
	set.Flags().Bool("include-stateful-nodes", false, "Patch nodes running stateful workloads")
	set.Flags().Bool("exclude-stateful-nodes", false, "Leave nodes running stateful workloads out")
	set.Flags().Duration("timeout", defaultNodeMaintenanceTimeout, "Maximum time to wait for the change")
	set.MarkFlagsMutuallyExclusive("enable", "disable")
	set.MarkFlagsMutuallyExclusive("include-stateful-nodes", "exclude-stateful-nodes")

	registerStructuredOutputFlags(get, set)
	command.AddCommand(get, set)
	return command
}

func newOSPatchesNodePolicyCommand(provider string) *cobra.Command {
	command := &cobra.Command{
		Use:   "node-policy <cluster_id|name>",
		Short: "Write, or with --revert remove, the node OS update hold",
		Long: `Write the node OS update hold - the apt policy that keeps unattended
upgrades away from the packages that restart networking - on the nodes named
with --node, on every node with --all-nodes, and on the bastion with
--include-bastion. The rolling OS patch lane writes and lifts it on its own;
this is the by-hand route. Writing it is refused unless the cluster's OS
patch policy is enabled; --revert removes it and is always allowed.

The command asks first (--yes skips, declining exits 4). Needs
clusters.operate.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			request := client.NodeOSPolicyRequest{}
			request.NodeIDs, _ = cmd.Flags().GetStringSlice("node")
			request.AllNodes, _ = cmd.Flags().GetBool("all-nodes")
			request.IncludeBastion, _ = cmd.Flags().GetBool("include-bastion")
			request.Revert, _ = cmd.Flags().GetBool("revert")
			if len(request.NodeIDs) == 0 && !request.AllNodes && !request.IncludeBastion {
				return withExitCode(exitUsage, errors.New("nothing to apply the policy to: pass --node, --all-nodes or --include-bastion"))
			}
			verb := "Write the node OS update hold on"
			if request.Revert {
				verb = "Remove the node OS update hold from"
			}
			yes, _ := cmd.Flags().GetBool("yes")
			prompt := fmt.Sprintf("%s %s on cluster %s? [y/N]: ", verb, nodePolicyTargets(request), clusterTarget(args[0], clusterID))
			if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(), prompt, yes); confirmError != nil {
				return confirmError
			}
			requestContext, cancel, timeoutError := nodeMaintenanceContext(cmd)
			if timeoutError != nil {
				return timeoutError
			}
			defer cancel()
			result, applyError := apiClient.ApplyNodeOSPolicy(requestContext, provider, clusterID, request)
			if applyError != nil {
				return nodeMaintenanceError("applying the node OS update policy", applyError)
			}
			return renderNodeMaintenanceResult(cmd, "node OS update policy", result)
		},
	}
	command.Flags().StringSlice("node", nil, "Node id to apply the policy to (repeatable)")
	command.Flags().Bool("all-nodes", false, "Apply the policy to every node")
	command.Flags().Bool("include-bastion", false, "Apply the policy to the bastion too")
	command.Flags().Bool("revert", false, "Remove the hold instead of writing it")
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	command.Flags().Duration("timeout", defaultNodeMaintenanceTimeout, "Maximum time to wait for the per-host report")
	command.MarkFlagsMutuallyExclusive("node", "all-nodes")
	registerStructuredOutputFlags(command)
	return command
}

// osPatchPolicyUpdateFromFlags reads `policy set`'s flags; a flag not passed
// is not sent, so the platform keeps that setting.
func osPatchPolicyUpdateFromFlags(cmd *cobra.Command) (client.OSPatchPolicyUpdate, error) {
	update := client.OSPatchPolicyUpdate{}
	flags := cmd.Flags()
	if flags.Changed("enable") || flags.Changed("disable") {
		enabled := flags.Changed("enable")
		update.Enabled = &enabled
	}
	if flags.Changed("include-stateful-nodes") || flags.Changed("exclude-stateful-nodes") {
		includeStateful := flags.Changed("include-stateful-nodes")
		update.IncludeStatefulNodes = &includeStateful
	}
	if flags.Changed("window-days") {
		update.WindowDays, _ = flags.GetStringSlice("window-days")
	}
	for _, integer := range []struct {
		flagName string
		target   **int
	}{
		{"window-start-hour", &update.WindowStartHourUTC},
		{"window-duration-hours", &update.WindowDurationHours},
		{"cadence-days", &update.CadenceDays},
		{"security-lag-days", &update.SecurityLagDays},
	} {
		if flags.Changed(integer.flagName) {
			value, _ := flags.GetInt(integer.flagName)
			*integer.target = &value
		}
	}
	if update.Enabled == nil && update.IncludeStatefulNodes == nil && update.WindowDays == nil &&
		update.WindowStartHourUTC == nil && update.WindowDurationHours == nil && update.CadenceDays == nil &&
		update.SecurityLagDays == nil {
		return update, withExitCode(exitUsage, errors.New("nothing to change: pass --enable, --disable or a window, cadence or lag flag"))
	}
	return update, nil
}

func nodePolicyTargets(request client.NodeOSPolicyRequest) string {
	var targets []string
	switch {
	case request.AllNodes:
		targets = append(targets, "every node")
	case len(request.NodeIDs) == 1:
		targets = append(targets, "node "+request.NodeIDs[0])
	case len(request.NodeIDs) > 1:
		targets = append(targets, fmt.Sprintf("%d nodes", len(request.NodeIDs)))
	}
	if request.IncludeBastion {
		targets = append(targets, "the bastion")
	}
	return strings.Join(targets, " and ")
}

// nodeMaintenanceContext bounds a dispatched job's wait by --timeout.
func nodeMaintenanceContext(cmd *cobra.Command) (context.Context, context.CancelFunc, error) {
	timeout, timeoutFlagError := cmd.Flags().GetDuration("timeout")
	if timeoutFlagError != nil {
		return nil, nil, fmt.Errorf("reading --timeout: %w", timeoutFlagError)
	}
	requestContext, cancel := context.WithTimeout(context.Background(), timeout)
	return requestContext, cancel, nil
}

// nodeMaintenanceError wraps a failed call; a --timeout that ran out exits 5.
func nodeMaintenanceError(action string, callError error) error {
	wrapped := fmt.Errorf("%s: %w", action, callError)
	if errors.Is(callError, context.DeadlineExceeded) {
		return withExitCode(exitWaitTimeout, wrapped)
	}
	return wrapped
}

// renderNodeMaintenanceResult prints a dispatched job: the operation always,
// the report when the job finished inside the platform's budget, the poll
// hint when it did not. A job that finished and failed exits 1 after its
// output.
func renderNodeMaintenanceResult(cmd *cobra.Command, label string, result *client.NodeMaintenanceResult) error {
	if rendered, renderError := renderStructured(cmd, result); renderError != nil {
		return renderError
	} else if !rendered {
		out := cmd.OutOrStdout()
		verb := "dispatched"
		if result.Attached {
			verb = "attached to the run already in flight"
		}
		_, _ = fmt.Fprintf(out, "%s %s.\n", capitalise(label), verb)
		_, _ = fmt.Fprintf(out, "  Operation ID: %s\n", result.OperationID)
		_, _ = fmt.Fprintf(out, "  Status:       %s\n", result.Status)
		if !result.Completed {
			_, _ = fmt.Fprintf(out, "\nStill running. Follow it with:\n  ankra cluster operations list %s\n", result.OperationID)
		} else if len(result.Report) > 0 {
			if writeError := writeDocument(out, "Report", result.Report); writeError != nil {
				return writeError
			}
		}
	}
	if !result.Completed {
		return nil
	}
	if result.ErrorExcerpt != "" {
		return fmt.Errorf("the %s failed (operation %s): %s", label, result.OperationID, result.ErrorExcerpt)
	}
	if result.Status != "success" {
		return fmt.Errorf("the %s ended %s (operation %s)", label, result.Status, result.OperationID)
	}
	return nil
}

// writeDocument prints a heading and the platform's answer as indented JSON.
func writeDocument(out io.Writer, heading string, document any) error {
	encoded, encodeError := json.MarshalIndent(document, "  ", "  ")
	if encodeError != nil {
		return fmt.Errorf("encoding %s: %w", strings.ToLower(heading), encodeError)
	}
	_, _ = fmt.Fprintf(out, "%s:\n  %s\n", heading, encoded)
	return nil
}

func capitalise(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func init() {
	providerCommands := map[string]struct {
		command *cobra.Command
		label   string
	}{
		"hetzner":      {hetznerCmd, "Hetzner"},
		"ovh":          {ovhCmd, "OVH"},
		"upcloud":      {upcloudCmd, "UpCloud"},
		"digitalocean": {digitaloceanCmd, "DigitalOcean"},
		"scaleway":     {scalewayCmd, "Scaleway"},
		"aws":          {awsCmd, "AWS"},
		"ankracloud":   {ankraCloudCmd, "Ankra Cloud"},
		"proxmox":      {proxmoxCmd, "Proxmox VE"},
		"morpheus":     {morpheusCmd, "HPE Morpheus"},
	}
	for _, provider := range []string{"hetzner", "ovh", "upcloud", "digitalocean", "scaleway", "aws", "ankracloud", "proxmox", "morpheus"} {
		entry := providerCommands[provider]
		hasEtcd := containsString(etcdLaneProviders, provider)
		hasLane := containsString(osPatchLaneProviders, provider)
		hasNodePolicy := containsString(nodeOSPolicyProviders, provider)
		if hasEtcd {
			entry.command.AddCommand(newEtcdCommand(provider, entry.label))
		}
		if hasLane || hasNodePolicy {
			entry.command.AddCommand(newOSPatchesCommand(provider, entry.label, hasLane, hasNodePolicy))
		}
	}
}

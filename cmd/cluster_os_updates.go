package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// `ankra cluster os-updates`: the OS updates mode of a cluster Ankra built
// (ankra-qdrg24.3.12.3). It drives the same platform routes as `ankra cluster
// <provider> os-patches`, but reads the provider from the cluster, so a
// person names the cluster and nothing else.

// osUpdatesTarget is the cluster an os-updates command acts on and the
// provider segment of its routes.
type osUpdatesTarget struct {
	typed     string
	clusterID string
	name      string
	provider  string
}

// display names the cluster the way the person recognises it.
func (target osUpdatesTarget) display() string {
	if target.name != "" {
		return target.name
	}
	return target.typed
}

// osUpdatesWindow is the policy's maintenance window.
type osUpdatesWindow struct {
	Days          []string `json:"days"`
	StartHourUTC  int      `json:"start_hour_utc"`
	DurationHours int      `json:"duration_hours"`
}

// osUpdatesHoldRevert is the removal of the node OS update hold from hosts a
// change made advisory.
type osUpdatesHoldRevert struct {
	OperationID string `json:"operation_id"`
	NotStarted  string `json:"not_started"`
}

// osUpdatesPolicy is the policy as the platform reports it, on the status
// read and as the answer to a change.
type osUpdatesPolicy struct {
	Mode                  string               `json:"mode"`
	ModeSource            string               `json:"mode_source"`
	DefaultMode           string               `json:"default_mode"`
	ModeReason            string               `json:"mode_reason"`
	ChosenMode            *string              `json:"chosen_mode"`
	NodeGroupModes        map[string]string    `json:"node_group_modes"`
	AutomaticFrom         string               `json:"automatic_from"`
	AutomaticNoticeSentAt string               `json:"automatic_notice_sent_at"`
	Window                osUpdatesWindow      `json:"window"`
	WindowDescription     string               `json:"window_description"`
	NextWindowOpensUTC    string               `json:"next_window_opens_utc"`
	WindowOpenNow         bool                 `json:"window_open_now"`
	CadenceDays           int                  `json:"cadence_days"`
	IncludeStatefulNodes  bool                 `json:"include_stateful_nodes"`
	SecurityLagDays       int                  `json:"security_lag_days"`
	HoldRevert            *osUpdatesHoldRevert `json:"hold_revert"`
	Message               string               `json:"message"`
}

// osUpdatesPlanSummary counts a dry run across the cluster's hosts.
type osUpdatesPlanSummary struct {
	Hosts                   int    `json:"hosts"`
	HostsWithWork           int    `json:"hosts_with_work"`
	HostsUnknown            int    `json:"hosts_unknown"`
	Pending                 int    `json:"pending"`
	SecurityPending         int    `json:"security_pending"`
	HeldBackPending         int    `json:"held_back_pending"`
	HeldBackSecurityPending int    `json:"held_back_security_pending"`
	RebootRequired          int    `json:"reboot_required"`
	ObservedAt              string `json:"observed_at"`
}

// osUpdatesRun is a run as the status read reports it.
type osUpdatesRun struct {
	OperationID  string `json:"operation_id"`
	Status       string `json:"status"`
	Trigger      string `json:"trigger"`
	CreatedAt    string `json:"created_at"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	ErrorExcerpt string `json:"error_excerpt"`
}

// osUpdatesStatus is the status read.
type osUpdatesStatus struct {
	Policy      osUpdatesPolicy `json:"policy"`
	SecurityLag struct {
		State         string `json:"state"`
		DaysPending   int    `json:"days_pending"`
		ThresholdDays int    `json:"threshold_days"`
		Message       string `json:"message"`
	} `json:"security_lag"`
	LastPlan            *osUpdatesPlanSummary `json:"last_plan"`
	LastPlanAt          string                `json:"last_plan_at"`
	LatestRun           *osUpdatesRun         `json:"latest_run"`
	LastSuccessfulRunAt string                `json:"last_successful_run_at"`
	LastRunRecord       *osUpdatesRun         `json:"last_run_record"`
}

// osUpdatesHost is one host of the cluster as a run or dry run names it.
type osUpdatesHost struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	NodeGroup string `json:"node_group"`
	Reason    string `json:"reason"`
}

// osUpdatesHostPlan is one host's dry-run answer.
type osUpdatesHostPlan struct {
	osUpdatesHost
	Status  string `json:"status"`
	Summary *struct {
		Pending         int `json:"pending"`
		SecurityPending int `json:"security_pending"`
		HeldBackPending int `json:"held_back_pending"`
	} `json:"summary"`
	RebootRequired       bool   `json:"reboot_required"`
	StatefulVerdict      string `json:"stateful_verdict"`
	Error                string `json:"error"`
	SimulationFailedNote string `json:"simulation_failed"`
}

// osUpdatesPlan is the dry run's answer.
type osUpdatesPlan struct {
	OperationID string                `json:"operation_id"`
	Completed   bool                  `json:"completed"`
	Hosts       []osUpdatesHostPlan   `json:"hosts"`
	Summary     *osUpdatesPlanSummary `json:"summary"`
	RunOrder    []string              `json:"run_order"`
	Skipped     []osUpdatesHost       `json:"skipped"`
}

// osUpdatesRunStarted is a started run.
type osUpdatesRunStarted struct {
	OperationID string               `json:"operation_id"`
	Order       []osUpdatesHost      `json:"order"`
	Skipped     []osUpdatesHost      `json:"skipped"`
	BastionNote string               `json:"bastion_note"`
	HoldRevert  *osUpdatesHoldRevert `json:"hold_revert"`
}

// The modes a person may choose: for the cluster, default clears its chosen
// mode so its environment decides again; for a node group, inherit removes
// its own mode so it follows the cluster again.
const (
	osUpdatesModeAutomatic = "automatic"
	osUpdatesModeAdvisory  = "advisory"
	osUpdatesModeDefault   = "default"
	osUpdatesModeInherit   = "inherit"
)

func newClusterOSUpdatesCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "os-updates",
		Short: "Show and choose how a cluster's nodes get their OS updates: automatic or advisory",
		Long: fmt.Sprintf(`OS updates are automatic or advisory. The mode is set on the cluster and can
be overridden on a node group; the control planes and the bastion follow the
cluster.

  automatic  Ankra starts the rolling run on its own, inside the maintenance
             window and at the cadence: one node at a time, drained first
             (PodDisruptionBudgets honoured), updated, rebooted only when an
             update asks for it, and back in service before the next. A
             failed node stops the run.
  advisory   nothing is drained or rebooted on its own. The daily dry run
             reports what is pending and how long security updates have
             waited, and a person starts the same rolling run.

When nobody chose a mode, the cluster's environment decides: automatic for a
cluster a person marked non-production, 14 days after the organisation was
told; advisory for production and for a cluster whose environment nobody
confirmed.

  get   the mode, why the cluster has it, the window and the last runs
  set   choose the mode of the cluster or of a node group, or change the window
  plan  the dry run: what each node would install; nothing changes
  run   start the rolling run (asks first)

These commands work on %s clusters; the provider is read from the cluster.

Exit codes: 2 a bad flag or a cluster of another kind, 3 the cluster is not
in the selected organisation, 4 the confirmation was declined, 5 --timeout ran
out, 7 your role lacks the permission, 8 the platform refused (the window is
closed, a run is in flight, a value is out of bounds) and nothing changed.`, osUpdatesProvidersSentence()),
	}
	command.AddCommand(newOSUpdatesGetCommand(), newOSUpdatesSetCommand(), newOSUpdatesPlanCommand(),
		newOSUpdatesRunCommand())
	return command
}

func newOSUpdatesGetCommand() *cobra.Command {
	command := withClusterArgument(clusterSelectionAllowed, &cobra.Command{
		Use:   "get [cluster_id|name]",
		Short: "Show the OS updates mode, why the cluster has it, the window and the last runs",
		Long: `Show the cluster's OS updates mode and why it has it: chosen by a person,
decided by its environment, or waiting out the 14-day notice before it becomes
automatic (with the date it does). Also the node groups with a mode of their
own, the maintenance window, the security-lag verdict, the last dry run and
the last run. Left out, the cluster is --cluster or the one 'ankra cluster
select' saved. Needs clusters.read.`,
		Example: `  ankra cluster os-updates get prod
  ankra cluster os-updates get prod -o json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			typed, clusterID, resolveError := resolveOptionalClusterArg(cmd, args)
			if resolveError != nil {
				return resolveError
			}
			target, targetError := resolveOSUpdatesTarget(typed, clusterID)
			if targetError != nil {
				return targetError
			}
			document, statusError := apiClient.GetOSPatchStatus(target.provider, target.clusterID)
			if statusError != nil {
				return osUpdatesCallError("reading the OS updates of "+target.display(), target, statusError)
			}
			if rendered, renderError := renderStructured(cmd, document); rendered || renderError != nil {
				return renderError
			}
			var status osUpdatesStatus
			if decodeError := decodeOSUpdatesDocument(document, &status); decodeError != nil {
				return decodeError
			}
			printOSUpdatesStatus(cmd.OutOrStdout(), target, status)
			return nil
		},
	})
	registerStructuredOutputFlags(command)
	return command
}

func newOSUpdatesSetCommand() *cobra.Command {
	command := withClusterArgument(clusterMustBeNamed, &cobra.Command{
		Use:   "set <cluster_id|name>",
		Short: "Choose the OS updates mode of the cluster or of a node group, or change the window",
		Long: `Choose how the cluster's nodes get their OS updates. Only what is passed
changes.

--mode alone sets the cluster's mode, which the control planes, the bastion
and every node group without a mode of its own follow:
  automatic  Ankra starts the rolling run on its own inside the window
  advisory   nothing is drained or rebooted on its own; a person runs it
  default    let the cluster's environment decide again

With --node-group, --mode sets that node group's mode instead (repeat
--node-group to set several to the same mode):
  automatic, advisory  the node group's own mode, whatever the cluster's
  inherit              follow the cluster again

A mode chosen here applies at once; choosing advisory before the date 'get'
shows keeps a cluster in its notice period advisory. Making nodes advisory
removes the node OS update hold from them, so the distribution's own updater
installs the held packages again; that change is refused while a run is in
flight. The window flags change when automatic runs may start. Needs
clusters.write.`,
		Example: `  ankra cluster os-updates set staging --mode automatic
  ankra cluster os-updates set prod --mode advisory
  ankra cluster os-updates set prod --node-group batch --mode automatic
  ankra cluster os-updates set prod --node-group batch --mode inherit
  ankra cluster os-updates set staging --mode default
  ankra cluster os-updates set staging --window-days sat,sun --window-start-hour 2 --window-duration-hours 4`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			update, parseError := osUpdatesPolicyUpdateFromFlags(cmd)
			if parseError != nil {
				return parseError
			}
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			target, targetError := resolveOSUpdatesTarget(args[0], clusterID)
			if targetError != nil {
				return targetError
			}
			requestContext, cancel, timeoutError := nodeMaintenanceContext(cmd)
			if timeoutError != nil {
				return timeoutError
			}
			defer cancel()
			document, setError := apiClient.SetOSPatchPolicy(requestContext, target.provider, target.clusterID, update)
			if setError != nil {
				return osUpdatesCallError("changing the OS updates of "+target.display(), target, setError)
			}
			if rendered, renderError := renderStructured(cmd, document); rendered || renderError != nil {
				return renderError
			}
			var policy osUpdatesPolicy
			if decodeError := decodeOSUpdatesDocument(document, &policy); decodeError != nil {
				return decodeError
			}
			printOSUpdatesChange(cmd.OutOrStdout(), target, policy)
			return nil
		},
	})
	command.Flags().String("mode", "", "automatic, advisory or default for the cluster; automatic, advisory or inherit with --node-group")
	command.Flags().StringArray("node-group", nil, "Node group whose mode --mode sets (repeatable)")
	command.Flags().StringSlice("window-days", nil, "Days the maintenance window opens (mon,tue,wed,thu,fri,sat,sun)")
	command.Flags().Int("window-start-hour", 0, "Hour of the day (UTC, 0-23) the window opens")
	command.Flags().Int("window-duration-hours", 0, "How many hours the window stays open (1-12)")
	command.Flags().Int("cadence-days", 0, "Fewest days between two automatic runs (1-90)")
	command.Flags().Int("security-lag-days", 0, "Days a held security update may wait before the cluster is reported lagging (1-90)")
	command.Flags().Bool("include-stateful-nodes", false, "Runs drain nodes that carry single-replica stateful workloads")
	command.Flags().Bool("exclude-stateful-nodes", false, "Runs leave nodes that carry single-replica stateful workloads out")
	command.Flags().Duration("timeout", defaultNodeMaintenanceTimeout, "Maximum time to wait for the change")
	command.MarkFlagsMutuallyExclusive("include-stateful-nodes", "exclude-stateful-nodes")
	registerStructuredOutputFlags(command)
	return command
}

func newOSUpdatesPlanCommand() *cobra.Command {
	command := withClusterArgument(clusterMustBeNamed, &cobra.Command{
		Use:   "plan <cluster_id|name>",
		Short: "Dry-run the OS updates on every node; nothing is installed",
		Long: `Run the dry run on every node and the bastion and show what each would
install, how many of those are security updates, whether a reboot would
follow, and the order 'run' would take. Nothing is installed, drained or
rebooted. The platform waits about 40 seconds; a slower dry run keeps going,
and 'get' shows its result once it finishes. Needs clusters.operate.`,
		Example: `  ankra cluster os-updates plan prod
  ankra cluster os-updates plan prod -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			target, targetError := resolveOSUpdatesTarget(args[0], clusterID)
			if targetError != nil {
				return targetError
			}
			requestContext, cancel, timeoutError := nodeMaintenanceContext(cmd)
			if timeoutError != nil {
				return timeoutError
			}
			defer cancel()
			document, planError := apiClient.PlanOSPatches(requestContext, target.provider, target.clusterID)
			if planError != nil {
				return osUpdatesCallError("dry-running the OS updates of "+target.display(), target, planError)
			}
			if rendered, renderError := renderStructured(cmd, document); rendered || renderError != nil {
				return renderError
			}
			var plan osUpdatesPlan
			if decodeError := decodeOSUpdatesDocument(document, &plan); decodeError != nil {
				return decodeError
			}
			printOSUpdatesPlan(cmd.OutOrStdout(), target, plan)
			return nil
		},
	})
	command.Flags().Duration("timeout", defaultNodeMaintenanceTimeout, "Maximum time to wait for the dry run")
	registerStructuredOutputFlags(command)
	return command
}

func newOSUpdatesRunCommand() *cobra.Command {
	command := withClusterArgument(clusterMustBeNamed, &cobra.Command{
		Use:   "run <cluster_id|name>",
		Short: "Start the rolling OS update run: one node at a time, drained first",
		Long: `Start the rolling run, whatever the cluster's mode: each node is drained
(PodDisruptionBudgets honoured, never forced), updated, rebooted only when an
update asks for it, and back in service before the next is touched; the
bastion goes last. A failed node stops the run. Workloads move between nodes
while it runs, so the command asks first (--yes skips, declining exits 4).

A run starts inside the cluster's maintenance window. While the window is
closed the command says when it next opens and starts nothing (exit 8); a
cluster whose OS updates are automatic is then run in that window on its own.
--now starts the run at once, outside the window. Nodes carrying
single-replica stateful workloads follow the policy unless
--include-stateful-nodes or --exclude-stateful-nodes says otherwise. Follow
the run with 'get' or 'ankra cluster operations list'. Needs clusters.operate.`,
		Example: `  ankra cluster os-updates run prod
  ankra cluster os-updates run staging --now --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterID, resolveError := resolveClusterArg(args[0])
			if resolveError != nil {
				return resolveError
			}
			target, targetError := resolveOSUpdatesTarget(args[0], clusterID)
			if targetError != nil {
				return targetError
			}
			statusDocument, statusError := apiClient.GetOSPatchStatus(target.provider, target.clusterID)
			if statusError != nil {
				return osUpdatesCallError("reading the maintenance window of "+target.display(), target, statusError)
			}
			var status osUpdatesStatus
			if decodeError := decodeOSUpdatesDocument(statusDocument, &status); decodeError != nil {
				return decodeError
			}
			now, _ := cmd.Flags().GetBool("now")
			window := status.Policy
			if !now && !window.WindowOpenNow {
				return withExitCode(exitBlocked, errors.New(osUpdatesWindowClosedMessage(target, window)))
			}
			request := client.OSPatchRunRequest{OutsideWindow: now}
			if cmd.Flags().Changed("include-stateful-nodes") || cmd.Flags().Changed("exclude-stateful-nodes") {
				includeStateful := cmd.Flags().Changed("include-stateful-nodes")
				request.IncludeStatefulNodes = &includeStateful
			}
			if excludeBastion, _ := cmd.Flags().GetBool("exclude-bastion"); excludeBastion {
				includeBastion := false
				request.IncludeBastion = &includeBastion
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
				osUpdatesRunPrompt(target, window, now), yes); confirmError != nil {
				return confirmError
			}
			requestContext, cancel, timeoutError := nodeMaintenanceContext(cmd)
			if timeoutError != nil {
				return timeoutError
			}
			defer cancel()
			document, runError := apiClient.RunOSPatches(requestContext, target.provider, target.clusterID, request)
			if runError != nil {
				return osUpdatesCallError("starting the OS update run on "+target.display(), target, runError)
			}
			if rendered, renderError := renderStructured(cmd, document); rendered || renderError != nil {
				return renderError
			}
			var started osUpdatesRunStarted
			if decodeError := decodeOSUpdatesDocument(document, &started); decodeError != nil {
				return decodeError
			}
			printOSUpdatesRunStarted(cmd.OutOrStdout(), target, started)
			return nil
		},
	})
	command.Flags().Bool("now", false, "Start at once, even though the maintenance window is closed")
	command.Flags().Bool("include-stateful-nodes", false, "Drain nodes carrying single-replica stateful workloads, whatever the policy says")
	command.Flags().Bool("exclude-stateful-nodes", false, "Leave nodes carrying single-replica stateful workloads out, whatever the policy says")
	command.Flags().Bool("exclude-bastion", false, "Leave the bastion out of this run")
	command.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	command.Flags().Duration("timeout", defaultNodeMaintenanceTimeout, "Maximum time to wait for the run to start")
	command.MarkFlagsMutuallyExclusive("include-stateful-nodes", "exclude-stateful-nodes")
	registerStructuredOutputFlags(command)
	return command
}

// resolveOSUpdatesTarget looks the cluster up for its kind, which names the
// provider segment of its routes. A cluster of a kind the OS updates lane
// does not serve is a usage error, before any request to the lane.
func resolveOSUpdatesTarget(typed string, clusterID string) (osUpdatesTarget, error) {
	cluster, lookupError := apiClient.GetClusterByID(clusterID)
	if lookupError != nil {
		if errors.Is(lookupError, client.ErrClusterNotFound) {
			return osUpdatesTarget{}, withExitCode(exitNotFound, fmt.Errorf("looking up cluster %q: %w", typed, lookupError))
		}
		return osUpdatesTarget{}, fmt.Errorf("looking up cluster %q: %w", typed, lookupError)
	}
	if cluster.Kind == "" {
		return osUpdatesTarget{}, fmt.Errorf("the platform did not report what kind of cluster %s is, so its OS updates cannot be read", typed)
	}
	if !containsString(osPatchLaneProviders, cluster.Kind) {
		return osUpdatesTarget{}, withExitCode(exitUsage, fmt.Errorf(
			"%s is a %s cluster; Ankra manages OS updates on %s clusters only", typed, cluster.Kind, osUpdatesProvidersSentence()))
	}
	return osUpdatesTarget{typed: typed, clusterID: clusterID, name: cluster.Name, provider: cluster.Kind}, nil
}

// osUpdatesProvidersSentence names, in prose, the cluster kinds the OS
// updates lane serves.
func osUpdatesProvidersSentence() string {
	labels := make([]string, 0, len(osPatchLaneProviders))
	for _, provider := range osPatchLaneProviders {
		labels = append(labels, providerFamilies[provider].label)
	}
	if len(labels) < 2 {
		return strings.Join(labels, "")
	}
	return strings.Join(labels[:len(labels)-1], ", ") + " and " + labels[len(labels)-1]
}

// osUpdatesCallError wraps a failed lane call. A platform without the route
// says so; a refusal (409) changed nothing and exits 8; a --timeout that ran
// out exits 5.
func osUpdatesCallError(action string, target osUpdatesTarget, callError error) error {
	if isRouteAbsent(callError) {
		return withExitCode(exitError, fmt.Errorf(
			"%s: this Ankra platform does not serve OS updates for %s clusters (the route answered 404)", action, target.provider))
	}
	var unexpected *client.UnexpectedResponseError
	if errors.As(callError, &unexpected) && unexpected.StatusCode == http.StatusConflict {
		return withExitCode(exitBlocked, fmt.Errorf("%s: %w", action, callError))
	}
	return nodeMaintenanceError(action, callError)
}

// decodeOSUpdatesDocument reads the platform's answer into the typed view the
// human output is printed from; -o json prints the answer itself.
func decodeOSUpdatesDocument(document map[string]any, target any) error {
	encoded, encodeError := json.Marshal(document)
	if encodeError != nil {
		return fmt.Errorf("reading the platform's answer: %w", encodeError)
	}
	if decodeError := json.Unmarshal(encoded, target); decodeError != nil {
		return fmt.Errorf("reading the platform's answer: %w", decodeError)
	}
	return nil
}

// osUpdatesPolicyUpdateFromFlags reads `set`'s flags; a flag not passed is
// not sent, so the platform keeps that setting.
func osUpdatesPolicyUpdateFromFlags(cmd *cobra.Command) (client.OSPatchPolicyUpdate, error) {
	update := client.OSPatchPolicyUpdate{}
	flags := cmd.Flags()
	nodeGroups, _ := flags.GetStringArray("node-group")
	mode := ""
	if flags.Changed("mode") {
		rawMode, _ := flags.GetString("mode")
		mode = strings.ToLower(strings.TrimSpace(rawMode))
	}
	switch {
	case len(nodeGroups) > 0:
		if mode == "" {
			return update, withExitCode(exitUsage, errors.New("--node-group needs --mode automatic, advisory or inherit"))
		}
		if mode != osUpdatesModeAutomatic && mode != osUpdatesModeAdvisory && mode != osUpdatesModeInherit {
			return update, withExitCode(exitUsage, fmt.Errorf(
				"a node group's --mode is automatic, advisory or inherit (follow the cluster), got %q", mode))
		}
		update.NodeGroupModes = map[string]string{}
		for _, nodeGroup := range nodeGroups {
			name := strings.TrimSpace(nodeGroup)
			if name == "" {
				return update, withExitCode(exitUsage, errors.New("--node-group needs a node group name"))
			}
			update.NodeGroupModes[name] = mode
		}
	case mode != "":
		if mode != osUpdatesModeAutomatic && mode != osUpdatesModeAdvisory && mode != osUpdatesModeDefault {
			return update, withExitCode(exitUsage, fmt.Errorf(
				"the cluster's --mode is automatic, advisory or default (let its environment decide), got %q", mode))
		}
		update.Mode = &mode
	case flags.Changed("mode"):
		return update, withExitCode(exitUsage, errors.New("--mode needs a value: automatic, advisory or default"))
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
	if update.Mode == nil && update.NodeGroupModes == nil && update.IncludeStatefulNodes == nil &&
		update.WindowDays == nil && update.WindowStartHourUTC == nil && update.WindowDurationHours == nil &&
		update.CadenceDays == nil && update.SecurityLagDays == nil {
		return update, withExitCode(exitUsage, errors.New(
			"nothing to change: pass --mode (with --node-group for a node group) or a window, cadence, lag or stateful-nodes flag"))
	}
	return update, nil
}

// osUpdatesModeSummary renders a cluster's mode with why it has it.
func osUpdatesModeSummary(policy osUpdatesPolicy) string {
	switch policy.ModeSource {
	case "chosen":
		return policy.Mode + " (chosen for this cluster)"
	case "notice_period":
		if policy.AutomaticFrom != "" {
			return fmt.Sprintf("%s until %s, then automatic (notice period)", policy.Mode, formatOSUpdatesTime(policy.AutomaticFrom))
		}
		return policy.Mode + " (notice period: automatic once the organisation has been told and the notice has run)"
	case "environment":
		return policy.Mode + " (decided by the cluster's environment)"
	default:
		return policy.Mode
	}
}

// formatOSUpdatesTime renders one of the platform's RFC 3339 times for a
// person; a value it cannot read is printed as given.
func formatOSUpdatesTime(value string) string {
	parsed, parseError := time.Parse(time.RFC3339, value)
	if parseError != nil {
		return value
	}
	return parsed.UTC().Format("2006-01-02 15:04 UTC")
}

func printOSUpdatesStatus(out io.Writer, target osUpdatesTarget, status osUpdatesStatus) {
	policy := status.Policy
	_, _ = fmt.Fprintf(out, "OS updates for cluster %s (%s):\n\n", target.display(), target.provider)
	_, _ = fmt.Fprintf(out, "  Mode:           %s\n", osUpdatesModeSummary(policy))
	if policy.ModeReason != "" {
		_, _ = fmt.Fprintf(out, "  Why:            %s\n", policy.ModeReason)
	}
	if policy.ModeSource != "chosen" && policy.DefaultMode != "" {
		_, _ = fmt.Fprintf(out, "  Default:        %s, from the cluster's environment; nobody chose a mode\n", policy.DefaultMode)
	}
	if policy.AutomaticFrom != "" {
		_, _ = fmt.Fprintf(out, "  Automatic from: %s\n", formatOSUpdatesTime(policy.AutomaticFrom))
	}
	_, _ = fmt.Fprintf(out, "  Window:         %s\n", osUpdatesWindowSummary(policy))
	_, _ = fmt.Fprintf(out, "  Cadence:        at most one automatic run every %d days\n", policy.CadenceDays)
	stateful := "left out of runs"
	if policy.IncludeStatefulNodes {
		stateful = "drained by runs"
	}
	_, _ = fmt.Fprintf(out, "  Stateful nodes: %s\n", stateful)
	if status.SecurityLag.State != "" {
		_, _ = fmt.Fprintf(out, "  Security lag:   %s", status.SecurityLag.State)
		if status.SecurityLag.Message != "" {
			_, _ = fmt.Fprintf(out, ": %s", status.SecurityLag.Message)
		}
		_, _ = fmt.Fprintln(out)
	}
	if status.LastPlan != nil {
		observed := status.LastPlanAt
		if observed == "" {
			observed = status.LastPlan.ObservedAt
		}
		_, _ = fmt.Fprintf(out, "  Last dry run:   %s: %s\n", formatOSUpdatesTime(observed), osUpdatesPlanSummaryLine(*status.LastPlan))
	}
	if status.LatestRun != nil && status.LatestRun.Status != "" && !osUpdatesRunFinished(status.LatestRun.Status) {
		_, _ = fmt.Fprintf(out, "  Running now:    %s, operation %s\n", status.LatestRun.Status, status.LatestRun.OperationID)
	}
	if record := status.LastRunRecord; record != nil {
		line := record.Status
		if record.Trigger != "" {
			line += ", started by " + record.Trigger
		}
		if record.StartedAt != "" {
			line += " at " + formatOSUpdatesTime(record.StartedAt)
		}
		_, _ = fmt.Fprintf(out, "  Last run:       %s (operation %s)\n", line, record.OperationID)
	}
	if status.LastSuccessfulRunAt != "" {
		_, _ = fmt.Fprintf(out, "  Last success:   %s\n", formatOSUpdatesTime(status.LastSuccessfulRunAt))
	}
	_, _ = fmt.Fprintln(out)
	printOSUpdatesNodeGroupModes(out, policy)
	printOSUpdatesNextStep(out, target, policy)
}

func printOSUpdatesNodeGroupModes(out io.Writer, policy osUpdatesPolicy) {
	if len(policy.NodeGroupModes) == 0 {
		_, _ = fmt.Fprintf(out, "Every node group follows the cluster (%s).\n", policy.Mode)
		return
	}
	names := make([]string, 0, len(policy.NodeGroupModes))
	for name := range policy.NodeGroupModes {
		names = append(names, name)
	}
	sort.Strings(names)
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.AppendHeader(table.Row{"NODE GROUP", "MODE"})
	for _, name := range names {
		writer.AppendRow(table.Row{name, policy.NodeGroupModes[name] + " (its own)"})
	}
	writer.Render()
	_, _ = fmt.Fprintf(out, "Every other node group, the control planes and the bastion follow the cluster (%s).\n", policy.Mode)
}

func printOSUpdatesNextStep(out io.Writer, target osUpdatesTarget, policy osUpdatesPolicy) {
	switch {
	case policy.ModeSource == "notice_period":
		_, _ = fmt.Fprintf(out, "\nTo keep them advisory: ankra cluster os-updates set %s --mode advisory\n", target.typed)
	case policy.Mode == osUpdatesModeAdvisory:
		_, _ = fmt.Fprintf(out, "\nStart the rolling run: ankra cluster os-updates run %s\n", target.typed)
		_, _ = fmt.Fprintf(out, "Make them automatic:   ankra cluster os-updates set %s --mode automatic\n", target.typed)
	}
}

// osUpdatesWindowSummary renders the window and whether it is open.
func osUpdatesWindowSummary(policy osUpdatesPolicy) string {
	description := policy.WindowDescription
	if description == "" {
		description = fmt.Sprintf("%s from %02d:00 UTC for %d hours",
			strings.Join(policy.Window.Days, ","), policy.Window.StartHourUTC, policy.Window.DurationHours)
	}
	switch {
	case policy.WindowOpenNow:
		return description + " (open now)"
	case policy.NextWindowOpensUTC != "":
		return description + " (closed; opens " + formatOSUpdatesTime(policy.NextWindowOpensUTC) + ")"
	default:
		return description + " (closed)"
	}
}

// osUpdatesRunFinished reports a run status that will not change again.
func osUpdatesRunFinished(status string) bool {
	switch status {
	case "success", "failed", "cancelled", "canceled", "error", "skipped":
		return true
	default:
		return false
	}
}

func osUpdatesPlanSummaryLine(summary osUpdatesPlanSummary) string {
	line := fmt.Sprintf("%d of %d hosts have updates, %d pending (%d security)",
		summary.HostsWithWork, summary.Hosts, summary.Pending, summary.SecurityPending)
	if summary.RebootRequired > 0 {
		line += fmt.Sprintf(", %d would reboot", summary.RebootRequired)
	}
	if summary.HostsUnknown > 0 {
		line += fmt.Sprintf(", %d unknown", summary.HostsUnknown)
	}
	return line
}

func printOSUpdatesChange(out io.Writer, target osUpdatesTarget, policy osUpdatesPolicy) {
	_, _ = fmt.Fprintf(out, "OS updates for cluster %s (%s) changed.\n\n", target.display(), target.provider)
	_, _ = fmt.Fprintf(out, "  Mode:   %s\n", osUpdatesModeSummary(policy))
	if policy.ModeReason != "" {
		_, _ = fmt.Fprintf(out, "  Why:    %s\n", policy.ModeReason)
	}
	_, _ = fmt.Fprintf(out, "  Window: %s\n\n", osUpdatesWindowSummary(policy))
	printOSUpdatesNodeGroupModes(out, policy)
	if policy.Message != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", policy.Message)
	}
	if revert := policy.HoldRevert; revert != nil {
		if revert.OperationID != "" {
			_, _ = fmt.Fprintf(out, "Follow the hold removal with: ankra cluster operations list %s --cluster %s\n",
				revert.OperationID, target.clusterID)
		}
		if revert.NotStarted != "" {
			_, _ = fmt.Fprintf(out, "The update hold was not removed from the hosts made advisory: %s.\n", revert.NotStarted)
		}
	}
}

func printOSUpdatesPlan(out io.Writer, target osUpdatesTarget, plan osUpdatesPlan) {
	_, _ = fmt.Fprintf(out, "OS updates dry run on cluster %s (%s); nothing was installed.\n\n", target.display(), target.provider)
	if len(plan.Hosts) > 0 {
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.AppendHeader(table.Row{"HOST", "ROLE", "NODE GROUP", "STATUS", "PENDING", "SECURITY", "HELD BACK", "REBOOT", "NOTE"})
		for _, host := range plan.Hosts {
			pending, security, heldBack := "-", "-", "-"
			if host.Summary != nil {
				pending = fmt.Sprint(host.Summary.Pending)
				security = fmt.Sprint(host.Summary.SecurityPending)
				heldBack = fmt.Sprint(host.Summary.HeldBackPending)
			}
			writer.AppendRow(table.Row{host.Name, host.Role, valueOrDashString(host.NodeGroup), host.Status,
				pending, security, heldBack, yesNo(host.RebootRequired), osUpdatesHostNote(host)})
		}
		writer.Render()
		_, _ = fmt.Fprintln(out)
	}
	if plan.Summary != nil && plan.Completed {
		_, _ = fmt.Fprintf(out, "Summary:   %s\n", osUpdatesPlanSummaryLine(*plan.Summary))
	}
	if len(plan.RunOrder) > 0 {
		_, _ = fmt.Fprintf(out, "Run order: %s\n", strings.Join(plan.RunOrder, ", "))
	}
	printOSUpdatesSkipped(out, plan.Skipped)
	if !plan.Completed {
		_, _ = fmt.Fprintf(out, "\nNot every host has reported yet (operation %s), so a host without counts is "+
			"unknown, not up to date. The dry run keeps going; 'ankra cluster os-updates get %s' shows it once it "+
			"finishes.\n", plan.OperationID, target.typed)
		return
	}
	_, _ = fmt.Fprintln(out, "\nA host whose status is not success has an unknown plan, not an empty one.")
}

// osUpdatesHostNote is what a person should know about one host's dry run.
func osUpdatesHostNote(host osUpdatesHostPlan) string {
	switch {
	case host.Error != "":
		return host.Error
	case host.SimulationFailedNote != "":
		return host.SimulationFailedNote
	case host.StatefulVerdict == "would_skip":
		return "stateful: left out unless --include-stateful-nodes"
	default:
		return ""
	}
}

func printOSUpdatesSkipped(out io.Writer, skipped []osUpdatesHost) {
	if len(skipped) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "Left out:")
	for _, host := range skipped {
		_, _ = fmt.Fprintf(out, "  %s (%s): %s\n", host.Name, host.Role, host.Reason)
	}
}

// osUpdatesWindowClosedMessage refuses a run while the window is closed.
func osUpdatesWindowClosedMessage(target osUpdatesTarget, policy osUpdatesPolicy) string {
	message := fmt.Sprintf("the maintenance window of cluster %s (%s) is closed", target.display(), policy.WindowDescription)
	if policy.NextWindowOpensUTC != "" {
		message += "; it next opens " + formatOSUpdatesTime(policy.NextWindowOpensUTC)
	}
	message += ". Nothing was started. "
	if policy.Mode == osUpdatesModeAutomatic {
		message += "OS updates on this cluster are automatic, so Ankra starts the run in the window on its own " +
			"once the cadence allows. "
	} else {
		message += "OS updates on this cluster are advisory, so run this again once the window is open. "
	}
	return message + "To start now, outside the window, pass --now"
}

// osUpdatesRunPrompt asks before a run, naming what it does to the nodes.
func osUpdatesRunPrompt(target osUpdatesTarget, policy osUpdatesPolicy, now bool) string {
	when := "now, inside its maintenance window"
	if now && !policy.WindowOpenNow {
		when = fmt.Sprintf("now, outside its maintenance window (%s)", policy.WindowDescription)
	}
	return fmt.Sprintf("Start the rolling OS update run on cluster %s %s? Nodes are drained and updated one at a "+
		"time, each back in service before the next, and may reboot. [y/N]: ",
		clusterTarget(target.typed, target.clusterID), when)
}

func printOSUpdatesRunStarted(out io.Writer, target osUpdatesTarget, started osUpdatesRunStarted) {
	_, _ = fmt.Fprintf(out, "Rolling OS update run started on cluster %s (%s).\n", target.display(), target.provider)
	_, _ = fmt.Fprintf(out, "  Operation: %s\n\n", started.OperationID)
	if len(started.Order) > 0 {
		_, _ = fmt.Fprintln(out, "Order:")
		for index, host := range started.Order {
			role := host.Role
			if host.NodeGroup != "" {
				role += ", node group " + host.NodeGroup
			}
			_, _ = fmt.Fprintf(out, "  %d. %s (%s)\n", index+1, host.Name, role)
		}
	}
	printOSUpdatesSkipped(out, started.Skipped)
	if started.BastionNote != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", started.BastionNote)
	}
	_, _ = fmt.Fprintln(out, "\nEach node is drained (PodDisruptionBudgets honoured, never forced), updated, rebooted "+
		"only if an update asks for it, and back in service before the next. A failed node stops the run.")
	if revert := started.HoldRevert; revert != nil && revert.NotStarted != "" {
		_, _ = fmt.Fprintf(out, "The update hold could not be removed from the hosts this run leaves out: %s.\n", revert.NotStarted)
	}
	_, _ = fmt.Fprintf(out, "\nFollow it with:\n  ankra cluster os-updates get %s\n  ankra cluster operations list %s --cluster %s\n",
		target.typed, started.OperationID, target.clusterID)
}

func init() {
	clusterCmd.AddCommand(newClusterOSUpdatesCommand())
}

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// powerScheduleFlags carries the shared create/update inputs. Exactly one
// of at (a one-off RFC 3339 fire time) or cron (a repeated 5-field
// expression, evaluated in timezone) must be set; the backend validates the
// values themselves (future run_at, parseable cron, IANA timezone).
type powerScheduleFlags struct {
	action        string
	at            string
	cron          string
	timezone      string
	enabled       bool
	stopMode      string
	preserveState *bool
	// acceptNodeLocalDataLoss is --accept-node-local-data-loss: the
	// acknowledgement a scale_to_zero stop needs on a cluster whose volumes
	// keep data on worker disks.
	acceptNodeLocalDataLoss bool
}

// registerPowerScheduleSpecFlags declares the shared create/update flag set.
func registerPowerScheduleSpecFlags(cmd *cobra.Command) {
	cmd.Flags().String("action", "", "What the schedule does when it fires: stop or start (required)")
	cmd.Flags().String("at", "", "Fire once at this RFC 3339 time, e.g. 2026-01-02T19:00:00Z (mutually exclusive with --cron)")
	cmd.Flags().String("cron", "", "Fire repeatedly per this 5-field cron expression, e.g. '0 19 * * 1-5' (mutually exclusive with --at)")
	cmd.Flags().String("timezone", "", "IANA timezone the cron expression is evaluated in, e.g. Europe/Stockholm (default UTC)")
	cmd.Flags().Bool("enabled", true, "Whether the schedule is armed; --enabled=false creates or leaves it paused")
	cmd.Flags().String("stop-mode", "", "How a stop schedule stops the cluster: delete_resources (default on create; terminates the VMs), scale_to_zero (deletes the worker servers at every stop and creates new ones at start: data on their own disks is lost; the control plane and etcd, cloud volumes and addresses are kept and keep billing) or pause (powers every server off and keeps it with its disks; k3s on Hetzner, UpCloud and DigitalOcean, and what a stop always does on AWS and Scaleway). pause saves no compute cost on Hetzner, DigitalOcean or UpCloud, which bill a powered-off server in full; use scale_to_zero or delete_resources to save. On update, omitting it keeps the schedule's current mode")
	cmd.Flags().Bool("accept-node-local-data-loss", false, "For scale_to_zero stop schedules: accept that volumes keeping data on a worker's own disk (local-path, hostPath, local PVs) are emptied at every stop. Required when the cluster has such volumes and you are not answering the prompt interactively")
	registerThreeStateFlag(cmd, "preserve-state", "For delete_resources stop schedules: omit to capture the cluster's state (an encrypted etcd snapshot the next start restores) whenever the provider and distribution support it; 'false' to tear down without it; 'true' to state the default explicitly. On update, omitting it keeps the schedule's current choice")
	_ = cmd.MarkFlagRequired("action")
}

// powerScheduleFlagsFromCommand reads and cross-validates the shared flags.
func powerScheduleFlagsFromCommand(cmd *cobra.Command) (powerScheduleFlags, error) {
	var flags powerScheduleFlags
	flags.action, _ = cmd.Flags().GetString("action")
	flags.at, _ = cmd.Flags().GetString("at")
	flags.cron, _ = cmd.Flags().GetString("cron")
	flags.timezone, _ = cmd.Flags().GetString("timezone")
	flags.enabled, _ = cmd.Flags().GetBool("enabled")
	flags.stopMode, _ = cmd.Flags().GetString("stop-mode")
	flags.preserveState = threeStateFlag(cmd, "preserve-state")
	flags.acceptNodeLocalDataLoss, _ = cmd.Flags().GetBool("accept-node-local-data-loss")

	flags.action = strings.ToLower(strings.TrimSpace(flags.action))
	if flags.action != "stop" && flags.action != "start" {
		return flags, withExitCode(exitUsage, fmt.Errorf("--action must be stop or start"))
	}
	flags.stopMode = strings.ToLower(strings.TrimSpace(flags.stopMode))
	if flags.stopMode != "" && flags.stopMode != "delete_resources" && flags.stopMode != "scale_to_zero" && flags.stopMode != "pause" {
		return flags, withExitCode(exitUsage, fmt.Errorf("--stop-mode must be delete_resources, scale_to_zero or pause"))
	}
	if flags.action != "stop" && (flags.stopMode != "" || flags.preserveState != nil) {
		return flags, withExitCode(exitUsage, fmt.Errorf("--stop-mode and --preserve-state only apply to stop schedules"))
	}
	flags.at = strings.TrimSpace(flags.at)
	flags.cron = strings.TrimSpace(flags.cron)
	flags.timezone = strings.TrimSpace(flags.timezone)
	if (flags.at == "") == (flags.cron == "") {
		return flags, withExitCode(exitUsage, fmt.Errorf("exactly one of --at (one-off) or --cron (repeated) must be provided"))
	}
	if flags.at != "" && flags.timezone != "" {
		return flags, withExitCode(exitUsage, fmt.Errorf("--timezone only applies to --cron schedules; encode the offset in the --at timestamp instead"))
	}
	return flags, nil
}

// carryStopChoicesFrom fills the stop mode and preserve-state choices an
// update left out from the schedule as it is now. The backend treats an
// update as a full replace and requires stop_mode on a stop schedule, so an
// update that only moved the cron would have been refused (422 "Stop mode
// must be provided") and one that restated the mode would have reset
// preserve_state to the server default. A schedule that is not found, or
// that is being turned from a start into a stop, gets delete_resources with
// the server's default for preserve_state, which is what create does.
func (flags powerScheduleFlags) carryStopChoicesFrom(schedules []client.PowerSchedule, scheduleID string) powerScheduleFlags {
	var current *client.PowerSchedule
	for index := range schedules {
		if schedules[index].ID == scheduleID {
			current = &schedules[index]
			break
		}
	}
	if flags.stopMode == "" {
		flags.stopMode = "delete_resources"
		if current != nil && current.Action == "stop" && current.StopMode != "" {
			flags.stopMode = current.StopMode
		}
	}
	if flags.preserveState == nil && flags.stopMode == "delete_resources" && current != nil && current.Action == "stop" {
		preserve := current.PreserveState
		flags.preserveState = &preserve
	}
	return flags
}

// request maps the validated flags onto the API body. The backend treats
// updates as full replaces, so enabled always rides along, and a cron
// schedule always restates its timezone (defaulting to UTC explicitly,
// matching the create-time default).
func (flags powerScheduleFlags) request() client.PowerScheduleRequest {
	request := client.PowerScheduleRequest{
		Action:                  flags.action,
		Enabled:                 flags.enabled,
		StopMode:                flags.stopMode,
		PreserveState:           flags.preserveState,
		AcceptNodeLocalDataLoss: flags.acceptNodeLocalDataLoss,
	}
	if flags.at != "" {
		request.ScheduleKind = "once"
		runAt := flags.at
		request.RunAt = &runAt
	} else {
		request.ScheduleKind = "cron"
		cronExpression := flags.cron
		request.CronExpression = &cronExpression
		timezone := flags.timezone
		if timezone == "" {
			timezone = "UTC"
		}
		request.Timezone = &timezone
	}
	return request
}

// scaleToZeroStopNote is what every enabled scale_to_zero stop schedule
// prints before it is written.
const scaleToZeroStopNote = "A scale_to_zero stop deletes the worker servers each time and creates new ones at the next start. " +
	"The control plane and etcd, cloud volumes and addresses are kept and keep billing. " +
	"Data on the workers' own disks is lost at every stop."

// nodeLocalNameLimit is how many volume names the warning spells out.
const nodeLocalNameLimit = 10

// promptIsInteractive reports whether the acknowledgement can be asked on
// in: only a terminal can answer it. A variable so tests can answer it.
var promptIsInteractive = func(in io.Reader) bool {
	file, isFile := in.(*os.File)
	return isFile && readline.IsTerminal(int(file.Fd()))
}

// nodeLocalVolumeList names the volumes, capped, with the remainder counted.
func nodeLocalVolumeList(storage *client.PowerScheduleNodeLocalStorage) string {
	count := len(storage.PVCNames)
	if storage.PVCCount != nil {
		count = *storage.PVCCount
	}
	shown := storage.PVCNames[:min(len(storage.PVCNames), nodeLocalNameLimit)]
	named := strings.Join(shown, ", ")
	if more := count - len(shown); more > 0 {
		if named != "" {
			named += fmt.Sprintf(" and %d more", more)
		} else {
			named = fmt.Sprintf("%d volumes", more)
		}
	}
	return named
}

// acknowledgeNodeLocalDataLoss runs before an enabled scale_to_zero stop is
// written. It prints what the stop deletes, reads the cluster's node-local
// volumes and, when there are some, needs the loss accepted: by
// --accept-node-local-data-loss, or by answering the prompt on a terminal.
// Anywhere else it fails naming the volumes, because the backend refuses the
// schedule without the acknowledgement. A reading that could not be made is
// a warning, not a refusal, matching the backend. The notes go to stderr so
// --output json|yaml stays parseable.
func acknowledgeNodeLocalDataLoss(cmd *cobra.Command, cluster client.ClusterListItem, flags powerScheduleFlags) (powerScheduleFlags, error) {
	if flags.action != "stop" || flags.stopMode != "scale_to_zero" || !flags.enabled {
		return flags, nil
	}
	errOut := cmd.ErrOrStderr()
	_, _ = fmt.Fprintln(errOut, scaleToZeroStopNote)
	storage, readError := apiClient.GetPowerScheduleNodeLocalStorage(cluster.ID)
	if readError != nil || storage == nil || (storage.State != "present" && storage.State != "none") {
		_, _ = fmt.Fprintf(errOut, "Warning: Ankra could not check whether volumes on cluster %s keep data on worker disks. Any that do are emptied at every stop.\n", cluster.Name)
		return flags, nil
	}
	if storage.State != "present" {
		return flags, nil
	}
	volumes := nodeLocalVolumeList(storage)
	_, _ = fmt.Fprintf(errOut, "Warning: these volumes on cluster %s keep data on worker disks, and it is deleted at every stop: %s\n", cluster.Name, volumes)
	if flags.acceptNodeLocalDataLoss {
		return flags, nil
	}
	if !promptIsInteractive(cmd.InOrStdin()) {
		return flags, withExitCode(exitUsage, fmt.Errorf(
			"cluster %s keeps data on worker disks (%s), which a scale_to_zero stop deletes at every stop; "+
				"re-run with --accept-node-local-data-loss to schedule it anyway", cluster.Name, volumes))
	}
	if promptError := confirmPrompt(cmd.InOrStdin(), errOut,
		"Schedule it anyway and lose the data on these volumes at every stop? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return flags, promptError
		}
		return flags, fmt.Errorf("reading the acknowledgement: %w", promptError)
	}
	flags.acceptNodeLocalDataLoss = true
	return flags, nil
}

var clusterPowerSchedulesCmd = &cobra.Command{
	Use:     "power-schedules",
	Aliases: []string{"power-schedule"},
	Short:   "Manage scheduled stop/start (power schedules) for the active cluster",
	Long: `Manage the cluster's power schedules: scheduled stop or start actions that
fire once at a chosen time or repeatedly on a cron expression, so a
development cluster can park itself outside working hours.

Power schedules are available for self-managed Hetzner, OVHcloud, UpCloud,
DigitalOcean, Scaleway, AWS (EC2), Proxmox VE, and HPE Morpheus clusters - the same
clusters that support manual stop and start. A scheduled stop behaves
like stopping the cluster yourself: on Hetzner, OVHcloud, UpCloud and
DigitalOcean the cluster's state is captured first (an encrypted etcd
snapshot the next start restores) unless --preserve-state=false; elsewhere
the provider VMs are terminated and only the configuration is preserved.
--stop-mode scale_to_zero deletes only the worker servers instead, and creates
new ones at the next start: the control plane and etcd, cloud volumes and
addresses are kept (and keep billing), and data on the workers' own disks is
lost at every stop. On a cluster whose volumes keep data there, the schedule
needs --accept-node-local-data-loss (or a yes at the prompt).
--stop-mode pause powers the servers off and keeps them.

--stop-mode pause keeps the cluster's state but saves no compute cost on
Hetzner, DigitalOcean or UpCloud (Developer and General Purpose plans):
they bill a powered-off server at its full price. To save money there, use
scale_to_zero or delete_resources. On AWS and Scaleway a powered-off server
stops billing compute; its disks and IPs keep billing.

Examples:
  # Park a development cluster on weekday evenings, back before morning
  ankra cluster power-schedules create --action stop --cron '0 19 * * 1-5' --timezone Europe/Stockholm
  ankra cluster power-schedules create --action start --cron '0 7 * * 1-5' --timezone Europe/Stockholm

  # One-off stop before the weekend
  ankra cluster power-schedules create --action stop --at 2026-01-02T19:00:00Z`,
}

var clusterPowerSchedulesListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the active cluster's power schedules",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		result, err := apiClient.ListPowerSchedules(cluster.ID)
		if err != nil {
			return fmt.Errorf("listing power schedules: %w", err)
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		printPowerScheduleTable(result.Schedules)
		return nil
	},
}

var clusterPowerSchedulesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a power schedule on the active cluster",
	Long: `Create a scheduled stop or start on the active cluster. The schedule fires
once at --at, or repeatedly per --cron evaluated in --timezone (UTC when
omitted). A cluster can hold up to 20 schedules.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		flags, err := powerScheduleFlagsFromCommand(cmd)
		if err != nil {
			return err
		}
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		flags, err = acknowledgeNodeLocalDataLoss(cmd, cluster, flags)
		if err != nil {
			return err
		}
		result, err := apiClient.CreatePowerSchedule(cluster.ID, flags.request())
		if err != nil {
			return fmt.Errorf("creating power schedule: %w", err)
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Power schedule created on cluster %s.\n\n", cluster.Name)
		printPowerScheduleTable(result.Schedules)
		return nil
	},
}

var clusterPowerSchedulesUpdateCmd = &cobra.Command{
	Use:   "update <schedule_id>",
	Short: "Replace a power schedule's action, timing, and enabled flag",
	Long: `Replace a power schedule. This is a full replace, not a patch: pass the
complete schedule as it should be afterwards - --action plus one of --at or
--cron (with --timezone for cron schedules), and --enabled=false to leave it
paused. A stop schedule's --stop-mode and --preserve-state are the exception:
when omitted, the schedule's current choices are carried over, so a change of
timing never silently turns a state-discarding stop into a preserving one.
Use 'ankra cluster power-schedules list' for the schedule ID and the current
values.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		flags, err := powerScheduleFlagsFromCommand(cmd)
		if err != nil {
			return err
		}
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		scheduleID := strings.TrimSpace(args[0])
		if flags.action == "stop" && (flags.stopMode == "" || flags.preserveState == nil) {
			current, listError := apiClient.ListPowerSchedules(cluster.ID)
			if listError != nil {
				return fmt.Errorf("reading the schedule's current stop mode: %w", listError)
			}
			flags = flags.carryStopChoicesFrom(current.Schedules, scheduleID)
		}
		flags, err = acknowledgeNodeLocalDataLoss(cmd, cluster, flags)
		if err != nil {
			return err
		}
		result, err := apiClient.UpdatePowerSchedule(cluster.ID, scheduleID, flags.request())
		if err != nil {
			return fmt.Errorf("updating power schedule: %w", err)
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Power schedule updated on cluster %s.\n\n", cluster.Name)
		printPowerScheduleTable(result.Schedules)
		return nil
	},
}

var clusterPowerSchedulesDeleteCmd = &cobra.Command{
	Use:     "delete <schedule_id>",
	Aliases: []string{"rm"},
	Short:   "Delete a power schedule",
	Long: `Delete a power schedule: it stops firing immediately and disappears from
the cluster's schedule list. The cluster itself is not touched. To pause a
schedule while keeping its configuration, use
'ankra cluster power-schedules update <schedule_id> ... --enabled=false'.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		scheduleID := strings.TrimSpace(args[0])
		yes, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete power schedule %s from cluster %s? [y/N]: ", scheduleID, cluster.Name), yes); err != nil {
			return err
		}
		result, err := apiClient.DeletePowerSchedule(cluster.ID, scheduleID)
		if err != nil {
			return fmt.Errorf("deleting power schedule: %w", err)
		}
		if handled, err := renderStructured(cmd, result); err != nil {
			return err
		} else if handled {
			return nil
		}
		fmt.Printf("Power schedule %s deleted.\n", scheduleID)
		return nil
	},
}

// printPowerScheduleTable renders the schedule listing in the shared
// column style.
func printPowerScheduleTable(schedules []client.PowerSchedule) {
	if len(schedules) == 0 {
		fmt.Println("No power schedules found.")
		return
	}
	fmt.Printf("%-36s  %-6s  %-16s  %-9s  %-5s  %-28s  %-8s  %-14s  %-14s  %-10s\n",
		"ID", "ACTION", "STOP_MODE", "STATE", "KIND", "SCHEDULE", "ENABLED", "NEXT_RUN", "LAST_RUN", "LAST_STATUS")
	for _, schedule := range schedules {
		fmt.Printf("%-36s  %-6s  %-16s  %-9s  %-5s  %-28s  %-8t  %-14s  %-14s  %-10s\n",
			schedule.ID,
			schedule.Action,
			powerScheduleStopMode(schedule),
			powerScheduleStateChoice(schedule),
			schedule.ScheduleKind,
			truncate(powerScheduleCadence(schedule), 28),
			schedule.Enabled,
			powerScheduleTimeAgo(schedule.NextRunAt),
			powerScheduleTimeAgo(schedule.LastRunAt),
			truncate(stringValue(schedule.LastRunStatus), 10),
		)
		if detail := stringValue(schedule.LastRunDetail); detail != "" {
			fmt.Printf("%-36s    last run: %s\n", "", truncate(detail, 100))
		}
	}
}

// powerScheduleStopMode is the STOP_MODE column: the mode of a stop
// schedule, "-" for a start.
func powerScheduleStopMode(schedule client.PowerSchedule) string {
	if schedule.Action != "stop" {
		return "-"
	}
	if schedule.StopMode == "" {
		return "delete_resources"
	}
	return schedule.StopMode
}

// powerScheduleStateChoice is the STATE column: whether a delete_resources
// stop captures the cluster's state first ("preserved") or tears down
// without it ("discarded"); "-" where the question does not arise.
func powerScheduleStateChoice(schedule client.PowerSchedule) string {
	if schedule.Action != "stop" || powerScheduleStopMode(schedule) != "delete_resources" {
		return "-"
	}
	if schedule.PreserveState {
		return "preserved"
	}
	return "discarded"
}

// powerScheduleCadence phrases a schedule's timing for the table.
func powerScheduleCadence(schedule client.PowerSchedule) string {
	if schedule.ScheduleKind == "once" {
		return "at " + stringValue(schedule.RunAt)
	}
	return stringValue(schedule.CronExpression) + " (" + schedule.Timezone + ")"
}

// powerScheduleTimeAgo renders a nullable RFC 3339 timestamp as a relative
// time; future times read "in N", past times "N ago".
func powerScheduleTimeAgo(timestamp *string) string {
	if timestamp == nil || *timestamp == "" {
		return "-"
	}
	if _, err := time.Parse(time.RFC3339, *timestamp); err != nil {
		return *timestamp
	}
	return formatTimeAgo(*timestamp)
}

func init() {
	registerStructuredOutputFlags(clusterPowerSchedulesListCmd,
		clusterPowerSchedulesCreateCmd, clusterPowerSchedulesUpdateCmd,
		clusterPowerSchedulesDeleteCmd)
	registerPowerScheduleSpecFlags(clusterPowerSchedulesCreateCmd)
	registerPowerScheduleSpecFlags(clusterPowerSchedulesUpdateCmd)
	clusterPowerSchedulesDeleteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	clusterPowerSchedulesCmd.AddCommand(clusterPowerSchedulesListCmd)
	clusterPowerSchedulesCmd.AddCommand(clusterPowerSchedulesCreateCmd)
	clusterPowerSchedulesCmd.AddCommand(clusterPowerSchedulesUpdateCmd)
	clusterPowerSchedulesCmd.AddCommand(clusterPowerSchedulesDeleteCmd)
	clusterCmd.AddCommand(clusterPowerSchedulesCmd)
}

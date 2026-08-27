package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// powerScheduleFlags carries the shared create/update inputs. Exactly one
// of at (a one-off RFC 3339 fire time) or cron (a repeated 5-field
// expression, evaluated in timezone) must be set; the backend validates the
// values themselves (future run_at, parseable cron, IANA timezone).
type powerScheduleFlags struct {
	action   string
	at       string
	cron     string
	timezone string
	enabled  bool
}

// registerPowerScheduleSpecFlags declares the shared create/update flag set.
func registerPowerScheduleSpecFlags(cmd *cobra.Command) {
	cmd.Flags().String("action", "", "What the schedule does when it fires: stop or start (required)")
	cmd.Flags().String("at", "", "Fire once at this RFC 3339 time, e.g. 2026-01-02T19:00:00Z (mutually exclusive with --cron)")
	cmd.Flags().String("cron", "", "Fire repeatedly per this 5-field cron expression, e.g. '0 19 * * 1-5' (mutually exclusive with --at)")
	cmd.Flags().String("timezone", "", "IANA timezone the cron expression is evaluated in, e.g. Europe/Stockholm (default UTC)")
	cmd.Flags().Bool("enabled", true, "Whether the schedule is armed; --enabled=false creates or leaves it paused")
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

	flags.action = strings.ToLower(strings.TrimSpace(flags.action))
	if flags.action != "stop" && flags.action != "start" {
		return flags, withExitCode(exitUsage, fmt.Errorf("--action must be stop or start"))
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

// request maps the validated flags onto the API body. The backend treats
// updates as full replaces, so enabled always rides along, and a cron
// schedule always restates its timezone (defaulting to UTC explicitly,
// matching the create-time default).
func (flags powerScheduleFlags) request() client.PowerScheduleRequest {
	request := client.PowerScheduleRequest{
		Action:  flags.action,
		Enabled: flags.enabled,
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

var clusterPowerSchedulesCmd = &cobra.Command{
	Use:     "power-schedules",
	Aliases: []string{"power-schedule"},
	Short:   "Manage scheduled stop/start (power schedules) for the active cluster",
	Long: `Manage the cluster's power schedules: scheduled stop or start actions that
fire once at a chosen time or repeatedly on a cron expression, so a
development cluster can park itself outside working hours.

Power schedules are available for self-managed Hetzner, OVHcloud, UpCloud,
DigitalOcean, Scaleway, Proxmox VE, and HPE Morpheus clusters - the same
clusters that support manual stop and start. A scheduled stop behaves
exactly like stopping the cluster yourself: the provider VMs are
terminated and only the cluster's configuration is preserved for the next
start.

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
paused. Use 'ankra cluster power-schedules list' for the schedule ID and the
current values.`,
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
		result, err := apiClient.UpdatePowerSchedule(cluster.ID, strings.TrimSpace(args[0]), flags.request())
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
	fmt.Printf("%-36s  %-6s  %-5s  %-28s  %-8s  %-14s  %-14s  %-10s\n",
		"ID", "ACTION", "KIND", "SCHEDULE", "ENABLED", "NEXT_RUN", "LAST_RUN", "LAST_STATUS")
	for _, schedule := range schedules {
		fmt.Printf("%-36s  %-6s  %-5s  %-28s  %-8t  %-14s  %-14s  %-10s\n",
			schedule.ID,
			schedule.Action,
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

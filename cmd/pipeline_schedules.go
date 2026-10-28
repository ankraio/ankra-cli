package cmd

// Cron schedules (go/internal/pipelineapi/schedules.go). The loop that fires
// them is a later WS-I item; these commands only guarantee that nothing it
// will ever read was stored without proving it can fire (the server validates
// the cron expression and timezone synchronously on create/update).

import (
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func newPipelineSchedulesCommand() *cobra.Command {
	schedulesCommand := &cobra.Command{
		Use:   "schedules",
		Short: "Manage a pipeline's cron schedules",
	}
	schedulesCommand.AddCommand(
		newPipelineSchedulesListCommand(),
		newPipelineSchedulesCreateCommand(),
		newPipelineSchedulesUpdateCommand(),
		newPipelineSchedulesDeleteCommand(),
	)
	return schedulesCommand
}

func newPipelineSchedulesListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List a pipeline's cron schedules",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineSchedulesList(command, selector)
		},
	}
	registerPipelineSelectorFlags(listCommand)
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func runPipelineSchedulesList(command *cobra.Command, selector client.PipelineSelector) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	list, listError := apiClient.ListPipelineSchedules(command.Context(), selector)
	if listError != nil {
		return listError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, list)
	}
	if len(list.Schedules) == 0 {
		_, _ = fmt.Fprintln(command.OutOrStdout(), "No schedules configured for this pipeline.")
		return nil
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(command.OutOrStdout())
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "CRON", "TIMEZONE", "REF", "ENABLED", "NEXT FIRE", "LAST FIRED"})
	for _, schedule := range list.Schedules {
		writer.AppendRow(table.Row{
			schedule.ID,
			schedule.Cron,
			schedule.Timezone,
			schedule.Ref,
			schedule.Enabled,
			pipelineOptionalTimeAgo(schedule.NextFireAt),
			pipelineOptionalTimeAgo(schedule.LastFiredAt),
		})
	}
	writer.Render()
	return nil
}

func pipelineOptionalTimeAgo(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return formatTimeAgo(*value)
}

func newPipelineSchedulesCreateCommand() *cobra.Command {
	createCommand := &cobra.Command{
		Use:   "create",
		Short: "Add a cron schedule",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineSchedulesCreate(command, selector)
		},
	}
	registerPipelineSelectorFlags(createCommand)
	createCommand.Flags().String("cron", "", "Cron expression (required)")
	createCommand.Flags().String("timezone", "", "IANA timezone the cron is evaluated in (default UTC)")
	createCommand.Flags().String("ref", "", "Git reference the schedule runs at (default the repository's default branch)")
	createCommand.Flags().StringArray("input", nil, "Dispatch input as key=value (repeatable)")
	createCommand.Flags().Bool("enabled", true, "Whether the schedule fires")
	registerStructuredOutputFlags(createCommand)
	return createCommand
}

func runPipelineSchedulesCreate(command *cobra.Command, selector client.PipelineSelector) error {
	cron, _ := command.Flags().GetString("cron")
	timezone, _ := command.Flags().GetString("timezone")
	ref, _ := command.Flags().GetString("ref")
	rawInputs, _ := command.Flags().GetStringArray("input")
	enabled, _ := command.Flags().GetBool("enabled")

	cron = strings.TrimSpace(cron)
	if cron == "" {
		return withExitCode(exitUsage, fmt.Errorf("--cron is required"))
	}
	inputs, inputsError := parsePipelineInputFlags(rawInputs)
	if inputsError != nil {
		return inputsError
	}

	schedule, createError := apiClient.CreatePipelineSchedule(command.Context(), selector, client.CreatePipelineScheduleRequest{
		Cron:     cron,
		Timezone: strings.TrimSpace(timezone),
		Ref:      strings.TrimSpace(ref),
		Inputs:   inputs,
		Enabled:  &enabled,
	})
	if createError != nil {
		return createError
	}
	if rendered, renderError := renderStructured(command, schedule); rendered || renderError != nil {
		return renderError
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Schedule created: %s (%s %s)\n", schedule.ID, schedule.Cron, schedule.Timezone)
	return nil
}

func newPipelineSchedulesUpdateCommand() *cobra.Command {
	updateCommand := &cobra.Command{
		Use:   "update <schedule-id>",
		Short: "Change a cron schedule",
		Long: `Change a cron schedule. Every flag is optional and only what is given
changes - an update that names only --enabled leaves the cron, timezone, ref,
and inputs exactly as they were.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineSchedulesUpdate(command, selector, arguments[0])
		},
	}
	registerPipelineSelectorFlags(updateCommand)
	updateCommand.Flags().String("cron", "", "New cron expression")
	updateCommand.Flags().String("timezone", "", "New IANA timezone")
	updateCommand.Flags().String("ref", "", "New git reference")
	updateCommand.Flags().StringArray("input", nil, "Replace the dispatch inputs entirely, as key=value (repeatable)")
	updateCommand.Flags().Bool("enabled", false, "Enable the schedule")
	updateCommand.Flags().Bool("disabled", false, "Disable the schedule")
	registerStructuredOutputFlags(updateCommand)
	return updateCommand
}

func runPipelineSchedulesUpdate(command *cobra.Command, selector client.PipelineSelector, scheduleID string) error {
	request := client.UpdatePipelineScheduleRequest{}
	if command.Flags().Changed("cron") {
		cron, _ := command.Flags().GetString("cron")
		cron = strings.TrimSpace(cron)
		request.Cron = &cron
	}
	if command.Flags().Changed("timezone") {
		timezone, _ := command.Flags().GetString("timezone")
		timezone = strings.TrimSpace(timezone)
		request.Timezone = &timezone
	}
	if command.Flags().Changed("ref") {
		ref, _ := command.Flags().GetString("ref")
		ref = strings.TrimSpace(ref)
		request.Ref = &ref
	}
	if command.Flags().Changed("input") {
		rawInputs, _ := command.Flags().GetStringArray("input")
		inputs, inputsError := parsePipelineInputFlags(rawInputs)
		if inputsError != nil {
			return inputsError
		}
		if inputs == nil {
			inputs = map[string]string{}
		}
		request.Inputs = &inputs
	}
	enabledChanged := command.Flags().Changed("enabled")
	disabledChanged := command.Flags().Changed("disabled")
	if enabledChanged && disabledChanged {
		return withExitCode(exitUsage, fmt.Errorf("--enabled and --disabled are mutually exclusive"))
	}
	switch {
	case enabledChanged:
		// The flag's value, not its presence: `--enabled=false` asks for a
		// disabled schedule, and reading only Changed() would enable the very
		// schedule the caller was turning off.
		enabled, enabledError := command.Flags().GetBool("enabled")
		if enabledError != nil {
			return enabledError
		}
		request.Enabled = &enabled
	case disabledChanged:
		disabled, disabledError := command.Flags().GetBool("disabled")
		if disabledError != nil {
			return disabledError
		}
		wanted := !disabled
		request.Enabled = &wanted
	}
	if request.Cron == nil && request.Timezone == nil && request.Ref == nil &&
		request.Inputs == nil && request.Enabled == nil {
		return withExitCode(exitUsage, fmt.Errorf("nothing to update - pass at least one of "+
			"--cron, --timezone, --ref, --input, --enabled, or --disabled"))
	}

	schedule, updateError := apiClient.UpdatePipelineSchedule(command.Context(), selector,
		strings.TrimSpace(scheduleID), request)
	if updateError != nil {
		return updateError
	}
	if rendered, renderError := renderStructured(command, schedule); rendered || renderError != nil {
		return renderError
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Schedule updated: %s (%s %s, enabled: %t)\n",
		schedule.ID, schedule.Cron, schedule.Timezone, schedule.Enabled)
	return nil
}

func newPipelineSchedulesDeleteCommand() *cobra.Command {
	deleteCommand := &cobra.Command{
		Use:     "delete <schedule-id>",
		Aliases: []string{"rm"},
		Short:   "Remove a cron schedule",
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector, selectorError := resolvePipelineSelector(command)
			if selectorError != nil {
				return selectorError
			}
			return runPipelineSchedulesDelete(command, selector, arguments[0])
		},
	}
	registerPipelineSelectorFlags(deleteCommand)
	deleteCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	return deleteCommand
}

func runPipelineSchedulesDelete(command *cobra.Command, selector client.PipelineSelector, scheduleID string) error {
	scheduleID = strings.TrimSpace(scheduleID)
	skipConfirmation, _ := command.Flags().GetBool("yes")
	confirmMessage := fmt.Sprintf("Delete pipeline schedule %s? [y/N] ", scheduleID)
	if confirmError := confirmPrompt(command.InOrStdin(), command.OutOrStdout(), confirmMessage, skipConfirmation); confirmError != nil {
		return confirmError
	}
	if deleteError := apiClient.DeletePipelineSchedule(command.Context(), selector, scheduleID); deleteError != nil {
		return deleteError
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Schedule %s deleted.\n", scheduleID)
	return nil
}

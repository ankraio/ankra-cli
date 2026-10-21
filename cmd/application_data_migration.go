package cmd

// `ankra application data-migration`: the runs that move an application's
// data off its vendors under one Launch Plan approval, and the one command
// that undoes a cutover inside its rollback window.

import (
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func newApplicationDataMigrationCommand() *cobra.Command {
	dataMigrationCommand := &cobra.Command{
		Use:     "data-migration",
		Aliases: []string{"data-migrations"},
		Short:   "Follow and roll back an application's data migration",
		Long: `Follow the runs that move an application's data off the vendors it used - a
database, an object store - into services on its cluster, and roll one back.

A run copies the data, makes every vendor source read-only, switches the
application's environment to the in-cluster services in one change, and keeps
the vendor sources read-only for a rollback window. Inside that window,
'ankra application data-migration rollback' puts the previous environment back
and makes the vendor sources writable again. Ankra never deletes data at a
vendor: once the window has closed, the run hands you the teardown checklist.`,
	}
	dataMigrationCommand.AddCommand(
		newApplicationDataMigrationListCommand(),
		newApplicationDataMigrationGetCommand(),
		newApplicationDataMigrationRollbackCommand(),
	)
	return dataMigrationCommand
}

func newApplicationDataMigrationListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:     "list <application>",
		Aliases: []string{"ls"},
		Short:   "List an application's data migration runs",
		Example: `  ankra application data-migration list shop
  ankra application data-migration list shop -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			list, listError := apiClient.ListApplicationDataMigrationRuns(command.Context(), applicationID)
			if listError != nil {
				return listError
			}
			if rendered, renderError := renderStructured(command, list); rendered || renderError != nil {
				return renderError
			}
			printDataMigrationRuns(command.OutOrStdout(), list.Runs)
			return nil
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newApplicationDataMigrationGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <application> <run-id>",
		Short: "Show one data migration run step by step",
		Example: `  ankra application data-migration get shop 6b257030-1d8f-4a9d-92a3-88f346f248bf
  ankra application data-migration get shop 6b257030-1d8f-4a9d-92a3-88f346f248bf -o yaml`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			run, readError := apiClient.GetApplicationDataMigrationRun(command.Context(), applicationID,
				strings.TrimSpace(arguments[1]))
			if readError != nil {
				return readError
			}
			if rendered, renderError := renderStructured(command, run); rendered || renderError != nil {
				return renderError
			}
			printDataMigrationRun(command.OutOrStdout(), run)
			return nil
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func newApplicationDataMigrationRollbackCommand() *cobra.Command {
	rollbackCommand := &cobra.Command{
		Use:   "rollback <application> <run-id>",
		Short: "Roll a data migration back to the vendors",
		Long: `Roll a data migration back: the application's environment goes back to what it
was before the cutover, and every vendor source the run made read-only is made
writable again. Nothing is deleted at the vendor or in the cluster.

A rollback is possible from the moment the cutover starts until the rollback
window closes. The platform records the request and carries it out on its next
pass; follow it with 'ankra application data-migration get'. It is refused
before the cutover (nothing at a vendor changed yet), after the window closed,
for a finished run, and while a rollback is already requested or running.

Requires permission to deploy applications on the run's cluster.`,
		Example: `  ankra application data-migration rollback shop 6b257030-1d8f-4a9d-92a3-88f346f248bf
  ankra application data-migration rollback shop 6b257030-1d8f-4a9d-92a3-88f346f248bf --yes -o json`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			runID := strings.TrimSpace(arguments[1])
			prompt := fmt.Sprintf("Roll back data migration %s of %q? The application's environment goes back to "+
				"the vendors and their sources become writable again; writes made to the in-cluster services since "+
				"the cutover stay there and are not copied back. [y/N]: ", runID, strings.TrimSpace(arguments[0]))
			yes, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), prompt, yes); confirmError != nil {
				return confirmError
			}
			run, rollbackError := apiClient.RollbackApplicationDataMigrationRun(command.Context(), applicationID, runID)
			if rollbackError != nil {
				return rollbackError
			}
			if rendered, renderError := renderStructured(command, run); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Rollback of data migration %s requested.\n", run.ID)
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Follow it with 'ankra application data-migration get %s %s': "+
				"it reads \"rolling-back\", then \"rolled-back\" once the environment is back and the vendor sources "+
				"accept writes, or \"rollback-failed\" with the step that could not finish.\n",
				strings.TrimSpace(arguments[0]), run.ID)
			return nil
		},
	}
	rollbackCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(rollbackCommand)
	return rollbackCommand
}

// printDataMigrationRuns renders the runs as a table, newest first.
func printDataMigrationRuns(out io.Writer, runs []client.DataMigrationRun) {
	if len(runs) == 0 {
		_, _ = fmt.Fprintln(out, "No data migration has been started for this application.")
		return
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "Status", "Started", "Rollback window closes"})
	for _, run := range runs {
		writer.AppendRow(table.Row{run.ID, dataMigrationStatus(run), run.CreatedAt, valueOrDash(run.RollbackClosesAt)})
	}
	writer.Render()
}

// printDataMigrationRun renders one run: its status and why it stopped, the
// rollback window, the variables it rewires, and every step.
func printDataMigrationRun(out io.Writer, run *client.DataMigrationRun) {
	_, _ = fmt.Fprintf(out, "Data migration %s: %s\n", run.ID, dataMigrationStatus(*run))
	if run.Run.Reason != "" {
		_, _ = fmt.Fprintf(out, "Reason: %s\n", run.Run.Reason)
	}
	if run.RollbackClosesAt != nil {
		_, _ = fmt.Fprintf(out, "Rollback window closes: %s\n", *run.RollbackClosesAt)
	}
	if len(run.EnvironmentNames) > 0 {
		_, _ = fmt.Fprintf(out, "Variables the cutover rewires: %s\n", strings.Join(run.EnvironmentNames, ", "))
	}
	printDataMigrationSteps(out, "Steps", run.Run.Steps)
	if hasStartedStep(run.Run.Rollback) {
		printDataMigrationSteps(out, "Rollback", run.Run.Rollback)
	}
}

func printDataMigrationSteps(out io.Writer, title string, steps []client.DataMigrationStep) {
	_, _ = fmt.Fprintf(out, "\n%s:\n", title)
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Step", "Status", "Detail"})
	for _, step := range steps {
		writer.AppendRow(table.Row{step.Key, step.Status, step.Detail})
	}
	writer.Render()
}

// dataMigrationStatus is the run's status, saying when a rollback was asked
// for but not yet applied.
func dataMigrationStatus(run client.DataMigrationRun) string {
	if run.RollbackRequestedAt != nil && run.Status != "rolling-back" && run.Status != "rolled-back" &&
		run.Status != "rollback-failed" {
		return run.Status + " (rollback requested)"
	}
	return run.Status
}

func hasStartedStep(steps []client.DataMigrationStep) bool {
	for _, step := range steps {
		if step.Status != "pending" {
			return true
		}
	}
	return false
}

func valueOrDash(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

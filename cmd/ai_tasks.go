package cmd

// Agent tasks: the definitions behind the runs `ankra agents` reads. An agent
// task is one AI agent of the organisation - its goal, what triggers it, the
// tools it may use and how far it may act alone - and these commands list,
// define, hire and control them.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

type aiTaskSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Trigger struct {
		Kind           string `json:"kind"`
		CronExpression string `json:"cron_expression"`
	} `json:"trigger"`
	ToolProfile   string  `json:"tool_profile"`
	AutonomyLevel string  `json:"autonomy_level"`
	Enabled       bool    `json:"enabled"`
	ClusterID     *string `json:"cluster_id"`
	TemplateID    *string `json:"template_id"`
	NextRunAt     *string `json:"next_run_at"`
	LastRunAt     *string `json:"last_run_at"`
	GoalPrompt    string  `json:"goal_prompt"`
}

func (task aiTaskSummary) trigger() string {
	if task.Trigger.CronExpression != "" {
		return task.Trigger.Kind + " (" + task.Trigger.CronExpression + ")"
	}
	return task.Trigger.Kind
}

// resolveAITaskID answers the id of the agent task a command names: the id
// itself, or the one task whose name is exactly the reference.
func resolveAITaskID(command *cobra.Command, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", withExitCode(exitUsage, errors.New("an agent task id or name is required"))
	}
	if looksLikeUUID(reference) {
		return reference, nil
	}
	document, listError := apiClient.ListAITasks(command.Context())
	if listError != nil {
		return "", fmt.Errorf("looking up agent task %q: %w", reference, listError)
	}
	var listing struct {
		Tasks []aiTaskSummary `json:"tasks"`
	}
	if decodeError := decodeDocument(document, &listing); decodeError != nil {
		return "", decodeError
	}
	matches := []string{}
	for _, task := range listing.Tasks {
		if task.Name == reference {
			matches = append(matches, task.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", withExitCode(exitNotFound, fmt.Errorf(
			"no agent task is named %q; 'ankra ai tasks list' shows the ones that exist", reference))
	default:
		return "", withExitCode(exitUsage, fmt.Errorf(
			"%d agent tasks are named %q - pass the id instead (%s)", len(matches), reference, strings.Join(matches, ", ")))
	}
}

func newAITasksCommand() *cobra.Command {
	tasksCommand := &cobra.Command{
		Use:     "tasks",
		Aliases: []string{"task"},
		Short:   "Define, hire and control the organisation's AI agents",
		Long: `Define, hire and control the organisation's agent tasks: the AI agents that
run on a schedule, on an alert or on demand, each with a goal, a tool profile,
an autonomy level and a budget.

'ankra agents' reads the runs these agents produce; the commands here manage
the agents themselves. A task is named by its id or by its exact name.
Reading needs ai.use; every change needs ai.manage and an organisation admin.`,
	}
	templatesCommand := &cobra.Command{
		Use:   "templates",
		Short: "List the built-in agent roles and hire one",
	}
	templatesCommand.AddCommand(newAITaskTemplatesListCommand(), newAITaskTemplatesHireCommand())
	tasksCommand.AddCommand(
		newAITasksListCommand(),
		newAITasksGetCommand(),
		newAITasksWriteCommand(false),
		newAITasksWriteCommand(true),
		newAITasksDeleteCommand(),
		newAITasksControlCommand("run", "Queue an agent task to run now",
			"Queue an enabled agent task for immediate dispatch. The scheduler picks it up\nwithin its poll interval, and does nothing when a run is already live."),
		newAITasksControlCommand("pause", "Stop scheduling an agent task",
			"Stop scheduling an agent task. A run already in progress finishes its current\nturn; 'ankra ai tasks stop' cancels it as well."),
		newAITasksControlCommand("resume", "Schedule a paused agent task again",
			"Re-enable a paused agent task and restore its schedule from its trigger."),
		newAITasksControlCommand("stop", "Pause an agent task and cancel its live run",
			"Pause an agent task and cancel its live run in the same step."),
		newAITasksReapplyTemplateCommand(),
		newAITasksRunsCommand(),
		templatesCommand,
	)
	return tasksCommand
}

func newAITasksListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List the organisation's agent tasks",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			document, listError := apiClient.ListAITasks(command.Context())
			if listError != nil {
				return listError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var listing struct {
					Tasks []aiTaskSummary `json:"tasks"`
				}
				if decodeError := decodeDocument(document, &listing); decodeError != nil {
					return decodeError
				}
				if len(listing.Tasks) == 0 {
					_, _ = fmt.Fprintln(out, "No agent tasks. 'ankra ai tasks templates list' shows the roles you can hire.")
					return nil
				}
				writer := table.NewWriter()
				writer.SetOutputMirror(out)
				writer.AppendHeader(table.Row{"ID", "NAME", "TRIGGER", "TOOLS", "AUTONOMY", "ENABLED", "NEXT RUN"})
				for _, task := range listing.Tasks {
					writer.AppendRow(table.Row{task.ID, truncateCell(task.Name, 40), task.trigger(), task.ToolProfile,
						task.AutonomyLevel, task.Enabled, valueOrDash(task.NextRunAt)})
				}
				writer.Render()
				return nil
			})
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func printAITask(out io.Writer, document json.RawMessage) error {
	var task aiTaskSummary
	if decodeError := decodeDocument(document, &task); decodeError != nil {
		return decodeError
	}
	_, _ = fmt.Fprintf(out, "Task:      %s (%s)\n", task.Name, task.ID)
	_, _ = fmt.Fprintf(out, "Trigger:   %s\n", task.trigger())
	_, _ = fmt.Fprintf(out, "Tools:     %s\n", task.ToolProfile)
	_, _ = fmt.Fprintf(out, "Autonomy:  %s\n", task.AutonomyLevel)
	_, _ = fmt.Fprintf(out, "Enabled:   %t\n", task.Enabled)
	_, _ = fmt.Fprintf(out, "Cluster:   %s\n", valueOrDash(task.ClusterID))
	_, _ = fmt.Fprintf(out, "Template:  %s\n", valueOrDash(task.TemplateID))
	_, _ = fmt.Fprintf(out, "Next run:  %s\n", valueOrDash(task.NextRunAt))
	_, _ = fmt.Fprintf(out, "Last run:  %s\n", valueOrDash(task.LastRunAt))
	_, _ = fmt.Fprintf(out, "Goal:      %s\n", truncateCell(strings.Join(strings.Fields(task.GoalPrompt), " "), 200))
	return nil
}

func newAITasksGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <task>",
		Short: "Show one agent task",
		Long:  "Show one agent task. -o json prints its whole definition, the budget and capabilities included.",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			taskID, resolveError := resolveAITaskID(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			document, getError := apiClient.GetAITask(command.Context(), taskID)
			if getError != nil {
				return getError
			}
			return renderDocument(command, document, func(out io.Writer) error { return printAITask(out, document) })
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

// aiTaskDefinitionFromFlags builds the body of a create or an update: the -f
// document, with every flag given laid over it.
func aiTaskDefinitionFromFlags(command *cobra.Command) (map[string]any, error) {
	definition := map[string]any{}
	if path, _ := command.Flags().GetString("file"); path != "" {
		document, readError := readAlertRuleDocument(command)
		if readError != nil {
			return nil, readError
		}
		definition = document
	}
	for flagName, member := range map[string]string{
		"name": "name", "goal": "goal_prompt", "tool-profile": "tool_profile",
		"autonomy-level": "autonomy_level", "model-mode": "model_mode",
	} {
		if command.Flags().Changed(flagName) {
			value, _ := command.Flags().GetString(flagName)
			definition[member] = strings.TrimSpace(value)
		}
	}
	if command.Flags().Changed("enabled") {
		definition["enabled"], _ = command.Flags().GetBool("enabled")
	}
	if clusterReference, _ := command.Flags().GetString("cluster"); strings.TrimSpace(clusterReference) != "" {
		clusterID, clusterError := resolveClusterID(strings.TrimSpace(clusterReference))
		if clusterError != nil {
			return nil, clusterError
		}
		definition["cluster_id"] = clusterID
	}
	trigger, _ := definition["trigger"].(map[string]any)
	if trigger == nil {
		trigger = map[string]any{}
	}
	for flagName, member := range map[string]string{
		"trigger": "kind", "cron": "cron_expression", "timezone": "timezone", "alert": "alert_id",
	} {
		if command.Flags().Changed(flagName) {
			value, _ := command.Flags().GetString(flagName)
			trigger[member] = strings.TrimSpace(value)
		}
	}
	if len(trigger) > 0 {
		if kind, _ := trigger["kind"].(string); kind == "" {
			return nil, withExitCode(exitUsage, errors.New(
				"a trigger needs its kind: pass --trigger mission|cron|alert|manual beside --cron, --timezone or --alert"))
		}
		definition["trigger"] = trigger
	}
	return definition, nil
}

func newAITasksWriteCommand(isUpdate bool) *cobra.Command {
	writeCommand := &cobra.Command{
		Use:   "create",
		Short: "Create an agent task",
		Long: `Create an agent task. --name, --goal and --trigger are required, as flags or
as the name, goal_prompt and trigger members of the -f document; a flag wins
over the document. The tool profile defaults to read_only.

The budget, capabilities and the other members of the definition are set
through -f, which takes the platform's body as YAML or JSON (- reads stdin).`,
		Example: `  ankra ai tasks create --name "Nightly cost review" --goal "Review yesterday's spend" \
      --trigger cron --cron "0 6 * * *" --timezone Europe/Stockholm
  ankra ai tasks create -f task.yaml`,
		Args: cobra.NoArgs,
	}
	if isUpdate {
		writeCommand.Use = "update <task>"
		writeCommand.Short = "Change an agent task's definition"
		writeCommand.Long = `Change an agent task's definition. Only the members you name change: as
flags, or as members of the -f document (YAML or JSON, - reads stdin); a flag
wins over the document. A trigger is written whole, so --cron, --timezone and
--alert need --trigger beside them.`
		writeCommand.Example = `  ankra ai tasks update "Nightly cost review" --autonomy-level propose
  ankra ai tasks update 0b8f6c2e-0000-4000-8000-000000000001 --enabled=false`
		writeCommand.Args = cobra.ExactArgs(1)
	}
	writeCommand.RunE = func(command *cobra.Command, arguments []string) error {
		if _, formatError := structuredFormatFromFlags(command); formatError != nil {
			return formatError
		}
		definition, definitionError := aiTaskDefinitionFromFlags(command)
		if definitionError != nil {
			return definitionError
		}
		var document json.RawMessage
		var writeError error
		verb := "Created"
		if isUpdate {
			if len(definition) == 0 {
				return withExitCode(exitUsage, errors.New("nothing to change: pass at least one flag or -f"))
			}
			taskID, resolveError := resolveAITaskID(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			verb = "Updated"
			document, writeError = apiClient.UpdateAITask(command.Context(), taskID, definition)
		} else {
			for _, member := range []string{"name", "goal_prompt", "trigger"} {
				if value, isSet := definition[member]; !isSet || value == "" {
					return withExitCode(exitUsage, fmt.Errorf(
						"%s is required: pass --name, --goal and --trigger, or name them in -f", member))
				}
			}
			document, writeError = apiClient.CreateAITask(command.Context(), definition)
		}
		if writeError != nil {
			return writeError
		}
		return renderDocument(command, document, func(out io.Writer) error {
			_, _ = fmt.Fprintf(out, "%s agent task.\n", verb)
			return printAITask(out, document)
		})
	}
	writeCommand.Flags().StringP("file", "f", "", "YAML or JSON file with the task definition (- reads stdin)")
	writeCommand.Flags().String("name", "", "The agent's name")
	writeCommand.Flags().String("goal", "", "What the agent is asked to do on every run")
	writeCommand.Flags().String("trigger", "", "What starts a run: mission, cron, alert or manual")
	writeCommand.Flags().String("cron", "", "Cron expression of a cron trigger")
	writeCommand.Flags().String("timezone", "", "Time zone the cron expression is read in")
	writeCommand.Flags().String("alert", "", "Alert id of an alert trigger")
	writeCommand.Flags().String("tool-profile", "", "Tools the agent may use: read_only, standard or admin")
	writeCommand.Flags().String("autonomy-level", "", "How far the agent acts alone: read_only, propose or auto")
	writeCommand.Flags().String("model-mode", "", "Model mode the agent runs on")
	writeCommand.Flags().String("cluster", "", "Cluster name or id the agent is scoped to")
	writeCommand.Flags().Bool("enabled", true, "Whether the agent is scheduled")
	registerStructuredOutputFlags(writeCommand)
	return writeCommand
}

func newAITasksDeleteCommand() *cobra.Command {
	deleteCommand := &cobra.Command{
		Use:   "delete <task>",
		Short: "Delete an agent task",
		Long:  "Delete an agent task so it is no longer scheduled. Its past runs stay readable under 'ankra agents runs'.",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			taskID, resolveError := resolveAITaskID(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			skipConfirmation, _ := command.Flags().GetBool("yes")
			if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
				fmt.Sprintf("Delete agent task %q? It is no longer scheduled and cannot be restored. [y/N]: ",
					strings.TrimSpace(arguments[0])), skipConfirmation); confirmError != nil {
				return confirmError
			}
			if deleteError := apiClient.DeleteAITask(command.Context(), taskID); deleteError != nil {
				return deleteError
			}
			if rendered, renderError := renderStructured(command, map[string]any{"deleted": true, "id": taskID}); rendered || renderError != nil {
				return renderError
			}
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Deleted agent task %s.\n", taskID)
			return nil
		},
	}
	deleteCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(deleteCommand)
	return deleteCommand
}

// newAITasksControlCommand builds one of the bodyless control verbs. Stop
// cancels work in flight, so it alone confirms first.
func newAITasksControlCommand(action string, short string, long string) *cobra.Command {
	controlCommand := &cobra.Command{
		Use:   action + " <task>",
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			taskID, resolveError := resolveAITaskID(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			if action == "stop" {
				skipConfirmation, _ := command.Flags().GetBool("yes")
				if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
					fmt.Sprintf("Stop agent task %q? It is paused and its live run is cancelled. [y/N]: ",
						strings.TrimSpace(arguments[0])), skipConfirmation); confirmError != nil {
					return confirmError
				}
			}
			document, controlError := apiClient.ControlAITask(command.Context(), taskID, action)
			if controlError != nil {
				return controlError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var outcome struct {
					Detail             string `json:"detail"`
					CancelledRunID     string `json:"cancelled_run_id"`
					CancelledSessionID string `json:"cancelled_session_id"`
				}
				if decodeError := decodeDocument(document, &outcome); decodeError != nil {
					return decodeError
				}
				_, _ = fmt.Fprintln(out, orDash(outcome.Detail))
				if outcome.CancelledRunID != "" {
					_, _ = fmt.Fprintf(out, "Cancelled run: %s\n", outcome.CancelledRunID)
				}
				return nil
			})
		},
	}
	if action == "stop" {
		controlCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	}
	registerStructuredOutputFlags(controlCommand)
	return controlCommand
}

func newAITasksReapplyTemplateCommand() *cobra.Command {
	reapplyCommand := &cobra.Command{
		Use:   "reapply-template <task>",
		Short: "Bring a hired agent up to its role template's current envelope",
		Long: `Bring a hired agent up to its role template's current envelope: the goal,
tools, autonomy and budget the role ships with today.

An envelope someone customised is left alone and the platform answers 409
with the reason. --force replaces it with the template's, and asks first.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			taskID, resolveError := resolveAITaskID(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			force, _ := command.Flags().GetBool("force")
			if force {
				skipConfirmation, _ := command.Flags().GetBool("yes")
				if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(),
					fmt.Sprintf("Replace the customised envelope of agent task %q with its template's? [y/N]: ",
						strings.TrimSpace(arguments[0])), skipConfirmation); confirmError != nil {
					return confirmError
				}
			}
			document, reapplyError := apiClient.ReapplyAITaskTemplate(command.Context(), taskID, force)
			if reapplyError != nil {
				return reapplyError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				_, _ = fmt.Fprintf(out, "Re-applied the role template to agent task %s.\n", taskID)
				return nil
			})
		},
	}
	reapplyCommand.Flags().Bool("force", false, "Replace an envelope that was customised")
	reapplyCommand.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt --force asks")
	registerStructuredOutputFlags(reapplyCommand)
	return reapplyCommand
}

func newAITasksRunsCommand() *cobra.Command {
	runsCommand := &cobra.Command{
		Use:   "runs <task>",
		Short: "List an agent task's recent runs, newest first",
		Long:  "List an agent task's recent runs, newest first. 'ankra agents run <run-id>' shows one in full.",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			limit, _ := command.Flags().GetInt("limit")
			if limit < 1 || limit > client.AITaskRunListCap {
				return withExitCode(exitUsage, fmt.Errorf("--limit must be between 1 and %d", client.AITaskRunListCap))
			}
			taskID, resolveError := resolveAITaskID(command, arguments[0])
			if resolveError != nil {
				return resolveError
			}
			document, listError := apiClient.ListAITaskRuns(command.Context(), taskID, limit)
			if listError != nil {
				return listError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var listing struct {
					Runs []struct {
						ID             string  `json:"id"`
						Status         string  `json:"status"`
						StartedAt      *string `json:"started_at"`
						FinishedAt     *string `json:"finished_at"`
						OutcomeSummary *string `json:"outcome_summary"`
					} `json:"runs"`
				}
				if decodeError := decodeDocument(document, &listing); decodeError != nil {
					return decodeError
				}
				if len(listing.Runs) == 0 {
					_, _ = fmt.Fprintln(out, "This agent task has not run yet.")
					return nil
				}
				writer := table.NewWriter()
				writer.SetOutputMirror(out)
				writer.AppendHeader(table.Row{"RUN ID", "STATUS", "STARTED", "FINISHED", "OUTCOME"})
				for _, run := range listing.Runs {
					writer.AppendRow(table.Row{run.ID, run.Status, valueOrDash(run.StartedAt), valueOrDash(run.FinishedAt),
						truncateCell(valueOrDash(run.OutcomeSummary), 60)})
				}
				writer.Render()
				return nil
			})
		},
	}
	runsCommand.Flags().Int("limit", 20, fmt.Sprintf("How many runs to list (1-%d)", client.AITaskRunListCap))
	registerStructuredOutputFlags(runsCommand)
	return runsCommand
}

func newAITaskTemplatesListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List the built-in agent roles and whether each is hired",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			document, listError := apiClient.ListAITaskTemplates(command.Context())
			if listError != nil {
				return listError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				var listing struct {
					Templates []struct {
						ID            string `json:"id"`
						Name          string `json:"name"`
						Category      string `json:"category"`
						AutonomyLevel string `json:"autonomy_level"`
						Hired         bool   `json:"hired"`
						Tagline       string `json:"tagline"`
					} `json:"templates"`
				}
				if decodeError := decodeDocument(document, &listing); decodeError != nil {
					return decodeError
				}
				writer := table.NewWriter()
				writer.SetOutputMirror(out)
				writer.AppendHeader(table.Row{"TEMPLATE", "NAME", "CATEGORY", "AUTONOMY", "HIRED", "WHAT IT DOES"})
				for _, template := range listing.Templates {
					writer.AppendRow(table.Row{template.ID, template.Name, template.Category, template.AutonomyLevel,
						template.Hired, truncateCell(template.Tagline, 60)})
				}
				writer.Render()
				return nil
			})
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newAITaskTemplatesHireCommand() *cobra.Command {
	hireCommand := &cobra.Command{
		Use:   "hire <template>",
		Short: "Hire an agent from a built-in role",
		Long: `Create an agent task from a built-in role; 'ankra ai tasks templates list'
names them. --cluster scopes the agent to one cluster. Hiring the Reviewer
also makes it the ticket desk's reviewer when none is set. A role that is
already hired answers 409.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			clusterID := ""
			if clusterReference, _ := command.Flags().GetString("cluster"); strings.TrimSpace(clusterReference) != "" {
				resolvedID, clusterError := resolveClusterID(strings.TrimSpace(clusterReference))
				if clusterError != nil {
					return clusterError
				}
				clusterID = resolvedID
			}
			document, hireError := apiClient.HireAITaskTemplate(command.Context(), strings.TrimSpace(arguments[0]), clusterID)
			if hireError != nil {
				return hireError
			}
			return renderDocument(command, document, func(out io.Writer) error {
				_, _ = fmt.Fprintln(out, "Hired.")
				return printAITask(out, document)
			})
		},
	}
	hireCommand.Flags().String("cluster", "", "Cluster name or id the agent is scoped to")
	registerStructuredOutputFlags(hireCommand)
	return hireCommand
}

func init() {
	aiCmd.AddCommand(newAITasksCommand(), newAIConditionsCommand(), newAIPlaybooksCommand(), newAIHandoffsCommand())
}

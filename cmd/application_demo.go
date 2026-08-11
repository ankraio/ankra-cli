package cmd

import (
	"encoding/json"
	"errors"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// newApplicationDemoCommand groups the ephemeral demo-workspace verbs behind
// `ankra application demo ...`. The demo mutations skip CSRF on the bearer
// path, so they are safe to drive from the CLI with a PAT.
func newApplicationDemoCommand() *cobra.Command {
	demoCommand := &cobra.Command{
		Use:   "demo",
		Short: "Manage ephemeral demo workspaces for an application",
		Long:  "Deploy, inspect, and stop short-lived demo workspaces for a branch or pull request of an application.",
	}
	demoCommand.AddCommand(
		newApplicationDemoListCommand(),
		newApplicationDemoBuildCommand(),
		newApplicationDemoDeployCommand(),
		newApplicationDemoStopCommand(),
		newApplicationDemoDetailCommand(),
		newApplicationDemoLogsCommand(),
		newApplicationDemoConfigCommand(),
		newApplicationDemoFixCommand(),
	)
	return demoCommand
}

func newApplicationDemoDetailCommand() *cobra.Command {
	detailCommand := &cobra.Command{
		Use:   "detail <application-id> <workspace-id>",
		Short: "Show a demo workspace's record, provisioning steps, and failure detail",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, detailError := apiClient.GetApplicationDemoDetail(command.Context(),
				strings.TrimSpace(arguments[0]), strings.TrimSpace(arguments[1]))
			if detailError != nil {
				return detailError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(detailCommand)
	return detailCommand
}

func newApplicationDemoLogsCommand() *cobra.Command {
	logsCommand := &cobra.Command{
		Use:   "logs <application-id> <workspace-id>",
		Short: "Fetch a bounded tail of the demo container's logs",
		Long:  "Fetch a bounded tail of the demo container's logs. One-shot: the command returns after the fetch instead of following the stream.",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			tailLines, _ := command.Flags().GetInt("tail")
			payload, logsError := apiClient.GetApplicationDemoLogs(command.Context(),
				strings.TrimSpace(arguments[0]), strings.TrimSpace(arguments[1]), tailLines)
			if logsError != nil {
				return logsError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	logsCommand.Flags().Int("tail", 200, "Number of log lines from the end")
	registerStructuredOutputFlags(logsCommand)
	return logsCommand
}

func newApplicationDemoConfigCommand() *cobra.Command {
	configCommand := &cobra.Command{
		Use:   "config",
		Short: "Read or update the application's saved demo defaults",
	}
	configCommand.AddCommand(
		newApplicationDemoConfigGetCommand(),
		newApplicationDemoConfigSetCommand(),
	)
	return configCommand
}

func newApplicationDemoConfigGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <application-id>",
		Short: "Show the saved demo defaults (env, database, migration command, extensions)",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, getError := apiClient.GetApplicationDemoConfig(command.Context(), strings.TrimSpace(arguments[0]))
			if getError != nil {
				return getError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

// demoConfigDocument is the demo-config wire shape this command edits. The
// dependency blocks (stack profile, base stack) are deliberately absent:
// the PUT preserves keys the body does not carry, and this command must
// never rewrite designations it does not manage.
type demoConfigDocument struct {
	Env                []map[string]any `json:"env"`
	Database           bool             `json:"database"`
	MigrateCommand     *string          `json:"migrate_command,omitempty"`
	DatabaseExtensions *[]string        `json:"database_extensions,omitempty"`
}

func newApplicationDemoConfigSetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set <application-id>",
		Short: "Update the saved demo defaults, preserving everything you do not name",
		Long: `Update the application's saved demo defaults. The command fetches the
current configuration first and applies only the flags you set: --env
entries override by name, everything else is carried forward.`,
		Example: `  ankra application demo config set <app-id> --database=true --env DATABASE_URL='${{ ankra.demo_database.url }}'
  ankra application demo config set <app-id> --migrate-command 'pnpm run db:migrate' --database-extension vector`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID := strings.TrimSpace(arguments[0])
			currentPayload, getError := apiClient.GetApplicationDemoConfig(command.Context(), applicationID)
			if getError != nil {
				return getError
			}
			var document demoConfigDocument
			if unmarshalError := json.Unmarshal(currentPayload, &document); unmarshalError != nil {
				return unmarshalError
			}
			if document.Env == nil {
				document.Env = []map[string]any{}
			}

			environmentFlags, _ := command.Flags().GetStringArray("env")
			for _, entry := range environmentFlags {
				name, value, found := strings.Cut(entry, "=")
				name = strings.TrimSpace(name)
				if !found || name == "" {
					return withExitCode(exitUsage, errors.New("--env entries must be NAME=VALUE"))
				}
				replaced := false
				for _, existing := range document.Env {
					if existingName, _ := existing["name"].(string); existingName == name {
						existing["value"] = value
						existing["secret"] = false
						replaced = true
					}
				}
				if !replaced {
					document.Env = append(document.Env,
						map[string]any{"name": name, "value": value, "secret": false})
				}
			}
			removeFlags, _ := command.Flags().GetStringArray("remove-env")
			for _, name := range removeFlags {
				name = strings.TrimSpace(name)
				kept := document.Env[:0]
				for _, existing := range document.Env {
					if existingName, _ := existing["name"].(string); existingName != name {
						kept = append(kept, existing)
					}
				}
				document.Env = kept
			}
			if command.Flags().Changed("database") {
				document.Database, _ = command.Flags().GetBool("database")
			}
			if command.Flags().Changed("migrate-command") {
				migrateCommand, _ := command.Flags().GetString("migrate-command")
				document.MigrateCommand = &migrateCommand
			}
			if command.Flags().Changed("database-extension") {
				extensions, _ := command.Flags().GetStringArray("database-extension")
				document.DatabaseExtensions = &extensions
			}

			body, marshalError := json.Marshal(document)
			if marshalError != nil {
				return marshalError
			}
			payload, updateError := apiClient.UpdateApplicationDemoConfig(command.Context(), applicationID, body)
			if updateError != nil {
				return updateError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	setCommand.Flags().StringArray("env", nil, "Set an env entry as NAME=VALUE (repeatable; overrides by name)")
	setCommand.Flags().StringArray("remove-env", nil, "Remove an env entry by name (repeatable)")
	setCommand.Flags().Bool("database", false, "Provision the throwaway per-demo Postgres")
	setCommand.Flags().String("migrate-command", "", "Command that provisions a fresh demo database's schema (empty clears)")
	setCommand.Flags().StringArray("database-extension", nil, "Postgres extension the demo database creates at initdb (repeatable; replaces the saved list)")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

func newApplicationDemoFixCommand() *cobra.Command {
	fixCommand := &cobra.Command{
		Use:   "fix <application-id> <workspace-id>",
		Short: "Dispatch the AI pre-setup mission for a failed demo",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, fixError := apiClient.FixApplicationDemo(command.Context(),
				strings.TrimSpace(arguments[0]), strings.TrimSpace(arguments[1]))
			if fixError != nil {
				return fixError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(fixCommand)
	return fixCommand
}

func newApplicationDemoListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:   "list <application-id>",
		Short: "List the application's active demo workspaces",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, listError := apiClient.GetApplicationDemos(command.Context(), strings.TrimSpace(arguments[0]))
			if listError != nil {
				return listError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newApplicationDemoBuildCommand() *cobra.Command {
	buildCommand := &cobra.Command{
		Use:   "build <application-id>",
		Short: "Check whether a branch has a demo-ready container image",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			branch, _ := command.Flags().GetString("branch")
			branch = strings.TrimSpace(branch)
			if branch == "" {
				return withExitCode(exitUsage, errors.New("--branch is required"))
			}
			payload, buildError := apiClient.CheckApplicationDemoBuild(command.Context(), strings.TrimSpace(arguments[0]), branch)
			if buildError != nil {
				return buildError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	buildCommand.Flags().String("branch", "", "Repository branch to inspect (required)")
	registerStructuredOutputFlags(buildCommand)
	return buildCommand
}

func newApplicationDemoDeployCommand() *cobra.Command {
	deployCommand := &cobra.Command{
		Use:   "deploy <application-id>",
		Short: "Deploy an ephemeral demo workspace",
		Long: `Deploy a short-lived demo workspace for a branch or pull request.

All flags are optional; only the flags you set are sent, so the backend
applies its own defaults for the rest.`,
		Example: `  ankra application demo deploy <app-id> --branch feature/login
  ankra application demo deploy <app-id> --pr-number 42 --ttl-hours 8`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			demoRequest := client.DeployApplicationDemoRequest{}
			if command.Flags().Changed("branch") {
				branch, _ := command.Flags().GetString("branch")
				demoRequest.Branch = &branch
			}
			if command.Flags().Changed("pr-number") {
				prNumber, _ := command.Flags().GetInt("pr-number")
				demoRequest.PRNumber = &prNumber
			}
			if command.Flags().Changed("image-tag") {
				imageTag, _ := command.Flags().GetString("image-tag")
				demoRequest.ImageTag = &imageTag
			}
			if command.Flags().Changed("ttl-hours") {
				ttlHours, _ := command.Flags().GetInt("ttl-hours")
				demoRequest.TTLHours = &ttlHours
			}
			if command.Flags().Changed("container-port") {
				containerPort, _ := command.Flags().GetInt("container-port")
				demoRequest.ContainerPort = &containerPort
			}
			payload, deployError := apiClient.DeployApplicationDemo(command.Context(), strings.TrimSpace(arguments[0]), demoRequest)
			if deployError != nil {
				return deployError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	deployCommand.Flags().String("branch", "", "Repository branch to deploy")
	deployCommand.Flags().Int("pr-number", 0, "Pull request number to deploy")
	deployCommand.Flags().String("image-tag", "", "Explicit container image tag to deploy")
	deployCommand.Flags().Int("ttl-hours", 0, "Lifetime of the demo workspace in hours")
	deployCommand.Flags().Int("container-port", 0, "Container port to expose")
	registerStructuredOutputFlags(deployCommand)
	return deployCommand
}

func newApplicationDemoStopCommand() *cobra.Command {
	stopCommand := &cobra.Command{
		Use:   "stop <application-id> <workspace-id>",
		Short: "Stop and tear down a demo workspace",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			workspaceID := strings.TrimSpace(arguments[1])
			if workspaceID == "" {
				return withExitCode(exitUsage, errors.New("workspace id cannot be empty"))
			}
			payload, stopError := apiClient.StopApplicationDemo(command.Context(), strings.TrimSpace(arguments[0]), workspaceID)
			if stopError != nil {
				return stopError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(stopCommand)
	return stopCommand
}

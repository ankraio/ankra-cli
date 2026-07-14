package cmd

import (
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
	)
	return demoCommand
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

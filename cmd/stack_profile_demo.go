package cmd

import (
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The stack-profile demo commands: launch a throwaway, quota-bounded demo of
// a profile on the staging cluster, inspect it, read its logs, and stop it.
// Only one demo of a profile runs at a time and every demo stops itself when
// its timer runs out.

var stackProfilesDemoCmd = &cobra.Command{
	Use:   "demo",
	Short: "Launch and manage throwaway demos of a profile",
	Long: `Launch a demo of a profile on the staging cluster to see what it actually
installs - no cluster of your own required. A demo installs the profile's
add-ons and manifests into one throwaway namespace, quota-bounded so it
cannot crowd the staging cluster, and cleans itself up automatically.`,
}

var stackProfilesDemoListCmd = &cobra.Command{
	Use:   "list [profile-id|profile-name]",
	Short: "List the live demos of a profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, listError := apiClient.ListStackProfileDemos(cmd.Context(), profileID)
		if listError != nil {
			return fmt.Errorf("listing stack profile demos: %w", listError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesDemoLaunchCmd = &cobra.Command{
	Use:   "launch [profile-id|profile-name]",
	Short: "Launch a demo of a profile on the staging cluster",
	Example: `  ankra stack-profiles demo launch postgres-ha
  ankra stack-profiles demo launch postgres-ha --version v2 --ttl-hours 4 --set replicas=1`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		versionRaw, _ := cmd.Flags().GetString("version")
		version, versionError := parseProfileVersionFlag(versionRaw)
		if versionError != nil {
			return versionError
		}
		setValues, _ := cmd.Flags().GetStringArray("set")
		setFiles, _ := cmd.Flags().GetStringArray("set-file")
		setEnvs, _ := cmd.Flags().GetStringArray("set-env")
		parameters, parametersError := buildParameterBindings(setValues, setFiles, setEnvs)
		if parametersError != nil {
			return parametersError
		}

		request := client.LaunchStackProfileDemoRequest{Parameters: parameters}
		if version > 0 {
			request.Version = &version
		}
		if cmd.Flags().Changed("ttl-hours") {
			ttlHours, _ := cmd.Flags().GetInt("ttl-hours")
			request.TTLHours = &ttlHours
		}
		payload, launchError := apiClient.LaunchStackProfileDemo(cmd.Context(), profileID, request)
		if launchError != nil {
			return fmt.Errorf("launching stack profile demo: %w", launchError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesDemoDetailCmd = &cobra.Command{
	Use:   "detail [profile-id|profile-name] <workspace-id>",
	Short: "Show a demo's workloads and readiness",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, detailError := apiClient.GetStackProfileDemoDetail(cmd.Context(), profileID,
			strings.TrimSpace(args[1]))
		if detailError != nil {
			return fmt.Errorf("getting stack profile demo detail: %w", detailError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesDemoLogsCmd = &cobra.Command{
	Use:   "logs [profile-id|profile-name] <workspace-id>",
	Short: "Read a demo's pod logs",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, logsError := apiClient.GetStackProfileDemoLogs(cmd.Context(), profileID,
			strings.TrimSpace(args[1]))
		if logsError != nil {
			return fmt.Errorf("getting stack profile demo logs: %w", logsError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesDemoStopCmd = &cobra.Command{
	Use:   "stop [profile-id|profile-name] <workspace-id>",
	Short: "Stop a demo and clean up its namespace",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, stopError := apiClient.StopStackProfileDemo(cmd.Context(), profileID,
			strings.TrimSpace(args[1]))
		if stopError != nil {
			return fmt.Errorf("stopping stack profile demo: %w", stopError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

func init() {
	registerStructuredOutputFlags(stackProfilesDemoListCmd)

	stackProfilesDemoLaunchCmd.Flags().String("version", "", "Profile version to demo, as 1 or v1 (defaults to the current version)")
	stackProfilesDemoLaunchCmd.Flags().Int("ttl-hours", 0, "Hours before the demo stops itself (defaults server-side)")
	stackProfilesDemoLaunchCmd.Flags().StringArray("set", nil, "Bind a parameter: name=value (repeatable; not for secrets)")
	stackProfilesDemoLaunchCmd.Flags().StringArray("set-file", nil, "Bind a parameter from a file: name=path (repeatable; secret-safe)")
	stackProfilesDemoLaunchCmd.Flags().StringArray("set-env", nil, "Bind a parameter from an environment variable: name=ENV_VAR (repeatable; secret-safe)")
	registerStructuredOutputFlags(stackProfilesDemoLaunchCmd)

	registerStructuredOutputFlags(stackProfilesDemoDetailCmd)
	registerStructuredOutputFlags(stackProfilesDemoLogsCmd)
	registerStructuredOutputFlags(stackProfilesDemoStopCmd)

	stackProfilesDemoCmd.AddCommand(
		stackProfilesDemoListCmd,
		stackProfilesDemoLaunchCmd,
		stackProfilesDemoDetailCmd,
		stackProfilesDemoLogsCmd,
		stackProfilesDemoStopCmd,
	)
	stackProfilesCmd.AddCommand(stackProfilesDemoCmd)
}

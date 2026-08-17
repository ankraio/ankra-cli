package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
		Long: `Show a demo workspace's record, its components, provisioning steps, and
failure detail. The default rendering summarises the demo; -o json or
-o yaml emit the full payload, including the resource inventory and the
Kubernetes events behind a stalled step.`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, detailError := apiClient.GetApplicationDemoDetail(command.Context(),
				strings.TrimSpace(arguments[0]), strings.TrimSpace(arguments[1]))
			if detailError != nil {
				return detailError
			}
			return renderApplicationDemoDetail(command, payload)
		},
	}
	registerStructuredOutputFlags(detailCommand)
	return detailCommand
}

func newApplicationDemoLogsCommand() *cobra.Command {
	logsCommand := &cobra.Command{
		Use:   "logs <application-id> <workspace-id>",
		Short: "Fetch a bounded tail of the demo container's logs",
		Long: `Fetch a bounded tail of the demo container's logs. One-shot: the command
returns after the fetch instead of following the stream.

A multi-component demo runs one pod per component, and without a selector
the backend reads whichever pod it finds first. --component picks the pod
belonging to that component; --pod addresses one by name (from
` + "`ankra application demo detail`" + `) when a component has more than one.`,
		Example: `  ankra application demo logs <app-id> <workspace-id> --tail 500
  ankra application demo logs <app-id> <workspace-id> --component crm-api`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			applicationID := strings.TrimSpace(arguments[0])
			workspaceID := strings.TrimSpace(arguments[1])
			tailLines, _ := command.Flags().GetInt("tail")
			componentName, _ := command.Flags().GetString("component")
			componentName = strings.TrimSpace(componentName)
			podName, _ := command.Flags().GetString("pod")
			podName = strings.TrimSpace(podName)
			if componentName != "" && podName != "" {
				return withExitCode(exitUsage, errors.New("--component and --pod are mutually exclusive"))
			}
			if componentName != "" {
				resolved, resolveError := resolveDemoComponentPod(command, applicationID, workspaceID, componentName)
				if resolveError != nil {
					return resolveError
				}
				podName = resolved
			}
			payload, logsError := apiClient.GetApplicationDemoLogs(command.Context(),
				applicationID, workspaceID, podName, tailLines)
			if logsError != nil {
				return logsError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	logsCommand.Flags().Int("tail", 200, "Number of log lines from the end")
	logsCommand.Flags().String("component", "", "Read the pod belonging to this component of a multi-component demo")
	logsCommand.Flags().String("pod", "", "Read this pod by name (default: the demo's own pod)")
	registerStructuredOutputFlags(logsCommand)
	return logsCommand
}

// resolveDemoComponentPod maps a component name to the pod whose logs to
// read. The logs endpoint addresses pods, not components, so the selector is
// resolved here from the detail payload's inventory, whose Pod entries carry
// the component they belong to. A ready pod wins over a present one: a
// redeploy leaves the previous rollout's pods in the inventory, and their
// logs describe the code that was replaced.
func resolveDemoComponentPod(command *cobra.Command, applicationID string,
	workspaceID string, componentName string) (string, error) {
	payload, detailError := apiClient.GetApplicationDemoDetail(command.Context(), applicationID, workspaceID)
	if detailError != nil {
		return "", detailError
	}
	var detail demoDetailDocument
	if unmarshalError := json.Unmarshal(payload, &detail); unmarshalError != nil {
		return "", fmt.Errorf("parsing demo detail: %w", unmarshalError)
	}

	candidate := ""
	componentHasPod := false
	for _, resource := range detail.Inspection.Resources {
		if resource.Kind != "Pod" || !strings.EqualFold(resource.Component, componentName) {
			continue
		}
		componentHasPod = true
		if !resource.Present {
			continue
		}
		if resource.Status == "ready" {
			return resource.Name, nil
		}
		if candidate == "" {
			candidate = resource.Name
		}
	}
	if candidate != "" {
		return candidate, nil
	}
	if componentHasPod {
		return "", withExitCode(exitNotFound, fmt.Errorf(
			"component %q has no pod on the cluster yet; run 'ankra application demo detail %s %s' for its provisioning state",
			componentName, applicationID, workspaceID))
	}
	known := detail.Demo.componentNames()
	if len(known) == 0 {
		return "", withExitCode(exitNotFound, fmt.Errorf(
			"this demo records no components, so --component %q cannot be resolved", componentName))
	}
	return "", withExitCode(exitNotFound, fmt.Errorf(
		"component %q is not part of this demo; it runs: %s", componentName, strings.Join(known, ", ")))
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
		Long: `List the application's active demo workspaces, one row each, with the
components every demo runs. -o json or -o yaml emit the full payload,
including the TTL policy and the staging cluster's status.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, listError := apiClient.GetApplicationDemos(command.Context(), strings.TrimSpace(arguments[0]))
			if listError != nil {
				return listError
			}
			return renderApplicationDemoList(command, payload)
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
applies its own defaults for the rest.

A monorepo demo runs every recorded component as its own pod by default.
--component narrows that to the components you name, and the per-component
override flags (--component-tag, --component-port, --component-path) tune
one component each. Because selection and overrides ride the same request
field, an override may only name a component that --component selects: to
tune one component of a full launch, list them all.`,
		Example: `  ankra application demo deploy <app-id> --branch feature/login
  ankra application demo deploy <app-id> --pr-number 42 --ttl-hours 8
  ankra application demo deploy <app-id> --branch main \
    --component crm-frontend --component crm-api \
    --component-port crm-api=8090 --component-path crm-api=/api \
    --entry-component crm-frontend`,
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
			components, componentsError := demoComponentsFromFlags(command)
			if componentsError != nil {
				return componentsError
			}
			demoRequest.Components = components
			if command.Flags().Changed("entry-component") {
				entryComponent, _ := command.Flags().GetString("entry-component")
				demoRequest.EntryComponent = &entryComponent
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
	deployCommand.Flags().StringArray("component", nil,
		"Component of a monorepo to deploy (repeatable; omitted deploys every component)")
	deployCommand.Flags().StringArray("component-tag", nil,
		"Image tag for one selected component as NAME=TAG (repeatable)")
	deployCommand.Flags().StringArray("component-port", nil,
		"Container port for one selected component as NAME=PORT (repeatable)")
	deployCommand.Flags().StringArray("component-path", nil,
		"Ingress path prefix for one selected component as NAME=/prefix (repeatable; empty keeps it in-cluster only)")
	deployCommand.Flags().String("entry-component", "",
		"Component that owns the demo host's root path (default: the backend's entry heuristic)")
	registerStructuredOutputFlags(deployCommand)
	return deployCommand
}

// demoComponentsFromFlags builds the request's components[] from --component
// and the per-component override flags. It returns nil when no component was
// named, which is how the backend is told to deploy every recorded one.
//
// The overrides are keyed by component name rather than positionally so the
// flags read the same way in any order, and every override must name a
// selected component: components[] carries the selection as well as the
// overrides, so an override for an unlisted component would silently narrow
// the launch to that component alone.
func demoComponentsFromFlags(command *cobra.Command) ([]client.DeployApplicationDemoComponent, error) {
	names, _ := command.Flags().GetStringArray("component")
	imageTags, tagError := demoComponentOverrides(command, "component-tag")
	if tagError != nil {
		return nil, tagError
	}
	ports, portError := demoComponentOverrides(command, "component-port")
	if portError != nil {
		return nil, portError
	}
	paths, pathError := demoComponentOverrides(command, "component-path")
	if pathError != nil {
		return nil, pathError
	}

	positionByName := map[string]int{}
	components := make([]client.DeployApplicationDemoComponent, 0, len(names))
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, withExitCode(exitUsage, errors.New("--component cannot be empty"))
		}
		if _, duplicate := positionByName[name]; duplicate {
			return nil, withExitCode(exitUsage, fmt.Errorf("--component %q is listed more than once", name))
		}
		positionByName[name] = len(components)
		components = append(components, client.DeployApplicationDemoComponent{Name: name})
	}

	for _, override := range []struct {
		flagName string
		values   map[string]string
		apply    func(*client.DeployApplicationDemoComponent, string) error
	}{
		{"component-tag", imageTags, func(component *client.DeployApplicationDemoComponent, value string) error {
			component.ImageTag = &value
			return nil
		}},
		{"component-port", ports, func(component *client.DeployApplicationDemoComponent, value string) error {
			port, conversionError := strconv.Atoi(strings.TrimSpace(value))
			if conversionError != nil {
				return fmt.Errorf("--component-port %q is not a number", value)
			}
			component.ContainerPort = &port
			return nil
		}},
		{"component-path", paths, func(component *client.DeployApplicationDemoComponent, value string) error {
			component.IngressPath = &value
			return nil
		}},
	} {
		for name, value := range override.values {
			position, selected := positionByName[name]
			if !selected {
				return nil, withExitCode(exitUsage, fmt.Errorf(
					"--%s names component %q, which --component does not select; add --component %s",
					override.flagName, name, name))
			}
			if applyError := override.apply(&components[position], value); applyError != nil {
				return nil, withExitCode(exitUsage, applyError)
			}
		}
	}
	if len(components) == 0 {
		return nil, nil
	}
	return components, nil
}

// demoComponentOverrides parses one repeatable NAME=VALUE override flag.
// A repeated name overrides by name, matching --env on `demo config set`.
func demoComponentOverrides(command *cobra.Command, flagName string) (map[string]string, error) {
	entries, _ := command.Flags().GetStringArray(flagName)
	if len(entries) == 0 {
		return nil, nil
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return nil, withExitCode(exitUsage, fmt.Errorf("--%s entries must be NAME=VALUE", flagName))
		}
		values[name] = value
	}
	return values, nil
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

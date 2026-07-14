package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// renderApplicationPayload prints a raw JSON payload from an application
// subresource. The default and -o json formats emit indented JSON; -o yaml
// re-encodes as YAML. Application subresources are deeply nested documents
// (security findings, chart graphs, deployment matrices), so structured
// output is the readable default rather than a bespoke table per endpoint.
func renderApplicationPayload(command *cobra.Command, payload json.RawMessage) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	output := command.OutOrStdout()
	if format == outputYAML {
		var decoded interface{}
		if len(payload) > 0 {
			if unmarshalError := json.Unmarshal(payload, &decoded); unmarshalError != nil {
				return fmt.Errorf("parsing response: %w", unmarshalError)
			}
		}
		return encodeStructured(output, outputYAML, decoded)
	}
	var buffer bytes.Buffer
	if indentError := json.Indent(&buffer, payload, "", "  "); indentError != nil {
		_, _ = output.Write(payload)
		_, _ = fmt.Fprintln(output)
		return nil
	}
	_, _ = output.Write(buffer.Bytes())
	_, _ = fmt.Fprintln(output)
	return nil
}

// applicationSubresourceInvoke fetches a raw JSON payload for a single
// application; it is invoked at run time so the shared apiClient global is
// already wired.
type applicationSubresourceInvoke func(command *cobra.Command, applicationID string) (json.RawMessage, error)

// newApplicationSubresourceCommand builds a `<verb> <application-id>` command
// that fetches a JSON payload and renders it. It covers the reads and the
// bodyless action routes (retry, reconcile, delete, make-public,
// upgrade-workflow) that only need the application id.
func newApplicationSubresourceCommand(name string, short string, invoke applicationSubresourceInvoke) *cobra.Command {
	command := &cobra.Command{
		Use:   name + " <application-id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			payload, invokeError := invoke(command, strings.TrimSpace(arguments[0]))
			if invokeError != nil {
				return invokeError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func registerApplicationResourceCommands(applicationCommand *cobra.Command) {
	applicationCommand.AddCommand(
		newApplicationListCommand(),
		newApplicationSubresourceCommand("get", "Show an application's detail", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationRaw(command.Context(), applicationID)
		}),
		newApplicationJobsCommand(),
		newApplicationSubresourceCommand("retry", "Re-trigger a failed application's setup", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.RetryApplication(command.Context(), applicationID)
		}),
		newApplicationSubresourceCommand("reconcile", "Request an application refresh", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.ReconcileApplication(command.Context(), applicationID)
		}),
		newApplicationSubresourceCommand("delete", "Delete an application", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.DeleteApplication(command.Context(), applicationID)
		}),
		newApplicationDeployCommand(),
		newApplicationSubresourceCommand("deployments", "List an application's cluster deployments", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationDeployments(command.Context(), applicationID)
		}),
		newApplicationSubresourceCommand("installations", "List an application's installation intents", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationInstallations(command.Context(), applicationID)
		}),
		newApplicationSubresourceCommand("chart-versions", "List an application's published chart versions", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationChartVersions(command.Context(), applicationID)
		}),
		newApplicationPlatformCommand(),
		newApplicationWorkflowRunsCommand(),
		newApplicationWorkflowRunJobsCommand(),
		newApplicationRerunWorkflowCommand(),
		newApplicationPullRequestReviewsCommand(),
		newApplicationSubresourceCommand("upgrade-workflow", "Add security scanning steps to the build workflow", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.UpgradeApplicationWorkflow(command.Context(), applicationID)
		}),
		newApplicationSubresourceCommand("branches", "List the application repository branches", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationBranches(command.Context(), applicationID)
		}),
		newApplicationSubresourceCommand("branch-files", "List the tracked files on the setup branch", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationBranchFiles(command.Context(), applicationID)
		}),
		newApplicationUpdateFilesCommand(),
		newApplicationSubresourceCommand("publish-readiness", "Report whether the repository can publish to GHCR", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationPublishReadiness(command.Context(), applicationID)
		}),
		newApplicationSubresourceCommand("container-security", "Show container image vulnerability findings", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationContainerSecurity(command.Context(), applicationID)
		}),
		newApplicationSubresourceCommand("code-security", "Show source code security findings", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationCodeSecurity(command.Context(), applicationID)
		}),
		newApplicationSubresourceCommand("package-visibility", "Show the GHCR image and chart package visibility", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.GetApplicationPackageVisibility(command.Context(), applicationID)
		}),
		newApplicationSetPackageVisibilityCommand(),
		newApplicationSubresourceCommand("make-public", "Make the GHCR image and chart packages public", func(command *cobra.Command, applicationID string) (json.RawMessage, error) {
			return apiClient.MakeApplicationPackagesPublic(command.Context(), applicationID)
		}),
	)
	applicationCommand.AddCommand(newApplicationDemoCommand())
}

func newApplicationListCommand() *cobra.Command {
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List applications",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			page, _ := command.Flags().GetInt("page")
			pageSize, _ := command.Flags().GetInt("page-size")
			search, _ := command.Flags().GetString("search")
			payload, listError := apiClient.ListApplicationsRaw(command.Context(), page, pageSize, strings.TrimSpace(search))
			if listError != nil {
				return listError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	listCommand.Flags().Int("page", 0, "Page number (1-based)")
	listCommand.Flags().Int("page-size", 0, "Page size (1-100)")
	listCommand.Flags().String("search", "", "Filter applications by name")
	registerStructuredOutputFlags(listCommand)
	return listCommand
}

func newApplicationJobsCommand() *cobra.Command {
	jobsCommand := &cobra.Command{
		Use:   "jobs <application-id>",
		Short: "List an application's platform jobs",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			page, _ := command.Flags().GetInt("page")
			pageSize, _ := command.Flags().GetInt("page-size")
			payload, jobsError := apiClient.GetApplicationJobs(command.Context(), strings.TrimSpace(arguments[0]), page, pageSize)
			if jobsError != nil {
				return jobsError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	jobsCommand.Flags().Int("page", 0, "Page number (1-based)")
	jobsCommand.Flags().Int("page-size", 0, "Page size (1-100)")
	registerStructuredOutputFlags(jobsCommand)
	return jobsCommand
}

func newApplicationDeployCommand() *cobra.Command {
	deployCommand := &cobra.Command{
		Use:   "deploy <application-id>",
		Short: "Deploy an application to a cluster",
		Long: `Deploy a packaged application to a target cluster.

The cluster is identified by ID. Use --set key=value (repeatable) to pass
deploy inputs declared by the application's chart.`,
		Example: `  ankra application deploy <app-id> --cluster <cluster-id>
  ankra application deploy <app-id> --cluster <cluster-id> --namespace prod --set replicas=3`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			clusterID, _ := command.Flags().GetString("cluster")
			clusterID = strings.TrimSpace(clusterID)
			if clusterID == "" {
				return withExitCode(exitUsage, errors.New("--cluster is required"))
			}
			namespace, _ := command.Flags().GetString("namespace")
			deployMode, _ := command.Flags().GetString("mode")
			deployMode = strings.TrimSpace(deployMode)
			if deployMode != "" && deployMode != "quick" && deployMode != "high_availability" {
				return withExitCode(exitUsage, errors.New("--mode must be quick or high_availability"))
			}
			setValues, _ := command.Flags().GetStringArray("set")
			inputs, parseError := parseKeyValueFlags(setValues)
			if parseError != nil {
				return withExitCode(exitUsage, parseError)
			}
			payload, deployError := apiClient.DeployApplication(command.Context(), strings.TrimSpace(arguments[0]),
				client.DeployApplicationRequest{
					ClusterID:  clusterID,
					Namespace:  strings.TrimSpace(namespace),
					DeployMode: deployMode,
					Inputs:     inputs,
				})
			if deployError != nil {
				return deployError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	deployCommand.Flags().String("cluster", "", "Target cluster ID (required)")
	deployCommand.Flags().String("namespace", "", "Target namespace (defaults to the platform default)")
	deployCommand.Flags().String("mode", "", "Deploy mode: quick or high_availability")
	deployCommand.Flags().StringArray("set", nil, "Deploy input as key=value (repeatable)")
	registerStructuredOutputFlags(deployCommand)
	return deployCommand
}

func newApplicationPlatformCommand() *cobra.Command {
	platformCommand := &cobra.Command{
		Use:   "platform <application-id>",
		Short: "Detect platform operators already present on a target cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			clusterID, _ := command.Flags().GetString("cluster")
			clusterID = strings.TrimSpace(clusterID)
			if clusterID == "" {
				return withExitCode(exitUsage, errors.New("--cluster is required"))
			}
			payload, platformError := apiClient.GetApplicationExistingPlatform(command.Context(), strings.TrimSpace(arguments[0]), clusterID)
			if platformError != nil {
				return platformError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	platformCommand.Flags().String("cluster", "", "Target cluster ID (required)")
	registerStructuredOutputFlags(platformCommand)
	return platformCommand
}

func newApplicationWorkflowRunsCommand() *cobra.Command {
	workflowRunsCommand := &cobra.Command{
		Use:   "workflow-runs <application-id>",
		Short: "List the application's GitHub Actions workflow runs",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			status, _ := command.Flags().GetString("status")
			page, _ := command.Flags().GetInt("page")
			pageSize, _ := command.Flags().GetInt("page-size")
			payload, runsError := apiClient.GetApplicationWorkflowRuns(command.Context(), strings.TrimSpace(arguments[0]),
				strings.TrimSpace(status), page, pageSize)
			if runsError != nil {
				return runsError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	workflowRunsCommand.Flags().String("status", "", "Filter by GitHub run status")
	workflowRunsCommand.Flags().Int("page", 0, "Page number (1-based)")
	workflowRunsCommand.Flags().Int("page-size", 0, "Page size (1-50)")
	registerStructuredOutputFlags(workflowRunsCommand)
	return workflowRunsCommand
}

func newApplicationWorkflowRunJobsCommand() *cobra.Command {
	jobsCommand := &cobra.Command{
		Use:   "workflow-run-jobs <application-id> <run-id>",
		Short: "List the jobs of a workflow run",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			runID, parseError := parseWorkflowRunID(arguments[1])
			if parseError != nil {
				return parseError
			}
			payload, jobsError := apiClient.GetApplicationWorkflowRunJobs(command.Context(), strings.TrimSpace(arguments[0]), runID)
			if jobsError != nil {
				return jobsError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(jobsCommand)
	return jobsCommand
}

func newApplicationRerunWorkflowCommand() *cobra.Command {
	rerunCommand := &cobra.Command{
		Use:   "rerun-workflow <application-id> <run-id>",
		Short: "Re-trigger a failed workflow run",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			runID, parseError := parseWorkflowRunID(arguments[1])
			if parseError != nil {
				return parseError
			}
			payload, rerunError := apiClient.RerunApplicationWorkflowRun(command.Context(), strings.TrimSpace(arguments[0]), runID)
			if rerunError != nil {
				return rerunError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	registerStructuredOutputFlags(rerunCommand)
	return rerunCommand
}

func newApplicationPullRequestReviewsCommand() *cobra.Command {
	reviewsCommand := &cobra.Command{
		Use:   "pull-request-reviews <application-id>",
		Short: "Show the AI reviews of the application's pull requests",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			limit, _ := command.Flags().GetInt("limit")
			payload, reviewsError := apiClient.GetApplicationPullRequestReviews(command.Context(), strings.TrimSpace(arguments[0]), limit)
			if reviewsError != nil {
				return reviewsError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	reviewsCommand.Flags().Int("limit", 0, "Maximum number of reviews (1-20)")
	registerStructuredOutputFlags(reviewsCommand)
	return reviewsCommand
}

func newApplicationSetPackageVisibilityCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set-package-visibility <application-id>",
		Short: "Set the GHCR image or chart package visibility",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			kind, _ := command.Flags().GetString("kind")
			kind = strings.TrimSpace(kind)
			if kind == "" {
				return withExitCode(exitUsage, errors.New("--kind is required"))
			}
			visibility, _ := command.Flags().GetString("visibility")
			visibility = strings.TrimSpace(visibility)
			if visibility == "" {
				return withExitCode(exitUsage, errors.New("--visibility is required"))
			}
			payload, setError := apiClient.SetApplicationPackageVisibility(command.Context(), strings.TrimSpace(arguments[0]),
				client.SetApplicationPackageVisibilityRequest{Kind: kind, Visibility: visibility})
			if setError != nil {
				return setError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	setCommand.Flags().String("kind", "", "Package kind: image or chart (required)")
	setCommand.Flags().String("visibility", "", "Visibility: public or private (required)")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

func newApplicationUpdateFilesCommand() *cobra.Command {
	filesCommand := &cobra.Command{
		Use:   "files <application-id>",
		Short: "Commit generated file changes to the setup pull request",
		Long: `Commit changes to the application's setup pull request.

Each --file maps a repository path to a local file whose contents are
uploaded. Use --delete to remove a tracked path.`,
		Example: `  ankra application files <app-id> --file Dockerfile=./Dockerfile --message "Update image"
  ankra application files <app-id> --delete .ankra/manifests/app.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			fileMappings, _ := command.Flags().GetStringArray("file")
			deletedPaths, _ := command.Flags().GetStringArray("delete")
			if len(fileMappings) == 0 && len(deletedPaths) == 0 {
				return withExitCode(exitUsage, errors.New("pass at least one --file or --delete"))
			}
			files, parseError := parseApplicationFileMappings(fileMappings)
			if parseError != nil {
				return parseError
			}
			commitMessage, _ := command.Flags().GetString("message")
			payload, filesError := apiClient.UpdateApplicationFiles(command.Context(), strings.TrimSpace(arguments[0]),
				client.UpdateApplicationFilesRequest{
					Files:         files,
					DeletedPaths:  deletedPaths,
					CommitMessage: strings.TrimSpace(commitMessage),
				})
			if filesError != nil {
				return filesError
			}
			return renderApplicationPayload(command, payload)
		},
	}
	filesCommand.Flags().StringArray("file", nil, "Repository path mapped to a local file as path=local-file (repeatable)")
	filesCommand.Flags().StringArray("delete", nil, "Repository path to delete (repeatable)")
	filesCommand.Flags().String("message", "", "Commit message")
	registerStructuredOutputFlags(filesCommand)
	return filesCommand
}

func parseWorkflowRunID(rawRunID string) (int64, error) {
	runID, parseError := strconv.ParseInt(strings.TrimSpace(rawRunID), 10, 64)
	if parseError != nil {
		return 0, withExitCode(exitUsage, fmt.Errorf("run id %q must be an integer", rawRunID))
	}
	return runID, nil
}

func parseKeyValueFlags(entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		separatorIndex := strings.Index(entry, "=")
		if separatorIndex <= 0 {
			return nil, fmt.Errorf("invalid --set %q; expected key=value", entry)
		}
		key := strings.TrimSpace(entry[:separatorIndex])
		if key == "" {
			return nil, fmt.Errorf("invalid --set %q; key cannot be empty", entry)
		}
		values[key] = entry[separatorIndex+1:]
	}
	return values, nil
}

func parseApplicationFileMappings(mappings []string) ([]client.ApplicationFileUpdate, error) {
	files := make([]client.ApplicationFileUpdate, 0, len(mappings))
	for _, mapping := range mappings {
		separatorIndex := strings.Index(mapping, "=")
		if separatorIndex <= 0 {
			return nil, withExitCode(exitUsage, fmt.Errorf("invalid --file %q; expected path=local-file", mapping))
		}
		repositoryPath := strings.TrimSpace(mapping[:separatorIndex])
		localPath := strings.TrimSpace(mapping[separatorIndex+1:])
		if repositoryPath == "" || localPath == "" {
			return nil, withExitCode(exitUsage, fmt.Errorf("invalid --file %q; expected path=local-file", mapping))
		}
		contents, readError := readApplicationFile(localPath)
		if readError != nil {
			return nil, readError
		}
		files = append(files, client.ApplicationFileUpdate{Path: repositoryPath, Content: string(contents)})
	}
	return files, nil
}

func readApplicationFile(localPath string) ([]byte, error) {
	contents, readError := os.ReadFile(localPath)
	if readError != nil {
		if os.IsNotExist(readError) {
			return nil, withExitCode(exitNotFound, fmt.Errorf("local file %q does not exist", localPath))
		}
		return nil, fmt.Errorf("reading local file %q: %w", localPath, readError)
	}
	return contents, nil
}

package cmd

import (
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func newDeploymentsCommand() *cobra.Command {
	deploymentsCommand := &cobra.Command{
		Use:     "deployments",
		Aliases: []string{"deployment"},
		Short:   "Inspect releases deployed to host deploy targets",
		Long: `Inspect deployments: each 'kind: deploy' pipeline stage that ran releases
one published artefact digest to every active host target of its environment,
and each deployment records how every host fared.`,
	}
	deploymentsCommand.AddCommand(newDeploymentsListCommand(), newDeploymentsGetCommand())
	return deploymentsCommand
}

func newDeploymentsListCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List deployments, newest first",
		Example: `  ankra deployments list --environment production
  ankra deployments list --repository <pipeline-repository-id> -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			environmentName, _ := command.Flags().GetString("environment")
			repositoryID, _ := command.Flags().GetString("repository")
			cursor, _ := command.Flags().GetString("cursor")
			limit, _ := command.Flags().GetInt("limit")
			environmentName = strings.TrimSpace(environmentName)
			repositoryID = strings.TrimSpace(repositoryID)
			if environmentName != "" {
				if validationError := validateEnvironmentName(environmentName); validationError != nil {
					return validationError
				}
			}
			if repositoryID != "" && !looksLikeUUID(repositoryID) {
				return withExitCode(exitUsage, fmt.Errorf(
					"--repository %q must be the pipeline repository id - see 'ankra pipeline repositories list'", repositoryID))
			}
			if limit < 0 {
				return withExitCode(exitUsage, fmt.Errorf("--limit %d must not be negative", limit))
			}
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			page, listError := apiClient.ListDeployments(command.Context(), client.ListDeploymentsOptions{
				Environment:  environmentName,
				RepositoryID: repositoryID,
				Cursor:       cursor,
				Limit:        limit,
			})
			if listError != nil {
				return deployLaneError("listing deployments", listError)
			}
			if page.Deployments == nil {
				page.Deployments = []client.Deployment{}
			}
			if rendered, renderError := renderStructured(command, page); rendered || renderError != nil {
				return renderError
			}
			if len(page.Deployments) == 0 {
				_, _ = fmt.Fprintln(command.OutOrStdout(), "No deployments found.")
				return nil
			}
			writeDeploymentTable(command.OutOrStdout(), page.Deployments)
			if page.NextCursor != nil && *page.NextCursor != "" {
				_, _ = fmt.Fprintf(command.ErrOrStderr(), "More deployments: re-run with --cursor %s\n", *page.NextCursor)
			}
			return nil
		},
	}
	command.Flags().String("environment", "", "Only list deployments to this environment")
	command.Flags().String("repository", "", "Only list deployments from this pipeline repository id")
	command.Flags().String("cursor", "", "Continue from the cursor a previous page printed")
	command.Flags().Int("limit", 0, "Deployments per page (default: the platform's page size)")
	registerStructuredOutputFlags(command)
	return command
}

func writeDeploymentTable(out io.Writer, deployments []client.Deployment) {
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "Environment", "Release", "Digest", "State", "Targets", "Started"})
	for _, deployment := range deployments {
		writer.AppendRow(table.Row{
			deployment.ID,
			deployment.Environment,
			deployment.ReleaseName,
			shortDigest(deployment.ArtifactDigest),
			deployment.State,
			fmt.Sprintf("%d/%d ok, %d failed", deployment.SucceededCount, deployment.TargetCount, deployment.FailedCount),
			formatOptionalTimestamp(deployment.StartedAt),
		})
	}
	writer.Render()
}

// shortDigest keeps a sha256 digest readable in a table column.
func shortDigest(digest string) string {
	const shownHexCharacters = 12
	hexPart, hasPrefix := strings.CutPrefix(digest, "sha256:")
	if !hasPrefix || len(hexPart) <= shownHexCharacters {
		return digest
	}
	return "sha256:" + hexPart[:shownHexCharacters]
}

func newDeploymentsGetCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "get <id>",
		Short: "Show a deployment and how each host target fared",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if _, formatError := structuredFormatFromFlags(command); formatError != nil {
				return formatError
			}
			deployment, getError := apiClient.GetDeployment(command.Context(), arguments[0])
			if getError != nil {
				return deployLaneError("getting deployment "+arguments[0], getError)
			}
			if rendered, renderError := renderStructured(command, deployment); rendered || renderError != nil {
				return renderError
			}
			printDeployment(command.OutOrStdout(), deployment)
			return nil
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func printDeployment(out io.Writer, deployment *client.Deployment) {
	_, _ = fmt.Fprintln(out, "Deployment:")
	_, _ = fmt.Fprintf(out, "  ID:           %s\n", deployment.ID)
	_, _ = fmt.Fprintf(out, "  Environment:  %s\n", deployment.Environment)
	_, _ = fmt.Fprintf(out, "  Release:      %s\n", deployment.ReleaseName)
	_, _ = fmt.Fprintf(out, "  State:        %s\n", deployment.State)
	repository := optionalText(deployment.Repository)
	if repository == "-" {
		repository = optionalText(deployment.RepositoryID)
	}
	_, _ = fmt.Fprintf(out, "  Repository:   %s\n", repository)
	_, _ = fmt.Fprintf(out, "  Commit:       %s\n", optionalText(deployment.CommitSHA))
	_, _ = fmt.Fprintf(out, "  Artefact:     %s@%s\n", deployment.ArtifactRepository, deployment.ArtifactDigest)
	_, _ = fmt.Fprintf(out, "  Tag:          %s\n", optionalText(deployment.ArtifactTag))
	_, _ = fmt.Fprintf(out, "  Signed with:  %s\n", optionalText(deployment.SignatureKeyID))
	_, _ = fmt.Fprintf(out, "  Pipeline run: %s\n", optionalText(deployment.PipelineRunID))
	_, _ = fmt.Fprintf(out, "  Requested by: %s\n", optionalText(deployment.RequestedBy))
	_, _ = fmt.Fprintf(out, "  Started:      %s\n", formatOptionalTimestamp(deployment.StartedAt))
	_, _ = fmt.Fprintf(out, "  Finished:     %s\n", formatOptionalTimestamp(deployment.FinishedAt))
	_, _ = fmt.Fprintf(out, "  Targets:      %d, %d succeeded, %d failed\n",
		deployment.TargetCount, deployment.SucceededCount, deployment.FailedCount)
	if deployment.ErrorClass != nil || deployment.ErrorMessage != nil {
		_, _ = fmt.Fprintf(out, "  Error:        %s: %s\n", optionalText(deployment.ErrorClass), optionalText(deployment.ErrorMessage))
	}
	if len(deployment.Targets) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out)
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Host Target", "Status", "Attempt", "Running", "Previous", "Error", "Finished"})
	for _, target := range deployment.Targets {
		hostTarget := target.HostTargetName
		if hostTarget == "" {
			hostTarget = target.HostTargetID
		}
		errorText := "-"
		if target.ErrorClass != nil || target.ErrorMessage != nil {
			errorText = strings.TrimSpace(optionalText(target.ErrorClass) + ": " + optionalText(target.ErrorMessage))
		}
		writer.AppendRow(table.Row{
			hostTarget,
			target.Status,
			target.Attempt,
			shortDigest(optionalText(target.RunningDigest)),
			shortDigest(optionalText(target.PreviousDigest)),
			errorText,
			formatOptionalTimestamp(target.FinishedAt),
		})
	}
	writer.Render()
}

func init() {
	rootCmd.AddCommand(newDeploymentsCommand())
}

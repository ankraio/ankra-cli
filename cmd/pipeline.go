package cmd

// The `ankra pipeline` family (ankra-vn0bd.2.8, WS-B item B8): the CLI
// surface over go/internal/pipelineapi's four route trees. Every leaf command
// here addresses one pipeline through a PipelineSelector - a repository or
// the application it is linked to - resolved from the shared --application /
// --repository flag pair. `ankra application pipeline …`
// (cmd/application_pipeline.go) is the by-application twin: it forces the
// selector from a leading <application-id> argument instead of a flag, and
// otherwise calls the exact same run* functions this file and its siblings
// define.

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

func newPipelineCommand() *cobra.Command {
	pipelineCommand := &cobra.Command{
		Use:     "pipeline",
		Aliases: []string{"pipelines"},
		Short:   "Manage Ankra Pipelines",
		Long: `Manage Ankra Pipelines: in-cluster CI/CD runs, the pipeline definition of
record, and its cron schedules.

Every command addresses one pipeline with exactly one of:

  --application <name-or-id>   the application the pipeline builds and deploys
  --repository <repository-id> the pipeline repository directly

There is no lookup route yet for a repository by "owner/name" - only by-id and
by-application addressing exist - so --repository takes the repository's id
(for example, the "repository.id" field 'ankra pipeline definition get'
returns). Most invocations want --application instead.

A run is addressed by its id (the RUN ID column, not the "run_id" field a
JSON/YAML response also carries - that second field is the cross-lifecycle
umbrella run this pipeline run belongs to, useful for correlating with
'ankra cluster operations', but not what these commands accept).`,
	}
	pipelineCommand.AddCommand(
		newPipelineRunCommand(),
		newPipelineListCommand(),
		newPipelineGetCommand(),
		newPipelineCancelCommand(),
		newPipelineRerunCommand(),
		newPipelineLogsCommand(),
		newPipelineArtifactsCommand(),
		newPipelineFindingsCommand(),
		newPipelineValidateCommand(),
		newPipelineDefinitionCommand(),
		newPipelineSchedulesCommand(),
	)
	return pipelineCommand
}

func init() {
	rootCmd.AddCommand(newPipelineCommand())
}

// registerPipelineSelectorFlags adds the shared --application / --repository
// pair to a pipeline command.
func registerPipelineSelectorFlags(command *cobra.Command) {
	command.Flags().String("application", "", "Application name or id whose pipeline to act on")
	command.Flags().String("repository", "", "Pipeline repository id to act on (mutually exclusive with --application)")
}

// resolvePipelineSelector reads --application / --repository and resolves
// them into the PipelineSelector every pipeline client call takes. Exactly
// one of the two must be given.
func resolvePipelineSelector(command *cobra.Command) (client.PipelineSelector, error) {
	applicationReference, _ := command.Flags().GetString("application")
	repositoryID, _ := command.Flags().GetString("repository")
	applicationReference = strings.TrimSpace(applicationReference)
	repositoryID = strings.TrimSpace(repositoryID)

	switch {
	case applicationReference != "" && repositoryID != "":
		return client.PipelineSelector{}, withExitCode(exitUsage,
			errors.New("--application and --repository are mutually exclusive"))
	case applicationReference != "":
		applicationID, resolveError := resolveApplicationID(command.Context(), apiClient, applicationReference)
		if resolveError != nil {
			return client.PipelineSelector{}, resolveError
		}
		return client.PipelineSelector{ApplicationID: applicationID}, nil
	case repositoryID != "":
		if !looksLikeUUID(repositoryID) {
			return client.PipelineSelector{}, withExitCode(exitUsage, fmt.Errorf(
				"--repository %q must be the pipeline repository id - there is no lookup by owner/name yet, "+
					"so pass the id from 'ankra pipeline definition get --application <name>' "+
					"(its repository.id field), or use --application instead", repositoryID))
		}
		return client.PipelineSelector{RepositoryID: repositoryID}, nil
	default:
		return client.PipelineSelector{}, withExitCode(exitUsage,
			errors.New("one of --application or --repository is required"))
	}
}

// parsePipelineInputFlags parses repeated --input key=value flags into the
// dispatch input map. A missing '=' or a repeated key is a usage error rather
// than a silently dropped or overwritten input.
func parsePipelineInputFlags(rawInputs []string) (map[string]string, error) {
	if len(rawInputs) == 0 {
		return nil, nil
	}
	inputs := make(map[string]string, len(rawInputs))
	for _, rawInput := range rawInputs {
		key, value, found := strings.Cut(rawInput, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, withExitCode(exitUsage, fmt.Errorf("--input %q is not key=value", rawInput))
		}
		if _, isDuplicate := inputs[key]; isDuplicate {
			return nil, withExitCode(exitUsage,
				fmt.Errorf("--input %s given more than once: name each input at most once", key))
		}
		inputs[key] = value
	}
	return inputs, nil
}

// pipelineOutcomeLabel renders a run or step's status/outcome pair as one
// word for a table cell: the outcome once the work has concluded, the status
// while it has not.
func pipelineOutcomeLabel(status string, outcome *string) string {
	if outcome != nil && *outcome != "" {
		return *outcome
	}
	return status
}

func pipelineOptionalString(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

// pipelineShortSHA renders a commit for a table cell: short enough to scan,
// long enough that two commits on the same branch stay distinguishable.
func pipelineShortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

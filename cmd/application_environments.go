package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The environments of an application and the branches each one follows. A
// branch rule is a list of glob patterns: `*` stays within one path segment,
// `**` crosses `/`, `!pattern` removes what earlier patterns matched, the last
// match wins, and a list that starts with `!` means every branch except those.
// An environment without a rule follows the application's tracked branch.
//
// Both commands read and write the auto-deploy resource, which carries the
// per-environment switch and rule next to the application-wide one.

const environmentsBranchRuleHelp = `A branch rule is a list of glob patterns, as in a CI 'branches' filter:
'*' matches within one path segment, '**' also crosses '/', '!pattern'
removes what earlier patterns matched, the last match wins, and a list that
starts with '!' means every branch except those. An environment without a
rule follows the application's tracked branch.`

type applicationAutoDeployEnvironment struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	Enabled   bool     `json:"enabled"`
	Inherited bool     `json:"inherited"`
	Branches  []string `json:"branches"`
}

type applicationAutoDeployState struct {
	Enabled      bool                               `json:"enabled"`
	Branch       string                             `json:"branch"`
	Environments []applicationAutoDeployEnvironment `json:"environments"`
}

func newApplicationEnvironmentsCommand() *cobra.Command {
	environmentsCommand := &cobra.Command{
		Use:     "environments <application>",
		Aliases: []string{"environment", "envs"},
		Short:   "List an application's environments and the branches each one follows",
		Long: `List an application's environments and the branches each one follows.

FOLLOWS shows the environment's branch rule, '<branch> (tracked)' when it has
no rule and follows the application's tracked branch, or 'manual' when pushes
do not deploy to it.

` + environmentsBranchRuleHelp,
		Example: `  ankra application environments my-app
  ankra application environments my-app -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			payload, getError := apiClient.GetApplicationAutoDeploy(command.Context(), applicationID)
			if getError != nil {
				return getError
			}
			return renderApplicationEnvironments(command, format, payload)
		},
	}
	registerStructuredOutputFlags(environmentsCommand)
	environmentsCommand.AddCommand(newApplicationEnvironmentsSetCommand())
	return environmentsCommand
}

func newApplicationEnvironmentsSetCommand() *cobra.Command {
	setCommand := &cobra.Command{
		Use:   "set <application> <environment>",
		Short: "Set the branches an environment follows, or whether pushes deploy to it",
		Long: `Set the branches an environment follows, or whether pushes deploy to it.

The environment is named by its name (case-insensitive) or its id, as listed by
'ankra application environments'. --branches sets the rule, --clear (or
--branches "") removes it so the environment follows the tracked branch again,
and --enable or --disable turns deploying on push on or off for the
environment. Without --enable or --disable the environment keeps its current
setting.

Changing a rule never deploys by itself: the next push to a matching branch
does.

` + environmentsBranchRuleHelp,
		Example: `  ankra application environments set my-app production --branches main
  ankra application environments set my-app development --branches develop
  ankra application environments set my-app development --branches '**,!main'
  ankra application environments set my-app staging --branches 'release/*'
  ankra application environments set my-app staging --clear
  ankra application environments set my-app production --disable`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			flags := command.Flags()
			hasBranches := flags.Changed("branches")
			shouldClear, _ := flags.GetBool("clear")
			shouldEnable, _ := flags.GetBool("enable")
			shouldDisable, _ := flags.GetBool("disable")
			if hasBranches && shouldClear {
				return withExitCode(exitUsage, errors.New("--branches and --clear cannot be combined"))
			}
			if shouldEnable && shouldDisable {
				return withExitCode(exitUsage, errors.New("--enable and --disable cannot be combined"))
			}
			if !hasBranches && !shouldClear && !shouldEnable && !shouldDisable {
				return withExitCode(exitUsage, errors.New("nothing to change: pass --branches, --clear, --enable or --disable"))
			}
			environmentArgument := strings.TrimSpace(arguments[1])
			if environmentArgument == "" {
				return withExitCode(exitUsage, errors.New("environment is empty: pass its name or id"))
			}

			applicationID, resolveError := resolveApplicationArgument(command, arguments)
			if resolveError != nil {
				return resolveError
			}
			currentPayload, getError := apiClient.GetApplicationAutoDeploy(command.Context(), applicationID)
			if getError != nil {
				return getError
			}
			var current applicationAutoDeployState
			if decodeError := json.Unmarshal(currentPayload, &current); decodeError != nil {
				return fmt.Errorf("parsing auto-deploy response: %w", decodeError)
			}
			environment, matchError := matchApplicationEnvironment(current.Environments, environmentArgument)
			if matchError != nil {
				return matchError
			}

			autoDeployRequest := client.SetApplicationAutoDeployRequest{
				Enabled:       environment.Enabled,
				EnvironmentID: environment.ID,
			}
			if shouldEnable {
				autoDeployRequest.Enabled = true
			}
			if shouldDisable {
				autoDeployRequest.Enabled = false
			}
			if shouldClear {
				autoDeployRequest.Branches = &[]string{}
			}
			if hasBranches {
				branchesValue, _ := flags.GetString("branches")
				branches := splitBranchPatterns(branchesValue)
				autoDeployRequest.Branches = &branches
			}
			payload, setError := apiClient.UpdateApplicationAutoDeploy(command.Context(), applicationID, autoDeployRequest)
			if setError != nil {
				return setError
			}
			if format == outputDefault && autoDeployRequest.Branches != nil {
				_, _ = fmt.Fprintln(command.ErrOrStderr(),
					"Rule saved. It does not deploy by itself: the next push to a matching branch does.")
			}
			return renderApplicationEnvironments(command, format, payload)
		},
	}
	setCommand.Flags().String("branches", "", `Comma-separated branch patterns, e.g. main or 'release/*' ("" removes the rule)`)
	setCommand.Flags().Bool("clear", false, "Remove the branch rule so the environment follows the tracked branch")
	setCommand.Flags().Bool("enable", false, "Deploy pushes to this environment")
	setCommand.Flags().Bool("disable", false, "Stop deploying pushes to this environment")
	registerStructuredOutputFlags(setCommand)
	return setCommand
}

// splitBranchPatterns turns a comma-separated flag value into its patterns,
// dropping blanks so that an empty value is an empty rule.
func splitBranchPatterns(value string) []string {
	patterns := []string{}
	for _, pattern := range strings.Split(value, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	return patterns
}

// matchApplicationEnvironment finds an environment by exact id, then by name
// regardless of case, and refuses a name that more than one environment has.
func matchApplicationEnvironment(environments []applicationAutoDeployEnvironment, reference string) (applicationAutoDeployEnvironment, error) {
	for _, environment := range environments {
		if environment.ID == reference {
			return environment, nil
		}
	}
	var matches []applicationAutoDeployEnvironment
	for _, environment := range environments {
		if strings.EqualFold(environment.Name, reference) {
			matches = append(matches, environment)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		names := make([]string, 0, len(environments))
		for _, environment := range environments {
			names = append(names, environment.Name)
		}
		if len(names) == 0 {
			return applicationAutoDeployEnvironment{}, withExitCode(exitNotFound,
				fmt.Errorf("environment %q not found: the application has no environments yet", reference))
		}
		return applicationAutoDeployEnvironment{}, withExitCode(exitNotFound,
			fmt.Errorf("environment %q not found; the application has: %s", reference, strings.Join(names, ", ")))
	default:
		identifiers := make([]string, 0, len(matches))
		for _, environment := range matches {
			identifiers = append(identifiers, environment.ID)
		}
		return applicationAutoDeployEnvironment{}, withExitCode(exitUsage,
			fmt.Errorf("environment name %q is ambiguous; pass one of these ids instead: %s",
				reference, strings.Join(identifiers, ", ")))
	}
}

// renderApplicationEnvironments prints the environments of an auto-deploy
// payload: the array itself for -o json|yaml, a table otherwise.
func renderApplicationEnvironments(command *cobra.Command, format outputFormat, payload json.RawMessage) error {
	output := command.OutOrStdout()
	if format != outputDefault {
		var envelope struct {
			Environments []any `json:"environments"`
		}
		if decodeError := json.Unmarshal(payload, &envelope); decodeError != nil {
			return fmt.Errorf("parsing auto-deploy response: %w", decodeError)
		}
		if envelope.Environments == nil {
			envelope.Environments = []any{}
		}
		return encodeStructured(output, format, envelope.Environments)
	}
	var state applicationAutoDeployState
	if decodeError := json.Unmarshal(payload, &state); decodeError != nil {
		return fmt.Errorf("parsing auto-deploy response: %w", decodeError)
	}
	return writeApplicationEnvironmentsTable(output, state)
}

func writeApplicationEnvironmentsTable(output io.Writer, state applicationAutoDeployState) error {
	if len(state.Environments) == 0 {
		_, writeError := fmt.Fprintln(output, "No environments. Deploy with --environment <name> to create one.")
		return writeError
	}
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "NAME\tROLE\tFOLLOWS\tENVIRONMENT ID")
	for _, environment := range state.Environments {
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n",
			environment.Name, valueOrDashString(environment.Role),
			environmentFollows(environment, state.Branch), environment.ID)
	}
	return writer.Flush()
}

// environmentFollows is the FOLLOWS cell: the rule, the tracked branch when
// there is no rule, or manual when pushes do not deploy to the environment.
func environmentFollows(environment applicationAutoDeployEnvironment, trackedBranch string) string {
	if !environment.Enabled {
		return "manual"
	}
	if len(environment.Branches) > 0 {
		return strings.Join(environment.Branches, ",")
	}
	if trackedBranch == "" {
		return "(tracked branch)"
	}
	return trackedBranch + " (tracked)"
}

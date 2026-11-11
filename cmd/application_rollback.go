package cmd

// `ankra application rollback` and `ankra application rollbacks`
// (ankra-tv2pwo): put a previously published image tag back on an
// application's deployment on one cluster, through the routes the portal's
// rollback confirmation uses. Without --to the command only reads: what the
// deployment runs, the builds it could go back to, and what a rollback leaves
// alone. A rollback is never started without a tag the caller named.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

type rollbackTargetOption struct {
	ImageTag      string `json:"image_tag"`
	PushedAt      string `json:"pushed_at"`
	IsCurrent     bool   `json:"is_current"`
	IsLatestBuild bool   `json:"is_latest_build"`
}

type rollbackRecord struct {
	ID               string   `json:"id"`
	TargetKind       string   `json:"target_kind"`
	ImageTag         string   `json:"image_tag"`
	PreviousImageTag *string  `json:"previous_image_tag"`
	Status           string   `json:"status"`
	Message          *string  `json:"message"`
	RequestedBy      string   `json:"requested_by"`
	RequestedAt      string   `json:"requested_at"`
	NotReverted      []string `json:"not_reverted"`
}

type rollbackTargets struct {
	TargetKind          string                 `json:"target_kind"`
	UnavailableReason   *string                `json:"unavailable_reason"`
	StackName           *string                `json:"stack_name"`
	Namespace           *string                `json:"namespace"`
	CurrentImageTag     *string                `json:"current_image_tag"`
	Targets             []rollbackTargetOption `json:"targets"`
	TargetsReason       *string                `json:"targets_reason"`
	LatestBuildImageTag *string                `json:"latest_build_image_tag"`
	AutoDeployEnabled   bool                   `json:"auto_deploy_enabled"`
	NotReverted         []string               `json:"not_reverted"`
	LastRollback        *rollbackRecord        `json:"last_rollback"`
}

// rollbackStatusFailed is the record status of a rollback the platform
// started and could not finish.
const rollbackStatusFailed = "failed"

func newApplicationRollbackCommand() *cobra.Command {
	rollbackCommand := &cobra.Command{
		Use:   "rollback <application> --cluster <cluster> [--to <image-tag>]",
		Short: "Show what a deployment can be rolled back to, or roll it back to a tag",
		Long: `Roll an application's deployment on one cluster back to an image tag it
published earlier.

Without --to the command changes nothing: it prints the tag the deployment
runs, the newest build, the tags still in the registry it could go back to,
and what a rollback does not undo (a migrated schema, data written since,
changed secrets, stack edits).

With --to <image-tag> it shows the same picture, asks, and then puts that tag
back. The platform confirms the tag is in the application's registry first,
and holds the rollback until a newer build is observed, so push-to-deploy does
not undo it. A rollback is never started without --to: there is no "previous"
default.

The cluster is named by its name or id. It needs the applications.deploy
permission; a scoped token must carry that scope.

Exit codes: 0 when the rollback was recorded or the deployment already runs
the tag, 8 when the deployment cannot be rolled back (the reason is printed),
4 when you decline.`,
		Example: `  ankra application rollback shop --cluster production
  ankra application rollback shop --cluster production --to sha-1a2b3c4
  ankra application rollback shop --cluster production --to sha-1a2b3c4 --yes -o json`,
		Args: cobra.ExactArgs(1),
		RunE: runApplicationRollback,
	}
	rollbackCommand.Flags().String("cluster", "", "Cluster the deployment runs on, by name or id (required)")
	rollbackCommand.Flags().String("to", "", "Image tag to roll back to; without it the command only lists the targets")
	rollbackCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(rollbackCommand)
	return rollbackCommand
}

func newApplicationRollbacksCommand() *cobra.Command {
	rollbacksCommand := &cobra.Command{
		Use:   "rollbacks <application> --cluster <cluster>",
		Short: "List the rollbacks recorded for a deployment",
		Long: `List the rollbacks recorded for an application's deployment on one cluster,
newest first. It is what explains a deployment running an older build than the
newest one the platform observed.`,
		Example: `  ankra application rollbacks shop --cluster production
  ankra application rollbacks shop --cluster production -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			applicationID, clusterID, _, targetError := resolveRollbackDeployment(command, arguments)
			if targetError != nil {
				return targetError
			}
			payload, listError := apiClient.ListApplicationRollbacks(command.Context(), applicationID, clusterID)
			if listError != nil {
				return listError
			}
			if format != outputDefault {
				_, renderError := renderStructured(command, json.RawMessage(payload))
				return renderError
			}
			var history struct {
				Rollbacks []rollbackRecord `json:"rollbacks"`
			}
			if decodeError := json.Unmarshal(payload, &history); decodeError != nil {
				return fmt.Errorf("parsing the rollbacks: %w", decodeError)
			}
			printRollbackHistory(command.OutOrStdout(), history.Rollbacks)
			return nil
		},
	}
	rollbacksCommand.Flags().String("cluster", "", "Cluster the deployment runs on, by name or id (required)")
	registerStructuredOutputFlags(rollbacksCommand)
	return rollbacksCommand
}

// resolveRollbackDeployment resolves the application argument and the
// --cluster flag, and answers the cluster as the caller named it for the
// messages.
func resolveRollbackDeployment(command *cobra.Command, arguments []string) (string, string, string, error) {
	clusterReference := strings.TrimSpace(mustFlagString(command, "cluster"))
	if clusterReference == "" {
		return "", "", "", withExitCode(exitUsage, errors.New("--cluster is required: the cluster the deployment runs on"))
	}
	applicationID, resolveError := resolveApplicationArgument(command, arguments)
	if resolveError != nil {
		return "", "", "", resolveError
	}
	clusterID, clusterError := resolveClusterArg(clusterReference)
	if clusterError != nil {
		return "", "", "", clusterError
	}
	return applicationID, clusterID, clusterReference, nil
}

func runApplicationRollback(command *cobra.Command, arguments []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	imageTag := strings.TrimSpace(mustFlagString(command, "to"))
	isWrite := command.Flags().Changed("to")
	if isWrite && imageTag == "" {
		return withExitCode(exitUsage, errors.New("--to needs an image tag: run without --to to list the tags this deployment can go back to"))
	}
	skipConfirmation, _ := command.Flags().GetBool("yes")
	if isWrite && format != outputDefault && !skipConfirmation {
		return withExitCode(exitUsage, errors.New("-o with --to needs --yes: structured output cannot ask for confirmation"))
	}
	applicationID, clusterID, clusterReference, targetError := resolveRollbackDeployment(command, arguments)
	if targetError != nil {
		return targetError
	}
	applicationName := strings.TrimSpace(arguments[0])

	targetsPayload, targetsError := apiClient.GetApplicationRollbackTargets(command.Context(), applicationID, clusterID)
	if targetsError != nil {
		return targetsError
	}
	var targets rollbackTargets
	if decodeError := json.Unmarshal(targetsPayload, &targets); decodeError != nil {
		return fmt.Errorf("parsing the rollback targets: %w", decodeError)
	}
	isAlreadyCurrent := isWrite && rollbackOptionalText(targets.CurrentImageTag) == imageTag
	isBlocked := isWrite && rollbackOptionalText(targets.UnavailableReason) != ""

	if format != outputDefault && (!isWrite || isAlreadyCurrent || isBlocked) {
		if _, renderError := renderStructured(command, json.RawMessage(targetsPayload)); renderError != nil {
			return renderError
		}
	}
	if format == outputDefault {
		printRollbackTargets(command.OutOrStdout(), targets, applicationName, clusterReference, !isWrite)
	}
	switch {
	case !isWrite:
		return nil
	case isBlocked:
		return withExitCode(exitBlocked, fmt.Errorf("%s on %s cannot be rolled back: %s",
			applicationName, clusterReference, *targets.UnavailableReason))
	case isAlreadyCurrent:
		if format == outputDefault {
			_, _ = fmt.Fprintf(command.OutOrStdout(), "%s already runs %s on %s; nothing was rolled back.\n",
				applicationName, imageTag, clusterReference)
		}
		return nil
	}

	message := fmt.Sprintf("Roll %s on %s back to %s? This deploys now. [y/N] ", applicationName, clusterReference, imageTag)
	if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), message, skipConfirmation); confirmError != nil {
		return confirmError
	}
	recordPayload, startError := apiClient.StartApplicationRollback(command.Context(), applicationID, clusterID, imageTag)
	if startError != nil {
		return startError
	}
	var record rollbackRecord
	if decodeError := json.Unmarshal(recordPayload, &record); decodeError != nil {
		return fmt.Errorf("parsing the rollback record: %w", decodeError)
	}
	if format != outputDefault {
		if _, renderError := renderStructured(command, json.RawMessage(recordPayload)); renderError != nil {
			return renderError
		}
	} else {
		printRollbackRecord(command.OutOrStdout(), record, applicationName, clusterReference, targets.AutoDeployEnabled)
	}
	if record.Status == rollbackStatusFailed {
		reason := rollbackOptionalText(record.Message)
		if reason == "" {
			reason = "the platform did not say why"
		}
		return fmt.Errorf("the rollback of %s on %s to %s failed: %s", applicationName, clusterReference, imageTag, reason)
	}
	return nil
}

func rollbackOptionalText(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func rollbackTextOr(value *string, fallback string) string {
	if text := rollbackOptionalText(value); text != "" {
		return text
	}
	return fallback
}

// printRollbackTargets writes what the deployment runs, what it could go back
// to and what a rollback leaves alone. withHint adds the command that starts
// one, for the read that was not already asked to.
func printRollbackTargets(out io.Writer, targets rollbackTargets, applicationName string, clusterReference string, withHint bool) {
	_, _ = fmt.Fprintf(out, "Application:    %s\n", applicationName)
	_, _ = fmt.Fprintf(out, "Cluster:        %s\n", clusterReference)
	deployment := targets.TargetKind
	if stackName := rollbackOptionalText(targets.StackName); stackName != "" {
		deployment += " " + stackName
	}
	if namespace := rollbackOptionalText(targets.Namespace); namespace != "" {
		deployment += " in namespace " + namespace
	}
	_, _ = fmt.Fprintf(out, "Deployment:     %s\n", deployment)
	if reason := rollbackOptionalText(targets.UnavailableReason); reason != "" {
		_, _ = fmt.Fprintf(out, "Cannot be rolled back: %s\n", reason)
		return
	}
	_, _ = fmt.Fprintf(out, "Running:        %s\n", rollbackTextOr(targets.CurrentImageTag, "unknown"))
	_, _ = fmt.Fprintf(out, "Latest build:   %s\n", rollbackTextOr(targets.LatestBuildImageTag, "unknown"))
	pushToDeploy := "off"
	if targets.AutoDeployEnabled {
		pushToDeploy = "on (a rollback holds until a newer build is observed)"
	}
	_, _ = fmt.Fprintf(out, "Push-to-deploy: %s\n", pushToDeploy)
	if last := targets.LastRollback; last != nil {
		_, _ = fmt.Fprintf(out, "Last rollback:  to %s at %s by %s (%s)\n", last.ImageTag, last.RequestedAt, last.RequestedBy, last.Status)
	}

	_, _ = fmt.Fprintln(out)
	if len(targets.Targets) == 0 {
		_, _ = fmt.Fprintf(out, "No rollback targets: %s\n",
			rollbackTextOr(targets.TargetsReason, "the platform listed none and gave no reason"))
	} else {
		targetTable := table.NewWriter()
		targetTable.SetOutputMirror(out)
		targetTable.SetStyle(table.StyleRounded)
		targetTable.AppendHeader(table.Row{"IMAGE TAG", "PUSHED", "NOTE"})
		for _, target := range targets.Targets {
			var notes []string
			if target.IsCurrent {
				notes = append(notes, "running")
			}
			if target.IsLatestBuild {
				notes = append(notes, "latest build")
			}
			targetTable.AppendRow(table.Row{target.ImageTag, target.PushedAt, strings.Join(notes, ", ")})
		}
		targetTable.Render()
	}
	if len(targets.NotReverted) > 0 {
		_, _ = fmt.Fprintln(out, "\nA rollback does not undo:")
		for _, item := range targets.NotReverted {
			_, _ = fmt.Fprintf(out, "  - %s\n", item)
		}
	}
	if withHint && len(targets.Targets) > 0 {
		_, _ = fmt.Fprintf(out, "\nRoll back with: ankra application rollback %s --cluster %s --to <image-tag>\n",
			applicationName, clusterReference)
	}
}

func printRollbackRecord(out io.Writer, record rollbackRecord, applicationName string, clusterReference string, isAutoDeployEnabled bool) {
	_, _ = fmt.Fprintf(out, "Rolled %s on %s back to %s (was %s). Status: %s.\n", applicationName, clusterReference,
		record.ImageTag, rollbackTextOr(record.PreviousImageTag, "unknown"), record.Status)
	if message := rollbackOptionalText(record.Message); message != "" {
		_, _ = fmt.Fprintln(out, message)
	}
	if isAutoDeployEnabled {
		_, _ = fmt.Fprintln(out, "Push-to-deploy stays on: the rollback holds until a newer build is observed.")
	}
	_, _ = fmt.Fprintf(out, "Rollback id: %s. History: ankra application rollbacks %s --cluster %s\n",
		record.ID, applicationName, clusterReference)
}

func printRollbackHistory(out io.Writer, rollbacks []rollbackRecord) {
	if len(rollbacks) == 0 {
		_, _ = fmt.Fprintln(out, "No rollbacks recorded for this deployment.")
		return
	}
	historyTable := table.NewWriter()
	historyTable.SetOutputMirror(out)
	historyTable.SetStyle(table.StyleRounded)
	historyTable.AppendHeader(table.Row{"REQUESTED", "TO", "FROM", "STATUS", "BY", "MESSAGE"})
	for _, record := range rollbacks {
		historyTable.AppendRow(table.Row{record.RequestedAt, record.ImageTag,
			rollbackTextOr(record.PreviousImageTag, "unknown"), record.Status, record.RequestedBy,
			truncateForDisplay(rollbackOptionalText(record.Message), 60)})
	}
	historyTable.Render()
}

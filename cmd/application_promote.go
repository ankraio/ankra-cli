package cmd

// `ankra application promote`: carry the build one environment has proven onto
// the next through the routes the portal's confirmation and the
// promote_application MCP tool use. The preview is the confirmation's data, and
// its fingerprint makes the write refuse if the picture changed since.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// promotionEnvironment is the slice of the environments read a reference
// resolves against.
type promotionEnvironment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type promotionSide struct {
	promotionEnvironment
	ImageTag *string `json:"image_tag"`
	Status   string  `json:"status"`
	Drift    string  `json:"drift"`
}

type promotionBlocker struct {
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
	FixPath string `json:"fix_path"`
}

type promotionConfigDifference struct {
	Key         string `json:"key"`
	SourceValue string `json:"source_value"`
	TargetValue string `json:"target_value"`
}

type promotionPreview struct {
	ReviewFingerprint string                      `json:"review_fingerprint"`
	ReadReason        string                      `json:"read_reason"`
	State             string                      `json:"state"`
	Source            *promotionSide              `json:"source"`
	Target            *promotionSide              `json:"target"`
	Blockers          []promotionBlocker          `json:"blockers"`
	ConfigDifferences []promotionConfigDifference `json:"config_differences"`
}

// The preview's states.
const (
	promotionStateReady          = "ready"
	promotionStateAlreadyCurrent = "already_current"
	promotionStateBlocked        = "blocked"
)

func newApplicationPromoteCommand() *cobra.Command {
	promoteCommand := &cobra.Command{
		Use:   "promote <application> --from <environment> --to <environment>",
		Short: "Move the build one environment has proven onto the next",
		Long: `Move the build one environment runs onto the next, without building again.

An environment is named by its id, its name, or its role when one environment
has that role, as listed by 'ankra application environments'. The command shows
what would change and asks before it deploys; --dry-run stops after showing it.
The write carries the preview's fingerprint, so it is refused if either
environment changed in between.

Exit codes: 0 when promoted or the target already runs the build, 8 when
something blocks the promotion (the blockers are printed), 4 when you decline.`,
		Example: `  ankra application promote my-app --from staging --to production
  ankra application promote my-app --from staging --to production --dry-run
  ankra application promote my-app --from staging --to production --yes -o json`,
		Args: cobra.ExactArgs(1),
		RunE: runApplicationPromote,
	}
	promoteCommand.Flags().String("from", "", "Environment whose build to promote (required)")
	promoteCommand.Flags().String("to", "", "Environment to promote it to (required)")
	promoteCommand.Flags().Bool("dry-run", false, "Show what would change and deploy nothing")
	promoteCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	registerStructuredOutputFlags(promoteCommand)
	return promoteCommand
}

func runApplicationPromote(command *cobra.Command, arguments []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	fromReference, _ := command.Flags().GetString("from")
	toReference, _ := command.Flags().GetString("to")
	if strings.TrimSpace(fromReference) == "" || strings.TrimSpace(toReference) == "" {
		return withExitCode(exitUsage, errors.New("--from and --to are required: name the two environments"))
	}
	isDryRun, _ := command.Flags().GetBool("dry-run")
	skipConfirmation, _ := command.Flags().GetBool("yes")
	if format != outputDefault && !isDryRun && !skipConfirmation {
		return withExitCode(exitUsage, errors.New("-o needs --yes or --dry-run: structured output cannot ask for confirmation"))
	}

	applicationID, resolveError := resolveApplicationArgument(command, arguments)
	if resolveError != nil {
		return resolveError
	}
	environmentsPayload, environmentsError := apiClient.GetApplicationEnvironments(command.Context(), applicationID)
	if environmentsError != nil {
		return environmentsError
	}
	var environments struct {
		Environments []promotionEnvironment `json:"environments"`
	}
	if decodeError := json.Unmarshal(environmentsPayload, &environments); decodeError != nil {
		return fmt.Errorf("parsing the environments: %w", decodeError)
	}
	source, sourceError := resolvePromotionEnvironment(environments.Environments, fromReference)
	if sourceError != nil {
		return sourceError
	}
	target, targetError := resolvePromotionEnvironment(environments.Environments, toReference)
	if targetError != nil {
		return targetError
	}
	if source.ID == target.ID {
		return withExitCode(exitUsage, fmt.Errorf("--from and --to both name %s", source.Name))
	}

	query := url.Values{}
	query.Set("source_environment_id", source.ID)
	query.Set("target_environment_id", target.ID)
	previewPayload, previewError := apiClient.PreviewApplicationPromotion(command.Context(), applicationID, query)
	if previewError != nil {
		return previewError
	}
	var preview promotionPreview
	if decodeError := json.Unmarshal(previewPayload, &preview); decodeError != nil {
		return fmt.Errorf("parsing the promotion preview: %w", decodeError)
	}

	if format == outputDefault {
		printPromotionPreview(command.OutOrStdout(), preview, source, target)
	} else if isDryRun || preview.State != promotionStateReady {
		if _, renderError := renderStructured(command, json.RawMessage(previewPayload)); renderError != nil {
			return renderError
		}
		return promotionPreviewBlock(preview)
	}
	if preview.State != promotionStateReady || isDryRun {
		return promotionPreviewBlock(preview)
	}

	message := fmt.Sprintf("Promote to %s? This deploys now. [y/N] ", target.Name)
	if confirmError := confirmPrompt(command.InOrStdin(), command.ErrOrStderr(), message, skipConfirmation); confirmError != nil {
		return confirmError
	}
	payload, startError := apiClient.StartApplicationPromotion(command.Context(), applicationID, map[string]string{
		"expected_fingerprint":  preview.ReviewFingerprint,
		"source_environment_id": source.ID,
		"target_environment_id": target.ID,
	})
	if startError != nil {
		return startError
	}
	if format != outputDefault {
		_, renderError := renderStructured(command, json.RawMessage(payload))
		return renderError
	}
	var started struct {
		State       string  `json:"state"`
		ImageTag    string  `json:"image_tag"`
		OperationID *string `json:"operation_id"`
	}
	if decodeError := json.Unmarshal(payload, &started); decodeError != nil {
		return fmt.Errorf("parsing the promotion: %w", decodeError)
	}
	if started.State == promotionStateAlreadyCurrent {
		_, _ = fmt.Fprintf(command.OutOrStdout(), "%s already runs %s.\n", target.Name, started.ImageTag)
		return nil
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Promoting %s to %s.", started.ImageTag, target.Name)
	if started.OperationID != nil && *started.OperationID != "" {
		_, _ = fmt.Fprintf(command.OutOrStdout(), " Follow it with 'ankra cluster operations steps %s'.", *started.OperationID)
	}
	_, _ = fmt.Fprintln(command.OutOrStdout())
	return nil
}

// promotionPreviewBlock answers the error of a preview that cannot go ahead.
func promotionPreviewBlock(preview promotionPreview) error {
	switch preview.State {
	case promotionStateReady, promotionStateAlreadyCurrent:
		return nil
	case promotionStateBlocked:
		details := make([]string, 0, len(preview.Blockers))
		for _, blocker := range preview.Blockers {
			details = append(details, blocker.Detail)
		}
		return withExitCode(exitBlocked, fmt.Errorf("the promotion is blocked: %s", strings.Join(details, "; ")))
	}
	reason := preview.ReadReason
	if reason == "" {
		reason = "the platform could not read both environments"
	}
	return fmt.Errorf("the promotion could not be checked (state %q): %s", preview.State, reason)
}

func printPromotionPreview(out io.Writer, preview promotionPreview, source promotionEnvironment, target promotionEnvironment) {
	tagOf := func(side *promotionSide) string {
		if side == nil || side.ImageTag == nil || *side.ImageTag == "" {
			return "unknown"
		}
		return *side.ImageTag
	}
	_, _ = fmt.Fprintf(out, "%s runs %s\n%s runs %s\n", source.Name, tagOf(preview.Source), target.Name, tagOf(preview.Target))
	if preview.State == promotionStateAlreadyCurrent {
		_, _ = fmt.Fprintf(out, "%s already runs this build.\n", target.Name)
	}
	for _, blocker := range preview.Blockers {
		_, _ = fmt.Fprintf(out, "Blocked: %s\n", blocker.Detail)
		if blocker.FixPath != "" {
			_, _ = fmt.Fprintf(out, "  fix it at %s\n", blocker.FixPath)
		}
	}
	for _, difference := range preview.ConfigDifferences {
		_, _ = fmt.Fprintf(out, "Stays different: %s is %q in %s and %q in %s\n",
			difference.Key, difference.SourceValue, source.Name, difference.TargetValue, target.Name)
	}
}

// resolvePromotionEnvironment finds the one environment a reference names: its
// id, its name, or its role when exactly one environment has that role. A
// reference matching several is refused rather than guessed at, since a wrong
// guess deploys to the wrong place.
func resolvePromotionEnvironment(environments []promotionEnvironment, reference string) (promotionEnvironment, error) {
	trimmed := strings.TrimSpace(reference)
	names := make([]string, 0, len(environments))
	for _, environment := range environments {
		names = append(names, environment.Name)
		if environment.ID == trimmed {
			return environment, nil
		}
	}
	for _, environment := range environments {
		if strings.EqualFold(environment.Name, trimmed) {
			return environment, nil
		}
	}
	var byRole []promotionEnvironment
	for _, environment := range environments {
		if strings.EqualFold(environment.Role, trimmed) {
			byRole = append(byRole, environment)
		}
	}
	switch len(byRole) {
	case 1:
		return byRole[0], nil
	case 0:
		return promotionEnvironment{}, withExitCode(exitNotFound, fmt.Errorf(
			"no environment is named %q; this application has: %s", trimmed, strings.Join(names, ", ")))
	}
	matching := make([]string, 0, len(byRole))
	for _, environment := range byRole {
		matching = append(matching, environment.Name)
	}
	return promotionEnvironment{}, withExitCode(exitUsage, fmt.Errorf(
		"%d environments have the role %q (%s): name one", len(byRole), trimmed, strings.Join(matching, ", ")))
}

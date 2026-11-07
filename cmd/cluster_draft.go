package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"ankra/internal/client"
	"github.com/spf13/cobra"
)

type stackDraftOutput struct {
	StackName       string `json:"stack_name" yaml:"stack_name"`
	DraftID         string `json:"draft_id,omitempty" yaml:"draft_id,omitempty"`
	ReplacedDraftID string `json:"replaced_draft_id,omitempty" yaml:"replaced_draft_id,omitempty"`
	NoChange        bool   `json:"no_change" yaml:"no_change"`
	Error           string `json:"error,omitempty" yaml:"error,omitempty"`
}

// stackDraftExistsRefusal is the part of the platform's refusal of a stack that
// already has a draft, which is the one refusal --replace is the way out of.
const stackDraftExistsRefusal = "already exists in this cluster draft"

var clusterDraftCmd = &cobra.Command{
	Use:   "draft",
	Short: "Stage an ImportCluster YAML as reviewable drafts instead of applying it",
	Long: `Stage all changes in an ImportCluster YAML as drafts on the cluster
without deploying anything. The local checks run first (the same ones as
'ankra cluster apply --dry-run'), then the stacks in the file are saved as
resource drafts you can review, edit, and deploy from the Ankra stack builder.

The file lands whole or not at all: if the platform refuses any stack, it says
everything it found and nothing is staged, so you fix the file and run the same
command again. A stack that already has a draft is refused. --replace discards
that draft and stages the file's version instead, including any edits made to
the draft since it was staged, so it asks first; without a terminal to ask on,
it needs --yes. A draft a data run is still waiting on, or one that is not yours
to discard, is refused either way.

If the cluster does not exist yet it is imported first without its stacks,
since drafts can only be attached to an existing cluster; every stack in the
file is then staged as a draft. Stacks that already match the cluster's
desired state are reported as "no changes" rather than creating an empty
draft.`,
	Args: cobra.NoArgs,
	RunE: runClusterDraft,
}

func init() {
	clusterDraftCmd.Flags().StringP("file", "f", "", "Path to the ImportCluster YAML file to stage as drafts")
	clusterDraftCmd.Flags().Bool("replace", false,
		"Discard the existing draft of each stack in the file, including edits made to it, and stage the file's version")
	clusterDraftCmd.Flags().BoolP("yes", "y", false, "Skip the --replace confirmation prompt")
	registerStructuredOutputFlags(clusterDraftCmd)
	_ = clusterDraftCmd.MarkFlagRequired("file")
	clusterCmd.AddCommand(clusterDraftCmd)
}

func runClusterDraft(cmd *cobra.Command, _ []string) error {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		return fmt.Errorf("reading --file: %w", err)
	}
	replace, err := cmd.Flags().GetBool("replace")
	if err != nil {
		return fmt.Errorf("reading --replace: %w", err)
	}
	format, err := structuredFormatFromFlags(cmd)
	if err != nil {
		return err
	}
	structured := format != outputDefault

	importRequest, err := buildImportRequest(filePath)
	if err != nil {
		return fmt.Errorf("invalid ImportCluster in %q:\n  %w", filePath, err)
	}
	if err := validateResourceGraph(importRequest); err != nil {
		return fmt.Errorf("invalid ImportCluster in %q:\n  %w", filePath, err)
	}

	// Asked before anything changes: importing a cluster that does not exist yet
	// is a change too, and declining has to leave nothing behind.
	stacks := importRequest.Spec.Stacks
	if replace && len(stacks) > 0 {
		if err := confirmDraftReplace(cmd, importRequest.Name, stacks); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	clusterID, err := resolveClusterForDraft(ctx, importRequest)
	if err != nil {
		return err
	}

	stackOutputs := make([]stackDraftOutput, 0, len(stacks))
	var created, unchanged int
	if len(stacks) > 0 {
		result, draftErr := apiClient.CreateStackDrafts(ctx, clusterID, stacks, replace)
		if draftErr != nil {
			if structured {
				for _, stack := range stacks {
					stackOutputs = append(stackOutputs, stackDraftOutput{StackName: stack.Name, Error: draftErr.Error()})
				}
				if encodeError := encodeStructured(cmd.OutOrStdout(), format, stackOutputs); encodeError != nil {
					return encodeError
				}
			}
			// Returned as it is, so a missing cluster still exits as not found and a
			// role that may not write still exits as a permission problem.
			return draftErr
		}
		if len(result.Errors) > 0 {
			stackOutputs = reportDraftRefusals(cmd.ErrOrStderr(), stacks, result.Errors, replace)
			if structured {
				if encodeError := encodeStructured(cmd.OutOrStdout(), format, stackOutputs); encodeError != nil {
					return encodeError
				}
			}
			return errors.New("nothing was staged: the platform refused this file")
		}
		for _, staged := range result.Stacks {
			stackOutputs = append(stackOutputs, stackDraftOutput{
				StackName:       staged.StackName,
				DraftID:         staged.DraftID,
				ReplacedDraftID: staged.ReplacedDraftID,
				NoChange:        staged.NoChange,
			})
			if staged.NoChange {
				unchanged++
			} else {
				created++
			}
			if !structured {
				fmt.Printf("- stack %q: %s\n", staged.StackName, describeStagedDraft(staged))
			}
		}
	}

	if structured {
		return encodeStructured(cmd.OutOrStdout(), format, stackOutputs)
	}

	fmt.Printf("\nStaged %d draft(s), %d stack(s) already up to date. Nothing was deployed.\n", created, unchanged)
	if created > 0 {
		fmt.Printf("Review and deploy the drafts in the UI:\n  %s/organisation/clusters/cluster/imported/%s/overview\n",
			strings.TrimRight(baseURL, "/"), clusterID)
	}
	return nil
}

// confirmDraftReplace says what --replace discards and asks before it does. Without
// a terminal to ask on, --yes is required, because a script that never saw what is
// discarded must not discard it. Everything goes to stderr so -o json|yaml stays
// parseable.
func confirmDraftReplace(command *cobra.Command, clusterName string, stacks []client.Stack) error {
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprintf(errOut, "--replace discards the existing draft of each of these stacks on cluster '%s', "+
		"including edits made to it since it was staged:\n\n", clusterName)
	for _, stack := range stacks {
		_, _ = fmt.Fprintf(errOut, "  %s\n", stack.Name)
	}
	_, _ = fmt.Fprintln(errOut, "\nA stack with no draft is simply staged. If the platform refuses any stack in the file, nothing changes.")

	yes, _ := command.Flags().GetBool("yes")
	if yes {
		_, _ = fmt.Fprintln(errOut)
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"--replace discards the existing drafts of this file's stacks on cluster '%s' and needs confirmation, "+
				"but there is no terminal to ask on; re-run with --yes to confirm", clusterName))
	}
	if promptError := confirmPrompt(command.InOrStdin(), errOut, "\nDiscard them and stage this file? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// describeStagedDraft is the line for one stack of a file that landed. A replace can
// discard a draft even where the stack ends up with no change, and that draft was
// someone's work, so the line says so.
func describeStagedDraft(staged client.StagedStackDraft) string {
	switch {
	case staged.NoChange && staged.ReplacedDraftID != "":
		return fmt.Sprintf("no changes, discarded draft %s", staged.ReplacedDraftID)
	case staged.NoChange:
		return "no changes"
	case staged.ReplacedDraftID != "":
		return fmt.Sprintf("draft created (%s), replacing draft %s", staged.DraftID, staged.ReplacedDraftID)
	default:
		return fmt.Sprintf("draft created (%s)", staged.DraftID)
	}
}

// reportDraftRefusals prints a refused file's reasons, each under the stack it belongs
// to, and returns one output entry per stack of the file. A refusal that belongs to no
// stack we can name holds up all of them, so it goes on every entry. A stack with
// nothing wrong still says it was not staged: the file lands whole or not at all.
func reportDraftRefusals(errOut io.Writer, stacks []client.Stack, refusals []client.ImportResponseResourceError, replace bool) []stackDraftOutput {
	linesByStack := make(map[string][]string, len(stacks))
	var fileLines []string
	mentionsExistingDraft := false
	for _, refusal := range refusals {
		owner, isOwned := stackOwningRefusal(stacks, refusal)
		for _, detail := range refusal.Errors {
			line := describeDraftResourceError(refusal, detail)
			if strings.Contains(line, stackDraftExistsRefusal) {
				mentionsExistingDraft = true
			}
			if isOwned {
				linesByStack[owner] = append(linesByStack[owner], line)
			} else {
				fileLines = append(fileLines, line)
			}
		}
	}

	_, _ = fmt.Fprintln(errOut, "The platform refused this file, so nothing was staged:")
	stackOutputs := make([]stackDraftOutput, 0, len(stacks))
	for _, stack := range stacks {
		lines := linesByStack[stack.Name]
		if len(lines) > 0 {
			_, _ = fmt.Fprintf(errOut, "- stack %q:\n", stack.Name)
			printDraftRefusalLines(errOut, lines)
		}
		reasons := append(append([]string{}, lines...), fileLines...)
		if len(reasons) == 0 {
			reasons = []string{"not staged: another stack in this file was refused"}
		}
		stackOutputs = append(stackOutputs, stackDraftOutput{StackName: stack.Name, Error: strings.Join(reasons, "; ")})
	}
	if len(fileLines) > 0 {
		_, _ = fmt.Fprintln(errOut, "- the file:")
		printDraftRefusalLines(errOut, fileLines)
	}
	if mentionsExistingDraft && !replace {
		_, _ = fmt.Fprintln(errOut, "\nTo discard an existing draft, including edits made to it, and stage this file instead, re-run with --replace.")
	}
	return stackOutputs
}

func printDraftRefusalLines(errOut io.Writer, lines []string) {
	for _, line := range lines {
		_, _ = fmt.Fprintf(errOut, "    • %s\n", line)
	}
}

// stackOwningRefusal finds the one stack of the file a refusal belongs to. A file of
// one stack owns everything. In a larger file an addon or manifest name two stacks
// share is not enough to say which one was meant, and naming one would be a guess, so
// the refusal then belongs to no stack and false is returned.
func stackOwningRefusal(stacks []client.Stack, refusal client.ImportResponseResourceError) (string, bool) {
	if len(stacks) == 1 {
		return stacks[0].Name, true
	}
	owners := make([]string, 0, 1)
	for _, stack := range stacks {
		if stackDeclaresRefused(stack, refusal) {
			owners = append(owners, stack.Name)
		}
	}
	if len(owners) == 1 {
		return owners[0], true
	}
	return "", false
}

func stackDeclaresRefused(stack client.Stack, refusal client.ImportResponseResourceError) bool {
	switch refusal.Kind {
	case "stack":
		return stack.Name == refusal.Name
	case "addon":
		for _, addon := range stack.Addons {
			if addon.Name == refusal.Name {
				return true
			}
		}
	case "manifest":
		for _, manifest := range stack.Manifests {
			if manifest.Name == refusal.Name {
				return true
			}
		}
	}
	return false
}

// describeDraftResourceError renders one draft refusal as
// `<kind> "<name>": <field> - <message>`, dropping the field when the
// platform named none and the location when it pinned the refusal on nothing.
func describeDraftResourceError(resourceError client.ImportResponseResourceError, detail client.ImportResponseErrorItem) string {
	reason := detail.Message
	if detail.Key != "" {
		reason = fmt.Sprintf("%s - %s", detail.Key, detail.Message)
	}
	if resourceError.Kind == "" && resourceError.Name == "" {
		return reason
	}
	return fmt.Sprintf("%s %q: %s", resourceError.Kind, resourceError.Name, reason)
}

// resolveClusterForDraft returns the cluster ID to attach drafts to. If the
// cluster does not exist yet it is imported first without any stacks, because
// drafts can only be attached to an existing cluster and importing the stacks
// would deploy them.
func resolveClusterForDraft(ctx context.Context, importRequest client.CreateImportClusterRequest) (string, error) {
	existing, err := apiClient.GetCluster(importRequest.Name)
	if err == nil && existing.ID != "" {
		return existing.ID, nil
	}
	if err != nil && !strings.Contains(err.Error(), "no cluster found") {
		return "", fmt.Errorf("looking up cluster %q: %w", importRequest.Name, err)
	}

	fmt.Fprintf(os.Stderr, "Cluster %q does not exist yet; importing it first (without stacks, so they can be staged as drafts)...\n", importRequest.Name)
	// Bootstrap the cluster without stacks: importing the full request would
	// deploy every stack as live desired state, leaving nothing to draft.
	bootstrapRequest := importRequest
	bootstrapRequest.Spec.Stacks = []client.Stack{}
	importResponse, _, importErr := apiClient.ApplyCluster(ctx, bootstrapRequest, true)
	if importErr != nil {
		return "", fmt.Errorf("importing cluster %q: %w", importRequest.Name, importErr)
	}
	if len(importResponse.Errors) > 0 {
		return "", fmt.Errorf("import of cluster %q failed: %v", importRequest.Name, importResponse.Errors)
	}
	if importResponse.GitPushDeferred {
		// The bootstrap import WAS saved and is live (that is what a git-push
		// deferral means), but the deferred response carries no cluster ID to
		// attach drafts to — succeeding here would feed an empty ID into
		// every staging call. Re-running resolves the now-existing cluster
		// through the GetCluster lookup above and proceeds normally.
		return "", fmt.Errorf("cluster %q was imported and is live, but the platform deferred its Git commit and returned no cluster ID; re-run the command to stage the drafts (the cluster now exists and will be found directly).\n%s",
			importRequest.Name, importResponse.GitPushMessage)
	}
	fmt.Fprintf(os.Stderr, "Cluster %q imported.\n", importResponse.Name)
	return importResponse.ClusterId, nil
}

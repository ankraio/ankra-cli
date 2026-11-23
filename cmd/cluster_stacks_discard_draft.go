package cmd

// `ankra cluster stacks discard-draft` (ankra-0xsdd.135).
//
// show-draft and deploy-draft let a person review a draft and ship it from the
// terminal; nothing let them throw one away. A clone's draft lives a day and
// holds its stack name meanwhile, so a reviewed clone nobody wants blocked a
// reclone under the same name, and a draft an AI conversation staged and the
// person then made in Git instead stayed deployable, waiting to undo that fix
// (conversation 5a18275f). This verb discards a draft over the write the
// stack builder's Discard button uses.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var clusterStacksDiscardDraftCmd = &cobra.Command{
	Use:   "discard-draft <stack name>",
	Short: "Discard a stack's pending draft",
	Long: "Discard the pending draft of a stack on the active cluster. Nothing is deployed or " +
		"removed from the cluster: a stack that is already deployed keeps running as it is, and a " +
		"stack that exists only as a draft is gone, which frees its name.\n\n" +
		"Review the draft first with 'ankra cluster stacks show-draft <stack name>'. The command asks " +
		"before it discards; pass --yes to skip the question, which a run with no terminal needs.\n\n" +
		"Ankra keeps a draft an AI conversation is working on, or has just written: the command then " +
		"says so and exits 8 with nothing discarded. Discarding one of those needs --force.",
	Example: `  ankra cluster stacks discard-draft shop --cluster staging
  ankra cluster stacks discard-draft shop --cluster staging --yes -o json
  ankra cluster stacks discard-draft shop --cluster staging --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := strings.TrimSpace(args[0])
		if stackName == "" {
			return withExitCode(exitUsage, fmt.Errorf("a stack name is required"))
		}
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		documents, listError := apiClient.ListClusterStackDocuments(cluster.ID)
		if listError != nil {
			return fmt.Errorf("listing stacks: %w", listError)
		}
		document, selectError := selectStackDraft(documents, stackName, "discard")
		if selectError != nil {
			return selectError
		}
		isForced, _ := cmd.Flags().GetBool("force")
		if confirmError := confirmStackDraftDiscard(cmd, cluster.Name, document, isForced); confirmError != nil {
			return confirmError
		}
		result, discardError := apiClient.DiscardClusterStackDraft(cmd.Context(), cluster.ID, document.DraftID(), isForced)
		if discardError != nil {
			var refusal *client.StackDraftDiscardError
			if errors.As(discardError, &refusal) && refusal.IsGone() {
				return withExitCode(exitNotFound, fmt.Errorf(
					"stack %q: %w. List the stacks again to see what is pending", document.Name(), discardError))
			}
			return fmt.Errorf("discarding the draft of stack %q: %w", document.Name(), discardError)
		}
		if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
			if renderError == nil && result.IsProtected() {
				return withExitCode(exitBlocked, errStackDraftProtected)
			}
			return renderError
		}
		return printStackDraftDiscardResult(cmd.OutOrStdout(), document, result)
	},
}

// errStackDraftProtected is the exit of a discard the platform declined
// because an AI conversation owns the draft: exit 8, a block that changed
// nothing, so a script can tell it from a failure and re-run with --force.
var errStackDraftProtected = errors.New("the draft is kept by an AI conversation; nothing was discarded")

// confirmStackDraftDiscard names the draft and asks before discarding it, on
// stderr so -o json|yaml stays parseable. A forced discard says that it
// also takes a draft an AI conversation is holding. --yes skips the
// question; a run with no terminal and no --yes is refused with nothing
// discarded.
func confirmStackDraftDiscard(cmd *cobra.Command, clusterName string, document client.ClusterStackDocument,
	isForced bool) error {
	errOut := cmd.ErrOrStderr()
	what := "Its edits are lost; the deployed stack keeps running as it is."
	if document.IsDraftOnly() {
		what = "The stack exists only as this draft, so it is gone and its name is free again."
	}
	if isForced {
		what += " --force: it is discarded even if an AI conversation is working on it."
	}
	_, _ = fmt.Fprintf(errOut, "Discarding draft %s of stack '%s' on cluster '%s'. %s\n",
		document.DraftID(), document.Name(), clusterName, what)
	if isConfirmed, _ := cmd.Flags().GetBool("yes"); isConfirmed {
		return nil
	}
	if !promptIsInteractive(cmd.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"discarding the draft of stack '%s' needs confirmation, but there is no terminal to ask on; "+
				"nothing was discarded. Re-run with --yes to confirm", document.Name()))
	}
	if promptError := confirmPrompt(cmd.InOrStdin(), errOut, "Discard this draft? [y/N]: ", false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// printStackDraftDiscardResult says what the platform did. A 200 is not proof
// of a discard: a draft that had already ended, and a draft an AI
// conversation owns, both answer it with nothing discarded.
func printStackDraftDiscardResult(out io.Writer, listed client.ClusterStackDocument,
	result *client.StackDraftDiscardResult) error {
	switch {
	case result.Discarded():
		_, _ = fmt.Fprintf(out, "Discarded draft %s of stack '%s'.\n", listed.DraftID(), listed.Name())
		return nil
	case result.IsProtected():
		_, _ = fmt.Fprintf(out, "Draft %s of stack '%s' was kept: an AI conversation is working on it or has "+
			"just written it, so nothing was discarded. Re-run with --force to discard it anyway.\n",
			listed.DraftID(), listed.Name())
		return withExitCode(exitBlocked, errStackDraftProtected)
	case result.AlreadyDiscarded:
		reason := "it was discarded or deployed since the stack was listed"
		if result.Reason != nil && *result.Reason != "" {
			reason = "it ended earlier (" + *result.Reason + ")"
		}
		_, _ = fmt.Fprintf(out, "Draft %s of stack '%s' had already ended: %s. Nothing was left to discard.\n",
			listed.DraftID(), listed.Name(), reason)
		return nil
	default:
		return withExitCode(exitError, fmt.Errorf(
			"the platform did not confirm the discard of draft %s of stack '%s'; list the stacks to see whether it is still pending",
			listed.DraftID(), listed.Name()))
	}
}

func init() {
	registerStructuredOutputFlags(clusterStacksDiscardDraftCmd)
	clusterStacksDiscardDraftCmd.Flags().BoolP("yes", "y", false, "Discard without asking for confirmation")
	clusterStacksDiscardDraftCmd.Flags().Bool("force", false,
		"Discard a draft an AI conversation is working on or has just written, which Ankra otherwise keeps")
	clusterStacksCmd.AddCommand(clusterStacksDiscardDraftCmd)
}

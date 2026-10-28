package cmd

// `ankra cluster stacks deploy-draft` (epic ankra-0xsdd, third live
// verification pass).
//
// Everything that builds a stack without running it leaves a draft: a clone,
// a stack-profile instantiation, `ankra cluster draft -f`. Deploying one was
// a portal-only move. `clone --deploy` is taken at clone time and cannot be
// replayed - a second clone onto the same target is refused on the name -
// and with `--with-data` it only plans the deploy and waits for a restore.
// So the terminal path stopped one step short of a running stack.
//
// This verb closes it over the write the portal's own Deploy button uses.
//
// It also deploys the draft of a stack that is already deployed (bead
// ankra-bjkszh.10) - what `ankra cluster draft -f` leaves on an existing
// stack. That draft was builder-only, so operators forced `manifests upgrade`
// member by member instead. It is promoted through the builder's own update
// write; see PromoteClusterStackDraft for why it is partial and why it reads
// the draft rather than the listing.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var clusterStacksDeployDraftCmd = &cobra.Command{
	Use:   "deploy-draft <stack name>",
	Short: "Deploy a stack's pending draft",
	Long: "Deploy the pending draft of a stack on the active cluster: a stack that exists only as a " +
		"draft (what a clone or a stack-profile instantiation leaves behind), or the draft of edits " +
		"'ankra cluster draft -f' or the stack builder left on a stack that is already deployed.\n\n" +
		"The draft's contents are deployed exactly as they are stored. Review them first with " +
		"'ankra cluster stacks list <stack name>', and edit them in the stack builder if the " +
		"clone warned that something has to change before it can come up.\n\n" +
		"For a stack that is already deployed only the members the draft changes are written; " +
		"members it does not mention keep running untouched.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := strings.TrimSpace(args[0])
		if stackName == "" {
			return withExitCode(exitUsage, fmt.Errorf("a stack name is required"))
		}
		format, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
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
		document, selectError := selectDeployableDraft(documents, stackName)
		if selectError != nil {
			return selectError
		}

		// The command's own context, so Ctrl-C cancels the request rather
		// than leaving its outcome unknown.
		var result *client.StackWriteResult
		if document.IsDraftOnly() {
			if format == outputDefault {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Deploying draft stack '%s' on cluster '%s'...\n",
					document.Name(), cluster.Name)
			}
			deployed, deployError := apiClient.DeployClusterStackDraft(cmd.Context(), cluster.ID, document)
			if deployError != nil {
				return fmt.Errorf("deploying the draft: %w", deployError)
			}
			result = deployed
		} else {
			if format == outputDefault {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Deploying the pending draft of stack '%s' on cluster '%s'...\n",
					document.Name(), cluster.Name)
			}
			promoted, promoteError := promoteDeployedStackDraft(cmd, cluster.ID, document)
			if promoteError != nil {
				return promoteError
			}
			result = promoted
		}
		if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
			if len(result.Errors) > 0 {
				return withExitCode(exitError, errStackWriteRefused)
			}
			return renderError
		}
		return printStackDraftDeployResult(cmd.OutOrStdout(), result)
	},
}

// errStackWriteRefused is the non-zero exit of a refused write whose reasons
// have already been rendered as structured output.
var errStackWriteRefused = fmt.Errorf("the stack write was refused")

// selectDeployableDraft finds the named stack and answers why it cannot be
// deployed when it cannot, rather than letting the write refuse it in
// wording written for a different caller. A stack that exists only as a
// draft and a deployed stack holding a draft of edits are both deployable;
// a deployed stack with no draft has nothing to deploy.
func selectDeployableDraft(documents []client.ClusterStackDocument, stackName string) (client.ClusterStackDocument, error) {
	var match client.ClusterStackDocument
	draftNames := []string{}
	for _, document := range documents {
		if document.IsDraftOnly() || document.DraftID() != "" {
			draftNames = append(draftNames, document.Name())
		}
		if strings.EqualFold(document.Name(), stackName) {
			match = document
		}
	}
	if match == nil {
		if len(draftNames) == 0 {
			return nil, withExitCode(exitNotFound, fmt.Errorf(
				"stack %q not found on the active cluster, and no stack on it has a draft to deploy", stackName))
		}
		return nil, withExitCode(exitNotFound, fmt.Errorf(
			"stack %q not found on the active cluster. Stacks with a draft: %s",
			stackName, strings.Join(draftNames, ", ")))
	}
	if match.DraftID() == "" {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"stack %q has no draft to deploy: it is already deployed and has no edits in flight", match.Name()))
	}
	return match, nil
}

// promoteDeployedStackDraft deploys the draft bound to an already-deployed
// stack. The listing names the draft but elides its bodies, so the draft is
// read in full first, and the read must still be the draft the listing named:
// a draft that ended in between is reported, never replaced by another.
func promoteDeployedStackDraft(cmd *cobra.Command, clusterID string, listed client.ClusterStackDocument) (*client.StackWriteResult, error) {
	draft, readError := apiClient.GetClusterStackDraft(cmd.Context(), clusterID, listed.DraftID())
	if readError != nil {
		var draftReadError *client.StackDraftReadError
		if errors.As(readError, &draftReadError) && draftReadError.IsGone() {
			return nil, withExitCode(exitNotFound, fmt.Errorf(
				"stack %q: %w. List the stacks again to see what is pending", listed.Name(), readError))
		}
		return nil, fmt.Errorf("reading the draft of stack %q: %w", listed.Name(), readError)
	}
	if !strings.EqualFold(draft.Name(), listed.Name()) || draft.DraftID() != listed.DraftID() {
		return nil, withExitCode(exitError, fmt.Errorf(
			"the draft read for stack %q answered stack %q, draft %q; nothing was deployed",
			listed.Name(), draft.Name(), draft.DraftID()))
	}
	promoted, promoteError := apiClient.PromoteClusterStackDraft(cmd.Context(), clusterID, draft)
	if promoteError != nil {
		var patchError *client.PatchStackError
		if errors.As(promoteError, &patchError) {
			return nil, fmt.Errorf("deploying the draft: %w", mapPatchError(patchError))
		}
		return nil, fmt.Errorf("deploying the draft: %w", promoteError)
	}
	result := stackWriteResultFromPromote(promoted)
	if len(result.Errors) == 0 {
		result.Warnings = append(result.Warnings, draftLeftoverWarnings(clusterID, listed)...)
	}
	return result, nil
}

// stackWriteResultFromPromote renders the update write's answer in the shape
// the draft-only deploy prints, so both deploys read - and serialise - alike.
// A deferred Git write-back is a warning: the stack is deployed, only the
// commit waits on the background sync.
func stackWriteResultFromPromote(promoted *client.PatchStackResult) *client.StackWriteResult {
	result := &client.StackWriteResult{StackName: promoted.StackName, JobCount: promoted.JobCount}
	for _, resourceError := range promoted.Errors {
		items := make([]client.StackResourceErrorItem, 0, len(resourceError.Errors))
		for _, item := range resourceError.Errors {
			items = append(items, client.StackResourceErrorItem(item))
		}
		result.Errors = append(result.Errors, client.StackResourceError{
			Name: resourceError.Name, Kind: resourceError.Kind, Errors: items,
		})
	}
	if promoted.OperationID != "" {
		operationID := promoted.OperationID
		result.OperationID = &operationID
	}
	if promoted.CommitSHA != "" {
		commitSHA := promoted.CommitSHA
		result.CommitSHA = &commitSHA
	}
	if promoted.CommitURL != "" {
		commitURL := promoted.CommitURL
		result.CommitURL = &commitURL
	}
	if promoted.GitPushDeferred {
		message := "The stack is deployed; the commit to its GitOps repository is deferred to the background sync."
		if promoted.GitPushMessage != "" {
			message += " " + promoted.GitPushMessage
		}
		result.Warnings = append(result.Warnings, client.ValidationWarning{Kind: "Stack", Message: message, Category: "gitops"})
	}
	return result
}

// draftLeftoverWarnings says when the deployed draft is still open after the
// write. The partial promote closes a draft once nothing is left in it, so a
// draft still bound to the stack holds something this write did not ship -
// a member removal, or an edit made in the builder meanwhile. A listing that
// cannot be read is said to be unconfirmed rather than taken as "closed".
func draftLeftoverWarnings(clusterID string, listed client.ClusterStackDocument) []client.ValidationWarning {
	documents, listError := apiClient.ListClusterStackDocuments(clusterID)
	if listError != nil {
		return []client.ValidationWarning{{Kind: "Stack", Name: listed.Name(), Category: "draft", Message: fmt.Sprintf(
			"Could not confirm the draft of stack '%s' was closed (%v); check it with 'ankra cluster stacks list %s'.",
			listed.Name(), listError, listed.Name())}}
	}
	for _, document := range documents {
		if strings.EqualFold(document.Name(), listed.Name()) && document.DraftID() == listed.DraftID() {
			return []client.ValidationWarning{{Kind: "Stack", Name: listed.Name(), Category: "draft", Message: fmt.Sprintf(
				"Stack '%s' still holds a draft after this deploy: it carries changes this command does not ship, "+
					"such as a member removal. Review and deploy them from the stack builder.", listed.Name())}}
		}
	}
	return nil
}

// printStackDraftDeployResult renders the write. A refusal is printed with
// every reason and exits non-zero: the draft is untouched and still
// deployable once the reasons are dealt with.
func printStackDraftDeployResult(out io.Writer, result *client.StackWriteResult) error {
	if len(result.Errors) > 0 {
		_, _ = fmt.Fprintf(out, "\nThe draft was not deployed and is kept as a draft:\n")
		for _, resourceError := range result.Errors {
			for _, item := range resourceError.Errors {
				_, _ = fmt.Fprintf(out, "  - %s %s: %s\n",
					resourceError.Kind, resourceError.Name, item.Message)
			}
		}
		return withExitCode(exitError, errStackWriteRefused)
	}
	_, _ = fmt.Fprintf(out, "\nDraft deployed.\n")
	_, _ = fmt.Fprintf(out, "  Stack:      %s\n", result.StackName)
	_, _ = fmt.Fprintf(out, "  Jobs:       %d\n", result.JobCount)
	if result.OperationID != nil && *result.OperationID != "" {
		_, _ = fmt.Fprintf(out, "  Operation:  %s\n", *result.OperationID)
	}
	if result.CommitSHA != nil && *result.CommitSHA != "" {
		_, _ = fmt.Fprintf(out, "  Commit:     %s\n", *result.CommitSHA)
	}
	printValidationWarnings(out, result.Warnings)
	if result.OperationID != nil && *result.OperationID != "" {
		_, _ = fmt.Fprintf(out, "\nFollow it with 'ankra cluster operations list %s'.\n", *result.OperationID)
	}
	return nil
}

func init() {
	registerStructuredOutputFlags(clusterStacksDeployDraftCmd)
	clusterStacksCmd.AddCommand(clusterStacksDeployDraftCmd)
}

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

import (
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var clusterStacksDeployDraftCmd = &cobra.Command{
	Use:   "deploy-draft <stack name>",
	Short: "Deploy a stack that exists on the cluster only as a draft",
	Long: "Deploy a draft stack - the state a clone, a stack-profile instantiation or " +
		"'ankra cluster draft' leaves behind - on the active cluster.\n\n" +
		"The draft's contents are deployed exactly as they are stored. Review them first with " +
		"'ankra cluster stacks list <stack name>', and edit them in the stack builder if the " +
		"clone warned that something has to change before it can come up.",
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

		if format == outputDefault {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Deploying draft stack '%s' on cluster '%s'...\n",
				document.Name(), cluster.Name)
		}
		// The command's own context, so Ctrl-C cancels the request rather
		// than leaving its outcome unknown.
		result, deployError := apiClient.DeployClusterStackDraft(cmd.Context(), cluster.ID, document)
		if deployError != nil {
			return fmt.Errorf("deploying the draft: %w", deployError)
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
// wording written for a different caller.
//
// A stack with edits in flight over a deployed one (deployed_dirty) is
// deliberately refused here: promoting that draft is the update write, not
// the create write this verb runs, and the create write's own refusal for
// the case ("use update_cluster_stack instead") names an API function no
// CLI user has.
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
	if !match.IsDraftOnly() {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"stack %q is already deployed and holds a draft of unsaved edits. Deploy those from the stack "+
				"builder in the Ankra dashboard; this command deploys a stack that exists only as a draft",
			match.Name()))
	}
	return match, nil
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
	printWarnings(out, result.Warnings)
	if result.OperationID != nil && *result.OperationID != "" {
		_, _ = fmt.Fprintf(out, "\nFollow it with 'ankra cluster operations list %s'.\n", *result.OperationID)
	}
	return nil
}

func init() {
	registerStructuredOutputFlags(clusterStacksDeployDraftCmd)
	clusterStacksCmd.AddCommand(clusterStacksDeployDraftCmd)
}

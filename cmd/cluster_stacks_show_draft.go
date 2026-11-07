package cmd

// `ankra cluster stacks show-draft` (ankra-0xsdd.132).
//
// A draft is what a person is asked to review before it deploys: a with-data
// clone waits on exactly that, and its draft can carry things that must not
// deploy untouched - a one-off import Job, the source's hostnames. Reviewing one
// was a portal-only move, because the listing elides member bodies, so the
// terminal path could deploy a draft it could not read.

import (
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var clusterStacksShowDraftCmd = &cobra.Command{
	Use:   "show-draft <stack name>",
	Short: "Show a stack's pending draft in full",
	Long: "Print the pending draft of a stack on the active cluster: every member, what it waits " +
		"for, and the content it would deploy, decoded. Nothing is deployed or changed.\n\n" +
		"Encrypted values are shown as they are stored, and variables are shown unresolved, because " +
		"both are filled in when the draft deploys.",
	Example: `  ankra cluster stacks show-draft shop --cluster staging
  ankra cluster stacks show-draft shop --cluster staging -o json`,
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
		document, selectError := selectDeployableDraft(documents, stackName)
		if selectError != nil {
			return selectError
		}
		// A stack that is only a draft can list with no draft id, and then
		// the listing is the draft: its deploy posts that same object.
		if document.DraftID() != "" {
			draft, readError := readListedStackDraft(cmd, cluster.ID, document)
			if readError != nil {
				return readError
			}
			document = draft
		}
		review, reviewError := document.DraftReview()
		if reviewError != nil {
			return fmt.Errorf("reading the draft of stack %q: %w", document.Name(), reviewError)
		}
		if rendered, renderError := renderStructured(cmd, review); rendered || renderError != nil {
			return renderError
		}
		printStackDraftReview(cmd.OutOrStdout(), review)
		return nil
	},
}

// printStackDraftReview prints the members first, so the shape of the draft and
// its order can be read before the bodies scroll past.
func printStackDraftReview(out io.Writer, review client.StackDraftReview) {
	_, _ = fmt.Fprintf(out, "Draft of stack '%s'", review.StackName)
	if review.DraftID != "" {
		_, _ = fmt.Fprintf(out, " (draft %s)", review.DraftID)
	}
	_, _ = fmt.Fprintln(out)
	if len(review.Members) == 0 {
		_, _ = fmt.Fprintln(out, "It has no members.")
		return
	}
	_, _ = fmt.Fprintln(out, "\nMembers:")
	for _, member := range review.Members {
		_, _ = fmt.Fprintf(out, "  %s\n", stackDraftMemberLabel(member))
		if len(member.Parents) > 0 {
			parents := make([]string, 0, len(member.Parents))
			for _, parent := range member.Parents {
				parents = append(parents, parent.Kind+" "+parent.Name)
			}
			_, _ = fmt.Fprintf(out, "    after: %s\n", strings.Join(parents, ", "))
		}
		if len(member.EncryptedPaths) > 0 {
			_, _ = fmt.Fprintf(out, "    encrypted: %s\n", strings.Join(member.EncryptedPaths, ", "))
		}
	}
	for _, member := range review.Members {
		_, _ = fmt.Fprintf(out, "\n--- %s ---\n", stackDraftMemberLabel(member))
		switch {
		case !member.HasContent:
			_, _ = fmt.Fprintln(out, "(this read carried no content for this member)")
		case member.Content == "":
			_, _ = fmt.Fprintln(out, "(empty)")
		default:
			_, _ = fmt.Fprintln(out, strings.TrimRight(member.Content, "\n"))
		}
	}
	_, _ = fmt.Fprintf(out, "\nThis draft has not been deployed. Deploy it with "+
		"'ankra cluster stacks deploy-draft %s' once it is right.\n", review.StackName)
}

// stackDraftMemberLabel names a member the same way in the list and over its
// body, so one can be found from the other.
func stackDraftMemberLabel(member client.StackDraftMember) string {
	label := member.Kind + " " + member.Name
	if member.Chart != "" {
		label += " (" + member.Chart + ")"
	}
	if member.Namespace != "" {
		label += " in namespace " + member.Namespace
	}
	return label
}

func init() {
	registerStructuredOutputFlags(clusterStacksShowDraftCmd)
	clusterStacksCmd.AddCommand(clusterStacksShowDraftCmd)
}

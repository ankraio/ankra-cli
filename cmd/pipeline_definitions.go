package cmd

// The definition-approval commands (ankra-vn0bd.10.8): reading and granting
// an administrator's approval of one stored pipeline definition's protected
// sections (go/internal/pipelineapi/approval.go), the human act "logic is
// open, authority is closed" (enginekit/pipelinepolicy) requires before a
// definition that changed authority on the default branch is trusted.
//
// Unlike every other `pipeline` subcommand, these two take neither
// --application nor --repository: the server addresses this pair of routes
// by the organisation alone, through the definition's own id
// (go/internal/pipelineapi/pipelineapi.go mounts them with mountOrganisation,
// not the four-way mountScoped every other pipeline route uses), so there is
// no selector to resolve.

import (
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

func newPipelineDefinitionsCommand() *cobra.Command {
	definitionsCommand := &cobra.Command{
		Use:   "definitions",
		Short: "Inspect and approve a pipeline definition's protected authority",
		Long: `Read and grant an administrator's approval of one stored pipeline
definition's protected sections: the authority-bearing parts of a pipeline -
permissions, credentials, secrets scope, network tier, image policy,
runs_on, environment gates and more - that a pull request may add logic
around but never grant itself ("logic is open, authority is closed").

These two commands address one stored definition directly by its own id,
unlike 'pipeline definition get|put' which read and write the definition of
record for a repository or application. There is no lookup or listing route
for a definition's id: find one from a pull request's status comment, or from
the "authority_definition_id" field 'ankra pipeline get' prints on a run
whose authority changed.`,
	}
	definitionsCommand.AddCommand(newPipelineDefinitionsGetCommand(), newPipelineDefinitionsApproveCommand())
	return definitionsCommand
}

func newPipelineDefinitionsGetCommand() *cobra.Command {
	getCommand := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one pipeline definition's protected-authority approval state",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			return runPipelineDefinitionsGet(command, strings.TrimSpace(arguments[0]))
		},
	}
	registerStructuredOutputFlags(getCommand)
	return getCommand
}

func runPipelineDefinitionsGet(command *cobra.Command, definitionID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	approval, getError := apiClient.GetPipelineDefinitionApproval(command.Context(), definitionID)
	if getError != nil {
		return getError
	}
	if approval == nil {
		return fmt.Errorf("the server answered no approval state for definition %s", definitionID)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, approval)
	}
	printPipelineDefinitionApproval(command.OutOrStdout(), *approval)
	return nil
}

func newPipelineDefinitionsApproveCommand() *cobra.Command {
	approveCommand := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve a pipeline definition's protected sections as trusted authority",
		Long: `Record an administrator's approval of one stored pipeline definition's
protected sections. Requires the pipelines.manage permission and a human
actor - a service-account or agent token is refused.

Approving grants whatever permissions, credentials, secrets scope, network
tier and other protected sections the definition declares - inspect them
first with 'ankra pipeline definitions get <id>' if you have not, since this
command prints only the protected-sections hash, not their content. Confirms
before approving; --yes skips the prompt for scripted use.

Only the repository's CURRENT default-branch definition can be approved, and
only once: approving a definition the default branch has since moved past,
or one already approved, answers a 409 this command prints verbatim rather
than treating as success. A definition recorded before its protected
sections were ever hashed answers the same 409.`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			return runPipelineDefinitionsApprove(command, strings.TrimSpace(arguments[0]))
		},
	}
	registerStructuredOutputFlags(approveCommand)
	approveCommand.Flags().Bool("yes", false, "Skip the confirmation prompt")
	return approveCommand
}

func runPipelineDefinitionsApprove(command *cobra.Command, definitionID string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	skipConfirmation, _ := command.Flags().GetBool("yes")
	confirmMessage := fmt.Sprintf("Approve pipeline definition %s as trusted authority? This grants whatever "+
		"permissions, credentials and network access its protected sections declare. [y/N] ", definitionID)
	if confirmError := confirmPrompt(command.InOrStdin(), command.OutOrStdout(), confirmMessage, skipConfirmation); confirmError != nil {
		return confirmError
	}
	approval, approveError := apiClient.ApprovePipelineDefinition(command.Context(), definitionID)
	if approveError != nil {
		return approveError
	}
	if approval == nil {
		return fmt.Errorf("the server answered no approval state for definition %s", definitionID)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, approval)
	}
	_, _ = fmt.Fprintf(command.OutOrStdout(), "Approved pipeline definition %s at protected hash %s.\n",
		approval.DefinitionID, approval.ProtectedHash)
	return nil
}

// printPipelineDefinitionApproval renders one definition's approval state:
// its own id, the identity of its protected sections, and who approved them
// and when, if anyone has.
func printPipelineDefinitionApproval(out io.Writer, approval client.PipelineDefinitionApproval) {
	_, _ = fmt.Fprintf(out, "Definition:     %s\n", approval.DefinitionID)
	_, _ = fmt.Fprintf(out, "Protected hash: %s\n", pipelineOptionalPlainString(approval.ProtectedHash))
	_, _ = fmt.Fprintf(out, "Approved hash:  %s\n", pipelineOptionalPlainString(approval.ApprovedHash))
	_, _ = fmt.Fprintf(out, "Approved by:    %s\n", pipelineOptionalPlainString(approval.ApprovedBy))
	_, _ = fmt.Fprintf(out, "Approved at:    %s\n", pipelineOptionalString(approval.ApprovedAt))
	_, _ = fmt.Fprintln(out)
	switch {
	case approval.ProtectedHash == "":
		_, _ = fmt.Fprintln(out, "This definition's protected sections have not been assessed yet: it predates "+
			"protected-hash tracking, and cannot be approved until a newer definition is recorded.")
	case approval.ApprovedHash == approval.ProtectedHash:
		_, _ = fmt.Fprintln(out, "Approved: this definition's protected sections are trusted authority.")
	default:
		_, _ = fmt.Fprintf(out, "Not approved: run 'ankra pipeline definitions approve %s' to grant it authority "+
			"(requires pipelines.manage and a human actor).\n", approval.DefinitionID)
	}
}

// pipelineOptionalPlainString renders an already-resolved string for a
// detail line, "-" for empty - the plain-string sibling of
// pipelineOptionalString for a field that is not itself a pointer.
func pipelineOptionalPlainString(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

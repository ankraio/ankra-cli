package cmd

import (
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// The cross-organisation suggestion review queue: proposers submit a draft
// against another organisation's public profile ('drafts submit-suggestion'),
// the owning organisation reviews it here. Approve publishes the frozen
// payload as the profile's next version; reject and withdraw close it.

var stackProfilesSuggestionsCmd = &cobra.Command{
	Use:   "suggestions",
	Short: "Review community suggestions on a profile",
	Long: `Review the cross-organisation suggestions submitted against a profile.
Approving one publishes its frozen contents as the profile's next version.
Withdraw is the proposer's verb for a suggestion their organisation submitted.`,
}

var stackProfilesSuggestionsListCmd = &cobra.Command{
	Use:   "list [profile-id|profile-name]",
	Short: "List the suggestions visible to your organisation on a profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, listError := apiClient.ListStackProfileSuggestions(cmd.Context(), profileID)
		if listError != nil {
			return fmt.Errorf("listing stack profile suggestions: %w", listError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesSuggestionsGetCmd = &cobra.Command{
	Use:   "get [profile-id|profile-name] <suggestion-id>",
	Short: "Show one suggestion's contents and upstream drift",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, getError := apiClient.GetStackProfileSuggestion(cmd.Context(), profileID,
			strings.TrimSpace(args[1]))
		if getError != nil {
			return fmt.Errorf("getting stack profile suggestion: %w", getError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesSuggestionsApproveCmd = &cobra.Command{
	Use:   "approve [profile-id|profile-name] <suggestion-id>",
	Short: "Approve a suggestion, publishing it as the profile's next version",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		channel, _ := cmd.Flags().GetString("channel")
		request := client.ApproveStackProfileSuggestionRequest{
			Channel:   channel,
			Changelog: optionalString(cmd, "changelog"),
		}
		payload, approveError := apiClient.ApproveStackProfileSuggestion(cmd.Context(), profileID,
			strings.TrimSpace(args[1]), request)
		if approveError != nil {
			return fmt.Errorf("approving stack profile suggestion: %w", approveError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesSuggestionsRejectCmd = &cobra.Command{
	Use:   "reject [profile-id|profile-name] <suggestion-id>",
	Short: "Reject a suggestion, optionally with a note the proposer can read",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		note, _ := cmd.Flags().GetString("note")
		payload, rejectError := apiClient.RejectStackProfileSuggestion(cmd.Context(), profileID,
			strings.TrimSpace(args[1]), note)
		if rejectError != nil {
			return fmt.Errorf("rejecting stack profile suggestion: %w", rejectError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesSuggestionsWithdrawCmd = &cobra.Command{
	Use:   "withdraw [profile-id|profile-name] <suggestion-id>",
	Short: "Withdraw a suggestion your organisation submitted",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, withdrawError := apiClient.WithdrawStackProfileSuggestion(cmd.Context(), profileID,
			strings.TrimSpace(args[1]))
		if withdrawError != nil {
			return fmt.Errorf("withdrawing stack profile suggestion: %w", withdrawError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

func init() {
	registerStructuredOutputFlags(stackProfilesSuggestionsListCmd)
	registerStructuredOutputFlags(stackProfilesSuggestionsGetCmd)

	stackProfilesSuggestionsApproveCmd.Flags().String("channel", "stable", "Release channel for the published version")
	stackProfilesSuggestionsApproveCmd.Flags().String("changelog", "", "Changelog note (defaults to one composed from the suggestion)")
	registerStructuredOutputFlags(stackProfilesSuggestionsApproveCmd)

	stackProfilesSuggestionsRejectCmd.Flags().String("note", "", "Resolution note the proposer can read")
	registerStructuredOutputFlags(stackProfilesSuggestionsRejectCmd)

	registerStructuredOutputFlags(stackProfilesSuggestionsWithdrawCmd)

	stackProfilesSuggestionsCmd.AddCommand(
		stackProfilesSuggestionsListCmd,
		stackProfilesSuggestionsGetCmd,
		stackProfilesSuggestionsApproveCmd,
		stackProfilesSuggestionsRejectCmd,
		stackProfilesSuggestionsWithdrawCmd,
	)
	stackProfilesCmd.AddCommand(stackProfilesSuggestionsCmd)
}

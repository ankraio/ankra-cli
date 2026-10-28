package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// The cross-organisation share commands: grant another organisation
// read-and-deploy access to a profile without making it public, list the
// grants, and revoke one. Only organisation admins can grant or revoke.

var stackProfilesShareCmd = &cobra.Command{
	Use:   "share",
	Short: "Share a profile with specific organisations without making it public",
	Long: `Share a profile with specific organisations. Shared organisations can view
and deploy every version but cannot change the profile. Ask the other
organisation for their slug - admins find it under organisation settings.`,
}

var stackProfilesShareListCmd = &cobra.Command{
	Use:   "list [profile-id|profile-name]",
	Short: "List the organisations a profile is shared with",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, listError := apiClient.ListStackProfileShares(cmd.Context(), profileID)
		if listError != nil {
			return fmt.Errorf("listing stack profile shares: %w", listError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesShareAddCmd = &cobra.Command{
	Use:     "add [profile-id|profile-name] <organisation-slug>",
	Short:   "Share a profile with another organisation",
	Example: "  ankra stack-profiles share add postgres-ha acme-corp",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, shareError := apiClient.CreateStackProfileShare(cmd.Context(), profileID,
			strings.TrimSpace(args[1]))
		if shareError != nil {
			return fmt.Errorf("sharing stack profile: %w", shareError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesShareRemoveCmd = &cobra.Command{
	Use:   "remove [profile-id|profile-name] <share-id>",
	Short: "Revoke a profile share grant",
	Long: `Revoke one share grant by its id (from 'share list'). The organisation
loses access to the profile; stacks it already deployed keep running.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, removeError := apiClient.DeleteStackProfileShare(cmd.Context(), profileID,
			strings.TrimSpace(args[1]))
		if removeError != nil {
			return fmt.Errorf("revoking stack profile share: %w", removeError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

func init() {
	registerStructuredOutputFlags(stackProfilesShareListCmd)
	registerStructuredOutputFlags(stackProfilesShareAddCmd)
	registerStructuredOutputFlags(stackProfilesShareRemoveCmd)
	stackProfilesShareCmd.AddCommand(
		stackProfilesShareListCmd,
		stackProfilesShareAddCmd,
		stackProfilesShareRemoveCmd,
	)
	stackProfilesCmd.AddCommand(stackProfilesShareCmd)
}

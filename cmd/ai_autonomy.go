package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var aiAutonomyCmd = &cobra.Command{
	Use:   "autonomy",
	Short: "Read and operate the organisation's AI kill switch",
	Long: `Read and operate the organisation-wide AI kill switch.

Engaging it stops all AI for everyone in the organisation: running sessions
and agent runs are cancelled, and chat is refused until it is released. Reach
for it during an incident, not to tighten a policy - a narrower stop lives in
the auto-remediation policy.`,
}

var aiAutonomyStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether AI is stopped for the organisation",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		state, err := apiClient.GetAIPauseState()
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, state); handled || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		if !state.Paused {
			_, _ = fmt.Fprintln(out, "AI is available for this organisation.")
			return nil
		}
		_, _ = fmt.Fprintln(out, "AI is STOPPED for this organisation.")
		if state.PausedAt != nil {
			_, _ = fmt.Fprintf(out, "Stopped at: %s\n", *state.PausedAt)
		}
		if state.PausedBy != nil && *state.PausedBy != "" {
			_, _ = fmt.Fprintf(out, "Stopped by: %s\n", *state.PausedBy)
		}
		if state.Reason != nil && *state.Reason != "" {
			_, _ = fmt.Fprintf(out, "Reason:     %s\n", *state.Reason)
		}
		_, _ = fmt.Fprintln(out, "\nRelease it with: ankra ai autonomy resume")
		return nil
	},
}

var aiAutonomyPauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Stop all AI for the organisation",
	Long: `Stop all AI for everyone in the organisation.

Every running AI session and agent run is cancelled, pending approvals
expire, and chat is refused until the switch is released. The reason is
shown to administrators alongside the switch.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		reason, _ := cmd.Flags().GetString("reason")
		skipConfirm, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			"Stop all AI for this organisation? Running sessions and agent runs are cancelled. [y/N]: ",
			skipConfirm); err != nil {
			return err
		}
		outcome, err := apiClient.SetAIPause(true, strings.TrimSpace(reason))
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, outcome); handled || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"AI is stopped for this organisation. Cancelled %d session(s) and %d agent run(s).\n",
			outcome.CancelledSessions, outcome.CancelledRuns)
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Release it with: ankra ai autonomy resume")
		return nil
	},
}

var aiAutonomyResumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Let AI run again for the organisation",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		outcome, err := apiClient.SetAIPause(false, "")
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, outcome); handled || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "AI is available for this organisation again.")
		return nil
	},
}

var aiBoardIdentityCmd = &cobra.Command{
	Use:   "board-identity",
	Short: "Manage the identity the AI board's agents act as",
	Long: `Manage the organisation's board agent identity.

Agents the platform created - the preloaded team - have no user account of
their own, so their headless runs cannot change anything. The board identity
gives them one, with its own role, so their work is attributable to the agent
rather than to whoever set it up. It cannot sign in and has no token.

Without it, a designated board worker is escalated to a human on every ticket
instead of working it.`,
}

var aiBoardIdentityStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether the board has an identity",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		identity, err := apiClient.GetBoardIdentity()
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, identity); handled || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		if !identity.Provisioned {
			_, _ = fmt.Fprintln(out, "The board has no identity, so its agents cannot work tickets.")
			_, _ = fmt.Fprintln(out, "Give it one with: ankra ai board-identity provision")
			return nil
		}
		_, _ = fmt.Fprintln(out, "The board has an identity.")
		_, _ = fmt.Fprintf(out, "Role:    %s\n", identity.RoleSlug)
		_, _ = fmt.Fprintf(out, "Subject: %s\n", identity.Subject)
		if identity.AnkraUserID != nil {
			_, _ = fmt.Fprintf(out, "User ID: %s\n", *identity.AnkraUserID)
		}
		return nil
	},
}

var aiBoardIdentityProvisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Give the board an identity its agents act as",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		role, _ := cmd.Flags().GetString("role")
		identity, err := apiClient.ProvisionBoardIdentity(strings.TrimSpace(role))
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, identity); handled || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"The board now acts as its own identity, with the %s role.\n", identity.RoleSlug)
		_, _ = fmt.Fprintln(cmd.OutOrStdout(),
			"Designated board workers can work tickets from the next dispatcher pass.")
		return nil
	},
}

var aiBoardIdentityRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Stand the board identity down",
	Long: `Stand the board identity down.

The principal itself is kept so its past actions stay attributable; what it
loses is every grant above the viewer floor, which makes the board's agents
ineligible again on the next dispatcher pass.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		skipConfirm, _ := cmd.Flags().GetBool("yes")
		if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			"Stand the board identity down? Designated agents stop working tickets. [y/N]: ",
			skipConfirm); err != nil {
			return err
		}
		identity, err := apiClient.RevokeBoardIdentity()
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, identity); handled || renderError != nil {
			return renderError
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(),
			"The board identity is stood down. Its agents escalate to a human until it is provisioned again.")
		return nil
	},
}

func init() {
	aiAutonomyPauseCmd.Flags().String("reason", "", "Why AI is being stopped (shown to administrators)")
	aiAutonomyPauseCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	aiBoardIdentityRevokeCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	aiBoardIdentityProvisionCmd.Flags().String("role", "",
		"Role the identity carries: operator (default), member, or viewer")
	registerStructuredOutputFlags(aiAutonomyStatusCmd, aiAutonomyPauseCmd, aiAutonomyResumeCmd,
		aiBoardIdentityStatusCmd, aiBoardIdentityProvisionCmd, aiBoardIdentityRevokeCmd)

	aiAutonomyCmd.AddCommand(aiAutonomyStatusCmd, aiAutonomyPauseCmd, aiAutonomyResumeCmd)
	aiBoardIdentityCmd.AddCommand(aiBoardIdentityStatusCmd, aiBoardIdentityProvisionCmd, aiBoardIdentityRevokeCmd)
	aiCmd.AddCommand(aiAutonomyCmd, aiBoardIdentityCmd)
}

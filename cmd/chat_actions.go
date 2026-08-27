package cmd

// `ankra chat actions ...`: the terminal half of agent-mode write approval.
// In agent mode every tool flagged requires_confirmation halts the turn and
// emits an `action_proposal` frame; the write only runs once the proposal is
// confirmed. These commands are how that confirmation is given without a
// browser.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

var chatActionsCmd = &cobra.Command{
	Use:   "actions",
	Short: "Review and confirm AI actions awaiting approval",
	Long: `Agent-mode chat halts before every write and proposes it as a pending
action. Confirm the action to run it, or reject it to discard it.

Pending actions expire, so confirm promptly. If the cluster changed since the
action was proposed, confirming answers a drift conflict; re-run with --force
to apply it against the changed state anyway.`,
}

var chatActionsConfirmCmd = &cobra.Command{
	Use:   "confirm <action_id>",
	Short: "Confirm a pending AI action so it runs",
	Long: `Confirm a pending action proposed by agent-mode chat.

The platform re-checks the action against live cluster state before running
it. If the state drifted since the proposal, the command reports the drift and
exits without running anything; pass --force to run it anyway.`,
	Example: `  # Confirm the action id printed by the chat stream
  ankra chat actions confirm 6f1c2f9e-6d5a-4a1e-9f0b-6d4c2b8a1e77

  # Apply it even though the cluster drifted since it was proposed
  ankra chat actions confirm 6f1c2f9e-6d5a-4a1e-9f0b-6d4c2b8a1e77 --force \
    --force-reason "drift is an unrelated label change"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		forceReason, _ := cmd.Flags().GetString("force-reason")
		return runChatActionDecision(cmd, args[0], true, force, forceReason, "")
	},
}

var chatActionsRejectCmd = &cobra.Command{
	Use:   "reject <action_id>",
	Short: "Reject a pending AI action so it never runs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reason, _ := cmd.Flags().GetString("reason")
		return runChatActionDecision(cmd, args[0], false, false, "", reason)
	},
}

var chatActionsListCmd = &cobra.Command{
	Use:   "list <conversation_id>",
	Short: "List the actions awaiting confirmation in a conversation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, listError := apiClient.ListPendingChatActions(args[0])
		if listError != nil {
			return listError
		}
		if handled, renderError := renderStructured(cmd, result); renderError != nil {
			return renderError
		} else if handled {
			return nil
		}
		if result.Count == 0 {
			fmt.Println("No actions awaiting confirmation.")
			return nil
		}
		for _, action := range result.Actions {
			fmt.Printf("%-38s %-28s %s\n",
				mapString(action, "action_id"),
				mapString(action, "tool_name"),
				mapString(action, "description"))
		}
		fmt.Printf("\n%d action(s) awaiting confirmation.\n", result.Count)
		return nil
	},
}

// runChatActionDecision performs the confirm/reject call and renders the
// answer, translating a drift conflict into the --force hint.
func runChatActionDecision(cmd *cobra.Command, actionID string, confirmed, force bool,
	forceReason, reason string) error {
	request := client.ConfirmChatActionRequest{ActionID: actionID, Confirmed: confirmed}
	if force {
		request.Force = &force
	}
	if forceReason != "" {
		request.ForceReason = &forceReason
	}
	if reason != "" {
		request.Reason = &reason
	}

	result, confirmError := apiClient.ConfirmChatAction(request)
	if confirmError != nil {
		var conflict *client.ActionConflictError
		if errors.As(confirmError, &conflict) {
			return chatActionConflictError(conflict, actionID)
		}
		return confirmError
	}

	if handled, renderError := renderStructured(cmd, result); renderError != nil {
		return renderError
	} else if handled {
		return nil
	}

	if !confirmed {
		fmt.Printf("Action %s rejected; it will not run.\n", actionID)
		return nil
	}
	fmt.Printf("Action %s confirmed.\n", actionID)
	if result.Message != "" {
		fmt.Println(result.Message)
	}
	if result.Status != "" {
		fmt.Printf("Status: %s\n", result.Status)
	}
	return nil
}

// chatActionConflictError renders a 409 as a usable next step: drift is
// recoverable with --force, a supersede never is.
func chatActionConflictError(conflict *client.ActionConflictError, actionID string) error {
	if conflict.IsDrift() {
		return fmt.Errorf(
			"%s\n\nThe cluster changed since this action was proposed, so it was not run.\n"+
				"Review the change, then re-run with --force to apply it anyway:\n"+
				"  ankra chat actions confirm %s --force",
			conflict.Error(), actionID)
	}
	return fmt.Errorf(
		"%s\n\nA newer action replaced this one, so it can no longer be confirmed.\n"+
			"Ask the assistant again to get a fresh proposal", conflict.Error())
}

// renderActionProposal prints an `action_proposal` frame. Agent-mode writes
// halt on this frame, so it must always be shown - a dropped proposal looks
// like the assistant silently ignored the request.
func renderActionProposal(proposal *client.ChatActionProposal) {
	fmt.Println()
	fmt.Println("── Action awaiting confirmation ──────────────────────────────")
	fmt.Printf("  Tool:        %s\n", proposal.ToolName)
	if proposal.Description != "" {
		fmt.Printf("  Description: %s\n", proposal.Description)
	}
	fmt.Printf("  Risk:        %s", proposal.RiskLevel)
	if proposal.Reversible {
		fmt.Print(" (reversible)")
	} else {
		fmt.Print(" (NOT reversible)")
	}
	fmt.Println()
	if parameters := formatProposalParameters(proposal.Parameters); parameters != "" {
		fmt.Printf("  Parameters:  %s\n", parameters)
	}
	if proposal.ExpiresInSeconds > 0 {
		fmt.Printf("  Expires in:  %ds\n", proposal.ExpiresInSeconds)
	}
	fmt.Printf("  Action ID:   %s\n", proposal.ActionID)
	fmt.Println("──────────────────────────────────────────────────────────────")
}

// formatProposalParameters renders the tool arguments on one line, keeping
// the server's key order.
func formatProposalParameters(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var decoded map[string]any
	if unmarshalError := json.Unmarshal(raw, &decoded); unmarshalError != nil {
		return strings.TrimSpace(string(raw))
	}
	if len(decoded) == 0 {
		return ""
	}
	compact, marshalError := json.Marshal(decoded)
	if marshalError != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(compact)
}

// decodeActionProposal converts the SSE frame's `data` member into the typed
// proposal. The stream decodes frames into `any`, so this round-trips.
func decodeActionProposal(data any) (*client.ChatActionProposal, error) {
	encoded, marshalError := json.Marshal(data)
	if marshalError != nil {
		return nil, marshalError
	}
	var proposal client.ChatActionProposal
	if unmarshalError := json.Unmarshal(encoded, &proposal); unmarshalError != nil {
		return nil, unmarshalError
	}
	if proposal.ActionID == "" {
		return nil, errors.New("action proposal carried no action_id")
	}
	return &proposal, nil
}

// resolvePendingProposals walks the proposals a turn halted on and asks the
// user to decide each one, so an interactive session never has to leave the
// prompt to approve a write. Answering anything but yes rejects the action,
// which is the safe default for a mutation.
func resolvePendingProposals(reader *bufio.Reader, proposals []*client.ChatActionProposal) {
	for _, proposal := range proposals {
		renderActionProposal(proposal)
		fmt.Print("Run this action? [y/N]: ")
		answer, readError := reader.ReadString('\n')
		if readError != nil && readError != io.EOF {
			fmt.Printf("Could not read your answer (%v); leaving the action pending.\n", readError)
			fmt.Printf("Confirm it later with: ankra chat actions confirm %s\n", proposal.ActionID)
			return
		}
		confirmed := isAffirmative(answer)

		result, confirmError := apiClient.ConfirmChatAction(client.ConfirmChatActionRequest{
			ActionID:  proposal.ActionID,
			Confirmed: confirmed,
		})
		if confirmError != nil {
			var conflict *client.ActionConflictError
			if errors.As(confirmError, &conflict) {
				fmt.Printf("%v\n", chatActionConflictError(conflict, proposal.ActionID))
				continue
			}
			fmt.Printf("Could not resolve the action: %v\n", confirmError)
			continue
		}
		if !confirmed {
			fmt.Printf("Rejected; %s will not run.\n\n", proposal.ToolName)
			continue
		}
		fmt.Printf("Confirmed; %s is running.\n", proposal.ToolName)
		if result != nil && result.Message != "" {
			fmt.Println(result.Message)
		}
		fmt.Println()
	}
}

func isAffirmative(answer string) bool {
	normalised := strings.ToLower(strings.TrimSpace(answer))
	return normalised == "y" || normalised == "yes"
}

func mapString(source map[string]any, key string) string {
	if value, present := source[key]; present {
		if text, isText := value.(string); isText {
			return text
		}
	}
	return "-"
}

func init() {
	chatActionsConfirmCmd.Flags().Bool("force", false,
		"Confirm even though cluster state drifted since the action was proposed")
	chatActionsConfirmCmd.Flags().String("force-reason", "",
		"Why the drift is acceptable (recorded in the audit trail, max 280 characters)")
	chatActionsRejectCmd.Flags().String("reason", "", "Why the action was rejected")

	registerStructuredOutputFlags(chatActionsConfirmCmd, chatActionsRejectCmd, chatActionsListCmd)
	chatActionsCmd.AddCommand(chatActionsConfirmCmd, chatActionsRejectCmd, chatActionsListCmd)
	chatCmd.AddCommand(chatActionsCmd)
}

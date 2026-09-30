package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// chatOneShotResult is the document `ankra chat -o json "<question>"` prints
// once the turn ends, so a CI job can parse the answer instead of scraping a
// streamed transcript (ankra-gtpc1). Every member comes from the turn itself:
// the session the platform opened, the scope the turn ran in, and the
// frames it streamed.
type chatOneShotResult struct {
	// ConversationID continues the conversation with --conversation when
	// Continuable is true. On a backend without durable chat sessions the id
	// is local to this run.
	ConversationID string `json:"conversation_id" yaml:"conversation_id"`
	Continuable    bool   `json:"continuable" yaml:"continuable"`
	SessionID      string `json:"session_id,omitempty" yaml:"session_id,omitempty"`
	// Mode is the safety mode the turn ran in: the one the platform
	// resolved for the session ("ask", "agent" or "plan"), or on the
	// deprecated stream the --mode asked for. Empty when neither is known.
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
	// ClusterID is null for an organisation-wide turn, including one whose
	// selected cluster no longer exists.
	ClusterID   *string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName string  `json:"cluster_name,omitempty" yaml:"cluster_name,omitempty"`
	Answer      string  `json:"answer" yaml:"answer"`
	// ToolCalls are the tools the model ran, in the order they started.
	ToolCalls []chatToolCall `json:"tool_calls" yaml:"tool_calls"`
	// PendingActions are writes the model proposed that have NOT run: each
	// waits for `ankra chat actions confirm|reject <action_id>`.
	PendingActions []*client.ChatActionProposal `json:"pending_actions" yaml:"pending_actions"`
	// Error is the turn's failure, if it failed; the command then exits
	// non-zero after printing the document.
	Error string `json:"error,omitempty" yaml:"error,omitempty"`
}

// runChatMessageStructured runs one question and prints its outcome as one
// JSON or YAML document on stdout. Nothing else reaches stdout: notices (a
// stale selection, stripped hidden runes, an error frame) go to stderr.
func runChatMessageStructured(cmd *cobra.Command, scope chatScope, conversationID string, query string, interactionMode string) error {
	errOut := cmd.ErrOrStderr()
	conversationID, startedConversation, query, err := prepareOneShot(conversationID, query, errOut)
	if err != nil {
		return err
	}
	req := client.ChatRequest{Query: query, InteractionMode: interactionMode}
	events, session, scope, err := openChatTurn(scope, conversationID, req, interactionMode, errOut)
	if err != nil {
		// No turn exists to describe, so no document: the error (and its exit
		// code - 6 for a rejected token, 3 for an unknown cluster) is the
		// answer, on stderr like every other failure.
		return fmt.Errorf("chat: %w", err)
	}

	// Collecting the proposals (rather than rendering them) keeps the
	// confirmation guidance off stdout; they are in the document instead.
	outcome := renderChatTurn(events, io.Discard, errOut, true)
	if session != nil && startedConversation && outcome.errorMessage == "" {
		requestConversationTitle(conversationID)
	}
	if session == nil && !startedConversation {
		legacyLaneNotice(errOut, conversationID, true)
	}

	result := newChatOneShotResult(conversationID, session, scope, interactionMode, outcome)
	if _, renderErr := renderStructured(cmd, result); renderErr != nil {
		return renderErr
	}
	if outcome.errorMessage != "" {
		return errors.New(outcome.errorMessage)
	}
	return nil
}

func newChatOneShotResult(conversationID string, session *client.ChatSession, scope chatScope,
	interactionMode string, outcome chatTurnOutcome) chatOneShotResult {
	result := chatOneShotResult{
		ConversationID: conversationID,
		Continuable:    session != nil,
		Mode:           sessionModeForInteraction(interactionMode),
		ClusterID:      scope.clusterID,
		Answer:         outcome.response,
		ToolCalls:      outcome.toolCalls,
		PendingActions: outcome.proposals,
		Error:          outcome.errorMessage,
	}
	if scope.clusterID != nil {
		result.ClusterName = scope.clusterName
	}
	if session != nil {
		result.SessionID = session.ID
		if session.Mode != "" {
			result.Mode = session.Mode
		}
	}
	// Empty lists print as [] rather than null, so `jq '.tool_calls | length'`
	// needs no null guard.
	if result.ToolCalls == nil {
		result.ToolCalls = []chatToolCall{}
	}
	if result.PendingActions == nil {
		result.PendingActions = []*client.ChatActionProposal{}
	}
	return result
}

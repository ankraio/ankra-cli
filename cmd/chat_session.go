package cmd

// One chat turn on the durable sessions lane (ankra-y8l44.2): create a
// session on the conversation, submit the turn, tail the event log until the
// terminal frame. The backend owns the transcript, so a conversation id is
// all the CLI carries between turns - no more resending the whole history
// every interactive turn, and a one-shot answer names the conversation it
// started so the next `ankra chat --conversation <id>` continues it. A
// backend without the lane (older self-hosted platforms) falls back to the
// deprecated stream transparently.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"ankra/internal/client"
)

// chatTailMaxReconnects bounds how many times a dropped event tail is
// re-opened from its last durable sequence before the turn is given up.
const chatTailMaxReconnects = 3

// chatTailReconnectDelay is the base back-off between tail re-opens; a var
// so tests do not wait.
var chatTailReconnectDelay = time.Second

// chatScope is the cluster context a chat runs in and where it came from:
// a persisted `ankra cluster select` is advisory (a deleted cluster degrades
// to the global lane with a notice), an explicit --cluster is not.
type chatScope struct {
	clusterID     *string
	fromSelection bool
	selectedName  string
}

// chatTurnOutcome is what one rendered turn leaves behind.
type chatTurnOutcome struct {
	response     string
	proposals    []*client.ChatActionProposal
	errorMessage string
}

// newChatUUID mints a random v4 UUID for conversation ids and idempotency
// keys without a UUID dependency.
func newChatUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

// sessionModeForInteraction maps the turn's interaction_mode onto the
// session safety-mode literal; empty leaves the server default.
func sessionModeForInteraction(interactionMode string) string {
	switch interactionMode {
	case "ask":
		return "ask"
	case "agentic":
		return "agent"
	case "plan":
		return "plan"
	}
	return ""
}

// validateConversationID checks a user-supplied --conversation value
// against the backend's constraints (1-128 characters, no whitespace).
func validateConversationID(conversationID string) error {
	trimmed := strings.TrimSpace(conversationID)
	if trimmed == "" || len(trimmed) > 128 || strings.ContainsAny(trimmed, " \t\r\n") {
		return fmt.Errorf("invalid --conversation %q: pass a conversation id from 'ankra chat history'", conversationID)
	}
	return nil
}

// startChatTurn opens one turn and returns its event stream. The sessions
// lane is tried first; a backend that does not serve it gets the deprecated
// stream with the same request. The returned bool reports the sessions lane
// (so callers know the conversation id is the server's too).
func startChatTurn(conversationID string, clusterID *string, req client.ChatRequest,
	interactionMode string) (<-chan client.ChatStreamEvent, bool, error) {
	idempotencyKey, keyErr := newChatUUID()
	if keyErr != nil {
		return nil, false, keyErr
	}
	session, err := apiClient.CreateChatSession(client.CreateChatSessionRequest{
		ConversationID: conversationID,
		ClusterID:      clusterID,
		Mode:           sessionModeForInteraction(interactionMode),
		IdempotencyKey: idempotencyKey,
	})
	if errors.Is(err, client.ErrChatSessionsUnavailable) {
		events, legacyErr := apiClient.StreamChat(clusterID, req)
		return events, false, legacyErr
	}
	if err != nil {
		return nil, false, err
	}
	// The transcript is server-owned on this lane: send the conversation
	// id, never the history.
	req.ConversationID = &conversationID
	req.ConversationHistory = nil
	submitted, err := apiClient.SubmitChatTurn(session.ID, req)
	if err != nil {
		// The session opened two calls ago now carries no turn: release it
		// instead of leaving it pending until it expires. Best-effort - the
		// submit error is the one to report.
		_ = apiClient.CancelChatSession(session.ID)
		return nil, true, err
	}
	return tailChatSession(session.ID, submitted.LastSeq), true, nil
}

// retryableTailError reports whether a failed tail open is worth re-opening:
// a transport failure or a 5xx is; a 4xx (the token rejected, the session
// or the lane gone) answers the same way every time.
func retryableTailError(err error) bool {
	if errors.Is(err, client.ErrUnauthorized) || errors.Is(err, client.ErrChatSessionsUnavailable) ||
		errors.Is(err, client.ErrClusterNotFound) {
		return false
	}
	var unexpected *client.UnexpectedResponseError
	if errors.As(err, &unexpected) && unexpected.StatusCode >= 400 && unexpected.StatusCode < 500 {
		return false
	}
	return true
}

// tailChatSession follows the session's durable event log until the
// terminal "end" frame, re-opening the tail from the last sequence it saw
// when the connection drops (the runner keeps working server-side and the
// log keeps every frame, so nothing is lost across a reconnect). The
// reconnect budget bounds consecutive failures: a re-opened tail that
// delivers a durable frame has resumed, and a later drop starts over.
func tailChatSession(sessionID string, since int64) <-chan client.ChatStreamEvent {
	out := make(chan client.ChatStreamEvent, 100)
	go func() {
		defer close(out)
		reconnects := 0
		for {
			events, err := apiClient.StreamChatSessionEvents(sessionID, since)
			if err != nil {
				if reconnects < chatTailMaxReconnects && retryableTailError(err) {
					reconnects++
					time.Sleep(time.Duration(reconnects) * chatTailReconnectDelay)
					continue
				}
				out <- client.ChatStreamEvent{Type: "error", Error: err.Error()}
				return
			}
			for event := range events {
				// A local stream failure (read error, idle watchdog) carries
				// Error and no payload: that is a drop to resume from, not a
				// backend verdict to show.
				if event.Type == "error" && event.Error != "" && event.Data == nil {
					break
				}
				// The backend serves `since` exclusively (events strictly
				// after the cursor), so a durable frame at or below it can
				// only be a replay: skip it rather than render it twice.
				if event.Sequence != 0 && event.Sequence <= since {
					continue
				}
				if event.Sequence > since {
					since = event.Sequence
					reconnects = 0
				}
				out <- event
				if event.Type == "end" {
					return
				}
			}
			// Dropped, or closed cleanly without an end frame (the server
			// finished its response window): resume from the cursor.
			if reconnects >= chatTailMaxReconnects {
				out <- client.ChatStreamEvent{Type: "error",
					Error: "the reply stream dropped and could not be resumed; the turn may still finish - check 'ankra chat show'"}
				return
			}
			reconnects++
			time.Sleep(time.Duration(reconnects) * chatTailReconnectDelay)
		}
	}()
	return out
}

// legacyLaneNotice says what the deprecated stream cannot do with a
// conversation id: that backend keeps no transcript to continue, so a
// --conversation continuation runs without its earlier context, and the id
// an interactive run prints cannot be continued later.
func legacyLaneNotice(errOut io.Writer, conversationID string, continued bool) {
	if continued {
		_, _ = fmt.Fprintf(errOut, "note: this backend has no durable chat sessions; conversation %s "+
			"cannot be continued here, so this turn runs without its earlier context.\n", conversationID)
		return
	}
	_, _ = fmt.Fprintf(errOut, "note: this backend has no durable chat sessions; conversation %s "+
		"is local to this run and cannot be continued with --conversation.\n", conversationID)
}

// chatErrorDetail renders the operator-facing detail a sessions-lane error
// frame carries (the sanitised upstream error) - only when ANKRA_DEBUG is
// set, because the message is the user-facing text.
func chatErrorDetail(event client.ChatStreamEvent) string {
	data, isMap := event.Data.(map[string]any)
	if !isMap {
		return ""
	}
	var parts []string
	if code, ok := data["error_code"].(string); ok && code != "" {
		parts = append(parts, "code: "+code)
	}
	if os.Getenv("ANKRA_DEBUG") != "" {
		if detail, ok := data["detail"].(string); ok && detail != "" {
			parts = append(parts, "detail: "+detail)
		}
	}
	return strings.Join(parts, "; ")
}

// renderChatTurn prints a turn's events as they arrive and reports what the
// turn left behind. Proposals are rendered inline (one-shot) or collected
// for the caller to resolve (interactive) depending on collectProposals.
func renderChatTurn(events <-chan client.ChatStreamEvent, out io.Writer, errOut io.Writer,
	collectProposals bool) chatTurnOutcome {
	var outcome chatTurnOutcome
	var response strings.Builder
	var hasStartedContent bool
	var hadStatus bool
	printContent := func(text string) {
		if text == "" {
			return
		}
		if !hasStartedContent {
			if hadStatus {
				_, _ = fmt.Fprint(out, "\n")
			}
			hasStartedContent = true
		}
		_, _ = fmt.Fprint(out, text)
		response.WriteString(text)
	}
	printLine := func(line string) {
		if hasStartedContent {
			_, _ = fmt.Fprintf(out, "\n\n[%s]\n\n", line)
		} else {
			_, _ = fmt.Fprintf(out, "[%s]", line)
			hadStatus = true
		}
	}
	for event := range events {
		switch event.Type {
		case "content":
			if text, ok := event.Data.(string); ok {
				printContent(text)
			} else {
				printContent(event.Content)
			}
		case "status":
			if status := chatStatusText(event.Data); status != "" {
				printLine(status)
			}
		case "system_notice":
			if data, ok := event.Data.(map[string]any); ok {
				if message, ok := data["message"].(string); ok && message != "" {
					printLine("note: " + message)
				}
			}
		case "action_proposal":
			proposal, decodeError := decodeActionProposal(event.Data)
			if decodeError != nil {
				_, _ = fmt.Fprintf(out, "\nAn action is awaiting confirmation but could not be read: %v\n", decodeError)
				continue
			}
			if collectProposals {
				outcome.proposals = append(outcome.proposals, proposal)
				continue
			}
			renderActionProposal(proposal)
			_, _ = fmt.Fprintln(out, "This write has NOT run. Confirm it to proceed:")
			_, _ = fmt.Fprintf(out, "  ankra chat actions confirm %s\n", proposal.ActionID)
			_, _ = fmt.Fprintf(out, "  ankra chat actions reject %s\n\n", proposal.ActionID)
		case "error":
			message := chatErrorMessage(event)
			if detail := chatErrorDetail(event); detail != "" {
				message += " (" + detail + ")"
			}
			if outcome.errorMessage == "" {
				outcome.errorMessage = message
			}
			if collectProposals {
				_, _ = fmt.Fprintf(errOut, "\nError: %s\n", message)
			}
		case "done", "complete", "end":
			// The turn's text is complete; the session_complete/end frames
			// that follow carry no more content.
		default:
			// Triage, thinking, budgets, tool telemetry and other metadata.
		}
	}
	outcome.response = response.String()
	return outcome
}

// requestConversationTitle asks the backend to name a conversation this
// command started, once its first exchange exists - the same call the portal
// makes after the first reply. Best-effort by design: a backend without the
// route, a title the user already set, or a summariser outage all leave the
// first question as the title, and none of them is worth a line of output.
func requestConversationTitle(conversationID string) {
	_ = apiClient.RegenerateChatTitle(conversationID)
}

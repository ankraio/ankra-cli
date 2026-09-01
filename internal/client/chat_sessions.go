package client

// The durable chat sessions lane (/api/v1/chat/sessions): the successor the
// deprecated bearer stream (POST /api/v1/chat/general and the per-cluster
// twin) has advertised in its Link header since 2026-07. A turn is three
// calls: create a session bound to a conversation id, submit the turn, and
// tail the session's durable event log over SSE until the terminal "end"
// frame. The server hydrates the conversation from its own transcript, so
// the client never resends history, and every frame carries a sequence
// number the tail can resume from after a dropped connection.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ChatSession mirrors the backend's AISession (the members the CLI uses).
type ChatSession struct {
	ID                   string  `json:"id"`
	ConversationID       string  `json:"conversation_id"`
	ClusterID            *string `json:"cluster_id"`
	Mode                 string  `json:"mode"`
	Status               string  `json:"status"`
	LatestSequenceNumber int64   `json:"latest_sequence_number"`
	Error                *string `json:"error"`
}

// CreateChatSessionRequest is the POST /api/v1/chat/sessions body. Mode is
// the session safety mode ("ask", "agent", "plan"); empty leaves the server
// default. IdempotencyKey lets a retried create find its own session.
type CreateChatSessionRequest struct {
	ConversationID string  `json:"conversation_id"`
	ClusterID      *string `json:"cluster_id,omitempty"`
	Mode           string  `json:"mode,omitempty"`
	IdempotencyKey string  `json:"idempotency_key,omitempty"`
}

// SubmitChatTurnResponse mirrors the backend's SubmitTurnResponse: LastSeq
// is the sequence number of the persisted user turn, so tailing from it
// yields exactly the assistant's side of the turn.
type SubmitChatTurnResponse struct {
	SessionID string `json:"session_id"`
	LastSeq   int64  `json:"last_seq"`
}

// ErrChatSessionsUnavailable marks a backend without the bearer sessions
// lane (a 404 on the route itself, not on a resource it names), so callers
// can fall back to the deprecated stream.
var ErrChatSessionsUnavailable = errors.New("chat sessions are not available on this backend")

// liveDeltaFrame reports the id-less live-plane mirror frames the session
// tail relays ahead of the durable log (content_delta, thinking_delta,
// tool_input_delta, tool_start_live). The durable frames carry the same
// bytes, so the CLI renders those and skips the mirrors; the terminal "end"
// frame is also id-less and is the one id-less frame that matters.
func liveDeltaFrame(eventName string) bool {
	return strings.HasSuffix(eventName, "_delta") || eventName == "tool_start_live"
}

// CreateChatSession opens a session on the conversation.
func (c *Client) CreateChatSession(request CreateChatSessionRequest) (*ChatSession, error) {
	var session ChatSession
	if err := c.sendJSON(http.MethodPost, c.BaseURL+"/api/v1/chat/sessions", request, &session); err != nil {
		return nil, classifyChatSessionError(err)
	}
	return &session, nil
}

// SubmitChatTurn persists the user turn on the session; the runner picks it
// up and the events tail carries the reply.
func (c *Client) SubmitChatTurn(sessionID string, request ChatRequest) (*SubmitChatTurnResponse, error) {
	payload := struct {
		Request ChatRequest `json:"request"`
	}{Request: request}
	var response SubmitChatTurnResponse
	if err := c.sendJSON(http.MethodPost,
		fmt.Sprintf("%s/api/v1/chat/sessions/%s/turns", c.BaseURL, url.PathEscape(sessionID)),
		payload, &response); err != nil {
		return nil, classifyChatSessionError(err)
	}
	return &response, nil
}

// CancelChatSession asks the runner to stop the session's in-flight turn.
func (c *Client) CancelChatSession(sessionID string) error {
	return classifyChatSessionError(c.sendJSON(http.MethodPost,
		fmt.Sprintf("%s/api/v1/chat/sessions/%s/cancel", c.BaseURL, url.PathEscape(sessionID)), nil, nil))
}

// classifyChatSessionError maps the two 404s the sessions lane can answer
// onto sentinel errors: the route missing altogether (older backend) and a
// cluster the backend does not know. Everything else passes through with
// the backend's detail (allowance exhausted, rate limited, AI paused).
func classifyChatSessionError(err error) error {
	if err == nil {
		return nil
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		return err
	}
	// "Cluster not found" is the literal the backend writes for an unknown
	// or foreign cluster on this route family (chatapi's
	// verifyClusterInOrganisation and authoriseSessionOr404); it carries no
	// error_code, so the text is the only signal. The other 404 the family
	// answers is the route itself, on a backend that predates the lane.
	if strings.Contains(strings.ToLower(err.Error()), "cluster not found") {
		return fmt.Errorf("%w: %v", ErrClusterNotFound, err)
	}
	return fmt.Errorf("%w: %v", ErrChatSessionsUnavailable, err)
}

// StreamChatSessionEvents tails the session's event log from the given
// sequence number (exclusive) until the terminal "end" frame or the
// connection drops; the channel closes either way. Every durable frame is
// delivered as a ChatStreamEvent whose Type is the SSE event name, Data the
// decoded JSON payload, and Sequence the frame's id; id-less live-plane
// mirrors are skipped. Heartbeat comments reset the same idle watchdog the
// legacy stream uses.
func (c *Client) StreamChatSessionEvents(sessionID string, since int64) (<-chan ChatStreamEvent, error) {
	eventsURL := fmt.Sprintf("%s/api/v1/chat/sessions/%s/events?stream=true&since=%d",
		c.BaseURL, url.PathEscape(sessionID), since)
	httpReq, err := http.NewRequest(http.MethodGet, eventsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.StreamingHTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, readErr := readResponseBody(resp)
		closeBody(resp)
		if readErr != nil {
			return nil, fmt.Errorf("read response: %w", readErr)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, ErrUnauthorized
		}
		if detail := detailFromBody(body); detail != "" {
			return nil, classifyChatSessionError(newUnexpectedResponseErrorWithMessage(resp.StatusCode, detail))
		}
		return nil, classifyChatSessionError(newUnexpectedResponseError("chat events failed", resp.StatusCode,
			redactedBodyForError(body, 500)))
	}

	events := make(chan ChatStreamEvent, 100)
	go func() {
		defer closeBody(resp)
		defer close(events)

		var idleTimedOut atomic.Bool
		watchdog := time.AfterFunc(chatStreamIdleTimeout, func() {
			idleTimedOut.Store(true)
			closeBody(resp)
		})
		defer watchdog.Stop()

		reader := bufio.NewReader(resp.Body)
		var frame sseFrame
		for {
			line, readErr := reader.ReadString('\n')
			watchdog.Reset(chatStreamIdleTimeout)
			if readErr != nil {
				if idleTimedOut.Load() {
					events <- ChatStreamEvent{Type: "error",
						Error: fmt.Sprintf("stream idle timeout: no data received for %s", chatStreamIdleTimeout)}
				} else if readErr != io.EOF {
					events <- ChatStreamEvent{Type: "error", Error: readErr.Error()}
				}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if event, ok := frame.event(); ok {
					events <- event
					if event.Type == "end" {
						return
					}
				}
				frame = sseFrame{}
				continue
			}
			frame.addLine(line)
		}
	}()
	return events, nil
}

// sseFrame accumulates one server-sent event's fields until the blank line
// that ends it.
type sseFrame struct {
	id        string
	eventName string
	data      []string
	hasData   bool
}

func (frame *sseFrame) addLine(line string) {
	if strings.HasPrefix(line, ":") {
		return // comment / heartbeat
	}
	field, value, _ := strings.Cut(line, ":")
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "id":
		frame.id = value
	case "event":
		frame.eventName = value
	case "data":
		frame.data = append(frame.data, value)
		frame.hasData = true
	}
}

// event renders the accumulated frame; false for frames the CLI skips
// (empty frames and the live-plane mirrors).
func (frame *sseFrame) event() (ChatStreamEvent, bool) {
	if !frame.hasData && frame.eventName == "" {
		return ChatStreamEvent{}, false
	}
	name := frame.eventName
	if name == "" {
		name = "message"
	}
	if frame.id == "" && liveDeltaFrame(name) {
		return ChatStreamEvent{}, false
	}
	event := ChatStreamEvent{Type: name}
	if frame.id != "" {
		if sequence, parseErr := strconv.ParseInt(frame.id, 10, 64); parseErr == nil {
			event.Sequence = sequence
		}
	}
	raw := strings.Join(frame.data, "\n")
	if raw == "" {
		return event, true
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		event.Data = raw
		return event, true
	}
	event.Data = decoded
	if name == "content" {
		if text, isString := decoded.(string); isString {
			event.Content = text
		}
	}
	event.Done = name == "end"
	return event, true
}

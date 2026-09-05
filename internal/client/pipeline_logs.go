package client

// The step log relay client: go/internal/pipelineapi/streams.go relays one
// step's output as SSE frames over the shared execution_output JetStream
// stream (the dedicated pipeline_output stream is WS-B item B3; when it lands
// nothing here changes - the frames, the seq resume cursor and the status
// codes are the shared sserelay's either way).
//
// The relay itself has no history to replay: a fresh connection (from_seq
// unset) only sees output published from the moment it connects. That is a
// real, permanent property of the relay, not a client bug, and
// PipelineLogEvent carries every frame decoded rather than pretending
// otherwise. It is not the whole story for a step that has already
// concluded, though: the step's complete output is also archived as a
// step_log pipeline artifact (enginekit/pipelineartifacts.KindStepLog), and
// cmd/pipeline_logs.go reads that instead of opening this relay once a
// step's Status is "concluded" - see PipelineArtifact and
// Client.ListPipelineArtifacts / DownloadPipelineArtifact in pipelines.go.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
)

// PipelineLogEvent is one decoded frame from the step log relay.
//
// Type is "line" for a step output line, "error" for a stream fault the relay
// reported before closing (its own ErrorFrame, or a local read failure), and
// "" is never sent - callers switch on Type.
type PipelineLogEvent struct {
	Type string
	// Stream is "stdout" or "stderr", set when Type == "line".
	Stream string
	// Line is the step's output text, set when Type == "line".
	Line string
	// Seq is the relay's stream sequence for this frame, the value a
	// reconnect echoes back as from_seq. Zero when Type == "error".
	Seq int64
	// Error is the fault message, set when Type == "error".
	Error string
}

// StreamPipelineStepLogs opens the step log SSE relay and returns a channel
// of decoded frames. The channel closes when the response ends (server
// disconnect, or the context is cancelled) or after one Type=="error" event -
// a stream fault is terminal, since the relay's own protocol answers it as a
// single frame before it stops (sserelay.ErrorFrame).
//
// fromSequence resumes a previous read after that stream sequence; zero
// starts from whatever the relay publishes next.
func (c *Client) StreamPipelineStepLogs(ctx context.Context, selector PipelineSelector,
	runID string, stepID string, fromSequence int64) (<-chan PipelineLogEvent, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	endpoint := fmt.Sprintf("%s%s/pipeline-runs/%s/steps/%s/logs",
		c.BaseURL, base, neturl.PathEscape(runID), neturl.PathEscape(stepID))
	if fromSequence > 0 {
		endpoint = fmt.Sprintf("%s?from_seq=%d", endpoint, fromSequence)
	}

	request, requestError := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Accept", "text/event-stream")

	response, doError := c.StreamingHTTP.Do(request)
	if doError != nil {
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	if response.StatusCode != http.StatusOK {
		body, readError := readResponseBody(response)
		closeBody(response)
		if readError != nil {
			return nil, fmt.Errorf("read response: %w", readError)
		}
		return nil, pipelineErrorFromResponse(response.StatusCode, body, response.Header.Get("Retry-After"))
	}

	events := make(chan PipelineLogEvent, 100)
	go func() {
		defer closeBody(response)
		defer close(events)
		reader := bufio.NewReader(response.Body)
		for {
			line, readError := reader.ReadString('\n')
			if readError != nil {
				if readError != io.EOF && ctx.Err() == nil {
					events <- PipelineLogEvent{Type: "error", Error: readError.Error()}
				}
				return
			}
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				// Comment keepalives and the blank frame separators; the
				// relay's own contract is "data:" frames only.
				continue
			}
			data := sseData(line)
			var frame struct {
				// The relay's own error frame (sserelay.ErrorFrame).
				Type    string `json:"type"`
				Message string `json:"message"`
				// The agent's output-line event
				// (agent/go/internal/scheduler/progress.go OnTaskOutput).
				EventType string `json:"event_type"`
				Stream    string `json:"stream"`
				Line      string `json:"line"`
				Seq       int64  `json:"seq"`
			}
			if unmarshalError := json.Unmarshal([]byte(data), &frame); unmarshalError != nil {
				continue
			}
			if frame.Type == "error" {
				events <- PipelineLogEvent{Type: "error", Error: frame.Message}
				return
			}
			if frame.EventType != "task_output" {
				continue
			}
			events <- PipelineLogEvent{Type: "line", Stream: frame.Stream, Line: frame.Line, Seq: frame.Seq}
		}
	}()
	return events, nil
}

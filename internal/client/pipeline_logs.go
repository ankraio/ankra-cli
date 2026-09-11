package client

// The step log relay client: go/internal/pipelineapi/streams.go relays one
// step's output as SSE frames from the dedicated pipeline_output JetStream
// stream, or from the shared execution_output one for an agent that cannot
// publish on the dedicated subject (sserelay.PumpEither). The seq resume
// cursor and the status codes are the shared sserelay's either way; the
// frames are not - see isPipelineLogLineFrame.
//
// Where in a step's output a connection starts, and whether it ends, are the
// relay's own decision from the step's status - and StepLogStreamOptions is
// how a caller overrides it. The relay retains a step's frames for a day
// (enginekit/pipelinerun.OutputRetention), so a concluded step's whole
// output can still be replayed from it, and `follow=false` ends the response
// once that history is drained instead of holding it open on keepalives for
// output that will never come. A platform older than that contract ignores
// both parameters and tails forever, which is why cmd/pipeline_logs.go
// bounds its replay rather than trusting the stream to end.
//
// The other copy of a concluded step's output is its archived step_log
// pipeline artifact (enginekit/pipelineartifacts.KindStepLog), which
// cmd/pipeline_logs.go prefers - see PipelineArtifact and
// Client.ListPipelineArtifacts / DownloadPipelineArtifact in pipelines.go.
// An organisation with no ready backup vault has nowhere to put one
// (pipelineartifacts.ErrNoVault), so the step is dispatched without uploads
// and no artifact row is ever minted; the replay above is what makes that
// step's output readable at all.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
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

// StepLogStreamOptions selects where in a step's output one connection to the
// relay starts and whether it ends when that output runs out. Each field maps
// onto one query parameter of the step log route.
//
// IsFollowing and IsReplaying are pointers because unset is a third answer
// with its own meaning: the route reads the step's own status for a request
// that expressed no preference, and must not read silence as false.
type StepLogStreamOptions struct {
	// FromSequence resumes a previous read after that stream sequence; zero
	// sends no cursor. The route ranks it above both flags below, so a
	// caller that says where it got to is never sent the history again.
	FromSequence int64
	// IsFollowing is `follow`: false ends the response once the retained
	// history is drained, true holds it open even for a concluded step.
	IsFollowing *bool
	// IsReplaying is `replay`: true delivers the retained history before the
	// live tail even for a running step, false refuses the history even for
	// a concluded one.
	IsReplaying *bool
}

// PipelineLogNoLongerRetainedError is the step log relay's 410
// (go/internal/pipelineapi/streams.go, error code LOG_NO_LONGER_RETAINED):
// the step concluded longer ago than the platform retains live output, so
// its frames aged out. It is deliberately not the relay's 503, which says
// the stream could not be read and is worth retrying - retrying this one
// will never produce a line.
type PipelineLogNoLongerRetainedError struct {
	// Detail is the platform's own sentence, printed verbatim.
	Detail string
	// ErrorCode is the machine-readable class, empty when the platform sent
	// none.
	ErrorCode string
}

func (retentionError *PipelineLogNoLongerRetainedError) Error() string {
	if retentionError == nil {
		return ""
	}
	return retentionError.Detail
}

// StreamPipelineStepLogs opens the step log SSE relay and returns a channel
// of decoded frames. The channel closes when the response ends (the relay
// drained a replay it was asked to end, a server disconnect, or the context
// is cancelled) or after one Type=="error" event - a stream fault is
// terminal, since the relay's own protocol answers it as a single frame
// before it stops (sserelay.ErrorFrame).
func (c *Client) StreamPipelineStepLogs(ctx context.Context, selector PipelineSelector,
	runID string, stepID string, options StepLogStreamOptions) (<-chan PipelineLogEvent, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	endpoint := fmt.Sprintf("%s%s/pipeline-runs/%s/steps/%s/logs",
		c.BaseURL, base, neturl.PathEscape(runID), neturl.PathEscape(stepID))
	query := neturl.Values{}
	if options.FromSequence > 0 {
		query.Set("from_seq", strconv.FormatInt(options.FromSequence, 10))
	}
	if options.IsFollowing != nil {
		query.Set("follow", strconv.FormatBool(*options.IsFollowing))
	}
	if options.IsReplaying != nil {
		query.Set("replay", strconv.FormatBool(*options.IsReplaying))
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
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
		if response.StatusCode == http.StatusGone {
			if retentionError := stepLogNoLongerRetainedFromBody(body); retentionError != nil {
				return nil, retentionError
			}
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
				// An output line, in one of the two shapes described at
				// isPipelineLogLineFrame.
				EventType string  `json:"event_type"`
				Stream    string  `json:"stream"`
				Line      *string `json:"line"`
				Seq       int64   `json:"seq"`
			}
			if unmarshalError := json.Unmarshal([]byte(data), &frame); unmarshalError != nil {
				continue
			}
			if frame.Type == "error" {
				events <- PipelineLogEvent{Type: "error", Error: frame.Message}
				return
			}
			if !isPipelineLogLineFrame(frame.EventType, frame.Line) {
				continue
			}
			outputLine := ""
			if frame.Line != nil {
				outputLine = *frame.Line
			}
			events <- PipelineLogEvent{Type: "line", Stream: frame.Stream, Line: outputLine, Seq: frame.Seq}
		}
	}()
	return events, nil
}

// isPipelineLogLineFrame reports whether a relay frame is one of the step's
// output lines. The relay reads the step's dedicated pipeline_output subject,
// falling back to the shared execution_output one for an agent that cannot
// publish on it (sserelay.PumpEither), and the agent writes a line
// differently on each: {"stream","line"} with no event_type at all on the
// dedicated subject (agent/go/internal/jobs pipelineProgressSink), and the
// scheduler's task_output event on the shared one
// (agent/go/internal/scheduler/progress.go OnTaskOutput), which also carries
// events that are not output. Reading only task_output dropped every line of
// every step an up-to-date agent ran.
func isPipelineLogLineFrame(eventType string, line *string) bool {
	if eventType == "" {
		return line != nil
	}
	return eventType == "task_output"
}

// stepLogNoLongerRetainedFromBody decodes the relay's 410 body
// ({"detail": "...", "error_code": "LOG_NO_LONGER_RETAINED"}). It answers nil
// for a 410 shaped like anything else, so a body this client does not
// recognise still reaches the shared mapping rather than being reported as a
// retention expiry it never claimed to be.
func stepLogNoLongerRetainedFromBody(body []byte) *PipelineLogNoLongerRetainedError {
	var expired struct {
		Detail    string `json:"detail"`
		ErrorCode string `json:"error_code"`
	}
	if unmarshalError := json.Unmarshal(body, &expired); unmarshalError != nil || expired.Detail == "" {
		return nil
	}
	return &PipelineLogNoLongerRetainedError{Detail: expired.Detail, ErrorCode: expired.ErrorCode}
}

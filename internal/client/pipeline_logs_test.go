package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// TestStreamPipelineStepLogsDecodesOutputLines pins the frame decode: only
// "task_output" events become Type=="line" events, everything else on the
// wire (a differently-shaped agent event, in particular) is dropped rather
// than surfaced as a log line.
func TestStreamPipelineStepLogsDecodesOutputLines(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/applications/app-1/pipeline-runs/run-1/steps/step-1/logs" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"event_type\":\"task_output\",\"stream\":\"stdout\",\"line\":\"building\",\"seq\":1}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"event_type\":\"plan_set\",\"seq\":2}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"event_type\":\"task_output\",\"stream\":\"stderr\",\"line\":\"warning\",\"seq\":3}\n\n")
		flusher.Flush()
	})

	events, streamError := testClient.StreamPipelineStepLogs(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, "run-1", "step-1", 0)
	if streamError != nil {
		t.Fatalf("StreamPipelineStepLogs error = %v", streamError)
	}

	var lines []PipelineLogEvent
	for event := range events {
		lines = append(lines, event)
	}
	if len(lines) != 2 {
		t.Fatalf("events = %+v, want exactly the two task_output frames", lines)
	}
	if lines[0].Line != "building" || lines[0].Stream != "stdout" || lines[0].Seq != 1 {
		t.Errorf("first event = %+v", lines[0])
	}
	if lines[1].Line != "warning" || lines[1].Stream != "stderr" || lines[1].Seq != 3 {
		t.Errorf("second event = %+v", lines[1])
	}
}

// TestStreamPipelineStepLogsSurfacesTheRelaysErrorFrame pins the relay's own
// terminal answer (sserelay.ErrorFrame): one Type=="error" event, and the
// channel closes without a further read hanging.
func TestStreamPipelineStepLogsSurfacesTheRelaysErrorFrame(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"stream closed\"}\n\n")
		flusher.Flush()
	})

	events, streamError := testClient.StreamPipelineStepLogs(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, "run-1", "step-1", 0)
	if streamError != nil {
		t.Fatalf("StreamPipelineStepLogs error = %v", streamError)
	}
	event, ok := <-events
	if !ok || event.Type != "error" || event.Error != "stream closed" {
		t.Fatalf("event = %+v, ok = %v", event, ok)
	}
	if _, stillOpen := <-events; stillOpen {
		t.Fatal("channel stayed open after the relay's error frame")
	}
}

// TestStreamPipelineStepLogsReconnectsFromSequence pins the resume contract:
// a caller that reconnects with fromSequence gets from_seq on the wire, and
// only the frames after it.
func TestStreamPipelineStepLogsReconnectsFromSequence(t *testing.T) {
	var capturedQuery string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"event_type\":\"task_output\",\"stream\":\"stdout\",\"line\":\"resumed\",\"seq\":6}\n\n")
		flusher.Flush()
	})

	events, streamError := testClient.StreamPipelineStepLogs(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, "run-1", "step-1", 5)
	if streamError != nil {
		t.Fatalf("StreamPipelineStepLogs error = %v", streamError)
	}
	event := <-events
	if event.Line != "resumed" || event.Seq != 6 {
		t.Fatalf("event = %+v", event)
	}
	if capturedQuery != "from_seq=5" {
		t.Errorf("query = %q, want from_seq=5", capturedQuery)
	}
}

// TestStreamPipelineStepLogsUnavailableCarriesRetryAfter pins the log
// relay's degraded-state 503: the client answers a typed error carrying the
// server's Retry-After rather than a generic failure.
func TestStreamPipelineStepLogsUnavailableCarriesRetryAfter(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, `{"detail":"The pipeline log stream is not available right now"}`)
	})
	_, streamError := testClient.StreamPipelineStepLogs(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, "run-1", "step-1", 0)
	if streamError == nil {
		t.Fatal("expected an error")
	}
	var unavailable *PipelineLogStreamUnavailableError
	if !errors.As(streamError, &unavailable) {
		t.Fatalf("error = %v (%T), want *PipelineLogStreamUnavailableError", streamError, streamError)
	}
	if unavailable.RetryAfterSeconds != 7 {
		t.Errorf("retry after = %d, want 7", unavailable.RetryAfterSeconds)
	}
}

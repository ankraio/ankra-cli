package client

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestStreamPodLogsFollowFalseDrainsAndReturns pins the --follow=false lane
// against a server that never closes the stream (the platform log route only
// follows): the client prints the backlog burst, then returns once the idle
// gap passes, instead of hanging until the caller kills it.
func TestStreamPodLogsFollowFalseDrainsAndReturns(t *testing.T) {
	handlerDone := make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, "data: backlog one")
		_, _ = fmt.Fprintln(w, "data: backlog two")
		flusher.Flush()
		// Hold the stream open like the real route does; the client must
		// leave on its own. Give up after a bound so a regression cannot
		// hang the test suite.
		select {
		case <-r.Context().Done():
		case <-time.After(15 * time.Second):
			t.Error("client never closed the non-follow stream")
		}
	}
	testClient := newTestClient(t, handler)
	var buf bytes.Buffer
	started := time.Now()
	err := testClient.StreamPodLogs(context.Background(), "cluster-id",
		PodLogOptions{Namespace: "default", PodName: "nginx-abc", Follow: false}, &buf)
	if err != nil {
		t.Fatalf("StreamPodLogs() error = %v", err)
	}
	if buf.String() != "backlog one\nbacklog two\n" {
		t.Fatalf("output = %q", buf.String())
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("drain took %s; expected to return shortly after the idle gap", elapsed)
	}
	<-handlerDone
}

// TestStreamPodLogsFollowFalseStopsOnKeepalive pins the fast path: the
// server's keepalive comment after a batch means nothing is buffered, so the
// client returns immediately without waiting out the idle gap.
func TestStreamPodLogsFollowFalseStopsOnKeepalive(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, "data: only line")
		_, _ = fmt.Fprintln(w, ": keepalive")
		flusher.Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(15 * time.Second):
			t.Error("client never closed the non-follow stream after keepalive")
		}
	}
	testClient := newTestClient(t, handler)
	var buf bytes.Buffer
	started := time.Now()
	err := testClient.StreamPodLogs(context.Background(), "cluster-id",
		PodLogOptions{Namespace: "default", PodName: "nginx-abc", Follow: false}, &buf)
	if err != nil {
		t.Fatalf("StreamPodLogs() error = %v", err)
	}
	if buf.String() != "only line\n" {
		t.Fatalf("output = %q", buf.String())
	}
	if elapsed := time.Since(started); elapsed > podLogsDrainIdle {
		t.Fatalf("keepalive should end the drain before the idle gap, took %s", elapsed)
	}
}

// TestStreamPodLogsFollowTrueReadsUntilStreamEnds pins the default: with
// Follow set the client keeps reading and only returns when the server
// closes the stream.
func TestStreamPodLogsFollowTrueReadsUntilStreamEnds(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprintln(w, "data: first")
		flusher.Flush()
		time.Sleep(3 * podLogsDrainIdle)
		_, _ = fmt.Fprintln(w, "data: late arrival")
		flusher.Flush()
	}
	testClient := newTestClient(t, handler)
	var buf bytes.Buffer
	err := testClient.StreamPodLogs(context.Background(), "cluster-id",
		PodLogOptions{Namespace: "default", PodName: "nginx-abc", Follow: true}, &buf)
	if err != nil {
		t.Fatalf("StreamPodLogs() error = %v", err)
	}
	if buf.String() != "first\nlate arrival\n" {
		t.Fatalf("follow mode must read past idle gaps, got %q", buf.String())
	}
}

// TestStreamPodLogsFollowFalseEndsOnServerEndFrame pins the bounded read
// against a route that honours follow=false: the request says so, the end
// frame stops the read at once instead of waiting out the idle gap, and the
// frame's "stream complete" payload never reaches the log output.
func TestStreamPodLogsFollowFalseEndsOnServerEndFrame(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("follow"); got != "false" {
			t.Errorf("follow query = %q, want %q", got, "false")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: backlog one\n\n")
		_, _ = fmt.Fprint(w, "event: end\ndata: stream complete\n\n")
		flusher.Flush()
		// Hold the response open: the client must leave on the frame, not
		// because the stream closed under it.
		select {
		case <-r.Context().Done():
		case <-time.After(15 * time.Second):
			t.Error("client never left on the end frame")
		}
	}
	testClient := newTestClient(t, handler)
	var buf bytes.Buffer
	started := time.Now()
	err := testClient.StreamPodLogs(context.Background(), "cluster-id",
		PodLogOptions{Namespace: "default", PodName: "nginx-abc", Follow: false}, &buf)
	if err != nil {
		t.Fatalf("StreamPodLogs() error = %v", err)
	}
	if buf.String() != "backlog one\n" {
		t.Fatalf("output = %q; the end frame's payload is protocol text", buf.String())
	}
	if elapsed := time.Since(started); elapsed >= podLogsDrainIdle {
		t.Fatalf("end frame should end the read before the idle gap, took %s", elapsed)
	}
}

// TestStreamPodLogsFollowTrueEndsOnEndFrame pins the follow lane's own
// terminator: the route ends a stream it has stopped hearing from with
// "event: end" + "stream idle timeout", which is protocol text and must not
// be printed as if it were a log line. Following requests leave the follow
// parameter off, keeping them byte-identical to older releases.
func TestStreamPodLogsFollowTrueEndsOnEndFrame(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("follow") {
			t.Errorf("follow query = %q, want it absent", r.URL.Query().Get("follow"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: first\n\n")
		_, _ = fmt.Fprint(w, "event: end\ndata: stream idle timeout\n\n")
		flusher.Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(15 * time.Second):
			t.Error("client never left on the end frame")
		}
	}
	testClient := newTestClient(t, handler)
	var buf bytes.Buffer
	err := testClient.StreamPodLogs(context.Background(), "cluster-id",
		PodLogOptions{Namespace: "default", PodName: "nginx-abc", Follow: true}, &buf)
	if err != nil {
		t.Fatalf("StreamPodLogs() error = %v", err)
	}
	if buf.String() != "first\n" {
		t.Fatalf("output = %q; the end frame's payload is protocol text", buf.String())
	}
}

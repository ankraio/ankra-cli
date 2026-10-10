package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func workspaceSSEFrame(writer io.Writer, event string, id string, data string) {
	if id != "" {
		_, _ = fmt.Fprintf(writer, "id: %s\n", id)
	}
	_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, data)
}

func collectRunEvents(t *testing.T, events <-chan WorkspaceRunEvent) []WorkspaceRunEvent {
	t.Helper()
	var collected []WorkspaceRunEvent
	timeout := time.After(10 * time.Second)
	for {
		select {
		case event, isOpen := <-events:
			if !isOpen {
				return collected
			}
			collected = append(collected, event)
		case <-timeout:
			t.Fatal("the event channel never closed")
		}
	}
}

func TestStreamWorkspaceRunDecodesFramesAndSendsOffsets(t *testing.T) {
	var gotQuery, gotLastEventID, gotAuthorization, gotPath string
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		gotQuery = request.URL.RawQuery
		gotLastEventID = request.Header.Get("Last-Event-ID")
		gotAuthorization = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, ": keepalive\n\n")
		workspaceSSEFrame(writer, "stdout", "15:3", base64.StdEncoding.EncodeToString([]byte("hello\n")))
		workspaceSSEFrame(writer, "stderr", "15:9", `{"data":"`+base64.StdEncoding.EncodeToString([]byte("warn\n"))+`"}`)
		workspaceSSEFrame(writer, "keepalive", "", "{}")
		workspaceSSEFrame(writer, "exit", "15:9", `{"code":3,"started":true}`)
		workspaceSSEFrame(writer, "stdout", "", base64.StdEncoding.EncodeToString([]byte("never read")))
	})
	events, streamError := testClient.StreamWorkspaceRun(context.Background(), "ws-1", "run-1",
		WorkspaceRunOffsets{Stdout: 9, Stderr: 3})
	if streamError != nil {
		t.Fatalf("StreamWorkspaceRun() error = %v", streamError)
	}
	collected := collectRunEvents(t, events)
	if gotPath != "/api/v1/org/workspaces/ws-1/runs/run-1/stream" {
		t.Errorf("path = %s", gotPath)
	}
	if gotQuery != "stderr_offset=3&stdout_offset=9" || gotLastEventID != "9:3" {
		t.Errorf("resume = %q / Last-Event-ID %q, want the offsets 9:3", gotQuery, gotLastEventID)
	}
	if gotAuthorization != "Bearer "+testToken {
		t.Errorf("Authorization = %q", gotAuthorization)
	}
	if len(collected) != 3 {
		t.Fatalf("events = %+v, want stdout, stderr, exit and nothing after the exit", collected)
	}
	if collected[0].Type != WorkspaceRunEventStdout || string(collected[0].Data) != "hello\n" || collected[0].ID != "15:3" {
		t.Errorf("stdout event = %+v", collected[0])
	}
	if collected[1].Type != WorkspaceRunEventStderr || string(collected[1].Data) != "warn\n" {
		t.Errorf("stderr event = %+v", collected[1])
	}
	if collected[2].Type != WorkspaceRunEventExit || collected[2].ExitCode != 3 || !collected[2].Started {
		t.Errorf("exit event = %+v", collected[2])
	}
}

func TestStreamWorkspaceRunEndsOnReconnectAndOnDrop(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		workspaceSSEFrame(writer, "stdout", "", base64.StdEncoding.EncodeToString([]byte("a")))
		if request.URL.Query().Get("stdout_offset") == "0" {
			workspaceSSEFrame(writer, "reconnect", "1:0", "{}")
			return
		}
		// A dropped connection: the response just ends.
	})
	events, _ := testClient.StreamWorkspaceRun(context.Background(), "ws", "run", WorkspaceRunOffsets{})
	collected := collectRunEvents(t, events)
	if len(collected) != 2 || collected[1].Type != WorkspaceRunEventReconnect {
		t.Fatalf("events = %+v, want output then reconnect", collected)
	}
	events, _ = testClient.StreamWorkspaceRun(context.Background(), "ws", "run", WorkspaceRunOffsets{Stdout: 1})
	collected = collectRunEvents(t, events)
	if len(collected) != 2 || collected[1].Type != WorkspaceRunEventError ||
		!strings.Contains(collected[1].Message, "without the run's exit") {
		t.Fatalf("events = %+v, want output then the drop as an error", collected)
	}
}

func TestStreamWorkspaceRunGivesUpOnASilentConnection(t *testing.T) {
	original := workspaceStreamIdleTimeout
	workspaceStreamIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { workspaceStreamIdleTimeout = original })
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.(http.Flusher).Flush()
		select {
		case <-request.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	events, streamError := testClient.StreamWorkspaceRun(context.Background(), "ws", "run", WorkspaceRunOffsets{})
	if streamError != nil {
		t.Fatalf("StreamWorkspaceRun() error = %v", streamError)
	}
	collected := collectRunEvents(t, events)
	if len(collected) != 1 || collected[0].Type != WorkspaceRunEventError {
		t.Fatalf("events = %+v, want one error for the silent connection", collected)
	}
}

func TestWorkspaceErrorEnvelopes(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/org/workspaces/not-ready/runs":
			writer.Header().Set("Retry-After", "5")
			writer.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(writer, `{"error_code":"WORKSPACE_NOT_READY","detail":"The workspace is not ready.","retry_after":5}`)
		case "/api/v1/org/workspaces/offline/runs":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"error_code":"CLUSTER_OFFLINE","detail":"The cluster is offline.","retry_after":30}`)
		case "/api/v1/org/workspaces/refused/runs":
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, `{"detail":"PATH cannot be forwarded."}`)
		case "/api/v1/org/workspaces/denied/runs":
			writer.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(writer, `{"detail":"permission_denied","permission":"workspaces.use"}`)
		}
	})
	_, notReadyError := testClient.StartWorkspaceRun(context.Background(), "not-ready", WorkspaceRunRequest{})
	var workspaceError *WorkspaceAPIError
	if !errors.As(notReadyError, &workspaceError) || workspaceError.Code != "WORKSPACE_NOT_READY" ||
		workspaceError.RetryAfterSeconds != 5 || !workspaceError.IsRetryable() {
		t.Errorf("not ready = %#v", notReadyError)
	}
	_, offlineError := testClient.StartWorkspaceRun(context.Background(), "offline", WorkspaceRunRequest{})
	if !errors.As(offlineError, &workspaceError) || workspaceError.Code != "CLUSTER_OFFLINE" || !workspaceError.IsRetryable() {
		t.Errorf("offline = %#v", offlineError)
	}
	_, refusedError := testClient.StartWorkspaceRun(context.Background(), "refused", WorkspaceRunRequest{})
	if !errors.As(refusedError, &workspaceError) || workspaceError.IsRetryable() ||
		refusedError.Error() != "PATH cannot be forwarded." {
		t.Errorf("refused = %#v", refusedError)
	}
	_, deniedError := testClient.StartWorkspaceRun(context.Background(), "denied", WorkspaceRunRequest{})
	var permissionDenied *PermissionDeniedError
	if !errors.As(deniedError, &permissionDenied) || permissionDenied.Permission != "workspaces.use" {
		t.Errorf("denied = %#v", deniedError)
	}
}

func TestUpWorkspaceSendsTheRepositoryAndReportsCreation(t *testing.T) {
	var body map[string]any
	status := http.StatusCreated
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/workspaces" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, `{"id":"ws-1","kind":"go","status":"provisioning"}`)
	})
	workspace, isCreated, upError := testClient.UpWorkspace(context.Background(), WorkspaceUpRequest{
		Provider: "gitlab", Owner: "acme/platform", Name: "shop", Kind: "go"})
	if upError != nil || !isCreated || workspace.ID != "ws-1" || workspace.Status != WorkspaceStatusProvisioning {
		t.Fatalf("UpWorkspace() = %+v, %v, %v", workspace, isCreated, upError)
	}
	if body["provider"] != "gitlab" || body["owner"] != "acme/platform" || body["name"] != "shop" || body["kind"] != "go" {
		t.Fatalf("body = %v", body)
	}
	if _, present := body["repository_id"]; present {
		t.Fatalf("body sends an empty repository_id: %v", body)
	}
	status = http.StatusOK
	if _, isCreated, _ = testClient.UpWorkspace(context.Background(), WorkspaceUpRequest{Provider: "github",
		Owner: "a", Name: "b"}); isCreated {
		t.Fatal("a 200 answer is the live workspace, not a created one")
	}
}

func TestPresignedTransfersNeverCarryTheToken(t *testing.T) {
	var uploaded []byte
	var authorizations []string
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		switch request.Method {
		case http.MethodPut:
			uploaded, _ = io.ReadAll(request.Body)
		case http.MethodGet:
			_, _ = io.WriteString(writer, "patch-bytes")
		}
	})
	if uploadError := testClient.UploadPresigned(context.Background(), testClient.BaseURL+"/vault/b?sig=1",
		strings.NewReader("bundle"), 6); uploadError != nil {
		t.Fatal(uploadError)
	}
	var downloaded strings.Builder
	if downloadError := testClient.DownloadPresigned(context.Background(), testClient.BaseURL+"/vault/p?sig=1",
		&downloaded); downloadError != nil {
		t.Fatal(downloadError)
	}
	if string(uploaded) != "bundle" || downloaded.String() != "patch-bytes" {
		t.Fatalf("uploaded %q, downloaded %q", uploaded, downloaded.String())
	}
	for _, authorization := range authorizations {
		if authorization != "" {
			t.Fatalf("a presigned transfer sent Authorization %q", authorization)
		}
	}
}

func TestWorkspaceRunOffsetsRoundTrip(t *testing.T) {
	offsets, isValid := ParseWorkspaceRunOffsets("120:7")
	if !isValid || offsets.Stdout != 120 || offsets.Stderr != 7 || offsets.ID() != "120:7" {
		t.Fatalf("ParseWorkspaceRunOffsets() = %+v, %v", offsets, isValid)
	}
	for _, invalid := range []string{"", "12", "a:1", "-1:2"} {
		if _, isValid := ParseWorkspaceRunOffsets(invalid); isValid {
			t.Errorf("ParseWorkspaceRunOffsets(%q) accepted", invalid)
		}
	}
}

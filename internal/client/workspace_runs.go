package client

// Runs in an Ankra Workspace (cluster go/internal/workspacesapi, PR4 of
// ankra-b5c3as.6): the delta bundle a run builds on, the run itself, its
// output as a resumable SSE stream, its cancel and the exports it leaves
// behind. Token lane only (/api/v1/org/workspaces/{workspace_id}/...):
//
//	POST .../bundles                    {sha256, size_bytes} -> presigned upload
//	POST .../runs                       start a run -> 202 {run_id}
//	GET  .../runs/{run_id}              read one
//	GET  .../runs/{run_id}/stream       SSE, below
//
// The stream (go/internal/workspacesapi/stream.go):
//
//	event: stdout     id: <o>:<e>   data: {"data":"<base64>"}
//	event: stderr     id: <o>:<e>   data: {"data":"<base64>"}
//	event: exit                     data: {"code":N,"started":true|false|null}
//	event: reconnect                data: {"stdout_offset":O,"stderr_offset":E,"reason":"..."}
//	event: error                    data: {"message":"...","state":"lost"}
//	: keepalive                     (a comment, every 15 s of silence)
//
// A connection starts at the byte offsets the client already has
// (stdout_offset/stderr_offset, or Last-Event-ID, which wins); exit,
// reconnect and error each end it.
//	POST .../runs/{run_id}/cancel       stop it
//	POST .../runs/{run_id}/exports      {kind: patch|dirs, paths} -> download
//
// The presigned upload and download URLs point at the organisation's vault,
// not the API: they are fetched with a plain client that never carries the
// API token.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"
)

// The states a run moves through.
const (
	WorkspaceRunStateAccepted   = "accepted"
	WorkspaceRunStateRunning    = "running"
	WorkspaceRunStateFinished   = "finished"
	WorkspaceRunStateNotStarted = "not_started"
	WorkspaceRunStateCancelled  = "cancelled"
	WorkspaceRunStateLost       = "lost"
)

// WorkspaceBundleUpload is the answer to POST .../bundles: where to PUT the
// bundle, and whether an identical one is already stored (content-addressed
// by its SHA-256, so the PUT is skipped).
type WorkspaceBundleUpload struct {
	ObjectKey string `json:"object_key"`
	UploadURL string `json:"upload_url"`
	ExpiresAt string `json:"expires_at"`
	Exists    bool   `json:"exists"`
}

// WorkspaceRunRequest is the body of POST .../runs.
type WorkspaceRunRequest struct {
	// AgentID names the client worktree: one checkout in the pod per agent.
	AgentID string   `json:"agent_id"`
	Argv    []string `json:"argv"`
	// CwdPrefix is the client's directory relative to its worktree root.
	CwdPrefix string `json:"cwd_prefix"`
	// Env is the forwarded environment (the server holds it to an allowlist).
	Env             map[string]string `json:"env,omitempty"`
	SnapshotSHA     string            `json:"snapshot_sha"`
	HeadSHA         string            `json:"head_sha"`
	BaseRef         string            `json:"base_ref,omitempty"`
	BaseSHA         string            `json:"base_sha,omitempty"`
	Shallow         []string          `json:"shallow,omitempty"`
	BundleObjectKey string            `json:"bundle_object_key,omitempty"`
	// Apply wraps the command so the changes it makes are exported as a
	// patch and the workspace checkout is restored.
	Apply bool `json:"apply"`
}

// WorkspaceRunStarted is the 202 answer to POST .../runs.
type WorkspaceRunStarted struct {
	RunID string `json:"run_id"`
}

// WorkspaceRun is one run as GET .../runs/{run_id} answers it.
type WorkspaceRun struct {
	ID          string `json:"run_id"`
	WorkspaceID string `json:"workspace_id"`
	State       string `json:"state"`
	// ExitCode is the command's exit code once it finished, or - with
	// Started false - why it never started (197: resend the full history).
	ExitCode *int `json:"exit_code"`
	// Started says whether the command itself started; null when the
	// platform cannot tell yet.
	Started    *bool      `json:"started"`
	FinishedAt *time.Time `json:"finished_at"`
}

// IsStarted reports whether the run's command is known to have started.
func (run WorkspaceRun) IsStarted() bool {
	if run.Started != nil {
		return *run.Started
	}
	switch run.State {
	case WorkspaceRunStateRunning, WorkspaceRunStateFinished:
		return true
	}
	return false
}

// IsTerminal reports whether the run has ended and nothing will change it.
func (run WorkspaceRun) IsTerminal() bool {
	if run.FinishedAt != nil {
		return true
	}
	switch run.State {
	case WorkspaceRunStateFinished, WorkspaceRunStateNotStarted, WorkspaceRunStateLost:
		return true
	}
	return false
}

// WorkspaceRunExportRequest is the body of POST .../exports.
type WorkspaceRunExportRequest struct {
	// Kind is "patch" (the changes an --apply run made, a binary git diff)
	// or "dirs" (a gzipped tar of Paths, relative to the run's directory).
	Kind  string   `json:"kind"`
	Paths []string `json:"paths,omitempty"`
}

// WorkspaceRunExport is the answer to POST .../exports: a presigned download,
// or Empty when there was nothing to export.
type WorkspaceRunExport struct {
	Empty       bool   `json:"empty"`
	DownloadURL string `json:"download_url"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	ExpiresAt   string `json:"expires_at"`
}

// The SSE event types of the run output stream.
const (
	WorkspaceRunEventStdout    = "stdout"
	WorkspaceRunEventStderr    = "stderr"
	WorkspaceRunEventExit      = "exit"
	WorkspaceRunEventReconnect = "reconnect"
	WorkspaceRunEventError     = "error"
)

// WorkspaceRunEvent is one decoded frame of the run output stream.
type WorkspaceRunEvent struct {
	// Type is one of the WorkspaceRunEvent* constants.
	Type string
	// Data is the decoded output of a stdout or stderr frame.
	Data []byte
	// ID is the frame's SSE id, "<stdout offset>:<stderr offset>" after it,
	// the value a reconnect sends back as Last-Event-ID.
	ID string
	// ExitCode and Started are set on an exit frame: the command's exit code,
	// or why it never started when Started is false.
	ExitCode int
	Started  bool
	// Message is an error frame's text.
	Message string
	// IsDrop marks an error this client synthesised for a connection that
	// ended without the run's exit (dropped, went silent), as opposed to the
	// platform's own error frame, which says the run can no longer be
	// followed.
	IsDrop bool
}

// WorkspaceRunOffsets is where in a run's stdout and stderr a stream
// connection starts: the bytes the client already has of each.
type WorkspaceRunOffsets struct {
	Stdout int64
	Stderr int64
}

// ID renders the offsets as the stream's SSE id.
func (offsets WorkspaceRunOffsets) ID() string {
	return strconv.FormatInt(offsets.Stdout, 10) + ":" + strconv.FormatInt(offsets.Stderr, 10)
}

// ParseWorkspaceRunOffsets reads a stream SSE id ("<o>:<e>").
func ParseWorkspaceRunOffsets(id string) (WorkspaceRunOffsets, bool) {
	stdoutText, stderrText, isFound := strings.Cut(strings.TrimSpace(id), ":")
	if !isFound {
		return WorkspaceRunOffsets{}, false
	}
	stdoutOffset, stdoutError := strconv.ParseInt(stdoutText, 10, 64)
	stderrOffset, stderrError := strconv.ParseInt(stderrText, 10, 64)
	if stdoutError != nil || stderrError != nil || stdoutOffset < 0 || stderrOffset < 0 {
		return WorkspaceRunOffsets{}, false
	}
	return WorkspaceRunOffsets{Stdout: stdoutOffset, Stderr: stderrOffset}, true
}

// CreateWorkspaceBundle asks where to upload a delta bundle of the given
// SHA-256 and size (POST .../bundles).
func (c *Client) CreateWorkspaceBundle(ctx context.Context, workspaceID string, sha256Hex string,
	sizeBytes int64) (*WorkspaceBundleUpload, error) {
	payload := map[string]any{"sha256": sha256Hex, "size_bytes": sizeBytes}
	var upload WorkspaceBundleUpload
	if _, requestError := c.doWorkspaceRequest(ctx, http.MethodPost, c.workspaceEndpoint(workspaceID, "bundles"),
		payload, &upload); requestError != nil {
		return nil, requestError
	}
	return &upload, nil
}

// StartWorkspaceRun starts a run (POST .../runs).
func (c *Client) StartWorkspaceRun(ctx context.Context, workspaceID string,
	request WorkspaceRunRequest) (*WorkspaceRunStarted, error) {
	var started WorkspaceRunStarted
	if _, requestError := c.doWorkspaceRequest(ctx, http.MethodPost, c.workspaceEndpoint(workspaceID, "runs"),
		request, &started); requestError != nil {
		return nil, requestError
	}
	if started.RunID == "" {
		return nil, errors.New("the platform started a run but answered no run_id")
	}
	return &started, nil
}

// GetWorkspaceRun reads one run (GET .../runs/{run_id}).
func (c *Client) GetWorkspaceRun(ctx context.Context, workspaceID string, runID string) (*WorkspaceRun, error) {
	var run WorkspaceRun
	if _, requestError := c.doWorkspaceRequest(ctx, http.MethodGet, c.workspaceEndpoint(workspaceID, "runs", runID),
		nil, &run); requestError != nil {
		return nil, requestError
	}
	return &run, nil
}

// CancelWorkspaceRun stops a run (POST .../runs/{run_id}/cancel).
func (c *Client) CancelWorkspaceRun(ctx context.Context, workspaceID string, runID string) error {
	_, requestError := c.doWorkspaceRequest(ctx, http.MethodPost,
		c.workspaceEndpoint(workspaceID, "runs", runID, "cancel"), map[string]any{}, nil)
	return requestError
}

// CreateWorkspaceRunExport asks for what a run left behind
// (POST .../runs/{run_id}/exports).
func (c *Client) CreateWorkspaceRunExport(ctx context.Context, workspaceID string, runID string,
	request WorkspaceRunExportRequest) (*WorkspaceRunExport, error) {
	var export WorkspaceRunExport
	if _, requestError := c.doWorkspaceRequest(ctx, http.MethodPost,
		c.workspaceEndpoint(workspaceID, "runs", runID, "exports"), request, &export); requestError != nil {
		return nil, requestError
	}
	return &export, nil
}

// presignedTransferTimeout bounds one presigned upload or download: a first
// sync can carry a repository's whole history.
const presignedTransferTimeout = 30 * time.Minute

// presignedHTTP is the client for vault URLs. It shares nothing with the API
// client: no token, no organisation header, no retry of a body-carrying PUT.
var presignedHTTP = &http.Client{Timeout: presignedTransferTimeout}

// UploadPresigned PUTs size bytes from body to a presigned URL.
func (c *Client) UploadPresigned(ctx context.Context, uploadURL string, body io.Reader, size int64) error {
	request, requestError := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, body)
	if requestError != nil {
		return fmt.Errorf("create upload request: %w", requestError)
	}
	request.ContentLength = size
	request.Header.Set("Content-Type", "application/octet-stream")
	response, doError := presignedHTTP.Do(request)
	if doError != nil {
		return fmt.Errorf("upload failed: %w", doError)
	}
	defer closeBody(response)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := readResponseBody(response)
		return fmt.Errorf("upload failed: status %d: %s", response.StatusCode, truncateForError(detail, 300))
	}
	return nil
}

// DownloadPresigned GETs a presigned URL into writer.
func (c *Client) DownloadPresigned(ctx context.Context, downloadURL string, writer io.Writer) error {
	request, requestError := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if requestError != nil {
		return fmt.Errorf("create download request: %w", requestError)
	}
	response, doError := presignedHTTP.Do(request)
	if doError != nil {
		return fmt.Errorf("download failed: %w", doError)
	}
	defer closeBody(response)
	if response.StatusCode != http.StatusOK {
		detail, _ := readResponseBody(response)
		return fmt.Errorf("download failed: status %d: %s", response.StatusCode, truncateForError(detail, 300))
	}
	if _, copyError := io.Copy(writer, response.Body); copyError != nil {
		return fmt.Errorf("download failed: %w", copyError)
	}
	return nil
}

// workspaceStreamIdleTimeout is how long one stream connection may stay
// silent - no output and no keepalive - before it is treated as dropped. The
// platform sends keepalives far more often than this.
var workspaceStreamIdleTimeout = 90 * time.Second

// workspaceStreamHTTP is the client one stream connection rides: the
// organisation override like every API call, but none of the shared
// transport's transient-error retries - the caller owns reconnection (from
// the offsets it has), and the retry warnings would land in the run's stderr.
func (c *Client) workspaceStreamHTTP() *http.Client {
	return &http.Client{Transport: &orgOverrideTransport{
		base:  &http.Transport{ResponseHeaderTimeout: 30 * time.Second},
		orgID: &c.orgOverride,
	}}
}

// StreamWorkspaceRun opens one connection to a run's output stream from the
// given offsets and returns its frames. The channel closes when the
// connection ends: after an exit frame, a reconnect frame, an error frame, a
// dropped connection (a final error event says so), or ctx ending. A caller
// that wants the whole output reconnects from the offsets it has whenever
// the channel closes without an exit frame.
func (c *Client) StreamWorkspaceRun(ctx context.Context, workspaceID string, runID string,
	offsets WorkspaceRunOffsets) (<-chan WorkspaceRunEvent, error) {
	query := neturl.Values{}
	query.Set("stdout_offset", strconv.FormatInt(offsets.Stdout, 10))
	query.Set("stderr_offset", strconv.FormatInt(offsets.Stderr, 10))
	endpoint := c.workspaceEndpoint(workspaceID, "runs", runID, "stream") + "?" + query.Encode()

	connectionContext, cancelConnection := context.WithCancel(ctx)
	request, requestError := http.NewRequestWithContext(connectionContext, http.MethodGet, endpoint, nil)
	if requestError != nil {
		cancelConnection()
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", offsets.ID())

	response, doError := c.workspaceStreamHTTP().Do(request)
	if doError != nil {
		cancelConnection()
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := readResponseBody(response)
		closeBody(response)
		cancelConnection()
		return nil, workspaceErrorFromResponse(response.StatusCode, body, response.Header.Get("Retry-After"))
	}

	events := make(chan WorkspaceRunEvent, 64)
	activity := make(chan struct{}, 1)
	go func() {
		// The idle watchdog: a connection nothing arrives on is cancelled,
		// which ends the read below with an error.
		timer := time.NewTimer(workspaceStreamIdleTimeout)
		defer timer.Stop()
		for {
			select {
			case <-connectionContext.Done():
				return
			case <-activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(workspaceStreamIdleTimeout)
			case <-timer.C:
				cancelConnection()
				return
			}
		}
	}()
	go func() {
		defer cancelConnection()
		defer closeBody(response)
		defer close(events)
		emit := func(event WorkspaceRunEvent) bool {
			select {
			case events <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		reader := bufio.NewReaderSize(response.Body, 64*1024)
		var eventType, eventID string
		var dataLines []string
		for {
			line, readError := reader.ReadString('\n')
			if len(line) > 0 {
				select {
				case activity <- struct{}{}:
				default:
				}
			}
			if readError != nil {
				if ctx.Err() == nil {
					message := readError.Error()
					if readError == io.EOF {
						message = "the stream ended without the run's exit"
					} else if connectionContext.Err() != nil {
						message = "the stream went silent"
					}
					emit(WorkspaceRunEvent{Type: WorkspaceRunEventError, Message: message, IsDrop: true})
				}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if len(dataLines) == 0 && eventType == "" {
					continue
				}
				event, isKnown := decodeWorkspaceRunFrame(eventType, eventID, strings.Join(dataLines, "\n"))
				eventType, eventID, dataLines = "", "", nil
				if !isKnown {
					continue
				}
				if !emit(event) {
					return
				}
				switch event.Type {
				case WorkspaceRunEventExit, WorkspaceRunEventReconnect, WorkspaceRunEventError:
					return
				}
				continue
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				eventType = value
			case "id":
				eventID = value
			case "data":
				dataLines = append(dataLines, value)
			}
		}
	}()
	return events, nil
}

// decodeWorkspaceRunFrame turns one SSE frame into an event. Output frames
// carry their bytes base64-encoded, either as the whole data line or as the
// "data" member of a JSON object; frames of a type this client does not
// know (keepalive) are skipped.
func decodeWorkspaceRunFrame(eventType string, eventID string, data string) (WorkspaceRunEvent, bool) {
	switch eventType {
	case WorkspaceRunEventStdout, WorkspaceRunEventStderr:
		encoded := strings.TrimSpace(data)
		if strings.HasPrefix(encoded, "{") {
			var frame struct {
				Data string `json:"data"`
			}
			if json.Unmarshal([]byte(encoded), &frame) != nil {
				return WorkspaceRunEvent{}, false
			}
			encoded = frame.Data
		}
		decoded, decodeError := base64.StdEncoding.DecodeString(encoded)
		if decodeError != nil {
			return WorkspaceRunEvent{Type: WorkspaceRunEventError,
				Message: "the stream sent output that is not base64"}, true
		}
		return WorkspaceRunEvent{Type: eventType, Data: decoded, ID: eventID}, true
	case WorkspaceRunEventExit:
		var frame struct {
			Code    *int  `json:"code"`
			Started *bool `json:"started"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil || frame.Code == nil {
			return WorkspaceRunEvent{Type: WorkspaceRunEventError,
				Message: "the stream sent an exit frame without a code"}, true
		}
		// started null means the platform could not tell (a cancelled or
		// lost run): read as started, so a caller never runs the command a
		// second time somewhere else.
		started := true
		if frame.Started != nil {
			started = *frame.Started
		}
		return WorkspaceRunEvent{Type: WorkspaceRunEventExit, ExitCode: *frame.Code, Started: started,
			ID: eventID}, true
	case WorkspaceRunEventReconnect:
		return WorkspaceRunEvent{Type: WorkspaceRunEventReconnect, ID: eventID}, true
	case WorkspaceRunEventError:
		message := strings.TrimSpace(data)
		var frame struct {
			Message string `json:"message"`
			Detail  string `json:"detail"`
		}
		if json.Unmarshal([]byte(data), &frame) == nil {
			if frame.Message != "" {
				message = frame.Message
			} else if frame.Detail != "" {
				message = frame.Detail
			}
		}
		return WorkspaceRunEvent{Type: WorkspaceRunEventError, Message: message, ID: eventID}, true
	}
	return WorkspaceRunEvent{}, false
}

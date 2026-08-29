package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// PodTerminalRequest names the container a terminal is opened in and the
// window it starts with.
type PodTerminalRequest struct {
	Namespace     string
	PodName       string
	ContainerName string
	Shell         string
	Cols          int
	Rows          int
}

// PodTerminalFrame is one message from the platform's terminal relay. Type
// is one of connecting, connected, stdout, stderr, pong, error or end; Data
// carries the base64 payload of a stdout/stderr frame and Message the text
// of an error frame.
type PodTerminalFrame struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Message string `json:"message,omitempty"`
}

// Payload decodes the bytes a stdout or stderr frame carries; every other
// frame carries none.
func (frame PodTerminalFrame) Payload() ([]byte, error) {
	if frame.Data == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(frame.Data)
}

// PodTerminal is an open terminal session on a pod. Frames delivers what
// the relay sends until the session ends, at which point the channel is
// closed and Err reports why.
type PodTerminal interface {
	Frames() <-chan PodTerminalFrame
	SendInput(data []byte) error
	Resize(cols int, rows int) error
	Ping() error
	Close() error
	Err() error
}

// PodTerminalClosedError is a session the relay ended with a close code the
// CLI has no better name for: 4001 (the token was refused) or an unexpected
// code. Message carries the relay's last error frame, when it sent one.
type PodTerminalClosedError struct {
	Code    int
	Reason  string
	Message string
}

func (e *PodTerminalClosedError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Reason != "" {
		return e.Reason
	}
	return fmt.Sprintf("the terminal session was closed (code %d)", e.Code)
}

// IsAuthentication reports whether the relay refused the credential.
func (e *PodTerminalClosedError) IsAuthentication() bool {
	return e.Code == podTerminalCloseAuthenticationRequired
}

// The relay's close codes (cluster-api terminalapi).
const (
	podTerminalCloseAuthenticationRequired = 4001
	podTerminalCloseClusterUnavailable     = 4002
	podTerminalCloseNoAgent                = 4003
	podTerminalCloseSandbox                = 4004
	podTerminalClosePermissionDenied       = 4403
)

const podTerminalReadLimitBytes = 4 << 20

// OpenPodTerminal opens a websocket terminal on the named container through
// the platform relay (the bearer twin of the portal's terminal route).
// Every session is recorded and audited by the platform. The returned
// session lives until ctx is cancelled, Close is called, or the relay ends
// it.
func (c *Client) OpenPodTerminal(ctx context.Context, clusterID string, request PodTerminalRequest) (PodTerminal, error) {
	if request.Namespace == "" || request.PodName == "" || request.ContainerName == "" {
		return nil, errors.New("a namespace, pod and container are required to open a terminal")
	}
	endpoint, parseError := url.Parse(c.BaseURL)
	if parseError != nil {
		return nil, fmt.Errorf("invalid API base URL %q: %w", c.BaseURL, parseError)
	}
	switch endpoint.Scheme {
	case "https":
		endpoint.Scheme = "wss"
	case "http":
		endpoint.Scheme = "ws"
	default:
		return nil, fmt.Errorf("unsupported API base URL scheme %q", endpoint.Scheme)
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/clusters/" + url.PathEscape(clusterID) + "/kubernetes/pod/terminal"
	query := url.Values{}
	query.Set("namespace", request.Namespace)
	query.Set("pod_name", request.PodName)
	query.Set("container_name", request.ContainerName)
	if request.Shell != "" {
		query.Set("shell", request.Shell)
	}
	if request.Cols > 0 {
		query.Set("cols", strconv.Itoa(request.Cols))
	}
	if request.Rows > 0 {
		query.Set("rows", strconv.Itoa(request.Rows))
	}
	endpoint.RawQuery = query.Encode()

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.Token)
	if c.orgOverride != "" {
		headers.Set(orgOverrideHeader, c.orgOverride)
	}
	connection, response, dialError := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{
		HTTPHeader: headers,
		HTTPClient: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}},
	})
	if dialError != nil {
		if response != nil {
			return nil, &UnexpectedResponseError{
				StatusCode: response.StatusCode,
				message:    fmt.Sprintf("the terminal handshake was refused with HTTP %d", response.StatusCode),
			}
		}
		return nil, fmt.Errorf("connecting to the pod terminal: %w", dialError)
	}
	connection.SetReadLimit(podTerminalReadLimitBytes)

	sessionContext, cancel := context.WithCancel(ctx)
	session := &podTerminalConnection{
		connection: connection,
		frames:     make(chan PodTerminalFrame, 64),
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	go session.readLoop(sessionContext)
	return session, nil
}

type podTerminalConnection struct {
	connection *websocket.Conn
	frames     chan PodTerminalFrame
	cancel     context.CancelFunc
	done       chan struct{}

	writeLock sync.Mutex
	closeOnce sync.Once

	stateLock        sync.Mutex
	lastErrorMessage string
	sawEnd           bool
	closeError       error
}

func (session *podTerminalConnection) Frames() <-chan PodTerminalFrame {
	return session.frames
}

func (session *podTerminalConnection) readLoop(ctx context.Context) {
	defer close(session.frames)
	for {
		messageType, payload, readError := session.connection.Read(ctx)
		if readError != nil {
			session.settle(ctx, readError)
			return
		}
		if messageType != websocket.MessageText {
			continue
		}
		var frame PodTerminalFrame
		if unmarshalError := json.Unmarshal(payload, &frame); unmarshalError != nil {
			continue
		}
		session.stateLock.Lock()
		switch frame.Type {
		case "error":
			session.lastErrorMessage = frame.Message
		case "end":
			session.sawEnd = true
		}
		session.stateLock.Unlock()
		select {
		case session.frames <- frame:
		case <-session.done:
			return
		case <-ctx.Done():
			session.settle(ctx, ctx.Err())
			return
		}
	}
}

// settle records why the session ended. A normal close, a close after the
// relay's own end frame, and a close the CLI asked for all count as a clean
// end; the relay's numbered refusals become the client's typed errors so
// the command layer maps them the way it maps the HTTP twins.
func (session *podTerminalConnection) settle(ctx context.Context, readError error) {
	session.stateLock.Lock()
	defer session.stateLock.Unlock()
	closeStatus := websocket.CloseStatus(readError)
	var closeError websocket.CloseError
	reason := ""
	if errors.As(readError, &closeError) {
		reason = closeError.Reason
	}
	switch {
	case closeStatus == websocket.StatusNormalClosure, session.sawEnd, ctx.Err() != nil && closeStatus == -1:
		session.closeError = nil
	case closeStatus == -1:
		session.closeError = fmt.Errorf("the terminal connection dropped: %w", readError)
	case closeStatus == podTerminalClosePermissionDenied:
		session.closeError = &PermissionDeniedError{Permission: "kubernetes.exec"}
	case closeStatus == podTerminalCloseClusterUnavailable:
		session.closeError = &ClusterUnavailableError{ErrorCode: "CLUSTER_OFFLINE", Detail: session.lastErrorMessage}
	case closeStatus == podTerminalCloseNoAgent:
		session.closeError = &ClusterUnavailableError{ErrorCode: "NO_AGENT", Detail: session.lastErrorMessage}
	case closeStatus == podTerminalCloseSandbox:
		session.closeError = &ClusterUnavailableError{ErrorCode: "SANDBOX_MODE", Detail: session.lastErrorMessage}
	default:
		session.closeError = &PodTerminalClosedError{Code: int(closeStatus), Reason: reason, Message: session.lastErrorMessage}
	}
}

func (session *podTerminalConnection) Err() error {
	session.stateLock.Lock()
	defer session.stateLock.Unlock()
	return session.closeError
}

func (session *podTerminalConnection) writeFrame(frame map[string]any) error {
	payload, marshalError := json.Marshal(frame)
	if marshalError != nil {
		return marshalError
	}
	session.writeLock.Lock()
	defer session.writeLock.Unlock()
	return session.connection.Write(context.Background(), websocket.MessageText, payload)
}

func (session *podTerminalConnection) SendInput(data []byte) error {
	return session.writeFrame(map[string]any{"type": "stdin", "data": base64.StdEncoding.EncodeToString(data)})
}

func (session *podTerminalConnection) Resize(cols int, rows int) error {
	return session.writeFrame(map[string]any{"type": "resize", "cols": cols, "rows": rows})
}

func (session *podTerminalConnection) Ping() error {
	return session.writeFrame(map[string]any{"type": "ping"})
}

func (session *podTerminalConnection) Close() error {
	var closeError error
	session.closeOnce.Do(func() {
		close(session.done)
		closeError = session.connection.Close(websocket.StatusNormalClosure, "")
		session.cancel()
	})
	return closeError
}

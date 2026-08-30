package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type terminalRelayBehaviour func(ctx context.Context, connection *websocket.Conn, request *http.Request)

func newTerminalRelay(t *testing.T, behaviour terminalRelayBehaviour) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, acceptError := websocket.Accept(writer, request, nil)
		if acceptError != nil {
			return
		}
		behaviour(request.Context(), connection, request)
	}))
	t.Cleanup(server.Close)
	return server
}

func relayFrame(ctx context.Context, connection *websocket.Conn, frame map[string]any) {
	payload, _ := json.Marshal(frame)
	_ = connection.Write(ctx, websocket.MessageText, payload)
}

func encodeTerminalData(text string) string {
	return base64.StdEncoding.EncodeToString([]byte(text))
}

func nextFrame(t *testing.T, session PodTerminal) PodTerminalFrame {
	t.Helper()
	select {
	case frame, isOpen := <-session.Frames():
		if !isOpen {
			t.Fatalf("the session ended early: %v", session.Err())
		}
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("no frame within 5s")
	}
	return PodTerminalFrame{}
}

func TestOpenPodTerminalRoundTrip(t *testing.T) {
	var seenRequest *http.Request
	server := newTerminalRelay(t, func(ctx context.Context, connection *websocket.Conn, request *http.Request) {
		seenRequest = request
		relayFrame(ctx, connection, map[string]any{"type": "connecting"})
		relayFrame(ctx, connection, map[string]any{"type": "connected"})
		for {
			_, payload, readError := connection.Read(ctx)
			if readError != nil {
				return
			}
			var frame map[string]any
			if unmarshalError := json.Unmarshal(payload, &frame); unmarshalError != nil {
				return
			}
			switch frame["type"] {
			case "stdin":
				data, _ := base64.StdEncoding.DecodeString(frame["data"].(string))
				if string(data) == "exit\n" {
					relayFrame(ctx, connection, map[string]any{"type": "end"})
					_ = connection.Close(websocket.StatusNormalClosure, "")
					return
				}
				relayFrame(ctx, connection, map[string]any{"type": "stdout", "data": encodeTerminalData("echo:" + string(data))})
			case "ping":
				relayFrame(ctx, connection, map[string]any{"type": "pong"})
			case "resize":
				relayFrame(ctx, connection, map[string]any{"type": "stdout",
					"data": encodeTerminalData(fmt.Sprintf("resize:%vx%v", frame["cols"], frame["rows"]))})
			}
		}
	})
	apiClient := New("secret-token", server.URL)
	apiClient.SetOrganisationOverride("11111111-2222-4333-8444-555555555555")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, openError := apiClient.OpenPodTerminal(ctx, "cluster-1", PodTerminalRequest{
		Namespace: "default", PodName: "web-1", ContainerName: "app", Shell: "/bin/bash", Cols: 120, Rows: 40,
	})
	if openError != nil {
		t.Fatalf("open: %v", openError)
	}
	defer func() { _ = session.Close() }()

	if frame := nextFrame(t, session); frame.Type != "connecting" {
		t.Fatalf("first frame = %+v", frame)
	}
	if frame := nextFrame(t, session); frame.Type != "connected" {
		t.Fatalf("second frame = %+v", frame)
	}
	if sendError := session.SendInput([]byte("pwd\n")); sendError != nil {
		t.Fatal(sendError)
	}
	echoed := nextFrame(t, session)
	payload, decodeError := echoed.Payload()
	if decodeError != nil || echoed.Type != "stdout" || string(payload) != "echo:pwd\n" {
		t.Fatalf("stdout frame = %+v (%v)", echoed, decodeError)
	}
	if pingError := session.Ping(); pingError != nil {
		t.Fatal(pingError)
	}
	if frame := nextFrame(t, session); frame.Type != "pong" {
		t.Fatalf("pong frame = %+v", frame)
	}
	if resizeError := session.Resize(100, 30); resizeError != nil {
		t.Fatal(resizeError)
	}
	resized := nextFrame(t, session)
	payload, _ = resized.Payload()
	if string(payload) != "resize:100x30" {
		t.Fatalf("resize echo = %q", payload)
	}
	if sendError := session.SendInput([]byte("exit\n")); sendError != nil {
		t.Fatal(sendError)
	}
	if frame := nextFrame(t, session); frame.Type != "end" {
		t.Fatalf("end frame = %+v", frame)
	}
	for range session.Frames() {
	}
	if closeError := session.Err(); closeError != nil {
		t.Fatalf("a session the relay ended normally reports %v", closeError)
	}

	if seenRequest == nil {
		t.Fatal("the relay never saw the handshake")
	}
	if seenRequest.Header.Get("Authorization") != "Bearer secret-token" {
		t.Errorf("authorization = %q", seenRequest.Header.Get("Authorization"))
	}
	if seenRequest.Header.Get(orgOverrideHeader) != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("organisation override was not sent: %q", seenRequest.Header.Get(orgOverrideHeader))
	}
	if seenRequest.URL.Path != "/api/v1/clusters/cluster-1/kubernetes/pod/terminal" {
		t.Errorf("path = %q", seenRequest.URL.Path)
	}
	query, _ := url.ParseQuery(seenRequest.URL.RawQuery)
	for key, expected := range map[string]string{
		"namespace": "default", "pod_name": "web-1", "container_name": "app", "shell": "/bin/bash", "cols": "120", "rows": "40",
	} {
		if query.Get(key) != expected {
			t.Errorf("query %s = %q, want %q", key, query.Get(key), expected)
		}
	}
}

func TestOpenPodTerminalMapsTheRelaysRefusals(t *testing.T) {
	cases := []struct {
		name    string
		code    websocket.StatusCode
		message string
		check   func(t *testing.T, closeError error)
	}{
		{
			name: "4403 is the exec permission", code: 4403, message: "Permission denied",
			check: func(t *testing.T, closeError error) {
				var denied *PermissionDeniedError
				if !errors.As(closeError, &denied) || denied.Permission != "kubernetes.exec" {
					t.Fatalf("got %v", closeError)
				}
			},
		},
		{
			name: "4002 is the cluster offline envelope", code: 4002, message: "Cluster is offline",
			check: func(t *testing.T, closeError error) {
				var unavailable *ClusterUnavailableError
				if !errors.As(closeError, &unavailable) || unavailable.ErrorCode != "CLUSTER_OFFLINE" || unavailable.Detail != "Cluster is offline" {
					t.Fatalf("got %v", closeError)
				}
			},
		},
		{
			name: "4003 is the no-agent envelope", code: 4003, message: "No agent connected",
			check: func(t *testing.T, closeError error) {
				var unavailable *ClusterUnavailableError
				if !errors.As(closeError, &unavailable) || unavailable.ErrorCode != "NO_AGENT" {
					t.Fatalf("got %v", closeError)
				}
			},
		},
		{
			name: "4001 is a refused credential", code: 4001, message: "Authentication required",
			check: func(t *testing.T, closeError error) {
				var closed *PodTerminalClosedError
				if !errors.As(closeError, &closed) || !closed.IsAuthentication() || closed.Error() != "Authentication required" {
					t.Fatalf("got %v", closeError)
				}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newTerminalRelay(t, func(ctx context.Context, connection *websocket.Conn, request *http.Request) {
				relayFrame(ctx, connection, map[string]any{"type": "error", "message": testCase.message})
				_ = connection.Close(testCase.code, testCase.message)
			})
			apiClient := New("secret-token", server.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			session, openError := apiClient.OpenPodTerminal(ctx, "cluster-1", PodTerminalRequest{Namespace: "default", PodName: "web-1", ContainerName: "app"})
			if openError != nil {
				t.Fatalf("open: %v", openError)
			}
			defer func() { _ = session.Close() }()
			sawErrorFrame := false
			for frame := range session.Frames() {
				if frame.Type == "error" && frame.Message == testCase.message {
					sawErrorFrame = true
				}
			}
			if !sawErrorFrame {
				t.Error("the error frame was not delivered before the close")
			}
			testCase.check(t, session.Err())
		})
	}
}

func TestOpenPodTerminalRefusedHandshake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	apiClient := New("secret-token", server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, openError := apiClient.OpenPodTerminal(ctx, "cluster-1", PodTerminalRequest{Namespace: "default", PodName: "web-1", ContainerName: "app"})
	var unexpected *UnexpectedResponseError
	if !errors.As(openError, &unexpected) || unexpected.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %v", openError)
	}
}

func TestOpenPodTerminalRequiresTheTarget(t *testing.T) {
	apiClient := New("secret-token", "https://api.example.test")
	if _, openError := apiClient.OpenPodTerminal(context.Background(), "cluster-1", PodTerminalRequest{Namespace: "default"}); openError == nil {
		t.Fatal("a request without a pod and container must be refused before dialing")
	}
}

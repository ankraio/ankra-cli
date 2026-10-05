package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// The hosted-logs switch routes are pinned on the request itself: a path or
// method typo fails as a 404 against a real platform and passes every
// command test that only checks the wiring.
func TestClusterHostedLogsLaneParity(t *testing.T) {
	lanes := []struct {
		name       string
		wantMethod string
		wantBody   map[string]any
		call       func(testClient *Client) (*ClusterHostedLogs, error)
	}{
		{
			name:       "get reads the cluster's switch",
			wantMethod: http.MethodGet,
			call: func(testClient *Client) (*ClusterHostedLogs, error) {
				return testClient.GetClusterHostedLogs(context.Background(), "cluster-1")
			},
		},
		{
			name:       "enable sends shipping_enabled true",
			wantMethod: http.MethodPut,
			wantBody:   map[string]any{"shipping_enabled": true},
			call: func(testClient *Client) (*ClusterHostedLogs, error) {
				return testClient.SetClusterHostedLogShipping(context.Background(), "cluster-1", true)
			},
		},
		{
			// false is the whole point of disable, so it must be sent, not
			// dropped as a zero value: the route refuses a body without it.
			name:       "disable sends shipping_enabled false",
			wantMethod: http.MethodPut,
			wantBody:   map[string]any{"shipping_enabled": false},
			call: func(testClient *Client) (*ClusterHostedLogs, error) {
				return testClient.SetClusterHostedLogShipping(context.Background(), "cluster-1", false)
			},
		},
	}

	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			var seenMethod, seenPath string
			var seenBody map[string]any
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				seenMethod, seenPath = request.Method, request.URL.EscapedPath()
				if request.Method != http.MethodGet {
					if decodeError := json.NewDecoder(request.Body).Decode(&seenBody); decodeError != nil {
						t.Fatalf("decode request body: %v", decodeError)
					}
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{"cluster_id":"cluster-1","shipping_enabled":true,"available":false,` +
					`"agent_supports_switch":true,"changed_at":"2026-10-05T20:00:00Z"}`))
			})

			state, callError := lane.call(testClient)
			if callError != nil {
				t.Fatalf("call error = %v", callError)
			}
			if seenMethod != lane.wantMethod {
				t.Errorf("method = %s, want %s", seenMethod, lane.wantMethod)
			}
			if seenPath != "/api/v1/org/clusters/cluster-1/hosted-logs" {
				t.Errorf("path = %s", seenPath)
			}
			if state.ClusterID != "cluster-1" || !state.ShippingEnabled || state.Available || !state.AgentSupportsSwitch ||
				state.ChangedAt == nil || *state.ChangedAt != "2026-10-05T20:00:00Z" {
				t.Errorf("decoded state = %+v", state)
			}
			if lane.wantBody == nil {
				return
			}
			if len(seenBody) != len(lane.wantBody) {
				t.Fatalf("body = %v, want exactly %v", seenBody, lane.wantBody)
			}
			for key, want := range lane.wantBody {
				if seenBody[key] != want {
					t.Errorf("body[%q] = %v, want %v", key, seenBody[key], want)
				}
			}
		})
	}
}

// A cluster id reaches the client straight from --cluster resolution, so it
// is escaped rather than concatenated into the path.
func TestClusterHostedLogsEscapesTheClusterID(t *testing.T) {
	var seenPath string
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		seenPath = request.URL.EscapedPath()
		jsonResponse(t, writer, http.StatusOK, ClusterHostedLogs{})
	})

	if _, getError := testClient.GetClusterHostedLogs(context.Background(), "a/../b"); getError != nil {
		t.Fatalf("GetClusterHostedLogs error = %v", getError)
	}
	if seenPath != "/api/v1/org/clusters/a%2F..%2Fb/hosted-logs" {
		t.Errorf("path = %s, want the id escaped into one segment", seenPath)
	}
}

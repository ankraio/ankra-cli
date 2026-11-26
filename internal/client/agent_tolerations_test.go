package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

// A path, method or body typo here fails against a real platform and passes
// every command-wiring test, so each call is pinned on the request itself.
// The clear is pinned byte for byte: the platform refuses a missing or null
// list, so an empty one must be sent as [].
func TestAgentTolerationsLaneParity(t *testing.T) {
	lanes := []struct {
		name       string
		wantMethod string
		wantBody   string
		call       func(testClient *Client) error
	}{
		{
			name:       "get reads the cluster's tolerations",
			wantMethod: http.MethodGet,
			call: func(testClient *Client) error {
				_, getError := testClient.GetAgentTolerations(context.Background(), "cluster-1")
				return getError
			},
		},
		{
			name:       "replace sends the whole list",
			wantMethod: http.MethodPut,
			wantBody: `{"tolerations":[{"key":"purpose","operator":"Equal","value":"db","effect":"NoSchedule"},` +
				`{"key":"dedicated","operator":"Exists","effect":"NoExecute"}]}`,
			call: func(testClient *Client) error {
				_, replaceError := testClient.ReplaceAgentTolerations(context.Background(), "cluster-1", []AgentToleration{
					{Key: "purpose", Operator: "Equal", Value: "db", Effect: "NoSchedule"},
					{Key: "dedicated", Operator: "Exists", Effect: "NoExecute"},
				})
				return replaceError
			},
		},
		{
			name:       "replacing with nothing sends an empty list",
			wantMethod: http.MethodPut,
			wantBody:   `{"tolerations":[]}`,
			call: func(testClient *Client) error {
				_, replaceError := testClient.ReplaceAgentTolerations(context.Background(), "cluster-1", nil)
				return replaceError
			},
		},
	}

	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			var seenMethod, seenPath, seenBody string
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				seenMethod, seenPath = request.Method, request.URL.Path
				body, readError := io.ReadAll(request.Body)
				if readError != nil {
					t.Errorf("reading the request body: %v", readError)
				}
				seenBody = string(body)
				jsonResponse(t, writer, http.StatusOK,
					AgentTolerations{ApplyState: AgentTolerationsApplyStateApplied})
			})

			if callError := lane.call(testClient); callError != nil {
				t.Fatalf("call error = %v", callError)
			}
			if seenMethod != lane.wantMethod {
				t.Errorf("method = %s, want %s", seenMethod, lane.wantMethod)
			}
			if seenPath != "/api/v1/org/clusters/cluster-1/agent/tolerations" {
				t.Errorf("path = %s", seenPath)
			}
			if seenBody != lane.wantBody {
				t.Errorf("body = %s, want %s", seenBody, lane.wantBody)
			}
		})
	}
}

func TestGetAgentTolerationsDecodesTheResponse(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"tolerations":[{"key":"purpose","operator":"Equal","value":"db",` +
			`"effect":"NoSchedule"}],"updated_at":"2026-11-25T10:00:00Z","apply_state":"pending_upgrade",` +
			`"message":"The tolerations are stored."}`))
	})

	tolerations, getError := testClient.GetAgentTolerations(context.Background(), "cluster-1")
	if getError != nil {
		t.Fatalf("GetAgentTolerations error = %v", getError)
	}
	want := AgentToleration{Key: "purpose", Operator: "Equal", Value: "db", Effect: "NoSchedule"}
	if len(tolerations.Tolerations) != 1 || tolerations.Tolerations[0] != want {
		t.Errorf("tolerations = %+v, want only %+v", tolerations.Tolerations, want)
	}
	if tolerations.UpdatedAt == nil || *tolerations.UpdatedAt != "2026-11-25T10:00:00Z" {
		t.Errorf("updated_at = %v", tolerations.UpdatedAt)
	}
	if tolerations.ApplyState != AgentTolerationsApplyStatePendingUpgrade ||
		tolerations.Message != "The tolerations are stored." {
		t.Errorf("apply state / message = %q / %q", tolerations.ApplyState, tolerations.Message)
	}
}

// A refusal carries the platform's own wording, and a 403 its permission.
func TestAgentTolerationsSurfaceTheRefusal(t *testing.T) {
	t.Run("422 keeps the detail", func(t *testing.T) {
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			jsonResponse(t, writer, http.StatusUnprocessableEntity,
				map[string]string{"detail": "The operator Exists takes no value"})
		})
		_, replaceError := testClient.ReplaceAgentTolerations(context.Background(), "cluster-1", nil)
		if replaceError == nil || replaceError.Error() != "The operator Exists takes no value" {
			t.Errorf("error = %v, want the server's detail unchanged", replaceError)
		}
	})
	t.Run("403 keeps the permission and answers no tolerations", func(t *testing.T) {
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			jsonResponse(t, writer, http.StatusForbidden,
				map[string]any{"detail": "permission_denied", "permission": "agents.manage"})
		})
		tolerations, getError := testClient.GetAgentTolerations(context.Background(), "cluster-1")
		var permissionError *PermissionDeniedError
		if !errors.As(getError, &permissionError) || permissionError.Permission != "agents.manage" || tolerations != nil {
			t.Fatalf("expected the permission refusal alone, got %+v, %v", tolerations, getError)
		}
	})
}

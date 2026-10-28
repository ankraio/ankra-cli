package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// The agent resources lane parity table. A path or method typo here fails as
// a 404 against a real platform and passes every command-wiring test that
// only checks the flag plumbing, so both routes are pinned on the request
// itself, as the agent CI settings lane is.
func TestAgentResourcesLaneParity(t *testing.T) {
	memoryLimit := "2Gi"
	cpuRequest := "500m"
	cleared := ""

	lanes := []struct {
		name       string
		wantMethod string
		wantBody   map[string]any
		call       func(testClient *Client) error
	}{
		{
			name:       "get reads the cluster's resources",
			wantMethod: http.MethodGet,
			call: func(testClient *Client) error {
				_, getError := testClient.GetAgentResources(context.Background(), "cluster-1")
				return getError
			},
		},
		{
			name:       "set sends only the named members",
			wantMethod: http.MethodPut,
			wantBody:   map[string]any{"memory_limit": "2Gi", "cpu_request": "500m"},
			call: func(testClient *Client) error {
				_, updateError := testClient.UpdateAgentResources(context.Background(), "cluster-1",
					AgentResourcesUpdate{MemoryLimit: &memoryLimit, CPURequest: &cpuRequest})
				return updateError
			},
		},
		{
			// "" clears a member back to the chart default, so it has to
			// survive marshalling rather than being dropped as empty.
			name:       "set sends an empty string that clears a member",
			wantMethod: http.MethodPut,
			wantBody:   map[string]any{"memory_limit": ""},
			call: func(testClient *Client) error {
				_, updateError := testClient.UpdateAgentResources(context.Background(), "cluster-1",
					AgentResourcesUpdate{MemoryLimit: &cleared})
				return updateError
			},
		},
	}

	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			var seenMethod, seenPath string
			var seenBody map[string]any
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				seenMethod, seenPath = request.Method, request.URL.Path
				if request.Method != http.MethodGet {
					if decodeError := json.NewDecoder(request.Body).Decode(&seenBody); decodeError != nil {
						t.Fatalf("decode request body: %v", decodeError)
					}
				}
				jsonResponse(t, writer, http.StatusOK, AgentResources{ApplyState: AgentResourcesApplyStateApplied})
			})

			if callError := lane.call(testClient); callError != nil {
				t.Fatalf("call error = %v", callError)
			}
			if seenMethod != lane.wantMethod {
				t.Errorf("method = %s, want %s", seenMethod, lane.wantMethod)
			}
			if seenPath != "/api/v1/org/clusters/cluster-1/agent/resources" {
				t.Errorf("path = %s", seenPath)
			}
			if lane.wantBody == nil {
				return
			}
			if len(seenBody) != len(lane.wantBody) {
				t.Fatalf("body = %v, want exactly %v", seenBody, lane.wantBody)
			}
			for key, want := range lane.wantBody {
				got, present := seenBody[key]
				if !present || got != want {
					t.Errorf("body[%q] = %v (present %v), want %v", key, got, present, want)
				}
			}
		})
	}
}

// Null members are "chart default", not "", and must decode as nil so the
// command can tell the two apart.
func TestGetAgentResourcesDecodesTheResponse(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"memory_limit":"2Gi","memory_request":null,"cpu_limit":null,` +
			`"cpu_request":"250m","updated_at":"2026-10-11T10:00:00Z","apply_state":"applied",` +
			`"message":"The agent is re-rendering its release with the new sizing."}`))
	})

	resources, getError := testClient.GetAgentResources(context.Background(), "cluster-1")
	if getError != nil {
		t.Fatalf("GetAgentResources error = %v", getError)
	}
	if resources.MemoryLimit == nil || *resources.MemoryLimit != "2Gi" {
		t.Errorf("memory_limit = %v", resources.MemoryLimit)
	}
	if resources.MemoryRequest != nil || resources.CPULimit != nil {
		t.Errorf("null members decoded as set: %+v", resources)
	}
	if resources.CPURequest == nil || *resources.CPURequest != "250m" {
		t.Errorf("cpu_request = %v", resources.CPURequest)
	}
	if resources.UpdatedAt == nil || *resources.UpdatedAt != "2026-10-11T10:00:00Z" {
		t.Errorf("updated_at = %v", resources.UpdatedAt)
	}
	if resources.ApplyState != AgentResourcesApplyStateApplied ||
		resources.Message != "The agent is re-rendering its release with the new sizing." {
		t.Errorf("apply state / message = %q / %q", resources.ApplyState, resources.Message)
	}
}

// A 422 carries the platform's own validation wording, which reaches the
// caller unchanged.
func TestUpdateAgentResourcesSurfacesTheValidationDetail(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusUnprocessableEntity,
			map[string]string{"detail": "memory_request 4Gi is above memory_limit 2Gi"})
	})
	memoryRequest := "4Gi"

	_, updateError := testClient.UpdateAgentResources(context.Background(), "cluster-1",
		AgentResourcesUpdate{MemoryRequest: &memoryRequest})
	if updateError == nil {
		t.Fatal("expected the 422 to fail the call")
	}
	if updateError.Error() != "memory_request 4Gi is above memory_limit 2Gi" {
		t.Errorf("error = %q, want the server's detail unchanged", updateError.Error())
	}
	var permissionError *PermissionDeniedError
	if errors.As(updateError, &permissionError) {
		t.Errorf("a 422 must not read as a permission refusal: %v", updateError)
	}
}

func TestAgentResourcesUpdateNamesAny(t *testing.T) {
	cleared := ""
	if (AgentResourcesUpdate{}).NamesAny() {
		t.Error("an empty update names nothing")
	}
	if !(AgentResourcesUpdate{CPULimit: &cleared}).NamesAny() {
		t.Error("an update clearing one member names it")
	}
}

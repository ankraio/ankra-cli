package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// The agent CI settings lane parity table. A path or method typo here fails
// as a 404 against a real platform and passes every command-wiring test that
// only checks the flag plumbing, so both routes are pinned on the request
// itself the way the bearer-lane application operations are.
func TestAgentCISettingsLaneParity(t *testing.T) {
	workerCount := 2
	storageClass := "proxmox-csi"

	lanes := []struct {
		name       string
		wantMethod string
		wantBody   map[string]any
		call       func(testClient *Client) error
	}{
		{
			name:       "get reads the cluster's settings",
			wantMethod: http.MethodGet,
			call: func(testClient *Client) error {
				_, getError := testClient.GetAgentCISettings(context.Background(), "cluster-1")
				return getError
			},
		},
		{
			name:       "set sends both members when both are named",
			wantMethod: http.MethodPut,
			wantBody:   map[string]any{"ci_worker_count": float64(2), "ci_storage_class": "proxmox-csi"},
			call: func(testClient *Client) error {
				_, updateError := testClient.UpdateAgentCISettings(context.Background(), "cluster-1",
					AgentCISettingsUpdate{CIWorkerCount: &workerCount, CIStorageClass: &storageClass})
				return updateError
			},
		},
		{
			// The whole point of the tri-state: a worker-count change must
			// not carry a storage class the caller never named, or it
			// resets one someone set earlier.
			name:       "set omits the storage class the caller did not name",
			wantMethod: http.MethodPut,
			wantBody:   map[string]any{"ci_worker_count": float64(2)},
			call: func(testClient *Client) error {
				_, updateError := testClient.UpdateAgentCISettings(context.Background(), "cluster-1",
					AgentCISettingsUpdate{CIWorkerCount: &workerCount})
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
				jsonResponse(t, writer, http.StatusOK, AgentCISettings{ApplyState: AgentCIApplyStateApplied})
			})

			if callError := lane.call(testClient); callError != nil {
				t.Fatalf("call error = %v", callError)
			}
			if seenMethod != lane.wantMethod {
				t.Errorf("method = %s, want %s", seenMethod, lane.wantMethod)
			}
			if seenPath != "/api/v1/org/clusters/cluster-1/agent/ci-settings" {
				t.Errorf("path = %s", seenPath)
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

// Zero is a real value on both members - 0 workers disables the scheduler
// and an empty storage class means the cluster default - so a pointer to a
// zero value has to survive marshalling rather than being dropped as empty.
func TestUpdateAgentCISettingsSendsExplicitZeroValues(t *testing.T) {
	workerCount := 0
	storageClass := ""
	var seenBody map[string]any
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if decodeError := json.NewDecoder(request.Body).Decode(&seenBody); decodeError != nil {
			t.Fatalf("decode request body: %v", decodeError)
		}
		jsonResponse(t, writer, http.StatusOK, AgentCISettings{})
	})

	if _, updateError := testClient.UpdateAgentCISettings(context.Background(), "cluster-1",
		AgentCISettingsUpdate{CIWorkerCount: &workerCount, CIStorageClass: &storageClass}); updateError != nil {
		t.Fatalf("UpdateAgentCISettings error = %v", updateError)
	}
	if _, present := seenBody["ci_worker_count"]; !present {
		t.Errorf("ci_worker_count 0 was dropped from the body: %v", seenBody)
	}
	if _, present := seenBody["ci_storage_class"]; !present {
		t.Errorf("ci_storage_class \"\" was dropped from the body: %v", seenBody)
	}
}

func TestGetAgentCISettingsDecodesTheResponse(t *testing.T) {
	updatedAt := "2026-09-06T10:00:00Z"
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusOK, AgentCISettings{
			CIWorkerCount:         4,
			CIStorageClass:        "proxmox-csi",
			AgentVersion:          "2.1.1107",
			SupportsPipelineSteps: true,
			ApplyState:            AgentCIApplyStateApplied,
			UpdatedAt:             &updatedAt,
		})
	})

	settings, getError := testClient.GetAgentCISettings(context.Background(), "cluster-1")
	if getError != nil {
		t.Fatalf("GetAgentCISettings error = %v", getError)
	}
	if settings.CIWorkerCount != 4 || settings.CIStorageClass != "proxmox-csi" {
		t.Errorf("settings = %+v", settings)
	}
	if !settings.SupportsPipelineSteps || settings.ApplyState != AgentCIApplyStateApplied {
		t.Errorf("capability/apply state = %+v", settings)
	}
	if settings.UpdatedAt == nil || *settings.UpdatedAt != updatedAt {
		t.Errorf("updated_at = %v", settings.UpdatedAt)
	}
}

// The three refusals the routes can answer with reach the caller as
// themselves: the RBAC 403 as a typed permission denial naming
// agents.manage, and the 404 and 422 carrying the server's own wording so
// the CLI never has to guess at a rule the platform owns.
func TestAgentCISettingsRefusalsSurfaceVerbatim(t *testing.T) {
	t.Run("403 names the permission", func(t *testing.T) {
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			jsonResponse(t, writer, http.StatusForbidden,
				map[string]any{"detail": "permission_denied", "permission": "agents.manage"})
		})

		_, updateError := testClient.UpdateAgentCISettings(context.Background(), "cluster-1",
			AgentCISettingsUpdate{})
		var denied *PermissionDeniedError
		if !errors.As(updateError, &denied) {
			t.Fatalf("error = %v, want *PermissionDeniedError", updateError)
		}
		if denied.Permission != "agents.manage" {
			t.Errorf("permission = %q, want agents.manage", denied.Permission)
		}
	})

	t.Run("404 keeps the server's wording and status", func(t *testing.T) {
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			jsonResponse(t, writer, http.StatusNotFound,
				map[string]any{"detail": "cluster not found in this organisation"})
		})

		_, getError := testClient.GetAgentCISettings(context.Background(), "cluster-1")
		var unexpected *UnexpectedResponseError
		if !errors.As(getError, &unexpected) {
			t.Fatalf("error = %v, want *UnexpectedResponseError", getError)
		}
		if unexpected.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", unexpected.StatusCode)
		}
		if unexpected.Error() != "cluster not found in this organisation" {
			t.Errorf("message = %q, want the server's detail verbatim", unexpected.Error())
		}
	})

	t.Run("422 keeps the server's validation wording", func(t *testing.T) {
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			jsonResponse(t, writer, http.StatusUnprocessableEntity,
				map[string]any{"detail": "ci_worker_count must be between 0 and 32"})
		})

		_, updateError := testClient.UpdateAgentCISettings(context.Background(), "cluster-1",
			AgentCISettingsUpdate{})
		if updateError == nil || updateError.Error() != "ci_worker_count must be between 0 and 32" {
			t.Fatalf("error = %v, want the server's detail verbatim", updateError)
		}
	})
}

// A cluster id reaches the client straight from --cluster resolution, so it
// is escaped rather than concatenated into the path.
func TestAgentCISettingsEscapesTheClusterID(t *testing.T) {
	var seenPath string
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		seenPath = request.URL.EscapedPath()
		jsonResponse(t, writer, http.StatusOK, AgentCISettings{})
	})

	if _, getError := testClient.GetAgentCISettings(context.Background(), "a/../b"); getError != nil {
		t.Fatalf("GetAgentCISettings error = %v", getError)
	}
	if seenPath != "/api/v1/org/clusters/a%2F..%2Fb/agent/ci-settings" {
		t.Errorf("path = %s, want the id escaped into one segment", seenPath)
	}
}

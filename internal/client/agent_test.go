package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestGetClusterAgent(t *testing.T) {
	agentVersion := "1.5.0"
	latestVersion := "1.6.0"
	checkedIn := "2025-06-01T12:00:00Z"
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/agent") || strings.Contains(r.URL.Path, "/upgrade") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, AgentInfo{
			UpgradeAvailable:   true,
			Upgrading:          false,
			CreatedAt:          "2025-01-01T00:00:00Z",
			CheckedInAt:        &checkedIn,
			AgentVersion:       &agentVersion,
			LatestAgentVersion: &latestVersion,
			DisableAutoUpgrade: false,
		})
	})
	got, err := testClient.GetClusterAgent("cluster-id")
	if err != nil {
		t.Fatalf("GetClusterAgent() error = %v", err)
	}
	if !got.UpgradeAvailable {
		t.Errorf("GetClusterAgent() expected UpgradeAvailable=true, got false")
	}
	if got.AgentVersion == nil || *got.AgentVersion != "1.5.0" {
		t.Errorf("GetClusterAgent() expected AgentVersion=1.5.0, got %v", got.AgentVersion)
	}
}

const agentInstallCommand = "helm upgrade --install ankra-agent oci://registry.ankra.cloud/ankra/ankra-agent " +
	"--version 2.1.439 --set config.ankra_url=https://platform.ankra.app " +
	"--set config.token=ank_cai_commandtoken --namespace=ankra --create-namespace"

func TestGetAgentToken(t *testing.T) {
	tests := []struct {
		name          string
		handler       http.HandlerFunc
		wantErr       bool
		wantToken     string
		wantClusterID string
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/cluster-agent/token") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, AgentToken{
					Token:     "existing-agent-token",
					ClusterID: "cluster-id",
					Command:   agentInstallCommand,
				})
			},
			wantErr:       false,
			wantToken:     "existing-agent-token",
			wantClusterID: "cluster-id",
		},
		{
			name: "command_only_response_extracts_token",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusOK, map[string]string{"command": agentInstallCommand})
			},
			wantErr:       false,
			wantToken:     "ank_cai_commandtoken",
			wantClusterID: "cluster-id",
		},
		{
			name: "unauthorized",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.GetAgentToken("cluster-id")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAgentToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if got.Token != tt.wantToken {
				t.Errorf("GetAgentToken() got.Token = %v, want %v", got.Token, tt.wantToken)
			}
			if got.ClusterID != tt.wantClusterID {
				t.Errorf("GetAgentToken() got.ClusterID = %v, want %v", got.ClusterID, tt.wantClusterID)
			}
		})
	}
}

func TestGenerateAgentToken(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/cluster-agent/token") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		jsonResponse(t, w, http.StatusOK, AgentToken{
			Token:     "new-agent-token",
			ClusterID: "cluster-id",
			Command:   agentInstallCommand,
		})
	})
	got, err := testClient.GenerateAgentToken(context.Background(), "cluster-id")
	if err != nil {
		t.Fatalf("GenerateAgentToken() error = %v", err)
	}
	if got.Token != "new-agent-token" {
		t.Errorf("GenerateAgentToken() got.Token = %v, want new-agent-token", got.Token)
	}
}

func TestGenerateAgentTokenCommandOnlyResponse(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusOK, map[string]string{"command": agentInstallCommand})
	})
	got, err := testClient.GenerateAgentToken(context.Background(), "cluster-id")
	if err != nil {
		t.Fatalf("GenerateAgentToken() error = %v", err)
	}
	if got.Token != "ank_cai_commandtoken" {
		t.Errorf("GenerateAgentToken() got.Token = %v, want the token extracted from the command", got.Token)
	}
	if got.ClusterID != "cluster-id" {
		t.Errorf("GenerateAgentToken() got.ClusterID = %v, want cluster-id", got.ClusterID)
	}
}

func TestUpgradeClusterAgent(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/agent/upgrade") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	got, err := testClient.UpgradeClusterAgent(context.Background(), "cluster-id", AgentUpgradeRequest{})
	if err != nil {
		t.Fatalf("UpgradeClusterAgent() error = %v", err)
	}
	if !got.Success {
		t.Errorf("UpgradeClusterAgent() got.Success = %v, want true", got.Success)
	}
}

// A plain upgrade posts no body, exactly as every CLI before --version did; a
// named version posts {version, allow_downgrade}.
func TestUpgradeClusterAgentSendsTheVersionOnlyWhenNamed(t *testing.T) {
	for name, testCase := range map[string]struct {
		request  AgentUpgradeRequest
		wantBody map[string]any
	}{
		"plain":     {AgentUpgradeRequest{}, nil},
		"a version": {AgentUpgradeRequest{Version: "2.1.1250"}, map[string]any{"version": "2.1.1250", "allow_downgrade": false}},
		"an allowed downgrade": {AgentUpgradeRequest{Version: "2.1.1247", AllowDowngrade: true},
			map[string]any{"version": "2.1.1247", "allow_downgrade": true}},
	} {
		var receivedBody []byte
		testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			receivedBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		})
		if _, upgradeError := testClient.UpgradeClusterAgent(context.Background(), "cluster-id", testCase.request); upgradeError != nil {
			t.Fatalf("%s: unexpected error %v", name, upgradeError)
		}
		if testCase.wantBody == nil {
			if len(receivedBody) != 0 {
				t.Errorf("%s: a plain upgrade sent a body: %s", name, receivedBody)
			}
			continue
		}
		var decoded map[string]any
		if decodeError := json.Unmarshal(receivedBody, &decoded); decodeError != nil {
			t.Fatalf("%s: body is not JSON: %v (%s)", name, decodeError, receivedBody)
		}
		if !reflect.DeepEqual(decoded, testCase.wantBody) {
			t.Errorf("%s: body = %v, want %v", name, decoded, testCase.wantBody)
		}
	}
}

// The platform's refusals of a named version arrive as its own sentence: 409
// is the downgrade refusal, 422 a release it cannot install. Any other status
// stays the generic unexpected-response error.
func TestUpgradeClusterAgentRelaysTheVersionRefusals(t *testing.T) {
	for name, testCase := range map[string]struct {
		status        int
		body          string
		wantRefused   bool
		wantDowngrade bool
	}{
		"older without allow_downgrade": {http.StatusConflict,
			`{"detail":"The agent runs 2.1.1250, which is newer than 2.1.1247. Pass allow_downgrade to downgrade it on purpose."}`, true, true},
		"unknown version": {http.StatusUnprocessableEntity,
			`{"detail":"Agent version 9.9.9 is not a published stable release or the version this agent runs."}`, true, false},
		"cluster offline": {http.StatusServiceUnavailable, `{"detail":"Cluster must be available to upgrade cluster agent"}`, false, false},
	} {
		testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(testCase.status)
			_, _ = w.Write([]byte(testCase.body))
		})
		_, upgradeError := testClient.UpgradeClusterAgent(context.Background(), "cluster-id",
			AgentUpgradeRequest{Version: "2.1.1247"})
		var refused *AgentUpgradeRefusedError
		isRefused := errors.As(upgradeError, &refused)
		if isRefused != testCase.wantRefused {
			t.Fatalf("%s: refused = %v (%v), want %v", name, isRefused, upgradeError, testCase.wantRefused)
		}
		if !isRefused {
			if upgradeError == nil {
				t.Fatalf("%s: expected an error", name)
			}
			continue
		}
		if refused.IsDowngradeRefusal() != testCase.wantDowngrade || refused.StatusCode != testCase.status {
			t.Errorf("%s: refusal = %+v", name, refused)
		}
		if !strings.Contains(testCase.body, refused.Error()) {
			t.Errorf("%s: message %q is not the platform's detail", name, refused.Error())
		}
	}
}

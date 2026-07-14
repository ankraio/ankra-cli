package client

import (
	"context"
	"net/http"
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
	got, err := testClient.UpgradeClusterAgent(context.Background(), "cluster-id")
	if err != nil {
		t.Fatalf("UpgradeClusterAgent() error = %v", err)
	}
	if !got.Success {
		t.Errorf("UpgradeClusterAgent() got.Success = %v, want true", got.Success)
	}
}

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

func TestGetAgentToken(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
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
					ExpiresAt: "2025-12-31",
					ClusterID: "cluster-id",
				})
			},
			wantErr: false,
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
			if !tt.wantErr && got.Token != "existing-agent-token" {
				t.Errorf("GetAgentToken() got.Token = %v, want existing-agent-token", got.Token)
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
			ExpiresAt: "2025-12-31",
			ClusterID: "cluster-id",
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

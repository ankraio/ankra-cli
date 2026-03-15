package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestGetClusterAgent(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/agent") || strings.Contains(r.URL.Path, "/upgrade") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, AgentInfo{
			ID:        "agent1",
			ClusterID: "cluster-id",
			Status:    "connected",
			Version:   "1.0.0",
			Healthy:   true,
		})
	})
	got, err := testClient.GetClusterAgent("cluster-id")
	if err != nil {
		t.Fatalf("GetClusterAgent() error = %v", err)
	}
	if got.ClusterID != "cluster-id" || !got.Healthy {
		t.Errorf("GetClusterAgent() got = %v", got)
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

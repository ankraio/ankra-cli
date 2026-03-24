package client

import (
	"net/http"
	"strings"
	"testing"
)

func TestListAPITokens(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/account/tokens" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, []APIToken{
			{ID: "token1", Name: "My Token", ExpiresAt: "2025-12-31", Revoked: false},
		})
	})
	got, err := testClient.ListAPITokens()
	if err != nil {
		t.Fatalf("ListAPITokens() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "My Token" {
		t.Errorf("ListAPITokens() got = %v", got)
	}
}

func TestCreateAPIToken(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/account/tokens" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusCreated, CreateAPITokenResponse{
			ID:        "new-token-id",
			Token:     "secret-token-value",
			ExpiresAt: "2025-12-31",
			Type:      "bearer",
		})
	})
	got, err := testClient.CreateAPIToken("New Token", nil)
	if err != nil {
		t.Fatalf("CreateAPIToken() error = %v", err)
	}
	if got.ID != "new-token-id" || got.Token != "secret-token-value" {
		t.Errorf("CreateAPIToken() got = %v", got)
	}
}

func TestRevokeAPIToken(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/tokens/token-id/revoke") {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			wantErr: false,
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("token not found"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.RevokeAPIToken("token-id")
			if (err != nil) != tt.wantErr {
				t.Errorf("RevokeAPIToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Success {
				t.Errorf("RevokeAPIToken() got.Success = false, want true")
			}
		})
	}
}

func TestDeleteAPIToken(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	got, err := testClient.DeleteAPIToken("token-id")
	if err != nil {
		t.Fatalf("DeleteAPIToken() error = %v", err)
	}
	if !got.Success {
		t.Errorf("DeleteAPIToken() got.Success = %v, want true", got.Success)
	}
}

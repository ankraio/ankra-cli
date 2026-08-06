package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestManagedK8sRoutesEscapePathsAndCarryTypedPayloads(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		wantPath string
		wantBody string
		response any
		invoke   func(*Client) error
	}{
		{
			name: "options", method: http.MethodGet,
			wantPath: "/api/v1/clusters/managed/kapsule/options?credential_id=cred%2Fid",
			response: ManagedOptions{},
			invoke: func(c *Client) error {
				_, err := c.ManagedOptions(context.Background(), "kapsule", "cred/id")
				return err
			},
		},
		{
			name: "create raw exact json", method: http.MethodPost,
			wantPath: "/api/v1/clusters/managed/kapsule",
			wantBody: `{"name":"x","count":1.00,"explicit":null}`,
			response: ManagedClusterCreateResponse{ClusterID: "id", Name: "x", Provenance: ManagedProvenanceCreated},
			invoke: func(c *Client) error {
				_, err := c.ManagedCreate(context.Background(), "kapsule", json.RawMessage(`{"name":"x","count":1.00,"explicit":null}`))
				return err
			},
		},
		{
			name: "pool scale", method: http.MethodPost,
			wantPath: "/api/v1/clusters/managed/kapsule/cluster%2Fid/node-pools/pool%2Fblue/scale",
			wantBody: `{"count":3}`,
			response: ManagedPoolOperationResponse{ClusterID: "id", NodePoolName: "pool/blue"},
			invoke: func(c *Client) error {
				_, err := c.ManagedScalePool(context.Background(), "kapsule", "cluster/id", "pool/blue", 3)
				return err
			},
		},
		{
			name: "pool update", method: http.MethodPatch,
			wantPath: "/api/v1/clusters/managed/kapsule/id/node-pools/workers",
			wantBody: `{"autoscaling_enabled":true,"autohealing":false}`,
			response: ManagedPoolOperationResponse{ClusterID: "id", NodePoolName: "workers"},
			invoke: func(c *Client) error {
				on, off := true, false
				_, err := c.ManagedUpdatePool(context.Background(), "kapsule", "id", "workers",
					ManagedPoolUpdateRequest{AutoscalingEnabled: &on, Autohealing: &off})
				return err
			},
		},
		{
			name: "provider delete", method: http.MethodPost,
			wantPath: "/api/v1/clusters/managed/kapsule/id/delete-provider-cluster?force=true&retention_policy=retain",
			response: ManagedDeprovisionResponse{Success: true, ClusterID: "id"},
			invoke: func(c *Client) error {
				_, err := c.ManagedDeleteProviderCluster(context.Background(), "kapsule", "id", true, "retain")
				return err
			},
		},
		{
			name: "upgrade operation", method: http.MethodPost,
			wantPath: "/api/v1/clusters/managed/kapsule/id/upgrade",
			wantBody: `{"version":"1.32"}`,
			response: ManagedUpgradeResponse{ClusterID: "id", Version: "1.32", OperationID: "op"},
			invoke: func(c *Client) error {
				result, err := c.ManagedUpgrade(context.Background(), "kapsule", "id", "1.32")
				if err == nil && result.OperationID != "op" {
					t.Fatalf("operation ID = %q", result.OperationID)
				}
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method {
					t.Errorf("method = %s, want %s", r.Method, test.method)
				}
				if r.URL.EscapedPath()+"?"+r.URL.RawQuery != test.wantPath && r.URL.EscapedPath() != test.wantPath {
					t.Errorf("path = %s?%s, want %s", r.URL.EscapedPath(), r.URL.RawQuery, test.wantPath)
				}
				if r.Header.Get("Authorization") != "Bearer "+testToken {
					t.Errorf("missing bearer auth")
				}
				if test.wantBody != "" {
					body, _ := io.ReadAll(r.Body)
					if string(body) != test.wantBody {
						t.Errorf("body = %s, want %s", body, test.wantBody)
					}
				}
				jsonResponse(t, w, http.StatusOK, test.response)
			})
			if err := test.invoke(c); err != nil {
				t.Fatalf("invoke: %v", err)
			}
		})
	}
}

func TestManagedK8sErrorsAreRedactedAndClassified(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, w, http.StatusBadRequest, map[string]any{
			"detail": "bad request", "secret_key": "do-not-leak", "access_key": "also-secret",
		})
	})
	_, err := c.ManagedStatus(context.Background(), "kapsule", "id")
	if err == nil {
		t.Fatal("expected error")
	}
	message := err.Error()
	if strings.Contains(message, "do-not-leak") || strings.Contains(message, "also-secret") {
		t.Fatalf("secret leaked in error: %s", message)
	}
	if !strings.Contains(message, "<redacted>") {
		t.Fatalf("redaction marker missing: %s", message)
	}
}

func TestManagedK8sUnauthorizedReturnsSentinel(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := c.ManagedStatus(context.Background(), "kapsule", "id")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
}

func TestManagedK8sHonoursContextCancellation(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusGatewayTimeout)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.ManagedStatus(ctx, "kapsule", "id")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

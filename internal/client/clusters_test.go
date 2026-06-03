package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestListClusters(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "200 with pagination",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/clusters" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, ClusterListResponse{
					Result: []ClusterListItem{
						{ID: "id1", Name: "cluster1", State: "online"},
					},
					Pagination: Pagination{TotalCount: 1, Page: 1, PageSize: 25, TotalPages: 1},
				})
			},
			wantErr: false,
		},
		{
			name: "401 error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.ListClusters(1, 25)
			if (err != nil) != tt.wantErr {
				t.Errorf("ListClusters() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && (got == nil || len(got.Result) != 1 || got.Result[0].Name != "cluster1") {
				t.Errorf("ListClusters() got = %v", got)
			}
		})
	}
}

func TestGetCluster(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.RawQuery, "cluster_name=my-cluster") {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				jsonResponse(t, w, http.StatusOK, ClusterListResponse{
					Result: []ClusterListItem{
						{ID: "id1", Name: "my-cluster", State: "online"},
					},
					Pagination: Pagination{TotalCount: 1, Page: 1, PageSize: 25, TotalPages: 1},
				})
			},
			wantErr: false,
		},
		{
			name: "not found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusOK, ClusterListResponse{
					Result:     []ClusterListItem{},
					Pagination: Pagination{TotalCount: 0, Page: 1, PageSize: 25, TotalPages: 0},
				})
			},
			wantErr: true,
		},
		{
			name: "prefix match is rejected",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusOK, ClusterListResponse{
					Result: []ClusterListItem{
						{ID: "id2", Name: "my-cluster-staging", State: "online"},
					},
					Pagination: Pagination{TotalCount: 1, Page: 1, PageSize: 25, TotalPages: 1},
				})
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.GetCluster("my-cluster")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetCluster() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.Name != "my-cluster" {
				t.Errorf("GetCluster() got.Name = %v, want my-cluster", got.Name)
			}
		})
	}
}

func TestDeleteCluster(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/clusters/my-cluster") {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			wantErr: false,
		},
		{
			name: "non-200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("server error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			err := testClient.DeleteCluster(context.Background(), "my-cluster")
			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteCluster() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTriggerReconcile(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusOK, TriggerReconcileResult{Success: true, Message: "reconciling"})
			},
			wantErr: false,
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("reconcile failed"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.TriggerReconcile(context.Background(), "cluster-id")
			if (err != nil) != tt.wantErr {
				t.Errorf("TriggerReconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Success {
				t.Errorf("TriggerReconcile() got.Success = %v, want true", got.Success)
			}
		})
	}
}

func TestProvisionCluster(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/provision") {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusOK, ProvisionClusterResult{MarkedToStartAt: "2025-06-01T00:00:00Z"})
			},
			wantErr: false,
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("provision failed"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.ProvisionCluster(context.Background(), "cluster-id")
			if (err != nil) != tt.wantErr {
				t.Errorf("ProvisionCluster() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.MarkedToStartAt == "" {
				t.Errorf("ProvisionCluster() got empty MarkedToStartAt")
			}
		})
	}
}

func TestDeprovisionCluster(t *testing.T) {
	tests := []struct {
		name       string
		autoDelete bool
		force      bool
		handler    http.HandlerFunc
		wantErr    bool
	}{
		{
			name:       "success without flags",
			autoDelete: false,
			force:      false,
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/deprovision") {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusOK, DeprovisionClusterResult{MarkedForDeprovisionAt: "2025-06-01T00:00:00Z"})
			},
			wantErr: false,
		},
		{
			name:       "success with auto_delete and force",
			autoDelete: true,
			force:      true,
			handler: func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.RawQuery
				if !strings.Contains(query, "auto_delete=true") || !strings.Contains(query, "force=true") {
					t.Errorf("expected auto_delete=true&force=true in query, got %s", query)
				}
				jsonResponse(t, w, http.StatusOK, DeprovisionClusterResult{MarkedForDeprovisionAt: "2025-06-01T00:00:00Z"})
			},
			wantErr: false,
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("deprovision failed"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.DeprovisionCluster(context.Background(), "cluster-id", tt.autoDelete, tt.force)
			if (err != nil) != tt.wantErr {
				t.Errorf("DeprovisionCluster() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.MarkedForDeprovisionAt == "" {
				t.Errorf("DeprovisionCluster() got empty MarkedForDeprovisionAt")
			}
		})
	}
}

func TestRollToClusterResourceVersion(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/resources/roll-to" {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusOK, RollToClusterResourceVersionResult{Ok: true})
			},
			wantErr: false,
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("roll-to failed"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.RollToClusterResourceVersion(context.Background(), "cluster-id", "version-1")
			if (err != nil) != tt.wantErr {
				t.Errorf("RollToClusterResourceVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Ok {
				t.Errorf("RollToClusterResourceVersion() got.Ok = false, want true")
			}
		})
	}
}

func TestApplyCluster(t *testing.T) {
	validRequest := CreateImportClusterRequest{
		Name:        "test-cluster",
		Description: "test",
		Spec: CreateResourceSpec{
			Stacks: []Stack{{Name: "stack1", Description: "desc"}},
		},
	}
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success with ImportResponse",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusOK, ImportResponse{
					Name:          "test-cluster",
					ClusterId:     "cluster-123",
					ImportCommand: "ankra cluster import ...",
				})
			},
			wantErr: false,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("internal server error"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.ApplyCluster(context.Background(), validRequest)
			if (err != nil) != tt.wantErr {
				t.Errorf("ApplyCluster() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && (got == nil || got.ClusterId != "cluster-123") {
				t.Errorf("ApplyCluster() got = %v", got)
			}
		})
	}
}

func TestValidateCluster(t *testing.T) {
	spec := CreateResourceSpec{Stacks: []Stack{{Name: "stack1"}}}

	t.Run("returns errors and warnings", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/validate" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			jsonResponse(t, w, http.StatusOK, ValidateClusterResponse{
				Errors: []ImportResponseResourceError{
					{Name: "nginx", Kind: "addon", Errors: []ImportResponseErrorItem{{Key: "chart_name", Message: "not found"}}},
				},
				Warnings: []ValidationWarning{
					{Kind: "manifest", Name: "db-secret", Key: "encrypted_paths", Message: "plaintext", Category: "secrets_plaintext"},
				},
			})
		}
		got, err := newTestClient(t, handler).ValidateCluster(context.Background(), spec, false, "")
		if err != nil {
			t.Fatalf("ValidateCluster() unexpected error: %v", err)
		}
		if len(got.Errors) != 1 || got.Errors[0].Name != "nginx" {
			t.Errorf("ValidateCluster() errors = %+v", got.Errors)
		}
		if len(got.Warnings) != 1 || got.Warnings[0].Category != "secrets_plaintext" {
			t.Errorf("ValidateCluster() warnings = %+v", got.Warnings)
		}
	})

	t.Run("forwards cluster_id query and strict_secrets body", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cluster_id") != "cluster-9" {
				t.Errorf("cluster_id = %q, want cluster-9", r.URL.Query().Get("cluster_id"))
			}
			var body ValidateClusterRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if !body.StrictSecrets {
				t.Errorf("strict_secrets = false, want true")
			}
			jsonResponse(t, w, http.StatusOK, ValidateClusterResponse{})
		}
		if _, err := newTestClient(t, handler).ValidateCluster(context.Background(), spec, true, "cluster-9"); err != nil {
			t.Fatalf("ValidateCluster() unexpected error: %v", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}
		if _, err := newTestClient(t, handler).ValidateCluster(context.Background(), spec, false, ""); err == nil {
			t.Error("ValidateCluster() expected error, got nil")
		}
	})
}

func TestCreateStackDraft(t *testing.T) {
	stack := Stack{Name: "web"}

	t.Run("created", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/cluster-1/stacks/draft" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			jsonResponse(t, w, http.StatusOK, map[string]any{"draft_id": "draft-123", "version": 1})
		}
		got, err := newTestClient(t, handler).CreateStackDraft(context.Background(), "cluster-1", stack)
		if err != nil {
			t.Fatalf("CreateStackDraft() unexpected error: %v", err)
		}
		if got.DraftID != "draft-123" || got.NoChange || len(got.Errors) != 0 {
			t.Errorf("CreateStackDraft() = %+v", got)
		}
	})

	t.Run("no changes maps to NoChange", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"no changes"}`))
		}
		got, err := newTestClient(t, handler).CreateStackDraft(context.Background(), "cluster-1", stack)
		if err != nil {
			t.Fatalf("CreateStackDraft() unexpected error: %v", err)
		}
		if !got.NoChange || got.DraftID != "" {
			t.Errorf("CreateStackDraft() = %+v, want NoChange", got)
		}
	})

	t.Run("validation errors surfaced", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(t, w, http.StatusUnprocessableEntity, map[string]any{
				"detail": []map[string]any{
					{"name": "grafana", "kind": "addon", "errors": []map[string]string{{"key": "chart_name", "message": "not found"}}},
				},
			})
		}
		got, err := newTestClient(t, handler).CreateStackDraft(context.Background(), "cluster-1", stack)
		if err != nil {
			t.Fatalf("CreateStackDraft() unexpected error: %v", err)
		}
		if len(got.Errors) != 1 || got.Errors[0].Name != "grafana" {
			t.Errorf("CreateStackDraft() errors = %+v", got.Errors)
		}
	})

	t.Run("server error", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}
		if _, err := newTestClient(t, handler).CreateStackDraft(context.Background(), "cluster-1", stack); err == nil {
			t.Error("CreateStackDraft() expected error, got nil")
		}
	})
}

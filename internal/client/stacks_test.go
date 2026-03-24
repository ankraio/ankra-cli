package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestListClusterStacks(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/stacks") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, ListClusterStacksResponse{
			Stacks: []ClusterStackListItem{
				{Name: "stack1", Description: "desc", State: "synced"},
			},
		})
	})
	got, err := testClient.ListClusterStacks("cluster-id")
	if err != nil {
		t.Fatalf("ListClusterStacks() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "stack1" {
		t.Errorf("ListClusterStacks() got = %v", got)
	}
}

func TestGetStackHistory(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/stacks/stack1/history") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, GetStackHistoryResponse{
			StackName: "stack1",
			History: []StackHistoryEntry{
				{ID: "v1", Version: 1, CreatedAt: "2025-01-01", ChangeType: "create"},
			},
		})
	})
	got, err := testClient.GetStackHistory("cluster-id", "stack1")
	if err != nil {
		t.Fatalf("GetStackHistory() error = %v", err)
	}
	if got.StackName != "stack1" || len(got.History) != 1 {
		t.Errorf("GetStackHistory() got = %v", got)
	}
}

func TestDeleteStack(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	got, err := testClient.DeleteStack(context.Background(), "cluster-id", "stack1")
	if err != nil {
		t.Fatalf("DeleteStack() error = %v", err)
	}
	if !got.Success {
		t.Errorf("DeleteStack() got.Success = %v, want true", got.Success)
	}
}

func TestRenameStack(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/rename-stack") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	got, err := testClient.RenameStack(context.Background(), "cluster-id", "stack1", "stack2")
	if err != nil {
		t.Fatalf("RenameStack() error = %v", err)
	}
	if !got.Success {
		t.Errorf("RenameStack() got.Success = %v, want true", got.Success)
	}
}

func TestCloneStackToCluster(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/stacks/clone") {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				jsonResponse(t, w, http.StatusOK, CloneStackToClusterResult{
					DraftID:         "draft-123",
					StackName:       "cloned-stack",
					AddonsCloned:    2,
					ManifestsCloned: 3,
				})
			},
			wantErr: false,
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("clone failed"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.CloneStackToCluster(context.Background(), "target-cluster-id", CloneStackToClusterRequest{
				SourceClusterID:            "source-cluster-id",
				StackName:                  "my-stack",
				IncludeAddonConfigurations: true,
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("CloneStackToCluster() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.DraftID != "draft-123" {
				t.Errorf("CloneStackToCluster() got.DraftID = %v, want draft-123", got.DraftID)
			}
		})
	}
}

func TestCreateStack(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/stacks") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	got, err := testClient.CreateStack(context.Background(), "cluster-id", "new-stack", "description")
	if err != nil {
		t.Fatalf("CreateStack() error = %v", err)
	}
	if !got.Success {
		t.Errorf("CreateStack() got.Success = %v, want true", got.Success)
	}
}

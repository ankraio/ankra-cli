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
			Pagination: Pagination{TotalCount: 1, Page: 1, PageSize: 100, TotalPages: 1},
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

// TestListClusterStacks_Paginates verifies the client walks every page
// instead of silently truncating at the backend's default page size.
func TestListClusterStacks_Paginates(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		stacks := []ClusterStackListItem{{Name: "stack-page-" + page}}
		jsonResponse(t, w, http.StatusOK, ListClusterStacksResponse{
			Stacks:     stacks,
			Pagination: Pagination{TotalCount: 2, Page: 1, PageSize: 100, TotalPages: 2},
		})
	})
	got, err := testClient.ListClusterStacks("cluster-id")
	if err != nil {
		t.Fatalf("ListClusterStacks() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != "stack-page-1" || got[1].Name != "stack-page-2" {
		t.Errorf("ListClusterStacks() got = %v, want stacks from pages 1 and 2", got)
	}
}

// TestListClusterStacksDecodesApplications pins that an application-backed
// stack does not arrive empty. The platform has always returned an
// `applications` array beside `manifests` and `addons`, but the client type
// omitted it, so a stack whose only member is an application decoded with
// zero members and rendered as an empty stack.
func TestListClusterStacksDecodesApplications(t *testing.T) {
	const responseBody = `{"stacks":[{"name":"deploy-website","description":"","state":"up",` +
		`"manifests":[],"addons":[],` +
		`"applications":[{"name":"website","namespace":"website",` +
		`"platform_application_id":"app-1","platform_application_version":"v7",` +
		`"state":"up","health":"healthy",` +
		`"parents":[{"name":"base","kind":"manifest"}],"delete_permanently":false}]}],` +
		`"pagination":{"total_count":1,"page":1,"page_size":100,"total_pages":1}}`

	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/stacks") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, writeError := w.Write([]byte(responseBody)); writeError != nil {
			t.Fatalf("writing test response: %v", writeError)
		}
	})

	stacks, err := testClient.ListClusterStacks("cluster-id")
	if err != nil {
		t.Fatalf("ListClusterStacks() error = %v", err)
	}
	if len(stacks) != 1 {
		t.Fatalf("ListClusterStacks() got %d stacks, want 1", len(stacks))
	}
	if len(stacks[0].Applications) != 1 {
		t.Fatalf("application members dropped: %+v", stacks[0])
	}

	application := stacks[0].Applications[0]
	if application.Name != "website" || application.Namespace != "website" {
		t.Errorf("application identity lost: %+v", application)
	}
	if application.PlatformApplicationID != "app-1" || application.PlatformApplicationVersion != "v7" {
		t.Errorf("application reference lost: %+v", application)
	}
	if application.State != "up" || application.Health != "healthy" {
		t.Errorf("application status lost: %+v", application)
	}
	if len(application.Parents) != 1 || application.Parents[0].Name != "base" {
		t.Errorf("application parents lost: %+v", application.Parents)
	}
}

// TestListClusterStacksToleratesNullApplicationMembers guards the nullable
// members of the wire shape: a stack with no namespace or version must not
// fail the whole listing.
func TestListClusterStacksToleratesNullApplicationMembers(t *testing.T) {
	const responseBody = `{"stacks":[{"name":"deploy-website",` +
		`"applications":[{"name":"website","namespace":null,` +
		`"platform_application_id":"app-1","platform_application_version":null,` +
		`"state":"up","health":null,"parents":[]}]}],` +
		`"pagination":{"total_count":1,"page":1,"page_size":100,"total_pages":1}}`

	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, writeError := w.Write([]byte(responseBody)); writeError != nil {
			t.Fatalf("writing test response: %v", writeError)
		}
	})

	stacks, err := testClient.ListClusterStacks("cluster-id")
	if err != nil {
		t.Fatalf("ListClusterStacks() error = %v", err)
	}
	if len(stacks) != 1 || len(stacks[0].Applications) != 1 {
		t.Fatalf("nullable members broke the listing: %+v", stacks)
	}
	application := stacks[0].Applications[0]
	if application.Namespace != "" || application.PlatformApplicationVersion != "" || application.Health != "" {
		t.Errorf("null members should decode empty, got %+v", application)
	}
}

func TestGetStackHistory(t *testing.T) {
	changeType := "create"
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/stacks/stack1/history") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonResponse(t, w, http.StatusOK, GetStackHistoryResponse{
			History: []StackHistoryItem{
				{
					ResourceName: "ingress",
					ResourceType: "addon",
					ResourceID:   "res-1",
					VersionHistory: []StackVersionHistoryEntry{
						{VersionID: "v1", ChangeType: &changeType},
					},
				},
			},
		})
	})
	got, err := testClient.GetStackHistory("cluster-id", "stack1")
	if err != nil {
		t.Fatalf("GetStackHistory() error = %v", err)
	}
	if len(got.History) != 1 || got.History[0].ResourceName != "ingress" {
		t.Errorf("GetStackHistory() got = %v", got)
	}
}

func TestGetStackAddonResourceID(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/stacks/core/history") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("resource_type") != "addon" {
			t.Errorf("resource_type = %s, want addon", r.URL.Query().Get("resource_type"))
		}
		jsonResponse(t, w, http.StatusOK, GetStackHistoryResponse{
			History: []StackHistoryItem{
				{ResourceName: "other", ResourceType: "addon", ResourceID: "res-0"},
				{ResourceName: "ingress", ResourceType: "addon", ResourceID: "res-1"},
			},
		})
	})
	got, err := testClient.GetStackAddonResourceID("cluster-id", "core", "ingress")
	if err != nil {
		t.Fatalf("GetStackAddonResourceID() error = %v", err)
	}
	if got != "res-1" {
		t.Errorf("GetStackAddonResourceID() = %q, want res-1", got)
	}

	if _, err := testClient.GetStackAddonResourceID("cluster-id", "core", "absent"); err == nil {
		t.Error("GetStackAddonResourceID() expected error for absent addon, got nil")
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

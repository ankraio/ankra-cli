package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
)

func TestGetWorkspaceProfilesReadsTheKinds(t *testing.T) {
	var gotPath, gotAuthorization string
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.Method + " " + request.URL.EscapedPath()
		gotAuthorization = request.Header.Get("Authorization")
		_, _ = io.WriteString(writer, `{"repository_id":"repo/1","profiles":{`+
			`"go":{"image":"golang:1.26","resources":{"cpu":"4","memory":"8Gi"},"volume_size":"40Gi",`+
			`"services":[{"name":"postgres","image":"postgres:17","ready":{"tcp":"5432"}}]},`+
			`"e2e":{}}}`)
	})
	profiles, readError := testClient.GetWorkspaceProfiles(context.Background(), "repo/1")
	if readError != nil {
		t.Fatalf("GetWorkspaceProfiles() error = %v", readError)
	}
	if gotPath != "GET /api/v1/org/pipeline-repositories/repo%2F1/workspace-profiles" {
		t.Errorf("request = %s", gotPath)
	}
	if gotAuthorization != "Bearer "+testToken {
		t.Errorf("Authorization = %q", gotAuthorization)
	}
	kinds := profiles.Kinds()
	sort.Strings(kinds)
	if strings.Join(kinds, ",") != "e2e,go" {
		t.Errorf("kinds = %v, want e2e and go", kinds)
	}
	if profile := profiles.Profiles["go"]; profile.Image != "golang:1.26" || profile.Resources.CPU != "4" ||
		profile.Resources.Memory != "8Gi" || profile.VolumeSize != "40Gi" {
		t.Errorf("go profile = %+v", profile)
	}
}

func TestGetWorkspaceProfilesOfARepositoryWithNone(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"repository_id":"repo-1","profiles":{}}`)
	})
	profiles, readError := testClient.GetWorkspaceProfiles(context.Background(), "repo-1")
	if readError != nil || len(profiles.Kinds()) != 0 {
		t.Fatalf("GetWorkspaceProfiles() = %+v, %v, want no kinds", profiles, readError)
	}
	var missing *WorkspaceProfiles
	if len(missing.Kinds()) != 0 {
		t.Fatal("a nil answer has no kinds")
	}
}

func TestGetWorkspaceProfilesRefusals(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.Contains(request.URL.Path, "/denied/"):
			writer.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(writer, `{"detail":"permission_denied","permission":"workspaces.manage"}`)
		case strings.Contains(request.URL.Path, "/gone/"):
			writer.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(writer, `{"detail":"Repository not found."}`)
		default:
			writer.WriteHeader(http.StatusUnauthorized)
		}
	})
	_, deniedError := testClient.GetWorkspaceProfiles(context.Background(), "denied")
	var permissionDenied *PermissionDeniedError
	if !errors.As(deniedError, &permissionDenied) || permissionDenied.Permission != "workspaces.manage" {
		t.Errorf("denied = %#v, want the workspaces.manage refusal", deniedError)
	}
	_, goneError := testClient.GetWorkspaceProfiles(context.Background(), "gone")
	if !IsWorkspaceNotFound(goneError) {
		t.Errorf("gone = %#v, want a workspace 404", goneError)
	}
	if _, unauthorizedError := testClient.GetWorkspaceProfiles(context.Background(), "x"); !errors.Is(unauthorizedError, ErrUnauthorized) {
		t.Errorf("unauthorized = %#v", unauthorizedError)
	}
}

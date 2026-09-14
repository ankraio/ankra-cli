package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Pins the related-repositories routes on the request itself: a path, method
// or query typo passes every command-wiring test and fails as a 404 or a 422
// against the platform.
func TestRelatedRepositoryRoutes(t *testing.T) {
	var seenMethod, seenPath, seenEscapedPath, seenProvider, seenInstallation string
	var seenBody map[string]any
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		seenMethod, seenPath, seenEscapedPath = request.Method, request.URL.Path, request.URL.EscapedPath()
		seenProvider = request.URL.Query().Get("provider")
		seenInstallation = request.URL.Query().Get("binding_external_id")
		seenBody = nil
		switch request.Method {
		case http.MethodGet:
			jsonResponse(t, writer, http.StatusOK, map[string]any{
				"related_repos": []RelatedRepository{{ID: "relation-1", Provider: "github",
					RepoFullName: "my-org/portal", RelatedRepoFullName: "my-org/api"}},
				"total": 1,
			})
		case http.MethodPost:
			if decodeError := json.NewDecoder(request.Body).Decode(&seenBody); decodeError != nil {
				t.Fatalf("decode request body: %v", decodeError)
			}
			jsonResponse(t, writer, http.StatusOK, RelatedRepository{ID: "relation-2", Provider: "github",
				RepoFullName: "my-org/web", RelatedRepoFullName: "my-org/shared"})
		case http.MethodDelete:
			writer.WriteHeader(http.StatusNoContent)
		}
	})
	ctx := context.Background()

	related, listError := testClient.ListRelatedRepositories(ctx, "135197959")
	if listError != nil || len(related) != 1 || related[0].RelatedRepoFullName != "my-org/api" {
		t.Fatalf("ListRelatedRepositories = %+v, %v", related, listError)
	}
	if seenMethod != http.MethodGet || seenPath != "/api/v1/org/ai-gateway/related-repos" {
		t.Errorf("list request = %s %s", seenMethod, seenPath)
	}
	if seenProvider != "github" || seenInstallation != "135197959" {
		t.Errorf("list query provider=%q binding_external_id=%q, want github/135197959", seenProvider, seenInstallation)
	}

	created, createError := testClient.CreateRelatedRepository(ctx, "135197959", "my-org/web", "my-org/shared")
	if createError != nil || created.ID != "relation-2" {
		t.Fatalf("CreateRelatedRepository = %+v, %v", created, createError)
	}
	if seenMethod != http.MethodPost || seenPath != "/api/v1/org/ai-gateway/related-repos" {
		t.Errorf("create request = %s %s", seenMethod, seenPath)
	}
	for field, expected := range map[string]string{
		"provider":               "github",
		"binding_external_id":    "135197959",
		"repo_full_name":         "my-org/web",
		"related_repo_full_name": "my-org/shared",
	} {
		if seenBody[field] != expected {
			t.Errorf("create body %s = %v, want %q", field, seenBody[field], expected)
		}
	}

	// An id is path-escaped: a slash or query character must stay inside the
	// one path segment, where the platform answers 404, rather than reaching
	// a different route.
	if deleteError := testClient.DeleteRelatedRepository(ctx, "relation/1?x"); deleteError != nil {
		t.Fatalf("DeleteRelatedRepository: %v", deleteError)
	}
	if seenMethod != http.MethodDelete || seenEscapedPath != "/api/v1/org/ai-gateway/related-repos/relation%2F1%3Fx" {
		t.Errorf("delete request = %s %s, want the id kept in one escaped segment", seenMethod, seenEscapedPath)
	}
}

// An answer without the list is not an organisation that stated nothing: only
// an explicit empty list may read as that.
func TestListRelatedRepositoriesTellsAMissingListFromAnEmptyOne(t *testing.T) {
	var answer map[string]any
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusOK, answer)
	})

	answer = map[string]any{"total": 0}
	if related, listError := testClient.ListRelatedRepositories(context.Background(), "1"); listError == nil {
		t.Errorf("an answer without related_repos = %+v, want an error", related)
	}

	answer = map[string]any{"related_repos": []any{}, "total": 0}
	related, listError := testClient.ListRelatedRepositories(context.Background(), "1")
	if listError != nil || related == nil || len(related) != 0 {
		t.Errorf("an explicit empty list = %+v, %v, want an empty non-nil list", related, listError)
	}
}

// The platform's refusal names the reason; it has to reach the user rather
// than a bare status code.
func TestCreateRelatedRepositorySurfacesThePlatformRefusal(t *testing.T) {
	const refusal = "Both repositories must be ones this GitHub App installation can access."
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusUnprocessableEntity, map[string]any{"detail": refusal})
	})

	_, createError := testClient.CreateRelatedRepository(context.Background(), "1", "my-org/web", "other-org/api")
	if createError == nil || !strings.Contains(createError.Error(), refusal) {
		t.Fatalf("CreateRelatedRepository error = %v, want the platform's refusal", createError)
	}
}

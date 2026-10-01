package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// scmBindingsFake serves the scm-bindings routes and records the writes.
type scmBindingsFake struct {
	bindings    []client.SCMBinding
	putPaths    []string
	putBodies   []map[string]interface{}
	deletePaths []string
	deleteRepos []string
	removed     bool
}

func (f *scmBindingsFake) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/org/ai-gateway/scm-bindings":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"bindings": f.bindings, "total": len(f.bindings)})
		case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/repo-overrides"):
			raw, _ := io.ReadAll(request.Body)
			body := map[string]interface{}{}
			_ = json.Unmarshal(raw, &body)
			f.putPaths = append(f.putPaths, request.URL.Path)
			f.putBodies = append(f.putBodies, body)
			body["success"] = true
			_ = json.NewEncoder(writer).Encode(body)
		case request.Method == http.MethodDelete && strings.HasSuffix(request.URL.Path, "/repo-overrides"):
			f.deletePaths = append(f.deletePaths, request.URL.Path)
			repository := request.URL.Query().Get("repo_full_name")
			f.deleteRepos = append(f.deleteRepos, repository)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"success": true, "repo_full_name": repository, "removed": f.removed,
			})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/repos"):
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"repositories": []client.SCMBindingRepository{{RepoFullName: "my-org/web", Name: "web"}},
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func twoBindings() []client.SCMBinding {
	return []client.SCMBinding{
		{
			Provider: "github", BindingExternalID: "111", DisplayName: "my-org", Detail: "GitHub App installation",
			AIReview: false, MentionReplies: true, PRPreviews: true,
			RepoOverrides: []client.SCMRepositoryRule{{RepoFullName: "my-org/api", AIReview: true, MentionReplies: false, PRPreviews: false}},
		},
		{
			Provider: "github", BindingExternalID: "222", DisplayName: "other-org", Detail: "GitHub App installation",
		},
	}
}

func runOrgAIReviewRepos(t *testing.T, server *httptest.Server, args ...string) (string, error) {
	t.Helper()
	useTestClient(t, server.URL)
	for _, command := range []*cobra.Command{orgAIReviewReposListCmd, orgAIReviewReposSetCmd, orgAIReviewReposUnsetCmd} {
		resetCommandFlags(t, command)
		command := command
		t.Cleanup(func() { resetCommandFlags(t, command) })
	}
	return executeCommand(append([]string{"org", "ai-review", "repos"}, args...)...)
}

func TestOrgAIReviewReposSetFillsUnspecifiedSwitchesFromTheBinding(t *testing.T) {
	fake := &scmBindingsFake{bindings: twoBindings()}
	server := fake.start(t)

	if _, err := runOrgAIReviewRepos(t, server, "set", "my-org/web", "--review"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if len(fake.putBodies) != 1 {
		t.Fatalf("expected one PUT, got %d", len(fake.putBodies))
	}
	if fake.putPaths[0] != "/api/v1/org/ai-gateway/scm-bindings/github/111/repo-overrides" {
		t.Errorf("binding resolved from the owner should be github/111, PUT went to %s", fake.putPaths[0])
	}
	body := fake.putBodies[0]
	if body["ai_review"] != true || body["mention_replies"] != true || body["pr_previews"] != true {
		t.Errorf("unspecified switches must come from the binding, body: %v", body)
	}
	for _, optional := range []string{"review_model", "review_drafts", "max_reviews_per_pr"} {
		if _, present := body[optional]; present {
			t.Errorf("%s was not passed and must be omitted, body: %v", optional, body)
		}
	}
}

func TestOrgAIReviewReposSetFillsFromTheExistingRule(t *testing.T) {
	fake := &scmBindingsFake{bindings: twoBindings()}
	server := fake.start(t)

	if _, err := runOrgAIReviewRepos(t, server, "set", "My-Org/API", "--previews", "--max-reviews-per-pr", "3"); err != nil {
		t.Fatalf("set: %v", err)
	}
	body := fake.putBodies[0]
	if body["ai_review"] != true || body["mention_replies"] != false || body["pr_previews"] != true {
		t.Errorf("unspecified switches must come from the existing rule, body: %v", body)
	}
	if body["max_reviews_per_pr"] != float64(3) {
		t.Errorf("max_reviews_per_pr = %v, want 3", body["max_reviews_per_pr"])
	}
}

func TestOrgAIReviewReposSetRefusesWithoutAnySwitch(t *testing.T) {
	fake := &scmBindingsFake{bindings: twoBindings()}
	server := fake.start(t)

	_, err := runOrgAIReviewRepos(t, server, "set", "my-org/web")
	if exitCodeFor(err) != exitUsage {
		t.Fatalf("exit code = %d, want usage: %v", exitCodeFor(err), err)
	}
	if len(fake.putBodies) != 0 {
		t.Errorf("nothing may be written, got %v", fake.putBodies)
	}
}

func TestOrgAIReviewReposSetRefusesAnAmbiguousBinding(t *testing.T) {
	fake := &scmBindingsFake{bindings: twoBindings()}
	server := fake.start(t)

	_, err := runOrgAIReviewRepos(t, server, "set", "third-org/web", "--review")
	if exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "--binding") {
		t.Fatalf("expected a usage error naming --binding, got %d: %v", exitCodeFor(err), err)
	}

	if _, err := runOrgAIReviewRepos(t, server, "set", "third-org/web", "--review", "--binding", "github/222"); err != nil {
		t.Fatalf("set with --binding: %v", err)
	}
	if fake.putPaths[0] != "/api/v1/org/ai-gateway/scm-bindings/github/222/repo-overrides" {
		t.Errorf("--binding must pick github/222, PUT went to %s", fake.putPaths[0])
	}
}

func TestOrgAIReviewReposUnsetNotFoundWhenNoRule(t *testing.T) {
	fake := &scmBindingsFake{bindings: twoBindings(), removed: false}
	server := fake.start(t)

	_, err := runOrgAIReviewRepos(t, server, "unset", "my-org/web", "--yes")
	if exitCodeFor(err) != exitNotFound {
		t.Fatalf("exit code = %d, want not found: %v", exitCodeFor(err), err)
	}
	if len(fake.deleteRepos) != 0 {
		t.Errorf("a repository with no rule in the listing must fail before any DELETE, got %v", fake.deleteRepos)
	}
}

func TestOrgAIReviewReposUnsetRemovesTheRule(t *testing.T) {
	fake := &scmBindingsFake{bindings: twoBindings(), removed: true}
	server := fake.start(t)

	output, err := runOrgAIReviewRepos(t, server, "unset", "my-org/api", "--yes")
	if err != nil {
		t.Fatalf("unset: %v", err)
	}
	if fake.deletePaths[0] != "/api/v1/org/ai-gateway/scm-bindings/github/111/repo-overrides" {
		t.Errorf("DELETE went to %s", fake.deletePaths[0])
	}
	if len(fake.deleteRepos) != 1 || fake.deleteRepos[0] != "my-org/api" {
		t.Errorf("DELETE must carry repo_full_name, got %v", fake.deleteRepos)
	}
	if !strings.Contains(output, "Removed the AI review rule for my-org/api") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestOrgAIReviewReposListReachableJSON(t *testing.T) {
	fake := &scmBindingsFake{bindings: twoBindings()}
	server := fake.start(t)

	output, err := runOrgAIReviewRepos(t, server, "list", "--reachable", "--binding", "github/111", "-o", "json")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listing scmBindingListing
	if decodeError := json.Unmarshal([]byte(output), &listing); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	if len(listing.Bindings) != 1 || listing.Bindings[0].BindingExternalID != "111" {
		t.Errorf("--binding must narrow the listing, got %+v", listing.Bindings)
	}
	if repositories := listing.ReachableRepositories["github/111"]; len(repositories) != 1 {
		t.Errorf("reachable repositories missing: %+v", listing.ReachableRepositories)
	}
}

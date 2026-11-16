package cmd

// ankra-3g40f8.6: `deploy-draft` of a stack that exists only as a draft posts
// the draft's own read, never the listing's entry. The listing elides every
// body (manifest_base64, from_file, an add-on's configuration), and the create
// write takes the request as the stack's content, so posting the entry shipped
// none of the draft's edits: the platform refused it, or deployed the stack
// with the live bodies under the draft's name.
//
// These run the real client against a fake platform, so what is checked is the
// request that reaches the wire.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ankra/internal/client"
)

const draftOnlyDraftID = "9f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f"

// draftOnlyListing is GET /stacks as the platform renders a draft-only stack:
// the members are named, their bodies are null.
const draftOnlyListing = `{"stacks":[{"name":"notes","draft_id":"` + draftOnlyDraftID + `","is_draft_only":true,
	"lifecycle":"draft_only","state":"draft","variables":{"replicas":"2"},
	"manifests":[{"name":"api","manifest_base64":null,"from_file":null,"encrypted_paths":[],"state":"draft"}],
	"addons":[{"name":"cache","chart_name":"redis","chart_version":"19.0.1","namespace":"data","configuration":null,"state":"draft"}],
	"applications":[]}],"pagination":{"total_pages":1}}`

// draftOnlyRead is GET /drafts/stacks/{draft_id}'s stack for the same draft:
// every body, as the draft stores it.
const draftOnlyRead = `{"name":"notes","draft_id":"` + draftOnlyDraftID + `","is_draft_only":false,"lifecycle":null,
	"state":"draft","variables":{"replicas":"2"},
	"manifests":[{"name":"api","manifest_base64":"a2luZDogQ29uZmlnTWFwCg==","from_file":"platform/manifests/api.yaml",
		"encrypted_paths":["data.PASSWORD"],"state":"draft"}],
	"addons":[{"name":"cache","chart_name":"redis","chart_version":"19.0.1","namespace":"data",
		"configuration":{"values_base64":"cmVwbGljYUNvdW50OiAzCg==","from_file":"platform/values/cache.yaml",
			"encrypted_paths":["auth.password"]},"state":"draft"}],
	"applications":[]}`

// draftOnlyPlatform is a fake platform for one draft-only stack. draftStatus
// and draftBody are what the draft read answers.
type draftOnlyPlatform struct {
	mutex       sync.Mutex
	draftStatus int
	draftBody   string
	draftReads  []string
	posts       []map[string]any
	unscripted  []string
}

// recorded answers what the fake platform saw, under its lock: the handler
// runs on the server's goroutines.
func (platform *draftOnlyPlatform) recorded() (draftReads []string, posts []map[string]any, unscripted []string) {
	platform.mutex.Lock()
	defer platform.mutex.Unlock()
	return append([]string(nil), platform.draftReads...), append([]map[string]any(nil), platform.posts...),
		append([]string(nil), platform.unscripted...)
}

func (platform *draftOnlyPlatform) postedStack(t *testing.T) map[string]any {
	t.Helper()
	platform.mutex.Lock()
	defer platform.mutex.Unlock()
	if len(platform.posts) != 1 {
		t.Fatalf("want exactly one create write, got %d", len(platform.posts))
	}
	specification, _ := platform.posts[0]["spec"].(map[string]any)
	stacks, _ := specification["stacks"].([]any)
	if len(stacks) != 1 {
		t.Fatalf("the create write must carry one stack, got %v", platform.posts[0])
	}
	stack, _ := stacks[0].(map[string]any)
	return stack
}

func serveDraftOnlyPlatform(t *testing.T, draftStatus int, draftBody string) *draftOnlyPlatform {
	t.Helper()
	platform := &draftOnlyPlatform{draftStatus: draftStatus, draftBody: draftBody}
	stacksPath := "/api/v1/org/clusters/imported/" + testClusterID + "/stacks"
	draftPath := "/api/v1/org/clusters/imported/" + testClusterID + "/drafts/stacks/"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		platform.mutex.Lock()
		defer platform.mutex.Unlock()
		switch {
		case request.Method == http.MethodGet && request.URL.Path == stacksPath:
			_, _ = writer.Write([]byte(draftOnlyListing))
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, draftPath):
			platform.draftReads = append(platform.draftReads, strings.TrimPrefix(request.URL.Path, draftPath))
			writer.WriteHeader(platform.draftStatus)
			_, _ = writer.Write([]byte(platform.draftBody))
		case request.Method == http.MethodPost && request.URL.Path == stacksPath:
			raw, _ := io.ReadAll(request.Body)
			posted := map[string]any{}
			if decodeError := json.Unmarshal(raw, &posted); decodeError != nil {
				t.Errorf("the create write's body is not a JSON object: %v: %s", decodeError, raw)
			}
			platform.posts = append(platform.posts, posted)
			_, _ = writer.Write([]byte(`{"stack_name":"notes","errors":[],"job_count":2,` +
				`"operation_id":"22222222-2222-2222-2222-222222222222"}`))
		default:
			platform.unscripted = append(platform.unscripted, request.Method+" "+request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"detail":"unscripted route in test"}`))
		}
	}))
	t.Cleanup(server.Close)
	writeSelectedClusterJSON(t)
	setMockClient(t, client.New("test-token", server.URL))
	return platform
}

func TestDeployDraftOfADraftOnlyStackPostsTheDraftsOwnRead(t *testing.T) {
	platform := serveDraftOnlyPlatform(t, http.StatusOK, `{"stack":`+draftOnlyRead+`}`)

	output, runError := executeCommand("cluster", "stacks", "deploy-draft", "notes")
	if runError != nil {
		t.Fatalf("deploy-draft: %v\n%s", runError, output)
	}
	draftReads, _, unscripted := platform.recorded()
	if len(draftReads) != 1 || draftReads[0] != draftOnlyDraftID {
		t.Fatalf("the draft the listing names must be read in full first, read %v", draftReads)
	}
	stack := platform.postedStack(t)
	encoded, _ := json.Marshal(stack)
	body := string(encoded)
	// The bodies the listing elides reach the write as the draft stores them.
	for _, fragment := range []string{
		`"manifest_base64":"a2luZDogQ29uZmlnTWFwCg=="`,
		`"from_file":"platform/manifests/api.yaml"`,
		`"encrypted_paths":["data.PASSWORD"]`,
		`"values_base64":"cmVwbGljYUNvdW50OiAzCg=="`,
		`"from_file":"platform/values/cache.yaml"`,
		`"encrypted_paths":["auth.password"]`,
		`"draft_id":"` + draftOnlyDraftID + `"`,
		`"variables":{"replicas":"2"}`,
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("the create write must carry %s from the draft read, got: %s", fragment, body)
		}
	}
	for _, fragment := range []string{`"manifest_base64":null`, `"configuration":null`, `"state"`} {
		if strings.Contains(body, fragment) {
			t.Fatalf("the create write must not carry %s, got: %s", fragment, body)
		}
	}
	if len(unscripted) != 0 {
		t.Fatalf("unexpected requests: %v", unscripted)
	}
	if !strings.Contains(output, "Draft deployed.") {
		t.Fatalf("an accepted deploy must say so:\n%s", output)
	}
}

func TestDeployDraftOfADraftOnlyStackRefusesADraftThatEndedSinceTheListing(t *testing.T) {
	platform := serveDraftOnlyPlatform(t, http.StatusNotFound,
		`{"detail":{"code":"DRAFT_TOMBSTONED","message":"The draft was discarded."}}`)

	_, runError := executeCommand("cluster", "stacks", "deploy-draft", "notes")
	if runError == nil {
		t.Fatal("a draft that ended since the listing must be refused")
	}
	if exitCodeFor(runError) != exitNotFound {
		t.Fatalf("exit code = %d, want %d (%v)", exitCodeFor(runError), exitNotFound, runError)
	}
	if !strings.Contains(runError.Error(), "has ended") {
		t.Fatalf("the refusal must say the draft ended: %v", runError)
	}
	if _, posts, _ := platform.recorded(); len(posts) != 0 {
		t.Fatalf("nothing may be deployed when the draft is gone, posted %v", posts)
	}
}

func TestDeployDraftOfADraftOnlyStackRefusesAReadOfAnotherDraft(t *testing.T) {
	for name, read := range map[string]string{
		"another draft": strings.Replace(draftOnlyRead, `"draft_id":"`+draftOnlyDraftID+`"`,
			`"draft_id":"8b8b8b8b-8b8b-8b8b-8b8b-8b8b8b8b8b8b"`, 1),
		"another stack": strings.Replace(draftOnlyRead, `"name":"notes"`, `"name":"other"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			platform := serveDraftOnlyPlatform(t, http.StatusOK, `{"stack":`+read+`}`)

			_, runError := executeCommand("cluster", "stacks", "deploy-draft", "notes")
			if runError == nil || !strings.Contains(runError.Error(), "nothing was deployed") {
				t.Fatalf("a read answering %s must be refused: %v", name, runError)
			}
			if exitCodeFor(runError) != exitError {
				t.Fatalf("exit code = %d, want %d (%v)", exitCodeFor(runError), exitError, runError)
			}
			if _, posts, _ := platform.recorded(); len(posts) != 0 {
				t.Fatalf("a read of %s must never be deployed, posted %v", name, posts)
			}
		})
	}
}

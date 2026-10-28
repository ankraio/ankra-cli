package cmd

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestDevKindForFamilyUsesTheFamilysProfileOnlyWhenItExists(t *testing.T) {
	cases := []struct {
		family   string
		profiles []string
		want     string
	}{
		{devFamilyGo, nil, defaultWorkspaceKind},
		{devFamilyGo, []string{"default"}, defaultWorkspaceKind},
		{devFamilyGo, []string{"go", "node"}, "go"},
		{devFamilyNode, []string{"go"}, defaultWorkspaceKind},
		{devFamilyNode, []string{"go", "node"}, "node"},
		{devFamilyE2E, []string{"node"}, defaultWorkspaceKind},
		{devFamilyE2E, []string{"playwright"}, "playwright"},
		{devFamilyE2E, []string{"playwright", "e2e"}, "e2e"},
		{"rust", []string{"rust"}, defaultWorkspaceKind},
	}
	for _, testCase := range cases {
		if got := devKindForFamily(testCase.family, testCase.profiles); got != testCase.want {
			t.Errorf("devKindForFamily(%q, %v) = %q, want %q", testCase.family, testCase.profiles, got, testCase.want)
		}
	}
}

// devKindTestCommand is a command whose root carries the persistent flags
// the cache key reads.
func devKindTestCommand() *cobra.Command {
	return devShimCmd
}

func TestDevWorkspaceKindReadsTheProfilesOnceAndCachesThem(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.profilesBody = `{"repository_id":"repo-1","profiles":{"go":{"image":"golang:1.26"},"e2e":{}}}`
	remote := repositoryRemote{Provider: "github", Owner: "acme", Name: "shop"}
	ctx := context.Background()

	if kind := devWorkspaceKind(ctx, devKindTestCommand(), remote, devFamilyGo); kind != "go" {
		t.Fatalf("go family kind = %q, want the repository's go profile", kind)
	}
	if kind := devWorkspaceKind(ctx, devKindTestCommand(), remote, devFamilyNode); kind != defaultWorkspaceKind {
		t.Fatalf("node family kind = %q, want default: the repository has no node profile", kind)
	}
	if kind := devWorkspaceKind(ctx, devKindTestCommand(), remote, devFamilyE2E); kind != "e2e" {
		t.Fatalf("e2e family kind = %q", kind)
	}
	if environment.api.profileReads != 1 {
		t.Fatalf("profile reads = %d, want one: the answer is cached", environment.api.profileReads)
	}

	// Past the TTL the next ask reads again, without listing the
	// repositories: the cache remembers its id.
	originalNow := execNow
	t.Cleanup(func() { execNow = originalNow })
	later := time.Now().Add(devKindCacheTTL + time.Minute)
	execNow = func() time.Time { return later }
	environment.api.profilesBody = `{"repository_id":"repo-1","profiles":{"node":{}}}`
	environment.api.repositories = nil
	if kind := devWorkspaceKind(ctx, devKindTestCommand(), remote, devFamilyNode); kind != "node" {
		t.Fatalf("after the TTL the node family kind = %q, want the new node profile", kind)
	}
	if environment.api.profileReads != 2 {
		t.Fatalf("profile reads = %d, want a second one after the TTL", environment.api.profileReads)
	}
}

func TestDevWorkspaceKindFallsBackToTheDefaultWhenTheReadIsRefused(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.profilesStatus = http.StatusForbidden
	environment.api.profilesBody = `{"detail":"permission_denied","permission":"workspaces.manage"}`
	remote := repositoryRemote{Provider: "github", Owner: "acme", Name: "shop"}
	ctx := context.Background()
	for range 3 {
		if kind := devWorkspaceKind(ctx, devKindTestCommand(), remote, devFamilyGo); kind != defaultWorkspaceKind {
			t.Fatalf("kind = %q, want default when the profiles may not be read", kind)
		}
	}
	if environment.api.profileReads != 1 {
		t.Fatalf("profile reads = %d, want the refusal cached", environment.api.profileReads)
	}
}

func TestDevWorkspaceKindNeverFailsAndKeepsTheLastAnswerThroughAnOutage(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.profilesBody = `{"repository_id":"repo-1","profiles":{"go":{}}}`
	remote := repositoryRemote{Provider: "github", Owner: "acme", Name: "shop"}
	ctx := context.Background()
	if kind := devWorkspaceKind(ctx, devKindTestCommand(), remote, devFamilyGo); kind != "go" {
		t.Fatalf("kind = %q", kind)
	}

	originalNow := execNow
	t.Cleanup(func() { execNow = originalNow })
	later := time.Now().Add(devKindCacheTTL + time.Minute)
	execNow = func() time.Time { return later }
	environment.api.profilesStatus = http.StatusInternalServerError
	environment.api.profilesBody = `{"error_code":"AGENT_TIMEOUT","detail":"down"}`
	if kind := devWorkspaceKind(ctx, devKindTestCommand(), remote, devFamilyGo); kind != "go" {
		t.Fatalf("kind during an outage = %q, want the stale go answer kept", kind)
	}

	// A cache that never had an answer, and an unreachable platform: the
	// default workspace.
	_ = os.RemoveAll(execRepositoryCachePath(devKindTestCommand(), remote, "dev-kinds"))
	if kind := devWorkspaceKind(ctx, devKindTestCommand(), remote, devFamilyGo); kind != defaultWorkspaceKind {
		t.Fatalf("kind with no answer at all = %q, want default", kind)
	}
	if entry, _ := readDevKindCache(execRepositoryCachePath(devKindTestCommand(), remote, "dev-kinds"), later); entry != nil {
		t.Fatalf("a failed read was cached: %+v", entry)
	}
}

func TestDevWorkspaceKindOfARepositoryNotConnected(t *testing.T) {
	environment := newExecTestEnv(t)
	environment.api.repositories = nil
	remote := repositoryRemote{Provider: "github", Owner: "acme", Name: "shop"}
	if kind := devWorkspaceKind(context.Background(), devKindTestCommand(), remote, devFamilyGo); kind != defaultWorkspaceKind {
		t.Fatalf("kind = %q", kind)
	}
	if environment.api.profileReads != 0 {
		t.Fatal("the profiles of a repository that is not connected were read")
	}
}

func TestReadDevKindCacheRefusesGarbageAndTheFuture(t *testing.T) {
	directory := t.TempDir()
	path := directory + "/entry"
	now := time.Now()
	if entry, isFresh := readDevKindCache(path, now); entry != nil || isFresh {
		t.Fatal("a missing file is an answer")
	}
	writeTestFile(t, path, "not json")
	if entry, _ := readDevKindCache(path, now); entry != nil {
		t.Fatal("garbage is an answer")
	}
	writeDevKindCache(path, &devKindCacheEntry{Kinds: []string{"go"}, FetchedAt: now.Add(time.Hour).Unix()})
	if entry, isFresh := readDevKindCache(path, now); entry == nil || isFresh {
		t.Fatalf("an entry from the future = %+v fresh %t, want it read but stale", entry, isFresh)
	}
}

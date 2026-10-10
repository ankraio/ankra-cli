package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// execTestRepository is a clone of a bare "origin" with one commit on main,
// origin/HEAD set, the way a developer's checkout looks.
type execTestRepository struct {
	origin string
	top    string
}

func gitTestOutput(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(gitEnvironment(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	output, runError := command.CombinedOutput()
	if runError != nil {
		t.Fatalf("git %v failed: %v\n%s", arguments, runError, output)
	}
	return strings.TrimSpace(string(output))
}

func newExecTestRepository(t *testing.T, remoteURL string) execTestRepository {
	t.Helper()
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitTestOutput(t, root, "init", "--bare", "--initial-branch", "main", origin)
	seed := filepath.Join(root, "seed")
	gitTestOutput(t, root, "clone", "-q", origin, seed)
	writeTestFile(t, filepath.Join(seed, "README.md"), "hello\n")
	writeTestFile(t, filepath.Join(seed, ".gitignore"), "ignored/\n")
	writeTestFile(t, filepath.Join(seed, "pkg", "a.go"), "package pkg\n")
	gitTestOutput(t, seed, "add", "-A")
	gitTestOutput(t, seed, "commit", "-q", "-m", "seed")
	gitTestOutput(t, seed, "push", "-q", "origin", "HEAD:main")
	top := filepath.Join(root, "work")
	gitTestOutput(t, root, "clone", "-q", origin, top)
	gitTestOutput(t, top, "remote", "set-url", "origin", remoteURL)
	resolvedTop, resolveError := filepath.EvalSymlinks(top)
	if resolveError != nil {
		t.Fatalf("resolve %s: %v", top, resolveError)
	}
	return execTestRepository{origin: origin, top: resolvedTop}
}

func writeTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	if mkdirError := os.MkdirAll(filepath.Dir(path), 0o755); mkdirError != nil {
		t.Fatal(mkdirError)
	}
	if writeError := os.WriteFile(path, []byte(contents), 0o644); writeError != nil {
		t.Fatal(writeError)
	}
}

func snapshotFor(t *testing.T, repository execTestRepository) execSnapshot {
	t.Helper()
	ctx := context.Background()
	checkout, checkoutError := inspectWorkspaceCheckout(ctx, repository.top)
	if checkoutError != nil {
		t.Fatalf("inspectWorkspaceCheckout() error = %v", checkoutError)
	}
	head := gitTestOutput(t, repository.top, "rev-parse", "HEAD")
	snapshot, snapshotError := takeExecSnapshot(ctx, checkout, head, t.TempDir())
	if snapshotError != nil {
		t.Fatalf("takeExecSnapshot() error = %v", snapshotError)
	}
	return snapshot
}

func TestExecSnapshotOfACleanTreeIsHead(t *testing.T) {
	repository := newExecTestRepository(t, "git@github.com:acme/shop.git")
	snapshot := snapshotFor(t, repository)
	if snapshot.Snapshot != snapshot.Head {
		t.Fatalf("snapshot of a clean tree = %s, want HEAD %s", snapshot.Snapshot, snapshot.Head)
	}
	if snapshot.BaseRef != "refs/remotes/origin/main" || snapshot.BaseSHA != snapshot.Head {
		t.Fatalf("base = %s %s, want refs/remotes/origin/main %s", snapshot.BaseRef, snapshot.BaseSHA, snapshot.Head)
	}
	if got := gitTestOutput(t, repository.top, "rev-parse", snapshot.snapshotRef()); got != snapshot.Head {
		t.Fatalf("snapshot ref = %s, want %s", got, snapshot.Head)
	}
}

func TestExecSnapshotTakesTheWorktreeAndLeavesTheIndexAlone(t *testing.T) {
	repository := newExecTestRepository(t, "git@github.com:acme/shop.git")
	writeTestFile(t, filepath.Join(repository.top, "README.md"), "changed\n")
	writeTestFile(t, filepath.Join(repository.top, "new.txt"), "untracked\n")
	writeTestFile(t, filepath.Join(repository.top, "ignored", "cache.bin"), "ignored\n")
	statusBefore := gitTestOutput(t, repository.top, "status", "--porcelain")

	first := snapshotFor(t, repository)
	if first.Snapshot == first.Head {
		t.Fatal("a dirty tree must not snapshot as HEAD")
	}
	if got := gitTestOutput(t, repository.top, "show", first.Snapshot+":README.md"); got != "changed" {
		t.Fatalf("snapshot README.md = %q, want the working tree's", got)
	}
	if got := gitTestOutput(t, repository.top, "show", first.Snapshot+":new.txt"); got != "untracked" {
		t.Fatalf("snapshot new.txt = %q, want the untracked file", got)
	}
	files := gitTestOutput(t, repository.top, "ls-tree", "-r", "--name-only", first.Snapshot)
	if strings.Contains(files, "ignored/") {
		t.Fatalf("the snapshot carries an ignored file:\n%s", files)
	}
	if parent := gitTestOutput(t, repository.top, "rev-parse", first.Snapshot+"^"); parent != first.Head {
		t.Fatalf("snapshot parent = %s, want HEAD %s", parent, first.Head)
	}
	if statusAfter := gitTestOutput(t, repository.top, "status", "--porcelain"); statusAfter != statusBefore {
		t.Fatalf("the snapshot changed the index or tree:\nbefore %q\nafter  %q", statusBefore, statusAfter)
	}
	// Deterministic: the same tree is the same commit.
	second := snapshotFor(t, repository)
	if second.Snapshot != first.Snapshot {
		t.Fatalf("an unchanged tree snapshotted as %s then %s", first.Snapshot, second.Snapshot)
	}
}

func TestExecAgentIDMatchesTheBashClient(t *testing.T) {
	agent := execAgentID("/Users/jane/src/shop")
	// printf '%s' /Users/jane/src/shop | shasum | cut -c1-12
	if !strings.HasSuffix(agent, "-83b9a9dd1d52") {
		t.Fatalf("execAgentID() = %q, want the shasum suffix 83b9a9dd1d52", agent)
	}
	userPart := strings.TrimSuffix(agent, "-83b9a9dd1d52")
	for _, character := range userPart {
		isAlphanumeric := (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9')
		if !isAlphanumeric && character != '-' {
			t.Fatalf("execAgentID() user part %q carries %q", userPart, character)
		}
	}
}

func TestExecBundleThinAfterSyncAndAppliesOntoTheWorkspace(t *testing.T) {
	repository := newExecTestRepository(t, "git@github.com:acme/shop.git")
	ctx := context.Background()
	markerPrefix := execMarkerPrefix("ws-1")
	scratch := t.TempDir()

	snapshot := snapshotFor(t, repository)
	full, fullError := makeExecBundle(ctx, snapshot, markerPrefix, true, filepath.Join(scratch, "full.bundle"))
	if fullError != nil {
		t.Fatalf("makeExecBundle() error = %v", fullError)
	}
	if full.SizeBytes == 0 || len(full.SHA256) != 64 {
		t.Fatalf("first bundle = %+v, want the whole history", full)
	}
	// The workspace's repository: a fresh one that unbundles what it is sent.
	workspaceRepository := filepath.Join(scratch, "workspace")
	gitTestOutput(t, scratch, "init", "-q", "--bare", workspaceRepository)
	gitTestOutput(t, workspaceRepository, "fetch", "-q", full.Path, "refs/*:refs/synced/*")

	if recordError := recordExecSynced(ctx, snapshot, markerPrefix, time.Unix(1000, 0)); recordError != nil {
		t.Fatalf("recordExecSynced() error = %v", recordError)
	}
	empty, emptyError := makeExecBundle(ctx, snapshotFor(t, repository), markerPrefix, true,
		filepath.Join(scratch, "empty.bundle"))
	if emptyError != nil || empty.SizeBytes != 0 {
		t.Fatalf("bundle with nothing missing = %+v, %v; want empty", empty, emptyError)
	}

	writeTestFile(t, filepath.Join(repository.top, "pkg", "b.go"), "package pkg\n\nvar B = 1\n")
	changed := snapshotFor(t, repository)
	thin, thinError := makeExecBundle(ctx, changed, markerPrefix, true, filepath.Join(scratch, "thin.bundle"))
	if thinError != nil || thin.SizeBytes == 0 {
		t.Fatalf("thin bundle = %+v, %v", thin, thinError)
	}
	if thin.SizeBytes >= full.SizeBytes*4 {
		t.Fatalf("thin bundle (%d bytes) is not thin next to the full one (%d)", thin.SizeBytes, full.SizeBytes)
	}
	// It names the snapshot's parent as a prerequisite and applies onto the
	// workspace that holds it.
	verify := gitTestOutput(t, workspaceRepository, "bundle", "verify", thin.Path)
	if !strings.Contains(verify, "okay") && !strings.Contains(verify, changed.Head) {
		t.Fatalf("git bundle verify said:\n%s", verify)
	}
	gitTestOutput(t, workspaceRepository, "fetch", "-q", thin.Path, "refs/*:refs/synced2/*")
	if got := gitTestOutput(t, workspaceRepository, "show", changed.Snapshot+":pkg/b.go"); !strings.Contains(got, "var B = 1") {
		t.Fatalf("the workspace does not see the change: %q", got)
	}

	// A full bundle ignores the markers.
	again, againError := makeExecBundle(ctx, changed, markerPrefix, false, filepath.Join(scratch, "again.bundle"))
	if againError != nil || again.SizeBytes <= thin.SizeBytes {
		t.Fatalf("full bundle = %+v, %v; want more than the thin %d bytes", again, againError, thin.SizeBytes)
	}
}

func TestExecMarkersKeepTheNewestForty(t *testing.T) {
	repository := newExecTestRepository(t, "git@github.com:acme/shop.git")
	ctx := context.Background()
	markerPrefix := execMarkerPrefix("ws-2")
	snapshot := snapshotFor(t, repository)
	for index := 0; index < 25; index++ {
		if recordError := recordExecSynced(ctx, snapshot, markerPrefix, time.Unix(int64(1_700_000_000+index), 0)); recordError != nil {
			t.Fatalf("recordExecSynced() error = %v", recordError)
		}
	}
	names, _, listError := execMarkers(ctx, repository.top, markerPrefix)
	if listError != nil {
		t.Fatal(listError)
	}
	if len(names) != execKeptMarkers {
		t.Fatalf("markers kept = %d, want %d", len(names), execKeptMarkers)
	}
	for _, name := range names {
		if strings.Contains(name, "/1700000000-") || strings.Contains(name, "/1700000004-") {
			t.Fatalf("an old marker survived: %s", name)
		}
	}
}

func TestExecShallowMarkersDroppedOnceTheCloneIsComplete(t *testing.T) {
	repository := newExecTestRepository(t, "git@github.com:acme/shop.git")
	ctx := context.Background()
	markerPrefix := execMarkerPrefix("ws-3")
	snapshot := snapshotFor(t, repository)
	shallowSnapshot := snapshot
	shallowSnapshot.Shallow = []string{snapshot.Head}
	if recordError := recordExecSynced(ctx, shallowSnapshot, markerPrefix, time.Unix(1, 0)); recordError != nil {
		t.Fatal(recordError)
	}
	names, _, _ := execMarkers(ctx, repository.top, markerPrefix)
	if len(names) == 0 || !strings.HasSuffix(names[0], execShallowTag) {
		t.Fatalf("shallow markers = %v, want the %s tag", names, execShallowTag)
	}
	// Still shallow: the markers stay.
	if reconcileError := reconcileShallowMarkers(ctx, shallowSnapshot, markerPrefix); reconcileError != nil {
		t.Fatal(reconcileError)
	}
	if names, _, _ = execMarkers(ctx, repository.top, markerPrefix); len(names) == 0 {
		t.Fatal("a still-shallow clone lost its markers")
	}
	// Complete now: everything goes again, once.
	if reconcileError := reconcileShallowMarkers(ctx, snapshot, markerPrefix); reconcileError != nil {
		t.Fatal(reconcileError)
	}
	if names, _, _ = execMarkers(ctx, repository.top, markerPrefix); len(names) != 0 {
		t.Fatalf("markers after the clone became complete = %v, want none", names)
	}
}

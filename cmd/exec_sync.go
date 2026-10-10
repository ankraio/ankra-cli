package cmd

// The git half of `ankra exec`: the snapshot of the worktree a run builds on,
// and the thin bundle of whatever the workspace does not hold yet. A Go port
// of claude-tools remote-exec/bin/ankra-exec (steps 1 and 2), byte for byte
// in what it writes to the repository, so a checkout moving from the bash
// client to this one keeps its pod worktree and sends no history twice.

import (
	"context"
	"crypto/sha1" //nolint:gosec // the agent id is the bash client's shasum, an identifier, not a security boundary
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// execSnapshotRefPrefix holds each agent's latest snapshot commit, so the
	// bundle can name it as a ref.
	execSnapshotRefPrefix = "refs/ankra-exec/snap/"
	// execSyncedRefPrefix holds, per workspace, the commits a workspace is
	// known to have: the bundle's --not set.
	execSyncedRefPrefix = "refs/ankra-exec/synced/"
	// execKeptMarkers is how many synced markers are kept per workspace;
	// older ones only make --not longer.
	execKeptMarkers = 40
	// execShallowTag marks a marker recorded while the clone was shallow.
	execShallowTag = "-shallow"
)

// The fixed identity and date of a snapshot commit: an unchanged tree is
// always the same commit.
var execSnapshotCommitEnv = []string{
	"GIT_AUTHOR_NAME=ankra-exec",
	"GIT_AUTHOR_EMAIL=ankra-exec@localhost",
	"GIT_AUTHOR_DATE=1970-01-01T00:00:00Z",
	"GIT_COMMITTER_NAME=ankra-exec",
	"GIT_COMMITTER_EMAIL=ankra-exec@localhost",
	"GIT_COMMITTER_DATE=1970-01-01T00:00:00Z",
}

// execSnapshot is one run's view of the worktree.
type execSnapshot struct {
	Top    string
	Prefix string
	// Agent names this worktree in the workspace: one checkout per agent.
	Agent    string
	Head     string
	Snapshot string
	// BaseRef and BaseSHA are the origin default branch and its commit, when
	// the clone has one.
	BaseRef string
	BaseSHA string
	// Shallow lists the clone's shallow boundary commits.
	Shallow []string
}

// shallowTag is the marker suffix of a shallow clone's sync.
func (snapshot execSnapshot) shallowTag() string {
	if len(snapshot.Shallow) > 0 {
		return execShallowTag
	}
	return ""
}

// snapshotRef is the agent's snapshot ref.
func (snapshot execSnapshot) snapshotRef() string {
	return execSnapshotRefPrefix + snapshot.Agent
}

// execAgentID is the bash client's agent id: the user name with everything
// but letters and digits turned into '-', then the first 12 hex digits of
// the SHA-1 of the worktree root.
func execAgentID(top string) string {
	name := "user"
	if current, userError := user.Current(); userError == nil && current.Username != "" {
		name = current.Username
	} else if fromEnvironment := os.Getenv("USER"); fromEnvironment != "" {
		name = fromEnvironment
	}
	var sanitized strings.Builder
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') {
			sanitized.WriteRune(character)
		} else {
			sanitized.WriteByte('-')
		}
	}
	userPart := sanitized.String()
	if userPart == "" || userPart[0] == '-' {
		// The workspace scripts take an id that starts with a letter or digit.
		userPart = "u" + userPart
	}
	digest := sha1.Sum([]byte(top)) //nolint:gosec // see the import
	return userPart + "-" + hex.EncodeToString(digest[:])[:12]
}

// takeExecSnapshot commits the worktree as it is - tracked files plus
// untracked files that are not ignored - from a temporary index, leaving the
// branch, the index and the working tree untouched.
func takeExecSnapshot(ctx context.Context, checkout workspaceCheckout, head string, temporaryDirectory string) (execSnapshot, error) {
	top := checkout.Top
	snapshot := execSnapshot{Top: top, Prefix: checkout.Prefix, Agent: execAgentID(top), Head: head}

	indexSource, indexError := gitStdout(ctx, top, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if indexError != nil {
		return execSnapshot{}, fmt.Errorf("locating the index: %w", indexError)
	}
	temporaryIndex := filepath.Join(temporaryDirectory, "index")
	if copyError := copyFileIfExists(indexSource, temporaryIndex); copyError != nil {
		return execSnapshot{}, fmt.Errorf("copying the index: %w", copyError)
	}
	indexEnv := []string{"GIT_INDEX_FILE=" + temporaryIndex}
	if _, addError := gitStdout(ctx, top, indexEnv, "add", "-A", "."); addError != nil {
		return execSnapshot{}, fmt.Errorf("could not snapshot the worktree: %w", addError)
	}
	tree, treeError := gitStdout(ctx, top, indexEnv, "write-tree")
	if treeError != nil {
		return execSnapshot{}, fmt.Errorf("could not write the snapshot tree: %w", treeError)
	}
	headTree, headTreeError := gitStdout(ctx, top, nil, "rev-parse", head+"^{tree}")
	if headTreeError != nil {
		return execSnapshot{}, fmt.Errorf("reading HEAD's tree: %w", headTreeError)
	}
	if tree == headTree {
		snapshot.Snapshot = head
	} else {
		commit, commitError := gitStdout(ctx, top, execSnapshotCommitEnv,
			"commit-tree", "--no-gpg-sign", tree, "-p", head, "-m", "ankra-exec snapshot")
		if commitError != nil {
			return execSnapshot{}, fmt.Errorf("could not write the snapshot commit: %w", commitError)
		}
		snapshot.Snapshot = commit
	}
	if _, refError := gitStdout(ctx, top, nil, "update-ref", snapshot.snapshotRef(), snapshot.Snapshot); refError != nil {
		return execSnapshot{}, fmt.Errorf("recording the snapshot: %w", refError)
	}

	snapshot.BaseRef, snapshot.BaseSHA = execBaseRef(ctx, top)

	shallowFile, shallowPathError := gitStdout(ctx, top, nil, "rev-parse", "--path-format=absolute", "--git-path", "shallow")
	if shallowPathError == nil {
		if contents, readError := os.ReadFile(shallowFile); readError == nil {
			for _, line := range strings.Split(string(contents), "\n") {
				if commit := strings.TrimSpace(line); commit != "" {
					snapshot.Shallow = append(snapshot.Shallow, commit)
				}
			}
		}
	}
	return snapshot, nil
}

// execBaseRef answers the clone's origin default branch ref and commit:
// origin/HEAD, else origin/main, else origin/master; both empty when the
// clone has none of them.
func execBaseRef(ctx context.Context, top string) (string, string) {
	baseRef, symbolicError := gitStdout(ctx, top, nil, "symbolic-ref", "-q", "refs/remotes/origin/HEAD")
	if symbolicError != nil || baseRef == "" {
		baseRef = ""
		for _, branch := range []string{"main", "master"} {
			candidate := "refs/remotes/origin/" + branch
			if _, verifyError := gitStdout(ctx, top, nil, "rev-parse", "-q", "--verify", candidate); verifyError == nil {
				baseRef = candidate
				break
			}
		}
	}
	if baseRef == "" {
		return "", ""
	}
	baseSHA, verifyError := gitStdout(ctx, top, nil, "rev-parse", "-q", "--verify", baseRef+"^{commit}")
	if verifyError != nil {
		return baseRef, ""
	}
	return baseRef, baseSHA
}

func copyFileIfExists(source string, destination string) error {
	input, openError := os.Open(source)
	if errors.Is(openError, os.ErrNotExist) {
		return nil
	}
	if openError != nil {
		return openError
	}
	defer func() { _ = input.Close() }()
	output, createError := os.Create(destination)
	if createError != nil {
		return createError
	}
	if _, copyError := io.Copy(output, input); copyError != nil {
		_ = output.Close()
		return copyError
	}
	return output.Close()
}

// execMarkerPrefix is where the commits one workspace holds are recorded.
func execMarkerPrefix(workspaceID string) string {
	return execSyncedRefPrefix + workspaceID + "/"
}

// execMarkers lists a workspace's markers: their ref names and commits.
func execMarkers(ctx context.Context, top string, markerPrefix string) ([]string, []string, error) {
	listing, listError := gitStdout(ctx, top, nil, "for-each-ref", "--format=%(refname) %(objectname)", markerPrefix)
	if listError != nil {
		return nil, nil, listError
	}
	var names, objects []string
	for _, line := range strings.Split(listing, "\n") {
		name, object, isFound := strings.Cut(strings.TrimSpace(line), " ")
		if !isFound {
			continue
		}
		names = append(names, name)
		objects = append(objects, object)
	}
	return names, objects, nil
}

// dropExecMarkers forgets everything a workspace was known to hold, so the
// next bundle carries the whole history.
func dropExecMarkers(ctx context.Context, top string, markerPrefix string) error {
	names, _, listError := execMarkers(ctx, top, markerPrefix)
	if listError != nil {
		return listError
	}
	for _, name := range names {
		if _, deleteError := gitStdout(ctx, top, nil, "update-ref", "-d", name); deleteError != nil {
			return deleteError
		}
	}
	return nil
}

// reconcileShallowMarkers drops the markers of a clone that was shallow when
// it last synced and is not any more: the whole history goes once, so the
// workspace drops the old boundary.
func reconcileShallowMarkers(ctx context.Context, snapshot execSnapshot, markerPrefix string) error {
	if len(snapshot.Shallow) > 0 {
		return nil
	}
	names, _, listError := execMarkers(ctx, snapshot.Top, markerPrefix)
	if listError != nil {
		return listError
	}
	for _, name := range names {
		// Only the marker's own name carries the tag; the workspace id in the
		// prefix is the platform's and could contain anything.
		if strings.HasSuffix(strings.TrimPrefix(name, markerPrefix), execShallowTag) {
			return dropExecMarkers(ctx, snapshot.Top, markerPrefix)
		}
	}
	return nil
}

// execBundle is the bundle file one attempt sends.
type execBundle struct {
	Path      string
	SizeBytes int64
	SHA256    string
}

// makeExecBundle writes the bundle of the snapshot and the base branch,
// minus every commit the workspace's markers say it holds (thin), or of the
// whole history (full). SizeBytes is 0 when nothing is missing.
func makeExecBundle(ctx context.Context, snapshot execSnapshot, markerPrefix string, isThin bool,
	path string) (execBundle, error) {
	bundle := execBundle{Path: path}
	if removeError := os.Remove(path); removeError != nil && !errors.Is(removeError, os.ErrNotExist) {
		return bundle, removeError
	}
	references := []string{snapshot.snapshotRef()}
	if snapshot.BaseSHA != "" {
		references = append(references, snapshot.BaseRef)
	}
	var excluded []string
	if isThin {
		_, objects, listError := execMarkers(ctx, snapshot.Top, markerPrefix)
		if listError != nil {
			return bundle, listError
		}
		excluded = objects
	}
	revisions := append([]string{}, references...)
	if len(excluded) > 0 {
		revisions = append(revisions, "--not")
		revisions = append(revisions, excluded...)
	}
	missing, countError := gitStdout(ctx, snapshot.Top, nil, append([]string{"rev-list", "--count"}, revisions...)...)
	if countError != nil {
		return bundle, countError
	}
	if missing == "0" {
		return bundle, nil
	}
	if _, bundleError := gitStdout(ctx, snapshot.Top, nil,
		append([]string{"bundle", "create", "-q", path}, revisions...)...); bundleError != nil {
		return bundle, bundleError
	}
	file, openError := os.Open(path)
	if openError != nil {
		return bundle, openError
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	size, hashError := io.Copy(hasher, file)
	if hashError != nil {
		return bundle, hashError
	}
	bundle.SizeBytes = size
	bundle.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	return bundle, nil
}

// recordExecSynced records that the workspace now holds the snapshot and the
// base commit, keeping the newest execKeptMarkers markers.
func recordExecSynced(ctx context.Context, snapshot execSnapshot, markerPrefix string, now time.Time) error {
	stamp := strconv.FormatInt(now.Unix(), 10)
	tag := snapshot.shallowTag()
	if _, refError := gitStdout(ctx, snapshot.Top, nil, "update-ref",
		markerPrefix+stamp+"-"+shortSHA(snapshot.Snapshot)+tag, snapshot.Snapshot); refError != nil {
		return refError
	}
	if snapshot.BaseSHA != "" {
		if _, refError := gitStdout(ctx, snapshot.Top, nil, "update-ref",
			markerPrefix+stamp+"-base-"+shortSHA(snapshot.BaseSHA)+tag, snapshot.BaseSHA); refError != nil {
			return refError
		}
	}
	listing, listError := gitStdout(ctx, snapshot.Top, nil, "for-each-ref", "--sort=-refname",
		"--format=%(refname)", markerPrefix)
	if listError != nil {
		return listError
	}
	names := strings.Fields(listing)
	for index := execKeptMarkers; index < len(names); index++ {
		if _, deleteError := gitStdout(ctx, snapshot.Top, nil, "update-ref", "-d", names[index]); deleteError != nil {
			return deleteError
		}
	}
	return nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

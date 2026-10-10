package cmd

// Which workspace kind a routed command runs in. The platform brings up one
// workspace per repository and kind, so a kind per tool would hand every
// repository a pod per tool family whether or not anyone set one up. The
// rule instead: a tool family (go, node, e2e) uses the repository's
// workspace profile named after it when the repository has one, and the
// default workspace otherwise. Which profiles a repository has is read from
// the platform and cached per repository, so a shim invocation pays no
// round trip for it; a read that fails never fails the command.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

const (
	// devKindCacheTTL is how long a repository's profile kinds are trusted:
	// a profile added or removed is picked up within it.
	devKindCacheTTL = 10 * time.Minute
	// devKindReadTimeout bounds the read, which sits in front of a command
	// somebody is waiting for.
	devKindReadTimeout = 10 * time.Second
)

// devFamilyProfileKinds are the profile names a family uses, first match
// first. The e2e family also takes a profile named playwright, the kind the
// bash client and `ankra exec --kind playwright` used.
var devFamilyProfileKinds = map[string][]string{
	devFamilyGo:   {"go"},
	devFamilyNode: {"node"},
	devFamilyE2E:  {"e2e", "playwright"},
}

// devKindCacheEntry is what is remembered about one repository's profiles.
type devKindCacheEntry struct {
	// RepositoryID is the connected pipeline repository, so a refresh skips
	// the listing.
	RepositoryID string `json:"repository_id,omitempty"`
	// Kinds are the kinds it has a profile for.
	Kinds []string `json:"kinds"`
	// IsRefused says the platform would not answer (the profiles are read
	// with workspaces.manage): every family then uses the default workspace.
	IsRefused bool `json:"is_refused,omitempty"`
	// FetchedAt is when the platform answered, in Unix seconds.
	FetchedAt int64 `json:"fetched_at"`
}

// devKindForFamily picks the kind out of the profiles a repository has.
func devKindForFamily(family string, profileKinds []string) string {
	for _, candidate := range devFamilyProfileKinds[family] {
		for _, kind := range profileKinds {
			if kind == candidate {
				return candidate
			}
		}
	}
	return defaultWorkspaceKind
}

// devWorkspaceKind answers the workspace kind a family's commands run in for
// the checkout's repository. It never fails: without an answer from the
// platform it uses the last one it had, and the default workspace when it
// never had one.
func devWorkspaceKind(ctx context.Context, cmd *cobra.Command, remote repositoryRemote, family string) string {
	if len(devFamilyProfileKinds[family]) == 0 {
		return defaultWorkspaceKind
	}
	cachePath := execRepositoryCachePath(cmd, remote, "dev-kinds")
	entry, isFresh := readDevKindCache(cachePath, execNow())
	if !isFresh {
		if fetched, isKnown := fetchDevKinds(ctx, cmd, remote, entry); isKnown {
			entry = fetched
			writeDevKindCache(cachePath, fetched)
		}
		// Otherwise the stale answer stands: a passing outage must not move
		// the command into another workspace.
	}
	if entry == nil {
		return defaultWorkspaceKind
	}
	return devKindForFamily(family, entry.Kinds)
}

// fetchDevKinds reads the repository's profile kinds from the platform.
// isKnown is false when the platform could not be asked, which is not
// cached.
func fetchDevKinds(parent context.Context, cmd *cobra.Command, remote repositoryRemote,
	stale *devKindCacheEntry) (*devKindCacheEntry, bool) {
	if clientError := ensureExecClient(cmd); clientError != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(parent, devKindReadTimeout)
	defer cancel()
	repositoryID := ""
	if stale != nil {
		repositoryID = stale.RepositoryID
	}
	for attempt := 0; attempt < 2; attempt++ {
		if repositoryID == "" {
			repository, isFound, findError := findPipelineRepository(ctx, remote)
			if findError != nil {
				return devKindRefusal("", findError)
			}
			if !isFound {
				return &devKindCacheEntry{Kinds: []string{}, FetchedAt: execNow().Unix()}, true
			}
			repositoryID = repository.ID
		}
		profiles, readError := apiClient.GetWorkspaceProfiles(ctx, repositoryID)
		if readError == nil {
			kinds := profiles.Kinds()
			if kinds == nil {
				kinds = []string{}
			}
			return &devKindCacheEntry{RepositoryID: repositoryID, Kinds: kinds, FetchedAt: execNow().Unix()}, true
		}
		if client.IsWorkspaceNotFound(readError) && attempt == 0 && stale != nil && stale.RepositoryID != "" {
			// The remembered repository was disconnected (and perhaps
			// connected again under a new id): look it up once.
			repositoryID = ""
			continue
		}
		return devKindRefusal(repositoryID, readError)
	}
	return nil, false
}

// devKindRefusal turns a failed read into what is remembered about it: the
// platform's own refusal is an answer (no profiles this caller may use), a
// failure to reach it is not.
func devKindRefusal(repositoryID string, err error) (*devKindCacheEntry, bool) {
	if isDefinitiveRefusal(err) || client.IsWorkspaceNotFound(err) {
		return &devKindCacheEntry{RepositoryID: repositoryID, Kinds: []string{}, IsRefused: true,
			FetchedAt: execNow().Unix()}, true
	}
	return nil, false
}

// readDevKindCache answers the remembered entry, nil when there is none, and
// whether it is still fresh.
func readDevKindCache(path string, now time.Time) (*devKindCacheEntry, bool) {
	if path == "" {
		return nil, false
	}
	contents, readError := os.ReadFile(path)
	if readError != nil {
		return nil, false
	}
	var entry devKindCacheEntry
	if unmarshalError := json.Unmarshal(contents, &entry); unmarshalError != nil || entry.FetchedAt == 0 {
		return nil, false
	}
	age := now.Sub(time.Unix(entry.FetchedAt, 0))
	return &entry, age >= 0 && age < devKindCacheTTL
}

func writeDevKindCache(path string, entry *devKindCacheEntry) {
	if path == "" || entry == nil {
		return
	}
	encoded, marshalError := json.Marshal(entry)
	if marshalError != nil {
		return
	}
	if mkdirError := os.MkdirAll(filepath.Dir(path), 0o700); mkdirError != nil {
		return
	}
	// Written whole and renamed in: concurrent shims never read half a file.
	temporary, createError := os.CreateTemp(filepath.Dir(path), ".kinds-")
	if createError != nil {
		return
	}
	_, writeError := temporary.Write(append(encoded, '\n'))
	closeError := temporary.Close()
	if writeError != nil || closeError != nil {
		_ = os.Remove(temporary.Name())
		return
	}
	if renameError := os.Rename(temporary.Name(), path); renameError != nil {
		_ = os.Remove(temporary.Name())
	}
}

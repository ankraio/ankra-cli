// Package cachenotice renders the one line a human-facing read prints when
// the platform answered it from the agent's cache instead of the live
// cluster.
//
// `ankra cluster get` reads are served from a cache the cluster's agent keeps
// in step with a Kubernetes watch. Most of the time that is a fresh answer
// that only skipped a round trip. When the cluster cannot answer - its API
// server down for a control-plane resize, the agent offline, the agent too
// slow - the platform keeps serving the last rows it has, and the read still
// succeeds. Before this package the only trace of that was served_from_cache
// in -o json: the table showed a control plane Ready on an instance type it
// had already left, with nothing to say the picture was minutes old
// (ankra-306z5f). The notice says, in one line above the rows, that the
// answer came from the cache, how old it is and, when the platform knows,
// why the cluster did not answer.
package cachenotice

import (
	"fmt"

	"ankra/internal/client"
)

// QuietAgeSeconds is how old a fresh cache answer may get before the notice
// says nothing newer has come from the cluster. The agent's watch reports
// every resource type at least about once a minute while the cluster's API
// server is up - each change, plus the API server's periodic bookmarks - so
// an inventory twice that old means the watch has gone quiet, which is what
// an API server outage looks like from the cache. It is a hint, not a
// verdict: the platform still calls the answer fresh until it is five
// minutes old.
const QuietAgeSeconds = 120

// Answer is how the platform answered one read, reduced to what the notice
// needs. AgeSeconds is negative when the age is unknown.
type Answer struct {
	ServedFromCache  bool
	Stale            bool
	AgeSeconds       int
	SyncStatus       string
	ReconcilePending bool
}

// FromResource reads a resources/get item's cache metadata. A nil metadata
// is a live answer. The age is cache_age_seconds when the platform sends it.
// Without it, staleness_seconds is the cache's age only on a fresh hit: on
// a stale one it is the agent's last check-in, which can be seconds old
// while the rows are minutes old, so the age is reported unknown rather
// than borrowed from it.
func FromResource(metadata *client.ResourceCacheMetadata) Answer {
	if metadata == nil {
		return Answer{}
	}
	age := -1
	switch {
	case metadata.CacheAgeSeconds != nil:
		age = *metadata.CacheAgeSeconds
	case !metadata.Stale:
		age = metadata.StalenessSeconds
	}
	return Answer{
		ServedFromCache:  metadata.ServedFromCache,
		Stale:            metadata.Stale,
		AgeSeconds:       age,
		SyncStatus:       metadata.SyncStatus,
		ReconcilePending: metadata.ReconcilePending,
	}
}

// FromPods reads the pods route's cache envelope, whose staleness_seconds
// is always the cache's own age.
func FromPods(info client.CacheInfo) Answer {
	return Answer{
		ServedFromCache:  info.ServedFromCache,
		AgeSeconds:       info.StalenessSeconds,
		SyncStatus:       info.SyncStatus,
		ReconcilePending: info.ReconcilePending,
	}
}

// Notice is the line to print above the rows, or "" for a live answer. A
// cached answer always gets one, healthy or not: the age is the one thing
// that tells a fresh inventory from a frozen one, and printing it only when
// something looks wrong would hide the slow slide from one to the other.
func Notice(answer Answer) string {
	if !answer.ServedFromCache {
		return ""
	}
	notice := "Served from the agent's cache"
	if answer.AgeSeconds >= 0 {
		notice += ", " + FormatAge(answer.AgeSeconds) + " old"
	} else {
		notice += " (age unknown)"
	}
	if reason := reasonFor(answer); reason != "" {
		return notice + ": " + reason + "."
	}
	return notice + "."
}

// reasonFor names why the cluster did not answer live, from what the
// platform said about the read. A stale answer always has one; a fresh one
// only when its watch reported trouble, has gone quiet, or has not yet been
// reconciled since the agent restarted.
func reasonFor(answer Answer) string {
	if answer.Stale {
		switch answer.SyncStatus {
		case "cluster_offline":
			return "the cluster is offline"
		case "no_agent":
			return "no agent is connected to the cluster"
		case "agent_timeout":
			return "the cluster's agent did not respond"
		case "agent_read_failed":
			return "the agent's live read failed (the Kubernetes API did not answer it)"
		default:
			return "the cluster did not answer live"
		}
	}
	switch {
	case answer.SyncStatus == "disconnected":
		return "the agent's watch is disconnected, so this may be out of date"
	case answer.SyncStatus == "error":
		return "the agent's last sync failed, so this may be out of date"
	case answer.AgeSeconds >= QuietAgeSeconds:
		return "nothing newer has come from the cluster since, so the Kubernetes API may not be answering"
	case answer.ReconcilePending:
		return "it is resyncing after an agent restart, so resources deleted in the meantime may still be listed"
	}
	return ""
}

// FormatAge renders an age in seconds as a compact duration: 45s, 1m30s,
// 2h5m, 3d4h. The second unit is dropped when it is zero.
func FormatAge(seconds int) string {
	type unit struct {
		size   int
		suffix string
	}
	units := []unit{{86400, "d"}, {3600, "h"}, {60, "m"}, {1, "s"}}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	for index, major := range units[:len(units)-1] {
		if seconds < major.size {
			continue
		}
		minor := units[index+1]
		majorCount := seconds / major.size
		minorCount := (seconds % major.size) / minor.size
		if minorCount == 0 {
			return fmt.Sprintf("%d%s", majorCount, major.suffix)
		}
		return fmt.Sprintf("%d%s%d%s", majorCount, major.suffix, minorCount, minor.suffix)
	}
	return fmt.Sprintf("%ds", seconds)
}

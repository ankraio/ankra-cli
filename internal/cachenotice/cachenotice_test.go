package cachenotice

import (
	"testing"

	"ankra/internal/client"
)

func intPointer(value int) *int { return &value }

// TestNoticeSaysHowOldEveryCachedAnswerIs pins ankra-306z5f: a cached answer
// prints its age even when nothing is wrong, and a live one prints nothing.
func TestNoticeSaysHowOldEveryCachedAnswerIs(t *testing.T) {
	cases := []struct {
		name   string
		answer Answer
		want   string
	}{
		{"live answer", Answer{}, ""},
		{"sandbox answer is not served from the cache", Answer{SyncStatus: "sandbox"}, ""},
		{"healthy fresh hit", Answer{ServedFromCache: true, AgeSeconds: 20, SyncStatus: "synced"},
			"Served from the agent's cache, 20s old."},
		{"unknown age", Answer{ServedFromCache: true, AgeSeconds: -1, SyncStatus: "synced"},
			"Served from the agent's cache (age unknown)."},
		{"quiet watch during an API outage",
			Answer{ServedFromCache: true, AgeSeconds: 150, SyncStatus: "synced"},
			"Served from the agent's cache, 2m30s old: nothing newer has come from the cluster since, " +
				"so the Kubernetes API may not be answering."},
		{"reconcile pending after an agent restart",
			Answer{ServedFromCache: true, AgeSeconds: 7, SyncStatus: "synced", ReconcilePending: true},
			"Served from the agent's cache, 7s old: it is resyncing after an agent restart, " +
				"so resources deleted in the meantime may still be listed."},
		{"disconnected watch",
			Answer{ServedFromCache: true, AgeSeconds: 30, SyncStatus: "disconnected"},
			"Served from the agent's cache, 30s old: the agent's watch is disconnected, so this may be out of date."},
		{"failed sync",
			Answer{ServedFromCache: true, AgeSeconds: 400, SyncStatus: "error"},
			"Served from the agent's cache, 6m40s old: the agent's last sync failed, so this may be out of date."},
		{"stale: agent timeout",
			Answer{ServedFromCache: true, Stale: true, AgeSeconds: 120, SyncStatus: "agent_timeout"},
			"Served from the agent's cache, 2m old: the cluster's agent did not respond."},
		{"stale: agent read failed",
			Answer{ServedFromCache: true, Stale: true, AgeSeconds: 95, SyncStatus: "agent_read_failed"},
			"Served from the agent's cache, 1m35s old: the agent's live read failed (the Kubernetes API did not answer it)."},
		{"stale: offline cluster",
			Answer{ServedFromCache: true, Stale: true, AgeSeconds: 3 * 86400, SyncStatus: "cluster_offline"},
			"Served from the agent's cache, 3d old: the cluster is offline."},
		{"stale: no agent",
			Answer{ServedFromCache: true, Stale: true, AgeSeconds: -1, SyncStatus: "no_agent"},
			"Served from the agent's cache (age unknown): no agent is connected to the cluster."},
		{"stale: a reason this CLI does not know",
			Answer{ServedFromCache: true, Stale: true, AgeSeconds: 61, SyncStatus: "something_new"},
			"Served from the agent's cache, 1m1s old: the cluster did not answer live."},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Notice(testCase.answer); got != testCase.want {
				t.Errorf("Notice(%+v)\n got  %q\n want %q", testCase.answer, got, testCase.want)
			}
		})
	}
}

// TestFromResourceNeverPassesTheAgentCheckInOffAsTheAge pins the field
// choice: on a stale item staleness_seconds is the agent's last check-in,
// not the rows' age, so it is only used on a fresh hit.
func TestFromResourceNeverPassesTheAgentCheckInOffAsTheAge(t *testing.T) {
	if answer := FromResource(nil); answer.ServedFromCache || Notice(answer) != "" {
		t.Fatalf("a live item must produce no notice, got %+v", answer)
	}
	fresh := FromResource(&client.ResourceCacheMetadata{ServedFromCache: true, StalenessSeconds: 48, SyncStatus: "synced"})
	if fresh.AgeSeconds != 48 {
		t.Errorf("a fresh hit from a platform without cache_age_seconds uses staleness_seconds, got %d", fresh.AgeSeconds)
	}
	staleOldPlatform := FromResource(&client.ResourceCacheMetadata{
		ServedFromCache: true, Stale: true, StalenessSeconds: 4, SyncStatus: "agent_timeout"})
	if staleOldPlatform.AgeSeconds >= 0 {
		t.Errorf("a stale hit's staleness_seconds is the agent's check-in, not the rows' age; got age %d",
			staleOldPlatform.AgeSeconds)
	}
	// A platform that predates the stale flag still names its stale fallback
	// in sync_status; that is enough to keep the check-in out of the age.
	preStaleFlag := FromResource(&client.ResourceCacheMetadata{
		ServedFromCache: true, StalenessSeconds: 3, SyncStatus: "agent_timeout"})
	if !preStaleFlag.Stale || preStaleFlag.AgeSeconds >= 0 {
		t.Errorf("a stale-fallback sync_status without the stale flag must read stale with an unknown age, got %+v",
			preStaleFlag)
	}
	if got := Notice(preStaleFlag); got != "Served from the agent's cache (age unknown): the cluster's agent did not respond." {
		t.Errorf("notice for a pre-flag stale item = %q", got)
	}
	stale := FromResource(&client.ResourceCacheMetadata{
		ServedFromCache: true, Stale: true, StalenessSeconds: 4, CacheAgeSeconds: intPointer(95),
		SyncStatus: "agent_timeout", ReconcilePending: true})
	if stale.AgeSeconds != 95 || !stale.Stale || !stale.ReconcilePending || stale.SyncStatus != "agent_timeout" {
		t.Errorf("cache_age_seconds is the rows' age and wins, got %+v", stale)
	}
	pods := FromPods(client.CacheInfo{ServedFromCache: true, StalenessSeconds: 33, SyncStatus: "synced", ReconcilePending: true})
	if pods.AgeSeconds != 33 || !pods.ReconcilePending || pods.Stale {
		t.Errorf("the pods envelope's staleness_seconds is the cache's age, got %+v", pods)
	}
}

func TestFormatAge(t *testing.T) {
	for seconds, want := range map[int]string{
		0: "0s", 59: "59s", 60: "1m", 90: "1m30s", 3599: "59m59s", 3600: "1h", 3900: "1h5m",
		86400: "1d", 86400 + 4*3600 + 59: "1d4h",
	} {
		if got := FormatAge(seconds); got != want {
			t.Errorf("FormatAge(%d) = %q, want %q", seconds, got, want)
		}
	}
}

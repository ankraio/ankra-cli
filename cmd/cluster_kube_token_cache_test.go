package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ankra/internal/client"
)

// countingKubeTokenMock mints a numbered token per call, expiring after its
// lifetime.
type countingKubeTokenMock struct {
	baseMock
	calls    int
	lifetime time.Duration
}

func (m *countingKubeTokenMock) GetClusterKubeToken(_ context.Context, _ string) (*client.KubeToken, error) {
	m.calls++
	return &client.KubeToken{
		Token:     "token-" + string(rune('0'+m.calls)),
		ExpiresAt: kubeTokenNow().Add(m.lifetime).UTC().Format(time.RFC3339),
	}, nil
}

// useKubeTokenCache points the cache at a temporary directory, installs the
// mock and a movable clock, and restores everything after the test.
func useKubeTokenCache(t *testing.T, lifetime time.Duration) (*countingKubeTokenMock, *time.Time, string) {
	t.Helper()
	// A directory the cache has to create, as on first use.
	directory := filepath.Join(t.TempDir(), "kube-tokens")
	clock := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	mock := &countingKubeTokenMock{lifetime: lifetime}
	previousClient, previousDirectory, previousNow := apiClient, kubeTokenCacheDirectory, kubeTokenNow
	previousToken, previousBase := apiToken, baseURL
	apiClient = mock
	kubeTokenCacheDirectory = func() (string, error) { return directory, nil }
	kubeTokenNow = func() time.Time { return clock }
	apiToken, baseURL = "login-a", "https://platform.example"
	t.Setenv(envKubeTokenCache, "")
	t.Cleanup(func() {
		apiClient, kubeTokenCacheDirectory, kubeTokenNow = previousClient, previousDirectory, previousNow
		apiToken, baseURL = previousToken, previousBase
	})
	return mock, &clock, directory
}

func mustKubeToken(t *testing.T, clusterID string) string {
	t.Helper()
	token, err := kubeTokenFor(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	return token.Token
}

func TestKubeTokenIsReusedWhileFresh(t *testing.T) {
	mock, clock, _ := useKubeTokenCache(t, 15*time.Minute)
	first := mustKubeToken(t, "cluster-1")
	*clock = clock.Add(4 * time.Minute)
	if second := mustKubeToken(t, "cluster-1"); second != first || mock.calls != 1 {
		t.Fatalf("a fresh token was re-minted: %s then %s after %d mints", first, second, mock.calls)
	}
}

func TestKubeTokenIsReMintedPastTheMaximumAge(t *testing.T) {
	mock, clock, _ := useKubeTokenCache(t, time.Hour)
	mustKubeToken(t, "cluster-1")
	*clock = clock.Add(kubeTokenCacheMaxAge + time.Second)
	mustKubeToken(t, "cluster-1")
	if mock.calls != 2 {
		t.Fatalf("a token older than the maximum age was reused (%d mints)", mock.calls)
	}
}

func TestKubeTokenIsReMintedNearItsExpiry(t *testing.T) {
	mock, clock, _ := useKubeTokenCache(t, 3*time.Minute)
	mustKubeToken(t, "cluster-1")
	*clock = clock.Add(90 * time.Second)
	mustKubeToken(t, "cluster-1")
	if mock.calls != 2 {
		t.Fatalf("a token about to expire was reused (%d mints)", mock.calls)
	}
}

func TestKubeTokenIsNotCachedWhenItIsAlreadyShortLived(t *testing.T) {
	mock, _, directory := useKubeTokenCache(t, time.Minute)
	mustKubeToken(t, "cluster-1")
	mustKubeToken(t, "cluster-1")
	entries, _ := os.ReadDir(directory)
	if mock.calls != 2 || len(entries) != 0 {
		t.Fatalf("a token inside the expiry margin was cached (%d mints, %d files)", mock.calls, len(entries))
	}
}

func TestKubeTokenCacheIsPerLoginAndPerCluster(t *testing.T) {
	mock, _, _ := useKubeTokenCache(t, 15*time.Minute)
	mustKubeToken(t, "cluster-1")
	mustKubeToken(t, "cluster-2")
	apiToken = "login-b"
	mustKubeToken(t, "cluster-1")
	if mock.calls != 3 {
		t.Fatalf("a token was shared across clusters or logins (%d mints, want 3)", mock.calls)
	}
}

func TestKubeTokenCacheFileIsPrivate(t *testing.T) {
	_, _, directory := useKubeTokenCache(t, 15*time.Minute)
	mustKubeToken(t, "cluster-1")
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one cache file, got %v (%v)", entries, err)
	}
	info, err := os.Stat(filepath.Join(directory, entries[0].Name()))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache file mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
	dirInfo, err := os.Stat(directory)
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("the cache directory it created has mode %v (%v), want 0700", dirInfo.Mode().Perm(), err)
	}
}

func TestKubeTokenCacheIgnoresAFileOthersCanRead(t *testing.T) {
	mock, _, directory := useKubeTokenCache(t, 15*time.Minute)
	mustKubeToken(t, "cluster-1")
	entries, _ := os.ReadDir(directory)
	if err := os.Chmod(filepath.Join(directory, entries[0].Name()), 0o644); err != nil {
		t.Fatal(err)
	}
	mustKubeToken(t, "cluster-1")
	if mock.calls != 2 {
		t.Fatalf("a world-readable cache file was trusted (%d mints)", mock.calls)
	}
}

func TestKubeTokenCacheCanBeTurnedOff(t *testing.T) {
	mock, _, directory := useKubeTokenCache(t, 15*time.Minute)
	t.Setenv(envKubeTokenCache, "off")
	mustKubeToken(t, "cluster-1")
	mustKubeToken(t, "cluster-1")
	entries, _ := os.ReadDir(directory)
	if mock.calls != 2 || len(entries) != 0 {
		t.Fatalf("ANKRA_KUBE_TOKEN_CACHE=off still cached (%d mints, %d files)", mock.calls, len(entries))
	}
}

func TestKubeTokenCacheThatCannotBeWrittenStillMints(t *testing.T) {
	mock, _, _ := useKubeTokenCache(t, 15*time.Minute)
	kubeTokenCacheDirectory = func() (string, error) { return "/dev/null/not-a-directory", nil }
	if token := mustKubeToken(t, "cluster-1"); token == "" || mock.calls != 1 {
		t.Fatalf("an unwritable cache broke the mint: %q after %d mints", token, mock.calls)
	}
}

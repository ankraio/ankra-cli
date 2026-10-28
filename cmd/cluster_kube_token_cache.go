package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ankra/internal/client"
)

// kubectl runs the credential plugin ('ankra cluster kube-token') on every
// invocation: client-go keeps an ExecCredential only for the life of one
// process. Without a cache, each kubectl command mints a fresh gateway token,
// and a script or a few agents running kubectl in a loop hit the platform's
// rate limit on the mint ("Too many kube token requests") and start failing.
//
// The cache keeps the last token per (API, login, cluster) on disk, readable
// only by the user, and reuses it while it is fresh: at least
// kubeTokenCacheExpiryMargin before it expires, and no longer than
// kubeTokenCacheMaxAge after it was minted, so an access grant revoked on the
// platform stops working here within minutes even if the token itself lives
// longer. It is best effort: a cache that cannot be read or written is a
// fresh mint, never a failure. ANKRA_KUBE_TOKEN_CACHE=off turns it off.
const (
	kubeTokenCacheExpiryMargin = 2 * time.Minute
	kubeTokenCacheMaxAge       = 5 * time.Minute
	envKubeTokenCache          = "ANKRA_KUBE_TOKEN_CACHE"
)

// cachedKubeToken is one cache file.
type cachedKubeToken struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	MintedAt  string `json:"minted_at"`
}

// kubeTokenCacheDirectory and kubeTokenNow are variables so tests can point
// the cache at a temporary directory and move the clock.
var (
	kubeTokenCacheDirectory = func() (string, error) {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "ankra", "kube-tokens"), nil
	}
	kubeTokenNow = time.Now
)

// kubeTokenFor answers a gateway token for the cluster: a fresh cached one,
// or a newly minted one, which it caches.
func kubeTokenFor(ctx context.Context, clusterID string) (*client.KubeToken, error) {
	isEnabled := !strings.EqualFold(strings.TrimSpace(os.Getenv(envKubeTokenCache)), "off")
	path := ""
	if isEnabled {
		path = kubeTokenCachePath(clusterID)
		if cached, isFresh := readCachedKubeToken(path); isFresh {
			return &client.KubeToken{Token: cached.Token, ExpiresAt: cached.ExpiresAt}, nil
		}
	}
	minted, err := apiClient.GetClusterKubeToken(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if path != "" {
		writeCachedKubeToken(path, minted)
	}
	return minted, nil
}

// kubeTokenCachePath is the cache file for this API, login and cluster. The
// login's token is hashed into the name, so logging in as someone else (or
// again) never reuses a token minted for the previous login.
func kubeTokenCachePath(clusterID string) string {
	directory, err := kubeTokenCacheDirectory()
	if err != nil || directory == "" {
		return ""
	}
	digest := sha256.New()
	for _, part := range []string{baseURL, apiToken, clusterID} {
		digest.Write([]byte(part))
		digest.Write([]byte{0})
	}
	return filepath.Join(directory, hex.EncodeToString(digest.Sum(nil))+".json")
}

// readCachedKubeToken answers the cached token when it is fresh enough to
// reuse.
func readCachedKubeToken(path string) (cachedKubeToken, bool) {
	if path == "" {
		return cachedKubeToken{}, false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return cachedKubeToken{}, false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return cachedKubeToken{}, false
	}
	var cached cachedKubeToken
	if json.Unmarshal(body, &cached) != nil || cached.Token == "" {
		return cachedKubeToken{}, false
	}
	expiresAt, expiryError := time.Parse(time.RFC3339, cached.ExpiresAt)
	mintedAt, mintError := time.Parse(time.RFC3339, cached.MintedAt)
	if expiryError != nil || mintError != nil {
		return cachedKubeToken{}, false
	}
	now := kubeTokenNow()
	if expiresAt.Sub(now) < kubeTokenCacheExpiryMargin || now.Sub(mintedAt) > kubeTokenCacheMaxAge ||
		mintedAt.After(now.Add(time.Minute)) {
		return cachedKubeToken{}, false
	}
	return cached, true
}

// writeCachedKubeToken stores a minted token, atomically and readable only by
// the user. A token without a readable expiry is not cached.
func writeCachedKubeToken(path string, minted *client.KubeToken) {
	if path == "" || minted == nil || minted.Token == "" {
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, minted.ExpiresAt)
	if err != nil || expiresAt.Sub(kubeTokenNow()) < kubeTokenCacheExpiryMargin {
		return
	}
	body, err := json.Marshal(cachedKubeToken{
		Token:     minted.Token,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		MintedAt:  kubeTokenNow().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	directory := filepath.Dir(path)
	if os.MkdirAll(directory, 0o700) != nil {
		return
	}
	temporary, err := os.CreateTemp(directory, ".kube-token-*")
	if err != nil {
		return
	}
	// Once renamed, the temporary name is gone and the Remove is a no-op.
	defer func() { _ = os.Remove(temporary.Name()) }()
	if temporary.Chmod(0o600) != nil {
		_ = temporary.Close()
		return
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return
	}
	if temporary.Close() != nil {
		return
	}
	_ = os.Rename(temporary.Name(), path)
}

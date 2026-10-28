package client

import (
	"context"
	"net/http"
	"testing"
)

// deferralDetail mimics the platform's designed-refusal message. It must
// contain "only the commit back to Git was deferred" so the tests also pin
// that the CLI passes the marker substring through verbatim (the cluster
// deploy script matches it; see GitPushDeferral).
const deferralDetail = "Saved. Your change is applied to the cluster and is live; only the commit back to Git was deferred, because Git has newer changes that Ankra has not synced yet. The background sync merges both sides and commits your change on its own - do not re-apply it."

const deferredBody = `{"detail":"` + deferralDetail + `","error_code":"GIT_PUSH_DEFERRED"}`
const failedBody = `{"detail":"GitHub authentication failed","error_code":"GIT_PUSH_FAILED"}`

func TestGitPushDeferralFromResponse(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{"deferred 422", http.StatusUnprocessableEntity, deferredBody, true},
		{"failed 422 stays an error", http.StatusUnprocessableEntity, failedBody, false},
		{"codeless 422 (older platform) stays an error", http.StatusUnprocessableEntity, `{"detail":"` + deferralDetail + `"}`, false},
		{"deferred body on 200 is not a deferral", http.StatusOK, deferredBody, false},
		{"deferred body on 500 is not a deferral", http.StatusInternalServerError, deferredBody, false},
		{"non-JSON body", http.StatusUnprocessableEntity, "<html>bad gateway</html>", false},
		{"empty body", http.StatusUnprocessableEntity, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gitPushDeferralFromResponse(tc.statusCode, []byte(tc.body))
			if (got != nil) != tc.want {
				t.Fatalf("gitPushDeferralFromResponse(%d, %q) = %v, want deferral=%v", tc.statusCode, tc.body, got, tc.want)
			}
			if got != nil && got.Message != deferralDetail {
				t.Errorf("Message = %q, want the platform detail verbatim", got.Message)
			}
		})
	}
}

// TestGitPushDeferralAcrossWriteLanes pins the class boundary on every
// hand-rolled write lane that can receive a git-push 422: a deferral becomes
// success carrying the platform message verbatim; GIT_PUSH_FAILED keeps
// today's error. (PatchClusterStackPartial and ApplyCluster have their own
// tests next to their richer harnesses.)
func TestGitPushDeferralAcrossWriteLanes(t *testing.T) {
	lanes := []struct {
		name string
		// call runs the lane against the test client and reports the
		// deferral flag and message it surfaced (only read when err == nil).
		call func(c *Client) (deferred bool, message string, err error)
	}{
		{"UninstallAddon", func(c *Client) (bool, string, error) {
			res, err := c.UninstallAddon(context.Background(), "cluster-id", "resource-id", false)
			if err != nil {
				return false, "", err
			}
			return res.GitPushDeferred, res.Message, nil
		}},
		{"UpdateAddonSettings", func(c *Client) (bool, string, error) {
			res, err := c.UpdateAddonSettings(context.Background(), "cluster-id", "ingress", AddonSettings{})
			if err != nil {
				return false, "", err
			}
			return res.GitPushDeferred, res.GitPushMessage, nil
		}},
		{"RenameStack", func(c *Client) (bool, string, error) {
			res, err := c.RenameStack(context.Background(), "cluster-id", "old", "new")
			if err != nil {
				return false, "", err
			}
			return res.GitPushDeferred, res.Message, nil
		}},
		{"DisconnectManifest", func(c *Client) (bool, string, error) {
			res, err := c.DisconnectManifest(context.Background(), "cluster-id", "stack", "manifest")
			if err != nil {
				return false, "", err
			}
			return res.GitPushDeferred, res.GitPushMessage, nil
		}},
	}
	for _, lane := range lanes {
		t.Run(lane.name+" deferred is success", func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(deferredBody))
			})
			deferred, message, err := lane.call(c)
			if err != nil {
				t.Fatalf("deferred git push must be success, got error: %v", err)
			}
			if !deferred {
				t.Error("deferral flag = false, want true")
			}
			if message != deferralDetail {
				t.Errorf("message = %q, want the platform detail verbatim", message)
			}
		})
		t.Run(lane.name+" failed stays an error", func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(failedBody))
			})
			if _, _, err := lane.call(c); err == nil {
				t.Fatal("expected error for GIT_PUSH_FAILED, got success")
			}
		})
	}
}

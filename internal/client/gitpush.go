package client

import (
	"encoding/json"
	"net/http"
)

// The platform answers every GitOps-push rejection with HTTP 422 and a
// machine-readable error_code alongside the human-readable detail
// (ankra-71vbx): GIT_PUSH_DEFERRED is the designed refusal — the DB write is
// committed, the cluster change is live, and only the write-back to Git
// waits on the background sync — while GIT_PUSH_FAILED is a genuine push
// failure where nothing reached Git. Both codes are always emitted together;
// an absent error_code means the platform predates the contract, which is a
// third answer, not the second: it keeps today's treat-as-error behavior.
const (
	gitPushErrorCodeDeferred = "GIT_PUSH_DEFERRED"
)

// GitPushDeferral reports a designed git-push refusal: the requested change
// is saved and live on the cluster, and the platform will commit it back to
// Git on its own. Message carries the platform's detail verbatim — callers
// must print it unchanged, because the deploy tooling still substring-matches
// it (cluster scripts/deploy_upgrade_addons.sh, pinned by
// enginekit/clusterengine/gitsync_deploy_marker_test.go).
type GitPushDeferral struct {
	Message string
}

// gitPushDeferralFromResponse classifies a response as a git-push deferral:
// non-nil only for a 422 whose body carries error_code GIT_PUSH_DEFERRED.
// Every other response — GIT_PUSH_FAILED, a codeless 422 from an older
// platform, any other status — returns nil so existing error paths apply.
func gitPushDeferralFromResponse(statusCode int, body []byte) *GitPushDeferral {
	if statusCode != http.StatusUnprocessableEntity {
		return nil
	}
	var parsed struct {
		Detail    string `json:"detail"`
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	if parsed.ErrorCode != gitPushErrorCodeDeferred {
		return nil
	}
	return &GitPushDeferral{Message: parsed.Detail}
}

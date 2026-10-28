package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

// cmdDeferralDetail mimics the platform's designed-refusal message. It
// contains "only the commit back to Git was deferred" so these tests also
// pin that the CLI surfaces the marker substring verbatim (the cluster
// deploy script matches it).
const cmdDeferralDetail = "Saved. Your change is applied to the cluster and is live; only the commit back to Git was deferred, because Git has newer changes that Ankra has not synced yet. The background sync merges both sides and commits your change on its own - do not re-apply it."

// A deferred git push on addons upgrade is a successful deploy: exit 0 with
// the platform message on stdout, not the retryable failure CI wrappers
// re-applied five times against (ankra-qezdv).
func TestRunAddonsUpgrade_DeferredGitPushIsSuccess(t *testing.T) {
	mock := &upgradeMock{
		iac: sampleIaCYAMLForCmd,
		patchResult: &client.PatchStackResult{
			StackName:       "demo-web-app",
			GitPushDeferred: true,
			GitPushMessage:  cmdDeferralDetail,
		},
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	output, err := executeCommand("cluster", "addons", "upgrade", "website",
		"--chart-version", "1.0.146",
		"--cluster", fakeClusterUUID,
	)
	if err != nil {
		t.Fatalf("deferred git push must exit 0, got error: %v\noutput: %s", err, output)
	}
	if !strings.Contains(output, cmdDeferralDetail) {
		t.Errorf("expected the platform deferral message verbatim in output, got: %s", output)
	}
	if !strings.Contains(output, `Stack "demo-web-app" updated.`) {
		t.Errorf("expected the update summary in output, got: %s", output)
	}
}

// -o json carries the deferral as machine-readable fields on parseable stdout.
func TestRunAddonsUpgrade_DeferredGitPushJSON(t *testing.T) {
	mock := &upgradeMock{
		iac: sampleIaCYAMLForCmd,
		patchResult: &client.PatchStackResult{
			StackName:       "demo-web-app",
			GitPushDeferred: true,
			GitPushMessage:  cmdDeferralDetail,
		},
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"cluster", "addons", "upgrade", "website",
		"--chart-version", "1.0.146",
		"--cluster", fakeClusterUUID,
		"--output", "json",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("deferred git push must exit 0, got error: %v\nstderr: %s", err, stderr.String())
	}
	var decoded struct {
		StackName       string `json:"stack_name"`
		GitPushDeferred bool   `json:"git_push_deferred"`
		GitPushMessage  string `json:"git_push_message"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not parseable JSON: %v\nstdout: %s", err, stdout.String())
	}
	if !decoded.GitPushDeferred {
		t.Error("git_push_deferred = false, want true")
	}
	if decoded.GitPushMessage != cmdDeferralDetail {
		t.Errorf("git_push_message = %q, want the platform detail verbatim", decoded.GitPushMessage)
	}
}

// GIT_PUSH_FAILED keeps today's non-zero "git push failed" exit.
func TestRunAddonsUpgrade_FailedGitPushStaysError(t *testing.T) {
	mock := &upgradeMock{
		iac: sampleIaCYAMLForCmd,
		patchErr: &client.PatchStackError{
			StatusCode: 422,
			Body:       []byte(`{"detail":"GitHub authentication failed","error_code":"GIT_PUSH_FAILED"}`),
		},
	}
	setMockClient(t, mock)
	resetUpgradeCommandFlags(t)

	output, err := executeCommand("cluster", "addons", "upgrade", "website",
		"--chart-version", "1.0.146",
		"--cluster", fakeClusterUUID,
	)
	if err == nil {
		t.Fatalf("expected error for GIT_PUSH_FAILED, got success; output: %s", output)
	}
	if !strings.Contains(err.Error(), "git push failed: GitHub authentication failed") {
		t.Errorf("error = %v, want the git-push failure surfaced", err)
	}
}

type applyGitPushMock struct {
	baseMock
	response *client.ImportResponse
	err      error
}

func (m *applyGitPushMock) ApplyCluster(ctx context.Context, clusterReq client.CreateImportClusterRequest, wait bool) (*client.ImportResponse, bool, error) {
	return m.response, false, m.err
}

// cluster apply with a deferred git push is a successful apply: exit 0 with
// the platform message, no empty-name success rendering.
func TestClusterApply_DeferredGitPushIsSuccess(t *testing.T) {
	mock := &applyGitPushMock{
		response: &client.ImportResponse{GitPushDeferred: true, GitPushMessage: cmdDeferralDetail},
	}
	setMockClient(t, mock)
	yamlPath := writeDraftImportClusterYAML(t)

	var output string
	var err error
	stdout := captureStdout(t, func() {
		output, err = executeCommand("cluster", "apply", "-f", yamlPath)
	})
	if err != nil {
		t.Fatalf("deferred git push must exit 0, got error: %v\noutput: %s%s", err, output, stdout)
	}
	combined := output + stdout
	if !strings.Contains(combined, cmdDeferralDetail) {
		t.Errorf("expected the platform deferral message verbatim, got: %s", combined)
	}
	if strings.Contains(combined, "Cluster ''") || strings.Contains(combined, `Cluster ""`) {
		t.Errorf("deferral must not fall through to name-based rendering, got: %s", combined)
	}
}

// Any apply error (including a genuine git-push failure) stays non-zero.
func TestClusterApply_FailedGitPushStaysError(t *testing.T) {
	mock := &applyGitPushMock{
		err: client.NewUnexpectedResponseError(422, "request failed: status 422: GitHub authentication failed"),
	}
	setMockClient(t, mock)
	yamlPath := writeDraftImportClusterYAML(t)

	var err error
	_ = captureStdout(t, func() {
		_, err = executeCommand("cluster", "apply", "-f", yamlPath)
	})
	if err == nil {
		t.Fatal("expected error for a failed git push, got success")
	}
}

type draftDeferredMock struct {
	draftBootstrapMock
}

func (m *draftDeferredMock) ApplyCluster(ctx context.Context, clusterReq client.CreateImportClusterRequest, wait bool) (*client.ImportResponse, bool, error) {
	m.applyCalls++
	return &client.ImportResponse{GitPushDeferred: true, GitPushMessage: cmdDeferralDetail}, false, nil
}

// The draft bootstrap needs the new cluster's ID from the apply response; a
// deferred response carries none, so deferral-as-success is forbidden here —
// it must fail with an actionable error instead of staging drafts against an
// empty cluster ID (plan-gate Finding 1).
func TestDraftBootstrap_DeferredGitPushFailsActionably(t *testing.T) {
	mock := &draftDeferredMock{}
	setMockClient(t, mock)
	yamlPath := writeDraftImportClusterYAML(t)

	var output string
	var err error
	_ = captureStdout(t, func() {
		output, err = executeCommand("cluster", "draft", "-f", yamlPath)
	})
	if err == nil {
		t.Fatalf("expected an error when the bootstrap import defers its git push; output: %s", output)
	}
	if !strings.Contains(err.Error(), "re-run") {
		t.Errorf("error = %v, want an actionable re-run instruction", err)
	}
	if len(mock.draftClusterIDs) != 0 {
		t.Errorf("no drafts may be staged without a cluster ID, got staging calls for %v", mock.draftClusterIDs)
	}
}

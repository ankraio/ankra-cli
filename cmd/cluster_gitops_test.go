package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

type gitopsStatusMock struct {
	baseMock
	status             *client.ClusterGitopsStatus
	statusError        error
	requestedClusterID string
}

func (m *gitopsStatusMock) GetCluster(name string) (client.ClusterListItem, error) {
	return client.ClusterListItem{ID: "gitops-cluster-id", Name: name}, nil
}

func (m *gitopsStatusMock) GetClusterGitopsStatus(clusterID string) (*client.ClusterGitopsStatus, error) {
	m.requestedClusterID = clusterID
	if m.statusError != nil {
		return nil, m.statusError
	}
	return m.status, nil
}

func syncedGitopsStatus() *client.ClusterGitopsStatus {
	return &client.ClusterGitopsStatus{
		SyncStatus:          strPtrCmd("synced"),
		LastSyncedAt:        strPtrCmd("2026-07-02T09:05:00Z"),
		LastSyncedFrom:      strPtrCmd("webhook"),
		LastCommitSHA:       strPtrCmd("abc123"),
		LastCommitTimestamp: strPtrCmd("2026-07-02T09:05:00Z"),
		ClusterName:         strPtrCmd("my-cluster"),
		ClusterShortID:      strPtrCmd("shortid1"),
		GitRepo: &client.ClusterGitopsRepo{
			Provider:       "github",
			Branch:         "develop",
			WebURL:         "https://github.com/ankra/gitops",
			RepoOwner:      strPtrCmd("ankra"),
			RepoName:       strPtrCmd("gitops"),
			CredentialName: strPtrCmd("github-creds"),
		},
	}
}

func TestClusterGitopsStatusCommand(t *testing.T) {
	mock := &gitopsStatusMock{status: syncedGitopsStatus()}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})

	if mock.requestedClusterID != "gitops-cluster-id" {
		t.Errorf("expected status request for the resolved cluster id, got %q", mock.requestedClusterID)
	}
	for _, fragment := range []string{
		"GitOps Status for my-cluster",
		"Repository: ankra/gitops (https://github.com/ankra/gitops)",
		"Branch: develop",
		"Credential: github-creds",
		"Provider: github",
		"Sync Status: synced",
		"Last Synced Commit: abc123",
		"via webhook",
	} {
		if !strings.Contains(stdoutOutput, fragment) {
			t.Errorf("expected output to contain %q, got: %s", fragment, stdoutOutput)
		}
	}
	for _, absent := range []string{"Pending Commit", "Error:", "Warning:"} {
		if strings.Contains(stdoutOutput, absent) {
			t.Errorf("expected output without %q, got: %s", absent, stdoutOutput)
		}
	}
}

func TestClusterGitopsStatusShowsPendingCommitAndError(t *testing.T) {
	status := syncedGitopsStatus()
	status.SyncStatus = strPtrCmd("error")
	status.SyncPhase = strPtrCmd("pushing")
	status.PendingCommitSHA = strPtrCmd("def456")
	status.Error = map[string]interface{}{
		"error_type": "general",
		"message":    "push rejected by remote",
	}
	mock := &gitopsStatusMock{status: status}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})

	for _, fragment := range []string{
		"Sync Status: error",
		"Sync Phase: pushing",
		"Pending Commit: def456",
		"Error: push rejected by remote",
	} {
		if !strings.Contains(stdoutOutput, fragment) {
			t.Errorf("expected output to contain %q, got: %s", fragment, stdoutOutput)
		}
	}
}

func intPtrCmd(value int) *int {
	return &value
}

// A sync that found the same resource changed in Git and on the cluster
// applies nothing until the conflicts are resolved; PLA-887's customer read
// "synced" at their unapplied commit and escalated (ankra-b3utc).
func TestClusterGitopsStatusShowsOpenConflictPause(t *testing.T) {
	status := syncedGitopsStatus()
	status.SyncStatus = strPtrCmd("conflict")
	status.OpenConflictCount = intPtrCmd(3)
	status.OpenConflictKeys = []string{"stack:web/addon:redis", "stack:web/manifest:ingress"}
	mock := &gitopsStatusMock{status: status}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})

	for _, fragment := range []string{
		"Sync Status: conflict",
		"Open Conflicts: 3",
		"    - stack:web/addon:redis",
		"    - stack:web/manifest:ingress",
		"    ... and 1 more",
		"Warning: GitOps sync is paused on these merge conflicts",
	} {
		if !strings.Contains(stdoutOutput, fragment) {
			t.Errorf("expected output to contain %q, got: %s", fragment, stdoutOutput)
		}
	}
	if strings.Contains(stdoutOutput, "Note:") {
		t.Errorf("expected the open-conflict warning, not the decided note, got: %s", stdoutOutput)
	}
}

func TestClusterGitopsStatusNotesADecidedConflict(t *testing.T) {
	status := syncedGitopsStatus()
	status.SyncStatus = strPtrCmd("conflict")
	status.OpenConflictCount = intPtrCmd(0)
	status.OpenConflictKeys = []string{}
	mock := &gitopsStatusMock{status: status}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})

	if !strings.Contains(stdoutOutput, "Note: the last sync stopped on a merge conflict and applied nothing") {
		t.Errorf("expected the decided-conflict note, got: %s", stdoutOutput)
	}
	if strings.Contains(stdoutOutput, "Open Conflicts") || strings.Contains(stdoutOutput, "Warning:") {
		t.Errorf("expected no open-conflict lines, got: %s", stdoutOutput)
	}
}

func TestClusterGitopsStatusWithoutConflictReportPrintsNoConflictLines(t *testing.T) {
	mock := &gitopsStatusMock{status: syncedGitopsStatus()}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})

	for _, absent := range []string{"Open Conflicts", "Note:"} {
		if strings.Contains(stdoutOutput, absent) {
			t.Errorf("expected output without %q, got: %s", absent, stdoutOutput)
		}
	}
}

func TestClusterGitopsStatusJSONOutputCarriesOpenConflicts(t *testing.T) {
	status := syncedGitopsStatus()
	status.SyncStatus = strPtrCmd("conflict")
	status.OpenConflictCount = intPtrCmd(1)
	status.OpenConflictKeys = []string{"stack:web/manifest:ingress"}
	mock := &gitopsStatusMock{status: status}
	setMockClient(t, mock)
	// -o persists on the shared command between executions; reset it so the
	// human-output tests after this one do not render JSON.
	t.Cleanup(func() { _ = clusterGitopsStatusCmd.Flags().Set("output", "") })

	output, err := executeCommand("cluster", "gitops", "status", "my-cluster", "-o", "json")
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	var decoded map[string]interface{}
	if unmarshalErr := json.Unmarshal([]byte(output), &decoded); unmarshalErr != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalErr, output)
	}
	if decoded["open_conflict_count"] != float64(1) {
		t.Errorf("open_conflict_count = %v, want 1", decoded["open_conflict_count"])
	}
	keys, ok := decoded["open_conflict_keys"].([]interface{})
	if !ok || len(keys) != 1 || keys[0] != "stack:web/manifest:ingress" {
		t.Errorf("open_conflict_keys = %v, want [stack:web/manifest:ingress]", decoded["open_conflict_keys"])
	}
}

func TestClusterGitopsStatusNotConfigured(t *testing.T) {
	mock := &gitopsStatusMock{status: &client.ClusterGitopsStatus{
		SyncStatus: strPtrCmd("not_configured"),
	}}
	setMockClient(t, mock)

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})

	if !strings.Contains(stdoutOutput, "Repository: not configured") {
		t.Errorf("expected not-configured repository line, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Sync Status: not_configured") {
		t.Errorf("expected not_configured sync status, got: %s", stdoutOutput)
	}
}

func TestClusterGitopsStatusJSONOutput(t *testing.T) {
	mock := &gitopsStatusMock{status: syncedGitopsStatus()}
	setMockClient(t, mock)

	output, err := executeCommand("cluster", "gitops", "status", "my-cluster", "-o", "json")
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	var decoded map[string]interface{}
	if unmarshalErr := json.Unmarshal([]byte(output), &decoded); unmarshalErr != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalErr, output)
	}
	if decoded["sync_status"] != "synced" {
		t.Errorf("sync_status = %v, want synced", decoded["sync_status"])
	}
	gitRepo, ok := decoded["git_repo"].(map[string]interface{})
	if !ok {
		t.Fatalf("git_repo missing from JSON output: %s", output)
	}
	if gitRepo["credential_name"] != "github-creds" {
		t.Errorf("git_repo.credential_name = %v, want github-creds", gitRepo["credential_name"])
	}
}

func TestGitopsRepositoryLabelProviderShapes(t *testing.T) {
	tests := []struct {
		name string
		repo client.ClusterGitopsRepo
		want string
	}{
		{
			name: "github owner and name",
			repo: client.ClusterGitopsRepo{
				WebURL:    "https://github.com/ankra/gitops",
				RepoOwner: strPtrCmd("ankra"),
				RepoName:  strPtrCmd("gitops"),
			},
			want: "ankra/gitops",
		},
		{
			name: "bitbucket cloud workspace and slug",
			repo: client.ClusterGitopsRepo{
				WebURL:    "https://bitbucket.org/ankra/gitops",
				Workspace: strPtrCmd("ankra"),
				RepoSlug:  strPtrCmd("gitops"),
			},
			want: "ankra/gitops",
		},
		{
			name: "bitbucket data center project and slug",
			repo: client.ClusterGitopsRepo{
				WebURL:     "https://bitbucket.internal/projects/ANK/repos/gitops",
				ProjectKey: strPtrCmd("ANK"),
				RepoSlug:   strPtrCmd("gitops"),
			},
			want: "ANK/gitops",
		},
		{
			name: "no pair falls back to the web url",
			repo: client.ClusterGitopsRepo{WebURL: "https://github.com/ankra/gitops"},
			want: "https://github.com/ankra/gitops",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := gitopsRepositoryLabel(&testCase.repo); got != testCase.want {
				t.Errorf("gitopsRepositoryLabel() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestGitopsErrorSummaryShapes(t *testing.T) {
	tests := []struct {
		name      string
		errorInfo map[string]interface{}
		want      string
	}{
		{
			name:      "general message",
			errorInfo: map[string]interface{}{"error_type": "general", "message": "boom"},
			want:      "boom",
		},
		{
			name: "multiple validation errors joined",
			errorInfo: map[string]interface{}{
				"error_type": "multiple_validation_errors",
				"errors": []interface{}{
					map[string]interface{}{"message": "first"},
					map[string]interface{}{"message": "second"},
				},
			},
			want: "first; second",
		},
		{
			name:      "error type fallback",
			errorInfo: map[string]interface{}{"error_type": "validation_error"},
			want:      "validation_error",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := gitopsErrorSummary(testCase.errorInfo); got != testCase.want {
				t.Errorf("gitopsErrorSummary() = %q, want %q", got, testCase.want)
			}
		})
	}
}

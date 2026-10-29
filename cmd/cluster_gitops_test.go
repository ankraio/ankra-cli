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
		"'ankra cluster gitops conflicts list'",
		"'ankra cluster gitops conflicts resolve'",
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
	// An unreported count is unknown, not zero: neither the open-conflict
	// lines nor the "no conflict awaits a decision" note may print, even
	// beside a conflict status.
	for _, syncStatus := range []string{"synced", "conflict"} {
		status := syncedGitopsStatus()
		status.SyncStatus = strPtrCmd(syncStatus)
		mock := &gitopsStatusMock{status: status}
		setMockClient(t, mock)

		stdoutOutput := captureStdout(t, func() {
			_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
		})

		for _, absent := range []string{"Open Conflicts", "Note:", "Warning:"} {
			if strings.Contains(stdoutOutput, absent) {
				t.Errorf("sync_status %s: expected output without %q, got: %s", syncStatus, absent, stdoutOutput)
			}
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
	resetGitopsStatusOutputFlag(t)

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

// PLA-914: Git's head moved without touching the cluster's files, so the
// platform advanced the applied commit without a sync, and the status read
// "last synced 2 days ago" for a current cluster (ankra-xx7u1l). The applied
// commit and the head check are printed beside the last full sync.
func TestClusterGitopsStatusShowsTheCommitPointer(t *testing.T) {
	resetGitopsStatusOutputFlag(t)
	status := syncedGitopsStatus()
	status.AppliedCommitSHA = strPtrCmd("def456")
	status.AppliedCommitAt = strPtrCmd("2026-10-27T10:00:00Z")
	status.HeadCheckedSHA = strPtrCmd("def456")
	status.HeadCheckedAt = strPtrCmd("2026-10-29T10:00:00Z")
	setMockClient(t, &gitopsStatusMock{status: status})

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})
	for _, fragment := range []string{
		"Last Synced Commit: abc123",
		"Applied Commit: def456, since 2026-10-27T10:00:00Z (",
		"Git Head Checked: def456, read 2026-10-29T10:00:00Z (",
		"Note: the cluster is in step with Git's head; commits since the last full sync did not change this cluster's files",
	} {
		if !strings.Contains(stdoutOutput, fragment) {
			t.Errorf("expected output to contain %q, got: %s", fragment, stdoutOutput)
		}
	}

	behind := syncedGitopsStatus()
	behind.AppliedCommitSHA = strPtrCmd("abc123")
	behind.HeadCheckedSHA = strPtrCmd("fff999")
	setMockClient(t, &gitopsStatusMock{status: behind})
	behindOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})
	if !strings.Contains(behindOutput, "Git Head Checked: fff999\n") ||
		!strings.Contains(behindOutput, "Note: Git's head is not applied to the cluster yet") {
		t.Errorf("expected the unapplied head named, got: %s", behindOutput)
	}

	// A platform that reports no pointer prints no pointer lines.
	setMockClient(t, &gitopsStatusMock{status: syncedGitopsStatus()})
	bareOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})
	for _, absent := range []string{"Applied Commit", "Git Head Checked", "Note:"} {
		if strings.Contains(bareOutput, absent) {
			t.Errorf("expected output without %q, got: %s", absent, bareOutput)
		}
	}
}

// ankra-u5m2k6: a diverged status names each preserved file, when it was
// detected and whether it is decided, instead of only "diverged".
func TestClusterGitopsStatusListsPreservedPaths(t *testing.T) {
	resetGitopsStatusOutputFlag(t)
	status := syncedGitopsStatus()
	status.SyncStatus = strPtrCmd("diverged")
	status.PreservedPathCount = intPtrCmd(3)
	status.PreservedPathUndecidedCount = intPtrCmd(2)
	status.PreservedPaths = []string{"clusters/staging/staging.yaml", "clusters/staging/stacks/a/values.yaml"}
	status.PreservedPathDetails = []client.ClusterGitopsPreservedPath{
		{Path: "clusters/staging/staging.yaml", DetectedAt: strPtrCmd("2026-10-23T08:00:00Z"), Decided: boolPtrCmd(false)},
		{Path: "clusters/staging/stacks/a/values.yaml", DetectedAt: strPtrCmd("2026-10-24T08:00:00Z"),
			Decided: boolPtrCmd(true), ResolutionChoice: strPtrCmd("git"), ResolvedAt: strPtrCmd("2026-10-25T08:00:00Z")},
	}
	setMockClient(t, &gitopsStatusMock{status: status})

	stdoutOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})
	for _, fragment := range []string{
		"Sync Status: diverged",
		"Preserved Paths: 3 (2 awaiting a decision)",
		"    - clusters/staging/staging.yaml (detected 2026-10-23T08:00:00Z (",
		"), awaiting a decision)",
		"    - clusters/staging/stacks/a/values.yaml (detected 2026-10-24T08:00:00Z (",
		"decided: take Git's version, runs on the next sync)",
		"    ... and 1 more",
		"Ankra keeps Git's version",
	} {
		if !strings.Contains(stdoutOutput, fragment) {
			t.Errorf("expected output to contain %q, got: %s", fragment, stdoutOutput)
		}
	}

	// A platform with names but no details still lists the names.
	namesOnly := syncedGitopsStatus()
	namesOnly.PreservedPathCount = intPtrCmd(1)
	namesOnly.PreservedPaths = []string{"clusters/staging/staging.yaml"}
	setMockClient(t, &gitopsStatusMock{status: namesOnly})
	namesOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})
	if !strings.Contains(namesOutput, "Preserved Paths: 1\n") || !strings.Contains(namesOutput, "    - clusters/staging/staging.yaml\n") {
		t.Errorf("expected the names listed, got: %s", namesOutput)
	}

	// An unreported decision is unknown, not undecided; a count with no
	// names says the names were not sent rather than implying none exist.
	unreported := syncedGitopsStatus()
	unreported.PreservedPathCount = intPtrCmd(2)
	unreported.PreservedPathDetails = []client.ClusterGitopsPreservedPath{{Path: "clusters/staging/staging.yaml"}}
	setMockClient(t, &gitopsStatusMock{status: unreported})
	unreportedOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})
	if !strings.Contains(unreportedOutput, "    - clusters/staging/staging.yaml\n") ||
		strings.Contains(unreportedOutput, "awaiting a decision") || !strings.Contains(unreportedOutput, "    ... and 1 more") {
		t.Errorf("expected the path without a decision claim, got: %s", unreportedOutput)
	}
	countOnly := syncedGitopsStatus()
	countOnly.PreservedPathCount = intPtrCmd(4)
	setMockClient(t, &gitopsStatusMock{status: countOnly})
	countOnlyOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})
	if !strings.Contains(countOnlyOutput, "Preserved Paths: 4\n") ||
		!strings.Contains(countOnlyOutput, "(the platform did not list the files)") {
		t.Errorf("expected the unlisted files named as unlisted, got: %s", countOnlyOutput)
	}

	// An unreported count is unknown, not zero: nothing prints.
	setMockClient(t, &gitopsStatusMock{status: syncedGitopsStatus()})
	bareOutput := captureStdout(t, func() {
		_, _ = executeCommand("cluster", "gitops", "status", "my-cluster")
	})
	if strings.Contains(bareOutput, "Preserved Paths") {
		t.Errorf("expected no preserved-path lines, got: %s", bareOutput)
	}
}

// -o json carries the preserved paths and the commit pointer, which the
// client used to drop on decode.
func TestClusterGitopsStatusJSONOutputCarriesPreservedPathsAndPointer(t *testing.T) {
	status := syncedGitopsStatus()
	status.PreservedPathCount = intPtrCmd(1)
	status.PreservedPathUndecidedCount = intPtrCmd(1)
	status.PreservedPaths = []string{"clusters/staging/staging.yaml"}
	status.PreservedPathDetails = []client.ClusterGitopsPreservedPath{
		{Path: "clusters/staging/staging.yaml", DetectedAt: strPtrCmd("2026-10-23T08:00:00Z"), Decided: boolPtrCmd(false)},
	}
	status.AppliedCommitSHA = strPtrCmd("def456")
	status.HeadCheckedAt = strPtrCmd("2026-10-29T10:00:00Z")
	setMockClient(t, &gitopsStatusMock{status: status})
	resetGitopsStatusOutputFlag(t)

	output, err := executeCommand("cluster", "gitops", "status", "my-cluster", "-o", "json")
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	var decoded map[string]interface{}
	if unmarshalErr := json.Unmarshal([]byte(output), &decoded); unmarshalErr != nil {
		t.Fatalf("expected parseable JSON on stdout, got %v: %s", unmarshalErr, output)
	}
	details, ok := decoded["preserved_path_details"].([]interface{})
	if decoded["preserved_path_count"] != float64(1) || decoded["preserved_path_undecided_count"] != float64(1) ||
		!ok || len(details) != 1 || decoded["applied_commit_sha"] != "def456" ||
		decoded["head_checked_at"] != "2026-10-29T10:00:00Z" {
		t.Fatalf("JSON output = %s", output)
	}
	detail, _ := details[0].(map[string]interface{})
	if detail["path"] != "clusters/staging/staging.yaml" || detail["decided"] != false ||
		detail["detected_at"] != "2026-10-23T08:00:00Z" {
		t.Errorf("preserved_path_details[0] = %v", detail)
	}
}

// The client decodes the platform's fields (ankra-xx7u1l, ankra-u5m2k6).
func TestClusterGitopsStatusDecodesThePlatformFields(t *testing.T) {
	var status client.ClusterGitopsStatus
	body := `{"sync_status":"diverged","preserved_path_count":2,"preserved_paths":["a.yaml","b.yaml"],` +
		`"preserved_path_undecided_count":1,"preserved_path_details":[{"path":"a.yaml","detected_at":"2026-10-23T08:00:00Z",` +
		`"last_seen_at":"2026-10-29T08:00:00Z","decided":true,"resolution_choice":"cluster","resolved_at":"2026-10-24T08:00:00Z"}],` +
		`"applied_commit_sha":"def456","applied_commit_at":"2026-10-27T10:00:00Z","head_checked_sha":"def456",` +
		`"head_checked_at":"2026-10-29T10:00:00Z"}`
	if decodeErr := json.Unmarshal([]byte(body), &status); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if *status.PreservedPathCount != 2 || *status.PreservedPathUndecidedCount != 1 || len(status.PreservedPaths) != 2 ||
		len(status.PreservedPathDetails) != 1 || status.PreservedPathDetails[0].Decided == nil ||
		!*status.PreservedPathDetails[0].Decided ||
		*status.PreservedPathDetails[0].ResolutionChoice != "cluster" || *status.AppliedCommitSHA != "def456" ||
		*status.AppliedCommitAt != "2026-10-27T10:00:00Z" || *status.HeadCheckedSHA != "def456" ||
		*status.HeadCheckedAt != "2026-10-29T10:00:00Z" {
		t.Fatalf("decoded = %+v", status)
	}
}

// resetGitopsStatusOutputFlag clears -o on the shared status command now and
// when the test ends: cobra keeps a flag's value between executions, so a JSON
// test left it set and every human-output test after it printed nothing.
func resetGitopsStatusOutputFlag(t *testing.T) {
	t.Helper()
	_ = clusterGitopsStatusCmd.Flags().Set("output", "")
	t.Cleanup(func() { _ = clusterGitopsStatusCmd.Flags().Set("output", "") })
}

func boolPtrCmd(value bool) *bool {
	return &value
}

package cmd

import (
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// clusterGitopsCmd groups the GitOps repository visibility commands under
// `ankra cluster gitops`.
var clusterGitopsCmd = &cobra.Command{
	Use:   "gitops",
	Short: "Inspect a cluster's GitOps wiring and resolve its merge conflicts",
	Long: `Commands for inspecting which GitOps repository a cluster syncs from, and for
listing and resolving the merge conflicts that pause its sync.`,
}

var clusterGitopsStatusCmd = &cobra.Command{
	Use:   "status [cluster_name]",
	Short: "Show which GitOps repository a cluster syncs from",
	Long: `Show the GitOps sync status of a cluster: the repository, branch, and
credential it syncs from, the last synced commit, the commit the cluster is
in step with and Git's head as last checked, any pending commit or sync
error, any open merge conflicts pausing the sync, and the files Git and the
platform are divided on (preserved paths), with when each was detected and
whether it is decided.

If no cluster name is provided, uses the currently selected cluster.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID, clusterName, err := resolveClusterFromArgs(cmd, args)
		if err != nil {
			return err
		}
		status, err := apiClient.GetClusterGitopsStatus(clusterID)
		if err != nil {
			return fmt.Errorf("fetching gitops status for %s: %w", clusterName, err)
		}
		if rendered, err := renderStructured(cmd, status); rendered || err != nil {
			return err
		}
		printClusterGitopsStatus(clusterName, status)
		return nil
	},
}

// gitopsRepositoryLabel builds the provider-shaped owner/name label of the
// repository: repo_owner/repo_name for GitHub, workspace/repo_slug for
// Bitbucket Cloud, project_key/repo_slug for Bitbucket Data Center.
func gitopsRepositoryLabel(repo *client.ClusterGitopsRepo) string {
	pair := func(left, right *string) string {
		if left == nil || right == nil {
			return ""
		}
		return *left + "/" + *right
	}
	if label := pair(repo.RepoOwner, repo.RepoName); label != "" {
		return label
	}
	if label := pair(repo.Workspace, repo.RepoSlug); label != "" {
		return label
	}
	if label := pair(repo.ProjectKey, repo.RepoSlug); label != "" {
		return label
	}
	return repo.WebURL
}

// gitopsErrorSummary flattens the backend's error info object (general,
// validation, or multiple-validation shape) into one human-readable line.
func gitopsErrorSummary(errorInfo map[string]interface{}) string {
	if message, ok := errorInfo["message"].(string); ok && message != "" {
		return message
	}
	if nested, ok := errorInfo["errors"].([]interface{}); ok {
		summary := ""
		for _, entry := range nested {
			entryObject, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			message, ok := entryObject["message"].(string)
			if !ok || message == "" {
				continue
			}
			if summary != "" {
				summary += "; "
			}
			summary += message
		}
		if summary != "" {
			return summary
		}
	}
	if errorType, ok := errorInfo["error_type"].(string); ok && errorType != "" {
		return errorType
	}
	return fmt.Sprintf("%v", errorInfo)
}

// printClusterGitopsStatus renders the human-readable status: repository,
// branch, credential, and provider first, then the sync outcome, with the
// pending commit and error lines only when present.
func printClusterGitopsStatus(clusterName string, status *client.ClusterGitopsStatus) {
	valueOrDash := func(value *string) string {
		if value == nil || *value == "" {
			return "-"
		}
		return *value
	}

	fmt.Printf("GitOps Status for %s:\n", clusterName)
	if repo := status.GitRepo; repo != nil {
		fmt.Printf("  Repository: %s (%s)\n", gitopsRepositoryLabel(repo), repo.WebURL)
		fmt.Printf("  Branch: %s\n", repo.Branch)
		fmt.Printf("  Credential: %s\n", valueOrDash(repo.CredentialName))
		fmt.Printf("  Provider: %s\n", repo.Provider)
	} else {
		fmt.Println("  Repository: not configured")
	}
	fmt.Printf("  Sync Status: %s\n", valueOrDash(status.SyncStatus))
	if status.SyncPhase != nil && *status.SyncPhase != "" {
		fmt.Printf("  Sync Phase: %s\n", *status.SyncPhase)
	}
	if status.SyncProgressMessage != nil && *status.SyncProgressMessage != "" {
		fmt.Printf("  Progress: %s\n", *status.SyncProgressMessage)
	}
	printGitopsConflictPause(status)
	printGitopsPreservedPaths(status)
	if status.LastCommitSHA != nil {
		fmt.Printf("  Last Synced Commit: %s\n", *status.LastCommitSHA)
	}
	if status.LastSyncedAt != nil {
		syncedLine := fmt.Sprintf("%s (%s)", *status.LastSyncedAt, formatTimeAgo(*status.LastSyncedAt))
		if status.LastSyncedFrom != nil && *status.LastSyncedFrom != "" {
			syncedLine += " via " + *status.LastSyncedFrom
		}
		fmt.Printf("  Last Synced: %s\n", syncedLine)
	}
	printGitopsCommitPointer(status)
	if status.PendingCommitSHA != nil && *status.PendingCommitSHA != "" {
		fmt.Printf("  Pending Commit: %s\n", *status.PendingCommitSHA)
	}
	if status.AppliedWithFailedMembers {
		fmt.Println("  Warning: applied, but member deployments are failing")
	}
	if status.Error != nil {
		fmt.Printf("  Error: %s\n", gitopsErrorSummary(status.Error))
	}
}

// gitopsConflictSyncStatus is the sync_status the platform reports while
// GitOps sync is paused on a merge conflict.
const gitopsConflictSyncStatus = "conflict"

// printGitopsConflictPause renders the merge-conflict pause: the open
// conflicts by key when any await a decision, or a note when the last sync
// stopped on a conflict that has since been decided. A platform that does not
// report open_conflict_count prints nothing rather than claiming zero: both
// the open-conflict lines and the "no conflict awaits a decision" note need a
// reported count.
func printGitopsConflictPause(status *client.ClusterGitopsStatus) {
	if status.OpenConflictCount == nil {
		return
	}
	if *status.OpenConflictCount > 0 {
		openConflictCount := *status.OpenConflictCount
		fmt.Printf("  Open Conflicts: %d\n", openConflictCount)
		for _, conflictKey := range status.OpenConflictKeys {
			fmt.Printf("    - %s\n", conflictKey)
		}
		if unlisted := openConflictCount - len(status.OpenConflictKeys); unlisted > 0 && len(status.OpenConflictKeys) > 0 {
			fmt.Printf("    ... and %d more\n", unlisted)
		}
		fmt.Println("  Warning: GitOps sync is paused on these merge conflicts; nothing from Git, including commits pushed since, is applied until each is resolved")
		fmt.Println("  See them with 'ankra cluster gitops conflicts list' and resolve them with 'ankra cluster gitops conflicts resolve' (or in the portal, cluster > GitOps)")
		return
	}
	if status.SyncStatus != nil && *status.SyncStatus == gitopsConflictSyncStatus {
		fmt.Println("  Note: the last sync stopped on a merge conflict and applied nothing; no conflict awaits a decision now, so the next sync re-evaluates both sides")
	}
}

// timeWithAge renders a platform timestamp with how long ago it was.
func timeWithAge(timestamp string) string {
	return fmt.Sprintf("%s (%s)", timestamp, formatTimeAgo(timestamp))
}

// printGitopsCommitPointer renders the commit the platform holds the cluster
// in step with and Git's head as last read. Last Synced describes the last
// full sync only: when Git's head moves without touching this cluster's
// files the platform advances the applied commit without a sync, and on
// PLA-914 a current cluster read "last synced 2 days ago". A platform that
// does not report the pointer prints nothing here.
func printGitopsCommitPointer(status *client.ClusterGitopsStatus) {
	if status.AppliedCommitSHA != nil && *status.AppliedCommitSHA != "" {
		appliedLine := *status.AppliedCommitSHA
		if status.AppliedCommitAt != nil {
			appliedLine += ", since " + timeWithAge(*status.AppliedCommitAt)
		}
		fmt.Printf("  Applied Commit: %s\n", appliedLine)
	}
	if status.HeadCheckedSHA == nil || *status.HeadCheckedSHA == "" {
		return
	}
	headLine := *status.HeadCheckedSHA
	if status.HeadCheckedAt != nil {
		headLine += ", read " + timeWithAge(*status.HeadCheckedAt)
	}
	fmt.Printf("  Git Head Checked: %s\n", headLine)
	if status.AppliedCommitSHA == nil || *status.AppliedCommitSHA == "" {
		return
	}
	switch {
	case *status.HeadCheckedSHA != *status.AppliedCommitSHA:
		fmt.Println("  Note: Git's head is not applied to the cluster yet; the next sync applies it")
	case status.LastCommitSHA != nil && *status.LastCommitSHA != *status.AppliedCommitSHA:
		fmt.Println("  Note: the cluster is in step with Git's head; commits since the last full sync did not change this cluster's files, so the platform advanced the applied commit without a new sync")
	}
}

// gitopsPreservedDecisionLabels words the two decisions a preserved path can
// carry.
var gitopsPreservedDecisionLabels = map[string]string{
	"git":     "take Git's version",
	"cluster": "take Ankra's version",
}

// printGitopsPreservedPaths renders the files Git and the platform are
// divided on, each with when it was detected and whether it is decided. A
// platform that does not report preserved_path_count prints nothing rather
// than claiming there are none.
func printGitopsPreservedPaths(status *client.ClusterGitopsStatus) {
	if status.PreservedPathCount == nil || *status.PreservedPathCount == 0 {
		return
	}
	preservedPathCount := *status.PreservedPathCount
	countLine := fmt.Sprintf("%d", preservedPathCount)
	if status.PreservedPathUndecidedCount != nil {
		countLine += fmt.Sprintf(" (%d awaiting a decision)", *status.PreservedPathUndecidedCount)
	}
	fmt.Printf("  Preserved Paths: %s\n", countLine)
	listed := len(status.PreservedPaths)
	if len(status.PreservedPathDetails) > 0 {
		listed = len(status.PreservedPathDetails)
		for _, detail := range status.PreservedPathDetails {
			var facts []string
			if detail.DetectedAt != nil {
				facts = append(facts, "detected "+timeWithAge(*detail.DetectedAt))
			}
			switch {
			case detail.Decided == nil:
			case !*detail.Decided:
				facts = append(facts, "awaiting a decision")
			case detail.ResolutionChoice != nil && gitopsPreservedDecisionLabels[*detail.ResolutionChoice] != "":
				facts = append(facts, "decided: "+gitopsPreservedDecisionLabels[*detail.ResolutionChoice]+
					", runs on the next sync")
			default:
				facts = append(facts, "decided, runs on the next sync")
			}
			if len(facts) == 0 {
				fmt.Printf("    - %s\n", detail.Path)
				continue
			}
			fmt.Printf("    - %s (%s)\n", detail.Path, strings.Join(facts, ", "))
		}
	} else {
		for _, preservedPath := range status.PreservedPaths {
			fmt.Printf("    - %s\n", preservedPath)
		}
	}
	switch unlisted := preservedPathCount - listed; {
	case listed == 0:
		fmt.Println("    (the platform did not list the files)")
	case unlisted > 0:
		fmt.Printf("    ... and %d more\n", unlisted)
	}
	fmt.Println("  Note: each file was edited in Git, so Ankra keeps Git's version and its own later changes to it do not reach Git until a person decides it (in the portal, cluster > GitOps, or the preserved-paths API); nothing is paused")
}

func init() {
	registerStructuredOutputFlags(clusterGitopsStatusCmd)
	clusterGitopsCmd.AddCommand(clusterGitopsStatusCmd)
	clusterCmd.AddCommand(clusterGitopsCmd)
}

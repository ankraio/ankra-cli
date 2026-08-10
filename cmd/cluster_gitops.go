package cmd

import (
	"fmt"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// clusterGitopsCmd groups the GitOps repository visibility commands under
// `ankra cluster gitops`.
var clusterGitopsCmd = &cobra.Command{
	Use:   "gitops",
	Short: "Inspect the GitOps repository wiring of a cluster",
	Long:  `Commands for inspecting which GitOps repository a cluster syncs from.`,
}

var clusterGitopsStatusCmd = &cobra.Command{
	Use:   "status [cluster_name]",
	Short: "Show which GitOps repository a cluster syncs from",
	Long: `Show the GitOps sync status of a cluster: the repository, branch, and
credential it syncs from, the last synced commit, and any pending commit or
sync error.

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

func init() {
	registerStructuredOutputFlags(clusterGitopsStatusCmd)
	clusterGitopsCmd.AddCommand(clusterGitopsStatusCmd)
	clusterCmd.AddCommand(clusterGitopsCmd)
}

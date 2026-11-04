package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// securityAllProjectsFlag widens a Security Center read to the whole
// organisation, as costAllProjectsFlag does a fleet cost read.
const securityAllProjectsFlag = "all-projects"

// registerSecurityProjectScopeFlag adds --all-projects to Security Center reads
// and sets the read's scope before the command runs, so a --cluster lookup and
// every read the command makes answer under the same scope.
func registerSecurityProjectScopeFlag(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().Bool(securityAllProjectsFlag, false, "Read the whole organisation instead of the project you act in")
		run := command.RunE
		command.RunE = func(cmd *cobra.Command, args []string) error {
			applySecurityProjectScope(cmd)
			return securityScopeRefusal(cmd, run(cmd, args))
		}
	}
}

// applySecurityProjectScope sets the Security Center reads' ?project_id= for this
// command: all with --all-projects, otherwise none, so a scope set by an earlier
// command on the same client never carries over. A pinned token or a member
// limited to projects is refused by the platform (403) for all.
func applySecurityProjectScope(cmd *cobra.Command) {
	scope := ""
	if securityReadsAllProjects(cmd) {
		scope = client.CostProjectScopeAll
	}
	apiClient.SetSecurityProjectScope(scope)
}

// securityScopeRefusal says what a 403 under --all-projects most likely means. It
// keeps the exit code: the platform refused the read.
func securityScopeRefusal(cmd *cobra.Command, runError error) error {
	if runError == nil || !securityReadsAllProjects(cmd) || exitCodeFor(runError) != exitForbidden {
		return runError
	}
	return withExitCode(exitForbidden, fmt.Errorf("--all-projects was refused: a pinned token or a member limited to projects reads only their own project, so leave the flag off: %w", runError))
}

func securityReadsAllProjects(cmd *cobra.Command) bool {
	allProjects, _ := cmd.Flags().GetBool(securityAllProjectsFlag)
	return allProjects
}

// resolveSecurityClusterID resolves a --cluster value. The cluster list answers
// for the project you act in, so under --all-projects a name is looked up in the
// Security Center's own cluster list, which the flag widens to every project.
func resolveSecurityClusterID(cmd *cobra.Command, nameOrID string) (string, error) {
	if !securityReadsAllProjects(cmd) || isLikelyClusterID(nameOrID) {
		return resolveClusterID(nameOrID)
	}
	list, err := apiClient.ListSecurityClusters(client.SecurityClustersOptions{Search: nameOrID, Page: 1, PageSize: 100})
	if err != nil {
		return "", fmt.Errorf("listing clusters: %w", err)
	}
	var caseInsensitiveMatches []client.SecurityClusterPosture
	for _, cluster := range list.Result {
		if cluster.ClusterName == nameOrID {
			return cluster.ClusterID, nil
		}
		if strings.EqualFold(cluster.ClusterName, nameOrID) {
			caseInsensitiveMatches = append(caseInsensitiveMatches, cluster)
		}
	}
	switch len(caseInsensitiveMatches) {
	case 0:
		if list.Pagination.TotalPages > 1 {
			return "", fmt.Errorf("cluster %q was not among the first 100 clusters matching it; pass the cluster id instead", nameOrID)
		}
		return "", withExitCode(exitNotFound, fmt.Errorf("cluster %q not found", nameOrID))
	case 1:
		return caseInsensitiveMatches[0].ClusterID, nil
	default:
		candidates := make([]string, 0, len(caseInsensitiveMatches))
		for _, cluster := range caseInsensitiveMatches {
			candidates = append(candidates, fmt.Sprintf("%s (%s)", cluster.ClusterName, cluster.ClusterID))
		}
		return "", withExitCode(exitUsage, fmt.Errorf("cluster %q is ambiguous - %d clusters differ from it only by case: %s; pass the cluster id instead",
			nameOrID, len(caseInsensitiveMatches), strings.Join(candidates, ", ")))
	}
}

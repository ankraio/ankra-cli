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

const (
	securityClusterLookupPageSize = 100
	securityClusterLookupMaxPages = 20
)

// registerSecurityProjectScopeFlag adds --all-projects to Security Center reads
// and sets the read's scope before the command runs, so a --cluster lookup and
// every read the command makes answer under the same scope.
func registerSecurityProjectScopeFlag(commands ...*cobra.Command) {
	for _, command := range commands {
		run := command.RunE
		if run == nil {
			continue
		}
		command.Flags().Bool(securityAllProjectsFlag, false, "Read the whole organisation instead of the project you act in")
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
	return withExitCode(exitForbidden, fmt.Errorf("--all-projects was refused, most likely because a pinned token or a member limited to projects reads only their own project; if so, leave the flag off: %w", runError))
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
	var exactMatches, caseInsensitiveMatches []client.SecurityClusterPosture
	for page := 1; ; page++ {
		if page > securityClusterLookupMaxPages {
			return "", fmt.Errorf("more than %d pages of clusters match %q, so it could not be resolved; pass the cluster id instead",
				securityClusterLookupMaxPages, nameOrID)
		}
		list, err := apiClient.ListSecurityClusters(client.SecurityClustersOptions{Search: nameOrID, Page: page, PageSize: securityClusterLookupPageSize})
		if err != nil {
			return "", fmt.Errorf("listing clusters: %w", err)
		}
		for _, cluster := range list.Result {
			switch {
			case cluster.ClusterName == nameOrID:
				exactMatches = append(exactMatches, cluster)
			case strings.EqualFold(cluster.ClusterName, nameOrID):
				caseInsensitiveMatches = append(caseInsensitiveMatches, cluster)
			}
		}
		// A response without a page count ends only on a short page, so a missing
		// count can never pass for having read everything.
		isLastPage := len(list.Result) < securityClusterLookupPageSize
		if list.Pagination.TotalPages > 0 {
			isLastPage = list.Pagination.TotalPages <= page
		}
		if isLastPage {
			break
		}
	}
	matches := exactMatches
	if len(matches) == 0 {
		matches = caseInsensitiveMatches
	}
	switch len(matches) {
	case 0:
		return "", withExitCode(exitNotFound, fmt.Errorf("cluster %q not found", nameOrID))
	case 1:
		return matches[0].ClusterID, nil
	default:
		candidates := make([]string, 0, len(matches))
		for _, cluster := range matches {
			candidates = append(candidates, fmt.Sprintf("%s (%s)", cluster.ClusterName, cluster.ClusterID))
		}
		return "", withExitCode(exitUsage, fmt.Errorf("cluster %q is ambiguous across projects - %d clusters match: %s; pass the cluster id instead",
			nameOrID, len(matches), strings.Join(candidates, ", ")))
	}
}

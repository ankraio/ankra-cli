package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// minimumExecutionIDPrefixLength is the shortest id a person may abbreviate
// to. Below it almost any word made of a-f would be read as an id.
const minimumExecutionIDPrefixLength = 6

// fullExecutionIDLength is the length of an execution's canonical UUID.
const fullExecutionIDLength = 36

// executionIDPrefixPattern is what the start of a canonical UUID can look
// like. Anything else is a typo, and the walk below would only spend calls
// finding that out.
var executionIDPrefixPattern = regexp.MustCompile(`^[0-9a-f][0-9a-f-]*$`)

// resolveExecutionReference turns the execution id a command was given into a
// full one. The executions API takes only a complete UUID and answers anything
// else with a bare 422, while people carry short ids around: reports and chat
// abbreviate them, the same way they abbreviate a commit.
//
// A full id is passed through without a call. A prefix is looked up among the
// active cluster's executions, or the whole organisation's when no cluster is
// selected, because one execution is read by its id alone and needs no
// cluster. Internal executions are included: the id is what selects here, not
// the listing's default view.
func resolveExecutionReference(cmd *cobra.Command, reference string) (string, error) {
	prefix := strings.ToLower(strings.TrimSpace(reference))
	if len(prefix) == fullExecutionIDLength {
		return prefix, nil
	}
	if len(prefix) < minimumExecutionIDPrefixLength || len(prefix) > fullExecutionIDLength ||
		!executionIDPrefixPattern.MatchString(prefix) {
		return "", withExitCode(exitUsage, fmt.Errorf(
			"%q is not an execution id: pass the full id, or at least its first %d characters",
			reference, minimumExecutionIDPrefixLength))
	}

	options := client.ListExecutionsOptions{IncludeInternalExecutions: true, Page: 1, PageSize: maxExecutionsPageSize}
	searched := "this organisation"
	cluster, clusterError := resolveActiveCluster(cmd)
	switch {
	case clusterError == nil:
		options.ClusterID = cluster.ID
		searched = "cluster " + cluster.Name
	case !errors.As(clusterError, &errNoClusterSelected{}):
		return "", clusterError
	}

	// One past the read cap, so the walk runs out of pages rather than
	// reaching its limit: only that path records that it stopped with rows
	// unread.
	page, listError := executionsQuery{options: options, limit: executionsReadCap() + 1}.fetch()
	if listError != nil {
		return "", listError
	}

	matches := make([]string, 0, 1)
	for _, execution := range page.executions {
		if strings.HasPrefix(strings.ToLower(execution.ID), prefix) {
			matches = append(matches, execution.ID)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		return "", withExitCode(exitUsage, fmt.Errorf(
			"%q matches %d executions in %s, pass more of the id:\n  %s",
			reference, len(matches), searched, strings.Join(matches, "\n  ")))
	case page.stoppedAtCap:
		// Not exitNotFound: rows nobody read are not a verified absence.
		return "", fmt.Errorf(
			"no execution starting with %q in the %d most recent in %s, which is as far back as a short id is looked up. Pass the full id",
			reference, executionsReadCap(), searched)
	}
	return "", withExitCode(exitNotFound, fmt.Errorf(
		"no execution starting with %q in %s. If it ran on another cluster, pass --cluster or the full id",
		reference, searched))
}

// resolveExecutionReferences resolves each id of a command that takes several.
func resolveExecutionReferences(cmd *cobra.Command, references []string) ([]string, error) {
	resolved := make([]string, 0, len(references))
	for _, reference := range references {
		executionID, resolveError := resolveExecutionReference(cmd, reference)
		if resolveError != nil {
			return nil, resolveError
		}
		resolved = append(resolved, executionID)
	}
	return resolved, nil
}

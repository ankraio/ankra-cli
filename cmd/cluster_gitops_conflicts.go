package cmd

// `ankra cluster gitops conflicts list|resolve`: the CLI surface over the
// GitOps merge conflicts of a cluster (PLA-887). A conflict is a resource
// changed both in the GitOps repository and on the platform since the two
// last agreed; while any is undecided, GitOps sync applies nothing from Git,
// including commits pushed since. Until these verbs existed a token client
// could see the pause in `ankra cluster gitops status` but only the portal
// could end it.
//
// Resolving discards one side of customer-authored content: --keep git
// overwrites the platform's version with the repository's, --keep cluster
// pushes the platform's version back over the change in the repository. So
// `resolve` takes no default side, names what it discards before it asks,
// and needs --yes when there is no terminal to ask on.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// gitopsConflictsRequestTimeout bounds one read or write of the conflict
// routes.
const gitopsConflictsRequestTimeout = 30 * time.Second

// The platform's own 404 details on the conflict routes. Only these are
// read as "not found"; the router's bare 404 (routeAbsentDetail) means the
// platform predates the routes.
const (
	gitopsConflictsClusterNotFoundDetail  = "Cluster not found"
	gitopsConflictsConflictNotFoundDetail = "No open GitOps conflict for the given resource"
)

// gitopsConflictsResolveHint is the pair of commands every listing and
// status warning points at.
const gitopsConflictsResolveHint = "Resolve one:  ankra cluster gitops conflicts resolve <resource-key> --keep git|cluster\n" +
	"Resolve all:  ankra cluster gitops conflicts resolve --all --keep git|cluster"

var clusterGitopsConflictsCmd = &cobra.Command{
	Use:   "conflicts",
	Short: "List and resolve the GitOps merge conflicts pausing a cluster's sync",
	Long: `A GitOps merge conflict is a resource that changed both in the cluster's GitOps
repository and on the platform (in the portal, the CLI or the API) since the two
last agreed. Ankra does not pick a side on its own: while any conflict is
undecided, GitOps sync applies nothing from Git, including commits pushed since.

'list' shows the open conflicts. 'resolve' records which side to keep, for one
conflict or for all of them, and the next sync applies it:

  --keep git      keep the repository's version; the platform's version is
                  overwritten and the cluster is updated to match Git.
  --keep cluster  keep the platform's version; it is pushed back to the
                  repository, over the change made there.`,
}

var clusterGitopsConflictsListCmd = &cobra.Command{
	Use:   "list [cluster_name]",
	Short: "List the open GitOps merge conflicts of a cluster",
	Long: `List the cluster's open GitOps merge conflicts: the resource key of each, how
Git and the cluster each changed it (added, modified or removed), when it was
detected, and whether a resolution is already recorded and waiting for the next
sync to apply it.

If no cluster name is provided, uses the --cluster flag or the currently
selected cluster. -o json|yaml prints the platform's answer as it is.`,
	Example: `  ankra cluster gitops conflicts list
  ankra cluster gitops conflicts list prod
  ankra cluster gitops conflicts list prod -o json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		return runClusterGitopsConflictsList(command, arguments)
	},
}

var clusterGitopsConflictsResolveCmd = &cobra.Command{
	Use:   "resolve [resource_key]",
	Short: "Resolve GitOps merge conflicts by keeping the Git or the cluster version",
	Long: `Record which side of a GitOps merge conflict to keep, for one conflict (by its
resource key, as 'ankra cluster gitops conflicts list' prints it) or for every
open conflict with --all. The next sync applies the choice and clears the
conflict; resolve triggers that sync.

--keep is required, because either side discards the other:

  --keep git      keep the repository's version. The platform's version of the
                  resource is overwritten and the cluster is updated to match Git.
  --keep cluster  keep the platform's version. It is pushed back to the
                  repository, over the change made there.

resolve lists what it is about to resolve and what each choice discards, then
asks for confirmation. --yes skips the question; without a terminal to ask on
(a script, CI) it is required. With --all, every conflict open when the platform
receives the request is resolved the same way, including one detected after the
list was shown.

Resolving needs permission to change the cluster's configuration
(clusters.write). The cluster is the --cluster flag or the currently selected
cluster.`,
	Example: `  ankra cluster gitops conflicts resolve stack:web/addon:nginx --keep git
  ankra cluster gitops conflicts resolve --all --keep cluster --cluster prod
  ankra cluster gitops conflicts resolve --all --keep git --yes -o json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		return runClusterGitopsConflictsResolve(command, arguments)
	},
}

// gitopsConflictsContext bounds one request by the command's own context, so
// an interrupted command stops the request instead of waiting it out.
func gitopsConflictsContext(command *cobra.Command) (context.Context, context.CancelFunc) {
	parent := command.Context()
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, gitopsConflictsRequestTimeout)
}

func runClusterGitopsConflictsList(command *cobra.Command, arguments []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	clusterID, clusterName, resolveError := resolveClusterFromArgs(command, arguments)
	if resolveError != nil {
		return resolveError
	}
	cluster := client.ClusterListItem{ID: clusterID, Name: clusterName}

	ctx, cancel := gitopsConflictsContext(command)
	defer cancel()
	conflicts, listError := apiClient.ListClusterGitopsConflicts(ctx, cluster.ID)
	if listError != nil {
		return gitopsConflictsError(listError, cluster, "", false)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, conflicts)
	}
	printGitopsConflictList(command.OutOrStdout(), cluster.Name, conflicts.Conflicts)
	return nil
}

func runClusterGitopsConflictsResolve(command *cobra.Command, arguments []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	resolveAll, _ := command.Flags().GetBool("all")
	keep, _ := command.Flags().GetString("keep")
	keep = strings.ToLower(strings.TrimSpace(keep))
	resourceKey := ""
	if len(arguments) > 0 {
		resourceKey = strings.TrimSpace(arguments[0])
	}
	switch {
	case resolveAll && resourceKey != "":
		return withExitCode(exitUsage, errors.New("pass either a resource key or --all, not both"))
	case !resolveAll && resourceKey == "":
		return withExitCode(exitUsage, errors.New(
			"name the conflict to resolve (its resource key, from 'ankra cluster gitops conflicts list') or pass --all"))
	}
	if keep != client.GitopsConflictKeepGit && keep != client.GitopsConflictKeepCluster {
		return withExitCode(exitUsage, errors.New(
			"--keep is required and must be 'git' (keep the repository's version) or "+
				"'cluster' (keep the platform's version and push it to the repository)"))
	}

	cluster, clusterError := resolveActiveCluster(command)
	if clusterError != nil {
		return clusterError
	}

	listContext, cancelList := gitopsConflictsContext(command)
	defer cancelList()
	listed, listError := apiClient.ListClusterGitopsConflicts(listContext, cluster.ID)
	if listError != nil {
		return gitopsConflictsError(listError, cluster, "", false)
	}

	targets := listed.Conflicts
	if !resolveAll {
		targets = nil
		for _, conflict := range listed.Conflicts {
			if conflict.ResourceKey == resourceKey {
				targets = append(targets, conflict)
			}
		}
		if len(targets) == 0 {
			return gitopsConflictNotOpenError(cluster, resourceKey)
		}
	}
	if len(targets) == 0 {
		nothingToResolve := &client.GitopsConflictResolution{
			Message: fmt.Sprintf("No open GitOps conflicts on cluster '%s'; nothing to resolve.", cluster.Name),
		}
		if format != outputDefault {
			return encodeStructured(command.OutOrStdout(), format, nothingToResolve)
		}
		_, _ = fmt.Fprintln(command.OutOrStdout(), nothingToResolve.Message)
		return nil
	}

	if confirmError := confirmGitopsConflictResolution(command, cluster, targets, keep, resolveAll); confirmError != nil {
		return confirmError
	}

	resolveContext, cancelResolve := gitopsConflictsContext(command)
	defer cancelResolve()
	var resolution *client.GitopsConflictResolution
	var resolveError error
	if resolveAll {
		resolution, resolveError = apiClient.ResolveAllClusterGitopsConflicts(resolveContext, cluster.ID, keep)
	} else {
		resolution, resolveError = apiClient.ResolveClusterGitopsConflict(resolveContext, cluster.ID, resourceKey, keep)
	}
	if resolveError != nil {
		return gitopsConflictsError(resolveError, cluster, resourceKey, true)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, resolution)
	}
	printGitopsConflictResolution(command.OutOrStdout(), command.ErrOrStderr(), resolution, len(targets), resolveAll)
	return nil
}

// confirmGitopsConflictResolution names every conflict about to be resolved
// and what keeping the chosen side discards, then asks. --yes skips the
// question; without a terminal to ask on, --yes is required, because a
// script that never saw what is discarded must not discard it. Everything
// goes to stderr so -o json|yaml stays parseable.
func confirmGitopsConflictResolution(command *cobra.Command, cluster client.ClusterListItem,
	targets []client.GitopsConflict, keep string, resolveAll bool) error {
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprintf(errOut, "Resolving %s on cluster '%s' by keeping the %s version:\n\n",
		gitopsConflictCountLabel(len(targets)), cluster.Name, gitopsConflictSideLabel(keep))
	for _, conflict := range targets {
		line := fmt.Sprintf("  %s  (Git: %s, cluster: %s)", conflict.ResourceKey,
			valueOrDashString(conflict.GitChangeType), valueOrDashString(conflict.DBChangeType))
		if conflict.ResolutionChoice != nil && *conflict.ResolutionChoice != "" {
			line += fmt.Sprintf(", replacing the recorded choice to keep the %s version",
				gitopsConflictSideLabel(*conflict.ResolutionChoice))
		}
		_, _ = fmt.Fprintln(errOut, line)
	}
	_, _ = fmt.Fprintf(errOut, "\n%s\n", gitopsConflictDiscardSentence(keep, len(targets)))
	if resolveAll {
		_, _ = fmt.Fprintln(errOut, "Every conflict open when the platform receives this is resolved the same way, "+
			"including one detected after this list was read.")
	}

	yes, _ := command.Flags().GetBool("yes")
	if yes {
		_, _ = fmt.Fprintln(errOut)
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"resolving GitOps conflicts on cluster '%s' discards the %s version and needs confirmation, but there "+
				"is no terminal to ask on; re-run with --yes to confirm", cluster.Name, gitopsConflictOtherSideLabel(keep)))
	}
	if promptError := confirmPrompt(command.InOrStdin(), errOut,
		fmt.Sprintf("\nKeep the %s version? [y/N]: ", gitopsConflictSideLabel(keep)), false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

// gitopsConflictDiscardSentence says what keeping one side throws away.
func gitopsConflictDiscardSentence(keep string, count int) string {
	resources := "this resource"
	if count != 1 {
		resources = "these resources"
	}
	if keep == client.GitopsConflictKeepGit {
		return fmt.Sprintf("Keeping Git discards the platform's version of %s: it is overwritten with the "+
			"repository's, and the cluster is updated to match Git.", resources)
	}
	return fmt.Sprintf("Keeping the cluster's version discards the change made in Git to %s: the platform's "+
		"version is pushed back to the repository over it.", resources)
}

func gitopsConflictSideLabel(side string) string {
	if side == client.GitopsConflictKeepGit {
		return "Git"
	}
	return "cluster"
}

func gitopsConflictOtherSideLabel(keep string) string {
	if keep == client.GitopsConflictKeepGit {
		return "cluster"
	}
	return "Git"
}

func gitopsConflictCountLabel(count int) string {
	if count == 1 {
		return "1 GitOps conflict"
	}
	return fmt.Sprintf("%d GitOps conflicts", count)
}

func valueOrDashString(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// printGitopsConflictList renders the listing: a one-line summary, the
// table, and what the pause means with the commands that end it.
func printGitopsConflictList(out io.Writer, clusterName string, conflicts []client.GitopsConflict) {
	if len(conflicts) == 0 {
		_, _ = fmt.Fprintf(out, "No open GitOps conflicts on cluster '%s'.\n", clusterName)
		return
	}
	undecided := 0
	for _, conflict := range conflicts {
		if conflict.ResolutionChoice == nil || *conflict.ResolutionChoice == "" {
			undecided++
		}
	}
	_, _ = fmt.Fprintf(out, "GitOps conflicts on cluster '%s': %d open, %d undecided.\n\n",
		clusterName, len(conflicts), undecided)

	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Resource key", "Git", "Cluster", "Detected", "Resolution"})
	for _, conflict := range conflicts {
		detected := "-"
		if conflict.DetectedAt != nil && *conflict.DetectedAt != "" {
			detected = formatTimeAgo(*conflict.DetectedAt)
		}
		resolution := "undecided"
		if conflict.ResolutionChoice != nil && *conflict.ResolutionChoice != "" {
			resolution = fmt.Sprintf("keep %s (applies on the next sync)", gitopsConflictSideLabel(*conflict.ResolutionChoice))
		}
		writer.AppendRow(table.Row{conflict.ResourceKey, valueOrDashString(conflict.GitChangeType),
			valueOrDashString(conflict.DBChangeType), detected, resolution})
	}
	writer.Render()

	if undecided > 0 {
		_, _ = fmt.Fprintf(out, "\nGitOps sync is paused: nothing from Git, including commits pushed since, is applied "+
			"until every conflict is resolved.\n%s\n", gitopsConflictsResolveHint)
		return
	}
	_, _ = fmt.Fprintln(out, "\nEvery conflict has a recorded resolution; the next sync applies them and clears the conflicts.")
}

// printGitopsConflictResolution reports what the platform recorded and that
// the next sync applies it. A count that differs from what was confirmed is
// said out loud on stderr: under --all the platform resolves every conflict
// open when it receives the request, which can include one detected after
// the list was read, and a conflict cleared in between is not counted.
func printGitopsConflictResolution(out io.Writer, errOut io.Writer, resolution *client.GitopsConflictResolution,
	confirmedCount int, resolveAll bool) {
	_, _ = fmt.Fprintln(out, resolution.Message)
	if resolution.SyncTriggered {
		_, _ = fmt.Fprintln(out, "A sync was triggered; it applies the choice and clears the conflict. "+
			"Follow it with 'ankra cluster gitops status'.")
	} else {
		_, _ = fmt.Fprintln(out, "The sync could not be triggered now; the next periodic reconcile applies the choice "+
			"and clears the conflict. Follow it with 'ankra cluster gitops status'.")
	}
	if !resolveAll || resolution.ClearedCount == confirmedCount {
		return
	}
	if resolution.ClearedCount > confirmedCount {
		_, _ = fmt.Fprintf(errOut, "Note: %d conflicts were listed and %d were resolved; the rest were detected after "+
			"the list was read and were resolved the same way. Check them with 'ankra cluster gitops conflicts list'.\n",
			confirmedCount, resolution.ClearedCount)
		return
	}
	_, _ = fmt.Fprintf(errOut, "Note: %d conflicts were listed and %d were resolved; the rest were cleared before "+
		"the request arrived.\n", confirmedCount, resolution.ClearedCount)
}

// gitopsConflictNotOpenError is the answer for a key with no open conflict:
// it was never one, or it was resolved or cleared since it was listed.
func gitopsConflictNotOpenError(cluster client.ClusterListItem, resourceKey string) error {
	return withExitCode(exitNotFound, fmt.Errorf(
		"no open GitOps conflict '%s' on cluster '%s'; it may have been resolved or cleared already. "+
			"List the open ones with 'ankra cluster gitops conflicts list'", resourceKey, cluster.Name))
}

// gitopsConflictsError maps a refusal from the conflict routes onto what the
// user can act on. A 403 on a resolve is a missing clusters.write and exits
// 7; a 404 carrying the platform's own detail is a cluster outside the
// selected organisation (or a conflict no longer open) and exits 3; the
// router's own 404 (detail "Not Found") is a platform that predates these
// routes, which is not "the cluster does not exist" and so exits 1. A 404
// with no detail at all says neither, so it is reported as unknown rather
// than guessed at. Anything else keeps the platform's detail.
func gitopsConflictsError(apiError error, cluster client.ClusterListItem, resourceKey string, writing bool) error {
	operation := fmt.Sprintf("listing GitOps conflicts on cluster '%s'", cluster.Name)
	if writing {
		operation = fmt.Sprintf("resolving GitOps conflicts on cluster '%s'", cluster.Name)
	}

	var denied *client.PermissionDeniedError
	var unexpected *client.UnexpectedResponseError
	hasStatus := errors.As(apiError, &unexpected)
	if errors.As(apiError, &denied) || (hasStatus && unexpected.StatusCode == http.StatusForbidden) {
		if writing {
			return withExitCode(exitForbidden, fmt.Errorf(
				"you need permission to change the configuration of cluster '%s' (clusters.write) to resolve its "+
					"GitOps conflicts. Ask an organisation admin for a role that has it", cluster.Name))
		}
		return withExitCode(exitForbidden, fmt.Errorf("%s: %w", operation, apiError))
	}
	if hasStatus && unexpected.StatusCode == http.StatusNotFound {
		switch unexpected.Detail {
		case "":
			return withExitCode(exitError, fmt.Errorf(
				"%s: the platform answered 404 without saying why, so it is not known whether the cluster is "+
					"outside this organisation or the platform does not offer these routes yet. "+
					"Check the selected organisation with 'ankra org current' and try again", operation))
		case routeAbsentDetail:
			return withExitCode(exitError, fmt.Errorf(
				"%s: this Ankra platform does not offer GitOps conflicts to the CLI yet (the route answered 404). "+
					"Resolve them in the portal (cluster > GitOps) until it does", operation))
		case gitopsConflictsClusterNotFoundDetail:
			return withExitCode(exitNotFound, fmt.Errorf(
				"cluster '%s' (%s) not found in this organisation. Check the selected organisation with "+
					"'ankra org current', or pick the cluster again with 'ankra cluster select'", cluster.Name, cluster.ID))
		case gitopsConflictsConflictNotFoundDetail:
			return gitopsConflictNotOpenError(cluster, resourceKey)
		}
		return withExitCode(exitError, fmt.Errorf("%s: %w", operation, apiError))
	}
	return fmt.Errorf("%s: %w", operation, apiError)
}

func init() {
	clusterGitopsConflictsResolveCmd.Flags().String("keep", "",
		"Which side to keep: 'git' (the repository's version) or 'cluster' (the platform's version, pushed to the repository)")
	clusterGitopsConflictsResolveCmd.Flags().Bool("all", false, "Resolve every open conflict on the cluster")
	clusterGitopsConflictsResolveCmd.Flags().BoolP("yes", "y", false,
		"Skip the confirmation prompt (required when stdin is not a terminal)")
	registerStructuredOutputFlags(clusterGitopsConflictsListCmd, clusterGitopsConflictsResolveCmd)
	clusterGitopsConflictsCmd.AddCommand(clusterGitopsConflictsListCmd, clusterGitopsConflictsResolveCmd)
	clusterGitopsCmd.AddCommand(clusterGitopsConflictsCmd)
}

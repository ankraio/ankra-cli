package cmd

// `ankra cluster gitops preserved list|show|resolve`: the CLI surface over a
// cluster's preserved GitOps paths (ankra-oh15ok.1). A preserved path is a
// file in the GitOps repository the platform did not write, for example a
// cluster index someone edited by hand: the ownership boundary keeps Git's
// version instead of overwriting it, so the platform's later changes to that
// file never reach Git until a person decides which version wins. Unlike a
// merge conflict it pauses nothing, which is why `conflicts resolve` (keyed by
// resource) cannot decide it: on ankra-ci-sto1 the cluster had no conflicts
// and one undecided preserved path, and only a hand-built API call could
// decide it.
//
// Deciding discards one version of a file people edited: --keep git imports
// Git's version into the platform and the cluster changes to match it (what
// only Ankra declares is removed from the cluster), --keep ankra writes the
// platform's version over the edit in Git. So `resolve` takes no default
// side, reads the two content digests from the listing itself (a hand-typed
// digest would decide over content nobody looked at), shows what the choice
// discards from the platform's structural difference before it asks, and
// needs --yes when there is no terminal to ask on.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// gitopsPreservedRequestTimeout bounds one read or write of the preserved-path
// routes. A listing carries up to 2 MiB of file content.
const gitopsPreservedRequestTimeout = 60 * time.Second

// The platform's own 404 and 409 details on the preserved-path routes. The
// 409s are matched by their opening words, which name the case; the rest of
// each sentence is advice the platform may reword.
const (
	gitopsPreservedClusterNotFoundDetail = "Cluster not found"
	gitopsPreservedPathNotFoundDetail    = "Preserved path not found"
	gitopsPreservedChangedPrefix         = "The file changed in Git or in Ankra since it was listed"
	gitopsPreservedAlreadyDecidedPrefix  = "This preserved path already has a different decision"
	gitopsPreservedSealedPrefix          = "This file holds SOPS-encrypted content"
)

// The --keep values. "ankra" and "cluster" name the same side: "ankra" reads
// as what it keeps, "cluster" matches `conflicts resolve --keep cluster` and
// the platform's own word for it.
const (
	gitopsPreservedKeepGit     = "git"
	gitopsPreservedKeepAnkra   = "ankra"
	gitopsPreservedKeepCluster = "cluster"
)

// gitopsPreservedResolveHint is the pair of commands every listing points at.
const gitopsPreservedResolveHint = "See where a file differs:  ankra cluster gitops preserved show <path>\n" +
	"Decide it:                 ankra cluster gitops preserved resolve <path> --keep git|ankra"

// gitopsPreservedRedactionLabels words why a file's content is withheld.
var gitopsPreservedRedactionLabels = map[string]string{
	"sops_encrypted":       "it holds SOPS-encrypted content",
	"sops_creation_rule":   "a .sops.yaml creation rule covers it",
	"unverifiable":         "it is not YAML the platform's checks can read",
	"secret":               "it declares a Kubernetes Secret",
	"sensitive_value":      "it holds a value under a key that reads as a credential",
	"encrypted_paths":      "it declares encrypted_paths over content it carries inline",
	"detected_secret":      "the secret detector flagged a value in it",
	"awaiting_examination": "the platform has not examined it under the current rules yet",
}

var clusterGitopsPreservedCmd = &cobra.Command{
	Use:   "preserved",
	Short: "List and decide the files Git and Ankra are divided on (preserved paths)",
	Long: `A preserved path is a file in the cluster's GitOps repository that Ankra did not
write, for example a cluster file someone edited by hand. Ankra keeps Git's
version instead of overwriting it, so Ankra's own later changes to that file
(a new add-on, a chart version it rolled) do not reach Git until a person
decides which version wins. Nothing is paused: the rest of the cluster keeps
syncing. These are not merge conflicts, so 'ankra cluster gitops conflicts'
does not list or decide them.

'list' shows the preserved paths and how many differences each has, 'show'
prints where one file's two versions differ, key by key, and 'resolve' records
a decision that the next sync runs:

  --keep git      Ankra imports Git's version of the file, and the cluster
                  changes to match it: what only Ankra's version declares is
                  removed from the cluster.
  --keep ankra    Ankra writes its own version of the file into Git, over the
                  edit made there. --keep cluster means the same.`,
}

var clusterGitopsPreservedListCmd = &cobra.Command{
	Use:   "list [cluster_name]",
	Short: "List the preserved GitOps paths of a cluster",
	Long: `List the files Git and Ankra are divided on: each file's path, when it was
detected, whether a decision is recorded, whether its content is withheld (and
why), and how many differences the platform found between Git's version and
Ankra's.

If no cluster name is provided, uses the --cluster flag or the currently
selected cluster. -o json|yaml prints the platform's answer as it is, including
each file's two versions where the platform lists them.`,
	Example: `  ankra cluster gitops preserved list
  ankra cluster gitops preserved list prod
  ankra cluster gitops preserved list prod -o json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		return runClusterGitopsPreservedList(command, arguments)
	},
}

var clusterGitopsPreservedShowCmd = &cobra.Command{
	Use:   "show <path>",
	Short: "Show where a preserved file's Git and Ankra versions differ",
	Long: `Show one preserved file: when it was detected, its decision, whether its content
is withheld, and every difference the platform found between Git's version and
Ankra's, key by key, read from Git's version to Ankra's:

  added      only Ankra's version has it
  removed    only Git's version has it
  changed    both have it, with different values
  reordered  both list the same members in another order

A value the platform does not show is printed as (masked): seeing values needs
permission to reveal secrets on the cluster (kubernetes.secrets_reveal), and
some files show no values to anyone. The platform lists at most 50 differences
per file; the total is always given.

The path is the full repository path, as 'ankra cluster gitops preserved list'
prints it. The cluster is the --cluster flag or the currently selected cluster.`,
	Example: `  ankra cluster gitops preserved show clusters/prod/prod.yaml
  ankra cluster gitops preserved show clusters/prod/prod.yaml --cluster prod -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		return runClusterGitopsPreservedShow(command, arguments)
	},
}

var clusterGitopsPreservedResolveCmd = &cobra.Command{
	Use:   "resolve <path>",
	Short: "Decide a preserved GitOps path by keeping Git's or Ankra's version",
	Long: `Record which version of a preserved file wins. The next sync runs the decision;
resolve triggers that sync.

--keep is required, because either version discards the other:

  --keep git      Ankra imports Git's version of the file, and the cluster
                  changes to match it. Stacks, add-ons and settings that only
                  Ankra's version declares are removed from the cluster. The
                  import runs once every preserved path on the cluster has a
                  decision.
  --keep ankra    Ankra writes its own version of the file into Git, over the
                  edit made there. --keep cluster means the same. Refused for a
                  file holding SOPS-encrypted content, which would put plaintext
                  in Git in place of ciphertext.

resolve reads the file's current state from the platform first, so the decision
is recorded over exactly the two versions it shows you (the platform refuses it
if either changed in between). It then lists what the choice discards, from the
platform's key-by-key difference, and asks for confirmation. --yes skips the
question; without a terminal to ask on (a script, CI) it is required.

--withdraw removes a recorded decision so the file awaits one again. It changes
nothing in Git or on the cluster, so it asks nothing; a decision the platform
already started running is not recalled.

Deciding needs permission to change the cluster's configuration
(clusters.write). The cluster is the --cluster flag or the currently selected
cluster.`,
	Example: `  ankra cluster gitops preserved resolve clusters/prod/prod.yaml --keep ankra
  ankra cluster gitops preserved resolve clusters/prod/prod.yaml --keep git --cluster prod
  ankra cluster gitops preserved resolve clusters/prod/prod.yaml --keep ankra --yes -o json
  ankra cluster gitops preserved resolve clusters/prod/prod.yaml --withdraw`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(command *cobra.Command, arguments []string) error {
		return runClusterGitopsPreservedResolve(command, arguments)
	},
}

// gitopsPreservedContext bounds one request by the command's own context.
func gitopsPreservedContext(command *cobra.Command) (context.Context, context.CancelFunc) {
	parent := command.Context()
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, gitopsPreservedRequestTimeout)
}

func runClusterGitopsPreservedList(command *cobra.Command, arguments []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	clusterID, clusterName, resolveError := resolveClusterFromArgs(command, arguments)
	if resolveError != nil {
		return resolveError
	}
	cluster := client.ClusterListItem{ID: clusterID, Name: clusterName}

	ctx, cancel := gitopsPreservedContext(command)
	defer cancel()
	listing, listError := apiClient.ListClusterGitopsPreservedPaths(ctx, cluster.ID)
	if listError != nil {
		return gitopsPreservedError(listError, cluster, "", false)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, listing)
	}
	printGitopsPreservedList(command.OutOrStdout(), cluster.Name, listing)
	return nil
}

func runClusterGitopsPreservedShow(command *cobra.Command, arguments []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	path := strings.TrimSpace(arguments[0])
	if path == "" {
		return withExitCode(exitUsage, errors.New("name the preserved path to show, as 'ankra cluster gitops preserved list' prints it"))
	}
	cluster, clusterError := resolveActiveCluster(command)
	if clusterError != nil {
		return clusterError
	}
	listing, entry, findError := findGitopsPreservedPath(command, cluster, path)
	if findError != nil {
		return findError
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, entry)
	}
	printGitopsPreservedPath(command.OutOrStdout(), cluster.Name, entry, listing.StructuralDiffValues)
	return nil
}

// gitopsPreservedKeepResolution maps --keep onto the resolution the platform
// takes: git stays git, ankra and cluster are the platform's "cluster". The
// second result is false for anything else.
func gitopsPreservedKeepResolution(keep string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(keep)) {
	case gitopsPreservedKeepGit:
		return client.GitopsPreservedResolutionGit, true
	case gitopsPreservedKeepAnkra, gitopsPreservedKeepCluster:
		return client.GitopsPreservedResolutionCluster, true
	}
	return "", false
}

func runClusterGitopsPreservedResolve(command *cobra.Command, arguments []string) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	keep, _ := command.Flags().GetString("keep")
	withdraw, _ := command.Flags().GetBool("withdraw")
	path := ""
	if len(arguments) > 0 {
		path = strings.TrimSpace(arguments[0])
	}
	if path == "" {
		return withExitCode(exitUsage, errors.New(
			"name the preserved path to decide (its full repository path, from 'ankra cluster gitops preserved list')"))
	}
	resolution := client.GitopsPreservedResolutionNone
	switch {
	case withdraw && strings.TrimSpace(keep) != "":
		return withExitCode(exitUsage, errors.New("pass either --keep or --withdraw, not both"))
	case !withdraw:
		mapped, known := gitopsPreservedKeepResolution(keep)
		if !known {
			return withExitCode(exitUsage, errors.New(
				"--keep is required and must be 'git' (import Git's version; the cluster changes to match it) or "+
					"'ankra' (write Ankra's version into Git; 'cluster' means the same), or pass --withdraw to "+
					"remove a recorded decision"))
		}
		resolution = mapped
	}

	cluster, clusterError := resolveActiveCluster(command)
	if clusterError != nil {
		return clusterError
	}
	listing, entry, findError := findGitopsPreservedPath(command, cluster, path)
	if findError != nil {
		return findError
	}
	if entry.HeadContentHash == "" || entry.RenderContentHash == "" {
		return fmt.Errorf("the platform listed '%s' on cluster '%s' without its content digests, so no decision "+
			"can name the versions it covers. Try again; if it persists, decide it in the portal", path, cluster.Name)
	}

	recorded := ""
	if entry.ResolutionChoice != nil {
		recorded = *entry.ResolutionChoice
	}
	if resolution == client.GitopsPreservedResolutionNone {
		if recorded == "" {
			return printGitopsPreservedNothingToDo(command, format, entry, resolution,
				fmt.Sprintf("%s has no decision to withdraw; it awaits one.", path))
		}
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "Withdrawing the decision to %s on %s (cluster '%s'). Nothing "+
			"changes in Git or on the cluster; the file awaits a decision again. A decision the platform already "+
			"started running is not recalled.\n\n", gitopsPreservedDecisionLabel(recorded), path, cluster.Name)
	} else {
		switch {
		case recorded == resolution:
			return printGitopsPreservedNothingToDo(command, format, entry, resolution,
				fmt.Sprintf("%s already has this decision (%s); it runs on the next sync. Nothing changed.",
					path, gitopsPreservedDecisionLabel(recorded)))
		case recorded != "":
			return gitopsPreservedAlreadyDecidedError(cluster, path, recorded)
		case resolution == client.GitopsPreservedResolutionCluster && entry.ContentRedactedReason != nil &&
			*entry.ContentRedactedReason == client.GitopsPreservedRedactedSopsEncrypted:
			return gitopsPreservedSealedError(cluster, path)
		}
		if confirmError := confirmGitopsPreservedDecision(command, cluster, listing, entry, resolution); confirmError != nil {
			return confirmError
		}
	}

	resolveContext, cancelResolve := gitopsPreservedContext(command)
	defer cancelResolve()
	result, resolveError := apiClient.ResolveClusterGitopsPreservedPath(resolveContext, cluster.ID,
		gitopsPreservedDecisionFor(entry, resolution))
	if resolveError != nil {
		return gitopsPreservedError(resolveError, cluster, path, true)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, result)
	}
	printGitopsPreservedResolution(command.OutOrStdout(), result)
	return nil
}

// gitopsPreservedDecisionFor builds the resolve body from the listed entry:
// the path and both digests are the listing's, never typed by hand, so the
// platform records the decision only over the two versions the person saw.
func gitopsPreservedDecisionFor(entry client.GitopsPreservedPath, resolution string) client.GitopsPreservedPathDecision {
	return client.GitopsPreservedPathDecision{
		Path:              entry.Path,
		Resolution:        resolution,
		HeadContentHash:   entry.HeadContentHash,
		RenderContentHash: entry.RenderContentHash,
	}
}

// printGitopsPreservedNothingToDo answers a decision the listing shows is
// already in place without sending a write.
func printGitopsPreservedNothingToDo(command *cobra.Command, format outputFormat, entry client.GitopsPreservedPath,
	resolution string, message string) error {
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, &client.GitopsPreservedPathResolution{
			Path: entry.Path, Resolution: resolution, Message: message})
	}
	_, _ = fmt.Fprintln(command.OutOrStdout(), message)
	return nil
}

// findGitopsPreservedPath lists the cluster's preserved paths and returns the
// one named. A path the listing does not carry is not found, unless the
// listing was capped, in which case it may sit past the cap.
func findGitopsPreservedPath(command *cobra.Command, cluster client.ClusterListItem,
	path string) (*client.GitopsPreservedPathList, client.GitopsPreservedPath, error) {
	ctx, cancel := gitopsPreservedContext(command)
	defer cancel()
	listing, listError := apiClient.ListClusterGitopsPreservedPaths(ctx, cluster.ID)
	if listError != nil {
		return nil, client.GitopsPreservedPath{}, gitopsPreservedError(listError, cluster, "", false)
	}
	for _, entry := range listing.PreservedPaths {
		if entry.Path == path {
			return listing, entry, nil
		}
	}
	if listing.Total > len(listing.PreservedPaths) {
		return nil, client.GitopsPreservedPath{}, fmt.Errorf(
			"'%s' is not among the %d preserved paths the platform listed for cluster '%s' (it has %d, and lists "+
				"at most that many), so it cannot be read or decided from the CLI. Decide the listed ones first, "+
				"or decide it in the portal", path, len(listing.PreservedPaths), cluster.Name, listing.Total)
	}
	return nil, client.GitopsPreservedPath{}, gitopsPreservedPathNotFoundError(cluster, path)
}

// confirmGitopsPreservedDecision prints what the decision discards and asks.
// --yes skips the question; without a terminal to ask on, --yes is required,
// because a script that never saw what is discarded must not discard it.
// Everything goes to stderr so -o json|yaml stays parseable.
func confirmGitopsPreservedDecision(command *cobra.Command, cluster client.ClusterListItem,
	listing *client.GitopsPreservedPathList, entry client.GitopsPreservedPath, resolution string) error {
	errOut := command.ErrOrStderr()
	_, _ = fmt.Fprint(errOut, gitopsPreservedPreview(cluster.Name, listing, entry, resolution))

	yes, _ := command.Flags().GetBool("yes")
	if yes {
		_, _ = fmt.Fprintln(errOut)
		return nil
	}
	if !promptIsInteractive(command.InOrStdin()) {
		return withExitCode(exitUsage, fmt.Errorf(
			"deciding preserved path '%s' on cluster '%s' discards %s version and needs confirmation, but there "+
				"is no terminal to ask on; re-run with --yes to confirm", entry.Path, cluster.Name,
			gitopsPreservedDiscardedSide(resolution)))
	}
	if promptError := confirmPrompt(command.InOrStdin(), errOut,
		fmt.Sprintf("\nKeep %s version of %s? [y/N]: ", gitopsPreservedKeptSide(resolution), entry.Path), false); promptError != nil {
		if errors.Is(promptError, errCancelled) {
			return promptError
		}
		return fmt.Errorf("reading the confirmation: %w", promptError)
	}
	return nil
}

func gitopsPreservedKeptSide(resolution string) string {
	if resolution == client.GitopsPreservedResolutionGit {
		return "Git's"
	}
	return "Ankra's"
}

func gitopsPreservedDiscardedSide(resolution string) string {
	if resolution == client.GitopsPreservedResolutionGit {
		return "Ankra's"
	}
	return "Git's"
}

// gitopsPreservedDecisionLabel words a recorded decision.
func gitopsPreservedDecisionLabel(resolution string) string {
	switch resolution {
	case client.GitopsPreservedResolutionGit:
		return "keep Git's version"
	case client.GitopsPreservedResolutionCluster:
		return "keep Ankra's version"
	}
	return "decide it as '" + resolution + "'"
}

// preservedChangeGroup is one line of the preview: a single change, or every
// change under a list member only one version has (a whole stack or add-on).
type preservedChangeGroup struct {
	// entity is the member's key path when the group is a whole member.
	entity  string
	change  string
	changes []client.GitopsPreservedPathChange
}

// groupGitopsPreservedChanges folds the changes under a list member that only
// one version has into one group per member, in the order the platform listed
// them. A member is whole when its own name key is added or removed: a member
// both versions have is matched by that name, so its name never differs.
func groupGitopsPreservedChanges(changes []client.GitopsPreservedPathChange) []preservedChangeGroup {
	entities := map[string][]string{}
	for _, change := range changes {
		if (change.Change == client.GitopsPreservedChangeAdded || change.Change == client.GitopsPreservedChangeRemoved) &&
			strings.HasSuffix(change.Path, "].name") {
			entities[change.Change] = append(entities[change.Change], strings.TrimSuffix(change.Path, ".name"))
		}
	}
	for kind := range entities {
		sort.SliceStable(entities[kind], func(left, right int) bool {
			return len(entities[kind][left]) < len(entities[kind][right])
		})
	}
	var groups []preservedChangeGroup
	groupIndex := map[string]int{}
	for _, change := range changes {
		entity := ""
		for _, candidate := range entities[change.Change] {
			if change.Path == candidate || strings.HasPrefix(change.Path, candidate+".") ||
				strings.HasPrefix(change.Path, candidate+"[") {
				entity = candidate
				break
			}
		}
		if entity == "" {
			groups = append(groups, preservedChangeGroup{change: change.Change,
				changes: []client.GitopsPreservedPathChange{change}})
			continue
		}
		key := change.Change + "\x00" + entity
		if index, seen := groupIndex[key]; seen {
			groups[index].changes = append(groups[index].changes, change)
			continue
		}
		groupIndex[key] = len(groups)
		groups = append(groups, preservedChangeGroup{entity: entity, change: change.Change,
			changes: []client.GitopsPreservedPathChange{change}})
	}
	return groups
}

// preservedPathPlainSegment is one key of a plain key path, with its member
// name when the key is a named list.
var preservedPathPlainSegment = regexp.MustCompile(`^([A-Za-z0-9_-]+)(?:\[name=([A-Za-z0-9_-]+)\])?$`)

// gitopsPreservedEntityLabel names a whole member in words:
// "spec.stacks[name=web].addons[name=nginx]" is "addon nginx in stack web".
// A path with quoting, positions or a document prefix is returned as it is.
func gitopsPreservedEntityLabel(entity string) string {
	var members []string
	for _, segment := range strings.Split(entity, ".") {
		match := preservedPathPlainSegment.FindStringSubmatch(segment)
		if match == nil {
			return entity
		}
		if match[2] == "" {
			continue
		}
		kind := match[1]
		if len(kind) > 1 && strings.HasSuffix(kind, "s") && !strings.HasSuffix(kind, "ss") {
			kind = strings.TrimSuffix(kind, "s")
		}
		members = append([]string{kind + " " + match[2]}, members...)
	}
	if len(members) == 0 {
		return entity
	}
	return strings.Join(members, " in ")
}

// gitopsPreservedValue renders one side's value for a change.
func gitopsPreservedValue(value *string, masked bool) string {
	switch {
	case value != nil:
		return *value
	case masked:
		return "(masked)"
	}
	return "-"
}

// gitopsPreservedGroupLine renders one preview line. Values read in the
// direction the decision moves them: keeping Git, from Ankra's to Git's;
// keeping Ankra's, from Git's to Ankra's.
func gitopsPreservedGroupLine(group preservedChangeGroup, resolution string) string {
	if group.entity != "" {
		keys := "1 key"
		if len(group.changes) != 1 {
			keys = fmt.Sprintf("%d keys", len(group.changes))
		}
		label := gitopsPreservedEntityLabel(group.entity)
		if label == group.entity {
			return fmt.Sprintf("    - %s, as a whole (%s)", label, keys)
		}
		return fmt.Sprintf("    - the whole %s (%s)", label, keys)
	}
	change := group.changes[0]
	switch {
	case change.Change == client.GitopsPreservedChangeChanged && (change.GitValue != nil || change.AnkraValue != nil):
		from, to := change.GitValue, change.AnkraValue
		if resolution == client.GitopsPreservedResolutionGit {
			from, to = to, from
		}
		return fmt.Sprintf("    - %s: %s -> %s", change.Path, gitopsPreservedValue(from, change.ValueMasked),
			gitopsPreservedValue(to, change.ValueMasked))
	case change.GitValue != nil:
		return fmt.Sprintf("    - %s: %s", change.Path, *change.GitValue)
	case change.AnkraValue != nil:
		return fmt.Sprintf("    - %s: %s", change.Path, *change.AnkraValue)
	case change.ValueMasked:
		return fmt.Sprintf("    - %s (value masked)", change.Path)
	}
	return "    - " + change.Path
}

// gitopsPreservedSectionHeadings says, per change kind, what the decision
// does to it.
func gitopsPreservedSectionHeadings(resolution string) map[string]string {
	if resolution == client.GitopsPreservedResolutionGit {
		return map[string]string{
			client.GitopsPreservedChangeAdded:     "Only in Ankra's version, so REMOVED from the cluster:",
			client.GitopsPreservedChangeRemoved:   "Only in Git's version, so ADDED to the cluster:",
			client.GitopsPreservedChangeChanged:   "Values that go back to Git's (Ankra's -> Git's):",
			client.GitopsPreservedChangeReordered: "Lists put back in Git's order:",
		}
	}
	return map[string]string{
		client.GitopsPreservedChangeRemoved:   "Only in Git's version, so REMOVED from the file in Git:",
		client.GitopsPreservedChangeAdded:     "Only in Ankra's version, so ADDED to the file in Git:",
		client.GitopsPreservedChangeChanged:   "Values in Git replaced by Ankra's (Git's -> Ankra's):",
		client.GitopsPreservedChangeReordered: "Lists put in Ankra's order in Git:",
	}
}

// gitopsPreservedSectionOrder lists the change kinds most destructive first.
func gitopsPreservedSectionOrder(resolution string) []string {
	if resolution == client.GitopsPreservedResolutionGit {
		return []string{client.GitopsPreservedChangeAdded, client.GitopsPreservedChangeRemoved,
			client.GitopsPreservedChangeChanged, client.GitopsPreservedChangeReordered}
	}
	return []string{client.GitopsPreservedChangeRemoved, client.GitopsPreservedChangeAdded,
		client.GitopsPreservedChangeChanged, client.GitopsPreservedChangeReordered}
}

// gitopsPreservedPreview is what a decision discards, from the platform's
// structural difference, worded for the decision being made.
func gitopsPreservedPreview(clusterName string, listing *client.GitopsPreservedPathList,
	entry client.GitopsPreservedPath, resolution string) string {
	var out strings.Builder
	_, _ = fmt.Fprintf(&out, "Deciding %s on cluster '%s': keep %s version.\n", entry.Path, clusterName,
		gitopsPreservedKeptSide(resolution))
	if resolution == client.GitopsPreservedResolutionGit {
		commit := ""
		if entry.HeadCommitSHA != nil && *entry.HeadCommitSHA != "" {
			commit = " (commit " + *entry.HeadCommitSHA + ")"
		}
		_, _ = fmt.Fprintf(&out, "Ankra imports the file as Git holds it%s, and the cluster changes to match it.\n",
			commit)
	} else {
		_, _ = fmt.Fprintln(&out, "Ankra writes its own version of the file into Git, over the edit made there; "+
			"the cluster keeps running what Ankra's version declares.")
	}
	if entry.ImportRefusedReason != nil && *entry.ImportRefusedReason != "" {
		_, _ = fmt.Fprintf(&out, "The last decision to keep Git's version of this content was not imported: %s\n",
			*entry.ImportRefusedReason)
	}

	diff := entry.StructuralDiff
	_, _ = fmt.Fprintln(&out)
	switch diff.Status {
	case client.GitopsPreservedDiffComputed:
	case client.GitopsPreservedDiffFormattingOnly:
		_, _ = fmt.Fprintln(&out, "The two versions hold the same keys and values; they differ only in how values "+
			"are written (quoting, a YAML 1.1 boolean), so neither choice changes a setting.")
	default:
		_, _ = fmt.Fprintf(&out, "The platform has no key-by-key difference for this file (%s), so this cannot "+
			"say what the choice discards. Compare both versions before deciding.\n", gitopsPreservedDiffStatusLabel(diff.Status))
	}
	if diff.Status == client.GitopsPreservedDiffComputed {
		if diff.Truncated || diff.Total > len(diff.Changes) {
			_, _ = fmt.Fprintf(&out, "Where the two versions differ: %d differences. The platform lists the first %d; "+
				"the other %d are not shown here, so review the file in Git before deciding.\n",
				diff.Total, len(diff.Changes), diff.Total-len(diff.Changes))
		} else {
			_, _ = fmt.Fprintf(&out, "Where the two versions differ: %s.\n", gitopsPreservedCountLabel(diff.Total, "difference"))
		}
		groups := groupGitopsPreservedChanges(diff.Changes)
		headings := gitopsPreservedSectionHeadings(resolution)
		for _, kind := range gitopsPreservedSectionOrder(resolution) {
			var lines []string
			for _, group := range groups {
				if group.change == kind {
					lines = append(lines, gitopsPreservedGroupLine(group, resolution))
				}
			}
			if len(lines) == 0 {
				continue
			}
			_, _ = fmt.Fprintf(&out, "  %s\n%s\n", headings[kind], strings.Join(lines, "\n"))
		}
		for _, group := range groups {
			if _, known := headings[group.change]; !known {
				_, _ = fmt.Fprintf(&out, "  Other (%s):\n%s\n", group.change, gitopsPreservedGroupLine(group, resolution))
			}
		}
		if warning := gitopsPreservedEncryptedPathsWarning(groups, resolution); warning != "" {
			_, _ = fmt.Fprintf(&out, "\n%s\n", warning)
		}
	}
	if note := gitopsPreservedValuesNote(listing.StructuralDiffValues, diff); note != "" {
		_, _ = fmt.Fprintf(&out, "\n%s\n", note)
	}
	if resolution == client.GitopsPreservedResolutionGit {
		if others := gitopsPreservedOtherUndecided(listing, entry.Path); others > 0 {
			verb := "await"
			if others == 1 {
				verb = "awaits"
			}
			_, _ = fmt.Fprintf(&out, "\nThe import waits until every preserved path on this cluster has a decision: "+
				"%s besides this one still %s one.\n", gitopsPreservedCountLabel(others, "file"), verb)
		}
	}
	return out.String()
}

// gitopsPreservedEncryptedPathsWarning warns before keeping Git's version
// drops encrypted_paths entries that only Ankra's version declares on a
// member both versions keep (PLA-914): if Git still holds those values
// SOPS-encrypted, Ankra would stop decrypting them and deploy the ciphertext.
// A member removed as a whole takes its entries with it, so it is not counted.
func gitopsPreservedEncryptedPathsWarning(groups []preservedChangeGroup, resolution string) string {
	if resolution != client.GitopsPreservedResolutionGit {
		return ""
	}
	dropped := 0
	for _, group := range groups {
		if group.entity == "" && group.change == client.GitopsPreservedChangeAdded &&
			strings.Contains(group.changes[0].Path, "encrypted_paths") {
			dropped++
		}
	}
	if dropped == 0 {
		return ""
	}
	return fmt.Sprintf("Warning: Git's version does not declare %s that Ankra's does (listed above). If Git holds "+
		"those values SOPS-encrypted, Ankra stops decrypting them and deploys the ciphertext as the value. Add the "+
		"entries in Git first, or keep Ankra's version.", gitopsPreservedCountLabel(dropped, "encrypted_paths entry"))
}

// gitopsPreservedValuesNote says why values are missing from a difference.
func gitopsPreservedValuesNote(diffValues string, diff client.GitopsPreservedPathDiff) string {
	if diff.ValuesWithheldReason != nil && *diff.ValuesWithheldReason != "" {
		reason := gitopsPreservedRedactionLabels[*diff.ValuesWithheldReason]
		if reason == "" {
			reason = *diff.ValuesWithheldReason
		}
		return "No value of this file is shown to anyone, because " + reason + "."
	}
	masked := false
	for _, change := range diff.Changes {
		if change.ValueMasked {
			masked = true
			break
		}
	}
	switch diffValues {
	case client.GitopsPreservedDiffValuesPermissionRequired:
		return "Values are masked: seeing them needs permission to reveal secrets on the cluster " +
			"(kubernetes.secrets_reveal)."
	case client.GitopsPreservedDiffValuesUnavailable:
		return "Values are masked: the platform could not check whether you may see them."
	}
	if masked {
		return "Values printed (masked) may be sensitive, so the platform does not show them."
	}
	return ""
}

// gitopsPreservedOtherUndecided counts the listed files other than path that
// await a decision.
func gitopsPreservedOtherUndecided(listing *client.GitopsPreservedPathList, path string) int {
	others := 0
	for _, entry := range listing.PreservedPaths {
		if entry.Path != path && (entry.ResolutionChoice == nil || *entry.ResolutionChoice == "") {
			others++
		}
	}
	return others + max(0, listing.Total-len(listing.PreservedPaths))
}

func gitopsPreservedCountLabel(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "entry") {
		return fmt.Sprintf("%d %sies", count, strings.TrimSuffix(noun, "y"))
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// gitopsPreservedDiffStatusLabel words a difference that is not computed.
func gitopsPreservedDiffStatusLabel(status string) string {
	switch status {
	case client.GitopsPreservedDiffNotStructured:
		return "a version is not YAML or JSON the platform can compare key by key"
	case client.GitopsPreservedDiffNotRecorded:
		return "the platform has not recorded one for the current versions yet; the next sync records it"
	case "":
		return "this platform does not report one"
	}
	return status
}

// gitopsPreservedDifferencesCell summarises a file's difference for the table.
func gitopsPreservedDifferencesCell(diff client.GitopsPreservedPathDiff) string {
	switch diff.Status {
	case client.GitopsPreservedDiffComputed:
	case client.GitopsPreservedDiffFormattingOnly:
		return "formatting only"
	case client.GitopsPreservedDiffNotStructured:
		return "not comparable by key"
	case client.GitopsPreservedDiffNotRecorded:
		return "not recorded yet"
	default:
		return "-"
	}
	counts := map[string]int{}
	for _, change := range diff.Changes {
		counts[change.Change]++
	}
	var parts []string
	for _, kind := range []string{client.GitopsPreservedChangeAdded, client.GitopsPreservedChangeRemoved,
		client.GitopsPreservedChangeChanged, client.GitopsPreservedChangeReordered} {
		if counts[kind] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[kind], kind))
		}
	}
	cell := fmt.Sprintf("%d", diff.Total)
	if len(parts) > 0 {
		cell += ": " + strings.Join(parts, ", ")
	}
	if diff.Truncated || diff.Total > len(diff.Changes) {
		cell += fmt.Sprintf(" (of the first %d)", len(diff.Changes))
	}
	return cell
}

// gitopsPreservedContentCell says whether a file's content is listed.
func gitopsPreservedContentCell(entry client.GitopsPreservedPath) string {
	switch {
	case entry.ContentRedacted && entry.ContentRedactedReason != nil:
		return "withheld: " + strings.ReplaceAll(*entry.ContentRedactedReason, "_", " ")
	case entry.ContentRedacted:
		return "withheld"
	case entry.ContentOmitted:
		return "omitted (listing size cap)"
	}
	return "listed"
}

// gitopsPreservedDecisionCell words a file's decision for the table.
func gitopsPreservedDecisionCell(entry client.GitopsPreservedPath) string {
	switch {
	case entry.ResolutionChoice != nil && *entry.ResolutionChoice == client.GitopsPreservedResolutionGit:
		return "keep Git (next sync)"
	case entry.ResolutionChoice != nil && *entry.ResolutionChoice == client.GitopsPreservedResolutionCluster:
		return "keep Ankra (next sync)"
	case entry.ResolutionChoice != nil && *entry.ResolutionChoice != "":
		return *entry.ResolutionChoice
	case entry.ImportRefusedReason != nil && *entry.ImportRefusedReason != "":
		return "undecided (last keep-Git refused)"
	}
	return "undecided"
}

func gitopsPreservedTimeCell(timestamp *string) string {
	if timestamp == nil || *timestamp == "" {
		return "-"
	}
	return formatTimeAgo(*timestamp)
}

// printGitopsPreservedList renders the listing: a summary line, the table,
// and what a preserved path means with the commands that decide one.
func printGitopsPreservedList(out io.Writer, clusterName string, listing *client.GitopsPreservedPathList) {
	if listing.Total == 0 && len(listing.PreservedPaths) == 0 {
		_, _ = fmt.Fprintf(out, "No preserved GitOps paths on cluster '%s': Git and Ankra agree on every file "+
			"Ankra writes.\n", clusterName)
		return
	}
	undecided := 0
	for _, entry := range listing.PreservedPaths {
		if entry.ResolutionChoice == nil || *entry.ResolutionChoice == "" {
			undecided++
		}
	}
	_, _ = fmt.Fprintf(out, "Preserved GitOps paths on cluster '%s': %d, %d of those listed awaiting a decision.\n\n",
		clusterName, listing.Total, undecided)

	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Path", "Detected", "Decision", "Content", "Differences"})
	for _, entry := range listing.PreservedPaths {
		writer.AppendRow(table.Row{entry.Path, gitopsPreservedTimeCell(entry.DetectedAt),
			gitopsPreservedDecisionCell(entry), gitopsPreservedContentCell(entry),
			gitopsPreservedDifferencesCell(entry.StructuralDiff)})
	}
	writer.Render()

	if listing.Total > len(listing.PreservedPaths) {
		_, _ = fmt.Fprintf(out, "\nThe platform lists the first %d of %d preserved paths.\n",
			len(listing.PreservedPaths), listing.Total)
	}
	_, _ = fmt.Fprintln(out, "\nEach file was edited in Git, so Ankra keeps Git's version and its own later changes "+
		"to it do not reach Git until a person decides which version wins. Nothing is paused.")
	_, _ = fmt.Fprintln(out, gitopsPreservedResolveHint)
}

// printGitopsPreservedPath renders one file and its difference, key by key.
func printGitopsPreservedPath(out io.Writer, clusterName string, entry client.GitopsPreservedPath, diffValues string) {
	_, _ = fmt.Fprintf(out, "Preserved path %s on cluster '%s'\n", entry.Path, clusterName)
	timeLine := func(timestamp *string) string {
		if timestamp == nil || *timestamp == "" {
			return "-"
		}
		return timeWithAge(*timestamp)
	}
	_, _ = fmt.Fprintf(out, "  Detected:   %s\n", timeLine(entry.DetectedAt))
	_, _ = fmt.Fprintf(out, "  Last seen:  %s\n", timeLine(entry.LastSeenAt))
	if entry.HeadCommitSHA != nil && *entry.HeadCommitSHA != "" {
		_, _ = fmt.Fprintf(out, "  Git commit: %s\n", *entry.HeadCommitSHA)
	}
	decision := "awaiting a decision"
	if entry.ResolutionChoice != nil && *entry.ResolutionChoice != "" {
		decision = gitopsPreservedDecisionLabel(*entry.ResolutionChoice)
		if entry.ResolvedAt != nil && *entry.ResolvedAt != "" {
			decision += ", recorded " + timeWithAge(*entry.ResolvedAt)
		}
		decision += "; runs on the next sync"
	}
	_, _ = fmt.Fprintf(out, "  Decision:   %s\n", decision)
	content := "listed (-o json carries both versions)"
	switch {
	case entry.ContentRedacted:
		reason := "it may carry a secret"
		if entry.ContentRedactedReason != nil {
			if label := gitopsPreservedRedactionLabels[*entry.ContentRedactedReason]; label != "" {
				reason = label
			} else {
				reason = *entry.ContentRedactedReason
			}
		}
		content = "withheld, because " + reason
	case entry.ContentOmitted:
		content = "omitted from this listing (the platform caps the content one listing carries)"
	}
	_, _ = fmt.Fprintf(out, "  Content:    %s\n", content)
	if entry.ImportRefusedReason != nil && *entry.ImportRefusedReason != "" {
		refused := *entry.ImportRefusedReason
		if entry.ImportRefusedAt != nil && *entry.ImportRefusedAt != "" {
			refused += " (" + timeWithAge(*entry.ImportRefusedAt) + ")"
		}
		_, _ = fmt.Fprintf(out, "  Last keep-Git decision not imported: %s\n", refused)
	}

	diff := entry.StructuralDiff
	_, _ = fmt.Fprintln(out)
	switch diff.Status {
	case client.GitopsPreservedDiffComputed:
		heading := fmt.Sprintf("Differences, read from Git's version to Ankra's: %d", diff.Total)
		if diff.Truncated || diff.Total > len(diff.Changes) {
			heading += fmt.Sprintf(" (the platform lists the first %d)", len(diff.Changes))
		}
		_, _ = fmt.Fprintln(out, heading)
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"Path", "Change", "Git", "Ankra"})
		for _, change := range diff.Changes {
			gitMasked := change.ValueMasked && change.Change != client.GitopsPreservedChangeAdded
			ankraMasked := change.ValueMasked && change.Change != client.GitopsPreservedChangeRemoved
			writer.AppendRow(table.Row{change.Path, change.Change, gitopsPreservedValue(change.GitValue, gitMasked),
				gitopsPreservedValue(change.AnkraValue, ankraMasked)})
		}
		writer.Render()
	case client.GitopsPreservedDiffFormattingOnly:
		_, _ = fmt.Fprintln(out, "The two versions hold the same keys and values; they differ only in how values are "+
			"written (quoting, a YAML 1.1 boolean).")
	default:
		_, _ = fmt.Fprintf(out, "No key-by-key difference: %s.\n", gitopsPreservedDiffStatusLabel(diff.Status))
	}
	if note := gitopsPreservedValuesNote(diffValues, diff); note != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", note)
	}
	_, _ = fmt.Fprintf(out, "\nDecide it:  ankra cluster gitops preserved resolve %s --keep git|ankra\n", entry.Path)
}

// printGitopsPreservedResolution reports what the platform recorded and when
// it runs.
func printGitopsPreservedResolution(out io.Writer, result *client.GitopsPreservedPathResolution) {
	_, _ = fmt.Fprintln(out, result.Message)
	switch {
	case result.Resolution == client.GitopsPreservedResolutionNone:
	case result.SyncTriggered:
		_, _ = fmt.Fprintln(out, "A sync was triggered to run it. Follow it with 'ankra cluster gitops status'.")
	default:
		_, _ = fmt.Fprintln(out, "The sync could not be triggered now; the next periodic reconcile runs it. "+
			"Follow it with 'ankra cluster gitops status'.")
	}
}

func gitopsPreservedPathNotFoundError(cluster client.ClusterListItem, path string) error {
	return withExitCode(exitNotFound, fmt.Errorf(
		"no preserved path '%s' on cluster '%s'; a push may have settled it since. List them with "+
			"'ankra cluster gitops preserved list'", path, cluster.Name))
}

func gitopsPreservedAlreadyDecidedError(cluster client.ClusterListItem, path string, recorded string) error {
	decided := "the other decision"
	if recorded != "" {
		decided = "the decision to " + gitopsPreservedDecisionLabel(recorded)
	}
	return fmt.Errorf("%s on cluster '%s' already carries %s, which runs on the next sync; nothing was changed. To "+
		"decide it differently, withdraw that first with 'ankra cluster gitops preserved resolve %s --withdraw', "+
		"then decide again", path, cluster.Name, decided, path)
}

func gitopsPreservedSealedError(cluster client.ClusterListItem, path string) error {
	return fmt.Errorf("%s on cluster '%s' holds SOPS-encrypted content, so Ankra will not write its own version "+
		"over it: that would replace ciphertext in Git with plaintext. Nothing was changed. Keep Git's version "+
		"with --keep git, or change the file in Git", path, cluster.Name)
}

// gitopsPreservedError maps a refusal from the preserved-path routes onto
// what the user can act on, like gitopsConflictsError: a 403 is a missing
// permission (exit 7), a 404 with the platform's own detail is a cluster
// outside the organisation or a path no longer preserved (exit 3), the
// router's bare 404 is a platform without these routes (exit 1), and each
// 409 says which refusal it is and what to run next.
func gitopsPreservedError(apiError error, cluster client.ClusterListItem, path string, writing bool) error {
	operation := fmt.Sprintf("listing preserved GitOps paths on cluster '%s'", cluster.Name)
	if writing {
		operation = fmt.Sprintf("deciding preserved GitOps path '%s' on cluster '%s'", path, cluster.Name)
	}

	var denied *client.PermissionDeniedError
	var unexpected *client.UnexpectedResponseError
	hasStatus := errors.As(apiError, &unexpected)
	if errors.As(apiError, &denied) || (hasStatus && unexpected.StatusCode == http.StatusForbidden) {
		if writing {
			return withExitCode(exitForbidden, fmt.Errorf(
				"you need permission to change the configuration of cluster '%s' (clusters.write) to decide its "+
					"preserved GitOps paths. Ask an organisation admin for a role that has it", cluster.Name))
		}
		return withExitCode(exitForbidden, fmt.Errorf("%s: %w", operation, apiError))
	}
	if !hasStatus {
		return fmt.Errorf("%s: %w", operation, apiError)
	}
	switch unexpected.StatusCode {
	case http.StatusNotFound:
		switch unexpected.Detail {
		case "":
			return withExitCode(exitError, fmt.Errorf(
				"%s: the platform answered 404 without saying why, so it is not known whether the cluster is "+
					"outside this organisation or the platform does not offer these routes yet. "+
					"Check the selected organisation with 'ankra org current' and try again", operation))
		case routeAbsentDetail:
			return withExitCode(exitError, fmt.Errorf(
				"%s: this Ankra platform does not offer preserved GitOps paths to the CLI yet (the route answered "+
					"404). 'ankra cluster gitops status' still names them", operation))
		case gitopsPreservedClusterNotFoundDetail:
			return withExitCode(exitNotFound, fmt.Errorf(
				"cluster '%s' (%s) not found in this organisation. Check the selected organisation with "+
					"'ankra org current', or pick the cluster again with 'ankra cluster select'", cluster.Name, cluster.ID))
		case gitopsPreservedPathNotFoundDetail:
			return gitopsPreservedPathNotFoundError(cluster, path)
		}
	case http.StatusConflict:
		switch {
		case strings.HasPrefix(unexpected.Detail, gitopsPreservedChangedPrefix):
			return fmt.Errorf("%s: the file changed in Git or in Ankra after it was read, so the platform refused a "+
				"decision over versions nobody reviewed; nothing was changed. Run the same command again: it reads "+
				"the file again and shows the current differences", operation)
		case strings.HasPrefix(unexpected.Detail, gitopsPreservedAlreadyDecidedPrefix):
			return gitopsPreservedAlreadyDecidedError(cluster, path, "")
		case strings.HasPrefix(unexpected.Detail, gitopsPreservedSealedPrefix):
			return gitopsPreservedSealedError(cluster, path)
		}
	}
	return fmt.Errorf("%s: %w", operation, apiError)
}

// printGitopsPreservedHint names undecided preserved paths under a listing
// that found no merge conflicts, where they are the likely reason Git and the
// cluster still disagree. It reads the GitOps status, which counts them
// without carrying any file content, and prints nothing when that read fails
// or the platform does not report the count: it is a pointer, not a check.
func printGitopsPreservedHint(out io.Writer, clusterID string) {
	status, statusError := apiClient.GetClusterGitopsStatus(clusterID)
	if statusError != nil || status == nil || status.PreservedPathUndecidedCount == nil ||
		*status.PreservedPathUndecidedCount == 0 {
		return
	}
	count := *status.PreservedPathUndecidedCount
	verb, files := "await", "files"
	if count == 1 {
		verb, files = "awaits", "a file"
	}
	_, _ = fmt.Fprintf(out, "%s %s a decision: %s edited in Git that Ankra keeps instead of overwriting. These are "+
		"not merge conflicts and pause nothing.\nSee and decide them with 'ankra cluster gitops preserved list'.\n",
		gitopsPreservedCountLabel(count, "preserved path"), verb, files)
}

func init() {
	clusterGitopsPreservedResolveCmd.Flags().String("keep", "",
		"Which version to keep: 'git' (import Git's version; the cluster changes to match it) or 'ankra' "+
			"(write Ankra's version into Git; 'cluster' means the same)")
	clusterGitopsPreservedResolveCmd.Flags().Bool("withdraw", false,
		"Withdraw the recorded decision, so the file awaits one again (changes nothing in Git or on the cluster)")
	clusterGitopsPreservedResolveCmd.Flags().BoolP("yes", "y", false,
		"Skip the confirmation prompt (required when stdin is not a terminal)")
	registerStructuredOutputFlags(clusterGitopsPreservedListCmd, clusterGitopsPreservedShowCmd,
		clusterGitopsPreservedResolveCmd)
	clusterGitopsPreservedCmd.AddCommand(clusterGitopsPreservedListCmd, clusterGitopsPreservedShowCmd,
		clusterGitopsPreservedResolveCmd)
	clusterGitopsCmd.AddCommand(clusterGitopsPreservedCmd)
}

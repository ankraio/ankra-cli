package cmd

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// clusterStacksRestorePointsCmd is the per-stack half of the restore point
// surface: the artifact every other backup verb is a verb over.
var clusterStacksRestorePointsCmd = &cobra.Command{
	Use:     "restore-points",
	Aliases: []string{"restore-point"},
	Short:   "List, take, inspect, delete and restore a stack's restore points",
	Long: "A restore point is an immutable copy of a stack's data in a backup vault, " +
		"self-describing enough to be read without the cluster it came from. Backing " +
		"up creates one; restoring applies one.",
}

// backupRestorePointsCmd is the organisation-wide half, alongside
// `ankra backup vaults`.
var backupRestorePointsCmd = &cobra.Command{
	Use:     "restore-points",
	Aliases: []string{"restore-point"},
	Short:   "List the organisation's restore points across every cluster",
}

// backupRunWaitTimeout is the default --timeout for the writes that follow a
// data run. A capture or a restore moves whatever the stack holds, so the
// shared ten-minute default would report a failure for a run that is still
// succeeding on anything but a small volume.
const backupRunWaitTimeout = 2 * time.Hour

// restorePointIDPattern matches the canonical UUID form the API expects for a
// restore point id. Anything else is treated as a prefix to resolve, the way
// backupVaultIDPattern treats anything that is not a uuid as a name.
var restorePointIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolveRestorePointID accepts a full restore point id or an unambiguous
// prefix of one. Restore points have no names, so a prefix is the only short
// form there is; an ambiguous one is refused rather than resolved to whichever
// the listing happened to order first.
//
// The ambiguity check is only as wide as the page it searched: a prefix unique
// among the stack's most recent restore points can still collide with an older
// one that was never read. That is why the help says so and why the full id is
// always accepted - a short prefix is a convenience for the listing in front of
// you, not an addressing scheme.
func resolveRestorePointID(restorePoints APIClient, clusterID string, stackName string,
	reference string) (string, error) {
	if restorePointIDPattern.MatchString(reference) {
		return reference, nil
	}
	if strings.TrimSpace(reference) == "" {
		return "", withExitCode(exitUsage, fmt.Errorf("a restore point id is required"))
	}
	listing, listError := restorePoints.ListStackRestorePoints(clusterID, stackName,
		client.ListRestorePointsOptions{Limit: restorePointResolutionPageSize})
	if listError != nil {
		return "", backupLaneError("looking up restore point "+reference, listError)
	}
	matched := make([]string, 0, 2)
	for _, restorePoint := range listing.RestorePoints {
		if strings.HasPrefix(restorePoint.ID, reference) {
			matched = append(matched, restorePoint.ID)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return "", withExitCode(exitNotFound, fmt.Errorf(
			"no restore point on stack %q has an id starting with %q among the %d most recent - pass the full id",
			stackName, reference, restorePointResolutionPageSize))
	default:
		return "", withExitCode(exitUsage, fmt.Errorf(
			"%d restore points on stack %q have an id starting with %q - pass the full id (%s)",
			len(matched), stackName, reference, strings.Join(matched, ", ")))
	}
}

// restorePointResolutionPageSize is how far back an id prefix is resolved. It
// is the platform's maximum page, so the refusal can say exactly what was
// searched instead of implying the whole history was.
const restorePointResolutionPageSize = 200

func printRestorePointTable(out io.Writer, restorePoints []client.RestorePoint, withLocation bool) {
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	header := table.Row{"ID", "Status", "Trigger", "Size", "Assets", "Not carried", "Created"}
	if withLocation {
		header = table.Row{"ID", "Cluster", "Stack", "Status", "Trigger", "Size", "Assets", "Not carried", "Created"}
	}
	writer.AppendHeader(header)
	for _, restorePoint := range restorePoints {
		row := table.Row{
			restorePoint.ID, restorePoint.Status, restorePoint.Trigger,
			formatByteSize(restorePoint.TotalBytes), restorePoint.AssetCount,
			restorePointOmissions(restorePoint), formatTimeAgo(restorePoint.CreatedAt),
		}
		if withLocation {
			row = table.Row{
				restorePoint.ID, restorePointClusterName(restorePoint),
				strings.Join(restorePoint.StackNames, ", "),
				restorePoint.Status, restorePoint.Trigger,
				formatByteSize(restorePoint.TotalBytes), restorePoint.AssetCount,
				restorePointOmissions(restorePoint), formatTimeAgo(restorePoint.CreatedAt),
			}
		}
		writer.AppendRow(row)
	}
	writer.Render()
}

// restorePointOmissionsAreKnown reports whether the capture has reported what
// it left behind. Only a sealed manifest carries that answer: a restore point
// still being taken has not reported yet, and one whose capture failed never
// sealed a manifest at all, so an empty list in either state means "not known"
// rather than "nothing missing". Every other status is reached FROM complete -
// the lifecycle allows complete -> expiring -> expired and complete -> deleted
// and nothing else - so their omissions were reported before they got there.
func restorePointOmissionsAreKnown(restorePoint client.RestorePoint) bool {
	switch restorePoint.Status {
	case client.RestorePointStatusCreating, client.RestorePointStatusFailed:
		return false
	default:
		return true
	}
}

// restorePointOmissions renders the not-carried column. Printing 0 for a
// capture that has not reported would present an unknown as a clean bill - the
// exact silence the column exists to remove.
func restorePointOmissions(restorePoint client.RestorePoint) any {
	if !restorePointOmissionsAreKnown(restorePoint) && len(restorePoint.NotCarried) == 0 {
		return "unknown"
	}
	return len(restorePoint.NotCarried)
}

// restorePointClusterName names the source cluster. A restore point outlives
// the cluster it came from, so the name recorded on it is the only thing that
// still answers for a destroyed one.
func restorePointClusterName(restorePoint client.RestorePoint) string {
	if restorePoint.SourceClusterName != "" {
		return restorePoint.SourceClusterName
	}
	if restorePoint.SourceClusterID != nil && *restorePoint.SourceClusterID != "" {
		return *restorePoint.SourceClusterID
	}
	return "-"
}

// printNotCarried prints what a capture knowingly left behind. It is printed
// on every read, never folded away behind a flag: silence about un-carried
// data is what turns a backup product into a liability.
func printNotCarried(out io.Writer, omissions []client.RestorePointNotCarried) {
	if len(omissions) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "\nNot carried:")
	for _, omission := range omissions {
		_, _ = fmt.Fprintf(out, "  - %s %s: %s\n", omission.Kind, omission.Name, omission.Reason)
		if omission.Remedy != "" {
			_, _ = fmt.Fprintf(out, "    Remedy: %s\n", omission.Remedy)
		}
	}
}

func printWarnings(out io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "\nWarnings:")
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(out, "  - %s\n", warning)
	}
}

func printRestorePointDetail(out io.Writer, restorePoint *client.RestorePoint) {
	_, _ = fmt.Fprintln(out, "Restore Point:")
	_, _ = fmt.Fprintf(out, "  ID:            %s\n", restorePoint.ID)
	_, _ = fmt.Fprintf(out, "  Stack:         %s\n", strings.Join(restorePoint.StackNames, ", "))
	_, _ = fmt.Fprintf(out, "  Source:        %s\n", restorePointClusterName(*restorePoint))
	_, _ = fmt.Fprintf(out, "  Status:        %s\n", restorePoint.Status)
	_, _ = fmt.Fprintf(out, "  Trigger:       %s\n", restorePoint.Trigger)
	_, _ = fmt.Fprintf(out, "  Vault:         %s\n", restorePoint.BackupVaultID)
	_, _ = fmt.Fprintf(out, "  Size:          %s\n", formatByteSize(restorePoint.TotalBytes))
	_, _ = fmt.Fprintf(out, "  Assets:        %d\n", restorePoint.AssetCount)
	_, _ = fmt.Fprintf(out, "  Immutability:  %s\n", restorePoint.ImmutabilityMode)
	_, _ = fmt.Fprintf(out, "  Verification:  %s\n", restorePoint.VerificationStatus)
	_, _ = fmt.Fprintf(out, "  Object prefix: %s\n", restorePoint.ObjectPrefix)
	_, _ = fmt.Fprintf(out, "  Created:       %s\n", formatTimeAgo(restorePoint.CreatedAt))
	if restorePoint.CompletedAt != nil && *restorePoint.CompletedAt != "" {
		_, _ = fmt.Fprintf(out, "  Completed:     %s\n", formatTimeAgo(*restorePoint.CompletedAt))
	}
	if restorePoint.ExpiresAt != nil && *restorePoint.ExpiresAt != "" {
		_, _ = fmt.Fprintf(out, "  Expires:       %s\n", formatTimeAgo(*restorePoint.ExpiresAt))
	}
	if restorePoint.ErrorExcerpt != nil && *restorePoint.ErrorExcerpt != "" {
		_, _ = fmt.Fprintf(out, "  Error:         %s\n", *restorePoint.ErrorExcerpt)
	}

	if manifest := restorePoint.Manifest; manifest != nil {
		_, _ = fmt.Fprintln(out, "\nManifest:")
		_, _ = fmt.Fprintf(out, "  Schema version: %d\n", manifest.SchemaVersion)
		if manifest.Source.KubernetesVersion != "" {
			_, _ = fmt.Fprintf(out, "  Kubernetes:     %s (%s)\n",
				manifest.Source.KubernetesVersion, manifest.Source.Topology)
		}
		if len(manifest.Source.StorageClasses) > 0 {
			_, _ = fmt.Fprintf(out, "  Storage classes: %s\n", strings.Join(manifest.Source.StorageClasses, ", "))
		}
		_, _ = fmt.Fprintf(out, "  Volumes:        %s\n", formatByteSize(manifest.Sizes.VolumesBytes))
		_, _ = fmt.Fprintf(out, "  Databases:      %s\n", formatByteSize(manifest.Sizes.DatabasesBytes))
		printWarnings(out, manifest.Warnings)
	}

	if len(restorePoint.Assets) > 0 {
		_, _ = fmt.Fprintln(out, "\nAssets:")
		writer := table.NewWriter()
		writer.SetOutputMirror(out)
		writer.SetStyle(table.StyleRounded)
		writer.AppendHeader(table.Row{"ID", "Kind", "Engine", "Consistency", "Namespace", "Name", "Size"})
		for _, asset := range restorePoint.Assets {
			writer.AppendRow(table.Row{
				asset.ID, asset.Kind, asset.Engine, asset.Consistency,
				asset.Namespace, asset.Name, formatByteSize(asset.SizeBytes),
			})
		}
		writer.Render()
	}

	switch {
	case restorePointOmissionsAreKnown(*restorePoint) || len(restorePoint.NotCarried) > 0:
		printNotCarried(out, restorePoint.NotCarried)
	case restorePoint.Status == client.RestorePointStatusCreating:
		_, _ = fmt.Fprintln(out,
			"\nNot carried: not known yet - this restore point is still being taken.")
	default:
		_, _ = fmt.Fprintln(out,
			"\nNot carried: not known - this capture never sealed a manifest, so what it "+
				"would have left behind was never reported.")
	}

	if run := restorePoint.Run; run != nil {
		_, _ = fmt.Fprintln(out, "\nProducing run:")
		_, _ = fmt.Fprintf(out, "  ID:     %s\n", run.ID)
		_, _ = fmt.Fprintf(out, "  Kind:   %s\n", run.Kind)
		_, _ = fmt.Fprintf(out, "  Status: %s (%s)\n", run.Status, run.Phase)
		if run.ErrorExcerpt != nil && *run.ErrorExcerpt != "" {
			_, _ = fmt.Fprintf(out, "  Error:  %s\n", *run.ErrorExcerpt)
		}
		_, _ = fmt.Fprintf(out, "\nEvery step of it: 'ankra runs get %s'\n", run.ID)
	}
}

// listRestorePointFilters reads the filters the two listings share.
func listRestorePointFilters(cmd *cobra.Command) client.ListRestorePointsOptions {
	statuses, _ := cmd.Flags().GetStringSlice("status")
	triggers, _ := cmd.Flags().GetStringSlice("trigger")
	vault, _ := cmd.Flags().GetString("vault")
	cursor, _ := cmd.Flags().GetString("cursor")
	limit, _ := cmd.Flags().GetInt("limit")
	return client.ListRestorePointsOptions{
		Statuses: statuses, Triggers: triggers, VaultID: vault, Cursor: cursor, Limit: limit,
	}
}

// resolveVaultFilter turns a --vault name into the id the query takes. The
// listing prints no vault column, so a name is the only form a user has.
func resolveVaultFilter(options *client.ListRestorePointsOptions) error {
	if options.VaultID == "" {
		return nil
	}
	vaultID, resolveError := resolveBackupVaultID(apiClient, options.VaultID)
	if resolveError != nil {
		return resolveError
	}
	options.VaultID = vaultID
	return nil
}

var clusterStacksRestorePointsListCmd = &cobra.Command{
	Use:   "list <stack>",
	Short: "List a stack's restore points, newest first",
	Long: `List the restore points taken of a stack, newest first.

The listing carries what each restore point does NOT contain as well as what
it does: a backup listing that showed sizes and hid omissions would be exactly
the silence this is here to remove.

Examples:
  ankra cluster stacks restore-points list shop
  ankra cluster stacks restore-points list shop --status complete
  ankra cluster stacks restore-points list shop --trigger scheduled --limit 10`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		options := listRestorePointFilters(cmd)
		if filterError := resolveVaultFilter(&options); filterError != nil {
			return filterError
		}
		listing, listError := apiClient.ListStackRestorePoints(cluster.ID, stackName, options)
		if listError != nil {
			return backupLaneError("listing restore points", listError)
		}
		if rendered, renderError := renderStructured(cmd, listing); rendered || renderError != nil {
			return renderError
		}
		if len(listing.RestorePoints) == 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No restore points for stack '%s'. Take one with "+
				"'ankra cluster stacks restore-points create %s'.\n", stackName, stackName)
			return nil
		}
		printRestorePointTable(cmd.OutOrStdout(), listing.RestorePoints, false)
		printNextCursor(cmd.ErrOrStderr(), listing.NextCursor,
			"ankra cluster stacks restore-points list "+stackName)
		return nil
	},
}

var backupRestorePointsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the organisation's restore points",
	Long: `List every restore point the organisation holds, newest first, across
every cluster and stack.

Examples:
  ankra backup restore-points list
  ankra backup restore-points list --cluster production --status complete
  ankra backup restore-points list --stack shop`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		options := listRestorePointFilters(cmd)
		if filterError := resolveVaultFilter(&options); filterError != nil {
			return filterError
		}
		options.StackName, _ = cmd.Flags().GetString("stack")
		if clusterReference, _ := cmd.Flags().GetString("cluster"); clusterReference != "" {
			clusterID, resolveError := resolveClusterID(clusterReference)
			if resolveError != nil {
				return resolveError
			}
			options.ClusterID = clusterID
		}
		listing, listError := apiClient.ListOrganisationRestorePoints(options)
		if listError != nil {
			return backupLaneError("listing restore points", listError)
		}
		if rendered, renderError := renderStructured(cmd, listing); rendered || renderError != nil {
			return renderError
		}
		if len(listing.RestorePoints) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No restore points found. Protect a stack with "+
				"'ankra cluster stacks protect <stack> --vault <vault>'.")
			return nil
		}
		printRestorePointTable(cmd.OutOrStdout(), listing.RestorePoints, true)
		printNextCursor(cmd.ErrOrStderr(), listing.NextCursor, "ankra backup restore-points list")
		return nil
	},
}

var clusterStacksRestorePointsGetCmd = &cobra.Command{
	Use:   "get <stack> <restore-point-id>",
	Short: "Show a restore point's manifest, assets and producing run",
	Long: "Describe one restore point: the manifest it carries, every asset in it, " +
		"everything it does not carry, and the run that produced it. The id may be " +
		"an unambiguous prefix of the one the listing printed, resolved against the " +
		"stack's 200 most recent restore points, so it is checked for ambiguity only " +
		"within that window - the full id is what addresses an older restore point " +
		"with certainty.",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName, reference := args[0], args[1]
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		restorePointID, resolveError := resolveRestorePointID(apiClient, cluster.ID, stackName, reference)
		if resolveError != nil {
			return resolveError
		}
		restorePoint, getError := apiClient.GetStackRestorePoint(cluster.ID, stackName, restorePointID)
		if getError != nil {
			return backupLaneError("getting restore point", getError)
		}
		if rendered, renderError := renderStructured(cmd, restorePoint); rendered || renderError != nil {
			return renderError
		}
		printRestorePointDetail(cmd.OutOrStdout(), restorePoint)
		return nil
	},
}

// buildSelection reads the selection flags shared by `restore-points create`
// and `protect`. A command whose selection flags were all left alone sends no
// selection at all, so the platform falls back to the stack's stored one
// rather than being told to widen to everything.
//
// Naming volumes is not a decision about databases. The platform tells an
// absent `databases` from an explicit one, so only --exclude-databases sets
// it: sending `databases: true` alongside --include-pvc would silently
// re-include the databases of a stack whose stored policy excludes them.
func buildSelection(cmd *cobra.Command) (*client.RestorePointSelection, error) {
	includeClaims, _ := cmd.Flags().GetStringArray("include-pvc")
	excludeDatabases, _ := cmd.Flags().GetBool("exclude-databases")
	confirmExclusion, _ := cmd.Flags().GetBool("confirm-exclude-databases")
	if len(includeClaims) == 0 && !excludeDatabases {
		return nil, nil
	}
	selection := &client.RestorePointSelection{PersistentVolumeClaims: includeClaims}
	if !excludeDatabases {
		return selection, nil
	}
	if !confirmExclusion {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"--exclude-databases also needs --confirm-exclude-databases: every restore point taken "+
				"under this selection carries no database contents, and that is not something to "+
				"discover during a restore"))
	}
	databasesExcluded := false
	selection.Databases = &databasesExcluded
	selection.ConfirmExcludeDatabases = true
	return selection, nil
}

// resolveSelectedVault turns --vault into the id the request carries. An
// empty flag stays empty: the platform then resolves the stack's own vault
// and, failing that, the organisation's single ready one.
func resolveSelectedVault(cmd *cobra.Command) (string, error) {
	reference, _ := cmd.Flags().GetString("vault")
	if strings.TrimSpace(reference) == "" {
		return "", nil
	}
	return resolveBackupVaultID(apiClient, reference)
}

var clusterStacksRestorePointsCreateCmd = &cobra.Command{
	Use:   "create <stack>",
	Short: "Take a restore point of a stack now",
	Long: `Take a restore point of a stack now.

The capture is dispatched by the platform, so the command answers with the
restore point in 'creating' and the run that will seal it. With --wait it
follows that run to completion and prints the sealed restore point.

The vault is resolved in order: --vault, then the stack's own backup policy,
then the organisation's single ready vault. With more than one ready vault and
nothing naming which, the platform refuses rather than choosing for you.

Examples:
  ankra cluster stacks restore-points create shop
  ankra cluster stacks restore-points create shop --vault production-backups --wait
  ankra cluster stacks restore-points create shop --include-pvc shop/data --note "before the 3.2 upgrade"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		format, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}
		vaultID, vaultError := resolveSelectedVault(cmd)
		if vaultError != nil {
			return vaultError
		}
		selection, selectionError := buildSelection(cmd)
		if selectionError != nil {
			return selectionError
		}
		note, _ := cmd.Flags().GetString("note")

		result, createError := apiClient.CreateStackRestorePoint(cluster.ID, stackName,
			client.CreateRestorePointRequest{VaultID: vaultID, Selection: selection, Note: note})
		if createError != nil {
			return backupLaneError("creating restore point", createError)
		}

		wait, waitFlagError := asyncWriteWaitFlag(cmd)
		if waitFlagError != nil {
			return waitFlagError
		}
		if !wait {
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Restore point %s is being taken of stack '%s'.\n",
				result.RestorePointID, stackName)
			printWarnings(cmd.OutOrStdout(), result.Warnings)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"\nWatch it with 'ankra runs get %s', or re-run with --wait to block until it is sealed.\n",
				result.RunID)
			return nil
		}

		requestContext, cancel, contextError := asyncWriteRequestContext(cmd)
		if contextError != nil {
			return contextError
		}
		defer cancel()
		printWarnings(cmd.ErrOrStderr(), result.Warnings)
		run, waitError := followRunToCompletion(requestContext, apiClient, result.RunID, cmd.ErrOrStderr())
		if waitError != nil {
			return asyncWriteError("creating restore point", true, waitError)
		}
		if outcomeError := runOutcomeError("creating restore point", run); outcomeError != nil {
			return outcomeError
		}
		sealed, getError := apiClient.GetStackRestorePoint(cluster.ID, stackName, result.RestorePointID)
		if getError != nil {
			return backupLaneError("reading the sealed restore point", getError)
		}
		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, sealed)
		}
		printRestorePointDetail(cmd.OutOrStdout(), sealed)
		return nil
	},
}

var clusterStacksRestorePointsDeleteCmd = &cobra.Command{
	Use:   "delete <stack> <restore-point-id>",
	Short: "Delete a restore point and sweep its objects from the vault",
	Long: `Delete a restore point.

The row goes either way, so a restore point whose cluster was destroyed is
never undeletable; the objects in the bucket are swept by an execution on the
source cluster, and when no sweep could be dispatched the command says why the
objects were left behind rather than understating the storage bill.

A restore point a backup, restore or clone is currently using is refused, and
so is one that is still being taken.

The id may be an unambiguous prefix of the one the listing printed, resolved
against the stack's 200 most recent restore points, so it is checked for
ambiguity only within that window; the full id is what addresses an older
restore point with certainty.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName, reference := args[0], args[1]
		yes, _ := cmd.Flags().GetBool("yes")
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		restorePointID, resolveError := resolveRestorePointID(apiClient, cluster.ID, stackName, reference)
		if resolveError != nil {
			return resolveError
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Delete restore point %s of stack %q? The data in it is gone for good. [y/N]: ",
				restorePointID, stackName), yes); confirmError != nil {
			return confirmError
		}
		result, deleteError := apiClient.DeleteStackRestorePoint(cluster.ID, stackName, restorePointID)
		if deleteError != nil {
			return backupLaneError("deleting restore point", deleteError)
		}
		if rendered, renderError := renderStructured(cmd, result); rendered || renderError != nil {
			return renderError
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Restore point %s deleted.\n", result.RestorePointID)
		if result.OperationID != nil && *result.OperationID != "" {
			_, _ = fmt.Fprintf(out, "Its objects are being swept from the vault by operation %s.\n",
				*result.OperationID)
			return nil
		}
		reason := result.ObjectsRetainedReason
		if reason == "" {
			reason = "no sweep could be dispatched"
		}
		_, _ = fmt.Fprintf(out,
			"Its objects were left in the bucket: %s. Remove them yourself to stop paying for them.\n", reason)
		return nil
	},
}

var clusterStacksRestorePointsRestoreCmd = &cobra.Command{
	Use:   "restore <stack> <restore-point-id>",
	Short: "Restore a restore point over the stack it was taken from",
	Long: `Restore a restore point over the stack it was taken from.

This destroys before it replaces. Typing the stack's own name is what gates
it - the sequence the platform will follow is printed from its answer, once
the restore has been accepted and before any of its steps run:

  1. Scale down the workloads that own the data.
  2. Remove the volumes the restore point replaces.
  3. Restore the restore point's assets onto the cluster.
  4. Scale the workloads back up over the restored data.

Confirmation is the typed stack name, not y/N; --yes skips it for
scripts. A restore point carrying a CloudNativePG or Percona database is
refused with the platform's own reason - the ordinary restore path would
report success over unchanged data, which is worse than refusing.

A stack whose data has changed since the restore point was taken is refused
unless --force. An inventory whose live database scan did not run counts as
drift, because "we could not read it" is not "it is unchanged".

The id may be an unambiguous prefix of the one the listing printed, resolved
against the stack's 200 most recent restore points, so it is checked for
ambiguity only within that window; the full id is what addresses an older
restore point with certainty.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName, reference := args[0], args[1]
		yes, _ := cmd.Flags().GetBool("yes")
		force, _ := cmd.Flags().GetBool("force")
		// Every flag is checked before the restore is dispatched: a
		// mistyped -o must not be discovered after the stack's volumes are
		// already gone.
		format, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}
		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		restorePointID, resolveError := resolveRestorePointID(apiClient, cluster.ID, stackName, reference)
		if resolveError != nil {
			return resolveError
		}
		if confirmError := confirmStackName(cmd, stackName, yes, fmt.Sprintf(
			"Restoring restore point %s over stack %q removes the stack's current volumes before it "+
				"writes the restore point's.", restorePointID, stackName)); confirmError != nil {
			return confirmError
		}
		result, restoreError := apiClient.RestoreStackRestorePoint(cluster.ID, stackName, restorePointID,
			client.RestoreRestorePointRequest{
				Mode: client.RestoreModeInPlace, Confirm: stackName, Force: force,
			})
		if restoreError != nil {
			return backupLaneError("restoring restore point", restoreError)
		}

		progress := cmd.OutOrStdout()
		if format != outputDefault {
			progress = cmd.ErrOrStderr()
		}
		_, _ = fmt.Fprintf(progress, "Restoring stack '%s' from restore point %s:\n", stackName, restorePointID)
		for position, step := range result.Sequence {
			_, _ = fmt.Fprintf(progress, "  %d. %s\n", position+1, step)
		}
		printWarnings(progress, result.Warnings)

		wait, waitFlagError := asyncWriteWaitFlag(cmd)
		if waitFlagError != nil {
			return waitFlagError
		}
		if !wait {
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"\nWatch it with 'ankra runs get %s', or re-run with --wait to block until it finishes.\n",
				result.RunID)
			return nil
		}
		requestContext, cancel, contextError := asyncWriteRequestContext(cmd)
		if contextError != nil {
			return contextError
		}
		defer cancel()
		run, waitError := followRunToCompletion(requestContext, apiClient, result.RunID, cmd.ErrOrStderr())
		if waitError != nil {
			return asyncWriteError("restoring restore point", true, waitError)
		}
		if outcomeError := runOutcomeError("restoring restore point", run); outcomeError != nil {
			return outcomeError
		}
		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, run)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nStack '%s' restored from restore point %s.\n",
			stackName, restorePointID)
		return nil
	},
}

// confirmStackName is the typed confirmation the platform's own destructive
// routes require: the operator retypes the stack's name, so a restore or an
// unprotect cannot be a mis-aimed y. The prompt goes to stderr, which keeps
// -o json parseable while still asking.
func confirmStackName(cmd *cobra.Command, stackName string, yes bool, consequence string) error {
	if yes {
		return nil
	}
	errorWriter := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(errorWriter, "%s\n", consequence)
	_, _ = fmt.Fprintf(errorWriter, "Type the stack name %q to continue: ", stackName)
	reader := bufio.NewReader(cmd.InOrStdin())
	line, readError := reader.ReadString('\n')
	if readError != nil && readError != io.EOF {
		return fmt.Errorf("read confirmation: %w", readError)
	}
	if strings.TrimSpace(line) == stackName {
		return nil
	}
	_, _ = fmt.Fprintln(errorWriter, "That is not the stack name; nothing was changed.")
	return errCancelled
}

// registerRestorePointFilterFlags adds the filters both listings share.
func registerRestorePointFilterFlags(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().StringSlice("status", nil,
			"Only restore points in these statuses (creating, complete, failed, expiring, expired, deleted)")
		command.Flags().StringSlice("trigger", nil,
			"Only restore points with these triggers (manual, scheduled, clone, migrate, pre_restore, verification)")
		command.Flags().String("vault", "", "Only restore points in this backup vault (name or id)")
		command.Flags().String("cursor", "", "Continue from a previous page's cursor")
		command.Flags().Int("limit", 0, "Page size (server default, maximum 200)")
	}
}

// registerSelectionFlags adds the D12 selection flags shared by the two
// commands that can start a capture.
func registerSelectionFlags(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().StringArray("include-pvc", nil,
			"Persistent volume claim to carry, as namespace/name (repeatable). Volumes are carried only where named")
		command.Flags().Bool("exclude-databases", false,
			"Leave this stack's database contents out of the capture")
		command.Flags().Bool("confirm-exclude-databases", false,
			"Acknowledge that --exclude-databases means no database contents are carried")
	}
}

func init() {
	registerRestorePointFilterFlags(clusterStacksRestorePointsListCmd, backupRestorePointsListCmd)
	backupRestorePointsListCmd.Flags().String("cluster", "", "Only restore points taken from this cluster (name or id)")
	backupRestorePointsListCmd.Flags().String("stack", "", "Only restore points taken of this stack")

	registerSelectionFlags(clusterStacksRestorePointsCreateCmd)
	clusterStacksRestorePointsCreateCmd.Flags().String("vault", "",
		"Backup vault to write to (name or id; default: the stack's own, then the organisation's single ready vault)")
	clusterStacksRestorePointsCreateCmd.Flags().String("note", "", "Why this restore point was taken")
	registerAsyncWriteFlagsWithTimeout(clusterStacksRestorePointsCreateCmd, backupRunWaitTimeout)

	clusterStacksRestorePointsDeleteCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")

	clusterStacksRestorePointsRestoreCmd.Flags().Bool("yes", false, "Skip the typed stack-name confirmation")
	clusterStacksRestorePointsRestoreCmd.Flags().Bool("force",
		false, "Restore even though the stack's data has changed since the restore point was taken")
	registerAsyncWriteFlagsWithTimeout(clusterStacksRestorePointsRestoreCmd, backupRunWaitTimeout)

	registerStructuredOutputFlags(
		clusterStacksRestorePointsListCmd, clusterStacksRestorePointsGetCmd,
		clusterStacksRestorePointsCreateCmd, clusterStacksRestorePointsDeleteCmd,
		clusterStacksRestorePointsRestoreCmd, backupRestorePointsListCmd)

	clusterStacksRestorePointsCmd.AddCommand(clusterStacksRestorePointsListCmd)
	clusterStacksRestorePointsCmd.AddCommand(clusterStacksRestorePointsGetCmd)
	clusterStacksRestorePointsCmd.AddCommand(clusterStacksRestorePointsCreateCmd)
	clusterStacksRestorePointsCmd.AddCommand(clusterStacksRestorePointsDeleteCmd)
	clusterStacksRestorePointsCmd.AddCommand(clusterStacksRestorePointsRestoreCmd)
	clusterStacksCmd.AddCommand(clusterStacksRestorePointsCmd)

	backupRestorePointsCmd.AddCommand(backupRestorePointsListCmd)
	backupCmd.AddCommand(backupRestorePointsCmd)
}

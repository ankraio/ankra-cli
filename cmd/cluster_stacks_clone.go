package cmd

// Cloning a stack to another cluster, with or without its data (epic
// ankra-0xsdd, beads ankra-0xsdd.9 and ankra-0xsdd.54).
//
// Cloning with data is one sentence: take a restore point on the source,
// restore it on the target - fresh by default, or the newest complete one
// the stack already has with --from latest. It is not a second route: it is
// the clone this command has always sent, with a data block on it, so a
// clone without --with-data puts exactly the bytes on the wire it used to.
//
// The data half never deploys at clone time. The restore has to land before
// the workloads come up, so the cloned stack is a draft the platform holds,
// and --deploy plans a deploy step that parks on `awaiting_deploy` until a
// lane dispatches it. Saying so plainly here is the whole point: a command
// that printed "cloned" and left the operator to discover that the copy is
// empty would be worse than one that refused.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// cloneRequestTimeout bounds the clone call itself. The data half validates
// the target - agents, storage classes, vault, quota - before it writes
// anything, so it is slower than a configuration clone but still a request
// rather than a run.
const cloneRequestTimeout = 180 * time.Second

// cloneDataFlags are the flags that only mean something on a clone that
// carries data. Naming one without --with-data is refused rather than
// ignored: a --vault or an --include-pvc that quietly did nothing would send
// somebody away believing they had chosen where the data went.
var cloneDataFlags = []string{
	"from", "vault", "include-pvc", "exclude-databases",
	"confirm-exclude-databases", "protect-source", "idempotency-key",
}

var clusterStacksCloneCmd = &cobra.Command{
	Use:   "clone <stack_name> --to <target_cluster>",
	Short: "Clone a stack to another cluster, optionally carrying its data",
	Long: `Clone a stack from the current cluster to a target cluster.

The cloned stack is created as a draft on the target so it can be reviewed
before it is deployed. Encrypted values are stripped during cloning and must
be reconfigured on the target.

With --with-data the clone also carries the stack's data: a restore point is
taken on the source and restored onto the target by a run the command names.
The data moves after the clone, not during it, so the copy is a draft until
the restore has landed - a with-data clone never deploys at clone time, and
--deploy plans the deploy step rather than running it.

Data flags need --with-data beside them. Databases travel by default and
volumes only where named with --include-pvc namespace/name; --exclude-databases
needs --confirm-exclude-databases. Naming any selection flag REPLACES the
stack's stored backup selection for this clone rather than narrowing it, so
name the volumes to keep alongside --exclude-databases; leave every selection
flag off and the stored selection decides.

The vault resolves in order: --vault, the stack's own backup policy, then the
organisation's single ready vault. With --from latest the restore point is
read from the vault that holds it, so --vault is not accepted there.

Carrying volume data keeps the stack, Helm release and namespace names, so the
platform refuses --name for a clone whose plan holds volumes (a database-only
clone may still be renamed), and a target that already has a stack of this
name is refused rather than suffixed around.

Examples:
  ankra cluster stacks clone shop --to staging
  ankra cluster stacks clone shop --to staging --with-data --wait
  ankra cluster stacks clone shop --to staging --with-data --from latest
  ankra cluster stacks clone shop --to staging --with-data --include-pvc shop/data --protect-source`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		stackName := args[0]
		targetCluster, _ := cmd.Flags().GetString("to")
		newName, _ := cmd.Flags().GetString("name")
		includeConfig, _ := cmd.Flags().GetBool("include-config")
		withData, _ := cmd.Flags().GetBool("with-data")
		deploy, _ := cmd.Flags().GetBool("deploy")

		if targetCluster == "" {
			return withExitCode(exitUsage,
				fmt.Errorf("--to flag is required: specify the target cluster name or ID"))
		}
		format, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}
		wait, waitFlagError := asyncWriteWaitFlag(cmd)
		if waitFlagError != nil {
			return waitFlagError
		}
		dataRequest, dataError := buildCloneDataRequest(cmd, withData, wait)
		if dataError != nil {
			return dataError
		}

		sourceCluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}
		targetClusterID, resolveError := resolveClusterID(targetCluster)
		if resolveError != nil {
			return fmt.Errorf("resolving target cluster: %w", resolveError)
		}
		if sourceCluster.ID == targetClusterID {
			return withExitCode(exitUsage, fmt.Errorf("cannot clone a stack to the same cluster"))
		}

		cloneRequest := client.CloneStackToClusterRequest{
			SourceClusterID:            sourceCluster.ID,
			StackName:                  stackName,
			NewStackName:               newName,
			IncludeAddonConfigurations: includeConfig,
			DeployAfterClone:           deploy,
			IncludeData:                dataRequest.IncludeData,
			DataCloneMode:              dataRequest.DataCloneMode,
			BackupVaultID:              dataRequest.BackupVaultID,
			DataSelection:              dataRequest.DataSelection,
			ProtectSource:              dataRequest.ProtectSource,
			IdempotencyKey:             dataRequest.IdempotencyKey,
		}

		if format == outputDefault {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Cloning stack '%s' to cluster '%s'...\n",
				stackName, targetCluster)
		}
		describeCloneSelection(cmd.ErrOrStderr(), cloneRequest.DataSelection)

		requestContext, cancelRequest := context.WithTimeout(context.Background(), cloneRequestTimeout)
		defer cancelRequest()
		result, cloneError := apiClient.CloneStackToCluster(requestContext, targetClusterID, cloneRequest)
		if cloneError != nil {
			return backupLaneError("cloning stack", cloneError)
		}

		dataCloneRunID := ""
		if result.DataCloneRunID != nil {
			dataCloneRunID = *result.DataCloneRunID
		}
		// A clone that asked for data and came back without a run carried
		// none: the target holds a configuration copy shaped exactly like the
		// one nobody asked for. The platform refuses that case on its own
		// side; a CLI talking to a build that predates clone-with-data has to
		// say so itself rather than report the copy as complete.
		if cloneRequest.IncludeData && dataCloneRunID == "" {
			if format != outputDefault {
				if encodeError := encodeStructured(cmd.OutOrStdout(), format, result); encodeError != nil {
					return encodeError
				}
			} else {
				printCloneResult(cmd.ErrOrStderr(), result)
			}
			return fmt.Errorf("the clone carried no data: the platform accepted it and named no data " +
				"clone run, which is how a platform without clone-with-data answers. The stack's " +
				"configuration is on the target as a draft; its data is not")
		}

		if !wait {
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, result)
			}
			printCloneResult(cmd.OutOrStdout(), result)
			return nil
		}

		return waitForCloneRun(cmd, format, result, dataCloneRunID)
	},
}

// waitForCloneRun follows the clone's data run and reports how it settled.
// The clone itself is printed to stderr first so the operator has the draft
// id and the omissions while the wait runs, and stdout stays parseable.
func waitForCloneRun(cmd *cobra.Command, format outputFormat,
	result *client.CloneStackToClusterResult, dataCloneRunID string) error {
	waitContext, cancelWait, contextError := asyncWriteRequestContext(cmd)
	if contextError != nil {
		return contextError
	}
	defer cancelWait()
	printCloneResult(cmd.ErrOrStderr(), result)
	run, followError := followRunUntil(waitContext, apiClient, dataCloneRunID,
		cmd.ErrOrStderr(), cloneRunHasSettled)
	if followError != nil {
		return asyncWriteError("cloning stack with its data", true, followError)
	}
	if format != outputDefault {
		return encodeStructured(cmd.OutOrStdout(), format, cloneWaitResult{Clone: result, Run: run})
	}
	return reportSettledCloneRun(cmd.OutOrStdout(), run)
}

// cloneWaitResult is what -o json|yaml answers with after --wait: the clone
// the platform accepted and the run as it settled. Both halves are carried
// because neither answers the question on its own - the clone says what the
// data was asked to cover, the run says whether it got there.
type cloneWaitResult struct {
	Clone *client.CloneStackToClusterResult `json:"clone" yaml:"clone"`
	Run   *client.Run                       `json:"run" yaml:"run"`
}

// cloneDataRequest is the data half of a clone as the flags resolved it.
type cloneDataRequest struct {
	IncludeData    bool
	DataCloneMode  string
	BackupVaultID  string
	DataSelection  *client.CloneDataSelection
	ProtectSource  bool
	IdempotencyKey string
}

// buildCloneDataRequest validates the data flags and resolves the ones that
// name platform objects. Everything it can refuse is refused before the
// clone reaches the platform, because a configuration clone that landed on
// the target is not undone by the data half failing afterwards.
func buildCloneDataRequest(cmd *cobra.Command, withData bool, wait bool) (cloneDataRequest, error) {
	if !withData {
		for _, flagName := range cloneDataFlags {
			if cmd.Flags().Changed(flagName) {
				return cloneDataRequest{}, withExitCode(exitUsage, fmt.Errorf(
					"--%s only applies to a clone that carries data: add --with-data, or drop the flag",
					flagName))
			}
		}
		if wait {
			return cloneDataRequest{}, withExitCode(exitUsage, fmt.Errorf(
				"--wait only applies to a clone that carries data: a configuration clone is complete "+
					"when the command returns, and the draft it created is deployed from the builder"))
		}
		return cloneDataRequest{}, nil
	}

	request := cloneDataRequest{IncludeData: true}
	mode, _ := cmd.Flags().GetString("from")
	switch mode {
	case "", client.CloneDataModeFresh:
		request.DataCloneMode = client.CloneDataModeFresh
	case client.CloneDataModeLatest:
		request.DataCloneMode = client.CloneDataModeLatest
	default:
		return cloneDataRequest{}, withExitCode(exitUsage, fmt.Errorf(
			"--from must be %q (take a restore point now) or %q (restore the newest complete one "+
				"the stack already has), got %q", client.CloneDataModeFresh, client.CloneDataModeLatest, mode))
	}

	selection, selectionError := buildCloneDataSelection(cmd)
	if selectionError != nil {
		return cloneDataRequest{}, selectionError
	}
	request.DataSelection = selection

	// A clone in `latest` mode reads the restore point it found, and a
	// restore point's objects live in the vault that took it, so the
	// platform uses that vault and never looks at the request's. A --vault
	// accepted here would be a flag that silently did nothing.
	if request.DataCloneMode == client.CloneDataModeLatest && cmd.Flags().Changed("vault") {
		return cloneDataRequest{}, withExitCode(exitUsage, fmt.Errorf(
			"--vault does not apply with --from latest: the clone restores the stack's newest complete "+
				"restore point from the vault that holds it. Drop --vault, or use --from fresh to take a "+
				"new restore point in a vault you choose"))
	}
	if request.DataCloneMode != client.CloneDataModeLatest {
		vaultID, vaultError := resolveSelectedVault(cmd)
		if vaultError != nil {
			return cloneDataRequest{}, vaultError
		}
		request.BackupVaultID = vaultID
	}
	request.ProtectSource, _ = cmd.Flags().GetBool("protect-source")
	request.IdempotencyKey, _ = cmd.Flags().GetString("idempotency-key")
	return request, nil
}

// buildCloneDataSelection reads the selection flags. A clone whose selection
// flags were all left alone sends no selection at all, so the platform falls
// back to the stack's stored one rather than being told to widen to
// everything - and naming a volume is not a decision about databases, so
// only --exclude-databases sets that field.
//
// A selection the request DOES carry replaces the stack's stored one whole:
// the platform resolves "the request, then the stored policy, then the
// default" and does not merge the two field by field. So --exclude-databases
// on its own is a clone of the databases-excluded, no-volumes selection, not
// the stored selection minus its databases, and describeCloneSelection says
// so rather than leaving the difference to be found on the target.
func buildCloneDataSelection(cmd *cobra.Command) (*client.CloneDataSelection, error) {
	includeClaims, _ := cmd.Flags().GetStringArray("include-pvc")
	excludeDatabases, _ := cmd.Flags().GetBool("exclude-databases")
	confirmExclusion, _ := cmd.Flags().GetBool("confirm-exclude-databases")
	if len(includeClaims) == 0 && !excludeDatabases {
		return nil, nil
	}
	for _, claim := range includeClaims {
		if validationError := validatePersistentVolumeClaimReference(claim); validationError != nil {
			return nil, validationError
		}
	}
	selection := &client.CloneDataSelection{PersistentVolumeClaims: includeClaims}
	if !excludeDatabases {
		return selection, nil
	}
	if !confirmExclusion {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"--exclude-databases also needs --confirm-exclude-databases: the clone then carries no "+
				"database contents, and that is not something to discover on the target"))
	}
	databasesExcluded := false
	selection.Databases = &databasesExcluded
	return selection, nil
}

// validatePersistentVolumeClaimReference refuses an --include-pvc value that
// is not namespace/name. The platform matches claims on exactly that
// spelling, so a malformed one names no volume, travels as a selection
// covering nothing, and is discovered as an empty target - the silence this
// lane exists to remove, and a lot cheaper to catch before a restore point
// is taken.
func validatePersistentVolumeClaimReference(claim string) error {
	namespace, name, separatorFound := strings.Cut(claim, "/")
	if separatorFound && namespace != "" && name != "" && !strings.Contains(name, "/") {
		return nil
	}
	return withExitCode(exitUsage, fmt.Errorf(
		"--include-pvc %q is not namespace/name: name the volume the way "+
			"'ankra cluster stacks data list' prints it, for example shop/data", claim))
}

// describeCloneSelection says what a carried selection covers, because the
// platform applies it instead of the stack's stored selection rather than
// alongside it. A stack protected with volumes named in its policy and
// cloned with --exclude-databases alone carries neither its databases nor
// those volumes, and nothing else in the output would say so.
func describeCloneSelection(out io.Writer, selection *client.CloneDataSelection) {
	if selection == nil {
		return
	}
	databases := "databases as the stack's own selection has them"
	if selection.Databases != nil && !*selection.Databases {
		databases = "no databases"
	}
	volumes := "no volumes"
	if len(selection.PersistentVolumeClaims) > 0 {
		volumes = "volumes " + strings.Join(selection.PersistentVolumeClaims, ", ")
	}
	_, _ = fmt.Fprintf(out, "This clone carries %s and %s. A selection given on the command line "+
		"replaces the stack's stored backup selection for this clone rather than narrowing it.\n",
		databases, volumes)
}

// cloneRunHasSettled reports whether a clone's data run has stopped moving.
// `blocked` counts: the platform parks there when the target's agent has not
// connected or the deploy step has nothing to dispatch it, and it says which
// in the run's blocked reason. Neither clears without something outside this
// command happening, so waiting past it is waiting for nothing.
func cloneRunHasSettled(run *client.Run) bool {
	return client.IsTerminalRunStatus(run.Status) || run.Status == client.RunStatusBlocked
}

// reportSettledCloneRun prints how a followed clone run ended. A blocked run
// is not a failure - a clone onto a cluster whose agent has never connected
// parks by design - so it exits zero with the platform's own sentence, while
// a failed one exits non-zero carrying the platform's reason.
func reportSettledCloneRun(out io.Writer, run *client.Run) error {
	if run == nil {
		return nil
	}
	if run.Status == client.RunStatusBlocked {
		_, _ = fmt.Fprintf(out, "\nThe data clone is waiting: %s\n", cloneBlockedReason(run))
		_, _ = fmt.Fprintf(out, "Follow it with 'ankra runs get %s'.\n", run.ID)
		return nil
	}
	if outcomeError := runOutcomeError("cloning stack with its data", run); outcomeError != nil {
		return outcomeError
	}
	_, _ = fmt.Fprintf(out, "\nThe stack's data has been restored onto the target (run %s).\n", run.ID)
	return nil
}

// cloneBlockedReason is the platform's own wording for why a run parked,
// relayed verbatim. The two a clone produces are the ordinary ones -
// the target's agent has not connected yet, and the deploy step is planned
// but undispatched - and both are answers a person acts on.
func cloneBlockedReason(run *client.Run) string {
	if run.DataRun != nil && run.DataRun.BlockedReason != nil && *run.DataRun.BlockedReason != "" {
		return *run.DataRun.BlockedReason
	}
	if run.ErrorExcerpt != nil && *run.ErrorExcerpt != "" {
		return *run.ErrorExcerpt
	}
	return "the platform did not say why"
}

// printCloneResult renders a clone the platform accepted. The data half is
// printed omissions-first: what a copy does not carry is what changes the
// answer to whether it is a copy.
func printCloneResult(out io.Writer, result *client.CloneStackToClusterResult) {
	_, _ = fmt.Fprintf(out, "\nStack cloned successfully!\n")
	_, _ = fmt.Fprintf(out, "  Draft ID:    %s\n", result.DraftID)
	_, _ = fmt.Fprintf(out, "  Stack Name:  %s\n", result.StackName)
	_, _ = fmt.Fprintf(out, "  Addons:      %d\n", result.AddonsCloned)
	_, _ = fmt.Fprintf(out, "  Manifests:   %d\n", result.ManifestsCloned)
	if result.ApplicationsCloned > 0 {
		_, _ = fmt.Fprintf(out, "  Applications: %d\n", result.ApplicationsCloned)
	}
	printWarnings(out, result.Warnings)
	printCloneNeedsInput(out, result.NeedsInput)
	printCloneDataResult(out, result)

	if result.DataCloneRunID == nil || *result.DataCloneRunID == "" {
		_, _ = fmt.Fprintf(out,
			"\nThe stack has been created as a draft. Review and deploy it from the Ankra dashboard.\n")
		return
	}
	_, _ = fmt.Fprintf(out, "\nThe stack has been created as a draft and its data is being moved by "+
		"run %s. Follow it with 'ankra runs get %s', or re-run with --wait.\n",
		*result.DataCloneRunID, *result.DataCloneRunID)
}

// printCloneDataResult renders the data half of a clone, or nothing at all
// for a configuration-only one.
func printCloneDataResult(out io.Writer, result *client.CloneStackToClusterResult) {
	if result.DataCloneRunID == nil || *result.DataCloneRunID == "" {
		return
	}
	_, _ = fmt.Fprintf(out, "\nData:\n")
	_, _ = fmt.Fprintf(out, "  Run:            %s\n", *result.DataCloneRunID)
	if result.DataRestorePointID != nil && *result.DataRestorePointID != "" {
		_, _ = fmt.Fprintf(out, "  Restore point:  %s\n", *result.DataRestorePointID)
	}
	if len(result.DataWarnings) > 0 {
		_, _ = fmt.Fprintf(out, "\nWhat the data clone will not carry:\n")
		for _, warning := range result.DataWarnings {
			_, _ = fmt.Fprintf(out, "  - %s\n", warning)
		}
	}
	if len(result.DataAssets) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\nCarrying %d asset(s):\n", len(result.DataAssets))
	writer := table.NewWriter()
	writer.SetOutputMirror(out)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Kind", "Namespace", "Name", "Engine", "Size"})
	for _, asset := range result.DataAssets {
		engine := asset.Engine
		if asset.DatabaseEngine != "" {
			engine = asset.Engine + " (" + asset.DatabaseEngine + ")"
		}
		size := "-"
		if asset.SizeBytes > 0 {
			size = formatByteSize(asset.SizeBytes)
		}
		writer.AppendRow(table.Row{asset.Kind, asset.Namespace, asset.Name, engine, size})
	}
	writer.Render()
}

// printCloneNeedsInput names the cloned members whose stripped secrets have
// to be re-entered before the target stack can come up.
func printCloneNeedsInput(out io.Writer, needsInput []client.CloneNeedsInputItem) {
	if len(needsInput) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\nNeeds input before the stack can deploy:\n")
	for _, item := range needsInput {
		_, _ = fmt.Fprintf(out, "  - %s %s: %s\n", item.MemberKind, item.MemberName, item.Reason)
		if len(item.Paths) > 0 {
			_, _ = fmt.Fprintf(out, "    %s\n", strings.Join(item.Paths, ", "))
		}
	}
}

func init() {
	clusterStacksCloneCmd.Flags().StringP("to", "t", "", "Target cluster name or ID (required)")
	clusterStacksCloneCmd.Flags().StringP("name", "n", "", "New stack name (optional, defaults to original)")
	clusterStacksCloneCmd.Flags().Bool("include-config", true, "Include addon configurations")
	clusterStacksCloneCmd.Flags().Bool("with-data", false,
		"Carry the stack's data: take a restore point on the source and restore it onto the target")
	clusterStacksCloneCmd.Flags().String("from", client.CloneDataModeFresh,
		"Where the data comes from: 'fresh' takes a restore point now, 'latest' restores the newest complete one the stack already has")
	clusterStacksCloneCmd.Flags().String("vault", "",
		"Backup vault name or id for the restore point --from fresh takes (default: the stack's policy vault, then the organisation's single ready vault); not accepted with --from latest")
	clusterStacksCloneCmd.Flags().StringArray("include-pvc", nil,
		"Carry this volume as namespace/name (repeatable); naming any selection flag replaces the stack's stored backup selection for this clone")
	clusterStacksCloneCmd.Flags().Bool("exclude-databases", false,
		"Carry no database contents (needs --confirm-exclude-databases); name the volumes to keep with --include-pvc, since this replaces the stored selection")
	clusterStacksCloneCmd.Flags().Bool("confirm-exclude-databases", false,
		"Acknowledge that the clone carries no database contents")
	clusterStacksCloneCmd.Flags().Bool("protect-source", false,
		"Write a backup policy onto the source stack if it has none, so this restore point is the first of a series")
	clusterStacksCloneCmd.Flags().Bool("deploy", false,
		"Deploy the cloned stack on the target; with --with-data the deploy is planned and waits for the restore")
	clusterStacksCloneCmd.Flags().String("idempotency-key", "",
		"Reuse this key when retrying a with-data clone that may already have been accepted, so no second restore point is taken")
	_ = clusterStacksCloneCmd.MarkFlagRequired("to")

	registerAsyncWriteFlagsWithTimeout(clusterStacksCloneCmd, backupRunWaitTimeout)
	registerStructuredOutputFlags(clusterStacksCloneCmd)
	clusterStacksCmd.AddCommand(clusterStacksCloneCmd)
}

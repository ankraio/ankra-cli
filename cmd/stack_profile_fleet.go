package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The fleet side of stack profiles: reading where a profile is deployed and
// rolling a published version out to those clusters. Both used to need a
// shell loop, jq and yq around export-iac and cluster apply.

// stackProfileDeployment is one row of GET /stack-profiles/{id}/instantiations.
type stackProfileDeployment struct {
	ID              string                    `json:"id"`
	TargetClusterID string                    `json:"target_cluster_id"`
	ClusterName     string                    `json:"cluster_name"`
	StackName       string                    `json:"stack_name"`
	StackState      string                    `json:"stack_state"`
	Version         int                       `json:"version"`
	Outdated        bool                      `json:"outdated"`
	Deprecated      bool                      `json:"deprecated"`
	Parameters      []client.ParameterBinding `json:"parameters"`
	CreatedAt       string                    `json:"created_at"`
}

type stackProfileDeployments struct {
	Result         []stackProfileDeployment `json:"result"`
	CurrentVersion int                      `json:"current_version"`
}

func loadStackProfileDeployments(requestContext context.Context, profileID string) (json.RawMessage, *stackProfileDeployments, error) {
	payload, listError := apiClient.ListStackProfileInstantiations(requestContext, profileID)
	if listError != nil {
		return nil, nil, fmt.Errorf("listing stack profile deployments: %w", listError)
	}
	var deployments stackProfileDeployments
	if len(payload) > 0 {
		if unmarshalError := json.Unmarshal(payload, &deployments); unmarshalError != nil {
			return nil, nil, fmt.Errorf("parsing stack profile deployments: %w", unmarshalError)
		}
	}
	return payload, &deployments, nil
}

// stackProfileDeploymentStatus reads the platform's own outdated flag; the
// current version is quoted only when the payload carried one, so an older
// platform that omits it never shows a defaulted "v0" as if measured. A
// deprecated version outranks being behind: the version the row runs was
// withdrawn, which is the thing to act on.
func stackProfileDeploymentStatus(deployment stackProfileDeployment, currentVersion int) string {
	if deployment.Deprecated {
		return "deprecated"
	}
	if !deployment.Outdated {
		return "up to date"
	}
	if currentVersion <= 0 {
		return "update available"
	}
	return fmt.Sprintf("update available (v%d)", currentVersion)
}

// renderStackProfileDeployments prints the fleet table: one row per stack
// deployed from the profile, with whether it is behind the current version.
func renderStackProfileDeployments(out io.Writer, deployments *stackProfileDeployments, onlyOutdated bool) {
	rows := deployments.Result
	if onlyOutdated {
		rows = []stackProfileDeployment{}
		for _, deployment := range deployments.Result {
			if deployment.Outdated {
				rows = append(rows, deployment)
			}
		}
	}
	sort.SliceStable(rows, func(left int, right int) bool {
		if rows[left].ClusterName != rows[right].ClusterName {
			return rows[left].ClusterName < rows[right].ClusterName
		}
		return rows[left].StackName < rows[right].StackName
	})
	clusters := map[string]bool{}
	behind := 0
	onDeprecatedVersion := 0
	for _, deployment := range deployments.Result {
		clusters[deployment.TargetClusterID] = true
		if deployment.Outdated {
			behind++
		}
		if deployment.Deprecated {
			onDeprecatedVersion++
		}
	}
	drift := "all up to date"
	if behind > 0 {
		drift = fmt.Sprintf("%d behind", behind)
		if deployments.CurrentVersion > 0 {
			drift = fmt.Sprintf("%d behind v%d", behind, deployments.CurrentVersion)
		}
	}
	currentVersion := "Current version unknown"
	if deployments.CurrentVersion > 0 {
		currentVersion = fmt.Sprintf("Current version v%d", deployments.CurrentVersion)
	}
	_, _ = fmt.Fprintf(out, "%s  ·  %d %s across %d %s  ·  %s",
		currentVersion,
		len(deployments.Result), pluralise(len(deployments.Result), "deployment", "deployments"),
		len(clusters), pluralise(len(clusters), "cluster", "clusters"),
		drift)
	if onDeprecatedVersion > 0 {
		_, _ = fmt.Fprintf(out, "  ·  %d on a deprecated version", onDeprecatedVersion)
	}
	_, _ = fmt.Fprintln(out)
	if len(rows) == 0 {
		if onlyOutdated {
			_, _ = fmt.Fprintln(out, "No deployment is behind the current version.")
		} else {
			_, _ = fmt.Fprintln(out, "No stack has been deployed from this profile yet. Deploy one with 'ankra stack-profiles apply <profile> --cluster <name> --deploy'.")
		}
		return
	}
	fleetTable := table.NewWriter()
	fleetTable.SetOutputMirror(out)
	fleetTable.SetStyle(table.StyleRounded)
	fleetTable.AppendHeader(table.Row{"CLUSTER", "STACK", "STATE", "VERSION", "STATUS", "DEPLOYED"})
	for _, deployment := range rows {
		fleetTable.AppendRow(table.Row{
			deployment.ClusterName,
			deployment.StackName,
			deployment.StackState,
			fmt.Sprintf("v%d", deployment.Version),
			stackProfileDeploymentStatus(deployment, deployments.CurrentVersion),
			formatDeploymentTimestamp(deployment.CreatedAt),
		})
	}
	fleetTable.Render()
	if behind > 0 && !onlyOutdated {
		_, _ = fmt.Fprintf(out, "\nRoll the current version out with 'ankra stack-profiles rollout <profile> --all --outdated'.\n")
	}
}

func formatDeploymentTimestamp(raw string) string {
	if len(raw) >= 16 && strings.Contains(raw, "T") {
		return strings.Replace(raw[:16], "T", " ", 1)
	}
	if raw == "" {
		return "-"
	}
	return raw
}

// rolloutTarget is one (cluster, stack) pair a rollout writes to.
type rolloutTarget struct {
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	StackName   string `json:"stack_name"`
	FromVersion int    `json:"from_version"`
	// recordedBindings are the non-secret inputs the deployment was last
	// applied with; the platform keeps them on the deployment so an upgrade
	// carries them forward without retyping. Secrets are never recorded.
	recordedBindings []client.ParameterBinding
}

type rolloutOutcome struct {
	rolloutTarget
	ToVersion   int    `json:"to_version"`
	Status      string `json:"status"`
	Message     string `json:"message,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	JobCount    int    `json:"job_count,omitempty"`
	// Removes is, on a dry run, the members (kind/name) the version would
	// remove from this stack; RemovesUnknown says why that could not be read.
	Removes        []string `json:"removes,omitempty"`
	RemovesUnknown string   `json:"removes_unknown,omitempty"`
}

// rolloutFailureError summarises a rollout that had failures, naming the
// stacks it then left untouched so nobody reads "1 of 5 failed" as "4 done".
func rolloutFailureError(failed int, outcomes []rolloutOutcome, verb string) error {
	skipped := 0
	for _, outcome := range outcomes {
		if outcome.Status == "skipped" {
			skipped++
		}
	}
	message := fmt.Sprintf("%d of %d %s %s", failed, len(outcomes), pluralise(len(outcomes), "rollout", "rollouts"), verb)
	if skipped > 0 {
		message += fmt.Sprintf("; %d not attempted", skipped)
	}
	return errors.New(message)
}

// skippedAfterFailure marks a target the loop did not attempt because an
// earlier one failed.
func skippedAfterFailure(target rolloutTarget, version int) rolloutOutcome {
	return rolloutOutcome{rolloutTarget: target, ToVersion: version, Status: "skipped",
		Message: "not attempted: an earlier stack failed (pass --continue-on-error to keep going)"}
}

// resolveClusterReference turns a cluster name or ID into both, paging the
// cluster list the way resolveClusterID does.
func resolveClusterReference(nameOrID string) (string, string, error) {
	const pageSize = 100
	const maxPages = 50
	// The same shape test resolveClusterID uses: a 36-character, four-dash
	// value is tried as an id (exact match) and, failing that, still as a
	// name, so a cluster whose name happens to look like a uuid stays
	// reachable by name.
	isID := len(nameOrID) == 36 && strings.Count(nameOrID, "-") == 4
	for page := 1; page <= maxPages; page++ {
		response, listError := apiClient.ListClusters(page, pageSize)
		if listError != nil {
			return "", "", fmt.Errorf("listing clusters: %w", listError)
		}
		for _, cluster := range response.Result {
			if (isID && cluster.ID == nameOrID) || strings.EqualFold(cluster.Name, nameOrID) {
				return cluster.ID, cluster.Name, nil
			}
		}
		if response.Pagination.TotalPages <= page || len(response.Result) == 0 {
			break
		}
	}
	return "", "", fmt.Errorf("cluster %q not found", nameOrID)
}

// rolloutTargets picks the deployments a rollout writes to: every
// deployment of the profile with --all, otherwise the ones on the named
// clusters. A cluster that does not run the profile is refused rather than
// silently given a first deployment; that is what apply is for.
func rolloutTargets(deployments *stackProfileDeployments, clusterFlags []string, all bool, onlyOutdated bool) ([]rolloutTarget, error) {
	targets := []rolloutTarget{}
	seen := map[string]bool{}
	add := func(deployment stackProfileDeployment) {
		key := deployment.TargetClusterID + "/" + deployment.StackName
		if seen[key] {
			return
		}
		if onlyOutdated && !deployment.Outdated {
			return
		}
		seen[key] = true
		targets = append(targets, rolloutTarget{
			ClusterID:        deployment.TargetClusterID,
			ClusterName:      deployment.ClusterName,
			StackName:        deployment.StackName,
			FromVersion:      deployment.Version,
			recordedBindings: deployment.Parameters,
		})
	}
	if all {
		for _, deployment := range deployments.Result {
			add(deployment)
		}
		return targets, nil
	}
	for _, clusterFlag := range clusterFlags {
		clusterID, clusterName, resolveError := resolveClusterReference(clusterFlag)
		if resolveError != nil {
			return nil, resolveError
		}
		matched := false
		for _, deployment := range deployments.Result {
			if deployment.TargetClusterID == clusterID {
				add(deployment)
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("cluster %q has no stack deployed from this profile; deploy one first with 'ankra stack-profiles apply <profile> --cluster %s --deploy'", clusterName, clusterName)
		}
	}
	return targets, nil
}

// rolloutRequest is the exported version, re-addressed to one target: the
// document's metadata.name becomes the cluster, and its stack takes the
// name the deployment already uses so the platform updates that stack in
// place instead of creating a second one. The caller has already checked
// the export holds exactly one stack.
func rolloutRequest(base client.CreateImportClusterRequest, target rolloutTarget) client.CreateImportClusterRequest {
	request := base
	request.Name = target.ClusterName
	stacks := append([]client.Stack(nil), base.Spec.Stacks...)
	if target.StackName != "" {
		stacks[0].Name = target.StackName
	}
	request.Spec.Stacks = stacks
	return request
}

var stackProfilesRolloutCmd = &cobra.Command{
	Use:   "rollout [profile-id|profile-name]",
	Short: "Roll a published profile version out to the clusters already running it",
	Long: `Update the stacks deployed from a profile to a published version, in place.

For every target the platform replaces the contents of the stack that
deployment already runs with the chosen version - the same stack, so
Kubernetes performs a rolling update of the workloads and nothing is created
twice - and records the deployment at the new version, so 'deployments' and
the profile's fleet view read up to date. The non-secret inputs each
deployment was last applied with are carried forward; bind new values or
secret inputs with --set, --set-file and --set-env, exactly as for apply.

Pick targets with --cluster (repeatable) or --all for every deployment of the
profile; add --outdated to touch only the ones behind the chosen version.
Without --version the profile's current version is rolled out.

A member of a target stack that the version does not list is removed. The
platform does that without asking only for a member it can show this profile
put there; any other removal (an add-on, manifest or application someone added
to the stack) is refused, with nothing changed, unless you name it with
--confirm-removal kind/name. --dry-run lists, per stack, the members the
version would remove, and writes nothing. A rollout to more than one stack
asks first (--yes skips the question), and it stops at the first stack that
fails (--continue-on-error keeps going).

--via-apply uses the older path instead: the version is exported as
ClusterInfrastructureAsCode and applied with the cluster apply lane. It
updates the stack the same way but leaves the fleet view at the previous
version; use it only against a platform that predates in-place upgrades. That
lane cannot tell whose a member is, so it refuses every removal you do not
name with --confirm-removal.

The write returns as soon as the platform has accepted it; the manifest and
add-on deploys run in the background. Watch them with
'ankra cluster operations list --cluster <name>'.`,
	Example: `  ankra stack-profiles rollout hello-fleet --all --outdated
  ankra stack-profiles rollout hello-fleet --cluster prod-eu --cluster prod-us --version 2
  ankra stack-profiles rollout hello-fleet --all --dry-run
  ankra stack-profiles rollout hello-fleet --cluster prod-eu --confirm-removal addon/legacy-exporter`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}
		clusterFlags, _ := cmd.Flags().GetStringArray("cluster")
		all, _ := cmd.Flags().GetBool("all")
		onlyOutdated, _ := cmd.Flags().GetBool("outdated")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		versionRaw, _ := cmd.Flags().GetString("version")
		if all == (len(clusterFlags) > 0) {
			return withExitCode(exitUsage, errors.New("pick the targets with --cluster (repeatable) or --all, not both"))
		}
		versionFlag, versionError := parseProfileVersionFlag(versionRaw)
		if versionError != nil {
			return versionError
		}

		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		detail, detailError := apiClient.GetStackProfile(profileID)
		if detailError != nil {
			return fmt.Errorf("reading stack profile: %w", detailError)
		}
		version := versionFlag
		if version <= 0 {
			version = detail.Profile.CurrentVersion
		}
		if version <= 0 {
			return fmt.Errorf("profile %s has no published version to roll out", detail.Profile.Name)
		}

		_, deployments, deploymentsError := loadStackProfileDeployments(cmd.Context(), profileID)
		if deploymentsError != nil {
			return deploymentsError
		}
		if onlyOutdated {
			for index := range deployments.Result {
				deployments.Result[index].Outdated = deployments.Result[index].Version < version
			}
		}
		targets, targetsError := rolloutTargets(deployments, clusterFlags, all, onlyOutdated)
		if targetsError != nil {
			return targetsError
		}
		if len(targets) == 0 {
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, map[string]any{"profile": detail.Profile.Name, "version": version, "targets": []rolloutOutcome{}})
			}
			if len(deployments.Result) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Nothing to roll out: no stack has been deployed from '%s' yet. Deploy one with 'ankra stack-profiles apply %s --cluster <name> --deploy'.\n", detail.Profile.Name, detail.Profile.Name)
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Nothing to roll out: every deployment of '%s' already runs v%d.\n", detail.Profile.Name, version)
			return nil
		}

		yes, _ := cmd.Flags().GetBool("yes")
		continueOnError, _ := cmd.Flags().GetBool("continue-on-error")
		confirmRemovalFlags, _ := cmd.Flags().GetStringArray("confirm-removal")
		confirmedRemovals, confirmError := parseConfirmedRemovals(confirmRemovalFlags)
		if confirmError != nil {
			return confirmError
		}
		// What each target would lose, read before anything is written: the
		// dry run reports it, the question before a multi-stack write counts
		// it, and the --via-apply lane refuses removals nobody confirmed.
		plans := planRolloutRemovals(cmd.Context(), profileID, version, targets)

		if dryRun {
			outcomes := make([]rolloutOutcome, 0, len(targets))
			for index, target := range targets {
				outcome := rolloutOutcome{rolloutTarget: target, ToVersion: version, Status: "planned", Removes: plans[index].Removes}
				if plans[index].ReadError != nil {
					outcome.RemovesUnknown = plans[index].ReadError.Error()
				}
				outcomes = append(outcomes, outcome)
			}
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, map[string]any{"profile": detail.Profile.Name, "version": version, "dry_run": true, "targets": outcomes})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Dry run: '%s' v%d would be rolled out to %d %s (nothing written):\n",
				detail.Profile.Name, version, len(targets), pluralise(len(targets), "stack", "stacks"))
			renderRolloutPlan(cmd.OutOrStdout(), outcomes, plans)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nA listed member is removed without asking only when the platform can show this profile put it there;")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "any other removal is refused unless you name it with --confirm-removal kind/name.")
			return nil
		}

		viaApply, _ := cmd.Flags().GetBool("via-apply")
		setValues, _ := cmd.Flags().GetStringArray("set")
		setFiles, _ := cmd.Flags().GetStringArray("set-file")
		setEnvs, _ := cmd.Flags().GetStringArray("set-env")
		bindings, bindingsError := buildParameterBindings(setValues, setFiles, setEnvs)
		if bindingsError != nil {
			return bindingsError
		}
		if !viaApply {
			if waitRequested, _ := asyncWriteWaitFlag(cmd); waitRequested {
				return withExitCode(exitUsage, errors.New("--wait applies to --via-apply only: the in-place update returns once the platform has accepted it, and the deploys it schedules are watched with 'ankra cluster operations list --cluster <name>'"))
			}
			if confirmationError := confirmMultiTargetRollout(cmd, detail.Profile.Name, version, targets, plans, yes); confirmationError != nil {
				return confirmationError
			}
			return rolloutInPlace(cmd, format, profileID, detail.Profile.Name, version, targets, bindings, confirmedRemovals, continueOnError)
		}
		if len(bindings) > 0 {
			return withExitCode(exitUsage, errors.New("--set, --set-file and --set-env bind inputs on the in-place lane; they cannot be combined with --via-apply"))
		}

		export, exportError := apiClient.ExportStackProfileIac(profileID, version)
		if exportError != nil {
			return fmt.Errorf("exporting profile version: %w", exportError)
		}
		document, decodeError := base64.StdEncoding.DecodeString(export.ContentBase64)
		if decodeError != nil {
			return fmt.Errorf("decoding export: %w", decodeError)
		}
		// An export embeds every manifest and values file, so it never
		// references a file; parsing it against an empty directory turns a
		// from_file that somehow appears into a loud failure instead of a
		// read of whatever happens to sit in the operator's working directory.
		emptyDirectory, temporaryError := os.MkdirTemp("", "ankra-rollout-")
		if temporaryError != nil {
			return fmt.Errorf("creating a scratch directory: %w", temporaryError)
		}
		defer func() { _ = os.RemoveAll(emptyDirectory) }()
		base, buildError := buildImportRequestFromBytes(document, emptyDirectory)
		if buildError != nil {
			return fmt.Errorf("invalid ImportCluster in the exported profile version: %w", buildError)
		}
		if len(base.Spec.Stacks) != 1 {
			return fmt.Errorf("profile '%s' v%d exports %d stacks; rollout updates one deployed stack per cluster, so it needs a single-stack profile version (apply it with 'ankra cluster apply' after renaming the stacks yourself)", detail.Profile.Name, version, len(base.Spec.Stacks))
		}

		wait, waitError := asyncWriteWaitFlag(cmd)
		if waitError != nil {
			return waitError
		}
		if confirmationError := confirmMultiTargetRollout(cmd, detail.Profile.Name, version, targets, plans, yes); confirmationError != nil {
			return confirmationError
		}
		requestContext, cancelRequestContext, contextError := asyncWriteRequestContext(cmd)
		if contextError != nil {
			return contextError
		}
		defer cancelRequestContext()

		if format == outputDefault {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rolling out '%s' v%d to %d %s...\n",
				detail.Profile.Name, version, len(targets), pluralise(len(targets), "stack", "stacks"))
		}
		outcomes := make([]rolloutOutcome, 0, len(targets))
		failed := 0
		for index, target := range targets {
			if failed > 0 && !continueOnError {
				outcomes = append(outcomes, skippedAfterFailure(target, version))
				continue
			}
			outcome := rolloutOutcome{rolloutTarget: target, ToVersion: version}
			// The apply lane replaces the stack with the version and cannot
			// tell whose a member is, so every removal must be named.
			if plans[index].ReadError != nil {
				outcome.Status = "failed"
				outcome.Message = "nothing applied: the stack's members could not be read, so what the apply would remove is unknown (" + plans[index].ReadError.Error() + ")"
				failed++
				outcomes = append(outcomes, outcome)
				continue
			}
			if unconfirmed := unconfirmedRemovals(plans[index], confirmedRemovals); len(unconfirmed) > 0 {
				outcome.Status = "refused"
				outcome.Message = fmt.Sprintf("nothing applied: it would remove %s, which the version does not list; to remove them anyway add %s",
					strings.Join(unconfirmed, ", "), confirmRolloutRemovalFlags(unconfirmed))
				failed++
				outcomes = append(outcomes, outcome)
				continue
			}
			request := rolloutRequest(base, target)
			response, submitted, applyError := apiClient.ApplyCluster(requestContext, request, wait)
			switch {
			case applyError != nil:
				outcome.Status = "failed"
				outcome.Message = applyError.Error()
				failed++
			case submitted:
				outcome.Status = "submitted"
				if response != nil && response.OperationID != "" {
					outcome.Message = "follow it with '" + followOperationCommand(response.OperationID) + "'"
				}
			case response != nil && len(response.Errors) > 0:
				outcome.Status = "failed"
				outcome.Message = describeImportErrors(response.Errors)
				failed++
			case response != nil && response.GitPushDeferred:
				outcome.Status = "applied"
				outcome.Message = response.GitPushMessage
			default:
				outcome.Status = "applied"
			}
			if format == outputDefault {
				line := fmt.Sprintf("  %s / %s: v%d -> v%d %s", target.ClusterName, target.StackName, target.FromVersion, version, outcome.Status)
				if outcome.Message != "" {
					line += " (" + outcome.Message + ")"
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			outcomes = append(outcomes, outcome)
		}

		if format != outputDefault {
			if encodeError := encodeStructured(cmd.OutOrStdout(), format, map[string]any{"profile": detail.Profile.Name, "version": version, "targets": outcomes}); encodeError != nil {
				return encodeError
			}
			if failed > 0 {
				return rolloutFailureError(failed, outcomes, "failed to apply")
			}
			return nil
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		renderRolloutOutcomes(cmd.OutOrStdout(), outcomes)
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nManifest and add-on deploys run in the background from here.")
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Watch them with 'ankra cluster operations list --cluster <name>'.")
		if failed > 0 {
			return rolloutFailureError(failed, outcomes, "failed to apply")
		}
		return nil
	},
}

func describeImportErrors(resourceErrors []client.ImportResponseResourceError) string {
	parts := []string{}
	for _, resourceError := range resourceErrors {
		for _, detail := range resourceError.Errors {
			parts = append(parts, fmt.Sprintf("%s %q: %s", resourceError.Kind, resourceError.Name, detail.Message))
		}
	}
	return strings.Join(parts, "; ")
}

// renderRolloutPlan prints the dry run: each target and what it would lose.
func renderRolloutPlan(out io.Writer, outcomes []rolloutOutcome, plans []rolloutRemovalPlan) {
	planTable := table.NewWriter()
	planTable.SetOutputMirror(out)
	planTable.SetStyle(table.StyleRounded)
	planTable.AppendHeader(table.Row{"CLUSTER", "STACK", "FROM", "TO", "WOULD REMOVE"})
	for index, outcome := range outcomes {
		planTable.AppendRow(table.Row{
			outcome.ClusterName, outcome.StackName,
			fmt.Sprintf("v%d", outcome.FromVersion), fmt.Sprintf("v%d", outcome.ToVersion),
			describeRemovalPlan(plans[index]),
		})
	}
	planTable.Render()
}

func renderRolloutOutcomes(out io.Writer, outcomes []rolloutOutcome) {
	rolloutTable := table.NewWriter()
	rolloutTable.SetOutputMirror(out)
	rolloutTable.SetStyle(table.StyleRounded)
	rolloutTable.AppendHeader(table.Row{"CLUSTER", "STACK", "FROM", "TO", "STATUS"})
	for _, outcome := range outcomes {
		rolloutTable.AppendRow(table.Row{
			outcome.ClusterName, outcome.StackName,
			fmt.Sprintf("v%d", outcome.FromVersion), fmt.Sprintf("v%d", outcome.ToVersion),
			outcome.Status,
		})
	}
	rolloutTable.Render()
}

func init() {
	stackProfilesRolloutCmd.Flags().StringArray("cluster", nil, "Target cluster name or ID (repeatable)")
	stackProfilesRolloutCmd.Flags().Bool("all", false, "Every cluster that runs a stack deployed from this profile")
	stackProfilesRolloutCmd.Flags().Bool("outdated", false, "Only the deployments behind the version being rolled out")
	stackProfilesRolloutCmd.Flags().String("version", "", "Profile version to roll out, as 2 or v2 (defaults to the profile's current version)")
	stackProfilesRolloutCmd.Flags().Bool("dry-run", false, "List the stacks that would be updated without writing anything")
	stackProfilesRolloutCmd.Flags().StringArray("set", nil, "Bind a parameter: name=value (repeatable; not for secrets)")
	stackProfilesRolloutCmd.Flags().StringArray("set-file", nil, "Bind a parameter from a file: name=path (repeatable; secret-safe)")
	stackProfilesRolloutCmd.Flags().StringArray("set-env", nil, "Bind a parameter from an environment variable: name=ENV_VAR (repeatable; secret-safe)")
	stackProfilesRolloutCmd.Flags().StringArray("confirm-removal", nil, "Allow the rollout to remove this member although the platform cannot show the profile put it there, as kind/name (addon, manifest or application; repeatable)")
	stackProfilesRolloutCmd.Flags().Bool("continue-on-error", false, "Keep rolling out to the remaining stacks after one fails (by default the rollout stops at the first failure)")
	stackProfilesRolloutCmd.Flags().Bool("yes", false, "Skip the confirmation asked before a rollout to more than one stack")
	stackProfilesRolloutCmd.Flags().Bool("via-apply", false, "Export the version as ClusterInfrastructureAsCode and apply it with the cluster apply lane instead of the platform's in-place upgrade (older platforms; the fleet view then keeps the previous version)")
	registerAsyncWriteFlags(stackProfilesRolloutCmd)
	if waitFlag := stackProfilesRolloutCmd.Flags().Lookup("wait"); waitFlag != nil {
		waitFlag.Usage = "Wait for each configuration write to be applied before moving to the next cluster. " +
			"Manifest and add-on deploys are dispatched afterwards and are NOT covered by this flag"
	}
	registerStructuredOutputFlags(stackProfilesRolloutCmd)
	stackProfilesCmd.AddCommand(stackProfilesRolloutCmd)
}

// rolloutInPlace asks the platform to replace each deployed stack with the
// version, through the profile's own lane, so the deployment is recorded at
// the new version. The deployment's recorded bindings go first and the
// caller's --set values override them by name. version is already resolved
// by the caller (the flag, else the profile's current version, never 0).
func rolloutInPlace(cmd *cobra.Command, format outputFormat, profileID string, profileName string, version int, targets []rolloutTarget, bindings []client.ParameterBinding, confirmedRemovals []string, continueOnError bool) error {
	out := cmd.OutOrStdout()
	if format == outputDefault {
		_, _ = fmt.Fprintf(out, "Rolling out '%s' v%d to %d %s...\n", profileName, version, len(targets), pluralise(len(targets), "stack", "stacks"))
	}
	outcomes := make([]rolloutOutcome, 0, len(targets))
	failed := 0
	legacyPlatform := false
	for _, target := range targets {
		outcome := rolloutOutcome{rolloutTarget: target, ToVersion: version}
		if legacyPlatform {
			outcome.Status = "skipped"
			outcome.Message = "not attempted: the platform predates in-place upgrades"
			outcomes = append(outcomes, outcome)
			continue
		}
		if failed > 0 && !continueOnError {
			outcomes = append(outcomes, skippedAfterFailure(target, version))
			continue
		}
		requestVersion := version
		request := client.InstantiateStackProfileRequest{
			ProfileID:               profileID,
			Version:                 &requestVersion,
			NewStackName:            target.StackName,
			Parameters:              mergeParameterBindings(target.recordedBindings, bindings),
			Deploy:                  true,
			UpgradeExisting:         true,
			ConfirmedMemberRemovals: confirmedRemovals,
		}
		result, applyError := instantiateProfileOnCluster(target.ClusterID, request)
		refusal, isRemovalRefusal := asMemberRemovalRefusal(applyError)
		switch {
		case isRemovalRefusal:
			outcome.Status = "refused"
			outcome.Message = describeRemovalRefusal(refusal)
			failed++
		case applyError != nil:
			outcome.Status = "failed"
			outcome.Message = applyError.Error()
			failed++
		case result.StackName != target.StackName && !result.Deployed && result.DraftID != "":
			// A platform that predates upgrade_existing ignores the flag, so
			// the ordinary lane runs: the taken name is resolved to a renamed,
			// undeployed draft. A supporting platform pins the name on this
			// lane and answers 404/409 rather than renaming, so a renamed
			// draft is the legacy signature. Only that draft - never a
			// deployed stack - is removed again, and the remaining targets
			// are skipped rather than littered the same way.
			outcome.Status = "failed"
			cleanup := "that draft was removed again"
			if _, deleteError := apiClient.DeleteStack(context.Background(), target.ClusterID, result.StackName); deleteError != nil {
				cleanup = fmt.Sprintf("removing that draft failed (%s), delete it with 'ankra cluster stacks delete %s --cluster %s'", deleteError.Error(), result.StackName, target.ClusterName)
			}
			outcome.Message = fmt.Sprintf("the platform did not update '%s' in place: it created draft '%s' instead, so it predates in-place upgrades; %s. Roll out with --via-apply",
				target.StackName, result.StackName, cleanup)
			failed++
			legacyPlatform = true
		case result.StackName != target.StackName:
			// Not the legacy shape (deployed, or no draft): reported, never
			// deleted, since the answer names something that may be live.
			outcome.Status = "failed"
			outcome.Message = fmt.Sprintf("the platform answered with stack '%s' instead of updating '%s' in place; nothing was removed - check the cluster before retrying", result.StackName, target.StackName)
			failed++
		case !result.Deployed:
			// Not expected from a supporting platform (an upgrade is its own
			// deploy); reported as what it is rather than as a legacy answer.
			outcome.Status = "failed"
			outcome.Message = fmt.Sprintf("the platform accepted the update of '%s' but reports it as not deployed", target.StackName)
			if result.DraftID != "" {
				outcome.Message += " (draft " + result.DraftID + ")"
			}
			failed++
		default:
			outcome.Status = "applied"
			if len(result.Warnings) > 0 {
				outcome.Message = strings.Join(result.Warnings, "; ")
			}
			if result.OperationID != nil {
				outcome.OperationID = *result.OperationID
			}
			outcome.JobCount = result.JobCount
		}
		if format == outputDefault {
			line := fmt.Sprintf("  %s / %s: v%d -> v%d %s", target.ClusterName, target.StackName, target.FromVersion, version, outcome.Status)
			if outcome.JobCount > 0 {
				line += fmt.Sprintf(", %d %s scheduled", outcome.JobCount, pluralise(outcome.JobCount, "job", "jobs"))
			}
			if outcome.Message != "" {
				line += " (" + outcome.Message + ")"
			}
			_, _ = fmt.Fprintln(out, line)
		}
		outcomes = append(outcomes, outcome)
	}
	if format != outputDefault {
		if encodeError := encodeStructured(out, format, map[string]any{"profile": profileName, "version": version, "targets": outcomes}); encodeError != nil {
			return encodeError
		}
		if failed > 0 {
			return rolloutFailureError(failed, outcomes, "failed")
		}
		return nil
	}
	_, _ = fmt.Fprintln(out)
	renderRolloutOutcomes(out, outcomes)
	_, _ = fmt.Fprintln(out, "\nThe deploys run in the background from here; 'ankra stack-profiles deployments "+profileName+"' now reports the new version.")
	_, _ = fmt.Fprintln(out, "Watch them with 'ankra cluster operations list --cluster <name>'.")
	if failed > 0 {
		return rolloutFailureError(failed, outcomes, "failed")
	}
	return nil
}

// mergeParameterBindings layers the caller's bindings over the recorded
// ones by name, keeping the recorded order for the rest. A name given more
// than once keeps its last value, so the request never carries two bindings
// for one input and leaves the winner to the server.
func mergeParameterBindings(recorded []client.ParameterBinding, overrides []client.ParameterBinding) []client.ParameterBinding {
	lastOverride := map[string]client.ParameterBinding{}
	overrideOrder := []string{}
	for _, binding := range overrides {
		if _, seen := lastOverride[binding.Name]; !seen {
			overrideOrder = append(overrideOrder, binding.Name)
		}
		lastOverride[binding.Name] = binding
	}
	merged := make([]client.ParameterBinding, 0, len(recorded)+len(overrideOrder))
	for _, binding := range recorded {
		if _, overridden := lastOverride[binding.Name]; !overridden {
			merged = append(merged, binding)
		}
	}
	for _, name := range overrideOrder {
		merged = append(merged, lastOverride[name])
	}
	return merged
}

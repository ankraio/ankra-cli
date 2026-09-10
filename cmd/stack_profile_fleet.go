package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	ID              string `json:"id"`
	TargetClusterID string `json:"target_cluster_id"`
	ClusterName     string `json:"cluster_name"`
	StackName       string `json:"stack_name"`
	StackState      string `json:"stack_state"`
	Version         int    `json:"version"`
	Outdated        bool   `json:"outdated"`
	CreatedAt       string `json:"created_at"`
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

func stackProfileDeploymentStatus(deployment stackProfileDeployment, currentVersion int) string {
	if deployment.Outdated {
		return fmt.Sprintf("update available (v%d)", currentVersion)
	}
	return "up to date"
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
	for _, deployment := range deployments.Result {
		clusters[deployment.TargetClusterID] = true
		if deployment.Outdated {
			behind++
		}
	}
	drift := "all up to date"
	if behind > 0 {
		drift = fmt.Sprintf("%d behind v%d", behind, deployments.CurrentVersion)
	}
	_, _ = fmt.Fprintf(out, "Current version v%d  ·  %d %s across %d %s  ·  %s\n",
		deployments.CurrentVersion,
		len(deployments.Result), pluralise(len(deployments.Result), "deployment", "deployments"),
		len(clusters), pluralise(len(clusters), "cluster", "clusters"),
		drift)
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
}

type rolloutOutcome struct {
	rolloutTarget
	ToVersion int    `json:"to_version"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
}

// resolveClusterReference turns a cluster name or ID into both, paging the
// cluster list the way resolveClusterID does.
func resolveClusterReference(nameOrID string) (string, string, error) {
	const pageSize = 100
	const maxPages = 50
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
			ClusterID:   deployment.TargetClusterID,
			ClusterName: deployment.ClusterName,
			StackName:   deployment.StackName,
			FromVersion: deployment.Version,
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
// document's metadata.name becomes the cluster, and its single stack takes
// the name the deployment already uses so the platform updates that stack
// in place instead of creating a second one.
func rolloutRequest(base client.CreateImportClusterRequest, target rolloutTarget) client.CreateImportClusterRequest {
	request := base
	request.Name = target.ClusterName
	stacks := append([]client.Stack(nil), base.Spec.Stacks...)
	if len(stacks) == 1 && target.StackName != "" {
		stacks[0].Name = target.StackName
	}
	request.Spec.Stacks = stacks
	return request
}

var stackProfilesRolloutCmd = &cobra.Command{
	Use:   "rollout [profile-id|profile-name]",
	Short: "Roll a published profile version out to the clusters already running it",
	Long: `Update the stacks deployed from a profile to a published version, in place.

For every target the profile version is exported as ClusterInfrastructureAsCode,
addressed to that cluster and the stack it already runs, and applied the way
'ankra cluster apply' applies a file: the same stack is updated, so Kubernetes
performs a rolling update of the workloads and nothing is created twice.

Pick targets with --cluster (repeatable) or --all for every deployment of the
profile; add --outdated to touch only the ones behind the chosen version.
Without --version the profile's current version is rolled out.

The configuration write returns as soon as the platform has accepted it; the
manifest and add-on deploys run in the background. Watch them with
'ankra cluster operations list --cluster <name>'.`,
	Example: `  ankra stack-profiles rollout hello-fleet --all --outdated
  ankra stack-profiles rollout hello-fleet --cluster prod-eu --cluster prod-us --version 2
  ankra stack-profiles rollout hello-fleet --all --dry-run`,
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
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Nothing to roll out: every deployment of '%s' already runs v%d.\n", detail.Profile.Name, version)
			return nil
		}

		if dryRun {
			outcomes := make([]rolloutOutcome, 0, len(targets))
			for _, target := range targets {
				outcomes = append(outcomes, rolloutOutcome{rolloutTarget: target, ToVersion: version, Status: "planned"})
			}
			if format != outputDefault {
				return encodeStructured(cmd.OutOrStdout(), format, map[string]any{"profile": detail.Profile.Name, "version": version, "dry_run": true, "targets": outcomes})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Dry run: '%s' v%d would be rolled out to %d %s (nothing written):\n",
				detail.Profile.Name, version, len(targets), pluralise(len(targets), "stack", "stacks"))
			renderRolloutOutcomes(cmd.OutOrStdout(), outcomes)
			return nil
		}

		export, exportError := apiClient.ExportStackProfileIac(profileID, version)
		if exportError != nil {
			return fmt.Errorf("exporting profile version: %w", exportError)
		}
		document, decodeError := base64.StdEncoding.DecodeString(export.ContentBase64)
		if decodeError != nil {
			return fmt.Errorf("decoding export: %w", decodeError)
		}
		base, buildError := buildImportRequestFromBytes(document, ".")
		if buildError != nil {
			return fmt.Errorf("invalid ImportCluster in the exported profile version: %w", buildError)
		}

		wait, waitError := asyncWriteWaitFlag(cmd)
		if waitError != nil {
			return waitError
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
		for _, target := range targets {
			outcome := rolloutOutcome{rolloutTarget: target, ToVersion: version}
			request := rolloutRequest(base, target)
			response, submitted, applyError := apiClient.ApplyCluster(requestContext, request, wait)
			switch {
			case applyError != nil:
				outcome.Status = "failed"
				outcome.Message = applyError.Error()
				failed++
			case submitted:
				outcome.Status = "submitted"
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
			return encodeStructured(cmd.OutOrStdout(), format, map[string]any{"profile": detail.Profile.Name, "version": version, "targets": outcomes})
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		renderRolloutOutcomes(cmd.OutOrStdout(), outcomes)
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nManifest and add-on deploys run in the background from here.")
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Watch them with 'ankra cluster operations list --cluster <name>'.")
		if failed > 0 {
			return fmt.Errorf("%d of %d %s failed to apply", failed, len(targets), pluralise(len(targets), "rollout", "rollouts"))
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
	registerAsyncWriteFlags(stackProfilesRolloutCmd)
	if waitFlag := stackProfilesRolloutCmd.Flags().Lookup("wait"); waitFlag != nil {
		waitFlag.Usage = "Wait for each configuration write to be applied before moving to the next cluster. " +
			"Manifest and add-on deploys are dispatched afterwards and are NOT covered by this flag"
	}
	registerStructuredOutputFlags(stackProfilesRolloutCmd)
	stackProfilesCmd.AddCommand(stackProfilesRolloutCmd)
}

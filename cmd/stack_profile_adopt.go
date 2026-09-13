package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// Adoption closes the gap between capturing a profile and rolling one out.
// 'create --stack' and 'save-version --stack' snapshot a live stack's
// contents but record no deployment for the stack they were captured from,
// so the profile reports zero deployments and 'rollout' has nothing to
// write to. Before adopt, the only route to a tracked deployment was
// deleting the stack and applying the profile - which for a CNI, ingress or
// storage stack takes the cluster down.
//
// Adopt writes the deployment record and nothing else. The cluster is not
// touched, so the command is safe to run against production; the risk lives
// in the FIRST rollout afterwards, which replaces the stack with exactly
// what the version lists. That is why the result reports the members the
// version does not carry: those are what a rollout would remove.
var stackProfilesAdoptCmd = &cobra.Command{
	Use:   "adopt [profile-id|profile-name]",
	Short: "Track a stack the cluster already runs as a deployment of a profile",
	Long: `Record an already-deployed stack as a tracked deployment of a stack profile.

Capturing a profile from a live stack ('create --stack', 'save-version
--stack') snapshots the contents but does not register the source stack as a
deployment, so the profile reports no deployments and 'rollout' cannot reach
it. Adopt writes that missing record.

Nothing is written to the cluster: no draft, no deploy, no change to the
stack or its members. Only the deployment record appears, after which
'deployments' lists the stack and 'rollout' can update it in place.

Adopt reports how the live stack differs from the version it is adopted at
rather than refusing a stack that has drifted - a stack patched per cluster
since capture has drifted by definition, and that is the case adopt exists
for. Read the report before rolling out: a rollout replaces the stack with
what the version lists, so any member listed under "only on the cluster" is
removed by it. Pick the version that matches with --version, or publish a
version that carries those members first.`,
	Example: `  ankra stack-profiles adopt so-cilium --stack so-cilium --cluster so-upcloud-production
  ankra stack-profiles adopt so-networking --stack so-networking --cluster so-development --version 2`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, formatError := structuredFormatFromFlags(cmd)
		if formatError != nil {
			return formatError
		}
		stackName, _ := cmd.Flags().GetString("stack")
		clusterReference, _ := cmd.Flags().GetString("cluster")
		versionRaw, _ := cmd.Flags().GetString("version")
		setValues, _ := cmd.Flags().GetStringArray("set")
		setFiles, _ := cmd.Flags().GetStringArray("set-file")
		setEnvs, _ := cmd.Flags().GetStringArray("set-env")
		if strings.TrimSpace(stackName) == "" {
			return withExitCode(exitUsage, errors.New("name the deployed stack to adopt with --stack"))
		}
		if strings.TrimSpace(clusterReference) == "" {
			return withExitCode(exitUsage, errors.New("name the cluster the stack runs on with --cluster"))
		}
		versionFlag, versionError := parseProfileVersionFlag(versionRaw)
		if versionError != nil {
			return versionError
		}
		bindings, bindingsError := buildParameterBindings(setValues, setFiles, setEnvs)
		if bindingsError != nil {
			return bindingsError
		}

		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		detail, detailError := apiClient.GetStackProfile(profileID)
		if detailError != nil {
			return fmt.Errorf("reading stack profile: %w", detailError)
		}
		clusterID, clusterName, clusterError := resolveClusterReference(clusterReference)
		if clusterError != nil {
			return clusterError
		}

		adoptRequest := client.AdoptStackProfileRequest{
			ProfileID:  profileID,
			StackName:  stackName,
			Parameters: bindings,
		}
		if versionFlag > 0 {
			requestVersion := versionFlag
			adoptRequest.Version = &requestVersion
		}

		requestContext, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		result, adoptError := apiClient.AdoptStackProfile(requestContext, clusterID, adoptRequest)
		if adoptError != nil {
			return fmt.Errorf("adopting stack %q into profile %q: %w", stackName, detail.Profile.Name, adoptError)
		}

		if format != outputDefault {
			return encodeStructured(cmd.OutOrStdout(), format, map[string]any{
				"profile":    detail.Profile.Name,
				"cluster":    clusterName,
				"cluster_id": clusterID,
				"stack_name": result.StackName,
				"version":    result.ProfileVersion,
				"current":    result.CurrentVersion,
				"outdated":   result.Outdated,
				"retracked":  result.Retracked,
				"drift":      result.Drift,
			})
		}
		renderAdoptResult(cmd.OutOrStdout(), detail.Profile.Name, clusterName, result)
		return nil
	},
}

// renderAdoptResult prints what was recorded and, when the live stack
// carries members the adopted version does not, what a rollout would
// remove. The drift block is deliberately loud: it is the only warning
// between an adoption and a rollout that deletes a CNI's extra members.
func renderAdoptResult(out io.Writer, profileName string, clusterName string, result *client.AdoptStackProfileResult) {
	verb := "is now tracked as"
	if result.Retracked {
		verb = "was already tracked and is now recorded as"
	}
	_, _ = fmt.Fprintf(out, "Stack '%s' on cluster '%s' %s a deployment of profile '%s' at v%d.\n",
		result.StackName, clusterName, verb, profileName, result.ProfileVersion)
	_, _ = fmt.Fprintln(out, "Nothing was written to the cluster; only the deployment record changed.")
	if result.Outdated {
		_, _ = fmt.Fprintf(out, "\nThe profile's current version is v%d, so this deployment now reports as outdated.\n",
			result.CurrentVersion)
	}

	if result.Drift == nil {
		_, _ = fmt.Fprintln(out, "\nThe platform reported no content comparison for this adoption.")
		return
	}
	if len(result.Drift.OnlyOnCluster) == 0 && len(result.Drift.OnlyInVersion) == 0 {
		_, _ = fmt.Fprintf(out, "\nThe live stack carries exactly the members v%d lists.\n", result.ProfileVersion)
	}
	if len(result.Drift.OnlyOnCluster) > 0 {
		_, _ = fmt.Fprintf(out, "\nOn the cluster but not in v%d (%d %s):\n",
			result.ProfileVersion, len(result.Drift.OnlyOnCluster),
			pluralise(len(result.Drift.OnlyOnCluster), "member", "members"))
		for _, member := range result.Drift.OnlyOnCluster {
			_, _ = fmt.Fprintf(out, "  - %s\n", member)
		}
		_, _ = fmt.Fprintf(out, "A rollout of v%d REMOVES these. Adopt at a version that carries them (--version),\n",
			result.ProfileVersion)
		_, _ = fmt.Fprintln(out, "or publish a version that does, before rolling this deployment out.")
	}
	if len(result.Drift.OnlyInVersion) > 0 {
		_, _ = fmt.Fprintf(out, "\nIn v%d but not on the cluster (%d %s, a rollout would add them):\n",
			result.ProfileVersion, len(result.Drift.OnlyInVersion),
			pluralise(len(result.Drift.OnlyInVersion), "member", "members"))
		for _, member := range result.Drift.OnlyInVersion {
			_, _ = fmt.Fprintf(out, "  - %s\n", member)
		}
	}
	_, _ = fmt.Fprintf(out, "\nNext: 'ankra stack-profiles deployments %s' lists it; 'ankra stack-profiles rollout %s --cluster %s --dry-run' shows what an update would do.\n",
		profileName, profileName, clusterName)
}

func init() {
	stackProfilesAdoptCmd.Flags().String("stack", "", "Name of the stack already deployed on the cluster")
	stackProfilesAdoptCmd.Flags().String("cluster", "", "Cluster name or ID the stack runs on")
	stackProfilesAdoptCmd.Flags().String("version", "", "Profile version to record the deployment at, as 1 or v1 (defaults to the profile's current version)")
	stackProfilesAdoptCmd.Flags().StringArray("set", nil, "Bind a parameter recorded on the deployment: name=value (repeatable; not for secrets)")
	stackProfilesAdoptCmd.Flags().StringArray("set-file", nil, "Bind a recorded parameter from a file: name=path (repeatable; secret-safe)")
	stackProfilesAdoptCmd.Flags().StringArray("set-env", nil, "Bind a recorded parameter from an environment variable: name=ENV_VAR (repeatable; secret-safe)")
	registerStructuredOutputFlags(stackProfilesAdoptCmd)
	stackProfilesCmd.AddCommand(stackProfilesAdoptCmd)
}

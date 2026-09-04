package cmd

// The organisation's Ankra Pipelines settings, over GET/PUT
// /api/v1/org/ci-settings. The endpoint has existed since the pipelines lane
// shipped; nothing in the CLI read it, so the two settings that decide whether
// a pipeline run can start at all - the pipeline cluster and the build
// fallback - were discoverable only by asking Ankra (PLA-825).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var orgCISettingsCmd = &cobra.Command{
	Use:     "ci-settings",
	Aliases: []string{"ci"},
	Short:   "Show or change the organisation's Ankra Pipelines settings",
	Long: `Show or change the settings every pipeline in this organisation runs under.

  ankra org ci-settings get
  ankra org ci-settings set --cluster build-cluster
  ankra org ci-settings set --build-fallback platform_builders

The two that decide whether a run can start at all:

  --cluster           The cluster pipeline steps execute on. Without one,
                      steps have nowhere to run and conclude with a named
                      reason rather than guessing a cluster.
  --build-fallback    Whether a 'build' step may fall back to the
                      Ankra-operated build cluster when the pipeline cluster's
                      agent does not run pipeline steps.
                        platform_builders  keep the fallback available (default)
                        none               refuse the build instead, for
                                           organisations whose source may not
                                           leave their own infrastructure

Reading requires organisation membership; changing requires organisation admin.`,
}

var orgCISettingsGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the organisation's pipeline settings",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		settings, requestError := apiClient.GetOrganisationCISettings(ctx)
		if requestError != nil {
			return fmt.Errorf("get organisation CI settings: %w", requestError)
		}
		if rendered, renderError := renderStructured(cmd, settings); rendered || renderError != nil {
			return renderError
		}
		renderOrganisationCISettings(cmd, settings)
		return nil
	},
}

var orgCISettingsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Change the organisation's pipeline settings",
	Long: `Change the settings every pipeline in this organisation runs under.

Only the flags you pass are written; every other setting keeps its stored
value, so raising one number can never clear the image policy. Clear the
pipeline cluster with an empty value:

  ankra org ci-settings set --cluster build-cluster
  ankra org ci-settings set --cluster ""
  ankra org ci-settings set --build-fallback none
  ankra org ci-settings set --allowed-image-prefix ghcr.io/ankraio --allowed-image-prefix docker.io/library
  ankra org ci-settings set --allowed-image-prefix ""

--allowed-image-prefix replaces the whole policy list rather than adding to it,
because a policy you can only grow is one you cannot correct. Passing it once
with an empty value clears the list, which means no organisation-level
restriction.

Requires organisation admin.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		changes, changesError := organisationCISettingsChanges(cmd)
		if changesError != nil {
			return changesError
		}
		if len(changes) == 0 {
			return withExitCode(exitUsage, errors.New(
				"pass at least one setting to change: --cluster, --build-fallback, "+
					"--max-parallel-runs, --max-parallel-steps, --allowed-image-prefix, "+
					"--artifact-retention-days, --cache-retention-days or --image-gate"))
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		settings, requestError := apiClient.UpdateOrganisationCISettings(ctx, changes)
		if requestError != nil {
			return fmt.Errorf("set organisation CI settings: %w", requestError)
		}
		if rendered, renderError := renderStructured(cmd, settings); rendered || renderError != nil {
			return renderError
		}
		renderOrganisationCISettings(cmd, settings)
		return nil
	},
}

// organisationCISettingsChanges turns the flags the caller actually passed
// into the endpoint's tri-state body. A flag left alone is absent from the map
// and its stored value is untouched; --cluster with an empty value is the
// explicit null that clears the organisation's pipeline cluster.
func organisationCISettingsChanges(cmd *cobra.Command) (map[string]any, error) {
	changes := map[string]any{}

	if cmd.Flags().Changed("cluster") {
		nameOrID, _ := cmd.Flags().GetString("cluster")
		if strings.TrimSpace(nameOrID) == "" {
			changes["ci_cluster_id"] = nil
		} else {
			// Resolved here rather than sent as typed: the endpoint takes a
			// uuid, and a command whose sibling flags all accept a cluster
			// name would otherwise be the one place a name silently fails.
			clusterID, resolveError := resolveClusterID(strings.TrimSpace(nameOrID))
			if resolveError != nil {
				return nil, resolveError
			}
			changes["ci_cluster_id"] = clusterID
		}
	}

	if cmd.Flags().Changed("build-fallback") {
		fallback, _ := cmd.Flags().GetString("build-fallback")
		fallback = strings.TrimSpace(fallback)
		// Checked here as well as on the platform because the value is the
		// answer to "what may this be set to", and a round trip that comes
		// back naming the field but not the vocabulary leaves the caller
		// exactly where they started.
		if fallback != client.CIBuildFallbackPlatformBuilders && fallback != client.CIBuildFallbackNone {
			return nil, withExitCode(exitUsage, fmt.Errorf("--build-fallback must be %s or %s",
				client.CIBuildFallbackPlatformBuilders, client.CIBuildFallbackNone))
		}
		changes["ci_build_fallback"] = fallback
	}

	if cmd.Flags().Changed("image-gate") {
		gate, _ := cmd.Flags().GetString("image-gate")
		gate = strings.TrimSpace(gate)
		switch gate {
		case client.CIImageGateApplicationDependencies, client.CIImageGateAllFindings, client.CIImageGateNothing:
			changes["ci_image_gate"] = gate
		default:
			return nil, withExitCode(exitUsage, fmt.Errorf("--image-gate must be %s, %s or %s",
				client.CIImageGateApplicationDependencies, client.CIImageGateAllFindings,
				client.CIImageGateNothing))
		}
	}

	for flagName, field := range organisationCISettingsIntFields {
		if !cmd.Flags().Changed(flagName) {
			continue
		}
		value, _ := cmd.Flags().GetInt(flagName)
		// The bounds are the platform's, and it refuses with a sentence
		// naming them, so nothing is duplicated here.
		changes[field] = value
	}

	if cmd.Flags().Changed("allowed-image-prefix") {
		raw, _ := cmd.Flags().GetStringArray("allowed-image-prefix")
		prefixes := make([]string, 0, len(raw))
		for _, prefix := range raw {
			if trimmed := strings.TrimSpace(prefix); trimmed != "" {
				prefixes = append(prefixes, trimmed)
			}
		}
		// An empty list is a real value here, not an omission: it is how the
		// organisation says "no restriction". Sent as [] rather than dropped.
		changes["ci_allowed_image_prefixes"] = prefixes
	}

	return changes, nil
}

// organisationCISettingsIntFields maps each numeric flag to the wire field it
// writes. Registration and parsing both walk it, so a renamed flag cannot
// become a no-op that cobra accepts and the change loop never matches.
var organisationCISettingsIntFields = map[string]string{
	"max-parallel-runs":       "ci_max_parallel_runs",
	"max-parallel-steps":      "ci_max_parallel_steps",
	"artifact-retention-days": "ci_artifact_retention_days",
	"cache-retention-days":    "ci_cache_retention_days",
}

// organisationCISettingsIntUsage is the help string for each field in
// organisationCISettingsIntFields, kept beside it so init can catch a field
// added without one rather than registering no flag at all.
var organisationCISettingsIntUsage = map[string]string{
	"max-parallel-runs":       "How many of this organisation's pipeline runs may be in flight at once",
	"max-parallel-steps":      "How many steps of one run may be in flight at once",
	"artifact-retention-days": "How long a run's artifacts survive",
	"cache-retention-days":    "How long a run's caches survive",
}

func renderOrganisationCISettings(cmd *cobra.Command, settings *client.OrganisationCISettings) {
	out := cmd.OutOrStdout()

	switch {
	case settings.ClusterID == nil:
		_, _ = fmt.Fprintln(out,
			"Pipeline cluster:        (none - pipeline steps have nowhere to run)")
	case settings.ClusterName == nil || *settings.ClusterName == "":
		// The id without a name is the deleted-cluster case, and it is worth
		// saying out loud: the settings still point somewhere, and the run
		// that fails for it gives no hint that the target stopped existing.
		_, _ = fmt.Fprintf(out,
			"Pipeline cluster:        %s (no such cluster - it has been deleted)\n", *settings.ClusterID)
	default:
		_, _ = fmt.Fprintf(out, "Pipeline cluster:        %s\n", *settings.ClusterName)
	}

	_, _ = fmt.Fprintf(out, "Build fallback:          %s\n", settings.BuildFallback)
	_, _ = fmt.Fprintf(out, "Max parallel runs:       %d\n", settings.MaxParallelRuns)
	_, _ = fmt.Fprintf(out, "Max parallel steps:      %d\n", settings.MaxParallelSteps)
	if len(settings.AllowedImagePrefixes) == 0 {
		_, _ = fmt.Fprintln(out, "Allowed image prefixes:  (any)")
	} else {
		_, _ = fmt.Fprintf(out, "Allowed image prefixes:  %s\n",
			strings.Join(settings.AllowedImagePrefixes, ", "))
	}
	_, _ = fmt.Fprintf(out, "Artifact retention:      %d days\n", settings.ArtifactRetentionDays)
	_, _ = fmt.Fprintf(out, "Cache retention:         %d days\n", settings.CacheRetentionDays)
	_, _ = fmt.Fprintf(out, "Image gate:              %s\n", settings.ImageGate)

	if settings.IsDefault {
		_, _ = fmt.Fprintln(out,
			"\nEvery value above is Ankra's default; nothing has been set on this organisation.")
	}

	// The one thing this endpoint cannot answer, said plainly rather than
	// left to be discovered from a contradiction. Opting into the fallback is
	// necessary but not sufficient: the Ankra-operated build lane is
	// additionally gated on a capability enabled per organisation, which is
	// not part of these settings and is not readable here. When it is off, a
	// build step still concludes "the organisation's build fallback is
	// 'none'" - naming a setting that this command shows as platform_builders
	// - and an administrator who trusts that sentence goes and changes a
	// setting that was already correct (PLA-825).
	if settings.BuildFallback == client.CIBuildFallbackPlatformBuilders {
		_, _ = fmt.Fprintln(out,
			"\nThe Ankra-operated build fallback also needs the platform-builders capability\n"+
				"enabled for this organisation, which is not one of these settings and is not\n"+
				"shown above. If a build step fails with \"the organisation's build fallback is\n"+
				"'none'\" while this says platform_builders, the setting is not what is missing -\n"+
				"ask Ankra support to enable the capability.")
	}
}

func init() {
	registerStructuredOutputFlags(orgCISettingsGetCmd, orgCISettingsSetCmd)

	orgCISettingsSetCmd.Flags().String("cluster", "",
		"Cluster name or id that pipeline steps run on; empty clears it")
	orgCISettingsSetCmd.Flags().String("build-fallback", "",
		"Whether a build may fall back to Ankra's build cluster: platform_builders or none")
	orgCISettingsSetCmd.Flags().StringArray("allowed-image-prefix", nil,
		"Image prefix a step may name (repeatable); replaces the list, empty clears it")
	orgCISettingsSetCmd.Flags().String("image-gate", "",
		"Which image findings block a publish: app, all or off")
	// Both directions of drift are fatal: walking the field map catches a
	// field with no usage string, the count check catches a usage string
	// naming a flag that writes nothing.
	if len(organisationCISettingsIntUsage) != len(organisationCISettingsIntFields) {
		panic("CI settings int flag usage and wire-field maps disagree")
	}
	for flagName := range organisationCISettingsIntFields {
		usage, hasUsage := organisationCISettingsIntUsage[flagName]
		if !hasUsage {
			panic("CI setting flag " + flagName + " has no usage string")
		}
		orgCISettingsSetCmd.Flags().Int(flagName, 0, usage)
	}

	orgCISettingsCmd.AddCommand(orgCISettingsGetCmd)
	orgCISettingsCmd.AddCommand(orgCISettingsSetCmd)
	orgCmd.AddCommand(orgCISettingsCmd)
}

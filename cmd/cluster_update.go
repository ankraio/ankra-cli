package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// 'ankra cluster update' changes what an existing cluster is for. Until this
// command the environment label and reporting level could be set only by
// 'ankra cluster aws create --environment' or in the portal, and an
// ImportCluster file cannot carry them either: the platform applies an
// import's environment only when the call creates the cluster, because GitOps
// re-applies the same file on every sync and would otherwise revert a later
// change on a schedule (PLA-913).

// clusterEnvironmentNameLimit is the platform's limit on an environment name.
// The platform counts it in bytes (Go len), so the check here does too: a
// rune count would pass a non-ASCII name the platform then refuses.
const clusterEnvironmentNameLimit = 50

// clusterCriticalities are the reporting levels the platform accepts, loudest
// first.
var clusterCriticalities = []string{"production", "staging", "development", "disposable"}

// presetEnvironmentReportingLevels pairs each preset environment with the
// reporting level the portal's environment control writes alongside it, so a
// cluster moved to development here is graded the way the same move in the
// portal grades it. An environment that is not a preset has no pairing.
var presetEnvironmentReportingLevels = map[string]string{
	"production":  "production",
	"staging":     "staging",
	"development": "development",
	"testing":     "development",
	"local":       "disposable",
}

// clusterUpdateOutput is the structured (-o json/yaml) shape of an update.
// Criticality is absent when the update left the stored level alone.
type clusterUpdateOutput struct {
	ClusterID   string  `json:"cluster_id"`
	ClusterName string  `json:"cluster_name"`
	Environment string  `json:"environment"`
	Criticality *string `json:"criticality,omitempty"`
}

var clusterUpdateCmd = &cobra.Command{
	Use:   "update [cluster]",
	Short: "Change a cluster's environment and reporting level",
	Long: `Set the environment of an existing cluster - production, staging, development,
testing, local, or a name of your own - and the reporting level its insights
are graded at.

The environment is what alert rules, dynamic cluster groups and the cluster
list filter on; 'local' also stops offline alerts for the cluster. The
reporting level decides how severe the cluster's findings are graded:
production reports everything the scan finds, and staging, development and
disposable each need a wider blast radius before an issue is treated as
critical.

A preset environment brings its usual reporting level, the same pairing the
portal writes: production, staging and development keep their own name,
testing is graded as development and local as disposable. --criticality
overrides it. An environment of your own leaves the reporting level as it is
unless --criticality is given.

The environment is not part of an ImportCluster file: 'ankra cluster apply'
never changes it on an existing cluster, so a value set here stays until it
is changed here or in the portal.

The cluster is given by name or id; without one, the selected cluster is
updated.`,
	Example: `  ankra cluster update so-infrastructure --environment development
  ankra cluster update --environment eu-qa --criticality staging
  ankra cluster update 3d0e2935-963b-4c7b-a494-b7266776ec68 --environment production -o json`,
	Args: cobra.MaximumNArgs(1),
	RunE: runClusterUpdate,
}

func runClusterUpdate(cmd *cobra.Command, args []string) error {
	environmentFlag, _ := cmd.Flags().GetString("environment")
	criticalityFlag, _ := cmd.Flags().GetString("criticality")
	environment, criticality, inputError := resolveClusterUpdateInput(environmentFlag, criticalityFlag)
	if inputError != nil {
		return withExitCode(exitUsage, inputError)
	}
	if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
		return formatError
	}

	clusterID, clusterName, _, resolveError := resolveClusterFromArgsWithKind(cmd, args)
	if resolveError != nil {
		return resolveError
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, updateError := apiClient.SetClusterEnvironment(ctx, clusterID, environment, criticality)
	if updateError != nil {
		return fmt.Errorf("updating cluster %q: %w", clusterName, updateError)
	}

	output := clusterUpdateOutput{ClusterID: clusterID, ClusterName: clusterName, Environment: result.Environment,
		Criticality: result.Criticality}
	if rendered, renderError := renderStructured(cmd, output); rendered || renderError != nil {
		return renderError
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Cluster '%s' environment set to '%s'.\n", clusterName, result.Environment)
	if result.Criticality != nil {
		_, _ = fmt.Fprintf(out, "Reporting level: %s.\n", *result.Criticality)
		return nil
	}
	_, _ = fmt.Fprintln(out, "Reporting level unchanged; set one with --criticality.")
	return nil
}

// resolveClusterUpdateInput turns the flags into what the platform is sent.
// A preset environment, matched case-insensitively, is sent as the preset
// itself so the stored label is the one the portal writes and alert rules
// match; any other name is sent as typed. The criticality is --criticality
// when given, else the preset's pairing, else empty (not sent).
func resolveClusterUpdateInput(environmentFlag string, criticalityFlag string) (string, string, error) {
	environment := strings.TrimSpace(environmentFlag)
	if environment == "" {
		return "", "", errors.New("--environment is required: production, staging, development, testing, local, or a name of your own")
	}
	if len(environment) > clusterEnvironmentNameLimit {
		return "", "", fmt.Errorf("--environment must be at most %d bytes (a letter outside ASCII counts as two or more)",
			clusterEnvironmentNameLimit)
	}
	pairedLevel, isPreset := presetEnvironmentReportingLevels[strings.ToLower(environment)]
	if isPreset {
		environment = strings.ToLower(environment)
	}

	criticality := strings.ToLower(strings.TrimSpace(criticalityFlag))
	if criticality == "" {
		return environment, pairedLevel, nil
	}
	for _, accepted := range clusterCriticalities {
		if criticality == accepted {
			return environment, criticality, nil
		}
	}
	return "", "", fmt.Errorf("--criticality must be one of: %s", strings.Join(clusterCriticalities, ", "))
}

func init() {
	clusterUpdateCmd.Flags().String("environment", "",
		"Environment: production, staging, development, testing, local, or a name of your own (required)")
	clusterUpdateCmd.Flags().String("criticality", "",
		"Reporting level: production, staging, development or disposable (default: the preset environment's own level; unchanged for a name of your own)")
	registerStructuredOutputFlags(clusterUpdateCmd)
	clusterCmd.AddCommand(clusterUpdateCmd)
}

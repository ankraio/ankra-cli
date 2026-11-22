package cmd

// `ankra cluster baseline` (ankra-hq41xb.15): the CLI surface over
// GET /api/v1/org/clusters/{cluster_id}/baseline, the cluster's Ankra
// Baseline score. `ankra cluster export` beside it prints the cluster's
// whole ImportCluster YAML from GET /api/v1/org/clusters/imported/{id}/iac,
// the route the YAML-editing commands already read.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

func newClusterBaselineCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "baseline",
		Short: "Show the cluster's Ankra Baseline score",
		Long: `Show the cluster's Ankra Baseline score: the latest nightly evaluation of the
Baseline checks (metrics and logs, service-level alerts, backups with a tested
restore, requests and limits, probes, PodDisruptionBudgets, pinned images,
supported versions, certificate automation, no critical or known-exploited
CVEs), each pass, fail or unknown with the evidence it rests on.

The score is passed weight over decided weight. An unknown check, one whose
data was absent or stale, is never a pass: it leaves the score and is reported
as coverage. A cluster the nightly recorder has not scored yet has no score,
which is not a score of zero.

The cluster is --cluster or the one 'ankra cluster select' saved. -o json
prints the platform's answer as it is.`,
		Example: `  ankra cluster baseline
  ankra cluster baseline --cluster prod -o json`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterBaseline(command)
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func runClusterBaseline(command *cobra.Command) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	ctx, cancel := hostedLogsContext(command)
	defer cancel()
	baseline, getError := apiClient.GetClusterBaseline(ctx, cluster.ID)
	if getError != nil {
		return clusterSettingRouteError("reading the Baseline score", cluster, getError)
	}
	if format != outputDefault {
		return encodeStructured(command.OutOrStdout(), format, baseline)
	}
	printClusterBaseline(command.OutOrStdout(), cluster.Name, baseline)
	return nil
}

func printClusterBaseline(out io.Writer, clusterName string, baseline *client.ClusterBaseline) {
	_, _ = fmt.Fprintf(out, "Baseline for cluster '%s':\n\n", clusterName)
	if !baseline.Computed {
		_, _ = fmt.Fprintln(out, "  Score:    not scored yet (the nightly evaluation has not run for this cluster)")
	} else if baseline.Score == nil {
		_, _ = fmt.Fprintln(out, "  Score:    none (no check could be decided)")
	} else {
		_, _ = fmt.Fprintf(out, "  Score:    %.0f%%\n", *baseline.Score)
	}
	_, _ = fmt.Fprintf(out, "  Coverage: weight %d of %d decided (passed %d, failed %d, unknown %d)\n",
		baseline.Weights.Passed+baseline.Weights.Failed, baseline.Weights.Total,
		baseline.Weights.Passed, baseline.Weights.Failed, baseline.Weights.Unknown)
	if baseline.ComputedAt != nil {
		_, _ = fmt.Fprintf(out, "  Computed: %s (%s)\n", formatTimeAgo(*baseline.ComputedAt), *baseline.ComputedAt)
	}
	if len(baseline.Checks) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "\nChecks:")
	for _, check := range baseline.Checks {
		_, _ = fmt.Fprintf(out, "  [%-7s] %s (weight %d)\n", check.Status, check.Title, check.Weight)
		if check.Evidence != "" {
			_, _ = fmt.Fprintf(out, "            %s\n", check.Evidence)
		}
	}
}

func newClusterExportCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "export",
		Short: "Export the cluster's specification as ImportCluster YAML",
		Long: `Print the cluster's whole specification as the ImportCluster YAML that
'ankra cluster apply -f' takes: its stacks with their add-ons, manifests and
applications, variables and settings, as Ankra holds them now. Use it to start
a GitOps repository from a cluster built in the portal, to review what a
cluster declares, or to copy a definition to another cluster.

The YAML goes to stdout, or to the file named with -f. The cluster is
--cluster or the one 'ankra cluster select' saved. A cluster that declares
nothing yet has nothing to export and exits 3.`,
		Example: `  ankra cluster export > cluster.yaml
  ankra cluster export --cluster prod -f prod.yaml`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterExport(command)
		},
	}
	command.Flags().StringP("file", "f", "", "Write the YAML to this file instead of stdout")
	return command
}

func runClusterExport(command *cobra.Command) error {
	cluster, resolveError := resolveActiveCluster(command)
	if resolveError != nil {
		return resolveError
	}
	ctx, cancel := hostedLogsContext(command)
	defer cancel()
	document, getError := apiClient.GetClusterIaC(ctx, cluster.ID)
	if getError != nil {
		if errors.Is(getError, client.ErrClusterEmpty) {
			return withExitCode(exitNotFound, fmt.Errorf(
				"cluster '%s' declares no stacks, add-ons or manifests yet, so there is nothing to export", cluster.Name))
		}
		return fmt.Errorf("exporting cluster '%s': %w", cluster.Name, getError)
	}
	if document != "" && !strings.HasSuffix(document, "\n") {
		document += "\n"
	}
	filePath, _ := command.Flags().GetString("file")
	if filePath == "" {
		_, writeError := io.WriteString(command.OutOrStdout(), document)
		return writeError
	}
	if writeError := os.WriteFile(filePath, []byte(document), 0o600); writeError != nil {
		return fmt.Errorf("writing %s: %w", filePath, writeError)
	}
	_, _ = fmt.Fprintf(command.ErrOrStderr(), "Wrote the specification of cluster '%s' to %s\n", cluster.Name, filePath)
	return nil
}

func init() {
	clusterCmd.AddCommand(newClusterBaselineCommand(), newClusterExportCommand())
}

package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// errUpgradePreflightBlocked is the refusal of a preflight that blocks.
var errUpgradePreflightBlocked = errors.New("the upgrade preflight blocked this Kubernetes upgrade; nothing was changed")

var clusterUpgradePreflightCmd = &cobra.Command{
	Use:   "preflight <cluster_id|name> <target_version>",
	Short: "Check whether a cloud cluster is ready for a Kubernetes upgrade, without changing anything",
	Long: `Run the read-only upgrade preflight for a hop to <target_version>.

Each check answers pass, warn, block or skipped, with a sentence and the evidence:

  hop_plan                    versions per node, which nodes upgrade or are skipped; blocks a
                              minor skip (and prints the one-minor hops to take), a downgrade
                              or a version outside the catalogue
  operations                  running operations, reconciling nodes, an operations block
  pod_disruption_budgets      a budget that allows no disruption parks the drain on the node it
                              names (the one block 'ankra cluster upgrade --force' goes past)
  cloudnative_pg              stopDelay longer than the 300 s drain timeout, single instances
  control_plane_memory        MemAvailable now and over 24 h, from the cluster's metrics source
  restore_points              newest cluster-state snapshot, backup vault, unprotected stacks
  control_plane_reachability  SSH failures to a control plane in the last 24 h
  k3s_address_pin             bind-address, advertise-address and node-ip on k3s control planes

A warning or a skipped check whose evidence could not be read was NOT checked; it did
not pass. 'ankra cluster upgrade' runs the same preflight first.

Exit codes: 0 when the verdict is ready (warnings included), 8 when a check blocks.

Examples:
  ankra cluster upgrade preflight my-cluster v1.35.9+k3s1
  ankra cluster upgrade preflight my-cluster v1.35.9 -o json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		clusterID, resolveError := resolveClusterArg(args[0])
		if resolveError != nil {
			return resolveError
		}
		cluster, lookupError := apiClient.GetClusterByID(clusterID)
		if lookupError != nil {
			return fmt.Errorf("looking up cluster %q: %w", clusterID, lookupError)
		}
		if _, supported := upgradeFunctionForKind(cluster.Kind); !supported {
			return unsupportedClusterKindError(args[0], cluster.Kind, "Kubernetes version upgrades")
		}
		report, preflightError := apiClient.GetUpgradePreflight(cluster.Kind, clusterID, args[1])
		if preflightError != nil {
			return fmt.Errorf("running the upgrade preflight: %w", preflightError)
		}
		written, renderError := renderStructured(cmd, report)
		if renderError != nil {
			return renderError
		}
		if !written {
			printUpgradePreflight(cmd.OutOrStdout(), report)
		}
		if report.Verdict == client.UpgradePreflightVerdictBlocked {
			return withExitCode(exitBlocked, errUpgradePreflightBlocked)
		}
		return nil
	},
}

// printUpgradePreflight renders a preflight report as a verdict line and one
// table row per check.
func printUpgradePreflight(out io.Writer, report *client.UpgradePreflightReport) {
	verdict := strings.ToUpper(report.Verdict)
	switch report.Verdict {
	case client.UpgradePreflightVerdictReady:
		verdict = text.FgGreen.Sprint(verdict)
	case client.UpgradePreflightVerdictBlocked:
		verdict = text.FgRed.Sprint(verdict)
	}
	_, _ = fmt.Fprintf(out, "Upgrade preflight to %s (%s): %s, %d blocking, %d warnings\n",
		report.TargetVersion, report.Distribution, verdict, report.Blocks, report.Warnings)
	checksTable := table.NewWriter()
	checksTable.SetOutputMirror(out)
	checksTable.SetStyle(table.StyleRounded)
	checksTable.AppendHeader(table.Row{"Check", "Status", "Finding"})
	checksTable.SetColumnConfigs([]table.ColumnConfig{{Number: 3, WidthMax: 100}})
	for _, finding := range report.Checks {
		checksTable.AppendRow(table.Row{finding.Check, upgradePreflightStatusLabel(finding), finding.Message})
	}
	checksTable.Render()
}

func upgradePreflightStatusLabel(finding client.UpgradePreflightFinding) string {
	switch finding.Status {
	case client.UpgradePreflightStatusPass:
		return text.FgGreen.Sprint("pass")
	case client.UpgradePreflightStatusWarn:
		return text.FgYellow.Sprint("warn")
	case client.UpgradePreflightStatusBlock:
		if finding.OverridableWithForce {
			return text.FgRed.Sprint("block (--force)")
		}
		return text.FgRed.Sprint("block")
	case client.UpgradePreflightStatusSkipped:
		return "skipped"
	default:
		return finding.Status
	}
}

// upgradePreflightHolds answers whether a report blocks an upgrade sent
// with the given force: any block does without force, and with force every
// block but the PodDisruptionBudget one still does. A blocked verdict that
// the listed blocks do not fully explain (an empty or capped check list, a
// block count above the blocks listed, or a status this CLI does not know)
// holds the upgrade even with force.
func upgradePreflightHolds(report *client.UpgradePreflightReport, force bool) bool {
	listedBlocks := 0
	for _, finding := range report.Checks {
		if finding.Status != client.UpgradePreflightStatusBlock {
			continue
		}
		listedBlocks++
		if force && finding.OverridableWithForce {
			continue
		}
		return true
	}
	if report.Verdict == client.UpgradePreflightVerdictBlocked && (listedBlocks == 0 || report.Blocks > listedBlocks) {
		return true
	}
	return false
}

func init() {
	registerStructuredOutputFlags(clusterUpgradePreflightCmd)
	clusterUpgradeCmd.AddCommand(clusterUpgradePreflightCmd)
}

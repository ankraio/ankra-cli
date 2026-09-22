package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// The per-cluster agent auto-upgrade opt-out. The platform's fleet rollout
// upgrades every online agent whose cluster has not opted out, as soon as
// the cluster has no write execution running; it knows nothing about a
// customer's freeze window. Until this command the opt-out lived only in
// the portal's cluster settings (ankra-f5y9z, support #1230), so a team
// running a migration night could not fence its agents off from an agent
// release from the terminal.

// agentAutoUpgradeOutput is the structured (-o json/yaml) shape of a switch.
type agentAutoUpgradeOutput struct {
	ClusterID          string `json:"cluster_id"`
	ClusterName        string `json:"cluster_name"`
	AutoUpgradeEnabled bool   `json:"auto_upgrade_enabled"`
}

func newClusterAgentAutoUpgradeCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "auto-upgrade",
		Short: "Enable or disable automatic upgrades of the cluster agent",
		Long: `The platform rolls new agent releases out to every online agent automatically,
as soon as the cluster has no write execution running. Disabling that here
fences this cluster's agent off from the rollout - for a freeze window or a
migration night - and enabling it puts the agent back in. Either way
'ankra cluster agent upgrade' still applies the latest release on demand,
and 'ankra cluster agent status' shows the current setting.`,
		Example: `  ankra cluster agent auto-upgrade disable --cluster prod
  ankra cluster agent auto-upgrade enable --cluster prod`,
	}
	command.AddCommand(newClusterAgentAutoUpgradeSwitchCommand(false), newClusterAgentAutoUpgradeSwitchCommand(true))
	return command
}

func newClusterAgentAutoUpgradeSwitchCommand(enabled bool) *cobra.Command {
	use, short := "disable", "Fence the cluster agent off from the automatic fleet rollout"
	if enabled {
		use, short = "enable", "Put the cluster agent back into the automatic fleet rollout"
	}
	command := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			return runClusterAgentAutoUpgrade(command, enabled)
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

func runClusterAgentAutoUpgrade(command *cobra.Command, enabled bool) error {
	cluster, err := resolveActiveCluster(command)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := apiClient.SetClusterAgentAutoUpgrade(ctx, cluster.ID, enabled); err != nil {
		return fmt.Errorf("updating agent auto-upgrade: %w", err)
	}

	output := agentAutoUpgradeOutput{ClusterID: cluster.ID, ClusterName: cluster.Name, AutoUpgradeEnabled: enabled}
	if rendered, err := renderStructured(command, output); rendered || err != nil {
		return err
	}

	out := command.OutOrStdout()
	if enabled {
		_, _ = fmt.Fprintf(out, "Automatic agent upgrades enabled for cluster '%s'.\n", cluster.Name)
		_, _ = fmt.Fprintln(out, "The fleet rollout applies new agent releases to it again once the cluster is quiet.")
		return nil
	}
	_, _ = fmt.Fprintf(out, "Automatic agent upgrades disabled for cluster '%s'.\n", cluster.Name)
	_, _ = fmt.Fprintln(out, "The fleet rollout skips this agent until you run 'ankra cluster agent auto-upgrade enable'.")
	_, _ = fmt.Fprintln(out, "'ankra cluster agent upgrade' still applies the latest release on demand.")
	return nil
}

func init() {
	clusterAgentCmd.AddCommand(newClusterAgentAutoUpgradeCommand())
}

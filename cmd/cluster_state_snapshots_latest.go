package cmd

// `ankra cluster state-snapshots latest`: the CLI surface over
// GET /api/v1/clusters/{kind}/{cluster_id}/state-snapshots/latest, the newest
// captured etcd snapshot a start of the cluster may restore, whose id a start
// takes with --restore-snapshot-id.

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newClusterStateSnapshotsLatestCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "latest",
		Short: "Show the newest state snapshot a start of the cluster may restore",
		Long: `Show the newest captured etcd state snapshot of the cluster that a start may
restore: its id, what captured it (stop, power_schedule or scheduled), when, and
the Kubernetes version it holds. Pass the id to the provider's start command
with --restore-snapshot-id to restore it.

-o json prints the platform's answer as it is.`,
		Example: `  ankra cluster state-snapshots latest --cluster prod
  ankra cluster ankracloud start prod --restore-snapshot-id <id>`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			format, formatError := structuredFormatFromFlags(command)
			if formatError != nil {
				return formatError
			}
			cluster, resolveError := resolveActiveCluster(command)
			if resolveError != nil {
				return resolveError
			}
			if cluster.Kind == "" {
				return fmt.Errorf("cluster '%s' has no provider kind; state snapshots exist only for clusters Ankra provisions", cluster.Name)
			}
			latest, readError := apiClient.GetLatestStateSnapshot(cluster.Kind, cluster.ID)
			if readError != nil {
				return fmt.Errorf("reading the latest state snapshot of cluster '%s': %w", cluster.Name, readError)
			}
			if format != outputDefault {
				return encodeStructured(command.OutOrStdout(), format, latest)
			}
			out := command.OutOrStdout()
			if latest.Snapshot == nil {
				_, _ = fmt.Fprintf(out, "Cluster '%s' has no restorable state snapshot.\n", cluster.Name)
				return nil
			}
			capturedAt, kubeVersion := "-", "-"
			if latest.Snapshot.CapturedAt != nil {
				capturedAt = *latest.Snapshot.CapturedAt
			}
			if latest.Snapshot.KubeVersion != nil {
				kubeVersion = *latest.Snapshot.KubeVersion
			}
			_, _ = fmt.Fprintf(out, "Latest state snapshot of cluster '%s':\n  ID:          %s\n  Trigger:     %s\n"+
				"  Captured at: %s\n  Kubernetes:  %s\n", cluster.Name, latest.Snapshot.ID, latest.Snapshot.Trigger,
				capturedAt, kubeVersion)
			return nil
		},
	}
	registerStructuredOutputFlags(command)
	return command
}

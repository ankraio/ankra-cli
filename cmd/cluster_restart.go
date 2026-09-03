package cmd

import (
	"fmt"
	"time"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

// restartedAtAnnotation is the annotation kubectl rollout restart writes into
// the pod template; changing it is what makes the controller roll every pod.
const restartedAtAnnotation = "kubectl.kubernetes.io/restartedAt"

var clusterRestartCmd = &cobra.Command{
	Use:   "restart <kind> <name> [name...]",
	Short: "Rolling-restart a Deployment, StatefulSet or DaemonSet",
	Long: `Rolling-restart one or more workloads in the active cluster.

This is 'kubectl rollout restart': the pod template gets a fresh
kubectl.kubernetes.io/restartedAt annotation, so the controller replaces
every pod under its own rollout strategy - no downtime for a Deployment with
more than one replica, one pod at a time for a StatefulSet. Nothing else in
the spec changes. The kind is deployment, statefulset or daemonset; the
kubectl short and plural spellings work too.

Examples:
  ankra cluster restart deployment web -n prod
  ankra cluster restart sts postgres -n data --yes
  ankra cluster restart daemonset node-exporter -n monitoring --dry-run`,
	Args:        cobra.MinimumNArgs(2),
	Annotations: map[string]string{"group": "kubernetes"},
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace, _ := cmd.Flags().GetString("namespace")
		yes, _ := cmd.Flags().GetBool("yes")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		resolvedKind, kindError := resolveK8sKind(args[0], "", "")
		if kindError != nil {
			return kindError
		}
		if !isRestartableKind(resolvedKind.kind) {
			return withExitCode(exitUsage, fmt.Errorf(
				"%s cannot be rolling-restarted: the kind must be deployment, statefulset or daemonset", resolvedKind.kind))
		}
		names := args[1:]
		if namespace == "" {
			return withExitCode(exitUsage, fmt.Errorf("--namespace (-n) is required to restart a %s", resolvedKind.kind))
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		if !dryRun {
			prompt := restartPrompt(resolvedKind, names, namespace, cluster.Name)
			if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), prompt, yes); err != nil {
				return err
			}
		}

		restartedAt := time.Now().UTC().Format(time.RFC3339)
		return runResourceMutations(cmd, resolvedKind, names, namespace, restartWording,
			func(name string) (*client.ResourceMutationResponse, error) {
				return apiClient.PatchResource(cluster.ID, client.PatchResourceRequest{
					Kind:      resolvedKind.kind,
					Group:     resolvedKind.group,
					Version:   resolvedKind.version,
					Namespace: namespace,
					Name:      name,
					Patch:     restartPatch(restartedAt),
					PatchType: "strategic",
					DryRun:    dryRun,
				})
			})
	},
}

func isRestartableKind(kind string) bool {
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet":
		return true
	}
	return false
}

func restartPatch(restartedAt string) map[string]interface{} {
	return map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						restartedAtAnnotation: restartedAt,
					},
				},
			},
		},
	}
}

func restartPrompt(kind k8sKind, names []string, namespace, clusterName string) string {
	if len(names) == 1 {
		return fmt.Sprintf("Rolling-restart %s %q in namespace %q on cluster %q? [y/N]: ",
			kind.kind, names[0], namespace, clusterName)
	}
	return fmt.Sprintf("Rolling-restart %d %s in namespace %q on cluster %q? [y/N]: ",
		len(names), pluralKindLabel(kind), namespace, clusterName)
}

func init() {
	clusterRestartCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace (required)")
	clusterRestartCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	clusterRestartCmd.Flags().Bool("dry-run", false, "Report what would be restarted without changing anything")

	clusterCmd.AddCommand(clusterRestartCmd)
}

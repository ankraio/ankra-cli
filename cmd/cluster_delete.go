package cmd

import (
	"errors"
	"fmt"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var clusterDeleteCmd = &cobra.Command{
	Use:   "delete <kind> <name> [name...]",
	Short: "Delete Kubernetes resources from the active cluster",
	Long: `Delete one or more Kubernetes resources from the active cluster.

The kind takes the kubectl spellings - pod, pods, po, deployment, deploy,
statefulset, sts, configmap, cm, namespace, node, pvc, ... - and a custom
resource is reachable with --group and --api-version, exactly like
'cluster get resources' and 'cluster describe'.

A pod owned by a controller (Deployment, StatefulSet, DaemonSet, Job) is
recreated by that controller, so deleting it is how you restart a single
replica; 'cluster restart' rolls a whole workload. The delete goes through
the cluster's Ankra agent: no kubeconfig is needed and the same organisation
permissions as the portal apply.

Each object reports its own outcome. The command exits 0 when everything was
deleted, 3 when one of the objects did not exist, and 1 when the cluster
refused a delete.

Examples:
  ankra cluster delete pod my-pod -n default
  ankra cluster delete pods web-0 web-1 -n prod --yes
  ankra cluster delete pod stuck-pod -n default --grace-period 0
  ankra cluster delete deployment web -n prod --dry-run
  ankra cluster delete namespace scratch
  ankra cluster delete Certificate web-tls -n prod --group cert-manager.io --api-version v1`,
	Args:        cobra.MinimumNArgs(2),
	Annotations: map[string]string{"group": "kubernetes"},
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace, _ := cmd.Flags().GetString("namespace")
		yes, _ := cmd.Flags().GetBool("yes")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		apiGroup, _ := cmd.Flags().GetString("group")
		apiVersion, _ := cmd.Flags().GetString("api-version")

		resolvedKind, kindError := resolveK8sKind(args[0], apiGroup, apiVersion)
		if kindError != nil {
			return kindError
		}
		names := args[1:]
		if !resolvedKind.clusterScoped && namespace == "" {
			return withExitCode(exitUsage, fmt.Errorf("--namespace (-n) is required to delete a %s", resolvedKind.kind))
		}
		if resolvedKind.clusterScoped {
			namespace = ""
		}
		var gracePeriodSeconds *int
		if cmd.Flags().Changed("grace-period") {
			gracePeriod, _ := cmd.Flags().GetInt("grace-period")
			if gracePeriod < 0 {
				return withExitCode(exitUsage, errors.New("--grace-period must be zero or a positive number of seconds"))
			}
			gracePeriodSeconds = &gracePeriod
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		if !dryRun {
			prompt := deleteResourcesPrompt(resolvedKind, names, namespace, cluster.Name)
			if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), prompt, yes); err != nil {
				return err
			}
		}

		return runResourceMutations(cmd, resolvedKind, names, namespace, deleteWording,
			func(name string) (*client.ResourceMutationResponse, error) {
				return apiClient.DeleteResource(cluster.ID, client.DeleteResourceRequest{
					Kind:               resolvedKind.kind,
					Group:              resolvedKind.group,
					Version:            resolvedKind.version,
					Resource:           resolvedKind.resource,
					Namespace:          namespace,
					Name:               name,
					DryRun:             dryRun,
					GracePeriodSeconds: gracePeriodSeconds,
				})
			})
	},
}

// mutationWording names one write verb in the three forms the per-object
// outcome lines and the error summary need.
type mutationWording struct {
	noun        string
	pastTense   string
	progressive string
}

var (
	deleteWording  = mutationWording{noun: "delete", pastTense: "deleted", progressive: "deleting"}
	restartWording = mutationWording{noun: "restart", pastTense: "restarted", progressive: "restarting"}
)

type resourceMutation func(name string) (*client.ResourceMutationResponse, error)

// runResourceMutations applies one mutation per name and turns the agent's
// per-object verdicts into the shared exit-code contract: every object
// prints its own line, a transport failure stops the run where it is, an
// "error" verdict exits 1 and a "not_found" verdict exits 3 once every
// other object has been handled.
func runResourceMutations(cmd *cobra.Command, kind k8sKind, names []string, namespace string,
	wording mutationWording, mutate resourceMutation) error {
	out := cmd.OutOrStdout()
	label := strings.ToLower(kind.kind)
	location := ""
	if namespace != "" {
		location = fmt.Sprintf(" in namespace %q", namespace)
	}
	missingCount := 0
	var refused []string
	for _, name := range names {
		response, err := mutate(name)
		if err != nil {
			return fmt.Errorf("%s %s %q: %w", wording.progressive, label, name, err)
		}
		switch response.Status {
		case "success":
			_, _ = fmt.Fprintf(out, "%s %q %s%s\n", label, name, wording.pastTense, location)
		case "dry_run":
			_, _ = fmt.Fprintf(out, "%s %q would be %s%s (dry run)\n", label, name, wording.pastTense, location)
		case "not_found":
			missingCount++
			_, _ = fmt.Fprintf(out, "%s %q not found%s\n", label, name, location)
		default:
			refused = append(refused, name+": "+mutationVerdictMessage(response))
		}
	}

	if len(refused) > 0 {
		return fmt.Errorf("%s refused for %d of %d %s:\n  %s",
			wording.noun, len(refused), len(names), pluralKindLabel(kind), strings.Join(refused, "\n  "))
	}
	if missingCount > 0 {
		return withExitCode(exitNotFound, fmt.Errorf("%d of %d %s not found%s",
			missingCount, len(names), pluralKindLabel(kind), location))
	}
	return nil
}

func deleteResourcesPrompt(kind k8sKind, names []string, namespace, clusterName string) string {
	where := fmt.Sprintf("on cluster %q", clusterName)
	if namespace != "" {
		where = fmt.Sprintf("in namespace %q on cluster %q", namespace, clusterName)
	}
	warning := deleteKindWarning(kind.kind)
	if len(names) == 1 {
		return fmt.Sprintf("%sDelete %s %q %s? [y/N]: ", warning, strings.ToLower(kind.kind), names[0], where)
	}
	return fmt.Sprintf("%sDelete %d %s (%s) %s? [y/N]: ",
		warning, len(names), pluralKindLabel(kind), strings.Join(names, ", "), where)
}

// deleteKindWarning spells out the consequence a delete of this kind carries
// beyond the object itself, so the prompt says it before the [y/N].
func deleteKindWarning(kind string) string {
	switch kind {
	case "Namespace":
		return "Deleting a namespace removes every object inside it.\n"
	case "PersistentVolume":
		return "Deleting a PersistentVolume releases its storage according to the reclaim policy.\n"
	case "Node":
		return "Deleting a Node removes it from the cluster; the machine itself is not touched.\n"
	}
	return ""
}

func pluralKindLabel(kind k8sKind) string {
	if kind.resource != "" {
		return kind.resource
	}
	return strings.ToLower(kind.kind) + "s"
}

// mutationVerdictMessage names the reason the agent gave for a non-converged
// verdict; the relay answers HTTP 200 for every verdict, so the message is
// the only thing that separates an RBAC deny from a wrong kind.
func mutationVerdictMessage(response *client.ResourceMutationResponse) string {
	if response.Message != nil && *response.Message != "" {
		return *response.Message
	}
	return "status " + response.Status
}

func init() {
	clusterDeleteCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace (required for namespaced kinds)")
	clusterDeleteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	clusterDeleteCmd.Flags().Bool("dry-run", false, "Report what would be deleted without deleting anything")
	clusterDeleteCmd.Flags().Int("grace-period", -1,
		"Seconds the object gets to shut down cleanly; 0 deletes it immediately, -1 keeps the object's own grace period")
	clusterDeleteCmd.Flags().String("group", "", "API group for a kind outside the built-in set (e.g. cert-manager.io)")
	clusterDeleteCmd.Flags().String("api-version", "", "API version for a kind outside the built-in set (e.g. v1, v1beta1)")

	clusterCmd.AddCommand(clusterDeleteCmd)
}

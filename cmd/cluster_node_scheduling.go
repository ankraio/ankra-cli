package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

var clusterCordonCmd = &cobra.Command{
	Use:   "cordon <node>",
	Short: "Mark a node unschedulable",
	Long: `Mark a node unschedulable so no new pods land on it. Pods already running
stay where they are; 'cluster drain' moves them off. 'cluster uncordon' puts
the node back into service.

Example:
  ankra cluster cordon worker-3`,
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"group": "kubernetes"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		return setNodeUnschedulable(cmd, cluster.ID, args[0], true)
	},
}

var clusterUncordonCmd = &cobra.Command{
	Use:   "uncordon <node>",
	Short: "Mark a node schedulable again",
	Long: `Clear the unschedulable mark 'cluster cordon' or 'cluster drain' set, so the
scheduler can place pods on the node again.

Example:
  ankra cluster uncordon worker-3`,
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"group": "kubernetes"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		return setNodeUnschedulable(cmd, cluster.ID, args[0], false)
	},
}

var clusterDrainCmd = &cobra.Command{
	Use:   "drain <node>",
	Short: "Cordon a node and delete the pods running on it",
	Long: `Cordon a node, then delete every pod scheduled on it so its controller
reschedules the pod elsewhere. DaemonSet pods are left alone (they would
come straight back on the same node) and so are the static pods the kubelet
runs from disk. A pod without a controller is deleted like any other and is
not recreated - the plan names every pod before anything happens, and
--dry-run stops after printing it.

The node stays cordoned afterwards; run 'cluster uncordon' to put it back
into service.

Examples:
  ankra cluster drain worker-3
  ankra cluster drain worker-3 --grace-period 30 --yes
  ankra cluster drain worker-3 --dry-run`,
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"group": "kubernetes"},
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeName := args[0]
		yes, _ := cmd.Flags().GetBool("yes")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		var gracePeriodSeconds *int
		if cmd.Flags().Changed("grace-period") {
			gracePeriod, _ := cmd.Flags().GetInt("grace-period")
			if gracePeriod < 0 {
				return withExitCode(exitUsage, fmt.Errorf("--grace-period must be zero or a positive number of seconds"))
			}
			gracePeriodSeconds = &gracePeriod
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		plan, err := planNodeDrain(cluster.ID, nodeName)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		printDrainPlan(out, nodeName, plan)
		if dryRun {
			return nil
		}

		prompt := fmt.Sprintf("Drain node %q on cluster %q (cordon it and delete %d pods)? [y/N]: ",
			nodeName, cluster.Name, len(plan.evictable))
		if err := confirmPrompt(cmd.InOrStdin(), out, prompt, yes); err != nil {
			return err
		}

		if err := setNodeUnschedulable(cmd, cluster.ID, nodeName, true); err != nil {
			return err
		}
		if len(plan.evictable) == 0 {
			_, _ = fmt.Fprintf(out, "node %q drained: nothing to delete\n", nodeName)
			return nil
		}

		var refused []string
		for _, pod := range plan.evictable {
			response, err := apiClient.DeleteResource(cluster.ID, client.DeleteResourceRequest{
				Kind:               "Pod",
				Version:            "v1",
				Namespace:          pod.namespace,
				Name:               pod.name,
				GracePeriodSeconds: gracePeriodSeconds,
			})
			if err != nil {
				return fmt.Errorf("deleting pod %s: %w", pod, err)
			}
			switch response.Status {
			case "success", "not_found":
				_, _ = fmt.Fprintf(out, "pod %s deleted\n", pod)
			default:
				refused = append(refused, pod.String()+": "+mutationVerdictMessage(response))
			}
		}
		if len(refused) > 0 {
			return fmt.Errorf("node %q is cordoned but %d of %d pods could not be deleted:\n  %s",
				nodeName, len(refused), len(plan.evictable), strings.Join(refused, "\n  "))
		}
		_, _ = fmt.Fprintf(out, "node %q drained: %d pods deleted\n", nodeName, len(plan.evictable))
		return nil
	},
}

// setNodeUnschedulable flips spec.unschedulable through the patch relay -
// the same call the portal's cordon toggle makes.
func setNodeUnschedulable(cmd *cobra.Command, clusterID, nodeName string, unschedulable bool) error {
	verb := "cordoned"
	if !unschedulable {
		verb = "uncordoned"
	}
	response, err := apiClient.PatchResource(clusterID, client.PatchResourceRequest{
		Kind:      "Node",
		Version:   "v1",
		Name:      nodeName,
		Patch:     map[string]interface{}{"spec": map[string]interface{}{"unschedulable": unschedulable}},
		PatchType: "strategic",
	})
	if err != nil {
		return fmt.Errorf("updating node %q: %w", nodeName, err)
	}
	switch response.Status {
	case "success":
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "node %q %s\n", nodeName, verb)
		return nil
	case "not_found":
		return withExitCode(exitNotFound, fmt.Errorf("node %q not found", nodeName))
	default:
		return fmt.Errorf("node %q could not be %s: %s", nodeName, verb, mutationVerdictMessage(response))
	}
}

type podReference struct {
	namespace string
	name      string
}

func (reference podReference) String() string {
	return reference.namespace + "/" + reference.name
}

// drainPlan splits the pods on a node into the ones a drain deletes and the
// ones it leaves in place, with the reason for each skip.
type drainPlan struct {
	evictable []podReference
	skipped   map[string][]podReference
}

func planNodeDrain(clusterID, nodeName string) (drainPlan, error) {
	response, err := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{{
			Kind:           "Pod",
			Version:        "v1",
			FieldSelectors: []client.FieldSelector{{Field: "spec.nodeName", Value: nodeName}},
		}},
		SkipCache: true,
	})
	if err != nil {
		return drainPlan{}, fmt.Errorf("listing pods on node %q: %w", nodeName, err)
	}
	plan := drainPlan{skipped: map[string][]podReference{}}
	if len(response.ResourceResponses) == 0 {
		return plan, nil
	}
	for _, item := range response.ResourceResponses[0].Items {
		pod, isObject := item.(map[string]interface{})
		if !isObject || getNestedString(pod, "spec", "nodeName") != nodeName {
			continue
		}
		reference := podReference{
			namespace: getNestedString(pod, "metadata", "namespace"),
			name:      getNestedString(pod, "metadata", "name"),
		}
		if reason := drainSkipReason(pod); reason != "" {
			plan.skipped[reason] = append(plan.skipped[reason], reference)
			continue
		}
		plan.evictable = append(plan.evictable, reference)
	}
	return plan, nil
}

// drainSkipReason mirrors kubectl drain's defaults: a DaemonSet pod is
// recreated on the same node the moment it is deleted, and a static pod
// belongs to the kubelet, not the API server.
func drainSkipReason(pod map[string]interface{}) string {
	metadata, hasMetadata := getNestedMap(pod, "metadata")
	if !hasMetadata {
		return ""
	}
	if annotations, hasAnnotations := getNestedMap(metadata, "annotations"); hasAnnotations {
		if _, isMirror := annotations["kubernetes.io/config.mirror"]; isMirror {
			return "static pod"
		}
	}
	owners, hasOwners := metadata["ownerReferences"].([]interface{})
	if !hasOwners {
		return ""
	}
	for _, rawOwner := range owners {
		owner, isObject := rawOwner.(map[string]interface{})
		if !isObject {
			continue
		}
		switch getNestedString(owner, "kind") {
		case "DaemonSet":
			return "DaemonSet pod"
		case "Node":
			return "static pod"
		}
	}
	return ""
}

func printDrainPlan(out io.Writer, nodeName string, plan drainPlan) {
	if len(plan.evictable) == 0 {
		_, _ = fmt.Fprintf(out, "No pods to delete on node %q.\n", nodeName)
	} else {
		_, _ = fmt.Fprintf(out, "%d pods on node %q will be deleted:\n", len(plan.evictable), nodeName)
		for _, pod := range plan.evictable {
			_, _ = fmt.Fprintf(out, "  %s\n", pod)
		}
	}
	reasons := make([]string, 0, len(plan.skipped))
	for reason := range plan.skipped {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		pods := plan.skipped[reason]
		names := make([]string, 0, len(pods))
		for _, pod := range pods {
			names = append(names, pod.String())
		}
		_, _ = fmt.Fprintf(out, "Skipping %d (%s): %s\n", len(pods), reason, strings.Join(names, ", "))
	}
}

func init() {
	clusterDrainCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	clusterDrainCmd.Flags().Bool("dry-run", false, "Print the plan without cordoning or deleting anything")
	clusterDrainCmd.Flags().Int("grace-period", -1,
		"Seconds each pod gets to shut down cleanly; 0 deletes immediately, -1 keeps each pod's own grace period")

	clusterCmd.AddCommand(clusterCordonCmd)
	clusterCmd.AddCommand(clusterUncordonCmd)
	clusterCmd.AddCommand(clusterDrainCmd)
}

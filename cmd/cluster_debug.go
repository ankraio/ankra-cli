package cmd

import (
	"errors"
	"fmt"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

// ankra cluster debug: a pod Ankra creates in a namespace that impersonates
// another pod - same service account, node, volumes and mounts, environment -
// under an image chosen for its tools, so a distroless or crash-looping
// workload can be inspected without an ephemeral container mutating it. The
// platform builds the mirror and records every terminal session into it.

const (
	debugPodDefaultTTL = time.Hour
	debugPodMinimumTTL = time.Minute
	debugPodMaximumTTL = 8 * time.Hour
)

var clusterDebugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Spin up, list and remove debug pods",
	Long: `Debug pods are pods Ankra creates on your behalf, in any namespace of the
active cluster, from an image chosen for its tools rather than for the
workload. Point one at an existing pod with --from-pod and it impersonates
that pod: the same service account, node, volumes and volume mounts,
environment variables and envFrom sources, tolerations and security
context - under an image that actually has curl, dig, psql or strace in it.
The workload itself is never touched.

Labels are never copied, so a debug pod never sits behind the workload's
Service. Every debug pod carries a lifetime the kubelet enforces on its own,
and every terminal session into one is recorded to the audit log.`,
	Annotations: map[string]string{"group": "kubernetes"},
}

var clusterDebugImagesCmd = &cobra.Command{
	Use:   "images",
	Short: "List the debug image catalogue",
	Long: `List the tag-pinned images the platform offers for debug pods. Any image
reference is accepted by "debug create --image"; the catalogue is the set
that ships with tools and is known to pull.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		catalogue, err := apiClient.ListDebugPodImages(cluster.ID)
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, catalogue.Images); handled || renderError != nil {
			return renderError
		}
		renderer := table.NewWriter()
		renderer.SetOutputMirror(cmd.OutOrStdout())
		renderer.AppendHeader(table.Row{"NAME", "IMAGE", "DEFAULT", "DESCRIPTION"})
		for _, image := range catalogue.Images {
			isDefault := ""
			if image.IsDefault {
				isDefault = "yes"
			}
			renderer.AppendRow(table.Row{image.Name, image.Image, isDefault, image.Description})
		}
		renderer.SetStyle(table.StyleLight)
		renderer.Render()
		return nil
	},
}

var clusterDebugCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a debug pod, optionally impersonating an existing pod",
	Long: `Create a debug pod in a namespace of the active cluster and wait for it to
start. With --from-pod the pod impersonates the named pod - its service
account, node, volumes and mounts, environment - so what the workload's
container sees, the debug pod sees. Without --from-pod it is a plain pod of
the chosen image with no service-account token.

The command prints the portal link to the new pod's terminal; every session
opened there is recorded to the audit log. Creating a debug pod needs the
kubernetes.write and kubernetes.exec permissions.

Examples:
  ankra cluster debug create -n payments --from-pod api-6d8f9c7b5-x2kq9
  ankra cluster debug create -n payments --from-pod api-6d8f9c7b5-x2kq9 --container sidecar --no-env
  ankra cluster debug create -n payments --image docker.io/library/alpine:3.21 --ttl 2h
  ankra cluster debug create -n payments --from-pod api-6d8f9c7b5-x2kq9 -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace, _ := cmd.Flags().GetString("namespace")
		fromPod, _ := cmd.Flags().GetString("from-pod")
		container, _ := cmd.Flags().GetString("container")
		image, _ := cmd.Flags().GetString("image")
		noMounts, _ := cmd.Flags().GetBool("no-mounts")
		noEnvironment, _ := cmd.Flags().GetBool("no-env")
		ttl, _ := cmd.Flags().GetDuration("ttl")
		attach, _ := cmd.Flags().GetBool("attach")
		shell, _ := cmd.Flags().GetString("shell")

		if namespace == "" {
			return withExitCode(exitUsage, errors.New("--namespace (-n) is required"))
		}
		if attach && cmd.Flags().Changed("output") {
			return withExitCode(exitUsage, errors.New("--attach opens an interactive terminal and cannot be combined with -o"))
		}
		if fromPod == "" && container != "" {
			return withExitCode(exitUsage, errors.New("--container only means something with --from-pod"))
		}
		if fromPod == "" && (noMounts || noEnvironment) {
			return withExitCode(exitUsage, errors.New("--no-mounts and --no-env only mean something with --from-pod: nothing is mirrored without one"))
		}
		if ttl < debugPodMinimumTTL || ttl > debugPodMaximumTTL {
			return withExitCode(exitUsage, fmt.Errorf("--ttl must be between %s and %s", debugPodMinimumTTL, debugPodMaximumTTL))
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		created, err := apiClient.CreateDebugPod(cluster.ID, client.CreateDebugPodRequest{
			Namespace:           namespace,
			TargetPodName:       fromPod,
			TargetContainerName: container,
			Image:               image,
			MirrorVolumeMounts:  !noMounts,
			MirrorEnvironment:   !noEnvironment,
			TTLSeconds:          int(ttl / time.Second),
		})
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, created); handled || renderError != nil {
			return renderError
		}
		renderDebugPodCreated(cmd, cluster.ID, created)
		if !attach {
			return nil
		}
		if !created.Ready {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "The pod is not running yet; open it once it is with:\n  ankra cluster terminal %s -n %s -c %s\n",
				created.PodName, created.Namespace, created.ContainerName)
			return nil
		}
		return runPodTerminal(cmd, cluster.ID, client.PodTerminalRequest{
			Namespace:     created.Namespace,
			PodName:       created.PodName,
			ContainerName: created.ContainerName,
			Shell:         shell,
		})
	},
}

func renderDebugPodCreated(cmd *cobra.Command, clusterID string, created *client.DebugPodResponse) {
	out := cmd.OutOrStdout()
	state := "created"
	if created.Ready {
		state = "running"
	}
	_, _ = fmt.Fprintf(out, "%s Debug pod %s %s in %s\n",
		text.FgGreen.Sprint("✓"), text.Bold.Sprint(created.PodName), state, created.Namespace)
	_, _ = fmt.Fprintf(out, "  Image:     %s\n", created.Image)
	if created.NodeName != "" {
		_, _ = fmt.Fprintf(out, "  Node:      %s\n", created.NodeName)
	}
	if created.TargetPodName != nil {
		targetContainer := ""
		if created.TargetContainerName != nil {
			targetContainer = " (container " + *created.TargetContainerName + ")"
		}
		_, _ = fmt.Fprintf(out, "  Mirrors:   %s%s\n", *created.TargetPodName, targetContainer)
		_, _ = fmt.Fprintf(out, "  Mirrored:  %d volume mount(s), %d env var(s), %d envFrom source(s)\n",
			len(created.MirroredVolumeMounts), created.MirroredEnvironmentVariables, created.MirroredEnvironmentSources)
	}
	_, _ = fmt.Fprintf(out, "  Expires:   %s\n", created.ExpiresAt)
	for _, warning := range created.Warnings {
		_, _ = fmt.Fprintf(out, "  %s %s\n", text.FgYellow.Sprint("!"), warning)
	}
	_, _ = fmt.Fprintf(out, "\nOpen a terminal in the portal (every session is recorded to the audit log):\n  %s\n",
		debugPodTerminalPath(clusterID, created.Namespace, created.PodName))
}

// debugPodTerminalPath is the portal page the CLI hands over to, as a path
// under the portal's own host: an interactive terminal from the CLI itself
// is a later piece.
func debugPodTerminalPath(clusterID string, namespace string, podName string) string {
	return "/organisation/clusters/cluster/imported/" + clusterID +
		"/kubernetes/workloads/pods/" + namespace + "/" + podName + "?activeTab=terminal"
}

var clusterDebugListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the debug pods on the active cluster",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace, _ := cmd.Flags().GetString("namespace")
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		listing, err := apiClient.ListDebugPods(cluster.ID, namespace)
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, listing.DebugPods); handled || renderError != nil {
			return renderError
		}
		if len(listing.DebugPods) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No debug pods on this cluster.")
			return nil
		}
		renderer := table.NewWriter()
		renderer.SetOutputMirror(cmd.OutOrStdout())
		renderer.AppendHeader(table.Row{"NAMESPACE", "NAME", "PHASE", "MIRRORS", "IMAGE", "REQUESTED BY", "EXPIRES"})
		for _, pod := range listing.DebugPods {
			mirrors := "-"
			if pod.TargetPodName != nil {
				mirrors = *pod.TargetPodName
			}
			expires := pod.ExpiresAt
			if pod.IsExpired {
				expires = text.FgYellow.Sprint(expires + " (expired)")
			}
			renderer.AppendRow(table.Row{pod.Namespace, pod.PodName, pod.Phase, mirrors, pod.Image, pod.RequestedBy, expires})
		}
		renderer.SetStyle(table.StyleLight)
		renderer.Render()
		return nil
	},
}

var clusterDebugDeleteCmd = &cobra.Command{
	Use:   "delete <pod_name>",
	Short: "Delete a debug pod",
	Long: `Delete a debug pod before its lifetime ends. Only a pod carrying the
ankra.io/debug-pod label can be deleted this way; a workload pod answers
not found.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace, _ := cmd.Flags().GetString("namespace")
		yes, _ := cmd.Flags().GetBool("yes")
		if namespace == "" {
			return withExitCode(exitUsage, errors.New("--namespace (-n) is required"))
		}
		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
			fmt.Sprintf("Delete debug pod %s in %s? [y/N]: ", args[0], namespace), yes); confirmError != nil {
			return confirmError
		}
		outcome, err := apiClient.DeleteDebugPod(cluster.ID, namespace, args[0])
		if err != nil {
			return err
		}
		if handled, renderError := renderStructured(cmd, outcome); handled || renderError != nil {
			return renderError
		}
		if outcome.Status == "not_found" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Debug pod %s was already gone from %s.\n", args[0], namespace)
			return nil
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s Deleted debug pod %s in %s\n", text.FgGreen.Sprint("✓"), args[0], namespace)
		return nil
	},
}

func init() {
	clusterDebugCreateCmd.Flags().StringP("namespace", "n", "", "Namespace to create the debug pod in (required)")
	clusterDebugCreateCmd.Flags().String("from-pod", "", "Pod to impersonate: its service account, node, volumes, mounts and environment are mirrored")
	clusterDebugCreateCmd.Flags().StringP("container", "c", "", "Container of --from-pod whose mounts and environment to mirror (default: the first)")
	clusterDebugCreateCmd.Flags().String("image", "", "Debug image (default: the catalogue's default; see \"debug images\")")
	clusterDebugCreateCmd.Flags().Bool("no-mounts", false, "Do not mirror the target's volumes and volume mounts")
	clusterDebugCreateCmd.Flags().Bool("no-env", false, "Do not mirror the target's environment variables")
	clusterDebugCreateCmd.Flags().Duration("ttl", debugPodDefaultTTL, "How long the pod lives before the kubelet ends it (1m-8h)")
	clusterDebugCreateCmd.Flags().BoolP("attach", "a", false, "Open a shell in the debug pod as soon as it runs (see \"cluster terminal\")")
	clusterDebugCreateCmd.Flags().String("shell", podTerminalDefaultShell, "Shell to start with --attach")

	clusterDebugListCmd.Flags().StringP("namespace", "n", "", "Only list debug pods in this namespace")

	clusterDebugDeleteCmd.Flags().StringP("namespace", "n", "", "Namespace of the debug pod (required)")
	clusterDebugDeleteCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	registerStructuredOutputFlags(clusterDebugImagesCmd, clusterDebugCreateCmd, clusterDebugListCmd, clusterDebugDeleteCmd)

	clusterDebugCmd.AddCommand(clusterDebugImagesCmd)
	clusterDebugCmd.AddCommand(clusterDebugCreateCmd)
	clusterDebugCmd.AddCommand(clusterDebugListCmd)
	clusterDebugCmd.AddCommand(clusterDebugDeleteCmd)
	clusterCmd.AddCommand(clusterDebugCmd)
}

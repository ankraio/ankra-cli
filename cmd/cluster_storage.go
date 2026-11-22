package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// The typed storage writes on the active cluster: creating and deleting a
// StorageClass and deleting PersistentVolumes, the CLI side of the platform's
// create_storage_class, delete_storage_class and delete_persistent_volumes.
// They ride the cluster agent's apply and delete relay, behind
// kubernetes.write like 'cluster delete'; what they add is the manifest built
// from flags, the confirmation, and the refusal to delete a volume a claim
// still holds.

// newClusterStorageClassesCommand builds `ankra cluster storage-classes`.
func newClusterStorageClassesCommand() *cobra.Command {
	command := &cobra.Command{
		Use:         "storage-classes",
		Aliases:     []string{"storageclasses", "sc"},
		Short:       "Create or delete StorageClasses on the active cluster",
		Annotations: map[string]string{"group": "kubernetes"},
		Long: `Create or delete StorageClasses on the active cluster through its Ankra
agent. List them with 'ankra cluster get storageclasses'. Both need
kubernetes.write on the cluster.`,
	}

	create := &cobra.Command{
		Use:   "create <name> --provisioner <provisioner>",
		Short: "Create a StorageClass",
		Long: `Create a StorageClass from flags. --provisioner names the CSI driver that
cuts the volumes (driver.longhorn.io, csi.hetzner.cloud, ebs.csi.aws.com,
...); --parameter passes the driver's own settings and repeats.

  ankra cluster storage-classes create fast --provisioner csi.hetzner.cloud --reclaim-policy Retain
  ankra cluster storage-classes create replicated --provisioner driver.longhorn.io \
    --parameter numberOfReplicas=3 --allow-volume-expansion --volume-binding-mode WaitForFirstConsumer`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, manifestError := storageClassManifestFromFlags(cmd, args[0])
			if manifestError != nil {
				return manifestError
			}
			cluster, clusterError := resolveActiveCluster(cmd)
			if clusterError != nil {
				return clusterError
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			response, applyError := apiClient.ApplyResource(cluster.ID, client.ApplyResourceRequest{Manifest: manifest, DryRun: dryRun})
			if applyError != nil {
				return fmt.Errorf("creating storageclass %q: %w", args[0], applyError)
			}
			if rendered, renderError := renderStructured(cmd, response); renderError != nil {
				return renderError
			} else if !rendered {
				switch response.Status {
				case "success":
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "storageclass %q created on cluster %q\n", args[0], cluster.Name)
				case "dry_run":
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "storageclass %q would be created on cluster %q (dry run)\n", args[0], cluster.Name)
				}
			}
			if response.Status != "success" && response.Status != "dry_run" {
				return fmt.Errorf("the cluster refused storageclass %q: %s", args[0], mutationVerdictMessage(response))
			}
			return nil
		},
	}
	create.Flags().String("provisioner", "", "CSI driver that provisions the volumes (required)")
	create.Flags().String("reclaim-policy", "", "Delete or Retain (the cluster's default is Delete)")
	create.Flags().String("volume-binding-mode", "", "Immediate or WaitForFirstConsumer")
	create.Flags().Bool("allow-volume-expansion", false, "Let claims of this class grow")
	create.Flags().StringArray("parameter", nil, "Driver parameter as key=value (repeatable)")
	create.Flags().StringArray("mount-option", nil, "Mount option for the volumes (repeatable)")
	create.Flags().Bool("default", false, "Mark this class the cluster's default")
	create.Flags().Bool("dry-run", false, "Report what would be created without creating anything")
	_ = create.MarkFlagRequired("provisioner")
	registerStructuredOutputFlags(create)

	remove := &cobra.Command{
		Use:   "delete <name> [name...]",
		Short: "Delete StorageClasses",
		Long: `Delete one or more StorageClasses. Volumes already provisioned from a class
keep running; new claims naming it stay Pending until a class of that name
exists again. The command asks first (--yes skips, declining exits 4); each
class reports its own outcome, and one that did not exist exits 3.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTypedStorageDelete(cmd, "storageclass", args, nil)
		},
	}
	remove.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	remove.Flags().Bool("dry-run", false, "Report what would be deleted without deleting anything")

	command.AddCommand(create, remove)
	return command
}

// newClusterPersistentVolumesCommand builds `ankra cluster persistent-volumes`.
func newClusterPersistentVolumesCommand() *cobra.Command {
	command := &cobra.Command{
		Use:         "persistent-volumes",
		Aliases:     []string{"persistentvolumes", "pv"},
		Short:       "Delete PersistentVolumes on the active cluster",
		Annotations: map[string]string{"group": "kubernetes"},
		Long: `Delete PersistentVolumes on the active cluster through its Ankra agent.
List them with 'ankra cluster get persistentvolumes'. Needs kubernetes.write
on the cluster.`,
	}

	remove := &cobra.Command{
		Use:   "delete <name> [name...]",
		Short: "Delete PersistentVolumes no claim holds",
		Long: `Delete one or more PersistentVolumes. Each volume's phase is read first: a
Bound volume still backs a claim, and deleting it takes the claim's data
away from the workload using it, so the command refuses it (exit 8, nothing
deleted) unless --force. Released, Available and Failed volumes are deleted;
their storage is then released according to the reclaim policy.

The command asks first (--yes skips, declining exits 4); each volume reports
its own outcome, and one that did not exist exits 3.

  ankra cluster persistent-volumes delete pvc-1f2e3d4c --yes`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			return runTypedStorageDelete(cmd, "persistentvolume", args, func(clusterID string) error {
				if force {
					return nil
				}
				return refuseBoundPersistentVolumes(clusterID, args)
			})
		},
	}
	remove.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	remove.Flags().Bool("dry-run", false, "Report what would be deleted without deleting anything")
	remove.Flags().Bool("force", false, "Delete a volume even while a claim still holds it (Bound)")

	command.AddCommand(remove)
	return command
}

// runTypedStorageDelete deletes names of one cluster-scoped storage kind on
// the active cluster: preflight (may be nil) runs before the prompt, so a
// refusal changes nothing and asks nothing.
func runTypedStorageDelete(cmd *cobra.Command, rawKind string, names []string, preflight func(clusterID string) error) error {
	kind, kindError := resolveK8sKind(rawKind, "", "")
	if kindError != nil {
		return kindError
	}
	cluster, clusterError := resolveActiveCluster(cmd)
	if clusterError != nil {
		return clusterError
	}
	if preflight != nil {
		if preflightError := preflight(cluster.ID); preflightError != nil {
			return preflightError
		}
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if !dryRun {
		yes, _ := cmd.Flags().GetBool("yes")
		if confirmError := confirmPrompt(cmd.InOrStdin(), cmd.ErrOrStderr(),
			deleteResourcesPrompt(kind, names, "", cluster.Name), yes); confirmError != nil {
			return confirmError
		}
	}
	return runResourceMutations(cmd, kind, names, "", deleteWording,
		func(name string) (*client.ResourceMutationResponse, error) {
			return apiClient.DeleteResource(cluster.ID, client.DeleteResourceRequest{
				Kind:     kind.kind,
				Group:    kind.group,
				Version:  kind.version,
				Resource: kind.resource,
				Name:     name,
				DryRun:   dryRun,
			})
		})
}

// refuseBoundPersistentVolumes reads each named volume's phase live and
// refuses the delete, before anything is sent, when one is Bound or when a
// phase could not be read: the check never passes on a read it did not make.
// A volume the cluster does not have is left for the delete to report.
func refuseBoundPersistentVolumes(clusterID string, names []string) error {
	requests := make([]client.ResourceRequestItem, 0, len(names))
	for _, name := range names {
		requests = append(requests, client.ResourceRequestItem{Kind: "PersistentVolume", Version: "v1", Name: name})
	}
	response, readError := apiClient.GetResources(clusterID, client.GetResourcesRequest{ResourceRequests: requests, SkipCache: true})
	if readError != nil {
		return fmt.Errorf("reading the volumes' phase before deleting them: %w", readError)
	}
	var bound, unread []string
	for _, item := range response.ResourceResponses {
		name := ""
		if item.Name != nil {
			name = *item.Name
		}
		if item.NotFound {
			continue
		}
		if item.Status == "error" || len(item.Items) == 0 {
			unread = append(unread, name)
			continue
		}
		phase, claim := persistentVolumePhase(item.Items[0])
		if phase == "Bound" {
			bound = append(bound, fmt.Sprintf("%s (bound to %s)", name, claim))
		}
	}
	if len(unread) > 0 {
		sort.Strings(unread)
		return fmt.Errorf("could not read the phase of %s, so nothing was deleted; retry, or pass --force to delete without the check",
			strings.Join(unread, ", "))
	}
	if len(bound) > 0 {
		return withExitCode(exitBlocked, fmt.Errorf("a claim still holds %s, so nothing was deleted: delete the claim first, or pass --force",
			strings.Join(bound, ", ")))
	}
	return nil
}

// persistentVolumePhase reads status.phase and the claim a PersistentVolume
// object is bound to.
func persistentVolumePhase(object any) (string, string) {
	document, _ := object.(map[string]interface{})
	status, _ := document["status"].(map[string]interface{})
	phase, _ := status["phase"].(string)
	spec, _ := document["spec"].(map[string]interface{})
	claimReference, _ := spec["claimRef"].(map[string]interface{})
	claimNamespace, _ := claimReference["namespace"].(string)
	claimName, _ := claimReference["name"].(string)
	claim := claimName
	if claimNamespace != "" {
		claim = claimNamespace + "/" + claimName
	}
	return phase, claim
}

// storageClassManifestFromFlags builds the StorageClass `create` applies.
func storageClassManifestFromFlags(cmd *cobra.Command, name string) (map[string]any, error) {
	provisioner, _ := cmd.Flags().GetString("provisioner")
	if strings.TrimSpace(provisioner) == "" {
		return nil, withExitCode(exitUsage, errors.New("--provisioner is required"))
	}
	metadata := map[string]any{"name": name}
	if isDefault, _ := cmd.Flags().GetBool("default"); isDefault {
		metadata["annotations"] = map[string]any{"storageclass.kubernetes.io/is-default-class": "true"}
	}
	manifest := map[string]any{
		"apiVersion":  "storage.k8s.io/v1",
		"kind":        "StorageClass",
		"metadata":    metadata,
		"provisioner": provisioner,
	}
	if reclaimPolicy, _ := cmd.Flags().GetString("reclaim-policy"); reclaimPolicy != "" {
		if reclaimPolicy != "Delete" && reclaimPolicy != "Retain" {
			return nil, withExitCode(exitUsage, fmt.Errorf("--reclaim-policy must be Delete or Retain, got %q", reclaimPolicy))
		}
		manifest["reclaimPolicy"] = reclaimPolicy
	}
	if bindingMode, _ := cmd.Flags().GetString("volume-binding-mode"); bindingMode != "" {
		if bindingMode != "Immediate" && bindingMode != "WaitForFirstConsumer" {
			return nil, withExitCode(exitUsage, fmt.Errorf("--volume-binding-mode must be Immediate or WaitForFirstConsumer, got %q", bindingMode))
		}
		manifest["volumeBindingMode"] = bindingMode
	}
	if allowExpansion, _ := cmd.Flags().GetBool("allow-volume-expansion"); allowExpansion {
		manifest["allowVolumeExpansion"] = true
	}
	rawParameters, _ := cmd.Flags().GetStringArray("parameter")
	if len(rawParameters) > 0 {
		parameters := map[string]any{}
		for _, rawParameter := range rawParameters {
			key, value, hasSeparator := strings.Cut(rawParameter, "=")
			if !hasSeparator || strings.TrimSpace(key) == "" {
				return nil, withExitCode(exitUsage, fmt.Errorf("--parameter takes key=value, got %q", rawParameter))
			}
			parameters[strings.TrimSpace(key)] = value
		}
		manifest["parameters"] = parameters
	}
	if mountOptions, _ := cmd.Flags().GetStringArray("mount-option"); len(mountOptions) > 0 {
		options := make([]any, 0, len(mountOptions))
		for _, option := range mountOptions {
			options = append(options, option)
		}
		manifest["mountOptions"] = options
	}
	return manifest, nil
}

func init() {
	clusterCmd.AddCommand(newClusterStorageClassesCommand(), newClusterPersistentVolumesCommand())
}

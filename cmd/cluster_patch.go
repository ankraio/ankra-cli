package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ankra/internal/client"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// patchTypeLabels spells out each patch type the relay accepts, in the words
// the confirmation prompt and the usage error use. The keys are the exact
// values the platform's resources/patch route and the agent understand.
var patchTypeLabels = map[string]string{
	"strategic": "strategic merge patch",
	"merge":     "JSON merge patch",
	"json":      "JSON patch",
}

var patchWording = mutationWording{noun: "patch", pastTense: "patched", progressive: "patching"}

var clusterPatchCmd = &cobra.Command{
	Use:   "patch <kind> <name> [name...]",
	Short: "Patch live Kubernetes resources in the active cluster",
	Long: `Patch one or more live Kubernetes resources in the active cluster.

This is 'kubectl patch': the document given with --patch (inline, JSON or
YAML) or --patch-file is sent to the API server as a patch of the chosen
--type - 'strategic' (the default, the kubectl default too), 'merge' (RFC
7386 JSON merge patch) or 'json' (RFC 6902 JSON patch, a list of operations).
A patch changes only the fields it names and does not go through
server-side apply, so it is the way to correct a field on an object another
manager owns - a value the API server defaulted on an older chart, a label a
release left behind - without taking that object over or deleting it.

The kind takes the kubectl spellings - pod, deployment, deploy, service, svc,
configmap, cm, node, ... - and a custom resource is reachable with --group
and --api-version, exactly like 'cluster get resources' and 'cluster delete'.
The patch goes through the cluster's Ankra agent: no kubeconfig is needed and
the same organisation permissions as the portal apply.

Each object reports its own outcome. The command exits 0 when everything was
patched, 3 when one of the objects did not exist, and 1 when the cluster
refused a patch.

Examples:
  ankra cluster patch deployment web -n prod --patch '{"spec":{"replicas":3}}'
  ankra cluster patch service redis-headless -n data --type merge \
    --patch '{"spec":{"ipFamilyPolicy":"SingleStack","ipFamilies":["IPv4"]}}'
  ankra cluster patch configmap settings -n prod --patch-file settings-patch.yaml --yes
  ankra cluster patch deployment web -n prod --type json \
    --patch '[{"op":"remove","path":"/metadata/annotations/deprecated"}]'
  ankra cluster patch node worker-1 --patch '{"metadata":{"labels":{"tier":"gpu"}}}' --dry-run
  ankra cluster patch Certificate web-tls -n prod --group cert-manager.io --api-version v1 \
    --patch '{"spec":{"renewBefore":"720h"}}'`,
	Args:        cobra.MinimumNArgs(2),
	Annotations: map[string]string{"group": "kubernetes"},
	RunE: func(cmd *cobra.Command, args []string) error {
		namespace, _ := cmd.Flags().GetString("namespace")
		yes, _ := cmd.Flags().GetBool("yes")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		apiGroup, _ := cmd.Flags().GetString("group")
		apiVersion, _ := cmd.Flags().GetString("api-version")
		inlinePatch, _ := cmd.Flags().GetString("patch")
		patchFile, _ := cmd.Flags().GetString("patch-file")
		patchType, _ := cmd.Flags().GetString("type")

		resolvedKind, kindError := resolveK8sKind(args[0], apiGroup, apiVersion)
		if kindError != nil {
			return kindError
		}
		names := args[1:]
		if !resolvedKind.clusterScoped && namespace == "" {
			return withExitCode(exitUsage, fmt.Errorf("--namespace (-n) is required to patch a %s", resolvedKind.kind))
		}
		if resolvedKind.clusterScoped {
			namespace = ""
		}

		patchType = strings.ToLower(strings.TrimSpace(patchType))
		if _, isKnownPatchType := patchTypeLabels[patchType]; !isKnownPatchType {
			return withExitCode(exitUsage, fmt.Errorf(
				"--type must be strategic, merge or json, got %q", patchType))
		}
		patch, patchError := loadPatchDocument(inlinePatch, patchFile, patchType)
		if patchError != nil {
			return patchError
		}

		cluster, err := resolveActiveCluster(cmd)
		if err != nil {
			return err
		}

		if !dryRun {
			prompt := patchResourcesPrompt(resolvedKind, names, namespace, cluster.Name, patchType)
			if err := confirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), prompt, yes); err != nil {
				return err
			}
		}

		return runResourceMutations(cmd, resolvedKind, names, namespace, patchWording,
			func(name string) (*client.ResourceMutationResponse, error) {
				return apiClient.PatchResource(cluster.ID, client.PatchResourceRequest{
					Kind:      resolvedKind.kind,
					Group:     resolvedKind.group,
					Version:   resolvedKind.version,
					Resource:  resolvedKind.resource,
					Namespace: namespace,
					Name:      name,
					Patch:     patch,
					PatchType: patchType,
					DryRun:    dryRun,
				})
			})
	},
}

// loadPatchDocument reads the patch from exactly one of --patch or
// --patch-file and checks it has the shape its type needs before anything
// reaches the cluster: a JSON patch is a list of operations, the two merge
// patches are an object. YAML is accepted because JSON is YAML, so the same
// flag takes either.
func loadPatchDocument(inlinePatch string, patchFile string, patchType string) (interface{}, error) {
	hasInline := strings.TrimSpace(inlinePatch) != ""
	hasFile := strings.TrimSpace(patchFile) != ""
	switch {
	case hasInline && hasFile:
		return nil, withExitCode(exitUsage, errors.New("--patch and --patch-file are mutually exclusive"))
	case !hasInline && !hasFile:
		return nil, withExitCode(exitUsage, errors.New("a patch is required: pass --patch '<json>' or --patch-file <path>"))
	}

	document := []byte(inlinePatch)
	source := "--patch"
	if hasFile {
		content, readError := os.ReadFile(patchFile)
		if readError != nil {
			return nil, withExitCode(exitUsage, fmt.Errorf("could not read the patch file: %w", readError))
		}
		document = content
		source = fmt.Sprintf("patch file %q", patchFile)
	}

	var patch interface{}
	if unmarshalError := yaml.Unmarshal(document, &patch); unmarshalError != nil {
		return nil, withExitCode(exitUsage, fmt.Errorf("%s is not valid JSON or YAML: %w", source, unmarshalError))
	}
	if patch == nil {
		return nil, withExitCode(exitUsage, fmt.Errorf("%s is empty", source))
	}

	switch patchType {
	case "json":
		operations, isList := patch.([]interface{})
		if !isList || len(operations) == 0 {
			return nil, withExitCode(exitUsage, fmt.Errorf(
				"%s must be a non-empty list of operations for --type json, e.g. [{\"op\":\"replace\",\"path\":\"/spec/replicas\",\"value\":3}]", source))
		}
		for index, operation := range operations {
			fields, isObject := operation.(map[string]interface{})
			if !isObject {
				return nil, withExitCode(exitUsage, fmt.Errorf("%s: operation %d is not an object", source, index+1))
			}
			if operationName, _ := fields["op"].(string); operationName == "" {
				return nil, withExitCode(exitUsage, fmt.Errorf("%s: operation %d has no \"op\"", source, index+1))
			}
		}
	default:
		fields, isObject := patch.(map[string]interface{})
		if !isObject || len(fields) == 0 {
			return nil, withExitCode(exitUsage, fmt.Errorf(
				"%s must be a non-empty object for a %s; a list of operations needs --type json", source, patchTypeLabels[patchType]))
		}
	}
	return patch, nil
}

func patchResourcesPrompt(kind k8sKind, names []string, namespace, clusterName, patchType string) string {
	where := fmt.Sprintf("on cluster %q", clusterName)
	if namespace != "" {
		where = fmt.Sprintf("in namespace %q on cluster %q", namespace, clusterName)
	}
	if len(names) == 1 {
		return fmt.Sprintf("Apply a %s to %s %q %s? [y/N]: ",
			patchTypeLabels[patchType], strings.ToLower(kind.kind), names[0], where)
	}
	return fmt.Sprintf("Apply a %s to %d %s (%s) %s? [y/N]: ",
		patchTypeLabels[patchType], len(names), pluralKindLabel(kind), strings.Join(names, ", "), where)
}

func init() {
	clusterPatchCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace (required for namespaced kinds)")
	clusterPatchCmd.Flags().StringP("patch", "p", "", "The patch document, inline, as JSON or YAML")
	clusterPatchCmd.Flags().String("patch-file", "", "Read the patch document from a JSON or YAML file instead of --patch")
	clusterPatchCmd.Flags().String("type", "strategic", "Patch type: strategic (default), merge (RFC 7386) or json (RFC 6902)")
	clusterPatchCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	clusterPatchCmd.Flags().Bool("dry-run", false, "Report what would change without changing anything")
	clusterPatchCmd.Flags().String("group", "", "API group for a kind outside the built-in set (e.g. cert-manager.io)")
	clusterPatchCmd.Flags().String("api-version", "", "API version for a kind outside the built-in set (e.g. v1, v1beta1)")

	clusterCmd.AddCommand(clusterPatchCmd)
}

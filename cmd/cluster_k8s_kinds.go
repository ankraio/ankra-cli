package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// k8sKind is the group/version/scope a resources/get request needs for a
// kind the user named on the command line. The platform derives the API
// plural from the kind, so only kinds whose plural is irregular carry an
// explicit resource.
type k8sKind struct {
	kind          string
	group         string
	version       string
	resource      string
	clusterScoped bool
}

// builtinK8sKinds resolves the kubectl spellings - singular, plural, and
// the common short names - for the kinds a debugging session reaches for.
// Anything outside this table is still reachable by passing --group and
// --api-version explicitly, exactly like `cluster get resources`.
var builtinK8sKinds = map[string]k8sKind{
	"pod":                     {kind: "Pod", version: "v1"},
	"configmap":               {kind: "ConfigMap", version: "v1"},
	"secret":                  {kind: "Secret", version: "v1"},
	"service":                 {kind: "Service", version: "v1"},
	"endpoints":               {kind: "Endpoints", version: "v1", resource: "endpoints"},
	"serviceaccount":          {kind: "ServiceAccount", version: "v1"},
	"persistentvolumeclaim":   {kind: "PersistentVolumeClaim", version: "v1"},
	"replicationcontroller":   {kind: "ReplicationController", version: "v1"},
	"event":                   {kind: "Event", version: "v1"},
	"limitrange":              {kind: "LimitRange", version: "v1"},
	"resourcequota":           {kind: "ResourceQuota", version: "v1"},
	"node":                    {kind: "Node", version: "v1", clusterScoped: true},
	"namespace":               {kind: "Namespace", version: "v1", clusterScoped: true},
	"persistentvolume":        {kind: "PersistentVolume", version: "v1", clusterScoped: true},
	"deployment":              {kind: "Deployment", group: "apps", version: "v1"},
	"replicaset":              {kind: "ReplicaSet", group: "apps", version: "v1"},
	"statefulset":             {kind: "StatefulSet", group: "apps", version: "v1"},
	"daemonset":               {kind: "DaemonSet", group: "apps", version: "v1"},
	"controllerrevision":      {kind: "ControllerRevision", group: "apps", version: "v1"},
	"job":                     {kind: "Job", group: "batch", version: "v1"},
	"cronjob":                 {kind: "CronJob", group: "batch", version: "v1"},
	"ingress":                 {kind: "Ingress", group: "networking.k8s.io", version: "v1", resource: "ingresses"},
	"networkpolicy":           {kind: "NetworkPolicy", group: "networking.k8s.io", version: "v1", resource: "networkpolicies"},
	"ingressclass":            {kind: "IngressClass", group: "networking.k8s.io", version: "v1", resource: "ingressclasses", clusterScoped: true},
	"horizontalpodautoscaler": {kind: "HorizontalPodAutoscaler", group: "autoscaling", version: "v2", resource: "horizontalpodautoscalers"},
	"poddisruptionbudget":     {kind: "PodDisruptionBudget", group: "policy", version: "v1"},
	"storageclass":            {kind: "StorageClass", group: "storage.k8s.io", version: "v1", resource: "storageclasses", clusterScoped: true},
	"role":                    {kind: "Role", group: "rbac.authorization.k8s.io", version: "v1"},
	"rolebinding":             {kind: "RoleBinding", group: "rbac.authorization.k8s.io", version: "v1"},
	"clusterrole":             {kind: "ClusterRole", group: "rbac.authorization.k8s.io", version: "v1", clusterScoped: true},
	"clusterrolebinding":      {kind: "ClusterRoleBinding", group: "rbac.authorization.k8s.io", version: "v1", clusterScoped: true},
	"customresourcedefinition": {kind: "CustomResourceDefinition", group: "apiextensions.k8s.io", version: "v1",
		resource: "customresourcedefinitions", clusterScoped: true},
	"apiservice": {kind: "APIService", group: "apiregistration.k8s.io", version: "v1", resource: "apiservices", clusterScoped: true},
	"validatingwebhookconfiguration": {kind: "ValidatingWebhookConfiguration", group: "admissionregistration.k8s.io", version: "v1",
		resource: "validatingwebhookconfigurations", clusterScoped: true},
	"mutatingwebhookconfiguration": {kind: "MutatingWebhookConfiguration", group: "admissionregistration.k8s.io", version: "v1",
		resource: "mutatingwebhookconfigurations", clusterScoped: true},
	"priorityclass":    {kind: "PriorityClass", group: "scheduling.k8s.io", version: "v1", resource: "priorityclasses", clusterScoped: true},
	"runtimeclass":     {kind: "RuntimeClass", group: "node.k8s.io", version: "v1", resource: "runtimeclasses", clusterScoped: true},
	"csidriver":        {kind: "CSIDriver", group: "storage.k8s.io", version: "v1", resource: "csidrivers", clusterScoped: true},
	"csinode":          {kind: "CSINode", group: "storage.k8s.io", version: "v1", resource: "csinodes", clusterScoped: true},
	"volumeattachment": {kind: "VolumeAttachment", group: "storage.k8s.io", version: "v1", resource: "volumeattachments", clusterScoped: true},
}

// k8sKindShortNames maps the kubectl short spellings onto their canonical
// singular key in builtinK8sKinds.
var k8sKindShortNames = map[string]string{
	"po":     "pod",
	"cm":     "configmap",
	"svc":    "service",
	"ep":     "endpoints",
	"sa":     "serviceaccount",
	"pvc":    "persistentvolumeclaim",
	"pv":     "persistentvolume",
	"rc":     "replicationcontroller",
	"ev":     "event",
	"no":     "node",
	"ns":     "namespace",
	"deploy": "deployment",
	"rs":     "replicaset",
	"sts":    "statefulset",
	"ds":     "daemonset",
	"cj":     "cronjob",
	"ing":    "ingress",
	"netpol": "networkpolicy",
	"hpa":    "horizontalpodautoscaler",
	"pdb":    "poddisruptionbudget",
	"sc":     "storageclass",
	"limits": "limitrange",
	"quota":  "resourcequota",
	"crd":    "customresourcedefinition",
	"crds":   "customresourcedefinition",
	"pc":     "priorityclass",
}

// singularK8sKindKey normalises a user-typed kind to the singular lower-case
// key used by builtinK8sKinds. It understands the short names, the plural
// forms the listing commands print, and CamelCase kinds as written in a
// manifest.
func singularK8sKindKey(rawKind string) string {
	lowered := strings.ToLower(strings.TrimSpace(rawKind))
	if canonical, isShortName := k8sKindShortNames[lowered]; isShortName {
		return canonical
	}
	if _, isKnown := builtinK8sKinds[lowered]; isKnown {
		return lowered
	}
	// "endpoints" is its own plural, so the plural trims below must not
	// touch a key that already resolved.
	for _, candidate := range []string{
		strings.TrimSuffix(lowered, "es"),
		strings.TrimSuffix(lowered, "s"),
	} {
		if candidate == lowered {
			continue
		}
		if _, isKnown := builtinK8sKinds[candidate]; isKnown {
			return candidate
		}
	}
	if strings.HasSuffix(lowered, "ies") {
		candidate := strings.TrimSuffix(lowered, "ies") + "y"
		if _, isKnown := builtinK8sKinds[candidate]; isKnown {
			return candidate
		}
	}
	return lowered
}

// resolveK8sKindFromFlags resolves the kind argument of a command that
// registers --group, --api-version and --cluster-scoped. A custom resource
// outside the table is namespaced unless --cluster-scoped says otherwise:
// the CLI has no discovery of its own, and guessing cluster scope for a
// namespaced kind would silently drop the namespace the caller typed.
func resolveK8sKindFromFlags(cmd *cobra.Command, rawKind string) (k8sKind, error) {
	apiGroup, _ := cmd.Flags().GetString("group")
	apiVersion, _ := cmd.Flags().GetString("api-version")
	resource, _ := cmd.Flags().GetString("resource")
	isClusterScoped, _ := cmd.Flags().GetBool("cluster-scoped")
	resolved, resolveError := resolveK8sKind(rawKind, apiGroup, apiVersion)
	if resolveError != nil {
		return k8sKind{}, resolveError
	}
	if isClusterScoped {
		resolved.clusterScoped = true
	}
	if resource != "" {
		if !k8sResourceNamePattern.MatchString(resource) {
			return k8sKind{}, withExitCode(exitUsage, fmt.Errorf("--resource must be a lowercase Kubernetes resource plural"))
		}
		resolved.resource = resource
	}
	return resolved, nil
}

var k8sResourceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// registerK8sKindFlags adds the flags resolveK8sKindFromFlags reads.
func registerK8sKindFlags(command *cobra.Command) {
	command.Flags().String("group", "", "API group for a kind outside the built-in set (e.g. cert-manager.io)")
	command.Flags().String("api-version", "", "API version for a kind outside the built-in set (e.g. v1, v1beta1)")
	command.Flags().String("resource", "", "Exact API resource plural when a custom kind has an irregular plural (e.g. backuprepositories)")
	command.Flags().Bool("cluster-scoped", false,
		"Treat a kind outside the built-in set as cluster-scoped (e.g. ClusterIssuer, ClusterPolicy); no --namespace is needed")
}

// namespaceRequiredError is the usage error for a namespaced kind given
// without --namespace. It names --cluster-scoped for a custom kind, since a
// cluster-scoped custom resource is the one case the table cannot know.
func namespaceRequiredError(verb string, kind k8sKind) error {
	if _, isBuiltin := builtinK8sKinds[strings.ToLower(kind.kind)]; isBuiltin {
		return withExitCode(exitUsage, fmt.Errorf("--namespace (-n) is required to %s a %s", verb, kind.kind))
	}
	return withExitCode(exitUsage, fmt.Errorf(
		"--namespace (-n) is required to %s a %s; pass --cluster-scoped if %s is a cluster-scoped kind",
		verb, kind.kind, kind.kind))
}

// resolveK8sKind maps a user-typed kind onto its group/version/scope.
// groupOverride and versionOverride come from --group / --api-version and
// win over the table, which is what makes custom resources reachable. An
// unknown kind with no --group is a usage error naming the known kinds
// rather than an empty result the caller has to interpret.
func resolveK8sKind(rawKind string, groupOverride string, versionOverride string) (k8sKind, error) {
	if strings.TrimSpace(rawKind) == "" {
		return k8sKind{}, withExitCode(exitUsage, fmt.Errorf("a resource kind is required"))
	}
	resolved, isKnown := builtinK8sKinds[singularK8sKindKey(rawKind)]
	if !isKnown {
		if groupOverride == "" || versionOverride == "" {
			return k8sKind{}, withExitCode(exitUsage, fmt.Errorf(
				"unknown resource kind %q: pass both --group and --api-version for a kind outside %s "+
					"(spell it as the CamelCase singular, e.g. Certificate, not certificates)",
				rawKind, strings.Join(knownK8sKindNames(), ", ")))
		}
		// The platform derives the API plural from the kind, so a plural
		// spelling here becomes a double plural ("certificates" ->
		// "certificateses") and the read comes back empty - reported as a
		// confident "not found" for an object that exists. Singularise so
		// the derivation lands on the real plural whichever spelling the
		// caller used.
		resolved = k8sKind{kind: singularKindSpelling(rawKind), version: "v1"}
	}
	if groupOverride != "" {
		resolved.group = groupOverride
		// The explicit plural belongs to the kind, not to the group, so a
		// matched builtin keeps it. Discarding it would hand the platform's
		// heuristic kinds it cannot pluralise from the name alone
		// (Endpoints, Ingress, NetworkPolicy, StorageClass) and 404 against
		// an object that exists.
		if !isKnown {
			resolved.resource = ""
		}
	}
	if versionOverride != "" {
		resolved.version = versionOverride
	}
	return resolved, nil
}

// singularKindSpelling trims a plural suffix while preserving the caller's
// capitalisation, which for a custom resource is the only thing that carries
// the CamelCase the API server expects.
func singularKindSpelling(rawKind string) string {
	trimmed := strings.TrimSpace(rawKind)
	lowered := strings.ToLower(trimmed)
	switch {
	case strings.HasSuffix(lowered, "ies") && len(trimmed) > 3:
		return trimmed[:len(trimmed)-3] + "y"
	case strings.HasSuffix(lowered, "sses"), strings.HasSuffix(lowered, "xes"),
		strings.HasSuffix(lowered, "ches"), strings.HasSuffix(lowered, "shes"):
		return trimmed[:len(trimmed)-2]
	case strings.HasSuffix(lowered, "s") && !strings.HasSuffix(lowered, "ss"):
		return trimmed[:len(trimmed)-1]
	default:
		return trimmed
	}
}

// knownK8sKindNames lists the table's canonical kinds for the usage error.
func knownK8sKindNames() []string {
	names := make([]string, 0, len(builtinK8sKinds))
	for _, entry := range builtinK8sKinds {
		names = append(names, entry.kind)
	}
	sort.Strings(names)
	return names
}

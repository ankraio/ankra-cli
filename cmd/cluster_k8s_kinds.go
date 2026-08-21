package cmd

import (
	"fmt"
	"sort"
	"strings"
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
		if groupOverride == "" && versionOverride == "" {
			return k8sKind{}, withExitCode(exitUsage, fmt.Errorf(
				"unknown resource kind %q: pass --group and --api-version for a kind outside %s",
				rawKind, strings.Join(knownK8sKindNames(), ", ")))
		}
		resolved = k8sKind{kind: rawKind, version: "v1"}
	}
	if groupOverride != "" {
		resolved.group = groupOverride
		resolved.resource = ""
	}
	if versionOverride != "" {
		resolved.version = versionOverride
	}
	return resolved, nil
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

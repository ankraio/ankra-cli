package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"ankra/internal/cachenotice"
	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// describeResult is the structured shape of `cluster describe`. The object
// is the manifest verbatim and the events are the ones whose involvedObject
// is that manifest, so a script gets exactly what the human rendering was
// built from.
type describeResult struct {
	Object map[string]interface{}   `json:"object" yaml:"object"`
	Events []map[string]interface{} `json:"events" yaml:"events"`
	// KubeletConfig is the node's live kubelet configuration, present only
	// with --kubelet-config.
	KubeletConfig map[string]interface{} `json:"kubelet_config,omitempty" yaml:"kubelet_config,omitempty"`
	// KubeletConfigError says why --kubelet-config could not be read, so an
	// absent kubelet_config is never mistaken for an empty one.
	KubeletConfigError string `json:"kubelet_config_error,omitempty" yaml:"kubelet_config_error,omitempty"`
}

var clusterDescribeCmd = &cobra.Command{
	Use:   "describe <kind> <name>",
	Short: "Show one resource's status, conditions, and its own events",
	Long: `Show a single Kubernetes resource together with the events scoped to it.

This answers the question a failing workload raises - why is it not ready? -
in one call: the object's conditions, a pod's per-container state (probe
failures, image-pull errors, OOMKills, exit codes), and the events whose
involvedObject is this resource, rather than a namespace-wide event list you
have to filter by eye.

Kinds outside the built-in set need --group (and sometimes --api-version),
exactly like 'cluster get resources'.

--managed-fields reads the object live from the cluster and adds who last
wrote it: each field manager with its operation, subresource and time. It is
how to see what resized a pod in place (subresource "resize"), scaled a
workload or changed its spec. Kubernetes keeps one entry per manager and
subresource, so this is the most recent write of each, not a full history. The
cached copy other reads are served from does not keep them, and -o json|yaml
then carry them under object.metadata.managedFields.

--kubelet-config, on a node, adds the kubelet's live configuration as the
kubelet reports it on /configz: the values in force once its flags and config
file were merged (eviction thresholds, reserved resources, image garbage
collection, max pods), which the Node object does not carry. A kubelet the
read cannot reach is reported with the reason in place of the configuration,
never as an empty one; -o json|yaml carry it as kubelet_config, or
kubelet_config_error.

Examples:
  ankra cluster describe pod web-7d9f-2xkvp -n default
  ankra cluster describe deployment web -n default
  ankra cluster describe node worker-1
  ankra cluster describe pod web-7d9f-2xkvp -n default --managed-fields
  ankra cluster describe node worker-1 --kubelet-config
  ankra cluster describe pvc data-web-0 -n default -o json`,
	Args:        cobra.ExactArgs(2),
	Annotations: map[string]string{"group": "kubernetes"},
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFormat, _ := cmd.Flags().GetString("output")
		if err := validateK8sOutputFormat(outputFormat); err != nil {
			return err
		}
		namespace, _ := cmd.Flags().GetString("namespace")
		showFieldManagers, _ := cmd.Flags().GetBool("managed-fields")
		showKubeletConfig, _ := cmd.Flags().GetBool("kubelet-config")

		resolvedKind, kindError := resolveK8sKindFromFlags(cmd, args[0])
		if kindError != nil {
			return kindError
		}
		name := args[1]
		if showKubeletConfig && (resolvedKind.kind != "Node" || resolvedKind.group != "") {
			return withExitCode(exitUsage, errors.New("--kubelet-config applies to nodes only: ankra cluster describe node <name> --kubelet-config"))
		}
		if !resolvedKind.clusterScoped && namespace == "" {
			return namespaceRequiredError("describe", resolvedKind)
		}
		if resolvedKind.clusterScoped {
			namespace = ""
		}

		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}

		object, objectAnswer, objectError := fetchSingleResource(cluster.ID, resolvedKind, namespace, name, showFieldManagers)
		if objectError != nil {
			return objectError
		}
		redactSecretData(resolvedKind.kind, object)
		events, eventsError := fetchEventsForObject(cluster.ID, resolvedKind.kind, name, namespace)
		if eventsError != nil {
			return eventsError
		}

		result := describeResult{Object: object, Events: events}
		if showKubeletConfig {
			kubeletConfig, kubeletConfigError := apiClient.GetNodeKubeletConfig(cluster.ID, name)
			if kubeletConfigError != nil {
				result.KubeletConfigError = kubeletConfigError.Error()
			} else {
				result.KubeletConfig = kubeletConfig.KubeletConfig
			}
		}
		if outputFormat == "json" || outputFormat == "yaml" {
			announceDegradedBareObject(objectAnswer)
			if showFieldManagers && objectAnswer.ServedFromCache {
				fmt.Fprintln(os.Stderr, fieldManagersUnavailableNotice)
			}
		}
		switch outputFormat {
		case "json":
			encoded, marshalError := json.MarshalIndent(result, "", "  ")
			if marshalError != nil {
				return fmt.Errorf("marshalling to JSON: %w", marshalError)
			}
			fmt.Println(string(encoded))
			return nil
		case "yaml":
			encoded, marshalError := yaml.Marshal(result)
			if marshalError != nil {
				return fmt.Errorf("marshalling to YAML: %w", marshalError)
			}
			fmt.Print(string(encoded))
			return nil
		}
		announceCachedAnswer(objectAnswer)
		renderDescribe(resolvedKind.kind, object, events)
		if showFieldManagers {
			renderFieldManagers(os.Stdout, object, objectAnswer)
		}
		if showKubeletConfig {
			renderKubeletConfig(os.Stdout, result)
		}
		return nil
	},
}

// renderKubeletConfig prints the --kubelet-config section: the configuration
// as indented YAML, or the reason it could not be read.
func renderKubeletConfig(out io.Writer, result describeResult) {
	fmt.Fprintln(out)
	if result.KubeletConfigError != "" {
		fmt.Fprintf(out, "Kubelet configuration: could not be read: %s\n", result.KubeletConfigError)
		return
	}
	fmt.Fprintln(out, "Kubelet configuration (live, as the kubelet reports it on /configz):")
	encoded, marshalError := yaml.Marshal(result.KubeletConfig)
	if marshalError != nil {
		fmt.Fprintf(out, "  could not be rendered: %v\n", marshalError)
		return
	}
	for _, line := range strings.Split(strings.TrimRight(string(encoded), "\n"), "\n") {
		fmt.Fprintf(out, "  %s\n", line)
	}
}

// lastAppliedConfigurationAnnotation is the annotation kubectl apply writes,
// containing the entire submitted manifest verbatim - base64 Secret values
// included.
const lastAppliedConfigurationAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// minimumRedactableSecretLength is the shortest secret value worth scanning
// *other* annotations for. The guaranteed leak vector,
// last-applied-configuration, is redacted unconditionally at any value
// length; this gate only bounds the heuristic substring scan, where a very
// short value would match unrelated annotations by coincidence and redact
// the lot. Four is low enough to catch a short token and high enough that a
// coincidental hit is unlikely; a value below it has too little entropy to
// be worth protecting at the cost of blanking every annotation that happens
// to contain those bytes.
const minimumRedactableSecretLength = 4

// redactSecretData replaces a Secret's values with their byte lengths, in
// place, before anything renders the object.
//
// kubectl describe redacts a Secret to key lengths for a reason, and this
// command is pitched as the narrower alternative to handing out a kubectl
// grant - it must not be an easier way to exfiltrate secret material than
// the tool it replaces. `describe secret X -o json` would otherwise print
// the full base64 payload to stdout, and from there into shell history and
// CI logs. Reading a Secret's contents deliberately remains the job of
// `cluster get secrets <name> -n <namespace> --reveal`, which is an explicit,
// permission-gated and audited request for them rather than a debugging verb.
//
// Since the platform's Secret value policy (cluster ankra-z0p76) the values
// usually arrive as sha256 digests already. A digest's length says nothing
// about the value's, so a digest is rendered as withheld rather than as a
// byte count.
//
// Clearing data and stringData is not sufficient on its own: a Secret
// created with `kubectl apply` carries the whole original manifest in the
// last-applied-configuration annotation, which reproduces every value
// verbatim. That annotation is always redacted for a Secret, and any other
// annotation that turns out to embed one of the values is redacted too -
// scanning for the values themselves catches a controller that copies them
// somewhere this code does not know the name of.
func redactSecretData(kind string, object map[string]interface{}) {
	// The resolved kind is authoritative: a cached item is not guaranteed to
	// carry its own kind field, so keying off the manifest could miss.
	if kind != "Secret" && getNestedString(object, "kind") != "Secret" {
		return
	}
	secretValues := []string{}
	for _, field := range []string{"data", "stringData"} {
		values, isMap := object[field].(map[string]interface{})
		if !isMap {
			continue
		}
		redacted := make(map[string]interface{}, len(values))
		for key, value := range values {
			rendered := fmt.Sprintf("%v", value)
			if isSecretValueDigest(rendered) {
				redacted[key] = "<redacted: value withheld by the platform>"
				continue
			}
			if len(rendered) >= minimumRedactableSecretLength {
				secretValues = append(secretValues, rendered)
			}
			redacted[key] = fmt.Sprintf("<redacted: %d bytes>", len(rendered))
		}
		object[field] = redacted
	}
	redactSecretAnnotations(object, secretValues)
}

// redactSecretAnnotations removes the annotations that can reproduce a
// Secret's values after data and stringData have been redacted.
func redactSecretAnnotations(object map[string]interface{}, secretValues []string) {
	metadata, hasMetadata := getNestedMap(object, "metadata")
	if !hasMetadata {
		return
	}
	annotations, isMap := metadata["annotations"].(map[string]interface{})
	if !isMap {
		return
	}
	for name, value := range annotations {
		rendered := fmt.Sprintf("%v", value)
		if name == lastAppliedConfigurationAnnotation {
			annotations[name] = "<redacted: contains the applied Secret manifest>"
			continue
		}
		for _, secretValue := range secretValues {
			if strings.Contains(rendered, secretValue) {
				annotations[name] = "<redacted: contains Secret data>"
				break
			}
		}
	}
}

// fetchSingleResource reads exactly one named object. A missing or deleted
// object exits with the not-found code and a failed read is an error
// (namedReadItems), rather than either rendering as a description. The
// answer says whether the platform served it from the agent's cache.
func fetchSingleResource(clusterID string, resolvedKind k8sKind, namespace string, name string, live bool) (map[string]interface{}, cachenotice.Answer, error) {
	request := client.ResourceRequestItem{
		Kind:     resolvedKind.kind,
		Group:    resolvedKind.group,
		Version:  resolvedKind.version,
		Resource: resolvedKind.resource,
		Name:     name,
	}
	if namespace != "" {
		request.Namespace = namespace
	}
	response, requestError := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{request},
		SkipCache:        live,
	})
	if requestError != nil {
		return nil, cachenotice.Answer{}, requestError
	}
	items, namedReadError := namedReadItems(response, resolvedKind.kind, name, namespace)
	if namedReadError != nil {
		return nil, cachenotice.Answer{}, namedReadError
	}
	object, isObject := items[0].(map[string]interface{})
	if !isObject {
		return nil, cachenotice.Answer{}, fmt.Errorf("unexpected %s payload for %q", resolvedKind.kind, name)
	}
	return object, cachenotice.FromResource(response.ResourceResponses[0].CacheMetadata), nil
}

func namespaceSuffix(namespace string) string {
	if namespace == "" {
		return ""
	}
	return " in namespace " + namespace
}

// renderDescribe prints the human view: identity, conditions, container
// states for a pod, then the object's own events.
func renderDescribe(kind string, object map[string]interface{}, events []map[string]interface{}) {
	fmt.Printf("%s\n", text.Bold.Sprintf("%s/%s", kind, getNestedString(object, "metadata", "name")))
	if namespace := getNestedString(object, "metadata", "namespace"); namespace != "" {
		fmt.Printf("Namespace:    %s\n", namespace)
	}
	if apiVersion := getNestedString(object, "apiVersion"); apiVersion != "" {
		fmt.Printf("API version:  %s\n", apiVersion)
	}
	fmt.Printf("Age:          %s\n", formatK8sAge(getNestedString(object, "metadata", "creationTimestamp")))
	if node := getNestedString(object, "spec", "nodeName"); node != "" {
		fmt.Printf("Node:         %s\n", node)
	}
	if phase := getNestedString(object, "status", "phase"); phase != "" {
		fmt.Printf("Status:       %s\n", phase)
	}
	if reason := getNestedString(object, "status", "reason"); reason != "" {
		fmt.Printf("Reason:       %s\n", reason)
	}
	if message := getNestedString(object, "status", "message"); message != "" {
		fmt.Printf("Message:      %s\n", message)
	}
	if labels := formatStringMap(object, "labels"); labels != "" {
		fmt.Printf("Labels:       %s\n", labels)
	}

	renderConditions(object)
	renderContainerStatuses(object)
	renderSecretKeys(kind, object)
	renderObjectEvents(events)
}

// fieldManagersUnavailableNotice is what --managed-fields says when the live
// read degraded to the cached copy. That copy never keeps field managers, so
// finding none in it says nothing about the object.
const fieldManagersUnavailableNotice = "Field managers are not available: the cluster did not answer live, " +
	"and the cached copy does not keep them."

// fieldManager is one metadata.managedFields entry, without the field list.
type fieldManager struct {
	manager     string
	operation   string
	subresource string
	time        string
}

// fieldManagersNewestFirst reads metadata.managedFields. RFC 3339 times in one
// zone sort as strings, and the apiserver writes every one in UTC.
func fieldManagersNewestFirst(object map[string]interface{}) []fieldManager {
	metadata, hasMetadata := getNestedMap(object, "metadata")
	if !hasMetadata {
		return nil
	}
	rawEntries, isList := metadata["managedFields"].([]interface{})
	if !isList {
		return nil
	}
	managers := make([]fieldManager, 0, len(rawEntries))
	for _, rawEntry := range rawEntries {
		entry, isMap := rawEntry.(map[string]interface{})
		if !isMap {
			continue
		}
		managers = append(managers, fieldManager{
			manager:     getNestedString(entry, "manager"),
			operation:   getNestedString(entry, "operation"),
			subresource: getNestedString(entry, "subresource"),
			time:        getNestedString(entry, "time"),
		})
	}
	sort.SliceStable(managers, func(left, right int) bool {
		return managers[left].time > managers[right].time
	})
	return managers
}

// renderFieldManagers prints who last wrote the object. The time is printed
// whole, not as an age: this table is read to line a write up against
// something else that happened.
func renderFieldManagers(out io.Writer, object map[string]interface{}, answer cachenotice.Answer) {
	_, _ = fmt.Fprintf(out, "\n%s\n", text.Bold.Sprint("Field managers"))
	if answer.ServedFromCache {
		_, _ = fmt.Fprintln(out, fieldManagersUnavailableNotice)
		return
	}
	managers := fieldManagersNewestFirst(object)
	if len(managers) == 0 {
		_, _ = fmt.Fprintln(out, "None recorded.")
		return
	}
	managersTable := table.NewWriter()
	managersTable.SetOutputMirror(out)
	managersTable.SetStyle(table.StyleRounded)
	managersTable.AppendHeader(table.Row{"Manager", "Operation", "Subresource", "Last Write"})
	for _, manager := range managers {
		managersTable.AppendRow(table.Row{manager.manager, manager.operation, manager.subresource, manager.time})
	}
	managersTable.Render()
}

// formatStringMap renders metadata.<key> as a sorted k=v list.
func formatStringMap(object map[string]interface{}, key string) string {
	metadata, hasMetadata := getNestedMap(object, "metadata")
	if !hasMetadata {
		return ""
	}
	entries, isMap := metadata[key].(map[string]interface{})
	if !isMap || len(entries) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(entries))
	for name, value := range entries {
		pairs = append(pairs, fmt.Sprintf("%s=%v", name, value))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ", ")
}

func renderConditions(object map[string]interface{}) {
	status, hasStatus := getNestedMap(object, "status")
	if !hasStatus {
		return
	}
	rawConditions, isList := status["conditions"].([]interface{})
	if !isList || len(rawConditions) == 0 {
		return
	}
	fmt.Printf("\n%s\n", text.Bold.Sprint("Conditions"))
	conditionsTable := table.NewWriter()
	conditionsTable.SetOutputMirror(os.Stdout)
	conditionsTable.SetStyle(table.StyleRounded)
	conditionsTable.AppendHeader(table.Row{"Type", "Status", "Reason", "Message", "Last Transition"})
	for _, rawCondition := range rawConditions {
		condition, isMap := rawCondition.(map[string]interface{})
		if !isMap {
			continue
		}
		conditionStatus := getNestedString(condition, "status")
		switch conditionStatus {
		case "True":
			conditionStatus = text.FgGreen.Sprint(conditionStatus)
		case "False":
			conditionStatus = text.FgRed.Sprint(conditionStatus)
		}
		lastTransition := getNestedString(condition, "lastTransitionTime")
		if lastTransition == "" {
			lastTransition = getNestedString(condition, "lastProbeTime")
		}
		conditionsTable.AppendRow(table.Row{
			getNestedString(condition, "type"),
			conditionStatus,
			getNestedString(condition, "reason"),
			getNestedString(condition, "message"),
			formatK8sAge(lastTransition),
		})
	}
	conditionsTable.Render()
}

// renderContainerStatuses prints the per-container state of a pod. This is
// where a CrashLoopBackOff names its exit code, an ImagePullBackOff names
// the registry error, and a failing readiness probe shows up as a not-ready
// container with a restart count.
func renderContainerStatuses(object map[string]interface{}) {
	status, hasStatus := getNestedMap(object, "status")
	if !hasStatus {
		return
	}
	sections := []struct {
		title string
		key   string
	}{
		{"Init containers", "initContainerStatuses"},
		{"Containers", "containerStatuses"},
		{"Ephemeral containers", "ephemeralContainerStatuses"},
	}
	for _, section := range sections {
		rawStatuses, isList := status[section.key].([]interface{})
		if !isList || len(rawStatuses) == 0 {
			continue
		}
		fmt.Printf("\n%s\n", text.Bold.Sprint(section.title))
		containersTable := table.NewWriter()
		containersTable.SetOutputMirror(os.Stdout)
		containersTable.SetStyle(table.StyleRounded)
		containersTable.AppendHeader(table.Row{"Name", "Ready", "Restarts", "State", "Detail", "Image"})
		for _, rawStatus := range rawStatuses {
			containerStatus, isMap := rawStatus.(map[string]interface{})
			if !isMap {
				continue
			}
			ready := getNestedString(containerStatus, "ready")
			switch ready {
			case "true":
				ready = text.FgGreen.Sprint("true")
			case "false":
				ready = text.FgRed.Sprint("false")
			}
			state, detail := containerStateSummary(containerStatus, "state")
			lastState, lastDetail := containerStateSummary(containerStatus, "lastState")
			if lastState != "" {
				state = fmt.Sprintf("%s (last: %s)", state, lastState)
				if detail == "" {
					detail = lastDetail
				} else if lastDetail != "" {
					detail = fmt.Sprintf("%s; last: %s", detail, lastDetail)
				}
			}
			containersTable.AppendRow(table.Row{
				getNestedString(containerStatus, "name"),
				ready,
				getNestedString(containerStatus, "restartCount"),
				state,
				detail,
				getNestedString(containerStatus, "image"),
			})
		}
		containersTable.Render()
	}
}

// containerStateSummary flattens a ContainerState union into a state name
// and the reason/exit-code detail that explains it.
func containerStateSummary(containerStatus map[string]interface{}, key string) (string, string) {
	state, hasState := getNestedMap(containerStatus, key)
	if !hasState {
		return "", ""
	}
	for _, stateName := range []string{"waiting", "running", "terminated"} {
		stateBody, isPresent := state[stateName].(map[string]interface{})
		if !isPresent {
			continue
		}
		details := []string{}
		if reason := getNestedString(stateBody, "reason"); reason != "" {
			details = append(details, reason)
		}
		if exitCode := getNestedString(stateBody, "exitCode"); exitCode != "" {
			details = append(details, "exit "+exitCode)
		}
		if signal := getNestedString(stateBody, "signal"); signal != "" && signal != "0" {
			details = append(details, "signal "+signal)
		}
		if message := getNestedString(stateBody, "message"); message != "" {
			details = append(details, message)
		}
		return stateName, strings.Join(details, ": ")
	}
	return "", ""
}

// renderSecretKeys lists a Secret's keys and value sizes, never the values -
// they are already redacted by the time this runs. "Is the mounted secret
// present, and is it non-empty?" is one of the questions that used to need a
// shell in the pod, and it is answerable without one.
func renderSecretKeys(kind string, object map[string]interface{}) {
	// Same authoritative check as redactSecretData: a cached item may not
	// carry its own kind, and redacting a Secret while skipping its key
	// listing would be a confusing pair of behaviours in one command.
	if kind != "Secret" && getNestedString(object, "kind") != "Secret" {
		return
	}
	for _, field := range []string{"data", "stringData"} {
		values, isMap := object[field].(map[string]interface{})
		if !isMap || len(values) == 0 {
			continue
		}
		fmt.Printf("\n%s\n", text.Bold.Sprint("Data ("+field+")"))
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		secretTable := table.NewWriter()
		secretTable.SetOutputMirror(os.Stdout)
		secretTable.SetStyle(table.StyleRounded)
		secretTable.AppendHeader(table.Row{"Key", "Value"})
		for _, key := range keys {
			secretTable.AppendRow(table.Row{key, values[key]})
		}
		secretTable.Render()
	}
}

func renderObjectEvents(events []map[string]interface{}) {
	fmt.Printf("\n%s\n", text.Bold.Sprint("Events"))
	if len(events) == 0 {
		fmt.Println("No events found for this resource.")
		return
	}
	renderEventsTable(events)
}

func init() {
	clusterDescribeCmd.Flags().StringP("namespace", "n", "", "Kubernetes namespace")
	clusterDescribeCmd.Flags().Bool("managed-fields", false,
		"Read the object live and show who last wrote it: each field manager with its operation, subresource and time")
	clusterDescribeCmd.Flags().Bool("kubelet-config", false,
		"On a node: add the kubelet's live configuration as it reports it on /configz")
	clusterDescribeCmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")
	registerK8sKindFlags(clusterDescribeCmd)

	clusterCmd.AddCommand(clusterDescribeCmd)
}

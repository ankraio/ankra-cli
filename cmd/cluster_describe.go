package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

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

Examples:
  ankra cluster describe pod web-7d9f-2xkvp -n default
  ankra cluster describe deployment web -n default
  ankra cluster describe node worker-1
  ankra cluster describe pvc data-web-0 -n default -o json`,
	Args:        cobra.ExactArgs(2),
	Annotations: map[string]string{"group": "kubernetes"},
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFormat, _ := cmd.Flags().GetString("output")
		if err := validateK8sOutputFormat(outputFormat); err != nil {
			return err
		}
		namespace, _ := cmd.Flags().GetString("namespace")
		apiGroup, _ := cmd.Flags().GetString("group")
		apiVersion, _ := cmd.Flags().GetString("api-version")

		resolvedKind, kindError := resolveK8sKind(args[0], apiGroup, apiVersion)
		if kindError != nil {
			return kindError
		}
		name := args[1]
		if !resolvedKind.clusterScoped && namespace == "" {
			return withExitCode(exitUsage, fmt.Errorf(
				"--namespace (-n) is required to describe a %s", resolvedKind.kind))
		}
		if resolvedKind.clusterScoped {
			namespace = ""
		}

		cluster, clusterError := resolveActiveCluster(cmd)
		if clusterError != nil {
			return clusterError
		}

		object, objectError := fetchSingleResource(cluster.ID, resolvedKind, namespace, name)
		if objectError != nil {
			return objectError
		}
		events, eventsError := fetchEventsForObject(cluster.ID, resolvedKind.kind, name, namespace)
		if eventsError != nil {
			return eventsError
		}

		result := describeResult{Object: object, Events: events}
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
		renderDescribe(resolvedKind.kind, object, events)
		return nil
	},
}

// fetchSingleResource reads exactly one named object. The resources/get
// wire has no distinct not-found status, so an empty item list is the
// not-found signal and is reported with the not-found exit code rather than
// rendered as an empty description.
func fetchSingleResource(clusterID string, resolvedKind k8sKind, namespace string, name string) (map[string]interface{}, error) {
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
	})
	if requestError != nil {
		return nil, requestError
	}
	if len(response.ResourceResponses) == 0 || len(response.ResourceResponses[0].Items) == 0 {
		return nil, withExitCode(exitNotFound, fmt.Errorf("%s %q not found%s",
			resolvedKind.kind, name, namespaceSuffix(namespace)))
	}
	object, isObject := response.ResourceResponses[0].Items[0].(map[string]interface{})
	if !isObject {
		return nil, fmt.Errorf("unexpected %s payload for %q", resolvedKind.kind, name)
	}
	return object, nil
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
	renderObjectEvents(events)
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
	clusterDescribeCmd.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")
	clusterDescribeCmd.Flags().String("group", "", "API group override (e.g. apps, networking.k8s.io)")
	clusterDescribeCmd.Flags().String("api-version", "", "API version override (e.g. v1, v1beta1)")

	clusterCmd.AddCommand(clusterDescribeCmd)
}

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// newClusterEventsCommand builds the events listing. The same command is
// mounted twice - as `cluster events` and as `cluster get events` - so both
// spellings share one implementation and both gain --for.
func newClusterEventsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "events",
		Short: "List cluster events, optionally scoped to one object",
		Long: `List Kubernetes events from the active cluster.

--for scopes the listing to a single object's involvedObject, which is what
turns "the pod is Pending" into "no node matches the nodeSelector". This is
a server-side field selector, not a substring match on the event text, so
an object whose name is a prefix of another object's name is not conflated
with it.

Examples:
  ankra cluster events -n default
  ankra cluster events --for pod/web-7d9f-2xkvp -n default
  ankra cluster events --for deployment/web -n default --type Warning
  ankra cluster events --all-namespaces --type Warning -o json`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"group": "kubernetes"},
		RunE:        runClusterEvents,
	}
	command.Flags().StringP("namespace", "n", "", "Kubernetes namespace")
	command.Flags().BoolP("all-namespaces", "A", false, "List across all namespaces")
	command.Flags().String("for", "", "Scope to one object's events, as kind/name (e.g. pod/web-7d9f-2xkvp)")
	command.Flags().String("type", "", "Filter by event type: Normal or Warning")
	command.Flags().String("name", "", "Filter by event object name")
	command.Flags().StringP("selector", "l", "", "Label selector")
	command.Flags().StringP("output", "o", "table", "Output format: table, json, yaml")
	return command
}

func runClusterEvents(cmd *cobra.Command, _ []string) error {
	outputFormat, _ := cmd.Flags().GetString("output")
	if err := validateK8sOutputFormat(outputFormat); err != nil {
		return err
	}
	namespace, _ := cmd.Flags().GetString("namespace")
	allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")
	forTarget, _ := cmd.Flags().GetString("for")
	eventType, _ := cmd.Flags().GetString("type")

	if allNamespaces {
		namespace = ""
	}
	if eventType != "" && !isKnownEventType(eventType) {
		return withExitCode(exitUsage, fmt.Errorf(
			"unsupported --type %q: use Normal or Warning", eventType))
	}

	fieldSelectors := []client.FieldSelector{}
	if forTarget != "" {
		targetKind, targetName, parseError := parseEventTarget(forTarget)
		if parseError != nil {
			return parseError
		}
		if !targetKind.clusterScoped && namespace == "" && !allNamespaces {
			return withExitCode(exitUsage, fmt.Errorf(
				"--namespace (-n) is required with --for %s/%s", strings.ToLower(targetKind.kind), targetName))
		}
		fieldSelectors = involvedObjectSelectors(targetKind.kind, targetName, namespace)
	}
	if eventType != "" {
		fieldSelectors = append(fieldSelectors, client.FieldSelector{Field: "type", Value: eventType})
	}

	nameFilter, _ := cmd.Flags().GetString("name")
	labelSelector, _ := cmd.Flags().GetString("selector")

	cluster, clusterError := resolveActiveCluster(cmd)
	if clusterError != nil {
		return clusterError
	}
	events, eventsError := fetchEvents(cluster.ID, namespace, fieldSelectors, nameFilter, labelSelector)
	if eventsError != nil {
		return eventsError
	}

	switch outputFormat {
	case "json":
		encoded, marshalError := json.MarshalIndent(events, "", "  ")
		if marshalError != nil {
			return fmt.Errorf("marshalling to JSON: %w", marshalError)
		}
		fmt.Println(string(encoded))
		return nil
	case "yaml":
		encoded, marshalError := yaml.Marshal(events)
		if marshalError != nil {
			return fmt.Errorf("marshalling to YAML: %w", marshalError)
		}
		fmt.Print(string(encoded))
		return nil
	}
	if len(events) == 0 {
		fmt.Println("No events found.")
		return nil
	}
	renderEventsTable(events)
	return nil
}

func isKnownEventType(eventType string) bool {
	return eventType == "Normal" || eventType == "Warning"
}

// parseEventTarget accepts the kubectl "kind/name" spelling that --for
// takes. A bare name has no kind to scope by, so it is a usage error
// rather than a silent namespace-wide listing.
func parseEventTarget(target string) (k8sKind, string, error) {
	kindPart, namePart, hasSeparator := strings.Cut(target, "/")
	if !hasSeparator || strings.TrimSpace(kindPart) == "" || strings.TrimSpace(namePart) == "" {
		return k8sKind{}, "", withExitCode(exitUsage, fmt.Errorf(
			"--for takes kind/name (e.g. --for pod/web-7d9f-2xkvp), got %q", target))
	}
	resolved, resolveError := resolveK8sKind(kindPart, "", "")
	if resolveError != nil {
		return k8sKind{}, "", resolveError
	}
	return resolved, namePart, nil
}

// involvedObjectSelectors builds the field selectors that scope an event
// listing to one object. The namespace selector is only meaningful for a
// namespaced target; a cluster-scoped object's events carry an empty
// involvedObject.namespace.
func involvedObjectSelectors(kind string, name string, namespace string) []client.FieldSelector {
	selectors := []client.FieldSelector{
		{Field: "involvedObject.kind", Value: kind},
		{Field: "involvedObject.name", Value: name},
	}
	if namespace != "" {
		selectors = append(selectors, client.FieldSelector{
			Field: "involvedObject.namespace", Value: namespace,
		})
	}
	return selectors
}

// fetchEventsForObject is the describe path's use of the same scoping.
func fetchEventsForObject(clusterID string, kind string, name string, namespace string) ([]map[string]interface{}, error) {
	return fetchEvents(clusterID, namespace, involvedObjectSelectors(kind, name, namespace), "", "")
}

// fetchEvents reads the Event list, newest last, so a terminal rendering
// reads top-to-bottom in the order the cluster produced them.
func fetchEvents(
	clusterID string,
	namespace string,
	fieldSelectors []client.FieldSelector,
	nameFilter string,
	labelSelector string,
) ([]map[string]interface{}, error) {
	request := client.ResourceRequestItem{Kind: "Event", Version: "v1"}
	if namespace != "" {
		request.Namespace = namespace
	}
	if nameFilter != "" {
		request.Name = nameFilter
	}
	if labelSelector != "" {
		request.LabelSelector = labelSelector
	}
	if len(fieldSelectors) > 0 {
		request.FieldSelectors = fieldSelectors
	}
	response, requestError := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{request},
	})
	if requestError != nil {
		return nil, requestError
	}
	if len(response.ResourceResponses) == 0 {
		return []map[string]interface{}{}, nil
	}
	events := make([]map[string]interface{}, 0, len(response.ResourceResponses[0].Items))
	for _, item := range response.ResourceResponses[0].Items {
		if event, isEvent := item.(map[string]interface{}); isEvent {
			events = append(events, event)
		}
	}
	sort.SliceStable(events, func(first int, second int) bool {
		return eventTimestamp(events[first]).Before(eventTimestamp(events[second]))
	})
	return events, nil
}

// eventTimestamp picks the most recent time an event carries. Series
// events report lastTimestamp, single events report eventTime or
// firstTimestamp, and everything falls back to the creation timestamp.
func eventTimestamp(event map[string]interface{}) time.Time {
	for _, key := range []string{"lastTimestamp", "eventTime", "firstTimestamp"} {
		if parsed, parseError := time.Parse(time.RFC3339, getNestedString(event, key)); parseError == nil {
			return parsed
		}
	}
	parsed, parseError := time.Parse(time.RFC3339, getNestedString(event, "metadata", "creationTimestamp"))
	if parseError != nil {
		return time.Time{}
	}
	return parsed
}

func renderEventsTable(events []map[string]interface{}) {
	eventsTable := table.NewWriter()
	eventsTable.SetOutputMirror(os.Stdout)
	eventsTable.SetStyle(table.StyleRounded)
	eventsTable.AppendHeader(table.Row{"Last Seen", "Type", "Reason", "Object", "Count", "Message"})
	for _, event := range events {
		eventType := getNestedString(event, "type")
		if eventType == "Warning" {
			eventType = text.FgYellow.Sprint(eventType)
		}
		involvedKind := strings.ToLower(getNestedString(event, "involvedObject", "kind"))
		involvedName := getNestedString(event, "involvedObject", "name")
		count := getNestedString(event, "count")
		if count == "" {
			count = "1"
		}
		eventsTable.AppendRow(table.Row{
			formatK8sAge(eventTimestamp(event).Format(time.RFC3339)),
			eventType,
			getNestedString(event, "reason"),
			fmt.Sprintf("%s/%s", involvedKind, involvedName),
			count,
			getNestedString(event, "message"),
		})
	}
	eventsTable.Render()
}

// The listing is mounted twice from one implementation: `cluster events` is
// the verb the debugging flow reaches for, `cluster get events` keeps the
// spelling the rest of the get family uses. Cobra owns a command's flag
// state, so each mount point needs its own instance.
var (
	clusterEventsCmd    = newClusterEventsCommand()
	clusterGetEventsCmd = newClusterEventsCommand()
)

func init() {
	clusterCmd.AddCommand(clusterEventsCmd)
	clusterGetCmd.AddCommand(clusterGetEventsCmd)
}

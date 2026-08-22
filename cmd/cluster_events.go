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
	events = filterEventsByType(events, eventType)

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

// filterEventsByType applies --type in the CLI rather than as a field
// selector. The platform answers an event listing from its resource cache
// whenever the cache is fresh, and the cache honours only a whitelist of
// field selectors - involvedObject.* and two spec.* fields - dropping
// anything else without saying so. A type selector would therefore have
// filtered against an unreachable cluster and silently not filtered against
// a healthy one, which is the worst of both.
func filterEventsByType(events []map[string]interface{}, eventType string) []map[string]interface{} {
	if eventType == "" {
		return events
	}
	filtered := make([]map[string]interface{}, 0, len(events))
	for _, event := range events {
		if getNestedString(event, "type") == eventType {
			filtered = append(filtered, event)
		}
	}
	return filtered
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

// formatEventAge renders an event's age, leaving the cell blank when the
// event carried no parseable timestamp at all - a zero time would otherwise
// render as two thousand years.
func formatEventAge(timestamp time.Time) string {
	if timestamp.IsZero() {
		return ""
	}
	return formatK8sAge(timestamp.Format(time.RFC3339))
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
			formatEventAge(eventTimestamp(event)),
			eventType,
			getNestedString(event, "reason"),
			fmt.Sprintf("%s/%s", involvedKind, involvedName),
			count,
			getNestedString(event, "message"),
		})
	}
	eventsTable.Render()
}

// `cluster events` is the dedicated debugging verb. `cluster get events`
// keeps the get family's own command, envelope and positional-name form -
// its --for and --type are wired through the kindConfig hooks below, so both
// spellings scope by involvedObject without either changing shape.
var clusterEventsCmd = newClusterEventsCommand()

func init() {
	clusterCmd.AddCommand(clusterEventsCmd)
}

// eventFieldSelectorsFromFlags turns `cluster get events --for kind/name`
// into the involvedObject selectors. It runs on the get-family command, whose
// namespace flag is absent for a cluster-scoped kind, hence the lookup guard.
func eventFieldSelectorsFromFlags(command *cobra.Command) ([]client.FieldSelector, error) {
	forTarget, _ := command.Flags().GetString("for")
	eventType, _ := command.Flags().GetString("type")
	if eventType != "" && !isKnownEventType(eventType) {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"unsupported --type %q: use Normal or Warning", eventType))
	}
	if forTarget == "" {
		return nil, nil
	}
	targetKind, targetName, parseError := parseEventTarget(forTarget)
	if parseError != nil {
		return nil, parseError
	}
	namespace := ""
	if command.Flags().Lookup("namespace") != nil {
		namespace, _ = command.Flags().GetString("namespace")
	}
	allNamespaces := false
	if command.Flags().Lookup("all-namespaces") != nil {
		allNamespaces, _ = command.Flags().GetBool("all-namespaces")
	}
	if allNamespaces {
		namespace = ""
	}
	if !targetKind.clusterScoped && namespace == "" && !allNamespaces {
		return nil, withExitCode(exitUsage, fmt.Errorf(
			"--namespace (-n) is required with --for %s/%s",
			strings.ToLower(targetKind.kind), targetName))
	}
	return involvedObjectSelectors(targetKind.kind, targetName, namespace), nil
}

// eventTypeFilterFromFlags applies --type in the CLI for the get-family
// command, for the same reason runClusterEvents does: the platform's cache
// silently drops a type field selector.
func eventTypeFilterFromFlags(items []interface{}, command *cobra.Command) []interface{} {
	eventType, _ := command.Flags().GetString("type")
	if eventType == "" {
		return items
	}
	filtered := make([]interface{}, 0, len(items))
	for _, item := range items {
		event, isEvent := item.(map[string]interface{})
		if isEvent && getNestedString(event, "type") == eventType {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

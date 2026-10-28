package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// The demo list and detail payloads are the only application subresources the
// CLI renders itself rather than dumping. A multi-component demo runs one pod
// per component, and the components array — which one owns the demo host's
// root path, which port and ingress path each got, which image tag each is
// running — is the part a human actually reads; scrolling a nested JSON
// document to find it is not reading.
//
// These documents decode only the fields the rendering needs. Everything else
// on the wire is reachable untouched through -o json|yaml, so the backend can
// add fields without this file caring, and a payload it cannot decode falls
// back to the raw JSON rather than failing a read.

// demoComponentDocument is one entry of a demo record's components array.
type demoComponentDocument struct {
	Name          string `json:"name"`
	ImageTag      string `json:"image_tag"`
	ContainerPort int    `json:"container_port"`
	IngressPath   string `json:"ingress_path"`
	Entry         bool   `json:"entry"`
}

// demoDocument is the demo record as list and detail both carry it.
type demoDocument struct {
	ID            string                  `json:"id"`
	Status        string                  `json:"status"`
	Branch        *string                 `json:"branch"`
	PRNumber      *int                    `json:"pr_number"`
	Namespace     string                  `json:"namespace"`
	PreviewURL    *string                 `json:"preview_url"`
	ImageTag      *string                 `json:"image_tag"`
	Component     *string                 `json:"component"`
	ContainerPort *int                    `json:"container_port"`
	Components    []demoComponentDocument `json:"components"`
	LastError     *string                 `json:"last_error"`
	ExpiresAt     string                  `json:"expires_at"`
	CreatedAt     string                  `json:"created_at"`
	DemoStackName *string                 `json:"demo_stack_name"`
}

// demoListDocument is the GET /demos payload. Demos is a pointer so an
// absent key is distinguishable from an empty list: "no active demos" is a
// claim about the application, and a payload that never carried the key must
// not be summarised into it.
type demoListDocument struct {
	Demos *[]demoDocument `json:"demos"`
}

// demoStepDocument is one provisioning step of the detail payload.
type demoStepDocument struct {
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// demoResourceDocument is one inventory entry of the detail payload. Pod
// entries carry the component they belong to, which is what resolves
// `demo logs --component` to a pod name.
type demoResourceDocument struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Component string `json:"component"`
	Present   bool   `json:"present"`
	Status    string `json:"status"`
}

// demoDetailDocument is the GET /demos/{id}/detail payload.
type demoDetailDocument struct {
	Demo       demoDocument `json:"demo"`
	Inspection struct {
		ClusterReachable  bool                   `json:"cluster_reachable"`
		UnreachableReason string                 `json:"unreachable_reason"`
		Steps             []demoStepDocument     `json:"steps"`
		Resources         []demoResourceDocument `json:"resources"`
	} `json:"inspection"`
}

// componentNames lists the demo's components in record order, degrading to
// the legacy single-component scalar for rows predating multi-component
// demos.
func (demo demoDocument) componentNames() []string {
	if len(demo.Components) == 0 {
		if demo.Component != nil && *demo.Component != "" {
			return []string{*demo.Component}
		}
		return nil
	}
	names := make([]string, 0, len(demo.Components))
	for _, component := range demo.Components {
		names = append(names, component.Name)
	}
	return names
}

// source describes what the demo was launched from: its branch, its pull
// request, or nothing recorded.
func (demo demoDocument) source() string {
	switch {
	case demo.Branch != nil && *demo.Branch != "":
		return *demo.Branch
	case demo.PRNumber != nil && *demo.PRNumber > 0:
		return "PR #" + strconv.Itoa(*demo.PRNumber)
	default:
		return "-"
	}
}

// renderApplicationDemoList prints one row per demo with the components it
// runs. -o json|yaml keep the untouched payload, which also carries the TTL
// policy and staging-cluster status this summary leaves out.
func renderApplicationDemoList(command *cobra.Command, payload json.RawMessage) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	if format != outputDefault {
		return renderApplicationPayload(command, payload)
	}
	var document demoListDocument
	if unmarshalError := json.Unmarshal(payload, &document); unmarshalError != nil || document.Demos == nil {
		return renderApplicationPayload(command, payload)
	}
	demos := *document.Demos

	output := command.OutOrStdout()
	if len(demos) == 0 {
		_, _ = fmt.Fprintln(output, "No active demo workspaces.")
		return nil
	}
	writer := table.NewWriter()
	writer.SetOutputMirror(output)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"ID", "Status", "Source", "Components", "URL", "Expires"})
	entryMarked := false
	for _, demo := range demos {
		components, marked := demoComponentSummary(demo)
		entryMarked = entryMarked || marked
		previewURL := "-"
		if demo.PreviewURL != nil && *demo.PreviewURL != "" {
			previewURL = *demo.PreviewURL
		}
		writer.AppendRow(table.Row{
			demo.ID, demo.Status, demo.source(), components, previewURL,
			formatDemoExpiry(demo.ExpiresAt),
		})
	}
	writer.Render()
	if entryMarked {
		_, _ = fmt.Fprintln(output, "* entry component — owns the demo host's root path")
	}
	return nil
}

// demoComponentSummary is the components column: every component the demo
// runs, the entry one marked. It reports whether it marked an entry so the
// caller only prints the legend when there is something to explain.
func demoComponentSummary(demo demoDocument) (string, bool) {
	if len(demo.Components) == 0 {
		if demo.Component != nil && *demo.Component != "" {
			return *demo.Component, false
		}
		return "-", false
	}
	marked := false
	labels := make([]string, 0, len(demo.Components))
	for _, component := range demo.Components {
		label := component.Name
		if component.Entry {
			label += "*"
			marked = true
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, ", "), marked
}

// renderApplicationDemoDetail prints the demo's record, its components, and
// its provisioning steps. The resource inventory and the namespace's
// Kubernetes events stay behind -o json|yaml: they are the deep debugging
// material, and printing them by default buries the summary a stalled demo
// is actually read for.
func renderApplicationDemoDetail(command *cobra.Command, payload json.RawMessage) error {
	format, formatError := structuredFormatFromFlags(command)
	if formatError != nil {
		return formatError
	}
	if format != outputDefault {
		return renderApplicationPayload(command, payload)
	}
	var document demoDetailDocument
	// A payload without a demo record is not the document this summarises —
	// an error envelope, or a wire shape the CLI has not caught up with.
	// Either way the raw JSON is more use than a summary of nothing.
	if unmarshalError := json.Unmarshal(payload, &document); unmarshalError != nil || document.Demo.ID == "" {
		return renderApplicationPayload(command, payload)
	}

	output := command.OutOrStdout()
	demo := document.Demo
	_, _ = fmt.Fprintf(output, "Demo %s\n", demo.ID)
	writeDemoField(output, "Status", demo.Status)
	writeDemoField(output, "Source", demo.source())
	writeDemoField(output, "Namespace", demo.Namespace)
	if demo.PreviewURL != nil {
		writeDemoField(output, "URL", *demo.PreviewURL)
	}
	if demo.DemoStackName != nil {
		writeDemoField(output, "Stack", *demo.DemoStackName)
	}
	writeDemoField(output, "Expires", formatDemoExpiry(demo.ExpiresAt))
	if demo.LastError != nil && *demo.LastError != "" {
		writeDemoField(output, "Last error", *demo.LastError)
	}
	if !document.Inspection.ClusterReachable {
		reason := document.Inspection.UnreachableReason
		if reason == "" {
			reason = "the staging cluster could not be reached, so no live state is available"
		}
		writeDemoField(output, "Cluster", reason)
	}

	renderDemoComponents(output, demo, document.Inspection.Resources)
	renderDemoSteps(output, document.Inspection.Steps)
	return nil
}

// renderDemoComponents prints the components table, one row per workload,
// with the pod each component's logs come from. A demo predating component
// records has none, so its single workload is described from the record's
// own scalars instead.
func renderDemoComponents(output io.Writer, demo demoDocument, resources []demoResourceDocument) {
	_, _ = fmt.Fprintln(output)
	if len(demo.Components) == 0 {
		_, _ = fmt.Fprintln(output, "Component:")
		name := "-"
		if demo.Component != nil && *demo.Component != "" {
			name = *demo.Component
		}
		writeDemoField(output, "Name", name)
		if demo.ImageTag != nil && *demo.ImageTag != "" {
			writeDemoField(output, "Image tag", *demo.ImageTag)
		}
		if demo.ContainerPort != nil && *demo.ContainerPort > 0 {
			writeDemoField(output, "Port", strconv.Itoa(*demo.ContainerPort))
		}
		return
	}

	podsByComponent := demoPodsByComponent(resources)
	_, _ = fmt.Fprintln(output, "Components:")
	writer := table.NewWriter()
	writer.SetOutputMirror(output)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Name", "Entry", "Port", "Path", "Image Tag", "Pods"})
	for _, component := range demo.Components {
		entry := ""
		if component.Entry {
			entry = "yes"
		}
		path := component.IngressPath
		if component.Entry {
			path = "/"
		}
		if path == "" {
			path = "in-cluster only"
		}
		port := "-"
		if component.ContainerPort > 0 {
			port = strconv.Itoa(component.ContainerPort)
		}
		imageTag := component.ImageTag
		if imageTag == "" {
			imageTag = "-"
		}
		pods := "-"
		if names := podsByComponent[component.Name]; len(names) > 0 {
			pods = strings.Join(names, ", ")
		}
		writer.AppendRow(table.Row{component.Name, entry, port, path, imageTag, pods})
	}
	writer.Render()
}

// demoPodsByComponent groups the inventory's live pods by the component that
// owns them, in a stable order so repeated calls print identically.
func demoPodsByComponent(resources []demoResourceDocument) map[string][]string {
	pods := map[string][]string{}
	for _, resource := range resources {
		if resource.Kind != "Pod" || resource.Component == "" || !resource.Present {
			continue
		}
		pods[resource.Component] = append(pods[resource.Component], resource.Name)
	}
	for component := range pods {
		sort.Strings(pods[component])
	}
	return pods
}

// renderDemoSteps prints the provisioning steps in order — the part that
// explains what a demo stuck in provisioning is waiting on.
func renderDemoSteps(output io.Writer, steps []demoStepDocument) {
	if len(steps) == 0 {
		return
	}
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "Steps:")
	writer := table.NewWriter()
	writer.SetOutputMirror(output)
	writer.SetStyle(table.StyleRounded)
	writer.AppendHeader(table.Row{"Step", "Status", "Detail"})
	for _, step := range steps {
		writer.AppendRow(table.Row{step.Label, step.Status, step.Detail})
	}
	writer.Render()
}

func writeDemoField(output io.Writer, label string, value string) {
	if value == "" {
		return
	}
	_, _ = fmt.Fprintf(output, "  %-12s %s\n", label+":", value)
}

// formatDemoExpiry renders a TTL deadline as the time left on it, which is
// the question a demo's expiry is ever asked. An unparseable timestamp is
// printed as it arrived rather than swallowed.
func formatDemoExpiry(timestamp string) string {
	if timestamp == "" {
		return "-"
	}
	expiresAt, parseError := time.Parse(time.RFC3339, timestamp)
	if parseError != nil {
		return timestamp
	}
	remaining := time.Until(expiresAt)
	switch {
	case remaining <= 0:
		return "expired"
	case remaining < time.Hour:
		return fmt.Sprintf("in %dm", int(remaining.Minutes()))
	case remaining < 24*time.Hour:
		return fmt.Sprintf("in %dh", int(remaining.Hours()))
	default:
		return fmt.Sprintf("in %dd", int(remaining.Hours()/24))
	}
}

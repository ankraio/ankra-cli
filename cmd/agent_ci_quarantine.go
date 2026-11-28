package cmd

// The CI nodes the platform quarantined, on `ankra cluster agent ci get`.
//
// A CI node whose kubelet does not start pipeline step pods - a sandbox it
// cannot create, a volume it cannot mount - is taken out of rotation by the
// platform for an organisation whose CI node quarantine is on: it carries the
// label and NoSchedule taint ankra.io/ci-quarantined, whose value is the
// reason, and the platform's stamp of when and why. Pipeline steps are kept
// off it and retried elsewhere, and the platform replaces or releases it. The
// settings answer does not carry it, so `get` reads the cluster's nodes for
// the label, best effort: a read that fails says so rather than reading as no
// quarantined node.

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"ankra/internal/client"
)

// The label and annotations the platform's quarantine writes.
const (
	ciQuarantinedNodeLabel           = "ankra.io/ci-quarantined"
	ciQuarantinedAtAnnotation        = "ankra.io/ci-quarantined-at"
	ciQuarantineDetailAnnotation     = "ankra.io/ci-quarantined-detail"
	ciReplacementRequestedAnnotation = "ankra.io/ci-replacement-requested-at"
	ciStartFailuresAnnotation        = "ankra.io/ci-start-failures"
)

// agentCIQuarantinedNode is one quarantined node as `get` prints it.
type agentCIQuarantinedNode struct {
	name          string
	reason        string
	since         string
	detail        string
	isReplacing   bool
	hasStartFails bool
}

// readAgentCIQuarantinedNodes lists the cluster's nodes carrying the
// quarantine label. A failed read is answered as its error.
func readAgentCIQuarantinedNodes(clusterID string) ([]agentCIQuarantinedNode, error) {
	response, readError := apiClient.GetResources(clusterID, client.GetResourcesRequest{
		ResourceRequests: []client.ResourceRequestItem{{
			Kind: "Node", Version: "v1", LabelSelector: ciQuarantinedNodeLabel,
		}},
	})
	if readError != nil {
		return nil, readError
	}
	if response == nil || len(response.ResourceResponses) == 0 {
		return nil, fmt.Errorf("the platform answered no node listing")
	}
	listing := response.ResourceResponses[0]
	if listing.Status == "error" {
		if listing.ErrorResponse != nil && strings.TrimSpace(*listing.ErrorResponse) != "" {
			return nil, fmt.Errorf("%s", strings.TrimSpace(*listing.ErrorResponse))
		}
		return nil, fmt.Errorf("the cluster's agent could not list its nodes")
	}
	nodes := make([]agentCIQuarantinedNode, 0, len(listing.Items))
	for _, item := range listing.Items {
		node, isObject := item.(map[string]interface{})
		if !isObject {
			continue
		}
		reason, isLabelled := getNestedValue(node, "metadata", "labels", ciQuarantinedNodeLabel)
		if !isLabelled {
			continue
		}
		_, isReplacing := getNestedValue(node, "metadata", "annotations", ciReplacementRequestedAnnotation)
		_, hasStartFails := getNestedValue(node, "metadata", "annotations", ciStartFailuresAnnotation)
		nodes = append(nodes, agentCIQuarantinedNode{
			name:          getNestedString(node, "metadata", "name"),
			reason:        fmt.Sprint(reason),
			since:         getNestedString(node, "metadata", "annotations", ciQuarantinedAtAnnotation),
			detail:        getNestedString(node, "metadata", "annotations", ciQuarantineDetailAnnotation),
			isReplacing:   isReplacing,
			hasStartFails: hasStartFails,
		})
	}
	sort.Slice(nodes, func(first int, second int) bool { return nodes[first].name < nodes[second].name })
	return nodes, nil
}

// getNestedValue walks a decoded object by keys, reporting whether the last
// one is present.
func getNestedValue(object map[string]interface{}, keys ...string) (interface{}, bool) {
	var current interface{} = object
	for _, key := range keys {
		level, isObject := current.(map[string]interface{})
		if !isObject {
			return nil, false
		}
		value, isPresent := level[key]
		if !isPresent {
			return nil, false
		}
		current = value
	}
	return current, true
}

// printAgentCIQuarantinedNodes prints the quarantined CI nodes of a cluster
// that runs pipeline steps. A cluster with pipeline steps off has none to
// show and is not read.
func printAgentCIQuarantinedNodes(out io.Writer, settings *client.AgentCISettings, clusterID string) {
	if settings.CIWorkerCount < 1 {
		return
	}
	nodes, readError := readAgentCIQuarantinedNodes(clusterID)
	if readError != nil {
		_, _ = fmt.Fprintf(out, "\nQuarantined CI nodes: could not be read (%v)\n", readError)
		return
	}
	if len(nodes) == 0 {
		_, _ = fmt.Fprintln(out, "\nQuarantined CI nodes: none")
		return
	}
	_, _ = fmt.Fprintf(out, "\nQuarantined CI nodes: %d (pipeline steps are kept off them and retried elsewhere)\n",
		len(nodes))
	for _, node := range nodes {
		line := fmt.Sprintf("  %s: %s", node.name, node.reason)
		if node.since != "" {
			line += ", since " + node.since
		}
		switch {
		case node.isReplacing:
			line += "; being replaced"
		case !node.hasStartFails:
			line += "; pods start there again, so it is released on the platform's next pass"
		}
		if node.detail != "" {
			line += " (" + node.detail + ")"
		}
		_, _ = fmt.Fprintln(out, line)
	}
}

package client

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The node is path-escaped, so a providerID's slashes reach the platform as
// one segment; force_drain and wait travel as query parameters (a DELETE has
// no body).
func TestRemoveNodeGroupNodeEscapesTheNodeAndSendsTheDrainChoice(t *testing.T) {
	var method, escapedPath, forceDrain, wait string
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		method, escapedPath = request.Method, request.URL.EscapedPath()
		forceDrain, wait = request.URL.Query().Get("force_drain"), request.URL.Query().Get("wait")
		jsonResponse(t, writer, http.StatusOK, map[string]any{
			"group_name": "opensearch", "node_name": "prod-opensearch-0", "node_id": "f3c1a3a2-6a55-4c1e-9d1c-2f3e4a5b6c7d",
			"previous_count": 4, "new_count": 3, "operation_id": "4b0e3f6a-6c2e-4d2a-9d7e-1f0e2a3b4c5d",
		})
	})

	result, removeError := client.RemoveNodeGroupNode(context.Background(), "hetzner", "cluster-1", "opensearch",
		"hcloud://168428300", DrainOptions{ForceDrain: true}, true)
	if removeError != nil || result == nil || result.Accepted {
		t.Fatalf("RemoveNodeGroupNode = (%+v, %v)", result, removeError)
	}
	if method != http.MethodDelete ||
		escapedPath != "/api/v1/clusters/hetzner/cluster-1/node-groups/opensearch/nodes/hcloud:%2F%2F168428300" {
		t.Fatalf("request = %s %s", method, escapedPath)
	}
	if forceDrain != "true" || wait != "true" {
		t.Fatalf("query force_drain=%q wait=%q, want both true", forceDrain, wait)
	}
	if result.NodeName != "prod-opensearch-0" || result.PreviousCount != 4 || result.NewCount != 3 ||
		result.OperationID == nil || *result.OperationID != "4b0e3f6a-6c2e-4d2a-9d7e-1f0e2a3b4c5d" {
		t.Fatalf("result = %+v", result)
	}
}

// An unforced request sends no force_drain. A removal the platform decided
// before answering 202 comes back as one accepted result: the worker that
// leaves, the counts and the operation, all from that one answer.
func TestRemoveNodeGroupNodeAcceptedNamesTheNodeThatLeaves(t *testing.T) {
	var forceDrainSent bool
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		_, forceDrainSent = request.URL.Query()["force_drain"]
		jsonResponse(t, writer, http.StatusAccepted, map[string]any{
			"status": "accepted", "group_name": "opensearch", "node_name": "prod-opensearch-0",
			"node_id": "f3c1a3a2-6a55-4c1e-9d1c-2f3e4a5b6c7d", "previous_count": 4, "new_count": 3,
			"operation_id": "4b0e3f6a-6c2e-4d2a-9d7e-1f0e2a3b4c5d",
		})
	})

	result, removeError := client.RemoveNodeGroupNode(context.Background(), "upcloud", "cluster-1", "opensearch",
		"prod-opensearch-0", DrainOptions{}, false)
	if removeError != nil || result == nil || !result.Accepted {
		t.Fatalf("RemoveNodeGroupNode = (%+v, %v)", result, removeError)
	}
	if result.NodeName != "prod-opensearch-0" || result.NodeID != "f3c1a3a2-6a55-4c1e-9d1c-2f3e4a5b6c7d" ||
		result.PreviousCount != 4 || result.NewCount != 3 ||
		result.OperationID == nil || *result.OperationID != "4b0e3f6a-6c2e-4d2a-9d7e-1f0e2a3b4c5d" {
		t.Fatalf("result = %+v", result)
	}
	if forceDrainSent {
		t.Fatal("an unforced removal must not send force_drain")
	}
}

// A removal the platform was still deciding when it answered names no node:
// it comes back accepted, with no node and no operation.
func TestRemoveNodeGroupNodeAcceptedWhileStillDecidingHasNoResult(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusAccepted, map[string]any{"status": "accepted"})
	})

	result, removeError := client.RemoveNodeGroupNode(context.Background(), "hetzner", "cluster-1", "opensearch",
		"prod-opensearch-0", DrainOptions{}, false)
	if removeError != nil || result == nil || !result.Accepted || result.NodeName != "" || result.OperationID != nil {
		t.Fatalf("RemoveNodeGroupNode = (%+v, %v)", result, removeError)
	}
}

// The --wait result keeps the platform's document, so -o json prints what
// the platform answered rather than the fields the CLI declares.
func TestRemoveNodeGroupNodeResultKeepsThePlatformsDocument(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusOK, map[string]any{
			"group_name": "opensearch", "node_name": "prod-opensearch-0", "node_id": "f3c1a3a2-6a55-4c1e-9d1c-2f3e4a5b6c7d",
			"previous_count": 4, "new_count": 3, "operation_id": nil, "added_later": "kept",
		})
	})

	result, removeError := client.RemoveNodeGroupNode(context.Background(), "hetzner", "cluster-1", "opensearch",
		"prod-opensearch-0", DrainOptions{}, true)
	if removeError != nil || result == nil {
		t.Fatalf("RemoveNodeGroupNode = (%+v, %v)", result, removeError)
	}
	if document := string(result.RawJSON()); !strings.Contains(document, `"added_later":"kept"`) {
		t.Fatalf("raw document = %s, want the platform's answer", document)
	}
}

// A refusal is the platform's 409 with its reason as the message, with and
// without --wait.
func TestRemoveNodeGroupNodeRefusalCarriesThePlatformsReason(t *testing.T) {
	const reason = "Node 'prod-opensearch-0' is already being removed from node group 'opensearch'"
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusConflict, map[string]any{"detail": reason})
	})
	for _, wait := range []bool{false, true} {
		_, removeError := client.RemoveNodeGroupNode(context.Background(), "hetzner", "cluster-1", "opensearch",
			"prod-opensearch-0", DrainOptions{}, wait)
		var unexpected *UnexpectedResponseError
		if !errors.As(removeError, &unexpected) || unexpected.StatusCode != http.StatusConflict ||
			unexpected.Detail != reason || removeError.Error() != reason {
			t.Fatalf("wait=%v: expected the platform's 409 with its reason, got %v", wait, removeError)
		}
	}
}

// A node the cluster does not have is the platform's 404, kept as such so
// the command exits not-found.
func TestRemoveNodeGroupNodeKeepsTheNotFound(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusNotFound, map[string]any{"detail": "Node 'ghost' not found in this cluster"})
	})

	_, removeError := client.RemoveNodeGroupNode(context.Background(), "hetzner", "cluster-1", "opensearch",
		"ghost", DrainOptions{}, true)
	var unexpected *UnexpectedResponseError
	if !errors.As(removeError, &unexpected) || unexpected.StatusCode != http.StatusNotFound ||
		unexpected.Detail != "Node 'ghost' not found in this cluster" || removeError.Error() != unexpected.Detail {
		t.Fatalf("expected the platform's 404 with its reason, got %v", removeError)
	}
}

// The cluster API's router answers an unknown route with {"detail":
// "Not Found"} (observed on the production platform), which reaches the
// caller as that detail, distinct from the node-not-found reason.
func TestRemoveNodeGroupNodeKeepsTheRoutersNotFoundDetail(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusNotFound, map[string]any{"detail": "Not Found"})
	})

	_, removeError := client.RemoveNodeGroupNode(context.Background(), "hetzner", "cluster-1", "opensearch",
		"prod-opensearch-0", DrainOptions{}, false)
	var unexpected *UnexpectedResponseError
	if !errors.As(removeError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "Not Found" {
		t.Fatalf("expected the router's 404 with detail %q, got %v", "Not Found", removeError)
	}
}

// Anything in front of the API that answers a bare 404 carries no detail at
// all, so a caller can tell it from a missing node too.
func TestRemoveNodeGroupNodeLeavesARouterNotFoundWithoutADetail(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		http.NotFound(writer, request)
	})

	_, removeError := client.RemoveNodeGroupNode(context.Background(), "hetzner", "cluster-1", "opensearch",
		"prod-opensearch-0", DrainOptions{}, true)
	var unexpected *UnexpectedResponseError
	if !errors.As(removeError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "" {
		t.Fatalf("expected a 404 with no detail, got %v", removeError)
	}
}

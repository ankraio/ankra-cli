package client

import (
	"context"
	"errors"
	"net/http"
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

	result, accepted, removeError := client.RemoveNodeGroupNode(context.Background(), "hetzner", "cluster-1", "opensearch",
		"hcloud://168428300", DrainOptions{ForceDrain: true}, true)
	if removeError != nil || accepted != nil {
		t.Fatalf("RemoveNodeGroupNode = (%+v, %+v, %v)", result, accepted, removeError)
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

// An unforced request sends no force_drain, and a removal the platform
// accepted without waiting comes back as accepted with its operation.
func TestRemoveNodeGroupNodeAcceptedCarriesTheOperation(t *testing.T) {
	var forceDrainSent bool
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		_, forceDrainSent = request.URL.Query()["force_drain"]
		jsonResponse(t, writer, http.StatusAccepted, map[string]any{
			"status": "accepted", "node_name": "prod-opensearch-0", "operation_id": "4b0e3f6a-6c2e-4d2a-9d7e-1f0e2a3b4c5d",
		})
	})

	result, accepted, removeError := client.RemoveNodeGroupNode(context.Background(), "upcloud", "cluster-1", "opensearch",
		"prod-opensearch-0", DrainOptions{}, false)
	if removeError != nil || result != nil || accepted == nil {
		t.Fatalf("RemoveNodeGroupNode = (%+v, %+v, %v)", result, accepted, removeError)
	}
	if accepted.OperationID != "4b0e3f6a-6c2e-4d2a-9d7e-1f0e2a3b4c5d" {
		t.Fatalf("accepted = %+v", accepted)
	}
	if forceDrainSent {
		t.Fatal("an unforced removal must not send force_drain")
	}
}

// A node the cluster does not have is the platform's 404, kept as such so
// the command exits not-found.
func TestRemoveNodeGroupNodeKeepsTheNotFound(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		jsonResponse(t, writer, http.StatusNotFound, map[string]any{"detail": "Node 'ghost' not found in this cluster"})
	})

	_, _, removeError := client.RemoveNodeGroupNode(context.Background(), "hetzner", "cluster-1", "opensearch",
		"ghost", DrainOptions{}, true)
	var unexpected *UnexpectedResponseError
	if !errors.As(removeError, &unexpected) || unexpected.StatusCode != http.StatusNotFound ||
		unexpected.Detail != "Node 'ghost' not found in this cluster" || removeError.Error() != unexpected.Detail {
		t.Fatalf("expected the platform's 404 with its reason, got %v", removeError)
	}
}

// A platform that does not serve the route answers the router's bare 404,
// which carries no detail, so a caller can tell it from a missing node.
func TestRemoveNodeGroupNodeLeavesARouterNotFoundWithoutADetail(t *testing.T) {
	client := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		http.NotFound(writer, request)
	})

	_, _, removeError := client.RemoveNodeGroupNode(context.Background(), "hetzner", "cluster-1", "opensearch",
		"prod-opensearch-0", DrainOptions{}, true)
	var unexpected *UnexpectedResponseError
	if !errors.As(removeError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "" {
		t.Fatalf("expected a 404 with no detail, got %v", removeError)
	}
}

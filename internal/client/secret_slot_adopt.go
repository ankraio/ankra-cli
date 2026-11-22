package client

// Adopting one key of a live Kubernetes Secret into a secret slot
// (POST /api/v1/org/secret-slots/adopt, cluster#6139). The platform reads
// the key from the cluster through its agent and stores it in a new slot;
// the value never travels to or from the CLI: the request names the Secret
// and the key, and the answer is the slot without its value.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// SecretSlotAdoptRequest names one key of a Kubernetes Secret and the slot
// to create for it: the platform's SecretSlotAdoptRequest. It has no value
// member, by design.
type SecretSlotAdoptRequest struct {
	// Label is the new slot's name, unique in the organisation.
	Label string `json:"label"`
	// Hint is a note about what the secret is for; empty sends none.
	Hint string `json:"hint,omitempty"`
	// ClusterID is the cluster the Secret runs in.
	ClusterID string `json:"cluster_id"`
	// Namespace and Name are the Secret's.
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Key is the key under the Secret's data to adopt. No other key is read
	// out.
	Key string `json:"key"`
	// StripTrailingLineEnding drops exactly one trailing \n or \r\n from the
	// value before it is stored. It is always sent, false included, so the
	// stored bytes never depend on a platform default.
	StripTrailingLineEnding bool `json:"strip_trailing_line_ending"`
}

// AdoptSecretSlot asks the platform to read one key of a live Secret and
// store it in a new slot, and returns the slot. Only the members SecretSlot
// declares are decoded, so nothing else an answer carries reaches the
// caller.
//
// Each refusal arrives as the type the CLI's exit codes key on:
//   - 401: ErrUnauthorized.
//   - 403: *PermissionDeniedError, naming the permission (the token lacks
//     variables.write) or carrying the platform's sentence (the caller
//     lacks kubernetes.secrets_reveal on the cluster).
//   - 503 with the frozen CLUSTER_OFFLINE, NO_AGENT or AGENT_TIMEOUT
//     envelope: *ClusterAgentUnavailableError, with the seconds to wait from
//     the body, else the Retry-After header.
//   - any other refusal with a detail (400 a slot rule or a value a slot
//     cannot hold, 404 no such cluster, Secret or key, 409 a taken label or
//     an answer from the synced cache, 422 a malformed request, 502 the
//     agent could not read the Secret): *UnexpectedResponseError whose
//     message is the platform's sentence.
//
// An adopt is a POST, so the client's transport never retries it.
func (c *Client) AdoptSecretSlot(ctx context.Context, request SecretSlotAdoptRequest) (*SecretSlot, error) {
	encoded, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	// One literal, so the route census check (cluster_routes_test.go) sees it.
	response, body, sendError := c.sendOrgRouteRequest(ctx, http.MethodPost, c.BaseURL+"/api/v1/org/secret-slots/adopt", encoded)
	if sendError != nil {
		return nil, sendError
	}
	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var slot SecretSlot
		if decodeError := decodeMCPResponse(body, &slot, false); decodeError != nil {
			return nil, decodeError
		}
		return &slot, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusForbidden:
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return nil, denied
		}
		if detail := mcpDetailFromBody(body); detail != "" {
			return nil, &PermissionDeniedError{Detail: detail}
		}
	case http.StatusServiceUnavailable:
		if unavailable := agentUnavailableFromResponse(response, body); unavailable != nil {
			return nil, unavailable
		}
	}
	// A 403 that carries no permission body and a 503 that is not the
	// agent envelope leave the switch on purpose: like every other status
	// they get the platform's detail sentence, or the unexpected-response
	// error when there is none.
	if detail := mcpDetailFromBody(body); detail != "" {
		return nil, newBackendDetailError(response.StatusCode, detail)
	}
	return nil, newUnexpectedResponseError("adopt secret slot request failed",
		response.StatusCode, redactedBodyForError(body, 500))
}

// agentUnavailableFromResponse reads the platform's frozen 503 envelope
// ({"error_code", "detail", "retry_after"} plus a Retry-After header). A 503
// without an error code is not the envelope (a proxy answered), and returns
// nil.
func agentUnavailableFromResponse(response *http.Response, body []byte) *ClusterAgentUnavailableError {
	var envelope struct {
		ErrorCode  string `json:"error_code"`
		Detail     string `json:"detail"`
		RetryAfter int    `json:"retry_after"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.ErrorCode == "" {
		return nil
	}
	retryAfter := envelope.RetryAfter
	if retryAfter <= 0 {
		if seconds, parseError := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After"))); parseError == nil && seconds > 0 {
			retryAfter = seconds
		}
	}
	return &ClusterAgentUnavailableError{Code: envelope.ErrorCode, Detail: envelope.Detail, RetryAfter: retryAfter}
}

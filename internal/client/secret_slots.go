package client

// Organisation secret slots: values stored in Vault and referenced from
// manifests, addon values and MCP server env by their ${SECRET_SLOT:<id>}
// sentinel (GET/POST /api/v1/org/secret-slots, PUT/DELETE
// /api/v1/org/secret-slots/{slot_id}). A value only ever travels inbound:
// no route returns one, and nothing here logs or echoes a request body.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
)

// ErrSecretSlotNotFound is returned when the platform answers 404 for a
// slot id: there is no slot with that id in the organisation.
var ErrSecretSlotNotFound = errors.New("secret slot not found")

// SecretSlotReferencedError is the platform's refusal to delete a slot that
// live resources still reference (409). Detail is the platform's sentence,
// which names the first of them.
type SecretSlotReferencedError struct {
	Detail string
}

func (referenced *SecretSlotReferencedError) Error() string {
	return referenced.Detail
}

// SecretSlotGenerate asks the platform to create a random value nobody sees.
// Zero values leave the platform defaults (32 characters, no prefix).
type SecretSlotGenerate struct {
	Length int    `json:"length,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

// SecretSlotCreateRequest creates a slot. Exactly one of Value and Generate
// is sent.
type SecretSlotCreateRequest struct {
	Label      string              `json:"label"`
	Value      string              `json:"value,omitempty"`
	Hint       string              `json:"hint,omitempty"`
	TTLSeconds int                 `json:"ttl_seconds,omitempty"`
	Generate   *SecretSlotGenerate `json:"generate,omitempty"`
}

// SecretSlotReplaceRequest replaces a slot's value in place. Exactly one of
// Value and Generate is sent.
type SecretSlotReplaceRequest struct {
	Value    string              `json:"value,omitempty"`
	Generate *SecretSlotGenerate `json:"generate,omitempty"`
}

func (c *Client) secretSlotsURL(suffix string) string {
	return c.BaseURL + "/api/v1/org/secret-slots" + suffix
}

// CreateSecretSlot stores a secret value server-side and returns the slot,
// whose Sentinel ("${SECRET_SLOT:<id>}") is what an MCP server env value
// should carry instead of the plaintext.
func (c *Client) CreateSecretSlot(ctx context.Context, label, value string) (*SecretSlot, error) {
	return c.CreateSecretSlotWith(ctx, SecretSlotCreateRequest{Label: label, Value: value})
}

// CreateSecretSlotWith creates a slot from a value or a generated one. The
// value is never returned.
func (c *Client) CreateSecretSlotWith(ctx context.Context, request SecretSlotCreateRequest) (*SecretSlot, error) {
	if (request.Value == "") == (request.Generate == nil) {
		return nil, errors.New("send exactly one of a value or generate")
	}
	encoded, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	body, requestError := c.doSecretSlotRequest(ctx, http.MethodPost, c.secretSlotsURL(""), encoded)
	if requestError != nil {
		return nil, requestError
	}
	var slot SecretSlot
	if decodeError := decodeMCPResponse(body, &slot, false); decodeError != nil {
		return nil, decodeError
	}
	return &slot, nil
}

// ReplaceSecretSlot replaces a slot's value in place. The slot keeps its id,
// so every ${SECRET_SLOT:<id>} reference stays valid.
func (c *Client) ReplaceSecretSlot(ctx context.Context, slotID string, request SecretSlotReplaceRequest) (*SecretSlot, error) {
	if (request.Value == "") == (request.Generate == nil) {
		return nil, errors.New("send exactly one of a value or generate")
	}
	encoded, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}
	body, requestError := c.doSecretSlotRequest(ctx, http.MethodPut,
		c.secretSlotsURL("/"+neturl.PathEscape(slotID)), encoded)
	if requestError != nil {
		return nil, requestError
	}
	var slot SecretSlot
	if decodeError := decodeMCPResponse(body, &slot, false); decodeError != nil {
		return nil, decodeError
	}
	return &slot, nil
}

// ListSecretSlots lists the organisation's secret slots, newest first:
// labels, access metadata and references only; values never come back.
func (c *Client) ListSecretSlots(ctx context.Context) (*SecretSlotListResult, error) {
	body, requestError := c.doSecretSlotRequest(ctx, http.MethodGet, c.secretSlotsURL(""), nil)
	if requestError != nil {
		if errors.Is(requestError, ErrSecretSlotNotFound) {
			return nil, errors.New("this platform does not serve the organisation secret slot routes")
		}
		return nil, requestError
	}
	var slots SecretSlotListResult
	if decodeError := decodeMCPResponse(body, &slots, false); decodeError != nil {
		return nil, decodeError
	}
	return &slots, nil
}

// DeleteSecretSlot removes a secret slot and its stored value. The platform
// refuses a slot live resources still reference with a
// *SecretSlotReferencedError.
func (c *Client) DeleteSecretSlot(ctx context.Context, slotID string) (*SecretSlotDeleteResult, error) {
	body, requestError := c.doSecretSlotRequest(ctx, http.MethodDelete,
		c.secretSlotsURL("/"+neturl.PathEscape(slotID)), nil)
	if requestError != nil {
		return nil, requestError
	}
	var result SecretSlotDeleteResult
	if decodeError := decodeMCPResponse(body, &result, true); decodeError != nil {
		return nil, decodeError
	}
	return &result, nil
}

// doSecretSlotRequest sends a secret-slot request: 404 maps to
// ErrSecretSlotNotFound and 409 to *SecretSlotReferencedError (the only 409
// a slot id route answers); every other refusal is the platform's detail.
func (c *Client) doSecretSlotRequest(ctx context.Context, method, url string, payload []byte) ([]byte, error) {
	body, requestError := c.doOrgRouteRequest(ctx, method, url, payload, ErrSecretSlotNotFound)
	var refusal *orgRouteRefusal
	if method == http.MethodDelete && errors.As(requestError, &refusal) && refusal.StatusCode == http.StatusConflict {
		return nil, &SecretSlotReferencedError{Detail: refusal.Detail}
	}
	return body, requestError
}

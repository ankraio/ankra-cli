package client

// Registry connection checks, a credential's rotation status and the
// learned AI profile (ankra-hq41xb.23). Every answer is returned as the
// platform sent it; none carries secret material.

import (
	"context"
	"encoding/json"
	"net/http"
	neturl "net/url"
)

const (
	registryConnectionDiscoverPath = "/api/v1/org/registry-connections/discover"
	registryConnectionVerifyPath   = "/api/v1/org/registry-connections/verify"
	aiMemoryProfilePath            = "/api/v1/org/ai-memory/profile"
)

// DiscoverRegistryConnection lists what an admin credential sees on a
// registry before any registry entry points at it. The credential is named,
// never sent. A connection that fails answers ok false with the reason.
// POST /api/v1/org/registry-connections/discover
func (c *Client) DiscoverRegistryConnection(ctx context.Context, registryURL string, adminCredentialName string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, registryConnectionDiscoverPath,
		map[string]string{"url": registryURL, "admin_credential_name": adminCredentialName})
}

// VerifyRegistryConnection checks that an admin credential can manage
// robots on an integrated registry entry. Nothing is minted.
// POST /api/v1/org/registry-connections/verify
func (c *Client) VerifyRegistryConnection(ctx context.Context, registry string, adminCredentialName string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, registryConnectionVerifyPath,
		map[string]string{"registry": registry, "admin_credential_name": adminCredentialName})
}

// GetCredentialPropagation reports where the credential's latest rotation
// has got to in each linked cluster and whether the old value is safe to
// revoke.
// GET /api/v1/org/credentials/{credential_id}/propagation
func (c *Client) GetCredentialPropagation(ctx context.Context, credentialID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, "/api/v1/org/credentials/"+neturl.PathEscape(credentialID)+"/propagation", nil)
}

// AIMemoryProfileUpdate replaces the calling user's learned profile, turns
// learning on or off, or both. A member left nil keeps its stored value.
type AIMemoryProfileUpdate struct {
	Profile         map[string]any `json:"profile,omitempty"`
	LearningEnabled *bool          `json:"learning_enabled,omitempty"`
}

// GetAIMemoryProfile reads what the assistant has learned about the calling
// user and whether learning is on.
// GET /api/v1/org/ai-memory/profile
func (c *Client) GetAIMemoryProfile(ctx context.Context) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, aiMemoryProfilePath, nil)
}

// UpdateAIMemoryProfile writes the calling user's learned profile.
// PUT /api/v1/org/ai-memory/profile
func (c *Client) UpdateAIMemoryProfile(ctx context.Context, update AIMemoryProfileUpdate) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPut, aiMemoryProfilePath, update)
}

// ResetAIMemoryProfile clears what the assistant has learned about the
// calling user's preferences. Stored memories are not affected.
// DELETE /api/v1/org/ai-memory/profile
func (c *Client) ResetAIMemoryProfile(ctx context.Context) error {
	return c.sendJSONContext(ctx, http.MethodDelete, c.BaseURL+aiMemoryProfilePath, nil, nil)
}

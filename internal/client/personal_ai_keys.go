package client

import (
	"fmt"
	"net/http"
	"net/url"
)

// Personal AI keys: the Anthropic and OpenRouter API keys a member adds to
// their own profile, reachable over the bearer /api/v1/org/account/ai-keys
// routes (the CLI twins of the portal's Profile > Connected accounts > Your AI
// keys). The chats and pull request reviews the member starts run on them, in
// the member's order, and a key only ever serves the member who added it. The
// raw key travels once, in the add request, and never comes back: every answer
// carries only a masked preview.
//
// The organisation switch (/api/v1/org/ai-settings/personal-keys) says whether
// the organisation's work may run on its members' keys at all. Every member can
// read it; changing it needs the ai.manage permission.

// The personal key statuses the platform reports.
const (
	PersonalAIKeyStatusActive      = "active"
	PersonalAIKeyStatusOutOfCredit = "out_of_credit"
	PersonalAIKeyStatusRejected    = "rejected"
)

// The organisation policies a key listing reports: allowed runs the member's
// work on their keys, disabled means an administrator turned personal keys off,
// and self_hosted means the organisation runs AI on its own endpoint, so
// members' keys are never used for its work.
const (
	PersonalAIKeysPolicyAllowed    = "allowed"
	PersonalAIKeysPolicyDisabled   = "disabled"
	PersonalAIKeysPolicySelfHosted = "self_hosted"
)

// PersonalAIKey is one AI provider key on the member's profile. The key itself
// is never returned; KeyPreview is the provider prefix and the last four
// characters. Position is where the key sits in the order work tries keys,
// from 0. RetryAfter is set for an out_of_credit key: when it is next tried.
type PersonalAIKey struct {
	RawDocument `json:"-" yaml:"-"`
	ID          string  `json:"id" yaml:"id"`
	Provider    string  `json:"provider" yaml:"provider"`
	Label       string  `json:"label" yaml:"label"`
	KeyPreview  string  `json:"key_preview" yaml:"key_preview"`
	Position    int     `json:"position" yaml:"position"`
	Status      string  `json:"status" yaml:"status"`
	RetryAfter  *string `json:"retry_after" yaml:"retry_after"`
	LastUsedAt  *string `json:"last_used_at" yaml:"last_used_at"`
	CreatedAt   string  `json:"created_at" yaml:"created_at"`
}

// PersonalAIKeyListing is the member's keys, in the order work tries them, and
// what the current organisation does with them (PersonalAIKeysPolicy*).
type PersonalAIKeyListing struct {
	RawDocument        `json:"-" yaml:"-"`
	Keys               []PersonalAIKey `json:"keys" yaml:"keys"`
	OrganisationPolicy string          `json:"organisation_policy" yaml:"organisation_policy"`
}

// PersonalAIKeyList is the member's keys in their new order, as a reorder
// answers them.
type PersonalAIKeyList struct {
	RawDocument `json:"-" yaml:"-"`
	Keys        []PersonalAIKey `json:"keys" yaml:"keys"`
}

// PersonalAIKeyTestResult is what the provider said about a stored key, and
// the key with the state the test recorded. Message says why the test failed,
// and is empty when it passed. A key the provider refuses is still a
// successful request: IsValid is false.
type PersonalAIKeyTestResult struct {
	RawDocument `json:"-" yaml:"-"`
	Key         PersonalAIKey `json:"key" yaml:"key"`
	IsValid     bool          `json:"is_valid" yaml:"is_valid"`
	Message     string        `json:"message" yaml:"message"`
}

// PersonalAIKeysPolicy is the organisation switch: whether the organisation's
// work may run on its members' own AI keys.
type PersonalAIKeysPolicy struct {
	RawDocument `json:"-" yaml:"-"`
	Allowed     bool `json:"allowed" yaml:"allowed"`
}

// AddPersonalAIKeyRequest is the body of an add. Provider is anthropic or
// openrouter; APIKey is the raw key, which the platform checks with the
// provider before storing it. Label is optional.
type AddPersonalAIKeyRequest struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	Label    string `json:"label,omitempty"`
}

// ListPersonalAIKeys returns the member's keys and the organisation's policy.
func (c *Client) ListPersonalAIKeys() (*PersonalAIKeyListing, error) {
	var listing PersonalAIKeyListing
	if requestError := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/account/ai-keys", nil, &listing); requestError != nil {
		return nil, requestError
	}
	return &listing, nil
}

// AddPersonalAIKey checks a key with its provider, stores it and appends it to
// the end of the member's order. The platform answers why it refused a key in
// the error's detail (not an API key of the provider, refused by the provider,
// a duplicate, a second key of the same Anthropic account, ten keys already).
func (c *Client) AddPersonalAIKey(request AddPersonalAIKeyRequest) (*PersonalAIKey, error) {
	var key PersonalAIKey
	if requestError := c.sendJSON(http.MethodPost, c.BaseURL+"/api/v1/org/account/ai-keys", request, &key); requestError != nil {
		return nil, requestError
	}
	return &key, nil
}

// ReorderPersonalAIKeys sets the order work tries the member's keys in. The
// platform refuses a list that does not name every key exactly once, so a
// caller that missed a key cannot drop it.
func (c *Client) ReorderPersonalAIKeys(keyIDs []string) (*PersonalAIKeyList, error) {
	payload := map[string][]string{"key_ids": append(make([]string, 0, len(keyIDs)), keyIDs...)}
	var list PersonalAIKeyList
	if requestError := c.sendJSON(http.MethodPut, c.BaseURL+"/api/v1/org/account/ai-keys/order", payload, &list); requestError != nil {
		return nil, requestError
	}
	return &list, nil
}

// RenamePersonalAIKey changes the label shown beside a key; an empty label
// clears it.
func (c *Client) RenamePersonalAIKey(keyID string, label string) (*PersonalAIKey, error) {
	var key PersonalAIKey
	keyURL := fmt.Sprintf("%s/api/v1/org/account/ai-keys/%s", c.BaseURL, url.PathEscape(keyID))
	if requestError := c.sendJSON(http.MethodPatch, keyURL, map[string]string{"label": label}, &key); requestError != nil {
		return nil, requestError
	}
	return &key, nil
}

// DeletePersonalAIKey removes a key and its stored secret, so work the member
// starts afterwards no longer runs on it.
func (c *Client) DeletePersonalAIKey(keyID string) error {
	keyURL := fmt.Sprintf("%s/api/v1/org/account/ai-keys/%s", c.BaseURL, url.PathEscape(keyID))
	return c.sendJSON(http.MethodDelete, keyURL, nil, nil)
}

// TestPersonalAIKey asks the provider about a stored key and records the
// answer: a refused key becomes rejected, an empty balance out_of_credit, and a
// passing key active again.
func (c *Client) TestPersonalAIKey(keyID string) (*PersonalAIKeyTestResult, error) {
	var result PersonalAIKeyTestResult
	testURL := fmt.Sprintf("%s/api/v1/org/account/ai-keys/%s/test", c.BaseURL, url.PathEscape(keyID))
	if requestError := c.sendJSON(http.MethodPost, testURL, nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetPersonalAIKeysPolicy reads whether the organisation's work may run on its
// members' own AI keys (member-level).
func (c *Client) GetPersonalAIKeysPolicy() (*PersonalAIKeysPolicy, error) {
	var policy PersonalAIKeysPolicy
	if requestError := c.sendJSON(http.MethodGet, c.BaseURL+"/api/v1/org/ai-settings/personal-keys", nil, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// SetPersonalAIKeysPolicy allows or stops the organisation's work running on
// its members' own AI keys. Members keep their keys either way. It needs the
// ai.manage permission.
func (c *Client) SetPersonalAIKeysPolicy(allowed bool) (*PersonalAIKeysPolicy, error) {
	var policy PersonalAIKeysPolicy
	payload := map[string]bool{"allowed": allowed}
	if requestError := c.sendJSON(http.MethodPut, c.BaseURL+"/api/v1/org/ai-settings/personal-keys", payload, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

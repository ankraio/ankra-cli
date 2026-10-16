package client

import (
	"fmt"
	"net/http"
	"net/url"
)

// RotateTokenResponse is the platform's answer to a token rotation: the new
// token (its value is shown once) and when the replaced token stops working.
// Service tokens also carry AccountName and Label.
type RotateTokenResponse struct {
	ID                 string   `json:"id" yaml:"id"`
	Token              string   `json:"token" yaml:"token"`
	Name               string   `json:"name" yaml:"name"`
	ExpiresAt          string   `json:"expires_at" yaml:"expires_at"`
	Type               string   `json:"type" yaml:"type"`
	Scopes             []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	ReplacedTokenID    string   `json:"replaced_token_id" yaml:"replaced_token_id"`
	OldTokenValidUntil string   `json:"old_token_valid_until" yaml:"old_token_valid_until"`
	AccountName        string   `json:"account_name,omitempty" yaml:"account_name,omitempty"`
	Label              string   `json:"label,omitempty" yaml:"label,omitempty"`
}

type rotateTokenRequest struct {
	OverlapHours int `json:"overlap_hours"`
}

// RotateAPIToken renews one of the caller's personal API tokens. overlapHours
// is how long the old token keeps working; nil leaves the platform default.
func (c *Client) RotateAPIToken(tokenID string, overlapHours *int) (*RotateTokenResponse, error) {
	target := fmt.Sprintf("%s/api/v1/org/account/tokens/%s/rotate", c.BaseURL, url.PathEscape(tokenID))
	return c.rotateToken(target, overlapHours)
}

// RotateServiceToken renews an organisation service token.
func (c *Client) RotateServiceToken(tokenID string, overlapHours *int) (*RotateTokenResponse, error) {
	target := fmt.Sprintf("%s/api/v1/org/organisation/service-tokens/%s/rotate", c.BaseURL, url.PathEscape(tokenID))
	return c.rotateToken(target, overlapHours)
}

func (c *Client) rotateToken(target string, overlapHours *int) (*RotateTokenResponse, error) {
	var payload any
	if overlapHours != nil {
		payload = rotateTokenRequest{OverlapHours: *overlapHours}
	}
	var rotated RotateTokenResponse
	if err := c.sendJSON(http.MethodPost, target, payload, &rotated); err != nil {
		return nil, err
	}
	return &rotated, nil
}

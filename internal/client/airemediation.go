package client

import (
	"fmt"
	"time"
)

// The organisation's AI auto-remediation policy: the envelope the platform
// copies into every dispatched remediation run. Read-only here on purpose.
// The PUT is an organisation-admin, CSRF-protected browser lane, and giving
// an API token a way to widen autonomy is not a convenience worth having.

// AIRemediationPolicy is the wire shape of
// GET /api/v1/org/ai-remediation/policy.
//
// UpdatedAt is the only field that separates a saved policy from the
// platform defaults. An organisation with no policy row is answered with the
// default DOCUMENT (enabled false, autonomy_level "propose") and HTTP 200,
// never a 404, so "nothing is configured" arrives looking exactly like a
// configured policy apart from a null updated_at. Every reader has to key on
// it; rendering the defaults as the organisation's choice would be a lie in
// the direction that matters.
//
// ClusterAllowList keeps null and [] apart deliberately. A []string decodes
// JSON null to nil and [] to an empty non-nil slice, and the platform's
// dispatcher reads the two differently: a null (or absent) allow list admits
// every cluster, while a list admits exactly its members, so an empty list
// admits none.
type AIRemediationPolicy struct {
	Enabled               bool              `json:"enabled" yaml:"enabled"`
	AutonomyLevel         string            `json:"autonomy_level" yaml:"autonomy_level"`
	TierOverrides         map[string]string `json:"tier_overrides" yaml:"tier_overrides"`
	SlackWebhookID        *string           `json:"slack_webhook_id" yaml:"slack_webhook_id"`
	ApproverUserIDs       []string          `json:"approver_user_ids" yaml:"approver_user_ids"`
	MaxActionsPerIncident int               `json:"max_actions_per_incident" yaml:"max_actions_per_incident"`
	CooldownMinutes       int               `json:"cooldown_minutes" yaml:"cooldown_minutes"`
	ClusterAllowList      []string          `json:"cluster_allow_list" yaml:"cluster_allow_list"`
	UpdatedAt             *time.Time        `json:"updated_at" yaml:"updated_at"`
}

// IsConfigured reports whether the organisation has actually saved a policy.
// A policy the platform synthesised from its defaults has never been written,
// so it has no updated_at.
func (policy *AIRemediationPolicy) IsConfigured() bool {
	return policy != nil && policy.UpdatedAt != nil
}

const aiRemediationPolicyPath = "/api/v1/org/ai-remediation/policy"

// GetAIRemediationPolicy reads the organisation's auto-remediation policy.
func (c *Client) GetAIRemediationPolicy() (*AIRemediationPolicy, error) {
	var policy AIRemediationPolicy
	if err := c.getJSON(c.BaseURL+aiRemediationPolicyPath, &policy); err != nil {
		return nil, fmt.Errorf("reading the AI auto-remediation policy: %w", err)
	}
	return &policy, nil
}

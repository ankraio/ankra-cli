package client

// Verified provenance at deploy (ankra-q573dh.13, surfaces ankra-q573dh.44):
// the organisation's default mode, an application's mode, the dry-run that
// verifies images against an application's policy, and the organisation's
// provenance signing keys. All are bearer routes under /api/v1/org.
//
// A mode is "off", "warn" or "enforce". On a write, a nil mode clears the
// choice (the organisation default then comes from Ankra's own default, and an
// application then follows the organisation); the body always carries the
// `mode` key, because the platform refuses a write that does not name it.

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// The three provenance modes the platform accepts.
const (
	ProvenanceModeOff     = "off"
	ProvenanceModeWarn    = "warn"
	ProvenanceModeEnforce = "enforce"
)

// OrganisationProvenancePolicy is the organisation's default. Mode is nil
// when no default was chosen, which is not the same as "off".
type OrganisationProvenancePolicy struct {
	Mode *string `json:"mode" yaml:"mode"`
}

// ApplicationProvenancePolicy is one application's effective mode and where it
// came from: "application" (its own choice), "organisation" (the default) or
// "default" (Ankra's own).
type ApplicationProvenancePolicy struct {
	ApplicationID    string  `json:"application_id" yaml:"application_id"`
	Mode             string  `json:"mode" yaml:"mode"`
	Source           string  `json:"source" yaml:"source"`
	ApplicationMode  *string `json:"application_mode" yaml:"application_mode"`
	OrganisationMode *string `json:"organisation_mode" yaml:"organisation_mode"`
}

// ProvenanceImageVerdict is the verdict for one image of a dry-run. State is
// "verified", "refused" or "unknown"; unknown means the check could not be
// made, never that the image was refused.
type ProvenanceImageVerdict struct {
	Image         string `json:"image" yaml:"image"`
	State         string `json:"state" yaml:"state"`
	Reason        string `json:"reason" yaml:"reason"`
	Message       string `json:"message" yaml:"message"`
	Digest        string `json:"digest,omitempty" yaml:"digest,omitempty"`
	KeyID         string `json:"key_id,omitempty" yaml:"key_id,omitempty"`
	PipelineRunID string `json:"pipeline_run_id,omitempty" yaml:"pipeline_run_id,omitempty"`
	CommitSHA     string `json:"commit_sha,omitempty" yaml:"commit_sha,omitempty"`
}

// ProvenanceVerdict is the overall dry-run verdict. UncheckedImages are the
// images outside the application's own registry project (third-party images),
// which are never judged and never refused.
type ProvenanceVerdict struct {
	State           string                   `json:"state" yaml:"state"`
	Images          []ProvenanceImageVerdict `json:"images" yaml:"images"`
	UncheckedImages []string                 `json:"unchecked_images" yaml:"unchecked_images"`
}

// ProvenanceVerification is the dry-run answer. Decision is what a deploy
// under the application's mode would do: deploy, deploy_with_finding, refuse
// or not_checked.
type ProvenanceVerification struct {
	ApplicationID string            `json:"application_id" yaml:"application_id"`
	Mode          string            `json:"mode" yaml:"mode"`
	Decision      string            `json:"decision" yaml:"decision"`
	Verdict       ProvenanceVerdict `json:"verdict" yaml:"verdict"`
}

// ProvenanceSigningKey is one generation of the organisation's provenance key.
// Status is "active" (signs and verifies), "trusted" (verifies only) or
// "revoked". Only the public half is ever returned.
type ProvenanceSigningKey struct {
	KeyID        string  `json:"key_id" yaml:"key_id"`
	PublicKeyPEM string  `json:"public_key_pem" yaml:"public_key_pem"`
	Generation   int     `json:"generation" yaml:"generation"`
	Status       string  `json:"status" yaml:"status"`
	RevokedAt    *string `json:"revoked_at" yaml:"revoked_at"`
}

// ProvenanceSigningKeys is the key listing and the attestation predicate type.
type ProvenanceSigningKeys struct {
	Keys          []ProvenanceSigningKey `json:"keys" yaml:"keys"`
	PredicateType string                 `json:"predicate_type" yaml:"predicate_type"`
}

type provenanceModeUpdate struct {
	Mode *string `json:"mode"`
}

type provenanceVerifyRequest struct {
	Images []string `json:"images"`
}

type provenanceKeyRevocation struct {
	KeyID  string `json:"key_id"`
	Reason string `json:"reason,omitempty"`
}

// Each route is spelled out in full: the route check
// (cluster_routes_test.go) reads /api/v1 literals, and a shared prefix plus a
// suffix would hide the real path from it.

func provenancePolicyURL(baseURL string) string {
	return baseURL + "/api/v1/org/provenance/policy"
}

func provenanceKeysURL(baseURL string) string {
	return baseURL + "/api/v1/org/provenance/keys"
}

func provenanceKeysRotateURL(baseURL string) string {
	return baseURL + "/api/v1/org/provenance/keys/rotate"
}

func provenanceKeysRevokeURL(baseURL string) string {
	return baseURL + "/api/v1/org/provenance/keys/revoke"
}

func applicationProvenancePolicyURL(baseURL string, applicationID string) string {
	return fmt.Sprintf("%s/api/v1/org/applications/%s/provenance-policy", baseURL, neturl.PathEscape(applicationID))
}

func applicationProvenanceVerifyURL(baseURL string, applicationID string) string {
	return fmt.Sprintf("%s/api/v1/org/applications/%s/provenance/verify", baseURL, neturl.PathEscape(applicationID))
}

// GetOrganisationProvenancePolicy reads the organisation's default mode.
// GET /api/v1/org/provenance/policy
func (c *Client) GetOrganisationProvenancePolicy(ctx context.Context) (*OrganisationProvenancePolicy, error) {
	var policy OrganisationProvenancePolicy
	if requestError := c.sendJSONContext(ctx, http.MethodGet,
		provenancePolicyURL(c.BaseURL), nil, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// SetOrganisationProvenancePolicy sets the organisation's default mode; nil
// clears it. Needs security.manage.
// PUT /api/v1/org/provenance/policy
func (c *Client) SetOrganisationProvenancePolicy(ctx context.Context, mode *string) (*OrganisationProvenancePolicy, error) {
	var policy OrganisationProvenancePolicy
	if requestError := c.sendJSONContext(ctx, http.MethodPut,
		provenancePolicyURL(c.BaseURL), provenanceModeUpdate{Mode: mode}, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// GetApplicationProvenancePolicy reads an application's effective mode.
// GET /api/v1/org/applications/{application_id}/provenance-policy
func (c *Client) GetApplicationProvenancePolicy(ctx context.Context, applicationID string) (*ApplicationProvenancePolicy, error) {
	var policy ApplicationProvenancePolicy
	if requestError := c.sendJSONContext(ctx, http.MethodGet,
		applicationProvenancePolicyURL(c.BaseURL, applicationID), nil, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// SetApplicationProvenancePolicy sets an application's own mode; nil clears it
// so the application follows the organisation again. Needs security.manage.
// PUT /api/v1/org/applications/{application_id}/provenance-policy
func (c *Client) SetApplicationProvenancePolicy(ctx context.Context, applicationID string, mode *string) (*ApplicationProvenancePolicy, error) {
	var policy ApplicationProvenancePolicy
	if requestError := c.sendJSONContext(ctx, http.MethodPut,
		applicationProvenancePolicyURL(c.BaseURL, applicationID),
		provenanceModeUpdate{Mode: mode}, &policy); requestError != nil {
		return nil, requestError
	}
	return &policy, nil
}

// VerifyApplicationProvenance is the dry-run: it verifies images (1 to 32)
// against the application's policy and reports what a deploy would do. It
// deploys nothing and files nothing.
// POST /api/v1/org/applications/{application_id}/provenance/verify
func (c *Client) VerifyApplicationProvenance(ctx context.Context, applicationID string, images []string) (*ProvenanceVerification, error) {
	var verification ProvenanceVerification
	if requestError := c.sendJSONContext(ctx, http.MethodPost,
		applicationProvenanceVerifyURL(c.BaseURL, applicationID),
		provenanceVerifyRequest{Images: images}, &verification); requestError != nil {
		return nil, requestError
	}
	return &verification, nil
}

// ListProvenanceKeys lists the organisation's provenance signing keys.
// GET /api/v1/org/provenance/keys
func (c *Client) ListProvenanceKeys(ctx context.Context) (*ProvenanceSigningKeys, error) {
	var keys ProvenanceSigningKeys
	if requestError := c.sendJSONContext(ctx, http.MethodGet,
		provenanceKeysURL(c.BaseURL), nil, &keys); requestError != nil {
		return nil, requestError
	}
	return &keys, nil
}

// RotateProvenanceKey creates the next key generation; the next publish signs
// with it and earlier generations keep verifying. Needs security.manage.
// POST /api/v1/org/provenance/keys/rotate
func (c *Client) RotateProvenanceKey(ctx context.Context) (*ProvenanceSigningKey, error) {
	var key ProvenanceSigningKey
	if requestError := c.sendJSONContext(ctx, http.MethodPost,
		provenanceKeysRotateURL(c.BaseURL), nil, &key); requestError != nil {
		return nil, requestError
	}
	return &key, nil
}

// RevokeProvenanceKey revokes one key: nothing it signed verifies any more.
// Needs security.manage.
// POST /api/v1/org/provenance/keys/revoke
func (c *Client) RevokeProvenanceKey(ctx context.Context, keyID string, reason string) (*ProvenanceSigningKey, error) {
	var key ProvenanceSigningKey
	if requestError := c.sendJSONContext(ctx, http.MethodPost,
		provenanceKeysRevokeURL(c.BaseURL),
		provenanceKeyRevocation{KeyID: keyID, Reason: reason}, &key); requestError != nil {
		return nil, requestError
	}
	return &key, nil
}

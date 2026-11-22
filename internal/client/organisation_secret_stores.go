package client

// Three organisation registries that hold secret material the platform
// stores and never returns (ankra-hq41xb.15): data source credentials
// (/api/v1/credentials/datasource), security evidence destinations and
// deliveries (/api/v1/org/security/evidence/...), and managed-service
// credential families, values and grants
// (/api/v1/org/service-admission/credentials...).
//
// Every read here returns metadata only. A write carries its secret in the
// request body alone; the request types that hold one are never returned
// to a caller, printed or put into an error by this package.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// ListDatasourceCredentials reads the credentials the metrics and log data
// sources reference by credential_name. Metadata only.
func (c *Client) ListDatasourceCredentials(ctx context.Context) ([]Credential, error) {
	credentials := []Credential{}
	address := c.servicesURL("/api/v1/credentials/datasource", nil)
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &credentials); getError != nil {
		return nil, getError
	}
	return credentials, nil
}

// CreateDatasourceCredentialRequest creates a data source credential: a
// token (AuthScheme bearer or api_key) or a username and password
// (AuthScheme basic). Token and Password are write-only.
type CreateDatasourceCredentialRequest struct {
	Name       string `json:"name"`
	AuthScheme string `json:"auth_scheme,omitempty"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	Token      string `json:"token,omitempty"`
}

// CreateDatasourceCredentialResult is the platform's answer to a create.
// Errors holds the validation errors of a create that did not succeed.
type CreateDatasourceCredentialResult struct {
	Success bool             `json:"success" yaml:"success"`
	ID      string           `json:"id" yaml:"id"`
	Name    string           `json:"name" yaml:"name"`
	Errors  []map[string]any `json:"errors" yaml:"errors"`
}

// CreateDatasourceCredential stores a data source credential. The secret is
// stored in the platform's secret store and never echoed.
func (c *Client) CreateDatasourceCredential(ctx context.Context, request CreateDatasourceCredentialRequest) (*CreateDatasourceCredentialResult, error) {
	var result CreateDatasourceCredentialResult
	address := c.servicesURL("/api/v1/credentials/datasource", nil)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address, request, &result); postError != nil {
		return nil, postError
	}
	return &result, nil
}

// SecurityEvidenceDestinationConfig is the non-secret settings of an
// evidence destination: URL (https) for a Wazuh relay or webhook;
// DocumentID and an optional https APIBaseURL for Vanta.
type SecurityEvidenceDestinationConfig struct {
	URL        string `json:"url,omitempty" yaml:"url,omitempty"`
	DocumentID string `json:"document_id,omitempty" yaml:"document_id,omitempty"`
	APIBaseURL string `json:"api_base_url,omitempty" yaml:"api_base_url,omitempty"`
}

// SecurityEvidenceDestination is a configured evidence receiver. Its
// credential is stored by the platform and never returned;
// CredentialConfigured only says whether one is held.
type SecurityEvidenceDestination struct {
	ID                   string                            `json:"id" yaml:"id"`
	Name                 string                            `json:"name" yaml:"name"`
	Kind                 string                            `json:"kind" yaml:"kind"`
	Config               SecurityEvidenceDestinationConfig `json:"config" yaml:"config"`
	CredentialConfigured bool                              `json:"credential_configured" yaml:"credential_configured"`
	Enabled              bool                              `json:"enabled" yaml:"enabled"`
	OrganisationID       string                            `json:"organisation_id" yaml:"organisation_id"`
	CreatedBy            *string                           `json:"created_by" yaml:"created_by"`
	CreatedAt            string                            `json:"created_at" yaml:"created_at"`
	UpdatedAt            string                            `json:"updated_at" yaml:"updated_at"`
	RevokedAt            *string                           `json:"revoked_at" yaml:"revoked_at"`
}

// CreateSecurityEvidenceDestinationRequest creates a destination.
// Credential is write-only: signing_secret for a webhook, bearer_token for
// Wazuh, client_id and client_secret for Vanta.
type CreateSecurityEvidenceDestinationRequest struct {
	Name       string                            `json:"name"`
	Kind       string                            `json:"kind"`
	Config     SecurityEvidenceDestinationConfig `json:"config"`
	Credential map[string]string                 `json:"credential"`
}

type securityEvidenceDestinationListing struct {
	Result []SecurityEvidenceDestination `json:"result"`
}

// ListSecurityEvidenceDestinations reads the organisation's evidence
// destinations. Credentials are never returned.
func (c *Client) ListSecurityEvidenceDestinations(ctx context.Context) ([]SecurityEvidenceDestination, error) {
	listing := securityEvidenceDestinationListing{Result: []SecurityEvidenceDestination{}}
	address := c.servicesURL("/api/v1/org/security/evidence/destinations", nil)
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &listing); getError != nil {
		return nil, getError
	}
	return listing.Result, nil
}

// CreateSecurityEvidenceDestination stores the credential, then records the
// destination.
func (c *Client) CreateSecurityEvidenceDestination(ctx context.Context, request CreateSecurityEvidenceDestinationRequest) (*SecurityEvidenceDestination, error) {
	var destination SecurityEvidenceDestination
	address := c.servicesURL("/api/v1/org/security/evidence/destinations", nil)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address, request, &destination); postError != nil {
		return nil, postError
	}
	return &destination, nil
}

// RevokeSecurityEvidenceDestination disables a destination, deletes its
// credential and cancels its undelivered deliveries. The row stays for the
// history.
func (c *Client) RevokeSecurityEvidenceDestination(ctx context.Context, destinationID string) (*SecurityEvidenceDestination, error) {
	var destination SecurityEvidenceDestination
	address := c.servicesURL("/api/v1/org/security/evidence/destinations/%s/revoke", nil, destinationID)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address, nil, &destination); postError != nil {
		return nil, postError
	}
	return &destination, nil
}

// SecurityEvidenceDelivery is one evidence snapshot sent to one destination.
// Status delivered is accepted transport, not a decoded or indexed event;
// uploaded is a Vanta draft until it is submitted.
type SecurityEvidenceDelivery struct {
	ID             string         `json:"id" yaml:"id"`
	DestinationID  string         `json:"destination_id" yaml:"destination_id"`
	Status         string         `json:"status" yaml:"status"`
	Framework      *string        `json:"framework" yaml:"framework"`
	Period         string         `json:"period" yaml:"period"`
	Complete       bool           `json:"complete" yaml:"complete"`
	Attempts       int            `json:"attempts" yaml:"attempts"`
	PayloadSHA256  string         `json:"payload_sha256" yaml:"payload_sha256"`
	Receipt        map[string]any `json:"receipt" yaml:"receipt"`
	LastError      *string        `json:"last_error" yaml:"last_error"`
	LastErrorClass *string        `json:"last_error_class" yaml:"last_error_class"`
	NextAttemptAt  *string        `json:"next_attempt_at" yaml:"next_attempt_at"`
	OrganisationID string         `json:"organisation_id" yaml:"organisation_id"`
	RequestedBy    *string        `json:"requested_by" yaml:"requested_by"`
	CreatedAt      string         `json:"created_at" yaml:"created_at"`
	UpdatedAt      string         `json:"updated_at" yaml:"updated_at"`
	DeliveredAt    *string        `json:"delivered_at" yaml:"delivered_at"`
	SubmittedAt    *string        `json:"submitted_at" yaml:"submitted_at"`
}

type securityEvidenceDeliveryListing struct {
	Result []SecurityEvidenceDelivery `json:"result"`
}

// ListSecurityEvidenceDeliveries reads the organisation's deliveries newest
// first, optionally for one destination. limit 0 leaves the platform's
// default.
func (c *Client) ListSecurityEvidenceDeliveries(ctx context.Context, destinationID string, limit int) ([]SecurityEvidenceDelivery, error) {
	query := url.Values{}
	if destinationID != "" {
		query.Set("destination_id", destinationID)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	listing := securityEvidenceDeliveryListing{Result: []SecurityEvidenceDelivery{}}
	address := c.servicesURL("/api/v1/org/security/evidence/deliveries", query)
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &listing); getError != nil {
		return nil, getError
	}
	return listing.Result, nil
}

// SendSecurityEvidenceRequest asks for one evidence snapshot to be captured
// and sent. Framework adds that framework's report; Month (YYYY-MM) sends a
// recorded month instead of current observations.
type SendSecurityEvidenceRequest struct {
	DestinationID string  `json:"destination_id"`
	Framework     *string `json:"framework,omitempty"`
	Month         *string `json:"month,omitempty"`
}

// SendSecurityEvidence captures one bounded snapshot and records it in the
// outbox, due now (202).
func (c *Client) SendSecurityEvidence(ctx context.Context, request SendSecurityEvidenceRequest) (*SecurityEvidenceDelivery, error) {
	var delivery SecurityEvidenceDelivery
	address := c.servicesURL("/api/v1/org/security/evidence/deliveries", nil)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address, request, &delivery); postError != nil {
		return nil, postError
	}
	return &delivery, nil
}

// SubmitSecurityEvidenceDelivery submits the Vanta document an uploaded
// delivery put there. A refused or uncertain submission leaves the delivery
// uploaded with the reason.
func (c *Client) SubmitSecurityEvidenceDelivery(ctx context.Context, deliveryID string) (*SecurityEvidenceDelivery, error) {
	var delivery SecurityEvidenceDelivery
	address := c.servicesURL("/api/v1/org/security/evidence/deliveries/%s/submit", nil, deliveryID)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address, nil, &delivery); postError != nil {
		return nil, postError
	}
	return &delivery, nil
}

// ServiceCredentialFamily is one managed-service credential family: a name
// and the id of its active value, nil while it has none.
type ServiceCredentialFamily struct {
	ID            string  `json:"id" yaml:"id"`
	Name          string  `json:"name" yaml:"name"`
	CreatedAt     string  `json:"created_at" yaml:"created_at"`
	ActiveValueID *string `json:"active_value_id" yaml:"active_value_id"`
}

// ServiceCredentialFamilyPage is one keyset page of families.
type ServiceCredentialFamilyPage struct {
	Items      []ServiceCredentialFamily `json:"items"`
	NextCursor *string                   `json:"next_cursor"`
}

// ServiceCredentialValue is the metadata of one immutable credential value.
// The value itself is never returned.
type ServiceCredentialValue struct {
	ID          string  `json:"id" yaml:"id"`
	State       string  `json:"state" yaml:"state"`
	CreatedAt   string  `json:"created_at" yaml:"created_at"`
	ActivatedAt *string `json:"activated_at" yaml:"activated_at"`
	RetiredAt   *string `json:"retired_at" yaml:"retired_at"`
	Retained    bool    `json:"retained" yaml:"retained"`
}

// ServiceCredentialValuePage is one keyset page of value metadata.
type ServiceCredentialValuePage struct {
	Items      []ServiceCredentialValue `json:"items"`
	NextCursor *string                  `json:"next_cursor"`
}

// ServiceCredentialGrant is one purpose-bound, independently revocable
// reference to a credential value.
type ServiceCredentialGrant struct {
	ID            string  `json:"id" yaml:"id"`
	ValueID       string  `json:"value_id" yaml:"value_id"`
	PackageDigest string  `json:"package_digest" yaml:"package_digest"`
	InputName     string  `json:"input_name" yaml:"input_name"`
	Mode          string  `json:"mode" yaml:"mode"`
	CreatedAt     string  `json:"created_at" yaml:"created_at"`
	RevokedAt     *string `json:"revoked_at" yaml:"revoked_at"`
}

// ServiceCredentialGrantPage is one keyset page of grants.
type ServiceCredentialGrantPage struct {
	Items      []ServiceCredentialGrant `json:"items"`
	NextCursor *string                  `json:"next_cursor"`
}

// ServiceCredentialRotation is the answer to a new value: its metadata, the
// grants the rotation revoked and the replacement grants it issued.
type ServiceCredentialRotation struct {
	Value    ServiceCredentialValue   `json:"value" yaml:"value"`
	Replaced []ServiceCredentialGrant `json:"replaced" yaml:"replaced"`
	Granted  []ServiceCredentialGrant `json:"granted" yaml:"granted"`
}

// ListServiceCredentialFamilies reads one page of credential families.
func (c *Client) ListServiceCredentialFamilies(ctx context.Context, options ServicePageOptions) (*ServiceCredentialFamilyPage, error) {
	var page ServiceCredentialFamilyPage
	address := c.servicesURL("/api/v1/org/service-admission/credentials", options.query())
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &page); getError != nil {
		return nil, getError
	}
	return &page, nil
}

type createServiceCredentialFamilyRequest struct {
	Name string `json:"name"`
}

// CreateServiceCredentialFamily creates an empty credential family.
func (c *Client) CreateServiceCredentialFamily(ctx context.Context, name string) (*ServiceCredentialFamily, error) {
	var family ServiceCredentialFamily
	address := c.servicesURL("/api/v1/org/service-admission/credentials", nil)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address, createServiceCredentialFamilyRequest{Name: name}, &family); postError != nil {
		return nil, postError
	}
	return &family, nil
}

// GetServiceCredentialFamily reads one family's current metadata.
func (c *Client) GetServiceCredentialFamily(ctx context.Context, familyID string) (*ServiceCredentialFamily, error) {
	var family ServiceCredentialFamily
	address := c.servicesURL("/api/v1/org/service-admission/credentials/%s", nil, familyID)
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &family); getError != nil {
		return nil, getError
	}
	return &family, nil
}

// ListServiceCredentialValues reads one page of a family's value metadata.
func (c *Client) ListServiceCredentialValues(ctx context.Context, familyID string, options ServicePageOptions) (*ServiceCredentialValuePage, error) {
	var page ServiceCredentialValuePage
	address := c.servicesURL("/api/v1/org/service-admission/credentials/%s/values", options.query(), familyID)
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &page); getError != nil {
		return nil, getError
	}
	return &page, nil
}

// addServiceCredentialValueRequest always sends expected_value_id: null
// requires the family to have no active value, and the route refuses a body
// without the member.
type addServiceCredentialValueRequest struct {
	Value           string  `json:"value"`
	ExpectedValueID *string `json:"expected_value_id"`
}

// AddServiceCredentialValue stores a new value in a family. expectedValueID
// is the active value the caller read, or nil when the family must have
// none: a stale or repeated request cannot replace a newer value. A
// successful rotation retires the previous value and revokes its grants.
func (c *Client) AddServiceCredentialValue(ctx context.Context, familyID string, value string, expectedValueID *string) (*ServiceCredentialRotation, error) {
	var rotation ServiceCredentialRotation
	address := c.servicesURL("/api/v1/org/service-admission/credentials/%s/values", nil, familyID)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address,
		addServiceCredentialValueRequest{Value: value, ExpectedValueID: expectedValueID}, &rotation); postError != nil {
		return nil, postError
	}
	return &rotation, nil
}

// ListServiceCredentialGrants reads one page of a family's grants.
func (c *Client) ListServiceCredentialGrants(ctx context.Context, familyID string, options ServicePageOptions) (*ServiceCredentialGrantPage, error) {
	var page ServiceCredentialGrantPage
	address := c.servicesURL("/api/v1/org/service-admission/credentials/%s/grants", options.query(), familyID)
	if getError := c.sendJSONContext(ctx, http.MethodGet, address, nil, &page); getError != nil {
		return nil, getError
	}
	return &page, nil
}

// ServiceCredentialActiveValue is the value_id that stands for a family's
// active value when a grant is issued.
const ServiceCredentialActiveValue = "00000000-0000-0000-0000-000000000000"

// IssueServiceCredentialGrantRequest issues a purpose-bound reference to a
// credential value for one input of one package version.
type IssueServiceCredentialGrantRequest struct {
	FamilyID      string `json:"family_id"`
	ValueID       string `json:"value_id"`
	PackageDigest string `json:"package_digest"`
	InputName     string `json:"input_name"`
	Mode          string `json:"mode"`
}

// IssueServiceCredentialGrant issues a grant. An empty ValueID names the
// family's active value.
func (c *Client) IssueServiceCredentialGrant(ctx context.Context, request IssueServiceCredentialGrantRequest) (*ServiceCredentialGrant, error) {
	if request.ValueID == "" {
		request.ValueID = ServiceCredentialActiveValue
	}
	var grant ServiceCredentialGrant
	address := c.servicesURL("/api/v1/org/service-admission/credential-grants", nil)
	if postError := c.sendJSONContext(ctx, http.MethodPost, address, request, &grant); postError != nil {
		return nil, postError
	}
	return &grant, nil
}

// RevokeServiceCredentialGrant revokes one grant (204).
func (c *Client) RevokeServiceCredentialGrant(ctx context.Context, grantID string) error {
	address := c.servicesURL("/api/v1/org/service-admission/credential-grants/%s", nil, grantID)
	return c.sendJSONContext(ctx, http.MethodDelete, address, nil, nil)
}

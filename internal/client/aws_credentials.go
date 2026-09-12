package client

import "net/url"

// awsCredentialProvider is the provider value the platform stores on an AWS
// credential, whether it holds an access key pair or an assumable role.
const awsCredentialProvider = "aws"

// AwsCredentialScopes are the scopes an AWS role credential is onboarded
// under; the CloudFormation template each launches differs in the
// permissions it grants. cost is the platform's default; a cluster create
// refuses cost-scoped roles, so a provisioning credential needs
// provisioning or self_managed.
var AwsCredentialScopes = []string{"cost", "provisioning", "self_managed"}

// AwsOnboardingResponse is GET /api/v1/credentials/aws/onboarding: the
// external id the platform generated for the role's trust policy, the
// CloudFormation launch-stack URL that creates the role with that id, and
// the principal the role has to trust. Configured is false when the
// platform has no template for the scope or no trust principal, in which
// case the URLs are null and the role has to be created by hand.
type AwsOnboardingResponse struct {
	Configured        bool    `json:"configured"`
	Scope             string  `json:"scope"`
	ExternalID        string  `json:"external_id"`
	Region            string  `json:"region"`
	LaunchStackURL    *string `json:"launch_stack_url"`
	TrustPrincipalARN *string `json:"trust_principal_arn"`
	TemplateURL       *string `json:"template_url"`
}

// AwsRoleCredentialCreateRequest is the body of POST /api/v1/credentials/aws/role.
// Region and Scope take the server defaults (us-east-1, cost) when omitted.
type AwsRoleCredentialCreateRequest struct {
	Name       string `json:"name"`
	RoleARN    string `json:"role_arn"`
	ExternalID string `json:"external_id"`
	Region     string `json:"region,omitempty"`
	Scope      string `json:"scope,omitempty"`
}

// AwsKeysCredentialCreateRequest is the body of POST /api/v1/credentials/aws/keys.
// Region takes the server default (us-east-1) when omitted.
type AwsKeysCredentialCreateRequest struct {
	Name            string `json:"name"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Region          string `json:"region,omitempty"`
}

// AwsCredentialCreateResponse is the credential record both writes answer
// with (201 on the bearer surface).
type AwsCredentialCreateResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	OrganisationID string `json:"organisation_id"`
	System         bool   `json:"system"`
	Available      bool   `json:"available"`
}

// ListAwsCredentials lists the organisation's AWS credentials. There is no
// /api/v1/credentials/aws listing: an AWS credential is the platform's
// existing aws credential (keys or role) and is read through the generic
// provider filter on the bearer credentials listing instead.
func (c *Client) ListAwsCredentials() ([]Credential, error) {
	provider := awsCredentialProvider
	return c.ListCredentials(&provider)
}

// GetAwsOnboarding reads the onboarding material for a role credential of
// the given scope; an empty scope lets the server default (cost) apply.
func (c *Client) GetAwsOnboarding(scope string) (*AwsOnboardingResponse, error) {
	endpoint := c.BaseURL + "/api/v1/credentials/aws/onboarding"
	if scope != "" {
		endpoint += "?" + url.Values{"scope": {scope}}.Encode()
	}
	var result AwsOnboardingResponse
	if getError := c.getJSON(endpoint, &result); getError != nil {
		return nil, getError
	}
	return &result, nil
}

// CreateAwsRoleCredential registers an assumable role as an AWS credential.
func (c *Client) CreateAwsRoleCredential(request AwsRoleCredentialCreateRequest) (*AwsCredentialCreateResponse, error) {
	var result AwsCredentialCreateResponse
	if sendError := c.sendJSON("POST", c.BaseURL+"/api/v1/credentials/aws/role", request, &result); sendError != nil {
		return nil, sendError
	}
	return &result, nil
}

// CreateAwsKeysCredential registers an access key pair as an AWS credential.
func (c *Client) CreateAwsKeysCredential(request AwsKeysCredentialCreateRequest) (*AwsCredentialCreateResponse, error) {
	var result AwsCredentialCreateResponse
	if sendError := c.sendJSON("POST", c.BaseURL+"/api/v1/credentials/aws/keys", request, &result); sendError != nil {
		return nil, sendError
	}
	return &result, nil
}

package client

// AzureCredentialListItem is one stored Azure service principal.
type AzureCredentialListItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	OrganisationID string `json:"organisation_id"`
	System         bool   `json:"system"`
	Available      bool   `json:"available"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// CreateAzureCredentialRequest mirrors the API's AzureCredentialCreateRequest:
// the service principal Ankra uses for AKS.
type CreateAzureCredentialRequest struct {
	Name           string `json:"name"`
	SubscriptionID string `json:"subscription_id"`
	TenantID       string `json:"tenant_id"`
	ClientID       string `json:"client_id"`
	ClientSecret   string `json:"client_secret"`
}

type CreateAzureCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors,omitempty"`
}

func (c *Client) ListAzureCredentials() ([]AzureCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/azure"
	var creds []AzureCredentialListItem
	if err := c.getJSON(url, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func (c *Client) CreateAzureCredential(req CreateAzureCredentialRequest) (*CreateAzureCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/azure"
	created, err := c.doCreateCredential(url, req)
	if err != nil {
		return nil, err
	}
	return &CreateAzureCredentialResponse{Success: created.Success, Errors: created.Errors}, nil
}

package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type ProxmoxCredentialListItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	OrganisationID string `json:"organisation_id"`
	System         bool   `json:"system"`
	Available      bool   `json:"available"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// CredentialJumphost is the optional SSH jumphost member shared by the
// Proxmox VE and HPE Morpheus credential create requests. The server
// defaults port to 22 and username to "root" when omitted.
type CredentialJumphost struct {
	Host       string `json:"host"`
	Port       int    `json:"port,omitempty"`
	Username   string `json:"username,omitempty"`
	PrivateKey string `json:"private_key"`
}

type CreateProxmoxCredentialRequest struct {
	Name        string              `json:"name"`
	APIURL      string              `json:"api_url"`
	TokenID     string              `json:"token_id"`
	TokenSecret string              `json:"token_secret"`
	TLSInsecure bool                `json:"tls_insecure"`
	Jumphost    *CredentialJumphost `json:"jumphost,omitempty"`
}

type CreateProxmoxCredentialResponse struct {
	Success bool            `json:"success"`
	Errors  []ResourceError `json:"errors,omitempty"`
}

func (c *Client) ListProxmoxCredentials() ([]ProxmoxCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/proxmox"
	var credentials []ProxmoxCredentialListItem
	if listError := c.getJSON(url, &credentials); listError != nil {
		return nil, listError
	}
	return credentials, nil
}

func (c *Client) CreateProxmoxCredential(createRequest CreateProxmoxCredentialRequest) (*CreateProxmoxCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/proxmox"
	payload, marshalError := json.Marshal(createRequest)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}

	httpRequest, requestError := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	httpResponse, sendError := c.HTTP.Do(httpRequest)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(httpResponse)

	body, readError := readResponseBody(httpResponse)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if httpResponse.StatusCode != http.StatusOK && httpResponse.StatusCode != http.StatusCreated {
		return nil, newUnexpectedResponseError("create failed", httpResponse.StatusCode, redactedBodyForError(body, 500))
	}

	var result CreateProxmoxCredentialResponse
	if decodeError := json.Unmarshal(body, &result); decodeError != nil {
		return nil, fmt.Errorf("parse response: %w", decodeError)
	}
	return &result, nil
}

// ProxmoxTailscale carries the Tailscale/Headscale join settings the platform
// stores on a Proxmox VE credential. Every VM the credential builds - the
// bastion/gateway included - joins the tailnet through the guest agent, which
// is the only way the platform can reach a VM on a node-local SDN.
type ProxmoxTailscale struct {
	LoginServer     string `json:"login_server"`
	AuthKey         string `json:"auth_key"`
	AcceptRoutes    bool   `json:"accept_routes,omitempty"`
	AdvertiseRoutes string `json:"advertise_routes,omitempty"`
}

// updateProxmoxTailscaleRequest keeps the member explicit: a null "tailscale"
// clears the settings, an object sets them.
type updateProxmoxTailscaleRequest struct {
	Tailscale *ProxmoxTailscale `json:"tailscale"`
}

// UpdateProxmoxCredentialTailscale sets the credential's Tailscale settings,
// or clears them when settings is nil. It updates the existing credential in
// place, without recreating it or re-running the infrastructure bootstrap.
func (c *Client) UpdateProxmoxCredentialTailscale(credentialID string, settings *ProxmoxTailscale) error {
	url := c.BaseURL + "/api/v1/credentials/proxmox/" + credentialID + "/tailscale"
	payload, marshalError := json.Marshal(updateProxmoxTailscaleRequest{Tailscale: settings})
	if marshalError != nil {
		return fmt.Errorf("marshal request: %w", marshalError)
	}

	httpRequest, requestError := http.NewRequest(http.MethodPut, url, bytes.NewReader(payload))
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	httpResponse, sendError := c.HTTP.Do(httpRequest)
	if sendError != nil {
		return fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(httpResponse)

	body, readError := readResponseBody(httpResponse)
	if readError != nil {
		return fmt.Errorf("read response: %w", readError)
	}
	if httpResponse.StatusCode != http.StatusOK {
		return newUnexpectedResponseError("update failed", httpResponse.StatusCode, redactedBodyForError(body, 500))
	}
	return nil
}

func (c *Client) ListProxmoxSSHKeyCredentials() ([]ProxmoxCredentialListItem, error) {
	url := c.BaseURL + "/api/v1/credentials/proxmox/ssh-keys"
	var credentials []ProxmoxCredentialListItem
	if listError := c.getJSON(url, &credentials); listError != nil {
		return nil, listError
	}
	return credentials, nil
}

func (c *Client) CreateProxmoxSSHKeyCredential(createRequest CreateSSHKeyCredentialRequest) (*CreateSSHKeyCredentialResponse, error) {
	url := c.BaseURL + "/api/v1/credentials/proxmox/ssh-key"
	return c.doCreateSSHKeyCredential(url, createRequest)
}

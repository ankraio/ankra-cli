package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// sshKeyCredentialRoutePrefix is the provider segment the org-wide SSH-key
// credential routes are reached through.
//
// The platform mounts POST /api/v1/credentials/<provider>/ssh-key and GET
// .../ssh-keys under every provider (hetzner, ovh, upcloud, digitalocean,
// azure, scaleway, proxmox, morpheus, ankracloud), but every mount serves
// the same handler: the create ignores the segment and stores one
// organisation-wide ssh-key credential, and the list filters on provider
// "ssh-key" whatever the segment says (cluster
// go/internal/providerapi/credentials.go). hetzner is used because it is
// the one segment no dark-rollout flag gates, so the route answers in every
// organisation.
const sshKeyCredentialRoutePrefix = "/api/v1/credentials/hetzner"

// SSHKeyCredentialListItem is one org-wide SSH-key credential as the
// platform lists it.
//
// Fingerprint, KeyType, Comment and SSHPublicKey are not sent by today's
// platform: the list item is the generic credential row, which names no key
// material (ankra-xjadx1 asks for them). They are read so the CLI shows
// them the day the platform does, and they stay nil until then rather than
// being filled in client-side.
type SSHKeyCredentialListItem struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Provider       string  `json:"provider"`
	OrganisationID string  `json:"organisation_id"`
	System         bool    `json:"system"`
	Available      bool    `json:"available"`
	State          *string `json:"state,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      *string `json:"updated_at,omitempty"`
	Fingerprint    *string `json:"fingerprint,omitempty"`
	KeyType        *string `json:"key_type,omitempty"`
	Comment        *string `json:"comment,omitempty"`
	SSHPublicKey   *string `json:"ssh_public_key,omitempty"`
}

// CreatedSSHKeyCredential is the platform's answer to a successful SSH-key
// credential create. PrivateKey is set only when the platform generated the
// keypair (generate=true); it is shown once and never stored by Ankra.
type CreatedSSHKeyCredential struct {
	Success    bool    `json:"success"`
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	PrivateKey *string `json:"private_key,omitempty"`
}

// SSHKeyCredentialRefusedError is the platform refusing an SSH-key
// credential create on its merits: a duplicate public key, a taken name, a
// request that named neither a key nor generate. Messages are the
// platform's own sentences, prefixed with the field they concern when the
// platform named one.
type SSHKeyCredentialRefusedError struct {
	StatusCode int
	Messages   []string
}

func (refusal *SSHKeyCredentialRefusedError) Error() string {
	if refusal == nil {
		return ""
	}
	if len(refusal.Messages) == 1 {
		return "the platform refused the SSH key credential: " + refusal.Messages[0]
	}
	return "the platform refused the SSH key credential:\n  - " + strings.Join(refusal.Messages, "\n  - ")
}

// ListOrgSSHKeyCredentials lists the organisation's SSH-key credentials.
func (c *Client) ListOrgSSHKeyCredentials() ([]SSHKeyCredentialListItem, error) {
	var credentials []SSHKeyCredentialListItem
	if err := c.getJSON(c.BaseURL+sshKeyCredentialRoutePrefix+"/ssh-keys", &credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

// CreateOrgSSHKeyCredential creates an organisation-wide SSH-key credential
// from a public key, or asks the platform to generate a keypair.
//
// A refusal the platform explains (HTTP 400) comes back as
// *SSHKeyCredentialRefusedError carrying its sentences, so the caller can
// print "An SSH key with the same public key already exists ('maas')"
// instead of a raw JSON body.
func (c *Client) CreateOrgSSHKeyCredential(request CreateSSHKeyCredentialRequest) (*CreatedSSHKeyCredential, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpRequest, err := http.NewRequest(http.MethodPost, c.BaseURL+sshKeyCredentialRoutePrefix+"/ssh-key", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	response, err := c.HTTP.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(response)

	body, err := readResponseBody(response)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return nil, denied
		}
		if restricted := BillingRestrictedFromResponse(body); restricted != nil {
			return nil, restricted
		}
		if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnprocessableEntity {
			if messages := sshKeyRefusalMessages(body); len(messages) > 0 {
				return nil, &SSHKeyCredentialRefusedError{StatusCode: response.StatusCode, Messages: messages}
			}
		}
		if detail := detailFromBody(body); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("create SSH key credential failed", response.StatusCode, redactedBodyForError(body, 500))
	}

	var created CreatedSSHKeyCredential
	if err := json.Unmarshal(body, &created); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if !created.Success {
		return nil, newUnexpectedResponseErrorWithMessage(response.StatusCode,
			"the platform did not report the SSH key credential as created")
	}
	return &created, nil
}

// sshKeyRefusalMessages reads the three refusal shapes the create route
// answers with:
//
//   - {"detail": "Either provide ssh_public_key or set generate=true"}
//   - {"detail": {"errors": [{"name": ..., "kind": ..., "errors":
//     [{"key": "ssh_public_key", "message": "An SSH key with the same
//     public key already exists ('maas')"}]}]}}
//   - a request-validation list: {"detail": [{"loc": [...], "msg": ...}]}
//
// It returns nil for anything else, so the caller falls back to the generic
// error.
func sshKeyRefusalMessages(body []byte) []string {
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Detail) == 0 {
		return nil
	}

	var detailText string
	if json.Unmarshal(envelope.Detail, &detailText) == nil {
		if strings.TrimSpace(detailText) == "" {
			return nil
		}
		return []string{detailText}
	}

	var resourceErrors struct {
		Errors []struct {
			Name   string `json:"name"`
			Errors []struct {
				Key     string `json:"key"`
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"errors"`
	}
	if json.Unmarshal(envelope.Detail, &resourceErrors) == nil && len(resourceErrors.Errors) > 0 {
		var messages []string
		for _, resourceError := range resourceErrors.Errors {
			for _, item := range resourceError.Errors {
				if item.Message == "" {
					continue
				}
				if item.Key != "" {
					messages = append(messages, item.Key+": "+item.Message)
				} else {
					messages = append(messages, item.Message)
				}
			}
		}
		return messages
	}

	var validationItems []struct {
		Location []any  `json:"loc"`
		Message  string `json:"msg"`
	}
	if json.Unmarshal(envelope.Detail, &validationItems) == nil {
		var messages []string
		for _, item := range validationItems {
			if item.Message == "" {
				continue
			}
			field := ""
			if len(item.Location) > 0 {
				field = fmt.Sprint(item.Location[len(item.Location)-1])
			}
			if field != "" {
				messages = append(messages, field+": "+item.Message)
			} else {
				messages = append(messages, item.Message)
			}
		}
		return messages
	}
	return nil
}

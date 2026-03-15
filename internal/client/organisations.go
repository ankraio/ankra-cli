package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

type OrganisationSummary struct {
	OrganisationID string  `json:"organisation_id"`
	Name           *string `json:"name"`
	UserCurrent    bool    `json:"user_current"`
	Status         *string `json:"status"`
	Role           *string `json:"role"`
}

type OrganisationMember struct {
	UserID    string  `json:"user_id"`
	Email     string  `json:"email"`
	Name      *string `json:"name"`
	Role      string  `json:"role"`
	JoinedAt  string  `json:"joined_at"`
	AvatarURL *string `json:"avatar_url"`
}

type OrganisationFull struct {
	OrganisationID string               `json:"organisation_id"`
	Name           *string              `json:"name"`
	Status         *string              `json:"status"`
	Members        []OrganisationMember `json:"members"`
	CreatedAt      string               `json:"created_at"`
}

type SwitchOrganisationRequest struct {
	OrganisationID string `json:"organisation_id"`
}

type SwitchOrganisationResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type CreateOrganisationRequest struct {
	Name    string  `json:"name"`
	Country *string `json:"country,omitempty"`
}

type CreateOrganisationResponse struct {
	OrganisationID string `json:"organisation_id"`
	Message        string `json:"message"`
}

func (c *Client) ListOrganisations() ([]OrganisationSummary, error) {
	url := c.BaseURL + "/api/v1/org/organisation"
	var orgs []OrganisationSummary
	if err := c.getJSON(url, &orgs); err != nil {
		return nil, err
	}
	return orgs, nil
}

func (c *Client) GetOrganisation(orgID string) (*OrganisationFull, error) {
	url := fmt.Sprintf("%s/api/v1/org/organisation/%s", c.BaseURL, orgID)
	var org OrganisationFull
	if err := c.getJSON(url, &org); err != nil {
		return nil, err
	}
	return &org, nil
}

func (c *Client) SwitchOrganisation(orgID string) (*SwitchOrganisationResponse, error) {
	url := c.BaseURL + "/api/v1/org/organisation/switch"
	reqBody := SwitchOrganisationRequest{OrganisationID: orgID}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("switch failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var switchResp SwitchOrganisationResponse
	if err := json.Unmarshal(body, &switchResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &switchResp, nil
}

type InviteUserRequest struct {
	OrganisationID string `json:"organisation_id"`
	InviteeEmail   string `json:"invitee_email"`
	Role           string `json:"role"`
}

type InviteUserResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type RemoveUserRequest struct {
	UserID         string `json:"user_id"`
	OrganisationID string `json:"organisation_id"`
}

type RemoveUserResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (c *Client) InviteUserToOrganisation(inviteReq InviteUserRequest) (*InviteUserResponse, error) {
	url := c.BaseURL + "/api/v1/org/organisation/invite"
	payload, err := json.Marshal(inviteReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invite failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var inviteResp InviteUserResponse
	if err := json.Unmarshal(body, &inviteResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &inviteResp, nil
}

func (c *Client) RemoveUserFromOrganisation(removeReq RemoveUserRequest) (*RemoveUserResponse, error) {
	url := c.BaseURL + "/api/v1/org/organisation/user"
	payload, err := json.Marshal(removeReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodDelete, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remove failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var removeResp RemoveUserResponse
	if err := json.Unmarshal(body, &removeResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &removeResp, nil
}

func (c *Client) CreateOrganisation(name string, country *string) (*CreateOrganisationResponse, error) {
	url := c.BaseURL + "/api/v1/org/organisation"
	reqBody := CreateOrganisationRequest{Name: name, Country: country}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var createResp CreateOrganisationResponse
	if err := json.Unmarshal(body, &createResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &createResp, nil
}

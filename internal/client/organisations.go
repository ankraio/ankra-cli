package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// OrganisationSummary represents a user's organisation
type OrganisationSummary struct {
	OrganisationID string  `json:"organisation_id"`
	Name           *string `json:"name"`
	UserCurrent    bool    `json:"user_current"`
	Status         *string `json:"status"`
	Role           *string `json:"role"`
}

// OrganisationMember represents a member of an organisation
type OrganisationMember struct {
	UserID    string  `json:"user_id"`
	Email     string  `json:"email"`
	Name      *string `json:"name"`
	Role      string  `json:"role"`
	JoinedAt  string  `json:"joined_at"`
	AvatarURL *string `json:"avatar_url"`
}

// OrganisationFull represents full organisation details including members
type OrganisationFull struct {
	OrganisationID string               `json:"organisation_id"`
	Name           *string              `json:"name"`
	Status         *string              `json:"status"`
	Members        []OrganisationMember `json:"members"`
	CreatedAt      string               `json:"created_at"`
}

// SwitchOrganisationRequest is the request to switch organisation
type SwitchOrganisationRequest struct {
	OrganisationID string `json:"organisation_id"`
}

// SwitchOrganisationResponse is the response from switching organisation
type SwitchOrganisationResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// CreateOrganisationRequest is the request to create an organisation
type CreateOrganisationRequest struct {
	Name    string  `json:"name"`
	Country *string `json:"country,omitempty"`
}

// CreateOrganisationResponse is the response from creating an organisation
type CreateOrganisationResponse struct {
	OrganisationID string `json:"organisation_id"`
	Message        string `json:"message"`
}

// ListOrganisations returns all organisations the user belongs to
func ListOrganisations(token, baseURL string) ([]OrganisationSummary, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/org/organisation"
	var orgs []OrganisationSummary
	if err := getJSON(url, token, &orgs); err != nil {
		return nil, err
	}
	return orgs, nil
}

// GetOrganisation returns full details of an organisation including members
func GetOrganisation(token, baseURL, orgID string) (*OrganisationFull, error) {
	url := fmt.Sprintf("%s/api/v1/org/organisation/%s", strings.TrimRight(baseURL, "/"), orgID)
	var org OrganisationFull
	if err := getJSON(url, token, &org); err != nil {
		return nil, err
	}
	return &org, nil
}

// SwitchOrganisation switches the user's current organisation
func SwitchOrganisation(token, baseURL, orgID string) (*SwitchOrganisationResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/org/organisation/switch"
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
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
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

// CreateOrganisation creates a new organisation
func CreateOrganisation(token, baseURL, name string, country *string) (*CreateOrganisationResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/org/organisation"
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
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
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

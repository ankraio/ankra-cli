package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

// OrganisationVariable mirrors the backend response shape for org-scoped
// variables. The id/timestamps are included for round-trip listing; the CLI
// surfaces only name/value/description by default but keeps them for -o json.
// The backend never returns the value of a secret variable here (it is null,
// so Value is empty); RevealOrganisationVariable reads it back.
type OrganisationVariable struct {
	ID             string    `json:"id"`
	OrganisationID string    `json:"organisation_id"`
	Name           string    `json:"name"`
	Value          string    `json:"value"`
	Description    string    `json:"description,omitempty"`
	IsSecret       bool      `json:"is_secret"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type OrganisationVariableResult struct {
	Variable OrganisationVariable `json:"variable"`
}

type OrganisationVariablesListResult struct {
	Variables []OrganisationVariable `json:"variables"`
}

// ClusterVariable mirrors the backend response shape for cluster-scoped
// variables.
type ClusterVariable struct {
	ID             string    `json:"id"`
	ClusterID      string    `json:"cluster_id"`
	OrganisationID string    `json:"organisation_id"`
	Name           string    `json:"name"`
	Value          string    `json:"value"`
	Description    string    `json:"description,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ClusterVariableResult struct {
	Variable ClusterVariable `json:"variable"`
}

type ClusterVariablesListResult struct {
	Variables []ClusterVariable `json:"variables"`
}

// createVariableRequest is the wire shape for create POSTs (org and cluster
// variants share it). The backend distinguishes them by URL.
type createVariableRequest struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

// updateVariableRequest is the wire shape for update PUTs (no name; the URL
// carries it).
type updateVariableRequest struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

// createOrganisationVariableRequest is the wire shape for organisation
// variable create POSTs. is_secret is sent only when true; the backend
// treats an omitted flag as false.
type createOrganisationVariableRequest struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	IsSecret    bool   `json:"is_secret,omitempty"`
}

// updateOrganisationVariableRequest is the wire shape for organisation
// variable update PUTs. A nil Value or IsSecret is omitted, so the backend
// keeps the stored value or flag; it accepts a body that names only is_secret
// or description.
type updateOrganisationVariableRequest struct {
	Value       *string `json:"value,omitempty"`
	Description string  `json:"description,omitempty"`
	IsSecret    *bool   `json:"is_secret,omitempty"`
}

// OrganisationVariableValue is the body of GET
// /api/v1/org/variables/{name}/value: one variable's stored value, secret or
// not.
type OrganisationVariableValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ErrVariableNotFound is returned when the backend reports 404 on a single
// variable lookup or update. Callers use errors.Is to distinguish it from
// network/auth errors.
var ErrVariableNotFound = errors.New("variable not found")

// ErrVariableDuplicate is returned when the backend reports 409 on create.
// Used by the CLI to implement upsert semantics (create-or-update).
var ErrVariableDuplicate = errors.New("variable already exists")

// --- Organisation variables ---

func (c *Client) ListOrganisationVariables(ctx context.Context) (*OrganisationVariablesListResult, error) {
	url := c.BaseURL + "/api/v1/org/variables"
	body, err := c.doVariableRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var out OrganisationVariablesListResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// CreateOrganisationVariable creates an organisation variable; isSecret marks
// it secret, so listings never return its value.
func (c *Client) CreateOrganisationVariable(ctx context.Context, name, value, description string, isSecret bool) (*OrganisationVariableResult, error) {
	url := c.BaseURL + "/api/v1/org/variables"
	payload, err := json.Marshal(createOrganisationVariableRequest{Name: name, Value: value, Description: description, IsSecret: isSecret})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.doVariableRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, err
	}
	var out OrganisationVariableResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// UpdateOrganisationVariable updates an organisation variable. A nil value
// keeps the stored value, and a nil isSecret keeps the secret flag.
func (c *Client) UpdateOrganisationVariable(ctx context.Context, name string, value *string, description string, isSecret *bool) (*OrganisationVariableResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/variables/%s", c.BaseURL, neturl.PathEscape(name))
	payload, err := json.Marshal(updateOrganisationVariableRequest{Value: value, Description: description, IsSecret: isSecret})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.doVariableRequest(ctx, http.MethodPut, url, payload)
	if err != nil {
		return nil, err
	}
	var out OrganisationVariableResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// RevealOrganisationVariable reads back one organisation variable's stored
// value, secret or not. The backend requires write access to variables and
// records every read in the audit log.
func (c *Client) RevealOrganisationVariable(ctx context.Context, name string) (*OrganisationVariableValue, error) {
	endpoint := fmt.Sprintf("%s/api/v1/org/variables/%s/value", c.BaseURL, neturl.PathEscape(name))
	body, requestError := c.doVariableRequest(ctx, http.MethodGet, endpoint, nil)
	if requestError != nil {
		return nil, requestError
	}
	var revealed OrganisationVariableValue
	if parseError := json.Unmarshal(body, &revealed); parseError != nil {
		return nil, fmt.Errorf("parse response: %w", parseError)
	}
	return &revealed, nil
}

func (c *Client) DeleteOrganisationVariable(ctx context.Context, name string) error {
	url := fmt.Sprintf("%s/api/v1/org/variables/%s", c.BaseURL, neturl.PathEscape(name))
	_, err := c.doVariableRequest(ctx, http.MethodDelete, url, nil)
	return err
}

// --- Cluster variables ---

func (c *Client) ListClusterVariables(ctx context.Context, clusterID string) (*ClusterVariablesListResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/variables", c.BaseURL, neturl.PathEscape(clusterID))
	body, err := c.doVariableRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var out ClusterVariablesListResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) CreateClusterVariable(ctx context.Context, clusterID, name, value, description string) (*ClusterVariableResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/variables", c.BaseURL, neturl.PathEscape(clusterID))
	payload, err := json.Marshal(createVariableRequest{Name: name, Value: value, Description: description})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.doVariableRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, err
	}
	var out ClusterVariableResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) UpdateClusterVariable(ctx context.Context, clusterID, name, value, description string) (*ClusterVariableResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/variables/%s",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(name))
	payload, err := json.Marshal(updateVariableRequest{Value: value, Description: description})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.doVariableRequest(ctx, http.MethodPut, url, payload)
	if err != nil {
		return nil, err
	}
	var out ClusterVariableResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

func (c *Client) DeleteClusterVariable(ctx context.Context, clusterID, name string) error {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/variables/%s",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(name))
	_, err := c.doVariableRequest(ctx, http.MethodDelete, url, nil)
	return err
}

// doVariableRequest is the shared transport helper for all variable CRUD
// calls. It maps the well-known status codes (401/404/409) to typed errors so
// the CLI can offer upsert and friendly not-found messages, and otherwise
// returns the raw body for the caller to JSON-unmarshal.
func (c *Client) doVariableRequest(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, bodyReader)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(resp)

	respBody, err := readResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return respBody, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, ErrVariableNotFound
	case http.StatusConflict:
		return nil, ErrVariableDuplicate
	default:
		return nil, newUnexpectedResponseError("variable request failed", resp.StatusCode,
			truncateForError(redactVariableErrorBody(respBody), 500))
	}
}

// variableValueKeys are the members of a variable error body that can carry
// the value the caller sent: the request's own `value`, and the `input` a 422
// validation error echoes back for the member it rejected.
var variableValueKeys = map[string]bool{"value": true, "input": true}

// redactVariableErrorBody masks every variableValueKeys member of an error
// body, at any depth, before the usual sensitive-key redaction. A variable's
// value is a credential as often as not, and a refused write must not print
// it in the error message.
func redactVariableErrorBody(body []byte) []byte {
	var decoded any
	if unmarshalError := json.Unmarshal(body, &decoded); unmarshalError != nil {
		return redactSensitiveJSON(body)
	}
	masked, marshalError := json.Marshal(maskVariableValues(decoded))
	if marshalError != nil {
		return redactSensitiveJSON(body)
	}
	return redactSensitiveJSON(masked)
}

func maskVariableValues(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		masked := make(map[string]any, len(typed))
		for key, child := range typed {
			if variableValueKeys[strings.ToLower(key)] && child != nil {
				masked[key] = "<redacted>"
				continue
			}
			masked[key] = maskVariableValues(child)
		}
		return masked
	case []any:
		masked := make([]any, len(typed))
		for index, child := range typed {
			masked[index] = maskVariableValues(child)
		}
		return masked
	default:
		return value
	}
}

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// RegistryRobot is one robot account a member of the organisation created
// on demand on the organisation's Ankra registry project
// (/api/v1/org/registry-robots). The secret is never part of it: it is
// answered once, by create and rotate, as RegistryRobotWithSecret.
type RegistryRobot struct {
	Name           string  `json:"name" yaml:"name"`
	RobotName      string  `json:"robot_name" yaml:"robot_name"`
	Host           string  `json:"host" yaml:"host"`
	Project        string  `json:"project" yaml:"project"`
	Scope          string  `json:"scope" yaml:"scope"`
	Description    string  `json:"description" yaml:"description"`
	CredentialName string  `json:"credential_name" yaml:"credential_name"`
	CreatedAt      string  `json:"created_at" yaml:"created_at"`
	RotatedAt      *string `json:"rotated_at" yaml:"rotated_at"`
}

// RegistryRobotWithSecret is a robot together with the secret the registry
// answered once and the docker login command that uses it.
type RegistryRobotWithSecret struct {
	RegistryRobot `yaml:",inline"`
	Secret        string `json:"secret" yaml:"secret"`
	DockerLogin   string `json:"docker_login" yaml:"docker_login"`
}

// RegistryRobotList is the list response.
type RegistryRobotList struct {
	Robots     []RegistryRobot `json:"robots" yaml:"robots"`
	TotalCount int             `json:"total_count" yaml:"total_count"`
}

// CreateRegistryRobotRequest is what a member states when minting a robot.
// Scope is "push" (push and pull) or "pull"; empty means push.
type CreateRegistryRobotRequest struct {
	Name        string `json:"name"`
	Scope       string `json:"scope,omitempty"`
	Description string `json:"description,omitempty"`
}

// The robot scope vocabulary, mirroring the platform's.
const (
	RegistryRobotScopePush = "push"
	RegistryRobotScopePull = "pull"
)

const registryRobotsAPIPath = "/api/v1/org/registry-robots"

// CreateRegistryRobot mints a robot on the organisation's registry project
// and answers its secret, the one time it is answered.
func (c *Client) CreateRegistryRobot(ctx context.Context, request CreateRegistryRobotRequest) (*RegistryRobotWithSecret, error) {
	encoded, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, fmt.Errorf("encode request: %w", marshalError)
	}
	body, requestError := c.doRegistryRobotRequest(ctx, http.MethodPost, registryRobotsAPIPath, encoded,
		http.StatusCreated, http.StatusOK)
	if requestError != nil {
		return nil, requestError
	}
	var created RegistryRobotWithSecret
	if unmarshalError := json.Unmarshal(body, &created); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &created, nil
}

// ListRegistryRobots lists the organisation's on-demand robots.
func (c *Client) ListRegistryRobots(ctx context.Context) (*RegistryRobotList, error) {
	body, requestError := c.doRegistryRobotRequest(ctx, http.MethodGet, registryRobotsAPIPath, nil, http.StatusOK)
	if requestError != nil {
		return nil, requestError
	}
	var list RegistryRobotList
	if unmarshalError := json.Unmarshal(body, &list); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &list, nil
}

// GetRegistryRobot reads one robot's record.
func (c *Client) GetRegistryRobot(ctx context.Context, robotName string) (*RegistryRobot, error) {
	body, requestError := c.doRegistryRobotRequest(ctx, http.MethodGet,
		registryRobotsAPIPath+"/"+url.PathEscape(robotName), nil, http.StatusOK)
	if requestError != nil {
		return nil, requestError
	}
	var robot RegistryRobot
	if unmarshalError := json.Unmarshal(body, &robot); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &robot, nil
}

// RotateRegistryRobotSecret mints a new secret for the robot and answers it
// once; the previous secret stops working.
func (c *Client) RotateRegistryRobotSecret(ctx context.Context, robotName string) (*RegistryRobotWithSecret, error) {
	body, requestError := c.doRegistryRobotRequest(ctx, http.MethodPost,
		registryRobotsAPIPath+"/"+url.PathEscape(robotName)+"/rotate", nil, http.StatusOK)
	if requestError != nil {
		return nil, requestError
	}
	var rotated RegistryRobotWithSecret
	if unmarshalError := json.Unmarshal(body, &rotated); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &rotated, nil
}

// DeleteRegistryRobot deletes the robot on the registry and the credential
// holding its login.
func (c *Client) DeleteRegistryRobot(ctx context.Context, robotName string) error {
	// The platform answers a 200 with {"success": true}; 204 is accepted too
	// so a future no-content answer never reads as a failed revoke.
	_, requestError := c.doRegistryRobotRequest(ctx, http.MethodDelete,
		registryRobotsAPIPath+"/"+url.PathEscape(robotName), nil, http.StatusOK, http.StatusNoContent)
	return requestError
}

func (c *Client) doRegistryRobotRequest(ctx context.Context, method string, path string, body []byte,
	successStatuses ...int) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	request, requestError := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, sendError := c.HTTP.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	for _, successStatus := range successStatuses {
		if response.StatusCode == successStatus {
			return responseBody, nil
		}
	}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusForbidden:
		if denied := PermissionDeniedFromResponse(response.StatusCode, responseBody); denied != nil {
			return nil, denied
		}
		return nil, &PermissionDeniedError{Detail: ciSettingsRefusalDetail(responseBody)}
	case http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable:
		// Every refusal this lane writes names the reason - an unknown robot,
		// a taken name, an organisation with no registry project yet, a
		// platform without the registry - so the sentence rides verbatim.
		if detail := ciSettingsRefusalDetail(responseBody); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
	}
	return nil, newUnexpectedResponseError("registry robot request failed",
		response.StatusCode, redactedBodyForError(responseBody, 500))
}

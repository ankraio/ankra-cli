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

// RegistryRobot is one robot account on a registry project the organisation
// publishes to (/api/v1/org/registry-robots): one a member created on demand,
// or one Ankra minted and manages - the organisation's ci and pull robots and
// each application's push robot. The secret is never part of it: it is
// answered once, by create and rotate, as RegistryRobotWithSecret.
//
// Kind, Managed, Projects, Permissions, Application and ExpiresAt are absent
// from a platform that predates them; KindOrUser reads such a robot as the
// member's own, which is all that platform ever listed.
type RegistryRobot struct {
	Name string `json:"name" yaml:"name"`
	// Kind is user, organisation or application.
	Kind string `json:"kind" yaml:"kind"`
	// Managed reports a robot Ankra minted and whose secret it fans out
	// itself: it is listed and read, never rotated or revoked here.
	Managed   bool   `json:"managed" yaml:"managed"`
	RobotName string `json:"robot_name" yaml:"robot_name"`
	Host      string `json:"host" yaml:"host"`
	Project   string `json:"project" yaml:"project"`
	// ProjectName is what the project the robot is bound to is addressed
	// by: default, the name of an extra project, or empty for a project on a
	// registry the organisation runs itself. Absent from a platform that
	// predates registry projects.
	ProjectName string `json:"project_name" yaml:"project_name"`
	// Projects is every registry project the login reaches.
	Projects []string `json:"projects" yaml:"projects"`
	// Scope is push, pull, or custom when the permissions are neither preset.
	Scope string `json:"scope" yaml:"scope"`
	// Permissions is every grant the robot holds, written resource:action.
	Permissions    []string `json:"permissions" yaml:"permissions"`
	Description    string   `json:"description" yaml:"description"`
	CredentialName string   `json:"credential_name" yaml:"credential_name"`
	// Application is the application an application robot belongs to.
	Application *RegistryRobotApplication `json:"application" yaml:"application"`
	CreatedAt   string                    `json:"created_at" yaml:"created_at"`
	RotatedAt   *string                   `json:"rotated_at" yaml:"rotated_at"`
	// ExpiresAt is when the registry stops honouring the robot; nil for one
	// that never expires.
	ExpiresAt *string `json:"expires_at" yaml:"expires_at"`
	// Registry is the integrated registry entry - a Harbor the organisation
	// runs itself - the robot lives on; empty on the Ankra registry.
	Registry string `json:"registry,omitempty" yaml:"registry,omitempty"`
	// AdminCredentialName is the organisation's credential the robot is
	// managed with on an integrated registry; empty on the Ankra registry.
	AdminCredentialName string `json:"admin_credential_name,omitempty" yaml:"admin_credential_name,omitempty"`
}

// KindOrUser is the robot's kind, read as a member's own when the platform
// did not state one.
func (robot RegistryRobot) KindOrUser() string {
	if robot.Kind == "" {
		return RegistryRobotKindUser
	}
	return robot.Kind
}

// RegistryRobotApplication names the application a robot was minted for.
type RegistryRobotApplication struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
}

// RegistryRobotWithSecret is a robot together with the secret the registry
// answered once and the docker login command that uses it. Warning is set
// when the platform could not store the secret afterwards, which makes this
// answer the only copy.
type RegistryRobotWithSecret struct {
	RegistryRobot `yaml:",inline"`
	Secret        string `json:"secret" yaml:"secret"`
	DockerLogin   string `json:"docker_login" yaml:"docker_login"`
	Warning       string `json:"warning,omitempty" yaml:"warning,omitempty"`
}

// RegistryRobotRegistry is the registry project the organisation's robots
// are minted on.
type RegistryRobotRegistry struct {
	Host    string `json:"host" yaml:"host"`
	Project string `json:"project" yaml:"project"`
}

// RegistryRobotPermission is one grant a robot may be created with.
type RegistryRobotPermission struct {
	Permission  string `json:"permission" yaml:"permission"`
	Label       string `json:"label" yaml:"label"`
	Description string `json:"description" yaml:"description"`
}

// RegistryRobotList is the list response: the robots, the registry project
// they are minted on (nil while it is not provisioned), and the grants a
// robot may be created with.
type RegistryRobotList struct {
	Robots               []RegistryRobot           `json:"robots" yaml:"robots"`
	TotalCount           int                       `json:"total_count" yaml:"total_count"`
	Registry             *RegistryRobotRegistry    `json:"registry" yaml:"registry"`
	AvailablePermissions []RegistryRobotPermission `json:"available_permissions" yaml:"available_permissions"`
}

// CreateRegistryRobotRequest is what a member states when minting a robot:
// a preset Scope ("push", which pushes and pulls, or "pull"; empty means
// push) or the robot's own Permissions, never both, and ExpiresInDays, where
// zero is a robot that never expires.
type CreateRegistryRobotRequest struct {
	Name          string   `json:"name"`
	Scope         string   `json:"scope,omitempty"`
	Permissions   []string `json:"permissions,omitempty"`
	Description   string   `json:"description,omitempty"`
	ExpiresInDays int      `json:"expires_in_days,omitempty"`
	// Project names the registry project the robot is bound to; empty is the
	// organisation's own. With Registry it is that registry's project and is
	// required: the platform binds the robot to exactly the project named.
	Project string `json:"project,omitempty"`
	// Registry names an integrated OCI registry entry to mint the robot on
	// instead of the Ankra registry, with AdminCredentialName - the
	// organisation's credential that may manage robots there.
	Registry            string `json:"registry,omitempty"`
	AdminCredentialName string `json:"admin_credential_name,omitempty"`
}

// The robot scope vocabulary, mirroring the platform's.
const (
	RegistryRobotScopePush   = "push"
	RegistryRobotScopePull   = "pull"
	RegistryRobotScopeCustom = "custom"
)

// The robot kind vocabulary, mirroring the platform's.
const (
	RegistryRobotKindUser         = "user"
	RegistryRobotKindOrganisation = "organisation"
	RegistryRobotKindApplication  = "application"
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

// ListRegistryRobots lists every robot login the organisation holds: its
// members' own and the ones Ankra manages.
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
	return c.doRegistryRequest(ctx, "registry robot request failed", method, path, body, successStatuses...)
}

// doRegistryRequest sends one bearer request on the organisation registry
// lanes (robots, projects, storage) and answers the body of a success
// status. operation names the lane in an error that carries no detail.
func (c *Client) doRegistryRequest(ctx context.Context, operation string, method string, path string, body []byte,
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
	case http.StatusBadRequest, http.StatusPaymentRequired, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity,
		http.StatusServiceUnavailable:
		// Every refusal this lane writes names the reason - an unknown robot,
		// a taken name, a robot Ankra manages, an expired robot, an
		// organisation with no registry project yet, a platform without the
		// registry - so the sentence rides verbatim.
		if detail := ciSettingsRefusalDetail(responseBody); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
	}
	return nil, newUnexpectedResponseError(operation,
		response.StatusCode, redactedBodyForError(responseBody, 500))
}

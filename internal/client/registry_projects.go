package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// RegistryProject is one project the organisation holds on the Ankra
// registry (/api/v1/org/registry-projects): its own, named "default", or an
// extra one it created. A robot account is bound to one project, so a
// project is the unit a login's reach is scoped by.
type RegistryProject struct {
	// Name is what the project is addressed by: default, or the name it was
	// created under.
	Name string `json:"name" yaml:"name"`
	// Project is its name on the registry: the path segment images are
	// pushed under.
	Project         string `json:"project" yaml:"project"`
	Host            string `json:"host" yaml:"host"`
	IsDefault       bool   `json:"is_default" yaml:"is_default"`
	RepositoryCount int64  `json:"repository_count" yaml:"repository_count"`
	// RobotCount is how many of the organisation's own robot accounts are
	// bound to the project.
	RobotCount int    `json:"robot_count" yaml:"robot_count"`
	CreatedAt  string `json:"created_at" yaml:"created_at"`
}

// RegistryProjectList is the list response: the organisation's own project
// first, then its extra projects, and how many extra projects it may hold.
type RegistryProjectList struct {
	Projects          []RegistryProject `json:"projects" yaml:"projects"`
	TotalCount        int               `json:"total_count" yaml:"total_count"`
	ExtraProjectLimit int               `json:"extra_project_limit" yaml:"extra_project_limit"`
}

// RegistryDefaultProjectName is the name the organisation's own project
// answers to.
const RegistryDefaultProjectName = "default"

const registryProjectsAPIPath = "/api/v1/org/registry-projects"

// ListRegistryProjects lists the organisation's registry projects.
func (c *Client) ListRegistryProjects(ctx context.Context) (*RegistryProjectList, error) {
	body, requestError := c.doRegistryRobotRequest(ctx, http.MethodGet, registryProjectsAPIPath, nil, http.StatusOK)
	if requestError != nil {
		return nil, requestError
	}
	var list RegistryProjectList
	if unmarshalError := json.Unmarshal(body, &list); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &list, nil
}

// CreateRegistryProject creates an extra registry project.
func (c *Client) CreateRegistryProject(ctx context.Context, projectName string) (*RegistryProject, error) {
	encoded, marshalError := json.Marshal(map[string]string{"name": projectName})
	if marshalError != nil {
		return nil, fmt.Errorf("encode request: %w", marshalError)
	}
	body, requestError := c.doRegistryRobotRequest(ctx, http.MethodPost, registryProjectsAPIPath, encoded,
		http.StatusCreated, http.StatusOK)
	if requestError != nil {
		return nil, requestError
	}
	var created RegistryProject
	if unmarshalError := json.Unmarshal(body, &created); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &created, nil
}

// DeleteRegistryProject deletes an extra registry project. The platform
// refuses one that still holds repositories or has robot accounts bound to
// it, and never deletes either on the way.
func (c *Client) DeleteRegistryProject(ctx context.Context, projectName string) error {
	_, requestError := c.doRegistryRobotRequest(ctx, http.MethodDelete,
		registryProjectsAPIPath+"/"+url.PathEscape(projectName), nil, http.StatusOK, http.StatusNoContent)
	return requestError
}

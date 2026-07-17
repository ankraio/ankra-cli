package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type CreateApplicationRequest struct {
	Name                     string `json:"name"`
	RepositoryCredentialName string `json:"app_repo_credential_name"`
	RepositoryOwner          string `json:"app_repo_owner"`
	RepositoryName           string `json:"app_repo_name"`
	RepositoryBranch         string `json:"app_repo_branch"`
}

type ApplicationErrorItem struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

type ApplicationResourceError struct {
	Name   string                 `json:"name"`
	Kind   string                 `json:"kind"`
	Errors []ApplicationErrorItem `json:"errors"`
}

type CreateApplicationResponse struct {
	ID     *string                    `json:"id"`
	Errors []ApplicationResourceError `json:"errors"`
}

func (client *Client) CreateApplication(requestContext context.Context, applicationRequest CreateApplicationRequest) (*CreateApplicationResponse, error) {
	requestBody, marshalError := json.Marshal(applicationRequest)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}

	request, requestError := http.NewRequestWithContext(
		requestContext,
		http.MethodPost,
		client.BaseURL+"/api/v1/org/applications",
		bytes.NewReader(requestBody),
	)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
	request.Header.Set("Content-Type", "application/json")

	response, sendError := client.HTTP.Do(request)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode != http.StatusOK {
		if permissionDenied := PermissionDeniedFromResponse(response.StatusCode, responseBody); permissionDenied != nil {
			return nil, permissionDenied
		}
		return nil, newUnexpectedResponseError(
			"create application failed",
			response.StatusCode,
			redactedBodyForError(responseBody, 500),
		)
	}

	var applicationResponse CreateApplicationResponse
	if unmarshalError := json.Unmarshal(responseBody, &applicationResponse); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &applicationResponse, nil
}

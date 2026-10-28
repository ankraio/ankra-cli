package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ImportClaudeDesignFile is one export file on the wire, base64.
type ImportClaudeDesignFile struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
}

// ImportClaudeDesignRequest mirrors the platform import body: the create
// route's repository fields plus the export files. The repository name,
// visibility and source URL are omitted when empty so the platform applies
// its defaults.
type ImportClaudeDesignRequest struct {
	Name                     string                   `json:"name"`
	RepositoryCredentialName string                   `json:"app_repo_credential_name"`
	RepositoryOwner          string                   `json:"app_repo_owner"`
	RepositoryName           string                   `json:"app_repo_name,omitempty"`
	Visibility               string                   `json:"visibility,omitempty"`
	SourceURL                string                   `json:"source_url,omitempty"`
	Files                    []ImportClaudeDesignFile `json:"files"`
}

// ImportedRepository is the repository the import landed in.
type ImportedRepository struct {
	Owner         string  `json:"owner"`
	Name          string  `json:"name"`
	WebURL        string  `json:"web_url"`
	DefaultBranch string  `json:"default_branch"`
	CommitSHA     *string `json:"commit_sha"`
	Reused        bool    `json:"reused"`
}

// ImportedDesignPage is one converted artboard.
type ImportedDesignPage struct {
	Artboard string `json:"artboard"`
	Path     string `json:"path"`
	Title    string `json:"title"`
	Dynamic  bool   `json:"dynamic"`
	PageID   string `json:"page_id,omitempty"`
}

// ImportedDesignWarning is a non-fatal finding about one artboard.
type ImportedDesignWarning struct {
	Artboard string `json:"artboard"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
}

// ImportClaudeDesignResponse carries the registration outcome in the create
// route's shape plus what the conversion produced.
type ImportClaudeDesignResponse struct {
	ID         *string                    `json:"id"`
	Errors     []ApplicationResourceError `json:"errors"`
	Repository *ImportedRepository        `json:"repository"`
	Pages      []ImportedDesignPage       `json:"pages"`
	Warnings   []ImportedDesignWarning    `json:"warnings"`
	Notes      []string                   `json:"notes"`
}

// ImportClaudeDesignApplication turns a Claude Design export into a
// registered application on a repository the platform creates.
func (client *Client) ImportClaudeDesignApplication(requestContext context.Context, importRequest ImportClaudeDesignRequest) (*ImportClaudeDesignResponse, error) {
	requestBody, marshalError := json.Marshal(importRequest)
	if marshalError != nil {
		return nil, fmt.Errorf("marshal request: %w", marshalError)
	}

	request, requestError := http.NewRequestWithContext(
		requestContext,
		http.MethodPost,
		client.BaseURL+"/api/v1/org/applications/imports/claude-design",
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
			"import claude design application failed",
			response.StatusCode,
			redactedBodyForError(responseBody, 500),
		)
	}

	var importResponse ImportClaudeDesignResponse
	if unmarshalError := json.Unmarshal(responseBody, &importResponse); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	return &importResponse, nil
}

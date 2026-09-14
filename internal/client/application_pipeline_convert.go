package client

// One-click conversion of an application that still builds from a GitHub
// Actions workflow onto Ankra Pipelines (cluster ankra-484en). The platform
// stores the converted pipeline as the definition of record, flips the
// application's pipeline_source to ankra_pipeline so the next push builds
// through it, switches Ankra's generated workflow off on GitHub, and opens a
// pull request committing the pipeline file (and, by default, removing the
// generated workflow). Nothing about building waits on that pull request
// merging.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// PipelineConversion is what the conversion answered.
type PipelineConversion struct {
	// Outcome is the platform's word for what happened: started (a fresh
	// conversion), already_open (the pull request from an earlier click is
	// still open; the answer is the same conversion converging).
	Outcome        string `json:"outcome" yaml:"outcome"`
	Message        string `json:"message" yaml:"message"`
	PullRequestURL string `json:"pull_request_url,omitempty" yaml:"pull_request_url,omitempty"`
	// PipelineSource is the disposition the application carries when the
	// call returns: ankra_pipeline the moment the conversion is stored.
	PipelineSource string `json:"pipeline_source,omitempty" yaml:"pipeline_source,omitempty"`
	// RemovedPaths are the generated workflow files the pull request removes.
	RemovedPaths []string `json:"removed_paths,omitempty" yaml:"removed_paths,omitempty"`
	// DisabledWorkflows are the generated workflow files switched off on
	// GitHub so they stop building before the pull request merges;
	// DisableWorkflowsMessage says why any could not be.
	DisabledWorkflows       []string         `json:"disabled_workflows,omitempty" yaml:"disabled_workflows,omitempty"`
	DisableWorkflowsMessage string           `json:"disable_workflows_message,omitempty" yaml:"disable_workflows_message,omitempty"`
	SpecYAML                string           `json:"spec_yaml,omitempty" yaml:"spec_yaml,omitempty"`
	Notes                   []map[string]any `json:"notes,omitempty" yaml:"notes,omitempty"`
	Violations              []string         `json:"violations,omitempty" yaml:"violations,omitempty"`
}

// convertApplicationPipelineRequest is the POST body. `{}` is the one-click
// conversion: the platform removes the generated workflow file in the same
// pull request by default and never touches a hand-written one, so the flag
// is only sent when the caller asked to keep the file.
type convertApplicationPipelineRequest struct {
	RemoveWorkflows *bool `json:"remove_workflows,omitempty"`
}

// ConvertApplicationPipeline converts the application onto Ankra Pipelines.
// keepWorkflows leaves Ankra's generated workflow file in the repository
// instead of removing it in the pull request.
func (c *Client) ConvertApplicationPipeline(ctx context.Context, applicationID string,
	keepWorkflows bool) (*PipelineConversion, error) {
	request := convertApplicationPipelineRequest{}
	if keepWorkflows {
		removeWorkflows := false
		request.RemoveWorkflows = &removeWorkflows
	}
	encoded, marshalError := json.Marshal(request)
	if marshalError != nil {
		return nil, fmt.Errorf("encode request: %w", marshalError)
	}
	path := applicationsAPIPath + "/" + url.PathEscape(applicationID) + "/pipeline-migration"
	httpRequest, requestError := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(encoded))
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.Token)

	response, sendError := c.HTTP.Do(httpRequest)
	if sendError != nil {
		return nil, fmt.Errorf("request failed: %w", sendError)
	}
	defer closeBody(response)

	responseBody, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	switch response.StatusCode {
	case http.StatusOK:
		var conversion PipelineConversion
		if unmarshalError := json.Unmarshal(responseBody, &conversion); unmarshalError != nil {
			return nil, fmt.Errorf("parse response: %w", unmarshalError)
		}
		return &conversion, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusForbidden:
		if denied := PermissionDeniedFromResponse(response.StatusCode, responseBody); denied != nil {
			return nil, denied
		}
		return nil, &PermissionDeniedError{Detail: ciSettingsRefusalDetail(responseBody)}
	case http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadGateway:
		// Every refusal names its reason for the user - no pipeline cluster,
		// no GitHub credential, nothing to convert, a conversion that did not
		// validate - as `detail` (409, 404) or as the result's `message`
		// (422), so the sentence rides verbatim.
		if refusal := pipelineConversionRefusal(responseBody); refusal != "" {
			return nil, newBackendDetailError(response.StatusCode, refusal)
		}
	}
	return nil, newUnexpectedResponseError("pipeline conversion failed", response.StatusCode,
		redactedBodyForError(responseBody, 500))
}

func pipelineConversionRefusal(body []byte) string {
	if detail := ciSettingsRefusalDetail(body); detail != "" {
		return detail
	}
	var result struct {
		Message string `json:"message"`
	}
	if unmarshalError := json.Unmarshal(body, &result); unmarshalError != nil {
		return ""
	}
	return result.Message
}

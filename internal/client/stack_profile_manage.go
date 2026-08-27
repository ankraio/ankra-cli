package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
)

const stackProfilesAPIPath = "/api/v1/org/stack-profiles"

// CreateStackProfileFromStackRequest mirrors the create_stack_profile body:
// snapshot an existing cluster stack as version 1 of a new profile.
type CreateStackProfileFromStackRequest struct {
	Name                       string   `json:"name"`
	Description                *string  `json:"description,omitempty"`
	LogoURL                    *string  `json:"logo_url,omitempty"`
	Category                   string   `json:"category,omitempty"`
	Tags                       []string `json:"tags,omitempty"`
	Visibility                 *string  `json:"visibility,omitempty"`
	SourceClusterID            string   `json:"source_cluster_id"`
	StackName                  string   `json:"stack_name"`
	IncludeAddonConfigurations bool     `json:"include_addon_configurations,omitempty"`
	Changelog                  *string  `json:"changelog,omitempty"`
}

// UpdateStackProfileRequest mirrors the metadata PATCH body. Absent fields
// leave the stored values unchanged; Tags is only sent when provided.
type UpdateStackProfileRequest struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	LogoURL     *string  `json:"logo_url,omitempty"`
	Category    *string  `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Visibility  *string  `json:"visibility,omitempty"`
}

// SaveStackProfileVersionRequest mirrors the save-version body: re-snapshot
// a source stack as the profile's next version.
type SaveStackProfileVersionRequest struct {
	SourceClusterID            string  `json:"source_cluster_id"`
	StackName                  string  `json:"stack_name"`
	IncludeAddonConfigurations bool    `json:"include_addon_configurations,omitempty"`
	Channel                    string  `json:"channel,omitempty"`
	Changelog                  *string `json:"changelog,omitempty"`
}

// LaunchStackProfileDemoRequest mirrors the demo launch body; every field is
// optional and an empty body launches the current version with defaults.
type LaunchStackProfileDemoRequest struct {
	Version    *int               `json:"version,omitempty"`
	Parameters []ParameterBinding `json:"parameters,omitempty"`
	TTLHours   *int               `json:"ttl_hours,omitempty"`
}

// StackProfileLogo carries the served logo bytes and their content type.
type StackProfileLogo struct {
	Content     []byte
	ContentType string
}

// stackProfileResourceRequest performs a bearer-authenticated request against
// a stack-profile route and returns the raw JSON body on success (200 or
// 201). The FastAPI `detail` string is surfaced as the error message so the
// CLI can print the backend's human-readable reason, and the HTTP status is
// preserved for exit-code classification.
func (client *Client) stackProfileResourceRequest(
	requestContext context.Context,
	method string,
	path string,
	query url.Values,
	requestBody any,
) (json.RawMessage, error) {
	var bodyReader io.Reader
	if requestBody != nil {
		encoded, marshalError := json.Marshal(requestBody)
		if marshalError != nil {
			return nil, fmt.Errorf("marshal request: %w", marshalError)
		}
		bodyReader = bytes.NewReader(encoded)
	}
	requestURL := client.BaseURL + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	request, requestError := http.NewRequestWithContext(requestContext, method, requestURL, bodyReader)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}

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
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		if permissionDenied := PermissionDeniedFromResponse(response.StatusCode, responseBody); permissionDenied != nil {
			return nil, permissionDenied
		}
		message := detailFromBody(responseBody)
		if message == "" {
			message = "stack profile request failed"
		}
		return nil, newUnexpectedResponseError(
			message,
			response.StatusCode,
			redactedBodyForError(responseBody, 500),
		)
	}
	return json.RawMessage(responseBody), nil
}

func stackProfilePath(profileID string, suffix string) string {
	return stackProfilesAPIPath + "/" + url.PathEscape(profileID) + suffix
}

// --- profile lifecycle ---

func (client *Client) CreateStackProfile(requestContext context.Context, createRequest CreateStackProfileFromStackRequest) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPost, stackProfilesAPIPath, nil, createRequest)
}

func (client *Client) UpdateStackProfile(requestContext context.Context, profileID string, updateRequest UpdateStackProfileRequest) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPatch, stackProfilePath(profileID, ""), nil, updateRequest)
}

func (client *Client) DeleteStackProfile(requestContext context.Context, profileID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodDelete, stackProfilePath(profileID, ""), nil, nil)
}

// --- versions ---

func (client *Client) GetStackProfileVersion(requestContext context.Context, profileID string, version int) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodGet,
		stackProfilePath(profileID, "/versions/"+strconv.Itoa(version)), nil, nil)
}

func (client *Client) SaveStackProfileVersion(requestContext context.Context, profileID string, saveRequest SaveStackProfileVersionRequest) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPost,
		stackProfilePath(profileID, "/versions"), nil, saveRequest)
}

func (client *Client) SetStackProfileCurrentVersion(requestContext context.Context, profileID string, version int) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPost,
		stackProfilePath(profileID, "/current-version"), nil, map[string]int{"version": version})
}

func (client *Client) DiffStackProfileVersions(requestContext context.Context, profileID string, fromVersion int, toVersion int) (json.RawMessage, error) {
	query := url.Values{}
	query.Set("from_version", strconv.Itoa(fromVersion))
	query.Set("to_version", strconv.Itoa(toVersion))
	return client.stackProfileResourceRequest(requestContext, http.MethodGet,
		stackProfilePath(profileID, "/diff"), query, nil)
}

// --- fleet ---

func (client *Client) ListStackProfileInstantiations(requestContext context.Context, profileID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodGet,
		stackProfilePath(profileID, "/instantiations"), nil, nil)
}

// --- shares ---

func (client *Client) ListStackProfileShares(requestContext context.Context, profileID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodGet,
		stackProfilePath(profileID, "/shares"), nil, nil)
}

func (client *Client) CreateStackProfileShare(requestContext context.Context, profileID string, organisationSlug string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPost,
		stackProfilePath(profileID, "/shares"), nil, map[string]string{"organisation_slug": organisationSlug})
}

func (client *Client) DeleteStackProfileShare(requestContext context.Context, profileID string, shareID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodDelete,
		stackProfilePath(profileID, "/shares/"+url.PathEscape(shareID)), nil, nil)
}

// --- suggestions ---

func (client *Client) ListStackProfileSuggestions(requestContext context.Context, profileID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodGet,
		stackProfilePath(profileID, "/suggestions"), nil, nil)
}

func (client *Client) GetStackProfileSuggestion(requestContext context.Context, profileID string, suggestionID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodGet,
		stackProfilePath(profileID, "/suggestions/"+url.PathEscape(suggestionID)), nil, nil)
}

// ApproveStackProfileSuggestionRequest mirrors the approve body; an empty
// channel defaults to stable and an absent changelog is composed server-side.
type ApproveStackProfileSuggestionRequest struct {
	Channel   string  `json:"channel,omitempty"`
	Changelog *string `json:"changelog,omitempty"`
}

func (client *Client) ApproveStackProfileSuggestion(requestContext context.Context, profileID string, suggestionID string, approveRequest ApproveStackProfileSuggestionRequest) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPost,
		stackProfilePath(profileID, "/suggestions/"+url.PathEscape(suggestionID)+"/approve"), nil, approveRequest)
}

func (client *Client) RejectStackProfileSuggestion(requestContext context.Context, profileID string, suggestionID string, note string) (json.RawMessage, error) {
	var requestBody any
	if note != "" {
		requestBody = map[string]string{"note": note}
	}
	return client.stackProfileResourceRequest(requestContext, http.MethodPost,
		stackProfilePath(profileID, "/suggestions/"+url.PathEscape(suggestionID)+"/reject"), nil, requestBody)
}

func (client *Client) WithdrawStackProfileSuggestion(requestContext context.Context, profileID string, suggestionID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPost,
		stackProfilePath(profileID, "/suggestions/"+url.PathEscape(suggestionID)+"/withdraw"), nil, nil)
}

// --- demos ---

func (client *Client) ListStackProfileDemos(requestContext context.Context, profileID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodGet,
		stackProfilePath(profileID, "/demos"), nil, nil)
}

func (client *Client) LaunchStackProfileDemo(requestContext context.Context, profileID string, launchRequest LaunchStackProfileDemoRequest) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPost,
		stackProfilePath(profileID, "/demos"), nil, launchRequest)
}

func (client *Client) GetStackProfileDemoDetail(requestContext context.Context, profileID string, workspaceID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodGet,
		stackProfilePath(profileID, "/demos/"+url.PathEscape(workspaceID)+"/detail"), nil, nil)
}

func (client *Client) GetStackProfileDemoLogs(requestContext context.Context, profileID string, workspaceID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodGet,
		stackProfilePath(profileID, "/demos/"+url.PathEscape(workspaceID)+"/logs"), nil, nil)
}

func (client *Client) StopStackProfileDemo(requestContext context.Context, profileID string, workspaceID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodDelete,
		stackProfilePath(profileID, "/demos/"+url.PathEscape(workspaceID)), nil, nil)
}

// --- logo ---

// GetStackProfileLogo fetches the uploaded logo bytes with their content
// type; a profile with no uploaded logo answers 404.
func (client *Client) GetStackProfileLogo(requestContext context.Context, profileID string) (*StackProfileLogo, error) {
	requestURL := client.BaseURL + stackProfilePath(profileID, "/logo")
	request, requestError := http.NewRequestWithContext(requestContext, http.MethodGet, requestURL, nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
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
		message := detailFromBody(responseBody)
		if message == "" {
			message = "get stack profile logo failed"
		}
		return nil, newUnexpectedResponseError(message, response.StatusCode, redactedBodyForError(responseBody, 500))
	}
	return &StackProfileLogo{
		Content:     responseBody,
		ContentType: response.Header.Get("Content-Type"),
	}, nil
}

// PutStackProfileLogo uploads logo bytes as the multipart form the browser
// sends, and returns the refreshed profile summary.
func (client *Client) PutStackProfileLogo(requestContext context.Context, profileID string, content []byte, contentType string) (json.RawMessage, error) {
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="logo"`)
	partHeader.Set("Content-Type", contentType)
	part, partError := writer.CreatePart(partHeader)
	if partError != nil {
		return nil, fmt.Errorf("build upload: %w", partError)
	}
	if _, writeError := part.Write(content); writeError != nil {
		return nil, fmt.Errorf("build upload: %w", writeError)
	}
	if closeError := writer.Close(); closeError != nil {
		return nil, fmt.Errorf("build upload: %w", closeError)
	}

	requestURL := client.BaseURL + stackProfilePath(profileID, "/logo")
	request, requestError := http.NewRequestWithContext(requestContext, http.MethodPut, requestURL, &multipartBody)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
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
		message := detailFromBody(responseBody)
		if message == "" {
			message = "upload stack profile logo failed"
		}
		return nil, newUnexpectedResponseError(message, response.StatusCode, redactedBodyForError(responseBody, 500))
	}
	return json.RawMessage(responseBody), nil
}

func (client *Client) DeleteStackProfileLogo(requestContext context.Context, profileID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodDelete,
		stackProfilePath(profileID, "/logo"), nil, nil)
}

// --- draft verbs beyond the stored-spec edit cycle ---

func (client *Client) ValidateStackProfileDraft(requestContext context.Context, draftID string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPost,
		stackProfilesAPIPath+"/drafts/"+url.PathEscape(draftID)+"/validate", nil, nil)
}

// RebaseStackProfileDraftRequest mirrors the rebase body. The only strategy
// today is "acknowledge": keep the draft contents and move its base to the
// profile's latest published version.
type RebaseStackProfileDraftRequest struct {
	Strategy string `json:"strategy"`
}

func (client *Client) RebaseStackProfileDraft(requestContext context.Context, draftID string, rebaseRequest RebaseStackProfileDraftRequest) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPost,
		stackProfilesAPIPath+"/drafts/"+url.PathEscape(draftID)+"/rebase", nil, rebaseRequest)
}

func (client *Client) SubmitStackProfileSuggestion(requestContext context.Context, draftID string, title string) (json.RawMessage, error) {
	return client.stackProfileResourceRequest(requestContext, http.MethodPost,
		stackProfilesAPIPath+"/drafts/"+url.PathEscape(draftID)+"/submit-suggestion", nil,
		map[string]string{"title": title})
}

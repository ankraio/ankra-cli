package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
)

// The Overview layout of one application, under
// /api/v1/org/applications/{application_id}/overview-layout: what the
// application's Overview shows, the versions the team saved, and the three
// changes (save a version, put an earlier one back, go back to Ankra's own
// layout). Every change names the version the caller's layout was made on
// (base_revision_id, null for Ankra's layout); when the Overview changed
// since, the platform changes nothing and answers 409 with what it shows now.

// Overview layout answers, as ApplicationOverviewLayout.OnPage says them.
const (
	// OverviewLayoutOnPageAnkra is Ankra's own layout: the page draws it and
	// the platform keeps no version of it.
	OverviewLayoutOnPageAnkra = "ankra"
	// OverviewLayoutOnPageTeam is a version the team saved, in Revision.
	OverviewLayoutOnPageTeam = "team"
)

// Refusal codes the Overview layout routes send in error_code.
const (
	// OverviewLayoutCodeVersionNotFound is the 404 of a version id that is
	// not one of the application's Overview versions.
	OverviewLayoutCodeVersionNotFound = "overview_layout_version_not_found"
	// OverviewLayoutCodeInvalidCursor is the 400 of a cursor the versions
	// list did not hand out.
	OverviewLayoutCodeInvalidCursor = "invalid_cursor"
	// overviewLayoutCodeRevisionConflict is the 409 of a stale base.
	overviewLayoutCodeRevisionConflict = "canvas_revision_conflict"
)

// ApplicationOverviewLayoutRevision is the team's version on the Overview,
// with its content: a canvas spec of schema_version 2 as JSON text.
// AuthorUserID is nil when the version records nobody; AuthorName is nil
// when neither the member's name nor their email is known.
type ApplicationOverviewLayoutRevision struct {
	ID           string  `json:"id" yaml:"id"`
	Number       int     `json:"number" yaml:"number"`
	Content      string  `json:"content" yaml:"content"`
	AuthorUserID *string `json:"author_user_id" yaml:"author_user_id"`
	AuthorName   *string `json:"author_name" yaml:"author_name"`
	CreatedAt    string  `json:"created_at" yaml:"created_at"`
}

// ApplicationOverviewLayout is what an application's Overview shows: OnPage
// is OverviewLayoutOnPageAnkra (Revision nil) or OverviewLayoutOnPageTeam
// (Revision the version on the page). Versions counts every saved version,
// on the page or not.
type ApplicationOverviewLayout struct {
	RawDocument `json:"-" yaml:"-"`

	OnPage   string                             `json:"on_page" yaml:"on_page"`
	Revision *ApplicationOverviewLayoutRevision `json:"revision" yaml:"revision"`
	Versions int                                `json:"versions" yaml:"versions"`
}

// ApplicationOverviewLayoutVersion is one saved version, without its
// content. OnPage says it is the one the Overview shows now.
type ApplicationOverviewLayoutVersion struct {
	ID           string  `json:"id" yaml:"id"`
	Number       int     `json:"number" yaml:"number"`
	AuthorUserID *string `json:"author_user_id" yaml:"author_user_id"`
	AuthorName   *string `json:"author_name" yaml:"author_name"`
	CreatedAt    string  `json:"created_at" yaml:"created_at"`
	SizeBytes    int     `json:"size_bytes" yaml:"size_bytes"`
	OnPage       bool    `json:"on_page" yaml:"on_page"`
}

// ApplicationOverviewLayoutVersions is one page of saved versions, newest
// first. NextCursor reads the next page; nil on the last one.
type ApplicationOverviewLayoutVersions struct {
	RawDocument `json:"-" yaml:"-"`

	Items      []ApplicationOverviewLayoutVersion `json:"items" yaml:"items"`
	NextCursor *string                            `json:"next_cursor" yaml:"next_cursor"`
}

// OverviewLayoutConflictError is a change the platform refused because the
// Overview changed since the version it was made on: nothing was changed.
// Detail is the platform's sentence; Layout is what the Overview shows now,
// and CurrentRevisionID its version (nil on Ankra's layout).
type OverviewLayoutConflictError struct {
	Detail            string
	CurrentRevisionID *string
	Layout            ApplicationOverviewLayout
}

// Error implements error with the platform's sentence.
func (conflictError *OverviewLayoutConflictError) Error() string {
	return conflictError.Detail
}

// OverviewLayoutRefusalError is a 400 or 404 refusal that names its
// error_code. It unwraps to the *UnexpectedResponseError of its status, so
// the exit code follows the status (404 exits 3), and its text is the
// platform's sentence.
type OverviewLayoutRefusalError struct {
	Code  string
	cause *UnexpectedResponseError
}

// Error implements error with the platform's sentence.
func (refusalError *OverviewLayoutRefusalError) Error() string {
	return refusalError.cause.Error()
}

// Unwrap returns the response error the refusal came with.
func (refusalError *OverviewLayoutRefusalError) Unwrap() error {
	return refusalError.cause
}

type overviewLayoutBase struct {
	BaseRevisionID *string `json:"base_revision_id"`
}

type overviewLayoutSave struct {
	Content        string  `json:"content"`
	BaseRevisionID *string `json:"base_revision_id"`
}

func (client *Client) overviewLayoutURL(applicationID string) string {
	return client.BaseURL + applicationsAPIPath + "/" + neturl.PathEscape(applicationID) + "/overview-layout"
}

// GetApplicationOverviewLayout reads what the application's Overview shows.
// GET /api/v1/org/applications/{application_id}/overview-layout
func (client *Client) GetApplicationOverviewLayout(requestContext context.Context, applicationID string) (*ApplicationOverviewLayout, error) {
	var layout ApplicationOverviewLayout
	if readError := client.sendOverviewLayoutRequest(requestContext, http.MethodGet,
		client.overviewLayoutURL(applicationID), nil, &layout); readError != nil {
		return nil, readError
	}
	return &layout, nil
}

// ListApplicationOverviewLayoutVersions reads one page of the saved versions,
// newest first. An empty cursor reads the first page; a zero limit leaves
// the page size to the platform (25).
// GET /api/v1/org/applications/{application_id}/overview-layout/versions
func (client *Client) ListApplicationOverviewLayoutVersions(requestContext context.Context, applicationID string,
	cursor string, limit int) (*ApplicationOverviewLayoutVersions, error) {
	requestURL := client.overviewLayoutURL(applicationID) + "/versions"
	query := neturl.Values{}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	var versions ApplicationOverviewLayoutVersions
	if listError := client.sendOverviewLayoutRequest(requestContext, http.MethodGet, requestURL, nil, &versions); listError != nil {
		return nil, listError
	}
	if versions.Items == nil {
		versions.Items = []ApplicationOverviewLayoutVersion{}
	}
	return &versions, nil
}

// SaveApplicationOverviewLayoutVersion saves content as a new version and
// puts it on the Overview. baseRevisionID is the version the content was
// made on, nil when the Overview showed Ankra's layout; it is always sent,
// as null when nil, because the platform requires it.
// POST /api/v1/org/applications/{application_id}/overview-layout/versions
func (client *Client) SaveApplicationOverviewLayoutVersion(requestContext context.Context, applicationID string,
	content string, baseRevisionID *string) (*ApplicationOverviewLayout, error) {
	var layout ApplicationOverviewLayout
	if saveError := client.sendOverviewLayoutRequest(requestContext, http.MethodPost,
		client.overviewLayoutURL(applicationID)+"/versions",
		overviewLayoutSave{Content: content, BaseRevisionID: baseRevisionID}, &layout); saveError != nil {
		return nil, saveError
	}
	return &layout, nil
}

// PutBackApplicationOverviewLayoutVersion puts an earlier version back on the
// Overview, as a new version with its content; the earlier one stays.
// baseRevisionID works as on a save.
// POST /api/v1/org/applications/{application_id}/overview-layout/versions/{revision_id}/put-back
func (client *Client) PutBackApplicationOverviewLayoutVersion(requestContext context.Context, applicationID string,
	revisionID string, baseRevisionID *string) (*ApplicationOverviewLayout, error) {
	var layout ApplicationOverviewLayout
	if putBackError := client.sendOverviewLayoutRequest(requestContext, http.MethodPost,
		client.overviewLayoutURL(applicationID)+"/versions/"+neturl.PathEscape(revisionID)+"/put-back",
		overviewLayoutBase{BaseRevisionID: baseRevisionID}, &layout); putBackError != nil {
		return nil, putBackError
	}
	return &layout, nil
}

// UseAnkraApplicationOverviewLayout puts the Overview back on Ankra's own
// layout. No version is written or removed. baseRevisionID works as on a
// save; nil while the Overview already shows Ankra's layout changes nothing.
// POST /api/v1/org/applications/{application_id}/overview-layout/use-ankra
func (client *Client) UseAnkraApplicationOverviewLayout(requestContext context.Context, applicationID string,
	baseRevisionID *string) (*ApplicationOverviewLayout, error) {
	var layout ApplicationOverviewLayout
	if useAnkraError := client.sendOverviewLayoutRequest(requestContext, http.MethodPost,
		client.overviewLayoutURL(applicationID)+"/use-ankra",
		overviewLayoutBase{BaseRevisionID: baseRevisionID}, &layout); useAnkraError != nil {
		return nil, useAnkraError
	}
	return &layout, nil
}

// sendOverviewLayoutRequest is sendJSONContext for the Overview layout
// routes, which read two refusals as documents: the 409 of a stale base
// becomes an *OverviewLayoutConflictError carrying what the Overview shows
// now, and a refusal that names an error_code an *OverviewLayoutRefusalError.
// Every other answer is classified as sendJSON classifies it.
func (client *Client) sendOverviewLayoutRequest(requestContext context.Context, method string, requestURL string,
	payload any, target any) error {
	var bodyReader *bytes.Reader
	if payload != nil {
		encoded, marshalError := json.Marshal(payload)
		if marshalError != nil {
			return fmt.Errorf("marshal request: %w", marshalError)
		}
		bodyReader = bytes.NewReader(encoded)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	request, requestError := http.NewRequestWithContext(requestContext, method, requestURL, bodyReader)
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.Token)
	response, doError := client.HTTP.Do(request)
	if doError != nil {
		return fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)
	body, readError := readResponseBody(response)
	if readError != nil {
		return fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusConflict {
		if conflictError := overviewLayoutConflictFromBody(body); conflictError != nil {
			return conflictError
		}
	}
	if responseError := errorFromResponse(response.StatusCode, body); responseError != nil {
		var unexpected *UnexpectedResponseError
		if errors.As(responseError, &unexpected) && unexpected.Detail != "" {
			if code := overviewLayoutErrorCode(body); code != "" {
				return &OverviewLayoutRefusalError{Code: code, cause: unexpected}
			}
		}
		return responseError
	}
	if unmarshalError := decodeKeepingRawDocument(body, target); unmarshalError != nil {
		return fmt.Errorf("parse response: %w", unmarshalError)
	}
	return nil
}

// overviewLayoutConflictFromBody reads a stale base's 409, nil when the body
// is not that document.
func overviewLayoutConflictFromBody(body []byte) *OverviewLayoutConflictError {
	var conflict struct {
		Detail            string                    `json:"detail"`
		ErrorCode         string                    `json:"error_code"`
		CurrentRevisionID *string                   `json:"current_revision_id"`
		Layout            ApplicationOverviewLayout `json:"layout"`
	}
	if json.Unmarshal(body, &conflict) != nil || conflict.ErrorCode != overviewLayoutCodeRevisionConflict || conflict.Detail == "" {
		return nil
	}
	return &OverviewLayoutConflictError{
		Detail: conflict.Detail, CurrentRevisionID: conflict.CurrentRevisionID, Layout: conflict.Layout,
	}
}

// overviewLayoutErrorCode is a refusal's error_code, empty when it has none.
func overviewLayoutErrorCode(body []byte) string {
	var refusal struct {
		ErrorCode string `json:"error_code"`
	}
	if json.Unmarshal(body, &refusal) != nil {
		return ""
	}
	return refusal.ErrorCode
}

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
)

// The searches saved for an application's Logs, under
// /api/v1/org/applications/{application_id}/saved-log-searches: the Logs
// page's "Saved searches" menu. A saved search belongs to the application and
// everyone who can read the application sees it. The platform checks a
// search's query and stores it whole; it never runs it.

// Codes of a refused saved search request, the platform's error_code.
const (
	// SavedLogSearchCodeNameInvalid is a name the rules refuse (400).
	SavedLogSearchCodeNameInvalid = "saved_log_search_name_invalid"
	// SavedLogSearchCodeQueryInvalid is a query the rules refuse (400).
	SavedLogSearchCodeQueryInvalid = "saved_log_search_query_invalid"
	// SavedLogSearchCodeTooLarge is a request body over the platform's bound (400).
	SavedLogSearchCodeTooLarge = "content_too_large"
	// SavedLogSearchCodeNameTaken is a name another search of the application
	// already reads as, capitals aside (409).
	SavedLogSearchCodeNameTaken = "saved_log_search_name_taken"
	// SavedLogSearchCodeLimitReached is an application that already keeps the
	// most searches it may (409).
	SavedLogSearchCodeLimitReached = "saved_log_search_limit_reached"
	// SavedLogSearchCodeNotFound is a search that is not one of the
	// application's (404).
	SavedLogSearchCodeNotFound = "saved_log_search_not_found"
)

// SavedLogSearchQueryUnreadable is the query_state of a search whose stored
// query the platform's rules no longer accept. Such a search is listed with
// a null query, so it can be replaced or removed.
const SavedLogSearchQueryUnreadable = "unreadable"

// SavedLogSearchFilter is one filter of a query: lines whose Field has the
// Value or, with Not, lines whose Field does not.
type SavedLogSearchFilter struct {
	Field string `json:"field" yaml:"field"`
	Value string `json:"value" yaml:"value"`
	Not   bool   `json:"not" yaml:"not"`
}

// SavedLogSearchSpan is a time range as the Logs page writes it: each end is
// relative to the moment the search is used ("now", "now-15m") or a UTC
// moment ("2026-11-14 14:00").
type SavedLogSearchSpan struct {
	From string `json:"from" yaml:"from"`
	To   string `json:"to" yaml:"to"`
}

// SavedLogSearchQuery is what a saved search searches with. Filters is never
// null on the wire, and Span is null when the search keeps the time range
// the page shows when it is used.
type SavedLogSearchQuery struct {
	Text    string                 `json:"text" yaml:"text"`
	Filters []SavedLogSearchFilter `json:"filters" yaml:"filters"`
	Span    *SavedLogSearchSpan    `json:"span" yaml:"span"`
}

// SavedLogSearchMember is who saved or last changed a search. Name is nil
// when the platform does not name them.
type SavedLogSearchMember struct {
	UserID string  `json:"user_id" yaml:"user_id"`
	Name   *string `json:"name" yaml:"name"`
}

// SavedLogSearch is one saved search. Query is the query document as the
// platform sent it, JSON null exactly when QueryState is
// SavedLogSearchQueryUnreadable; ReadQuery decodes it.
type SavedLogSearch struct {
	RawDocument `json:"-" yaml:"-"`

	ID         string               `json:"id" yaml:"id"`
	Name       string               `json:"name" yaml:"name"`
	Query      json.RawMessage      `json:"query" yaml:"query"`
	QueryState string               `json:"query_state" yaml:"query_state"`
	CreatedBy  SavedLogSearchMember `json:"created_by" yaml:"created_by"`
	CreatedAt  string               `json:"created_at" yaml:"created_at"`
	UpdatedBy  SavedLogSearchMember `json:"updated_by" yaml:"updated_by"`
	UpdatedAt  string               `json:"updated_at" yaml:"updated_at"`
}

// ReadQuery decodes the search's query. It answers nil with no error when
// the platform sent no query (an unreadable search), and an error when the
// query it sent does not decode.
func (search SavedLogSearch) ReadQuery() (*SavedLogSearchQuery, error) {
	trimmed := bytes.TrimSpace(search.Query)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var query SavedLogSearchQuery
	if decodeError := json.Unmarshal(trimmed, &query); decodeError != nil {
		return nil, fmt.Errorf("reading the query of saved search %s: %w", search.ID, decodeError)
	}
	return &query, nil
}

// SavedLogSearches is every search saved for an application, ordered by
// name. The list is whole: Limit is the most one application keeps.
type SavedLogSearches struct {
	RawDocument `json:"-" yaml:"-"`

	Searches []SavedLogSearch `json:"searches" yaml:"searches"`
	Limit    int              `json:"limit" yaml:"limit"`
}

// SavedLogSearchRequest is the body of a save and of a replace: the whole
// search. Query is the query document; the platform refuses one that is
// missing a member or has one it does not keep.
type SavedLogSearchRequest struct {
	Name  string          `json:"name"`
	Query json.RawMessage `json:"query"`
}

// SavedLogSearchRefusalError is a saved search request the platform refused
// with an error_code. Its text is the platform's sentence, and it unwraps to
// the *UnexpectedResponseError of its status, so the exit code follows the
// status unless the caller classifies the code.
type SavedLogSearchRefusalError struct {
	Code  string
	cause *UnexpectedResponseError
}

// Error implements error with the platform's sentence.
func (refusal *SavedLogSearchRefusalError) Error() string {
	return refusal.cause.Error()
}

// Unwrap returns the response error the refusal came with.
func (refusal *SavedLogSearchRefusalError) Unwrap() error {
	return refusal.cause
}

func (client *Client) savedLogSearchesURL(applicationID string) string {
	return client.BaseURL + applicationsAPIPath + "/" + neturl.PathEscape(applicationID) + "/saved-log-searches"
}

func (client *Client) savedLogSearchURL(applicationID string, searchID string) string {
	return client.savedLogSearchesURL(applicationID) + "/" + neturl.PathEscape(searchID)
}

// ListApplicationSavedLogSearches reads every search saved for the
// application's Logs.
// GET /api/v1/org/applications/{application_id}/saved-log-searches
func (client *Client) ListApplicationSavedLogSearches(requestContext context.Context, applicationID string) (*SavedLogSearches, error) {
	var searches SavedLogSearches
	if listError := client.sendSavedLogSearchRequest(requestContext, http.MethodGet,
		client.savedLogSearchesURL(applicationID), nil, &searches); listError != nil {
		return nil, listError
	}
	if searches.Searches == nil {
		searches.Searches = []SavedLogSearch{}
	}
	return &searches, nil
}

// SaveApplicationLogSearch saves a new search for the application's Logs.
// The platform answers 201 with the search.
// POST /api/v1/org/applications/{application_id}/saved-log-searches
func (client *Client) SaveApplicationLogSearch(requestContext context.Context, applicationID string,
	searchRequest SavedLogSearchRequest) (*SavedLogSearch, error) {
	var saved SavedLogSearch
	if saveError := client.sendSavedLogSearchRequest(requestContext, http.MethodPost,
		client.savedLogSearchesURL(applicationID), searchRequest, &saved); saveError != nil {
		return nil, saveError
	}
	return &saved, nil
}

// ReplaceApplicationSavedLogSearch replaces a saved search's name and query,
// both. The platform answers the search as it is now.
// PUT /api/v1/org/applications/{application_id}/saved-log-searches/{search_id}
func (client *Client) ReplaceApplicationSavedLogSearch(requestContext context.Context, applicationID string, searchID string,
	searchRequest SavedLogSearchRequest) (*SavedLogSearch, error) {
	var replaced SavedLogSearch
	if replaceError := client.sendSavedLogSearchRequest(requestContext, http.MethodPut,
		client.savedLogSearchURL(applicationID, searchID), searchRequest, &replaced); replaceError != nil {
		return nil, replaceError
	}
	return &replaced, nil
}

// DeleteApplicationSavedLogSearch removes a saved search for everyone. The
// platform answers 204 with no body.
// DELETE /api/v1/org/applications/{application_id}/saved-log-searches/{search_id}
func (client *Client) DeleteApplicationSavedLogSearch(requestContext context.Context, applicationID string, searchID string) error {
	return client.sendSavedLogSearchRequest(requestContext, http.MethodDelete,
		client.savedLogSearchURL(applicationID, searchID), nil, nil)
}

// sendSavedLogSearchRequest is sendJSONContext that keeps the error_code of
// a refusal: a refusal with a detail and a code answers a
// *SavedLogSearchRefusalError, every other failure what sendJSONContext
// answers. A nil target reads no body.
func (client *Client) sendSavedLogSearchRequest(requestContext context.Context, method string, requestURL string,
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
	if responseError := errorFromResponse(response.StatusCode, body); responseError != nil {
		var unexpected *UnexpectedResponseError
		if errors.As(responseError, &unexpected) && unexpected.Detail != "" {
			if code := savedLogSearchErrorCode(body); code != "" {
				return &SavedLogSearchRefusalError{Code: code, cause: unexpected}
			}
		}
		return responseError
	}
	if target == nil {
		return nil
	}
	if unmarshalError := decodeKeepingRawDocument(body, target); unmarshalError != nil {
		return fmt.Errorf("parse response: %w", unmarshalError)
	}
	return nil
}

// savedLogSearchErrorCode reads a refusal's error_code; "" when it has none.
func savedLogSearchErrorCode(body []byte) string {
	var refusal struct {
		ErrorCode string `json:"error_code"`
	}
	if json.Unmarshal(body, &refusal) != nil {
		return ""
	}
	return refusal.ErrorCode
}

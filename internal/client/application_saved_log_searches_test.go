package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// These pin the method, path and body of the saved Logs search routes, that
// an unreadable search is kept with no query, and that a refusal arrives as
// the platform's own sentence with its code.

const savedLogSearchTestBody = `{"id": "search-1", "name": "Checkout errors",
  "query": {"text": "checkout", "filters": [{"field": "level", "value": "error", "not": false}], "span": null},
  "query_state": "readable",
  "created_by": {"user_id": "u-1", "name": "On-call engineer"}, "created_at": "2026-11-20T08:15:00Z",
  "updated_by": {"user_id": "u-1", "name": null}, "updated_at": "2026-11-20T08:15:00Z"}`

func TestListApplicationSavedLogSearchesKeepsTheDocumentAndAnUnreadableSearch(t *testing.T) {
	listBody := `{"searches": [` + savedLogSearchTestBody + `, {"id": "search-2", "name": "Old", "query": null,
	  "query_state": "unreadable", "created_by": {"user_id": "u-1", "name": null}, "created_at": "2026-11-18T07:00:00Z",
	  "updated_by": {"user_id": "u-1", "name": null}, "updated_at": "2026-11-18T07:00:00Z"}], "limit": 50, "a_newer_field": 1}`
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/org/applications/app-1/saved-log-searches" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		writeApplicationAlertAnswer(writer, http.StatusOK, listBody)
	})

	searches, listError := testClient.ListApplicationSavedLogSearches(context.Background(), "app-1")

	if listError != nil {
		t.Fatalf("ListApplicationSavedLogSearches error = %v", listError)
	}
	if len(searches.Searches) != 2 || searches.Limit != 50 {
		t.Fatalf("searches = %+v", searches)
	}
	readable, readableError := searches.Searches[0].ReadQuery()
	if readableError != nil || readable == nil || readable.Text != "checkout" || len(readable.Filters) != 1 || readable.Span != nil {
		t.Errorf("readable query = %+v, %v", readable, readableError)
	}
	unreadable, unreadableError := searches.Searches[1].ReadQuery()
	if unreadableError != nil || unreadable != nil || searches.Searches[1].QueryState != SavedLogSearchQueryUnreadable {
		t.Errorf("unreadable query = %+v, %v, state %q", unreadable, unreadableError, searches.Searches[1].QueryState)
	}
	if !strings.Contains(string(searches.RawJSON()), "a_newer_field") {
		t.Errorf("the platform's document was not kept: %s", searches.RawJSON())
	}
}

func TestSaveApplicationLogSearchPostsTheNameAndTheQueryAsGiven(t *testing.T) {
	query := json.RawMessage(`{"text":"checkout","filters":[],"span":{"from":"now-1h","to":"now"}}`)
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/applications/app-1/saved-log-searches" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		body, _ := io.ReadAll(request.Body)
		if want := `{"name":"Checkout errors","query":` + string(query) + `}`; string(body) != want {
			t.Errorf("body = %s, want %s", body, want)
		}
		writeApplicationAlertAnswer(writer, http.StatusCreated, savedLogSearchTestBody)
	})

	saved, saveError := testClient.SaveApplicationLogSearch(context.Background(), "app-1",
		SavedLogSearchRequest{Name: "Checkout errors", Query: query})

	if saveError != nil || saved.ID != "search-1" || len(saved.RawJSON()) == 0 {
		t.Errorf("saved = %+v, %v", saved, saveError)
	}
}

func TestReplaceApplicationSavedLogSearchRefusalCarriesTheSentenceTheCodeAndTheStatus(t *testing.T) {
	sentence := `This application already has a saved search named "Errors". Choose another name.`
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/api/v1/org/applications/app-1/saved-log-searches/search-1" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		encoded, _ := json.Marshal(map[string]string{"detail": sentence, "error_code": SavedLogSearchCodeNameTaken})
		writeApplicationAlertAnswer(writer, http.StatusConflict, string(encoded))
	})

	_, replaceError := testClient.ReplaceApplicationSavedLogSearch(context.Background(), "app-1", "search-1",
		SavedLogSearchRequest{Name: "Errors", Query: json.RawMessage(`{"text":"","filters":[],"span":null}`)})

	var refusal *SavedLogSearchRefusalError
	if !errors.As(replaceError, &refusal) || refusal.Code != SavedLogSearchCodeNameTaken {
		t.Fatalf("error = %#v, want a refusal with the name-taken code", replaceError)
	}
	if replaceError.Error() != sentence {
		t.Errorf("error text = %q, want the platform's sentence", replaceError.Error())
	}
	var unexpected *UnexpectedResponseError
	if !errors.As(replaceError, &unexpected) || unexpected.StatusCode != http.StatusConflict {
		t.Errorf("the refusal does not unwrap to its status: %#v", replaceError)
	}
}

func TestDeleteApplicationSavedLogSearchReadsNoBodyFromTheNoContentAnswer(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/api/v1/org/applications/app-1/saved-log-searches/search-1" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		writer.WriteHeader(http.StatusNoContent)
	})

	if deleteError := testClient.DeleteApplicationSavedLogSearch(context.Background(), "app-1", "search-1"); deleteError != nil {
		t.Errorf("DeleteApplicationSavedLogSearch error = %v", deleteError)
	}
}

func TestApplicationNotFoundStaysAPlainNotFoundWithoutACode(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writeApplicationAlertAnswer(writer, http.StatusNotFound, `{"detail": "Application not found"}`)
	})

	_, listError := testClient.ListApplicationSavedLogSearches(context.Background(), "app-1")

	var refusal *SavedLogSearchRefusalError
	var unexpected *UnexpectedResponseError
	if errors.As(listError, &refusal) || !errors.As(listError, &unexpected) || unexpected.StatusCode != http.StatusNotFound {
		t.Errorf("error = %#v, want a plain 404", listError)
	}
}

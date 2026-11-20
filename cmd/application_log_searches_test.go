package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"ankra/internal/client"
)

// These drive `ankra application log-searches` through the real client
// against a stub platform (the alerts tests' stub), so what is asserted is
// the request that leaves and what the command prints from the answer.

const (
	logSearchesTestCollection   = "/api/v1/org/applications/" + testApplicationID + "/saved-log-searches"
	logSearchesTestCheckoutID   = "0b7c4d01-5f6a-4b8c-9d0e-1f2a3b4c5d6e"
	logSearchesTestUnreadableID = "0b7c4d02-5f6a-4b8c-9d0e-1f2a3b4c5d6e"
	logSearchesTestCheckoutPath = logSearchesTestCollection + "/" + logSearchesTestCheckoutID
)

const logSearchesTestCheckout = `{
  "id": "0b7c4d01-5f6a-4b8c-9d0e-1f2a3b4c5d6e",
  "name": "Checkout errors",
  "query": {"text": "checkout", "filters": [
    {"field": "level", "value": "error", "not": false},
    {"field": "pod", "value": "shop-7f9c", "not": true}
  ], "span": {"from": "now-1h", "to": "now"}},
  "query_state": "readable",
  "created_by": {"user_id": "u-1", "name": "On-call engineer"},
  "created_at": "2026-11-20T08:15:00Z",
  "updated_by": {"user_id": "u-1", "name": "On-call engineer"},
  "updated_at": "2026-11-20T08:15:00Z"
}`

const logSearchesTestListPayload = `{
  "searches": [
    ` + logSearchesTestCheckout + `,
    {
      "id": "0b7c4d03-5f6a-4b8c-9d0e-1f2a3b4c5d6e",
      "name": "Everything",
      "query": {"text": "", "filters": [], "span": null},
      "query_state": "readable",
      "created_by": {"user_id": "u-2", "name": null},
      "created_at": "2026-11-19T10:00:00Z",
      "updated_by": {"user_id": "u-2", "name": null},
      "updated_at": "2026-11-19T10:00:00Z"
    },
    {
      "id": "0b7c4d02-5f6a-4b8c-9d0e-1f2a3b4c5d6e",
      "name": "Old incident",
      "query": null,
      "query_state": "unreadable",
      "created_by": {"user_id": "u-1", "name": "On-call engineer"},
      "created_at": "2026-11-18T07:00:00Z",
      "updated_by": {"user_id": "u-1", "name": "On-call engineer"},
      "updated_at": "2026-11-18T07:00:00Z"
    }
  ],
  "limit": 50,
  "answered_by_a_newer_platform": "kept"
}`

func runApplicationLogSearches(t *testing.T, stub *alertsStub, input string, arguments ...string) (string, error) {
	t.Helper()
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	return runApplicationCommandWithInput(t, client.New("test-token", server.URL), input,
		append([]string{"log-searches"}, arguments...)...)
}

func TestApplicationLogSearchesListSaysWhatEachSearchLooksForAndKeepsAnUnreadableOne(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + logSearchesTestCollection: {http.StatusOK, logSearchesTestListPayload},
	})

	output, executeError := runApplicationLogSearches(t, stub, "", "list", testApplicationID)

	if executeError != nil {
		t.Fatalf("log-searches list error = %v", executeError)
	}
	expected := strings.Join([]string{
		"NAME             SEARCHES                                                                               SAVED BY          SAVED                 SEARCH ID",
		`Checkout errors  text "checkout", level is error, copy is not shop-7f9c, the last hour                  On-call engineer  2026-11-20 08:15 UTC  0b7c4d01-5f6a-4b8c-9d0e-1f2a3b4c5d6e`,
		"Everything       every line, the time range on the page                                                 -                 2026-11-19 10:00 UTC  0b7c4d03-5f6a-4b8c-9d0e-1f2a3b4c5d6e",
		"Old incident     cannot be read: the platform no longer accepts its stored query; replace or remove it  On-call engineer  2026-11-18 07:00 UTC  0b7c4d02-5f6a-4b8c-9d0e-1f2a3b4c5d6e",
		"Saved searches: 3 of at most 50.",
		"",
	}, "\n")
	if output != expected {
		t.Errorf("output differs.\n got:\n%s\nwant:\n%s", output, expected)
	}
}

func TestApplicationLogSearchesListJSONPrintsThePlatformsDocument(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + logSearchesTestCollection: {http.StatusOK, logSearchesTestListPayload},
	})

	output, executeError := runApplicationLogSearches(t, stub, "", "list", testApplicationID, "-o", "json")

	if executeError != nil {
		t.Fatalf("log-searches list -o json error = %v", executeError)
	}
	var printed, answered any
	if decodeError := json.Unmarshal([]byte(output), &printed); decodeError != nil {
		t.Fatalf("output is not JSON: %v\n%s", decodeError, output)
	}
	_ = json.Unmarshal([]byte(logSearchesTestListPayload), &answered)
	if !reflect.DeepEqual(printed, answered) {
		t.Errorf("-o json is not the platform's document:\n%s", output)
	}
}

func TestApplicationLogSearchesListThatCannotBeReadIsAnErrorNotAnEmptyList(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + logSearchesTestCollection: {http.StatusInternalServerError, `{"detail": "Internal server error"}`},
	})

	output, executeError := runApplicationLogSearches(t, stub, "", "list", testApplicationID)

	if executeError == nil {
		t.Fatal("a failed read succeeded")
	}
	if strings.Contains(output, "No Logs searches") || strings.Contains(output, "Saved searches:") {
		t.Errorf("a failed read was printed as a list:\n%s", output)
	}
	if exitCodeFor(executeError) != exitError {
		t.Errorf("exit code = %d, want %d", exitCodeFor(executeError), exitError)
	}
}

func TestApplicationLogSearchesListWithNoSearchSaysSo(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + logSearchesTestCollection: {http.StatusOK, `{"searches": [], "limit": 50}`},
	})

	output, executeError := runApplicationLogSearches(t, stub, "", "list", testApplicationID)

	if executeError != nil {
		t.Fatalf("log-searches list error = %v", executeError)
	}
	if want := "No Logs searches are saved on " + testApplicationID + ".\n"; output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
}

func TestApplicationLogSearchesSaveSendsTheQueryTheFlagsGive(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"POST " + logSearchesTestCollection: {http.StatusCreated, logSearchesTestCheckout},
	})

	output, executeError := runApplicationLogSearches(t, stub, "", "save", testApplicationID,
		"--name", "Checkout errors", "--text", "checkout", "--level", "error", "--copy", "shop-7f9c",
		"--filter", "route!=/health", "--filter", "status=a=b", "--last", "1h")

	if executeError != nil {
		t.Fatalf("log-searches save error = %v", executeError)
	}
	requests := stub.sent(http.MethodPost, logSearchesTestCollection)
	if len(requests) != 1 {
		t.Fatalf("save requests = %+v, want one", requests)
	}
	expectedBody := map[string]any{
		"name": "Checkout errors",
		"query": map[string]any{
			"text": "checkout",
			"filters": []any{
				map[string]any{"field": "level", "value": "error", "not": false},
				map[string]any{"field": "pod", "value": "shop-7f9c", "not": false},
				map[string]any{"field": "route", "value": "/health", "not": true},
				map[string]any{"field": "status", "value": "a=b", "not": false},
			},
			"span": map[string]any{"from": "now-1h", "to": "now"},
		},
	}
	if !reflect.DeepEqual(requests[0].body, expectedBody) {
		t.Errorf("save body = %#v, want %#v", requests[0].body, expectedBody)
	}
	if want := "Saved the Logs search \"Checkout errors\" on " + testApplicationID + " (" + logSearchesTestCheckoutID + ").\n"; output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
}

func TestApplicationLogSearchesSaveWithNoQueryFlagSendsEveryLineOverThePagesRange(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"POST " + logSearchesTestCollection: {http.StatusCreated, logSearchesTestCheckout},
	})

	_, executeError := runApplicationLogSearches(t, stub, "", "save", testApplicationID, "--name", "Everything")

	if executeError != nil {
		t.Fatalf("log-searches save error = %v", executeError)
	}
	requests := stub.sent(http.MethodPost, logSearchesTestCollection)
	expectedQuery := map[string]any{"text": "", "filters": []any{}, "span": nil}
	if len(requests) != 1 || !reflect.DeepEqual(requests[0].body["query"], expectedQuery) {
		t.Errorf("save requests = %+v, want one with %v", requests, expectedQuery)
	}
}

func TestApplicationLogSearchesSaveFromAToMomentSendsBothEnds(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"POST " + logSearchesTestCollection: {http.StatusCreated, logSearchesTestCheckout},
	})

	_, executeError := runApplicationLogSearches(t, stub, "", "save", testApplicationID, "--name", "Incident",
		"--from", "2026-11-14 14:00")

	if executeError != nil {
		t.Fatalf("log-searches save error = %v", executeError)
	}
	requests := stub.sent(http.MethodPost, logSearchesTestCollection)
	query, _ := requests[0].body["query"].(map[string]any)
	if want := map[string]any{"from": "2026-11-14 14:00", "to": "now"}; !reflect.DeepEqual(query["span"], want) {
		t.Errorf("span = %v, want %v", query["span"], want)
	}
}

func TestApplicationLogSearchesSaveFromStdinSendsTheQueryDocumentAsWritten(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"POST " + logSearchesTestCollection: {http.StatusCreated, logSearchesTestCheckout},
	})
	document := `{"text": "", "filters": [{"field": "version", "value": "2.8.1", "not": true}], "span": null}`

	_, executeError := runApplicationLogSearches(t, stub, document, "save", testApplicationID,
		"--name", "Not the canary", "--file", "-")

	if executeError != nil {
		t.Fatalf("log-searches save error = %v", executeError)
	}
	var expectedQuery any
	_ = json.Unmarshal([]byte(document), &expectedQuery)
	requests := stub.sent(http.MethodPost, logSearchesTestCollection)
	if len(requests) != 1 || !reflect.DeepEqual(requests[0].body["query"], expectedQuery) {
		t.Errorf("save requests = %+v, want the document %s", requests, document)
	}
}

func TestApplicationLogSearchesSaveRefusedPrintsThePlatformsSentenceAndExitsUsage(t *testing.T) {
	refusals := []struct {
		status int
		body   string
	}{
		{http.StatusConflict, `{"detail": "This application already has a saved search named \"Checkout errors\". Choose another name.", "error_code": "saved_log_search_name_taken"}`},
		{http.StatusConflict, `{"detail": "This application already has 50 saved searches, the most it can keep. Remove one before saving another.", "error_code": "saved_log_search_limit_reached"}`},
		{http.StatusBadRequest, `{"detail": "The span's from must come before its to.", "error_code": "saved_log_search_query_invalid"}`},
		{http.StatusBadRequest, `{"detail": "A name must be 1 to 80 characters; this one is 81.", "error_code": "saved_log_search_name_invalid"}`},
	}
	for _, refusal := range refusals {
		stub := newAlertsStub(t, map[string]alertsStubAnswer{
			"POST " + logSearchesTestCollection: {refusal.status, refusal.body},
		})

		output, executeError := runApplicationLogSearches(t, stub, "", "save", testApplicationID, "--name", "Checkout errors")

		var answered struct {
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal([]byte(refusal.body), &answered)
		if executeError == nil || executeError.Error() != "saving the search: "+answered.Detail {
			t.Errorf("error = %v, want the platform's sentence %q", executeError, answered.Detail)
		}
		if exitCodeFor(executeError) != exitUsage {
			t.Errorf("%s: exit code = %d, want %d", answered.Detail, exitCodeFor(executeError), exitUsage)
		}
		if strings.Contains(output, "Saved the Logs search") {
			t.Errorf("a refusal printed a success line: %q", output)
		}
	}
}

func TestApplicationLogSearchesSaveRefusesFlagsItCannotSendBeforeSendingAnything(t *testing.T) {
	invocations := [][]string{
		{"--file", "-", "--text", "checkout"},
		{"--filter", "level"},
		{"--filter", "=error"},
		{"--filter", "level="},
		{"--last", "1 hour"},
		{"--last", "1h", "--from", "now-2h"},
		{"--to", "now"},
		{"--name", " "},
	}
	for _, invocation := range invocations {
		stub := newAlertsStub(t, map[string]alertsStubAnswer{})
		arguments := append([]string{"save", testApplicationID, "--name", "Checkout errors"}, invocation...)

		_, executeError := runApplicationLogSearches(t, stub, `{"text": ""}`, arguments...)

		if exitCodeFor(executeError) != exitUsage {
			t.Errorf("%v: exit code = %d (%v), want %d", invocation, exitCodeFor(executeError), executeError, exitUsage)
		}
		if requests := stub.sent(http.MethodPost, logSearchesTestCollection); len(requests) != 0 {
			t.Errorf("%v: sent %+v", invocation, requests)
		}
	}
}

func TestApplicationLogSearchesSaveFromAFileThatIsNotAnObjectIsRefused(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{})

	_, executeError := runApplicationLogSearches(t, stub, `["checkout"]`, "save", testApplicationID,
		"--name", "Checkout errors", "--file", "-")

	if exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "does not hold one JSON object") {
		t.Errorf("error = %v (exit %d), want a usage refusal", executeError, exitCodeFor(executeError))
	}
}

func TestApplicationLogSearchesReplaceByNameWithANewNameKeepsTheStoredQuery(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + logSearchesTestCollection:   {http.StatusOK, logSearchesTestListPayload},
		"PUT " + logSearchesTestCheckoutPath: {http.StatusOK, strings.Replace(logSearchesTestCheckout, `"Checkout errors"`, `"Checkout failures"`, 1)},
	})

	output, executeError := runApplicationLogSearches(t, stub, "", "replace", testApplicationID, "checkout   ERRORS",
		"--name", "Checkout failures")

	if executeError != nil {
		t.Fatalf("log-searches replace error = %v", executeError)
	}
	requests := stub.sent(http.MethodPut, logSearchesTestCheckoutPath)
	if len(requests) != 1 {
		t.Fatalf("replace requests = %+v, want one", requests)
	}
	var stored struct {
		Query any `json:"query"`
	}
	_ = json.Unmarshal([]byte(logSearchesTestCheckout), &stored)
	if requests[0].body["name"] != "Checkout failures" || !reflect.DeepEqual(requests[0].body["query"], stored.Query) {
		t.Errorf("replace body = %#v, want the new name and the stored query", requests[0].body)
	}
	if want := "Replaced the Logs search \"Checkout failures\" on " + testApplicationID + " (" + logSearchesTestCheckoutID + ").\n"; output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
}

func TestApplicationLogSearchesReplaceByIDWithAWholeSearchSendsItWithoutReadingTheList(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"PUT " + logSearchesTestCheckoutPath: {http.StatusOK, logSearchesTestCheckout},
	})

	_, executeError := runApplicationLogSearches(t, stub, "", "replace", testApplicationID, logSearchesTestCheckoutID,
		"--name", "Checkout errors", "--level", "error", "--last", "6h")

	if executeError != nil {
		t.Fatalf("log-searches replace error = %v", executeError)
	}
	if reads := stub.sent(http.MethodGet, logSearchesTestCollection); len(reads) != 0 {
		t.Errorf("the list was read: %+v", reads)
	}
	requests := stub.sent(http.MethodPut, logSearchesTestCheckoutPath)
	expectedQuery := map[string]any{"text": "", "filters": []any{
		map[string]any{"field": "level", "value": "error", "not": false},
	}, "span": map[string]any{"from": "now-6h", "to": "now"}}
	if len(requests) != 1 || !reflect.DeepEqual(requests[0].body["query"], expectedQuery) {
		t.Errorf("replace requests = %+v, want one with %v", requests, expectedQuery)
	}
}

func TestApplicationLogSearchesReplaceAnUnreadableSearchNeedsANewQuery(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + logSearchesTestCollection: {http.StatusOK, logSearchesTestListPayload},
	})

	_, executeError := runApplicationLogSearches(t, stub, "", "replace", testApplicationID, logSearchesTestUnreadableID,
		"--name", "Older incident")

	if exitCodeFor(executeError) != exitUsage || !strings.Contains(executeError.Error(), "cannot be read") {
		t.Errorf("error = %v (exit %d), want a usage refusal that says the query cannot be read", executeError, exitCodeFor(executeError))
	}
	if requests := stub.sent(http.MethodPut, logSearchesTestCollection+"/"+logSearchesTestUnreadableID); len(requests) != 0 {
		t.Errorf("sent %+v", requests)
	}
}

func TestApplicationLogSearchesReplaceWithNothingToReplaceIsAUsageError(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{})

	_, executeError := runApplicationLogSearches(t, stub, "", "replace", testApplicationID, logSearchesTestCheckoutID)

	if exitCodeFor(executeError) != exitUsage {
		t.Errorf("exit code = %d (%v), want %d", exitCodeFor(executeError), executeError, exitUsage)
	}
}

func TestApplicationLogSearchesReplaceOfASearchThatIsGoneExitsNotFoundWithThePlatformsSentence(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"PUT " + logSearchesTestCheckoutPath: {http.StatusNotFound,
			`{"detail": "This saved search does not exist. It may have been removed.", "error_code": "saved_log_search_not_found"}`},
	})

	_, executeError := runApplicationLogSearches(t, stub, "", "replace", testApplicationID, logSearchesTestCheckoutID,
		"--name", "Checkout errors", "--text", "checkout")

	if exitCodeFor(executeError) != exitNotFound {
		t.Errorf("exit code = %d (%v), want %d", exitCodeFor(executeError), executeError, exitNotFound)
	}
	if executeError == nil || !strings.Contains(executeError.Error(), "This saved search does not exist. It may have been removed.") {
		t.Errorf("error = %v, want the platform's sentence", executeError)
	}
}

func TestApplicationLogSearchesRemoveByNameAsksThenRemoves(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + logSearchesTestCollection:      {http.StatusOK, logSearchesTestListPayload},
		"DELETE " + logSearchesTestCheckoutPath: {http.StatusNoContent, ""},
	})

	output, executeError := runApplicationLogSearches(t, stub, "y\n", "remove", testApplicationID, "Checkout errors")

	if executeError != nil {
		t.Fatalf("log-searches remove error = %v", executeError)
	}
	if requests := stub.sent(http.MethodDelete, logSearchesTestCheckoutPath); len(requests) != 1 {
		t.Errorf("remove requests = %+v, want one", requests)
	}
	want := "Remove the Logs search \"Checkout errors\" from application " + testApplicationID +
		" for everyone? It cannot be undone. [y/N]: Removed the Logs search \"Checkout errors\" from " + testApplicationID + ".\n"
	if output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
}

func TestApplicationLogSearchesRemoveDeclinedRemovesNothingAndExitsCancelled(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + logSearchesTestCollection: {http.StatusOK, logSearchesTestListPayload},
	})

	_, executeError := runApplicationLogSearches(t, stub, "n\n", "remove", testApplicationID, logSearchesTestCheckoutID)

	if exitCodeFor(executeError) != exitCancelled {
		t.Errorf("exit code = %d (%v), want %d", exitCodeFor(executeError), executeError, exitCancelled)
	}
	if requests := stub.sent(http.MethodDelete, logSearchesTestCheckoutPath); len(requests) != 0 {
		t.Errorf("removed after a no: %+v", requests)
	}
}

func TestApplicationLogSearchesRemoveByIDWithYesRemovesWithoutReadingTheList(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"DELETE " + logSearchesTestCheckoutPath: {http.StatusNoContent, ""},
	})

	output, executeError := runApplicationLogSearches(t, stub, "", "remove", testApplicationID, logSearchesTestCheckoutID, "--yes")

	if executeError != nil {
		t.Fatalf("log-searches remove error = %v", executeError)
	}
	if reads := stub.sent(http.MethodGet, logSearchesTestCollection); len(reads) != 0 {
		t.Errorf("the list was read: %+v", reads)
	}
	if want := "Removed the Logs search " + logSearchesTestCheckoutID + " from " + testApplicationID + ".\n"; output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
}

func TestApplicationLogSearchesRemoveOfANameNotSavedExitsNotFound(t *testing.T) {
	stub := newAlertsStub(t, map[string]alertsStubAnswer{
		"GET " + logSearchesTestCollection: {http.StatusOK, logSearchesTestListPayload},
	})

	_, executeError := runApplicationLogSearches(t, stub, "", "remove", testApplicationID, "Slow requests", "--yes")

	if exitCodeFor(executeError) != exitNotFound {
		t.Errorf("exit code = %d (%v), want %d", exitCodeFor(executeError), executeError, exitNotFound)
	}
}

func TestDescribeSavedLogSearchQueryQuotesAFilterThatIsNotOnePlainWord(t *testing.T) {
	// The platform keeps a filter's value whole: a space, a comma, a line break and a terminal control sequence are
	// all stored. Each is printed quoted, so it stays on its row and reaches the terminal as text.
	query := client.SavedLogSearchQuery{Filters: []client.SavedLogSearchFilter{
		{Field: "level", Value: "error"},
		{Field: "message", Value: "payment failed"},
		{Field: "route", Value: "a,b"},
		{Field: "pod", Value: "shop\n\x1b[2Jgone", Not: true},
		{Field: "request id", Value: "42"},
	}}
	want := `level is error, message is "payment failed", route is "a,b", copy is not "shop\n\x1b[2Jgone", ` +
		`"request id" is 42, the time range on the page`

	if got := describeSavedLogSearchQuery(query); got != want {
		t.Errorf("describeSavedLogSearchQuery() = %q, want %q", got, want)
	}
}

func TestDescribeSavedLogSearchSpanSaysTheRangeInWords(t *testing.T) {
	cases := []struct {
		span *client.SavedLogSearchSpan
		want string
	}{
		{nil, "the time range on the page"},
		{&client.SavedLogSearchSpan{From: "now-15m", To: "now"}, "the last 15 minutes"},
		{&client.SavedLogSearchSpan{From: "now-1d", To: "now"}, "the last day"},
		{&client.SavedLogSearchSpan{From: "now-2h", To: "now-1h"}, "from 2 hours ago to 1 hour ago"},
		{&client.SavedLogSearchSpan{From: "2026-11-14 14:00", To: "2026-11-14T15:00:00Z"}, "from 2026-11-14 14:00 UTC to 2026-11-14T15:00:00Z"},
		{&client.SavedLogSearchSpan{From: "2026-11-14 14:00+01:00", To: "now"}, "from 2026-11-14 14:00+01:00 to now"},
	}
	for _, testCase := range cases {
		if got := describeSavedLogSearchSpan(testCase.span); got != testCase.want {
			t.Errorf("describeSavedLogSearchSpan(%+v) = %q, want %q", testCase.span, got, testCase.want)
		}
	}
}

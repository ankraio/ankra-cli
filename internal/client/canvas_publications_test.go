package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestPublishCanvasPublication_CreatedAndUpdated(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		status  int
		created bool
	}{
		{"new page", http.StatusCreated, true},
		{"updated page", http.StatusOK, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != "/api/v1/org/canvas-publications" {
					t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
				}
				if request.Header.Get("Authorization") != "Bearer "+testToken {
					t.Errorf("missing bearer token")
				}
				var payload CanvasPublicationRequest
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if payload.ArtifactID != "artifact" || payload.Language != "html" || payload.TTLHours == nil || *payload.TTLHours != 24 {
					t.Errorf("payload = %+v", payload)
				}
				jsonResponse(t, responseWriter, testCase.status, CanvasPublication{ID: "id-1", URL: "https://pages.example/o/s"})
			})
			ttlHours := 24
			result, err := testClient.PublishCanvasPublication(context.Background(), CanvasPublicationRequest{
				ConversationID: "conversation", ArtifactID: "artifact", Language: "html", Content: "<p>x</p>", TTLHours: &ttlHours,
			})
			if err != nil {
				t.Fatalf("publish: %v", err)
			}
			if result.Created != testCase.created || result.URL != "https://pages.example/o/s" {
				t.Errorf("result = %+v", result)
			}
		})
	}
}

func TestPublishCanvasPublication_Refusals(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		status   int
		body     string
		wantCode string
	}{
		{"disabled detail string", http.StatusServiceUnavailable, `{"detail":"canvas_pages_disabled"}`, "canvas_pages_disabled"},
		{"language code field", http.StatusBadRequest, `{"code":"unsupported_language","detail":"javascript is not supported"}`, "unsupported_language"},
		{"problem json taken down", http.StatusForbidden, `{"type":"about:blank","title":"Forbidden","status":403,"detail":"publication_taken_down","reason":"publication_taken_down","code":"publication_taken_down"}`, "publication_taken_down"},
		{"problem json reason only", http.StatusForbidden, `{"reason":"publication_owned_by_another_member"}`, "publication_owned_by_another_member"},
		{"limit nested detail", http.StatusConflict, `{"detail":{"code":"publication_limit_reached","message":"limit"}}`, "publication_limit_reached"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
				responseWriter.WriteHeader(testCase.status)
				_, _ = responseWriter.Write([]byte(testCase.body))
			})
			_, err := testClient.PublishCanvasPublication(context.Background(), CanvasPublicationRequest{})
			var pagesError *CanvasPagesError
			if !errors.As(err, &pagesError) || pagesError.Code != testCase.wantCode || pagesError.StatusCode != testCase.status {
				t.Fatalf("err = %v, want code %s", err, testCase.wantCode)
			}
			if !strings.Contains(err.Error(), testCase.wantCode) {
				t.Errorf("message %q lacks code", err.Error())
			}
		})
	}
}

func TestCanvasPublicationStatusMapping(t *testing.T) {
	testCases := []struct {
		status int
		check  func(error) bool
	}{
		{http.StatusUnauthorized, func(err error) bool { return errors.Is(err, ErrUnauthorized) }},
		{http.StatusNotFound, func(err error) bool { return errors.Is(err, ErrCanvasPublicationNotFound) }},
		{http.StatusForbidden, func(err error) bool {
			var denied *PermissionDeniedError
			return errors.As(err, &denied)
		}},
		{http.StatusInternalServerError, func(err error) bool {
			var unexpected *UnexpectedResponseError
			return errors.As(err, &unexpected) && unexpected.StatusCode == 500
		}},
		{http.StatusBadRequest, func(err error) bool {
			var unexpected *UnexpectedResponseError
			return errors.As(err, &unexpected)
		}},
	}
	for _, testCase := range testCases {
		testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
			responseWriter.WriteHeader(testCase.status)
		})
		err := testClient.UnpublishCanvasPublication(context.Background(), "id")
		if !testCase.check(err) {
			t.Errorf("status %d: err = %v", testCase.status, err)
		}
	}
}

func TestListAndUnpublishCanvasPublications(t *testing.T) {
	testClient := newTestClient(t, func(responseWriter http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			if request.URL.Query().Get("cursor") != "next" || request.URL.Query().Get("limit") != "5" {
				t.Errorf("query = %s", request.URL.RawQuery)
			}
			jsonResponse(t, responseWriter, http.StatusOK, CanvasPublicationsListResult{
				Items: []CanvasPublication{{ID: "id-1"}}, NextCursor: "more",
			})
		case http.MethodDelete:
			if request.URL.Path != "/api/v1/org/canvas-publications/id%2F1" && request.URL.Path != "/api/v1/org/canvas-publications/id/1" {
				t.Errorf("path = %s", request.URL.Path)
			}
			responseWriter.WriteHeader(http.StatusNoContent)
		}
	})
	list, err := testClient.ListCanvasPublications(context.Background(), "next", 5)
	if err != nil || len(list.Items) != 1 || list.NextCursor != "more" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := testClient.UnpublishCanvasPublication(context.Background(), "id/1"); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
}

func TestCanvasPagesErrorMessage(t *testing.T) {
	if got := (&CanvasPagesError{StatusCode: 400}).Error(); !strings.Contains(got, "400") {
		t.Errorf("got %q", got)
	}
	if got := (&CanvasPagesError{Detail: "d"}).Error(); got != "d" {
		t.Errorf("got %q", got)
	}
}

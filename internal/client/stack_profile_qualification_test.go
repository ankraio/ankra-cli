package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const qualificationTestID = "b7cc7595-195d-4fc4-84ae-94873999ab22"

func TestRuntimeQualificationRoutes(t *testing.T) {
	for _, operation := range []string{"start", "get", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			connection := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				path, method, body := profileRuntimeQualificationPath, http.MethodPost, `{"request_id":"`+qualificationTestID+`"}`
				if operation != "start" {
					path += "/" + qualificationTestID
				}
				if operation == "get" {
					method, body = http.MethodGet, ""
				}
				if operation == "cancel" {
					path += "/cancel"
					body = "{}"
				}
				if r.URL.RequestURI() != path || r.Method != method || r.Header.Get("Authorization") == "" {
					t.Errorf("wrong request: %s %s", r.Method, r.URL)
				}
				if body != "" {
					var actual json.RawMessage
					if decodeError := json.NewDecoder(r.Body).Decode(&actual); decodeError != nil || string(actual) != body {
						t.Errorf("wrong body: %s (%v)", actual, decodeError)
					}
				}
				w.WriteHeader(http.StatusAccepted)
				_, _ = fmt.Fprintf(w, `{"operation_id":%q,"request_id":%q,"state":"running","future_observation":{"known":false}}`, qualificationTestID, qualificationTestID)
			})
			var result *ProfileRuntimeQualification
			var requestError error
			switch operation {
			case "start":
				result, requestError = connection.StartProfileRuntimeQualification(context.Background(), qualificationTestID)
			case "get":
				result, requestError = connection.GetProfileRuntimeQualification(context.Background(), qualificationTestID)
			case "cancel":
				result, requestError = connection.CancelProfileRuntimeQualification(context.Background(), qualificationTestID)
			}
			if requestError != nil || calls != 1 || result.State != "running" || !strings.Contains(string(result.RawJSON()), `"future_observation"`) {
				t.Fatalf("lost response: %+v error=%v calls=%d", result, requestError, calls)
			}
		})
	}
}

func TestRuntimeQualificationWritesDoNotRetry(t *testing.T) {
	for _, operation := range []string{"start", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			connection := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				jsonResponse(t, w, http.StatusServiceUnavailable, map[string]string{"detail": "private detail", "code": "qualification_unavailable"})
			})
			connection.HTTP.Transport = &retryTransport{base: connection.HTTP.Transport}
			var requestError error
			if operation == "start" {
				_, requestError = connection.StartProfileRuntimeQualification(context.Background(), qualificationTestID)
			} else {
				_, requestError = connection.CancelProfileRuntimeQualification(context.Background(), qualificationTestID)
			}
			var responseError *UnexpectedResponseError
			if calls != 1 || !errors.As(requestError, &responseError) || responseError.StatusCode != 503 || strings.Contains(requestError.Error(), "private detail") {
				t.Fatalf("unsafe refusal: %v calls=%d", requestError, calls)
			}
		})
	}
}

func TestRuntimeQualificationRejectsEmptyResponsesAndHonoursCancellation(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"operation_id":"x","state":"running"}`, `[]`, `{"broken":`} {
		connection := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) })
		if result, requestError := connection.GetProfileRuntimeQualification(context.Background(), qualificationTestID); requestError == nil || result != nil {
			t.Fatalf("accepted malformed response: %s", body)
		}
	}
	connection := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled request reached server") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, requestError := connection.StartProfileRuntimeQualification(ctx, qualificationTestID); !errors.Is(requestError, context.Canceled) {
		t.Fatalf("lost cancellation: %v", requestError)
	}
}

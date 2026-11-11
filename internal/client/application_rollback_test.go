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

// The rollback routes (ankra-tv2pwo). A path typo fails as a 404 against a
// real platform and passes every test that only checks the command wiring, so
// the assertions are on the request itself.
func TestApplicationRollbackReadsNameTheCluster(t *testing.T) {
	for name, testCase := range map[string]struct {
		target string
		call   func(testClient *Client) (json.RawMessage, error)
	}{
		"targets": {"/api/v1/org/applications/app-1/rollback-targets?cluster_id=cluster-1",
			func(testClient *Client) (json.RawMessage, error) {
				return testClient.GetApplicationRollbackTargets(context.Background(), "app-1", "cluster-1")
			}},
		"history": {"/api/v1/org/applications/app-1/rollbacks?cluster_id=cluster-1",
			func(testClient *Client) (json.RawMessage, error) {
				return testClient.ListApplicationRollbacks(context.Background(), "app-1", "cluster-1")
			}},
	} {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.RequestURI() != testCase.target {
					t.Errorf("request = %s %s, want GET %s", request.Method, request.URL.RequestURI(), testCase.target)
				}
				if request.Header.Get("Authorization") != "Bearer "+testToken {
					t.Errorf("the request carries no bearer token")
				}
				_, _ = fmt.Fprint(writer, `{"kept":true}`)
			})
			payload, callError := testCase.call(testClient)
			if callError != nil || string(payload) != `{"kept":true}` {
				t.Fatalf("payload = %s, error = %v", payload, callError)
			}
		})
	}
}

func TestStartApplicationRollbackSendsTheClusterAndTheTag(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.RequestURI() != "/api/v1/org/applications/app-1/rollbacks" {
			t.Errorf("request = %s %s", request.Method, request.URL.RequestURI())
		}
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil {
			t.Fatalf("decode request: %v", decodeError)
		}
		if len(body) != 2 || body["cluster_id"] != "cluster-1" || body["image_tag"] != "sha-1a2b3c4" {
			t.Errorf("body = %v", body)
		}
		_, _ = fmt.Fprint(writer, `{"id":"rollback-1","status":"pending"}`)
	})
	payload, callError := testClient.StartApplicationRollback(context.Background(), "app-1", "cluster-1", "sha-1a2b3c4")
	if callError != nil || !strings.Contains(string(payload), "rollback-1") {
		t.Fatalf("payload = %s, error = %v", payload, callError)
	}
}

func TestStartApplicationRollbackSurfacesThePlatformRefusal(t *testing.T) {
	for name, testCase := range map[string]struct {
		status int
		body   string
		check  func(t *testing.T, callError error)
	}{
		"a missing token": {http.StatusUnauthorized, `{"detail":"Invalid token"}`, func(t *testing.T, callError error) {
			if !errors.Is(callError, ErrUnauthorized) {
				t.Errorf("error = %v, want ErrUnauthorized", callError)
			}
		}},
		"a role without applications.deploy": {http.StatusForbidden, `{"detail":"permission_denied","permission":"applications.deploy"}`,
			func(t *testing.T, callError error) {
				var denied *PermissionDeniedError
				if !errors.As(callError, &denied) || denied.Permission != "applications.deploy" {
					t.Errorf("error = %v, want the permission denial", callError)
				}
			}},
		"a deploy still running": {http.StatusConflict, `{"detail":"A deploy of this application to that cluster is still running."}`,
			func(t *testing.T, callError error) {
				var unexpected *UnexpectedResponseError
				if !errors.As(callError, &unexpected) || unexpected.StatusCode != http.StatusConflict ||
					!strings.Contains(callError.Error(), "still running") {
					t.Errorf("error = %v, want the 409 with the platform's sentence", callError)
				}
			}},
	} {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(testCase.status)
				_, _ = fmt.Fprint(writer, testCase.body)
			})
			_, callError := testClient.StartApplicationRollback(context.Background(), "app-1", "cluster-1", "sha-1a2b3c4")
			if callError == nil {
				t.Fatal("the refusal was read as success")
			}
			testCase.check(t, callError)
		})
	}
}

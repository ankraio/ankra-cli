package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordedDeployRequest is one request a fake deploy API received.
type recordedDeployRequest struct {
	method string
	path   string
	query  string
	body   string
	token  string
}

func newDeployServer(t *testing.T, status int, answer string) (*Client, *[]recordedDeployRequest) {
	t.Helper()
	requests := []recordedDeployRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests = append(requests, recordedDeployRequest{
			method: request.Method,
			path:   request.URL.Path,
			query:  request.URL.RawQuery,
			body:   string(body),
			token:  request.Header.Get("Authorization"),
		})
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	return New("deploy-token", server.URL), &requests
}

func TestDeployClientAddressesTheOrgBearerRoutes(t *testing.T) {
	cases := []struct {
		name        string
		answer      string
		call        func(apiClient *Client) error
		wantMethod  string
		wantPath    string
		wantQuery   string
		wantRequest string
	}{
		{
			name:   "environments",
			answer: `{"environments":[{"id":"e1","name":"production"}]}`,
			call: func(apiClient *Client) error {
				_, callError := apiClient.ListEnvironments(context.Background())
				return callError
			},
			wantMethod: http.MethodGet, wantPath: "/api/v1/org/environments",
		},
		{
			name:   "join token with the platform's default lifetime",
			answer: `{"join_token":"secret","expires_at":"2026-09-30T12:00:00Z","environment":"production"}`,
			call: func(apiClient *Client) error {
				_, callError := apiClient.CreateHostJoinToken(context.Background(), "production", CreateHostJoinTokenRequest{})
				return callError
			},
			wantMethod: http.MethodPost, wantPath: "/api/v1/org/environments/production/host-join-tokens", wantRequest: `{}`,
		},
		{
			name:   "join token with a lifetime",
			answer: `{"join_token":"secret","expires_at":"2026-09-30T12:00:00Z","environment":"production"}`,
			call: func(apiClient *Client) error {
				_, callError := apiClient.CreateHostJoinToken(context.Background(), "production", CreateHostJoinTokenRequest{TTLSeconds: 7200})
				return callError
			},
			wantMethod: http.MethodPost, wantPath: "/api/v1/org/environments/production/host-join-tokens", wantRequest: `{"ttl_seconds":7200}`,
		},
		{
			name:   "host targets in an environment",
			answer: `{"host_targets":[]}`,
			call: func(apiClient *Client) error {
				_, callError := apiClient.ListHostTargets(context.Background(), "production")
				return callError
			},
			wantMethod: http.MethodGet, wantPath: "/api/v1/org/host-targets", wantQuery: "environment=production",
		},
		{
			name:   "every host target",
			answer: `{"host_targets":[]}`,
			call: func(apiClient *Client) error {
				_, callError := apiClient.ListHostTargets(context.Background(), "")
				return callError
			},
			wantMethod: http.MethodGet, wantPath: "/api/v1/org/host-targets",
		},
		{
			name:   "one host target",
			answer: `{"id":"t1","name":"web-1"}`,
			call: func(apiClient *Client) error {
				_, callError := apiClient.GetHostTarget(context.Background(), "t1")
				return callError
			},
			wantMethod: http.MethodGet, wantPath: "/api/v1/org/host-targets/t1",
		},
		{
			name:   "revoke",
			answer: `{"id":"t1","name":"web-1","revoked_at":"2026-09-30T12:00:00Z"}`,
			call: func(apiClient *Client) error {
				_, callError := apiClient.RevokeHostTarget(context.Background(), "t1")
				return callError
			},
			wantMethod: http.MethodPost, wantPath: "/api/v1/org/host-targets/t1/revoke",
		},
		{
			name:   "deployments with every filter",
			answer: `{"deployments":[],"next_cursor":null}`,
			call: func(apiClient *Client) error {
				_, callError := apiClient.ListDeployments(context.Background(), ListDeploymentsOptions{
					Environment: "production", RepositoryID: "r1", Cursor: "c1", Limit: 5,
				})
				return callError
			},
			wantMethod: http.MethodGet, wantPath: "/api/v1/org/deployments",
			wantQuery: "cursor=c1&environment=production&limit=5&repository_id=r1",
		},
		{
			name:   "one deployment",
			answer: `{"id":"d1","state":"succeeded","targets":[]}`,
			call: func(apiClient *Client) error {
				_, callError := apiClient.GetDeployment(context.Background(), "d1")
				return callError
			},
			wantMethod: http.MethodGet, wantPath: "/api/v1/org/deployments/d1",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			apiClient, requests := newDeployServer(t, http.StatusOK, testCase.answer)
			if callError := testCase.call(apiClient); callError != nil {
				t.Fatalf("call failed: %v", callError)
			}
			if len(*requests) != 1 {
				t.Fatalf("one request expected, got %d", len(*requests))
			}
			received := (*requests)[0]
			if received.method != testCase.wantMethod || received.path != testCase.wantPath || received.query != testCase.wantQuery {
				t.Fatalf("request = %s %s?%s, want %s %s?%s", received.method, received.path, received.query,
					testCase.wantMethod, testCase.wantPath, testCase.wantQuery)
			}
			if testCase.wantRequest != "" && received.body != testCase.wantRequest {
				t.Fatalf("body = %s, want %s", received.body, testCase.wantRequest)
			}
			if received.token != "Bearer deploy-token" {
				t.Fatalf("Authorization = %q", received.token)
			}
		})
	}
}

func TestDeployClientDecodesTheContractShapes(t *testing.T) {
	apiClient, _ := newDeployServer(t, http.StatusOK, `{"id":"d1","environment":"production","release_name":"ai-portal",
		"artifact_repository":"harbor.example/p/repo","artifact_digest":"sha256:abc","state":"failed","target_count":2,
		"succeeded_count":1,"failed_count":1,"targets":[{"id":"j1","host_target_id":"t1","status":"rolled_back","attempt":1,
		"error_class":"health_failed","result":{"outcome":"rolled_back"}}]}`)
	deployment, getError := apiClient.GetDeployment(context.Background(), "d1")
	if getError != nil {
		t.Fatalf("GetDeployment: %v", getError)
	}
	if deployment.State != "failed" || deployment.TargetCount != 2 || len(deployment.Targets) != 1 {
		t.Fatalf("deployment decoded as %+v", deployment)
	}
	target := deployment.Targets[0]
	if target.Status != "rolled_back" || target.ErrorClass == nil || *target.ErrorClass != "health_failed" {
		t.Fatalf("target decoded as %+v", target)
	}
	encoded, _ := json.Marshal(target.Result)
	if string(encoded) != `{"outcome":"rolled_back"}` {
		t.Fatalf("result kept as %s", encoded)
	}
}

func TestDeployClientSurfacesNotFoundAndPermissionDenied(t *testing.T) {
	apiClient, _ := newDeployServer(t, http.StatusNotFound, `{"detail":"host target not found"}`)
	_, getError := apiClient.GetHostTarget(context.Background(), "missing")
	var unexpected *UnexpectedResponseError
	if !errors.As(getError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "host target not found" {
		t.Fatalf("404 surfaced as %#v", getError)
	}

	apiClient, _ = newDeployServer(t, http.StatusForbidden, `{"detail":"permission_denied","permission":"pipelines:manage"}`)
	_, revokeError := apiClient.RevokeHostTarget(context.Background(), "t1")
	var denied *PermissionDeniedError
	if !errors.As(revokeError, &denied) || denied.Permission != "pipelines:manage" {
		t.Fatalf("403 surfaced as %#v", revokeError)
	}
}

func TestJoinTokenErrorsNeverEchoTheToken(t *testing.T) {
	apiClient, _ := newDeployServer(t, http.StatusInternalServerError, `{"join_token":"leaked-secret","error":"boom"}`)
	_, createError := apiClient.CreateHostJoinToken(context.Background(), "production", CreateHostJoinTokenRequest{})
	if createError == nil {
		t.Fatal("a 500 must fail")
	}
	if strings.Contains(createError.Error(), "leaked-secret") {
		t.Fatalf("the error echoes the token: %v", createError)
	}
}

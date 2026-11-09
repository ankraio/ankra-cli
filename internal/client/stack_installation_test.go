package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stackInstallationCall is one of the two verbs, so every test below holds
// both to the same contract.
type stackInstallationCall struct {
	action string
	call   func(apiClient *Client, clusterID, stackName string) (*StackInstallationResult, error)
}

var stackInstallationCalls = []stackInstallationCall{
	{action: "uninstall", call: func(apiClient *Client, clusterID, stackName string) (*StackInstallationResult, error) {
		return apiClient.UninstallStack(context.Background(), clusterID, stackName)
	}},
	{action: "install", call: func(apiClient *Client, clusterID, stackName string) (*StackInstallationResult, error) {
		return apiClient.InstallStack(context.Background(), clusterID, stackName)
	}},
}

// TestStackInstallationSendsABodilessBearerPostToTheVerbsRoute pins the wire:
// the /api/v1 bearer twin of each verb (ankraio/cluster#6021 openapi.json),
// the stack name escaped as one path segment, the token, and no body.
func TestStackInstallationSendsABodilessBearerPostToTheVerbsRoute(t *testing.T) {
	for _, verb := range stackInstallationCalls {
		t.Run(verb.action, func(t *testing.T) {
			installed := verb.action == "install"
			responseBody := `{"stack_name":"my stack","installed":` + map[bool]string{true: "true", false: "false"}[installed] +
				`,"resource_ids":["addon-1","manifest-2"],"future_member":"kept"}`
			var method, escapedPath, authorization string
			var requestBody []byte
			handler := func(writer http.ResponseWriter, request *http.Request) {
				method, escapedPath = request.Method, request.URL.EscapedPath()
				authorization = request.Header.Get("Authorization")
				requestBody, _ = io.ReadAll(request.Body)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(responseBody))
			}
			result, callError := verb.call(newTestClient(t, handler), "cluster-1", "my stack")
			if callError != nil {
				t.Fatalf("%s: %v", verb.action, callError)
			}
			wantPath := "/api/v1/org/clusters/imported/cluster-1/stacks/my%20stack/" + verb.action
			if method != http.MethodPost || escapedPath != wantPath {
				t.Fatalf("request = %s %s, want POST %s", method, escapedPath, wantPath)
			}
			if authorization != "Bearer "+testToken {
				t.Fatalf("Authorization = %q, want the bearer token", authorization)
			}
			if len(requestBody) != 0 {
				t.Fatalf("the route takes no body, sent %q", requestBody)
			}
			if result.StackName != "my stack" || result.Installed != installed ||
				strings.Join(result.ResourceIDs, ",") != "addon-1,manifest-2" {
				t.Fatalf("result = %+v", result)
			}
			if string(result.RawJSON()) != responseBody {
				t.Fatalf("the platform's document must be kept for -o json, got %s", result.RawJSON())
			}
		})
	}
}

// TestStackInstallationRefusalsKeepTheSentenceAndTheCode pins every 409 the
// routes document: the platform's sentence is the whole message, verbatim, and
// the error_code (or its absence) survives for the caller to classify.
func TestStackInstallationRefusalsKeepTheSentenceAndTheCode(t *testing.T) {
	refusals := []struct {
		name   string
		body   string
		detail string
		code   string
	}{
		{
			name:   "open draft",
			body:   `{"detail":"Stack 'web' has an open draft. Deploy or discard the draft, then uninstall the stack.","error_code":"stack_installation_refused"}`,
			detail: "Stack 'web' has an open draft. Deploy or discard the draft, then uninstall the stack.",
			code:   StackInstallationRefusedCode,
		},
		{
			name:   "cluster offline",
			body:   `{"detail":"Cluster has to be online to uninstall a stack.","error_code":"cluster_unavailable"}`,
			detail: "Cluster has to be online to uninstall a stack.",
			code:   StackInstallationClusterUnavailableCode,
		},
		{
			name:   "uninstall still removing",
			body:   `{"detail":"Stack 'web' is still being uninstalled: wait until its members are off the cluster.","error_code":"STACK_UNINSTALL_IN_PROGRESS"}`,
			detail: "Stack 'web' is still being uninstalled: wait until its members are off the cluster.",
			code:   StackUninstallInProgressCode,
		},
		{
			name:   "service-owned, no code",
			body:   `{"detail":"Stack 'web' belongs to the managed service 'db'. Retire the service instead."}`,
			detail: "Stack 'web' belongs to the managed service 'db'. Retire the service instead.",
		},
	}
	for _, verb := range stackInstallationCalls {
		for _, refusal := range refusals {
			t.Run(verb.action+"/"+refusal.name, func(t *testing.T) {
				handler := func(writer http.ResponseWriter, _ *http.Request) {
					writer.Header().Set("Content-Type", "application/json")
					writer.WriteHeader(http.StatusConflict)
					_, _ = writer.Write([]byte(refusal.body))
				}
				_, callError := verb.call(newTestClient(t, handler), "cluster-1", "web")
				var refused *StackInstallationRefusedError
				if !errors.As(callError, &refused) {
					t.Fatalf("a 409 must be a refusal, got %T: %v", callError, callError)
				}
				if refused.Detail != refusal.detail || refused.ErrorCode != refusal.code || callError.Error() != refusal.detail {
					t.Fatalf("refusal = %+v (message %q), want detail %q and code %q", refused, callError.Error(), refusal.detail, refusal.code)
				}
			})
		}
	}
}

// TestStackInstallationKeepsTheOtherAnswersApart pins what the commands
// classify beside a refusal: the RBAC 403 names the permission, a 404 keeps
// its sentence (so a missing stack can be told from a platform without the
// route), a 401 is the login error, a billing refusal keeps its type even on a
// 409, and a 409 with no sentence is not presented as a refusal.
func TestStackInstallationKeepsTheOtherAnswersApart(t *testing.T) {
	answer := func(status int, body string) http.HandlerFunc {
		return func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(status)
			_, _ = writer.Write([]byte(body))
		}
	}
	for _, verb := range stackInstallationCalls {
		t.Run(verb.action, func(t *testing.T) {
			permission := map[string]string{"uninstall": "stacks.delete", "install": "stacks.deploy"}[verb.action]
			_, deniedError := verb.call(newTestClient(t, answer(http.StatusForbidden,
				`{"detail":"permission_denied","permission":"`+permission+`","scope_type":"cluster","scope_id":"cluster-1"}`)), "cluster-1", "web")
			var denied *PermissionDeniedError
			if !errors.As(deniedError, &denied) || denied.Permission != permission {
				t.Fatalf("a 403 must be a permission denial naming %s, got %T: %v", permission, deniedError, deniedError)
			}

			for _, detail := range []string{"Stack 'web' could not be found in this cluster.", "Cluster not found", RouterNotFoundDetail} {
				_, missingError := verb.call(newTestClient(t, answer(http.StatusNotFound, `{"detail":"`+detail+`"}`)), "cluster-1", "web")
				var unexpected *UnexpectedResponseError
				if !errors.As(missingError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != detail {
					t.Fatalf("a 404 must keep its sentence %q, got %T: %v", detail, missingError, missingError)
				}
			}

			_, loginError := verb.call(newTestClient(t, answer(http.StatusUnauthorized, `{"detail":"Not authenticated"}`)), "cluster-1", "web")
			if !errors.Is(loginError, ErrUnauthorized) {
				t.Fatalf("a 401 must be ErrUnauthorized, got %v", loginError)
			}

			_, billingError := verb.call(newTestClient(t, answer(http.StatusConflict,
				`{"detail":"Your prepaid credit is used up.","error_code":"BILLING_RESTRICTED"}`)), "cluster-1", "web")
			var restricted *BillingRestrictedError
			if !errors.As(billingError, &restricted) {
				t.Fatalf("a BILLING_RESTRICTED body must keep its type, got %T: %v", billingError, billingError)
			}

			_, bareError := verb.call(newTestClient(t, answer(http.StatusConflict, `<html>conflict</html>`)), "cluster-1", "web")
			var refused *StackInstallationRefusedError
			var unexpected *UnexpectedResponseError
			if errors.As(bareError, &refused) || !errors.As(bareError, &unexpected) || unexpected.StatusCode != http.StatusConflict {
				t.Fatalf("a 409 without the platform's sentence is not its refusal, got %T: %v", bareError, bareError)
			}
		})
	}
}

// TestStackInstallationTimeoutIsAnUnknownOutcome pins the one transport error
// that may hide a write that landed: a timeout says the outcome is unknown and
// names the read that settles it. A refused connection never reached the
// platform, so it must not say the stack may have changed.
func TestStackInstallationTimeoutIsAnUnknownOutcome(t *testing.T) {
	for _, verb := range stackInstallationCalls {
		t.Run(verb.action, func(t *testing.T) {
			server := slowHeaderServer(t, time.Second, `{}`)
			apiClient := New(testToken, server.URL)
			// Bound the call far below the server's delay so the timeout is
			// the deterministic outcome.
			apiClient.HTTP = &http.Client{Timeout: 50 * time.Millisecond}
			_, timeoutError := verb.call(apiClient, "cluster-1", "web")
			var unknown *WriteOutcomeUnknownError
			if !errors.As(timeoutError, &unknown) {
				t.Fatalf("a timeout = %T (%v), want *WriteOutcomeUnknownError", timeoutError, timeoutError)
			}
			for _, want := range []string{verb.action + " stack 'web'", "may have completed", "ankra cluster stacks list web --cluster cluster-1"} {
				if !strings.Contains(timeoutError.Error(), want) {
					t.Errorf("the timeout does not say %q:\n%s", want, timeoutError.Error())
				}
			}

			closed := httptest.NewServer(http.NotFoundHandler())
			closedURL := closed.URL
			closed.Close()
			_, refusedError := verb.call(New(testToken, closedURL), "cluster-1", "web")
			if refusedError == nil || errors.As(refusedError, &unknown) {
				t.Fatalf("a refused connection = %v, want a plain failure, not an unknown outcome", refusedError)
			}
		})
	}
}

package client

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestAppendWaitQuery(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wait     bool
		want     string
	}{
		{
			name:     "append false",
			endpoint: "https://platform.example/api/v1/clusters/import",
			wait:     false,
			want:     "https://platform.example/api/v1/clusters/import?wait=false",
		},
		{
			name:     "append true",
			endpoint: "https://platform.example/api/v1/clusters/hetzner/id/node-groups",
			wait:     true,
			want:     "https://platform.example/api/v1/clusters/hetzner/id/node-groups?wait=true",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := appendWaitQuery(testCase.endpoint, testCase.wait)
			if got != testCase.want {
				t.Fatalf("appendWaitQuery() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// RBAC 403s on write endpoints must surface as *PermissionDeniedError (exit
// code 7) in both the wait and no-wait paths; legacy 403 bodies keep the
// UnexpectedResponseError handling.
func TestParseAsyncWriteResponseClassifiesRBACDenial(t *testing.T) {
	rbacBody := []byte(`{"detail":"permission_denied","permission":"stacks.deploy"}`)
	for _, wait := range []bool{true, false} {
		response := &http.Response{StatusCode: http.StatusForbidden}
		_, err := parseAsyncWriteResponse(response, rbacBody, wait, nil)
		var denied *PermissionDeniedError
		if !errors.As(err, &denied) {
			t.Fatalf("wait=%v: expected *PermissionDeniedError, got %T: %v", wait, err, err)
		}
		if denied.Permission != "stacks.deploy" {
			t.Errorf("wait=%v: permission = %q, want stacks.deploy", wait, denied.Permission)
		}
	}

	legacy := &http.Response{StatusCode: http.StatusForbidden}
	_, err := parseAsyncWriteResponse(legacy, []byte(`{"detail":"sandbox"}`), true, nil)
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusForbidden {
		t.Fatalf("legacy 403 should stay *UnexpectedResponseError, got %T: %v", err, err)
	}
}

// A refusal that lands late is answered as an error on the wait=true route:
// the --wait path of a node-group write reports the provider's reason from
// the response, so it never needs to poll the node group's plan.
func TestParseAsyncWriteResponseWaitCarriesTheProviderReason(t *testing.T) {
	body := []byte(`{"detail":"Server type 'cpx32' is not available in location 'fsn1'"}`)
	response := &http.Response{StatusCode: http.StatusConflict}
	_, err := parseAsyncWriteResponse(response, body, true, nil)
	if err == nil || !strings.Contains(err.Error(), "cpx32") || !strings.Contains(err.Error(), "fsn1") {
		t.Fatalf("error = %v, want the provider's reason verbatim", err)
	}
}

// The platform's own refusal of a write (a 409, or a 404 for something the
// write names) reaches every caller of the shared write path as its reason,
// with the status and the detail kept, whether or not the caller waits.
func TestParseAsyncWriteResponseRelaysThePlatformsRefusal(t *testing.T) {
	for _, statusCode := range []int{http.StatusConflict, http.StatusNotFound} {
		for _, wait := range []bool{false, true} {
			const reason = "Node group 'workers' not found in this cluster"
			response := &http.Response{StatusCode: statusCode}
			_, err := parseAsyncWriteResponse(response, []byte(`{"detail":"`+reason+`"}`), wait, nil)
			var unexpected *UnexpectedResponseError
			if !errors.As(err, &unexpected) || unexpected.StatusCode != statusCode || unexpected.Detail != reason ||
				err.Error() != reason {
				t.Errorf("status %d wait=%v: got %#v, want the platform's reason with its status", statusCode, wait, err)
			}
		}
	}
}

// The router's own 404 for a path the platform does not serve keeps its
// detail, so a caller can recognise it, and keeps the status in the message,
// since "Not Found" alone says nothing. A detail that is not a sentence (a
// validation list, an object) keeps the answer's existing handling.
func TestParseAsyncWriteResponseKeepsTheRoutersNotFoundAndOtherShapes(t *testing.T) {
	_, err := parseAsyncWriteResponse(&http.Response{StatusCode: http.StatusNotFound}, []byte(`{"detail":"Not Found"}`), false, nil)
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.Detail != "Not Found" || err.Error() != "request failed: status 404: Not Found" {
		t.Errorf("router 404: got %#v", err)
	}

	_, err = parseAsyncWriteResponse(&http.Response{StatusCode: http.StatusConflict}, []byte(`{"detail":{"code":"busy"}}`), true, nil)
	if !errors.As(err, &unexpected) || unexpected.Detail != "" || !strings.Contains(err.Error(), "status 409") {
		t.Errorf("object detail: got %#v, want the existing status message", err)
	}

	_, err = parseAsyncWriteResponse(&http.Response{StatusCode: http.StatusNotFound}, []byte("404 page not found"), true, nil)
	if !errors.As(err, &unexpected) || unexpected.Detail != "" || !strings.Contains(err.Error(), "status 404") {
		t.Errorf("bare 404: got %#v, want the existing status message", err)
	}
}

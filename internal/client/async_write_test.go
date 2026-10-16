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

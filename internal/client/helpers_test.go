package client

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestGetJSON(t *testing.T) {
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		wantErr        bool
		validateResult func(t *testing.T, target interface{})
	}{
		{
			name: "200 with valid JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusOK, map[string]string{"key": "value"})
			},
			wantErr: false,
			validateResult: func(t *testing.T, target interface{}) {
				m, ok := target.(*map[string]string)
				if !ok || m == nil {
					t.Fatal("target should be *map[string]string")
				}
				if (*m)["key"] != "value" {
					t.Errorf("got %v, want value", (*m)["key"])
				}
			},
		},
		{
			name: "401 unauthorized",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantErr:        true,
			validateResult: nil,
		},
		{
			name: "non-200 status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantErr:        true,
			validateResult: nil,
		},
		{
			name: "malformed JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("not valid json"))
			},
			wantErr:        true,
			validateResult: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			url := testClient.BaseURL + "/api/test"
			var target map[string]string
			err := testClient.getJSON(url, &target)
			if (err != nil) != tt.wantErr {
				t.Errorf("getJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.validateResult != nil {
				tt.validateResult(t, &target)
			}
		})
	}
}

func TestPermissionDeniedFromResponse(t *testing.T) {
	rbacBody := []byte(`{"detail":"permission_denied","permission":"clusters.write","scope_type":"organisation"}`)

	if got := PermissionDeniedFromResponse(http.StatusForbidden, rbacBody); got == nil {
		t.Fatal("expected a PermissionDeniedError for the RBAC 403 body")
	} else if got.Permission != "clusters.write" {
		t.Errorf("permission = %q, want clusters.write", got.Permission)
	}

	// Only 403s qualify, and only with the permission_denied discriminator:
	// legacy 403 bodies must keep their existing handling.
	if got := PermissionDeniedFromResponse(http.StatusNotFound, rbacBody); got != nil {
		t.Errorf("non-403 must not classify, got %v", got)
	}
	for _, body := range []string{`{"detail":"sandbox mode is enabled"}`, `not json`, ``} {
		if got := PermissionDeniedFromResponse(http.StatusForbidden, []byte(body)); got != nil {
			t.Errorf("body %q must not classify, got %v", body, got)
		}
	}
}

func TestPermissionDeniedErrorMessages(t *testing.T) {
	named := &PermissionDeniedError{Permission: "sops.manage"}
	if msg := named.Error(); !strings.Contains(msg, `"sops.manage"`) || !strings.Contains(msg, "admin") {
		t.Errorf("named-permission message should cite the permission and the admin remedy, got %q", msg)
	}
	if msg := (&PermissionDeniedError{}).Error(); !strings.Contains(msg, "does not permit") {
		t.Errorf("permissionless message = %q", msg)
	}
}

func TestGetJSONClassifiesRBACDenial(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusForbidden, map[string]string{
			"detail":     "permission_denied",
			"permission": "clusters.read",
		})
	})
	var target map[string]string
	err := testClient.getJSON(testClient.BaseURL+"/api/test", &target)
	var denied *PermissionDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("expected *PermissionDeniedError, got %T: %v", err, err)
	}
	if denied.Permission != "clusters.read" {
		t.Errorf("permission = %q, want clusters.read", denied.Permission)
	}
}

func TestGetJSONLegacyForbiddenStaysUnexpected(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(t, w, http.StatusForbidden, map[string]string{"detail": "nope"})
	})
	var target map[string]string
	err := testClient.getJSON(testClient.BaseURL+"/api/test", &target)
	var unexpected *UnexpectedResponseError
	if !errors.As(err, &unexpected) || unexpected.StatusCode != http.StatusForbidden {
		t.Fatalf("legacy 403 should stay *UnexpectedResponseError, got %T: %v", err, err)
	}
}

func TestParseJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		target  interface{}
		wantErr bool
	}{
		{
			name:    "valid JSON",
			data:    []byte(`{"name":"test","value":42}`),
			target:  &struct{ Name string `json:"name"`; Value int `json:"value"` }{},
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			data:    []byte(`{invalid json}`),
			target:  &struct{}{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseJSON(tt.data, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

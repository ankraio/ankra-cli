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

func TestCloudDNSAccessUsesCurrentOrganisationAndEditableFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != cloudDNSZonesPath+"/zone-1/access" || request.Header.Get(orgOverrideHeader) != "org-1" || request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("request = %s %s with wrong auth or organisation", request.Method, request.URL.Path)
		}
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil || body["organisation_id"] != nil || body["mode"] != "organisation" || body["roles"] == nil {
			t.Errorf("body = %+v, %v", body, decodeError)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"access":{"mode":"organisation","organisation_id":"org-1","roles":[]}}`))
	}))
	defer server.Close()
	api := New(testToken, server.URL)
	api.SetOrganisationOverride("org-1")
	policy, updateError := api.UpdateCloudDNSAccess(context.Background(), "zone-1", CloudDNSAccessPolicy{Mode: "organisation", OrganisationID: "untrusted"})
	if updateError != nil || policy.OrganisationID != "org-1" {
		t.Fatalf("policy = %+v, %v", policy, updateError)
	}
}

func TestCloudDNSAccessPermissionDenialIsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"detail":"permission_denied","permission":"members.manage"}`))
	}))
	defer server.Close()
	api := New(testToken, server.URL)
	_, updateError := api.UpdateCloudDNSAccess(context.Background(), "zone-1", CloudDNSAccessPolicy{Mode: "organisation"})
	var denial *PermissionDeniedError
	if !errors.As(updateError, &denial) || denial.Permission != "members.manage" {
		t.Fatalf("denial = %v", updateError)
	}
}

func TestUpdateCloudDNSAccessSendsAProjectOnlyWhenChanging(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		bodies = append(bodies, string(body))
		_, _ = writer.Write([]byte(`{"access":{"mode":"organisation","roles":[]}}`))
	}))
	defer server.Close()
	api := New(testToken, server.URL)
	project, cleared := "1ed7f4df-23b0-4571-ad50-89b968748bc4", ""
	for _, change := range []*string{nil, &project, &cleared} {
		if _, updateError := api.UpdateCloudDNSAccess(context.Background(), "zone", CloudDNSAccessPolicy{Mode: "organisation", ProjectChange: change}); updateError != nil {
			t.Fatal(updateError)
		}
	}
	if strings.Contains(bodies[0], "project_id") || !strings.Contains(bodies[1], project) || !strings.Contains(bodies[2], `"project_id":""`) {
		t.Fatalf("bodies = %q", bodies)
	}
}

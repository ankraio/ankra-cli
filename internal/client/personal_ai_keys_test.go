package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// personalAIKeyBody is one key as the platform answers it.
func personalAIKeyBody(id string, label string, position int) map[string]any {
	return map[string]any{
		"id":           id,
		"provider":     "anthropic",
		"label":        label,
		"key_preview":  "sk-ant-...1234",
		"position":     position,
		"status":       "out_of_credit",
		"retry_after":  "2026-11-07T12:00:00Z",
		"last_used_at": nil,
		"created_at":   "2026-11-06T09:30:00Z",
	}
}

// personalAIKeyRequest is what a fake platform saw.
type personalAIKeyRequest struct {
	method      string
	path        string
	escapedPath string
	body        map[string]any
	rawBody     string
	auth        string
}

// newPersonalAIKeysClient answers every request with status and response, and
// records the request.
func newPersonalAIKeysClient(t *testing.T, status int, response any) (*Client, *personalAIKeyRequest) {
	t.Helper()
	seen := &personalAIKeyRequest{}
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		seen.method, seen.path, seen.escapedPath = request.Method, request.URL.Path, request.URL.EscapedPath()
		seen.rawBody, seen.body, seen.auth = string(raw), nil, request.Header.Get("Authorization")
		if len(raw) > 0 {
			if decodeError := json.Unmarshal(raw, &seen.body); decodeError != nil {
				t.Fatalf("decode request body %q: %v", raw, decodeError)
			}
		}
		jsonResponse(t, writer, status, response)
	})
	return testClient, seen
}

// Each route is pinned on the request itself: a path or method typo passes
// every command test and fails as a 404 against the platform.
func TestListPersonalAIKeysReadsTheKeysAndTheOrganisationPolicy(t *testing.T) {
	testClient, seen := newPersonalAIKeysClient(t, http.StatusOK, map[string]any{
		"keys":                []any{personalAIKeyBody("key-1", "work", 0), personalAIKeyBody("key-2", "", 1)},
		"organisation_policy": "disabled",
	})

	listing, listError := testClient.ListPersonalAIKeys()
	if listError != nil {
		t.Fatalf("ListPersonalAIKeys: %v", listError)
	}
	if seen.method != http.MethodGet || seen.path != "/api/v1/org/account/ai-keys" {
		t.Errorf("request = %s %s, want GET /api/v1/org/account/ai-keys", seen.method, seen.path)
	}
	if seen.auth != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want the login token as a bearer", seen.auth)
	}
	if listing.OrganisationPolicy != PersonalAIKeysPolicyDisabled || len(listing.Keys) != 2 {
		t.Fatalf("listing = %+v", listing)
	}
	first := listing.Keys[0]
	if first.ID != "key-1" || first.Label != "work" || first.KeyPreview != "sk-ant-...1234" ||
		first.Status != PersonalAIKeyStatusOutOfCredit || first.Position != 0 {
		t.Errorf("first key = %+v", first)
	}
	if first.RetryAfter == nil || *first.RetryAfter != "2026-11-07T12:00:00Z" || first.LastUsedAt != nil {
		t.Errorf("first key times = retry %v, last used %v", first.RetryAfter, first.LastUsedAt)
	}
	if !strings.Contains(string(listing.RawJSON()), `"organisation_policy"`) {
		t.Errorf("the listing should keep the platform's document for -o json, got %s", listing.RawJSON())
	}
}

func TestAddPersonalAIKeySendsTheProviderKeyAndLabel(t *testing.T) {
	testClient, seen := newPersonalAIKeysClient(t, http.StatusCreated, personalAIKeyBody("key-3", "personal", 2))

	key, addError := testClient.AddPersonalAIKey(AddPersonalAIKeyRequest{
		Provider: "anthropic", APIKey: "sk-ant-api03-secret", Label: "personal",
	})
	if addError != nil {
		t.Fatalf("AddPersonalAIKey: %v", addError)
	}
	if seen.method != http.MethodPost || seen.path != "/api/v1/org/account/ai-keys" {
		t.Errorf("request = %s %s, want POST /api/v1/org/account/ai-keys", seen.method, seen.path)
	}
	if seen.body["provider"] != "anthropic" || seen.body["api_key"] != "sk-ant-api03-secret" || seen.body["label"] != "personal" {
		t.Errorf("body = %v", seen.body)
	}
	if key.ID != "key-3" || key.Position != 2 || key.KeyPreview != "sk-ant-...1234" {
		t.Errorf("key = %+v", key)
	}
}

func TestAddPersonalAIKeyLeavesAnUnsetLabelOut(t *testing.T) {
	testClient, seen := newPersonalAIKeysClient(t, http.StatusCreated, personalAIKeyBody("key-3", "", 0))

	if _, addError := testClient.AddPersonalAIKey(AddPersonalAIKeyRequest{Provider: "openrouter", APIKey: "sk-or-v1-x"}); addError != nil {
		t.Fatalf("AddPersonalAIKey: %v", addError)
	}
	if _, hasLabel := seen.body["label"]; hasLabel {
		t.Errorf("body = %v, want no label when none was given", seen.body)
	}
}

// The platform says why it refused a key; that sentence is what the member
// needs, so it is the error's message, with the status kept for exit codes.
func TestAddPersonalAIKeySurfacesThePlatformRefusal(t *testing.T) {
	for _, testCase := range []struct {
		status int
		detail string
	}{
		{http.StatusBadRequest, "Anthropic refused this key: invalid x-api-key"},
		{http.StatusConflict, "This key is already on your profile"},
		{http.StatusForbidden, "AI keys can only be changed from your own signed-in browser session."},
	} {
		testClient, _ := newPersonalAIKeysClient(t, testCase.status, map[string]any{"detail": testCase.detail})

		_, addError := testClient.AddPersonalAIKey(AddPersonalAIKeyRequest{Provider: "anthropic", APIKey: "sk-ant-api03-secret"})

		var unexpected *UnexpectedResponseError
		if !errors.As(addError, &unexpected) || unexpected.StatusCode != testCase.status {
			t.Fatalf("status %d: error = %#v, want an UnexpectedResponseError with the status", testCase.status, addError)
		}
		if addError.Error() != testCase.detail {
			t.Errorf("status %d: error = %q, want the platform's detail %q", testCase.status, addError.Error(), testCase.detail)
		}
		if strings.Contains(addError.Error(), "sk-ant-api03-secret") {
			t.Errorf("status %d: the error must not carry the key, got %q", testCase.status, addError.Error())
		}
	}
}

func TestReorderPersonalAIKeysSendsEveryKeyID(t *testing.T) {
	testClient, seen := newPersonalAIKeysClient(t, http.StatusOK, map[string]any{
		"keys": []any{personalAIKeyBody("key-2", "", 0), personalAIKeyBody("key-1", "work", 1)},
	})

	list, reorderError := testClient.ReorderPersonalAIKeys([]string{"key-2", "key-1"})
	if reorderError != nil {
		t.Fatalf("ReorderPersonalAIKeys: %v", reorderError)
	}
	if seen.method != http.MethodPut || seen.path != "/api/v1/org/account/ai-keys/order" {
		t.Errorf("request = %s %s, want PUT /api/v1/org/account/ai-keys/order", seen.method, seen.path)
	}
	keyIDs, isList := seen.body["key_ids"].([]any)
	if !isList || len(keyIDs) != 2 || keyIDs[0] != "key-2" || keyIDs[1] != "key-1" {
		t.Errorf("body = %v, want key_ids [key-2 key-1]", seen.body)
	}
	if len(list.Keys) != 2 || list.Keys[0].ID != "key-2" {
		t.Errorf("list = %+v", list)
	}
}

// The platform reads a missing key_ids as a malformed request, so an empty
// order is still a list.
func TestReorderPersonalAIKeysSendsAnEmptyListNotNull(t *testing.T) {
	testClient, seen := newPersonalAIKeysClient(t, http.StatusOK, map[string]any{"keys": []any{}})

	if _, reorderError := testClient.ReorderPersonalAIKeys(nil); reorderError != nil {
		t.Fatalf("ReorderPersonalAIKeys: %v", reorderError)
	}
	if seen.rawBody != `{"key_ids":[]}` {
		t.Errorf("body = %s, want an empty key_ids list", seen.rawBody)
	}
}

func TestReorderPersonalAIKeysSurfacesAPartialOrderRefusal(t *testing.T) {
	const detail = "The order must list every key on your profile exactly once"
	testClient, _ := newPersonalAIKeysClient(t, http.StatusBadRequest, map[string]any{"detail": detail})

	_, reorderError := testClient.ReorderPersonalAIKeys([]string{"key-1"})
	if reorderError == nil || reorderError.Error() != detail {
		t.Errorf("error = %v, want the platform's detail", reorderError)
	}
}

func TestRenamePersonalAIKeySendsTheLabel(t *testing.T) {
	testClient, seen := newPersonalAIKeysClient(t, http.StatusOK, personalAIKeyBody("key-1", "renamed", 0))

	key, renameError := testClient.RenamePersonalAIKey("key-1", "renamed")
	if renameError != nil {
		t.Fatalf("RenamePersonalAIKey: %v", renameError)
	}
	if seen.method != http.MethodPatch || seen.path != "/api/v1/org/account/ai-keys/key-1" {
		t.Errorf("request = %s %s, want PATCH /api/v1/org/account/ai-keys/key-1", seen.method, seen.path)
	}
	if seen.body["label"] != "renamed" || key.Label != "renamed" {
		t.Errorf("body = %v, key = %+v", seen.body, key)
	}
}

// Clearing a label sends an empty string: the platform reads a missing label
// as a malformed request.
func TestRenamePersonalAIKeySendsAnEmptyLabelToClearIt(t *testing.T) {
	testClient, seen := newPersonalAIKeysClient(t, http.StatusOK, personalAIKeyBody("key-1", "", 0))

	if _, renameError := testClient.RenamePersonalAIKey("key-1", ""); renameError != nil {
		t.Fatalf("RenamePersonalAIKey: %v", renameError)
	}
	if label, hasLabel := seen.body["label"]; !hasLabel || label != "" {
		t.Errorf("body = %s, want an explicit empty label", seen.rawBody)
	}
}

func TestDeletePersonalAIKeyDeletesTheKey(t *testing.T) {
	testClient, seen := newPersonalAIKeysClient(t, http.StatusOK, map[string]any{"deleted": true})

	if deleteError := testClient.DeletePersonalAIKey("key-1"); deleteError != nil {
		t.Fatalf("DeletePersonalAIKey: %v", deleteError)
	}
	if seen.method != http.MethodDelete || seen.path != "/api/v1/org/account/ai-keys/key-1" {
		t.Errorf("request = %s %s, want DELETE /api/v1/org/account/ai-keys/key-1", seen.method, seen.path)
	}
}

func TestDeletePersonalAIKeySurfacesNotFound(t *testing.T) {
	testClient, _ := newPersonalAIKeysClient(t, http.StatusNotFound, map[string]any{"detail": "AI key not found"})

	deleteError := testClient.DeletePersonalAIKey("key-9")
	var unexpected *UnexpectedResponseError
	if !errors.As(deleteError, &unexpected) || unexpected.StatusCode != http.StatusNotFound || unexpected.Detail != "AI key not found" {
		t.Errorf("error = %#v, want a 404 carrying the platform's detail", deleteError)
	}
}

func TestTestPersonalAIKeyReadsTheOutcome(t *testing.T) {
	rejected := personalAIKeyBody("key-1", "work", 0)
	rejected["status"] = "rejected"
	testClient, seen := newPersonalAIKeysClient(t, http.StatusOK, map[string]any{
		"key": rejected, "is_valid": false, "message": "Anthropic refused this key: it was revoked",
	})

	result, testError := testClient.TestPersonalAIKey("key-1")
	if testError != nil {
		t.Fatalf("TestPersonalAIKey: %v", testError)
	}
	if seen.method != http.MethodPost || seen.path != "/api/v1/org/account/ai-keys/key-1/test" {
		t.Errorf("request = %s %s, want POST /api/v1/org/account/ai-keys/key-1/test", seen.method, seen.path)
	}
	if result.IsValid || result.Message != "Anthropic refused this key: it was revoked" ||
		result.Key.Status != PersonalAIKeyStatusRejected || result.Key.ID != "key-1" {
		t.Errorf("result = %+v", result)
	}
}

// A key id is one path segment, whatever it holds: a '/' or '?' must not
// reach another route.
func TestPersonalAIKeyRoutesKeepTheKeyIDOneSegment(t *testing.T) {
	testClient, seen := newPersonalAIKeysClient(t, http.StatusOK, map[string]any{"key": personalAIKeyBody("x", "", 0)})

	if _, testError := testClient.TestPersonalAIKey("a/b?c"); testError != nil {
		t.Fatalf("TestPersonalAIKey: %v", testError)
	}
	if seen.escapedPath != "/api/v1/org/account/ai-keys/a%2Fb%3Fc/test" {
		t.Errorf("escaped path = %s, want the id kept in one escaped segment", seen.escapedPath)
	}
}

func TestPersonalAIKeysPolicyReadAndWrite(t *testing.T) {
	testClient, seen := newPersonalAIKeysClient(t, http.StatusOK, map[string]any{"allowed": false})

	policy, getError := testClient.GetPersonalAIKeysPolicy()
	if getError != nil {
		t.Fatalf("GetPersonalAIKeysPolicy: %v", getError)
	}
	if seen.method != http.MethodGet || seen.path != "/api/v1/org/ai-settings/personal-keys" || policy.Allowed {
		t.Errorf("get = %s %s, policy %+v", seen.method, seen.path, policy)
	}

	if _, setError := testClient.SetPersonalAIKeysPolicy(false); setError != nil {
		t.Fatalf("SetPersonalAIKeysPolicy: %v", setError)
	}
	if seen.method != http.MethodPut || seen.path != "/api/v1/org/ai-settings/personal-keys" || seen.rawBody != `{"allowed":false}` {
		t.Errorf("set = %s %s %s, want PUT with an explicit allowed false", seen.method, seen.path, seen.rawBody)
	}
}

func TestSetPersonalAIKeysPolicyNeedsAnAdministrator(t *testing.T) {
	testClient, _ := newPersonalAIKeysClient(t, http.StatusForbidden, map[string]any{
		"detail": "permission_denied", "permission": "ai.manage",
	})

	_, setError := testClient.SetPersonalAIKeysPolicy(true)
	var denied *PermissionDeniedError
	if !errors.As(setError, &denied) || denied.Permission != "ai.manage" {
		t.Errorf("error = %#v, want a PermissionDeniedError naming ai.manage", setError)
	}
}

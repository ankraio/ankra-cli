package client

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestDiscardClusterStackDraftDeletesTheDraftAndReadsTheAnswer(t *testing.T) {
	testCases := map[string]struct {
		force         bool
		body          string
		wantQuery     string
		wantDiscarded bool
		wantProtected bool
	}{
		"discarded":      {false, `{"success":true,"stack_name":"edited","already_discarded":false,"reason":"USER_DISCARD"}`, "", true, false},
		"kept by a chat": {false, `{"success":true,"stack_name":"edited","already_discarded":true,"reason":"protected"}`, "", false, true},
		"forced":         {true, `{"success":true,"stack_name":"edited","already_discarded":false,"reason":"USER_DISCARD"}`, "force=true", true, false},
		"ended before":   {false, `{"success":true,"stack_name":null,"already_discarded":true,"reason":"DEPLOYED"}`, "", false, false},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			var receivedMethod, receivedPath, receivedQuery string
			testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				receivedMethod, receivedPath, receivedQuery = r.Method, r.URL.Path, r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(testCase.body))
			})

			result, discardError := testClient.DiscardClusterStackDraft(context.Background(), "cluster-1",
				"7a7a7a7a-7a7a-7a7a-7a7a-7a7a7a7a7a7a", testCase.force)
			if discardError != nil {
				t.Fatalf("DiscardClusterStackDraft: %v", discardError)
			}
			if receivedMethod != http.MethodDelete ||
				receivedPath != "/api/v1/org/clusters/imported/cluster-1/drafts/stacks/7a7a7a7a-7a7a-7a7a-7a7a-7a7a7a7a7a7a" ||
				receivedQuery != testCase.wantQuery {
				t.Fatalf("request = %s %s?%s", receivedMethod, receivedPath, receivedQuery)
			}
			if result.Discarded() != testCase.wantDiscarded || result.IsProtected() != testCase.wantProtected {
				t.Fatalf("result = %+v, discarded %v protected %v", result, result.Discarded(), result.IsProtected())
			}
		})
	}
}

func TestDiscardClusterStackDraftClassifiesTheRefusals(t *testing.T) {
	testCases := map[string]struct {
		status   int
		body     string
		wantGone bool
	}{
		"unknown draft":         {http.StatusNotFound, `{"detail":{"message":"The requested cluster stack draft could not be found.","code":"DRAFT_UNKNOWN"}}`, true},
		"managed service draft": {http.StatusConflict, `{"detail":"This draft belongs to a managed service installation."}`, false},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			})

			_, discardError := testClient.DiscardClusterStackDraft(context.Background(), "cluster-1", "draft-1", false)
			var refusal *StackDraftDiscardError
			if !errors.As(discardError, &refusal) || refusal.StatusCode != testCase.status || refusal.IsGone() != testCase.wantGone {
				t.Fatalf("error = %#v", discardError)
			}
		})
	}

	denied := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"permission_denied","permission":"stacks.write"}`))
	})
	_, deniedError := denied.DiscardClusterStackDraft(context.Background(), "cluster-1", "draft-1", false)
	var permissionDenied *PermissionDeniedError
	if !errors.As(deniedError, &permissionDenied) || permissionDenied.Permission != "stacks.write" {
		t.Fatalf("a refused permission must name it, got %#v", deniedError)
	}
}

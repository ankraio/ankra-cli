package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
)

func TestProfileSummaryRoutesAndRevisionBodies(t *testing.T) {
	cases := []struct {
		name, method, path string
		invoke             func(*Client) (json.RawMessage, error)
		body               map[string]any
	}{
		{"summary", http.MethodGet, "/api/v1/org/stack-profiles/profile/versions/3/summary", func(c *Client) (json.RawMessage, error) {
			return c.GetProfileSummary(context.Background(), "profile", 3)
		}, nil},
		{"evidence", http.MethodGet, "/api/v1/org/stack-profiles/profile/versions/3/summary/evidence/f0", func(c *Client) (json.RawMessage, error) {
			return c.GetProfileSummaryEvidence(context.Background(), "profile", 3, "f0")
		}, nil},
		{"generation", http.MethodGet, "/api/v1/org/stack-profiles/summary-generations/job", func(c *Client) (json.RawMessage, error) {
			return c.GetProfileSummaryGeneration(context.Background(), "job")
		}, nil},
		{"edit", http.MethodPut, "/api/v1/org/stack-profiles/summary-generations/job/candidate", func(c *Client) (json.RawMessage, error) {
			return c.EditProfileSummaryCandidate(context.Background(), "job", ProfileSummaryCandidateRequest{ExpectedCandidateRevision: 2, Outcomes: []ProfileSummaryOutcome{}})
		}, map[string]any{"expected_candidate_revision": float64(2), "outcomes": []any{}}},
		{"review", http.MethodPost, "/api/v1/org/stack-profiles/summary-generations/job/review", func(c *Client) (json.RawMessage, error) {
			return c.ReviewProfileSummary(context.Background(), "job", ProfileSummaryReviewRequest{ExpectedSourceRevision: 3, ExpectedCandidateRevision: 2, ExpectedApprovedRevision: 0, Decision: "approve"})
		}, map[string]any{"expected_source_revision": float64(3), "expected_candidate_revision": float64(2), "expected_approved_revision": float64(0), "decision": "approve"}},
		{"withdraw", http.MethodPost, "/api/v1/org/stack-profiles/profile/versions/3/summary/withdraw", func(c *Client) (json.RawMessage, error) {
			return c.WithdrawProfileSummary(context.Background(), "profile", 3, ProfileSummaryWithdrawalRequest{ExpectedSummaryRevision: 1, Reason: "incorrect_claim"})
		}, map[string]any{"expected_summary_revision": float64(1), "reason": "incorrect_claim"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			calls := 0
			testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != testCase.method || r.URL.Path != testCase.path {
					t.Errorf("wrong route: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") == "" {
					t.Error("missing bearer authority")
				}
				if testCase.body != nil {
					var body map[string]any
					if decodeError := json.NewDecoder(r.Body).Decode(&body); decodeError != nil {
						t.Error(decodeError)
					}
					if !reflect.DeepEqual(body, testCase.body) {
						t.Errorf("revision body changed: %#v want %#v", body, testCase.body)
					}
				}
				jsonResponse(t, w, http.StatusOK, map[string]any{"state": "available", "summary_revision": 1})
			})
			response, requestError := testCase.invoke(testClient)
			if requestError != nil || !json.Valid(response) || calls != 1 {
				t.Fatalf("request failed: %v %s calls=%d", requestError, response, calls)
			}
		})
	}
}

func TestProfileSummaryRefusalIsNotAbsentOrRetried(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				jsonResponse(t, w, status, map[string]any{"detail": "The summary decision could not be confirmed. Reload before retrying.", "code": "outcome_unknown"})
			})
			response, requestError := testClient.ReviewProfileSummary(context.Background(), "job", ProfileSummaryReviewRequest{ExpectedSourceRevision: 3, ExpectedCandidateRevision: 2, ExpectedApprovedRevision: 0, Decision: "approve"})
			if requestError == nil || response != nil || calls != 1 {
				t.Fatalf("refusal hidden or retried: %v %s calls=%d", requestError, response, calls)
			}
			if status == http.StatusUnauthorized && !errors.Is(requestError, ErrUnauthorized) {
				t.Fatalf("lost unauthorized identity: %v", requestError)
			}
			if status == http.StatusForbidden {
				var denied *PermissionDeniedError
				if !errors.As(requestError, &denied) {
					t.Fatalf("lost permission refusal: %T %v", requestError, requestError)
				}
			} else if status != http.StatusUnauthorized {
				var refusal *UnexpectedResponseError
				if !errors.As(requestError, &refusal) {
					t.Fatalf("lost typed HTTP refusal: %T %v", requestError, requestError)
				}
			}
		})
	}
}

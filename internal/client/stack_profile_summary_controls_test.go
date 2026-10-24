package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSummaryControlRoutesAndBodies(t *testing.T) {
	admission := ProfileSummaryAdmissionRequest{ExpectedSourceRevision: 3, ExpectedSummaryRevision: 0, IdempotencyKey: "01937e50-4980-7000-8000-000000000001"}
	approval := ProfileSummaryPolicyApprovalRequest{ExpectedRevision: 0, ExpectedConsentDigest: strings.Repeat("a", 64), AcceptDataUse: true, ConfirmProviderTerms: true, Limits: ProfileSummaryPolicyLimits{ContentRetentionDays: 7, MetadataRetentionDays: 30, DailyTokenLimit: 100000, RequestTokenLimit: 1000, DailyCostMicrousd: 1000001, RequestCostMicrousd: 1}}
	for _, test := range []struct {
		name, method, path string
		status             int
		body               any
		call               func(*Client) (json.RawMessage, error)
	}{
		{"version status", "GET", "/profile/versions/3/summary-generation", 200, nil, func(c *Client) (json.RawMessage, error) {
			return c.GetProfileSummaryStatus(context.Background(), "profile", 3)
		}},
		{"draft status", "GET", "/drafts/draft/summary-generation?expected_source_revision=3", 200, nil, func(c *Client) (json.RawMessage, error) {
			return c.GetDraftSummaryStatus(context.Background(), "draft", 3)
		}},
		{"version admission", "POST", "/profile/versions/3/summary-generation", 202, admission, func(c *Client) (json.RawMessage, error) {
			return c.GenerateProfileSummary(context.Background(), "profile", 3, admission)
		}},
		{"draft admission", "POST", "/drafts/draft/summary-generation", 202, admission, func(c *Client) (json.RawMessage, error) {
			return c.GenerateDraftSummary(context.Background(), "draft", admission)
		}},
		{"cancel", "POST", "/summary-generations/job/cancel", 200, struct{}{}, func(c *Client) (json.RawMessage, error) {
			return c.CancelProfileSummaryGeneration(context.Background(), "job")
		}},
		{"policy read", "GET", "/summary-generation-policy", 200, nil, func(c *Client) (json.RawMessage, error) { return c.GetProfileSummaryPolicy(context.Background()) }},
		{"policy approval", "PUT", "/summary-generation-policy", 200, approval, func(c *Client) (json.RawMessage, error) {
			return c.ApproveProfileSummaryPolicy(context.Background(), approval)
		}},
		{"policy revoke", "POST", "/summary-generation-policy/revoke", 200, ProfileSummaryPolicyRevocationRequest{ExpectedRevision: 2}, func(c *Client) (json.RawMessage, error) {
			return c.RevokeProfileSummaryPolicy(context.Background(), ProfileSummaryPolicyRevocationRequest{ExpectedRevision: 2})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != test.method || r.URL.RequestURI() != stackProfilesAPIPath+test.path || r.Header.Get("Authorization") == "" {
					t.Errorf("incorrect route or authority: %s %s", r.Method, r.URL)
				}
				if test.body != nil {
					var actual, expected map[string]any
					if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
						t.Error(err)
					}
					encoded, _ := json.Marshal(test.body)
					_ = json.Unmarshal(encoded, &expected)
					if !reflect.DeepEqual(actual, expected) {
						t.Errorf("body changed: %#v want %#v", actual, expected)
					}
				}
				jsonResponse(t, w, test.status, map[string]any{"state": "queued"})
			})
			body, err := test.call(c)
			if err != nil || calls != 1 || !json.Valid(body) {
				t.Fatalf("unexpected outcome: %v %s calls=%d", err, body, calls)
			}
		})
	}
}

func TestSummaryResponseLimitRejectsTruncatedOrOversizedSuccess(t *testing.T) {
	for _, content := range []string{`{"state":"queued"}` + strings.Repeat(" ", 1<<20), `{"broken":`, strings.Repeat(" ", 1<<20) + `{}`} {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, content) })
		body, err := c.GetProfileSummaryPolicy(context.Background())
		if err == nil || body != nil {
			t.Fatalf("invalid response accepted: %v", err)
		}
	}
}

func TestSummaryWritesNeverRetryAndPreserveRefusalClasses(t *testing.T) {
	for _, status := range []int{401, 403, 404, 409, 422, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				jsonResponse(t, w, status, map[string]any{"detail": "private-provider-diagnostic"})
			})
			c.HTTP.Transport = &retryTransport{base: c.HTTP.Transport}
			body, err := c.GenerateDraftSummary(context.Background(), "draft", ProfileSummaryAdmissionRequest{})
			if err == nil || body != nil || calls != 1 || strings.Contains(err.Error(), "private-provider-diagnostic") {
				t.Fatalf("refusal hidden, leaked or retried: %v calls=%d", err, calls)
			}
			if status == 401 && !errors.Is(err, ErrUnauthorized) {
				t.Fatal("lost auth identity")
			}
			if status == 403 {
				var denied *PermissionDeniedError
				if !errors.As(err, &denied) {
					t.Fatal("lost permission identity")
				}
			}
			if status != 401 && status != 403 {
				var response *UnexpectedResponseError
				if !errors.As(err, &response) || response.StatusCode != status {
					t.Fatal("lost refusal status")
				}
			}
		})
	}
}

func TestSummaryRequestUsesBoundedDeadlineAndRespectsCancellation(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled request reached server") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetProfileSummaryPolicy(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	// Observe the client-side context; HTTP does not transmit deadlines to the server.
	c.HTTP.Transport = summaryDeadlineTransport{t: t}
	if _, err := c.GetProfileSummaryPolicy(context.Background()); err == nil {
		t.Fatal("expected transport sentinel")
	}
}

type summaryDeadlineTransport struct{ t *testing.T }

func (s summaryDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	deadline, ok := r.Context().Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining <= 0 || remaining > 15*time.Second {
		s.t.Errorf("unbounded request: %v %s", ok, remaining)
	}
	return nil, errors.New("deadline inspected")
}

func TestSummaryRefusalMessagesKeepRecoveryGuidanceWithoutPrivateText(t *testing.T) {
	for code, expected := range map[string]string{"generation_unavailable": "existing summaries", "route_unavailable": "policy get", "outcome_unknown": "original request key"} {
		payload, _ := json.Marshal(map[string]string{"code": code, "detail": "private diagnostic"})
		message := summaryRefusalMessage(http.StatusServiceUnavailable, payload)
		if !strings.Contains(message, expected) || strings.Contains(message, "private diagnostic") {
			t.Fatalf("unsafe or unhelpful refusal: %s", message)
		}
	}
	if !strings.Contains(summaryRefusalMessage(429, []byte(`{}`)), "budget") {
		t.Fatal("budget refusal lost")
	}
}

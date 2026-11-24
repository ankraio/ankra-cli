package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

// hostedLogsBillingBody is the platform's answer as cluster#6912's openapi
// names it: every required member present, the month paused at the free
// amount.
const hostedLogsBillingBody = `{"paid_enabled":false,"monthly_limit_cents":2000,"updated_at":"2026-11-24T10:00:00Z",` +
	`"priced_from":"2027-01-01","free_bytes_per_month":5000000000,"cents_per_gb":25,"bytes_per_gb":1000000000,` +
	`"currency":"eur","paid_readiness":"blocked","paid_blocker":"Add a payment method on the Billing page before turning on paid hosted logs.",` +
	`"month":{"month_start":"2027-01-01","priced":true,"bytes":5200000000,"free_bytes":5000000000,"billable_bytes":200000000,` +
	`"amount_cents":0,"state":"paused_free","paused_reason":"free_allowance_used","computed_at":"2027-01-09T08:05:00Z"}}`

// The routes are pinned on the request itself: a path or method typo fails
// as a 404 against a real platform and passes every command test that only
// checks the wiring. paid_enabled is always sent, false included, and a
// limit that was not given is left out rather than sent as null, which the
// route refuses.
func TestHostedLogsBillingLaneParity(t *testing.T) {
	limit := int64(2000)
	lanes := []struct {
		name       string
		wantMethod string
		wantBody   string
		call       func(testClient *Client) (*HostedLogsBilling, error)
	}{
		{
			name:       "get reads the setting and the month",
			wantMethod: http.MethodGet,
			call: func(testClient *Client) (*HostedLogsBilling, error) {
				return testClient.GetHostedLogsBilling(context.Background())
			},
		},
		{
			name:       "turning paid logs on sends the limit in cents",
			wantMethod: http.MethodPut,
			wantBody:   `{"paid_enabled":true,"monthly_limit_cents":2000}`,
			call: func(testClient *Client) (*HostedLogsBilling, error) {
				return testClient.UpdateHostedLogsBilling(context.Background(),
					HostedLogsBillingUpdate{PaidEnabled: true, MonthlyLimitCents: &limit})
			},
		},
		{
			name:       "turning paid logs off keeps the stored limit",
			wantMethod: http.MethodPut,
			wantBody:   `{"paid_enabled":false}`,
			call: func(testClient *Client) (*HostedLogsBilling, error) {
				return testClient.UpdateHostedLogsBilling(context.Background(), HostedLogsBillingUpdate{PaidEnabled: false})
			},
		},
	}

	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			var seenMethod, seenPath, seenBody string
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				seenMethod, seenPath = request.Method, request.URL.EscapedPath()
				raw, _ := io.ReadAll(request.Body)
				seenBody = string(raw)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(hostedLogsBillingBody))
			})

			view, callError := lane.call(testClient)
			if callError != nil {
				t.Fatalf("call error = %v", callError)
			}
			if seenMethod != lane.wantMethod || seenPath != "/api/v1/org/billing/hosted-logs" {
				t.Errorf("request = %s %s, want %s /api/v1/org/billing/hosted-logs", seenMethod, seenPath, lane.wantMethod)
			}
			if lane.wantBody != "" {
				var got, want map[string]any
				if decodeError := json.Unmarshal([]byte(seenBody), &got); decodeError != nil {
					t.Fatalf("the request body is not JSON: %v: %s", decodeError, seenBody)
				}
				_ = json.Unmarshal([]byte(lane.wantBody), &want)
				if len(got) != len(want) {
					t.Fatalf("body = %s, want exactly %s", seenBody, lane.wantBody)
				}
				for key, value := range want {
					if got[key] != value {
						t.Errorf("body[%q] = %v, want %v", key, got[key], value)
					}
				}
			}
			month := view.Month
			if view.PaidEnabled || view.MonthlyLimitCents == nil || *view.MonthlyLimitCents != 2000 ||
				view.PricedFrom != "2027-01-01" || view.FreeBytesPerMonth != 5_000_000_000 || view.CentsPerGB != 25 ||
				view.BytesPerGB != 1_000_000_000 || view.Currency != "eur" || view.PaidReadiness != HostedLogsPaidBlocked ||
				view.PaidBlocker == nil || view.UpdatedAt == nil {
				t.Errorf("decoded setting = %+v", view)
			}
			if month.MonthStart != "2027-01-01" || !month.Priced || month.Bytes != 5_200_000_000 || month.BillableBytes != 200_000_000 ||
				month.State != HostedLogsStatePausedFree || month.PausedReason == nil ||
				*month.PausedReason != HostedLogsPausedFreeAllowanceUsed || month.ComputedAt == nil {
				t.Errorf("decoded month = %+v", month)
			}
		})
	}
}

// The platform's refusal of turning paid logs on names its remedy; it
// arrives as the detail, with the status, so the command can print it as
// given.
func TestHostedLogsBillingRefusalKeepsThePlatformsSentence(t *testing.T) {
	remedy := "Add a payment method on the Billing page before turning on paid hosted logs."
	testClient := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		jsonResponse(t, writer, http.StatusPaymentRequired, map[string]string{"detail": remedy})
	})
	limit := int64(500)
	_, callError := testClient.UpdateHostedLogsBilling(context.Background(), HostedLogsBillingUpdate{PaidEnabled: true, MonthlyLimitCents: &limit})
	var unexpected *UnexpectedResponseError
	if !errors.As(callError, &unexpected) || unexpected.StatusCode != http.StatusPaymentRequired || unexpected.Detail != remedy {
		t.Fatalf("refusal = %#v, want a 402 carrying the platform's sentence", callError)
	}
}

// The cluster switch answers the organisation's month pause beside the
// switch; a platform that predates it leaves it out, which reads as nothing
// pausing them.
func TestClusterHostedLogsDecodesThePauseReason(t *testing.T) {
	for _, testCase := range []struct {
		body string
		want *string
	}{
		{`{"cluster_id":"c","shipping_enabled":true,"available":true,"agent_supports_switch":true,"changed_at":null,` +
			`"paused_reason":"monthly_limit_reached"}`, strPtr(HostedLogsPausedMonthlyLimitReached)},
		{`{"cluster_id":"c","shipping_enabled":true,"available":true,"agent_supports_switch":true,"changed_at":null,` +
			`"paused_reason":null}`, nil},
		{`{"cluster_id":"c","shipping_enabled":true,"available":true,"agent_supports_switch":true,"changed_at":null}`, nil},
	} {
		testClient := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(testCase.body))
		})
		state, getError := testClient.GetClusterHostedLogs(context.Background(), "c")
		if getError != nil {
			t.Fatalf("GetClusterHostedLogs error = %v", getError)
		}
		if (state.PausedReason == nil) != (testCase.want == nil) ||
			(state.PausedReason != nil && *state.PausedReason != *testCase.want) {
			t.Errorf("paused_reason from %s = %v, want %v", testCase.body, state.PausedReason, testCase.want)
		}
	}
}

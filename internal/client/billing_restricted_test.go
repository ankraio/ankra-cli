package client

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

const billingRestrictedBody = `{"error_code":"BILLING_RESTRICTED","detail":"Your prepaid credit is used up; new clusters and AI are paused. Your running clusters are unaffected. Add credit to continue."}`

const billingRestrictedDetail = "Your prepaid credit is used up; new clusters and AI are paused. Your running clusters are unaffected. Add credit to continue."

func TestDecodeAPIErrorBody(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantOK   bool
		wantCode string
		wantText string
	}{
		{"code and detail", billingRestrictedBody, true, "BILLING_RESTRICTED", billingRestrictedDetail},
		{"detail only", `{"detail":"Cluster not found"}`, true, "", "Cluster not found"},
		{"code only", `{"error_code":"RATE_LIMITED"}`, true, "RATE_LIMITED", ""},
		{"neither member", `{"message":"nope"}`, false, "", ""},
		{"not JSON", `<html>502</html>`, false, "", ""},
		{"empty", ``, false, "", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			decoded, ok := DecodeAPIErrorBody([]byte(testCase.body))
			if ok != testCase.wantOK {
				t.Fatalf("ok = %v, want %v", ok, testCase.wantOK)
			}
			if decoded.ErrorCode != testCase.wantCode || decoded.Detail != testCase.wantText {
				t.Errorf("decoded = %+v, want code %q detail %q", decoded, testCase.wantCode, testCase.wantText)
			}
		})
	}
}

func TestBillingRestrictedFromResponse_OnlyTheCodeIsTheRefusal(t *testing.T) {
	if restricted := BillingRestrictedFromResponse([]byte(billingRestrictedBody)); restricted == nil || restricted.Detail != billingRestrictedDetail {
		t.Fatalf("BillingRestrictedFromResponse = %v, want the refusal with its sentence", restricted)
	}
	for _, body := range []string{`{"detail":"Payment required"}`, `{"error_code":"AI_ALLOWANCE_EXHAUSTED","detail":"..."}`, `not json`} {
		if restricted := BillingRestrictedFromResponse([]byte(body)); restricted != nil {
			t.Errorf("BillingRestrictedFromResponse(%q) = %v, want nil", body, restricted)
		}
	}
	empty := &BillingRestrictedError{}
	if empty.Error() == "" {
		t.Error("an empty refusal still has to say something")
	}
}

func TestCreateChatSession_BillingRestricted402IsTheTypedRefusal(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = fmt.Fprint(w, billingRestrictedBody)
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.CreateChatSession(CreateChatSessionRequest{ConversationID: "conv-1"})
	var restricted *BillingRestrictedError
	if !errors.As(err, &restricted) {
		t.Fatalf("err = %v (%T), want *BillingRestrictedError", err, err)
	}
	if restricted.Detail != billingRestrictedDetail || err.Error() != billingRestrictedDetail {
		t.Errorf("refusal = %q, want the platform's sentence verbatim", err.Error())
	}
}

func TestGetClusterHealth_BillingRestricted402IsTheTypedRefusal(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = fmt.Fprint(w, billingRestrictedBody)
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.GetClusterHealth("cluster-1", true)
	var restricted *BillingRestrictedError
	if !errors.As(err, &restricted) {
		t.Fatalf("err = %v (%T), want *BillingRestrictedError through getJSON", err, err)
	}
}

func TestStreamChat_BillingRestricted402IsTheTypedRefusal(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = fmt.Fprint(w, billingRestrictedBody)
	}
	testClient := newTestClient(t, handler)
	_, err := testClient.StreamChat(nil, ChatRequest{Query: "hi"})
	var restricted *BillingRestrictedError
	if !errors.As(err, &restricted) {
		t.Fatalf("err = %v (%T), want *BillingRestrictedError", err, err)
	}
	if restricted.Detail != billingRestrictedDetail {
		t.Errorf("Detail = %q, want the platform's sentence", restricted.Detail)
	}
}

func TestStreamChat_BillingRestrictedErrorFrameCarriesTheRefusal(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"error\",\"data\":{\"error_code\":\"BILLING_RESTRICTED\",\"message\":%q}}\n\n", billingRestrictedDetail)
		flusher.Flush()
	}
	testClient := newTestClient(t, handler)
	events, err := testClient.StreamChat(nil, ChatRequest{Query: "hi"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	var received []ChatStreamEvent
	for event := range events {
		received = append(received, event)
	}
	if len(received) != 1 || received[0].Type != "error" {
		t.Fatalf("expected one error event, got %v", received)
	}
	restricted := BillingRestrictedFromStreamEvent(received[0])
	if restricted == nil || restricted.Detail != billingRestrictedDetail {
		t.Fatalf("BillingRestrictedFromStreamEvent = %v, want the refusal with its sentence", restricted)
	}
}

func TestBillingRestrictedFromStreamEvent_OtherFramesAreNotTheRefusal(t *testing.T) {
	cases := []ChatStreamEvent{
		{Type: "error", Data: map[string]any{"message": "rate limited", "is_rate_limited": true}},
		{Type: "error", Data: map[string]any{"error_code": "AI_ALLOWANCE_EXHAUSTED", "message": "..."}},
		{Type: "error", Error: "stream idle timeout"},
		{Type: "content", Data: map[string]any{"error_code": "BILLING_RESTRICTED"}},
		{Type: "error", Data: "BILLING_RESTRICTED"},
	}
	for _, event := range cases {
		if restricted := BillingRestrictedFromStreamEvent(event); restricted != nil {
			t.Errorf("BillingRestrictedFromStreamEvent(%+v) = %v, want nil", event, restricted)
		}
	}
	detailOnly := ChatStreamEvent{Type: "error", Data: map[string]any{"error_code": "BILLING_RESTRICTED", "detail": "Invoice overdue; AI is paused."}}
	if restricted := BillingRestrictedFromStreamEvent(detailOnly); restricted == nil || restricted.Detail != "Invoice overdue; AI is paused." {
		t.Errorf("a frame with detail and no message = %v, want the detail as the sentence", restricted)
	}
}

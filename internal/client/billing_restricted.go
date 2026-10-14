package client

import "encoding/json"

// BillingRestrictedErrorCode is the error_code the platform answers when the
// organisation's billing standing pauses AI and new clusters. REST routes
// answer it with HTTP 402; the streaming chat routes stay 200 and carry it on
// their SSE error frame instead.
const BillingRestrictedErrorCode = "BILLING_RESTRICTED"

// DecodeAPIErrorBody decodes the platform's JSON error shape
// ({"error_code": ..., "detail": ...}) from a response body. The second
// result is false when the body is not that shape (a bare router 404, a proxy
// page) or carries neither member, so callers can fall back to the raw body.
func DecodeAPIErrorBody(body []byte) (ApiErrorResponse, bool) {
	var decoded ApiErrorResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ApiErrorResponse{}, false
	}
	if decoded.ErrorCode == "" && decoded.Detail == "" {
		return ApiErrorResponse{}, false
	}
	return decoded, true
}

// BillingRestrictedError is the platform's refusal to run AI while the
// organisation's billing standing pauses it: prepaid credit used up, an
// invoice overdue, a credit limit or the free tier exceeded. Detail is the
// platform's own sentence for the user, which names the standing and what
// resolves it; the CLI prints it verbatim and adds where to top up. Re-running
// the command cannot help, and neither can a re-login, so the exit code is the
// generic refusal (1), not the auth code.
type BillingRestrictedError struct {
	Detail string
}

func (e *BillingRestrictedError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return e.Detail
	}
	return "AI is paused for this organisation until its billing standing is resolved"
}

// BillingRestrictedFromResponse returns the typed refusal when a response
// body carries error_code BILLING_RESTRICTED, and nil for any other body. The
// code is the signal, not the status: the platform sends it with 402, but a
// body that names it is the refusal whatever status rode with it.
func BillingRestrictedFromResponse(body []byte) *BillingRestrictedError {
	decoded, ok := DecodeAPIErrorBody(body)
	if !ok || decoded.ErrorCode != BillingRestrictedErrorCode {
		return nil
	}
	return &BillingRestrictedError{Detail: decoded.Detail}
}

// BillingRestrictedFromStreamEvent returns the typed refusal when a chat
// stream's error frame carries error_code BILLING_RESTRICTED in its payload
// ({"type":"error","data":{"error_code":..., "message"|"detail":...}}), and
// nil for every other frame.
func BillingRestrictedFromStreamEvent(event ChatStreamEvent) *BillingRestrictedError {
	if event.Type != "error" {
		return nil
	}
	data, isMap := event.Data.(map[string]any)
	if !isMap {
		return nil
	}
	code, _ := data["error_code"].(string)
	if code != BillingRestrictedErrorCode {
		return nil
	}
	detail, _ := data["detail"].(string)
	if message, ok := data["message"].(string); ok && message != "" {
		detail = message
	}
	return &BillingRestrictedError{Detail: detail}
}

package client

// Limit-increase requests: the org-facing side of the limits an organisation
// cannot raise itself (playground memory, the monthly AI allowance),
// reviewed by the Ankra team.

import "fmt"

// LimitRequest is one request's org-facing view.
type LimitRequest struct {
	ID             string  `json:"id"`
	LimitKind      string  `json:"limit_kind"`
	RequestedValue int64   `json:"requested_value"`
	Justification  string  `json:"justification"`
	Status         string  `json:"status"`
	RequestedAt    *string `json:"requested_at"`
	ReviewedAt     *string `json:"reviewed_at"`
}

// LimitRequestList wraps the latest request per kind.
type LimitRequestList struct {
	Requests []LimitRequest `json:"requests"`
}

// ListLimitRequests reads the organisation's latest request per limit kind.
func (c *Client) ListLimitRequests() (*LimitRequestList, error) {
	url := fmt.Sprintf("%s/api/v1/org/billing/limit-requests", c.BaseURL)
	var list LimitRequestList
	if err := c.getJSON(url, &list); err != nil {
		return nil, err
	}
	return &list, nil
}

type limitRequestBody struct {
	LimitKind      string `json:"limit_kind"`
	RequestedValue int64  `json:"requested_value"`
	Justification  string `json:"justification"`
}

// SubmitLimitRequest asks for a higher limit; kinds are playground_memory
// (value in MiB) and ai_tokens (value in USD cents per month).
func (c *Client) SubmitLimitRequest(limitKind string, requestedValue int64, justification string) (*LimitRequest, error) {
	url := fmt.Sprintf("%s/api/v1/org/billing/limit-request", c.BaseURL)
	var request LimitRequest
	if err := c.postCSRFJSON(url, limitRequestBody{
		LimitKind:      limitKind,
		RequestedValue: requestedValue,
		Justification:  justification,
	}, &request, "submit limit request"); err != nil {
		return nil, err
	}
	return &request, nil
}

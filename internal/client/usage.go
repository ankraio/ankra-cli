package client

// The organisation usage read (epic ankra-t5jf5.34.12, decision D4): GET
// /api/v1/org/billing/usage, the bearer twin of the route the portal's
// billing page reads. One report over a window of UTC days, per meter and
// period, optionally split by cluster, application or managed service
// instance.
//
// The shape is the platform's own and the client keeps it exactly: a
// quantity nobody measured is null, never zero, and an amount is set only
// where a published rate priced the line. The human rendering is the
// command's job; -o json carries what the platform answered.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const organisationUsagePath = "/api/v1/org/billing/usage"

// pydanticValueErrorPrefix opens the msg of a pydantic value_error entry.
const pydanticValueErrorPrefix = "Value error, "

// Usage coverage states, as the platform names them.
const (
	UsageCoverageComplete = "complete"
	UsageCoveragePartial  = "partial"
	UsageCoverageUnknown  = "unknown"
)

// Usage price states, as the platform names them.
const (
	UsagePriceIncluded = "included"
	UsagePricePriced   = "priced"
	UsagePriceUnpriced = "unpriced"
)

// UsageOptions selects the window and shape of a usage report. Every field
// is optional: an empty From reads from the first day of the current month,
// an empty To reads up to today, Granularity defaults to month and GroupBy
// to meter. Dates are UTC days as YYYY-MM-DD; To is exclusive.
type UsageOptions struct {
	From        string
	To          string
	Granularity string
	GroupBy     string
}

func (options UsageOptions) query() url.Values {
	values := url.Values{}
	if options.From != "" {
		values.Set("from", options.From)
	}
	if options.To != "" {
		values.Set("to", options.To)
	}
	if options.Granularity != "" {
		values.Set("granularity", options.Granularity)
	}
	if options.GroupBy != "" {
		values.Set("group_by", options.GroupBy)
	}
	return values
}

// UsageReport is what GET /api/v1/org/billing/usage answers.
type UsageReport struct {
	// From and To bound the window as instants: midnight UTC of the first
	// day read, and midnight UTC after the last day read or the time of the
	// request when that is earlier.
	From        string      `json:"from" yaml:"from"`
	To          string      `json:"to" yaml:"to"`
	Granularity string      `json:"granularity" yaml:"granularity"`
	GroupBy     string      `json:"group_by" yaml:"group_by"`
	Lines       []UsageLine `json:"lines" yaml:"lines"`
}

// UsageLine is one meter over one period for one group. Grouped by meter
// every meter answers every period; grouped otherwise a group that used
// none of a meter in a fully measured period has no line.
type UsageLine struct {
	Meter       string `json:"meter" yaml:"meter"`
	Unit        string `json:"unit" yaml:"unit"`
	PeriodStart string `json:"period_start" yaml:"period_start"`
	PeriodEnd   string `json:"period_end" yaml:"period_end"`
	// The group: null unless the report is split that way, and for meters
	// the platform does not attribute to it (AI usage has no cluster).
	ClusterID     *string `json:"cluster_id" yaml:"cluster_id"`
	ClusterName   *string `json:"cluster_name" yaml:"cluster_name"`
	ApplicationID *string `json:"application_id" yaml:"application_id"`
	InstanceID    *string `json:"instance_id" yaml:"instance_id"`
	InstanceName  *string `json:"instance_name" yaml:"instance_name"`
	// IsShared marks an application line some of whose usage is of an
	// instance that serves other applications too; such usage is counted
	// under each of them, so application lines do not add up to the
	// organisation's total.
	IsShared bool `json:"is_shared" yaml:"is_shared"`
	// Quantity is exact when Coverage is complete, a lower bound when
	// partial, and nil when unknown: nothing of the period was measured.
	Quantity *int64 `json:"quantity" yaml:"quantity"`
	Coverage string `json:"coverage" yaml:"coverage"`
	// PriceState is included (the pilot: no rate, nothing charged), priced
	// (AmountMinor is what the published rate makes the quantity cost) or
	// unpriced (no rate applies as the line is; PriceBasis says why).
	PriceState string `json:"price_state" yaml:"price_state"`
	PriceBasis string `json:"price_basis" yaml:"price_basis"`
	// AmountMinor is in minor units of Currency (euro cents); nil unless
	// PriceState is priced.
	AmountMinor *int64  `json:"amount_minor" yaml:"amount_minor"`
	Currency    *string `json:"currency" yaml:"currency"`
}

// UsageQueryRefusedError is the platform's 422 for a usage read: one entry
// per refused query parameter, each naming the parameter. The command maps
// the parameter back to the flag that set it.
type UsageQueryRefusedError struct {
	Refusals []UsageQueryRefusal
}

// UsageQueryRefusal is one refused query parameter and the platform's
// reason.
type UsageQueryRefusal struct {
	Parameter string
	Message   string
}

func (e *UsageQueryRefusedError) Error() string {
	parts := make([]string, 0, len(e.Refusals))
	for _, refusal := range e.Refusals {
		if refusal.Parameter == "" {
			parts = append(parts, refusal.Message)
			continue
		}
		parts = append(parts, refusal.Parameter+": "+refusal.Message)
	}
	return "the platform refused the usage query: " + strings.Join(parts, "; ")
}

// usageQueryRefusedFromBody reads the pydantic v2 422 shape
// ({"detail": [{"loc": ["query", "to"], "msg": "Value error, ..."}]}) into
// a UsageQueryRefusedError, or nil when the body is not that shape. The
// "Value error, " prefix pydantic puts on a validator's message is dropped:
// the reason after it is the part a person reads.
func usageQueryRefusedFromBody(body []byte) *UsageQueryRefusedError {
	var envelope struct {
		Detail []struct {
			Loc []any  `json:"loc"`
			Msg string `json:"msg"`
		} `json:"detail"`
	}
	if unmarshalError := json.Unmarshal(body, &envelope); unmarshalError != nil || len(envelope.Detail) == 0 {
		return nil
	}
	refused := &UsageQueryRefusedError{}
	for _, item := range envelope.Detail {
		parameter := ""
		for _, member := range item.Loc {
			name := fmt.Sprintf("%v", member)
			if name == "query" || name == "body" || name == "path" {
				continue
			}
			parameter = name
		}
		message := strings.TrimPrefix(item.Msg, pydanticValueErrorPrefix)
		refused.Refusals = append(refused.Refusals, UsageQueryRefusal{Parameter: parameter, Message: message})
	}
	return refused
}

// GetOrganisationUsage reads the organisation's usage over the window the
// options select. Requires billing.read; a refusal comes back as the
// client's usual error types, and a refused query parameter as a
// *UsageQueryRefusedError naming it.
func (c *Client) GetOrganisationUsage(ctx context.Context, options UsageOptions) (*UsageReport, error) {
	address := c.BaseURL + organisationUsagePath
	if query := options.query().Encode(); query != "" {
		address += "?" + query
	}
	request, requestError := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if requestError != nil {
		return nil, fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	// The --org override header is not set here because c.HTTP sets it on
	// every request (orgOverrideTransport, client.go), as for every read.
	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return nil, fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)
	body, readError := readResponseBody(response)
	if readError != nil {
		return nil, fmt.Errorf("read response: %w", readError)
	}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusUnprocessableEntity:
		if refused := usageQueryRefusedFromBody(body); refused != nil {
			return nil, refused
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if denied := PermissionDeniedFromResponse(response.StatusCode, body); denied != nil {
			return nil, denied
		}
		if detail := detailFromBody(body); detail != "" {
			return nil, newBackendDetailError(response.StatusCode, detail)
		}
		return nil, newUnexpectedResponseError("reading usage", response.StatusCode, redactedBodyForError(body, 500))
	}
	var report UsageReport
	if unmarshalError := json.Unmarshal(body, &report); unmarshalError != nil {
		return nil, fmt.Errorf("parse response: %w", unmarshalError)
	}
	if report.Lines == nil {
		return nil, errors.New("parse response: the usage report carries no lines field")
	}
	return &report, nil
}

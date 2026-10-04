package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// AsyncWriteAcceptedResponse is a write the platform accepted without
// waiting for it. OperationID, when the platform answers one, names the
// execution that records the write's outcome
// (ankra cluster operations list <id>); a route that records none, or an
// older platform, leaves it empty.
type AsyncWriteAcceptedResponse struct {
	Status      string `json:"status"`
	OperationID string `json:"operation_id,omitempty"`
}

// parseAsyncWriteAccepted reads a 202 body. The body is informational: a
// body that does not decode still means the write was accepted.
func parseAsyncWriteAccepted(responseBody []byte) *AsyncWriteAcceptedResponse {
	accepted := &AsyncWriteAcceptedResponse{Status: "accepted"}
	_ = json.Unmarshal(responseBody, accepted)
	return accepted
}

func appendWaitQuery(endpoint string, wait bool) string {
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		separator := "?"
		if strings.Contains(endpoint, "?") {
			separator = "&"
		}
		waitValue := "false"
		if wait {
			waitValue = "true"
		}
		return endpoint + separator + "wait=" + waitValue
	}
	query := parsedURL.Query()
	if wait {
		query.Set("wait", "true")
	} else {
		query.Set("wait", "false")
	}
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String()
}

func (c *Client) httpClientForAsyncWrite(wait bool) *http.Client {
	if !wait {
		return c.HTTP
	}
	waitBaseTransport := &http.Transport{
		ResponseHeaderTimeout: 0,
	}
	waitTransport := &orgOverrideTransport{base: waitBaseTransport, orgID: &c.orgOverride}
	return &http.Client{
		Transport: waitTransport,
	}
}

// gitPushDeferredError carries a git-push deferral out of the shared async
// write path as an error, so a caller whose route can receive one (today only
// ApplyCluster) converts it to success-with-deferral, while any other caller
// keeps treating it as the failure it would have been before classification.
type gitPushDeferredError struct {
	deferral GitPushDeferral
}

func (e *gitPushDeferredError) Error() string {
	return e.deferral.Message
}

func parseAsyncWriteResponse(
	response *http.Response,
	responseBody []byte,
	wait bool,
	target interface{},
) (accepted *AsyncWriteAcceptedResponse, err error) {
	if response.StatusCode == http.StatusAccepted {
		return parseAsyncWriteAccepted(responseBody), nil
	}
	if denied := PermissionDeniedFromResponse(response.StatusCode, responseBody); denied != nil {
		return nil, denied
	}
	if deferral := gitPushDeferralFromResponse(response.StatusCode, responseBody); deferral != nil {
		return nil, &gitPushDeferredError{deferral: *deferral}
	}
	if wait {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, newUnexpectedResponseErrorWithMessage(response.StatusCode, fmt.Sprintf("request failed: status %d: %s", response.StatusCode, redactedBodyForError(responseBody, 500)))
		}
		if target == nil {
			return nil, nil
		}
		if err := decodeJSON(responseBody, target); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newUnexpectedResponseErrorWithMessage(response.StatusCode, fmt.Sprintf("request failed: status %d: %s", response.StatusCode, redactedBodyForError(responseBody, 500)))
	}
	return nil, newUnexpectedResponseErrorWithMessage(response.StatusCode, fmt.Sprintf("unexpected status %d for async submit", response.StatusCode))
}

func decodeJSON(responseBody []byte, target interface{}) error {
	if err := json.Unmarshal(responseBody, target); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

func (c *Client) doJSONWriteRequest(
	ctx context.Context,
	method string,
	endpoint string,
	payload []byte,
	wait bool,
	target interface{},
) (submitted bool, err error) {
	accepted, err := c.doJSONWriteRequestAccepted(ctx, method, endpoint, payload, wait, target)
	return accepted != nil, err
}

// doJSONWriteRequestAccepted is doJSONWriteRequest for a caller that reads
// the accepted answer: non-nil when the platform accepted the write without
// waiting for it.
func (c *Client) doJSONWriteRequestAccepted(
	ctx context.Context,
	method string,
	endpoint string,
	payload []byte,
	wait bool,
	target interface{},
) (accepted *AsyncWriteAcceptedResponse, err error) {
	requestURL := appendWaitQuery(endpoint, wait)
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	httpClient := c.httpClientForAsyncWrite(wait)
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(response)

	responseBody, err := readResponseBody(response)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return parseAsyncWriteResponse(response, responseBody, wait, target)
}

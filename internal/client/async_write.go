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

type AsyncWriteAcceptedResponse struct {
	Status string `json:"status"`
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

func parseAsyncWriteResponse(
	response *http.Response,
	responseBody []byte,
	wait bool,
	target interface{},
) (submitted bool, err error) {
	if response.StatusCode == http.StatusAccepted {
		return true, nil
	}
	if denied := PermissionDeniedFromResponse(response.StatusCode, responseBody); denied != nil {
		return false, denied
	}
	if wait {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return false, newUnexpectedResponseErrorWithMessage(response.StatusCode, fmt.Sprintf("request failed: status %d: %s", response.StatusCode, redactedBodyForError(responseBody, 500)))
		}
		if target == nil {
			return false, nil
		}
		if err := decodeJSON(responseBody, target); err != nil {
			return false, err
		}
		return false, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, newUnexpectedResponseErrorWithMessage(response.StatusCode, fmt.Sprintf("request failed: status %d: %s", response.StatusCode, redactedBodyForError(responseBody, 500)))
	}
	return false, newUnexpectedResponseErrorWithMessage(response.StatusCode, fmt.Sprintf("unexpected status %d for async submit", response.StatusCode))
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
	requestURL := appendWaitQuery(endpoint, wait)
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	httpClient := c.httpClientForAsyncWrite(wait)
	response, err := httpClient.Do(request)
	if err != nil {
		return false, fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(response)

	responseBody, err := readResponseBody(response)
	if err != nil {
		return false, fmt.Errorf("read response: %w", err)
	}
	return parseAsyncWriteResponse(response, responseBody, wait, target)
}

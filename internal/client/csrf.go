package client

// CSRF-protected JSON verbs for backend routes that sit behind the
// double-submit CSRF guard (header + cookie must match): the CLI mints a
// random token per request and presents it both ways, mirroring the
// browser client. Used by the managed-clusters lane.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
)

const csrfHeaderName = "X-Ankra-CSRF"

func (c *Client) postCSRFJSON(requestURL string, requestBody interface{}, target interface{}, operation string) error {
	payload, err := marshalOptionalJSON(requestBody)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	c.applyAuthAndCSRFHeaders(request)
	return c.doJSON(request, target, operation)
}

func (c *Client) patchCSRFJSON(requestURL string, requestBody interface{}, target interface{}, operation string) error {
	payload, err := marshalOptionalJSON(requestBody)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPatch, requestURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	c.applyAuthAndCSRFHeaders(request)
	return c.doJSON(request, target, operation)
}

func (c *Client) deleteCSRFJSON(requestURL string, target interface{}, operation string) error {
	request, err := http.NewRequest(http.MethodDelete, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.applyAuthAndCSRFHeaders(request)
	return c.doJSON(request, target, operation)
}

func (c *Client) applyAuthAndCSRFHeaders(request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+c.Token)
	csrfToken := generateClientCSRFToken()
	request.Header.Set(csrfHeaderName, csrfToken)
	request.AddCookie(&http.Cookie{Name: "ankra_csrf", Value: csrfToken})
}

func (c *Client) doJSON(request *http.Request, target interface{}, operation string) error {
	return c.doJSONWithClient(c.HTTP, request, target, operation)
}

// doJSONWithClient is doJSON against a caller-chosen http.Client, so a
// long synchronous write can ride a transport without the shared 30s
// response-header deadline (see slow_write.go) while keeping one copy of
// the status and body handling.
func (c *Client) doJSONWithClient(
	httpClient *http.Client,
	request *http.Request,
	target interface{},
	operation string,
) error {
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer closeBody(response)

	body, err := readResponseBody(response)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		detail := detailFromBody(body)
		if detail != "" {
			return newBackendDetailError(response.StatusCode, detail)
		}
		return newUnexpectedResponseError(operation, response.StatusCode, redactedBodyForError(body, 500))
	}
	if target == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

func marshalOptionalJSON(value interface{}) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return payload, nil
}

func generateClientCSRFToken() string {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "ankra-cli-csrf"
	}
	return base64.RawURLEncoding.EncodeToString(tokenBytes)
}
func (c *Client) putCSRFJSON(requestURL string, requestBody interface{}, target interface{}, operation string) error {
	payload, err := marshalOptionalJSON(requestBody)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPut, requestURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	c.applyAuthAndCSRFHeaders(request)
	return c.doJSON(request, target, operation)
}

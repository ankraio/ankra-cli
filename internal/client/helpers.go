package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const maxResponseBodySize = 10 * 1024 * 1024

type Pagination struct {
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
}

func readResponseBody(resp *http.Response) ([]byte, error) {
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
}

// truncateForError truncates a body string for inclusion in an error
// message. Use redactedBodyForError for any caller that may render the
// response into stdout/stderr or onwards to logs.
func truncateForError(body []byte, maxLen int) string {
	s := string(body)
	if len(s) > maxLen {
		return s[:maxLen] + "... (truncated)"
	}
	return s
}

// redactedBodyForError redacts likely secret material in a JSON response
// body and returns a truncated string suitable for inclusion in an error
// message. The redaction is a best-effort defensive measure: API responses
// should not echo secrets back, but the CLI surfaces these strings to
// terminals, CI logs, and bug reports, so the small overhead is justified.
func redactedBodyForError(body []byte, maxLen int) string {
	redacted := redactSensitiveJSON(body)
	return truncateForError(redacted, maxLen)
}

func closeBody(resp *http.Response) {
	if closeErr := resp.Body.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to close response body: %v\n", closeErr)
	}
}

func (c *Client) getJSON(url string, target interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer closeBody(resp)
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := readResponseBody(resp)
		if denied := PermissionDeniedFromResponse(resp.StatusCode, body); denied != nil {
			return denied
		}
		return newUnexpectedResponseErrorWithMessage(resp.StatusCode, fmt.Sprintf("unexpected status: %s", resp.Status))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBodySize)).Decode(target)
}

func parseJSON(data []byte, target interface{}) error {
	return json.Unmarshal(data, target)
}

// detailFromBody extracts the FastAPI `detail` string from a JSON error
// body, returning "" when the body is not JSON or carries no string detail.
// FastAPI's HTTPException serialises its message as {"detail": "..."}, so
// surfacing it gives the user the backend's human-readable reason (for
// example "A sync is already in progress for this registry.").
func detailFromBody(body []byte) string {
	var parsed struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return parsed.Detail
}

// PermissionDeniedFromResponse parses the platform RBAC 403 body
// ({"detail": "permission_denied", "permission": ...}) into a
// *PermissionDeniedError; nil for any other body, so legacy 403s keep
// their existing handling.
func PermissionDeniedFromResponse(statusCode int, body []byte) *PermissionDeniedError {
	if statusCode != http.StatusForbidden {
		return nil
	}
	var parsed struct {
		Detail     string `json:"detail"`
		Permission string `json:"permission"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Detail != "permission_denied" {
		return nil
	}
	return &PermissionDeniedError{Permission: parsed.Permission}
}

// sensitiveKeyFragments lists case-insensitive substrings that mark a JSON
// field as carrying credential material. A match triggers redaction of the
// associated value before the body is included in any error message.
var sensitiveKeyFragments = []string{
	"token",
	"secret",
	"password",
	"passwd",
	"authorization",
	"private_key",
	"privatekey",
	"api_key",
	"apikey",
	"access_key",
	"accesskey",
	"client_secret",
	"clientsecret",
	"consumer_key",
	"consumerkey",
	"refresh_token",
	"refreshtoken",
	"id_token",
	"idtoken",
	"session",
	"cookie",
	"credential",
}

// redactSensitiveJSON walks a JSON document (if the body parses as JSON)
// and replaces values whose key contains any sensitive-key fragment with
// "<redacted>". If the body is not valid JSON or the walk fails, the
// original body is returned unchanged.
func redactSensitiveJSON(body []byte) []byte {
	trimmed := []byte(strings.TrimSpace(string(body)))
	if len(trimmed) == 0 {
		return body
	}
	switch trimmed[0] {
	case '{', '[':
	default:
		return body
	}

	var decoded interface{}
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return body
	}
	redacted := redactJSONValue(decoded, false)

	var buf strings.Builder
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(redacted); err != nil {
		return body
	}
	out := strings.TrimRight(buf.String(), "\n")
	return []byte(out)
}

func redactJSONValue(value interface{}, parentSensitive bool) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			childSensitive := parentSensitive || keyLooksSensitive(key)
			out[key] = redactJSONValue(child, childSensitive)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, child := range typed {
			out[index] = redactJSONValue(child, parentSensitive)
		}
		return out
	default:
		if parentSensitive && value != nil {
			return "<redacted>"
		}
		return value
	}
}

func keyLooksSensitive(key string) bool {
	lower := strings.ToLower(key)
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

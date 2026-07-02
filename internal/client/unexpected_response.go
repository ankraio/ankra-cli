package client

import (
	"errors"
	"fmt"
)

// UnexpectedResponseError marks an API response the CLI has no specific
// handling for (the default branch of a status-code switch). cmd.Execute
// pattern-matches on it to suggest filing a bug via `ankra support create`.
// The message preserves the historical "<operation>: status <code>, body:
// <body>" shape so existing output and tests are unaffected.
type UnexpectedResponseError struct {
	StatusCode int
	message    string
}

func (e *UnexpectedResponseError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func newUnexpectedResponseError(operation string, statusCode int, bodyForDisplay string) *UnexpectedResponseError {
	return &UnexpectedResponseError{
		StatusCode: statusCode,
		message:    fmt.Sprintf("%s: status %d, body: %s", operation, statusCode, bodyForDisplay),
	}
}

func newUnexpectedResponseErrorWithMessage(statusCode int, message string) *UnexpectedResponseError {
	return &UnexpectedResponseError{
		StatusCode: statusCode,
		message:    message,
	}
}

// ErrUnauthorized is returned for 401 API responses so callers — and the
// CLI's exit-code classification — can detect authentication failures with
// errors.Is instead of matching message text.
var ErrUnauthorized = errors.New("unauthorized. Run `ankra login` to re-authenticate")

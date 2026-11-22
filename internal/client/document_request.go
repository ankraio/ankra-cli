package client

import (
	"context"
	"encoding/json"
)

// requestDocument sends one JSON request and returns the 2xx answer as the
// platform sent it. A refusal is sendJSON's own error: ErrUnauthorized, a
// *PermissionDeniedError, or an *UnexpectedResponseError carrying the
// platform's detail.
func (c *Client) requestDocument(ctx context.Context, method string, path string, payload any) (json.RawMessage, error) {
	var document json.RawMessage
	if requestError := c.sendJSONContext(ctx, method, c.BaseURL+path, payload, &document); requestError != nil {
		return nil, requestError
	}
	return document, nil
}

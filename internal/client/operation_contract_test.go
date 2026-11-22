package client

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// operationRequest is what the stand-in platform received for one call.
type operationRequest struct {
	line          string
	body          string
	authorization string
	headers       string
}

// operationClient answers every request with status and answer and records
// the last one.
func operationClient(t *testing.T, status int, answer string) (*Client, *operationRequest) {
	t.Helper()
	received := &operationRequest{}
	apiClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		headers := &strings.Builder{}
		_ = r.Header.Write(headers)
		*received = operationRequest{line: r.Method + " " + r.RequestURI, body: string(body),
			authorization: r.Header.Get("Authorization"), headers: headers.String()}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	})
	return apiClient, received
}

// operationCase is one document route: the call, and the request line and
// body it has to send.
type operationCase struct {
	name     string
	call     func(c *Client) (json.RawMessage, error)
	wantLine string
	wantBody string
}

// requireOperationContract makes each call against a stand-in platform and
// fails unless it sent its contract and returned the answer as it was sent.
func requireOperationContract(t *testing.T, cases []operationCase) {
	t.Helper()
	const answer = `{"id":"1","field_from_a_newer_platform":true}`
	for _, testCase := range cases {
		apiClient, received := operationClient(t, http.StatusOK, answer)
		document, callError := testCase.call(apiClient)
		if callError != nil || string(document) != answer {
			t.Errorf("%s: answer = %s, %v; want the body as answered", testCase.name, document, callError)
		}
		if received.line != testCase.wantLine || received.body != testCase.wantBody {
			t.Errorf("%s: sent %s %s, want %s %s", testCase.name, received.line, received.body, testCase.wantLine, testCase.wantBody)
		}
		if received.authorization != "Bearer "+testToken {
			t.Errorf("%s: authorization = %q", testCase.name, received.authorization)
		}
	}
}

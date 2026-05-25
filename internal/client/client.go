package client

import (
	"net/http"
	"time"
)

type Client struct {
	Token         string
	BaseURL       string
	HTTP          *http.Client
	StreamingHTTP *http.Client
}

// New constructs a Client with the supplied token and a base URL that is
// expected to already be validated and normalized via NormalizeBaseURL.
func New(token, baseURL string) *Client {
	sharedTransport := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &Client{
		Token:   token,
		BaseURL: baseURL,
		HTTP: &http.Client{
			Timeout:   5 * time.Minute,
			Transport: sharedTransport,
		},
		StreamingHTTP: &http.Client{
			Transport: sharedTransport,
		},
	}
}

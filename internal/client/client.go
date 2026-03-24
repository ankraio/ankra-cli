package client

import (
	"net/http"
	"strings"
	"time"
)

type Client struct {
	Token         string
	BaseURL       string
	HTTP          *http.Client
	StreamingHTTP *http.Client
}

func New(token, baseURL string) *Client {
	sharedTransport := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &Client{
		Token:   token,
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP: &http.Client{
			Timeout:   5 * time.Minute,
			Transport: sharedTransport,
		},
		StreamingHTTP: &http.Client{
			Transport: sharedTransport,
		},
	}
}

package client

import (
	"net/http"
	"strings"
)

type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

func New(token, baseURL string) *Client {
	return &Client{
		Token:   token,
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{},
	}
}

package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

func truncateForError(body []byte, maxLen int) string {
	s := string(body)
	if len(s) > maxLen {
		return s[:maxLen] + "... (truncated)"
	}
	return s
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
		return fmt.Errorf("unauthorized. Run `ankra login` to re-authenticate")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBodySize)).Decode(target)
}

func parseJSON(data []byte, target interface{}) error {
	return json.Unmarshal(data, target)
}

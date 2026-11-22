package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// GitopsPreservedDiffPage contains structural metadata only. Unknown response
// fields are deliberately not retained in a RawDocument or printed to users.
type GitopsPreservedDiffPage struct {
	Path                 string                      `json:"path" yaml:"path"`
	HeadContentHash      string                      `json:"head_content_hash" yaml:"head_content_hash"`
	RenderContentHash    string                      `json:"render_content_hash" yaml:"render_content_hash"`
	HeadCommitSHA        *string                     `json:"head_commit_sha" yaml:"head_commit_sha"`
	Status               string                      `json:"status" yaml:"status"`
	Changes              []GitopsPreservedPathChange `json:"changes" yaml:"changes"`
	Total                int                         `json:"total" yaml:"total"`
	Offset               int                         `json:"offset" yaml:"offset"`
	HasMore              bool                        `json:"has_more" yaml:"has_more"`
	NextCursor           *string                     `json:"next_cursor" yaml:"next_cursor"`
	StructuralDiffValues string                      `json:"structural_diff_values" yaml:"structural_diff_values"`
	ValuesWithheldReason *string                     `json:"values_withheld_reason" yaml:"values_withheld_reason"`
}

// CompleteClusterGitopsPreservedDiff reads every page before returning anything.
// A changed, incomplete or malformed continuation returns no partial result.
func (c *Client) CompleteClusterGitopsPreservedDiff(ctx context.Context, clusterID, path string) (*GitopsPreservedDiffPage, error) {
	if strings.TrimSpace(clusterID) == "" || strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("a cluster and preserved repository path are required")
	}
	var result *GitopsPreservedDiffPage
	cursor := ""
	seen := map[string]bool{}
	bytesRead := 0
	for pageCount := 0; pageCount < 10000; pageCount++ {
		query := url.Values{"path": []string{path}, "limit": []string{"200"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var page GitopsPreservedDiffPage
		if readError := c.sendJSONContext(ctx, http.MethodGet, clusterGitopsPreservedPathsURL(c.BaseURL, clusterID)+"/diff?"+query.Encode(), nil, &page); readError != nil {
			return nil, readError
		}
		encoded, encodeError := json.Marshal(page)
		if encodeError != nil {
			return nil, fmt.Errorf("checking complete diff metadata size: %w", encodeError)
		}
		bytesRead += len(encoded)
		if bytesRead > 32<<20 {
			return nil, fmt.Errorf("complete preserved diff exceeded its metadata budget; no partial result was returned")
		}
		if page.Path != path || page.HeadContentHash == "" || page.RenderContentHash == "" || page.Total < 0 || len(page.Changes) > 200 ||
			(page.Status != GitopsPreservedDiffComputed && page.Status != GitopsPreservedDiffFormattingOnly) {
			return nil, fmt.Errorf("invalid complete preserved diff metadata; no partial result was returned")
		}
		switch page.StructuralDiffValues {
		case GitopsPreservedDiffValuesRevealed, GitopsPreservedDiffValuesPermissionRequired, GitopsPreservedDiffValuesUnavailable:
		default:
			return nil, fmt.Errorf("unknown preserved diff reveal outcome; no partial result was returned")
		}
		for _, change := range page.Changes {
			if (page.StructuralDiffValues != GitopsPreservedDiffValuesRevealed || change.ValueMasked) && (change.GitValue != nil || change.AnkraValue != nil) {
				return nil, fmt.Errorf("preserved diff violated its masking boundary; no partial result was returned")
			}
		}
		if result == nil {
			if page.Offset != 0 {
				return nil, fmt.Errorf("complete preserved diff did not start at the first change")
			}
			aggregate := page
			aggregate.Changes = []GitopsPreservedPathChange{}
			result = &aggregate
		} else if page.Offset != len(result.Changes) || page.Total != result.Total || page.Status != result.Status || page.HeadContentHash != result.HeadContentHash ||
			page.RenderContentHash != result.RenderContentHash || optionalDiffText(page.HeadCommitSHA) != optionalDiffText(result.HeadCommitSHA) ||
			page.StructuralDiffValues != result.StructuralDiffValues || optionalDiffText(page.ValuesWithheldReason) != optionalDiffText(result.ValuesWithheldReason) {
			return nil, fmt.Errorf("preserved diff changed during pagination; restart from the first page")
		}
		if len(result.Changes)+len(page.Changes) > result.Total {
			return nil, fmt.Errorf("preserved diff returned more changes than its total")
		}
		result.Changes = append(result.Changes, page.Changes...)
		if !page.HasMore {
			if len(result.Changes) != result.Total || page.NextCursor != nil {
				return nil, fmt.Errorf("preserved diff ended before its complete total; no partial result was returned")
			}
			result.HasMore = false
			result.NextCursor = nil
			result.Offset = 0
			return result, nil
		}
		if len(page.Changes) == 0 || page.NextCursor == nil || *page.NextCursor == "" || len(*page.NextCursor) > 2048 || seen[*page.NextCursor] {
			return nil, fmt.Errorf("preserved diff continuation made no progress; no partial result was returned")
		}
		cursor = *page.NextCursor
		seen[cursor] = true
	}
	return nil, fmt.Errorf("complete preserved diff exceeded its page budget; no partial result was returned")
}

func optionalDiffText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

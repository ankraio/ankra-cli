package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func completeDiffTestPage(offset, end, total int) GitopsPreservedDiffPage {
	page := GitopsPreservedDiffPage{Path: "clusters/prod/a b.yaml", HeadContentHash: "head", RenderContentHash: "render",
		HeadCommitSHA: strPtr("commit"), Status: GitopsPreservedDiffComputed, Total: total, Offset: offset,
		Changes: []GitopsPreservedPathChange{}, StructuralDiffValues: GitopsPreservedDiffValuesRevealed}
	for index := offset; index < end; index++ {
		page.Changes = append(page.Changes, GitopsPreservedPathChange{Path: fmt.Sprintf("spec.values[%d]", index),
			Change: GitopsPreservedChangeChanged, GitValue: strPtr("old"), AnkraValue: strPtr("new")})
	}
	page.HasMore = end < total
	if page.HasMore {
		page.NextCursor = strPtr(fmt.Sprintf("cursor-%d", end))
	}
	return page
}

func TestCompletePreservedDiffReadsEveryPageWithoutRetainingUnknownFields(t *testing.T) {
	requests := 0
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/org/clusters/cluster-1/gitops/preserved-paths/diff" ||
			request.URL.Query().Get("path") != "clusters/prod/a b.yaml" || request.URL.Query().Get("limit") != "200" {
			t.Errorf("incorrect paged request: %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Error("missing bearer authentication")
		}
		offset := requests * 50
		if offset == 0 && request.URL.Query().Get("cursor") != "" || offset > 0 && request.URL.Query().Get("cursor") != fmt.Sprintf("cursor-%d", offset) {
			t.Errorf("incorrect continuation at %d", offset)
		}
		requests++
		page := completeDiffTestPage(offset, min(offset+50, 108), 108)
		page.Changes[0].ValueMasked = true
		page.Changes[0].GitValue, page.Changes[0].AnkraValue = nil, nil
		encoded, _ := json.Marshal(page)
		// A future server can add fields, but the CLI must not echo raw files
		// or credentials accidentally added to a response.
		encoded = append(encoded[:len(encoded)-1], []byte(`,"head_content":"synthetic-private-file","temp_clone_token":"synthetic-private-token"}`)...)
		_, _ = writer.Write(encoded)
	})
	result, readError := testClient.CompleteClusterGitopsPreservedDiff(context.Background(), "cluster-1", "clusters/prod/a b.yaml")
	if readError != nil {
		t.Fatal(readError)
	}
	if requests != 3 || len(result.Changes) != 108 || result.HasMore || result.NextCursor != nil || result.Offset != 0 {
		t.Fatalf("incomplete result: %#v", result)
	}
	for index, change := range result.Changes {
		if change.Path != fmt.Sprintf("spec.values[%d]", index) {
			t.Fatalf("gap or duplicate at %d: %#v", index, change)
		}
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "synthetic-private") || strings.Contains(string(encoded), "head_content\"") {
		t.Fatal("unknown response fields leaked into structured output")
	}
}

func TestCompletePreservedDiffRefusesEveryBrokenContinuationWithoutPartialResult(t *testing.T) {
	cases := map[string]func(*GitopsPreservedDiffPage){
		"offset gap":     func(page *GitopsPreservedDiffPage) { page.Offset++ },
		"path changed":   func(page *GitopsPreservedDiffPage) { page.Path = "another.yaml" },
		"git changed":    func(page *GitopsPreservedDiffPage) { page.HeadContentHash = "new" },
		"render changed": func(page *GitopsPreservedDiffPage) { page.RenderContentHash = "new" },
		"commit changed": func(page *GitopsPreservedDiffPage) { page.HeadCommitSHA = strPtr("new") },
		"total changed":  func(page *GitopsPreservedDiffPage) { page.Total++ },
		"status changed": func(page *GitopsPreservedDiffPage) { page.Status = GitopsPreservedDiffFormattingOnly },
		"permission changed": func(page *GitopsPreservedDiffPage) {
			page.StructuralDiffValues = GitopsPreservedDiffValuesPermissionRequired
		},
		"reason changed":        func(page *GitopsPreservedDiffPage) { page.ValuesWithheldReason = strPtr("unavailable") },
		"unknown reveal mode":   func(page *GitopsPreservedDiffPage) { page.StructuralDiffValues = "unknown" },
		"unavailable metadata":  func(page *GitopsPreservedDiffPage) { page.Status = "not_recorded" },
		"masked value returned": func(page *GitopsPreservedDiffPage) { page.Changes[0].ValueMasked = true },
		"early end":             func(page *GitopsPreservedDiffPage) { page.Changes = page.Changes[:1] },
		"repeated cursor":       func(page *GitopsPreservedDiffPage) { page.HasMore = true; page.NextCursor = strPtr("cursor-2") },
		"empty progress": func(page *GitopsPreservedDiffPage) {
			page.HasMore = true
			page.NextCursor = strPtr("cursor-4")
			page.Changes = nil
		},
		"missing cursor":  func(page *GitopsPreservedDiffPage) { page.HasMore = true; page.NextCursor = nil },
		"trailing cursor": func(page *GitopsPreservedDiffPage) { page.NextCursor = strPtr("unexpected") },
	}
	for name, breakPage := range cases {
		t.Run(name, func(t *testing.T) {
			requests := 0
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				page := completeDiffTestPage(0, 2, 4)
				if requests > 0 {
					page = completeDiffTestPage(2, 4, 4)
					breakPage(&page)
				}
				requests++
				_ = json.NewEncoder(writer).Encode(page)
			})
			result, readError := testClient.CompleteClusterGitopsPreservedDiff(context.Background(), "cluster-1", "clusters/prod/a b.yaml")
			if readError == nil || result != nil {
				t.Fatalf("broken continuation returned a result: %#v, %v", result, readError)
			}
		})
	}
}

func TestCompletePreservedDiffPreservesPageErrorsAndPermissionMasking(t *testing.T) {
	t.Run("conflict after first page", func(t *testing.T) {
		requests := 0
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			requests++
			if requests == 1 {
				_ = json.NewEncoder(writer).Encode(completeDiffTestPage(0, 2, 4))
				return
			}
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(`{"detail":"snapshot changed"}`))
		})
		result, readError := testClient.CompleteClusterGitopsPreservedDiff(context.Background(), "cluster-1", "clusters/prod/a b.yaml")
		if readError == nil || result != nil || !strings.Contains(readError.Error(), "snapshot changed") {
			t.Fatalf("result=%#v error=%v", result, readError)
		}
	})
	t.Run("masked complete page", func(t *testing.T) {
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			page := completeDiffTestPage(0, 1, 1)
			page.StructuralDiffValues = GitopsPreservedDiffValuesPermissionRequired
			page.Changes[0].GitValue, page.Changes[0].AnkraValue = nil, nil
			page.Changes[0].ValueMasked = true
			_ = json.NewEncoder(writer).Encode(page)
		})
		result, readError := testClient.CompleteClusterGitopsPreservedDiff(context.Background(), "cluster-1", "clusters/prod/a b.yaml")
		if readError != nil || result == nil || !result.Changes[0].ValueMasked {
			t.Fatalf("result=%#v error=%v", result, readError)
		}
	})
	t.Run("zero differences", func(t *testing.T) {
		testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
			page := completeDiffTestPage(0, 0, 0)
			page.Status = GitopsPreservedDiffFormattingOnly
			_ = json.NewEncoder(writer).Encode(page)
		})
		result, readError := testClient.CompleteClusterGitopsPreservedDiff(context.Background(), "cluster-1", "clusters/prod/a b.yaml")
		if readError != nil || result == nil || result.Total != 0 {
			t.Fatalf("result=%#v error=%v", result, readError)
		}
	})
}

func TestCompletePreservedDiffValidatesInputsBeforeSending(t *testing.T) {
	requests := 0
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) { requests++ })
	for _, args := range [][2]string{{"", "a.yaml"}, {"cluster-1", " "}} {
		result, readError := testClient.CompleteClusterGitopsPreservedDiff(context.Background(), args[0], args[1])
		if result != nil || readError == nil {
			t.Fatal("missing input accepted")
		}
	}
	if requests != 0 {
		t.Fatal("invalid inputs sent a request")
	}
}

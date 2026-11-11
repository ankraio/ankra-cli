package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"testing"
)

// These pin the method, path, query and body of the requests behind the
// commands added for ankra-hq41xb.22. A path typo fails as a
// 404 against a real platform and passes every test that only checks the
// command wiring, so the assertions are on the request itself.
func TestOperationParityRequests(t *testing.T) {
	ctx := context.Background()
	title, visibility, parent := "Final", "organisation", "comment-1"
	for _, testCase := range []struct {
		name   string
		method string
		target string
		body   string
		call   func(testClient *Client) error
	}{
		{"timeline", http.MethodGet, "/api/v1/org/applications/app-1/timeline?before=cursor-1&limit=20", "",
			func(testClient *Client) error {
				_, callError := testClient.GetApplicationTimeline(ctx, "app-1", neturl.Values{"limit": {"20"}, "before": {"cursor-1"}})
				return callError
			}},
		{"launch plan", http.MethodGet, "/api/v1/org/applications/app-1/launch-plan?cluster_id=cluster-1", "",
			func(testClient *Client) error {
				_, callError := testClient.GetApplicationLaunchPlan(ctx, "app-1", neturl.Values{"cluster_id": {"cluster-1"}})
				return callError
			}},
		{"list canvases", http.MethodGet, "/api/v1/org/canvases?cursor=c-1&kind=page&limit=10&pinned=true&query=report", "",
			func(testClient *Client) error {
				_, callError := testClient.ListCanvases(ctx, CanvasListOptions{Cursor: "c-1", Limit: 10, Query: "report", Kind: "page", IsPinnedOnly: true})
				return callError
			}},
		{"list canvases with defaults", http.MethodGet, "/api/v1/org/canvases", "",
			func(testClient *Client) error {
				_, callError := testClient.ListCanvases(ctx, CanvasListOptions{})
				return callError
			}},
		{"get canvas", http.MethodGet, "/api/v1/org/canvases/canvas-1", "",
			func(testClient *Client) error {
				_, callError := testClient.GetCanvas(ctx, "canvas-1")
				return callError
			}},
		{"create canvas", http.MethodPost, "/api/v1/org/canvases",
			`{"kind":"page","title":"Report","language":"html","content":"hello"}`,
			func(testClient *Client) error {
				_, callError := testClient.CreateCanvas(ctx, CreateCanvasRequest{Kind: "page", Title: "Report", Language: "html", Content: "hello"})
				return callError
			}},
		{"update canvas", http.MethodPatch, "/api/v1/org/canvases/canvas-1", `{"title":"Final","visibility":"organisation"}`,
			func(testClient *Client) error {
				_, callError := testClient.UpdateCanvas(ctx, "canvas-1", UpdateCanvasRequest{Title: &title, Visibility: &visibility})
				return callError
			}},
		{"update canvas title only", http.MethodPatch, "/api/v1/org/canvases/canvas-1", `{"title":"Final"}`,
			func(testClient *Client) error {
				_, callError := testClient.UpdateCanvas(ctx, "canvas-1", UpdateCanvasRequest{Title: &title})
				return callError
			}},
		{"delete canvas", http.MethodDelete, "/api/v1/org/canvases/canvas-1", "",
			func(testClient *Client) error { return testClient.DeleteCanvas(ctx, "canvas-1") }},
		{"list revisions", http.MethodGet, "/api/v1/org/canvases/canvas-1/revisions?limit=5", "",
			func(testClient *Client) error {
				_, callError := testClient.ListCanvasRevisions(ctx, "canvas-1", "", 5)
				return callError
			}},
		{"get revision", http.MethodGet, "/api/v1/org/canvases/canvas-1/revisions/revision-2", "",
			func(testClient *Client) error {
				_, callError := testClient.GetCanvasRevision(ctx, "canvas-1", "revision-2")
				return callError
			}},
		{"list comments", http.MethodGet, "/api/v1/org/canvases/canvas-1/comments?include_resolved=true", "",
			func(testClient *Client) error {
				_, callError := testClient.ListCanvasComments(ctx, "canvas-1", "", 0, true)
				return callError
			}},
		{"add comment", http.MethodPost, "/api/v1/org/canvases/canvas-1/comments", `{"body":"Fixed","parent_comment_id":"comment-1"}`,
			func(testClient *Client) error {
				_, callError := testClient.CreateCanvasComment(ctx, "canvas-1", CreateCanvasCommentRequest{Body: "Fixed", ParentCommentID: &parent})
				return callError
			}},
		{"resolve comment", http.MethodPost, "/api/v1/org/canvases/canvas-1/comments/comment-1/resolve", "",
			func(testClient *Client) error {
				_, callError := testClient.ResolveCanvasComment(ctx, "canvas-1", "comment-1")
				return callError
			}},
		{"reopen comment", http.MethodPost, "/api/v1/org/canvases/canvas-1/comments/comment-1/reopen", "",
			func(testClient *Client) error {
				_, callError := testClient.ReopenCanvasComment(ctx, "canvas-1", "comment-1")
				return callError
			}},
		{"list plans", http.MethodGet, "/api/v1/chat/plans?conversation_id=conversation-1", "",
			func(testClient *Client) error {
				_, callError := testClient.ListChatPlans(ctx, "conversation-1")
				return callError
			}},
		{"get plan", http.MethodGet, "/api/v1/chat/plans/plan-1", "",
			func(testClient *Client) error {
				_, callError := testClient.GetChatPlan(ctx, "plan-1")
				return callError
			}},
		{"reject plan", http.MethodPost, "/api/v1/chat/plans/plan-1/reject", `{"reason":"not now"}`,
			func(testClient *Client) error {
				_, callError := testClient.RejectChatPlan(ctx, "plan-1", "not now")
				return callError
			}},
		{"reject plan without a reason", http.MethodPost, "/api/v1/chat/plans/plan-1/reject", `{}`,
			func(testClient *Client) error {
				_, callError := testClient.RejectChatPlan(ctx, "plan-1", "")
				return callError
			}},
		{"approve plan", http.MethodPost, "/api/v1/chat/plans/plan-1/approve", `{"acknowledged_step_orders":[2,4]}`,
			func(testClient *Client) error {
				return testClient.ApproveChatPlan(ctx, "plan-1", []int{2, 4}, func(ChatStreamEvent) {})
			}},
		{"repository release queue", http.MethodGet, "/api/v1/org/pipeline-repositories/repo-1/pipeline-release-queue", "",
			func(testClient *Client) error {
				_, callError := testClient.GetPipelineReleaseQueue(ctx, PipelineSelector{RepositoryID: "repo-1"})
				return callError
			}},
		{"application release queue", http.MethodGet, "/api/v1/org/applications/app-1/pipeline-release-queue", "",
			func(testClient *Client) error {
				_, callError := testClient.GetPipelineReleaseQueue(ctx, PipelineSelector{ApplicationID: "app-1"})
				return callError
			}},
		{"repository resource suggestions", http.MethodGet,
			"/api/v1/org/pipeline-repositories/repo-1/pipeline-runs/run-9/resource-suggestions?window=50", "",
			func(testClient *Client) error {
				_, callError := testClient.GetPipelineRunResourceSuggestions(ctx, PipelineSelector{RepositoryID: "repo-1"}, "run-9", 50)
				return callError
			}},
		{"application resource suggestions", http.MethodGet,
			"/api/v1/org/applications/app-1/pipeline-runs/run-9/resource-suggestions", "",
			func(testClient *Client) error {
				_, callError := testClient.GetPipelineRunResourceSuggestions(ctx, PipelineSelector{ApplicationID: "app-1"}, "run-9", 0)
				return callError
			}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requests := 0
			testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
				requests++
				if request.Method != testCase.method {
					t.Errorf("method = %s, want %s", request.Method, testCase.method)
				}
				if target := request.URL.RequestURI(); target != testCase.target {
					t.Errorf("target = %s, want %s", target, testCase.target)
				}
				if request.Header.Get("Authorization") != "Bearer "+testToken {
					t.Errorf("the request carries no bearer token")
				}
				sent, _ := io.ReadAll(request.Body)
				if strings.TrimSpace(string(sent)) != testCase.body {
					t.Errorf("body = %s, want %s", sent, testCase.body)
				}
				if request.Method == http.MethodDelete {
					writer.WriteHeader(http.StatusNoContent)
					return
				}
				_, _ = fmt.Fprint(writer, `{}`)
			})
			if callError := testCase.call(testClient); callError != nil {
				t.Fatalf("call failed: %v", callError)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
}

func TestCanvasRequestsMapRefusals(t *testing.T) {
	for name, testCase := range map[string]struct {
		status int
		body   string
		check  func(callError error) bool
	}{
		"not found": {http.StatusNotFound,
			`{"detail":"canvas_not_found","reason":"canvas_not_found","code":"canvas_not_found","message":"No such canvas"}`,
			func(callError error) bool {
				var refusal *CanvasPagesError
				return errors.As(callError, &refusal) && refusal.Code == "canvas_not_found" && refusal.StatusCode == http.StatusNotFound
			}},
		"another member's": {http.StatusForbidden, `{"detail":"canvas_owned_by_another_member","reason":"canvas_owned_by_another_member"}`,
			func(callError error) bool {
				var refusal *CanvasPagesError
				return errors.As(callError, &refusal) && refusal.Code == "canvas_owned_by_another_member"
			}},
		"no permission": {http.StatusForbidden, `{"detail":"permission_denied","permission":"canvases.read"}`,
			func(callError error) bool {
				var denied *PermissionDeniedError
				return errors.As(callError, &denied) && denied.Permission == "canvases.read"
			}},
		"unauthorised": {http.StatusUnauthorized, `{"detail":"Unauthorized"}`,
			func(callError error) bool { return errors.Is(callError, ErrUnauthorized) }},
		"a body with nothing to say": {http.StatusInternalServerError, `oops`,
			func(callError error) bool {
				var unexpected *UnexpectedResponseError
				return errors.As(callError, &unexpected) && unexpected.StatusCode == http.StatusInternalServerError
			}},
	} {
		t.Run(name, func(t *testing.T) {
			testClient := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(testCase.status)
				_, _ = fmt.Fprint(writer, testCase.body)
			})
			_, callError := testClient.GetCanvas(context.Background(), "canvas-1")
			if callError == nil || !testCase.check(callError) {
				t.Fatalf("error = %#v", callError)
			}
		})
	}
}

func TestApproveChatPlanReadsTheStreamFrameByFrame(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "data: {\"type\":\"plan_step_result\",\"schema_version\":1,\"data\":{\"step_order\":1,\"status\":\"completed\"}}\n\n"+
			": heartbeat\n\n"+
			"data: {\"type\":\"plan_complete\",\"schema_version\":1,\"data\":{\"status\":\"completed\"}}\n\n")
	})
	var events []ChatStreamEvent
	approveError := testClient.ApproveChatPlan(context.Background(), "plan-1", nil, func(event ChatStreamEvent) {
		events = append(events, event)
	})
	if approveError != nil {
		t.Fatalf("ApproveChatPlan: %v", approveError)
	}
	if len(events) != 2 || events[0].Type != ChatPlanEventStepResult || events[1].Type != ChatPlanEventComplete {
		t.Fatalf("events = %+v", events)
	}
	if data, _ := events[1].Data.(map[string]any); data["status"] != "completed" {
		t.Errorf("the complete frame lost its data: %+v", events[1])
	}
}

func TestApproveChatPlanSendsAnEmptyBodyObjectWithoutAcknowledgements(t *testing.T) {
	testClient := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if decodeError := json.NewDecoder(request.Body).Decode(&body); decodeError != nil || len(body) != 0 {
			t.Errorf("body = %v (%v), want an empty object", body, decodeError)
		}
	})
	if approveError := testClient.ApproveChatPlan(context.Background(), "plan-1", nil, func(ChatStreamEvent) {}); approveError != nil {
		t.Fatalf("ApproveChatPlan: %v", approveError)
	}
}

func TestApproveChatPlanRefusals(t *testing.T) {
	acknowledgement := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(writer, `{"error_code":"PLAN_ACKNOWLEDGEMENT_REQUIRED","detail":"Acknowledge the steps.",
			"payload":{"plan_id":"plan-1","unacknowledged_steps":[{"step_order":2,"tool_name":"delete_pvc",
			"description":"Delete the old volume","risk_level":"high","reversible":false,"reason":"irreversible"}]}}`)
	})
	called := false
	approveError := acknowledgement.ApproveChatPlan(context.Background(), "plan-1", nil, func(ChatStreamEvent) { called = true })
	var required *ChatPlanAcknowledgementRequiredError
	if !errors.As(approveError, &required) || len(required.Steps) != 1 || required.Steps[0].StepOrder != 2 ||
		required.Steps[0].ToolName != "delete_pvc" || called {
		t.Fatalf("error = %#v, called = %v", approveError, called)
	}

	for status, check := range map[int]func(callError error) bool{
		http.StatusUnauthorized: func(callError error) bool { return errors.Is(callError, ErrUnauthorized) },
		http.StatusGone: func(callError error) bool {
			var unexpected *UnexpectedResponseError
			return errors.As(callError, &unexpected) && unexpected.StatusCode == http.StatusGone &&
				strings.Contains(callError.Error(), "predates")
		},
	} {
		refusing := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(status)
			_, _ = fmt.Fprint(writer, `{"detail":"This plan predates the platform upgrade."}`)
		})
		if callError := refusing.ApproveChatPlan(context.Background(), "plan-1", nil, func(ChatStreamEvent) {}); callError == nil || !check(callError) {
			t.Errorf("status %d: error = %#v", status, callError)
		}
	}
}

func TestPipelineReadsDecodeAndTreatA404AsUnavailable(t *testing.T) {
	serving := newTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/pipeline-release-queue") {
			_, _ = fmt.Fprint(writer, `{"repository_id":"repo-1","application_id":"app-1","branch":"main","queued":2,
				"running":1,"oldest_run_id":"run-7","checked_at":"2026-11-10T09:00:00Z","added_later":true}`)
			return
		}
		_, _ = fmt.Fprint(writer, `{"run_id":"run-9","definition_id":null,"window":20,
			"method":{"percentile":95,"headroom_percent":25,"minimum_samples":3},
			"stages":[{"stage":"build","sample_count":12,
			 "memory":{"status":"oversized","samples":12,"current_request_bytes":4294967296,"suggested":"2Gi"},
			 "cpu":{"status":"at_limit","samples":12,"throttled_share_p95":0.4,"suggested":null}}]}`)
	})
	queue, queueError := serving.GetPipelineReleaseQueue(context.Background(), PipelineSelector{RepositoryID: "repo-1"})
	if queueError != nil || queue.Queued != 2 || queue.Running != 1 || queue.OldestRunID != "run-7" || queue.Branch != "main" {
		t.Fatalf("queue = %+v, error = %v", queue, queueError)
	}
	if !strings.Contains(string(queue.RawJSON()), "added_later") {
		t.Errorf("the queue did not keep the platform's document: %s", queue.RawJSON())
	}
	suggestions, suggestionsError := serving.GetPipelineRunResourceSuggestions(context.Background(),
		PipelineSelector{RepositoryID: "repo-1"}, "run-9", 0)
	if suggestionsError != nil || len(suggestions.Stages) != 1 || suggestions.Method.Percentile != 95 {
		t.Fatalf("suggestions = %+v, error = %v", suggestions, suggestionsError)
	}
	stage := suggestions.Stages[0]
	if stage.Memory.Suggested == nil || *stage.Memory.Suggested != "2Gi" || stage.Memory.CurrentRequestBytes == nil ||
		*stage.Memory.CurrentRequestBytes != 4294967296 || stage.CPU.Suggested != nil || stage.CPU.Status != "at_limit" {
		t.Errorf("stage = %+v", stage)
	}

	missing := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(writer, `{"detail":"Not Found"}`)
	})
	if _, queueError = missing.GetPipelineReleaseQueue(context.Background(), PipelineSelector{ApplicationID: "app-1"}); !errors.Is(queueError, ErrPipelineReleaseQueueUnavailable) {
		t.Errorf("error = %v, want ErrPipelineReleaseQueueUnavailable", queueError)
	}
	if _, suggestionsError = missing.GetPipelineRunResourceSuggestions(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "run-9", 0); !errors.Is(suggestionsError, ErrPipelineResourceSuggestionsUnavailable) {
		t.Errorf("error = %v, want ErrPipelineResourceSuggestionsUnavailable", suggestionsError)
	}
	if _, selectorError := missing.GetPipelineReleaseQueue(context.Background(), PipelineSelector{}); !errors.Is(selectorError, ErrPipelineSelectorRequired) {
		t.Errorf("error = %v, want ErrPipelineSelectorRequired", selectorError)
	}
}

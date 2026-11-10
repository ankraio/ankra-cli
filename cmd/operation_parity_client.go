package cmd

import (
	"context"
	"encoding/json"
	"net/url"

	"ankra/internal/client"
)

// operationParityAPIClient is the part of APIClient behind the commands that
// closed operation-level gaps inside capabilities the CLI already served
// (ankra-hq41xb.22): the application timeline and Launch Plan, the canvas
// library, AI plans, and the pipeline release queue and resource suggestions.
type operationParityAPIClient interface {
	GetApplicationTimeline(requestContext context.Context, applicationID string, query url.Values) (json.RawMessage, error)
	GetApplicationLaunchPlan(requestContext context.Context, applicationID string, query url.Values) (json.RawMessage, error)

	ListCanvases(ctx context.Context, options client.CanvasListOptions) (json.RawMessage, error)
	GetCanvas(ctx context.Context, canvasID string) (json.RawMessage, error)
	CreateCanvas(ctx context.Context, request client.CreateCanvasRequest) (json.RawMessage, error)
	UpdateCanvas(ctx context.Context, canvasID string, request client.UpdateCanvasRequest) (json.RawMessage, error)
	DeleteCanvas(ctx context.Context, canvasID string) error
	ListCanvasRevisions(ctx context.Context, canvasID string, cursor string, limit int) (json.RawMessage, error)
	GetCanvasRevision(ctx context.Context, canvasID string, revisionID string) (json.RawMessage, error)
	ListCanvasComments(ctx context.Context, canvasID string, cursor string, limit int, includeResolved bool) (json.RawMessage, error)
	CreateCanvasComment(ctx context.Context, canvasID string, request client.CreateCanvasCommentRequest) (json.RawMessage, error)
	ResolveCanvasComment(ctx context.Context, canvasID string, commentID string) (json.RawMessage, error)
	ReopenCanvasComment(ctx context.Context, canvasID string, commentID string) (json.RawMessage, error)

	ListChatPlans(ctx context.Context, conversationID string) (json.RawMessage, error)
	GetChatPlan(ctx context.Context, planID string) (json.RawMessage, error)
	RejectChatPlan(ctx context.Context, planID string, reason string) (json.RawMessage, error)
	ApproveChatPlan(ctx context.Context, planID string, acknowledgedStepOrders []int, onEvent func(client.ChatStreamEvent)) error

	GetPipelineReleaseQueue(ctx context.Context, selector client.PipelineSelector) (*client.PipelineReleaseQueue, error)
	GetPipelineRunResourceSuggestions(ctx context.Context, selector client.PipelineSelector, runID string, window int) (*client.PipelineResourceSuggestions, error)
}

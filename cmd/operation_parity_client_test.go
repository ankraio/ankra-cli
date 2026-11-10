package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// runFreshCommand runs a newly built command tree against mock and answers
// what it wrote to stdout and to stderr. A fresh tree carries no flag values
// from an earlier test.
func runFreshCommand(t *testing.T, root *cobra.Command, mock APIClient, input string, arguments ...string) (string, string, error) {
	t.Helper()
	setMockClient(t, mock)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(input))
	root.SetArgs(arguments)
	root.SilenceUsage = true
	root.SilenceErrors = true
	executeError := root.Execute()
	return stdout.String(), stderr.String(), executeError
}

// The baseMock stubs of operationParityAPIClient. Every one refuses, so a
// test that reaches a call it did not mock fails on it.

var errOperationParityNotMocked = errors.New("not implemented")

func (m baseMock) GetApplicationTimeline(context.Context, string, url.Values) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) GetApplicationLaunchPlan(context.Context, string, url.Values) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) ListCanvases(context.Context, client.CanvasListOptions) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) GetCanvas(context.Context, string) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) CreateCanvas(context.Context, client.CreateCanvasRequest) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) UpdateCanvas(context.Context, string, client.UpdateCanvasRequest) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) DeleteCanvas(context.Context, string) error {
	return errOperationParityNotMocked
}

func (m baseMock) ListCanvasRevisions(context.Context, string, string, int) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) GetCanvasRevision(context.Context, string, string) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) ListCanvasComments(context.Context, string, string, int, bool) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) CreateCanvasComment(context.Context, string, client.CreateCanvasCommentRequest) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) ResolveCanvasComment(context.Context, string, string) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) ReopenCanvasComment(context.Context, string, string) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) ListChatPlans(context.Context, string) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) GetChatPlan(context.Context, string) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) RejectChatPlan(context.Context, string, string) (json.RawMessage, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) ApproveChatPlan(context.Context, string, []int, func(client.ChatStreamEvent)) error {
	return errOperationParityNotMocked
}

func (m baseMock) GetPipelineReleaseQueue(context.Context, client.PipelineSelector) (*client.PipelineReleaseQueue, error) {
	return nil, errOperationParityNotMocked
}

func (m baseMock) GetPipelineRunResourceSuggestions(context.Context, client.PipelineSelector, string, int) (*client.PipelineResourceSuggestions, error) {
	return nil, errOperationParityNotMocked
}

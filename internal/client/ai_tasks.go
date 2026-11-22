package client

// Agent tasks (cluster go/internal/aitasksapi): the organisation's AI agents
// that run on a schedule, on an alert or on demand. `ankra agents` reads the
// runs these produce; the routes here define, hire and control the agents
// themselves.
//
// Every method answers the platform's own document, so -o json prints what
// the platform sent rather than the subset the CLI renders.

import (
	"context"
	"encoding/json"
	"net/http"
	neturl "net/url"
	"strconv"
)

const (
	aiTasksPath         = "/api/v1/org/ai-tasks"
	aiTaskTemplatesPath = "/api/v1/org/ai-tasks/templates"
)

// AITaskRunListCap is the most runs one task run listing returns.
const AITaskRunListCap = 100

func aiTaskPath(taskID string) string {
	return aiTasksPath + "/" + neturl.PathEscape(taskID)
}

// ListAITasks lists the organisation's agent tasks. GET /api/v1/org/ai-tasks
func (c *Client) ListAITasks(ctx context.Context) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, aiTasksPath, nil)
}

// GetAITask reads one agent task. GET /api/v1/org/ai-tasks/{task_id}
func (c *Client) GetAITask(ctx context.Context, taskID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, aiTaskPath(taskID), nil)
}

// CreateAITask creates an agent task from its definition; name, goal_prompt
// and trigger are required. POST /api/v1/org/ai-tasks
func (c *Client) CreateAITask(ctx context.Context, definition map[string]any) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, aiTasksPath, definition)
}

// UpdateAITask changes the members of an agent task's definition that changes
// names. PUT /api/v1/org/ai-tasks/{task_id}
func (c *Client) UpdateAITask(ctx context.Context, taskID string, changes map[string]any) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPut, aiTaskPath(taskID), changes)
}

// DeleteAITask deletes an agent task so it is no longer scheduled.
// DELETE /api/v1/org/ai-tasks/{task_id}
func (c *Client) DeleteAITask(ctx context.Context, taskID string) error {
	return c.sendJSONContext(ctx, http.MethodDelete, c.BaseURL+aiTaskPath(taskID), nil, nil)
}

// ControlAITask sends one of the bodyless control actions of an agent task:
// run, pause, resume or stop. POST /api/v1/org/ai-tasks/{task_id}/{action}
func (c *Client) ControlAITask(ctx context.Context, taskID string, action string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, aiTaskPath(taskID)+"/"+neturl.PathEscape(action), nil)
}

// ReapplyAITaskTemplate brings an agent up to its role template's current
// envelope; force replaces an envelope an operator customised.
// POST /api/v1/org/ai-tasks/{task_id}/reapply-template
func (c *Client) ReapplyAITaskTemplate(ctx context.Context, taskID string, force bool) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, aiTaskPath(taskID)+"/reapply-template",
		map[string]any{"force": force})
}

// ListAITaskRuns lists an agent task's recent runs, newest first.
// GET /api/v1/org/ai-tasks/{task_id}/runs
func (c *Client) ListAITaskRuns(ctx context.Context, taskID string, limit int) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, aiTaskPath(taskID)+"/runs?limit="+strconv.Itoa(limit), nil)
}

// ListAITaskTemplates lists the built-in agent roles that can be hired.
// GET /api/v1/org/ai-tasks/templates
func (c *Client) ListAITaskTemplates(ctx context.Context) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, aiTaskTemplatesPath, nil)
}

// HireAITaskTemplate creates an agent task from a built-in role, scoped to
// clusterID when one is given.
// POST /api/v1/org/ai-tasks/templates/{template_id}/hire
func (c *Client) HireAITaskTemplate(ctx context.Context, templateID string, clusterID string) (json.RawMessage, error) {
	hire := map[string]any{}
	if clusterID != "" {
		hire["cluster_id"] = clusterID
	}
	return c.requestDocument(ctx, http.MethodPost,
		aiTaskTemplatesPath+"/"+neturl.PathEscape(templateID)+"/hire", hire)
}

// --- conditions, playbooks and engineering handoffs ---

const (
	aiConditionsPath          = "/api/v1/org/ai-conditions"
	aiPlaybooksPath           = "/api/v1/org/ai-playbooks"
	aiEngineeringHandoffsPath = "/api/v1/org/ai-engineering-handoffs"
)

// AIConditionListCap is the most conditions one listing returns.
const AIConditionListCap = 500

// ListAIConditions lists the recurring problems AI tickets are filed against,
// most recently observed first; chronicOnly keeps the ones closed without
// action three times in a row. GET /api/v1/org/ai-conditions
func (c *Client) ListAIConditions(ctx context.Context, chronicOnly bool, limit int) (json.RawMessage, error) {
	query := neturl.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if chronicOnly {
		query.Set("chronic", "true")
	}
	return c.requestDocument(ctx, http.MethodGet, aiConditionsPath+"?"+query.Encode(), nil)
}

// GetAICondition reads one condition's evidence timeline.
// GET /api/v1/org/ai-conditions/{condition_id}
func (c *Client) GetAICondition(ctx context.Context, conditionID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, aiConditionsPath+"/"+neturl.PathEscape(conditionID), nil)
}

// ListAIPlaybooks lists the organisation's playbooks and the platform
// templates it can adopt. GET /api/v1/org/ai-playbooks
func (c *Client) ListAIPlaybooks(ctx context.Context) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodGet, aiPlaybooksPath, nil)
}

// AdoptAIPlaybook approves a playbook for use.
// POST /api/v1/org/ai-playbooks/{playbook_id}/adopt
func (c *Client) AdoptAIPlaybook(ctx context.Context, playbookID string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost, aiPlaybooksPath+"/"+neturl.PathEscape(playbookID)+"/adopt", nil)
}

// RetireAIPlaybook retires one of the organisation's own playbooks.
// POST /api/v1/org/ai-playbooks/{playbook_id}/retire
func (c *Client) RetireAIPlaybook(ctx context.Context, playbookID string, reason string) (json.RawMessage, error) {
	retirement := map[string]any{}
	if reason != "" {
		retirement["reason"] = reason
	}
	return c.requestDocument(ctx, http.MethodPost,
		aiPlaybooksPath+"/"+neturl.PathEscape(playbookID)+"/retire", retirement)
}

// ListAIEngineeringHandoffs lists the platform-level conditions handed to the
// organisation's engineering tracker; status is a comma-separated subset of
// open, filed and resolved, empty for all.
// GET /api/v1/org/ai-engineering-handoffs
func (c *Client) ListAIEngineeringHandoffs(ctx context.Context, status string) (json.RawMessage, error) {
	path := aiEngineeringHandoffsPath
	if status != "" {
		path += "?" + neturl.Values{"status": {status}}.Encode()
	}
	return c.requestDocument(ctx, http.MethodGet, path, nil)
}

// AcknowledgeAIEngineeringHandoff records the engineering tracker's reference
// for a handoff. POST /api/v1/org/ai-engineering-handoffs/{handoff_id}/acknowledge
func (c *Client) AcknowledgeAIEngineeringHandoff(ctx context.Context, handoffID string, trackerReference string) (json.RawMessage, error) {
	return c.requestDocument(ctx, http.MethodPost,
		aiEngineeringHandoffsPath+"/"+neturl.PathEscape(handoffID)+"/acknowledge",
		map[string]any{"tracker_reference": trackerReference})
}

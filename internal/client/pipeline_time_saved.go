package client

// The time-saved reads (ankra-efrnzf.20): how much CI time path filters and
// promoted publishes kept a pipeline from spending
// (go/internal/pipelineapi/timesaved.go on the cluster-api).
//
//	GET …/pipeline-runs/{run_id}/time-saved   what one run did not spend
//	GET …/pipeline-time-saved?window=7d|30d   one repository over a window
//	GET /org/pipelines/time-saved?window=…    the organisation over a window
//
// Every figure is an estimate the platform made when the run was planned or
// skipped, from the stage's own recent history in the repository. A saving
// with no history carries SavedSeconds nil: it is unknown, never zero.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
)

// ErrPipelineTimeSavedUnavailable is a platform that answers the time-saved
// read with 404: one older than the routes, or a run it holds no record for.
// It is "nothing to show", not a failure, so a caller leaves the figure out
// rather than failing a command whose run it did read.
var ErrPipelineTimeSavedUnavailable = errors.New("the platform does not report the time this run saved")

// PipelineTimeSavedMethod states how every estimate in a body was made.
type PipelineTimeSavedMethod struct {
	Statistic      string `json:"statistic" yaml:"statistic"`
	SampleWindow   int    `json:"sample_window" yaml:"sample_window"`
	HistoryRunScan int    `json:"history_run_scan" yaml:"history_run_scan"`
}

// PipelineTimeSavedEntry is one saving of a run: a stage a path filter left
// out, a stage skipped behind one, a step a promoted publish did not run
// again, or the whole run (StepKey and Stage empty, Reason
// "run_paths_excluded").
type PipelineTimeSavedEntry struct {
	StepKey         string   `json:"step_key" yaml:"step_key"`
	Stage           string   `json:"stage" yaml:"stage"`
	Reason          string   `json:"reason" yaml:"reason"`
	CauseStage      *string  `json:"cause_stage" yaml:"cause_stage"`
	Basis           string   `json:"basis" yaml:"basis"`
	SampleCount     int      `json:"sample_count" yaml:"sample_count"`
	SavedSeconds    *int64   `json:"saved_seconds" yaml:"saved_seconds"`
	SavedCPUMinutes *float64 `json:"saved_cpu_minutes" yaml:"saved_cpu_minutes"`
	SourceRunID     *string  `json:"source_run_id" yaml:"source_run_id"`
}

// PipelineRunTimeSaved is the GET …/pipeline-runs/{run_id}/time-saved body.
// Savings is empty both for a run that skipped nothing that counts and for
// one planned before savings were recorded.
type PipelineRunTimeSaved struct {
	RunID           string                   `json:"run_id" yaml:"run_id"`
	SavedSeconds    int64                    `json:"saved_seconds" yaml:"saved_seconds"`
	SavedCPUMinutes *float64                 `json:"saved_cpu_minutes" yaml:"saved_cpu_minutes"`
	EstimatedCount  int                      `json:"estimated_count" yaml:"estimated_count"`
	UnknownCount    int                      `json:"unknown_count" yaml:"unknown_count"`
	Savings         []PipelineTimeSavedEntry `json:"savings" yaml:"savings"`
	Method          PipelineTimeSavedMethod  `json:"method" yaml:"method"`
}

// PipelineTimeSavedTotals is one row of a window summary: a reason, a
// repository or a day.
type PipelineTimeSavedTotals struct {
	SavedSeconds    int64    `json:"saved_seconds" yaml:"saved_seconds"`
	SavedCPUMinutes *float64 `json:"saved_cpu_minutes" yaml:"saved_cpu_minutes"`
	EstimatedCount  int      `json:"estimated_count" yaml:"estimated_count"`
	UnknownCount    int      `json:"unknown_count" yaml:"unknown_count"`
}

// PipelineTimeSavedReason is the window's savings for one reason.
type PipelineTimeSavedReason struct {
	Reason                  string `json:"reason" yaml:"reason"`
	PipelineTimeSavedTotals `yaml:",inline"`
}

// PipelineTimeSavedRepository is the window's savings for one repository.
type PipelineTimeSavedRepository struct {
	RepositoryID            string `json:"repository_id" yaml:"repository_id"`
	FullName                string `json:"full_name" yaml:"full_name"`
	Runs                    int    `json:"runs" yaml:"runs"`
	PipelineTimeSavedTotals `yaml:",inline"`
}

// PipelineTimeSavedDay is one UTC day of the window; every day is present.
type PipelineTimeSavedDay struct {
	Date                    string `json:"date" yaml:"date"`
	PipelineTimeSavedTotals `yaml:",inline"`
}

// PipelineTimeSavedSummary is the body of both window routes.
type PipelineTimeSavedSummary struct {
	Window                  string `json:"window" yaml:"window"`
	From                    string `json:"from" yaml:"from"`
	To                      string `json:"to" yaml:"to"`
	Runs                    int    `json:"runs" yaml:"runs"`
	PipelineTimeSavedTotals `yaml:",inline"`
	Reasons                 []PipelineTimeSavedReason     `json:"reasons" yaml:"reasons"`
	Repositories            []PipelineTimeSavedRepository `json:"repositories" yaml:"repositories"`
	Days                    []PipelineTimeSavedDay        `json:"days" yaml:"days"`
	Method                  PipelineTimeSavedMethod       `json:"method" yaml:"method"`
}

// GetPipelineRunTimeSaved reads what one run did not spend. A 404, from a
// platform older than the route or for a run it holds no record for, answers
// ErrPipelineTimeSavedUnavailable whatever its body says.
func (c *Client) GetPipelineRunTimeSaved(ctx context.Context, selector PipelineSelector,
	runID string) (*PipelineRunTimeSaved, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineRunTimeSaved
	if requestError := c.doTimeSavedRequest(ctx,
		fmt.Sprintf("%s%s/pipeline-runs/%s/time-saved", c.BaseURL, base, neturl.PathEscape(runID)),
		&result); requestError != nil {
		return nil, requestError
	}
	if result.Savings == nil {
		result.Savings = []PipelineTimeSavedEntry{}
	}
	return &result, nil
}

// GetPipelineTimeSavedSummary reads the time saved over a window ("7d" or
// "30d"; empty takes the platform's default, 30d). A selector naming neither
// a repository nor an application reads the whole organisation.
func (c *Client) GetPipelineTimeSavedSummary(ctx context.Context, selector PipelineSelector,
	window string) (*PipelineTimeSavedSummary, error) {
	endpoint := c.BaseURL + "/api/v1/org/pipelines/time-saved"
	if selector.RepositoryID != "" || selector.ApplicationID != "" {
		base, selectorError := selector.basePath()
		if selectorError != nil {
			return nil, selectorError
		}
		endpoint = c.BaseURL + base + "/pipeline-time-saved"
	}
	if window != "" {
		endpoint += "?" + neturl.Values{"window": []string{window}}.Encode()
	}
	var result PipelineTimeSavedSummary
	if requestError := c.doTimeSavedRequest(ctx, endpoint, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// doTimeSavedRequest is doPipelineRequest for a GET whose 404 means the
// platform has nothing to say. The status is checked before the body is
// mapped, because a router 404 ({"detail": "Not Found"}) would otherwise
// become a bare error that has lost its status.
func (c *Client) doTimeSavedRequest(ctx context.Context, endpoint string, target any) error {
	request, requestError := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if requestError != nil {
		return fmt.Errorf("create request: %w", requestError)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, doError := c.HTTP.Do(request)
	if doError != nil {
		return fmt.Errorf("request failed: %w", doError)
	}
	defer closeBody(response)
	body, readError := readResponseBody(response)
	if readError != nil {
		return fmt.Errorf("read response: %w", readError)
	}
	if response.StatusCode == http.StatusNotFound {
		return ErrPipelineTimeSavedUnavailable
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return pipelineErrorFromResponse(response.StatusCode, body, response.Header.Get("Retry-After"))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if unmarshalError := json.Unmarshal(body, target); unmarshalError != nil {
		return fmt.Errorf("parse response: %w", unmarshalError)
	}
	return nil
}

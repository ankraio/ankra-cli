package client

// Two pipeline reads (ankra-hq41xb.22), each served on the repository and
// the application surface:
//
//	GET …/pipeline-release-queue                         releases in flight
//	GET …/pipeline-runs/{run_id}/resource-suggestions    right-sizing advice
//
// Both need pipelines.read and write nothing.

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"strconv"
)

// ErrPipelineReleaseQueueUnavailable is a platform that answers the release
// queue read with 404: a repository or application it holds no pipeline for,
// or a platform older than the route.
var ErrPipelineReleaseQueueUnavailable = errors.New("the platform reports no release queue for this pipeline")

// ErrPipelineResourceSuggestionsUnavailable is a platform that answers the
// resource suggestions read with 404: a run it holds no record for, an
// application with no pipeline repository, or a platform older than the
// route.
var ErrPipelineResourceSuggestionsUnavailable = errors.New("the platform reports no resource suggestions for this run")

// PipelineReleaseQueue counts the release runs of a pipeline's branch that
// are queued or running. OldestRunID is empty when none is in flight. It
// keeps the body it was decoded from, so -o json|yaml prints the platform's
// answer as sent.
type PipelineReleaseQueue struct {
	RawDocument   `json:"-" yaml:"-"`
	RepositoryID  string `json:"repository_id" yaml:"repository_id"`
	ApplicationID string `json:"application_id" yaml:"application_id"`
	Branch        string `json:"branch" yaml:"branch"`
	Queued        int    `json:"queued" yaml:"queued"`
	Running       int    `json:"running" yaml:"running"`
	OldestRunID   string `json:"oldest_run_id" yaml:"oldest_run_id"`
	CheckedAt     string `json:"checked_at" yaml:"checked_at"`
}

// PipelineResourceSuggestionMethod is the parameters every suggestion in a
// body is made of.
type PipelineResourceSuggestionMethod struct {
	Percentile               int `json:"percentile" yaml:"percentile"`
	HeadroomPercent          int `json:"headroom_percent" yaml:"headroom_percent"`
	MinimumSamples           int `json:"minimum_samples" yaml:"minimum_samples"`
	MemoryAtLimitPercent     int `json:"memory_at_limit_percent" yaml:"memory_at_limit_percent"`
	CPUThrottledSharePercent int `json:"cpu_throttled_share_percent" yaml:"cpu_throttled_share_percent"`
	TolerancePercent         int `json:"tolerance_percent" yaml:"tolerance_percent"`
	CPUReservePercent        int `json:"cpu_reserve_percent" yaml:"cpu_reserve_percent"`
}

// PipelineMemoryResourceSuggestion is a stage's memory axis. Status is
// oversized, undersized, right_sized, at_limit, insufficient_samples or
// unknown_request; Suggested is the value for the stage's resources.memory
// and nil when there is no suggestion.
type PipelineMemoryResourceSuggestion struct {
	Status              string  `json:"status" yaml:"status"`
	Samples             int     `json:"samples" yaml:"samples"`
	CurrentRequestBytes *int64  `json:"current_request_bytes" yaml:"current_request_bytes"`
	PeakP95Bytes        *int64  `json:"peak_p95_bytes" yaml:"peak_p95_bytes"`
	PeakMaxBytes        *int64  `json:"peak_max_bytes" yaml:"peak_max_bytes"`
	AtLimitSamples      int     `json:"at_limit_samples" yaml:"at_limit_samples"`
	OOMKilledSamples    int     `json:"oom_killed_samples" yaml:"oom_killed_samples"`
	SuggestedBytes      *int64  `json:"suggested_bytes" yaml:"suggested_bytes"`
	Suggested           *string `json:"suggested" yaml:"suggested"`
}

// PipelineCPUResourceSuggestion is a stage's CPU axis, with the statuses of
// the memory axis; Suggested is the value for the stage's resources.cpu.
type PipelineCPUResourceSuggestion struct {
	Status                     string   `json:"status" yaml:"status"`
	Samples                    int      `json:"samples" yaml:"samples"`
	CurrentRequestMillicores   *int64   `json:"current_request_millicores" yaml:"current_request_millicores"`
	CurrentLimitMillicores     *int64   `json:"current_limit_millicores" yaml:"current_limit_millicores"`
	MeanP95Millicores          *int64   `json:"mean_p95_millicores" yaml:"mean_p95_millicores"`
	ThrottledShareP95          *float64 `json:"throttled_share_p95" yaml:"throttled_share_p95"`
	SuggestedRequestMillicores *int64   `json:"suggested_request_millicores" yaml:"suggested_request_millicores"`
	Suggested                  *string  `json:"suggested" yaml:"suggested"`
}

// PipelineStageResourceSuggestion is the advice for one stage.
type PipelineStageResourceSuggestion struct {
	Stage       string                           `json:"stage" yaml:"stage"`
	SampleCount int                              `json:"sample_count" yaml:"sample_count"`
	Memory      PipelineMemoryResourceSuggestion `json:"memory" yaml:"memory"`
	CPU         PipelineCPUResourceSuggestion    `json:"cpu" yaml:"cpu"`
}

// PipelineResourceSuggestions is the GET …/resource-suggestions body. A stage
// absent from Stages has no successful attempt with a usage report yet, not
// zero use. It keeps the body it was decoded from.
type PipelineResourceSuggestions struct {
	RawDocument  `json:"-" yaml:"-"`
	RunID        string                            `json:"run_id" yaml:"run_id"`
	DefinitionID *string                           `json:"definition_id" yaml:"definition_id"`
	Window       int                               `json:"window" yaml:"window"`
	Method       PipelineResourceSuggestionMethod  `json:"method" yaml:"method"`
	Stages       []PipelineStageResourceSuggestion `json:"stages" yaml:"stages"`
}

// GetPipelineReleaseQueue counts the releases in flight for a pipeline. A 404
// answers ErrPipelineReleaseQueueUnavailable.
func (c *Client) GetPipelineReleaseQueue(ctx context.Context, selector PipelineSelector) (*PipelineReleaseQueue, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineReleaseQueue
	if requestError := c.doPipelineReadRequest(ctx, c.BaseURL+base+"/pipeline-release-queue", &result,
		ErrPipelineReleaseQueueUnavailable); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetPipelineRunResourceSuggestions reads the right-sizing advice for the
// stages of the definition a run was planned from. window is how many of
// each stage's latest successful attempts to size from; zero takes the
// platform's default. A 404 answers ErrPipelineResourceSuggestionsUnavailable.
func (c *Client) GetPipelineRunResourceSuggestions(ctx context.Context, selector PipelineSelector, runID string,
	window int) (*PipelineResourceSuggestions, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	endpoint := fmt.Sprintf("%s%s/pipeline-runs/%s/resource-suggestions", c.BaseURL, base, neturl.PathEscape(runID))
	if window > 0 {
		endpoint += "?" + neturl.Values{"window": []string{strconv.Itoa(window)}}.Encode()
	}
	var result PipelineResourceSuggestions
	if requestError := c.doPipelineReadRequest(ctx, endpoint, &result,
		ErrPipelineResourceSuggestionsUnavailable); requestError != nil {
		return nil, requestError
	}
	if result.Stages == nil {
		result.Stages = []PipelineStageResourceSuggestion{}
	}
	return &result, nil
}

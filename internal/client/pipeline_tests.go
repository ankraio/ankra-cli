package client

// The test results reads (ankra-q573dh.9.1, go/internal/pipelineapi/tests.go
// on the platform): the per-test outcomes Ankra records from the reports a
// stage declares under test_results. Three routes, each addressable by
// application or by repository, all needing pipelines.read.

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
)

// PipelineTestCounts is a report's or a run's tests by outcome.
type PipelineTestCounts struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Flaky   int `json:"flaky"`
}

// PipelineTestReport is one declared report of a run and what became of it.
// Status is the platform's: "ingested" and "unreadable" for a report it
// read, and a state derived from the artifact (for example "pending" or
// "not_uploaded") for one it has not; ErrorMessage says why when there is a
// reason. The CLI prints Status as the server spells it, so a status the
// platform adds later is shown rather than refused.
type PipelineTestReport struct {
	ArtifactID   string             `json:"artifact_id"`
	ReportID     *string            `json:"report_id"`
	StepID       *string            `json:"step_id"`
	Stage        string             `json:"stage"`
	Format       string             `json:"format"`
	Status       string             `json:"status"`
	ErrorMessage string             `json:"error_message"`
	Counts       PipelineTestCounts `json:"counts"`
	DurationMS   int64              `json:"duration_ms"`
	StoredCount  int                `json:"stored_count"`
	IsTruncated  bool               `json:"is_truncated"`
	IsSuperseded bool               `json:"is_superseded"`
}

// PipelineTestCase is one recorded test of a run.
type PipelineTestCase struct {
	TestKey        string  `json:"test_key"`
	Suite          string  `json:"suite"`
	Name           string  `json:"name"`
	File           string  `json:"file"`
	Outcome        string  `json:"outcome"`
	DurationMS     int64   `json:"duration_ms"`
	Attempts       int     `json:"attempts"`
	FailureMessage string  `json:"failure_message"`
	StepID         *string `json:"step_id"`
	Stage          string  `json:"stage"`
}

// PipelineRunTests is the GET …/pipeline-runs/{run_id}/tests body.
// HasReports false is a run that declared no reports, not a green one.
type PipelineRunTests struct {
	RunID       string               `json:"run_id"`
	HasReports  bool                 `json:"has_reports"`
	Counts      PipelineTestCounts   `json:"counts"`
	DurationMS  int64                `json:"duration_ms"`
	IsTruncated bool                 `json:"is_truncated"`
	Reports     []PipelineTestReport `json:"reports"`
	Failed      []PipelineTestCase   `json:"failed"`
	Flaky       []PipelineTestCase   `json:"flaky"`
	Slowest     []PipelineTestCase   `json:"slowest"`
}

// PipelineTestHistoryEntry is one run's outcome for a test.
type PipelineTestHistoryEntry struct {
	RunID      string `json:"run_id"`
	RunNumber  int64  `json:"run_number"`
	HeadSHA    string `json:"head_sha"`
	Outcome    string `json:"outcome"`
	DurationMS int64  `json:"duration_ms"`
	Attempts   int    `json:"attempts"`
	RecordedAt string `json:"recorded_at"`
}

// PipelineTestHistory is the GET …/pipeline-tests/history body. FlakyRate and
// FailureRate are nil when the test never ran on the branch in the window,
// which is not a rate of zero.
type PipelineTestHistory struct {
	TestKey     string                     `json:"test_key"`
	Suite       string                     `json:"suite"`
	Name        string                     `json:"name"`
	File        string                     `json:"file"`
	Branch      string                     `json:"branch"`
	Observed    int                        `json:"observed"`
	Flaky       int                        `json:"flaky"`
	Failed      int                        `json:"failed"`
	FlakyRate   *float64                   `json:"flaky_rate"`
	FailureRate *float64                   `json:"failure_rate"`
	Entries     []PipelineTestHistoryEntry `json:"entries"`
}

// PipelineTestTiming is one file's (or, grouped by test, one test's) mean
// recorded duration. Suite, Name and TestKey are set only when grouped by
// test.
type PipelineTestTiming struct {
	File       string  `json:"file"`
	Suite      *string `json:"suite"`
	Name       *string `json:"name"`
	TestKey    *string `json:"test_key"`
	DurationMS int64   `json:"duration_ms"`
	Samples    int     `json:"samples"`
}

// PipelineTestTimings is the GET …/pipeline-tests/timings body. RunsSampled
// zero means no recorded data for the stage on the branch yet, which a
// sharding script must fall back from rather than read as every test taking
// no time.
type PipelineTestTimings struct {
	Stage       string               `json:"stage"`
	Branch      string               `json:"branch"`
	GroupBy     string               `json:"group_by"`
	RunsSampled int                  `json:"runs_sampled"`
	IsPartial   bool                 `json:"is_partial"`
	Entries     []PipelineTestTiming `json:"entries"`
}

// The group_by values of the timings route.
const (
	PipelineTestTimingsByFile = "file"
	PipelineTestTimingsByTest = "test"
)

// PipelineTestHistoryOptions narrows a history read. A zero Branch is the
// repository's default branch and a zero Runs the server's default window.
type PipelineTestHistoryOptions struct {
	Branch string
	Runs   int
}

// PipelineTestTimingsOptions narrows a timings read. Zero values take the
// server's defaults: the default branch, group_by file and its default
// window.
type PipelineTestTimingsOptions struct {
	Branch  string
	GroupBy string
	Runs    int
}

// GetPipelineRunTests reads a run's test summary (GET
// …/pipeline-runs/{run_id}/tests): its declared reports and what became of
// each, counts by outcome, and the failed, flaky and slowest tests. A
// negative slowest leaves the count to the server.
func (c *Client) GetPipelineRunTests(ctx context.Context, selector PipelineSelector, runID string,
	slowest int) (*PipelineRunTests, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	query := neturl.Values{}
	if slowest >= 0 {
		query.Set("slowest", strconv.Itoa(slowest))
	}
	var result PipelineRunTests
	if requestError := c.doPipelineRequest(ctx, http.MethodGet,
		withQuery(fmt.Sprintf("%s%s/pipeline-runs/%s/tests", c.BaseURL, base, neturl.PathEscape(runID)), query),
		nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetPipelineTestHistory reads one test's outcomes in the newest runs of a
// branch and its flaky rate over them (GET …/pipeline-tests/history).
func (c *Client) GetPipelineTestHistory(ctx context.Context, selector PipelineSelector, testKey string,
	options PipelineTestHistoryOptions) (*PipelineTestHistory, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	query := neturl.Values{}
	query.Set("test_key", testKey)
	if options.Branch != "" {
		query.Set("branch", options.Branch)
	}
	if options.Runs > 0 {
		query.Set("runs", strconv.Itoa(options.Runs))
	}
	var result PipelineTestHistory
	if requestError := c.doPipelineRequest(ctx, http.MethodGet,
		withQuery(c.BaseURL+base+"/pipeline-tests/history", query), nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// GetPipelineTestTimings reads each file's or test's mean recorded duration
// for one stage on a branch (GET …/pipeline-tests/timings).
func (c *Client) GetPipelineTestTimings(ctx context.Context, selector PipelineSelector, stage string,
	options PipelineTestTimingsOptions) (*PipelineTestTimings, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	query := neturl.Values{}
	query.Set("stage", stage)
	if options.Branch != "" {
		query.Set("branch", options.Branch)
	}
	if options.GroupBy != "" {
		query.Set("group_by", options.GroupBy)
	}
	if options.Runs > 0 {
		query.Set("runs", strconv.Itoa(options.Runs))
	}
	var result PipelineTestTimings
	if requestError := c.doPipelineRequest(ctx, http.MethodGet,
		withQuery(c.BaseURL+base+"/pipeline-tests/timings", query), nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// withQuery appends an encoded query to an endpoint when it has one.
func withQuery(endpoint string, query neturl.Values) string {
	if len(query) == 0 {
		return endpoint
	}
	return endpoint + "?" + query.Encode()
}

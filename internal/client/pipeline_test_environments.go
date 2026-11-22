package client

// The test environments of a run (ankra-aqb78l.2): the legs of a pipeline
// stage of `kind: environment`. Each leg is one cell of the stage's matrix
// (for example Kubernetes 1.36 x PostgreSQL 17) for which the platform built
// an ephemeral cluster, deployed the build under test, ran the tests,
// recorded the result and tore the cluster down.
//
//	GET …/pipeline-runs/{run_id}/test-environments
//
// A leg the platform has not started yet, because the organisation's
// concurrency cap holds it back, is "queued" with no result and no times.

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
)

// ErrPipelineTestEnvironmentsUnavailable is a platform that answers the test
// environments read with 404: a run it holds no record for, or a platform
// older than the route. A caller that read the run itself treats it as
// "nothing to show".
var ErrPipelineTestEnvironmentsUnavailable = errors.New("the platform reports no test environments for this run")

// PipelineTestEnvironmentsSummary counts a run's legs by outcome. Verdict is
// "passed", "failed", "running" or "none".
type PipelineTestEnvironmentsSummary struct {
	Total     int    `json:"total" yaml:"total"`
	Passed    int    `json:"passed" yaml:"passed"`
	Failed    int    `json:"failed" yaml:"failed"`
	Errored   int    `json:"errored" yaml:"errored"`
	Cancelled int    `json:"cancelled" yaml:"cancelled"`
	TimedOut  int    `json:"timed_out" yaml:"timed_out"`
	Running   int    `json:"running" yaml:"running"`
	Queued    int    `json:"queued" yaml:"queued"`
	TornDown  int    `json:"torn_down" yaml:"torn_down"`
	Verdict   string `json:"verdict" yaml:"verdict"`
}

// PipelineTestEnvironment is one leg: one attempt of one matrix cell.
//
// Status is the leg's phase (queued, provisioning, deploying, testing, held,
// tearing_down, destroyed) and Result its outcome once it has one (passed,
// failed, cancelled, timed_out, error). Every pointer is nil until the
// platform has the value.
type PipelineTestEnvironment struct {
	StepID            string            `json:"step_id" yaml:"step_id"`
	StepKey           string            `json:"step_key" yaml:"step_key"`
	Stage             string            `json:"stage" yaml:"stage"`
	Attempt           int               `json:"attempt" yaml:"attempt"`
	Matrix            map[string]string `json:"matrix" yaml:"matrix"`
	KubernetesVersion string            `json:"kubernetes_version" yaml:"kubernetes_version"`
	PostgresVersion   string            `json:"postgres_version" yaml:"postgres_version"`
	Provider          string            `json:"provider" yaml:"provider"`
	Plan              string            `json:"plan" yaml:"plan"`
	Status            string            `json:"status" yaml:"status"`
	Result            *string           `json:"result" yaml:"result"`
	ClusterID         *string           `json:"cluster_id" yaml:"cluster_id"`
	Message           string            `json:"message" yaml:"message"`
	TestLogTail       string            `json:"test_log_tail" yaml:"test_log_tail"`
	CreatedAt         *string           `json:"created_at" yaml:"created_at"`
	ReadyAt           *string           `json:"ready_at" yaml:"ready_at"`
	ConcludedAt       *string           `json:"concluded_at" yaml:"concluded_at"`
	DestroyedAt       *string           `json:"destroyed_at" yaml:"destroyed_at"`
	ExpiresAt         *string           `json:"expires_at" yaml:"expires_at"`
	DurationSeconds   *int64            `json:"duration_seconds" yaml:"duration_seconds"`
}

// PipelineRunTestEnvironments is the GET …/test-environments body. Axes is
// the sorted set of matrix axis names across the legs. It keeps the body it
// was decoded from, so -o json|yaml prints the platform's answer as sent.
type PipelineRunTestEnvironments struct {
	RawDocument  `json:"-" yaml:"-"`
	RunID        string                          `json:"run_id" yaml:"run_id"`
	Axes         []string                        `json:"axes" yaml:"axes"`
	Summary      PipelineTestEnvironmentsSummary `json:"summary" yaml:"summary"`
	Environments []PipelineTestEnvironment       `json:"environments" yaml:"environments"`
}

// GetPipelineRunTestEnvironments reads the test environment legs of one run.
// A 404, for a run the platform holds no record for or from a platform older
// than the route, answers ErrPipelineTestEnvironmentsUnavailable.
func (c *Client) GetPipelineRunTestEnvironments(ctx context.Context, selector PipelineSelector,
	runID string) (*PipelineRunTestEnvironments, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineRunTestEnvironments
	if requestError := c.doPipelineReadRequest(ctx,
		fmt.Sprintf("%s%s/pipeline-runs/%s/test-environments", c.BaseURL, base, neturl.PathEscape(runID)),
		&result, ErrPipelineTestEnvironmentsUnavailable); requestError != nil {
		return nil, requestError
	}
	if result.Axes == nil {
		result.Axes = []string{}
	}
	if result.Environments == nil {
		result.Environments = []PipelineTestEnvironment{}
	}
	return &result, nil
}

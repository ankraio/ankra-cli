package client

// The Ankra merge train routes (cluster ankra-q573dh.11,
// go/internal/pipelineapi/mergetrain.go): a repository's train, its settings,
// and putting a pull request in or taking one out.

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
)

// PipelineMergeTrainSettings is a repository's train configuration.
// AutoAdmit is "off", "shadow" or "on" (cluster ankra-ld8mcv.1): whether the
// train puts green pull requests in by itself, and AutoAdmitBy and
// AutoAdmitSince name who switched it to that mode and when. A platform older
// than auto-admit sends none of the three, and AutoAdmit is then empty.
//
// AutoAdmitCriterion says what auto-admit waits for before it admits a pull
// request (cluster ankra-q573dh.79.2), one of PipelineMergeTrainCriteria.
// AutoAdmitProtectedPaths are the patterns that keep the green rule at ready,
// the effective list whichever way it was set, and
// AutoAdmitProtectedPathsIsDefault says the list is the platform's default. A
// platform older than the criterion sends none of the three: the criterion is
// then empty and the two pointers nil, which is "not reported", never "green"
// or "nothing protected". The list is a pointer so that an empty one, which
// protects nothing, survives into -o json as [] rather than being omitted.
type PipelineMergeTrainSettings struct {
	Enabled                          bool      `json:"enabled"`
	MaxCars                          int       `json:"max_cars"`
	AutoAdmit                        string    `json:"auto_admit,omitempty"`
	AutoAdmitBy                      *string   `json:"auto_admit_by,omitempty"`
	AutoAdmitSince                   *string   `json:"auto_admit_since,omitempty"`
	AutoAdmitCriterion               string    `json:"auto_admit_criterion,omitempty" yaml:"auto_admit_criterion,omitempty"`
	AutoAdmitProtectedPaths          *[]string `json:"auto_admit_protected_paths,omitempty" yaml:"auto_admit_protected_paths,omitempty"`
	AutoAdmitProtectedPathsIsDefault *bool     `json:"auto_admit_protected_paths_is_default,omitempty" yaml:"auto_admit_protected_paths_is_default,omitempty"`
}

// The auto-admit criteria. green admits a pull request once its own run has
// passed (every repository's default). ready_shadow does the same, and records
// which pull requests whose own run is still going ready would have admitted.
// ready admits a pull request whose own run is still going and has no failed
// step, except one that changes a protected path, which keeps the green rule.
const (
	PipelineMergeTrainCriterionGreen       = "green"
	PipelineMergeTrainCriterionReadyShadow = "ready_shadow"
	PipelineMergeTrainCriterionReady       = "ready"
)

// PipelineMergeTrainCriteria lists the criteria in the order a repository moves
// through them, which is also the order help text and refusals name them.
var PipelineMergeTrainCriteria = []string{
	PipelineMergeTrainCriterionGreen, PipelineMergeTrainCriterionReadyShadow, PipelineMergeTrainCriterionReady,
}

// The platform's bounds on a protected path list (cluster
// enginekit/mergetrain): how many patterns, and how long one may be once
// trimmed.
const (
	PipelineMergeTrainProtectedPathsLimit      = 50
	PipelineMergeTrainProtectedPathLengthLimit = 256
)

// PipelineMergeTrainEntry is one pull request in, or recently out of, a
// train. The car fields are null until the train built a car for it.
type PipelineMergeTrainEntry struct {
	ID                 string  `json:"id"`
	PullRequestNumber  int64   `json:"pull_request_number"`
	PullRequestHeadSHA string  `json:"pull_request_head_sha"`
	PullRequestTitle   string  `json:"pull_request_title"`
	// PullRequestHeadBranch is the branch the pull request lives on, null
	// until the train read it.
	PullRequestHeadBranch *string `json:"pull_request_head_branch"`
	BaseBranch            string  `json:"base_branch"`
	// ParentEntryID and ParentPullRequestNumber name the entry a stacked pull
	// request rides on (cluster ankra-8tb4gu): it is based on that pull
	// request's branch and merges right after it. Both are null for a pull
	// request based on the default branch.
	ParentEntryID           *string `json:"parent_entry_id"`
	ParentPullRequestNumber *int64  `json:"parent_pull_request_number"`
	Status             string  `json:"status"`
	Position           int64   `json:"position"`
	Attempt            int     `json:"attempt"`
	CarBranch          *string `json:"car_branch"`
	CarBaseSHA         *string `json:"car_base_sha"`
	CarCommitSHA       *string `json:"car_commit_sha"`
	CarTreeSHA         *string `json:"car_tree_sha"`
	PredecessorEntryID *string `json:"predecessor_entry_id"`
	PipelineRunID      *string `json:"pipeline_run_id"`
	MergeCommitSHA     *string `json:"merge_commit_sha"`
	MergeTreeMatches   *bool   `json:"merge_tree_matches"`
	Reason             *string `json:"reason"`
	ReasonText         *string `json:"reason_text"`
	Detail             *string `json:"detail"`
	RequestedBy        string  `json:"requested_by"`
	RequestedVia       string  `json:"requested_via"`
	EnqueuedAt         string  `json:"enqueued_at"`
	StartedAt          *string `json:"started_at"`
	FinishedAt         *string `json:"finished_at"`
	UpdatedAt          string  `json:"updated_at"`
}

// PipelineMergeTrainAdmissionDecision is one auto-admit decision about a
// green pull request: admitted, or the outcome that says why not.
type PipelineMergeTrainAdmissionDecision struct {
	PullRequestNumber int64   `json:"pull_request_number"`
	HeadSHA           string  `json:"head_sha"`
	PipelineRunID     *string `json:"pipeline_run_id"`
	Mode              string  `json:"mode"`
	Outcome           string  `json:"outcome"`
	Detail            *string `json:"detail"`
	DecidedAt         string  `json:"decided_at"`
}

// PipelineMergeTrain is GET …/merge-train: the settings, the train in order,
// the most recently settled entries and auto-admit's latest decisions.
type PipelineMergeTrain struct {
	RepositoryID       string                                `json:"repository_id"`
	Settings           PipelineMergeTrainSettings            `json:"settings"`
	Entries            []PipelineMergeTrainEntry             `json:"entries"`
	Recent             []PipelineMergeTrainEntry             `json:"recent"`
	AutoAdmitDecisions []PipelineMergeTrainAdmissionDecision `json:"auto_admit_decisions,omitempty"`
}

// SetPipelineMergeTrainSettingsRequest is the PUT …/merge-train/settings
// body; a nil MaxCars, AutoAdmit, AutoAdmitCriterion or AutoAdmitProtectedPaths
// keeps the current value.
//
// AutoAdmitProtectedPaths is a pointer because the platform reads three things
// into the field. A nil pointer leaves it out, which keeps the list. A pointer
// to a nil slice sends null, which restores the default list. A pointer to a
// slice sends that list, and an empty one protects nothing.
type SetPipelineMergeTrainSettingsRequest struct {
	Enabled                 bool      `json:"enabled"`
	MaxCars                 *int      `json:"max_cars,omitempty"`
	AutoAdmit               *string   `json:"auto_admit,omitempty"`
	AutoAdmitCriterion      *string   `json:"auto_admit_criterion,omitempty"`
	AutoAdmitProtectedPaths *[]string `json:"auto_admit_protected_paths,omitempty"`
}

// EnqueuePipelineMergeTrainRequest is the POST …/merge-train/entries body.
type EnqueuePipelineMergeTrainRequest struct {
	PullRequestNumber int64  `json:"pull_request_number"`
	HeadSHA           string `json:"head_sha,omitempty"`
}

// GetPipelineMergeTrain reads the selected repository's train.
func (c *Client) GetPipelineMergeTrain(ctx context.Context, selector PipelineSelector) (*PipelineMergeTrain, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineMergeTrain
	if requestError := c.doPipelineRequest(ctx, http.MethodGet, c.BaseURL+base+"/merge-train",
		nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// SetPipelineMergeTrainSettings switches the train on or off and sets what the
// request names: its max cars, auto-admit's mode, criterion and protected paths.
func (c *Client) SetPipelineMergeTrainSettings(ctx context.Context, selector PipelineSelector,
	request SetPipelineMergeTrainSettingsRequest) (*PipelineMergeTrainSettings, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineMergeTrainSettings
	if requestError := c.doPipelineRequest(ctx, http.MethodPut, c.BaseURL+base+"/merge-train/settings",
		request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// EnqueuePipelineMergeTrain puts a pull request in the train, or answers the
// entry it already has there.
func (c *Client) EnqueuePipelineMergeTrain(ctx context.Context, selector PipelineSelector,
	request EnqueuePipelineMergeTrainRequest) (*PipelineMergeTrainEntry, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineMergeTrainEntry
	if requestError := c.doPipelineRequest(ctx, http.MethodPost, c.BaseURL+base+"/merge-train/entries",
		request, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// DequeuePipelineMergeTrain takes an entry out of the train and answers it.
func (c *Client) DequeuePipelineMergeTrain(ctx context.Context, selector PipelineSelector,
	entryID string) (*PipelineMergeTrainEntry, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineMergeTrainEntry
	if requestError := c.doPipelineRequest(ctx, http.MethodDelete,
		fmt.Sprintf("%s%s/merge-train/entries/%s", c.BaseURL, base, neturl.PathEscape(entryID)),
		nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

// MovePipelineMergeTrainToFront puts an entry ahead of every car in the train
// that is still queued, building or testing, and answers it. The platform
// allows it to organisation administrators only.
func (c *Client) MovePipelineMergeTrainToFront(ctx context.Context, selector PipelineSelector,
	entryID string) (*PipelineMergeTrainEntry, error) {
	base, selectorError := selector.basePath()
	if selectorError != nil {
		return nil, selectorError
	}
	var result PipelineMergeTrainEntry
	if requestError := c.doPipelineRequest(ctx, http.MethodPost,
		fmt.Sprintf("%s%s/merge-train/entries/%s/front", c.BaseURL, base, neturl.PathEscape(entryID)),
		nil, &result); requestError != nil {
		return nil, requestError
	}
	return &result, nil
}

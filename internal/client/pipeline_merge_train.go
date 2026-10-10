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
type PipelineMergeTrainSettings struct {
	Enabled bool `json:"enabled"`
	MaxCars int  `json:"max_cars"`
}

// PipelineMergeTrainEntry is one pull request in, or recently out of, a
// train. The car fields are null until the train built a car for it.
type PipelineMergeTrainEntry struct {
	ID                 string  `json:"id"`
	PullRequestNumber  int64   `json:"pull_request_number"`
	PullRequestHeadSHA string  `json:"pull_request_head_sha"`
	PullRequestTitle   string  `json:"pull_request_title"`
	BaseBranch         string  `json:"base_branch"`
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

// PipelineMergeTrain is GET …/merge-train: the settings, the train in order
// and the most recently settled entries.
type PipelineMergeTrain struct {
	RepositoryID string                     `json:"repository_id"`
	Settings     PipelineMergeTrainSettings `json:"settings"`
	Entries      []PipelineMergeTrainEntry  `json:"entries"`
	Recent       []PipelineMergeTrainEntry  `json:"recent"`
}

// SetPipelineMergeTrainSettingsRequest is the PUT …/merge-train/settings
// body; a nil MaxCars keeps the current value.
type SetPipelineMergeTrainSettingsRequest struct {
	Enabled bool `json:"enabled"`
	MaxCars *int `json:"max_cars,omitempty"`
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

// SetPipelineMergeTrainSettings switches the train on or off and sets its
// max cars.
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

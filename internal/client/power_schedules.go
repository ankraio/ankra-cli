package client

import (
	"fmt"
	"net/http"
	neturl "net/url"
)

// PowerSchedule is one scheduled stop/start entry on a cluster, as returned
// by the power-schedule routes. A "once" schedule fires a single time at
// RunAt; a "cron" schedule fires repeatedly per CronExpression evaluated in
// Timezone. NextRunAt is the armed next fire (nil while disabled), and the
// LastRun fields report the most recent outcome (completed, skipped,
// deferred, or failed, with detail).
type PowerSchedule struct {
	ID             string  `json:"id"`
	Action         string  `json:"action"`
	ScheduleKind   string  `json:"schedule_kind"`
	RunAt          *string `json:"run_at"`
	CronExpression *string `json:"cron_expression"`
	Timezone       string  `json:"timezone"`
	Enabled        bool    `json:"enabled"`
	NextRunAt      *string `json:"next_run_at"`
	LastRunAt      *string `json:"last_run_at"`
	LastRunStatus  *string `json:"last_run_status"`
	LastRunDetail  *string `json:"last_run_detail"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

// PowerScheduleListResult wraps the cluster's schedule listing. Create and
// update answer the same refreshed listing, so callers always render the
// full post-write state.
type PowerScheduleListResult struct {
	Schedules []PowerSchedule `json:"schedules"`
}

// PowerScheduleRequest is the shared create/update body. Exactly one of
// RunAt (schedule kind "once") or CronExpression (+ Timezone, schedule kind
// "cron") applies. Updates are full replaces on the backend: Enabled must
// always be sent, and Timezone must be restated for "cron" schedules.
type PowerScheduleRequest struct {
	Action         string  `json:"action"`
	ScheduleKind   string  `json:"schedule_kind"`
	RunAt          *string `json:"run_at,omitempty"`
	CronExpression *string `json:"cron_expression,omitempty"`
	Timezone       *string `json:"timezone,omitempty"`
	Enabled        bool    `json:"enabled"`
}

// DeletePowerScheduleResult reports a schedule deletion.
type DeletePowerScheduleResult struct {
	Deleted bool `json:"deleted"`
}

// ListPowerSchedules returns the cluster's power schedules, newest first.
// GET /api/v1/org/clusters/imported/{cluster_id}/power-schedules
func (c *Client) ListPowerSchedules(clusterID string) (*PowerScheduleListResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/power-schedules",
		c.BaseURL, neturl.PathEscape(clusterID))
	var result PowerScheduleListResult
	if err := c.getJSON(url, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreatePowerSchedule creates one schedule and returns the cluster's
// refreshed schedule listing.
// POST /api/v1/org/clusters/imported/{cluster_id}/power-schedules
func (c *Client) CreatePowerSchedule(clusterID string, request PowerScheduleRequest) (*PowerScheduleListResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/power-schedules",
		c.BaseURL, neturl.PathEscape(clusterID))
	var result PowerScheduleListResult
	if err := c.sendJSON(http.MethodPost, url, request, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdatePowerSchedule replaces a schedule's action, timing, and enabled
// flag (a full replace, not a patch) and returns the refreshed listing.
// PUT /api/v1/org/clusters/imported/{cluster_id}/power-schedules/{schedule_id}
func (c *Client) UpdatePowerSchedule(clusterID, scheduleID string, request PowerScheduleRequest) (*PowerScheduleListResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/power-schedules/%s",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(scheduleID))
	var result PowerScheduleListResult
	if err := c.sendJSON(http.MethodPut, url, request, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeletePowerSchedule soft-deletes one schedule.
// DELETE /api/v1/org/clusters/imported/{cluster_id}/power-schedules/{schedule_id}
func (c *Client) DeletePowerSchedule(clusterID, scheduleID string) (*DeletePowerScheduleResult, error) {
	url := fmt.Sprintf("%s/api/v1/org/clusters/imported/%s/power-schedules/%s",
		c.BaseURL, neturl.PathEscape(clusterID), neturl.PathEscape(scheduleID))
	var result DeletePowerScheduleResult
	if err := c.sendJSON(http.MethodDelete, url, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

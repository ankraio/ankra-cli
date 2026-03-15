package client

import (
	"fmt"
	"net/url"
)

type JobStatusUpdate struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	StartAt   *string `json:"start_at"`
	StopAt    *string `json:"stop_at"`
}

type OperationalStatus struct {
	Name            string  `json:"name"`
	Status          string  `json:"status"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	LastJobUpdateAt *string `json:"last_job_update_at"`
}

type StatusEvent struct {
	Status    string `json:"status"`
	EventTime string `json:"event_time"`
}

type JobInformation struct {
	ID           string        `json:"id"`
	Result       interface{}   `json:"result"`
	StatusEvents []StatusEvent `json:"status_events"`
	ResourceID   *string       `json:"resource_id"`
	ResourceKind *string       `json:"resource_kind"`
}

type GetJobStatusResponse struct {
	OperationInformation   *OperationalStatus `json:"operation_information"`
	Jobs                   []JobStatusUpdate  `json:"jobs"`
	DetailedJobInformation []JobInformation   `json:"detailed_job_information"`
}

type ListOperationJobsOptions struct {
	JobKind          string
	FromUTCTimestamp string
}

func (c *Client) ListOperationJobs(clusterID, operationID string, opts *ListOperationJobsOptions) (GetJobStatusResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/clusters/%s/operations/%s/jobs",
		c.BaseURL, clusterID, operationID)

	if opts != nil {
		params := url.Values{}
		if opts.JobKind != "" {
			params.Set("job_kind", opts.JobKind)
		}
		if opts.FromUTCTimestamp != "" {
			params.Set("from_utc_timestamp", opts.FromUTCTimestamp)
		}
		if encoded := params.Encode(); encoded != "" {
			endpoint += "?" + encoded
		}
	}

	var response GetJobStatusResponse
	if err := c.getJSON(endpoint, &response); err != nil {
		return GetJobStatusResponse{}, err
	}
	return response, nil
}

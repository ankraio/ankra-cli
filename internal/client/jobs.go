package client

import (
	"fmt"
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

func ListOperationJobs(token, baseURL, clusterID, operationID string) (GetJobStatusResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/operations/%s/jobs", baseURL, clusterID, operationID)
	var response GetJobStatusResponse
	if err := getJSON(url, token, &response); err != nil {
		return GetJobStatusResponse{}, err
	}
	return response, nil
}

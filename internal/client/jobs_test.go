package client

import (
	"net/http"
	"strings"
	"testing"
)

func TestListOperationJobs(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		opts    *ListOperationJobsOptions
		wantErr bool
	}{
		{
			name: "without opts",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "/operations/") || !strings.Contains(r.URL.Path, "/jobs") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				jsonResponse(t, w, http.StatusOK, GetJobStatusResponse{
					Jobs: []JobStatusUpdate{
						{ID: "job1", Name: "job1", Status: "running", CreatedAt: "2025-01-01", UpdatedAt: "2025-01-02"},
					},
				})
			},
			opts:    nil,
			wantErr: false,
		},
		{
			name: "with opts",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.RawQuery, "job_kind=") {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				jsonResponse(t, w, http.StatusOK, GetJobStatusResponse{
					Jobs: []JobStatusUpdate{},
				})
			},
			opts:    &ListOperationJobsOptions{JobKind: "reconcile"},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			got, err := testClient.ListOperationJobs("cluster-id", "op-id", tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("ListOperationJobs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.opts == nil && len(got.Jobs) != 1 {
				t.Errorf("ListOperationJobs() got %d jobs, want 1", len(got.Jobs))
			}
		})
	}
}

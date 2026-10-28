package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestGetPipelineRunTestsAsksTheRunsRouteWithSlowest(t *testing.T) {
	var capturedPath, capturedQuery string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath, capturedQuery = r.URL.Path, r.URL.RawQuery
		_, _ = fmt.Fprint(w, `{"run_id":"run-1","has_reports":true,"counts":{"total":3,"passed":2,"failed":1,
			"skipped":0,"flaky":0},"duration_ms":1500,"is_truncated":false,"reports":[{"artifact_id":"a-1",
			"report_id":null,"step_id":"s-1","stage":"test","format":"go-test","status":"ingested",
			"error_message":"","counts":{"total":3,"passed":2,"failed":1,"skipped":0,"flaky":0},
			"duration_ms":1500,"stored_count":3,"is_truncated":false,"is_superseded":false}],
			"failed":[{"test_key":"go:p/t","suite":"p","name":"T","file":"p/t_test.go","outcome":"failed",
			"duration_ms":10,"attempts":1,"failure_message":"boom","step_id":"s-1","stage":"test"}],
			"flaky":[],"slowest":[]}`)
	})
	summary, readError := testClient.GetPipelineRunTests(context.Background(),
		PipelineSelector{RepositoryID: "repo-1"}, "run/1", 5)
	if readError != nil {
		t.Fatalf("GetPipelineRunTests error = %v", readError)
	}
	if capturedPath != "/api/v1/org/pipeline-repositories/repo-1/pipeline-runs/run/1/tests" &&
		capturedPath != "/api/v1/org/pipeline-repositories/repo-1/pipeline-runs/run%2F1/tests" {
		t.Errorf("path = %q", capturedPath)
	}
	if capturedQuery != "slowest=5" {
		t.Errorf("query = %q", capturedQuery)
	}
	if !summary.HasReports || summary.Counts.Failed != 1 || len(summary.Failed) != 1 ||
		summary.Failed[0].FailureMessage != "boom" || summary.Reports[0].Status != "ingested" {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestGetPipelineRunTestsLeavesANegativeSlowestToTheServer(t *testing.T) {
	var capturedQuery string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		_, _ = fmt.Fprint(w, `{"run_id":"run-1","has_reports":false}`)
	})
	if _, readError := testClient.GetPipelineRunTests(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, "run-1", -1); readError != nil {
		t.Fatalf("GetPipelineRunTests error = %v", readError)
	}
	if capturedQuery != "" {
		t.Errorf("query = %q, want none", capturedQuery)
	}
}

func TestGetPipelineTestHistoryEncodesTheKeyAndOptions(t *testing.T) {
	var capturedPath string
	var capturedQuery url.Values
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath, capturedQuery = r.URL.Path, r.URL.Query()
		_, _ = fmt.Fprint(w, `{"test_key":"go:p/t","branch":"main","observed":0,"flaky":0,"failed":0,
			"flaky_rate":null,"failure_rate":null,"entries":[]}`)
	})
	history, readError := testClient.GetPipelineTestHistory(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, "go:p/t&x", PipelineTestHistoryOptions{Branch: "release/1", Runs: 7})
	if readError != nil {
		t.Fatalf("GetPipelineTestHistory error = %v", readError)
	}
	if capturedPath != "/api/v1/org/applications/app-1/pipeline-tests/history" {
		t.Errorf("path = %q", capturedPath)
	}
	if capturedQuery.Get("test_key") != "go:p/t&x" || capturedQuery.Get("branch") != "release/1" ||
		capturedQuery.Get("runs") != "7" {
		t.Errorf("query = %v", capturedQuery)
	}
	if history.FlakyRate != nil || history.FailureRate != nil {
		t.Errorf("a null rate decoded as %v / %v, want nil", history.FlakyRate, history.FailureRate)
	}
}

func TestGetPipelineTestTimingsSendsOnlyWhatWasGiven(t *testing.T) {
	var capturedQuery url.Values
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		_, _ = fmt.Fprint(w, `{"stage":"e2e","branch":"main","group_by":"file","runs_sampled":0,
			"is_partial":false,"entries":[]}`)
	})
	timings, readError := testClient.GetPipelineTestTimings(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, "e2e", PipelineTestTimingsOptions{})
	if readError != nil {
		t.Fatalf("GetPipelineTestTimings error = %v", readError)
	}
	if capturedQuery.Get("stage") != "e2e" || capturedQuery.Has("branch") || capturedQuery.Has("group_by") ||
		capturedQuery.Has("runs") {
		t.Errorf("query = %v, want only stage", capturedQuery)
	}
	if timings.RunsSampled != 0 || timings.GroupBy != "file" {
		t.Errorf("timings = %+v", timings)
	}
}

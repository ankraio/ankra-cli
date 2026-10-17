package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestGetPipelineRunTimeSavedReadsTheRunRoute(t *testing.T) {
	capturedPath := ""
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		_, _ = fmt.Fprint(w, `{"run_id":"run-9","saved_seconds":840,"saved_cpu_minutes":28.5,
			"estimated_count":1,"unknown_count":1,"savings":[
			{"step_key":"build","stage":"build","reason":"path_filter","cause_stage":null,"basis":"stage_history",
			 "sample_count":20,"saved_seconds":840,"saved_cpu_minutes":28.5,"source_run_id":null},
			{"step_key":"e2e","stage":"e2e","reason":"dependency_skipped","cause_stage":"build","basis":"no_history",
			 "sample_count":0,"saved_seconds":null,"saved_cpu_minutes":null,"source_run_id":null}],
			"method":{"statistic":"median","sample_window":20,"history_run_scan":300}}`)
	})
	saved, readError := testClient.GetPipelineRunTimeSaved(context.Background(),
		PipelineSelector{RepositoryID: "repo-1"}, "run-9")
	if readError != nil {
		t.Fatalf("GetPipelineRunTimeSaved: %v", readError)
	}
	if capturedPath != "/api/v1/org/pipeline-repositories/repo-1/pipeline-runs/run-9/time-saved" {
		t.Errorf("path = %q", capturedPath)
	}
	if saved.SavedSeconds != 840 || len(saved.Savings) != 2 || saved.Savings[1].SavedSeconds != nil ||
		saved.Savings[1].CauseStage == nil || *saved.Savings[1].CauseStage != "build" || saved.Method.SampleWindow != 20 {
		t.Fatalf("decoded = %+v", saved)
	}
}

// A platform older than the route answers the router's 404, whose body is a
// detail string the generic pipeline error mapping would turn into a bare
// error without its status. It must read as "unavailable", not as a failure.
func TestGetPipelineRunTimeSavedTreatsA404AsUnavailable(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Not Found"}`)
	})
	_, readError := testClient.GetPipelineRunTimeSaved(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, "run-9")
	if !errors.Is(readError, ErrPipelineTimeSavedUnavailable) {
		t.Fatalf("error = %v, want ErrPipelineTimeSavedUnavailable", readError)
	}
}

func TestGetPipelineRunTimeSavedKeepsOtherErrors(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"detail":"database unavailable"}`)
	})
	_, readError := testClient.GetPipelineRunTimeSaved(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, "run-9")
	if readError == nil || errors.Is(readError, ErrPipelineTimeSavedUnavailable) {
		t.Fatalf("error = %v, want the server's error", readError)
	}
}

func TestGetPipelineTimeSavedSummaryAddressesTheOrganisationOrOnePipeline(t *testing.T) {
	for name, testCase := range map[string]struct {
		selector PipelineSelector
		window   string
		path     string
		query    string
	}{
		"organisation": {selector: PipelineSelector{}, window: "30d",
			path: "/api/v1/org/pipelines/time-saved", query: "window=30d"},
		"repository": {selector: PipelineSelector{RepositoryID: "repo-1"}, window: "7d",
			path: "/api/v1/org/pipeline-repositories/repo-1/pipeline-time-saved", query: "window=7d"},
		"application": {selector: PipelineSelector{ApplicationID: "app-1"},
			path: "/api/v1/org/applications/app-1/pipeline-time-saved", query: ""},
	} {
		t.Run(name, func(t *testing.T) {
			capturedPath, capturedQuery := "", ""
			testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				capturedPath, capturedQuery = r.URL.Path, r.URL.RawQuery
				_, _ = fmt.Fprint(w, `{"window":"7d","from":"2026-10-10T00:00:00Z","to":"2026-10-17T00:00:00Z",
					"runs":3,"saved_seconds":1200,"saved_cpu_minutes":null,"estimated_count":4,"unknown_count":1,
					"reasons":[{"reason":"path_filter","saved_seconds":1200,"saved_cpu_minutes":null,
					"estimated_count":4,"unknown_count":1}],
					"repositories":[{"repository_id":"repo-1","full_name":"acme/api","runs":3,"saved_seconds":1200,
					"saved_cpu_minutes":null,"estimated_count":4,"unknown_count":1}],
					"days":[],"method":{"statistic":"median","sample_window":20,"history_run_scan":300}}`)
			})
			summary, readError := testClient.GetPipelineTimeSavedSummary(context.Background(),
				testCase.selector, testCase.window)
			if readError != nil {
				t.Fatalf("GetPipelineTimeSavedSummary: %v", readError)
			}
			if capturedPath != testCase.path || capturedQuery != testCase.query {
				t.Errorf("requested %q?%q, want %q?%q", capturedPath, capturedQuery, testCase.path, testCase.query)
			}
			if summary.SavedSeconds != 1200 || summary.Runs != 3 || len(summary.Reasons) != 1 ||
				summary.Reasons[0].SavedSeconds != 1200 || summary.Repositories[0].FullName != "acme/api" {
				t.Fatalf("decoded = %+v", summary)
			}
		})
	}
}

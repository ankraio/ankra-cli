package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

const testEnvironmentsBody = `{"run_id":"run-9","axes":["kubernetes","postgres"],
	"summary":{"total":2,"passed":1,"failed":0,"errored":0,"cancelled":0,"timed_out":0,"running":0,
	 "queued":1,"torn_down":1,"verdict":"running"},
	"environments":[
	{"step_id":"step-1","step_key":"test-matrix:kubernetes=1.36,postgres=17","stage":"test-matrix","attempt":1,
	 "matrix":{"kubernetes":"1.36","postgres":"17"},"kubernetes_version":"1.36","postgres_version":"17",
	 "provider":"ankracloud","plan":"","status":"destroyed","result":"passed","cluster_id":"cluster-1",
	 "message":"Tests passed.","test_log_tail":"ok","created_at":"2026-11-09T10:00:00Z",
	 "ready_at":"2026-11-09T10:02:00Z","concluded_at":"2026-11-09T10:05:12Z",
	 "destroyed_at":"2026-11-09T10:06:00Z","expires_at":"2026-11-09T12:00:00Z","duration_seconds":312},
	{"step_id":"step-2","step_key":"test-matrix:kubernetes=1.37,postgres=17","stage":"test-matrix","attempt":1,
	 "matrix":{"kubernetes":"1.37","postgres":"17"},"kubernetes_version":"1.37","postgres_version":"17",
	 "provider":"ankracloud","plan":"","status":"queued","result":null,"cluster_id":null,
	 "message":"","test_log_tail":"","created_at":null,"ready_at":null,"concluded_at":null,
	 "destroyed_at":null,"expires_at":null,"duration_seconds":null}]}`

func TestGetPipelineRunTestEnvironmentsReadsTheRunRoute(t *testing.T) {
	for name, testCase := range map[string]struct {
		selector PipelineSelector
		path     string
	}{
		"repository": {selector: PipelineSelector{RepositoryID: "repo-1"},
			path: "/api/v1/org/pipeline-repositories/repo-1/pipeline-runs/run-9/test-environments"},
		"application": {selector: PipelineSelector{ApplicationID: "app-1"},
			path: "/api/v1/org/applications/app-1/pipeline-runs/run-9/test-environments"},
	} {
		t.Run(name, func(t *testing.T) {
			capturedPath := ""
			testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				_, _ = fmt.Fprint(w, testEnvironmentsBody)
			})
			environments, readError := testClient.GetPipelineRunTestEnvironments(context.Background(),
				testCase.selector, "run-9")
			if readError != nil {
				t.Fatalf("GetPipelineRunTestEnvironments: %v", readError)
			}
			if capturedPath != testCase.path {
				t.Errorf("path = %q, want %q", capturedPath, testCase.path)
			}
			if environments.RunID != "run-9" || len(environments.Axes) != 2 || environments.Summary.Total != 2 ||
				environments.Summary.Verdict != "running" || len(environments.Environments) != 2 {
				t.Fatalf("decoded = %+v", environments)
			}
			passed := environments.Environments[0]
			if passed.Result == nil || *passed.Result != "passed" || passed.DurationSeconds == nil ||
				*passed.DurationSeconds != 312 || passed.Matrix["postgres"] != "17" || passed.ClusterID == nil {
				t.Errorf("passed leg = %+v", passed)
			}
			if len(environments.RawJSON()) == 0 {
				t.Error("the platform's document was not kept for structured output")
			}
		})
	}
}

func TestGetPipelineRunTestEnvironmentsDecodesNullsAsNil(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, testEnvironmentsBody)
	})
	environments, readError := testClient.GetPipelineRunTestEnvironments(context.Background(),
		PipelineSelector{RepositoryID: "repo-1"}, "run-9")
	if readError != nil {
		t.Fatalf("GetPipelineRunTestEnvironments: %v", readError)
	}
	queued := environments.Environments[1]
	if queued.Status != "queued" || queued.Result != nil || queued.ClusterID != nil || queued.CreatedAt != nil ||
		queued.ReadyAt != nil || queued.ConcludedAt != nil || queued.DestroyedAt != nil || queued.ExpiresAt != nil ||
		queued.DurationSeconds != nil {
		t.Fatalf("queued leg = %+v, want every nullable field nil", queued)
	}
}

func TestGetPipelineRunTestEnvironmentsTreatsA404AsUnavailable(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Pipeline run not found"}`)
	})
	_, readError := testClient.GetPipelineRunTestEnvironments(context.Background(),
		PipelineSelector{RepositoryID: "repo-1"}, "run-9")
	if !errors.Is(readError, ErrPipelineTestEnvironmentsUnavailable) {
		t.Fatalf("error = %v, want ErrPipelineTestEnvironmentsUnavailable", readError)
	}
}

func TestGetPipelineRunTestEnvironmentsKeepsOtherErrors(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"detail":"database unavailable"}`)
	})
	_, readError := testClient.GetPipelineRunTestEnvironments(context.Background(),
		PipelineSelector{RepositoryID: "repo-1"}, "run-9")
	if readError == nil || errors.Is(readError, ErrPipelineTestEnvironmentsUnavailable) {
		t.Fatalf("error = %v, want the server's error", readError)
	}
}

func TestGetPipelineRunTestEnvironmentsNeedsASelector(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request should be sent without a selector")
	})
	_, readError := testClient.GetPipelineRunTestEnvironments(context.Background(), PipelineSelector{}, "run-9")
	if !errors.Is(readError, ErrPipelineSelectorRequired) {
		t.Fatalf("error = %v, want ErrPipelineSelectorRequired", readError)
	}
}

func TestGetPipelineRunTestEnvironmentsDefaultsMissingListsToEmpty(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"run_id":"run-9","summary":{"total":0,"verdict":"none"}}`)
	})
	environments, readError := testClient.GetPipelineRunTestEnvironments(context.Background(),
		PipelineSelector{RepositoryID: "repo-1"}, "run-9")
	if readError != nil {
		t.Fatalf("GetPipelineRunTestEnvironments: %v", readError)
	}
	if environments.Axes == nil || environments.Environments == nil {
		t.Fatalf("decoded = %+v, want empty lists", environments)
	}
}

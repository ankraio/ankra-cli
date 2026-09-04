package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestListPipelineRunsHappyPath(t *testing.T) {
	var capturedPath, capturedQuery string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.RawQuery
		_, _ = fmt.Fprint(w, `{"runs":[{"id":"run-1","run_id":"umbrella-1","organisation_id":"org-1",
			"repository_id":"repo-1","application_id":"app-1","definition_id":"def-1","run_number":3,
			"trigger":"push","trigger_ref":"refs/heads/main","head_sha":"`+strings.Repeat("a", 40)+`",
			"base_sha":"","is_fork":false,"concurrency_group":"repo-1:main","status":"concluded",
			"outcome":"success","requested_by":"user:1","queued_at":"2026-09-01T00:00:00Z",
			"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}],"next_cursor":null}`)
	})

	list, err := testClient.ListPipelineRuns(context.Background(),
		PipelineSelector{ApplicationID: "app-1"}, ListPipelineRunsOptions{Status: "concluded", Limit: 10})
	if err != nil {
		t.Fatalf("ListPipelineRuns error = %v", err)
	}
	if capturedPath != "/api/v1/org/applications/app-1/pipeline-runs" {
		t.Errorf("path = %q", capturedPath)
	}
	if !strings.Contains(capturedQuery, "status=concluded") || !strings.Contains(capturedQuery, "limit=10") {
		t.Errorf("query = %q", capturedQuery)
	}
	if len(list.Runs) != 1 || list.Runs[0].ID != "run-1" || list.Runs[0].RunNumber != 3 {
		t.Fatalf("runs = %+v", list.Runs)
	}
	if list.NextCursor != nil {
		t.Errorf("next cursor = %v, want nil", list.NextCursor)
	}
}

func TestListPipelineRunsEmpty(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"runs":[],"next_cursor":null}`)
	})
	list, err := testClient.ListPipelineRuns(context.Background(), PipelineSelector{RepositoryID: "repo-1"}, ListPipelineRunsOptions{})
	if err != nil {
		t.Fatalf("ListPipelineRuns error = %v", err)
	}
	if len(list.Runs) != 0 {
		t.Fatalf("runs = %+v, want empty", list.Runs)
	}
}

func TestListPipelineRunsByRepositoryPath(t *testing.T) {
	var capturedPath string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		_, _ = fmt.Fprint(w, `{"runs":[],"next_cursor":null}`)
	})
	if _, err := testClient.ListPipelineRuns(context.Background(), PipelineSelector{RepositoryID: "repo-9"}, ListPipelineRunsOptions{}); err != nil {
		t.Fatalf("ListPipelineRuns error = %v", err)
	}
	if capturedPath != "/api/v1/org/pipeline-repositories/repo-9/pipeline-runs" {
		t.Errorf("path = %q", capturedPath)
	}
}

func TestGetPipelineRunNotFound(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Pipeline run not found"}`)
	})
	_, err := testClient.GetPipelineRun(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "missing-run")
	if err == nil {
		t.Fatal("expected an error")
	}
	// The server's sentinel text is rendered verbatim, so a caller cannot
	// tell "absent" from "belongs to another organisation" - matching the
	// route's own contract.
	if err.Error() != "Pipeline run not found" {
		t.Errorf("error = %q, want the server's sentinel text verbatim", err.Error())
	}
}

func TestCreatePipelineRunRequiresHeadSHA(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(w, `{"detail":"A pipeline run needs the full commit sha to run at"}`)
	})
	_, err := testClient.CreatePipelineRun(context.Background(), PipelineSelector{ApplicationID: "app-1"},
		CreatePipelineRunRequest{Ref: "main"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if err.Error() != "A pipeline run needs the full commit sha to run at" {
		t.Errorf("error = %q", err.Error())
	}
}

// TestCreatePipelineRunPlanRefusalCarriesDiagnostics pins the
// PipelineValidationError shape the planner-refusal 422
// ({"detail": "...", "diagnostics": [...]}) decodes into, so a caller can
// show every diagnostic the planner recorded, not just the headline reason.
func TestCreatePipelineRunPlanRefusalCarriesDiagnostics(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(w, `{"detail":"This pipeline definition has at least one fatal violation",
			"diagnostics":["pipeline_stages: stage \"build\" has no kind","pipeline_matrix: matrix leg 2 is empty"]}`)
	})
	_, err := testClient.CreatePipelineRun(context.Background(), PipelineSelector{ApplicationID: "app-1"},
		CreatePipelineRunRequest{HeadSHA: strings.Repeat("a", 40)})
	if err == nil {
		t.Fatal("expected an error")
	}
	var validationError *PipelineValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error = %v (%T), want *PipelineValidationError", err, err)
	}
	if validationError.Reason != "This pipeline definition has at least one fatal violation" {
		t.Errorf("reason = %q", validationError.Reason)
	}
	if len(validationError.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %v", validationError.Diagnostics)
	}
}

// TestPipelineScheduleValidationErrorFlattensPydanticShape pins the other
// 422 shape a schedule write can answer: pydantic's
// {"detail": [{"loc": [...], "msg": "..."}]}, which carries no diagnostics
// array and so must not be mistaken for a plan refusal.
func TestPipelineScheduleValidationErrorFlattensPydanticShape(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(w, `{"detail":[{"loc":["body","cron"],"msg":"invalid cron expression"}]}`)
	})
	_, err := testClient.CreatePipelineSchedule(context.Background(), PipelineSelector{ApplicationID: "app-1"},
		CreatePipelineScheduleRequest{Cron: "not a cron"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "cron") || !strings.Contains(err.Error(), "invalid cron expression") {
		t.Errorf("error = %q, want it to name the field and the message", err.Error())
	}
	var validationError *PipelineValidationError
	if errors.As(err, &validationError) {
		t.Errorf("a pydantic validation error must not decode as *PipelineValidationError: %+v", validationError)
	}
}

func TestCancelPipelineRunAlreadyConcluded(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"detail":"This pipeline run has already concluded"}`)
	})
	_, err := testClient.CancelPipelineRun(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "run-1")
	if err == nil || err.Error() != "This pipeline run has already concluded" {
		t.Fatalf("error = %v", err)
	}
}

func TestSelectorRequiresExactlyOneAddress(t *testing.T) {
	if _, err := (PipelineSelector{}).basePath(); !errors.Is(err, ErrPipelineSelectorRequired) {
		t.Errorf("empty selector error = %v, want ErrPipelineSelectorRequired", err)
	}
}

func TestListPipelineArtifactsEmptyUntilTheStoreLands(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"artifacts":[]}`)
	})
	list, err := testClient.ListPipelineArtifacts(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "run-1")
	if err != nil {
		t.Fatalf("ListPipelineArtifacts error = %v", err)
	}
	if len(list.Artifacts) != 0 {
		t.Fatalf("artifacts = %+v, want empty", list.Artifacts)
	}
}

func TestDownloadPipelineArtifactUnavailablePlaceholder(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Artifacts are not available for this run"}`)
	})
	var buf strings.Builder
	err := testClient.DownloadPipelineArtifact(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "artifact-1", &buf)
	if err == nil || err.Error() != "Artifacts are not available for this run" {
		t.Fatalf("error = %v", err)
	}
}

func TestValidatePipelineDefinitionEmptySpecValidatesStored(t *testing.T) {
	var capturedBody string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		_, _ = fmt.Fprint(w, `{"severity":"ok","violations":[],"events":[]}`)
	})
	validation, err := testClient.ValidatePipelineDefinition(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "")
	if err != nil {
		t.Fatalf("ValidatePipelineDefinition error = %v", err)
	}
	if validation.Severity != "ok" {
		t.Errorf("severity = %q", validation.Severity)
	}
	if !strings.Contains(capturedBody, `"spec_yaml":""`) {
		t.Errorf("body = %q, want an explicit empty spec_yaml", capturedBody)
	}
}

func TestUpdatePipelineScheduleOnlyChangedFieldsCross(t *testing.T) {
	var capturedBody string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		_, _ = fmt.Fprint(w, `{"id":"sched-1","repository_id":"repo-1","cron":"0 0 * * *","timezone":"UTC","ref":"main",
			"inputs":{},"enabled":false,"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`)
	})
	enabled := false
	schedule, err := testClient.UpdatePipelineSchedule(context.Background(), PipelineSelector{ApplicationID: "app-1"},
		"sched-1", UpdatePipelineScheduleRequest{Enabled: &enabled})
	if err != nil {
		t.Fatalf("UpdatePipelineSchedule error = %v", err)
	}
	if schedule.Enabled {
		t.Errorf("enabled = %v, want false", schedule.Enabled)
	}
	// Only "enabled" was set on the request, so the body must not carry
	// cron/timezone/ref/inputs at all - the server contract is "an omitted
	// field leaves the stored value alone".
	for _, field := range []string{"cron", "timezone", "ref", "inputs"} {
		if strings.Contains(capturedBody, `"`+field+`"`) {
			t.Errorf("body = %q, must not carry unset field %q", capturedBody, field)
		}
	}
}

func TestDeletePipelineScheduleNotFound(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Pipeline schedule not found"}`)
	})
	err := testClient.DeletePipelineSchedule(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "sched-missing")
	if err == nil || err.Error() != "Pipeline schedule not found" {
		t.Fatalf("error = %v", err)
	}
}

// TestGetPipelineDefinitionApprovalTargetsTheOrganisationRoute pins that the
// definition-approval read is addressed by the definition's own id alone -
// no PipelineSelector prefix, unlike every other route in this file - and
// decodes every field of the response.
func TestGetPipelineDefinitionApprovalTargetsTheOrganisationRoute(t *testing.T) {
	var capturedMethod, capturedPath string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		_, _ = fmt.Fprint(w, `{"definition_id":"def-1","protected_hash":`+
			`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"approved_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"approved_at":"2026-09-01T00:00:00Z","approved_by":"user-1"}`)
	})

	approval, err := testClient.GetPipelineDefinitionApproval(context.Background(), "def-1")
	if err != nil {
		t.Fatalf("GetPipelineDefinitionApproval error = %v", err)
	}
	if capturedMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", capturedMethod)
	}
	if capturedPath != "/api/v1/org/pipelines/definitions/def-1" {
		t.Errorf("path = %s", capturedPath)
	}
	if approval.DefinitionID != "def-1" || approval.ApprovedBy != "user-1" {
		t.Errorf("approval = %+v", approval)
	}
	if approval.ApprovedHash != approval.ProtectedHash {
		t.Errorf("approved hash = %q, want it to equal the protected hash %q", approval.ApprovedHash, approval.ProtectedHash)
	}
	if approval.ApprovedAt == nil || *approval.ApprovedAt != "2026-09-01T00:00:00Z" {
		t.Errorf("approved at = %v", approval.ApprovedAt)
	}
}

// TestGetPipelineDefinitionApprovalNotFound pins that an unknown id's 404
// sentinel reaches the caller verbatim.
func TestGetPipelineDefinitionApprovalNotFound(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Pipeline definition not found"}`)
	})
	_, err := testClient.GetPipelineDefinitionApproval(context.Background(), "missing-definition")
	if err == nil || err.Error() != "Pipeline definition not found" {
		t.Fatalf("error = %v, want the server's sentinel text verbatim", err)
	}
}

// TestApprovePipelineDefinitionSendsNoBody pins the method, path and empty
// body of the approve route - a path typo or a stray request body here
// passes every command-level test that only checks the wiring and fails as
// a 404/422 against a real platform.
func TestApprovePipelineDefinitionSendsNoBody(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedContentLength int64
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedContentLength = r.ContentLength
		_, _ = fmt.Fprint(w, `{"definition_id":"def-1",`+
			`"protected_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",`+
			`"approved_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",`+
			`"approved_at":"2026-09-01T00:00:00Z","approved_by":"user-1"}`)
	})

	approval, err := testClient.ApprovePipelineDefinition(context.Background(), "def-1")
	if err != nil {
		t.Fatalf("ApprovePipelineDefinition error = %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", capturedMethod)
	}
	if capturedPath != "/api/v1/org/pipelines/definitions/def-1/approve" {
		t.Errorf("path = %s", capturedPath)
	}
	if capturedContentLength > 0 {
		t.Errorf("content length = %d, want no body", capturedContentLength)
	}
	if approval.ApprovedHash == "" {
		t.Errorf("approved hash = %q, want it set", approval.ApprovedHash)
	}
}

// TestApprovePipelineDefinitionConflict and
// TestApprovePipelineDefinitionForbidden pin that the approval route's 409
// and 403 sentinels - not-current, already-approved, and no-human-actor -
// reach the caller verbatim, the way TestGetPipelineDefinitionApprovalNotFound
// pins its 404.
func TestApprovePipelineDefinitionConflict(t *testing.T) {
	for _, sentinel := range []string{
		"Only the repository's current default-branch definition can be approved",
		"This pipeline definition is already approved",
		"This pipeline definition's protected sections have not been assessed",
	} {
		t.Run(sentinel, func(t *testing.T) {
			testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, _ = fmt.Fprintf(w, `{"detail":%q}`, sentinel)
			})
			_, err := testClient.ApprovePipelineDefinition(context.Background(), "def-1")
			if err == nil || err.Error() != sentinel {
				t.Fatalf("error = %v, want the server's sentinel text verbatim", err)
			}
		})
	}
}

func TestApprovePipelineDefinitionForbidden(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"detail":"A pipeline definition's authority can only be approved by a human administrator"}`)
	})
	_, err := testClient.ApprovePipelineDefinition(context.Background(), "def-1")
	if err == nil || err.Error() != "A pipeline definition's authority can only be approved by a human administrator" {
		t.Fatalf("error = %v, want the server's sentinel text verbatim", err)
	}
	// This 403 body's detail is not the RBAC "permission_denied" shape, so it
	// must not decode as a *PermissionDeniedError - that would rewrite the
	// human-actor sentinel into a generic permission message.
	var permissionDenied *PermissionDeniedError
	if errors.As(err, &permissionDenied) {
		t.Errorf("decoded as *PermissionDeniedError: %+v, want the plain sentinel error", permissionDenied)
	}
}

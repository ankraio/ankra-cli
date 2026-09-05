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

func TestListPipelineArtifactsEmpty(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"artifacts":[],"next_cursor":null}`)
	})
	list, err := testClient.ListPipelineArtifacts(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "run-1",
		ListPipelineArtifactsOptions{})
	if err != nil {
		t.Fatalf("ListPipelineArtifacts error = %v", err)
	}
	if len(list.Artifacts) != 0 {
		t.Fatalf("artifacts = %+v, want empty", list.Artifacts)
	}
}

func TestListPipelineArtifactsDecodesKindStatusAndNullableStepID(t *testing.T) {
	var capturedPath string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		_, _ = fmt.Fprint(w, `{"artifacts":[
			{"id":"artifact-1","run_id":"run-1","step_id":"step-1","kind":"step_log","name":"step.log",
			 "content_type":"text/plain; charset=utf-8","size_bytes":512,"sha256":"deadbeef",
			 "status":"uploaded","error_message":"","expires_at":"2026-10-01T00:00:00Z",
			 "created_at":"2026-09-01T00:00:00Z","uploaded_at":"2026-09-01T00:05:00Z"},
			{"id":"artifact-2","run_id":"run-1","step_id":null,"kind":"artifact","name":"coverage.xml",
			 "content_type":"application/octet-stream","size_bytes":0,"sha256":"","status":"pending",
			 "error_message":"","expires_at":"2026-10-01T00:00:00Z","created_at":"2026-09-01T00:00:00Z",
			 "uploaded_at":null}
		],"next_cursor":null}`)
	})
	list, err := testClient.ListPipelineArtifacts(context.Background(), PipelineSelector{RepositoryID: "repo-1"}, "run-1",
		ListPipelineArtifactsOptions{})
	if err != nil {
		t.Fatalf("ListPipelineArtifacts error = %v", err)
	}
	if capturedPath != "/api/v1/org/pipeline-repositories/repo-1/pipeline-runs/run-1/artifacts" {
		t.Errorf("path = %q", capturedPath)
	}
	if len(list.Artifacts) != 2 {
		t.Fatalf("artifacts = %+v", list.Artifacts)
	}
	stepLog := list.Artifacts[0]
	if stepLog.Kind != PipelineArtifactKindStepLog || stepLog.Status != PipelineArtifactStatusUploaded {
		t.Errorf("step log artifact = %+v", stepLog)
	}
	if stepLog.StepID == nil || *stepLog.StepID != "step-1" {
		t.Errorf("step log artifact step id = %v, want \"step-1\"", stepLog.StepID)
	}
	declared := list.Artifacts[1]
	if declared.Kind != PipelineArtifactKindArtifact || declared.Status != PipelineArtifactStatusPending {
		t.Errorf("declared artifact = %+v", declared)
	}
	if declared.StepID != nil {
		t.Errorf("declared artifact step id = %v, want nil (a run-level object)", declared.StepID)
	}
}

func TestListPipelineArtifactsSendsCursorAndLimit(t *testing.T) {
	var capturedCursor, capturedLimit string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedCursor = r.URL.Query().Get("cursor")
		capturedLimit = r.URL.Query().Get("limit")
		_, _ = fmt.Fprint(w, `{"artifacts":[],"next_cursor":"cursor-2"}`)
	})
	list, err := testClient.ListPipelineArtifacts(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "run-1",
		ListPipelineArtifactsOptions{Cursor: "cursor-1", Limit: 100})
	if err != nil {
		t.Fatalf("ListPipelineArtifacts error = %v", err)
	}
	if capturedCursor != "cursor-1" || capturedLimit != "100" {
		t.Errorf("query cursor = %q, limit = %q, want \"cursor-1\" and \"100\"", capturedCursor, capturedLimit)
	}
	if list.NextCursor == nil || *list.NextCursor != "cursor-2" {
		t.Errorf("next cursor = %v, want the server's own", list.NextCursor)
	}
}

func TestListPipelineArtifactsOmitsUnsetPaging(t *testing.T) {
	var capturedRawQuery string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedRawQuery = r.URL.RawQuery
		_, _ = fmt.Fprint(w, `{"artifacts":[],"next_cursor":null}`)
	})
	if _, err := testClient.ListPipelineArtifacts(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "run-1",
		ListPipelineArtifactsOptions{}); err != nil {
		t.Fatalf("ListPipelineArtifacts error = %v", err)
	}
	if capturedRawQuery != "" {
		t.Errorf("raw query = %q, want none so the server chooses its own page", capturedRawQuery)
	}
}

func TestDownloadPipelineArtifactNotFound(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Pipeline artifact not found"}`)
	})
	var buf strings.Builder
	err := testClient.DownloadPipelineArtifact(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "artifact-1", &buf)
	if err == nil || err.Error() != "Pipeline artifact not found" {
		t.Fatalf("error = %v, want the server's sentinel text verbatim", err)
	}
}

func TestDownloadPipelineArtifactNotYetUploaded(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"detail":"This artifact has not been uploaded yet"}`)
	})
	var buf strings.Builder
	err := testClient.DownloadPipelineArtifact(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "artifact-1", &buf)
	if err == nil || err.Error() != "This artifact has not been uploaded yet" {
		t.Fatalf("error = %v, want the server's 409 sentinel text verbatim", err)
	}
}

func TestDownloadPipelineArtifactHappyPathStreamsTheBody(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/org/applications/app-1/artifacts/artifact-1/download" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, "line one\nline two\n")
	})
	var buf strings.Builder
	err := testClient.DownloadPipelineArtifact(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "artifact-1", &buf)
	if err != nil {
		t.Fatalf("DownloadPipelineArtifact error = %v", err)
	}
	if buf.String() != "line one\nline two\n" {
		t.Errorf("body = %q", buf.String())
	}
}

func TestListPipelineFindingsHappyPath(t *testing.T) {
	var capturedPath string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		_, _ = fmt.Fprint(w, `{"findings":[{"id":"finding-1","run_id":"run-1","step_id":"step-1",
			"head_sha":"`+strings.Repeat("a", 40)+`","tool":"trivy","severity":"CRITICAL",
			"identity_hash":"hash-1","rule_id":"","cve_id":"CVE-2026-1234","package_name":"openssl",
			"package_version":"1.0.0","fixed_version":"1.0.1","path":"","line":null,
			"title":"OpenSSL vulnerability","detail":{},"first_seen_run_id":"run-1",
			"first_seen_at":"2026-09-01T00:00:00Z","created_at":"2026-09-01T00:00:00Z",
			"updated_at":"2026-09-01T00:00:00Z"}]}`)
	})
	list, err := testClient.ListPipelineFindings(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "run-1")
	if err != nil {
		t.Fatalf("ListPipelineFindings error = %v", err)
	}
	if capturedPath != "/api/v1/org/applications/app-1/pipeline-runs/run-1/findings" {
		t.Errorf("path = %q", capturedPath)
	}
	if len(list.Findings) != 1 || list.Findings[0].Tool != PipelineFindingToolTrivy ||
		list.Findings[0].Severity != PipelineFindingSeverityCritical {
		t.Fatalf("findings = %+v", list.Findings)
	}
	if list.Findings[0].CVEID == nil || *list.Findings[0].CVEID != "CVE-2026-1234" {
		t.Errorf("cve id = %v", list.Findings[0].CVEID)
	}
}

func TestListPipelineFindingsEmptyRunHasNoScanStep(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"findings":[]}`)
	})
	list, err := testClient.ListPipelineFindings(context.Background(), PipelineSelector{RepositoryID: "repo-1"}, "run-1")
	if err != nil {
		t.Fatalf("ListPipelineFindings error = %v", err)
	}
	if len(list.Findings) != 0 {
		t.Fatalf("findings = %+v, want empty", list.Findings)
	}
}

func TestListPipelineFindingsRunNotFound(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Pipeline run not found"}`)
	})
	_, err := testClient.ListPipelineFindings(context.Background(), PipelineSelector{ApplicationID: "app-1"}, "missing-run")
	if err == nil || err.Error() != "Pipeline run not found" {
		t.Fatalf("error = %v, want the server's sentinel text verbatim", err)
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

func TestConnectPipelineRepositoryHappyPath(t *testing.T) {
	var capturedPath, capturedBody string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"id":"repo-1","organisation_id":"org-1","provider":"github","owner":"acme",
			"name":"webapp","credential_name":"github-app","default_branch":"main","application_id":null,
			"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z",
			"definition":{"status":"recorded","detail":"Recorded the committed .ankra/pipeline.yaml as the pipeline definition of record.",
			"definition_id":"def-1","spec_hash":"sha256:abc","violations":[],"read_error":null}}`)
	})
	result, err := testClient.ConnectPipelineRepository(context.Background(), ConnectPipelineRepositoryRequest{
		Provider: "github", Owner: "acme", Name: "webapp", CredentialName: "github-app",
	})
	if err != nil {
		t.Fatalf("ConnectPipelineRepository error = %v", err)
	}
	if capturedPath != "/api/v1/org/pipelines/repositories" {
		t.Errorf("path = %q", capturedPath)
	}
	if !strings.Contains(capturedBody, `"provider":"github"`) || !strings.Contains(capturedBody, `"owner":"acme"`) {
		t.Errorf("body = %q", capturedBody)
	}
	if result.ID != "repo-1" || result.Owner != "acme" || result.Name != "webapp" {
		t.Fatalf("result = %+v", result)
	}
	if result.Definition.Status != "recorded" || result.Definition.DefinitionID == nil || *result.Definition.DefinitionID != "def-1" {
		t.Errorf("definition = %+v", result.Definition)
	}
}

// TestConnectPipelineRepositoryAlreadyConnectedCarriesTheExistingID pins the
// 409 shape go/internal/pipelineapi/repositories.go writeRepositoryError
// answers for a duplicate connect: the sentence, plus the existing
// repository's id as its own field so a caller does not have to parse the
// sentence to act on it.
func TestConnectPipelineRepositoryAlreadyConnectedCarriesTheExistingID(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"detail":"This repository is already connected to pipelines (repository repo-existing)",
			"repository_id":"repo-existing"}`)
	})
	_, err := testClient.ConnectPipelineRepository(context.Background(), ConnectPipelineRepositoryRequest{
		Provider: "github", Owner: "acme", Name: "webapp",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	var alreadyConnected *PipelineRepositoryAlreadyConnectedError
	if !errors.As(err, &alreadyConnected) {
		t.Fatalf("error = %v (%T), want *PipelineRepositoryAlreadyConnectedError", err, err)
	}
	if alreadyConnected.RepositoryID != "repo-existing" {
		t.Errorf("repository id = %q", alreadyConnected.RepositoryID)
	}
	if !strings.Contains(alreadyConnected.Error(), "already connected") {
		t.Errorf("error text = %q, want the server's sentence verbatim", alreadyConnected.Error())
	}
}

// TestConnectPipelineRepositoryUnknownProviderFlattensPydanticShape pins that
// the HTTP-layer provider-enum refusal (pyvalidation.Enum, decoded ahead of
// the usecase) - the pydantic v2 {"detail": [{"loc": [...], "msg": "..."}]}
// shape - is not mistaken for either the duplicate-connect 409 or the
// planner-refusal shape.
func TestConnectPipelineRepositoryUnknownProviderFlattensPydanticShape(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(w, `{"detail":[{"loc":["body","provider"],"msg":"value is not a valid enumeration member"}]}`)
	})
	_, err := testClient.ConnectPipelineRepository(context.Background(), ConnectPipelineRepositoryRequest{
		Provider: "svn", Owner: "acme", Name: "webapp",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "provider") || !strings.Contains(err.Error(), "enumeration") {
		t.Errorf("error = %q, want it to name the field and the message", err.Error())
	}
	var alreadyConnected *PipelineRepositoryAlreadyConnectedError
	if errors.As(err, &alreadyConnected) {
		t.Errorf("a provider refusal must not decode as *PipelineRepositoryAlreadyConnectedError: %+v", alreadyConnected)
	}
}

func TestConnectPipelineRepositoryIdentityRequired(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(w, `{"detail":"A pipeline repository needs an owner and a name"}`)
	})
	_, err := testClient.ConnectPipelineRepository(context.Background(), ConnectPipelineRepositoryRequest{Provider: "github"})
	if err == nil || err.Error() != "A pipeline repository needs an owner and a name" {
		t.Fatalf("error = %v, want the sentinel text verbatim", err)
	}
}

func TestListPipelineRepositoriesHappyPath(t *testing.T) {
	var capturedPath, capturedQuery string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.RawQuery
		_, _ = fmt.Fprint(w, `{"repositories":[{"id":"repo-1","organisation_id":"org-1","provider":"github",
			"owner":"acme","name":"webapp","credential_name":"github-app","default_branch":"main",
			"application_id":null,"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}],
			"next_cursor":null}`)
	})
	list, err := testClient.ListPipelineRepositories(context.Background(),
		ListPipelineRepositoriesOptions{Provider: "github", Limit: 10})
	if err != nil {
		t.Fatalf("ListPipelineRepositories error = %v", err)
	}
	if capturedPath != "/api/v1/org/pipelines/repositories" {
		t.Errorf("path = %q", capturedPath)
	}
	if !strings.Contains(capturedQuery, "provider=github") || !strings.Contains(capturedQuery, "limit=10") {
		t.Errorf("query = %q", capturedQuery)
	}
	if len(list.Repositories) != 1 || list.Repositories[0].ID != "repo-1" {
		t.Fatalf("repositories = %+v", list.Repositories)
	}
	if list.NextCursor != nil {
		t.Errorf("next cursor = %v, want nil", list.NextCursor)
	}
}

func TestListPipelineRepositoriesEmpty(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"repositories":[],"next_cursor":null}`)
	})
	list, err := testClient.ListPipelineRepositories(context.Background(), ListPipelineRepositoriesOptions{})
	if err != nil {
		t.Fatalf("ListPipelineRepositories error = %v", err)
	}
	if len(list.Repositories) != 0 {
		t.Fatalf("repositories = %+v, want empty", list.Repositories)
	}
}

func TestGetPipelineRepositoryHappyPath(t *testing.T) {
	var capturedPath string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		_, _ = fmt.Fprint(w, `{"id":"repo-1","organisation_id":"org-1","provider":"gitlab","owner":"acme",
			"name":"webapp","credential_name":"","default_branch":"develop","application_id":"app-1",
			"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`)
	})
	repository, err := testClient.GetPipelineRepository(context.Background(), "repo-1")
	if err != nil {
		t.Fatalf("GetPipelineRepository error = %v", err)
	}
	if capturedPath != "/api/v1/org/pipelines/repositories/repo-1" {
		t.Errorf("path = %q", capturedPath)
	}
	if repository.DefaultBranch != "develop" || repository.ApplicationID == nil || *repository.ApplicationID != "app-1" {
		t.Fatalf("repository = %+v", repository)
	}
}

func TestGetPipelineRepositoryNotFound(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Pipeline repository not found"}`)
	})
	_, err := testClient.GetPipelineRepository(context.Background(), "repo-missing")
	if err == nil || err.Error() != "Pipeline repository not found" {
		t.Fatalf("error = %v, want the sentinel text verbatim", err)
	}
}

// TestConnectPipelineRepositoryWithApplicationAndCluster pins that
// --application/--cluster (cluster PR #2509) round-trip through the request
// body and the response's application_id/cluster_id fields.
func TestConnectPipelineRepositoryWithApplicationAndCluster(t *testing.T) {
	var capturedBody string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"id":"repo-1","organisation_id":"org-1","provider":"github","owner":"acme",
			"name":"webapp","credential_name":"","default_branch":"main","application_id":"app-1",
			"cluster_id":"cluster-1","created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z",
			"definition":{"status":"absent","detail":"no file","definition_id":null,"spec_hash":null,"violations":[],"read_error":null}}`)
	})
	result, err := testClient.ConnectPipelineRepository(context.Background(), ConnectPipelineRepositoryRequest{
		Provider: "github", Owner: "acme", Name: "webapp", ApplicationID: "app-1", ClusterID: "cluster-1",
	})
	if err != nil {
		t.Fatalf("ConnectPipelineRepository error = %v", err)
	}
	if !strings.Contains(capturedBody, `"application_id":"app-1"`) || !strings.Contains(capturedBody, `"cluster_id":"cluster-1"`) {
		t.Errorf("body = %q, want both ids sent", capturedBody)
	}
	if result.ApplicationID == nil || *result.ApplicationID != "app-1" {
		t.Errorf("application_id = %v", result.ApplicationID)
	}
	if result.ClusterID == nil || *result.ClusterID != "cluster-1" {
		t.Errorf("cluster_id = %v", result.ClusterID)
	}
}

func TestConnectPipelineRepositoryApplicationNotFound(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(w, `{"detail":"Application not found in this organisation"}`)
	})
	_, err := testClient.ConnectPipelineRepository(context.Background(), ConnectPipelineRepositoryRequest{
		Provider: "github", Owner: "acme", Name: "webapp", ApplicationID: "00000000-0000-0000-0000-000000000000",
	})
	if err == nil || err.Error() != "Application not found in this organisation" {
		t.Fatalf("error = %v, want the sentinel text verbatim", err)
	}
}

func TestConnectPipelineRepositoryClusterCannotRunPipelines(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(w, `{"detail":"This cluster's agent cannot run pipeline steps"}`)
	})
	_, err := testClient.ConnectPipelineRepository(context.Background(), ConnectPipelineRepositoryRequest{
		Provider: "github", Owner: "acme", Name: "webapp", ClusterID: "00000000-0000-0000-0000-000000000000",
	})
	if err == nil || err.Error() != "This cluster's agent cannot run pipeline steps" {
		t.Fatalf("error = %v, want the sentinel text verbatim", err)
	}
}

func TestDisconnectPipelineRepositoryHappyPath(t *testing.T) {
	var capturedMethod, capturedPath string
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	err := testClient.DisconnectPipelineRepository(context.Background(), "repo-1")
	if err != nil {
		t.Fatalf("DisconnectPipelineRepository error = %v", err)
	}
	if capturedMethod != http.MethodDelete {
		t.Errorf("method = %q", capturedMethod)
	}
	if capturedPath != "/api/v1/org/pipelines/repositories/repo-1" {
		t.Errorf("path = %q", capturedPath)
	}
}

// TestDisconnectPipelineRepositoryHasLiveRun pins the 409 cluster PR #2509
// added for a repository with a queued or running pipeline run: the plain
// {"detail": "..."} shape, rendered verbatim rather than as the typed
// duplicate-connect error (it carries no repository_id field).
func TestDisconnectPipelineRepositoryHasLiveRun(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"detail":"This repository has a pipeline run that is queued or running"}`)
	})
	err := testClient.DisconnectPipelineRepository(context.Background(), "repo-1")
	if err == nil || err.Error() != "This repository has a pipeline run that is queued or running" {
		t.Fatalf("error = %v, want the sentinel text verbatim", err)
	}
	var alreadyConnected *PipelineRepositoryAlreadyConnectedError
	if errors.As(err, &alreadyConnected) {
		t.Errorf("a live-run refusal must not decode as *PipelineRepositoryAlreadyConnectedError: %+v", alreadyConnected)
	}
}

func TestDisconnectPipelineRepositoryNotFound(t *testing.T) {
	testClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"detail":"Pipeline repository not found"}`)
	})
	err := testClient.DisconnectPipelineRepository(context.Background(), "repo-missing")
	if err == nil || err.Error() != "Pipeline repository not found" {
		t.Fatalf("error = %v, want the sentinel text verbatim", err)
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

package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const (
	testProjectID      = "071d45b1-f8fe-4f47-b13a-4f13a01689f1"
	testOtherProjectID = "3d2720de-4956-439b-b672-c0d1cf83b98a"
	testClusterID      = "1834920e-3001-4157-8938-33c447031033"
)

// recordingPlatform answers one fixed status and body and keeps every
// request's method, path, query, headers and body.
type recordingPlatform struct {
	mutex    sync.Mutex
	status   int
	body     string
	requests []projectsRecordedRequest
}

type projectsRecordedRequest struct {
	method  string
	path    string
	query   string
	headers http.Header
	body    string
}

func (platform *recordingPlatform) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	platform.mutex.Lock()
	platform.requests = append(platform.requests, projectsRecordedRequest{
		method: request.Method, path: request.URL.Path, query: request.URL.RawQuery,
		headers: request.Header.Clone(), body: string(body),
	})
	platform.mutex.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(platform.status)
	_, _ = writer.Write([]byte(platform.body))
}

func (platform *recordingPlatform) last(t *testing.T) projectsRecordedRequest {
	t.Helper()
	platform.mutex.Lock()
	defer platform.mutex.Unlock()
	if len(platform.requests) == 0 {
		t.Fatal("no request reached the platform")
	}
	return platform.requests[len(platform.requests)-1]
}

func newRecordingClient(t *testing.T, status int, body string) (*Client, *recordingPlatform) {
	t.Helper()
	platform := &recordingPlatform{status: status, body: body}
	server := httptest.NewServer(platform)
	t.Cleanup(server.Close)
	return New(testToken, server.URL), platform
}

func TestListProjectsReadsTheProjectsDocument(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK,
		`{"projects":[{"id":"`+testProjectID+`","name":"GA probe","slug":"ga-probe","description":"","is_default":false,"is_current":true,"created_at":"2026-10-25T19:11:28Z"}]}`)
	projects, err := testClient.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].Slug != "ga-probe" || !projects[0].IsCurrent {
		t.Fatalf("projects = %+v", projects)
	}
	if request := platform.last(t); request.method != http.MethodGet || request.path != "/api/v1/org/projects" {
		t.Fatalf("request = %s %s", request.method, request.path)
	}
}

func TestListProjectsEmptyDocumentIsAnEmptySlice(t *testing.T) {
	testClient, _ := newRecordingClient(t, http.StatusOK, `{"projects":null}`)
	projects, err := testClient.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if projects == nil || len(projects) != 0 {
		t.Fatalf("projects = %#v, want an empty slice", projects)
	}
}

func TestProjectRoutesClassifyTheirNotFoundAnswers(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
		want error
	}{
		{name: "projects off", body: `{"detail":"Projects are not enabled for this organisation"}`, want: ErrProjectsDisabled},
		{name: "project missing", body: `{"detail":"Project not found"}`, want: ErrProjectNotFound},
		{name: "route missing", body: `404 page not found`, want: ErrProjectsRouteMissing},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testClient, _ := newRecordingClient(t, http.StatusNotFound, testCase.body)
			_, err := testClient.GetProject(testProjectID)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("GetProject error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestProjectRoutesTurnAPlainDetail403IntoARoleRefusal(t *testing.T) {
	testClient, _ := newRecordingClient(t, http.StatusForbidden, `{"detail":"You do not have permission to manage projects"}`)
	_, err := testClient.CreateProject(CreateProjectRequest{Name: "Edge", Slug: "edge"})
	var denied *PermissionDeniedError
	if !errors.As(err, &denied) || denied.Detail != "You do not have permission to manage projects" {
		t.Fatalf("CreateProject error = %v, want a PermissionDeniedError with the platform's detail", err)
	}
}

func TestCreateProjectSendsNameSlugAndDescription(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusCreated,
		`{"id":"`+testProjectID+`","name":"Edge","slug":"edge","description":"Edge sites","is_default":false,"is_current":false,"created_at":"2026-10-25T19:11:28Z"}`)
	created, err := testClient.CreateProject(CreateProjectRequest{Name: "Edge", Slug: "edge", Description: "Edge sites"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if created.ID != testProjectID {
		t.Fatalf("created = %+v", created)
	}
	if request := platform.last(t); request.body != `{"name":"Edge","slug":"edge","description":"Edge sites"}` {
		t.Fatalf("wire body = %s", request.body)
	}
}

func TestUpdateProjectSendsOnlyTheGivenFields(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK,
		`{"id":"`+testProjectID+`","name":"Edge sites","slug":"edge","description":"","is_default":false,"is_current":false,"created_at":""}`)
	name := "Edge sites"
	if _, err := testClient.UpdateProject(testProjectID, UpdateProjectRequest{Name: &name}); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	request := platform.last(t)
	if request.method != http.MethodPatch || request.path != "/api/v1/org/projects/"+testProjectID {
		t.Fatalf("request = %s %s", request.method, request.path)
	}
	if request.body != `{"name":"Edge sites"}` {
		t.Fatalf("wire body = %s", request.body)
	}
}

func TestSwitchProjectPostsTheProjectID(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `{"success":true,"message":"Switched","project_id":"`+testProjectID+`"}`)
	response, err := testClient.SwitchProject(testProjectID)
	if err != nil {
		t.Fatalf("SwitchProject: %v", err)
	}
	if !response.Success || response.ProjectID != testProjectID {
		t.Fatalf("response = %+v", response)
	}
	request := platform.last(t)
	if request.path != "/api/v1/org/projects/switch" || request.body != `{"project_id":"`+testProjectID+`"}` {
		t.Fatalf("request = %s %s", request.path, request.body)
	}
}

func TestListProjectMembersReadsTheMembersDocument(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK,
		`{"members":[{"ankra_user_id":"0a000000-0000-4000-8000-000000000002","email":"dev@example.com","via":"project","role_slug":"member"}]}`)
	members, err := testClient.ListProjectMembers(testProjectID)
	if err != nil {
		t.Fatalf("ListProjectMembers: %v", err)
	}
	if len(members) != 1 || members[0].Via != "project" || members[0].RoleSlug != "member" {
		t.Fatalf("members = %+v", members)
	}
	if request := platform.last(t); request.path != "/api/v1/org/projects/"+testProjectID+"/members" {
		t.Fatalf("path = %s", request.path)
	}
}

func TestDeleteProjectReadsTheDeleteDocument(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `{"success":true,"retired_assignments":2,"repointed_members":1,"pinned_tokens":0}`)
	response, err := testClient.DeleteProject(testProjectID)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if response.RetiredAssignments != 2 || response.RepointedMembers != 1 {
		t.Fatalf("response = %+v", response)
	}
	if request := platform.last(t); request.method != http.MethodDelete {
		t.Fatalf("method = %s", request.method)
	}
}

func TestMoveResourceToProjectSendsTheTriStateDestination(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		request ProjectMoveRequest
		want    string
	}{
		{name: "into a project", request: ProjectMoveRequest{ResourceKind: "cluster", ResourceID: testClusterID, ProjectID: testProjectID},
			want: `{"project_id":"` + testProjectID + `","resource_id":"` + testClusterID + `","resource_kind":"cluster"}`},
		{name: "organisation-wide", request: ProjectMoveRequest{ResourceKind: "credential", ResourceID: testClusterID, OrganisationWide: true},
			want: `{"project_id":null,"resource_id":"` + testClusterID + `","resource_kind":"credential"}`},
		{name: "current project", request: ProjectMoveRequest{ResourceKind: "application", ResourceID: testClusterID},
			want: `{"resource_id":"` + testClusterID + `","resource_kind":"application"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testClient, platform := newRecordingClient(t, http.StatusOK,
				`{"resource_kind":"`+testCase.request.ResourceKind+`","resource_id":"`+testClusterID+`","resource_name":"prod-eu","from_project_id":"`+testOtherProjectID+`","to_project_id":null,"followed":{"dns_zones":2},"followed_notification_routes":1}`)
			moved, err := testClient.MoveResourceToProject(testCase.request)
			if err != nil {
				t.Fatalf("MoveResourceToProject: %v", err)
			}
			if moved.ResourceName != "prod-eu" || moved.Followed["dns_zones"] != 2 || moved.FollowedNotificationRoutes != 1 || moved.ToProjectID != nil {
				t.Fatalf("moved = %+v", moved)
			}
			request := platform.last(t)
			if request.method != http.MethodPost || request.path != "/api/v1/org/projects/move" {
				t.Fatalf("request = %s %s", request.method, request.path)
			}
			if request.body != testCase.want {
				t.Fatalf("wire body = %s, want %s", request.body, testCase.want)
			}
		})
	}
}

func TestMoveResourceToProjectCarriesTheRefusalCodeAndConflicts(t *testing.T) {
	testClient, _ := newRecordingClient(t, http.StatusConflict,
		`{"detail":"Operations are still running on the cluster. Wait for them to finish, then move it.","code":"operations_in_flight","conflicts":["upgrade","scale"]}`)
	_, err := testClient.MoveResourceToProject(ProjectMoveRequest{ResourceKind: "cluster", ResourceID: testClusterID, ProjectID: testProjectID})
	var refused *ProjectMoveRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want a ProjectMoveRefusedError", err)
	}
	if refused.Code != ProjectMoveRefusalOperationsInFlight || len(refused.Conflicts) != 2 {
		t.Fatalf("refusal = %+v", refused)
	}
	if refused.Error() != "Operations are still running on the cluster. Wait for them to finish, then move it. (upgrade, scale)" {
		t.Fatalf("message = %q", refused.Error())
	}
}

func TestMoveResourceToProjectClassifiesProjectsOff(t *testing.T) {
	testClient, _ := newRecordingClient(t, http.StatusNotFound, `{"detail":"Projects are not enabled for this organisation"}`)
	_, err := testClient.MoveResourceToProject(ProjectMoveRequest{ResourceKind: "cluster", ResourceID: testClusterID, ProjectID: testProjectID})
	if !errors.Is(err, ErrProjectsDisabled) {
		t.Fatalf("error = %v, want ErrProjectsDisabled", err)
	}
}

func TestProjectOverrideIsSentAsTheProjectHeaderOnEveryRequest(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `[]`)
	testClient.SetOrganisationOverride("3b7dccca-0788-4470-9910-19478ae345ae")
	testClient.SetProjectOverride(testProjectID)
	if got := testClient.ProjectOverride(); got != testProjectID {
		t.Fatalf("ProjectOverride = %q", got)
	}
	if _, err := testClient.ListOrganisations(); err != nil {
		t.Fatalf("request: %v", err)
	}
	request := platform.last(t)
	if got := request.headers.Get("X-Ankra-Project-Id"); got != testProjectID {
		t.Fatalf("project header = %q, want %q", got, testProjectID)
	}
	if got := request.headers.Get("X-Ankra-Organisation-Id"); got != "3b7dccca-0788-4470-9910-19478ae345ae" {
		t.Fatalf("organisation header = %q: the project header must not displace it", got)
	}

	testClient.SetProjectOverride("")
	if _, err := testClient.ListOrganisations(); err != nil {
		t.Fatalf("request: %v", err)
	}
	if got := platform.last(t).headers.Get("X-Ankra-Project-Id"); got != "" {
		t.Fatalf("project header after clearing = %q, want none", got)
	}
}

func TestProjectHeaderRidesTheSlowWriteLaneToo(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `{}`)
	testClient.SetProjectOverride(testProjectID)
	request, err := http.NewRequest(http.MethodGet, testClient.BaseURL+"/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := testClient.httpClientForSlowWrite().Do(request)
	if err != nil {
		t.Fatalf("slow-write request: %v", err)
	}
	_ = response.Body.Close()
	if got := platform.last(t).headers.Get("X-Ankra-Project-Id"); got != testProjectID {
		t.Fatalf("slow-write project header = %q", got)
	}
}

func TestCostProjectScopeIsSentOnTheFleetCostReads(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `{}`)
	testClient.SetCostProjectScope(CostProjectScopeAll)
	reads := []struct {
		name string
		read func() error
		path string
		want string
	}{
		{name: "summary", read: func() error { _, err := testClient.GetFleetCloudCost(); return err }, path: "/api/v1/org/cloud-cost/summary", want: "project_id=all"},
		{name: "savings", read: func() error { _, err := testClient.GetCloudSavings(); return err }, path: "/api/v1/org/cloud-cost/savings", want: "project_id=all"},
		{name: "ledger", read: func() error { _, err := testClient.GetCloudLedger(); return err }, path: "/api/v1/org/cloud-cost/ledger", want: "project_id=all"},
		{name: "trend with days", read: func() error { _, err := testClient.GetFleetCostTrend(7); return err }, path: "/api/v1/org/cloud-cost/trend", want: "days=7&project_id=all"},
		{name: "events", read: func() error { _, err := testClient.GetCostEvents(0); return err }, path: "/api/v1/org/cloud-cost/events", want: "project_id=all"},
		{name: "reconciliation with month", read: func() error { _, err := testClient.GetCostReconciliation("2026-09"); return err }, path: "/api/v1/org/cloud-cost/reconciliation", want: "month=2026-09&project_id=all"},
		{name: "budgets", read: func() error { _, err := testClient.ListCostBudgets(); return err }, path: "/api/v1/org/cloud-cost/budgets", want: "project_id=all"},
	}
	for _, read := range reads {
		t.Run(read.name, func(t *testing.T) {
			if err := read.read(); err != nil {
				t.Fatalf("%s: %v", read.name, err)
			}
			request := platform.last(t)
			if request.path != read.path || request.query != read.want {
				t.Fatalf("request = %s?%s, want %s?%s", request.path, request.query, read.path, read.want)
			}
		})
	}

	testClient.SetCostProjectScope("")
	if _, err := testClient.GetFleetCloudCost(); err != nil {
		t.Fatal(err)
	}
	if query := platform.last(t).query; query != "" {
		t.Fatalf("query without a scope = %q, want none", query)
	}
}

func TestFleetCloudCostDecodesTheProjectRollup(t *testing.T) {
	testClient, _ := newRecordingClient(t, http.StatusOK,
		`{"currency":"eur","cluster_count":2,"monthly_cost_estimate_cents":100,"month_to_date_cents":50,"projected_month_end_cents":120,
		  "by_provider":[],"top_clusters":[],
		  "by_project":[{"project_id":"`+testProjectID+`","project_name":"Edge","cluster_count":2,"monthly_cost_estimate_cents":100,"month_to_date_cents":50,"projected_month_end_cents":120}],
		  "scope":{"project_id":"`+testProjectID+`","project_name":"Edge","all_projects":false,"partial":false}}`)
	summary, err := testClient.GetFleetCloudCost()
	if err != nil {
		t.Fatalf("GetFleetCloudCost: %v", err)
	}
	if len(summary.ByProject) != 1 || *summary.ByProject[0].ProjectName != "Edge" {
		t.Fatalf("by_project = %+v", summary.ByProject)
	}
	if summary.Scope == nil || *summary.Scope.ProjectName != "Edge" || summary.Scope.AllProjects {
		t.Fatalf("scope = %+v", summary.Scope)
	}
}

func TestGetObjectCostKnowsTheProjectKind(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `{"kind":"project","object":{"id":"`+testProjectID+`","name":"Edge"},"currency":"eur","priced":true,"open_waste":{},"trend_30d":[]}`)
	projection, err := testClient.GetObjectCost(ObjectCostKindProject, testProjectID)
	if err != nil {
		t.Fatalf("GetObjectCost: %v", err)
	}
	if projection.Kind != "project" {
		t.Fatalf("projection = %+v", projection)
	}
	if request := platform.last(t); request.path != "/api/v1/org/cloud-cost/objects/project/"+testProjectID {
		t.Fatalf("path = %s", request.path)
	}
}

func TestGetProjectShowbackReadsTheWindow(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK,
		`{"window_hours":24,"total_worker_vcpu_hours":150.0,"unattributed_worker_vcpu_hours":0.0,"unattributed_cluster_count":0,
		  "projects":[{"project_id":"`+testOtherProjectID+`","project_name":"Default","cluster_count":2,"current_worker_vcpu":6,"worker_vcpu_hours":150.0,"share_pct":100.0}]}`)
	showback, err := testClient.GetProjectShowback(24)
	if err != nil {
		t.Fatalf("GetProjectShowback: %v", err)
	}
	if showback.WindowHours != 24 || len(showback.Projects) != 1 || showback.Projects[0].SharePct != 100 {
		t.Fatalf("showback = %+v", showback)
	}
	request := platform.last(t)
	if request.path != "/api/v1/org/billing/analytics/by-project" || request.query != "hours=24" {
		t.Fatalf("request = %s?%s", request.path, request.query)
	}
	if _, err := testClient.GetProjectShowback(0); err != nil {
		t.Fatal(err)
	}
	if query := platform.last(t).query; query != "" {
		t.Fatalf("query for the default window = %q, want none", query)
	}
}

func TestTokenRequestsCarryTheProjectPinOnlyWhenGiven(t *testing.T) {
	testClient, platform := newRecordingClient(t, http.StatusOK, `{"id":"t","token":"secret","expires_at":"2027-01-01T00:00:00Z","type":"Bearer"}`)
	pin := testProjectID
	if _, err := testClient.CreateAPIToken(CreateAPITokenRequest{Name: "laptop", ProjectID: &pin}); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	var personal map[string]any
	if err := json.Unmarshal([]byte(platform.last(t).body), &personal); err != nil {
		t.Fatal(err)
	}
	if personal["project_id"] != testProjectID {
		t.Fatalf("personal token body = %v", personal)
	}
	if _, err := testClient.CreateAPIToken(CreateAPITokenRequest{Name: "laptop"}); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if _, present := decodeBody(t, platform.last(t).body)["project_id"]; present {
		t.Fatal("a token without --project must not send project_id: the platform decides the pin")
	}
	if _, err := testClient.CreateServiceToken(CreateServiceTokenRequest{Name: "ci", RoleSlug: "member", ProjectID: &pin}); err != nil {
		t.Fatalf("CreateServiceToken: %v", err)
	}
	if got := decodeBody(t, platform.last(t).body)["project_id"]; got != testProjectID {
		t.Fatalf("service token body project_id = %v", got)
	}
}

func decodeBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return decoded
}

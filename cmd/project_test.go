package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ankra/internal/client"
	"ankra/internal/kubeconfig"

	"github.com/spf13/cobra"
)

const (
	projectTestOrganisationID = "3b7dccca-0788-4470-9910-19478ae345ae"
	projectTestDefaultID      = "3d2720de-4956-439b-b672-c0d1cf83b98a"
	projectTestEdgeID         = "071d45b1-f8fe-4f47-b13a-4f13a01689f1"
	projectTestClusterID      = "1834920e-3001-4157-8938-33c447031033"
)

// projectsMock answers the project routes and records what the commands
// asked of the client: the scope overrides, the writes and the reads.
type projectsMock struct {
	baseMock
	projects       []client.ProjectSummary
	listError      error
	listCalls      int
	current        *client.ProjectSummary
	organisation   string
	project        string
	costScope      string
	created        *client.CreateProjectRequest
	updated        map[string]client.UpdateProjectRequest
	deleted        []string
	switched       []string
	members        []client.ProjectMember
	moves          []client.ProjectMoveRequest
	moveError      error
	showbackHours  []int
	fleet          *client.FleetCloudCost
	objectKinds    []string
	objectSegments [][]string
	budgetWrites   []client.CostBudgetWrite
	personalTokens []client.CreateAPITokenRequest
	serviceTokens  []client.CreateServiceTokenRequest
	clusters       []client.ClusterListItem
}

func (m *projectsMock) SetOrganisationOverride(organisationID string) {
	m.organisation = organisationID
}
func (m *projectsMock) OrganisationOverride() string        { return m.organisation }
func (m *projectsMock) SetProjectOverride(projectID string) { m.project = projectID }
func (m *projectsMock) ProjectOverride() string             { return m.project }
func (m *projectsMock) SetCostProjectScope(scope string)    { m.costScope = scope }

func (m *projectsMock) ListProjects() ([]client.ProjectSummary, error) {
	m.listCalls++
	if m.listError != nil {
		return nil, m.listError
	}
	return m.projects, nil
}

func (m *projectsMock) GetCurrentProject() (*client.ProjectSummary, error) {
	if m.current == nil {
		return nil, errors.New("no current project")
	}
	return m.current, nil
}

func (m *projectsMock) CreateProject(request client.CreateProjectRequest) (*client.ProjectSummary, error) {
	if m.listError != nil {
		return nil, m.listError
	}
	m.created = &request
	return &client.ProjectSummary{ID: projectTestEdgeID, Name: request.Name, Slug: request.Slug, Description: request.Description, CreatedAt: "2026-10-25T19:11:28Z"}, nil
}

func (m *projectsMock) UpdateProject(projectID string, request client.UpdateProjectRequest) (*client.ProjectSummary, error) {
	if m.updated == nil {
		m.updated = map[string]client.UpdateProjectRequest{}
	}
	m.updated[projectID] = request
	updated := client.ProjectSummary{ID: projectID, Slug: "edge", Name: "Edge"}
	if request.Name != nil {
		updated.Name = *request.Name
	}
	if request.Description != nil {
		updated.Description = *request.Description
	}
	return &updated, nil
}

func (m *projectsMock) DeleteProject(projectID string) (*client.DeleteProjectResponse, error) {
	m.deleted = append(m.deleted, projectID)
	return &client.DeleteProjectResponse{Success: true, RetiredAssignments: 1, RepointedMembers: 2, PinnedTokens: 0}, nil
}

func (m *projectsMock) SwitchProject(projectID string) (*client.SwitchProjectResponse, error) {
	m.switched = append(m.switched, projectID)
	return &client.SwitchProjectResponse{Success: true, Message: "Switched", ProjectID: projectID}, nil
}

func (m *projectsMock) ListProjectMembers(string) ([]client.ProjectMember, error) {
	return m.members, nil
}

func (m *projectsMock) MoveResourceToProject(request client.ProjectMoveRequest) (*client.ProjectMoveResponse, error) {
	m.moves = append(m.moves, request)
	if m.moveError != nil {
		return nil, m.moveError
	}
	from := projectTestDefaultID
	response := &client.ProjectMoveResponse{
		ResourceKind: request.ResourceKind, ResourceID: request.ResourceID, ResourceName: "prod-eu",
		FromProjectID: &from, Followed: map[string]int{"dns_zones": 2}, FollowedNotificationRoutes: 1,
	}
	if !request.OrganisationWide {
		to := request.ProjectID
		response.ToProjectID = &to
	}
	return response, nil
}

func (m *projectsMock) GetProjectShowback(hours int) (*client.ProjectShowback, error) {
	m.showbackHours = append(m.showbackHours, hours)
	return &client.ProjectShowback{
		WindowHours: hours, TotalWorkerVcpuHours: 150, UnattributedWorkerVcpuHours: 6, UnattributedClusterCount: 1,
		Projects: []client.ProjectShowbackRow{{ProjectID: projectTestDefaultID, ProjectName: "Default", ClusterCount: 2, CurrentWorkerVcpu: 6, WorkerVcpuHours: 144, SharePct: 96}},
	}, nil
}

func (m *projectsMock) GetFleetCloudCost() (*client.FleetCloudCost, error) { return m.fleet, nil }

func (m *projectsMock) GetObjectCost(kind string, pathSegments ...string) (*client.ObjectCostProjection, error) {
	m.objectKinds = append(m.objectKinds, kind)
	m.objectSegments = append(m.objectSegments, pathSegments)
	return &client.ObjectCostProjection{Kind: kind, Object: client.ObjectCostSubject{ID: pathSegments[0], Name: "Edge"}, Currency: "eur", Trend30d: []client.ObjectCostDay{}}, nil
}

func (m *projectsMock) CreateCostBudget(write client.CostBudgetWrite) (*client.CostBudget, error) {
	m.budgetWrites = append(m.budgetWrites, write)
	return &client.CostBudget{ID: "budget-1", Name: "Edge", ScopeKind: "project", ScopeName: "Edge", Currency: "eur"}, nil
}

func (m *projectsMock) CreateAPIToken(request client.CreateAPITokenRequest) (*client.CreateAPITokenResponse, error) {
	m.personalTokens = append(m.personalTokens, request)
	return &client.CreateAPITokenResponse{ID: "personal-id", Token: "ankra_personal", ExpiresAt: "2027-01-01T00:00:00Z", Type: "Bearer"}, nil
}

func (m *projectsMock) CreateServiceToken(request client.CreateServiceTokenRequest) (*client.CreateServiceTokenResponse, error) {
	m.serviceTokens = append(m.serviceTokens, request)
	return &client.CreateServiceTokenResponse{ID: "svc", Token: "ankra_service", AccountName: request.Name, RoleSlug: request.RoleSlug, Label: "ci", ExpiresAt: "2027-01-01T00:00:00Z", Type: "Bearer"}, nil
}

func (m *projectsMock) ListClusters(int, int) (*client.ClusterListResponse, error) {
	return &client.ClusterListResponse{Result: m.clusters, Pagination: client.Pagination{TotalPages: 1, Page: 1}}, nil
}

func (m *projectsMock) GetCluster(name string) (client.ClusterListItem, error) {
	for _, cluster := range m.clusters {
		if cluster.Name == name || cluster.ID == name {
			return cluster, nil
		}
	}
	return client.ClusterListItem{}, client.ErrClusterNotFound
}

func twoProjects() []client.ProjectSummary {
	return []client.ProjectSummary{
		{ID: projectTestDefaultID, Name: "Default", Slug: "default", IsDefault: true, IsCurrent: true, CreatedAt: "2026-10-14T08:16:05Z"},
		{ID: projectTestEdgeID, Name: "Edge", Slug: "edge", Description: "Edge sites", CreatedAt: "2026-10-25T19:11:28Z"},
	}
}

func projectCommandTree() []*cobra.Command {
	return []*cobra.Command{projectListCmd, projectCurrentCmd, projectSwitchCmd, projectCreateCmd, projectGetCmd,
		projectRenameCmd, projectDeleteCmd, projectMembersCmd, projectMoveCmd}
}

// resetGlobalProjectFlag puts the root's --project back to unset, so one
// test's override never leaks into the next through the shared command tree.
func resetGlobalProjectFlag(t *testing.T) {
	t.Helper()
	flag := rootCmd.PersistentFlags().Lookup("project")
	if flag == nil {
		t.Fatal("the root command carries no --project flag")
	}
	_ = flag.Value.Set("")
	flag.Changed = false
}

// runProjectCommand runs the root command against mock in an isolated HOME,
// with no project override from the environment, and returns stdout and
// stderr. Every flag of the project tree and the global --project are reset
// before and after.
func runProjectCommand(t *testing.T, mock APIClient, input string, args ...string) (string, string, error) {
	t.Helper()
	withTempHome(t)
	t.Setenv(envAnkraProject, "")
	t.Setenv(envAnkraOrg, "")
	setMockClient(t, mock)
	reset := func() {
		resetGlobalProjectFlag(t)
		resetTreeFlags(t, projectCommandTree()...)
		resetTreeFlags(t, costSummaryCmd, costObjectProjectCmd, costBudgetsSetCmd, tokensCreateCmd)
	}
	reset()
	t.Cleanup(reset)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetIn(strings.NewReader(input))
	rootCmd.SetArgs(args)
	executeError := rootCmd.Execute()
	return stdout.String(), stderr.String(), executeError
}

func TestProjectListRendersTheProjectsAndMarksTheCurrentOne(t *testing.T) {
	mock := &projectsMock{projects: twoProjects()}
	stdout, _, err := runProjectCommand(t, mock, "", "project", "list")
	if err != nil {
		t.Fatalf("project list: %v", err)
	}
	plain := stripANSICodes(stdout)
	for _, fragment := range []string{projectTestDefaultID, "Default", "default", "yes", "✓", "Edge", "edge", "Edge sites"} {
		if !strings.Contains(plain, fragment) {
			t.Errorf("list missing %q:\n%s", fragment, plain)
		}
	}
	stdout, _, err = runProjectCommand(t, mock, "", "project", "list", "-o", "json")
	if err != nil {
		t.Fatalf("project list -o json: %v", err)
	}
	if !strings.Contains(stdout, `"slug": "edge"`) || !strings.Contains(stdout, `"is_current": true`) {
		t.Fatalf("json = %s", stdout)
	}
}

func TestProjectCommandsExit3WhileProjectsAreOff(t *testing.T) {
	for _, arguments := range [][]string{
		{"project", "list"},
		{"project", "current"},
		{"project", "switch", "edge"},
		{"project", "get", "edge"},
		{"project", "create", "Edge"},
		{"project", "move", "--kind", "cluster", "--id", projectTestClusterID, "--to", "edge"},
	} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			mock := &projectsMock{listError: client.ErrProjectsDisabled}
			_, _, err := runProjectCommand(t, mock, "", arguments...)
			if err == nil {
				t.Fatal("expected the projects-off refusal")
			}
			if exitCodeFor(err) != exitNotFound {
				t.Fatalf("exit code = %d, want %d: %v", exitCodeFor(err), exitNotFound, err)
			}
			if !strings.Contains(err.Error(), "projects are not enabled for this organisation") {
				t.Fatalf("error = %v, want the platform's own sentence", err)
			}
		})
	}
}

func TestProjectCommandsExit1WhenThePlatformPredatesProjects(t *testing.T) {
	mock := &projectsMock{listError: client.ErrProjectsRouteMissing}
	_, _, err := runProjectCommand(t, mock, "", "project", "list")
	if err == nil || exitCodeFor(err) != exitError || !strings.Contains(err.Error(), "/api/v1/org/projects is not registered") {
		t.Fatalf("error = %v (exit %d)", err, exitCodeFor(err))
	}
}

func TestProjectSwitchSavesTheSelectionWithItsOrganisation(t *testing.T) {
	mock := &projectsMock{projects: twoProjects(), organisation: projectTestOrganisationID}
	stdout, _, err := runProjectCommand(t, mock, "", "project", "switch", "Edge")
	if err != nil {
		t.Fatalf("project switch: %v", err)
	}
	if len(mock.switched) != 1 || mock.switched[0] != projectTestEdgeID {
		t.Fatalf("switched = %v", mock.switched)
	}
	if !strings.Contains(stdout, "Switched to project: Edge (edge)") {
		t.Fatalf("stdout = %s", stdout)
	}
	selected, loadError := loadSelectedProject()
	if loadError != nil {
		t.Fatalf("loading the selection: %v", loadError)
	}
	if selected.ProjectID != projectTestEdgeID || selected.Slug != "edge" || selected.OrganisationID != projectTestOrganisationID {
		t.Fatalf("selection = %+v", selected)
	}

	_, _, err = runProjectCommand(t, mock, "", "project", "switch", "nowhere")
	if err == nil || exitCodeFor(err) != exitNotFound || !strings.Contains(err.Error(), "Available projects: Default (default), Edge (edge)") {
		t.Fatalf("unknown project: %v (exit %d)", err, exitCodeFor(err))
	}

	twins := twoProjects()
	twins[0].Name, twins[1].Name = "Shared", "Shared"
	_, _, err = runProjectCommand(t, &projectsMock{projects: twins}, "", "project", "switch", "Shared")
	if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "multiple projects are named") {
		t.Fatalf("ambiguous name: %v (exit %d)", err, exitCodeFor(err))
	}
	if _, _, err := runProjectCommand(t, &projectsMock{projects: twins}, "", "project", "switch", "edge"); err != nil {
		t.Fatalf("the slug must still settle an ambiguous name: %v", err)
	}
}

func TestProjectCurrentReportsWhereTheProjectCameFrom(t *testing.T) {
	t.Run("server", func(t *testing.T) {
		stdout, _, err := runProjectCommand(t, &projectsMock{projects: twoProjects()}, "", "project", "current")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "Current project (server):") || !strings.Contains(stdout, "Slug:        default") {
			t.Fatalf("stdout = %s", stdout)
		}
	})
	t.Run("override flag", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects()}
		stdout, _, err := runProjectCommand(t, mock, "", "--project", "edge", "project", "current", "-o", "json")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, `"source": "override"`) || !strings.Contains(stdout, `"slug": "edge"`) {
			t.Fatalf("json = %s", stdout)
		}
		if mock.project != projectTestEdgeID {
			t.Fatalf("the override must be sent as the project header: got %q", mock.project)
		}
	})
	t.Run("environment", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects()}
		withTempHome(t)
		t.Setenv(envAnkraProject, "Edge")
		setMockClient(t, mock)
		t.Cleanup(func() { resetGlobalProjectFlag(t); resetTreeFlags(t, projectCommandTree()...) })
		stdout := new(bytes.Buffer)
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(new(bytes.Buffer))
		rootCmd.SetArgs([]string{"project", "current"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "Current project (override):") || mock.project != projectTestEdgeID {
			t.Fatalf("stdout = %s, header %q", stdout.String(), mock.project)
		}
	})
	t.Run("local selection of this organisation", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects(), organisation: projectTestOrganisationID}
		withTempHome(t)
		t.Setenv(envAnkraProject, "")
		if err := saveSelectedProject(SelectedProject{ProjectID: projectTestEdgeID, Slug: "edge", OrganisationID: projectTestOrganisationID}); err != nil {
			t.Fatal(err)
		}
		setMockClient(t, mock)
		t.Cleanup(func() { resetGlobalProjectFlag(t); resetTreeFlags(t, projectCommandTree()...) })
		stdout := new(bytes.Buffer)
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(new(bytes.Buffer))
		rootCmd.SetArgs([]string{"project", "current"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "Current project (local):") || !strings.Contains(stdout.String(), "Slug:        edge") {
			t.Fatalf("stdout = %s", stdout.String())
		}
	})
	t.Run("local selection of another organisation is ignored", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects(), organisation: projectTestOrganisationID}
		withTempHome(t)
		t.Setenv(envAnkraProject, "")
		if err := saveSelectedProject(SelectedProject{ProjectID: projectTestEdgeID, Slug: "edge", OrganisationID: "7b2fedb6-4d79-45fd-b71a-044a2cfddce7"}); err != nil {
			t.Fatal(err)
		}
		setMockClient(t, mock)
		t.Cleanup(func() { resetGlobalProjectFlag(t); resetTreeFlags(t, projectCommandTree()...) })
		stdout := new(bytes.Buffer)
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(new(bytes.Buffer))
		rootCmd.SetArgs([]string{"project", "current"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "Current project (server):") {
			t.Fatalf("stdout = %s", stdout.String())
		}
	})
}

func TestGlobalProjectFlagScopesTheWholeInvocation(t *testing.T) {
	t.Run("a slug is resolved once and sent as the id", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects(), fleet: &client.FleetCloudCost{Currency: "eur"}}
		if _, _, err := runProjectCommand(t, mock, "", "cost", "summary", "--project", "edge"); err != nil {
			t.Fatal(err)
		}
		if mock.project != projectTestEdgeID || mock.listCalls != 1 {
			t.Fatalf("project header %q after %d listings", mock.project, mock.listCalls)
		}
	})
	t.Run("an id is sent without a listing", func(t *testing.T) {
		mock := &projectsMock{listError: errors.New("the listing must not be asked for an id"), fleet: &client.FleetCloudCost{Currency: "eur"}}
		if _, _, err := runProjectCommand(t, mock, "", "--project", strings.ToUpper(projectTestEdgeID), "cost", "summary"); err != nil {
			t.Fatal(err)
		}
		if mock.project != projectTestEdgeID {
			t.Fatalf("project header = %q", mock.project)
		}
	})
	t.Run("an unknown slug is a missing target", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects(), fleet: &client.FleetCloudCost{Currency: "eur"}}
		_, _, err := runProjectCommand(t, mock, "", "--project", "nowhere", "cost", "summary")
		if err == nil || exitCodeFor(err) != exitNotFound {
			t.Fatalf("error = %v (exit %d)", err, exitCodeFor(err))
		}
		if mock.project != "" {
			t.Fatalf("no header may be sent for a project that does not exist, got %q", mock.project)
		}
	})
	t.Run("the flag wins over the environment", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects(), fleet: &client.FleetCloudCost{Currency: "eur"}}
		withTempHome(t)
		t.Setenv(envAnkraProject, "default")
		setMockClient(t, mock)
		t.Cleanup(func() { resetGlobalProjectFlag(t); resetTreeFlags(t, costSummaryCmd) })
		rootCmd.SetOut(new(bytes.Buffer))
		rootCmd.SetErr(new(bytes.Buffer))
		rootCmd.SetArgs([]string{"cost", "summary", "--project", "edge"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if mock.project != projectTestEdgeID {
			t.Fatalf("project header = %q, want the flag's project", mock.project)
		}
	})
}

// projectFlagCommand builds a root carrying the global --project flag,
// optionally set, and returns its child command.
func projectFlagCommand(t *testing.T, projectFlag string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "ankra"}
	root.PersistentFlags().String("project", "", "")
	if projectFlag != "" {
		if err := root.PersistentFlags().Set("project", projectFlag); err != nil {
			t.Fatal(err)
		}
	}
	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)
	return child
}

func TestSavedProjectSelectionAppliesOnlyWithTheSavedLoginInItsOrganisation(t *testing.T) {
	withTempHome(t)
	t.Setenv(envAnkraProject, "")
	if err := saveSelectedProject(SelectedProject{ProjectID: projectTestEdgeID, OrganisationID: projectTestOrganisationID}); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name         string
		source       credentialSource
		organisation string
		command      *cobra.Command
		want         string
	}{
		{name: "saved login in the same organisation", source: sourceConfigFile, organisation: projectTestOrganisationID, command: projectFlagCommand(t, ""), want: projectTestEdgeID},
		{name: "saved login in another organisation", source: sourceConfigFile, organisation: "7b2fedb6-4d79-45fd-b71a-044a2cfddce7", command: projectFlagCommand(t, ""), want: ""},
		{name: "environment token", source: sourceEnv, organisation: projectTestOrganisationID, command: projectFlagCommand(t, ""), want: ""},
		{name: "flag token", source: sourceFlag, organisation: projectTestOrganisationID, command: projectFlagCommand(t, ""), want: ""},
		{name: "explicit flag beats the selection", source: sourceConfigFile, organisation: projectTestOrganisationID, command: projectFlagCommand(t, projectTestDefaultID), want: projectTestDefaultID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mock := &projectsMock{projects: twoProjects(), organisation: testCase.organisation}
			setMockClient(t, mock)
			if err := applyProjectOverride(testCase.command, testCase.source); err != nil {
				t.Fatalf("applyProjectOverride: %v", err)
			}
			if mock.project != testCase.want {
				t.Fatalf("project header = %q, want %q", mock.project, testCase.want)
			}
		})
	}
	t.Run("a command that ignores the selection", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects(), organisation: projectTestOrganisationID}
		setMockClient(t, mock)
		command := projectFlagCommand(t, "")
		setIgnoresLocalProject(command)
		if err := applyProjectOverride(command, sourceConfigFile); err != nil {
			t.Fatal(err)
		}
		if mock.project != "" {
			t.Fatalf("project header = %q, want none", mock.project)
		}
	})
}

type organisationSwitchMock struct {
	projectsMock
}

func (m *organisationSwitchMock) ListOrganisations() ([]client.OrganisationSummary, error) {
	name, slug := "Ankra", "ankra"
	return []client.OrganisationSummary{{OrganisationID: projectTestOrganisationID, Name: &name, Slug: &slug}}, nil
}

func (m *organisationSwitchMock) SwitchOrganisation(string) (*client.SwitchOrganisationResponse, error) {
	return &client.SwitchOrganisationResponse{Success: true}, nil
}

func TestOrganisationSwitchForgetsTheProjectSelection(t *testing.T) {
	mock := &organisationSwitchMock{}
	withTempHome(t)
	t.Setenv(envAnkraProject, "")
	t.Setenv(envAnkraOrg, "")
	if err := saveSelectedProject(SelectedProject{ProjectID: projectTestEdgeID, OrganisationID: "7b2fedb6-4d79-45fd-b71a-044a2cfddce7"}); err != nil {
		t.Fatal(err)
	}
	setMockClient(t, mock)
	t.Cleanup(func() { resetGlobalProjectFlag(t) })
	_ = captureStdout(t, func() {
		rootCmd.SetOut(new(bytes.Buffer))
		rootCmd.SetErr(new(bytes.Buffer))
		rootCmd.SetArgs([]string{"org", "switch", "ankra"})
		if err := rootCmd.Execute(); err != nil {
			t.Errorf("org switch: %v", err)
		}
	})
	path, err := selectedProjectFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, statError := os.Stat(path); !errors.Is(statError, os.ErrNotExist) {
		t.Fatalf("the project selection must be forgotten on an organisation switch, stat = %v", statError)
	}
	if mock.project != "" {
		t.Fatalf("org switch must not carry the old organisation's project header, got %q", mock.project)
	}
}

func TestProjectCreateDerivesTheSlugFromTheName(t *testing.T) {
	mock := &projectsMock{}
	stdout, _, err := runProjectCommand(t, mock, "", "project", "create", "Platform Team (EU)", "--description", "Shared clusters")
	if err != nil {
		t.Fatalf("project create: %v", err)
	}
	if mock.created == nil || mock.created.Slug != "platform-team-eu" || mock.created.Name != "Platform Team (EU)" || mock.created.Description != "Shared clusters" {
		t.Fatalf("create request = %+v", mock.created)
	}
	if !strings.Contains(stdout, "ankra project switch platform-team-eu") {
		t.Fatalf("stdout = %s", stdout)
	}

	mock = &projectsMock{}
	if _, _, err := runProjectCommand(t, mock, "", "project", "create", "Edge", "--slug", "EDGE-Sites"); err != nil {
		t.Fatal(err)
	}
	if mock.created.Slug != "edge-sites" {
		t.Fatalf("an explicit slug is lower-cased, got %q", mock.created.Slug)
	}

	for _, slug := range []string{"-edge", "edge_sites", projectTestEdgeID, strings.Repeat("a", 64)} {
		_, _, err := runProjectCommand(t, &projectsMock{}, "", "project", "create", "Edge", "--slug", slug)
		if err == nil || exitCodeFor(err) != exitUsage {
			t.Errorf("slug %q: error = %v (exit %d), want a usage error", slug, err, exitCodeFor(err))
		}
	}
}

func TestProjectSlugFromName(t *testing.T) {
	for name, want := range map[string]string{
		"Platform Team":         "platform-team",
		"  Edge / Sites  ":      "edge-sites",
		"Ünïcode & Co.":         "n-code-co",
		strings.Repeat("a", 70): strings.Repeat("a", 63),
	} {
		if got := projectSlugFromName(name); got != want {
			t.Errorf("projectSlugFromName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestProjectGetAndRenameUseTheResolvedProject(t *testing.T) {
	mock := &projectsMock{projects: twoProjects()}
	stdout, _, err := runProjectCommand(t, mock, "", "project", "get", "edge", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"id": "`+projectTestEdgeID+`"`) {
		t.Fatalf("json = %s", stdout)
	}

	mock = &projectsMock{projects: twoProjects()}
	if _, _, err := runProjectCommand(t, mock, "", "project", "rename", "edge", "Edge sites"); err != nil {
		t.Fatal(err)
	}
	request := mock.updated[projectTestEdgeID]
	if request.Name == nil || *request.Name != "Edge sites" || request.Description != nil {
		t.Fatalf("rename without --description must send the name only: %+v", request)
	}

	mock = &projectsMock{projects: twoProjects()}
	if _, _, err := runProjectCommand(t, mock, "", "project", "rename", "edge", "Edge sites", "--description", ""); err != nil {
		t.Fatal(err)
	}
	request = mock.updated[projectTestEdgeID]
	if request.Description == nil || *request.Description != "" {
		t.Fatalf("an explicit empty --description clears it: %+v", request)
	}
}

func TestProjectDeleteConfirmsAndForgetsTheSelection(t *testing.T) {
	mock := &projectsMock{projects: twoProjects()}
	_, _, err := runProjectCommand(t, mock, "n\n", "project", "delete", "edge")
	if !errors.Is(err, errCancelled) || exitCodeFor(err) != exitCancelled {
		t.Fatalf("a declined prompt must exit %d, got %v", exitCancelled, err)
	}
	if len(mock.deleted) != 0 {
		t.Fatal("nothing may be deleted after a declined prompt")
	}

	mock = &projectsMock{projects: twoProjects()}
	withTempHome(t)
	if err := saveSelectedProject(SelectedProject{ProjectID: projectTestEdgeID}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envAnkraProject, "")
	setMockClient(t, mock)
	t.Cleanup(func() { resetGlobalProjectFlag(t); resetTreeFlags(t, projectCommandTree()...) })
	stdout := new(bytes.Buffer)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(new(bytes.Buffer))
	rootCmd.SetArgs([]string{"project", "delete", "edge", "--yes"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("project delete --yes: %v", err)
	}
	if len(mock.deleted) != 1 || mock.deleted[0] != projectTestEdgeID {
		t.Fatalf("deleted = %v", mock.deleted)
	}
	if !strings.Contains(stdout.String(), "1 project-scoped role assignment(s) retired, 2 member(s) pointed back") {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if _, loadError := loadSelectedProject(); !errors.Is(loadError, os.ErrNotExist) {
		t.Fatalf("the selection of a deleted project must be forgotten, load = %v", loadError)
	}
}

func TestProjectMembersRendersWhoReachesTheProject(t *testing.T) {
	mock := &projectsMock{projects: twoProjects(), members: []client.ProjectMember{
		{AnkraUserID: "0a000000-0000-4000-8000-000000000002", Email: "dev@example.com", Via: "project", RoleSlug: "member"},
		{AnkraUserID: "0a000000-0000-4000-8000-000000000003", Email: "ops@example.com", Via: "organisation", RoleSlug: "admin"},
	}}
	stdout, _, err := runProjectCommand(t, mock, "", "project", "members", "edge")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"dev@example.com", "project", "ops@example.com", "organisation", "admin"} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("members missing %q:\n%s", fragment, stdout)
		}
	}
}

func TestProjectMoveResolvesKindResourceAndDestination(t *testing.T) {
	t.Run("a cluster by name into a project by slug", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects(), clusters: []client.ClusterListItem{{ID: projectTestClusterID, Name: "prod-eu"}}}
		stdout, _, err := runProjectCommand(t, mock, "", "project", "move", "--kind", "cluster", "--id", "prod-eu", "--to", "edge")
		if err != nil {
			t.Fatalf("project move: %v", err)
		}
		if len(mock.moves) != 1 || mock.moves[0].ResourceID != projectTestClusterID || mock.moves[0].ProjectID != projectTestEdgeID || mock.moves[0].OrganisationWide {
			t.Fatalf("move = %+v", mock.moves)
		}
		if !strings.Contains(stdout, `Moved cluster "prod-eu"`) || !strings.Contains(stdout, "2 row(s) of dns_zones followed it") || !strings.Contains(stdout, "1 notification route(s)") {
			t.Fatalf("stdout = %s", stdout)
		}
	})
	t.Run("a shared kind to organisation-wide", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects()}
		stdout, _, err := runProjectCommand(t, mock, "", "project", "move", "--kind", "credential", "--id", projectTestClusterID, "--to", "org-wide")
		if err != nil {
			t.Fatal(err)
		}
		if len(mock.moves) != 1 || !mock.moves[0].OrganisationWide || mock.moves[0].ProjectID != "" {
			t.Fatalf("move = %+v", mock.moves)
		}
		if !strings.Contains(stdout, "to organisation-wide.") {
			t.Fatalf("stdout = %s", stdout)
		}
	})
	t.Run("a strict kind cannot be organisation-wide", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects()}
		_, _, err := runProjectCommand(t, mock, "", "project", "move", "--kind", "cluster", "--id", projectTestClusterID, "--to", "org-wide")
		if err == nil || exitCodeFor(err) != exitUsage || len(mock.moves) != 0 {
			t.Fatalf("error = %v (exit %d), moves %v", err, exitCodeFor(err), mock.moves)
		}
	})
	t.Run("unknown kind and missing flags are usage errors", func(t *testing.T) {
		for _, arguments := range [][]string{
			{"project", "move", "--kind", "bucket", "--id", projectTestClusterID, "--to", "edge"},
			{"project", "move", "--kind", "cluster", "--to", "edge"},
			{"project", "move", "--kind", "cluster", "--id", projectTestClusterID},
			{"project", "move", "--kind", "environment", "--id", "staging", "--to", "edge"},
		} {
			_, _, err := runProjectCommand(t, &projectsMock{projects: twoProjects()}, "", arguments...)
			if err == nil || exitCodeFor(err) != exitUsage {
				t.Errorf("%v: error = %v (exit %d)", arguments, err, exitCodeFor(err))
			}
		}
	})
	t.Run("the platform's refusal is named with its code", func(t *testing.T) {
		mock := &projectsMock{projects: twoProjects(), moveError: &client.ProjectMoveRefusedError{
			Code: client.ProjectMoveRefusalOperationsInFlight, Detail: "Operations are still running on the cluster. Wait for them to finish, then move it.", Conflicts: []string{"upgrade"}}}
		_, _, err := runProjectCommand(t, mock, "", "project", "move", "--kind", "cluster", "--id", projectTestClusterID, "--to", "edge")
		if err == nil || exitCodeFor(err) != exitError {
			t.Fatalf("error = %v (exit %d)", err, exitCodeFor(err))
		}
		for _, fragment := range []string{"operations_in_flight", "Operations are still running", "(upgrade)"} {
			if !strings.Contains(err.Error(), fragment) {
				t.Errorf("error %q missing %q", err.Error(), fragment)
			}
		}
	})
}

func TestPortalLinksCarryTheSelectedProject(t *testing.T) {
	mock := &projectsMock{project: projectTestEdgeID}
	setMockClient(t, mock)
	link := portalURL("org-1", nil, "clusters", "cluster", "imported", projectTestClusterID, "overview")
	if !strings.Contains(link, "org=org-1") || !strings.Contains(link, "project="+projectTestEdgeID) {
		t.Fatalf("link = %s", link)
	}
	mock.project = ""
	if link := portalURL("org-1", nil, "clusters"); strings.Contains(link, "project=") {
		t.Fatalf("a link without a selected project must carry no project marker: %s", link)
	}
}

type projectKubeconfigMock struct {
	kubeconfigMock
	project string
}

func (m projectKubeconfigMock) ProjectOverride() string { return m.project }

func TestKubeconfigAddPinsTheProjectItWasAddedIn(t *testing.T) {
	withKubeconfigMock(t, kubeconfigMock{cluster: client.ClusterListItem{ID: "id-1", Name: "demo", OrganisationID: "org-1"}})
	apiClient = projectKubeconfigMock{kubeconfigMock: kubeconfigMock{cluster: client.ClusterListItem{ID: "id-1", Name: "demo", OrganisationID: "org-1"}}, project: projectTestEdgeID}
	path := filepath.Join(t.TempDir(), "config")
	resetKubeconfigFlags(path)
	kubeconfigClusterFlag = "demo"
	var out bytes.Buffer
	if err := kubeconfigAdd(&out); err != nil {
		t.Fatal(err)
	}
	config, err := kubeconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "cluster kube-token --cluster id-1 --org org-1 --project " + projectTestEdgeID
	if got := strings.Join(execArgsForUser(t, config, "ankra-demo"), " "); got != want {
		t.Errorf("exec args = %q, want %q", got, want)
	}
}

func TestTokensCreatePinsTheProject(t *testing.T) {
	mock := &projectsMock{projects: twoProjects()}
	_ = captureStdout(t, func() {
		if _, _, err := runProjectCommand(t, mock, "", "tokens", "create", "laptop", "--project", "edge"); err != nil {
			t.Errorf("tokens create --project: %v", err)
		}
	})
	if len(mock.personalTokens) != 1 || mock.personalTokens[0].ProjectID == nil || *mock.personalTokens[0].ProjectID != projectTestEdgeID {
		t.Fatalf("personal token request = %+v", mock.personalTokens)
	}
	if mock.project != projectTestEdgeID {
		t.Fatalf("--project on tokens create is the global flag, so the request is scoped to the project too: header %q", mock.project)
	}
	if mock.listCalls != 1 {
		t.Fatalf("the slug is resolved once for the header and reused for the pin, got %d listings", mock.listCalls)
	}

	mock = &projectsMock{projects: twoProjects()}
	_ = captureStdout(t, func() {
		if _, _, err := runProjectCommand(t, mock, "", "tokens", "create", "ci", "--service", "--role", "member", "--project", projectTestEdgeID); err != nil {
			t.Errorf("tokens create --service --project: %v", err)
		}
	})
	if len(mock.serviceTokens) != 1 || mock.serviceTokens[0].ProjectID == nil || *mock.serviceTokens[0].ProjectID != projectTestEdgeID {
		t.Fatalf("service token request = %+v", mock.serviceTokens)
	}

	mock = &projectsMock{projects: twoProjects()}
	_ = captureStdout(t, func() {
		if _, _, err := runProjectCommand(t, mock, "", "tokens", "create", "laptop"); err != nil {
			t.Errorf("tokens create: %v", err)
		}
	})
	if len(mock.personalTokens) != 1 || mock.personalTokens[0].ProjectID != nil {
		t.Fatalf("without --project the platform decides the pin: %+v", mock.personalTokens)
	}
}

func TestCostSummaryReadsAllProjectsAndRendersTheProjectRollup(t *testing.T) {
	edgeID, edgeName := projectTestEdgeID, "Edge"
	fleet := &client.FleetCloudCost{
		Currency: "eur", ClusterCount: 2, MonthlyCostEstimateCents: 84200, MonthToDateCents: 40000, ProjectedMonthEndCents: 90000,
		ByProvider: []client.FleetProviderCost{{Provider: "hetzner", ClusterCount: 2, MonthlyCostEstimateCents: 84200, MonthToDateCents: 40000, ProjectedMonthEndCents: 90000}},
		ByProject: []client.FleetProjectCost{
			{ProjectID: &edgeID, ProjectName: &edgeName, ClusterCount: 1, MonthlyCostEstimateCents: 50000, MonthToDateCents: 25000, ProjectedMonthEndCents: 55000},
			{ClusterCount: 1, MonthlyCostEstimateCents: 34200, MonthToDateCents: 15000, ProjectedMonthEndCents: 35000},
		},
		Scope: &client.FleetCostScope{AllProjects: true, Partial: true},
	}
	mock := &projectsMock{fleet: fleet}
	stdout, _, err := runProjectCommand(t, mock, "", "cost", "summary", "--all-projects")
	if err != nil {
		t.Fatalf("cost summary --all-projects: %v", err)
	}
	if mock.costScope != client.CostProjectScopeAll {
		t.Fatalf("cost scope = %q, want all", mock.costScope)
	}
	for _, fragment := range []string{"By project:", "Edge", "(no project)", "€500.00", "Scope: every project of the organisation (partial"} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("summary missing %q:\n%s", fragment, stdout)
		}
	}

	mock = &projectsMock{fleet: fleet}
	if _, _, err := runProjectCommand(t, mock, "", "cost", "summary"); err != nil {
		t.Fatal(err)
	}
	if mock.costScope != "" {
		t.Fatalf("without --all-projects the read stays in the current project, got scope %q", mock.costScope)
	}
}

func TestCostObjectProjectResolvesTheSlug(t *testing.T) {
	mock := &projectsMock{projects: twoProjects()}
	stdout, _, err := runProjectCommand(t, mock, "", "cost", "object", "project", "edge", "-o", "json")
	if err != nil {
		t.Fatalf("cost object project: %v", err)
	}
	if len(mock.objectKinds) != 1 || mock.objectKinds[0] != client.ObjectCostKindProject || mock.objectSegments[0][0] != projectTestEdgeID {
		t.Fatalf("object read = %v %v", mock.objectKinds, mock.objectSegments)
	}
	if !strings.Contains(stdout, `"kind": "project"`) {
		t.Fatalf("json = %s", stdout)
	}
}

func TestCostBudgetsSetTakesAProjectScope(t *testing.T) {
	mock := &projectsMock{projects: twoProjects()}
	_, _, err := runProjectCommand(t, mock, "", "cost", "budgets", "set", "--scope", "project", "--scope-id", "edge", "--name", "Edge", "--amount", "5000", "--currency", "eur")
	if err != nil {
		t.Fatalf("cost budgets set --scope project: %v", err)
	}
	if len(mock.budgetWrites) != 1 || mock.budgetWrites[0].ScopeKind == nil || *mock.budgetWrites[0].ScopeKind != "project" ||
		mock.budgetWrites[0].ScopeID == nil || *mock.budgetWrites[0].ScopeID != projectTestEdgeID {
		t.Fatalf("budget write = %+v", mock.budgetWrites)
	}
	_, _, err = runProjectCommand(t, &projectsMock{projects: twoProjects()}, "", "cost", "budgets", "set", "--scope", "project", "--name", "Edge", "--amount", "5000", "--currency", "eur")
	if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "a project budget needs --scope-id") {
		t.Fatalf("error = %v", err)
	}
}

func TestOrgUsageGroupByProjectReadsTheShowbackOverTheWindow(t *testing.T) {
	mock := &projectsMock{}
	withTempHome(t)
	setMockClient(t, mock)
	previousNow := usageNow
	usageNow = func() time.Time { return time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { usageNow = previousNow })
	command := newOrgUsageCommand()
	stdout := new(bytes.Buffer)
	command.SetOut(stdout)
	command.SetErr(new(bytes.Buffer))
	command.SetArgs([]string{"--group-by", "project", "--period", "last-month"})
	if err := command.Execute(); err != nil {
		t.Fatalf("org usage --group-by project: %v", err)
	}
	if len(mock.showbackHours) != 1 || mock.showbackHours[0] != 30*24 {
		t.Fatalf("showback hours = %v, want September's 720", mock.showbackHours)
	}
	for _, fragment := range []string{"Worker vCPU-hours by project over the last 720 hours: 150.0 vCPU-h", "Default", "144.0 vCPU-h", "96.0%", "Not attributed to any project: 6.0 vCPU-h over 1 cluster(s)."} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Errorf("showback missing %q:\n%s", fragment, stdout.String())
		}
	}

	command = newOrgUsageCommand()
	stdout.Reset()
	command.SetOut(stdout)
	command.SetErr(new(bytes.Buffer))
	command.SetArgs([]string{"--group-by", "project", "-o", "json"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"window_hours"`) || !strings.Contains(stdout.String(), `"share_pct": 96`) {
		t.Fatalf("json = %s", stdout.String())
	}
	if mock.showbackHours[1] != 7*24+12 {
		t.Fatalf("month to date on the 8th at noon is 180 hours, got %d", mock.showbackHours[1])
	}
}

func TestUsageShowbackHoursClampsTheWindow(t *testing.T) {
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name    string
		options client.UsageOptions
		want    int
	}{
		{name: "month to date", want: 180},
		{name: "explicit window", options: client.UsageOptions{From: "2026-10-01", To: "2026-10-03"}, want: 48},
		{name: "future end is clipped to now", options: client.UsageOptions{From: "2026-10-08", To: "2026-10-20"}, want: 12},
		{name: "a window of over a year is capped", options: client.UsageOptions{From: "2024-01-01"}, want: usageShowbackHoursMaximum},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := usageShowbackHours(testCase.options, now)
			if err != nil {
				t.Fatal(err)
			}
			if got != testCase.want {
				t.Fatalf("hours = %d, want %d", got, testCase.want)
			}
		})
	}
}

func TestUsageShowbackHoursRefusesAWindowThatEndsBeforeItStarts(t *testing.T) {
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	for _, options := range []client.UsageOptions{{From: "2026-10-09"}, {From: "2026-10-05", To: "2026-10-01"}} {
		if _, err := usageShowbackHours(options, now); err == nil || exitCodeFor(err) != exitUsage {
			t.Errorf("%+v: error = %v (exit %d), want a usage error", options, err, exitCodeFor(err))
		}
	}
}

func TestCostScopeIsResetByACommandWithoutAllProjects(t *testing.T) {
	mock := &projectsMock{fleet: &client.FleetCloudCost{Currency: "eur"}, costScope: client.CostProjectScopeAll}
	if _, _, err := runProjectCommand(t, mock, "", "cost", "summary"); err != nil {
		t.Fatal(err)
	}
	if mock.costScope != "" {
		t.Fatalf("cost scope = %q, want it cleared", mock.costScope)
	}
}

func TestSavedProjectSelectionMatchesTheSavedOrganisationWithoutAnOverride(t *testing.T) {
	withTempHome(t)
	t.Setenv(envAnkraProject, "")
	name := "Ankra"
	if err := saveSelectedOrganisation(SelectedOrganisation{OrganisationID: projectTestOrganisationID, Name: &name}); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name                string
		projectOrganisation string
		want                string
	}{
		{name: "same organisation", projectOrganisation: projectTestOrganisationID, want: projectTestEdgeID},
		{name: "another organisation", projectOrganisation: "7b2fedb6-4d79-45fd-b71a-044a2cfddce7", want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := saveSelectedProject(SelectedProject{ProjectID: projectTestEdgeID, OrganisationID: testCase.projectOrganisation}); err != nil {
				t.Fatal(err)
			}
			mock := &projectsMock{projects: twoProjects()}
			setMockClient(t, mock)
			if err := applyProjectOverride(projectFlagCommand(t, ""), sourceConfigFile); err != nil {
				t.Fatal(err)
			}
			if mock.project != testCase.want {
				t.Fatalf("project header = %q, want %q", mock.project, testCase.want)
			}
		})
	}
}

func TestLocalProjectFlagsShadowTheGlobalOneWhileDeprecated(t *testing.T) {
	t.Run("skills --project is the directory", func(t *testing.T) {
		directory := t.TempDir()
		withTempHome(t)
		t.Setenv(envAnkraProject, "")
		setMockClient(t, &projectsMock{listError: errors.New("the global --project must not be consulted")})
		t.Cleanup(func() { resetGlobalProjectFlag(t); resetTreeFlags(t, skillsClientsCmd) })
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(stderr)
		rootCmd.SetArgs([]string{"skills", "clients", "--project", directory, "-o", "json"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("skills clients --project <dir>: %v", err)
		}
		if !strings.Contains(stdout.String(), directory) {
			t.Fatalf("the directory must be the install root: %s", stdout.String())
		}
		// Cobra prints flag deprecations through the command's Print, which
		// is stderr in a terminal and the Out writer a test installs.
		notices := stdout.String() + stderr.String()
		if !strings.Contains(notices, "--project has been deprecated") || !strings.Contains(notices, "--dir") {
			t.Fatalf("output = %q, want the deprecation pointing at --dir", notices)
		}
		if rootCmd.PersistentFlags().Lookup("project").Changed {
			t.Fatal("the local flag must shadow the global --project, not set it")
		}
	})
	t.Run("skills --dir is the new spelling", func(t *testing.T) {
		directory := t.TempDir()
		withTempHome(t)
		setMockClient(t, &projectsMock{})
		t.Cleanup(func() { resetGlobalProjectFlag(t); resetTreeFlags(t, skillsClientsCmd) })
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(stderr)
		rootCmd.SetArgs([]string{"skills", "clients", "--dir", directory, "-o", "json"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), directory) || strings.Contains(stderr.String(), "deprecated") {
			t.Fatalf("stdout = %s, stderr = %s", stdout.String(), stderr.String())
		}
	})
	t.Run("registry robots create --project is the registry project", func(t *testing.T) {
		mock := &registryRobotsMock{}
		withTempHome(t)
		t.Setenv(envAnkraProject, "")
		setMockClient(t, mock)
		t.Cleanup(func() { resetGlobalProjectFlag(t) })
		commandOutput, stderr := new(bytes.Buffer), new(bytes.Buffer)
		_ = captureStdout(t, func() {
			rootCmd.SetOut(commandOutput)
			rootCmd.SetErr(stderr)
			rootCmd.SetArgs([]string{"registry", "robots", "create", "staging-ci", "--project", "staging"})
			if err := rootCmd.Execute(); err != nil {
				t.Errorf("registry robots create --project: %v", err)
			}
		})
		if mock.createRequest == nil || mock.createRequest.Project != "staging" {
			t.Fatalf("create request = %+v, want the registry project staging", mock.createRequest)
		}
		notices := commandOutput.String() + stderr.String()
		if !strings.Contains(notices, "--project has been deprecated") || !strings.Contains(notices, "--registry-project") {
			t.Fatalf("output = %q, want the deprecation pointing at --registry-project", notices)
		}
		if rootCmd.PersistentFlags().Lookup("project").Changed {
			t.Fatal("the local flag must shadow the global --project, not set it")
		}
	})
}

func TestProjectsDisabledIsAMissingTarget(t *testing.T) {
	if got := exitCodeFor(projectsCommandError(client.ErrProjectsDisabled)); got != exitNotFound {
		t.Fatalf("exit code = %d, want %d", got, exitNotFound)
	}
	if got := exitCodeFor(projectsCommandError(client.ErrProjectNotFound)); got != exitNotFound {
		t.Fatalf("exit code = %d, want %d", got, exitNotFound)
	}
	if got := exitCodeFor(projectsCommandError(client.ErrProjectsRouteMissing)); got != exitError {
		t.Fatalf("exit code = %d, want %d", got, exitError)
	}
}

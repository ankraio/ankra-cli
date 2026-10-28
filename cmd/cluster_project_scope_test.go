package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ankra/internal/client"
)

const (
	defaultProjectID   = "aaaaaaaa-0000-0000-0000-000000000001"
	siblingProjectID   = "aaaaaaaa-0000-0000-0000-000000000002"
	thirdProjectID     = "aaaaaaaa-0000-0000-0000-000000000003"
	siblingClusterID   = "33333333-3333-3333-3333-333333333333"
	siblingClusterName = "edge-eu"
)

// projectScopeMock adds the project layer to orgScopeMock: inside the
// selected organisation the backend reads a cluster only in the request's
// project (the override header, else the member's current project), and
// answers 404 for a cluster of a sibling project.
type projectScopeMock struct {
	*orgScopeMock
	projectOverride   string
	projects          []client.ProjectSummary
	listProjectsError error
	listProjectsCalls int
	clustersByProject map[string][]client.ClusterListItem
	// otherOrganisationProjects records the project override seen by each
	// lookup outside the selected organisation.
	otherOrganisationProjects []string
	tokenProjects             []string
}

func (m *projectScopeMock) SetProjectOverride(projectID string) { m.projectOverride = projectID }
func (m *projectScopeMock) ProjectOverride() string             { return m.projectOverride }

func (m *projectScopeMock) ListProjects() ([]client.ProjectSummary, error) {
	m.listProjectsCalls++
	if m.listProjectsError != nil {
		return nil, m.listProjectsError
	}
	return m.projects, nil
}

func (m *projectScopeMock) project() string {
	if m.projectOverride != "" {
		return m.projectOverride
	}
	return defaultProjectID
}

func (m *projectScopeMock) GetClusterByID(clusterID string) (client.ClusterListItem, error) {
	if m.scope() != m.selected {
		m.otherOrganisationProjects = append(m.otherOrganisationProjects, m.projectOverride)
		return m.orgScopeMock.GetClusterByID(clusterID)
	}
	m.byIDScopes = append(m.byIDScopes, m.scope()+"/"+m.project())
	for _, cluster := range m.clustersByProject[m.project()] {
		if cluster.ID == clusterID {
			return cluster, nil
		}
	}
	return client.ClusterListItem{}, fmt.Errorf("no cluster found for id %q: %w", clusterID, client.ErrClusterNotFound)
}

func (m *projectScopeMock) GetClusterKubeToken(ctx context.Context, clusterID string) (*client.KubeToken, error) {
	m.tokenProjects = append(m.tokenProjects, m.project())
	return m.orgScopeMock.GetClusterKubeToken(ctx, clusterID)
}

func newProjectScopeMock(t *testing.T) *projectScopeMock {
	t.Helper()
	organisations := crossOrgMock()
	organisations.clustersByOrg[selectedOrgID] = append(organisations.clustersByOrg[selectedOrgID],
		client.ClusterListItem{ID: siblingClusterID, Name: siblingClusterName, OrganisationID: selectedOrgID})
	mock := &projectScopeMock{
		orgScopeMock: organisations,
		projects: []client.ProjectSummary{
			{ID: defaultProjectID, Slug: "default", IsDefault: true, IsCurrent: true},
			{ID: siblingProjectID, Slug: "edge"},
			{ID: thirdProjectID, Slug: "data"},
		},
		clustersByProject: map[string][]client.ClusterListItem{
			defaultProjectID: {{ID: "22222222-2222-2222-2222-222222222222", Name: "depict-prod", OrganisationID: selectedOrgID}},
			siblingProjectID: {{ID: siblingClusterID, Name: siblingClusterName, OrganisationID: selectedOrgID}},
		},
	}
	withOrgScopeMock(t, organisations)
	apiClient = mock
	originalPin := projectPinnedExplicitly
	projectPinnedExplicitly = false
	t.Cleanup(func() { projectPinnedExplicitly = originalPin })
	return mock
}

func TestKubeTokenFindsAClusterInASiblingProject(t *testing.T) {
	mock := newProjectScopeMock(t)

	clusterID, known, err := resolveGatewayClusterID(siblingClusterID, nil)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if clusterID != siblingClusterID || !known {
		t.Fatalf("resolve = (%q, %v), want the cluster with its owner known", clusterID, known)
	}
	if mock.projectOverride != siblingProjectID {
		t.Fatalf("project scope = %q, want the sibling project %q", mock.projectOverride, siblingProjectID)
	}
	if mock.listOrganisationsCalls != 0 {
		t.Errorf("a cluster of the organisation in scope needs no cross-organisation search, got %d listings", mock.listOrganisationsCalls)
	}
	if mock.override != "" {
		t.Errorf("organisation scope moved to %q", mock.override)
	}
	if _, err := apiClient.GetClusterKubeToken(context.Background(), clusterID); err != nil {
		t.Fatalf("token mint failed: %v", err)
	}
	if len(mock.tokenProjects) != 1 || mock.tokenProjects[0] != siblingProjectID {
		t.Errorf("token minted in projects %v, want [%s]", mock.tokenProjects, siblingProjectID)
	}
}

func TestProjectSweepSkipsTheProjectAlreadyAsked(t *testing.T) {
	mock := newProjectScopeMock(t)
	mock.projectOverride = thirdProjectID
	mock.clustersByProject[thirdProjectID] = nil

	if _, _, err := resolveGatewayClusterID(siblingClusterID, nil); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	asked := map[string]int{}
	for _, scope := range mock.byIDScopes {
		asked[scope]++
	}
	if asked[selectedOrgID+"/"+thirdProjectID] != 1 {
		t.Errorf("the project in scope is asked once, got lookups %v", mock.byIDScopes)
	}
	if mock.projectOverride != siblingProjectID {
		t.Errorf("project scope = %q, want %q", mock.projectOverride, siblingProjectID)
	}
}

func TestProjectSweepRestoresTheProjectOnAMiss(t *testing.T) {
	mock := newProjectScopeMock(t)
	mock.projectOverride = siblingProjectID
	mock.clustersByProject[siblingProjectID] = nil

	_, _, err := resolveGatewayClusterID(otherOrgClusterID, nil)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if mock.override != owningOrgID {
		t.Fatalf("organisation scope = %q, want the owning organisation", mock.override)
	}
	for _, project := range mock.otherOrganisationProjects {
		if project != "" {
			t.Fatalf("another organisation was asked with a project of this one: %v", mock.otherOrganisationProjects)
		}
	}
	if mock.projectOverride != "" {
		t.Errorf("after adopting another organisation the project override is cleared, got %q", mock.projectOverride)
	}
}

func TestProjectSweepHonoursAnExplicitPin(t *testing.T) {
	mock := newProjectScopeMock(t)
	projectPinnedExplicitly = true
	mock.projectOverride = defaultProjectID

	_, _, err := resolveGatewayClusterID(siblingClusterID, nil)
	if err == nil {
		t.Fatal("a cluster outside the pinned project must not be found in another project")
	}
	if mock.listProjectsCalls != 0 {
		t.Errorf("an explicit pin lists no projects, got %d", mock.listProjectsCalls)
	}
	if mock.projectOverride != defaultProjectID {
		t.Errorf("the pinned project changed to %q", mock.projectOverride)
	}
	if code := exitCodeFor(err); code != exitNotFound {
		t.Errorf("exit code = %d, want %d", code, exitNotFound)
	}
}

func TestProjectSweepFallsThroughWhenProjectsCannotBeListed(t *testing.T) {
	mock := newProjectScopeMock(t)
	mock.listProjectsError = client.ErrProjectsDisabled

	if _, _, err := resolveGatewayClusterID(otherOrgClusterID, nil); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if mock.override != owningOrgID {
		t.Errorf("organisation scope = %q, want the owning organisation", mock.override)
	}
}

func TestSelectedClusterProjectScopesTheTokenMint(t *testing.T) {
	mock := newProjectScopeMock(t)
	path, err := selectedClusterFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	projectID := siblingProjectID
	if err := saveSelectedCluster(client.ClusterListItem{ID: siblingClusterID, Name: siblingClusterName, OrganisationID: selectedOrgID, ProjectID: &projectID}); err != nil {
		t.Fatal(err)
	}

	clusterID, known, err := resolveGatewayClusterID("", nil)
	if err != nil || clusterID != siblingClusterID || !known {
		t.Fatalf("resolve = (%q, %v, %v)", clusterID, known, err)
	}
	if mock.listProjectsCalls != 0 {
		t.Errorf("a selection that carries its project needs no project sweep, got %d listings", mock.listProjectsCalls)
	}
	if mock.projectOverride != siblingProjectID {
		t.Errorf("project scope = %q, want %q", mock.projectOverride, siblingProjectID)
	}
}

func TestSelectedClusterProjectIgnoredForAnotherOrganisation(t *testing.T) {
	mock := newProjectScopeMock(t)
	projectID := siblingProjectID
	adoptSelectedClusterProject(client.ClusterListItem{ID: siblingClusterID, OrganisationID: owningOrgID, ProjectID: &projectID})
	if mock.projectOverride != "" {
		t.Errorf("a project saved in another organisation must not apply, got %q", mock.projectOverride)
	}
	projectPinnedExplicitly = true
	adoptSelectedClusterProject(client.ClusterListItem{ID: siblingClusterID, OrganisationID: selectedOrgID, ProjectID: &projectID})
	if mock.projectOverride != "" {
		t.Errorf("an explicit pin wins over the selection, got %q", mock.projectOverride)
	}
}

func TestFindClusterInOrganisationProjectsIgnoresNames(t *testing.T) {
	mock := newProjectScopeMock(t)
	if answer := findClusterInOrganisationProjects(siblingClusterName); answer.found() {
		t.Fatal("only ids are swept across projects")
	}
	if mock.listProjectsCalls != 0 {
		t.Errorf("a name lists no projects, got %d", mock.listProjectsCalls)
	}
	mock.projects = mock.projects[:1]
	if answer := findClusterInOrganisationProjects(siblingClusterID); answer.found() {
		t.Fatal("an organisation with one project has no sibling to sweep")
	}
}

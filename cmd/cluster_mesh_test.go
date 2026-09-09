package cmd

import (
	"errors"
	"strings"
	"testing"

	"ankra/internal/client"
)

// The mesh routes take cluster ids. These are id-shaped so resolveClusterArg
// passes them through untouched; a cluster NAME reaching these commands is
// covered in cluster_name_args_test.go.
const (
	meshTestClusterID      = "9c1f2d3e-4a5b-4c6d-8e9f-0a1b2c3d4e5f"
	meshTestOtherClusterID = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
)

type clusterMeshMock struct {
	baseMock
	meshes    []client.ClusterMesh
	readiness map[string]client.ClusterMeshReadiness

	createdNames []string
	joins        [][2]string
	leaves       [][2]string
	deleted      []string
	readinessFor [][]string

	joinError error

	madeReadyCluster string
	madeReadySiteIP  string
	makeReadyError   error
}

func (m *clusterMeshMock) ListClusterMeshes() ([]client.ClusterMesh, error) {
	return m.meshes, nil
}

func (m *clusterMeshMock) GetClusterMesh(meshID string) (*client.ClusterMesh, error) {
	for _, mesh := range m.meshes {
		if mesh.ID == meshID {
			return &mesh, nil
		}
	}
	return nil, errors.New("cluster mesh not found")
}

func (m *clusterMeshMock) CreateClusterMesh(name string) (*client.ClusterMesh, error) {
	m.createdNames = append(m.createdNames, name)
	return &client.ClusterMesh{ID: "mesh-1", Slug: "production-eu", Name: name, Status: "pending"}, nil
}

func (m *clusterMeshMock) DeleteClusterMesh(meshID string) error {
	m.deleted = append(m.deleted, meshID)
	return nil
}

func (m *clusterMeshMock) MakeClusterMeshReady(clusterID string, sitePublicIP string) (*client.ClusterMeshMakeReadyResult, error) {
	m.madeReadyCluster = clusterID
	m.madeReadySiteIP = sitePublicIP
	if m.makeReadyError != nil {
		return nil, m.makeReadyError
	}
	return &client.ClusterMeshMakeReadyResult{
		ClusterID: clusterID, CiliumClusterID: 7, CiliumClusterName: "made-ready-7",
		IdentityAllocated: true, TransitionedResources: 2,
	}, nil
}

func (m *clusterMeshMock) JoinClusterMesh(meshID string, clusterID string) error {
	if m.joinError != nil {
		return m.joinError
	}
	m.joins = append(m.joins, [2]string{meshID, clusterID})
	return nil
}

func (m *clusterMeshMock) LeaveClusterMesh(meshID string, clusterID string) error {
	m.leaves = append(m.leaves, [2]string{meshID, clusterID})
	return nil
}

func (m *clusterMeshMock) CheckClusterMeshReadiness(clusterIDs []string) (map[string]client.ClusterMeshReadiness, error) {
	m.readinessFor = append(m.readinessFor, clusterIDs)
	return m.readiness, nil
}

func executeMeshCommand(t *testing.T, args ...string) error {
	t.Helper()
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	rootCmd.SetOut(new(strings.Builder))
	rootCmd.SetErr(new(strings.Builder))
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}

func TestClusterMeshCreatePassesTheName(t *testing.T) {
	mock := &clusterMeshMock{}
	setMockClient(t, mock)

	if err := executeMeshCommand(t, "cluster", "mesh", "create", "Production EU"); err != nil {
		t.Fatalf("create returned %v", err)
	}
	if len(mock.createdNames) != 1 || mock.createdNames[0] != "Production EU" {
		t.Fatalf("expected the name to reach the API, got %v", mock.createdNames)
	}
}

func TestClusterMeshJoinPassesMeshAndCluster(t *testing.T) {
	mock := &clusterMeshMock{}
	setMockClient(t, mock)

	if err := executeMeshCommand(t, "cluster", "mesh", "join", "mesh-1", meshTestClusterID); err != nil {
		t.Fatalf("join returned %v", err)
	}
	if len(mock.joins) != 1 || mock.joins[0] != [2]string{"mesh-1", meshTestClusterID} {
		t.Fatalf("expected one join of %s into mesh-1, got %v", meshTestClusterID, mock.joins)
	}
}

// A refusal from the platform carries the operator-facing reason, so the CLI
// must surface it rather than replacing it with a generic failure.
func TestClusterMeshJoinSurfacesTheRefusalReason(t *testing.T) {
	mock := &clusterMeshMock{
		joinError: errors.New("cluster c1 cannot join the mesh: the cluster's nodes are not on the platform WireGuard overlay"),
	}
	setMockClient(t, mock)

	err := executeMeshCommand(t, "cluster", "mesh", "join", "mesh-1", meshTestClusterID)
	if err == nil {
		t.Fatal("expected the refusal to fail the command")
	}
	if !strings.Contains(err.Error(), "not on the platform WireGuard overlay") {
		t.Fatalf("the reason must reach the operator, got %v", err)
	}
}

func TestClusterMeshLeavePassesMeshAndCluster(t *testing.T) {
	mock := &clusterMeshMock{}
	setMockClient(t, mock)

	if err := executeMeshCommand(t, "cluster", "mesh", "leave", "mesh-1", meshTestClusterID); err != nil {
		t.Fatalf("leave returned %v", err)
	}
	if len(mock.leaves) != 1 || mock.leaves[0] != [2]string{"mesh-1", meshTestClusterID} {
		t.Fatalf("expected one leave, got %v", mock.leaves)
	}
}

func TestClusterMeshReadinessPassesEveryCluster(t *testing.T) {
	mock := &clusterMeshMock{
		readiness: map[string]client.ClusterMeshReadiness{
			meshTestClusterID: {Ready: true},
			meshTestOtherClusterID: {Ready: false, Items: []client.ClusterMeshReadinessItem{
				{Name: "transport", Ready: false, Detail: "not on the overlay", Remediable: false},
			}},
		},
	}
	setMockClient(t, mock)

	if err := executeMeshCommand(t, "cluster", "mesh", "readiness", meshTestClusterID, meshTestOtherClusterID); err != nil {
		t.Fatalf("readiness returned %v", err)
	}
	if len(mock.readinessFor) != 1 || len(mock.readinessFor[0]) != 2 {
		t.Fatalf("expected both clusters in one call, got %v", mock.readinessFor)
	}
}

func TestClusterMeshReadinessNeedsAtLeastOneCluster(t *testing.T) {
	mock := &clusterMeshMock{}
	setMockClient(t, mock)

	if err := executeMeshCommand(t, "cluster", "mesh", "readiness"); err == nil {
		t.Fatal("readiness with no clusters should be rejected by the argument check")
	}
}

func TestClusterMeshShowRejectsAnUnknownMesh(t *testing.T) {
	mock := &clusterMeshMock{}
	setMockClient(t, mock)

	if err := executeMeshCommand(t, "cluster", "mesh", "show", "missing"); err == nil {
		t.Fatal("an unknown mesh should fail the command")
	}
}

func TestClusterMeshDeletePassesTheMesh(t *testing.T) {
	mock := &clusterMeshMock{}
	setMockClient(t, mock)

	if err := executeMeshCommand(t, "cluster", "mesh", "delete", "mesh-1"); err != nil {
		t.Fatalf("delete returned %v", err)
	}
	if len(mock.deleted) != 1 || mock.deleted[0] != "mesh-1" {
		t.Fatalf("expected one delete of mesh-1, got %v", mock.deleted)
	}
}

func TestClusterMeshMakeReadyCommand(t *testing.T) {
	mock := &clusterMeshMock{}
	setMockClient(t, mock)

	if err := executeMeshCommand(t, "cluster", "mesh", "make-ready", meshTestClusterID, "--site-public-ip", "203.0.113.9"); err != nil {
		t.Fatalf("make-ready returned %v", err)
	}
	if mock.madeReadyCluster != meshTestClusterID || mock.madeReadySiteIP != "203.0.113.9" {
		t.Fatalf("make-ready must pass the cluster and site address through, got %q %q",
			mock.madeReadyCluster, mock.madeReadySiteIP)
	}
}

// The platform's refusal names the reason (an unwired provider, a missing
// site address); the CLI must surface it, not replace it.
func TestClusterMeshMakeReadySurfacesTheRefusalReason(t *testing.T) {
	mock := &clusterMeshMock{makeReadyError: errors.New("network_mode is not supported for hetzner clusters")}
	setMockClient(t, mock)

	err := executeMeshCommand(t, "cluster", "mesh", "make-ready", meshTestClusterID)
	if err == nil {
		t.Fatal("expected the refusal to fail the command")
	}
	if !strings.Contains(err.Error(), "not supported for hetzner") {
		t.Fatalf("the refusal must carry the platform's reason, got %v", err)
	}
}

package cmd

import (
	"strings"
	"testing"

	"ankra/internal/client"
)

const meshTestThirdClusterID = "2b3c4d5e-6f7a-4b8c-9d0e-1f2a3b4c5d6e"

func meshUpReadiness(ready bool, items ...client.ClusterMeshReadinessItem) client.ClusterMeshReadiness {
	return client.ClusterMeshReadiness{Ready: ready, Items: items}
}

func failing(name string, remediable bool) client.ClusterMeshReadinessItem {
	return client.ClusterMeshReadinessItem{Name: name, Ready: false, Detail: name + " failed", Remediable: remediable}
}

// TestPlanMeshUpDecidesPerCluster pins the planner's rules: members are
// left alone, a ready cluster joins, identity/transport failures mean a
// make-ready, an overlapping pod range renumbers every colliding cluster
// after the first (and only with the flag), a burned-in failure blocks.
func TestPlanMeshUpDecidesPerCluster(t *testing.T) {
	order := []string{"a", "b", "c", "d", "e", "f"}
	labels := map[string]string{"a": "a", "b": "b", "c": "c", "d": "d", "e": "e", "f": "f"}
	readiness := map[string]client.ClusterMeshReadiness{
		"a": meshUpReadiness(true),
		"b": meshUpReadiness(false, failing(meshItemIdentity, true), failing(meshItemTransport, true)),
		"c": meshUpReadiness(false, failing(meshItemRanges, true)),
		"d": meshUpReadiness(false, failing("platform", false)),
		"e": meshUpReadiness(false, failing("reachability", true)),
	}
	actions := planMeshUp(order, labels, readiness, map[string]bool{"f": true}, false)
	kinds := map[string]string{}
	for _, action := range actions {
		kinds[action.ClusterID] = action.Kind
	}
	if kinds["a"] != meshUpJoin || kinds["b"] != meshUpMakeReady || kinds["d"] != meshUpBlocked || kinds["e"] != meshUpWait || kinds["f"] != meshUpMember {
		t.Fatalf("kinds = %v", kinds)
	}
	if kinds["c"] != meshUpBlocked || !strings.Contains(actions[2].Reason, "--renumber-pods") {
		t.Fatalf("an overlapping range without the flag must block with the flag named: %+v", actions[2])
	}
	withFlag := planMeshUp(order, labels, readiness, map[string]bool{}, true)
	if withFlag[2].Kind != meshUpRenumber {
		t.Fatalf("with the flag the colliding cluster after the first renumbers: %+v", withFlag[2])
	}
	readiness["a"] = meshUpReadiness(false, failing(meshItemRanges, true))
	firstKeeps := planMeshUp(order, labels, readiness, map[string]bool{}, true)
	if firstKeeps[0].Kind != meshUpWait || !strings.Contains(firstKeeps[0].Reason, "keeps its pod range") {
		t.Fatalf("the first cluster keeps its range while the others move: %+v", firstKeeps[0])
	}
	if unknown := planMeshUp([]string{"zz"}, map[string]string{"zz": "zz"}, readiness, nil, false); unknown[0].Kind != meshUpBlocked {
		t.Fatalf("a cluster the platform reported nothing for is blocked: %+v", unknown[0])
	}
}

// TestClusterMeshUpOnePassPreparesJoinsAndReportsBlockers drives one pass
// (--wait 0) through the mock: the named mesh is found by slug, the ready
// cluster joins it, the identity-less one gets a make-ready, and the
// cluster whose pod range collides is reported as needing the flag.
func TestClusterMeshUpOnePassPreparesJoinsAndReportsBlockers(t *testing.T) {
	mock := &clusterMeshMock{
		meshes: []client.ClusterMesh{{ID: "mesh-1", Slug: "production-eu", Name: "Production EU", Status: "pending"}},
		readiness: map[string]client.ClusterMeshReadiness{
			meshTestClusterID:      meshUpReadiness(true),
			meshTestOtherClusterID: meshUpReadiness(false, failing(meshItemIdentity, true), failing(meshItemTransport, true)),
			meshTestThirdClusterID: meshUpReadiness(false, failing(meshItemRanges, true)),
		},
	}
	setMockClient(t, mock)

	err := executeMeshCommand(t, "cluster", "mesh", "up", "production-eu", meshTestClusterID, meshTestOtherClusterID, meshTestThirdClusterID, "--wait", "0")
	if err == nil || !strings.Contains(err.Error(), "--renumber-pods") {
		t.Fatalf("the colliding cluster must be reported as blocked until the flag is given: %v", err)
	}
	if len(mock.createdNames) != 0 {
		t.Fatalf("an existing mesh must be reused, not created: %v", mock.createdNames)
	}
	if len(mock.joins) != 1 || mock.joins[0] != [2]string{"mesh-1", meshTestClusterID} {
		t.Fatalf("the ready cluster must join the found mesh: %v", mock.joins)
	}
	if len(mock.madeReadyClusters) != 1 || mock.madeReadyClusters[0] != meshTestOtherClusterID || mock.madeReadyPodCIDR != "" {
		t.Fatalf("the identity-less cluster gets a plain make-ready: %v %q", mock.madeReadyClusters, mock.madeReadyPodCIDR)
	}
}

// TestClusterMeshUpRenumbersWithTheFlagAndCreatesTheMesh pins the other
// half: with --renumber-pods the colliding cluster is made ready with
// pod_cidr auto, and a mesh that does not exist yet is created by name.
func TestClusterMeshUpRenumbersWithTheFlagAndCreatesTheMesh(t *testing.T) {
	mock := &clusterMeshMock{
		readiness: map[string]client.ClusterMeshReadiness{
			meshTestClusterID:      meshUpReadiness(false, failing(meshItemRanges, true)),
			meshTestOtherClusterID: meshUpReadiness(false, failing(meshItemRanges, true)),
		},
	}
	setMockClient(t, mock)

	err := executeMeshCommand(t, "cluster", "mesh", "up", "Production EU", meshTestClusterID, meshTestOtherClusterID, "--renumber-pods", "--wait", "0")
	if err == nil || !strings.Contains(err.Error(), "still converging") {
		t.Fatalf("one pass with work pending ends with the converging message: %v", err)
	}
	if len(mock.createdNames) != 1 || mock.createdNames[0] != "Production EU" {
		t.Fatalf("a missing mesh is created by name: %v", mock.createdNames)
	}
	if len(mock.madeReadyClusters) != 1 || mock.madeReadyClusters[0] != meshTestOtherClusterID || mock.madeReadyPodCIDR != "auto" {
		t.Fatalf("only the cluster after the first renumbers, with pod_cidr auto: %v %q", mock.madeReadyClusters, mock.madeReadyPodCIDR)
	}
	if len(mock.joins) != 0 {
		t.Fatalf("nothing joins while the ranges still collide: %v", mock.joins)
	}
}

// TestClusterMeshUpPlanChangesNothing pins --plan: the pass is printed and
// no mesh is created, no cluster made ready, nothing joined.
func TestClusterMeshUpPlanChangesNothing(t *testing.T) {
	mock := &clusterMeshMock{
		readiness: map[string]client.ClusterMeshReadiness{
			meshTestClusterID:      meshUpReadiness(true),
			meshTestOtherClusterID: meshUpReadiness(false, failing(meshItemIdentity, true)),
		},
	}
	setMockClient(t, mock)
	output := new(strings.Builder)
	rootCmd.SetOut(output)
	rootCmd.SetArgs([]string{"cluster", "mesh", "up", "new-mesh", meshTestClusterID, meshTestOtherClusterID, "--plan"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("plan must not fail: %v", err)
	}
	if len(mock.createdNames) != 0 || len(mock.joins) != 0 || len(mock.madeReadyClusters) != 0 {
		t.Fatal("plan must change nothing")
	}
	for _, line := range []string{"plan: create mesh 'new-mesh'", "plan: join \"" + meshTestClusterID, "plan: make-ready \"" + meshTestOtherClusterID} {
		if !strings.Contains(output.String(), line) {
			t.Fatalf("plan output lacks %q:\n%s", line, output.String())
		}
	}
}

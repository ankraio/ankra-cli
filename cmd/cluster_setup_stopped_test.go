package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"
)

const stoppedSetupOperationID = "bc80d0cf-1a2b-4c3d-8e9f-0a1b2c3d4e5f"

// stoppedSetupCluster is the 2026-11-03 shape (ankra-2v9ewh): a create that
// failed on a Hetzner quota refusal, whose stored state is still creating.
func stoppedSetupCluster() client.ClusterListItem {
	refusal := "Hetzner API 403 resource_limit_exceeded\nserver limit reached"
	stoppedAt := "2026-11-03T21:48:26Z"
	return client.ClusterListItem{
		ID: testClusterID, Name: "provisioning-test", State: "creating", Kind: "hetzner",
		CreatedAt: "2026-11-03T21:40:00Z",
		SetupStopped: &client.ClusterSetupStopped{
			ExecutionID: stoppedSetupOperationID, Status: "failed", ErrorExcerpt: &refusal, StoppedAt: &stoppedAt,
		},
	}
}

func TestClusterListMarksAStoppedSetup(t *testing.T) {
	setMockClient(t, &clusterListMock{clusters: []client.ClusterListItem{
		stoppedSetupCluster(),
		{ID: "cluster-id-2", Name: "building", State: "creating", Kind: "hetzner", CreatedAt: "2026-11-03T21:40:00Z"},
	}})

	output := runPortalLinkCommand(t, clusterListCmd, "cluster", "list")

	if !strings.Contains(output, "creating (setup stopped)") {
		t.Errorf("a cluster whose setup stopped must say so in the State column, got:\n%s", output)
	}
	if strings.Count(output, "(setup stopped)") != 1 {
		t.Errorf("only the stopped cluster is marked; a creating cluster without setup_stopped is progressing, got:\n%s", output)
	}
	if !strings.Contains(output, "Setup stopped on 1 cluster: `ankra cluster info <name>`") {
		t.Errorf("the table must point at the command that shows the error, got:\n%s", output)
	}
}

func TestClusterListWithoutAStoppedSetupPrintsNoHint(t *testing.T) {
	setMockClient(t, &clusterListMock{clusters: []client.ClusterListItem{
		{ID: testClusterID, Name: "production", State: "online", Kind: "hetzner", CreatedAt: "2026-11-03T21:40:00Z"},
	}})

	output := runPortalLinkCommand(t, clusterListCmd, "cluster", "list")

	if strings.Contains(output, "etup stopped") {
		t.Errorf("no cluster's setup stopped, so nothing may say so, got:\n%s", output)
	}
}

func TestClusterListJSONCarriesTheStoppedSetup(t *testing.T) {
	setMockClient(t, &clusterListMock{clusters: []client.ClusterListItem{stoppedSetupCluster()}})

	output := runPortalLinkCommand(t, clusterListCmd, "cluster", "list", "-o", "json")

	var clusters []struct {
		State        string `json:"state"`
		SetupStopped *struct {
			ExecutionID  string  `json:"execution_id"`
			Status       string  `json:"status"`
			ErrorExcerpt *string `json:"error_excerpt"`
			StoppedAt    *string `json:"stopped_at"`
		} `json:"setup_stopped"`
	}
	if decodeError := json.Unmarshal([]byte(output), &clusters); decodeError != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", decodeError, output)
	}
	if len(clusters) != 1 || clusters[0].SetupStopped == nil {
		t.Fatalf("-o json must carry setup_stopped, got:\n%s", output)
	}
	stopped := clusters[0].SetupStopped
	if clusters[0].State != "creating" || stopped.ExecutionID != stoppedSetupOperationID || stopped.Status != "failed" ||
		stopped.ErrorExcerpt == nil || stopped.StoppedAt == nil || *stopped.StoppedAt != "2026-11-03T21:48:26Z" {
		t.Errorf("-o json must carry the stored state and the platform's setup_stopped unchanged, got:\n%s", output)
	}
	if strings.Contains(output, "(setup stopped)") {
		t.Errorf("-o json keeps the stored state; the label is for people, got:\n%s", output)
	}
}

func TestClusterInfoShowsTheOperationTheSetupStoppedAt(t *testing.T) {
	setMockClient(t, &clusterGetMock{cluster: stoppedSetupCluster()})

	output := runPortalLinkCommand(t, clusterInfoCmd, "cluster", "info", "provisioning-test")

	for _, expected := range []string{
		"State: creating (setup stopped)",
		"Setup stopped: operation " + stoppedSetupOperationID + " failed",
		"Error: Hetzner API 403 resource_limit_exceeded",
		"server limit reached",
		"Details: ankra cluster operations list " + stoppedSetupOperationID,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("cluster info must print %q, got:\n%s", expected, output)
		}
	}
}

func TestClusterInfoWithoutAStoppedSetupPrintsItsState(t *testing.T) {
	setMockClient(t, &clusterGetMock{cluster: client.ClusterListItem{
		ID: testClusterID, Name: "building", State: "creating",
	}})

	output := runPortalLinkCommand(t, clusterInfoCmd, "cluster", "info", "building")

	if !strings.Contains(output, "State: creating\n") || strings.Contains(output, "etup stopped") {
		t.Errorf("a setup that has not stopped reads as its stored state alone, got:\n%s", output)
	}
}

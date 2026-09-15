package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ankra/internal/client"

	"github.com/spf13/cobra"
)

func sampleStackDataInventory() *client.StackDataInventory {
	return &client.StackDataInventory{
		StackName:  backupTestStack,
		Namespaces: []string{"shop"},
		Assets: []client.StackDataAsset{
			{
				Kind: "persistent_volume_claim", Namespace: "shop", Name: "data",
				Engine: "velero", Consistency: "crash", RequestedBytes: 5368709120,
				StorageClasses: []string{"standard"},
			},
			{
				Kind: "cnpg_cluster", Namespace: "shop", Name: "orders",
				Engine: "cnpg", Consistency: "transactional", RequestedBytes: 10737418240,
				PersistentVolumeClaimNames: []string{"orders-1"},
			},
		},
		TotalRequestedBytes: 16106127360,
		CustomResourceScan:  client.CustomResourceScanComplete,
	}
}

func TestStackDataListRendersEveryAssetAndItsCarrier(t *testing.T) {
	mock := newBackupLaneMock()
	mock.inventory = sampleStackDataInventory()

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksDataListCmd},
		"cluster", "stacks", "data", "list", backupTestStack, "--cluster", "demo")

	if executeError != nil {
		t.Fatalf("listing a stack's data: %v", executeError)
	}
	stripped := stripANSICodes(output)
	for _, expected := range []string{
		"KIND", "NAMESPACE", "ENGINE", "CONSISTENCY", "REQUESTED", "CARRIED BY",
		"orders", "cnpg", "transactional", "data", "velero", "crash", "15.0 GiB",
	} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected %q in the inventory, got:\n%s", expected, stripped)
		}
	}
}

// A scan that did not run is not "this stack runs no databases": saying so is
// the difference between a partial answer and a wrong one.
func TestStackDataListWarnsWhenTheLiveScanDidNotRun(t *testing.T) {
	inventory := sampleStackDataInventory()
	inventory.CustomResourceScan = client.CustomResourceScanUnavailable
	mock := newBackupLaneMock()
	mock.inventory = inventory

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksDataListCmd},
		"cluster", "stacks", "data", "list", backupTestStack, "--cluster", "demo")

	if executeError != nil {
		t.Fatalf("listing a stack's data: %v", executeError)
	}
	if !strings.Contains(output, "live database-operator read did not run") {
		t.Fatalf("an unavailable scan must be said out loud, got:\n%s", output)
	}
}

func TestStackDataListEmptySaysSo(t *testing.T) {
	mock := newBackupLaneMock()
	mock.inventory = &client.StackDataInventory{
		StackName: backupTestStack, CustomResourceScan: client.CustomResourceScanComplete,
	}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksDataListCmd},
		"cluster", "stacks", "data", "list", backupTestStack, "--cluster", "demo")

	if executeError != nil {
		t.Fatalf("listing a stack's data: %v", executeError)
	}
	if !strings.Contains(output, "holds no data a backup would carry") {
		t.Fatalf("an empty inventory must say so, got:\n%s", output)
	}
}

func TestStackDataListStructuredOutputStaysParseable(t *testing.T) {
	mock := newBackupLaneMock()
	mock.inventory = sampleStackDataInventory()

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksDataListCmd},
		"cluster", "stacks", "data", "list", backupTestStack, "--cluster", "demo", "-o", "json")

	if executeError != nil {
		t.Fatalf("listing a stack's data: %v", executeError)
	}
	var decoded client.StackDataInventory
	if decodeError := json.Unmarshal([]byte(output), &decoded); decodeError != nil {
		t.Fatalf("-o json must stay parseable, got %v for:\n%s", decodeError, output)
	}
	if len(decoded.Assets) != 2 || decoded.TotalRequestedBytes != 16106127360 {
		t.Fatalf("the inventory did not survive the structured output: %+v", decoded)
	}
}

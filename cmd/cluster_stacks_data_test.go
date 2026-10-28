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
				Attribution:    client.StackDataAttributionExclusiveNamespace,
			},
			{
				Kind: "cnpg_cluster", Namespace: "shop", Name: "orders",
				Engine: "cnpg", Consistency: "transactional", RequestedBytes: 10737418240,
				PersistentVolumeClaimNames: []string{"orders-1"},
				Member:                     &client.StackDataAssetMember{Kind: "addon", Name: "orders-db"},
				Attribution:                client.StackDataAttributionMember,
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
		"OWNED VIA", "member addon/orders-db", "namespace only this stack uses",
	} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected %q in the inventory, got:\n%s", expected, stripped)
		}
	}
	if strings.Contains(stripped, "Not owned by this stack") {
		t.Errorf("an inventory with nothing unattributed must not print the unattributed section, got:\n%s", stripped)
	}
}

func unattributedPlatformDatabase() client.StackDataAsset {
	return client.StackDataAsset{
		Kind: "cnpg_cluster", Namespace: "platform", Name: "platform-db",
		Engine: "cnpg", Consistency: "transactional", RequestedBytes: 1759218604441,
		HelmRelease: "platform", Attribution: client.StackDataAttributionUnattributed,
	}
}

// Data the namespace sweep found that no member owns (another stack's
// database in a shared namespace) is no longer in assets. Printing only
// assets would hide it silently; it has to be listed, and listed as outside
// every capture and restore (ankra-0xsdd.114).
func TestStackDataListShowsUnattributedDataAsNotOwned(t *testing.T) {
	inventory := sampleStackDataInventory()
	inventory.UnattributedAssets = []client.StackDataAsset{
		unattributedPlatformDatabase(),
		{
			Kind: "pvc", Namespace: "shop", Name: "orphan-claim", RequestedBytes: 1073741824,
			Attribution: client.StackDataAttributionUnattributed,
		},
	}
	mock := newBackupLaneMock()
	mock.inventory = inventory

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksDataListCmd},
		"cluster", "stacks", "data", "list", backupTestStack, "--cluster", "demo")

	if executeError != nil {
		t.Fatalf("listing a stack's data: %v", executeError)
	}
	stripped := stripANSICodes(output)
	sectionStart := strings.Index(stripped, "Not owned by this stack (unattributed)")
	if sectionStart < 0 {
		t.Fatalf("unattributed data must be listed under its own heading, got:\n%s", stripped)
	}
	section := stripped[sectionStart:]
	for _, expected := range []string{
		"never captured or restored", "HELM RELEASE",
		"platform-db", "platform", "1.6 TiB", "orphan-claim", "1.0 GiB",
	} {
		if !strings.Contains(section, expected) {
			t.Errorf("expected %q in the unattributed section, got:\n%s", expected, section)
		}
	}
	if strings.Contains(stripped[:sectionStart], "platform-db") {
		t.Errorf("unattributed data must not be listed among the stack's own assets, got:\n%s", stripped)
	}
}

// A stack that owns nothing can still share a namespace with data it does
// not own; the empty answer must not hide that data.
func TestStackDataListEmptyStillShowsUnattributedData(t *testing.T) {
	mock := newBackupLaneMock()
	mock.inventory = &client.StackDataInventory{
		StackName: backupTestStack, Namespaces: []string{"platform"},
		Assets:             []client.StackDataAsset{},
		UnattributedAssets: []client.StackDataAsset{unattributedPlatformDatabase()},
		CustomResourceScan: client.CustomResourceScanComplete,
	}

	output, executeError := runBackupCommand(t, mock, "",
		[]*cobra.Command{clusterStacksDataListCmd},
		"cluster", "stacks", "data", "list", backupTestStack, "--cluster", "demo")

	if executeError != nil {
		t.Fatalf("listing a stack's data: %v", executeError)
	}
	stripped := stripANSICodes(output)
	for _, expected := range []string{
		"holds no data a backup would carry", "Not owned by this stack (unattributed)", "platform-db",
	} {
		if !strings.Contains(stripped, expected) {
			t.Errorf("expected %q, got:\n%s", expected, stripped)
		}
	}
}

func TestStackDataListHelpSaysUnattributedDataIsNeverCaptured(t *testing.T) {
	help := strings.Join(strings.Fields(clusterStacksDataListCmd.Long), " ")
	if !strings.Contains(help, "Unattributed data is never captured or restored") {
		t.Fatalf("the help must say unattributed data is never captured or restored, got:\n%s",
			clusterStacksDataListCmd.Long)
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
	// Without the live read a database's ownership cannot be proven, so the
	// platform lists it as unattributed: the warning has to say an
	// unattributed database may still be this stack's.
	if !strings.Contains(output, "listed as not owned by this stack") {
		t.Fatalf("an unavailable scan must say unattributed data may be this stack's, got:\n%s", output)
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
	mock.inventory.UnattributedAssets = []client.StackDataAsset{unattributedPlatformDatabase()}

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
	if decoded.Assets[1].Attribution != client.StackDataAttributionMember {
		t.Fatalf("-o json must carry each asset's attribution, got %+v", decoded.Assets[1])
	}
	if len(decoded.UnattributedAssets) != 1 || decoded.UnattributedAssets[0].Name != "platform-db" ||
		decoded.UnattributedAssets[0].HelmRelease != "platform" {
		t.Fatalf("-o json must carry the unattributed assets, got %+v", decoded.UnattributedAssets)
	}
}

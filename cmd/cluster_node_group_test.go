package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ankra/internal/client"
)

func TestReadNodeGroupUserDataFileEmptyPathMeansUnset(t *testing.T) {
	userData, err := readNodeGroupUserDataFile("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userData != "" {
		t.Fatalf("userData = %q, want empty for an unset flag", userData)
	}
}

func TestReadNodeGroupUserDataFileLoadsTheDocumentVerbatim(t *testing.T) {
	document := "#cloud-config\ngrowpart:\n  mode: 'off'\n"
	path := filepath.Join(t.TempDir(), "user-data.yaml")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	userData, err := readNodeGroupUserDataFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userData != document {
		t.Fatalf("userData = %q, want the file content verbatim", userData)
	}
}

func TestReadNodeGroupUserDataFileErrorsOnMissingFile(t *testing.T) {
	_, err := readNodeGroupUserDataFile(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestReadNodeGroupUserDataFileRefusesOversizedDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.yaml")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxNodeGroupUserDataBytes+1)), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	_, err := readNodeGroupUserDataFile(path)
	if err == nil || !strings.Contains(err.Error(), "65535") {
		t.Fatalf("error = %v, want the size-cap refusal naming the limit", err)
	}
}

func TestNodeGroupZoneSuffixIsEmptyWithoutZonePlacement(t *testing.T) {
	// Every provider but OVH, and an OVH group created before zone
	// placement existed: the list line must read exactly as it always did.
	if suffix := nodeGroupZoneSuffix(client.NodeGroupInfo{Name: "default"}); suffix != "" {
		t.Fatalf("suffix = %q, want empty for a group with no zones", suffix)
	}
}

func TestNodeGroupZoneSuffixNamesTheZones(t *testing.T) {
	// The shape that matters: a group whose name promises one zone and
	// whose nodes are all in another has to be readable off the list line.
	drifted := client.NodeGroupInfo{Name: "general-par-a", AvailabilityZones: []string{"eu-west-par-c"}}
	if suffix := nodeGroupZoneSuffix(drifted); suffix != "  az=eu-west-par-c" {
		t.Fatalf("suffix = %q, want the single zone named", suffix)
	}

	spread := client.NodeGroupInfo{
		Name:              "ts",
		AvailabilityZones: []string{"eu-west-par-a", "eu-west-par-b", "eu-west-par-c"},
	}
	if suffix := nodeGroupZoneSuffix(spread); suffix != "  az=eu-west-par-a,eu-west-par-b,eu-west-par-c" {
		t.Fatalf("suffix = %q, want every zone of a spread group", suffix)
	}
}

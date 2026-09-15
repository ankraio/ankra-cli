package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallRecordRoundTrip(t *testing.T) {
	root := t.TempDir()

	options, found, err := RecordedInstallOptions(root, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if found || options != DefaultInstallOptions() {
		t.Fatalf("no record must read as not found with the defaults, got found=%v %+v", found, options)
	}

	custom := InstallOptions{Rules: false, Workflows: false, Hooks: true}
	if err := RecordInstallOptions(root, "claude-code", custom); err != nil {
		t.Fatal(err)
	}
	if err := RecordInstallOptions(root, "cursor", DefaultInstallOptions()); err != nil {
		t.Fatal(err)
	}
	options, found, err = RecordedInstallOptions(root, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if !found || options != custom {
		t.Fatalf("want %+v recorded for claude-code, got found=%v %+v", custom, found, options)
	}
	options, found, err = RecordedInstallOptions(root, "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if !found || options != DefaultInstallOptions() {
		t.Fatalf("recording one client must not disturb another, got found=%v %+v", found, options)
	}

	if err := ForgetInstallOptions(root, "claude-code"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := RecordedInstallOptions(root, "claude-code"); found {
		t.Fatal("a forgotten client must read as not found")
	}
	if err := ForgetInstallOptions(root, "never-installed"); err != nil {
		t.Fatalf("forgetting a client with no record must be a no-op, got %v", err)
	}
	if err := ForgetInstallOptions(root, "cursor"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(InstallRecordPath(root)); !os.IsNotExist(err) {
		t.Fatalf("an empty record must not leave a file behind, stat: %v", err)
	}
}

func TestInstallRecordRejectsMalformedFile(t *testing.T) {
	root := t.TempDir()
	path := InstallRecordPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecordedInstallOptions(root, "claude-code"); err == nil {
		t.Fatal("a malformed record must be an error, not \"nothing recorded\"")
	}
}

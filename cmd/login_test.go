package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureSecureConfigFileCreatesWithRestrictedPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission semantics differ on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".ankra.yaml")

	if err := ensureSecureConfigFile(path); err != nil {
		t.Fatalf("ensureSecureConfigFile returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected mode 0600, got %#o", mode)
	}
}

func TestEnsureSecureConfigFileTightensLoosePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission semantics differ on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".ankra.yaml")
	if err := os.WriteFile(path, []byte("token: leaked\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := ensureSecureConfigFile(path); err != nil {
		t.Fatalf("ensureSecureConfigFile returned error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected mode tightened to 0600, got %#o", mode)
	}
}

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveSafePathAcceptsCleanRelativePaths(t *testing.T) {
	base := t.TempDir()
	resolved, err := resolveSafePath(base, "manifests/app.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(base, "manifests", "app.yaml")
	if resolved != expected {
		t.Errorf("got %q want %q", resolved, expected)
	}
}

func TestResolveSafePathCleansDotSegments(t *testing.T) {
	base := t.TempDir()
	resolved, err := resolveSafePath(base, "./manifests/./app.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(resolved, filepath.Join("manifests", "app.yaml")) {
		t.Errorf("expected cleaned path, got %q", resolved)
	}
}

func TestResolveSafePathRejectsTraversal(t *testing.T) {
	base := t.TempDir()
	cases := []string{
		"../etc/passwd",
		"manifests/../../etc/passwd",
		"..",
		"./..",
		"manifests/../..",
	}
	for _, relPath := range cases {
		t.Run(relPath, func(t *testing.T) {
			if _, err := resolveSafePath(base, relPath); err == nil {
				t.Fatalf("expected error for %q, got nil", relPath)
			} else if !errors.Is(err, errSafePathEscape) {
				t.Errorf("expected errSafePathEscape, got %v", err)
			}
		})
	}
}

func TestResolveSafePathRejectsAbsoluteAndEmpty(t *testing.T) {
	base := t.TempDir()
	cases := []string{
		"",
		"/etc/passwd",
		"/root/.ssh/authorized_keys",
		"C:\\Windows\\system32\\evil.dll",
	}
	for _, relPath := range cases {
		t.Run(relPath, func(t *testing.T) {
			if _, err := resolveSafePath(base, relPath); err == nil {
				t.Fatalf("expected error for %q, got nil", relPath)
			}
		})
	}
}

func TestResolveSafePathDetectsSymlinkParentEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation unreliable on default Windows runners")
	}
	base := t.TempDir()
	outside := t.TempDir()

	// Build base/escape -> outside, then try writing base/escape/file.yaml.
	if err := os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	_, err := resolveSafePath(base, "escape/file.yaml")
	if err == nil {
		t.Fatalf("expected symlinked-parent escape to be rejected")
	}
	if !errors.Is(err, errSafePathEscape) {
		t.Errorf("expected errSafePathEscape, got %v", err)
	}
}

func TestSafeRelURLPathRejectsTraversal(t *testing.T) {
	cases := []string{
		"",
		"/etc/passwd",
		"..",
		"foo/../../etc/passwd",
	}
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			if _, err := safeRelURLPath(value); err == nil {
				t.Fatalf("expected error for %q", value)
			}
		})
	}
}

func TestSafeRelURLPathAccepts(t *testing.T) {
	got, err := safeRelURLPath("manifests/app.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "manifests/app.yaml" {
		t.Errorf("got %q", got)
	}
}

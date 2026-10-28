package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withForceFlag sets forceFlag for the duration of a test.
func withForceFlag(t *testing.T, value bool) {
	t.Helper()
	original := forceFlag
	forceFlag = value
	t.Cleanup(func() { forceFlag = original })
}

func TestCopyFileRejectsTraversal(t *testing.T) {
	srcBase := t.TempDir()
	dstBase := t.TempDir()
	withForceFlag(t, false)

	cases := []string{
		"../escape.yaml",
		"manifests/../../escape.yaml",
		"/etc/passwd",
		"./../escape.yaml",
	}
	for _, relPath := range cases {
		t.Run(relPath, func(t *testing.T) {
			err := copyFile(srcBase, dstBase, relPath)
			if err == nil {
				t.Fatalf("expected error for %q", relPath)
			}
			if !errors.Is(err, errSafePathEscape) {
				t.Errorf("expected errSafePathEscape, got %v", err)
			}
		})
	}
}

func TestCopyStackFilesRejectsAddonFromFileEscape(t *testing.T) {
	srcBase := t.TempDir()
	dstBase := t.TempDir()
	withForceFlag(t, false)

	if err := os.MkdirAll(srcBase, 0o755); err != nil {
		t.Fatal(err)
	}

	stack := StackConfig{
		Addons: []AddonConfig{
			{
				Configuration: map[string]interface{}{
					"from_file": "../../etc/passwd",
				},
			},
		},
	}
	err := copyStackFiles(stack, srcBase, dstBase, false, false)
	if err == nil {
		t.Fatalf("expected addon configuration traversal to be rejected")
	}
	if !errors.Is(err, errSafePathEscape) {
		t.Errorf("expected errSafePathEscape, got %v", err)
	}
}

func TestCopyStackFilesRejectsManifestEscape(t *testing.T) {
	srcBase := t.TempDir()
	dstBase := t.TempDir()
	withForceFlag(t, false)

	stack := StackConfig{
		Manifests: []ManifestConfig{
			{Name: "evil", FromFile: "../escape.yaml"},
		},
	}
	err := copyStackFiles(stack, srcBase, dstBase, false, false)
	if err == nil {
		t.Fatalf("expected manifest traversal to be rejected")
	}
	if !errors.Is(err, errSafePathEscape) {
		t.Errorf("expected errSafePathEscape, got %v", err)
	}
}

func TestCopyStackFilesRejectsDescriptionFromFileEscape(t *testing.T) {
	srcBase := t.TempDir()
	dstBase := t.TempDir()
	withForceFlag(t, false)

	stack := StackConfig{
		DescriptionFromFile: "../README.md",
	}
	err := copyStackFiles(stack, srcBase, dstBase, false, false)
	if err == nil {
		t.Fatalf("expected description_from_file traversal to be rejected")
	}
	if !errors.Is(err, errSafePathEscape) {
		t.Errorf("expected errSafePathEscape, got %v", err)
	}
}

func TestOpenDestinationFileRefusesExistingFileWithoutForce(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "existing.yaml")
	if err := os.WriteFile(target, []byte("pre-existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	withForceFlag(t, false)
	if _, err := openDestinationFile(target); err == nil {
		t.Fatalf("expected refusal when file already exists and --force is not set")
	}

	withForceFlag(t, true)
	f, err := openDestinationFile(target)
	if err != nil {
		t.Fatalf("force should permit overwrite: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeRelURLPathMatchesShape(t *testing.T) {
	if _, err := safeRelURLPath("manifests/app.yaml"); err != nil {
		t.Fatalf("relative path should be accepted: %v", err)
	}
	for _, evil := range []string{"../foo.yaml", "/etc/passwd", "", ".."} {
		t.Run(evil, func(t *testing.T) {
			if _, err := safeRelURLPath(evil); err == nil {
				t.Fatalf("expected rejection for %q", evil)
			}
		})
	}
}

func TestEnsureSafeDownloadURL(t *testing.T) {
	t.Setenv("ANKRA_ALLOW_INSECURE_HTTP", "")
	cases := []struct {
		name      string
		input     string
		expectErr bool
	}{
		{"https public", "https://github.com/user/repo/raw/main/cluster.yaml", false},
		{"http public", "http://example.com/cluster.yaml", true},
		{"http localhost", "http://localhost:8080/cluster.yaml", false},
		{"http 127.0.0.1", "http://127.0.0.1:8080/cluster.yaml", false},
		{"http ipv6 loopback", "http://[::1]:8080/cluster.yaml", false},
		{"ftp scheme", "ftp://example.com/cluster.yaml", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ensureSafeDownloadURL(tc.input)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.input)
				}
			} else if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
		})
	}
}

func TestEnsureSafeDownloadURLInsecureOverride(t *testing.T) {
	t.Setenv("ANKRA_ALLOW_INSECURE_HTTP", "1")
	if err := ensureSafeDownloadURL("http://internal.example.lan/cluster.yaml"); err != nil {
		t.Fatalf("override should permit plaintext, got %v", err)
	}
}

func TestResolveSafePathBaseSlashCornerCase(t *testing.T) {
	// Defensive: a relative path that lexically looks safe must not break
	// when baseDir has a trailing separator or current-directory references.
	base := t.TempDir()
	cases := map[string]string{
		"manifests/app.yaml":   filepath.Join(base, "manifests", "app.yaml"),
		"./manifests/app.yaml": filepath.Join(base, "manifests", "app.yaml"),
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			got, err := resolveSafePath(strings.TrimRight(base, string(os.PathSeparator))+string(os.PathSeparator), input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != want {
				t.Errorf("got %q want %q", got, want)
			}
		})
	}
}

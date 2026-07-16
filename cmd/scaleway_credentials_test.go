package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteGeneratedPrivateKeyCreatesExclusive0600File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_ed25519")
	want := []byte("PRIVATE KEY\n")
	if err := writeGeneratedPrivateKey(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want regular 0600", info.Mode())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWriteGeneratedPrivateKeyRefusesExistingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(path, []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}
	err := writeGeneratedPrivateKey(path, []byte("replace"))
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("error = %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "keep" {
		t.Fatalf("existing file changed to %q", got)
	}
}

func TestWriteGeneratedPrivateKeyRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "private-key")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := writeGeneratedPrivateKey(link, []byte("replace"))
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("error = %v", err)
	}
	info, statErr := os.Lstat(link)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was replaced: mode %v", info.Mode())
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "keep" {
		t.Fatalf("symlink target changed to %q", got)
	}
}

var errInjectedPrivateKeyWrite = errors.New("injected private-key write failure")

type failingPrivateKeyFile struct {
	*os.File
}

func (file *failingPrivateKeyFile) Write(data []byte) (int, error) {
	count := 1
	if len(data) == 0 {
		count = 0
	}
	if count > 0 {
		written, _ := file.File.Write(data[:count])
		return written, errInjectedPrivateKeyWrite
	}
	return 0, errInjectedPrivateKeyWrite
}

func TestWriteGeneratedPrivateKeyDeletesPartialOnWriteFailure(t *testing.T) {
	originalOpen := openPrivateKeyOutput
	openPrivateKeyOutput = func(path string, flag int, permission os.FileMode) (privateKeyOutputFile, error) {
		file, err := os.OpenFile(path, flag, permission)
		if err != nil {
			return nil, err
		}
		return &failingPrivateKeyFile{File: file}, nil
	}
	t.Cleanup(func() { openPrivateKeyOutput = originalOpen })

	path := filepath.Join(t.TempDir(), "partial")
	err := writeGeneratedPrivateKey(path, []byte("secret"))
	if !errors.Is(err, errInjectedPrivateKeyWrite) {
		t.Fatalf("error = %v, want injected write failure", err)
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial file remains: %v", statErr)
	}
}

package skills

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The Claude app cannot read the machine it is talking to, so a directory of
// SKILL.md files is not a delivery mechanism for it. What it does take is an
// uploaded skill bundle: one zip whose single top-level directory holds the
// SKILL.md and anything it references. Packaging writes those bundles to disk
// so the upload is a file picker away.

// PackageBundles writes one .zip per selected skill into destDir, each
// containing the skill directory at its root. Existing bundles are replaced
// unless force is false and the file already exists, matching Install's
// skip-unless-forced behaviour. Returns the bundles written and skipped.
func PackageBundles(fsys fs.FS, destDir string, names []string, force bool) (written, skipped []string, err error) {
	available, err := Names(fsys)
	if err != nil {
		return nil, nil, err
	}
	selected, err := selectNames(available, names)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, nil, err
	}
	for _, name := range selected {
		destination := filepath.Join(destDir, name+".zip")
		if _, statError := os.Stat(destination); statError == nil && !force {
			skipped = append(skipped, name)
			continue
		}
		archive, buildError := buildBundle(fsys, name)
		if buildError != nil {
			return written, skipped, buildError
		}
		if writeError := os.WriteFile(destination, archive, 0o644); writeError != nil {
			return written, skipped, writeError
		}
		written = append(written, name)
	}
	return written, skipped, nil
}

// RemoveBundles deletes the .zip bundles for the named skills from destDir,
// returning how many existed.
func RemoveBundles(destDir string, names []string) (int, error) {
	removed := 0
	for _, name := range names {
		destination := filepath.Join(destDir, name+".zip")
		if _, err := os.Stat(destination); err != nil {
			continue
		}
		if err := os.Remove(destination); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// buildBundle zips one skill directory, keeping the skill name as the
// archive's single top-level directory.
func buildBundle(fsys fs.FS, name string) ([]byte, error) {
	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)
	walkError := fs.WalkDir(fsys, name, func(entryPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, readError := fs.ReadFile(fsys, entryPath)
		if readError != nil {
			return readError
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(entryPath, name), "/")
		file, createError := archive.Create(path.Join(name, relative))
		if createError != nil {
			return createError
		}
		_, writeError := file.Write(data)
		return writeError
	})
	if walkError != nil {
		return nil, walkError
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

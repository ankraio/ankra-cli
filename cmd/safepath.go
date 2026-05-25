package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errSafePathEscape indicates a relative path attempted to escape the
// trusted base directory (via absolute path, `..`, or a symlinked parent
// that points outside the base).
var errSafePathEscape = errors.New("path escapes the trusted base directory")

// resolveSafePath validates that relPath is a clean relative path inside
// baseDir. It returns the absolute joined path, or an error.
//
// Rules:
//   - relPath must be non-empty and must not equal "." or "..".
//   - relPath must not be an absolute path (POSIX or Windows).
//   - After cleaning, relPath must not contain any ".." segment.
//   - The lexically resolved path must remain under the cleaned baseDir.
//   - If the parent directory of the leaf already exists on disk, its
//     symlink-resolved real path must also be within baseDir (after the
//     baseDir itself is symlink-resolved). The leaf itself may not yet
//     exist; that is the common case for clone/copy destinations.
//
// The function does not create any files or directories.
func resolveSafePath(baseDir, relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("relative path is empty: %w", errSafePathEscape)
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("absolute path %q: %w", relPath, errSafePathEscape)
	}
	// Reject Windows-style absolute paths even on POSIX builds so a
	// cross-platform-distributed YAML can't smuggle "C:\..." through.
	if len(relPath) >= 2 && relPath[1] == ':' {
		return "", fmt.Errorf("drive-prefixed path %q: %w", relPath, errSafePathEscape)
	}

	cleaned := filepath.Clean(relPath)
	if cleaned == "." || cleaned == ".." {
		return "", fmt.Errorf("path %q resolves outside base: %w", relPath, errSafePathEscape)
	}
	for _, segment := range strings.Split(cleaned, string(os.PathSeparator)) {
		if segment == ".." {
			return "", fmt.Errorf("path %q contains '..' traversal: %w", relPath, errSafePathEscape)
		}
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve base dir %q: %w", baseDir, err)
	}
	absBase = filepath.Clean(absBase)
	joined := filepath.Clean(filepath.Join(absBase, cleaned))

	if !pathHasPrefix(joined, absBase) {
		return "", fmt.Errorf("path %q resolves outside base %q: %w", relPath, absBase, errSafePathEscape)
	}

	// For writes, the leaf typically does not exist yet. Validate the
	// existing parent chain (if any) by walking up until we find a directory
	// that exists, then resolving symlinks to ensure the real parent is
	// still inside the real base.
	if err := verifyParentChainInsideBase(absBase, joined); err != nil {
		return "", err
	}

	return joined, nil
}

// pathHasPrefix reports whether path is equal to prefix, or is a child of
// prefix. It expects both paths to be cleaned/absolute.
func pathHasPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	separator := string(os.PathSeparator)
	prefixWithSeparator := strings.TrimRight(prefix, separator) + separator
	return strings.HasPrefix(path, prefixWithSeparator)
}

// verifyParentChainInsideBase resolves the closest existing ancestor of
// joined (which may itself be joined if it exists) to its real on-disk
// path, then verifies that this real path is still inside the symlink-
// resolved base. This catches the case where a parent directory of the
// destination is a symlink that points outside baseDir.
func verifyParentChainInsideBase(absBase, joined string) error {
	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("resolve base %q: %w", absBase, err)
		}
		// If the base itself does not exist, we can only rely on the
		// lexical check that was already performed.
		return nil
	}

	current := joined
	for {
		if _, err := os.Lstat(current); err == nil {
			realCurrent, evalErr := filepath.EvalSymlinks(current)
			if evalErr != nil {
				return fmt.Errorf("resolve symlinks for %q: %w", current, evalErr)
			}
			if !pathHasPrefix(realCurrent, realBase) {
				return fmt.Errorf(
					"resolved path %q escapes base %q: %w",
					realCurrent, realBase, errSafePathEscape)
			}
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %q: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// safeRelURLPath validates a relative path destined for use in an HTTP URL
// fetch. It enforces the same shape rules as resolveSafePath (no absolute,
// no ".."), but does not touch the filesystem. The returned value uses
// forward slashes so it can be safely appended to a base URL.
func safeRelURLPath(relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("relative path is empty: %w", errSafePathEscape)
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("absolute path %q: %w", relPath, errSafePathEscape)
	}
	if strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, "\\") {
		return "", fmt.Errorf("absolute path %q: %w", relPath, errSafePathEscape)
	}
	cleaned := filepath.ToSlash(filepath.Clean(relPath))
	if cleaned == "." || cleaned == ".." {
		return "", fmt.Errorf("path %q resolves outside base: %w", relPath, errSafePathEscape)
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == ".." {
			return "", fmt.Errorf("path %q contains '..' traversal: %w", relPath, errSafePathEscape)
		}
	}
	return cleaned, nil
}

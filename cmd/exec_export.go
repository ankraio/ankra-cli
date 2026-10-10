package cmd

// What `ankra exec` brings back after a run: the patch an --apply run made,
// the directories --fetch names, and the cheap --check answer the guards ask
// for on every classified command.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" //nolint:gosec // a cache file name, not a security boundary
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"ankra/internal/client"
)

// applyPatch fetches the changes an --apply run made and applies them to the
// worktree. A patch that no longer applies is kept under the git dir. It
// answers false when the changes did not reach the worktree.
func (runner *execRunner) applyPatch(ctx context.Context, runID string) bool {
	export, exportError := apiClient.CreateWorkspaceRunExport(ctx, runner.workspace.ID, runID,
		client.WorkspaceRunExportRequest{Kind: "patch"})
	if exportError != nil {
		runner.say("could not fetch the changes from the workspace: %v", exportError)
		return false
	}
	if export.Empty || export.DownloadURL == "" {
		return true
	}
	var patch bytes.Buffer
	if downloadError := apiClient.DownloadPresigned(ctx, export.DownloadURL, &patch); downloadError != nil {
		runner.say("could not fetch the changes from the workspace: %v", downloadError)
		return false
	}
	if patch.Len() == 0 {
		return true
	}
	patchPath := filepath.Join(runner.temporaryDir, "patch")
	if writeError := os.WriteFile(patchPath, patch.Bytes(), 0o600); writeError != nil {
		runner.say("could not store the changes: %v", writeError)
		return false
	}
	if _, applyError := gitStdout(ctx, runner.snapshot.Top, nil, "apply", "--whitespace=nowarn", patchPath); applyError != nil {
		gitDirectory, gitDirectoryError := gitStdout(ctx, runner.snapshot.Top, nil, "rev-parse",
			"--path-format=absolute", "--git-dir")
		if gitDirectoryError != nil {
			gitDirectory = runner.temporaryDir
		}
		keep := filepath.Join(gitDirectory, "ankra-exec-unapplied-"+safeFileComponent(runID)+".patch")
		if keepError := os.WriteFile(keep, patch.Bytes(), 0o600); keepError != nil {
			runner.say("the changes no longer apply here, and could not be kept: %v", keepError)
			return false
		}
		runner.say("the changes no longer apply here (did the files change meanwhile?): %v", applyError)
		runner.say("kept them in %s (git apply --3way it, or re-run).", keep)
		return false
	}
	runner.say("applied the workspace's changes (%d file(s))", bytes.Count(patch.Bytes(), []byte("\ndiff --git "))+
		boolToInt(bytes.HasPrefix(patch.Bytes(), []byte("diff --git "))))
	return true
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// safeFileComponent keeps a run id usable as part of a file name.
func safeFileComponent(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('-')
		}
	}
	return builder.String()
}

// fetchDirectoryUsable reports whether a --fetch directory stays inside the
// run's directory: relative, no "..".
func fetchDirectoryUsable(directory string) bool {
	if directory == "" || filepath.IsAbs(directory) || strings.HasPrefix(directory, "-") {
		return false
	}
	for _, segment := range strings.Split(filepath.ToSlash(directory), "/") {
		if segment == ".." {
			return false
		}
	}
	cleaned := filepath.Clean(directory)
	return cleaned != "." && cleaned != ""
}

// fetchBack copies the --fetch directories back from the workspace after the
// run, replacing the local ones, whatever the exit code. Failures are said,
// never fatal: the run's own answer stands.
func (runner *execRunner) fetchBack(ctx context.Context, runID string) {
	if len(runner.fetchDirectories) == 0 {
		return
	}
	var directories []string
	for _, directory := range runner.fetchDirectories {
		if !fetchDirectoryUsable(directory) {
			runner.say("not fetching %q: it must be a relative path inside the worktree", directory)
			continue
		}
		directories = append(directories, filepath.ToSlash(filepath.Clean(directory)))
	}
	if len(directories) == 0 {
		return
	}
	export, exportError := apiClient.CreateWorkspaceRunExport(ctx, runner.workspace.ID, runID,
		client.WorkspaceRunExportRequest{Kind: "dirs", Paths: directories})
	if exportError != nil {
		runner.say("could not copy back %s: %v", strings.Join(directories, " "), exportError)
		return
	}
	if export.Empty || export.DownloadURL == "" {
		return
	}
	archivePath := filepath.Join(runner.temporaryDir, "fetch.tgz")
	archive, createError := os.Create(archivePath)
	if createError != nil {
		runner.say("could not copy back %s: %v", strings.Join(directories, " "), createError)
		return
	}
	downloadError := apiClient.DownloadPresigned(ctx, export.DownloadURL, archive)
	closeError := archive.Close()
	if downloadError != nil || closeError != nil {
		runner.say("could not copy back %s: %v", strings.Join(directories, " "), errors.Join(downloadError, closeError))
		return
	}
	workingDirectory, workingDirectoryError := os.Getwd()
	if workingDirectoryError != nil {
		runner.say("could not copy back %s: %v", strings.Join(directories, " "), workingDirectoryError)
		return
	}
	// Extract into a staging directory first and swap each directory in only
	// once the whole archive extracted: a corrupt or truncated download keeps
	// the local results. The staging directory sits in the git dir, on the
	// worktree's filesystem (so the swap is a rename) and outside the tree.
	stagingParent, gitDirectoryError := gitStdout(ctx, runner.snapshot.Top, nil, "rev-parse",
		"--path-format=absolute", "--git-dir")
	if gitDirectoryError != nil || stagingParent == "" {
		stagingParent = workingDirectory
	}
	staging, stagingError := os.MkdirTemp(stagingParent, "ankra-exec-fetch-")
	if stagingError != nil {
		runner.say("could not copy back %s: %v", strings.Join(directories, " "), stagingError)
		return
	}
	defer func() { _ = os.RemoveAll(staging) }()
	skipped, extractError := extractExecArchive(archivePath, staging, directories)
	if extractError != nil {
		runner.say("could not copy back %s (the local ones are unchanged): %v", strings.Join(directories, " "),
			extractError)
		return
	}
	for _, directory := range directories {
		target, resolveError := resolveSafePath(workingDirectory, directory)
		if resolveError != nil {
			runner.say("not replacing %s: %v", directory, resolveError)
			continue
		}
		if removeError := os.RemoveAll(target); removeError != nil {
			runner.say("could not replace %s: %v", directory, removeError)
			continue
		}
		staged := filepath.Join(staging, filepath.FromSlash(directory))
		if _, statError := os.Lstat(staged); statError != nil {
			// The workspace had none: the local one is replaced by nothing.
			continue
		}
		if mkdirError := os.MkdirAll(filepath.Dir(target), 0o755); mkdirError != nil {
			runner.say("could not replace %s: %v", directory, mkdirError)
			continue
		}
		if renameError := os.Rename(staged, target); renameError != nil {
			runner.say("could not replace %s: %v", directory, renameError)
		}
	}
	if skipped > 0 {
		runner.say("skipped %d archive entr(ies) that were links or outside %s", skipped, strings.Join(directories, " "))
	}
	runner.say("copied back %s (whatever existed)", strings.Join(directories, " "))
}

// extractExecArchive extracts a gzipped tar into base, keeping only regular
// files and directories that resolve inside base and under one of the
// requested directories. Links and anything else are skipped and counted.
func extractExecArchive(archivePath string, base string, directories []string) (int, error) {
	archive, openError := os.Open(archivePath)
	if openError != nil {
		return 0, openError
	}
	defer func() { _ = archive.Close() }()
	decompressed, gzipError := gzip.NewReader(archive)
	if gzipError != nil {
		return 0, fmt.Errorf("not a gzipped tar: %w", gzipError)
	}
	defer func() { _ = decompressed.Close() }()
	reader := tar.NewReader(decompressed)
	skipped := 0
	for {
		header, nextError := reader.Next()
		if errors.Is(nextError, io.EOF) {
			return skipped, nil
		}
		if nextError != nil {
			return skipped, nextError
		}
		name := strings.TrimPrefix(filepath.ToSlash(header.Name), "./")
		if !underFetchDirectory(name, directories) {
			skipped++
			continue
		}
		target, resolveError := resolveSafePath(base, filepath.FromSlash(name))
		if resolveError != nil {
			skipped++
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if mkdirError := os.MkdirAll(target, 0o755); mkdirError != nil {
				return skipped, mkdirError
			}
		case tar.TypeReg:
			if mkdirError := os.MkdirAll(filepath.Dir(target), 0o755); mkdirError != nil {
				return skipped, mkdirError
			}
			mode := os.FileMode(header.Mode).Perm() | 0o600
			file, createError := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if createError != nil {
				return skipped, createError
			}
			_, copyError := io.Copy(file, reader) //nolint:gosec // the archive is the caller's own run output
			closeError := file.Close()
			if copyError != nil || closeError != nil {
				return skipped, errors.Join(copyError, closeError)
			}
		default:
			skipped++
		}
	}
}

// underFetchDirectory reports whether an archive entry is one of the
// requested directories or inside one.
func underFetchDirectory(name string, directories []string) bool {
	cleaned := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(filepath.FromSlash(name))), "/")
	for _, directory := range directories {
		if cleaned == directory || strings.HasPrefix(cleaned, directory+"/") {
			return true
		}
	}
	return false
}

// check answers --check: 0 when the checkout's repository is connected to
// Ankra Pipelines and the caller may use workspaces, 1 otherwise. The answer
// is cached (an hour when yes, ten minutes when no), since the guards ask on
// every classified command.
func (runner *execRunner) check(ctx context.Context) int {
	checkout, checkoutError := inspectWorkspaceCheckout(ctx, ".")
	if checkoutError != nil {
		return 1
	}
	cachePath := execCheckCachePath(runner.cmd, checkout.Remote)
	if cachePath != "" {
		if answer, isFresh := readExecCheckCache(cachePath, execNow()); isFresh {
			return answer
		}
	}
	if clientError := ensureExecClient(runner.cmd); clientError != nil {
		return 1
	}
	answer, isKnown := runner.checkRemotely(ctx, checkout)
	if isKnown && cachePath != "" {
		writeExecCheckCache(cachePath, answer, execNow())
	}
	return answer
}

// checkRemotely asks the platform. isKnown is false when it could not be
// asked, which is not cached.
func (runner *execRunner) checkRemotely(ctx context.Context, checkout workspaceCheckout) (int, bool) {
	_, isFound, findError := findPipelineRepository(ctx, checkout.Remote)
	if findError != nil {
		return 1, isDefinitiveRefusal(findError)
	}
	if !isFound {
		return 1, true
	}
	// The probe is for the workspaces.use permission, which is the same for
	// every kind (any kind resolves to an image server-side), so it names no
	// kind and the cached answer holds for all of them.
	if _, listError := apiClient.ListWorkspaces(ctx, client.ListWorkspacesOptions{}); listError != nil {
		return 1, isDefinitiveRefusal(listError)
	}
	return 0, true
}

// isDefinitiveRefusal reports whether an error is the platform's answer (a
// refusal worth caching) rather than a failure to reach it.
func isDefinitiveRefusal(err error) bool {
	var permissionDenied *client.PermissionDeniedError
	var workspaceError *client.WorkspaceAPIError
	switch {
	case errors.As(err, &permissionDenied):
		return true
	case errors.As(err, &workspaceError):
		return workspaceError.StatusCode == 403
	}
	return false
}

// execCheckCachePath is the --check cache file for a repository under the
// platform and organisation this invocation addresses. It is read before any
// network call, so it keys on what was asked for (the base URL, the --org,
// ANKRA_ORG and ANKRA_EXEC_ORG as given), not on what they resolve to.
func execCheckCachePath(cmd *cobra.Command, remote repositoryRemote) string {
	cacheDirectory, cacheError := os.UserCacheDir()
	if cacheError != nil {
		return ""
	}
	platform := baseURL
	if platform == "" {
		if resolved, resolveError := resolveCredentials(cmd); resolveError == nil {
			platform = resolved.baseURL
		}
	}
	orgFlag, _ := flagValue(cmd.Root().PersistentFlags().Lookup("org"))
	key := strings.Join([]string{platform, orgFlag, os.Getenv(envAnkraOrg), os.Getenv("ANKRA_EXEC_ORG"),
		remote.Provider, strings.ToLower(remote.Owner), strings.ToLower(remote.Name)}, "\x00")
	digest := sha1.Sum([]byte(key)) //nolint:gosec // see the import
	return filepath.Join(cacheDirectory, "ankra", "exec-check", hex.EncodeToString(digest[:]))
}

func readExecCheckCache(path string, now time.Time) (int, bool) {
	contents, readError := os.ReadFile(path)
	if readError != nil {
		return 0, false
	}
	verdict, stampText, isFound := strings.Cut(strings.TrimSpace(string(contents)), " ")
	if !isFound {
		return 0, false
	}
	stamp, parseError := strconv.ParseInt(stampText, 10, 64)
	if parseError != nil {
		return 0, false
	}
	age := now.Sub(time.Unix(stamp, 0))
	switch verdict {
	case "ok":
		return 0, age >= 0 && age < execCheckPositiveTTL
	case "no":
		return 1, age >= 0 && age < execCheckNegativeTTL
	}
	return 0, false
}

func writeExecCheckCache(path string, answer int, now time.Time) {
	verdict := "no"
	if answer == 0 {
		verdict = "ok"
	}
	if mkdirError := os.MkdirAll(filepath.Dir(path), 0o700); mkdirError != nil {
		return
	}
	_ = os.WriteFile(path, []byte(verdict+" "+strconv.FormatInt(now.Unix(), 10)+"\n"), 0o600)
}

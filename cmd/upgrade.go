package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	githubLatestReleaseAPI = "https://api.github.com/repos/ankraio/ankra-cli/releases/latest"
	githubReleasesListAPI  = "https://api.github.com/repos/ankraio/ankra-cli/releases?per_page=30"
	githubReleaseDownload  = "https://github.com/ankraio/ankra-cli/releases/download"
	installScriptURL       = "https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh"
	upgradeHTTPTimeout     = 120 * time.Second
	checksumReadLimit      = 4 << 10
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the Ankra CLI to the latest release",
	Long: `Download and install a specific Ankra CLI release, replacing the
currently running binary in place.

By default the command upgrades to the latest stable release. Install an
exact release with --version (for example --version v0.2.5, or --version
0.2.5). When a version is pinned the command installs it whether it is
newer, older or the same as the running binary, so --version doubles as a
downgrade: 'ankra upgrade --version v0.1.9' rolls back to that release.
Use --check to report whether a newer release is available without
installing anything.

When the beta channel is enabled (see 'ankra config beta enable') the
newest release including pre-releases (release candidates such as
v0.3.0-rc.1) is installed instead. Use --beta or --beta=false to override
the saved channel for a single run.

The downloaded binary is verified against its published SHA-256 checksum
before it replaces the existing executable.`,
	Aliases: []string{"self-update"},
	Args:    cobra.NoArgs,
	RunE:    runUpgrade,
}

func init() {
	upgradeCmd.Flags().String("version", "", "exact release to install for an upgrade or downgrade, e.g. v0.2.5 (default: latest)")
	upgradeCmd.Flags().Bool("check", false, "check for a newer release without installing")
	upgradeCmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt")
	upgradeCmd.Flags().Bool("force", false, "reinstall even if already on the target version")
	upgradeCmd.Flags().Bool("beta", false, "include pre-release versions for this run (overrides the saved channel)")
	upgradeCmd.Flags().Bool("allow-unverified", false, "install even when no SHA-256 checksum is published (insecure)")

	setRequiresAuth(upgradeCmd, false)
	rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	pinnedVersion, _ := cmd.Flags().GetString("version")
	checkOnly, _ := cmd.Flags().GetBool("check")
	skipConfirm, _ := cmd.Flags().GetBool("yes")
	force, _ := cmd.Flags().GetBool("force")
	allowUnverified, _ := cmd.Flags().GetBool("allow-unverified")

	assetName, err := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	httpClient := &http.Client{Timeout: upgradeHTTPTimeout}

	betaEnabled := betaReleasesEnabled()
	if cmd.Flags().Changed("beta") {
		betaEnabled, _ = cmd.Flags().GetBool("beta")
	}

	versionPinned := cmd.Flags().Changed("version")
	targetTag := strings.TrimSpace(pinnedVersion)
	if targetTag == "" {
		var latestTag string
		var latestErr error
		if betaEnabled {
			_, _ = fmt.Fprintln(out, "Beta channel enabled: including pre-release versions.")
			latestTag, latestErr = latestReleaseTagIncludingPrereleases(httpClient)
		} else {
			latestTag, latestErr = latestReleaseTag(httpClient)
		}
		if latestErr != nil {
			return fmt.Errorf("could not determine the latest release: %w", latestErr)
		}
		targetTag = latestTag
	} else {
		targetTag = ensureTagPrefix(targetTag)
	}

	currentVersion := normalizeVersion(version)
	targetVersion := normalizeVersion(targetTag)
	comparison := compareVersions(targetVersion, currentVersion)

	if checkOnly {
		switch {
		case comparison > 0:
			_, _ = fmt.Fprintf(out, "A newer release is available: v%s (current: v%s)\n", targetVersion, currentVersion)
			_, _ = fmt.Fprintln(out, "Run `ankra upgrade` to install it.")
		case comparison < 0:
			_, _ = fmt.Fprintf(out, "Installed version v%s is newer than v%s.\n", currentVersion, targetVersion)
		default:
			_, _ = fmt.Fprintf(out, "Already up to date (v%s).\n", currentVersion)
		}
		return nil
	}

	if comparison == 0 && !force && !versionPinned {
		_, _ = fmt.Fprintf(out, "Already up to date (v%s).\n", currentVersion)
		return nil
	}
	if comparison < 0 && !force && !versionPinned {
		_, _ = fmt.Fprintf(out,
			"Installed version v%s is newer than the requested v%s. "+
				"Pin it with --version v%s to downgrade, or use --force.\n",
			currentVersion, targetVersion, targetVersion)
		return nil
	}

	executablePath, err := currentExecutablePath()
	if err != nil {
		return err
	}
	if isHomebrewManagedPath(executablePath) {
		return fmt.Errorf(
			"this ankra binary is managed by Homebrew (%s).\n"+
				"Self-updating it would be reverted by the next `brew upgrade`. Upgrade with:\n"+
				"  brew update && brew upgrade ankra",
			executablePath)
	}

	action := "Upgrade"
	switch {
	case comparison < 0:
		action = "Downgrade"
	case comparison == 0:
		action = "Reinstall"
	}
	prompt := fmt.Sprintf("%s ankra from v%s to v%s (%s)? [y/N]: ",
		action, currentVersion, targetVersion, executablePath)
	if err := confirmPrompt(cmd.InOrStdin(), out, prompt, skipConfirm); err != nil {
		return err
	}

	binaryURL, checksumURL := releaseDownloadURLs(targetTag, assetName)

	stagingDir, err := os.MkdirTemp("", "ankra-upgrade-")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(stagingDir); removeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to clean up %s: %v\n", stagingDir, removeErr)
		}
	}()

	downloadedBinary := filepath.Join(stagingDir, "ankra")
	_, _ = fmt.Fprintf(out, "Downloading %s ...\n", binaryURL)
	if err := downloadToFile(httpClient, binaryURL, downloadedBinary); err != nil {
		return fmt.Errorf("download binary: %w", err)
	}

	expectedChecksum, err := fetchChecksum(httpClient, checksumURL)
	if err != nil {
		return fmt.Errorf("download checksum: %w", err)
	}
	if expectedChecksum == "" {
		if !allowUnverified {
			return fmt.Errorf(
				"no SHA-256 checksum published for %s; refusing to install an unverified binary "+
					"(pass --allow-unverified to override)", targetTag)
		}
		_, _ = fmt.Fprintln(out, "Warning: no checksum published for this release; skipping verification (--allow-unverified).")
	} else {
		actualChecksum, sumErr := sha256OfFile(downloadedBinary)
		if sumErr != nil {
			return sumErr
		}
		if !strings.EqualFold(actualChecksum, expectedChecksum) {
			return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
		}
		_, _ = fmt.Fprintln(out, "Checksum verified.")
	}

	if err := replaceExecutable(executablePath, downloadedBinary); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "Ankra CLI upgraded to v%s.\n", targetVersion)
	return nil
}

// releaseAssetName maps the Go runtime OS/arch onto the published release
// asset name (for example darwin/arm64 -> ankra-cli-darwin-arm64). It mirrors
// the matrix in install.sh; only the OS/arch pairs that ship a binary are
// accepted.
func releaseAssetName(goos, goarch string) (string, error) {
	switch goos {
	case "darwin", "linux":
	default:
		return "", fmt.Errorf(
			"unsupported operating system %q: `ankra upgrade` supports darwin and linux. Reinstall manually from https://github.com/ankraio/ankra-cli/releases",
			goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf(
			"unsupported architecture %q: `ankra upgrade` supports amd64 and arm64",
			goarch)
	}
	return fmt.Sprintf("ankra-cli-%s-%s", goos, goarch), nil
}

// releaseDownloadURLs returns the binary and checksum URLs for a given release
// tag and asset. The tag is used verbatim so a pinned `--version v0.2.5`
// resolves to the matching GitHub release path.
func releaseDownloadURLs(tag, asset string) (binaryURL, checksumURL string) {
	binaryURL = fmt.Sprintf("%s/%s/%s", githubReleaseDownload, tag, asset)
	checksumURL = binaryURL + ".sha256"
	return binaryURL, checksumURL
}

func latestReleaseTag(httpClient *http.Client) (string, error) {
	request, err := http.NewRequest(http.MethodGet, githubLatestReleaseAPI, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")

	response, err := httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from %s", response.StatusCode, githubLatestReleaseAPI)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode release response: %w", err)
	}
	if strings.TrimSpace(payload.TagName) == "" {
		return "", errors.New("release response did not include a tag_name")
	}
	return payload.TagName, nil
}

// latestReleaseTagIncludingPrereleases returns the newest published release
// tag, including pre-releases (release candidates). GitHub returns the list
// sorted newest first, so the first non-draft entry is the latest version on
// the beta channel.
func latestReleaseTagIncludingPrereleases(httpClient *http.Client) (string, error) {
	request, err := http.NewRequest(http.MethodGet, githubReleasesListAPI, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")

	response, err := httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from %s", response.StatusCode, githubReleasesListAPI)
	}

	var releases []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	if err := json.NewDecoder(response.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("decode releases response: %w", err)
	}

	for _, release := range releases {
		if release.Draft {
			continue
		}
		if tag := strings.TrimSpace(release.TagName); tag != "" {
			return tag, nil
		}
	}
	return "", errors.New("no published releases found")
}

func downloadToFile(httpClient *http.Client, url, destination string) error {
	response, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d for %s", response.StatusCode, url)
	}

	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, response.Body); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// fetchChecksum downloads the .sha256 file for a release asset and returns the
// hex digest. A 404 yields an empty string (older releases predate published
// checksums), matching the lenient behaviour of install.sh.
func fetchChecksum(httpClient *http.Client, url string) (string, error) {
	response, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d for %s", response.StatusCode, url)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, checksumReadLimit))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", errors.New("checksum file is empty")
	}
	return fields[0], nil
}

func sha256OfFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// isHomebrewManagedPath reports whether a resolved executable path lives in a
// Homebrew Cellar, meaning Homebrew owns the file. Self-updating such a binary
// would desynchronise it from the recorded formula version and be undone by
// the next `brew upgrade`, so `ankra upgrade` refuses and defers to brew. The
// path must already have symlinks resolved (currentExecutablePath does this):
// brew installs `<prefix>/bin/ankra` as a symlink into the Cellar, while a
// manually copied binary under `<prefix>/bin` resolves outside it and stays
// self-updatable.
func isHomebrewManagedPath(executablePath string) bool {
	normalized := filepath.ToSlash(executablePath)
	return strings.Contains(normalized, "/Cellar/")
}

// currentExecutablePath resolves the path of the running binary, following any
// symlinks so the upgrade replaces the real file rather than a link.
func currentExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return executable, nil
	}
	return resolved, nil
}

// replaceExecutable atomically swaps the running binary for the freshly
// downloaded one. The replacement is staged in the destination directory so
// the final rename stays on the same filesystem (os.Rename cannot cross
// devices), giving an all-or-nothing swap.
func replaceExecutable(executablePath, newBinaryPath string) error {
	destinationDir := filepath.Dir(executablePath)

	staged, err := os.CreateTemp(destinationDir, ".ankra-upgrade-*")
	if err != nil {
		if os.IsPermission(err) {
			return permissionDeniedError(destinationDir, executablePath)
		}
		return fmt.Errorf("stage new binary in %s: %w", destinationDir, err)
	}
	stagedPath := staged.Name()
	defer func() { _ = os.Remove(stagedPath) }()

	source, err := os.Open(newBinaryPath)
	if err != nil {
		_ = staged.Close()
		return err
	}
	defer func() { _ = source.Close() }()

	if _, err := io.Copy(staged, source); err != nil {
		_ = staged.Close()
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := staged.Close(); err != nil {
		return err
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		return err
	}

	if err := os.Rename(stagedPath, executablePath); err != nil {
		if os.IsPermission(err) {
			return permissionDeniedError(destinationDir, executablePath)
		}
		return fmt.Errorf("replace %s: %w", executablePath, err)
	}
	return nil
}

func permissionDeniedError(destinationDir, executablePath string) error {
	return fmt.Errorf(
		"permission denied writing to %s.\n"+
			"The Ankra CLI at %s is not writable by the current user.\n"+
			"Re-run with elevated privileges (sudo ankra upgrade) or reinstall with:\n"+
			"  bash <(curl -sL %s)",
		destinationDir, executablePath, installScriptURL)
}

// normalizeVersion strips a leading v/V and surrounding whitespace so build
// versions ("0.2.4") and release tags ("v0.2.4") compare cleanly.
func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	value = strings.TrimPrefix(value, "V")
	return value
}

// ensureTagPrefix normalises a user-supplied release version into the tag form
// used by GitHub releases (a leading "v"), so `--version 0.2.5` and
// `--version v0.2.5` both resolve to the same release.
func ensureTagPrefix(value string) string {
	normalized := normalizeVersion(value)
	if normalized == "" {
		return ""
	}
	return "v" + normalized
}

// compareVersions compares two versions using semantic-versioning precedence,
// returning -1, 0 or 1 when left is older, equal or newer than right. The
// numeric core (major.minor.patch) is compared first; when the cores are
// equal, a release with no pre-release outranks one with a pre-release (so
// 0.3.0 is newer than 0.3.0-rc.1), and pre-release identifiers are compared
// left to right.
func compareVersions(left, right string) int {
	leftCore, leftPrerelease := splitPrerelease(left)
	rightCore, rightPrerelease := splitPrerelease(right)

	if coreComparison := compareCoreVersions(leftCore, rightCore); coreComparison != 0 {
		return coreComparison
	}
	return comparePrerelease(leftPrerelease, rightPrerelease)
}

func splitPrerelease(value string) (core string, prerelease string) {
	if dash := strings.IndexByte(value, '-'); dash >= 0 {
		return value[:dash], value[dash+1:]
	}
	return value, ""
}

func compareCoreVersions(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")

	segments := len(leftParts)
	if len(rightParts) > segments {
		segments = len(rightParts)
	}
	for index := 0; index < segments; index++ {
		leftSegment := numericSegment(leftParts, index)
		rightSegment := numericSegment(rightParts, index)
		if leftSegment < rightSegment {
			return -1
		}
		if leftSegment > rightSegment {
			return 1
		}
	}
	return 0
}

func numericSegment(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	number, err := strconv.Atoi(parts[index])
	if err != nil {
		return 0
	}
	return number
}

// comparePrerelease applies semver pre-release precedence. An empty
// pre-release (a stable release) outranks any pre-release. Otherwise the
// dot-separated identifiers are compared left to right: numeric identifiers
// compare numerically and rank below alphanumeric ones, and a longer set of
// identifiers wins when all shared identifiers are equal.
func comparePrerelease(left, right string) int {
	if left == right {
		return 0
	}
	if left == "" {
		return 1
	}
	if right == "" {
		return -1
	}

	leftIdentifiers := strings.Split(left, ".")
	rightIdentifiers := strings.Split(right, ".")

	shared := len(leftIdentifiers)
	if len(rightIdentifiers) < shared {
		shared = len(rightIdentifiers)
	}
	for index := 0; index < shared; index++ {
		if comparison := comparePrereleaseIdentifier(leftIdentifiers[index], rightIdentifiers[index]); comparison != 0 {
			return comparison
		}
	}
	switch {
	case len(leftIdentifiers) < len(rightIdentifiers):
		return -1
	case len(leftIdentifiers) > len(rightIdentifiers):
		return 1
	default:
		return 0
	}
}

func comparePrereleaseIdentifier(left, right string) int {
	leftNumber, leftErr := strconv.Atoi(left)
	rightNumber, rightErr := strconv.Atoi(right)
	switch {
	case leftErr == nil && rightErr == nil:
		if leftNumber < rightNumber {
			return -1
		}
		if leftNumber > rightNumber {
			return 1
		}
		return 0
	case leftErr == nil:
		return -1
	case rightErr == nil:
		return 1
	default:
		return strings.Compare(left, right)
	}
}
